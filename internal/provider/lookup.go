package provider

import "strings"

// PackageLookupKeys — Exact package identifiers first so @scope/pkg works, then logical names and slash basenames for tap aliases.
func PackageLookupKeys(name, pkg string) []string {
	if pkg == "" {
		pkg = name
	}
	keys := make([]string, 0, 3)
	add := func(s string) {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			return
		}
		for _, existing := range keys {
			if existing == s {
				return
			}
		}
		keys = append(keys, s)
	}
	add(pkg)
	add(name)
	if strings.Contains(pkg, "/") {
		parts := strings.Split(pkg, "/")
		add(parts[len(parts)-1])
	}
	return keys
}

func LookupString(m map[string]string, keys []string) (string, bool) {
	for _, key := range keys {
		if v, ok := m[key]; ok {
			return v, true
		}
	}
	return "", false
}

func LookupInstalledEntry(m map[string]InstalledEntry, keys []string) InstalledEntry {
	for _, key := range keys {
		if entry, ok := m[key]; ok {
			return entry
		}
	}
	return InstalledEntry{}
}

func LookupInstalledMetadata(m map[string]InstalledMetadata, keys []string) (InstalledMetadata, bool) {
	for _, key := range keys {
		if entry, ok := m[key]; ok {
			return entry, true
		}
	}
	return InstalledMetadata{}, false
}
