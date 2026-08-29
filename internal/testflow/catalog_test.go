package testflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/lkshrk/omni/internal/testguard"
)

func TestLoadRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flows.json")
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"evidence_types":[],"flows":[],"extra":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("Load() error = %v, want unknown field", err)
	}
}

func TestLoadRejectsDuplicateKeysAtAnyDepth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flows.json")
	body := `{"schema_version":1,"evidence_types":[],"flows":[{"id":"tools.install","requirements":[{"reason":"first","reason":"second"}]}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), `duplicate key "reason"`) {
		t.Fatalf("Load() error = %v, want nested duplicate key", err)
	}
}

func TestLoadRejectsCatalogAuthoredEvidenceInheritance(t *testing.T) {
	path := filepath.Join(t.TempDir(), "flows.json")
	body := `{"schema_version":1,"evidence_types":[{"id":"integration","inherits":["unit"]}],"flows":[]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), `unknown field "evidence_types"`) {
		t.Fatalf("Load() error = %v, want evidence_types rejection", err)
	}
}

func TestValidateChecksActionsEvidenceAndParity(t *testing.T) {
	root := testModule(t)
	dir := filepath.Join(root, "internal", "sample")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte("package sample\nimport \"testing\"\nfunc TestFlow(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flow := Flow{
		ID: "tools.install", Capability: "tools", Title: "Install a tool",
		Criticality: CriticalityHigh, CriticalityReason: "mutates installed software",
		ActionIDs: []string{"tools.install"},
		Parity:    &Parity{SemanticState: "installed tool", Rationale: "compare provider and version"},
		Requirements: []Requirement{
			{Level: LevelCLIBlackBox, Status: StatusGap, Reason: "CLI pending", TargetStage: "Stage 5"},
			{Level: LevelTUIBlackBox, Status: StatusGap, Reason: "TUI pending", TargetStage: "Stage 5"},
			{Level: LevelParity, Status: StatusRequired, Evidence: []Evidence{{
				Type: LevelParity, Role: EvidencePrimary,
				Selector: Selector{Package: "example.test/internal/sample", Test: "TestFlow", Tags: []string{}, Lane: "unit-remaining", OS: []string{"linux"}},
			}}},
		},
	}
	if err := Validate(validCatalog(flow), []ActionSurface{{ID: "tools.install", CLI: true, CLICommands: []CLICommandSurface{{Command: []string{"tools", "install"}}}, TUI: true, Mutates: true}}, root); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRejectsDuplicateActionAndMissingReference(t *testing.T) {
	flow := Flow{
		ID: "tools.install", Capability: "tools", Title: "Install",
		Criticality: CriticalityHigh, CriticalityReason: "mutates software", ActionIDs: []string{"tools.install"},
		Requirements: []Requirement{
			{Level: LevelCLIBlackBox, Status: StatusGap, Reason: "pending", TargetStage: "Stage 5"},
			{Level: LevelIntegration, Status: StatusRequired, Evidence: []Evidence{{
				Type: LevelIntegration, Role: EvidencePrimary,
				Selector: Selector{Package: "example.test/internal/missing", Test: "TestMissing", Tags: []string{}, Lane: "unit-remaining", OS: []string{"linux"}},
			}}},
		},
	}
	duplicate := Flow{
		ID: "tools.install_again", Capability: "tools", Title: "Install again",
		Criticality: CriticalityLow, CriticalityReason: "duplicate guard", ActionIDs: []string{"tools.install"},
		Requirements: []Requirement{{Level: LevelCLIBlackBox, Status: StatusGap, Reason: "not linked", TargetStage: "Stage 5"}},
	}
	err := Validate(validCatalog(flow, duplicate), []ActionSurface{{ID: "tools.install", CLI: true}}, testModule(t))
	if err == nil || !strings.Contains(err.Error(), "maps to both") || !strings.Contains(err.Error(), "missing Go test") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRequiresAuthoredNonActionSurfaces(t *testing.T) {
	flow := Flow{
		ID: "tui.startup", Capability: "tui", Title: "Start",
		Criticality: CriticalityCritical, CriticalityReason: "entry point",
		Requirements: []Requirement{{Level: LevelTUIBlackBox, Status: StatusGap, Reason: "PTY pending", TargetStage: "Stage 5"}},
	}
	err := Validate(validCatalog(flow), nil, testModule(t))
	if err == nil || !strings.Contains(err.Error(), "must author both surfaces") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateParityRequiresExactlyOneMode(t *testing.T) {
	yes := true
	flow := Flow{
		ID: "tools.list", Capability: "tools", Title: "List tools",
		Criticality: CriticalityHigh, CriticalityReason: "inventory",
		Surfaces: Surfaces{CLI: &yes, TUI: &yes},
		Parity:   &Parity{SemanticState: "tools", SemanticQuery: "tools"},
		Requirements: []Requirement{
			{Level: LevelCLIBlackBox, Status: StatusGap, Reason: "pending", TargetStage: "Stage 5"},
			{Level: LevelTUIBlackBox, Status: StatusGap, Reason: "pending", TargetStage: "Stage 5"},
			{Level: LevelParity, Status: StatusGap, Reason: "pending", TargetStage: "Stage 6"},
		},
	}
	err := Validate(validCatalog(flow), nil, testModule(t))
	if err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateTestscriptSelectorMatchesExecutableFixture(t *testing.T) {
	root := testModule(t)
	dir := filepath.Join(root, "integration_tests")
	if err := os.MkdirAll(filepath.Join(dir, "testdata", "scripts"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cli_test.go"), []byte("package integration_test\nimport \"testing\"\nfunc TestCLI(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "testdata", "scripts", "list.txtar")
	if err := os.WriteFile(fixture, []byte("exec echo not-omni\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	flow := Flow{
		ID: "tools.list", Capability: "tools", Title: "List tools",
		Criticality: CriticalityHigh, CriticalityReason: "inventory",
		Surfaces: Surfaces{CLI: boolPtr(true), TUI: boolPtr(false)},
		Requirements: []Requirement{
			{Level: LevelIntegration, Status: StatusRequired, Evidence: []Evidence{{
				Type: LevelIntegration, Role: EvidencePrimary,
				Selector: Selector{Package: "example.test/integration_tests", Test: "TestCLI/list", Fixture: "integration_tests/testdata/scripts/list.txtar", Tags: []string{"testscript"}, Lane: "integration", OS: []string{"linux"}},
			}}},
			{Level: LevelCLIBlackBox, Status: StatusGap, Reason: "binary pending", TargetStage: "Stage 5"},
		},
	}
	err := Validate(validCatalog(flow), nil, root)
	if err == nil || !strings.Contains(err.Error(), "does not execute omni") {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestValidateRejectsDuplicateNonActionCLICommands(t *testing.T) {
	first := cliOnlyFlow("tools.list", []string{"tools", "list"})
	second := cliOnlyFlow("tools.inventory", []string{"tools", "list"})
	err := Validate(validCatalog(first, second), nil, testModule(t))
	if err == nil || !strings.Contains(err.Error(), "maps to both non-action flows") {
		t.Fatalf("Validate() error = %v, want duplicate CLI command", err)
	}
}

func TestValidateRejectsNonActionCommandContradictingAction(t *testing.T) {
	nonAction := cliOnlyFlow("agents.outdated", []string{"agents", "outdated"})
	action := ActionSurface{ID: "agents.refresh", CLI: true, CLICommands: []CLICommandSurface{{Command: []string{"agents", "outdated"}}}, TUI: true}
	err := Validate(validCatalog(nonAction, actionFlow("agents.refresh")), []ActionSurface{action}, testModule(t))
	if err == nil || !strings.Contains(err.Error(), "contradicts action-backed flow ownership") {
		t.Fatalf("Validate() error = %v, want action/non-action contradiction", err)
	}
}

func TestValidateRejectsContradictoryActionCLISurface(t *testing.T) {
	action := ActionSurface{ID: "tools.refresh", CLI: true}
	err := Validate(validCatalog(actionCLIOnlyFlow("tools.refresh")), []ActionSurface{action}, testModule(t))
	if err == nil || !strings.Contains(err.Error(), "contradictory CLI surface") {
		t.Fatalf("Validate() error = %v, want contradictory action CLI surface", err)
	}
}

func TestResolveCLICommandPreservesActionVariants(t *testing.T) {
	actions := []ActionSurface{
		{ID: "tools.update", CLI: true, CLICommands: []CLICommandSurface{{Command: []string{"tools", "upgrade"}}}},
		{ID: "tools.update_all", CLI: true, CLICommands: []CLICommandSurface{{Command: []string{"tools", "upgrade"}, RequiredFlags: []string{"--all"}}}},
	}
	catalog := validCatalog(actionCLIOnlyFlow("tools.update"), actionCLIOnlyFlow("tools.update_all"))
	if err := Validate(catalog, actions, testModule(t)); err != nil {
		t.Fatal(err)
	}
	owners := ResolveCLICommand(catalog, actions, []string{"tools", "upgrade"})
	if len(owners) != 2 || owners[0].ActionID != "tools.update" || len(owners[0].RequiredFlags) != 0 || owners[1].ActionID != "tools.update_all" || strings.Join(owners[1].RequiredFlags, " ") != "--all" {
		t.Fatalf("ResolveCLICommand() = %+v", owners)
	}
}

func TestValidateRejectsDuplicateActionVariants(t *testing.T) {
	actions := []ActionSurface{
		{ID: "tools.update", CLI: true, CLICommands: []CLICommandSurface{{Command: []string{"tools", "upgrade"}}}},
		{ID: "tools.update_all", CLI: true, CLICommands: []CLICommandSurface{{Command: []string{"tools", "upgrade"}}}},
	}
	err := Validate(validCatalog(actionCLIOnlyFlow("tools.update"), actionCLIOnlyFlow("tools.update_all")), actions, testModule(t))
	if err == nil || !strings.Contains(err.Error(), "belongs to both actions") {
		t.Fatalf("Validate() error = %v, want duplicate action variant", err)
	}
}

func TestValidateRejectsInheritedEvidenceAndUnverifiableSelectors(t *testing.T) {
	root := testModule(t)
	dir := filepath.Join(root, "internal", "sample")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte("package sample\nimport \"testing\"\nfunc TestFlow(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base := Selector{Package: "example.test/internal/sample", Test: "TestFlow", Tags: []string{}, Lane: "unit-remaining", OS: []string{"linux"}}
	tests := []struct {
		name     string
		typeName Level
		selector Selector
		want     string
	}{
		{name: "inherited level", typeName: LevelCLIBlackBox, selector: base, want: "must exactly match"},
		{name: "subtest", typeName: LevelIntegration, selector: withTest(base, "TestFlow/sub"), want: "unverifiable subtest"},
		{name: "lane", typeName: LevelIntegration, selector: withLane(base, "integration"), want: "not a current CI lane"},
		{name: "os", typeName: LevelIntegration, selector: withOS(base, "freebsd"), want: "OS \"freebsd\" is not supported"},
		{name: "tag", typeName: LevelIntegration, selector: withTag(base, "slow"), want: "tag \"slow\" is not supported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			flow := cliOnlyFlow("tools.list", []string{"tools", "list"})
			flow.Requirements = append([]Requirement{{Level: LevelIntegration, Status: StatusRequired, Evidence: []Evidence{{Type: test.typeName, Role: EvidencePrimary, Selector: test.selector}}}}, flow.Requirements...)
			err := Validate(validCatalog(flow), nil, root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateEvidenceRoles(t *testing.T) {
	root := testModule(t)
	dir := filepath.Join(root, "internal", "sample")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sample_test.go"), []byte("package sample\nimport \"testing\"\nfunc TestFlow(t *testing.T) {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	selector := Selector{Package: "example.test/internal/sample", Test: "TestFlow", Tags: []string{}, Lane: "unit-remaining", OS: []string{"linux"}}
	for _, test := range []struct {
		name      string
		role      EvidenceRole
		reference string
		want      string
	}{
		{name: "unknown", role: "historical", want: "unknown evidence role"},
		{name: "regression without reference", role: EvidenceRegression, want: "regression evidence requires reference"},
		{name: "regression with reference", role: EvidenceRegression, reference: "Fixes prior orphan classification regression"},
	} {
		t.Run(test.name, func(t *testing.T) {
			flow := cliOnlyFlow("tools.list", []string{"tools", "list"})
			flow.Requirements = append([]Requirement{{Level: LevelIntegration, Status: StatusRequired, Evidence: []Evidence{{Type: LevelIntegration, Role: test.role, Reference: test.reference, Selector: selector}}}}, flow.Requirements...)
			err := Validate(validCatalog(flow), nil, root)
			if test.want == "" && err != nil {
				t.Fatal(err)
			}
			if test.want != "" && (err == nil || !strings.Contains(err.Error(), test.want)) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestValidateParityModeMatchesMutability(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutates bool
		parity  *Parity
		want    string
	}{
		{name: "mutable query", mutates: true, parity: &Parity{SemanticQuery: "query"}, want: "requires parity semantic_state"},
		{name: "read state", parity: &Parity{SemanticState: "state"}, want: "requires parity semantic_query"},
	} {
		t.Run(test.name, func(t *testing.T) {
			flow := Flow{
				ID: "tools.refresh", Capability: "tools", Title: "Refresh tools",
				Criticality: CriticalityHigh, CriticalityReason: "inventory", ActionIDs: []string{"tools.refresh"}, Parity: test.parity,
				Requirements: []Requirement{
					{Level: LevelCLIBlackBox, Status: StatusGap, Reason: "pending", TargetStage: "Stage 5"},
					{Level: LevelTUIBlackBox, Status: StatusGap, Reason: "pending", TargetStage: "Stage 5"},
					{Level: LevelParity, Status: StatusGap, Reason: "pending", TargetStage: "Stage 6"},
				},
			}
			err := Validate(validCatalog(flow), []ActionSurface{{ID: "tools.refresh", CLI: true, TUI: true, Mutates: test.mutates}}, testModule(t))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Validate() error = %v, want %q", err, test.want)
			}
		})
	}
}

func validCatalog(flows ...Flow) Catalog {
	return Catalog{SchemaVersion: SchemaVersion, Flows: flows}
}

func testModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func boolPtr(value bool) *bool { return &value }

func cliOnlyFlow(id string, command []string) Flow {
	return Flow{
		ID: id, Capability: "tools", Title: id,
		Criticality: CriticalityMedium, CriticalityReason: "command coverage",
		Surfaces: Surfaces{CLI: boolPtr(true), TUI: boolPtr(false)}, Mutates: boolPtr(false), CLICommands: [][]string{command},
		Requirements: []Requirement{{Level: LevelCLIBlackBox, Status: StatusGap, Reason: "pending", TargetStage: "Stage 5"}},
	}
}

func actionFlow(id string) Flow {
	return Flow{
		ID: id, Capability: "agents", Title: id,
		Criticality: CriticalityMedium, CriticalityReason: "action coverage", ActionIDs: []string{id},
		Parity: &Parity{SemanticQuery: "normalized dependency update query"},
		Requirements: []Requirement{
			{Level: LevelCLIBlackBox, Status: StatusGap, Reason: "pending", TargetStage: "Stage 5"},
			{Level: LevelTUIBlackBox, Status: StatusGap, Reason: "pending", TargetStage: "Stage 5"},
			{Level: LevelParity, Status: StatusGap, Reason: "pending", TargetStage: "Stage 6"},
		},
	}
}

func actionCLIOnlyFlow(id string) Flow {
	return Flow{
		ID: id, Capability: "tools", Title: id,
		Criticality: CriticalityHigh, CriticalityReason: "tool lifecycle", ActionIDs: []string{id},
		Requirements: []Requirement{{Level: LevelCLIBlackBox, Status: StatusGap, Reason: "pending", TargetStage: "Stage 5"}},
	}
}

func withTest(selector Selector, test string) Selector { selector.Test = test; return selector }
func withLane(selector Selector, lane string) Selector { selector.Lane = lane; return selector }
func withOS(selector Selector, osName string) Selector {
	selector.OS = []string{osName}
	return selector
}
func withTag(selector Selector, tag string) Selector { selector.Tags = []string{tag}; return selector }
