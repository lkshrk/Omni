package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

type includeCycleOperation struct {
	name string
	run  func(string) error
}

var includeCycleOperations = []includeCycleOperation{
	{
		name: "load",
		run: func(path string) error {
			_, err := config.Load(path)
			return err
		},
	},
	{
		name: "routed write",
		run: func(path string) error {
			return config.PatchRawRouted(path, map[string]json.RawMessage{
				"settings": json.RawMessage(`{"auto_import":true}`),
			})
		},
	},
	{
		name: "tool source",
		run: func(path string) error {
			_, err := config.ToolSource(path, "jq")
			return err
		},
	},
}

func TestIncludeCyclesAreRejected(t *testing.T) {
	cycleFixtures := []struct {
		name  string
		setup func(*testing.T, string) string
	}{
		{
			name: "direct",
			setup: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "settings.json")
				writeObjectShapeFixture(t, path, `{"version":19,"$include":["settings.json"]}`)
				return path
			},
		},
		{
			name: "indirect",
			setup: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "settings.json")
				writeObjectShapeFixture(t, path, `{"version":19,"$include":["a.json"]}`)
				writeObjectShapeFixture(t, filepath.Join(dir, "a.json"), `{"$include":["settings.json"]}`)
				return path
			},
		},
		{
			name: "normalized parent",
			setup: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "settings.json")
				if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
					t.Fatal(err)
				}
				writeObjectShapeFixture(t, path, `{"version":19,"$include":["./sub/../settings.json"]}`)
				return path
			},
		},
		{
			name: "symlink alias",
			setup: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, "settings.json")
				writeObjectShapeFixture(t, path, `{"version":19,"$include":["alias.json"]}`)
				if err := os.Symlink(path, filepath.Join(dir, "alias.json")); err != nil {
					t.Fatal(err)
				}
				return path
			},
		},
	}

	for _, operation := range includeCycleOperations {
		for _, fixture := range cycleFixtures {
			t.Run(operation.name+"/"+fixture.name, func(t *testing.T) {
				dir := t.TempDir()
				path := fixture.setup(t, dir)
				before, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}

				err = operation.run(path)
				if err == nil || !strings.Contains(err.Error(), "include cycle") {
					t.Fatalf("error = %v, want descriptive include cycle", err)
				}
				if !strings.Contains(err.Error(), "settings.json") || !strings.Contains(err.Error(), " -> ") {
					t.Fatalf("error = %q, want cycle path chain", err)
				}
				after, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if string(after) != string(before) {
					t.Fatalf("cycle changed root config: got %q, want %q", after, before)
				}
			})
		}
	}
}

func TestAcyclicDiamondIncludesRemainValid(t *testing.T) {
	for _, operation := range includeCycleOperations {
		t.Run(operation.name, func(t *testing.T) {
			dir := t.TempDir()
			path := writeRoutedFixture(t, dir, map[string]string{
				"settings.json": `{"version":19,"$include":["a.json","b.json"],"settings":{}}`,
				"a.json":        `{"$include":["shared.json"]}`,
				"b.json":        `{"$include":["shared.json"]}`,
				"shared.json":   `{"tools":{"jq":{"providers":[{"provider":"brew"}]}}}`,
			})
			if err := operation.run(path); err != nil {
				t.Fatalf("acyclic diamond: %v", err)
			}
		})
	}
}
