package app

import (
	"context"
	"testing"
)

func TestParseFindOutput(t *testing.T) {
	t.Parallel()
	out := "" +
		"vercel-labs/agent-skills@find-skills  1.2k installs\n" +
		"└ https://skills.sh/find-skills\n" +
		"acme/tools@deploy  340 installs\n" +
		"└ https://skills.sh/deploy\n"
	got := parseFindOutput(out)
	if len(got) != 2 {
		t.Fatalf("got %d results, want 2: %+v", len(got), got)
	}
	if got[0].Source != "vercel-labs/agent-skills" || got[0].Skill != "find-skills" {
		t.Errorf("result[0] = %+v", got[0])
	}
	if got[0].Installs != "1.2k installs" {
		t.Errorf("installs[0] = %q", got[0].Installs)
	}
	if got[1].Source != "acme/tools" || got[1].Skill != "deploy" {
		t.Errorf("result[1] = %+v", got[1])
	}
}

func TestParseFindOutputEmpty(t *testing.T) {
	t.Parallel()
	if got := parseFindOutput("No skills found.\n"); len(got) != 0 {
		t.Errorf("want 0 results, got %+v", got)
	}
}

func TestFindSkillPackagesParsesRunnerOutput(t *testing.T) {
	t.Parallel()
	var gotName string
	var gotArgs []string
	fakeExec := func(_ context.Context, name string, args ...string) (string, string, error) {
		gotName, gotArgs = name, args
		return "owner/repo@skill  5 installs\n└ https://skills.sh/skill\n", "", nil
	}
	res, err := findSkillPackages(context.Background(), fakeExec, "npx", "query words")
	if err != nil {
		t.Fatal(err)
	}
	if gotName != "npx" {
		t.Errorf("runner = %q", gotName)
	}
	want := []string{"skills", "find", "query", "words"}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %v want %v", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("args = %v want %v", gotArgs, want)
		}
	}
	if len(res) != 1 || res[0].Source != "owner/repo" {
		t.Fatalf("results = %+v", res)
	}
}
