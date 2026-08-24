package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
)

func TestInitOnboardingReadOnlyCreatesNothing(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "missing", "settings.json")
	state := filepath.Join(root, "state")
	cache := filepath.Join(root, "cache")
	a := New(configPath)
	a.StateDir, a.CacheDir = state, cache
	if err := a.InitOnboardingReadOnly(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Dir(configPath), state, cache} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("read-only init created %s", path)
		}
	}
}

func TestAgentsOnboardPlanDoesNotWriteAnywhereUnderHome(t *testing.T) {
	a, _, home := newOnboardPlanTestApp(t,
		`{"version":23,"agents":{"packages":[{"source":"acme/package","agents":["codex"]}]}}`,
		`[{"target":"codex"}]`,
	)
	unrelated := filepath.Join(home, "unrelated.txt")
	if err := os.WriteFile(unrelated, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := snapshotOnboardTestTree(t, home)
	if _, err := a.AgentsOnboardPlan(t.Context(), AgentsOnboardOptions{}); err != nil {
		t.Fatal(err)
	}
	after := snapshotOnboardTestTree(t, home)
	if !maps.Equal(before, after) {
		t.Fatalf("preview changed HOME: before=%v after=%v", before, after)
	}
}

func TestAgentsOnboardPlanUsesLiveAPMTargetsWithoutClientAllowlist(t *testing.T) {
	a, _, _ := newOnboardPlanTestApp(t,
		`{"version":23,"agents":{"packages":[{"source":"acme/package","agents":["future-client"]}]}}`,
		`[{"target":"zeta"},{"target":"future-client"},{"target":"alpha"}]`,
	)
	result, err := a.AgentsOnboardPlan(t.Context(), AgentsOnboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Envelope.Plan == nil || len(result.Envelope.Plan.Items) != 1 {
		t.Fatalf("plan=%+v", result.Envelope.Plan)
	}
	item := result.Envelope.Plan.Items[0]
	if got := strings.Join(item.TargetOptions, ","); got != "alpha,future-client,zeta" {
		t.Fatalf("target options=%q", got)
	}
	if got := strings.Join(item.Blockers, ","); strings.Contains(got, "unknown-target") {
		t.Fatalf("live APM target was rejected: %s", got)
	}
}

func TestAgentsOnboardApplyRejectsLegacyPreimageDriftBeforeJournalWrite(t *testing.T) {
	a, mock, home := newOnboardPlanTestApp(t,
		`{"version":23,"agents":{"packages":[{"source":"acme/package","agents":["codex"]}]}}`,
		`[{"target":"codex"}]`,
	)
	result, err := a.AgentsOnboardPlan(t.Context(), AgentsOnboardOptions{})
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(home, ".config", "omni", "settings.json")
	if err := os.WriteFile(configPath, []byte(`{"version":23,"agents":{"packages":[{"source":"acme/changed","agents":["codex"]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	mock.Reset()
	_, err = a.AgentsOnboardApplyReviewed(t.Context(), *result.Envelope.Plan)
	if err == nil || !strings.Contains(err.Error(), "reviewed plan is stale") {
		t.Fatalf("err=%v", err)
	}
	stateRoot := filepath.Join(home, ".local", "state", "omni", "onboarding")
	if _, err := os.Stat(stateRoot); !os.IsNotExist(err) {
		t.Fatalf("stale apply wrote journal state: %v", err)
	}
}

func newOnboardPlanTestApp(t *testing.T, legacy, targets string) (*App, *executor.MockExecutor, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	configPath := filepath.Join(home, ".config", "omni", "settings.json")
	if err := os.MkdirAll(filepath.Dir(configPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	mock := &executor.MockExecutor{Responses: []executor.MockCall{
		{Stdout: "Agent Package Manager (APM) CLI version 0.28.0+omni.7\n"},
		{Stdout: targets},
	}}
	a := New(configPath)
	if err := a.InitOnboardingReadOnly(t.Context()); err != nil {
		t.Fatal(err)
	}
	a.SetFallbackExecutor(mock)
	t.Cleanup(func() { _ = a.Close() })
	return a, mock, home
}

func snapshotOnboardTestTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		value := info.Mode().String()
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			value += ":" + target
		} else if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			value += ":" + digestBytes(data)
		}
		out[filepath.ToSlash(rel)] = value
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestReadReviewedPlanRejectsUnknownFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "plan.json")
	data := `{"schema_version":1,"coordinator":"omni-v24","operation_id":"0123456789abcdef0123456789abcdef","plan_id":"` + strings.Repeat("a", 64) + `","resolution_id":"` + strings.Repeat("b", 64) + `","scope":"global","sources":[],"candidate_set_id":"` + strings.Repeat("c", 64) + `","inventory_fingerprint":"` + strings.Repeat("d", 64) + `","items":[],"summary":{},"warnings":[],"blockers":[],"unknown":true}`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readReviewedPlan(path); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("err=%v", err)
	}
}

func TestApprovedTargetsComeFromReviewedItem(t *testing.T) {
	plan := OnboardPlan{Items: []OnboardItem{{ID: "item", Name: "demo", TargetOptions: []string{"future-agent", "next-agent"}}}}
	if err := validateApprovedTargetResolutions(plan, map[string][]string{"item": {"future-agent"}}); err != nil {
		t.Fatal(err)
	}
	for _, values := range []map[string][]string{{"item": {"codex"}}, {"missing": {"future-agent"}}} {
		if err := validateApprovedTargetResolutions(plan, values); err == nil {
			t.Fatalf("accepted %#v", values)
		}
	}
}

func TestInitDetectsJoinedOnboardingRecoveryBeforeMutation(t *testing.T) {
	for _, readOnly := range []bool{false, true} {
		t.Run(fmt.Sprintf("readOnly=%v", readOnly), func(t *testing.T) {
			root := t.TempDir()
			configPath := filepath.Join(root, "settings.json")
			cache := filepath.Join(root, "cache")
			state := filepath.Join(root, "state")
			if err := os.WriteFile(configPath, []byte(`{"version":24}`), 0o600); err != nil {
				t.Fatal(err)
			}
			stateRoot, err := onboardingRoot(state)
			if err != nil {
				t.Fatal(err)
			}
			op := "0123456789abcdef0123456789abcdef"
			opRoot, err := stateRoot.Child(op)
			if err != nil {
				t.Fatal(err)
			}
			journal := onboardJournal{SchemaVersion: 1, OperationID: op, PlanID: strings.Repeat("a", 64), ResolutionID: strings.Repeat("b", 64), CandidateSetID: strings.Repeat("c", 64), Phase: "packages-installed", ManifestPath: filepath.Join(root, "apm.yml")}
			if err := writeOnboardJournal(opRoot, journal); err != nil {
				t.Fatal(err)
			}
			mock := &executor.MockExecutor{Responses: []executor.MockCall{{Stdout: "APM CLI version 0.28.0+omni.7\n"}}}
			a := New(configPath)
			a.StateDir = state
			a.CacheDir = cache
			a.SetFallbackExecutor(mock)
			if readOnly {
				err = a.InitReadOnly(context.Background())
			} else {
				err = a.Init(context.Background())
			}
			var recovery *OnboardingRecoveryError
			if !errors.As(err, &recovery) {
				t.Fatalf("err=%v", err)
			}
			if _, err := os.Stat(cache); !os.IsNotExist(err) {
				t.Fatalf("cache mutated: %v", err)
			}
			if _, err := os.Stat(configPath + settingsBackupSuffix); !os.IsNotExist(err) {
				t.Fatalf("backup created: %v", err)
			}
		})
	}
}

func TestExtractLegacyCandidatesNestedIncludesAndSecrets(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "settings.json")
	one := filepath.Join(dir, "one.json")
	two := filepath.Join(dir, "two.json")
	mustWrite := func(path, data string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(root, `{"version":23,"$include":["one.json"],"settings":{"dots_repo":"keep"},"agents":{"packages":[{"source":"https://user:TOPSECRET@example/pkg","agents":["codex"]}]}}`)
	mustWrite(one, `{"$include":["two.json"],"agents":{"skills":[{"name":"review","source":"https://example/skills","agents":["claude-code"]}]}}`)
	mustWrite(two, `{"agents":{"mcp_servers":[{"name":"api","transport":"http","url":"https://user:TOPSECRET@example.test","headers":{"Authorization":"TOPSECRET"},"agents":["codex"]}]}}`)
	first, err := ExtractLegacyCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ExtractLegacyCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Envelope.CandidateSetID != second.Envelope.CandidateSetID || len(first.Envelope.Candidates) != 3 {
		t.Fatalf("unstable/incomplete candidates: %#v", first.Envelope)
	}
	encoded, _ := json.Marshal(first.Envelope)
	if strings.Contains(string(encoded), "TOPSECRET") || !strings.Contains(string(encoded), "literal-secret") {
		t.Fatalf("secret leak/redaction missing: %s", encoded)
	}
	if len(first.Documents) != 3 {
		t.Fatalf("documents=%v", first.Documents)
	}
}

func TestExtractLegacyCandidatesV23IncludesPreserveExactDeploymentTargets(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "settings.json")
	child := filepath.Join(dir, "agents.json")
	if err := os.WriteFile(root, []byte(`{"version":23,"$include":["agents.json"],"agents":{"packages":[{"source":"acme/package","agents":["codex"]}],"marketplaces":[{"name":"tools","source":"acme/tools","agents":["claude-code"]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(child, []byte(`{"agents":{"plugins":[{"name":"reviewer","marketplace":"tools","agents":["claude-code","codex"]}],"mcp_servers":[{"name":"api","transport":"stdio","command":"api-server","agents":["codex"]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	inventory, err := ExtractLegacyCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, candidate := range inventory.Envelope.Candidates {
		got[candidate.Kind+"/"+candidate.Name] = strings.Join(candidate.SourceTargets, ",")
	}
	want := map[string]string{
		"package/acme/package": "codex",
		"marketplace/tools":    "claude",
		"plugin/reviewer":      "claude,codex",
		"mcp/api":              "codex",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("targets=%v want=%v", got, want)
	}
}

func TestExtractLegacyCandidatesFromSanitizedRealDotfiles(t *testing.T) {
	root := filepath.Join("testdata", "onboarding", "real-dotfiles-v22", "settings.json")
	first, err := ExtractLegacyCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ExtractLegacyCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	if first.Envelope.CandidateSetID != second.Envelope.CandidateSetID {
		t.Fatal("real-world fixture produced an unstable candidate set")
	}
	if len(first.Documents) != 3 || len(first.Envelope.SourcePreimages) != 3 {
		t.Fatalf("documents=%d preimages=%d", len(first.Documents), len(first.Envelope.SourcePreimages))
	}
	wantKinds := map[string]int{"package": 4, "skill": 1, "plugin": 22, "marketplace": 14, "mcp": 6, "unsupported": 1}
	wantTargets := map[string]int{"": 21, "claude": 20, "claude,codex": 2, "codex": 5}
	gotKinds := map[string]int{}
	excluded, conditional, placeholders := 0, 0, 0
	targets := map[string]int{}
	preimagePaths := map[string]string{}
	for _, preimage := range first.Envelope.SourcePreimages {
		preimagePaths[preimage.ID] = preimage.AbsolutePath
	}
	owners := map[string]int{}
	for _, candidate := range first.Envelope.Candidates {
		gotKinds[candidate.Kind]++
		targets[strings.Join(candidate.SourceTargets, ",")]++
		if len(candidate.SourcePreimageIDs) != 1 {
			t.Fatalf("candidate %s preimages=%v", candidate.ID, candidate.SourcePreimageIDs)
		}
		source := preimagePaths[candidate.SourcePreimageIDs[0]]
		if source == "" || !strings.HasPrefix(first.Pointers[candidate.ID], source+"#") {
			t.Fatalf("candidate %s source=%q pointer=%q", candidate.ID, source, first.Pointers[candidate.ID])
		}
		owners[filepath.Base(source)]++
		payload := string(candidate.Payload)
		if strings.Contains(strings.ToLower(payload), "lkshrk") {
			t.Fatal("fixture was not sanitized")
		}
		if strings.Contains(payload, `"disposition":"excluded"`) {
			excluded++
		}
		if strings.Contains(payload, `"unsupported_reason":"conditional-group-host"`) {
			conditional++
		}
		if strings.Contains(payload, "${") {
			placeholders++
		}
	}
	if !maps.Equal(gotKinds, wantKinds) || !maps.Equal(targets, wantTargets) || !maps.Equal(owners, map[string]int{"agents.json": 47, "groups.json": 1}) || excluded != 16 || conditional != 1 || placeholders != 1 {
		t.Fatalf("kinds=%v targets=%v owners=%v excluded=%d conditional=%d placeholders=%d", gotKinds, targets, owners, excluded, conditional, placeholders)
	}
	email := regexp.MustCompile(`(?i)[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}`)
	for _, path := range first.Documents {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(data))
		for _, denied := range []string{"lkshrk", "h-cloud", ".lan", ".local", "/users/", "/home/", "topsecret", `"password"`} {
			if strings.Contains(lower, denied) {
				t.Fatalf("fixture contains denied personal data %q in %s", denied, path)
			}
		}
		if email.Match(data) {
			t.Fatalf("fixture contains an email address in %s", path)
		}
	}
}

func TestExtractLegacyCandidatesFromRealV22PreservesEveryDeclaredTarget(t *testing.T) {
	root := filepath.Join("testdata", "onboarding", "real-dotfiles-v22", "settings.json")
	inventory, err := ExtractLegacyCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, candidate := range inventory.Envelope.Candidates {
		if candidate.Kind != "package" && candidate.Kind != "mcp" && candidate.Kind != "marketplace" && candidate.Kind != "plugin" {
			continue
		}
		if strings.Contains(string(candidate.Payload), `"disposition":"excluded"`) {
			continue
		}
		got[candidate.Kind+"/"+candidate.Name] = strings.Join(candidate.SourceTargets, ",")
	}
	want := map[string]string{
		"package/example/linear-ai":           "",
		"package/example/useful-skills":       "",
		"package/ShiplightAI/agent-skills-v2": "",
		"package/sopaco/deepwiki-rs":          "",
		"mcp/litellm-tools":                   "claude",
		"mcp/shiplight":                       "claude",
		"mcp/codebase-memory-mcp":             "claude",
		"mcp/context-mode":                    "codex",
		"marketplace/context-mode":            "claude,codex",
		"marketplace/example":                 "codex",
		"marketplace/caveman":                 "claude",
		"marketplace/claude-plugins-official": "claude",
		"marketplace/ecc":                     "claude",
		"marketplace/ponytail":                "codex",
		"marketplace/openai-codex":            "claude",
		"marketplace/litellm":                 "claude",
		"marketplace/harness-marketplace":     "claude",
		"marketplace/i-have-adhd":             "claude",
		"plugin/superpowers":                  "claude",
		"plugin/github":                       "claude",
		"plugin/context-mode":                 "claude,codex",
		"plugin/ponytail":                     "codex",
		"plugin/caveman":                      "claude",
		"plugin/gopls-lsp":                    "claude",
		"plugin/code-simplifier":              "claude",
		"plugin/codex":                        "claude",
		"plugin/claude-md-management":         "claude",
		"plugin/smart-docs":                   "claude",
		"plugin/harness":                      "claude",
		"plugin/i-have-adhd":                  "claude",
		"plugin/linear-ai":                    "codex",
	}
	if !maps.Equal(got, want) {
		t.Fatalf("targets=%v want=%v", got, want)
	}
}

func TestExtractLegacyCandidatesCycle(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "settings.json")
	child := filepath.Join(dir, "child.json")
	_ = os.WriteFile(root, []byte(`{"version":22,"$include":["child.json"]}`), 0o600)
	_ = os.WriteFile(child, []byte(`{"$include":["settings.json"]}`), 0o600)
	if _, err := ExtractLegacyCandidates(root); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("err=%v", err)
	}
}

func TestExtractLegacyCandidatesAppliesCurrentHostGroupsAndTargets(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "legacy-host")
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := `{"version":22,"agents":{"packages":[{"source":"active","agents":["claude","codex"]},{"source":"inactive","agents":["claude"]},{"source":"global","agents":["claude","codex"]}]},"groups":[{"name":"active-group","skills":["active"]},{"name":"inactive-group","skills":["inactive"]}],"hosts":{"legacy-host":["active-group"]},"host_settings":{"legacy-host":{"agents_use":["codex"]}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	inv, err := ExtractLegacyCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]OnboardCandidate{}
	for _, candidate := range inv.Envelope.Candidates {
		got[candidate.Name] = candidate
	}
	for _, name := range []string{"active", "global"} {
		if strings.Join(got[name].SourceTargets, ",") != "codex" || strings.Contains(string(got[name].Payload), "excluded") {
			t.Fatalf("%s=%+v payload=%s", name, got[name], got[name].Payload)
		}
	}
	if !strings.Contains(string(got["inactive"].Payload), "excluded-inactive-group") {
		t.Fatalf("inactive payload=%s", got["inactive"].Payload)
	}
}

func TestExtractLegacyCandidatesAppliesCurrentHostFeatureDisables(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "legacy-host")
	for flag, kind := range map[string]string{"skills_disabled": "package", "mcp_disabled": "mcp", "plugins_disabled": "plugin"} {
		t.Run(flag, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "settings.json")
			raw := fmt.Sprintf(`{"version":22,"agents":{"packages":[{"source":"pkg","agents":["codex"]}],"mcp_servers":[{"name":"server","transport":"stdio","command":"true","agents":["codex"]}],"plugins":[{"name":"plug","source":"https://example.test/plug.git","agents":["codex"]}]},"host_settings":{"legacy-host":{"%s":true}}}`, flag)
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			inv, err := ExtractLegacyCandidates(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range inv.Envelope.Candidates {
				if candidate.Kind == kind && !strings.Contains(string(candidate.Payload), "excluded-disabled") {
					t.Fatalf("%s payload=%s", kind, candidate.Payload)
				}
			}
		})
	}
}

func TestExtractLegacyCandidatesExpandsAgentMacrosForActiveAndInactiveGroups(t *testing.T) {
	for _, active := range []bool{false, true} {
		t.Run(fmt.Sprintf("active=%v", active), func(t *testing.T) {
			t.Setenv("OMNI_HOSTNAME", "macro-host")
			path := filepath.Join(t.TempDir(), "settings.json")
			hosts := "{}"
			if active {
				hosts = `{"macro-host":["bundle"]}`
			}
			raw := fmt.Sprintf(`{"version":22,"agents":{"packages":[{"source":"pkg-a","agents":["codex"]},{"source":"pkg-b","agents":["codex"]}],"mcp_servers":[{"name":"mcp-a","transport":"stdio","command":"true","agents":["codex"]}],"plugins":[{"name":"plug-a","source":"https://example.test/plug.git","agents":["codex"]}],"marketplaces":[{"name":"market-a","source":"acme/market"}]},"groups":[{"name":"bundle","skills":["@agents.packages"],"mcp_servers":["@agents.mcp_servers"],"plugins":["@agents.plugins"],"marketplaces":["@agents.marketplaces"]}],"hosts":%s}`, hosts)
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatal(err)
			}
			inv, err := ExtractLegacyCandidates(path)
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range inv.Envelope.Candidates {
				if candidate.Kind == "unsupported" {
					continue
				}
				excluded := strings.Contains(string(candidate.Payload), "excluded-inactive-group")
				if excluded == active {
					t.Fatalf("active=%v candidate=%s/%s payload=%s", active, candidate.Kind, candidate.Name, candidate.Payload)
				}
			}
		})
	}
}

func TestExtractLegacyCandidatesHostSettingsIgnoreActiveGroupNameCollision(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "laptop")
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := `{"version":22,"agents":{"packages":[{"source":"pkg","agents":["claude","codex"]}]},"groups":[{"name":"dev","skills":["pkg"]}],"hosts":{"laptop":["dev"]},"host_settings":{"dev":{"agents_disabled":true},"laptop":{"agents_use":["codex"]}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	inv, err := ExtractLegacyCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	var candidate OnboardCandidate
	for _, item := range inv.Envelope.Candidates {
		if item.Kind == "package" {
			candidate = item
		}
	}
	if candidate.ID == "" {
		t.Fatalf("candidates=%+v", inv.Envelope.Candidates)
	}
	if strings.Contains(string(candidate.Payload), "excluded-disabled") || strings.Join(candidate.SourceTargets, ",") != "codex" {
		t.Fatalf("candidate=%+v payload=%s", candidate, candidate.Payload)
	}
}

func TestLegacyNonMCPURLQueryCredentialsAreRedacted(t *testing.T) {
	object := map[string]any{"source": "https://example.test/pkg.git?token=SENTINEL_SECRET"}
	if !sanitizeLegacyPayload(object, false) {
		t.Fatal("credential URL was not blocked")
	}
	data, _ := json.Marshal(object)
	if strings.Contains(string(data), "SENTINEL_SECRET") {
		t.Fatalf("secret leaked: %s", data)
	}
}

func TestExtractLegacyCandidatesAgentsDisabledExcludesEveryDeploymentKind(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "legacy-host")
	path := filepath.Join(t.TempDir(), "settings.json")
	raw := `{"version":22,"agents":{"packages":[{"source":"pkg","agents":["codex"]}],"mcp_servers":[{"name":"server","transport":"stdio","command":"true","agents":["codex"]}],"plugins":[{"name":"plug","source":"https://example.test/plug.git","agents":["codex"]}],"marketplaces":[{"name":"tools","source":"acme/tools","agents":["codex"]}]},"host_settings":{"legacy-host":{"agents_disabled":true}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	inv, err := ExtractLegacyCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, candidate := range inv.Envelope.Candidates {
		if !strings.Contains(string(candidate.Payload), "excluded-disabled") {
			t.Fatalf("%s/%s payload=%s", candidate.Kind, candidate.Name, candidate.Payload)
		}
	}
}

func TestExtractLegacyCandidatesV24UsesEmptyArrays(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":24}`), 0o600); err != nil {
		t.Fatal(err)
	}
	inventory, err := ExtractLegacyCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(inventory.Envelope)
	if strings.Contains(string(data), `"candidates":null`) || strings.Contains(string(data), `"source_preimages":null`) {
		t.Fatalf("invalid envelope: %s", data)
	}
}

func TestExtractLegacyCandidatesMarksUnscopedTargetsForChoice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(`{"version":23,"agents":{"skills":[{"name":"x","source":"https://example/x"}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	inventory, err := ExtractLegacyCandidates(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(inventory.Envelope.Candidates) != 1 || !strings.Contains(string(inventory.Envelope.Candidates[0].Payload), "target_resolution_required") {
		t.Fatalf("candidate=%#v", inventory.Envelope.Candidates)
	}
}

func TestExtractLegacyCandidatesResolvesActiveGroupsAndEmitsConditionalChoices(t *testing.T) {
	t.Setenv("OMNI_HOSTNAME", "testhost")
	dir := t.TempDir()
	root := filepath.Join(dir, "settings.json")
	nested := filepath.Join(dir, "nested.json")
	rootJSON := `{"version":22,"$include":["nested.json"],"hosts":{"testhost":["active"]},"agents":{"skills":[{"name":"active-skill","source":"https://example/active","agents":["codex"]}]},"groups":[{"name":"active","skills":["active-skill"]}]}`
	nestedJSON := `{"groups":[{"name":"inactive","plugins":["later"]}],"host_settings":{"other":{"skills_disabled":true}}}`
	if err := os.WriteFile(root, []byte(rootJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, []byte(nestedJSON), 0o600); err != nil {
		t.Fatal(err)
	}
	inventory, err := ExtractLegacyCandidates(root)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, candidate := range inventory.Envelope.Candidates {
		kinds[candidate.Name] = candidate.Kind
	}
	if kinds["active-skill"] != "skill" || kinds["inactive"] != "unsupported" || kinds["host-other"] != "unsupported" {
		t.Fatalf("candidates=%v", kinds)
	}
	for _, candidate := range inventory.Envelope.Candidates {
		if candidate.Kind == "unsupported" && !strings.Contains(string(candidate.Payload), "conditional-group-host") {
			t.Fatalf("payload=%s", candidate.Payload)
		}
	}
}

func TestCommitLegacyFragmentsPreservesUnrelatedKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	data := []byte(`{"version":23,"settings":{"dots_repo":"keep","agents_disabled":true},"agents":{"packages":[{"source":"x"}]}}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := captureJournalDocuments([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	if err := commitLegacyFragments(docs, path); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"dots_repo": "keep"`) || strings.Contains(string(got), `"agents"`) || !strings.Contains(string(got), `"version": 24`) {
		t.Fatalf("got %s", got)
	}
}

func TestCommitLegacyFragmentsPreservesConcurrentUnrelatedEditAndResumes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"version":23,"settings":{"dots_repo":"old"},"agents":{"packages":[{"source":"x"}]}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := captureJournalDocuments([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"version":23,"settings":{"dots_repo":"new"},"agents":{"packages":[{"source":"x"}]}}`)
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := commitLegacyFragments(docs, path); err != nil {
		t.Fatal(err)
	}
	if err := commitLegacyFragments(docs, path); err != nil {
		t.Fatalf("idempotent resume: %v", err)
	}
	got, _ := os.ReadFile(path)
	if !strings.Contains(string(got), `"dots_repo": "new"`) {
		t.Fatalf("unrelated edit lost: %s", got)
	}
}

func TestCommitLegacyFragmentsRejectsChangedLegacyNode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "settings.json")
	original := []byte(`{"version":23,"agents":{"packages":[{"source":"x"}]}}`)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	docs, err := captureJournalDocuments([]string{path})
	if err != nil {
		t.Fatal(err)
	}
	changed := []byte(`{"version":23,"agents":{"packages":[{"source":"y"}]}}`)
	if err := os.WriteFile(path, changed, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := commitLegacyFragments(docs, path); err == nil || !strings.Contains(err.Error(), "fragment-conflict") {
		t.Fatalf("err=%v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != string(changed) {
		t.Fatal("conflict changed file")
	}
}

func TestCommitLegacyFragmentsRejectsModeAndSymlinkSwaps(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered by Windows ACL/reparse job")
	}
	for _, kind := range []string{"mode", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "settings.json")
			data := []byte(`{"version":23,"agents":{"packages":[{"source":"x"}]}}`)
			if err := os.WriteFile(path, data, 0o600); err != nil {
				t.Fatal(err)
			}
			docs, err := captureJournalDocuments([]string{path})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "mode" {
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			} else {
				other := filepath.Join(dir, "other.json")
				if err := os.WriteFile(other, data, 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(other, path); err != nil {
					t.Fatal(err)
				}
			}
			if err := commitLegacyFragments(docs, path); err == nil || !strings.Contains(err.Error(), "fragment-conflict") {
				t.Fatalf("err=%v", err)
			}
		})
	}
}

func TestCommitLegacyFragmentsRestartsAtRenameBoundaries(t *testing.T) {
	for _, boundary := range []string{"before-rename", "after-rename"} {
		t.Run(boundary, func(t *testing.T) {
			dir := t.TempDir()
			root := filepath.Join(dir, "settings.json")
			fragment := filepath.Join(dir, "agents.json")
			if err := os.WriteFile(root, []byte(`{"version":23,"$include":["agents.json"],"agents":{"packages":[{"source":"root","agents":["codex"]}]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(fragment, []byte(`{"agents":{"skills":[{"name":"nested","source":"x","agents":["codex"]}]}}`), 0o600); err != nil {
				t.Fatal(err)
			}
			docs, err := captureJournalDocuments([]string{root, fragment})
			if err != nil {
				t.Fatal(err)
			}
			failed := false
			onboardingFragmentFailpoint = func(got, _ string) error {
				if got == boundary && !failed {
					failed = true
					return errors.New("injected")
				}
				return nil
			}
			defer func() { onboardingFragmentFailpoint = nil }()
			if err := commitLegacyFragments(docs, root); err == nil {
				t.Fatal("failpoint did not fire")
			}
			onboardingFragmentFailpoint = nil
			if err := commitLegacyFragments(docs, root); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{root, fragment} {
				data, _ := os.ReadFile(path)
				if strings.Contains(string(data), `"agents"`) {
					t.Fatalf("legacy state remains in %s: %s", path, data)
				}
			}
		})
	}
}
