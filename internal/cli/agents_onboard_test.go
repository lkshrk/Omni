package cli

import "testing"

func TestParseOnboardResolutions(t *testing.T) {
	got, err := parseOnboardResolutions([]string{"item=future-agent"}, []string{"item:/env/TOKEN=API_TOKEN"}, []string{"item=bin/run"}, []string{"skip"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovedTargets["item"][0] != "future-agent" || got.EnvBindings["item"]["/env/TOKEN"] != "API_TOKEN" || got.ApprovedExecutables["item"][0] != "bin/run" || !got.Excluded["skip"] {
		t.Fatalf("got=%#v", got)
	}
}
func TestParseOnboardResolutionsRejectsUnsafeValues(t *testing.T) {
	for _, args := range [][]string{{"item=bad/target"}, {"=codex"}} {
		if _, err := parseOnboardResolutions(args, nil, nil, nil); err == nil {
			t.Fatalf("accepted %v", args)
		}
	}
	if _, err := parseOnboardResolutions(nil, []string{"item:/x=bad-name"}, nil, nil); err == nil {
		t.Fatal("bad env accepted")
	}
	if _, err := parseOnboardResolutions(nil, nil, []string{"item=../run"}, nil); err == nil {
		t.Fatal("traversal accepted")
	}
}

func TestAgentsOnboardExposesProjectRoot(t *testing.T) {
	flag := newAgentsOnboardCmd(&rootState{}).Flags().Lookup("project-root")
	if flag == nil {
		t.Fatal("missing --project-root")
	}
}
