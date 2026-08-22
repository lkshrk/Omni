package testguard

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

func TestAPMHardMigrationHasNoNativeLifecycleCode(t *testing.T) {
	root := repoRoot(t)
	forbiddenFiles := map[string]bool{
		"internal/agent/mcp_adapter.go":           true,
		"internal/agent/plugin_adapter.go":        true,
		"internal/agent/plugin_claude_adapter.go": true,
		"internal/agent/plugin_codex_adapter.go":  true,
		"internal/agent/plugin_grok_adapter.go":   true,
		"internal/agent/plugin_hermes_adapter.go": true,
		"internal/agent/plugin_json.go":           true,
		"internal/agent/skill_source.go":          true,
		"internal/agent/skills.go":                true,
		"internal/agent/skills_http.go":           true,
		"internal/agent/skills_lock.go":           true,
		"internal/agent/skills_outdated.go":       true,
		"internal/agent/skills_store.go":          true,
		"internal/app/agents_plugins_resolve.go":  true,
		"internal/app/agents_skill_service.go":    true,
		"internal/app/agents_skills.go":           true,
		"internal/app/agents_skills_outdated.go":  true,
		"internal/app/agents_skills_resolve.go":   true,
		"internal/app/agents_skills_rows.go":      true,
		"internal/app/agents_skills_status.go":    true,
		"internal/app/agents_skills_store.go":     true,
		"internal/app/agents_skills_text.go":      true,
		"internal/app/apm_foreign.go":             true,
		"internal/app/apm_ledger.go":              true,
		"internal/app/apm_manifest.go":            true,
		"internal/app/apm_mcp.go":                 true,
		"internal/app/apm_owned.go":               true,
		"internal/app/apm_packages.go":            true,
		"internal/app/apm_targets.go":             true,
		"internal/app/marketplace_rows.go":        true,
		"internal/app/mcp_adapter.go":             true,
		"internal/app/plugin_adapter.go":          true,
		"internal/app/plugin_ops.go":              true,
		"internal/app/plugin_rows.go":             true,
		"internal/app/plugin_shadow.go":           true,
	}
	forbiddenNames := map[string]bool{
		"McpAdapter": true, "PluginAdapter": true, "Skills": true,
		"RestorePlugins": true, "RestoreSkills": true,
		"ImportPlugins": true, "ImportSkills": true,
		"UpdatePlugins": true, "UpdateSkills": true,
		"AddPlugin": true, "RemovePlugin": true,
		"AddMarketplace": true, "RemoveMarketplace": true,
		"SkillLockFile": true, "SkillPackage": true,
	}

	var violations []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if forbiddenFiles[rel] {
			violations = append(violations, rel+": forbidden native lifecycle file")
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			ident, ok := node.(*ast.Ident)
			if ok && forbiddenNames[ident.Name] {
				violations = append(violations, rel+": forbidden native lifecycle symbol "+ident.Name)
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("APM hard migration regressed:\n%s", strings.Join(uniqueTestStrings(violations), "\n"))
	}
}

func TestAPMHardMigrationHasNoDirectAgentRuntimeWrites(t *testing.T) {
	root := repoRoot(t)
	allowed := map[string]bool{
		"internal/agent/registry.go":     true,
		"internal/app/dots_discovery.go": true,
		"internal/config/agent_dirs.go":  true,
		"internal/config/migrate_v14.go": true,
	}
	runtimeFragments := []string{
		".agents", ".claude", ".codex", ".copilot", ".cursor", ".gemini",
		".hermes", ".kiro", ".opencode", ".windsurf", "mcp_config.json", "mcp-config.json",
	}
	mutators := map[string]bool{
		"Create": true, "Mkdir": true, "MkdirAll": true, "OpenFile": true,
		"Remove": true, "RemoveAll": true, "Rename": true, "Symlink": true,
		"Truncate": true, "WriteFile": true,
	}

	var violations []string
	err := filepath.WalkDir(filepath.Join(root, "internal"), func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if allowed[rel] {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			return err
		}
		hasRuntimePath := false
		hasMutation := false
		ast.Inspect(file, func(node ast.Node) bool {
			switch value := node.(type) {
			case *ast.BasicLit:
				if value.Kind != token.STRING {
					return true
				}
				literal, err := strconv.Unquote(value.Value)
				if err != nil {
					return true
				}
				for _, fragment := range runtimeFragments {
					if strings.Contains(literal, fragment) {
						hasRuntimePath = true
						break
					}
				}
			case *ast.CallExpr:
				selector, ok := value.Fun.(*ast.SelectorExpr)
				if !ok || !mutators[selector.Sel.Name] {
					return true
				}
				pkg, ok := selector.X.(*ast.Ident)
				if ok && pkg.Name == "os" {
					hasMutation = true
				}
			}
			return true
		})
		if hasRuntimePath && hasMutation {
			violations = append(violations, rel)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) > 0 {
		sort.Strings(violations)
		t.Fatalf("production files combine agent runtime paths with direct filesystem mutation:\n%s", strings.Join(violations, "\n"))
	}
}

func uniqueTestStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}
