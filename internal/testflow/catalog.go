// Package testflow loads and validates the user-visible flow coverage catalog.
package testflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const SchemaVersion = 1

type Level string

const (
	LevelUnit        Level = "unit"
	LevelComponent   Level = "component"
	LevelIntegration Level = "integration"
	LevelCLIBlackBox Level = "cli_blackbox"
	LevelTUIBlackBox Level = "tui_blackbox"
	LevelParity      Level = "parity"
)

type Status string

const (
	StatusRequired Status = "required"
	StatusGap      Status = "gap"
)

type Criticality string

const (
	CriticalityCritical Criticality = "critical"
	CriticalityHigh     Criticality = "high"
	CriticalityMedium   Criticality = "medium"
	CriticalityLow      Criticality = "low"
)

type EvidenceRole string

const (
	EvidencePrimary      EvidenceRole = "primary"
	EvidenceRegression   EvidenceRole = "regression"
	EvidenceSupplemental EvidenceRole = "supplemental"
)

type ExemptionRule string

const (
	ExemptActionSurface ExemptionRule = "action_surface"
	ExemptRequiredLevel ExemptionRule = "required_level"
	ExemptParity        ExemptionRule = "parity"
)

type Catalog struct {
	SchemaVersion int    `json:"schema_version"`
	Flows         []Flow `json:"flows"`
}
type Flow struct {
	ID                string        `json:"id"`
	Capability        string        `json:"capability"`
	Title             string        `json:"title"`
	Criticality       Criticality   `json:"criticality"`
	CriticalityReason string        `json:"criticality_reason"`
	ActionIDs         []string      `json:"action_ids,omitempty"`
	CompositeReason   string        `json:"composite_reason,omitempty"`
	Surfaces          Surfaces      `json:"surfaces,omitempty"`
	Mutates           *bool         `json:"mutates,omitempty"`
	CLICommands       [][]string    `json:"cli_commands,omitempty"`
	Requirements      []Requirement `json:"requirements"`
	Parity            *Parity       `json:"parity,omitempty"`
	Exemptions        []Exemption   `json:"exemptions,omitempty"`
}
type Surfaces struct {
	CLI *bool `json:"cli,omitempty"`
	TUI *bool `json:"tui,omitempty"`
}
type Requirement struct {
	Level       Level      `json:"level"`
	Status      Status     `json:"status"`
	Reason      string     `json:"reason,omitempty"`
	TargetStage string     `json:"target_stage,omitempty"`
	Evidence    []Evidence `json:"evidence,omitempty"`
}
type Evidence struct {
	Type      Level        `json:"type"`
	Role      EvidenceRole `json:"role"`
	Reference string       `json:"reference,omitempty"`
	Selector  Selector     `json:"selector"`
}
type Selector struct {
	Package string   `json:"package,omitempty"`
	Test    string   `json:"test,omitempty"`
	Fixture string   `json:"fixture,omitempty"`
	Tags    []string `json:"tags,omitempty"`
	Lane    string   `json:"lane,omitempty"`
	OS      []string `json:"os,omitempty"`
}
type Parity struct {
	SemanticState string `json:"semantic_state,omitempty"`
	SemanticQuery string `json:"semantic_query,omitempty"`
	Rationale     string `json:"rationale,omitempty"`
}
type Exemption struct {
	Rule        ExemptionRule `json:"rule"`
	Reason      string        `json:"reason"`
	TargetStage string        `json:"target_stage"`
}

// ActionSurface is the small projection the catalog needs from the action registry.
type ActionSurface struct {
	ID          string
	CLI         bool
	CLICommands []CLICommandSurface
	TUI         bool
	Mutates     bool
}

type CLICommandSurface struct {
	Command       []string
	RequiredFlags []string
}

type CLICommandOwner struct {
	FlowID        string
	ActionID      string
	RequiredFlags []string
}

func Load(path string) (Catalog, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Catalog{}, err
	}
	if err := rejectDuplicateKeys(body); err != nil {
		return Catalog{}, err
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	var catalog Catalog
	if err := dec.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode flow catalog: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return Catalog{}, fmt.Errorf("decode flow catalog trailer: %w", err)
		}
		return Catalog{}, errors.New("flow catalog contains multiple JSON values")
	}
	return catalog, nil
}

func rejectDuplicateKeys(body []byte) error {
	dec := json.NewDecoder(bytes.NewReader(body))
	var walk func(string) error
	walk = func(path string) error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				keyToken, err := dec.Token()
				if err != nil {
					return err
				}
				key, ok := keyToken.(string)
				if !ok {
					return fmt.Errorf("decode flow catalog: object key at %s is not a string", path)
				}
				if seen[key] {
					return fmt.Errorf("decode flow catalog: duplicate key %q at %s", key, path)
				}
				seen[key] = true
				if err := walk(path + "." + key); err != nil {
					return err
				}
			}
		case '[':
			for index := 0; dec.More(); index++ {
				if err := walk(fmt.Sprintf("%s[%d]", path, index)); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("decode flow catalog: unexpected delimiter %q at %s", delim, path)
		}
		_, err = dec.Token()
		return err
	}
	return walk("$")
}

var flowIDPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._][a-z0-9]+)*$`)

// Validate performs static catalog, action-registry, and test-reference checks.
func Validate(catalog Catalog, actions []ActionSurface, root string) error {
	v := validator{root: root, actionByID: map[string]ActionSurface{}, actionFlow: map[string]string{}, actionCLICommands: map[string][]string{}, actionVariants: map[string]map[string]string{}, cliCommands: map[string]string{}}
	v.catalog(catalog, actions)
	if len(v.errs) == 0 {
		return nil
	}
	sort.Strings(v.errs)
	return errors.New(strings.Join(v.errs, "\n"))
}

type validator struct {
	root              string
	module            string
	actionByID        map[string]ActionSurface
	actionFlow        map[string]string
	actionCLICommands map[string][]string
	actionVariants    map[string]map[string]string
	cliCommands       map[string]string
	errs              []string
}

func (v *validator) catalog(c Catalog, actions []ActionSurface) {
	if c.SchemaVersion != SchemaVersion {
		v.add("schema_version: got %d, want %d", c.SchemaVersion, SchemaVersion)
	}
	for _, action := range actions {
		if action.ID == "" {
			v.add("action registry contains an empty ID")
			continue
		}
		if _, exists := v.actionByID[action.ID]; exists {
			v.add("action registry contains duplicate ID %q", action.ID)
		}
		if action.CLI != (len(action.CLICommands) > 0) {
			v.add("action %q has contradictory CLI surface and command bindings", action.ID)
		}
		v.actionByID[action.ID] = action
		for _, command := range action.CLICommands {
			key, ok := commandKey(command.Command)
			if !ok {
				v.add("action %q has a non-canonical CLI command", action.ID)
				continue
			}
			flags, ok := requiredFlagsKey(command.RequiredFlags)
			if !ok {
				v.add("action %q has invalid required CLI flags for %q", action.ID, strings.Join(command.Command, " "))
				continue
			}
			v.actionCLICommands[key] = append(v.actionCLICommands[key], action.ID)
			if v.actionVariants[key] == nil {
				v.actionVariants[key] = map[string]string{}
			}
			if other := v.actionVariants[key][flags]; other != "" {
				v.add("CLI command %q variant %q belongs to both actions %q and %q", strings.Join(command.Command, " "), strings.Join(command.RequiredFlags, " "), other, action.ID)
			} else {
				v.actionVariants[key][flags] = action.ID
			}
		}
	}
	for key, variants := range v.actionVariants {
		if variants[""] == "" {
			v.add("CLI command %q has no default action owner", strings.ReplaceAll(key, "\x00", " "))
		}
	}
	module, err := readModulePath(v.root)
	if err != nil {
		v.add("module path: %v", err)
	} else {
		v.module = module
	}
	seen := map[string]bool{}
	for i := range c.Flows {
		flow := &c.Flows[i]
		if seen[flow.ID] {
			v.add("flow %q is declared more than once", flow.ID)
		}
		seen[flow.ID] = true
		v.flow(flow)
	}
	for id := range v.actionByID {
		if v.actionFlow[id] == "" {
			v.add("action %q is not mapped to a flow", id)
		}
	}
}

func (v *validator) flow(flow *Flow) {
	prefix := fmt.Sprintf("flow %q", flow.ID)
	if !flowIDPattern.MatchString(flow.ID) {
		v.add("%s has an invalid ID", prefix)
	}
	if strings.TrimSpace(flow.Capability) == "" || strings.TrimSpace(flow.Title) == "" {
		v.add("%s requires capability and title", prefix)
	}
	if !validCriticality(flow.Criticality) || strings.TrimSpace(flow.CriticalityReason) == "" {
		v.add("%s requires valid criticality and criticality_reason", prefix)
	}
	cli, tui := false, false
	if len(flow.ActionIDs) > 0 {
		if flow.Surfaces.CLI != nil || flow.Surfaces.TUI != nil {
			v.add("%s is action-backed; surfaces must be derived from the action registry", prefix)
		}
		if flow.Mutates != nil {
			v.add("%s is action-backed; mutates must be derived from the action registry", prefix)
		}
		if len(flow.CLICommands) != 0 {
			v.add("%s is action-backed; cli_commands must be derived from the action registry", prefix)
		}
		if len(flow.ActionIDs) > 1 && strings.TrimSpace(flow.CompositeReason) == "" {
			v.add("%s maps multiple actions without composite_reason", prefix)
		}
		if len(flow.ActionIDs) == 1 && flow.CompositeReason != "" {
			v.add("%s has composite_reason but maps only one action", prefix)
		}
		seen := map[string]bool{}
		for _, id := range flow.ActionIDs {
			if seen[id] {
				v.add("%s repeats action %q", prefix, id)
				continue
			}
			seen[id] = true
			action, ok := v.actionByID[id]
			if !ok {
				v.add("%s maps unknown action %q", prefix, id)
				continue
			}
			if other := v.actionFlow[id]; other != "" {
				v.add("action %q maps to both %q and %q", id, other, flow.ID)
			} else {
				v.actionFlow[id] = flow.ID
			}
			cli = cli || action.CLI
			tui = tui || action.TUI
		}
	} else {
		if flow.CompositeReason != "" {
			v.add("%s has composite_reason without action_ids", prefix)
		}
		if flow.Surfaces.CLI == nil || flow.Surfaces.TUI == nil {
			v.add("%s is not action-backed and must author both surfaces", prefix)
		} else {
			cli, tui = *flow.Surfaces.CLI, *flow.Surfaces.TUI
			if !cli && !tui {
				v.add("%s is exposed by neither CLI nor TUI", prefix)
			}
		}
		if flow.Mutates == nil {
			v.add("%s is not action-backed and must author mutates", prefix)
		}
		if cli && len(flow.CLICommands) == 0 {
			v.add("%s exposes CLI but has no cli_commands", prefix)
		}
		if !cli && len(flow.CLICommands) != 0 {
			v.add("%s has cli_commands without a CLI surface", prefix)
		}
		for _, command := range flow.CLICommands {
			key, ok := commandKey(command)
			if !ok {
				v.add("%s has a non-canonical cli_commands entry", prefix)
				continue
			}
			if other := v.cliCommands[key]; other != "" {
				v.add("CLI command %q maps to both non-action flows %q and %q", strings.Join(command, " "), other, flow.ID)
			} else if actions := v.actionCLICommands[key]; len(actions) != 0 {
				v.add("CLI command %q contradicts action-backed flow ownership by %s", strings.Join(command, " "), strings.Join(actions, ", "))
			} else {
				v.cliCommands[key] = flow.ID
			}
		}
	}
	v.requirements(flow, cli, tui, flowMutates(flow, v.actionByID))
	for _, exemption := range flow.Exemptions {
		if !validExemptionRule(exemption.Rule) || exemption.Reason == "" || exemption.TargetStage == "" {
			v.add("%s has an incomplete typed exemption", prefix)
		}
	}
}

func (v *validator) requirements(flow *Flow, cli, tui, mutates bool) {
	prefix := fmt.Sprintf("flow %q", flow.ID)
	if len(flow.Requirements) == 0 {
		v.add("%s has no coverage requirements", prefix)
		return
	}
	seen := map[Level]bool{}
	for _, requirement := range flow.Requirements {
		if !validLevel(requirement.Level) {
			v.add("%s has unknown requirement level %q", prefix, requirement.Level)
			continue
		}
		if seen[requirement.Level] {
			v.add("%s repeats requirement level %q", prefix, requirement.Level)
		}
		seen[requirement.Level] = true
		switch requirement.Status {
		case StatusGap:
			if requirement.Reason == "" || requirement.TargetStage == "" {
				v.add("%s gap %q requires reason and target_stage", prefix, requirement.Level)
			}
			if len(requirement.Evidence) != 0 {
				v.add("%s gap %q must not claim evidence", prefix, requirement.Level)
			}
		case StatusRequired:
			if len(requirement.Evidence) == 0 {
				v.add("%s required %q has no evidence", prefix, requirement.Level)
			}
			for _, evidence := range requirement.Evidence {
				v.evidence(prefix, requirement.Level, evidence)
			}
		default:
			v.add("%s requirement %q has invalid status %q", prefix, requirement.Level, requirement.Status)
		}
	}
	if seen[LevelCLIBlackBox] && !cli {
		v.add("%s requires CLI black-box coverage without a CLI surface", prefix)
	}
	if seen[LevelTUIBlackBox] && !tui {
		v.add("%s requires TUI black-box coverage without a TUI surface", prefix)
	}
	if cli && !seen[LevelCLIBlackBox] {
		v.add("%s exposes CLI but has no cli_blackbox requirement", prefix)
	}
	if tui && !seen[LevelTUIBlackBox] {
		v.add("%s exposes TUI but has no tui_blackbox requirement", prefix)
	}
	if cli && tui && !seen[LevelParity] {
		v.add("%s exposes CLI and TUI but has no parity requirement", prefix)
	}
	if seen[LevelParity] {
		if !cli || !tui {
			v.add("%s requires parity without both CLI and TUI surfaces", prefix)
		}
		if flow.Parity == nil || (flow.Parity.SemanticState == "") == (flow.Parity.SemanticQuery == "") {
			v.add("%s parity requires exactly one of semantic_state or semantic_query", prefix)
		} else if mutates && flow.Parity.SemanticState == "" {
			v.add("%s mutates state and requires parity semantic_state", prefix)
		} else if !mutates && flow.Parity.SemanticQuery == "" {
			v.add("%s is read-only and requires parity semantic_query", prefix)
		}
	} else if flow.Parity != nil {
		v.add("%s defines parity without a parity requirement", prefix)
	}
}

func (v *validator) evidence(prefix string, required Level, evidence Evidence) {
	if !validLevel(evidence.Type) {
		v.add("%s uses unknown evidence type %q", prefix, evidence.Type)
	} else if evidence.Type != required {
		v.add("%s evidence type %q must exactly match requirement %q", prefix, evidence.Type, required)
	}
	switch evidence.Role {
	case EvidencePrimary, EvidenceSupplemental:
	case EvidenceRegression:
		if strings.TrimSpace(evidence.Reference) == "" {
			v.add("%s regression evidence requires reference", prefix)
		}
	default:
		v.add("%s uses unknown evidence role %q", prefix, evidence.Role)
	}
	sel := evidence.Selector
	if strings.TrimSpace(sel.Package) == "" || strings.TrimSpace(sel.Test) == "" || strings.TrimSpace(sel.Lane) == "" || len(sel.OS) == 0 || sel.Tags == nil {
		v.add("%s required evidence needs package, test, lane, nonempty os, and explicit tags", prefix)
		return
	}
	for _, osName := range sel.OS {
		if strings.TrimSpace(osName) == "" {
			v.add("%s evidence os entries must be nonempty", prefix)
		}
	}
	for _, tag := range sel.Tags {
		if strings.TrimSpace(tag) == "" {
			v.add("%s evidence tags must be nonempty", prefix)
		}
	}
	if sel.Package != "" && !v.goTestExists(sel.Package, sel.Test) {
		v.add("%s references missing Go test %s.%s", prefix, sel.Package, sel.Test)
	}
	if strings.Contains(sel.Test, "/") && sel.Fixture == "" && !v.goSubtestExists(sel.Package, sel.Test) {
		v.add("%s Go selector %q names an unverifiable subtest", prefix, sel.Test)
	}
	if !validLane(sel.Lane) {
		v.add("%s evidence lane %q is not a current CI lane", prefix, sel.Lane)
	}
	for _, osName := range sel.OS {
		if !validOS(osName) {
			v.add("%s evidence OS %q is not supported", prefix, osName)
		}
	}
	for _, tag := range sel.Tags {
		if !validTag(tag) {
			v.add("%s evidence tag %q is not supported", prefix, tag)
		}
	}
	if sel.Fixture != "" {
		path, ok := v.repoPath(sel.Fixture)
		if !ok {
			v.add("%s fixture escapes the repository: %q", prefix, sel.Fixture)
		} else if info, err := os.Stat(path); err != nil || info.IsDir() {
			v.add("%s references missing fixture %q", prefix, sel.Fixture)
		} else if !testscriptRunsOmni(path) {
			v.add("%s fixture %q does not execute omni", prefix, sel.Fixture)
		} else if !strings.HasPrefix(sel.Test, "TestCLI/") || strings.TrimPrefix(sel.Test, "TestCLI/") != strings.TrimSuffix(filepath.Base(sel.Fixture), filepath.Ext(sel.Fixture)) {
			v.add("%s testscript selector %q does not match fixture %q", prefix, sel.Test, sel.Fixture)
		}
	}
}

func (v *validator) goTestExists(pkg, name string) bool {
	if v.module == "" || (pkg != v.module && !strings.HasPrefix(pkg, v.module+"/")) {
		return false
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(pkg, v.module), "/")
	dir, ok := v.repoPath(rel)
	if !ok {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			continue
		}
		baseName := strings.Split(name, "/")[0]
		for _, decl := range file.Decls {
			if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == baseName {
				return true
			}
		}
	}
	return false
}

func (v *validator) goSubtestExists(pkg, name string) bool {
	parts := strings.SplitN(name, "/", 2)
	if len(parts) != 2 {
		return false
	}
	rel := strings.TrimPrefix(strings.TrimPrefix(pkg, v.module), "/")
	dir, ok := v.repoPath(rel)
	if !ok {
		return false
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, entry.Name()), nil, 0)
		if err != nil {
			continue
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv != nil || fn.Name.Name != parts[0] || fn.Body == nil {
				continue
			}
			found := false
			ast.Inspect(fn.Body, func(node ast.Node) bool {
				if found {
					return false
				}
				call, ok := node.(*ast.CallExpr)
				if !ok || len(call.Args) == 0 {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				literal, literalOK := call.Args[0].(*ast.BasicLit)
				if !ok || sel.Sel.Name != "Run" || !literalOK || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err == nil && (value == parts[1] || strings.ReplaceAll(value, " ", "_") == parts[1]) {
					found = true
					return false
				}
				return true
			})
			if found {
				return true
			}
		}
	}
	return false
}

func readModulePath(root string) (string, error) {
	body, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(string(body), "\n") {
		if fields := strings.Fields(line); len(fields) == 2 && fields[0] == "module" {
			return fields[1], nil
		}
	}
	return "", errors.New("go.mod has no module directive")
}

func testscriptRunsOmni(path string) bool {
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "exec" && fields[1] == "omni" {
			return true
		}
		if len(fields) >= 3 && fields[0] == "!" && fields[1] == "exec" && fields[2] == "omni" {
			return true
		}
	}
	return false
}

func (v *validator) repoPath(rel string) (string, bool) {
	if filepath.IsAbs(rel) {
		return "", false
	}
	root, err := filepath.Abs(v.root)
	if err != nil {
		return "", false
	}
	path, err := filepath.Abs(filepath.Join(root, filepath.Clean(rel)))
	if err != nil {
		return "", false
	}
	inside, err := filepath.Rel(root, path)
	return path, err == nil && inside != ".." && !strings.HasPrefix(inside, ".."+string(filepath.Separator))
}

func (v *validator) add(format string, args ...any) {
	v.errs = append(v.errs, fmt.Sprintf(format, args...))
}

func flowMutates(flow *Flow, actions map[string]ActionSurface) bool {
	if len(flow.ActionIDs) == 0 {
		return flow.Mutates != nil && *flow.Mutates
	}
	for _, id := range flow.ActionIDs {
		if actions[id].Mutates {
			return true
		}
	}
	return false
}

func commandKey(command []string) (string, bool) {
	if len(command) == 0 {
		return "", false
	}
	for _, token := range command {
		if token == "" || strings.TrimSpace(token) != token || strings.HasPrefix(token, "-") {
			return "", false
		}
	}
	return strings.Join(command, "\x00"), true
}

func requiredFlagsKey(flags []string) (string, bool) {
	copyFlags := append([]string(nil), flags...)
	sort.Strings(copyFlags)
	for i, flag := range copyFlags {
		if !strings.HasPrefix(flag, "--") || strings.TrimSpace(flag) != flag || (i > 0 && flag == copyFlags[i-1]) {
			return "", false
		}
	}
	return strings.Join(copyFlags, "\x00"), true
}

// ResolveCLICommand returns every catalog flow/action owning an exact canonical command path.
func ResolveCLICommand(catalog Catalog, actions []ActionSurface, command []string) []CLICommandOwner {
	key, ok := commandKey(command)
	if !ok {
		return nil
	}
	actionFlows := map[string]string{}
	for _, flow := range catalog.Flows {
		for _, actionID := range flow.ActionIDs {
			actionFlows[actionID] = flow.ID
		}
	}
	var owners []CLICommandOwner
	for _, action := range actions {
		for _, candidate := range action.CLICommands {
			if candidateKey, valid := commandKey(candidate.Command); valid && candidateKey == key {
				owners = append(owners, CLICommandOwner{FlowID: actionFlows[action.ID], ActionID: action.ID, RequiredFlags: append([]string(nil), candidate.RequiredFlags...)})
			}
		}
	}
	for _, flow := range catalog.Flows {
		for _, candidate := range flow.CLICommands {
			if candidateKey, valid := commandKey(candidate); valid && candidateKey == key {
				owners = append(owners, CLICommandOwner{FlowID: flow.ID})
				break
			}
		}
	}
	sort.Slice(owners, func(i, j int) bool {
		if owners[i].FlowID != owners[j].FlowID {
			return owners[i].FlowID < owners[j].FlowID
		}
		return owners[i].ActionID < owners[j].ActionID
	})
	return owners
}

var validLanes = map[string]bool{
	"script-tests": true, "unit-app": true, "unit-cli": true, "unit-tui": true, "unit-remaining": true,
	"apm-platform-contracts": true, "onboarding-unit": true, "docker-cli-ad": true, "docker-cli-rest": true,
	"docker-non-cli": true, "docker-providers": true, "docker-apm": true, "docker-onboarding": true,
	"docker-full":    true,
	"docker-realism": true,
}

func validLane(lane string) bool { return validLanes[lane] }
func validOS(name string) bool   { return name == "linux" || name == "macos" || name == "windows" }
func validTag(tag string) bool {
	switch tag {
	case "docker", "integration", "pty", "race", "real-binary", "sandbox", "testscript":
		return true
	default:
		return false
	}
}
func validLevel(level Level) bool {
	switch level {
	case LevelUnit, LevelComponent, LevelIntegration, LevelCLIBlackBox, LevelTUIBlackBox, LevelParity:
		return true
	}
	return false
}
func validCriticality(c Criticality) bool {
	switch c {
	case CriticalityCritical, CriticalityHigh, CriticalityMedium, CriticalityLow:
		return true
	}
	return false
}
func validExemptionRule(rule ExemptionRule) bool {
	switch rule {
	case ExemptActionSurface, ExemptRequiredLevel, ExemptParity:
		return true
	}
	return false
}
