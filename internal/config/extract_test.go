package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

const monolithicSettings = `{
  "version": 17,
  "settings": { "auto_import": true, "dots_repo": "/tmp/repo" },
  "hosts": { "laptop": ["core"] },
  "agents": {
    "packages": [{ "source": "acme/skills" }]
  },
  "tools": {
    "jq": { "providers": [{ "provider": "brew", "package": "jq" }] }
  },
  "groups": [
    {
      "name": "core",
      "tools": ["jq"],
      "dots": [
        { "name": "vim", "path": "~/.vim" },
        { "name": "zshrc", "path": "~/.zshrc", "ignore": ["*.zwc"] }
      ]
    },
    { "name": "laptop", "special": "host" }
  ]
}`

func TestExtractIncludeFragments_DecomposesMonolithicConfig(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(mainPath, []byte(monolithicSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load before: %v", err)
	}

	report, err := config.ExtractIncludeFragments(mainPath)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if report.Unchanged {
		t.Fatal("expected extraction to change layout")
	}

	mainRaw := rawKeys(t, mainPath)
	for _, key := range []string{"agents", "tools", "groups"} {
		if _, ok := mainRaw[key]; ok {
			t.Fatalf("main still contains %q after extract", key)
		}
	}
	for _, keep := range []string{"settings", "hosts"} {
		if _, ok := mainRaw[keep]; !ok {
			t.Fatalf("main lost %q during extract", keep)
		}
	}

	groupsRaw := string(rawKeys(t, filepath.Join(dir, "settings.d", "groups.json"))["groups"])
	if strings.Contains(groupsRaw, `"dots"`) {
		t.Fatalf("groups.json must not carry dots: %s", groupsRaw)
	}
	if !strings.Contains(groupsRaw, `"special": "host"`) && !strings.Contains(groupsRaw, `"special":"host"`) {
		t.Fatalf("groups.json lost host group: %s", groupsRaw)
	}
	dotsRaw := string(rawKeys(t, filepath.Join(dir, "settings.d", "dots.json"))["groups"])
	for _, want := range []string{"vim", "zshrc", "*.zwc"} {
		if !strings.Contains(dotsRaw, want) {
			t.Fatalf("dots.json = %s, want %s", dotsRaw, want)
		}
	}
	if strings.Contains(dotsRaw, "jq") {
		t.Fatalf("dots.json must not carry tool membership: %s", dotsRaw)
	}

	after, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load after: %v", err)
	}
	if len(after.MergeNotices) != 0 {
		t.Fatalf("extracted layout must have no duplicate notices: %v", after.MergeNotices)
	}
	assertSameEffectiveConfig(t, before, after)
}

func TestExtractIncludeFragments_Idempotent(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(mainPath, []byte(monolithicSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.ExtractIncludeFragments(mainPath); err != nil {
		t.Fatalf("first extract: %v", err)
	}
	firstMain, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	fragmentPaths := []string{
		filepath.Join(dir, "settings.d", "agents.json"),
		filepath.Join(dir, "settings.d", "tools.json"),
		filepath.Join(dir, "settings.d", "groups.json"),
		filepath.Join(dir, "settings.d", "dots.json"),
	}
	firstFragments := make(map[string][]byte)
	for _, path := range fragmentPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		firstFragments[path] = data
	}
	report, err := config.ExtractIncludeFragments(mainPath)
	if err != nil {
		t.Fatalf("second extract: %v", err)
	}
	if !report.Unchanged {
		t.Fatalf("second extract must be a no-op, got moved=%v", report.Moved)
	}
	secondMain, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstMain) != string(secondMain) {
		t.Fatalf("main changed on second extract:\nfirst:\n%s\nsecond:\n%s", firstMain, secondMain)
	}
	for _, path := range fragmentPaths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s after second extract: %v", path, err)
		}
		if string(data) != string(firstFragments[path]) {
			t.Fatalf("fragment %s changed on second extract", path)
		}
	}
	after, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(after.MergeNotices) != 0 {
		t.Fatalf("unexpected duplicate notices after re-extract: %v", after.MergeNotices)
	}
}

func TestExtractIncludeFragments_CollapsesDivergentDuplicates(t *testing.T) {
	dir := t.TempDir()
	mainPath := writeRoutedFixture(t, dir, map[string]string{
		"settings.json": `{
  "version": 17,
  "$include": ["settings.d/groups.json"],
  "settings": { "auto_import": true },
  "groups": [
    { "name": "core", "dots": [{ "name": "vim", "path": "~/.vim", "ignore": ["stale"] }] }
  ]
}`,
		"settings.d/groups.json": `{
  "groups": [
    { "name": "core", "dots": [{ "name": "vim", "path": "~/.vim", "ignore": ["fresh"] }] }
  ]
}`,
	})

	if _, err := config.ExtractIncludeFragments(mainPath); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	dotsRaw := string(rawKeys(t, filepath.Join(dir, "settings.d", "dots.json"))["groups"])
	if !strings.Contains(dotsRaw, "fresh") || strings.Contains(dotsRaw, "stale") {
		t.Fatalf("dots.json = %s, want effective (fragment) ignore list only", dotsRaw)
	}
	if _, ok := rawKeys(t, mainPath)["groups"]; ok {
		t.Fatal("main still contains groups after extract")
	}
	cfg, err := config.Load(mainPath)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.MergeNotices) != 0 {
		t.Fatalf("duplicates must be gone: %v", cfg.MergeNotices)
	}
}

func TestExtractThenRoutedDotWriteStaysInDotsFragment(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(mainPath, []byte(monolithicSettings), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := config.ExtractIncludeFragments(mainPath); err != nil {
		t.Fatalf("Extract: %v", err)
	}

	patch := mergedGroupsPatch(t, mainPath, func(cfg *config.RootConfig) {
		for _, group := range cfg.Groups {
			if group.Name == "core" {
				group.Dots = append(group.Dots, config.DotEntry{Name: "tmux", Path: "~/.config/tmux"})
			}
		}
	})
	if err := config.PatchRawRouted(mainPath, patch); err != nil {
		t.Fatalf("PatchRawRouted: %v", err)
	}
	if _, ok := rawKeys(t, mainPath)["groups"]; ok {
		t.Fatal("groups re-inlined into main after routed write")
	}
	dotsRaw := string(rawKeys(t, filepath.Join(dir, "settings.d", "dots.json"))["groups"])
	if !strings.Contains(dotsRaw, "tmux") {
		t.Fatalf("dots.json = %s, want new dot entry", dotsRaw)
	}
	groupsRaw := string(rawKeys(t, filepath.Join(dir, "settings.d", "groups.json"))["groups"])
	if strings.Contains(groupsRaw, "tmux") {
		t.Fatalf("groups.json received dot entry: %s", groupsRaw)
	}
}

func assertSameEffectiveConfig(t *testing.T, before, after *config.RootConfig) {
	t.Helper()
	normalize := func(cfg *config.RootConfig) *config.RootConfig {
		copied := *cfg
		copied.Include = nil
		copied.MergeNotices = nil
		copied.Schema = ""
		return &copied
	}
	b, a := normalize(before), normalize(after)
	bJSON, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	aJSON, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var bVal, aVal any
	if err := json.Unmarshal(bJSON, &bVal); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(aJSON, &aVal); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bVal, aVal) {
		t.Fatalf("effective config changed by extraction:\nbefore: %s\nafter:  %s", bJSON, aJSON)
	}
}
