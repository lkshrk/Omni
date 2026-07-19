package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseFrontmatterDescription(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name: "block scalar multi-paragraph",
			content: `---
name: my-skill
description: |
  First line of description.
  Second line continues here.

  A new paragraph after a blank line.
agents: [claude-code]
---
body`,
			want: "First line of description. Second line continues here. A new paragraph after a blank line.",
		},
		{
			name: "quoted single-line",
			content: `---
name: my-skill
description: "A short quoted description."
---
body`,
			want: "A short quoted description.",
		},
		{
			name: "bare unquoted scalar",
			content: `---
name: my-skill
description: A bare description
---
body`,
			want: "A bare description",
		},
		{
			name:    "missing frontmatter delimiters",
			content: "name: my-skill\ndescription: nope\n",
			want:    "",
		},
		{
			name: "missing description key",
			content: `---
name: my-skill
agents: [claude-code]
---
body`,
			want: "",
		},
		{
			name: "unterminated frontmatter",
			content: `---
name: my-skill
description: "unterminated"
`,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseFrontmatterDescription(tc.content); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSkillDescription_MissingFile(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	if got := skillDescription(home, "nonexistent-skill"); got != "" {
		t.Errorf("got %q, want empty for missing file", got)
	}
}

func TestSkillDescription_ReadsFromDisk(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, ".agents", "skills", "my-skill")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: my-skill\ndescription: \"On-disk description.\"\n---\nbody"
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := skillDescription(home, "my-skill"); got != "On-disk description." {
		t.Errorf("got %q, want %q", got, "On-disk description.")
	}
}
