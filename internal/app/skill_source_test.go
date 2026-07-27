package app

import (
	"path/filepath"
	"testing"
)

func TestParseSkillPackage(t *testing.T) {
	t.Parallel()
	a := &App{ConfigPath: filepath.Join(t.TempDir(), "settings.json")}
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
		got, err := a.parseSkillPackage(in)
		if err != nil {
			t.Fatalf("%q: unexpected error %v", in, err)
		}
		if got.Source != want.source || got.Ref != want.ref {
			t.Errorf("%q -> (%q,%q), want (%q,%q)", in, got.Source, got.Ref, want.source, want.ref)
		}
	}
	for _, bad := range []string{"", "   ", "notaurl", "owner", "owner/", "https://github.com/owner/repo/tree/main#v2"} {
		if _, err := a.parseSkillPackage(bad); err == nil {
			t.Errorf("%q: expected error, got nil", bad)
		}
	}
}

func TestParseSkillPackageRejectsTraversalSegments(t *testing.T) {
	t.Parallel()
	a := &App{ConfigPath: filepath.Join(t.TempDir(), "settings.json")}
	for _, in := range []string{"owner/..", "owner/."} {
		if _, err := a.parseSkillPackage(in); err == nil {
			t.Errorf("parseSkillPackage(%q) = nil error, want traversal rejection", in)
		}
	}
	if _, err := a.parseSkillPackage("dot.owner/dot.repo"); err != nil {
		t.Errorf("dotted-but-valid segments rejected: %v", err)
	}
}

func TestSkillSourceResolvesAgainstConfigDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	a := &App{ConfigPath: filepath.Join(dir, "settings.json")}
	want := filepath.Join(dir, "skills")

	got, err := a.parseSkillPackage("./skills")
	if err != nil || got.Source != want {
		t.Fatalf("parseSkillPackage(./skills) = %q, want %q: %v", got.Source, want, err)
	}
	if id := a.skillSourceIdentity("./skills"); id != want {
		t.Errorf("skillSourceIdentity(./skills) = %q, want %q", id, want)
	}
	if id := a.skillSourceIdentity(want); id != want {
		t.Errorf("skillSourceIdentity(%q) = %q, want unchanged", want, id)
	}
	if id := a.skillSourceIdentity("../skills"); id != filepath.Join(filepath.Dir(dir), "skills") {
		t.Errorf("skillSourceIdentity(../skills) = %q", id)
	}
}
