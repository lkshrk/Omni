package app

import (
	"fmt"
	"regexp"
	"strings"
)

var ownerRepoRe = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// normalizeSkillSource turns user input (owner/repo, owner/repo@skill,
// owner/repo#ref, or a github URL/scp form) into a canonical owner/repo source
// and an optional ref. The @skill component is stripped — packages are tracked
// at the repo level.
func normalizeSkillSource(in string) (source, ref string, err error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return "", "", fmt.Errorf("empty skill source")
	}
	s = strings.TrimPrefix(s, "git@github.com:")
	for _, p := range []string{"https://github.com/", "http://github.com/", "github.com/"} {
		if strings.HasPrefix(s, p) {
			s = strings.TrimPrefix(s, p)
			break
		}
	}
	s = strings.TrimSuffix(s, ".git")
	treeIdx := strings.Index(s, "/tree/")
	hashIdx := strings.Index(s, "#")
	if treeIdx >= 0 && hashIdx >= 0 {
		return "", "", fmt.Errorf("ambiguous ref in %q: specify either /tree/<ref> or #<ref>, not both", in)
	}
	if treeIdx >= 0 {
		ref = strings.Trim(s[treeIdx+len("/tree/"):], "/")
		s = s[:treeIdx]
	}
	if hashIdx >= 0 {
		ref = s[hashIdx+1:]
		s = s[:hashIdx]
	}
	if i := strings.Index(s, "@"); i >= 0 {
		s = s[:i]
	}
	s = strings.Trim(s, "/")
	if !ownerRepoRe.MatchString(s) {
		return "", "", fmt.Errorf("cannot parse skill source %q (want owner/repo or a github URL)", in)
	}
	// "." and ".." match the owner/repo charset but are traversal-shaped, not
	// repo names; the string is handed as an argv element to the skills CLI
	// and used as a manifest key, so reject rather than trust downstream.
	owner, repo, _ := strings.Cut(s, "/")
	if owner == "." || owner == ".." || repo == "." || repo == ".." {
		return "", "", fmt.Errorf("invalid skill source %q: %q is not a valid owner/repo segment", in, s)
	}
	return s, ref, nil
}
