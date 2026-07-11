package app

import "testing"

func TestNormalizeSkillSource(t *testing.T) {
	cases := map[string]struct{ source, ref string }{
		"owner/repo":                              {"owner/repo", ""},
		"owner/repo#main":                         {"owner/repo", "main"},
		"owner/repo@some-skill":                   {"owner/repo", ""},
		"owner/repo@some-skill#v2":                {"owner/repo", "v2"},
		"https://github.com/owner/repo":           {"owner/repo", ""},
		"https://github.com/owner/repo.git":       {"owner/repo", ""},
		"https://github.com/owner/repo/tree/main": {"owner/repo", "main"},
		"git@github.com:owner/repo.git":           {"owner/repo", ""},
		"  owner/repo  ":                          {"owner/repo", ""},
	}
	for in, want := range cases {
		gotSource, gotRef, err := normalizeSkillSource(in)
		if err != nil {
			t.Fatalf("%q: unexpected error %v", in, err)
		}
		if gotSource != want.source || gotRef != want.ref {
			t.Errorf("%q -> (%q,%q), want (%q,%q)", in, gotSource, gotRef, want.source, want.ref)
		}
	}
	for _, bad := range []string{"", "   ", "notaurl", "owner", "/repo", "owner/", "https://github.com/owner/repo/tree/main#v2"} {
		if _, _, err := normalizeSkillSource(bad); err == nil {
			t.Errorf("%q: expected error, got nil", bad)
		}
	}
}
