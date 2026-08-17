package app

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/agent"
	"github.com/lkshrk/omni/internal/config"
)

type stubInventory struct {
	drifted map[string][]string
	err     error
}

func (s stubInventory) Inventory(_ context.Context, source string, _ []string) (agent.SkillInventory, error) {
	if s.err != nil {
		return agent.SkillInventory{}, s.err
	}
	inv := agent.SkillInventory{PerTargetDrifted: map[string]bool{}}
	for _, id := range s.drifted[source] {
		inv.PerTargetDrifted[id] = true
	}
	return inv, nil
}

func TestImportPackages_KeepsNarrowedSelector(t *testing.T) {
	t.Parallel()
	existing := []config.SkillPackage{{Source: "o/pkg", Skills: []string{"a"}}}
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"a": {Source: "o/pkg"},
		"b": {Source: "o/pkg"},
	}}
	merged, diff := importPackages(existing, lock)
	if len(merged) != 1 || len(merged[0].Skills) != 1 || merged[0].Skills[0] != "a" {
		t.Fatalf("merged = %+v, want the narrowed selector untouched", merged)
	}
	if len(diff.Updated) != 0 || len(diff.Unchanged) != 1 {
		t.Fatalf("diff = %+v, want the managed package reported unchanged", diff)
	}
}

func TestImportPackages_SelectorOrderIsNotAChange(t *testing.T) {
	t.Parallel()
	existing := []config.SkillPackage{{Source: "o/pkg", Skills: []string{"b", "a"}}}
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"a": {Source: "o/pkg"},
		"b": {Source: "o/pkg"},
	}}
	_, diff := importPackages(existing, lock)
	if len(diff.Updated) != 0 {
		t.Fatalf("diff.Updated = %v, want manifest order not to count as a change", diff.Updated)
	}
}

func TestImportPackages_StillTakesLockfileRef(t *testing.T) {
	t.Parallel()
	existing := []config.SkillPackage{{Source: "o/pkg", Ref: "old"}}
	lock := &config.SkillLockFile{Skills: map[string]config.SkillLockEntry{
		"a": {Source: "o/pkg", Ref: "new"},
	}}
	merged, diff := importPackages(existing, lock)
	if merged[0].Ref != "new" || len(diff.Updated) != 1 {
		t.Fatalf("merged = %+v diff = %+v, want the ref imported", merged, diff)
	}
}

func TestDryRunLines_SkipsDriftedTargets(t *testing.T) {
	t.Parallel()
	pkgs := []resolvedPackage{{SkillPackage: config.SkillPackage{
		Source: "o/pkg",
		Agents: []string{"claude-code", "codex"},
	}}}
	inv := stubInventory{drifted: map[string][]string{"o/pkg": {"claude-code"}}}
	lines := dryRunLines(t.Context(), pkgs, nil, inv)
	if len(lines) != 2 {
		t.Fatalf("lines = %v, want a skip line and a narrowed install line", lines)
	}
	if !strings.HasPrefix(lines[0], "skip o/pkg on claude-code (drifted") {
		t.Errorf("lines[0] = %q, want the drift skip the real sync performs", lines[0])
	}
	if !strings.HasSuffix(lines[1], "for targets codex") {
		t.Errorf("lines[1] = %q, want the drifted target left out of the install", lines[1])
	}
}

func TestDryRunLines_AllTargetsDriftedPromisesNothing(t *testing.T) {
	t.Parallel()
	pkgs := []resolvedPackage{{SkillPackage: config.SkillPackage{
		Source: "o/pkg",
		Agents: []string{"claude-code"},
	}}}
	inv := stubInventory{drifted: map[string][]string{"o/pkg": {"claude-code"}}}
	for _, line := range dryRunLines(t.Context(), pkgs, nil, inv) {
		if strings.HasPrefix(line, "install ") {
			t.Fatalf("line = %q, want no install the real sync would not perform", line)
		}
	}
}

func TestClassifySkillRows_ShadowedAndDriftedAreNotMissing(t *testing.T) {
	t.Parallel()
	rows := []SkillPackageRow{
		{Source: "o/shadowed", ShadowedByPlugin: true},
		{Source: "o/drifted", PerAgentStatus: map[string]SkillStatus{"claude-code": SkillStatusDrifted}},
		{Source: "o/gone", PerAgentStatus: map[string]SkillStatus{"claude-code": SkillStatusMissing}},
		{Source: "o/ok", Installed: true},
		{Source: "o/bad", Error: "unsupported source"},
	}
	counts := classifySkillRows(rows)
	if counts.Installed != 2 {
		t.Errorf("Installed = %d, want the shadowed package counted as satisfied", counts.Installed)
	}
	if len(counts.Missing) != 1 || counts.Missing[0] != "o/gone" {
		t.Errorf("Missing = %v, want only the genuinely absent package", counts.Missing)
	}
	if len(counts.Drifted) != 1 || counts.Drifted[0] != "o/drifted" {
		t.Errorf("Drifted = %v, want the drifted package reported once", counts.Drifted)
	}
	if len(counts.Errored) != 1 || counts.Errored[0] != "o/bad" {
		t.Errorf("Errored = %v, want the unreadable source in its own bucket", counts.Errored)
	}
}

func TestDriftedResolveTargets_NeverInstalledIsNotEligible(t *testing.T) {
	t.Parallel()
	targets := []string{"claude-code", "codex"}
	_, err := driftedResolveTargets("o/pkg", targets, agent.SkillInventory{}, nil)
	if err == nil || !strings.Contains(err.Error(), "sync") {
		t.Fatalf("err = %v, want the advice to sync rather than a droppable target set", err)
	}
}

func TestDriftedResolveTargets_RequestedAgentOnNeverInstalledPackage(t *testing.T) {
	t.Parallel()
	_, err := driftedResolveTargets("o/pkg", []string{"claude-code"}, agent.SkillInventory{}, []string{"claude-code"})
	if err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("err = %v, want the not-installed refusal", err)
	}
}

func TestDriftedResolveTargets_InstalledWithoutSkillsStaysEligible(t *testing.T) {
	t.Parallel()
	inv := agent.SkillInventory{PerTarget: map[string]bool{"claude-code": true}}
	got, err := driftedResolveTargets("o/pkg", []string{"claude-code"}, inv, nil)
	if err != nil || len(got) != 1 {
		t.Fatalf("got %v, %v; want the whole target set eligible", got, err)
	}
}

func TestResolveMcpDriftUseLocal_RefusesWhenAnotherAgentMatches(t *testing.T) {
	t.Parallel()
	target := config.McpServer{Name: "ctx7", Transport: "stdio", Command: "npx -y ctx7@1", Agents: []string{"claude-code", "codex"}}
	live := map[string]InstalledMcpServer{
		"claude-code": {Name: "ctx7", Transport: "stdio", Command: "npx -y ctx7@2"},
	}
	synced := map[string]InstalledMcpServer{
		"codex": {Name: "ctx7", Transport: "stdio", Command: "npx -y ctx7@1"},
	}
	a := &App{}
	_, err := a.resolveMcpDriftUseLocal(McpDriftResolution{}, target, []string{"claude-code"}, live, synced, false)
	if err == nil || !strings.Contains(err.Error(), "codex") {
		t.Fatalf("err = %v, want a refusal naming the agent adoption would drift", err)
	}
}

func TestResolveMcpDriftUseLocal_DryRunStillAdoptsWhenNobodyMatches(t *testing.T) {
	t.Parallel()
	target := config.McpServer{Name: "ctx7", Transport: "stdio", Command: "npx -y ctx7@1"}
	live := map[string]InstalledMcpServer{
		"claude-code": {Name: "ctx7", Transport: "stdio", Command: "npx -y ctx7@2"},
	}
	a := &App{}
	res, err := a.resolveMcpDriftUseLocal(McpDriftResolution{}, target, []string{"claude-code"}, live, nil, true)
	if err != nil {
		t.Fatalf("err = %v, want the uncontested adoption to proceed", err)
	}
	if len(res.Actions) != 1 {
		t.Fatalf("Actions = %v, want the adopt action", res.Actions)
	}
}

func TestPluginForRow_PrefersDeclaredMarketplace(t *testing.T) {
	t.Parallel()
	declared := config.Plugin{Name: "superpowers", Marketplace: "obra/superpowers"}
	listed := []InstalledPlugin{
		{Name: "superpowers", Marketplace: "me/superpowers"},
		{Name: "superpowers", Marketplace: "obra/superpowers"},
	}
	got, found := pluginForRow(listed, declared)
	if !found || got.Marketplace != "obra/superpowers" {
		t.Fatalf("got %+v, %v; want the declared identity regardless of listing order", got, found)
	}
}

func TestForeignPluginCopy_IgnoresUnknownMarketplace(t *testing.T) {
	t.Parallel()
	declared := config.Plugin{Name: "superpowers", Marketplace: "obra/superpowers"}
	if _, found := foreignPluginCopy([]InstalledPlugin{{Name: "superpowers"}}, declared); found {
		t.Fatal("an adapter that reports no marketplace cannot contradict the manifest")
	}
}

func TestCatalogCacheKey_IncludesEndpoint(t *testing.T) {
	t.Parallel()
	if catalogCacheKey("https://a.example/api", "react", "") == catalogCacheKey("https://b.example/api", "react", "") {
		t.Fatal("cache key must include the endpoint")
	}
}

func TestValidateCatalogEndpoint(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw string
		ok  bool
	}{
		{"https://skills.sh/api/search", true},
		{"http://127.0.0.1:8080/api", true},
		{"http://localhost:8080/api", true},
		{"http://evil.example/api", false},
		{"https:///api", false},
	} {
		u := mustParseURL(t, tc.raw)
		if err := validateCatalogEndpoint(u); (err == nil) != tc.ok {
			t.Errorf("validateCatalogEndpoint(%q) = %v, want ok=%v", tc.raw, err, tc.ok)
		}
	}
}

func TestFindSkillPackages_FutureCacheStampIsRefetched(t *testing.T) {
	t.Parallel()
	state := &futureStampState{}
	_, err := findSkillPackages(t.Context(), nil, "https://catalog.invalid/api", state, "react", "")
	if err == nil {
		t.Fatal("a cache entry stamped in the future must not be served forever")
	}
	if !state.read {
		t.Fatal("the cache entry was never consulted")
	}
}

type futureStampState struct {
	read bool
}

func (s *futureStampState) GetState(_ context.Context, _ string) (string, error) {
	s.read = true
	return `{"fetched_at":"2999-01-01T00:00:00Z","results":[]}`, nil
}

func (s *futureStampState) SetState(_ context.Context, _, _ string) error { return nil }

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return u
}

func TestAgentsSyncAllSummaryText_CountsUntouchedWork(t *testing.T) {
	t.Parallel()
	res := AgentsSyncAllResult{
		Plan:     []string{"packages: would add o/pkg"},
		Drift:    []string{"o/pkg: drifted on claude-code"},
		Warnings: []string{"skills: something"},
	}
	got := AgentsSyncAllSummaryText(res)
	for _, want := range []string{"1 planned changes", "1 drifted", "1 warnings"} {
		if !strings.Contains(got, want) {
			t.Errorf("summary = %q, want it to mention %q", got, want)
		}
	}
}

func TestAgentsSyncAllSummaryText_CleanRunStaysTerse(t *testing.T) {
	t.Parallel()
	if got := AgentsSyncAllSummaryText(AgentsSyncAllResult{}); !strings.HasSuffix(got, "0 failed") {
		t.Fatalf("summary = %q, want no trailing zero counts", got)
	}
}

var errStubInventory = errors.New("stub inventory failure")

func TestDriftedSkillTargets_UnreadableInventoryInventsNoDrift(t *testing.T) {
	t.Parallel()
	inv := stubInventory{err: errStubInventory}
	if got := driftedSkillTargets(t.Context(), inv, "o/pkg", []string{"claude-code"}); got != nil {
		t.Fatalf("got %v, want no drifted targets", got)
	}
}
