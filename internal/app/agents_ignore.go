package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/lkshrk/omni/internal/config"
)

var (
	agentIgnoreTargets = []string{"claude", "codex"}
	agentIgnoreKinds   = []string{"plugin", "mcp", "marketplace"}
)

// AgentIgnoreSelector names one native artifact; every field is required to address exactly one item.
type AgentIgnoreSelector struct {
	Host   string
	Target string
	Kind   string
	ID     string
	Reason string
}

func (s AgentIgnoreSelector) validate() error {
	if strings.TrimSpace(s.Host) == "" {
		return fmt.Errorf("host is required")
	}
	if !slices.Contains(agentIgnoreTargets, s.Target) {
		return fmt.Errorf("target %q is not one of %s", s.Target, strings.Join(agentIgnoreTargets, ", "))
	}
	if !slices.Contains(agentIgnoreKinds, s.Kind) {
		return fmt.Errorf("kind %q is not one of %s", s.Kind, strings.Join(agentIgnoreKinds, ", "))
	}
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("id is required")
	}
	return nil
}

func (s AgentIgnoreSelector) matches(e config.AgentIgnoreEntry) bool {
	return e.Host == s.Host && e.Target == s.Target && e.Kind == s.Kind && e.ID == s.ID
}

// AgentIgnore records a native artifact omni must leave alone. Re-ignoring an existing entry
// updates its reason rather than duplicating it.
func (a *App) AgentIgnore(sel AgentIgnoreSelector) error {
	if err := sel.validate(); err != nil {
		return err
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Agents == nil {
			cfg.Agents = &config.AgentsConfig{}
		}
		entry := config.AgentIgnoreEntry{Host: sel.Host, Target: sel.Target, Kind: sel.Kind, ID: sel.ID, Reason: strings.TrimSpace(sel.Reason)}
		for i, existing := range cfg.Agents.Ignored {
			if sel.matches(existing) {
				cfg.Agents.Ignored[i] = entry
				return nil
			}
		}
		cfg.Agents.Ignored = append(cfg.Agents.Ignored, entry)
		return nil
	})
}

// AgentUnignore drops a recorded exception; removing one that was never recorded is an error rather
// than a silent success, because the caller believed it was protected.
func (a *App) AgentUnignore(sel AgentIgnoreSelector) error {
	if err := sel.validate(); err != nil {
		return err
	}
	return a.withConfig(func(cfg *config.RootConfig) error {
		if cfg.Agents == nil {
			return fmt.Errorf("no ignore entry for %s/%s/%s on host %s", sel.Target, sel.Kind, sel.ID, sel.Host)
		}
		kept := cfg.Agents.Ignored[:0]
		removed := false
		for _, existing := range cfg.Agents.Ignored {
			if !removed && sel.matches(existing) {
				removed = true
				continue
			}
			kept = append(kept, existing)
		}
		if !removed {
			return fmt.Errorf("no ignore entry for %s/%s/%s on host %s", sel.Target, sel.Kind, sel.ID, sel.Host)
		}
		cfg.Agents.Ignored = kept
		return nil
	})
}

// AgentIgnoreEntries lists recorded exceptions, unfiltered by host so an operator can see the whole file.
func (a *App) AgentIgnoreEntries() ([]config.AgentIgnoreEntry, error) {
	cfg, err := a.LoadConfig()
	if err != nil {
		return nil, err
	}
	if cfg == nil || cfg.Agents == nil {
		return nil, nil
	}
	return slices.Clone(cfg.Agents.Ignored), nil
}
