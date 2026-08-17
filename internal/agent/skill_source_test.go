package agent

import (
	"reflect"
	"testing"
)

func TestParseSkillSource(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  SkillSource
	}{
		{
			name:  "github shorthand ref selector",
			input: "owner/repo#v1.2.3@review",
			want:  SkillSource{Source: "owner/repo", Ref: "v1.2.3", Skills: []string{"review"}},
		},
		{
			name:  "github ssh",
			input: "git@github.com:owner/repo.git#main@test",
			want:  SkillSource{Source: "owner/repo", Ref: "main", Skills: []string{"test"}},
		},
		{
			name:  "github tree subpath",
			input: "https://github.com/owner/repo/tree/dev/skills/review@review",
			want:  SkillSource{Source: "owner/repo/skills/review", Ref: "dev", Skills: []string{"review"}},
		},
		{
			name:  "github shorthand subpath",
			input: "owner/repo/skills/review@review",
			want:  SkillSource{Source: "owner/repo/skills/review", Skills: []string{"review"}},
		},
		{
			name:  "gitlab url remains locator",
			input: "https://gitlab.com/group/repo.git#release",
			want:  SkillSource{Source: "https://gitlab.com/group/repo.git", Ref: "release"},
		},
		{
			name:  "gitlab tree subpath",
			input: "https://gitlab.com/group/repo/-/tree/release/skills/review@review",
			want: SkillSource{
				Source: "https://gitlab.com/group/repo.git/skills/review",
				Ref:    "release",
				Skills: []string{"review"},
			},
		},
		{
			name:  "self hosted gitlab tree subpath",
			input: "https://gitlab.example/group/repo/-/tree/release/skills/review@review",
			want: SkillSource{
				Source: "https://gitlab.example/group/repo.git/skills/review",
				Ref:    "release",
				Skills: []string{"review"},
			},
		},
		{
			name:  "github enterprise tree subpath",
			input: "https://ghe.example/owner/repo/tree/main/skills/review@review",
			want: SkillSource{
				Source: "https://ghe.example/owner/repo.git/skills/review",
				Ref:    "main",
				Skills: []string{"review"},
			},
		},
		{
			name:  "scp locator without a path separator",
			input: "git@example.com:myrepo",
			want:  SkillSource{Source: "git@example.com:myrepo"},
		},
		{
			name:  "scp locator without a path separator and a skill selector",
			input: "git@example.com:myrepo@review",
			want:  SkillSource{Source: "git@example.com:myrepo", Skills: []string{"review"}},
		},
		{
			name:  "generic ssh",
			input: "ssh://git@git.example.com/team/skills.git#main",
			want:  SkillSource{Source: "ssh://git@git.example.com/team/skills.git", Ref: "main"},
		},
		{
			name:  "well known http",
			input: "https://skills.example.com/team@review",
			want:  SkillSource{Source: "https://skills.example.com/team", Skills: []string{"review"}},
		},
		{
			name:  "local relative",
			input: "./testdata/skills@review",
			want:  SkillSource{Source: "./testdata/skills", Skills: []string{"review"}},
		},
		{
			name:  "file url",
			input: "file:///tmp/skills#dev@review",
			want:  SkillSource{Source: "file:///tmp/skills", Ref: "dev", Skills: []string{"review"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseSkillSource(tt.input)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ParseSkillSource(%q) = %+v, want %+v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseSkillSourceRejectsUnsafeOrUnsupportedInputs(t *testing.T) {
	for _, input := range []string{
		"",
		"ext::sh -c evil",
		"ftp://example.com/skills",
		"owner/../repo",
		"owner/repo#",
		"owner/repo#--upload-pack=evil",
		"owner/repo@../escape",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := ParseSkillSource(input); err == nil {
				t.Fatalf("ParseSkillSource(%q) succeeded", input)
			}
		})
	}
}

func TestSourceRequiresGitForHostedRepositorySubpaths(t *testing.T) {
	t.Parallel()
	for _, source := range []string{
		"https://ghe.example/owner/repo.git/skills/review",
		"https://gitlab.example/group/repo.git/skills/review",
	} {
		if !SourceRequiresGit(source) {
			t.Errorf("SourceRequiresGit(%q) = false", source)
		}
	}
	if SourceRequiresGit("https://skills.example/.well-known") {
		t.Fatal("well-known HTTP source unexpectedly requires git")
	}
}
