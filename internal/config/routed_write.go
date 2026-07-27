package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Ordered as loadIncludes applies them: main first, fragments after.
type routedFile struct {
	path string
	raw  map[string]json.RawMessage
}

// Missing fragment files abort: routing a write into a file the load path could not read would hide the problem.
func loadIncludeChain(mainPath string) ([]routedFile, error) {
	resolved := mainPath
	if writePath, err := resolveConfigWritePath(mainPath); err == nil {
		resolved = writePath
	}
	var stack includePathStack
	return loadIncludeChainFrom(resolved, &stack)
}

func loadIncludeChainFrom(path string, stack *includePathStack) ([]routedFile, error) {
	if err := stack.push(path); err != nil {
		return nil, err
	}
	defer stack.pop()

	raw := make(map[string]json.RawMessage)
	if data, err := os.ReadFile(path); err == nil {
		if err := unmarshalJSONObject(data, &raw); err != nil {
			return nil, fmt.Errorf("parsing config %q: %w", path, err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("reading config %q: %w", path, err)
	}
	chain := []routedFile{{path: path, raw: raw}}
	var includes []string
	if includeRaw := raw["$include"]; len(includeRaw) > 0 {
		if err := json.Unmarshal(includeRaw, &includes); err != nil {
			return nil, fmt.Errorf("parsing config includes in %q: %w", path, err)
		}
	}
	for _, include := range includes {
		include = strings.TrimSpace(include)
		if include == "" {
			continue
		}
		includePath := include
		if !filepath.IsAbs(includePath) {
			includePath = filepath.Join(filepath.Dir(path), include)
		}
		if resolvedInclude, err := resolveConfigWritePath(includePath); err == nil {
			includePath = resolvedInclude
		}
		sub, err := loadIncludeChainFrom(includePath, stack)
		if err != nil {
			return nil, err
		}
		chain = append(chain, sub...)
	}
	return chain, nil
}

// PatchRawRouted — Each key goes to the last fragment defining it (mirroring merge order), unowned keys to the main file, with "groups" split at group-field granularity.
func PatchRawRouted(path string, patch map[string]json.RawMessage) error {
	chain, err := loadIncludeChain(path)
	if err != nil {
		return err
	}
	if len(chain) == 1 {
		return PatchRaw(path, patch)
	}
	mainPatch := make(map[string]json.RawMessage)
	fragmentPatches := make(map[int]map[string]json.RawMessage)
	addFragmentPatch := func(idx int, key string, value json.RawMessage) {
		if fragmentPatches[idx] == nil {
			fragmentPatches[idx] = make(map[string]json.RawMessage)
		}
		fragmentPatches[idx][key] = value
	}

	keys := make([]string, 0, len(patch))
	for key := range patch {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := patch[key]
		if key == "groups" && countChainFilesWithKey(chain, "groups") > 0 {
			perFile, err := routeGroupsPatch(chain, value)
			if err != nil {
				return err
			}
			for idx, groupsValue := range perFile {
				if idx == 0 {
					mainPatch["groups"] = groupsValue
					continue
				}
				addFragmentPatch(idx, "groups", groupsValue)
			}
			continue
		}
		owner := 0
		for idx := 1; idx < len(chain); idx++ {
			if _, ok := chain[idx].raw[key]; ok {
				owner = idx
			}
		}
		if owner == 0 {
			mainPatch[key] = value
			continue
		}
		addFragmentPatch(owner, key, value)
		// The patch is the full merged section, so stale copies elsewhere in the chain would union-merge back and resurrect removed entries.
		if _, ok := chain[0].raw[key]; ok {
			mainPatch[key] = json.RawMessage("null")
		}
		for idx := 1; idx < len(chain); idx++ {
			if idx == owner {
				continue
			}
			if _, ok := chain[idx].raw[key]; ok {
				addFragmentPatch(idx, key, json.RawMessage("null"))
			}
		}
	}

	for idx := 1; idx < len(chain); idx++ {
		fragmentPatch := fragmentPatches[idx]
		if len(fragmentPatch) == 0 {
			continue
		}
		if err := patchFragmentRaw(chain[idx].path, chain[idx].raw, fragmentPatch); err != nil {
			return err
		}
	}
	if len(mainPatch) == 0 {
		return nil
	}
	return PatchRaw(path, mainPatch)
}

func countChainFilesWithKey(chain []routedFile, key string) int {
	count := 0
	for idx := 1; idx < len(chain); idx++ {
		if _, ok := chain[idx].raw[key]; ok {
			count++
		}
	}
	return count
}

// Deliberately stamps no $schema or version: fragments are partial documents owned by their parent config.
func patchFragmentRaw(path string, raw map[string]json.RawMessage, patch map[string]json.RawMessage) error {
	rendered := renderFragmentRaw(raw, patch)
	if current, err := os.ReadFile(path); err == nil && bytes.Equal(current, rendered) {
		return nil
	}
	return atomicWrite(path, rendered)
}

func renderFragmentRaw(raw map[string]json.RawMessage, patch map[string]json.RawMessage) []byte {
	merged := make(map[string]json.RawMessage, len(raw)+len(patch))
	for key, value := range raw {
		merged[key] = value
	}
	for key, value := range patch {
		if isJSONNull(value) {
			delete(merged, key)
			continue
		}
		merged[key] = value
	}
	keys := make([]string, 0, len(merged))
	for key := range merged {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteString("{")
	for i, key := range keys {
		if i > 0 {
			buf.WriteString(",")
		}
		buf.WriteString("\n  ")
		keyJSON, _ := json.Marshal(key)
		buf.Write(keyJSON)
		buf.WriteString(": ")
		var indented bytes.Buffer
		if err := json.Indent(&indented, merged[key], "  ", "  "); err == nil {
			buf.Write(indented.Bytes())
		} else {
			buf.Write(merged[key])
		}
	}
	buf.WriteString("\n}\n")
	return buf.Bytes()
}

// groupFieldNames are the GroupConfig fields routed independently per group.
var groupFieldNames = []string{"taps", "tools", "dots", "skills", "mcp_servers", "plugins", "marketplaces", "description", "special"}

type groupsOwnership struct {
	declared map[string]int            // group name -> last file declaring it
	field    map[string]map[string]int // group name -> field -> owning file
	dotsHome int                       // last file carrying any group dots
	home     int                       // last file carrying the groups key
}

func chainGroupsOwnership(chain []routedFile) (groupsOwnership, error) {
	owner := groupsOwnership{
		declared: make(map[string]int),
		field:    make(map[string]map[string]int),
	}
	for idx, file := range chain {
		groupsRaw, ok := file.raw["groups"]
		if !ok {
			continue
		}
		if idx > 0 {
			owner.home = idx
		}
		var groups []*GroupConfig
		if err := json.Unmarshal(groupsRaw, &groups); err != nil {
			return owner, fmt.Errorf("parsing groups in %q: %w", file.path, err)
		}
		for _, group := range groups {
			if group == nil || strings.TrimSpace(group.Name) == "" {
				continue
			}
			owner.declared[group.Name] = idx
			if owner.field[group.Name] == nil {
				owner.field[group.Name] = make(map[string]int)
			}
			for _, fieldName := range groupFieldNames {
				if groupFieldEmpty(group, fieldName) {
					continue
				}
				owner.field[group.Name][fieldName] = idx
				if fieldName == "dots" && idx > 0 {
					owner.dotsHome = idx
				}
			}
		}
	}
	if owner.dotsHome == 0 {
		owner.dotsHome = owner.home
	}
	return owner, nil
}

func groupFieldEmpty(group *GroupConfig, fieldName string) bool {
	switch fieldName {
	case "taps":
		return len(group.Taps) == 0
	case "tools":
		return len(group.Tools) == 0
	case "dots":
		return len(group.Dots) == 0
	case "skills":
		return len(group.Skills) == 0
	case "mcp_servers":
		return len(group.McpServers) == 0
	case "plugins":
		return len(group.Plugins) == 0
	case "marketplaces":
		return len(group.Marketplaces) == 0
	case "description":
		return strings.TrimSpace(group.Description) == ""
	case "special":
		return strings.TrimSpace(group.Special) == ""
	}
	return true
}

func copyGroupField(dst, src *GroupConfig, fieldName string) {
	switch fieldName {
	case "taps":
		dst.Taps = src.Taps
	case "tools":
		dst.Tools = src.Tools
	case "dots":
		dst.Dots = src.Dots
	case "skills":
		dst.Skills = src.Skills
	case "mcp_servers":
		dst.McpServers = src.McpServers
	case "plugins":
		dst.Plugins = src.Plugins
	case "marketplaces":
		dst.Marketplaces = src.Marketplaces
	case "description":
		dst.Description = src.Description
	case "special":
		dst.Special = src.Special
	}
}

// Each group field stays in the file that defines it today; files whose projection ends up empty get JSON null (key removal), and index 0 is main.
func routeGroupsPatch(chain []routedFile, newGroupsRaw json.RawMessage) (map[int]json.RawMessage, error) {
	owner, err := chainGroupsOwnership(chain)
	if err != nil {
		return nil, err
	}
	if isJSONNull(newGroupsRaw) {
		result := make(map[int]json.RawMessage)
		for idx, file := range chain {
			if _, ok := file.raw["groups"]; ok {
				result[idx] = json.RawMessage("null")
			}
		}
		return result, nil
	}
	var newGroups []*GroupConfig
	if err := json.Unmarshal(newGroupsRaw, &newGroups); err != nil {
		return nil, fmt.Errorf("parsing groups patch: %w", err)
	}
	projections := make(map[int][]*GroupConfig)
	for _, group := range newGroups {
		if group == nil || strings.TrimSpace(group.Name) == "" {
			continue
		}
		declaringFile, declaredKnown := owner.declared[group.Name]
		if !declaredKnown {
			declaringFile = owner.home
		}
		perFile := make(map[int]*GroupConfig)
		fileGroup := func(idx int) *GroupConfig {
			if perFile[idx] == nil {
				perFile[idx] = &GroupConfig{Name: group.Name}
			}
			return perFile[idx]
		}
		assigned := false
		for _, fieldName := range groupFieldNames {
			if groupFieldEmpty(group, fieldName) {
				continue
			}
			target, ok := owner.field[group.Name][fieldName]
			if !ok {
				if fieldName == "dots" {
					target = owner.dotsHome
				} else {
					target = declaringFile
				}
			}
			copyGroupField(fileGroup(target), group, fieldName)
			assigned = true
		}
		if !assigned {
			// A group with only a name still needs a declaring home.
			fileGroup(declaringFile)
		}
		for idx, projected := range perFile {
			projections[idx] = append(projections[idx], projected)
		}
	}

	result := make(map[int]json.RawMessage)
	for idx, file := range chain {
		_, hadGroups := file.raw["groups"]
		projected := projections[idx]
		if len(projected) == 0 {
			if hadGroups {
				result[idx] = json.RawMessage("null")
			}
			continue
		}
		data, err := json.Marshal(projected)
		if err != nil {
			return nil, fmt.Errorf("encoding groups for %q: %w", file.path, err)
		}
		result[idx] = data
	}
	return result, nil
}
