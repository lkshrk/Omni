package cli

import "testing"

func TestParseOnboardResolutions(t *testing.T) {
	got, err := parseOnboardResolutions([]string{"item=codex"}, []string{"item:/env/TOKEN=API_TOKEN"}, []string{"item=bin/run"}, []string{"skip"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ApprovedTargets["item"][0] != "codex" || got.EnvBindings["item"]["/env/TOKEN"] != "API_TOKEN" || got.ApprovedExecutables["item"][0] != "bin/run" || !got.Excluded["skip"] {
		t.Fatalf("got=%#v", got)
	}
}
func TestParseOnboardResolutionsRejectsBroadOrUnsafeValues(t *testing.T) {
	for _, args := range [][]string{{"item=cursor"}, {"=codex"}} {
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
