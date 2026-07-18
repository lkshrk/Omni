package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Group list fields whose entries are plain strings. Merge keeps the EARLIER
// definition for these (appendUniqueStrings/appendUniqueToolEntries skip
// values already seen), so the duplicate in the LATER file is the dead copy.
var optimizeStringListKeys = []string{"taps", "tools", "skills", "mcp_servers", "plugins", "marketplaces"}

// OptimizeRemoval records duplicate entries removed from one file.
// Key is the group list field ("dots", "tools", ...) or "group" when an
// entire name-only group shell was dropped after its entries deduped away.
type OptimizeRemoval struct {
	File  string   `json:"file"`
	Group string   `json:"group"`
	Key   string   `json:"key"`
	Names []string `json:"names"`
}

// OptimizeReport describes everything OptimizeIncludeChain removed (or, in
// dry-run, would remove).
type OptimizeReport struct {
	Removals []OptimizeRemoval `json:"removals"`
}

func (r *OptimizeReport) Empty() bool { return r == nil || len(r.Removals) == 0 }

func (r *OptimizeReport) TotalRemoved() int {
	if r == nil {
		return 0
	}
	n := 0
	for _, rem := range r.Removals {
		// Group-shell drops aren't duplicate entries removed; they're a
		// side effect of dedupe emptying a group down to its name. Only
		// count actual duplicate list/dot entries.
		if rem.Key == "group" {
			continue
		}
		n += len(rem.Names)
	}
	return n
}

type optimizeGroup struct {
	raw     map[string]json.RawMessage
	base    string
	touched bool
}

type optimizeFile struct {
	path    string
	raw     map[string]json.RawMessage
	groups  []optimizeGroup
	hasKey  bool // file had a "groups" key
	changed bool
}

func planOptimize(chain []routedFile) ([]*optimizeFile, *OptimizeReport, error) {
	files, err := parseOptimizeFiles(chain)
	if err != nil {
		return nil, nil, err
	}
	report := &OptimizeReport{}
	if err := dedupeDots(files, report); err != nil {
		return nil, nil, err
	}
	if err := dedupeStringLists(files, report); err != nil {
		return nil, nil, err
	}
	removeEmptiedGroups(files, report)
	return files, report, nil
}

func parseOptimizeFiles(chain []routedFile) ([]*optimizeFile, error) {
	files := make([]*optimizeFile, 0, len(chain))
	for _, rf := range chain {
		f := &optimizeFile{path: rf.path, raw: rf.raw}
		groupsRaw, ok := rf.raw["groups"]
		if ok && len(groupsRaw) > 0 && !isJSONNull(groupsRaw) {
			f.hasKey = true
			var arr []json.RawMessage
			if err := json.Unmarshal(groupsRaw, &arr); err != nil {
				return nil, fmt.Errorf("parsing groups in %q: %w", rf.path, err)
			}
			for _, g := range arr {
				obj := make(map[string]json.RawMessage)
				if err := json.Unmarshal(g, &obj); err != nil {
					return nil, fmt.Errorf("parsing group in %q: %w", rf.path, err)
				}
				var name string
				if nameRaw, ok := obj["name"]; ok {
					if err := json.Unmarshal(nameRaw, &name); err != nil {
						return nil, fmt.Errorf("parsing group name in %q: %w", rf.path, err)
					}
				}
				gc := GroupConfig{Name: name}
				f.groups = append(f.groups, optimizeGroup{raw: obj, base: gc.BaseName()})
			}
		}
		files = append(files, f)
	}
	return files, nil
}

func dotEntryName(raw json.RawMessage) (string, error) {
	var e struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return "", fmt.Errorf("parsing dot entry: %w", err)
	}
	return e.Name, nil
}

// dedupeDots removes from each file any group dot entry that a LATER file in
// merge order also defines: appendUniqueDotEntries replaces in place, so the
// later definition is the effective one and the earlier copy is dead.
func dedupeDots(files []*optimizeFile, report *OptimizeReport) error {
	for i, f := range files {
		for gi := range f.groups {
			g := &f.groups[gi]
			dotsRaw, ok := g.raw["dots"]
			if !ok {
				continue
			}
			laterNames := make(map[string]bool)
			for _, later := range files[i+1:] {
				for li := range later.groups {
					lg := &later.groups[li]
					if lg.base != g.base {
						continue
					}
					lRaw, ok := lg.raw["dots"]
					if !ok {
						continue
					}
					var lArr []json.RawMessage
					if err := json.Unmarshal(lRaw, &lArr); err != nil {
						return fmt.Errorf("parsing dots in %q: %w", later.path, err)
					}
					for _, d := range lArr {
						name, err := dotEntryName(d)
						if err != nil {
							return fmt.Errorf("%s: %w", later.path, err)
						}
						if name != "" {
							laterNames[name] = true
						}
					}
				}
			}
			if len(laterNames) == 0 {
				continue
			}
			var arr []json.RawMessage
			if err := json.Unmarshal(dotsRaw, &arr); err != nil {
				return fmt.Errorf("parsing dots in %q: %w", f.path, err)
			}
			kept := make([]json.RawMessage, 0, len(arr))
			var removed []string
			for _, d := range arr {
				name, err := dotEntryName(d)
				if err != nil {
					return fmt.Errorf("%s: %w", f.path, err)
				}
				if name != "" && laterNames[name] {
					removed = append(removed, name)
					continue
				}
				kept = append(kept, d)
			}
			if len(removed) == 0 {
				continue
			}
			setGroupRawList(g, "dots", kept)
			g.touched = true
			f.changed = true
			report.Removals = append(report.Removals, OptimizeRemoval{
				File: f.path, Group: g.base, Key: "dots", Names: removed,
			})
		}
	}
	return nil
}

// dedupeStringLists removes from each file any group list value an EARLIER
// file already defines: appendUniqueStrings keeps the first occurrence, so
// the later copy is dead. Removing the later copy also preserves merge order.
func dedupeStringLists(files []*optimizeFile, report *OptimizeReport) error {
	for _, key := range optimizeStringListKeys {
		seen := make(map[string]map[string]bool) // group base -> earlier values
		for _, f := range files {
			for gi := range f.groups {
				g := &f.groups[gi]
				listRaw, ok := g.raw[key]
				if !ok {
					continue
				}
				var values []string
				if err := json.Unmarshal(listRaw, &values); err != nil {
					return fmt.Errorf("parsing group %s in %q: %w", key, f.path, err)
				}
				prior := seen[g.base]
				kept := make([]string, 0, len(values))
				var removed []string
				for _, v := range values {
					trimmed := strings.TrimSpace(v)
					if trimmed != "" && prior[trimmed] {
						removed = append(removed, v)
						continue
					}
					kept = append(kept, v)
				}
				if seen[g.base] == nil {
					seen[g.base] = make(map[string]bool)
				}
				for _, v := range kept {
					if trimmed := strings.TrimSpace(v); trimmed != "" {
						seen[g.base][trimmed] = true
					}
				}
				if len(removed) == 0 {
					continue
				}
				if len(kept) == 0 {
					delete(g.raw, key)
				} else {
					b, err := json.Marshal(kept)
					if err != nil {
						return err
					}
					g.raw[key] = b
				}
				g.touched = true
				f.changed = true
				report.Removals = append(report.Removals, OptimizeRemoval{
					File: f.path, Group: g.base, Key: key, Names: removed,
				})
			}
		}
	}
	return nil
}

func setGroupRawList(g *optimizeGroup, key string, kept []json.RawMessage) {
	if len(kept) == 0 {
		delete(g.raw, key)
		return
	}
	b, err := json.Marshal(kept)
	if err != nil {
		return
	}
	g.raw[key] = b
}

// removeEmptiedGroups drops group objects that our dedupe reduced to a bare
// {"name": ...} shell. Pre-existing name-only groups (touched == false) stay.
func removeEmptiedGroups(files []*optimizeFile, report *OptimizeReport) {
	for _, f := range files {
		kept := f.groups[:0]
		for _, g := range f.groups {
			nameOnly := true
			for k := range g.raw {
				if k != "name" {
					nameOnly = false
					break
				}
			}
			if g.touched && nameOnly {
				f.changed = true
				report.Removals = append(report.Removals, OptimizeRemoval{
					File: f.path, Group: g.base, Key: "group", Names: []string{g.base},
				})
				continue
			}
			kept = append(kept, g)
		}
		f.groups = kept
	}
}

// OptimizeIncludeChain removes duplicate definitions across the $include
// chain: group dot entries shadowed by a later file, and group list values
// repeated from an earlier file. The merged config is provably unchanged —
// after writing, the chain is reloaded and compared (order-normalized)
// against the pre-fix merge; any difference restores the original files and
// returns an error. With dryRun, the report is computed and nothing written.
func OptimizeIncludeChain(mainPath string, dryRun bool) (*OptimizeReport, error) {
	before, err := Load(mainPath)
	if err != nil {
		return nil, fmt.Errorf("config optimize: load: %w", err)
	}
	beforeNorm, err := normalizeForCompare(before)
	if err != nil {
		return nil, fmt.Errorf("config optimize: normalize: %w", err)
	}
	chain, err := loadIncludeChain(mainPath)
	if err != nil {
		return nil, fmt.Errorf("config optimize: %w", err)
	}
	files, report, err := planOptimize(chain)
	if err != nil {
		return nil, fmt.Errorf("config optimize: %w", err)
	}
	if report.Empty() || dryRun {
		return report, nil
	}

	originals := make(map[string][]byte)
	for _, f := range files {
		if !f.changed {
			continue
		}
		data, err := os.ReadFile(f.path)
		if err != nil {
			return nil, fmt.Errorf("config optimize: snapshot %q: %w", f.path, err)
		}
		originals[f.path] = data
	}

	restore := func() error {
		var failed []string
		for path, data := range originals {
			if err := atomicWrite(path, data); err != nil {
				failed = append(failed, fmt.Sprintf("%s: %v", path, err))
			}
		}
		if len(failed) > 0 {
			return fmt.Errorf("RESTORE FAILED for %d file(s), config may be in a partially-optimized state; inspect: %s", len(failed), strings.Join(failed, "; "))
		}
		return nil
	}

	for i, f := range files {
		if !f.changed {
			continue
		}
		patch, err := renderGroupsPatch(f)
		if err != nil {
			if restoreErr := restore(); restoreErr != nil {
				return nil, fmt.Errorf("config optimize aborted: render: %w: %w", err, restoreErr)
			}
			return nil, fmt.Errorf("config optimize: %w", err)
		}
		if i == 0 {
			err = PatchRaw(f.path, patch)
		} else {
			err = patchFragmentRaw(f.path, f.raw, patch)
		}
		if err != nil {
			if restoreErr := restore(); restoreErr != nil {
				return nil, fmt.Errorf("config optimize aborted: write %q: %w: %w", f.path, err, restoreErr)
			}
			return nil, fmt.Errorf("config optimize: write %q: %w", f.path, err)
		}
	}

	after, err := Load(mainPath)
	if err != nil {
		if restoreErr := restore(); restoreErr != nil {
			return nil, fmt.Errorf("config optimize aborted: reload: %w: %w", err, restoreErr)
		}
		return nil, fmt.Errorf("config optimize aborted, files restored: reload: %w", err)
	}
	afterNorm, err := normalizeForCompare(after)
	if err != nil {
		if restoreErr := restore(); restoreErr != nil {
			return nil, fmt.Errorf("config optimize aborted: normalize: %w: %w", err, restoreErr)
		}
		return nil, fmt.Errorf("config optimize aborted, files restored: normalize: %w", err)
	}
	if !bytes.Equal(beforeNorm, afterNorm) {
		if restoreErr := restore(); restoreErr != nil {
			return nil, fmt.Errorf("config optimize aborted: merged config would change: %w", restoreErr)
		}
		return nil, fmt.Errorf("config optimize aborted: merged config would change; original files restored")
	}
	return report, nil
}

func renderGroupsPatch(f *optimizeFile) (map[string]json.RawMessage, error) {
	if len(f.groups) == 0 {
		return map[string]json.RawMessage{"groups": json.RawMessage("null")}, nil
	}
	arr := make([]json.RawMessage, 0, len(f.groups))
	for _, g := range f.groups {
		b, err := json.Marshal(g.raw)
		if err != nil {
			return nil, err
		}
		arr = append(arr, b)
	}
	b, err := json.Marshal(arr)
	if err != nil {
		return nil, err
	}
	return map[string]json.RawMessage{"groups": b}, nil
}

// normalizeForCompare renders a merged config with insignificant ordering
// removed: groups sorted by base name, and each group's dots and string
// lists sorted. Dedupe may reorder entries within these lists; ordering
// there carries no behavior (merge itself dedupes by name).
func normalizeForCompare(cfg *RootConfig) ([]byte, error) {
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, err
	}
	var clone RootConfig
	if err := json.Unmarshal(data, &clone); err != nil {
		return nil, err
	}
	// "$schema" is editor metadata PatchRaw stamps on every write
	// regardless of patch content (see PatchRaw); it carries no settings
	// semantics, so it must not participate in the equivalence check.
	clone.Schema = ""
	sort.SliceStable(clone.Groups, func(i, j int) bool {
		return clone.Groups[i].BaseName() < clone.Groups[j].BaseName()
	})
	for _, g := range clone.Groups {
		if g == nil {
			continue
		}
		sort.SliceStable(g.Dots, func(i, j int) bool { return g.Dots[i].Name < g.Dots[j].Name })
		sort.SliceStable(g.Tools, func(i, j int) bool { return g.Tools[i].Name < g.Tools[j].Name })
		sort.Strings(g.Taps)
		sort.Strings(g.Skills)
		sort.Strings(g.McpServers)
		sort.Strings(g.Plugins)
		sort.Strings(g.Marketplaces)
	}
	return json.Marshal(&clone)
}
