package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

const manifestlessSyncLock = `dependencies:
- repo_url: acme/skills
  name: skills
  version: unknown
  package_type: skill_bundle
  resolved_commit: deadbeef
  deployed_files:
  - .agents/skills/demo/SKILL.md
`

func TestAgentsSyncNoStateDoesNotCreateMissingHome(t *testing.T) {
	home := filepath.Join(t.TempDir(), "missing", "home")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	mock := &executor.MockExecutor{}
	a := New(filepath.Join(t.TempDir(), "settings.json"))
	a.SetFallbackExecutor(mock)

	result, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err != nil || result.Output != "" || result.Stderr != "" || result.Warning != "" || len(result.Notices) != 0 {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if _, err := os.Stat(home); !os.IsNotExist(err) {
		t.Fatalf("missing HOME was mutated: %v", err)
	}
	if len(mock.Calls) != 0 {
		t.Fatalf("apm was invoked: %#v", mock.Calls)
	}
}

func setupManifestlessSync(t *testing.T) (*App, *executor.MockExecutor, string, string, string, []byte) {
	t.Helper()
	template := ownedSyncManifest("  mcp:\n  - name: unrelated\n    transport: stdio\n    command: echo\n", "acme/skills")
	a, mock, home := setupOwnedSync(t, template)
	writeFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"), manifestlessSyncLock)
	module := filepath.Join(home, ".apm", "apm_modules", "acme", "skills")
	writeSkillManifest(t, module, "skills/demo/SKILL.md")
	dir := filepath.Join(home, ".apm")
	candidatePath, candidate, err := agentsSyncCandidate(dir)
	if err != nil {
		t.Fatal(err)
	}
	return a, mock, dir, module, candidatePath, candidate
}

func TestAgentsSyncAllowsManifestlessSkillPackageWithUnrelatedStandaloneService(t *testing.T) {
	a, mock, _, _, _, _ := setupManifestlessSync(t)
	mock.Responses = []executor.MockCall{
		{Stdout: "APM CLI version " + apmVersionPin + "\n"},
		{Stdout: "installed\n"},
	}

	result, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
	if err != nil || result.Output != "installed\n" {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if len(mock.Calls) != 2 {
		t.Fatalf("calls = %#v", mock.Calls)
	}
}

func TestAgentsSyncRejectsMalformedLockBeforeMutation(t *testing.T) {
	for name, standalone := range map[string]string{
		"without standalone service": "",
		"with standalone service":    "  mcp:\n  - name: unrelated\n    transport: stdio\n    command: echo\n",
	} {
		t.Run(name, func(t *testing.T) {
			const secret = "TOP_SECRET_LOCK_VALUE"
			a, mock, home := setupOwnedSync(t, ownedSyncManifest(standalone))
			writeFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"), "dependencies: ["+secret+"\n")

			_, err := a.AgentsSyncAll(t.Context(), AgentsSyncAllOptions{})
			if err == nil || !strings.Contains(err.Error(), "APM lockfile unavailable") || strings.Contains(err.Error(), secret) {
				t.Fatalf("err = %v", err)
			}
			if _, err := os.Stat(filepath.Join(home, ".apm", "apm.yml")); !os.IsNotExist(err) {
				t.Fatalf("live manifest was materialized: %v", err)
			}
			if len(mock.Calls) != 0 {
				t.Fatalf("apm was invoked: %#v", mock.Calls)
			}
		})
	}
}

func TestAgentsSyncRejectsChangedManifestlessEvidenceBeforeMutation(t *testing.T) {
	for name, mutate := range map[string]func(*testing.T, string, string){
		"lock identity": func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "apm.lock.yaml"), strings.Replace(manifestlessSyncLock, "repo_url: acme/skills", "repo_url: other/skills", 1))
		},
		"package type": func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "apm.lock.yaml"), strings.Replace(manifestlessSyncLock, "package_type: skill_bundle", "package_type: claude_skill", 1))
		},
		"commit": func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "apm.lock.yaml"), strings.Replace(manifestlessSyncLock, "resolved_commit: deadbeef", "resolved_commit: changed", 1))
		},
		"deployed files": func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "apm.lock.yaml"), strings.Replace(manifestlessSyncLock, ".agents/skills/demo/SKILL.md", ".agents/skills/other/SKILL.md", 1))
		},
		"unrelated lock entry": func(t *testing.T, dir, _ string) {
			writeFile(t, filepath.Join(dir, "apm.lock.yaml"), manifestlessSyncLock+"- repo_url: unrelated/package\n  name: package\n  version: 1.0.0\n")
		},
		"positive leaf": func(t *testing.T, _, module string) {
			writeFile(t, filepath.Join(module, "skills", "demo", "SKILL.md"), "changed\n")
		},
		"negative leaf": func(t *testing.T, _, module string) {
			writeFile(t, filepath.Join(module, "mcp.json"), "{}\n")
		},
		"negative parent": func(t *testing.T, _, module string) {
			if err := os.Mkdir(filepath.Join(module, ".github"), 0o755); err != nil {
				t.Fatal(err)
			}
		},
		"module directory": func(t *testing.T, _, module string) {
			if err := os.RemoveAll(module); err != nil {
				t.Fatal(err)
			}
			writeSkillManifest(t, module, "skills/demo/SKILL.md")
		},
	} {
		t.Run(name, func(t *testing.T) {
			a, mock, dir, module, candidatePath, candidate := setupManifestlessSync(t)
			_, verify, _, err := checkAgentsOwnershipPreflight(dir, a.StateDir, candidatePath, candidate)
			if err != nil {
				t.Fatal(err)
			}
			mutate(t, dir, module)
			if err := verify(); err == nil || err.Error() != "APM package evidence changed during ownership preflight" {
				t.Fatalf("err = %v", err)
			}
			if _, err := os.Stat(filepath.Join(dir, "apm.yml")); !os.IsNotExist(err) {
				t.Fatalf("live manifest was materialized: %v", err)
			}
			if len(mock.Calls) != 0 {
				t.Fatalf("apm was invoked: %#v", mock.Calls)
			}
		})
	}
}
