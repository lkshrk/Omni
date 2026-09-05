package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// AgentIgnoreEntry pins one host-local agent artifact that omni must leave alone.
type AgentIgnoreEntry struct {
	Host   string `json:"host"`
	Target string `json:"target"`
	Kind   string `json:"kind"`
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

type AgentsConfig struct {
	Ignored []AgentIgnoreEntry `json:"ignored"`
}

func (a AgentsConfig) MarshalJSON() ([]byte, error) {
	entries := a.Ignored
	if entries == nil {
		entries = []AgentIgnoreEntry{}
	}
	return json.Marshal(struct {
		Ignored []AgentIgnoreEntry `json:"ignored"`
	}{Ignored: entries})
}

type agentsBlockKind int

const (
	agentsBlockAbsent agentsBlockKind = iota
	agentsBlockIgnored
	agentsBlockRetired
)

type agentsBlockClass struct {
	kind    agentsBlockKind
	field   string
	ignored []AgentIgnoreEntry
}

var (
	agentIgnoreTargets = []string{"claude", "codex"}
	agentIgnoreKinds   = []string{"plugin", "mcp", "marketplace"}
)

// classifyAgentsBlock is the single source of truth for the top-level "agents"
// key: only the exact new-style {"ignored": [...]} shape escapes the v24 tombstone.
func classifyAgentsBlock(raw map[string]json.RawMessage) agentsBlockClass {
	block, ok := raw["agents"]
	if !ok {
		return agentsBlockClass{kind: agentsBlockAbsent}
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(block, &fields); err != nil || fields == nil {
		return agentsBlockClass{kind: agentsBlockRetired}
	}
	names := make([]string, 0, len(fields))
	for name := range fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name != "ignored" {
			return agentsBlockClass{kind: agentsBlockRetired, field: name}
		}
	}
	value, ok := fields["ignored"]
	if !ok {
		return agentsBlockClass{kind: agentsBlockRetired}
	}
	entries, err := parseAgentIgnoreEntries(value)
	if err != nil {
		return agentsBlockClass{kind: agentsBlockRetired, field: "ignored"}
	}
	return agentsBlockClass{kind: agentsBlockIgnored, ignored: entries}
}

func (c agentsBlockClass) path() string {
	if c.field == "" {
		return "$.agents"
	}
	return "$.agents." + c.field
}

func parseAgentIgnoreEntries(value json.RawMessage) ([]AgentIgnoreEntry, error) {
	var rawEntries []json.RawMessage
	if err := json.Unmarshal(value, &rawEntries); err != nil {
		return nil, err
	}
	if rawEntries == nil {
		return nil, fmt.Errorf("agents.ignored must be an array")
	}
	entries := make([]AgentIgnoreEntry, 0, len(rawEntries))
	for i, rawEntry := range rawEntries {
		entry, err := parseAgentIgnoreEntry(rawEntry)
		if err != nil {
			return nil, fmt.Errorf("agents.ignored[%d]: %w", i, err)
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseAgentIgnoreEntry(data json.RawMessage) (AgentIgnoreEntry, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return AgentIgnoreEntry{}, err
	}
	if fields == nil {
		return AgentIgnoreEntry{}, fmt.Errorf("entry must be an object")
	}
	var entry AgentIgnoreEntry
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&entry); err != nil {
		return AgentIgnoreEntry{}, err
	}
	if strings.TrimSpace(entry.Host) == "" {
		return AgentIgnoreEntry{}, fmt.Errorf("host must be a non-empty string")
	}
	if !slices.Contains(agentIgnoreTargets, entry.Target) {
		return AgentIgnoreEntry{}, fmt.Errorf("target must be one of %s", strings.Join(agentIgnoreTargets, ", "))
	}
	if !slices.Contains(agentIgnoreKinds, entry.Kind) {
		return AgentIgnoreEntry{}, fmt.Errorf("kind must be one of %s", strings.Join(agentIgnoreKinds, ", "))
	}
	if strings.TrimSpace(entry.ID) == "" {
		return AgentIgnoreEntry{}, fmt.Errorf("id must be a non-empty string")
	}
	if _, ok := fields["reason"]; ok && strings.TrimSpace(entry.Reason) == "" {
		return AgentIgnoreEntry{}, fmt.Errorf("reason must be a non-empty string")
	}
	return entry, nil
}
