package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/apm"
	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/dots"
	"github.com/lkshrk/omni/internal/securefile"
)

type AgentsOnboardOptions struct {
	PlanJSON, ApplyPlan string
	Sources             []string
	ProjectRoot         string
}
type AgentsOnboardResolutions struct {
	ApprovedTargets                 map[string][]string
	EnvBindings                     map[string]map[string]string
	Excluded, MoveToAPM, KeepInDots map[string]bool
	ApprovedExecutables             map[string][]string // compatibility; APM validates executables during install
}
type OnboardEnvelope struct {
	OK                          bool         `json:"ok"`
	Kind                        string       `json:"kind"`
	Plan                        *OnboardPlan `json:"plan,omitempty"`
	OperationID                 string       `json:"operation_id,omitempty"`
	State                       string       `json:"state,omitempty"`
	Applied, Retained, Blockers []string
}
type AgentsOnboardResult struct {
	Envelope       OnboardEnvelope `json:"onboarding"`
	CandidateSetID string          `json:"candidate_set_id"`
	PreimageSet    string          `json:"preimage_set"`
}
type AgentsOnboardStatusResult struct {
	OperationID string          `json:"operation_id"`
	OmniPhase   string          `json:"omni_phase"`
	APM         OnboardEnvelope `json:"onboarding"`
}
type OnboardingRecoveryError struct {
	OperationID, OmniPhase, APMState string
	Cause                            error
}

var onboardingPinnedAPMCheck = func(ctx context.Context, a *App) error { return a.requirePinnedAPM(ctx) }

func (e *OnboardingRecoveryError) Error() string {
	return fmt.Sprintf("onboarding operation %s is incomplete (phase=%s); run 'omni agents onboard resume --operation %s'", e.OperationID, e.OmniPhase, e.OperationID)
}
func (e *OnboardingRecoveryError) Unwrap() error { return e.Cause }

func (a *App) detectOnboardingRecovery(context.Context) error {
	path := filepath.Join(a.StateDir, "onboarding")
	entries, err := os.ReadDir(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	root, err := securefile.OpenRoot(path)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		op, err := root.OpenChild(entry.Name())
		if err != nil {
			return err
		}
		journal, err := readOnboardJournal(op)
		if err != nil {
			return err
		}
		if journal.Phase != "complete" && journal.Phase != "rolled-back" {
			return &OnboardingRecoveryError{OperationID: journal.OperationID, OmniPhase: journal.Phase}
		}
	}
	return nil
}

func (a *App) onboardingClient(ctx context.Context) (*apm.Client, error) {
	if err := onboardingPinnedAPMCheck(ctx, a); err != nil {
		return nil, err
	}
	return a.APMClient(apm.Global), nil
}

func (a *App) AgentsOnboardPlan(ctx context.Context, opts AgentsOnboardOptions) (AgentsOnboardResult, error) {
	if strings.TrimSpace(opts.ProjectRoot) != "" || slices.Contains(opts.Sources, "native") {
		return AgentsOnboardResult{}, errors.New("project/native reverse import is unsupported; Omni migrates its legacy config and managed dots")
	}
	plan, err := a.buildAgentsOnboardPlan(ctx)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	if opts.PlanJSON != "" {
		path, err := filepath.Abs(opts.PlanJSON)
		if err != nil {
			return AgentsOnboardResult{}, err
		}
		data, err := json.MarshalIndent(plan, "", "  ")
		if err != nil {
			return AgentsOnboardResult{}, err
		}
		if err := atomicWriteOnboardFile(path, append(data, '\n'), 0o600); err != nil {
			return AgentsOnboardResult{}, err
		}
	}
	env := OnboardEnvelope{OK: len(plan.Blockers) == 0, Kind: "onboard-plan", Plan: &plan, State: "planned", Blockers: plan.Blockers}
	return AgentsOnboardResult{Envelope: env, CandidateSetID: plan.CandidateSetID, PreimageSet: plan.PreimageSet}, nil
}

func (a *App) buildAgentsOnboardPlan(ctx context.Context) (OnboardPlan, error) {
	inv, err := ExtractLegacyCandidates(a.ConfigPath)
	if err != nil {
		return OnboardPlan{}, err
	}
	dotCandidates, dotPreimages, err := a.extractDotsCandidates()
	if err != nil {
		return OnboardPlan{}, err
	}
	inv.Envelope.Candidates = append(inv.Envelope.Candidates, dotCandidates...)
	inv.Envelope.SourcePreimages = append(inv.Envelope.SourcePreimages, dotPreimages...)
	client, err := a.onboardingClient(ctx)
	if err != nil {
		return OnboardPlan{}, err
	}
	targetResult, err := client.TargetsJSON(ctx)
	if err != nil {
		return OnboardPlan{}, err
	}
	var rows []struct {
		Target    string `json:"target"`
		DeployDir string `json:"deploy_dir"`
		Meta      bool   `json:"meta_target"`
	}
	if err := json.Unmarshal([]byte(targetResult.Stdout), &rows); err != nil {
		return OnboardPlan{}, fmt.Errorf("decode APM targets: %w", err)
	}
	var targets []string
	var deployDirs []string
	for _, row := range rows {
		if row.Target != "" && !row.Meta {
			targets = append(targets, row.Target)
		}
		if row.DeployDir != "" {
			deployDirs = append(deployDirs, row.DeployDir)
		}
	}
	targets = sortedUnique(targets)
	manifestPath, err := onboardManifestPath()
	if err != nil {
		return OnboardPlan{}, err
	}
	existing, _, _, readErr := captureOnboardOptionalFile(manifestPath)
	if readErr != nil {
		return OnboardPlan{}, readErr
	}
	markerPath, err := onboardCompletionMarkerPath()
	if err != nil {
		return OnboardPlan{}, err
	}
	if !regularOnboardFile(markerPath) && !onboardManifestHasOmniImports(existing) {
		nativeCandidates, nativePreimages, err := extractNativeCandidates(sortedUnique(deployDirs), dotCandidates)
		if err != nil {
			return OnboardPlan{}, err
		}
		inv.Envelope.Candidates = append(inv.Envelope.Candidates, nativeCandidates...)
		inv.Envelope.SourcePreimages = append(inv.Envelope.SourcePreimages, nativePreimages...)
	}
	sort.Slice(inv.Envelope.Candidates, func(i, j int) bool { return inv.Envelope.Candidates[i].ID < inv.Envelope.Candidates[j].ID })
	sort.Slice(inv.Envelope.SourcePreimages, func(i, j int) bool { return inv.Envelope.SourcePreimages[i].ID < inv.Envelope.SourcePreimages[j].ID })
	inv.Envelope.CandidateSetID = canonicalDigest(map[string]any{"preimages": inv.Envelope.SourcePreimages, "candidates": inv.Envelope.Candidates})
	rawPreimages, _ := json.Marshal(inv.Envelope.SourcePreimages)
	inv.PreimageSet = digestBytes(rawPreimages)
	items := make([]OnboardItem, 0, len(inv.Envelope.Candidates))
	for _, c := range inv.Envelope.Candidates {
		item := OnboardItem{ID: c.ID, Kind: c.Kind, Name: c.Name, Source: c.SourceHandle, ProposedTargets: append([]string(nil), c.SourceTargets...), TargetOptions: targets, Payload: c.Payload, ContentFingerprint: c.ContentFingerprint, Dots: c.Dots, Resolution: OnboardResolution{EnvBindings: map[string]string{}, ApprovedTargets: append([]string(nil), c.SourceTargets...)}}
		var payload map[string]any
		_ = json.Unmarshal(c.Payload, &payload)
		if disposition, _ := payload["disposition"].(string); strings.HasPrefix(disposition, "excluded") {
			item.Resolution.Decision = "keep-unmanaged"
		}
		if blocker, _ := payload["blocker"].(string); blocker != "" {
			item.Blockers = append(item.Blockers, blocker)
			if c.Dots != nil && c.Dots.Native {
				item.Resolution.Decision = "keep-unmanaged"
			} else {
				item.Resolution.Decision = "keep-in-dots"
			}
		}
		if c.Kind == "unsupported" {
			item.Blockers = append(item.Blockers, "unsupported")
			item.Resolution.Decision = "keep-unmanaged"
		}
		if c.Dots != nil && c.Dots.Native && item.Resolution.Decision == "" {
			item.Resolution.Decision = "keep-unmanaged"
			item.Blockers = append(item.Blockers, "target-resolution-required")
		}
		if c.Dots != nil && !c.Dots.Native && item.Resolution.Decision == "" {
			item.Resolution.Decision = "keep-in-dots"
		}
		if c.Dots == nil && item.Resolution.Decision == "" {
			item.Resolution.Decision = "migrate"
		}
		if len(c.SourceTargets) == 0 && item.Resolution.Decision == "migrate" {
			item.Blockers = append(item.Blockers, "target-resolution-required")
		}
		for _, target := range c.SourceTargets {
			if !slices.Contains(targets, target) {
				item.Blockers = append(item.Blockers, "unknown-target:"+target)
			}
		}
		if c.SecretBlocked {
			item.Blockers = append(item.Blockers, "secret-mapping-required")
		}
		items = append(items, item)
	}
	proposed, markets, mergeBlockers, err := buildOnboardManifest(existing, items)
	if err != nil {
		return OnboardPlan{}, err
	}
	mergeBlockers = attachOnboardMergeBlockers(items, mergeBlockers)
	plan := OnboardPlan{SchemaVersion: onboardSchemaVersion, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano), CandidateSetID: inv.Envelope.CandidateSetID, PreimageSet: inv.PreimageSet, SourcePreimages: inv.Envelope.SourcePreimages, Items: items, Marketplaces: sortedMarketplaces(markets), ProposedManifest: string(proposed), Blockers: mergeBlockers}
	for _, item := range items {
		if item.Resolution.Decision != "keep-unmanaged" && item.Resolution.Decision != "keep-in-dots" {
			plan.Blockers = append(plan.Blockers, item.Blockers...)
		}
	}
	plan.Blockers = sortedUnique(plan.Blockers)
	if err := bindOnboardPlan(&plan); err != nil {
		return OnboardPlan{}, err
	}
	return plan, nil
}

func attachOnboardMergeBlockers(items []OnboardItem, blockers []string) []string {
	global := make([]string, 0, len(blockers))
	for _, blocker := range blockers {
		id, reason, ok := strings.Cut(blocker, ":")
		if ok {
			for i := range items {
				if items[i].ID == id {
					items[i].Blockers = append(items[i].Blockers, reason)
					reason = ""
					break
				}
			}
			if reason == "" {
				continue
			}
		}
		global = append(global, blocker)
	}
	return global
}

func (a *App) AgentsOnboardApply(ctx context.Context, path string) (AgentsOnboardResult, error) {
	plan, err := readReviewedPlan(path)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	return a.agentsOnboardApplyPlan(ctx, plan)
}
func (a *App) AgentsOnboardApplyReviewed(ctx context.Context, plan OnboardPlan) (AgentsOnboardResult, error) {
	if err := bindOnboardPlan(&plan); err != nil {
		return AgentsOnboardResult{}, err
	}
	return a.agentsOnboardApplyPlan(ctx, plan)
}
func (a *App) AgentsOnboardApplyResolved(ctx context.Context, path string, res AgentsOnboardResolutions) (AgentsOnboardResult, error) {
	plan, err := readReviewedPlan(path)
	if err != nil {
		return AgentsOnboardResult{}, err
	}
	if err := validateApprovedTargetResolutions(plan, res.ApprovedTargets); err != nil {
		return AgentsOnboardResult{}, err
	}
	if err := validateOnboardResolutionKeys(plan, res); err != nil {
		return AgentsOnboardResult{}, err
	}
	for i := range plan.Items {
		item := &plan.Items[i]
		if targets := lookupResolution(res.ApprovedTargets, item.ID, item.Name); len(targets) > 0 {
			item.Resolution.ApprovedTargets = targets
		}
		if bindings := lookupResolution(res.EnvBindings, item.ID, item.Name); len(bindings) > 0 {
			item.Resolution.EnvBindings = bindings
			item.Resolution.Decision = "map-secret"
		}
		if res.Excluded[item.ID] || res.Excluded[item.Name] {
			item.Resolution.Decision = "keep-unmanaged"
		}
		if res.MoveToAPM[item.ID] || res.MoveToAPM[item.Name] {
			item.Resolution.Decision = "move-to-apm"
		}
		if res.KeepInDots[item.ID] || res.KeepInDots[item.Name] {
			item.Resolution.Decision = "keep-in-dots"
		}
	}
	if err := bindOnboardPlan(&plan); err != nil {
		return AgentsOnboardResult{}, err
	}
	return a.agentsOnboardApplyPlan(ctx, plan)
}

func (a *App) agentsOnboardApplyPlan(ctx context.Context, plan OnboardPlan) (result AgentsOnboardResult, retErr error) {
	if err := validateReviewedOnboardPlan(plan); err != nil {
		return result, err
	}
	lock, err := config.AcquireWriteLock(a.ConfigPath)
	if err != nil {
		return result, err
	}
	lockOpen := true
	defer func() {
		if lockOpen {
			retErr = errors.Join(retErr, lock.Close())
		}
	}()
	fresh, err := a.buildAgentsOnboardPlan(ctx)
	if err != nil {
		return result, err
	}
	if err := validateReviewedOnboardFresh(plan, fresh); err != nil {
		return result, err
	}
	manifestPath, err := onboardManifestPath()
	if err != nil {
		return result, err
	}
	existing, manifestExisted, manifestModeValue, readErr := captureOnboardOptionalFile(manifestPath)
	if readErr != nil {
		return result, readErr
	}
	proposed, markets, blockers, err := buildOnboardManifest(existing, plan.Items)
	if err != nil {
		return result, err
	}
	for _, item := range plan.Items {
		if slices.Contains([]string{"migrate", "move-to-apm", "map-secret"}, item.Resolution.Decision) {
			for _, blocker := range unresolvedOnboardBlockers(item) {
				blockers = append(blockers, item.ID+":"+blocker)
			}
		}
	}
	if len(blockers) > 0 {
		return result, fmt.Errorf("onboarding plan has unresolved blockers: %s", strings.Join(sortedUnique(blockers), "; "))
	}
	marketData, marketExisted, _, err := captureOnboardOptionalFile(filepath.Join(filepath.Dir(manifestPath), "marketplaces.json"))
	if err != nil {
		return result, err
	}
	if err := validateOnboardMarketplacePreflight(markets); err != nil {
		return result, err
	}
	inv, err := ExtractLegacyCandidates(a.ConfigPath)
	if err != nil {
		return result, err
	}
	docs, err := captureJournalDocuments(inv.Documents)
	if err != nil {
		return result, err
	}
	stateRoot, err := onboardingRoot(a.StateDir)
	if err != nil {
		return result, err
	}
	opRoot, err := stateRoot.Child(plan.OperationID)
	if err != nil {
		return result, err
	}
	journal := onboardJournal{SchemaVersion: 1, OperationID: plan.OperationID, PlanID: plan.PlanID, ResolutionID: plan.ResolutionID, CandidateSetID: plan.CandidateSetID, PreimageSet: plan.PreimageSet, Phase: "planned", Documents: docs, ManifestPath: manifestPath, ManifestData: base64.StdEncoding.EncodeToString(existing), ManifestExisted: manifestExisted, ManifestMode: manifestModeValue, ManifestHash: digestBytes(existing), ProposedManifestHash: digestBytes(proposed), MarketplaceData: base64.StdEncoding.EncodeToString(marketData), MarketplaceExisted: marketExisted, MarketplaceHash: digestBytes(marketData), Targets: reviewedOnboardTargets(plan), Marketplaces: markets}
	if err := writePlanAndJournal(opRoot, plan, &journal); err != nil {
		return result, err
	}
	if err := lock.Close(); err != nil {
		return result, err
	}
	lockOpen = false
	if err := a.continueOnboardOperation(ctx, opRoot, plan, &journal, proposed); err != nil {
		return result, err
	}
	env := OnboardEnvelope{OK: true, Kind: "onboard-apply", OperationID: plan.OperationID, State: "complete"}
	return AgentsOnboardResult{Envelope: env, CandidateSetID: plan.CandidateSetID, PreimageSet: plan.PreimageSet}, nil
}

var onboardingPhaseFailpoint func(phase string) error
var onboardingDotMaterializer = func(ctx context.Context, a *App, ref OnboardDotsRef) error { return a.materializeOnboardDot(ctx, ref) }

func (a *App) continueOnboardOperation(ctx context.Context, root *securefile.Root, plan OnboardPlan, journal *onboardJournal, proposed []byte) error {
	advance := func(phase string) error {
		journal.Phase = phase
		if err := writeOnboardJournal(root, *journal); err != nil {
			return err
		}
		if onboardingPhaseFailpoint != nil {
			return onboardingPhaseFailpoint(phase)
		}
		return nil
	}
	if journal.Phase == "planned" {
		if err := stageOnboardDotPackages(root, plan.Items, journal); err != nil {
			return err
		}
		if err := advance("preflighted"); err != nil {
			return err
		}
	}
	scrub, err := deriveOnboardScrubEnv(plan, proposed, *journal)
	if err != nil {
		return err
	}
	if journal.Phase == "preflighted" {
		if err := verifyOnboardMarketplacePreimage(*journal); err != nil {
			return err
		}
		if err := dryRunOnboardManifest(ctx, a, root, proposed, *journal, scrub); err != nil {
			return err
		}
		if err := advance("materializing-dots"); err != nil {
			return err
		}
	}
	if journal.Phase == "materializing-dots" {
		for _, item := range plan.Items {
			if item.Resolution.Decision != "move-to-apm" || item.Dots == nil || slices.Contains(journal.MaterializedItems, item.ID) {
				continue
			}
			if journal.PendingMaterializeItem == item.ID {
				if item.Dots.Native {
					if _, err := os.Lstat(item.Dots.SourcePath); errors.Is(err, os.ErrNotExist) {
						journal.MaterializedItems = append(journal.MaterializedItems, item.ID)
						journal.PendingMaterializeItem = ""
						if err := writeOnboardJournal(root, *journal); err != nil {
							return err
						}
						continue
					}
				}
				if err := verifyMaterializedOnboardItem(item, *journal); err == nil && onboardDotOwnershipReleased(a.ConfigPath, *item.Dots) {
					journal.MaterializedItems = append(journal.MaterializedItems, item.ID)
					journal.PendingMaterializeItem = ""
					if err := writeOnboardJournal(root, *journal); err != nil {
						return err
					}
					continue
				}
			}
			fingerprint, err := onboardTreeFingerprint(item.Dots.SourcePath, item.Dots.SourcePath)
			if err != nil || fingerprint != item.ContentFingerprint {
				return &OnboardingRecoveryError{OperationID: plan.OperationID, OmniPhase: journal.Phase, Cause: errors.New("dots source changed after review")}
			}
			journal.PendingMaterializeItem = item.ID
			if err := writeOnboardJournal(root, *journal); err != nil {
				return err
			}
			if item.Dots.Native {
				if err := removeOnboardNativeSource(*item.Dots); err != nil {
					return &OnboardingRecoveryError{OperationID: plan.OperationID, OmniPhase: journal.Phase, Cause: err}
				}
			} else {
				if err := onboardingDotMaterializer(ctx, a, *item.Dots); err != nil {
					return &OnboardingRecoveryError{OperationID: plan.OperationID, OmniPhase: journal.Phase, Cause: err}
				}
				if err := verifyMaterializedOnboardItem(item, *journal); err != nil {
					return &OnboardingRecoveryError{OperationID: plan.OperationID, OmniPhase: journal.Phase, Cause: fmt.Errorf("%s: %w", item.Name, err)}
				}
			}
			if onboardingPhaseFailpoint != nil {
				if err := onboardingPhaseFailpoint("after-materialize:" + item.ID); err != nil {
					return err
				}
			}
			journal.MaterializedItems = append(journal.MaterializedItems, item.ID)
			journal.PendingMaterializeItem = ""
			if err := writeOnboardJournal(root, *journal); err != nil {
				return err
			}
			if onboardingPhaseFailpoint != nil {
				if err := onboardingPhaseFailpoint("materialized:" + item.ID); err != nil {
					return err
				}
			}
		}
		if err := advance("dots-materialized"); err != nil {
			return err
		}
	}
	if journal.Phase == "dots-materialized" {
		client, err := a.onboardingClient(ctx)
		if err != nil {
			return err
		}
		if err := promoteOnboardPackages(*journal); err != nil {
			return err
		}
		if err := registerOnboardMarketplaces(ctx, client, journal.Marketplaces); err != nil {
			return err
		}
		if err := writeOnboardManifestCAS(*journal, proposed); err != nil {
			return err
		}
		if err := advance("manifest-installed"); err != nil {
			return err
		}
	}
	if journal.Phase == "manifest-installed" {
		if err := verifyOnboardManifestHash(journal.ManifestPath, journal.ProposedManifestHash); err != nil {
			return err
		}
		client, err := a.onboardingClient(ctx)
		if err != nil {
			return err
		}
		if _, err := client.InstallOnly(ctx, apm.SurfacePackages, journal.Targets, apm.InstallOptions{DryRun: true, ScrubEnv: scrub}); err != nil {
			journal.Phase = "dots-materialized"
			phaseErr := writeOnboardJournal(root, *journal)
			if phaseErr == nil && onboardingPhaseFailpoint != nil {
				phaseErr = onboardingPhaseFailpoint("manifest-replayable-before-restore")
			}
			if phaseErr != nil {
				return errors.Join(err, phaseErr)
			}
			restoreErr := restoreOnboardManifest(*journal)
			return errors.Join(err, restoreErr, phaseErr)
		}
		if _, err := client.InstallOnly(ctx, apm.SurfaceMcp, journal.Targets, apm.InstallOptions{DryRun: true, ScrubEnv: scrub}); err != nil {
			return err
		}
		if err := prepareOnboardTransformedTargets(plan, *journal); err != nil {
			return err
		}
		if _, err := client.InstallOnly(ctx, apm.SurfacePackages, journal.Targets, apm.InstallOptions{ScrubEnv: scrub}); err != nil {
			return err
		}
		if err := advance("packages-installed"); err != nil {
			return err
		}
	}
	if journal.Phase == "packages-installed" {
		client, err := a.onboardingClient(ctx)
		if err != nil {
			return err
		}
		if _, err := client.InstallOnly(ctx, apm.SurfaceMcp, journal.Targets, apm.InstallOptions{DryRun: true, ScrubEnv: scrub}); err != nil {
			return err
		}
		if _, err := client.InstallOnly(ctx, apm.SurfaceMcp, journal.Targets, apm.InstallOptions{ScrubEnv: scrub}); err != nil {
			return err
		}
		if err := advance("mcp-installed"); err != nil {
			return err
		}
	}
	if journal.Phase == "mcp-installed" {
		client, err := a.onboardingClient(ctx)
		if err != nil {
			return err
		}
		if _, err := client.AuditGlobal(ctx, scrub); err != nil {
			return err
		}
		if err := advance("audited"); err != nil {
			return err
		}
	}
	if journal.Phase == "audited" {
		lock, err := config.AcquireWriteLock(a.ConfigPath)
		if err != nil {
			return err
		}
		if err := commitLegacyFragments(journal.Documents, a.ConfigPath); err != nil {
			return errors.Join(err, lock.Close())
		}
		if err := lock.Close(); err != nil {
			return err
		}
		if err := advance("v24-committed"); err != nil {
			return err
		}
	}
	if journal.Phase == "v24-committed" {
		markerPath, err := onboardCompletionMarkerPath()
		if err != nil {
			return err
		}
		if err := atomicWriteOnboardFile(markerPath, []byte(journal.OperationID+"\n"), 0o600); err != nil {
			return err
		}
		return advance("complete")
	}
	if journal.Phase != "complete" {
		return fmt.Errorf("unsupported onboarding journal phase %q", journal.Phase)
	}
	return nil
}

func onboardCompletionMarkerPath() (string, error) {
	workspace, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(workspace, ".omni-onboarding-complete"), nil
}

func verifyOnboardMarketplacePreimage(j onboardJournal) error {
	path := filepath.Join(filepath.Dir(j.ManifestPath), "marketplaces.json")
	data, existed, _, err := captureOnboardOptionalFile(path)
	if err != nil {
		return err
	}
	if existed != j.MarketplaceExisted || digestBytes(data) != j.MarketplaceHash {
		return errors.New("APM marketplace registry changed since onboarding preflight")
	}
	return nil
}

func registerOnboardMarketplaces(ctx context.Context, client *apm.Client, markets []OnboardMarketplace) error {
	registered, err := readRegisteredOnboardMarketplaces()
	if err != nil {
		return err
	}
	for _, market := range markets {
		if current, ok := registered[strings.ToLower(market.Name)]; ok {
			if !sameOnboardMarketplaceSource(current, market.Source) {
				return fmt.Errorf("marketplace %q conflicts with registered source", market.Name)
			}
			continue
		}
		if _, err := client.Run(ctx, "marketplace", "add", market.Source, "--name", market.Name); err != nil {
			return err
		}
		registered[strings.ToLower(market.Name)] = market.Source
	}
	return nil
}

func readRegisteredOnboardMarketplaces() (map[string]string, error) {
	workspace, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(workspace, "marketplaces.json"))
	if errors.Is(err, os.ErrNotExist) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, err
	}
	var registry struct {
		Marketplaces []struct {
			Name  string `json:"name"`
			URL   string `json:"url"`
			Owner string `json:"owner"`
			Repo  string `json:"repo"`
		} `json:"marketplaces"`
	}
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, fmt.Errorf("parse APM marketplace registry: %w", err)
	}
	out := make(map[string]string, len(registry.Marketplaces))
	for _, market := range registry.Marketplaces {
		source := market.URL
		if source == "" && market.Owner != "" && market.Repo != "" {
			source = market.Owner + "/" + market.Repo
		}
		out[strings.ToLower(market.Name)] = source
	}
	return out, nil
}

func sameOnboardMarketplaceSource(left, right string) bool {
	normalize := func(value string) string {
		value = strings.TrimSpace(strings.TrimSuffix(value, ".git"))
		value = strings.TrimSuffix(value, "/")
		for _, prefix := range []string{"https://github.com/", "http://github.com/", "git@github.com:"} {
			value = strings.TrimPrefix(value, prefix)
		}
		if strings.HasPrefix(value, "/") || strings.HasPrefix(value, ".") || strings.HasPrefix(value, "~") {
			if abs, err := filepath.Abs(strings.TrimPrefix(value, "~/")); err == nil {
				value = abs
			}
		}
		return strings.ToLower(value)
	}
	return normalize(left) == normalize(right)
}

func unresolvedOnboardBlockers(item OnboardItem) []string {
	var out []string
	for _, blocker := range item.Blockers {
		switch {
		case blocker == "target-resolution-required" && len(item.Resolution.ApprovedTargets) > 0:
			continue
		case strings.HasPrefix(blocker, "unknown-target:") && allOnboardTargetsAllowed(item):
			continue
		case blocker == "secret-mapping-required" && len(item.Resolution.EnvBindings) > 0:
			continue
		}
		out = append(out, blocker)
	}
	return out
}

func allOnboardTargetsAllowed(item OnboardItem) bool {
	if len(item.Resolution.ApprovedTargets) == 0 {
		return false
	}
	for _, target := range item.Resolution.ApprovedTargets {
		if !slices.Contains(item.TargetOptions, target) {
			return false
		}
	}
	return true
}

func validateReviewedOnboardFresh(reviewed, fresh OnboardPlan) error {
	if reviewed.CandidateSetID != fresh.CandidateSetID || reviewed.PreimageSet != fresh.PreimageSet || !reflect.DeepEqual(reviewed.SourcePreimages, fresh.SourcePreimages) {
		return errors.New("reviewed plan is stale: legacy or dots inventory changed")
	}
	if len(reviewed.Items) != len(fresh.Items) {
		return errors.New("reviewed plan item count differs from fresh inventory")
	}
	freshByID := make(map[string]OnboardItem, len(fresh.Items))
	for _, item := range fresh.Items {
		if !hexID(item.ID, 64) || freshByID[item.ID].ID != "" {
			return errors.New("fresh onboarding inventory has invalid item identity")
		}
		freshByID[item.ID] = item
	}
	for _, item := range reviewed.Items {
		if !hexID(item.ID, 64) {
			return fmt.Errorf("invalid reviewed item ID %q", item.ID)
		}
		current, ok := freshByID[item.ID]
		if !ok {
			return fmt.Errorf("reviewed item %s is absent from fresh inventory", item.ID)
		}
		left, right := item, current
		left.Resolution, right.Resolution = OnboardResolution{}, OnboardResolution{}
		if !reflect.DeepEqual(left, right) {
			return fmt.Errorf("reviewed item %s immutable fields differ from fresh inventory", item.ID)
		}
		delete(freshByID, item.ID)
	}
	return nil
}

func validateOnboardResolutionKeys(plan OnboardPlan, res AgentsOnboardResolutions) error {
	byName := map[string][]string{}
	for _, item := range plan.Items {
		byName[item.Name] = append(byName[item.Name], item.ID)
	}
	validateKey := func(key string) error {
		for _, item := range plan.Items {
			if item.ID == key {
				return nil
			}
		}
		ids := byName[key]
		if len(ids) == 1 {
			return nil
		}
		if len(ids) > 1 {
			return fmt.Errorf("onboarding resolution name %q is ambiguous; use one of item IDs %s", key, strings.Join(ids, ", "))
		}
		return fmt.Errorf("onboarding resolution item %q is not in the reviewed plan", key)
	}
	keys := map[string]bool{}
	for key := range res.ApprovedTargets {
		keys[key] = true
	}
	for key := range res.EnvBindings {
		keys[key] = true
	}
	for key := range res.Excluded {
		keys[key] = true
	}
	for key := range res.MoveToAPM {
		keys[key] = true
	}
	for key := range res.KeepInDots {
		keys[key] = true
	}
	for key := range keys {
		if err := validateKey(key); err != nil {
			return err
		}
		decisions := 0
		if res.Excluded[key] {
			decisions++
		}
		if res.MoveToAPM[key] {
			decisions++
		}
		if res.KeepInDots[key] {
			decisions++
		}
		if len(res.EnvBindings[key]) > 0 {
			decisions++
		}
		if decisions > 1 {
			return fmt.Errorf("onboarding item %q has conflicting decisions", key)
		}
	}
	for _, item := range plan.Items {
		itemKeys := []string{item.ID}
		if len(byName[item.Name]) == 1 {
			itemKeys = append(itemKeys, item.Name)
		}
		decisions := 0
		for _, key := range itemKeys {
			if res.Excluded[key] {
				decisions++
			}
			if res.MoveToAPM[key] {
				decisions++
			}
			if res.KeepInDots[key] {
				decisions++
			}
			if len(res.EnvBindings[key]) > 0 {
				decisions++
			}
		}
		if decisions > 1 {
			return fmt.Errorf("onboarding item %q has conflicting decisions", item.ID)
		}
	}
	return nil
}

func captureOnboardOptionalFile(path string) ([]byte, bool, uint32, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, 0, nil
	}
	if err != nil {
		return nil, false, 0, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, false, 0, fmt.Errorf("APM preimage %q must be a regular non-symlink file", path)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, 0, err
	}
	return data, true, uint32(info.Mode().Perm()), nil
}

func validateOnboardMarketplacePreflight(markets []OnboardMarketplace) error {
	registered, err := readRegisteredOnboardMarketplaces()
	if err != nil {
		return err
	}
	for _, market := range markets {
		if current, ok := registered[strings.ToLower(market.Name)]; ok && !sameOnboardMarketplaceSource(current, market.Source) {
			return fmt.Errorf("marketplace %q conflicts with registered source", market.Name)
		}
	}
	return nil
}

func (a *App) AgentsOnboardStatus(_ context.Context, id string) (AgentsOnboardStatusResult, error) {
	j, err := a.readOnboardOperation(id)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	state := "incomplete"
	if j.Phase == "complete" || j.Phase == "rolled-back" {
		state = j.Phase
	}
	return AgentsOnboardStatusResult{OperationID: id, OmniPhase: j.Phase, APM: OnboardEnvelope{OK: state != "incomplete", Kind: "onboard-status", OperationID: id, State: state}}, nil
}
func (a *App) AgentsOnboardResume(ctx context.Context, id string) (result AgentsOnboardStatusResult, retErr error) {
	j, err := a.readOnboardOperation(id)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	if j.Phase == "complete" {
		return a.AgentsOnboardStatus(ctx, id)
	}
	root, err := securefile.OpenRoot(filepath.Join(a.StateDir, "onboarding", id))
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	plan, err := readReviewedPlan(filepath.Join(root.Path(), "plan.json"))
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	preimage, err := decodeOnboardRecoveryData(j.ManifestData)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	proposed, _, blockers, err := buildOnboardManifest(preimage, plan.Items)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	if len(blockers) > 0 {
		return AgentsOnboardStatusResult{}, fmt.Errorf("resume manifest blockers: %s", strings.Join(blockers, ", "))
	}
	if err := a.continueOnboardOperation(ctx, root, plan, &j, proposed); err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	return a.AgentsOnboardStatus(ctx, id)
}

type AgentsOnboardCleanupPreview struct {
	OperationID  string   `json:"operation_id"`
	Paths        []string `json:"paths"`
	Count        int      `json:"count"`
	AlreadyClean bool     `json:"already_clean"`
}

func (a *App) AgentsOnboardCleanup(_ context.Context, id string, confirm bool) (AgentsOnboardCleanupPreview, error) {
	path := filepath.Join(a.StateDir, "onboarding", id)
	p := AgentsOnboardCleanupPreview{OperationID: id, Paths: []string{path}, Count: 1}
	j, err := a.readOnboardOperation(id)
	if errors.Is(err, os.ErrNotExist) {
		p.AlreadyClean = true
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if j.Phase != "complete" && j.Phase != "rolled-back" {
		return p, errors.New("incomplete onboarding cannot be cleaned; resume or roll it back")
	}
	if !confirm {
		return p, nil
	}
	root, err := securefile.OpenRoot(filepath.Join(a.StateDir, "onboarding"))
	if err != nil {
		return p, err
	}
	return p, root.Remove(id)
}
func (a *App) AgentsOnboardRollback(_ context.Context, id string) (AgentsOnboardStatusResult, error) {
	root, err := securefile.OpenRoot(filepath.Join(a.StateDir, "onboarding", id))
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	j, err := readOnboardJournal(root)
	if err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	if phaseAtOrAfter(j.Phase, "materializing-dots") {
		return AgentsOnboardStatusResult{}, errors.New("dots materialization started; rollback is unsafe, resume forward")
	}
	if err := root.Remove("staging"); err != nil && !errors.Is(err, os.ErrNotExist) {
		return AgentsOnboardStatusResult{}, err
	}
	j.Phase = "rolled-back"
	if err := writeOnboardJournal(root, j); err != nil {
		return AgentsOnboardStatusResult{}, err
	}
	return AgentsOnboardStatusResult{OperationID: id, OmniPhase: j.Phase, APM: OnboardEnvelope{OK: true, State: "rolled-back"}}, nil
}

func (a *App) readOnboardOperation(id string) (onboardJournal, error) {
	if !hexID(id, 32) {
		return onboardJournal{}, errors.New("invalid operation ID")
	}
	root, err := securefile.OpenRoot(filepath.Join(a.StateDir, "onboarding", id))
	if err != nil {
		return onboardJournal{}, err
	}
	return readOnboardJournal(root)
}
func validateReviewedOnboardPlan(plan OnboardPlan) error {
	if plan.SchemaVersion != onboardSchemaVersion {
		return errors.New("reviewed plan schema mismatch")
	}
	for _, item := range plan.Items {
		if item.Dots != nil && item.Dots.Native && item.Resolution.Decision == "keep-in-dots" {
			return fmt.Errorf("native item %s cannot be kept in dots", item.Name)
		}
	}
	p, r, o := plan.PlanID, plan.ResolutionID, plan.OperationID
	if err := bindOnboardPlan(&plan); err != nil {
		return err
	}
	if plan.PlanID != p || plan.ResolutionID != r || plan.OperationID != o {
		return errors.New("reviewed plan identity mismatch")
	}
	return nil
}
func readReviewedPlan(path string) (OnboardPlan, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return OnboardPlan{}, err
	}
	var plan OnboardPlan
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&plan); err != nil {
		return OnboardPlan{}, err
	}
	return plan, validateReviewedOnboardPlan(plan)
}
func sortedUnique(v []string) []string {
	o := append([]string(nil), v...)
	sort.Strings(o)
	return slices.Compact(o)
}
func lookupResolution[T any](v map[string]T, id, name string) T {
	if x, ok := v[id]; ok {
		return x
	}
	return v[name]
}

func validateApprovedTargetResolutions(plan OnboardPlan, resolutions map[string][]string) error {
	for key, targets := range resolutions {
		matched := false
		for _, item := range plan.Items {
			if key != item.ID && key != item.Name {
				continue
			}
			matched = true
			for _, target := range targets {
				if !slices.Contains(item.TargetOptions, target) {
					return fmt.Errorf("item %s does not allow target %s", item.Name, target)
				}
			}
		}
		if !matched || len(targets) == 0 {
			return fmt.Errorf("invalid target resolution item %s", key)
		}
	}
	return nil
}
func onboardManifestPath() (string, error) {
	root, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "apm.yml"), nil
}
func reviewedOnboardTargets(plan OnboardPlan) []string {
	var out []string
	for _, item := range plan.Items {
		if slices.Contains([]string{"migrate", "move-to-apm", "map-secret"}, item.Resolution.Decision) {
			out = append(out, item.Resolution.ApprovedTargets...)
		}
	}
	return sortedUnique(out)
}
func reviewedOnboardEnv(plan OnboardPlan) []string {
	var out []string
	for _, item := range plan.Items {
		if !slices.Contains([]string{"migrate", "map-secret"}, item.Resolution.Decision) {
			continue
		}
		for _, name := range item.Resolution.EnvBindings {
			if validOnboardEnvName(name) {
				out = append(out, name)
			}
		}
		var payload any
		if json.Unmarshal(item.Payload, &payload) == nil {
			collectOnboardPlaceholderEnv(payload, &out)
		}
	}
	return sortedUnique(out)
}

func deriveOnboardScrubEnv(plan OnboardPlan, proposed []byte, journal onboardJournal) ([]string, error) {
	out := reviewedOnboardEnv(plan)
	collectOnboardPlaceholderText(string(proposed), &out)
	for _, pkg := range journal.Packages {
		root := pkg.StagedPath
		if _, err := os.Lstat(root); errors.Is(err, os.ErrNotExist) {
			root, err = durableOnboardPackagePath(pkg.ItemID)
			if err != nil {
				return nil, err
			}
		}
		err := filepath.WalkDir(root, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			info, err := os.Lstat(path)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("staged APM input %q is a symlink", path)
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			collectOnboardPlaceholderText(string(data), &out)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return sortedUnique(out), nil
}

func collectOnboardPlaceholderText(text string, out *[]string) {
	collectOnboardPlaceholderEnv(text, out)
}

func collectOnboardPlaceholderEnv(value any, out *[]string) {
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			collectOnboardPlaceholderEnv(child, out)
		}
	case []any:
		for _, child := range typed {
			collectOnboardPlaceholderEnv(child, out)
		}
	case string:
		for offset := 0; offset < len(typed); {
			start := strings.Index(typed[offset:], "${")
			if start < 0 {
				break
			}
			start += offset
			end := strings.Index(typed[start+2:], "}")
			if end < 0 {
				break
			}
			end += start + 2
			name := strings.TrimPrefix(typed[start+2:end], "env:")
			if validOnboardEnvName(name) {
				*out = append(*out, name)
			}
			offset = end + 1
		}
	}
}
func writePlanAndJournal(root *securefile.Root, plan OnboardPlan, j *onboardJournal) error {
	data, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	if err := root.WriteFileAtomic("plan.json", append(data, '\n')); err != nil {
		return err
	}
	return writeOnboardJournal(root, *j)
}

func removeOnboardNativeSource(ref OnboardDotsRef) error {
	rel, err := filepath.Rel(ref.OwnerRoot, ref.SourcePath)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || strings.ContainsRune(rel, os.PathSeparator) {
		return errors.New("unsafe native onboarding source")
	}
	if err := validateOnboardSourceAncestors(ref.OwnerRoot, ref.SourcePath); err != nil {
		return err
	}
	return os.RemoveAll(ref.SourcePath)
}
func stageOnboardDotPackages(root *securefile.Root, items []OnboardItem, j *onboardJournal) error {
	for _, item := range items {
		if item.Resolution.Decision != "move-to-apm" || item.Dots == nil {
			continue
		}
		if err := validateOnboardSourceAncestors(item.Dots.OwnerRoot, item.Dots.SourcePath); err != nil {
			return err
		}
		fingerprint, err := onboardTreeFingerprint(item.Dots.SourcePath, item.Dots.SourcePath)
		if err != nil || fingerprint != item.ContentFingerprint {
			return fmt.Errorf("dots item %s source changed before staging", item.ID)
		}
		pkg := filepath.Join(root.Path(), "staging", item.ID)
		if err := os.MkdirAll(filepath.Dir(pkg), 0o700); err != nil {
			return err
		}
		resource := onboardPackageResourcePath(pkg, item)
		if err := os.MkdirAll(filepath.Dir(resource), 0o700); err != nil {
			return err
		}
		if err := dots.CopyDotPath(item.Dots.SourcePath, resource, nil); err != nil {
			return err
		}
		if item.Kind != "package" && item.Kind != "plugin" {
			manifest := []byte("name: omni-import-" + item.ID[:12] + "\nversion: 1.0.0\nincludes: auto\n")
			if err := atomicWriteOnboardFile(filepath.Join(pkg, "apm.yml"), manifest, 0o600); err != nil {
				return err
			}
		}
		if item.Kind == "plugin" && !regularOnboardFile(filepath.Join(pkg, "apm.yml")) {
			if err := normalizeStagedOnboardPlugin(pkg); err != nil {
				return err
			}
			manifest := []byte("name: omni-import-" + item.ID[:12] + "\nversion: 1.0.0\nincludes: auto\n")
			if err := atomicWriteOnboardFile(filepath.Join(pkg, "apm.yml"), manifest, 0o600); err != nil {
				return err
			}
		}
		if err := validateStagedOnboardPackage(pkg, item); err != nil {
			return err
		}
		hash, err := onboardTreeFingerprint(pkg, pkg)
		if err != nil {
			return err
		}
		j.Packages = append(j.Packages, journalPackage{ItemID: item.ID, StagedPath: pkg, Hash: hash})
	}
	return writeOnboardJournal(root, *j)
}

func normalizeStagedOnboardPlugin(pkg string) error {
	for _, dir := range []string{"skills", "agents", "hooks"} {
		source := filepath.Join(pkg, dir)
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := dots.CopyDotPath(source, filepath.Join(pkg, ".apm", dir), nil); err != nil {
			return err
		}
	}
	commands := filepath.Join(pkg, "commands")
	entries, err := os.ReadDir(commands)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".md") + ".prompt.md"
		if err := dots.CopyDotPath(filepath.Join(commands, entry.Name()), filepath.Join(pkg, ".apm", "prompts", name), nil); err != nil {
			return err
		}
	}
	return nil
}

func onboardPackageResourcePath(pkg string, item OnboardItem) string {
	if item.Kind == "package" || item.Kind == "plugin" {
		return pkg
	}
	dir, name := item.Kind+"s", item.Name
	if item.Kind == "command" || item.Kind == "prompt" {
		dir = "prompts"
		name = strings.TrimSuffix(strings.TrimSuffix(name, ".prompt.md"), ".md") + ".prompt.md"
	}
	return filepath.Join(pkg, ".apm", dir, name)
}
func dryRunOnboardManifest(ctx context.Context, a *App, root *securefile.Root, manifest []byte, j onboardJournal, scrub []string) error {
	for _, pkg := range j.Packages {
		durable, err := durableOnboardPackagePath(pkg.ItemID)
		if err != nil {
			return err
		}
		manifest = bytes.ReplaceAll(manifest, []byte(durable), []byte(pkg.StagedPath))
	}
	validation := filepath.Join(root.Path(), "validation")
	if err := os.MkdirAll(validation, 0o700); err != nil {
		return err
	}
	if err := atomicWriteOnboardFile(filepath.Join(validation, "apm.yml"), manifest, 0o600); err != nil {
		return err
	}
	client, err := a.onboardingClient(ctx)
	if err != nil {
		return err
	}
	_, err = client.AtProjectRoot(validation).DryRunOnly(ctx, apm.SurfacePackages, j.Targets, scrub)
	if err != nil {
		return err
	}
	_, err = client.AtProjectRoot(validation).DryRunOnly(ctx, apm.SurfaceMcp, j.Targets, scrub)
	return err
}
func verifyMaterializedOnboardItem(item OnboardItem, j onboardJournal) error {
	var pkg journalPackage
	for _, x := range j.Packages {
		if x.ItemID == item.ID {
			pkg = x
			break
		}
	}
	if pkg.ItemID == "" {
		return errors.New("missing staged dots package")
	}
	packageRoot := pkg.StagedPath
	if _, err := os.Lstat(packageRoot); errors.Is(err, os.ErrNotExist) {
		packageRoot, err = durableOnboardPackagePath(pkg.ItemID)
		if err != nil {
			return err
		}
	}
	resource := onboardPackageResourcePath(packageRoot, item)
	want, err := onboardContentFingerprint(resource)
	if item.Kind == "plugin" {
		want, err = onboardContentFingerprintIgnoring(resource, "plugin-generated")
	}
	if err != nil {
		return err
	}
	got, err := onboardContentFingerprint(item.Dots.TargetPath)
	if err != nil {
		return err
	}
	if want != got {
		return errors.New("post-materialization content drift")
	}
	return nil
}

func prepareOnboardTransformedTargets(plan OnboardPlan, journal onboardJournal) error {
	for _, item := range plan.Items {
		if item.Dots == nil || item.Dots.Native || item.Resolution.Decision != "move-to-apm" || (item.Kind != "command" && item.Kind != "prompt") {
			continue
		}
		if _, err := os.Lstat(item.Dots.TargetPath); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return err
		}
		if err := verifyMaterializedOnboardItem(item, journal); err != nil {
			return err
		}
		if err := os.Remove(item.Dots.TargetPath); err != nil {
			return err
		}
	}
	return nil
}
func promoteOnboardPackages(j onboardJournal) error {
	workspace, err := apm.GlobalWorkspaceDir()
	if err != nil {
		return err
	}
	for _, pkg := range j.Packages {
		dst := filepath.Join(workspace, "omni-imports", pkg.ItemID)
		if got, err := onboardTreeFingerprint(dst, dst); err == nil {
			if got != pkg.Hash {
				return errors.New("durable package hash conflict")
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
			return err
		}
		if err := os.Rename(pkg.StagedPath, dst); err != nil {
			return err
		}
	}
	return nil
}
func restoreOnboardManifest(j onboardJournal) error {
	if err := verifyOnboardManifestHash(j.ManifestPath, j.ProposedManifestHash); err != nil {
		return err
	}
	if !j.ManifestExisted {
		err := os.Remove(j.ManifestPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	mode := os.FileMode(j.ManifestMode)
	if mode == 0 {
		mode = 0o600
	}
	data, err := decodeOnboardRecoveryData(j.ManifestData)
	if err != nil {
		return err
	}
	return atomicWriteOnboardFile(j.ManifestPath, data, mode)
}

func decodeOnboardRecoveryData(value string) ([]byte, error) {
	data, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return nil, errors.New("invalid onboarding recovery data")
	}
	return data, nil
}

func writeOnboardManifestCAS(j onboardJournal, proposed []byte) error {
	data, existed, _, err := captureOnboardOptionalFile(j.ManifestPath)
	if err != nil {
		return err
	}
	currentHash := digestBytes(data)
	preimageMatches := existed == j.ManifestExisted && currentHash == j.ManifestHash
	proposalMatches := existed && currentHash == j.ProposedManifestHash
	if !preimageMatches && !proposalMatches {
		return errors.New("APM manifest changed since onboarding preflight")
	}
	if proposalMatches {
		return nil
	}
	mode := os.FileMode(j.ManifestMode)
	if mode == 0 {
		mode = 0o600
	}
	return atomicWriteOnboardFile(j.ManifestPath, proposed, mode)
}

func verifyOnboardManifestHash(path, want string) error {
	data, existed, _, err := captureOnboardOptionalFile(path)
	if err != nil {
		return err
	}
	if !existed || digestBytes(data) != want {
		return errors.New("APM manifest no longer matches onboarding journal")
	}
	return nil
}
func phaseAtOrAfter(current, threshold string) bool {
	p := []string{"planned", "preflighted", "materializing-dots", "dots-materialized", "manifest-installed", "packages-installed", "mcp-installed", "audited", "v24-committed", "complete"}
	return slices.Index(p, current) >= slices.Index(p, threshold)
}
