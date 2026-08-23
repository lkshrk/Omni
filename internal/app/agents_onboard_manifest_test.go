package app

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/securefile"
)

func TestOnboardPrimitiveBoundaries(t *testing.T) {
	root := t.TempDir()
	cases := []struct {
		rel, kind string
		dir       bool
	}{
		{"agents/reviewer.md", "agent", false}, {"prompts/release.md", "prompt", false}, {"commands/check.md", "command", false}, {"hooks/events.json", "hook", false}, {"hooks/lifecycle", "hook", true},
	}
	for _, tc := range cases {
		path := filepath.Join(root, filepath.FromSlash(tc.rel))
		if tc.dir {
			if err := os.MkdirAll(path, 0o700); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		info, err := os.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		got, ok := apmResourceBoundary(root, path, fs.FileInfoToDirEntry(info))
		if !ok || got != tc.kind {
			t.Fatalf("%s = %q,%v want %q,true", tc.rel, got, ok, tc.kind)
		}
	}
}

func TestContinueOnboardOperationResumesAfterPartialDotsMaterialization(t *testing.T) {
	root, err := securefile.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	items := make([]OnboardItem, 2)
	journal := onboardJournal{SchemaVersion: 1, OperationID: strings.Repeat("a", 32), PlanID: strings.Repeat("b", 64), ResolutionID: strings.Repeat("c", 64), CandidateSetID: strings.Repeat("d", 64), Phase: "materializing-dots", ManifestPath: filepath.Join(t.TempDir(), "apm.yml")}
	for i := range items {
		id := strings.Repeat(string(rune('1'+i)), 64)
		name := "item" + string(rune('1'+i))
		staged := filepath.Join(root.Path(), "staging", id)
		sourcePath := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(sourcePath, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		fingerprint, err := onboardTreeFingerprint(sourcePath, sourcePath)
		if err != nil {
			t.Fatal(err)
		}
		item := OnboardItem{ID: id, Kind: "agent", Name: name, ContentFingerprint: fingerprint, Resolution: OnboardResolution{Decision: "move-to-apm"}, Dots: &OnboardDotsRef{TargetPath: filepath.Join(t.TempDir(), name), SourcePath: sourcePath}}
		resource := onboardPackageResourcePath(staged, item)
		if err := os.MkdirAll(filepath.Dir(resource), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(resource, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(item.Dots.TargetPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(item.Dots.TargetPath, []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
		items[i] = item
		journal.Packages = append(journal.Packages, journalPackage{ItemID: id, StagedPath: staged, Hash: strings.Repeat("e", 64)})
	}
	plan := OnboardPlan{OperationID: journal.OperationID, Items: items}
	originalMaterializer, originalFailpoint := onboardingDotMaterializer, onboardingPhaseFailpoint
	defer func() { onboardingDotMaterializer, onboardingPhaseFailpoint = originalMaterializer, originalFailpoint }()
	var calls []string
	onboardingDotMaterializer = func(_ context.Context, _ *App, ref OnboardDotsRef) error {
		calls = append(calls, ref.TargetPath)
		return os.RemoveAll(ref.SourcePath)
	}
	onboardingPhaseFailpoint = func(phase string) error {
		if phase == "after-materialize:"+items[0].ID {
			return errors.New("crash")
		}
		return nil
	}
	if err := (&App{}).continueOnboardOperation(t.Context(), root, plan, &journal, nil); err == nil {
		t.Fatal("failpoint did not stop operation")
	}
	persisted, err := readOnboardJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(persisted.MaterializedItems) != 0 || persisted.PendingMaterializeItem != items[0].ID {
		t.Fatalf("persisted=%v pending=%s", persisted.MaterializedItems, persisted.PendingMaterializeItem)
	}
	onboardingPhaseFailpoint = func(phase string) error {
		if phase == "dots-materialized" {
			return errors.New("stop before APM")
		}
		return nil
	}
	if err := (&App{}).continueOnboardOperation(t.Context(), root, plan, &persisted, nil); err == nil {
		t.Fatal("second failpoint did not stop operation")
	}
	if len(calls) != 2 || calls[0] != items[0].Dots.TargetPath || calls[1] != items[1].Dots.TargetPath {
		t.Fatalf("materializer calls=%v", calls)
	}
	final, err := readOnboardJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if final.Phase != "dots-materialized" || len(final.MaterializedItems) != 2 {
		t.Fatalf("phase=%s items=%v", final.Phase, final.MaterializedItems)
	}
}

func TestContinueOnboardOperationRestoresPhaseWithManifestAfterFailedPackageDryRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifest := filepath.Join(home, ".apm", "apm.yml")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o700); err != nil {
		t.Fatal(err)
	}
	old := []byte("name: old\nversion: 1.0.0\ndependencies: {apm: [], mcp: []}\n")
	proposed := []byte("name: proposed\nversion: 1.0.0\ndependencies: {apm: [], mcp: []}\n")
	if err := os.WriteFile(manifest, proposed, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := securefile.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	journal := onboardJournal{SchemaVersion: 1, OperationID: strings.Repeat("a", 32), PlanID: strings.Repeat("1", 64), ResolutionID: strings.Repeat("2", 64), CandidateSetID: strings.Repeat("3", 64), Phase: "manifest-installed", ManifestPath: manifest, ManifestData: base64.StdEncoding.EncodeToString(old), ManifestExisted: true, ManifestMode: 0o600, ManifestHash: digestBytes(old), ProposedManifestHash: digestBytes(proposed), MarketplaceHash: digestBytes(nil)}
	if err := writeOnboardJournal(root, journal); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("package dry-run failed")
	mock := &availableMatchMock{executor.NewMatchMock(
		executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "Agent Package Manager (APM) CLI version 0.28.0+omni.6\n"}},
		executor.MatchRule{Pattern: "apm install -g --only apm --dry-run", Response: executor.MockCall{Err: failure}},
	).WithFallback(executor.MockCall{})}
	a := &App{}
	a.SetFallbackExecutor(mock)
	plan := OnboardPlan{OperationID: journal.OperationID}
	if err := a.continueOnboardOperation(t.Context(), root, plan, &journal, proposed); !errors.Is(err, failure) {
		t.Fatalf("err=%v calls=%+v", err, mock.Calls)
	}
	persisted, err := readOnboardJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Phase != "dots-materialized" {
		t.Fatalf("phase=%s", persisted.Phase)
	}
	if got, err := os.ReadFile(manifest); err != nil || string(got) != string(old) {
		t.Fatalf("manifest=%q err=%v", got, err)
	}
	resume := &availableMatchMock{executor.NewMatchMock(executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "Agent Package Manager (APM) CLI version 0.28.0+omni.6\n"}}).WithFallback(executor.MockCall{})}
	a.SetFallbackExecutor(resume)
	if err := a.continueOnboardOperation(t.Context(), root, plan, &persisted, proposed); err != nil {
		t.Fatal(err)
	}
	if persisted.Phase != "complete" {
		t.Fatalf("resume phase=%s", persisted.Phase)
	}
	if got, err := os.ReadFile(manifest); err != nil || string(got) != string(proposed) {
		t.Fatalf("resumed manifest=%q err=%v", got, err)
	}
}

func TestManifestDryRunFailurePersistsReplayPhaseBeforeRestore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifest := filepath.Join(home, ".apm", "apm.yml")
	if err := os.MkdirAll(filepath.Dir(manifest), 0o700); err != nil {
		t.Fatal(err)
	}
	old := []byte("name: old\n")
	proposal := []byte("name: proposed\n")
	if err := os.WriteFile(manifest, proposal, 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := securefile.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	journal := onboardJournal{SchemaVersion: 1, OperationID: strings.Repeat("a", 32), PlanID: strings.Repeat("b", 64), ResolutionID: strings.Repeat("c", 64), CandidateSetID: strings.Repeat("d", 64), Phase: "manifest-installed", ManifestPath: manifest, ManifestData: base64.StdEncoding.EncodeToString(old), ManifestExisted: true, ManifestMode: 0o600, ManifestHash: digestBytes(old), ProposedManifestHash: digestBytes(proposal), MarketplaceHash: digestBytes(nil)}
	if err := writeOnboardJournal(root, journal); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("dryrun")
	mock := &availableMatchMock{executor.NewMatchMock(executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "Agent Package Manager (APM) CLI version 0.28.0+omni.6\n"}}, executor.MatchRule{Pattern: "apm install -g --only apm --dry-run", Response: executor.MockCall{Err: failure}}).WithFallback(executor.MockCall{})}
	a := &App{}
	a.SetFallbackExecutor(mock)
	original := onboardingPhaseFailpoint
	onboardingPhaseFailpoint = func(phase string) error {
		if phase == "manifest-replayable-before-restore" {
			return errors.New("crash")
		}
		return nil
	}
	defer func() { onboardingPhaseFailpoint = original }()
	if err := a.continueOnboardOperation(t.Context(), root, OnboardPlan{OperationID: journal.OperationID}, &journal, proposal); err == nil {
		t.Fatal("failpoint not reached")
	}
	persisted, err := readOnboardJournal(root)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Phase != "dots-materialized" {
		t.Fatalf("phase=%s", persisted.Phase)
	}
	got, err := os.ReadFile(manifest)
	if err != nil || string(got) != string(proposal) {
		t.Fatalf("manifest=%q err=%v", got, err)
	}
}

func TestContinueOnboardOperationRunsMCPDryRunsAndScrubsPlaceholders(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifest := filepath.Join(home, ".apm", "apm.yml")
	proposed := []byte("name: proposed\nversion: 1.0.0\ndependencies: {apm: [], mcp: []}\n")
	root, err := securefile.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	journal := onboardJournal{SchemaVersion: 1, OperationID: strings.Repeat("b", 32), PlanID: strings.Repeat("4", 64), ResolutionID: strings.Repeat("5", 64), CandidateSetID: strings.Repeat("6", 64), Phase: "preflighted", ManifestPath: manifest, ManifestHash: digestBytes(nil), ProposedManifestHash: digestBytes(proposed), MarketplaceHash: digestBytes(nil)}
	plan := OnboardPlan{OperationID: journal.OperationID, Items: []OnboardItem{{Payload: []byte(`{"env":{"TOKEN":"${TOKEN}"},"headers":{"Authorization":"Bearer ${env:HEADER_TOKEN}"}}`), Resolution: OnboardResolution{Decision: "migrate", EnvBindings: map[string]string{"literal": "MAPPED"}}}}}
	mock := &availableMatchMock{executor.NewMatchMock(executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "Agent Package Manager (APM) CLI version 0.28.0+omni.6\n"}}).WithFallback(executor.MockCall{})}
	a := &App{}
	a.SetFallbackExecutor(mock)
	if err := a.continueOnboardOperation(t.Context(), root, plan, &journal, proposed); err != nil {
		t.Fatalf("err=%v calls=%+v", err, mock.Calls)
	}
	firstMCPDry, firstPackageInstall, lastMCPInstall := -1, -1, -1
	for i, call := range mock.Calls {
		if call.Name != "apm" || len(call.Args) == 0 || call.Args[0] != "install" {
			continue
		}
		args := strings.Join(call.Args, " ")
		for _, name := range []string{"HEADER_TOKEN", "MAPPED", "TOKEN"} {
			if !slices.Contains(call.Env, name) {
				t.Fatalf("call %q did not scrub %s: env=%v", args, name, call.Env)
			}
		}
		if strings.Contains(args, "--only mcp") && strings.Contains(args, "--dry-run") && firstMCPDry < 0 {
			firstMCPDry = i
		}
		if strings.Contains(args, "--only apm") && !strings.Contains(args, "--dry-run") && firstPackageInstall < 0 {
			firstPackageInstall = i
		}
		if strings.Contains(args, "--only mcp") && !strings.Contains(args, "--dry-run") {
			lastMCPInstall = i
		}
	}
	if firstMCPDry < 0 || firstPackageInstall < 0 || lastMCPInstall < 0 || !(firstMCPDry < firstPackageInstall && firstPackageInstall < lastMCPInstall) {
		t.Fatalf("invalid install order: mcp-dry=%d package=%d mcp=%d calls=%+v", firstMCPDry, firstPackageInstall, lastMCPInstall, mock.Calls)
	}
}

func TestContinueOnboardOperationScrubsAuditEnvironment(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root, err := securefile.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	journal := onboardJournal{SchemaVersion: 1, OperationID: strings.Repeat("7", 32), PlanID: strings.Repeat("8", 64), ResolutionID: strings.Repeat("9", 64), CandidateSetID: strings.Repeat("a", 64), Phase: "mcp-installed", ManifestPath: filepath.Join(home, ".apm", "apm.yml"), MarketplaceHash: digestBytes(nil)}
	plan := OnboardPlan{OperationID: journal.OperationID, Items: []OnboardItem{{Payload: []byte(`{"env":{"AUDIT_TOKEN":"${AUDIT_TOKEN}"}}`), Resolution: OnboardResolution{Decision: "migrate"}}}}
	mock := &availableMatchMock{executor.NewMatchMock(executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "Agent Package Manager (APM) CLI version 0.28.0+omni.6\n"}}).WithFallback(executor.MockCall{})}
	a := &App{}
	a.SetFallbackExecutor(mock)
	original := onboardingPhaseFailpoint
	onboardingPhaseFailpoint = func(phase string) error {
		if phase == "audited" {
			return errors.New("stop")
		}
		return nil
	}
	defer func() { onboardingPhaseFailpoint = original }()
	if err := a.continueOnboardOperation(t.Context(), root, plan, &journal, nil); err == nil {
		t.Fatal("audit failpoint not reached")
	}
	for _, call := range mock.Calls {
		if call.Name == "apm" && len(call.Args) > 0 && call.Args[0] == "audit" {
			if !slices.Contains(call.Env, "AUDIT_TOKEN") {
				t.Fatalf("audit environment was not scrubbed: %+v", call)
			}
			return
		}
	}
	t.Fatalf("audit call missing: %+v", mock.Calls)
}

func TestReadRegisteredOnboardMarketplaces(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("USERPROFILE", home)
	path := filepath.Join(home, ".apm", "marketplaces.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"marketplaces":[{"name":"Tools","owner":"acme","repo":"tools"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readRegisteredOnboardMarketplaces()
	if err != nil {
		t.Fatal(err)
	}
	if got["tools"] != "acme/tools" || !sameOnboardMarketplaceSource(got["tools"], "https://github.com/acme/tools.git") {
		t.Fatalf("got=%v", got)
	}
}

func TestOnboardMarketplaceConflictAndManifestCASBlockBeforeMutation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("USERPROFILE", home)
	registry := filepath.Join(home, ".apm", "marketplaces.json")
	if err := os.MkdirAll(filepath.Dir(registry), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(registry, []byte(`{"marketplaces":[{"name":"tools","url":"https://example.test/old"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateOnboardMarketplacePreflight([]OnboardMarketplace{{Name: "tools", Source: "https://example.test/new"}}); err == nil {
		t.Fatal("marketplace conflict accepted")
	}
	manifest := filepath.Join(home, ".apm", "apm.yml")
	pre := []byte("name: old\n")
	proposal := []byte("name: proposed\n")
	if err := os.WriteFile(manifest, pre, 0o600); err != nil {
		t.Fatal(err)
	}
	j := onboardJournal{ManifestPath: manifest, ManifestExisted: true, ManifestData: base64.StdEncoding.EncodeToString(pre), ManifestHash: digestBytes(pre), ProposedManifestHash: digestBytes(proposal), ManifestMode: 0o600}
	if err := writeOnboardManifestCAS(j, proposal); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(manifest, []byte("name: user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeOnboardManifestCAS(j, proposal); err == nil {
		t.Fatal("manifest CAS overwrote user edit")
	}
	marketPreimage := []byte(`{"marketplaces":[]}`)
	if err := os.WriteFile(registry, marketPreimage, 0o600); err != nil {
		t.Fatal(err)
	}
	marketJournal := onboardJournal{MarketplaceExisted: true, MarketplaceData: string(marketPreimage), MarketplaceHash: digestBytes(marketPreimage)}
	if err := os.WriteFile(registry, []byte(`{"marketplaces":[{"name":"user"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := verifyOnboardMarketplacePreimage(marketJournal); err == nil {
		t.Fatal("marketplace registry tamper accepted")
	}
}

func TestBuildOnboardManifestNoopIsByteIdentical(t *testing.T) {
	original := []byte("# keep\nname: demo\nversion: 1.0.0\nx-policy: &policy\n  allow: true\ndependencies:\n  apm:\n    - git: https://example.test/pkg.git # package\n      ref: main\n      targets: [codex]\n  mcp: []\npolicy: *policy\n")
	item := OnboardItem{ID: strings.Repeat("a", 64), Kind: "package", Name: "pkg", Payload: []byte(`{"source":"https://example.test/pkg.git","ref":"main"}`), Resolution: OnboardResolution{Decision: "migrate", ApprovedTargets: []string{"codex"}}}
	got, _, blockers, err := buildOnboardManifest(original, []OnboardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 {
		t.Fatalf("blockers=%v", blockers)
	}
	if string(got) != string(original) {
		t.Fatalf("no-op changed manifest:\n%s", got)
	}
}

func TestBuildOnboardManifestAddsDependencyAndPreservesUnknownNodes(t *testing.T) {
	original := []byte("# keep\nname: demo\nversion: 1.0.0\nx-policy: &policy\n  allow: true\ndependencies:\n  apm: []\n  mcp: []\npolicy: *policy\n")
	item := OnboardItem{ID: strings.Repeat("b", 64), Kind: "plugin", Name: "plug", Payload: []byte(`{"name":"plug","marketplace":"tools"}`), Resolution: OnboardResolution{Decision: "migrate", ApprovedTargets: []string{"claude"}}}
	got, _, blockers, err := buildOnboardManifest(original, []OnboardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 {
		t.Fatalf("blockers=%v", blockers)
	}
	if !strings.Contains(string(got), "# keep") || !strings.Contains(string(got), "&policy") || !strings.Contains(string(got), "marketplace: tools") {
		t.Fatalf("rendered manifest lost nodes:\n%s", got)
	}
	var decoded map[string]any
	if err := yaml.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["x-policy"] == nil || decoded["policy"] == nil {
		t.Fatalf("unknown nodes missing: %#v", decoded)
	}
}

func TestBuildOnboardManifestPreservesReviewedTargetsForEveryLegacyDeploymentKind(t *testing.T) {
	items := []OnboardItem{
		{ID: strings.Repeat("1", 64), Kind: "package", Name: "acme/package", Payload: []byte(`{"source":"acme/package"}`), Resolution: OnboardResolution{Decision: "migrate", ApprovedTargets: []string{"codex"}}},
		{ID: strings.Repeat("2", 64), Kind: "plugin", Name: "reviewer", Payload: []byte(`{"name":"reviewer","marketplace":"tools"}`), Resolution: OnboardResolution{Decision: "migrate", ApprovedTargets: []string{"claude", "codex"}}},
		{ID: strings.Repeat("3", 64), Kind: "mcp", Name: "api", Payload: []byte(`{"name":"api","transport":"stdio","command":"api-server"}`), Resolution: OnboardResolution{Decision: "migrate", ApprovedTargets: []string{"codex"}}},
		{ID: strings.Repeat("4", 64), Kind: "marketplace", Name: "tools", Payload: []byte(`{"name":"tools","source":"acme/tools"}`), Resolution: OnboardResolution{Decision: "migrate", ApprovedTargets: []string{"claude"}}},
	}
	got, markets, blockers, err := buildOnboardManifest(nil, items)
	if err != nil {
		t.Fatal(err)
	}
	if len(blockers) != 0 || len(markets) != 1 || markets[0].Name != "tools" || markets[0].Source != "acme/tools" {
		t.Fatalf("markets=%v blockers=%v", markets, blockers)
	}
	var decoded struct {
		Dependencies struct {
			APM []struct {
				Name, Git string
				Targets   []string
			} `yaml:"apm"`
			MCP []struct {
				Name    string
				Targets []string
			} `yaml:"mcp"`
		} `yaml:"dependencies"`
	}
	if err := yaml.Unmarshal(got, &decoded); err != nil {
		t.Fatal(err)
	}
	if len(decoded.Dependencies.APM) != 2 || decoded.Dependencies.APM[0].Git != "acme/package" || strings.Join(decoded.Dependencies.APM[0].Targets, ",") != "codex" || decoded.Dependencies.APM[1].Name != "reviewer" || strings.Join(decoded.Dependencies.APM[1].Targets, ",") != "claude,codex" {
		t.Fatalf("apm dependencies=%+v", decoded.Dependencies.APM)
	}
	if len(decoded.Dependencies.MCP) != 1 || decoded.Dependencies.MCP[0].Name != "api" || strings.Join(decoded.Dependencies.MCP[0].Targets, ",") != "codex" {
		t.Fatalf("mcp dependencies=%+v", decoded.Dependencies.MCP)
	}
}

func TestOnboardAPMEntryOmitsEmptyTargetSubset(t *testing.T) {
	entry, err := onboardAPMEntry(OnboardItem{ID: strings.Repeat("d", 64), Kind: "skill", Dots: &OnboardDotsRef{Native: true}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := entry["targets"]; ok {
		t.Fatalf("empty target subset emitted: %v", entry)
	}
}

func TestExtractNativeCandidatesUsesAPMDeployRoots(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	skill := filepath.Join(home, ".agents", "skills", "custom")
	if err := os.MkdirAll(skill, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte("# custom\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agents", "SKILL.md"), []byte("# root marker\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	candidates, preimages, err := extractNativeCandidates([]string{".agents", "../escape"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || len(preimages) != 1 || candidates[0].Name != "custom" || candidates[0].Kind != "skill" || candidates[0].Dots == nil || !candidates[0].Dots.Native || candidates[0].Dots.OwnerRoot != filepath.Dir(skill) {
		t.Fatalf("candidates=%#v preimages=%#v", candidates, preimages)
	}
	owned := []OnboardCandidate{{Dots: &OnboardDotsRef{TargetPath: skill}}}
	if candidates, _, err := extractNativeCandidates([]string{".agents"}, owned); err != nil || len(candidates) != 0 {
		t.Fatalf("owned candidates=%#v err=%v", candidates, err)
	}
}

func TestExtractNativeCandidatesSkipsClientOwnedTrees(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	root := filepath.Join(home, ".codex")
	writeResource := func(rel, marker string) string {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		markerPath := filepath.Join(path, filepath.FromSlash(marker))
		if err := os.MkdirAll(filepath.Dir(markerPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(markerPath, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	wantSkill := writeResource("skills/custom", "SKILL.md")
	wantPlugin := writeResource("plugins/custom", ".codex-plugin/plugin.json")
	writeResource("skills/.system/internal", "SKILL.md")
	writeResource(".tmp/generated", "SKILL.md")
	writeResource("plugins/cache/cached", ".codex-plugin/plugin.json")
	writeResource("plugins/marketplaces/catalog", ".codex-plugin/plugin.json")

	candidates, preimages, err := extractNativeCandidates([]string{".codex"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || len(preimages) != 2 {
		t.Fatalf("candidates=%#v preimages=%#v", candidates, preimages)
	}
	want := map[string]string{"native:" + wantSkill: "skill", "native:" + wantPlugin: "plugin"}
	for _, candidate := range candidates {
		if want[candidate.SourceHandle] != candidate.Kind {
			t.Fatalf("unexpected native candidate: %#v", candidate)
		}
		delete(want, candidate.SourceHandle)
	}
	if len(want) != 0 {
		t.Fatalf("missing custom candidates: %v", want)
	}
}

func TestExtractNativeCandidatesBlocksUnsafeResourceAndKeepsSibling(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink setup requires Unix semantics")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	safe := filepath.Join(home, ".claude", "skills", "safe")
	unsafe := filepath.Join(home, ".claude", "plugins", "unsafe")
	for _, dir := range []string{safe, filepath.Join(unsafe, ".claude-plugin")} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(safe, "SKILL.md"), []byte("safe"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unsafe, ".claude-plugin", "plugin.json"), []byte(`{"name":"unsafe"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(home, filepath.Join(unsafe, "escape")); err != nil {
		t.Fatal(err)
	}
	candidates, _, err := extractNativeCandidates([]string{".claude"}, nil)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("candidates=%#v err=%v", candidates, err)
	}
	blocked := false
	for _, candidate := range candidates {
		if candidate.Name == "unsafe" {
			blocked = strings.Contains(string(candidate.Payload), "unsafe-unowned-symlink")
		}
	}
	if !blocked {
		t.Fatalf("unsafe plugin was not item-blocked: %#v", candidates)
	}
}

func TestOnboardManifestHasOmniImports(t *testing.T) {
	if !onboardManifestHasOmniImports([]byte("dependencies:\n  apm:\n    - path: /tmp/.apm/omni-imports/abc\n")) || onboardManifestHasOmniImports([]byte("dependencies:\n  apm:\n    - path: /tmp/custom\n")) {
		t.Fatal("durable Omni ownership marker detection failed")
	}
}

func TestRemoveOnboardNativeSourceOnlyRemovesReviewedChild(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "custom")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeOnboardNativeSource(OnboardDotsRef{OwnerRoot: root, SourcePath: source, Native: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(source); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source still exists: %v", err)
	}
	if err := removeOnboardNativeSource(OnboardDotsRef{OwnerRoot: root, SourcePath: root, Native: true}); err == nil {
		t.Fatal("owner root removal accepted")
	}
}

func TestBuildOnboardManifestConflictingIdentityBlocks(t *testing.T) {
	original := []byte("name: demo\nversion: 1.0.0\ndependencies:\n  apm:\n    - git: https://example.test/pkg.git\n      ref: old\n  mcp: []\n")
	item := OnboardItem{ID: strings.Repeat("c", 64), Kind: "package", Name: "pkg", Payload: []byte(`{"source":"https://example.test/pkg.git","ref":"new"}`), Resolution: OnboardResolution{Decision: "migrate"}}
	got, _, blockers, err := buildOnboardManifest(original, []OnboardItem{item})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil || len(blockers) != 1 || !strings.Contains(blockers[0], "dependency-conflict") {
		t.Fatalf("got=%q blockers=%v", got, blockers)
	}
}

func TestAttachOnboardMergeBlockersMakesItemConflictResolvable(t *testing.T) {
	items := []OnboardItem{{ID: "one"}, {ID: "two"}}
	global := attachOnboardMergeBlockers(items, []string{"two:dependency-conflict", "ambiguous-existing-dependencies"})
	if !slices.Equal(items[1].Blockers, []string{"dependency-conflict"}) || !slices.Equal(global, []string{"ambiguous-existing-dependencies"}) {
		t.Fatalf("items=%#v global=%v", items, global)
	}
}

func TestManifestConflictAttachesToConflictingItemAndCanBeExcluded(t *testing.T) {
	items := []OnboardItem{
		{ID: "one", Kind: "package", Payload: []byte(`{"source":"https://example.test/pkg.git","ref":"one"}`), Resolution: OnboardResolution{Decision: "migrate"}},
		{ID: "two", Kind: "package", Payload: []byte(`{"source":"https://example.test/pkg.git","ref":"two"}`), Resolution: OnboardResolution{Decision: "migrate"}},
	}
	_, _, blockers, err := buildOnboardManifest(nil, items)
	if err != nil {
		t.Fatal(err)
	}
	if global := attachOnboardMergeBlockers(items, blockers); len(global) != 0 || len(items[0].Blockers) != 0 || !slices.Equal(items[1].Blockers, []string{"dependency-conflict"}) {
		t.Fatalf("items=%#v blockers=%v global=%v", items, blockers, global)
	}
	items[1].Resolution.Decision = "keep-unmanaged"
	if _, _, blockers, err := buildOnboardManifest(nil, items); err != nil || len(blockers) != 0 {
		t.Fatalf("excluded conflict remains: blockers=%v err=%v", blockers, err)
	}
}

func TestBuildOnboardManifestRejectsDuplicateYAML(t *testing.T) {
	item := OnboardItem{ID: strings.Repeat("a", 64), Kind: "package", Name: "pkg", Payload: []byte(`{"source":"https://example.test/new.git"}`), Resolution: OnboardResolution{Decision: "migrate"}}
	for _, manifest := range []string{
		"name: x\ndependencies: {apm: [], mcp: []}\ndependencies: {apm: [], mcp: []}\n",
		"name: x\ndependencies:\n  apm:\n    - git: https://example.test/pkg.git\n    - git: https://example.test/pkg.git\n  mcp: []\n",
	} {
		got, _, blockers, err := buildOnboardManifest([]byte(manifest), []OnboardItem{item})
		if err != nil {
			t.Fatal(err)
		}
		if got != nil || len(blockers) == 0 || !strings.Contains(strings.Join(blockers, ","), "duplicate") {
			t.Fatalf("got=%q blockers=%v", got, blockers)
		}
	}
}

func TestStageOnboardDotPackagesPreservesNativeRootsAndBlocksZeroDeployment(t *testing.T) {
	root, err := securefile.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	makeItem := func(kind, name string, files map[string]string) OnboardItem {
		source := t.TempDir()
		for rel, content := range files {
			path := filepath.Join(source, filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		fingerprint, err := onboardTreeFingerprint(source, source)
		if err != nil {
			t.Fatal(err)
		}
		return OnboardItem{ID: digest(kind, name, fingerprint), Kind: kind, Name: name, ContentFingerprint: fingerprint, Dots: &OnboardDotsRef{SourcePath: source}, Resolution: OnboardResolution{Decision: "move-to-apm"}}
	}
	items := []OnboardItem{
		makeItem("package", "pkg", map[string]string{"apm.yml": "name: pkg\nversion: 1.0.0\nincludes: auto\n", ".apm/agents/pkg.md": "# pkg"}),
		makeItem("plugin", "plug", map[string]string{".claude-plugin/plugin.json": `{"name":"plug","version":"1.0.0"}`, "commands/run.md": "run"}),
	}
	j := onboardJournal{ManifestPath: filepath.Join(t.TempDir(), "apm.yml")}
	if err := stageOnboardDotPackages(root, items, &j); err != nil {
		t.Fatal(err)
	}
	for _, item := range items {
		pkg := filepath.Join(root.Path(), "staging", item.ID)
		if _, err := os.Stat(filepath.Join(pkg, map[string]string{"package": "apm.yml", "plugin": ".claude-plugin/plugin.json"}[item.Kind])); err != nil {
			t.Fatalf("%s root not preserved: %v", item.Kind, err)
		}
		if _, err := os.Stat(filepath.Join(pkg, ".apm", item.Kind+"s")); !os.IsNotExist(err) {
			t.Fatalf("%s nested under unsupported path", item.Kind)
		}
	}
	zero := makeItem("package", "zero", map[string]string{"apm.yml": "name: zero\nversion: 1.0.0\n"})
	j.Packages = nil
	if err := stageOnboardDotPackages(root, []OnboardItem{zero}, &j); err == nil || !strings.Contains(err.Error(), "zero deployable") {
		t.Fatalf("zero package err=%v", err)
	}
	drift := makeItem("agent", "drift.md", map[string]string{"drift.md": "old"})
	if err := os.WriteFile(drift.Dots.SourcePath, []byte("changed"), 0o600); err == nil {
		t.Fatal("expected directory source")
	}
	if err := os.WriteFile(filepath.Join(drift.Dots.SourcePath, "drift.md"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := stageOnboardDotPackages(root, []OnboardItem{drift}, &j); err == nil || !strings.Contains(err.Error(), "changed before staging") {
		t.Fatalf("drift err=%v", err)
	}
}

func TestContinueOnboardOperationRejectsSourceDriftBeforeMaterialization(t *testing.T) {
	for _, mutate := range []struct {
		name string
		run  func(string) error
	}{
		{name: "content", run: func(root string) error {
			return os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("changed"), 0o600)
		}},
		{name: "mode", run: func(root string) error { return os.Chmod(filepath.Join(root, "SKILL.md"), 0o700) }},
		{name: "symlink-target", run: func(root string) error {
			if err := os.Remove(filepath.Join(root, "current")); err != nil {
				return err
			}
			return os.Symlink("b", filepath.Join(root, "current"))
		}},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			if mutate.name == "mode" && runtime.GOOS == "windows" {
				t.Skip("Windows does not preserve POSIX mode drift")
			}
			source := t.TempDir()
			for name, content := range map[string]string{"SKILL.md": "reviewed", "a": "a", "b": "b"} {
				if err := os.WriteFile(filepath.Join(source, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink("a", filepath.Join(source, "current")); err != nil {
				t.Fatal(err)
			}
			fingerprint, err := onboardTreeFingerprint(source, source)
			if err != nil {
				t.Fatal(err)
			}
			item := OnboardItem{ID: digest("drift", mutate.name), Kind: "skill", Name: "skill", ContentFingerprint: fingerprint, Dots: &OnboardDotsRef{SourcePath: source, TargetPath: filepath.Join(t.TempDir(), "skill")}, Resolution: OnboardResolution{Decision: "move-to-apm"}}
			if err := mutate.run(source); err != nil {
				t.Fatal(err)
			}
			root, err := securefile.NewRoot(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			journal := onboardJournal{Phase: "materializing-dots"}
			original := onboardingDotMaterializer
			calls := 0
			onboardingDotMaterializer = func(context.Context, *App, OnboardDotsRef) error { calls++; return nil }
			t.Cleanup(func() { onboardingDotMaterializer = original })
			err = (&App{}).continueOnboardOperation(t.Context(), root, OnboardPlan{OperationID: strings.Repeat("c", 32), Items: []OnboardItem{item}}, &journal, nil)
			var recovery *OnboardingRecoveryError
			if !errors.As(err, &recovery) || recovery.Cause == nil || !strings.Contains(recovery.Cause.Error(), "changed after review") || calls != 0 {
				t.Fatalf("err=%v materializer calls=%d", err, calls)
			}
		})
	}
}

func TestContinueOnboardOperationRejectsDriftedPendingMaterialization(t *testing.T) {
	root, err := securefile.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("d", 64)
	staged := filepath.Join(root.Path(), "staging", id)
	target := filepath.Join(t.TempDir(), "reviewer.md")
	item := OnboardItem{ID: id, Kind: "agent", Name: "reviewer.md", ContentFingerprint: digest("gone"), Dots: &OnboardDotsRef{SourcePath: filepath.Join(t.TempDir(), "removed"), TargetPath: target}, Resolution: OnboardResolution{Decision: "move-to-apm"}}
	resource := onboardPackageResourcePath(staged, item)
	if err := os.MkdirAll(filepath.Dir(resource), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resource, []byte("reviewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("tampered"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal := onboardJournal{Phase: "materializing-dots", PendingMaterializeItem: id, Packages: []journalPackage{{ItemID: id, StagedPath: staged}}}
	original := onboardingDotMaterializer
	calls := 0
	onboardingDotMaterializer = func(context.Context, *App, OnboardDotsRef) error { calls++; return nil }
	t.Cleanup(func() { onboardingDotMaterializer = original })
	err = (&App{}).continueOnboardOperation(t.Context(), root, OnboardPlan{OperationID: strings.Repeat("e", 32), Items: []OnboardItem{item}}, &journal, nil)
	if err == nil || calls != 0 || journal.PendingMaterializeItem != id {
		t.Fatalf("err=%v calls=%d pending=%s", err, calls, journal.PendingMaterializeItem)
	}
}

func TestPendingIdenticalTargetWithOwnedSourceStillRunsMaterializer(t *testing.T) {
	a, repo, home := newOnboardDotsTestApp(t, config.DotEntry{Name: "agents", Path: "~/.agents"})
	source := filepath.Join(repo, "dotfiles", "agents", ".agents", "agents", "reviewer.md")
	target := filepath.Join(home, ".agents", "agents", "reviewer.md")
	if err := os.MkdirAll(filepath.Dir(source), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("reviewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("reviewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("f", 64)
	staged := filepath.Join(t.TempDir(), id)
	item := OnboardItem{ID: id, Kind: "agent", Name: "reviewer.md", Dots: &OnboardDotsRef{Entry: "agents", Subpath: "agents/reviewer.md", SourcePath: source, TargetPath: target, OwnerRoot: filepath.Join(repo, "dotfiles")}, Resolution: OnboardResolution{Decision: "move-to-apm"}}
	fingerprint, err := onboardTreeFingerprint(source, source)
	if err != nil {
		t.Fatal(err)
	}
	item.ContentFingerprint = fingerprint
	resource := onboardPackageResourcePath(staged, item)
	if err := os.MkdirAll(filepath.Dir(resource), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resource, []byte("reviewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	root, err := securefile.NewRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	journal := onboardJournal{SchemaVersion: 1, OperationID: strings.Repeat("a", 32), PlanID: strings.Repeat("b", 64), ResolutionID: strings.Repeat("c", 64), CandidateSetID: strings.Repeat("d", 64), Phase: "materializing-dots", ManifestPath: filepath.Join(home, ".apm", "apm.yml"), PendingMaterializeItem: id, Packages: []journalPackage{{ItemID: id, StagedPath: staged}}}
	original := onboardingPhaseFailpoint
	onboardingPhaseFailpoint = func(phase string) error {
		if phase == "dots-materialized" {
			return errors.New("stop")
		}
		return nil
	}
	defer func() { onboardingPhaseFailpoint = original }()
	_ = a.continueOnboardOperation(t.Context(), root, OnboardPlan{OperationID: journal.OperationID, Items: []OnboardItem{item}}, &journal, nil)
	if !onboardDotOwnershipReleased(a.ConfigPath, *item.Dots) {
		t.Fatal("pending item accepted without releasing dots ownership")
	}
}

func TestCaptureOnboardPreimageRejectsSymlinkAndEncodesSecrets(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real")
	if err := os.WriteFile(real, []byte("SENTINEL_SECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"apm.yml", "marketplaces.json"} {
		link := filepath.Join(root, name)
		if err := os.Symlink(real, link); err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := captureOnboardOptionalFile(link); err == nil {
			t.Fatalf("symlinked %s preimage accepted", name)
		}
	}
	encoded := base64.StdEncoding.EncodeToString([]byte("SENTINEL_SECRET"))
	j := onboardJournal{ManifestData: encoded, MarketplaceData: encoded, Documents: []journalDocument{{Data: encoded}}}
	data, err := json.Marshal(j)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "SENTINEL_SECRET") {
		t.Fatalf("journal leaked secret: %s", data)
	}
}

func TestDeriveOnboardScrubEnvScansProposedAndMovedPlugin(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".mcp.json"), []byte(`{"env":{"PLUGIN_TOKEN":"${PLUGIN_TOKEN}"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := deriveOnboardScrubEnv(OnboardPlan{}, []byte("header: ${env:EXISTING_TOKEN}\n"), onboardJournal{Packages: []journalPackage{{ItemID: strings.Repeat("a", 64), StagedPath: root}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "EXISTING_TOKEN,PLUGIN_TOKEN" {
		t.Fatalf("scrub=%v", got)
	}
}

func TestValidateOnboardSourceAncestorsRejectsIntermediateSymlink(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := validateOnboardSourceAncestors(root, filepath.Join(root, "linked", "SKILL.md")); err == nil {
		t.Fatal("symlink ancestor accepted")
	}
	if data, err := os.ReadFile(filepath.Join(outside, "SKILL.md")); err != nil || string(data) != "outside" {
		t.Fatalf("outside mutated: %q %v", data, err)
	}
}

func TestMaterializedFingerprintIncludesModesAndEmptyDirectories(t *testing.T) {
	left, right := t.TempDir(), t.TempDir()
	for _, root := range []string{left, right} {
		if err := os.Mkdir(filepath.Join(root, "empty"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := onboardContentFingerprint(left)
	b, _ := onboardContentFingerprint(right)
	if a != b {
		t.Fatal("equal shapes differ")
	}
	if err := os.Chmod(filepath.Join(right, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ = onboardContentFingerprint(right)
	if a == b {
		t.Fatal("mode drift ignored")
	}
	if err := os.Remove(filepath.Join(right, "empty")); err != nil {
		t.Fatal(err)
	}
	b, _ = onboardContentFingerprint(right)
	if a == b {
		t.Fatal("empty directory drift ignored")
	}
}

func TestOnboardPlanJSONRedactsManifestAndRejectsInvalidItemID(t *testing.T) {
	plan := OnboardPlan{SchemaVersion: onboardSchemaVersion, ProposedManifest: "authorization: secret", Items: []OnboardItem{{ID: strings.Repeat("a", 64), Resolution: OnboardResolution{Decision: "move-to-apm"}}}}
	if err := bindOnboardPlan(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.Items[0].Resolution.Decision != "move-to-apm" {
		t.Fatal("binding erased reviewed resolution")
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "authorization") || strings.Contains(string(data), "proposed_manifest") {
		t.Fatalf("plan leaked manifest: %s", data)
	}
	plan.Items[0].ID = "../escape"
	plan.PlanID = ""
	if err := bindOnboardPlan(&plan); err == nil {
		t.Fatal("invalid item ID accepted")
	}
}

func TestValidateReviewedOnboardFreshBindsImmutableItemFields(t *testing.T) {
	id := strings.Repeat("a", 64)
	base := OnboardPlan{CandidateSetID: strings.Repeat("b", 64), PreimageSet: strings.Repeat("c", 64), Items: []OnboardItem{{ID: id, Kind: "skill", Name: "safe", Payload: []byte(`{"source":"safe"}`), ContentFingerprint: strings.Repeat("d", 64), Dots: &OnboardDotsRef{SourcePath: "/safe/source", TargetPath: "/safe/target"}}}}
	for name, mutate := range map[string]func(*OnboardItem){"name": func(i *OnboardItem) { i.Name = "changed" }, "payload": func(i *OnboardItem) { i.Payload = []byte(`{"source":"changed"}`) }, "source": func(i *OnboardItem) { i.Dots.SourcePath = "/escape" }, "target": func(i *OnboardItem) { i.Dots.TargetPath = "/escape" }} {
		t.Run(name, func(t *testing.T) {
			reviewed := base
			reviewed.Items = append([]OnboardItem(nil), base.Items...)
			item := reviewed.Items[0]
			dots := *item.Dots
			item.Dots = &dots
			reviewed.Items[0] = item
			mutate(&reviewed.Items[0])
			if err := validateReviewedOnboardFresh(reviewed, base); err == nil {
				t.Fatal("mutation accepted")
			}
		})
	}
}

func TestResolveOnboardLocalSourceExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, source := range []string{"~/pkg", "file://~/pkg"} {
		got, err := resolveOnboardLocalSource(source)
		if err != nil {
			t.Fatal(err)
		}
		if got != filepath.Join(home, "pkg") {
			t.Fatalf("%s -> %s", source, got)
		}
	}
}

func TestRestoreOnboardManifestPreservesExistingEmptyFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "apm.yml")
	proposal := []byte("name: proposed\n")
	if err := os.WriteFile(path, proposal, 0o640); err != nil {
		t.Fatal(err)
	}
	j := onboardJournal{ManifestPath: path, ManifestExisted: true, ManifestData: "", ManifestMode: 0o640, ProposedManifestHash: digestBytes(proposal)}
	if err := restoreOnboardManifest(j); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) != 0 || (runtime.GOOS != "windows" && info.Mode().Perm() != 0o640) {
		t.Fatalf("data=%q mode=%o", data, info.Mode().Perm())
	}
}

func TestAgentsOnboardRollbackDoesNotTouchPrewriteManifest(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "apm.yml")
	user := []byte("name: user-edit\n")
	if err := os.WriteFile(manifest, user, 0o600); err != nil {
		t.Fatal(err)
	}
	a := &App{StateDir: filepath.Join(root, "state")}
	state, err := onboardingRoot(a.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("a", 32)
	op, err := state.Child(id)
	if err != nil {
		t.Fatal(err)
	}
	j := onboardJournal{SchemaVersion: 1, OperationID: id, PlanID: strings.Repeat("b", 64), ResolutionID: strings.Repeat("c", 64), CandidateSetID: strings.Repeat("d", 64), Phase: "preflighted", ManifestPath: manifest, ManifestExisted: false, ManifestHash: digestBytes(nil)}
	if err := writeOnboardJournal(op, j); err != nil {
		t.Fatal(err)
	}
	if _, err := a.AgentsOnboardRollback(t.Context(), id); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(user) {
		t.Fatalf("manifest changed: %q", got)
	}
}

func TestReviewedOnboardEnvIncludesLegacyPlaceholders(t *testing.T) {
	plan := OnboardPlan{Items: []OnboardItem{{Resolution: OnboardResolution{Decision: "migrate", EnvBindings: map[string]string{"literal": "MAPPED"}}, Payload: []byte(`{"env":{"TOKEN":"${TOKEN}"},"headers":{"Authorization":"Bearer ${env:HEADER_TOKEN}"}}`)}}}
	got := reviewedOnboardEnv(plan)
	want := []string{"HEADER_TOKEN", "MAPPED", "TOKEN"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got=%v want=%v", got, want)
	}
}

func TestValidateOnboardResolutionKeysRejectsAmbiguityAndConflicts(t *testing.T) {
	plan := OnboardPlan{Items: []OnboardItem{{ID: strings.Repeat("a", 64), Name: "same"}, {ID: strings.Repeat("b", 64), Name: "same"}}}
	if err := validateOnboardResolutionKeys(plan, AgentsOnboardResolutions{Excluded: map[string]bool{"same": true}}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguity err=%v", err)
	}
	id := plan.Items[0].ID
	if err := validateOnboardResolutionKeys(plan, AgentsOnboardResolutions{Excluded: map[string]bool{id: true}, MoveToAPM: map[string]bool{id: true}}); err == nil || !strings.Contains(err.Error(), "conflicting") {
		t.Fatalf("conflict err=%v", err)
	}
}

func TestOnboardTreeFingerprintRejectsUnsafeLinksPerItem(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "inside.txt")
	if err := os.WriteFile(inside, []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("inside.txt", filepath.Join(root, "safe-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := onboardTreeFingerprint(root, root); err != nil {
		t.Fatalf("safe link: %v", err)
	}
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "unsafe-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := onboardTreeFingerprint(root, root); err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("unsafe err=%v", err)
	}
	if err := os.Remove(filepath.Join(root, "unsafe-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(root, "broken-link")); err != nil {
		t.Fatal(err)
	}
	if _, err := onboardTreeFingerprint(root, root); err == nil || !strings.Contains(err.Error(), "broken") {
		t.Fatalf("broken err=%v", err)
	}
}

func TestOnboardTreeFingerprintRejectsAncestorSymlinkCycle(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(".", filepath.Join(root, "loop")); err != nil {
		t.Fatal(err)
	}
	if _, err := onboardTreeFingerprint(root, root); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("cycle accepted: %v", err)
	}
}

func TestMaterializeOnboardDotMovesWholeStowEntryToRealFiles(t *testing.T) {
	a, repo, home := newOnboardDotsTestApp(t, config.DotEntry{Name: "skills", Path: "~/.agents/skills"})
	source := filepath.Join(repo, "dotfiles", "skills", ".agents", "skills")
	target := filepath.Join(home, ".agents", "skills")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}
	if err := a.materializeOnboardDot(t.Context(), OnboardDotsRef{Entry: "skills", Subpath: ".", SourcePath: source, TargetPath: target}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target was not materialized: info=%v err=%v", info, err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "SKILL.md")); err != nil || string(data) != "skill" {
		t.Fatalf("materialized content=%q err=%v", data, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("dots source remains: %v", err)
	}
}

func TestMaterializeOnboardDotMovesSubpathAndMaterializesNestedLinks(t *testing.T) {
	a, repo, home := newOnboardDotsTestApp(t, config.DotEntry{Name: "agents", Path: "~/.agents"})
	sourceRoot := filepath.Join(repo, "dotfiles", "agents", ".agents")
	source := filepath.Join(sourceRoot, "skills", "custom")
	targetRoot := filepath.Join(home, ".agents")
	target := filepath.Join(targetRoot, "skills", "custom")
	if err := os.MkdirAll(filepath.Join(source, "assets"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "assets", "shared.txt"), []byte("shared"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("assets/shared.txt", filepath.Join(source, "nested-link")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}
	if err := a.materializeOnboardDot(t.Context(), OnboardDotsRef{Entry: "agents", Subpath: "skills/custom", SourcePath: source, TargetPath: target}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(filepath.Join(target, "nested-link"))
	if err != nil || !info.Mode().IsRegular() {
		t.Fatalf("nested link was not materialized: info=%v err=%v", info, err)
	}
	if data, err := os.ReadFile(filepath.Join(target, "nested-link")); err != nil || string(data) != "shared" {
		t.Fatalf("nested content=%q err=%v", data, err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("dots subpath remains: %v", err)
	}
}

func TestAgentsOnboardApplyKeepInDotsDoesNotWriteDotsPaths(t *testing.T) {
	a, repo, home := newOnboardDotsTestApp(t, config.DotEntry{Name: "agents", Path: "~/.agents"})
	source := filepath.Join(repo, "dotfiles", "agents", ".agents", "skills", "custom")
	target := filepath.Join(home, ".agents", "skills", "custom")
	if err := os.MkdirAll(source, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "SKILL.md"), []byte("skill"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(source, target); err != nil {
		t.Fatal(err)
	}
	mock := executor.NewMatchMock(
		executor.MatchRule{Pattern: "apm --version", Response: executor.MockCall{Stdout: "Agent Package Manager (APM) CLI version 0.28.0+omni.6\n"}},
		executor.MatchRule{Pattern: "apm targets --json", Response: executor.MockCall{Stdout: `[{"target":"codex"}]`}},
	).WithFallback(executor.MockCall{})
	a.SetFallbackExecutor(mock)
	originalCheck := onboardingPinnedAPMCheck
	onboardingPinnedAPMCheck = func(context.Context, *App) error { return nil }
	t.Cleanup(func() { onboardingPinnedAPMCheck = originalCheck })
	beforeTarget, beforeRepo := snapshotOnboardTestTree(t, filepath.Join(home, ".agents")), snapshotOnboardTestTree(t, repo)
	result, err := a.AgentsOnboardPlan(t.Context(), AgentsOnboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Envelope.Plan == nil || len(result.Envelope.Plan.Items) != 1 {
		t.Fatalf("plan=%+v", result.Envelope.Plan)
	}
	result.Envelope.Plan.Items[0].Resolution.Decision = "keep-in-dots"
	if _, err := a.AgentsOnboardApplyReviewed(t.Context(), *result.Envelope.Plan); err != nil {
		t.Fatal(err)
	}
	if !maps.Equal(beforeTarget, snapshotOnboardTestTree(t, filepath.Join(home, ".agents"))) || !maps.Equal(beforeRepo, snapshotOnboardTestTree(t, repo)) {
		t.Fatal("keep-in-dots apply mutated target or dots repo")
	}
}

func TestOnboardCompletionMarkerSurvivesCleanupWithoutLocalImports(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	configPath := filepath.Join(home, ".config", "omni", "settings.json")
	if err := config.Save(configPath, &config.RootConfig{Version: config.CurrentVersion}); err != nil {
		t.Fatal(err)
	}
	native := filepath.Join(home, ".agents", "skills", "local")
	if err := os.MkdirAll(native, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(native, "SKILL.md"), []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}
	a := New(configPath)
	a.StateDir = filepath.Join(home, ".local", "state", "omni")
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	raw["agents"] = map[string]any{"skills": []any{map[string]any{"name": "remote", "source": "acme/remote", "agents": []string{"codex"}}}}
	data, _ = json.Marshal(raw)
	if err := os.WriteFile(configPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &availableMatchMock{executor.NewMatchMock(
		executor.MatchRule{Pattern: "apm targets --json --all", Response: executor.MockCall{Stdout: `[{"target":"codex","deploy_dir":".codex"},{"target":"agent-skills","deploy_dir":".agents","meta_target":true}]`}},
	).WithFallback(executor.MockCall{})}
	a.SetFallbackExecutor(mock)
	originalCheck := onboardingPinnedAPMCheck
	onboardingPinnedAPMCheck = func(context.Context, *App) error { return nil }
	t.Cleanup(func() { onboardingPinnedAPMCheck = originalCheck })
	planPath := filepath.Join(home, "reviewed-plan.json")
	result, err := a.AgentsOnboardPlan(t.Context(), AgentsOnboardOptions{PlanJSON: planPath})
	if err != nil || result.Envelope.Plan == nil || len(result.Envelope.Plan.Items) != 2 {
		t.Fatalf("plan=%#v err=%v", result.Envelope.Plan, err)
	}
	if _, err := a.AgentsOnboardApplyResolved(t.Context(), planPath, AgentsOnboardResolutions{KeepInDots: map[string]bool{"local": true}}); err == nil || !strings.Contains(err.Error(), "cannot be kept in dots") {
		t.Fatalf("native keep-in-dots error=%v", err)
	}
	if _, err := os.Stat(filepath.Join(a.StateDir, "onboarding")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected native decision created journal state: %v", err)
	}
	for i := range result.Envelope.Plan.Items {
		if result.Envelope.Plan.Items[i].Name == "local" {
			result.Envelope.Plan.Items[i].Resolution.Decision = "keep-unmanaged"
		}
	}
	applied, err := a.AgentsOnboardApplyReviewed(t.Context(), *result.Envelope.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.AgentsOnboardCleanup(t.Context(), applied.Envelope.OperationID, true); err != nil {
		t.Fatal(err)
	}
	marker, err := onboardCompletionMarkerPath()
	if err != nil || !regularOnboardFile(marker) {
		t.Fatalf("completion marker=%q err=%v", marker, err)
	}
	second, err := a.AgentsOnboardPlan(t.Context(), AgentsOnboardOptions{})
	if err != nil || second.Envelope.Plan == nil || len(second.Envelope.Plan.Items) != 0 {
		t.Fatalf("post-cleanup plan=%#v err=%v", second.Envelope.Plan, err)
	}
}

func TestExtractDotsCandidatesBlocksUnsafeNestedLinkWithoutAbortingSiblings(t *testing.T) {
	a, repo, _ := newOnboardDotsTestApp(t, config.DotEntry{Name: "agents", Path: "~/.agents"})
	root := filepath.Join(repo, "dotfiles", "agents", ".agents", "skills")
	for _, name := range []string{"safe", "unsafe"} {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "SKILL.md"), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "unsafe", "nested")); err != nil {
		t.Fatal(err)
	}
	candidates, _, err := a.extractDotsCandidates()
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 {
		t.Fatalf("candidates=%+v", candidates)
	}
	blocked := map[string]bool{}
	for _, candidate := range candidates {
		blocked[candidate.Name] = strings.Contains(string(candidate.Payload), `"blocker":"unsafe-unowned-symlink"`)
	}
	if blocked["safe"] || !blocked["unsafe"] {
		t.Fatalf("blocked=%v", blocked)
	}
}

func TestVerifyMaterializedOnboardItemRejectsPostCopyDrift(t *testing.T) {
	id := strings.Repeat("d", 64)
	staged := t.TempDir()
	target := filepath.Join(t.TempDir(), "reviewer.md")
	item := OnboardItem{ID: id, Kind: "agent", Name: "reviewer.md", Dots: &OnboardDotsRef{TargetPath: target}}
	resource := onboardPackageResourcePath(staged, item)
	if err := os.MkdirAll(filepath.Dir(resource), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(resource, []byte("reviewed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	err := verifyMaterializedOnboardItem(item, onboardJournal{Packages: []journalPackage{{ItemID: id, StagedPath: staged}}})
	if err == nil || !strings.Contains(err.Error(), "post-materialization content drift") {
		t.Fatalf("err=%v", err)
	}
}

func newOnboardDotsTestApp(t *testing.T, entry config.DotEntry) (*App, string, string) {
	t.Helper()
	home := t.TempDir()
	repo := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OMNI_HOSTNAME", "onboard-test")
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	configPath := filepath.Join(home, ".config", "omni", "settings.json")
	if err := config.Save(configPath, &config.RootConfig{
		Settings: config.Settings{DotsRepo: repo},
		Groups:   []*config.GroupConfig{{Name: "work", Dots: []config.DotEntry{entry}}},
	}); err != nil {
		t.Fatal(err)
	}
	a := New(configPath)
	a.StateDir = filepath.Join(home, ".local", "state", "omni")
	if err := a.InitTestMode(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a, repo, home
}

type availableMatchMock struct{ *executor.MatchMockExecutor }

func (*availableMatchMock) CommandAvailable(string) bool { return true }

func TestSplitOnboardCommand(t *testing.T) {
	got, err := splitOnboardCommand(`npx -y "package name" 'arg value'`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"npx", "-y", "package name", "arg value"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("got=%q", got)
	}
	if _, err := splitOnboardCommand(`npx "broken`); err == nil {
		t.Fatal("unterminated quote accepted")
	}
}
