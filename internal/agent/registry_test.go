package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestRegistryAllPreservesSupportedTargetOrder(t *testing.T) {
	r := mustRegistry(t)

	all := r.All()
	got := make([]string, len(all))
	for i, target := range all {
		got[i] = target.ID + "|" + target.Display
	}
	want := strings.Split(strings.TrimSpace(`
aider-desk|AiderDesk
amp|Amp
replit|Replit
universal|Universal
antigravity|Antigravity
antigravity-cli|Antigravity CLI
astrbot|AstrBot
autohand-code|Autohand Code CLI
augment|Augment
bob|IBM Bob
claude-code|Claude Code
openclaw|OpenClaw
cline|Cline
dexto|Dexto
kimi-code-cli|Kimi Code CLI
loaf|Loaf
warp|Warp
zed|Zed
codearts-agent|CodeArts Agent
codebuddy|CodeBuddy
codemaker|Codemaker
codestudio|Code Studio
codex|Codex
command-code|Command Code
continue|Continue
cortex|Cortex Code
crush|Crush
cursor|Cursor
deepagents|Deep Agents
devin|Devin for Terminal
droid|Droid
firebender|Firebender
forgecode|ForgeCode
gemini-cli|Gemini CLI
github-copilot|GitHub Copilot
goose|Goose
grok|Grok
hermes-agent|Hermes Agent
inference-sh|inference.sh
jazz|Jazz
junie|Junie
iflow-cli|iFlow CLI
kilo|Kilo Code
kiro-cli|Kiro CLI
kode|Kode
lingma|Lingma
mcpjam|MCPJam
mistral-vibe|Mistral Vibe
moxby|Moxby
mux|Mux
opencode|OpenCode
openhands|OpenHands
ona|Ona
pi|Pi
qoder|Qoder
qoder-cn|Qoder CN
qwen-code|Qwen Code
reasonix|Reasonix
rovodev|Rovo Dev
roo|Roo Code
tabnine-cli|Tabnine CLI
terramind|Terramind
tinycloud|Tinycloud
trae|Trae
trae-cn|Trae CN
windsurf|Windsurf
zencoder|Zencoder
zenflow|Zenflow
neovate|Neovate
pochi|Pochi
adal|AdaL
`), "\n")
	if !slices.Equal(got, want) {
		t.Fatalf("Registry.All() = %v, want %v", got, want)
	}
}

func TestRegistryAllReturnsCopy(t *testing.T) {
	r := mustRegistry(t)
	all := r.All()
	all[0].ID = "changed"
	if got := r.All()[0].ID; got != "aider-desk" {
		t.Fatalf("Registry.All()[0].ID = %q after caller mutation, want aider-desk", got)
	}
}

func TestRegistryByID(t *testing.T) {
	r := mustRegistry(t)
	if got, ok := r.ByID("codex"); !ok || got.ID != "codex" || got.Display != "Codex" {
		t.Fatalf("Registry.ByID(codex) = %+v, %v", got, ok)
	}
	if got, ok := r.ByID("missing"); ok || got.ID != "" || got.Display != "" {
		t.Fatalf("Registry.ByID(missing) = %+v, %v, want zero target, false", got, ok)
	}
}

func TestHermesTargetMetadata(t *testing.T) {
	r := mustRegistry(t)
	hermes, ok := r.ByID("hermes-agent")
	if !ok {
		t.Fatal("hermes-agent target missing")
	}
	if hermes.binary != "hermes" || hermes.configEnv != "HERMES_HOME" || hermes.configDir != ".hermes" {
		t.Fatalf("hermes-agent metadata = %+v", hermes)
	}
	home := t.TempDir()
	hermesHome := t.TempDir()
	t.Setenv("HERMES_HOME", hermesHome)
	if got := hermes.configPath(home); got != hermesHome {
		t.Fatalf("configPath() = %q, want %q", got, hermesHome)
	}
}

func TestTargetHasAnySkill(t *testing.T) {
	r := testRegistry(t)
	codex, ok := r.ByID("codex")
	if !ok {
		t.Fatal("codex target missing")
	}
	home := t.TempDir()
	if codex.HasAnySkill(home, []string{"missing"}) {
		t.Fatal("Target.HasAnySkill() = true for missing skill")
	}
	mkdir(t, filepath.Join(home, ".codex", "skills", "primary"))
	if !codex.HasAnySkill(home, []string{"missing", "primary"}) {
		t.Fatal("Target.HasAnySkill() = false for primary skills dir")
	}
	mkdir(t, filepath.Join(home, ".agents", "skills", "shared"))
	if !codex.HasAnySkill(home, []string{"shared"}) {
		t.Fatal("Target.HasAnySkill() = false for additional skills dir")
	}
	override := t.TempDir()
	t.Setenv("CODEX_HOME", override)
	mkdir(t, filepath.Join(override, "skills", "override"))
	if !codex.HasAnySkill(home, []string{"override"}) {
		t.Fatal("Target.HasAnySkill() = false for config env override")
	}
}

func TestRegistryConfigDotCandidateNames(t *testing.T) {
	r := mustRegistry(t)
	want := []string{"agents", "crush", "devin", "goose", "opencode"}
	if got := r.ConfigDotCandidateNames(); !slices.Equal(got, want) {
		t.Fatalf("Registry.ConfigDotCandidateNames() = %v, want %v", got, want)
	}
}

func TestRegistryInstalledPreservesDetectionBehavior(t *testing.T) {
	t.Run("missing config dirs", func(t *testing.T) {
		r := testRegistry(t)
		stubBinariesOnPath(t)
		if got := installedIDs(r, t.TempDir()); len(got) != 0 {
			t.Fatalf("Registry.Installed() = %v, want none", got)
		}
	})

	t.Run("shared config requires target binary", func(t *testing.T) {
		r := testRegistry(t)
		home := t.TempDir()
		stubBinariesOnPath(t)
		mkdir(t, filepath.Join(home, ".agents", "demo"))
		mkdir(t, filepath.Join(home, ".config", "agents", "demo"))
		mkdir(t, filepath.Join(home, ".zencoder", "demo"))
		if got := installedIDs(r, home); len(got) != 0 {
			t.Fatalf("Registry.Installed() = %v, want none", got)
		}
	})

	t.Run("shared config with target binary", func(t *testing.T) {
		r := testRegistry(t)
		home := t.TempDir()
		stubBinariesOnPath(t, "cline")
		mkdir(t, filepath.Join(home, ".agents", "demo"))
		if got := installedIDs(r, home); !slices.Equal(got, []string{"cline"}) {
			t.Fatalf("Registry.Installed() = %v, want [cline]", got)
		}
	})

	t.Run("empty dedicated config without binary", func(t *testing.T) {
		r := testRegistry(t)
		home := t.TempDir()
		stubBinariesOnPath(t)
		mkdir(t, filepath.Join(home, ".openclaw"))
		if got := installedIDs(r, home); len(got) != 0 {
			t.Fatalf("Registry.Installed() = %v, want none", got)
		}
	})

	t.Run("nonempty dedicated config without binary", func(t *testing.T) {
		r := testRegistry(t)
		home := t.TempDir()
		stubBinariesOnPath(t)
		mkdir(t, filepath.Join(home, ".openclaw", "state"))
		if got := installedIDs(r, home); !slices.Equal(got, []string{"openclaw"}) {
			t.Fatalf("Registry.Installed() = %v, want [openclaw]", got)
		}
	})

	t.Run("dedicated config with missing binary", func(t *testing.T) {
		r := testRegistry(t)
		home := t.TempDir()
		stubBinariesOnPath(t)
		mkdir(t, filepath.Join(home, ".cursor", "state"))
		if got := installedIDs(r, home); len(got) != 0 {
			t.Fatalf("Registry.Installed() = %v, want none", got)
		}
	})

	t.Run("config env override and catalog order", func(t *testing.T) {
		r := testRegistry(t)
		home := t.TempDir()
		stubBinariesOnPath(t, "codex", "cursor")
		mkdir(t, filepath.Join(home, ".aider-desk", "state"))
		mkdir(t, filepath.Join(home, ".openclaw", "state"))
		mkdir(t, filepath.Join(home, ".cursor", "state"))
		override := t.TempDir()
		t.Setenv("CODEX_HOME", override)
		want := []string{"aider-desk", "openclaw", "codex", "cursor"}
		if got := installedIDs(r, home); !slices.Equal(got, want) {
			t.Fatalf("Registry.Installed() = %v, want %v", got, want)
		}
	})
}

func TestRegistryInstalledByID(t *testing.T) {
	r := testRegistry(t)
	home := t.TempDir()
	stubBinariesOnPath(t)
	mkdir(t, filepath.Join(home, ".openclaw", "state"))
	if got, ok := r.InstalledByID(home, "openclaw"); !ok || got.ID != "openclaw" {
		t.Fatalf("Registry.InstalledByID(openclaw) = %+v, %v", got, ok)
	}
	for _, id := range []string{"codex", "missing"} {
		if got, ok := r.InstalledByID(home, id); ok || got.ID != "" || got.Display != "" {
			t.Fatalf("Registry.InstalledByID(%s) = %+v, %v, want zero target, false", id, got, ok)
		}
	}
}

func testRegistry(t *testing.T) *Registry {
	t.Helper()
	for _, name := range []string{"CLAUDE_CONFIG_DIR", "CODEX_HOME", "GROK_HOME"} {
		t.Setenv(name, "")
	}
	return mustRegistry(t)
}

func mustRegistry(t *testing.T, opts ...Option) *Registry {
	t.Helper()
	r, err := NewRegistry(opts...)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func installedIDs(r *Registry, home string) []string {
	targets := r.Installed(home)
	ids := make([]string, len(targets))
	for i, target := range targets {
		ids[i] = target.ID
	}
	return ids
}

func stubBinariesOnPath(t *testing.T, names ...string) {
	t.Helper()
	dir := t.TempDir()
	for _, name := range names {
		path := filepath.Join(dir, name)
		if runtime.GOOS == "windows" {
			path += ".exe"
		}
		if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", dir)
}

func mkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}
