package agent

import (
	"fmt"
	"net/url"
	"path/filepath"
	"slices"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

type SkillSource struct {
	Source string
	Ref    string
	Skills []string
}

func ParseSkillSource(input string) (SkillSource, error) {
	raw := strings.TrimSpace(input)
	if raw == "" {
		return SkillSource{}, fmt.Errorf("skill source is required")
	}
	if strings.Contains(raw, "::") {
		return SkillSource{}, fmt.Errorf("git remote helpers are not supported")
	}

	var skills []string
	at := strings.LastIndex(raw, "@")
	hash := strings.LastIndex(raw, "#")
	if at < scpLocatorEnd(raw) {
		at = -1
	}
	if at > strings.LastIndex(raw, "/") {
		end := len(raw)
		if hash > at {
			end = hash
		}
		skill := raw[at+1 : end]
		if !validSkillName(skill) {
			return SkillSource{}, fmt.Errorf("invalid skill selector %q", skill)
		}
		skills = []string{skill}
		raw = raw[:at] + raw[end:]
	}

	var ref string
	if hash := strings.LastIndex(raw, "#"); hash >= 0 {
		ref = strings.TrimSpace(raw[hash+1:])
		if ref == "" || strings.Contains(ref, "#") {
			return SkillSource{}, fmt.Errorf("invalid Git ref")
		}
		raw = raw[:hash]
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return SkillSource{}, fmt.Errorf("skill source is required")
	}

	source, treeRef, err := normalizeSourceLocator(raw)
	if err != nil {
		return SkillSource{}, err
	}
	if ref == "" {
		ref = treeRef
	} else if treeRef != "" && ref != treeRef {
		return SkillSource{}, fmt.Errorf("conflicting Git refs %q and %q", treeRef, ref)
	}
	if ref != "" && !validGitRef(ref) {
		return SkillSource{}, fmt.Errorf("invalid Git ref %q", ref)
	}
	return SkillSource{Source: source, Ref: ref, Skills: skills}, nil
}

func normalizeSkillPackage(pkg config.SkillPackage) (config.SkillPackage, error) {
	parsed, err := ParseSkillSource(pkg.Source)
	if err != nil {
		return config.SkillPackage{}, err
	}
	pkg.Source = parsed.Source
	if parsed.Ref != "" {
		if pkg.Ref != "" && pkg.Ref != parsed.Ref {
			return config.SkillPackage{}, fmt.Errorf("conflicting Git refs %q and %q", parsed.Ref, pkg.Ref)
		}
		pkg.Ref = parsed.Ref
	}
	if pkg.Ref != "" && !validGitRef(pkg.Ref) {
		return config.SkillPackage{}, fmt.Errorf("invalid Git ref %q", pkg.Ref)
	}
	for _, name := range append(parsed.Skills, pkg.Skills...) {
		if !validSkillName(name) {
			return config.SkillPackage{}, fmt.Errorf("invalid skill selector %q", name)
		}
		if !slices.Contains(pkg.Skills, name) {
			pkg.Skills = append(pkg.Skills, name)
		}
	}
	return pkg, nil
}

// scpLocatorEnd offsets past the colon of a user@host: locator, or 0 for anything else; the '@' in one is never a skill selector.
func scpLocatorEnd(raw string) int {
	at := strings.Index(raw, "@")
	if at <= 0 || strings.ContainsAny(raw[:at], "/:") {
		return 0
	}
	colon := strings.Index(raw[at+1:], ":")
	if colon <= 0 || strings.ContainsAny(raw[at+1:at+1+colon], "/@") {
		return 0
	}
	return at + colon + 2
}

func normalizeSourceLocator(raw string) (string, string, error) {
	if strings.HasPrefix(raw, "git@github.com:") {
		return normalizeGitHubPath(strings.TrimPrefix(raw, "git@github.com:"))
	}
	// url.Parse rejects the colon in an scp locator's host, so it never reaches the scp branch below.
	if scpLocatorEnd(raw) > 0 {
		return raw, "", nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("invalid skill source: %w", err)
	}
	if u.Scheme != "" {
		switch strings.ToLower(u.Scheme) {
		case "https", "http", "ssh", "git", "file":
		default:
			return "", "", fmt.Errorf("unsupported skill source protocol %q", u.Scheme)
		}
		if strings.EqualFold(u.Hostname(), "github.com") {
			return normalizeGitHubPath(strings.TrimPrefix(u.Path, "/"))
		}
		if marker := strings.Index(u.Path, "/-/tree/"); marker >= 0 {
			return normalizeHostedTreeURL(u, raw, marker, "/-/tree/")
		}
		if marker := strings.Index(u.Path, "/tree/"); marker >= 0 {
			return normalizeHostedTreeURL(u, raw, marker, "/tree/")
		}
		return strings.TrimRight(raw, "/"), "", nil
	}

	if strings.HasPrefix(raw, "./") || strings.HasPrefix(raw, "."+string(filepath.Separator)) {
		clean := filepath.Clean(raw)
		if clean == "." {
			return "", "", fmt.Errorf("skill source must name a directory")
		}
		return "./" + filepath.ToSlash(clean), "", nil
	}
	if strings.HasPrefix(raw, ".") || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "~") {
		return filepath.Clean(raw), "", nil
	}
	if strings.Contains(raw, ":") {
		return raw, "", nil // scp-style Git locator
	}
	return normalizeGitHubPath(raw)
}

func normalizeHostedTreeURL(u *url.URL, raw string, marker int, separator string) (string, string, error) {
	repository := strings.Trim(u.Path[:marker], "/")
	if len(strings.Split(repository, "/")) < 2 {
		return "", "", fmt.Errorf("invalid hosted Git tree source %q", raw)
	}
	remainder := strings.TrimPrefix(u.Path[marker+len(separator):], "/")
	parts := strings.Split(remainder, "/")
	if len(parts) == 0 || parts[0] == "" {
		return "", "", fmt.Errorf("invalid hosted Git tree source %q", raw)
	}
	repoPath := "/" + strings.TrimSuffix(repository, ".git") + ".git"
	if len(parts) > 1 {
		repoPath += "/" + strings.Join(parts[1:], "/")
	}
	u.Path = repoPath
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), parts[0], nil
}

func normalizeGitHubPath(raw string) (string, string, error) {
	parts := strings.Split(strings.Trim(raw, "/"), "/")
	if len(parts) < 2 || invalidSourceSegments(parts) {
		return "", "", fmt.Errorf("invalid GitHub skill source %q", raw)
	}
	parts[1] = strings.TrimSuffix(parts[1], ".git")
	if parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid GitHub skill source %q", raw)
	}
	var ref string
	if len(parts) >= 4 && parts[2] == "tree" {
		ref = parts[3]
		parts = append(parts[:2], parts[4:]...)
	}
	return strings.Join(parts, "/"), ref, nil
}

func invalidSourceSegments(parts []string) bool {
	for _, part := range parts {
		if part == "" || part == "." || part == ".." || strings.ContainsAny(part, "\\@") {
			return true
		}
	}
	return false
}

func validSkillName(name string) bool {
	if len(name) == 0 || len(name) > 64 || name[0] == '-' || name[len(name)-1] == '-' || strings.Contains(name, "--") {
		return false
	}
	for _, char := range name {
		if (char < 'a' || char > 'z') && (char < '0' || char > '9') && char != '-' {
			return false
		}
	}
	return true
}

func validGitRef(ref string) bool {
	if ref == "" || strings.HasPrefix(ref, "-") || strings.HasSuffix(ref, "/") ||
		strings.Contains(ref, "..") || strings.Contains(ref, "@{") ||
		strings.Contains(ref, "//") {
		return false
	}
	for _, char := range ref {
		if char <= ' ' || char == 0x7f || strings.ContainsRune(`~^:?*[\`, char) {
			return false
		}
	}
	for _, part := range strings.Split(ref, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".") ||
			strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}

func SourceRequiresGit(source string) bool {
	if strings.HasPrefix(source, ".") || strings.HasPrefix(source, "/") || strings.HasPrefix(source, "~") || strings.HasPrefix(source, "file://") {
		return false
	}
	if !strings.Contains(source, "://") {
		return true
	}
	u, err := url.Parse(source)
	if err != nil {
		return true
	}
	switch u.Scheme {
	case "ssh", "git":
		return true
	case "http", "https":
		host := strings.ToLower(u.Hostname())
		path := strings.ToLower(u.Path)
		return strings.HasSuffix(path, ".git") || strings.Contains(path, ".git/") ||
			strings.Contains(host, "github") ||
			strings.Contains(host, "gitlab")
	default:
		return false
	}
}
