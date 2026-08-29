package main

import (
	"os"
	"path/filepath"
	"testing"

	_ "github.com/lkshrk/omni/internal/testguard"
)

func TestRunRejectsMissingEvidenceDirectory(t *testing.T) {
	catalog := filepath.Join(t.TempDir(), "flows.json")
	if err := os.WriteFile(catalog, []byte(`{"schema_version":1,"flows":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := run(catalog, filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("run() succeeded with missing evidence directory")
	}
}

func TestRunAcceptsCatalogWithDeclaredGap(t *testing.T) {
	root := t.TempDir()
	catalog := filepath.Join(root, "flows.json")
	body := `{"schema_version":1,"flows":[{"id":"tui.startup","capability":"tui","title":"startup","criticality":"high","criticality_reason":"startup","requirements":[{"level":"component","status":"gap","reason":"pending","target_stage":"Stage 3"}]}]}`
	if err := os.WriteFile(catalog, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(root, "evidence")
	if err := os.Mkdir(evidence, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := run(catalog, evidence); err != nil {
		t.Fatalf("run(): %v", err)
	}
}
