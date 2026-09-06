package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
)

const templateStateName = "agents-template-state"

var errAgentsSyncLockfile = errors.New("APM lockfile unavailable")

type AgentsSyncAllOptions struct {
	DryRun        bool
	Frozen        bool
	ForceTemplate bool
	Progress      func(string)
	Output        func(stdout, stderr string)
}

type AgentsSyncAllResult struct {
	Output  string
	Stderr  string
	Warning string
	Notices []string
}

func (a *App) APMClient(scope apm.Scope) *apm.Client {
	return apm.New(a.fallbackExecutor(), scope)
}

func (a *App) commandAvailable(name string) bool {
	if checker, ok := a.fallbackExecutor().(interface{ CommandAvailable(string) bool }); ok {
		return checker.CommandAvailable(name)
	}
	return executor.CommandAvailable(name)
}

// APMAvailable memoizes its PATH walk: it runs on every apm invocation, and apm cannot appear mid-process unless we install it.
func (a *App) APMAvailable() bool {
	a.pinnedAPMMu.Lock()
	defer a.pinnedAPMMu.Unlock()
	if !a.apmPresentDone {
		a.apmPresent, a.apmPresentDone = a.commandAvailable("apm"), true
	}
	return a.apmPresent
}

// forgetAPMProbes drops both memos after an install, upgrade, or executor swap changes what apm resolves to.
func (a *App) forgetAPMProbes() {
	a.pinnedAPMMu.Lock()
	defer a.pinnedAPMMu.Unlock()
	a.apmPresentDone, a.pinnedAPMDone, a.pinnedAPMErr = false, false, nil
}

func errAPMNotInstalled() error {
	return &APMRepairError{Kind: APMRepairMissing, Required: apmVersionPin,
		Err: fmt.Errorf("%w: %s", apm.ErrNotInstalled, apm.InstallHint)}
}

// RunAPM delegates lifecycle serialization and mutation safety to APM.
func (a *App) RunAPM(ctx context.Context, args ...string) (apm.Result, error) {
	if !a.APMAvailable() {
		return apm.Result{}, errAPMNotInstalled()
	}
	if err := a.requirePinnedAPM(ctx); err != nil {
		return apm.Result{}, err
	}
	result, err := a.APMClient(apm.Global).Run(ctx, args...)
	if apmMutatesHooks(args) {
		if home, homeErr := os.UserHomeDir(); homeErr == nil {
			if _, pathErr := portableHookPaths(home); pathErr != nil && err == nil {
				err = fmt.Errorf("anchor hook paths to $HOME: %w", pathErr)
			}
		}
	}
	return result, err
}

func (a *App) AgentsOutdated(ctx context.Context) (apm.OutdatedResult, error) {
	if !a.APMAvailable() {
		return apm.OutdatedResult{}, errAPMNotInstalled()
	}
	if err := a.requirePinnedAPM(ctx); err != nil {
		return apm.OutdatedResult{}, err
	}
	readiness, err := a.AgentsReadiness(ctx)
	if err != nil {
		return apm.OutdatedResult{}, err
	}
	if readiness.State != AgentsReadinessReady {
		return apm.OutdatedResult{}, nil
	}
	_, missing, err := AgentsMissingLockfile()
	if err != nil {
		return apm.OutdatedResult{}, err
	}
	if missing {
		return apm.OutdatedResult{}, nil
	}
	result, err := a.APMClient(apm.Global).Outdated(ctx)
	if err != nil {
		return result, err
	}
	// The cache only saves the next start a round trip, so failing to write it must not fail this query.
	_ = a.CacheAgentsOutdated(ctx, result)
	return result, nil
}

// AgentsMissingLockfile reports the global APM workspace and whether it holds no lockfile.
func AgentsMissingLockfile() (string, bool, error) {
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return "", false, err
	}
	switch _, statErr := os.Stat(filepath.Join(dir, "apm.lock.yaml")); {
	case statErr == nil:
		return dir, false, nil
	case os.IsNotExist(statErr):
		return dir, true, nil
	default:
		return dir, false, fmt.Errorf("inspect APM lockfile: %w", statErr)
	}
}

type AgentsOutdatedResult = apm.OutdatedResult

// AgentsTemplatePath resolves the host template APM installs never write to.
func AgentsTemplatePath() (string, error) {
	base, err := config.DefaultConfigDir()
	if err != nil {
		return "", err
	}
	path := filepath.Join(base, "apm.yml")
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		legacy := filepath.Join(home, "Library", "Application Support", "omni", "apm.yml")
		if err := migrateLegacyDarwinAgentsTemplate(path, legacy); err != nil {
			return "", err
		}
	}
	return path, nil
}

func migrateLegacyDarwinAgentsTemplate(canonical, legacy string) error {
	// A symlinked canonical template is dotfiles-managed: adopting the legacy copy would write through the link.
	if info, err := os.Lstat(canonical); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	old, oldExists, err := readRegularTemplate(legacy)
	if err != nil || !oldExists {
		return err
	}
	current, currentExists, err := readRegularTemplate(canonical)
	if err != nil {
		return err
	}
	switch {
	case !currentExists:
		_, err = writeAgentsMigrationTemplate(canonical, old)
		return err
	case bytes.Equal(current, old), !emptyMigrationTemplate(current):
		return nil
	case emptyMigrationTemplate(current) && !emptyMigrationTemplate(old):
		_, err = writeAgentsMigrationTemplate(canonical, old)
		return err
	default:
		return fmt.Errorf("canonical and legacy Darwin APM templates differ; review %s and %s", canonical, legacy)
	}
}

func readRegularTemplate(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, true, fmt.Errorf("APM template %s is not a regular file", path)
	}
	raw, err := os.ReadFile(path)
	return raw, true, err
}

func emptyMigrationTemplate(raw []byte) bool {
	if !bytes.HasPrefix(raw, []byte(agentsMigrationMarker+"\n")) {
		return false
	}
	var manifest apmManifest
	return yaml.Unmarshal(raw, &manifest) == nil && len(manifest.Dependencies.APM) == 0 && len(manifest.Dependencies.MCP) == 0 && len(manifest.Dependencies.LSP) == 0
}

func manifestHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func liveManifestHash(workspaceDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(workspaceDir, "apm.yml"))
	if err != nil {
		return "", err
	}
	return manifestHash(data), nil
}

func snapshotLiveManifest(workspaceDir, stateDir string) error {
	hash, err := liveManifestHash(workspaceDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(stateDir, templateStateName), []byte(hash+"\n"), 0o644)
}

// Reports whether the live manifest ends up matching the template, so the caller can snapshot even when
// nothing was written. Live edits omni never snapshotted win over the template unless force is set.
func materializeAgentsTemplate(workspaceDir, stateDir string, force bool) (bool, string, error) {
	tmplPath, err := AgentsTemplatePath()
	if err != nil {
		return false, "", err
	}
	tmpl, err := os.ReadFile(tmplPath)
	if os.IsNotExist(err) {
		return false, "", nil
	}
	if err != nil {
		return false, "", fmt.Errorf("read agents template: %w", err)
	}
	return materializeAgentsTemplateBytes(workspaceDir, stateDir, force, tmpl)
}

func materializeAgentsTemplateCandidate(workspaceDir, stateDir string, force bool, candidatePath string, candidate []byte) (bool, string, error) {
	tmplPath, err := AgentsTemplatePath()
	if err != nil {
		return false, "", err
	}
	if candidatePath != tmplPath || candidate == nil {
		if _, err := os.ReadFile(tmplPath); err == nil || !os.IsNotExist(err) {
			return false, "", fmt.Errorf("agents template changed during ownership preflight")
		}
		return false, "", nil
	}
	current, err := os.ReadFile(tmplPath)
	if err != nil || manifestHash(current) != manifestHash(candidate) {
		return false, "", fmt.Errorf("agents template changed during ownership preflight")
	}
	return materializeAgentsTemplateBytes(workspaceDir, stateDir, force, candidate)
}

func materializeAgentsTemplateBytes(workspaceDir, stateDir string, force bool, tmpl []byte) (bool, string, error) {
	livePath := filepath.Join(workspaceDir, "apm.yml")
	liveHash, liveErr := liveManifestHash(workspaceDir)
	if liveErr != nil && !os.IsNotExist(liveErr) {
		return false, "", fmt.Errorf("inspect live manifest: %w", liveErr)
	}
	if liveErr == nil && liveHash == manifestHash(tmpl) {
		return true, "", nil
	}
	if liveErr == nil && !force {
		state, stateErr := os.ReadFile(filepath.Join(stateDir, templateStateName))
		if os.IsNotExist(stateErr) {
			return false, "first sync with a template: verify it matches the live manifest, then re-run with --force-template to adopt it", nil
		}
		if stateErr != nil {
			return false, "", stateErr
		}
		if strings.TrimSpace(string(state)) != liveHash {
			return false, fmt.Sprintf("live %s diverged from last sync (direct apm edits?); template not applied — re-run with --force-template to overwrite", livePath), nil
		}
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		return false, "", err
	}
	if err := os.WriteFile(livePath, tmpl, 0o644); err != nil {
		return false, "", fmt.Errorf("materialize agents template: %w", err)
	}
	return true, "", nil
}

// AgentsSyncAll delegates the complete lifecycle to one APM install.
func (a *App) AgentsSyncAll(ctx context.Context, opts AgentsSyncAllOptions) (AgentsSyncAllResult, error) {
	exists, err := agentsSyncStateExists()
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	if !exists {
		return AgentsSyncAllResult{}, nil
	}
	templatePath, err := AgentsTemplatePath()
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	lock, err := config.AcquireWriteLock(templatePath)
	if err != nil {
		return AgentsSyncAllResult{}, fmt.Errorf("lock agents sync: %w", err)
	}
	defer func() { _ = lock.Close() }()

	var res AgentsSyncAllResult
	err = apm.WithGlobalWorkspaceLock(ctx, func(ctx context.Context) error {
		var runErr error
		res, runErr = a.agentsSyncAllLocked(ctx, opts)
		return runErr
	})
	return res, err
}

func agentsSyncStateExists() (bool, error) {
	if dir, err := apm.GlobalWorkspaceDir(); err == nil {
		if _, err := os.Lstat(filepath.Join(dir, "apm.yml")); err == nil || !os.IsNotExist(err) {
			return true, nil
		}
	}
	path, err := AgentsTemplatePath()
	if err != nil {
		return false, err
	}
	if _, err := os.Lstat(path); err == nil || !os.IsNotExist(err) {
		return true, nil
	}
	return false, nil
}

func (a *App) agentsSyncAllLocked(ctx context.Context, opts AgentsSyncAllOptions) (AgentsSyncAllResult, error) {
	dir, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	candidatePath, candidate, err := agentsSyncCandidate(dir)
	if err != nil {
		return AgentsSyncAllResult{}, err
	}
	manifest, verify, notices, err := checkAgentsOwnershipPreflight(dir, a.StateDir, candidatePath, candidate)
	if err != nil {
		if errors.Is(err, errAgentsSyncLockfile) {
			if guardErr := checkAgentsLSPHazardsManifest(opts, manifest); guardErr != nil {
				return AgentsSyncAllResult{}, guardErr
			}
		}
		return AgentsSyncAllResult{}, err
	}
	if err := verify(); err != nil {
		return AgentsSyncAllResult{}, err
	}

	var synced bool
	res := AgentsSyncAllResult{Notices: notices}
	// A dry run must not rewrite the live manifest, so the template stays unapplied.
	if !opts.DryRun {
		synced, res.Warning, err = materializeAgentsTemplateCandidate(dir, a.StateDir, opts.ForceTemplate, candidatePath, candidate)
		if err != nil {
			return AgentsSyncAllResult{}, err
		}
	}
	// A refused template application and a dry run both execute against the existing live manifest.
	// Validate it too when it differs from the candidate selected above.
	if !synced {
		livePath := filepath.Join(dir, "apm.yml")
		live, readErr := os.ReadFile(livePath)
		if readErr == nil && (livePath != candidatePath || manifestHash(live) != manifestHash(candidate)) {
			manifest, verify, notices, err = checkAgentsOwnershipPreflight(dir, a.StateDir, livePath, live)
			if err != nil {
				if errors.Is(err, errAgentsSyncLockfile) {
					if guardErr := checkAgentsLSPHazardsManifest(opts, manifest); guardErr != nil {
						return res, guardErr
					}
				}
				return res, err
			}
			if err := verify(); err != nil {
				return res, err
			}
			res.Notices = notices
		} else if readErr != nil && !os.IsNotExist(readErr) {
			return res, fmt.Errorf("read APM manifest: %w", readErr)
		}
	}
	// Guards run before ensureMarketplaces because that already invokes apm; a hazard must fail with apm untouched.
	if err := checkAgentsLSPHazardsManifest(opts, manifest); err != nil {
		return res, err
	}
	// Marketplace-sourced dependencies cannot install before their marketplace is registered.
	if err := a.ensureMarketplaces(ctx, dir, opts); err != nil {
		return res, err
	}
	installable, err := agentsManifestInstallable(dir)
	if err != nil || !installable {
		return res, err
	}
	if opts.Progress != nil {
		opts.Progress("Installing agent packages with APM...")
	}
	// Plugins that declare their own MCP server sit at depth 2 behind the shared package; apm drops those unless trusted.
	args := []string{"install", "-g", "--trust-transitive-mcp"}
	if opts.Frozen {
		args = append(args, "--frozen")
	}
	if opts.DryRun {
		args = append(args, "--dry-run")
	}
	result, err := a.RunAPM(ctx, args...)
	body, shadowed := collapseShadowWarnings(result.Stdout)
	if shadowed > 0 {
		note := shadowedFilesNote(shadowed)
		res.Notices = append(res.Notices, note)
		body += note + "\n"
	}
	res.Output, res.Stderr = body, result.Stderr
	if opts.Output != nil {
		opts.Output(res.Output, res.Stderr)
	}
	if err != nil || !synced {
		return res, err
	}
	// Snapshot the manifest as apm normalized it, so the next sync sees no divergence.
	return res, snapshotLiveManifest(dir, a.StateDir)
}

func agentsSyncCandidate(dir string) (string, []byte, error) {
	template, err := AgentsTemplatePath()
	if err != nil {
		return "", nil, err
	}
	for _, path := range []string{template, filepath.Join(dir, "apm.yml")} {
		raw, err := os.ReadFile(path)
		if err == nil {
			return path, raw, nil
		}
		if !os.IsNotExist(err) {
			return "", nil, fmt.Errorf("read APM manifest: %w", err)
		}
	}
	return template, nil, nil
}

type agentsSyncIdentity struct {
	path string
	hash string
}

func checkAgentsOwnershipPreflight(dir, stateDir, manifestPath string, raw []byte) (apmManifest, func() error, []string, error) {
	var manifest apmManifest
	wrapperEvidence, wrapperCount, err := inspectAgentsMigrationOwnership(raw, stateDir)
	if err != nil {
		return manifest, nil, nil, err
	}
	// Never echo YAML parse details: manifests may contain literal secrets.
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return manifest, nil, nil, fmt.Errorf("invalid APM manifest")
	}
	lockPath := filepath.Join(dir, "apm.lock.yaml")
	lockIdentity, lockRaw, err := readAgentsFileIdentity(lockPath)
	if err != nil {
		return manifest, nil, nil, fmt.Errorf("%w: cannot read %s", errAgentsSyncLockfile, lockPath)
	}
	var lock apmLockfile
	if err := yaml.Unmarshal(lockRaw, &lock); err != nil {
		return manifest, nil, nil, fmt.Errorf("%w: %v", errAgentsSyncLockfile, agentsInvalidYAMLError("APM lockfile", lockPath, err))
	}
	packages := joinAPMPackages(manifest, lock)
	packages = slices.DeleteFunc(packages, func(pkg AgentsPackageRow) bool { return pkg.Status == AgentsPackageOrphaned })
	evidence := readAPMModuleManifests(dir, packages)
	identities := evidence.Manifests
	evidence.Children = append(evidence.Children, wrapperEvidence.Children...)
	evidence.Children = compactAgentsOwnedChildren(evidence.Children)
	if len(manifest.Dependencies.APM) > len(identities)+wrapperCount && len(evidence.Unavailable) == 0 {
		evidence.Unavailable = []string{"one or more packages"}
	}

	var notices []string
	standalone := len(manifest.Dependencies.MCP) + len(manifest.Dependencies.LSP)
	if standalone > 0 && len(evidence.Unavailable) > 0 {
		// Without a lockfile nothing is installed yet, so apm's own install decides instead of this guard.
		if !lockIdentity.absent {
			return manifest, nil, nil, fmt.Errorf("cannot verify package-owned MCP/LSP declarations for %s while standalone MCP/LSP declarations exist; install or reconcile packages first", strings.Join(evidence.Unavailable, ", "))
		}
		notices = append(notices, "packages not installed yet ("+strings.Join(evidence.Unavailable, ", ")+"); APM install will resolve package-owned MCP/LSP declarations")
	}
	if err := checkAgentsOwnedChildOwners(evidence.Children); err != nil {
		return manifest, nil, nil, err
	}
	collisions := classifyAgentsOwnedChildren(manifest, evidence.Children)
	if len(collisions) > 0 {
		collision := collisions[0]
		kind := strings.ToUpper(string(collision.Child.Kind))
		if collision.Exact {
			return manifest, nil, nil, fmt.Errorf("%s %q from package %q duplicates a standalone declaration; run 'omni doctor --fix'", kind, collision.Child.Name, collision.Child.Owner)
		}
		fields := agentsOwnedCollisionFields(collision, manifest)
		if len(fields) == 0 {
			fields = []string{"configuration"}
		}
		return manifest, nil, nil, fmt.Errorf("%s %q standalone declaration conflicts with package %q (%s differ)", kind, collision.Child.Name, collision.Child.Owner, strings.Join(fields, ", "))
	}

	manifestIdentity := agentsSyncIdentity{path: manifestPath, hash: manifestHash(raw)}
	verify := func() error {
		current, err := os.ReadFile(manifestIdentity.path)
		if raw == nil && os.IsNotExist(err) {
			return nil
		}
		if err != nil || manifestHash(current) != manifestIdentity.hash {
			return fmt.Errorf("APM manifest changed during ownership preflight")
		}
		if slices.ContainsFunc(identities, func(identity agentsModuleManifestIdentity) bool { return identity.Lock != nil }) {
			err = verifyAgentsPackageOwnershipEvidence(raw, identities, []agentsFileContentIdentity{lockIdentity})
		} else {
			err = verifyAgentsModuleManifestIdentities(identities)
		}
		if err != nil {
			return fmt.Errorf("APM package evidence changed during ownership preflight")
		}
		_, _, err = inspectAgentsMigrationOwnership(raw, stateDir)
		return err
	}
	return manifest, verify, notices, nil
}

func compactAgentsOwnedChildren(children []agentsOwnedChild) []agentsOwnedChild {
	seen := make(map[string]bool, len(children))
	out := children[:0]
	for _, child := range children {
		key := agentsChildKey(child.Kind, child.Name) + "\x00" + strings.ToLower(child.Owner) + "\x00" + child.Fingerprint
		if !seen[key] {
			seen[key] = true
			out = append(out, child)
		}
	}
	return out
}

func checkAgentsOwnedChildOwners(children []agentsOwnedChild) error {
	owners := make(map[string]map[string]string)
	definitions := make(map[string]map[string]string)
	for _, child := range children {
		key := agentsChildKey(child.Kind, child.Name)
		ownerKey := strings.ToLower(child.Owner)
		if definitions[key] == nil {
			definitions[key] = make(map[string]string)
		}
		fingerprint := agentsOwnedChildSemanticFingerprint(child)
		if previous := definitions[key][ownerKey]; previous != "" && previous != fingerprint {
			return fmt.Errorf("%s %q has conflicting definitions in package %q", strings.ToUpper(string(child.Kind)), child.Name, child.Owner)
		}
		definitions[key][ownerKey] = fingerprint
		if owners[key] == nil {
			owners[key] = make(map[string]string)
		}
		owners[key][ownerKey] = child.Owner
	}
	keys := make([]string, 0, len(owners))
	for key := range owners {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		byOwner := owners[key]
		if len(byOwner) < 2 {
			continue
		}
		names := make([]string, 0, len(byOwner))
		for _, owner := range byOwner {
			names = append(names, owner)
		}
		sort.Strings(names)
		kind, name, _ := strings.Cut(key, "\x00")
		return fmt.Errorf("%s %q has multiple package owners: %s", strings.ToUpper(kind), name, strings.Join(names, ", "))
	}
	return nil
}

func agentsOwnedCollisionFields(collision agentsChildCollision, manifest apmManifest) []string {
	if collision.Child.Kind == agentsChildMCP && collision.Child.MCP != nil {
		for _, dep := range manifest.Dependencies.MCP {
			if strings.EqualFold(dep.Name, collision.Child.Name) {
				return agentsMCPDiffFields(*collision.Child.MCP, dep, collision.Child.OwnerRoot)
			}
		}
	}
	if collision.Child.Kind == agentsChildLSP && collision.Child.LSP != nil {
		for _, dep := range manifest.Dependencies.LSP {
			if strings.EqualFold(dep.Name, collision.Child.Name) {
				return agentsLSPDiffFields(*collision.Child.LSP, dep, collision.Child.OwnerRoot)
			}
		}
	}
	return []string{"configuration"}
}

func inspectAgentsMigrationOwnership(raw []byte, stateDir string) (agentsOwnershipEvidence, int, error) {
	var evidence agentsOwnershipEvidence
	lines := strings.Split(string(raw), "\n")
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != agentsMigrationMarker {
		return evidence, 0, nil
	}
	var manifest apmManifest
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return evidence, 0, fmt.Errorf("invalid migration-managed manifest")
	}
	owners, children := map[string]bool{}, map[string]bool{}
	standalone := map[string]bool{}
	for _, dep := range manifest.Dependencies.MCP {
		key := agentsChildKey(agentsChildMCP, dep.Name)
		if dep.Name != "" && standalone[key] {
			return evidence, 0, fmt.Errorf("duplicate migration child mcp %q", dep.Name)
		}
		standalone[key] = dep.Name != ""
	}
	for _, dep := range manifest.Dependencies.LSP {
		key := agentsChildKey(agentsChildLSP, dep.Name)
		if dep.Name != "" && standalone[key] {
			return evidence, 0, fmt.Errorf("duplicate migration child lsp %q", dep.Name)
		}
		standalone[key] = dep.Name != ""
	}
	claimChild := func(child agentsOwnedChild) error {
		if child.Name == "" {
			return nil
		}
		key := agentsChildKey(child.Kind, child.Name)
		if children[key] {
			return fmt.Errorf("duplicate migration child %s %q", child.Kind, child.Name)
		}
		children[key] = true
		evidence.Children = append(evidence.Children, child)
		return nil
	}
	wrapperRoot, err := filepath.Abs(filepath.Join(stateDir, "agents-migration", "bundles"))
	if err != nil {
		return evidence, 0, fmt.Errorf("resolve migration wrapper root: %w", err)
	}
	wrapperCount := 0
	for _, dep := range manifest.Dependencies.APM {
		identity, err := migrationOwnerIdentity(dep)
		if err != nil {
			return evidence, wrapperCount, err
		}
		if owners[identity] {
			return evidence, wrapperCount, fmt.Errorf("duplicate migration owner %q", dep.Path)
		}
		owners[identity] = true
		path, err := filepath.Abs(dep.Path)
		if dep.Path == "" || err != nil {
			continue
		}
		lexicalWrapper := filepath.Dir(path) == wrapperRoot
		resolvedPath, pathErr := filepath.EvalSymlinks(path)
		resolvedRoot, rootErr := filepath.EvalSymlinks(wrapperRoot)
		resolvedWrapper := pathErr == nil && rootErr == nil && filepath.Dir(resolvedPath) == resolvedRoot && validAgentBundleHash(filepath.Base(path))
		if !lexicalWrapper && !resolvedWrapper {
			continue
		}
		if resolvedWrapper && dep.Path != filepath.Join(wrapperRoot, filepath.Base(path)) {
			return evidence, wrapperCount, fmt.Errorf("noncanonical migration wrapper path")
		}
		wrapper, err := inspectMigrationWrapper(path, stateDir)
		if err != nil {
			return evidence, wrapperCount, err
		}
		wrapperCount++
		if owners["name\x00"+strings.ToLower(wrapper.Name)] {
			return evidence, wrapperCount, fmt.Errorf("duplicate migration owner %q", wrapper.Name)
		}
		owners["name\x00"+strings.ToLower(wrapper.Name)] = true
		for _, child := range wrapper.Dependencies.MCP {
			child := child
			if err := claimChild(agentsOwnedChild{
				Kind: agentsChildMCP, Name: child.Name, Owner: wrapper.Name, OwnerRoot: path,
				Fingerprint: agentsMCPFingerprint(child, path), MCP: &child,
			}); err != nil {
				return evidence, wrapperCount, err
			}
		}
		for _, child := range wrapper.Dependencies.LSP {
			child := child
			if err := claimChild(agentsOwnedChild{
				Kind: agentsChildLSP, Name: child.Name, Owner: wrapper.Name, OwnerRoot: path,
				Fingerprint: agentsLSPFingerprint(child, path), LSP: &child,
			}); err != nil {
				return evidence, wrapperCount, err
			}
		}
	}
	return evidence, wrapperCount, nil
}

func migrationOwnerIdentity(dep apmPackageDep) (string, error) {
	if dep.Path != "" {
		path, err := filepath.Abs(dep.Path)
		if err != nil {
			return "", fmt.Errorf("resolve migration owner path: %w", err)
		}
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		return "path\x00" + filepath.Clean(path), nil
	}
	if dep.Git != "" {
		return "git\x00" + apmNormalizeRepo(dep.Git) + "\x00" + strings.TrimSpace(dep.Ref), nil
	}
	return "marketplace\x00" + strings.ToLower(strings.TrimSpace(dep.Marketplace)) + "\x00" +
		strings.ToLower(strings.TrimSpace(dep.Name)) + "\x00" + strings.TrimSpace(dep.Ref), nil
}

func inspectMigrationWrapper(path, stateDir string) (apmManifest, error) {
	var manifest apmManifest
	hash := filepath.Base(path)
	if !validAgentBundleHash(hash) {
		return manifest, fmt.Errorf("invalid migration wrapper %q", path)
	}
	state, err := filepath.Abs(stateDir)
	if err != nil || !pathWithin(path, state) {
		return manifest, fmt.Errorf("migration wrapper escapes state directory")
	}
	rel, _ := filepath.Rel(state, path)
	current := state
	for _, part := range append([]string{"."}, strings.Split(rel, string(filepath.Separator))...) {
		if part != "." {
			current = filepath.Join(current, part)
		}
		info, statErr := os.Lstat(current)
		switch {
		case os.IsNotExist(statErr):
			return manifest, fmt.Errorf("missing migration wrapper %s", hash)
		case statErr != nil:
			return manifest, fmt.Errorf("corrupt migration wrapper %s", hash)
		case info.Mode()&os.ModeSymlink != 0:
			return manifest, fmt.Errorf("symlinked migration wrapper path")
		case current != path && !info.IsDir():
			return manifest, fmt.Errorf("corrupt migration wrapper %s", hash)
		}
	}
	resolvedState, stateErr := filepath.EvalSymlinks(state)
	resolvedPath, pathErr := filepath.EvalSymlinks(path)
	if stateErr != nil || pathErr != nil || !pathWithin(resolvedPath, resolvedState) ||
		filepath.Dir(resolvedPath) != filepath.Join(resolvedState, "agents-migration", "bundles") {
		return manifest, fmt.Errorf("migration wrapper escapes state directory")
	}
	if info, err := os.Lstat(path); err != nil || !info.IsDir() {
		return manifest, fmt.Errorf("corrupt migration wrapper %s", hash)
	}
	raw, err := os.ReadFile(filepath.Join(path, "apm.yml"))
	if err != nil {
		return manifest, fmt.Errorf("corrupt migration wrapper %s", hash)
	}
	if err := yaml.Unmarshal(raw, &manifest); err != nil {
		return manifest, fmt.Errorf("corrupt migration wrapper %s", hash)
	}
	type identity struct {
		path, hash string
		mode       os.FileMode
	}
	var files []identity
	err = filepath.WalkDir(path, func(name string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		fileInfo, err := entry.Info()
		if err != nil || !fileInfo.Mode().IsRegular() || fileInfo.Mode().Perm() != normalizedBundleMode(fileInfo.Mode()) {
			return fmt.Errorf("invalid wrapper file")
		}
		rel, _ := filepath.Rel(path, name)
		if filepath.ToSlash(rel) == "apm.yml" {
			if fileInfo.Mode().Perm() != 0o600 {
				return fmt.Errorf("invalid wrapper manifest mode")
			}
			return nil
		}
		fileHash, err := hashFile(name)
		if err != nil {
			return err
		}
		files = append(files, identity{filepath.ToSlash(rel), fileHash, fileInfo.Mode()})
		return nil
	})
	if err != nil {
		return manifest, fmt.Errorf("corrupt migration wrapper %s", hash)
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	digest := sha256.New()
	_, _ = digest.Write(raw)
	for _, file := range files {
		_, _ = fmt.Fprintf(digest, "%s\x00%s\x00%04o\x00", file.path, file.hash, normalizedBundleMode(file.mode))
	}
	if hex.EncodeToString(digest.Sum(nil)) != hash {
		return manifest, fmt.Errorf("corrupt migration wrapper %s", hash)
	}
	return manifest, nil
}

// A workspace with no manifest is simply nothing to install; only a broken workspace is an error.
func agentsManifestInstallable(dir string) (bool, error) {
	_, err := os.Stat(filepath.Join(dir, "apm.yml"))
	if err == nil {
		return true, nil
	}
	if !os.IsNotExist(err) {
		return false, fmt.Errorf("inspect global APM manifest: %w", err)
	}
	workspace, err := os.Stat(dir)
	switch {
	case err != nil && !os.IsNotExist(err):
		return false, fmt.Errorf("inspect global APM workspace: %w", err)
	case err == nil && !workspace.IsDir():
		return false, fmt.Errorf("inspect global APM workspace: %s is not a directory", dir)
	}
	return false, nil
}

// Both hazards are apm 0.29.0 defects: a non-intersecting target aborts the whole install, and --frozen ignores lsp entries.
func checkAgentsLSPHazards(opts AgentsSyncAllOptions) error {
	manifest, err := readAPMManifest()
	// An unparseable manifest is apm's own transactional validation to report, not this guard's.
	if err != nil {
		return nil
	}
	return checkAgentsLSPHazardsManifest(opts, manifest)
}

func checkAgentsLSPHazardsManifest(opts AgentsSyncAllOptions, manifest apmManifest) error {
	if len(manifest.Dependencies.LSP) == 0 {
		return nil
	}
	// The target hazard is decided by the manifest alone, so no lockfile problem may disable it.
	if err := checkAgentsLSPTargets(manifest); err != nil {
		return err
	}
	if !opts.Frozen {
		return nil
	}
	lock, err := readAPMLockfile()
	// Frozen means the manifest and lock provably agree; an unreadable lock proves nothing.
	if err != nil {
		return fmt.Errorf("frozen sync: cannot verify lockfile: %w", err)
	}
	locked := make(map[string]bool, len(lock.LSPServers))
	for _, name := range lock.LSPServers {
		locked[name] = true
	}
	for _, dep := range manifest.Dependencies.LSP {
		if !locked[dep.Name] {
			return fmt.Errorf("frozen sync: lsp server %q is not in the lockfile (apm --frozen does not check lsp entries)", dep.Name)
		}
	}
	return nil
}

func checkAgentsLSPTargets(manifest apmManifest) error {
	if len(manifest.Targets) == 0 {
		return nil
	}
	for _, target := range manifest.Targets {
		if slices.Contains(agentsLSPTargets, target) {
			return nil
		}
	}
	return fmt.Errorf("lsp dependencies are declared but no target supports them: declare target claude or copilot, or remove the lsp entries")
}
