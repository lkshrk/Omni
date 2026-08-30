package testflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/lkshrk/omni/internal/testguard"
)

func TestVerifyEvidenceAcceptsExactPassAndReportsGaps(t *testing.T) {
	root := t.TempDir()
	writeLane(t, root, evidenceMeta{SchemaVersion: 1, Lane: "unit-app", GOOS: "linux", Tags: []string{"race"}, Count: 1}, []goTestEvent{
		{Action: "run", Package: "example.test/app", Test: "TestFlow"},
		{Action: "pass", Package: "example.test/app", Test: "TestFlow"},
		{Action: "pass", Package: "example.test/app"},
	}, nil)
	catalog := evidenceCatalog(Selector{Package: "example.test/app", Test: "TestFlow", Lane: "unit-app", OS: []string{"linux", "macos"}, Tags: []string{"race"}})
	catalog.Flows[0].Requirements = append(catalog.Flows[0].Requirements, Requirement{Level: LevelParity, Status: StatusGap, Reason: "later", TargetStage: "Stage 6"})

	report, err := VerifyEvidence(catalog, root)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verified != 1 || len(report.Gaps) != 1 || report.Gaps[0].FlowID != "tools.list" {
		t.Fatalf("report = %+v", report)
	}
}

func TestVerifyEvidenceRequiresExactTestscriptSubtest(t *testing.T) {
	root := t.TempDir()
	writeLane(t, root, evidenceMeta{SchemaVersion: 1, Lane: "script-tests", GOOS: "linux", Tags: []string{"testscript"}, Count: 1}, []goTestEvent{
		{Action: "pass", Package: "example.test/integration_tests", Test: "TestCLI"},
		{Action: "pass", Package: "example.test/integration_tests"},
	}, nil)
	selector := Selector{Package: "example.test/integration_tests", Test: "TestCLI/tools-list", Fixture: "integration_tests/testdata/scripts/tools-list.txtar", Lane: "script-tests", OS: []string{"linux"}, Tags: []string{"testscript"}}

	_, err := VerifyEvidence(evidenceCatalog(selector), root)
	if err == nil || !strings.Contains(err.Error(), "TestCLI/tools-list has 0 terminal pass events") {
		t.Fatalf("VerifyEvidence() error = %v", err)
	}
}

func TestVerifyEvidenceAcceptsTypedOutputTypeAndStillRejectsUnknownEventFields(t *testing.T) {
	root := t.TempDir()
	meta := evidenceMeta{SchemaVersion: 1, Lane: "unit-app", GOOS: "linux", Tags: []string{}, Count: 1}
	writeLane(t, root, meta, []goTestEvent{
		{Action: "output", Package: "example.test/app", Test: "TestFlow", Output: "running\n", OutputType: "stdout"},
		{Action: "pass", Package: "example.test/app", Test: "TestFlow"},
		{Action: "pass", Package: "example.test/app"},
	}, nil)
	selector := Selector{Package: "example.test/app", Test: "TestFlow", Lane: "unit-app", OS: []string{"linux"}, Tags: []string{}}
	if _, err := VerifyEvidence(evidenceCatalog(selector), root); err != nil {
		t.Fatalf("typed OutputType rejected: %v", err)
	}

	writeFile(t, filepath.Join(root, "unit-app", evidenceEventsFile), []byte("{\"Action\":\"pass\",\"Package\":\"example.test/app\",\"UnknownType\":\"stdout\"}\n"))
	if _, err := VerifyEvidence(evidenceCatalog(selector), root); err == nil || !strings.Contains(err.Error(), `unknown field "UnknownType"`) {
		t.Fatalf("unknown event field error = %v", err)
	}
}

func TestVerifyEvidenceRejectsUntrustworthyGoOutput(t *testing.T) {
	tests := []struct {
		name   string
		events []goTestEvent
		raw    string
		want   string
	}{
		{name: "skip", events: []goTestEvent{{Action: "skip", Package: "example.test/app", Test: "TestFlow"}, {Action: "pass", Package: "example.test/app"}}, want: "ended with skip"},
		{name: "fail", events: []goTestEvent{{Action: "fail", Package: "example.test/app", Test: "TestFlow"}, {Action: "fail", Package: "example.test/app"}}, want: "ended with fail"},
		{name: "missing test", events: []goTestEvent{{Action: "pass", Package: "example.test/app"}}, want: "0 terminal pass events"},
		{name: "missing package terminal", events: []goTestEvent{{Action: "pass", Package: "example.test/app", Test: "TestFlow"}}, want: "package example.test/app has 0 terminal"},
		{name: "duplicate test terminal", events: []goTestEvent{{Action: "pass", Package: "example.test/app", Test: "TestFlow"}, {Action: "pass", Package: "example.test/app", Test: "TestFlow"}, {Action: "pass", Package: "example.test/app"}}, want: "2 terminal pass events"},
		{name: "cached", events: []goTestEvent{{Action: "output", Package: "example.test/app", Output: "ok\t(cached)\n"}, {Action: "pass", Package: "example.test/app", Test: "TestFlow"}, {Action: "pass", Package: "example.test/app"}}, want: "cached test output"},
		{name: "truncated", raw: `{"Action":"pass","Package":"example.test/app"`, want: "EOF"},
	}
	selector := Selector{Package: "example.test/app", Test: "TestFlow", Lane: "unit-app", OS: []string{"linux"}, Tags: []string{}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeLane(t, root, evidenceMeta{SchemaVersion: 1, Lane: "unit-app", GOOS: "linux", Tags: []string{}, Count: 1}, test.events, nil)
			if test.raw != "" {
				writeFile(t, filepath.Join(root, "unit-app", evidenceEventsFile), []byte(test.raw))
			}
			_, err := VerifyEvidence(evidenceCatalog(selector), root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyEvidence() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyEvidenceRejectsSelectorMetadataMismatch(t *testing.T) {
	tests := []struct {
		name     string
		selector Selector
		want     string
	}{
		{name: "missing lane", selector: Selector{Package: "example.test/app", Test: "TestFlow", Lane: "unit-cli", OS: []string{"linux"}, Tags: []string{"race"}}, want: `missing evidence lane "unit-cli"`},
		{name: "goos", selector: Selector{Package: "example.test/app", Test: "TestFlow", Lane: "unit-app", OS: []string{"macos"}, Tags: []string{"race"}}, want: "does not allow goos"},
		{name: "tags", selector: Selector{Package: "example.test/app", Test: "TestFlow", Lane: "unit-app", OS: []string{"linux"}, Tags: []string{}}, want: "do not match lane tags"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeLane(t, root, evidenceMeta{SchemaVersion: 1, Lane: "unit-app", GOOS: "linux", Tags: []string{"race"}, Count: 1}, passingEvents(), nil)
			_, err := VerifyEvidence(evidenceCatalog(test.selector), root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyEvidence() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyEvidenceRejectsBadLaneMetadata(t *testing.T) {
	tests := []struct {
		name string
		dir  string
		meta evidenceMeta
		want string
	}{
		{name: "count", dir: "unit-app", meta: evidenceMeta{SchemaVersion: 1, Lane: "unit-app", GOOS: "linux", Tags: []string{}, Count: 2}, want: "invalid metadata"},
		{name: "unknown schema", dir: "unit-app", meta: evidenceMeta{SchemaVersion: 2, Lane: "unit-app", GOOS: "linux", Tags: []string{}, Count: 1}, want: "invalid metadata"},
		{name: "directory mismatch", dir: "unit-app", meta: evidenceMeta{SchemaVersion: 1, Lane: "unit-cli", GOOS: "linux", Tags: []string{}, Count: 1}, want: "disagrees with metadata"},
		{name: "unknown lane", dir: "unit-unknown", meta: evidenceMeta{SchemaVersion: 1, Lane: "unit-unknown", GOOS: "linux", Tags: []string{}, Count: 1}, want: "unknown lane or goos"},
		{name: "unknown goos", dir: "unit-app", meta: evidenceMeta{SchemaVersion: 1, Lane: "unit-app", GOOS: "plan9", Tags: []string{}, Count: 1}, want: "unknown lane or goos"},
		{name: "duplicate tag", dir: "unit-app", meta: evidenceMeta{SchemaVersion: 1, Lane: "unit-app", GOOS: "linux", Tags: []string{"race", "race"}, Count: 1}, want: "unknown or duplicate tag"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			writeLaneNamed(t, root, test.dir, test.meta, passingEvents(), nil)
			_, err := VerifyEvidence(Catalog{}, root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyEvidence() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyEvidenceRejectsInvalidRequiredCatalogEntries(t *testing.T) {
	root := t.TempDir()
	writeLane(t, root, evidenceMeta{SchemaVersion: 1, Lane: "unit-app", GOOS: "linux", Tags: []string{}, Count: 1}, passingEvents(), nil)
	tests := []struct {
		name        string
		requirement Requirement
		want        string
	}{
		{name: "unknown status", requirement: Requirement{Level: LevelUnit, Status: "maybe"}, want: "unknown requirement status"},
		{name: "no references", requirement: Requirement{Level: LevelUnit, Status: StatusRequired}, want: "has no references"},
		{name: "wrong type", requirement: Requirement{Level: LevelUnit, Status: StatusRequired, Evidence: []Evidence{{Type: LevelIntegration, Role: EvidencePrimary, Selector: Selector{Lane: "unit-app"}}}}, want: "does not match"},
		{name: "unknown role", requirement: Requirement{Level: LevelUnit, Status: StatusRequired, Evidence: []Evidence{{Type: LevelUnit, Role: "unknown"}}}, want: "unknown evidence role"},
		{name: "regression without reference", requirement: Requirement{Level: LevelUnit, Status: StatusRequired, Evidence: []Evidence{{Type: LevelUnit, Role: EvidenceRegression}}}, want: "has no reference"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			catalog := Catalog{Flows: []Flow{{ID: "tools.list", Requirements: []Requirement{test.requirement}}}}
			_, err := VerifyEvidence(catalog, root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyEvidence() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyEvidenceRejectsUnknownOrDuplicateJSONFields(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "unknown", body: `{"schema_version":1,"lane":"unit-app","goos":"linux","tags":[],"count":1,"extra":true}`, want: "unknown field"},
		{name: "duplicate", body: `{"schema_version":1,"lane":"unit-app","lane":"unit-cli","goos":"linux","tags":[],"count":1}`, want: "duplicate key"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "unit-app")
			if err := os.Mkdir(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(dir, "meta.json"), []byte(test.body))
			writeFile(t, filepath.Join(dir, evidenceEventsFile), []byte(`{"Action":"pass","Package":"example.test/app"}`+"\n"))
			_, err := VerifyEvidence(Catalog{}, root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyEvidence() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestVerifyEvidenceRequiresPassingContainerGate(t *testing.T) {
	valid := gateReceipt{SchemaVersion: 1, Kind: "container_gate", Lane: "docker-apm", GOOS: "linux", ImageRef: "omni-test:local", ImageID: "sha256:image", BinarySHA256: strings.Repeat("a", 64), CommandID: "docker-apm", ExitCode: 0, Status: "pass", Events: evidenceEventsFile}
	tests := []struct {
		name   string
		mutate func(*gateReceipt)
		want   string
	}{
		{name: "failed", mutate: func(g *gateReceipt) { g.ExitCode, g.Status = 1, "fail" }, want: "did not pass"},
		{name: "wrong lane", mutate: func(g *gateReceipt) { g.Lane = "docker-cli-ad" }, want: "does not match metadata"},
		{name: "missing identity", mutate: func(g *gateReceipt) { g.ImageID = "" }, want: "identity fields"},
		{name: "unknown events", mutate: func(g *gateReceipt) { g.Events = "other.jsonl" }, want: "unknown events reference"},
	}
	selector := Selector{Package: "example.test/app", Test: "TestFlow", Lane: "docker-apm", OS: []string{"linux"}, Tags: []string{"docker", "integration"}}
	t.Run("missing", func(t *testing.T) {
		root := t.TempDir()
		writeLane(t, root, evidenceMeta{SchemaVersion: 1, Lane: "docker-apm", GOOS: "linux", Tags: []string{"docker", "integration"}, Count: 1}, passingEvents(), nil)
		_, err := VerifyEvidence(evidenceCatalog(selector), root)
		if err == nil || !strings.Contains(err.Error(), "gate.json") {
			t.Fatalf("VerifyEvidence() error = %v, want missing gate", err)
		}
	})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			gate := valid
			test.mutate(&gate)
			writeLane(t, root, evidenceMeta{SchemaVersion: 1, Lane: "docker-apm", GOOS: "linux", Tags: []string{"docker", "integration"}, Count: 1}, passingEvents(), &gate)
			_, err := VerifyEvidence(evidenceCatalog(selector), root)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("VerifyEvidence() error = %v, want %q", err, test.want)
			}
		})
	}
	root := t.TempDir()
	writeLane(t, root, evidenceMeta{SchemaVersion: 1, Lane: "docker-apm", GOOS: "linux", Tags: []string{"docker", "integration"}, Count: 1}, passingEvents(), &valid)
	if _, err := VerifyEvidence(evidenceCatalog(selector), root); err != nil {
		t.Fatalf("valid container evidence: %v", err)
	}
}

func evidenceCatalog(selector Selector) Catalog {
	return Catalog{SchemaVersion: SchemaVersion, Flows: []Flow{{
		ID: "tools.list", Requirements: []Requirement{{Level: LevelUnit, Status: StatusRequired, Evidence: []Evidence{{Type: LevelUnit, Role: EvidencePrimary, Selector: selector}}}},
	}}}
}

func passingEvents() []goTestEvent {
	return []goTestEvent{{Action: "pass", Package: "example.test/app", Test: "TestFlow"}, {Action: "pass", Package: "example.test/app"}}
}

func writeLane(t *testing.T, root string, meta evidenceMeta, events []goTestEvent, gate *gateReceipt) {
	t.Helper()
	writeLaneNamed(t, root, meta.Lane, meta, events, gate)
}

func writeLaneNamed(t *testing.T, root, name string, meta evidenceMeta, events []goTestEvent, gate *gateReceipt) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	writeJSON(t, filepath.Join(dir, "meta.json"), meta)
	var body []byte
	for _, event := range events {
		line, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		body = append(body, append(line, '\n')...)
	}
	writeFile(t, filepath.Join(dir, evidenceEventsFile), body)
	if gate != nil {
		writeJSON(t, filepath.Join(dir, "gate.json"), gate)
	}
}

func writeJSON(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, body)
}

func writeFile(t *testing.T, path string, body []byte) {
	t.Helper()
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}
