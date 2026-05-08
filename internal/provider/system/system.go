// Package system implements the "system" ecosystem provider.
// It delegates to the first available system package manager in the configured
// priority order: apt → dnf → pacman → brew (native Linux PMs take precedence
// over brew so that distro-native packages are preferred).
package system

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/lkshrk/omni/internal/provider"
)

// Provider is an ecosystem provider that delegates to the first available PM.
type Provider struct {
	delegates []provider.Provider
	mu        sync.Mutex
	resolved  provider.Provider // cached after first successful resolution
}

// New creates a system Provider with the given delegate chain.
// Delegates are tried in order; the first one reporting Available=true is used.
func New(delegates ...provider.Provider) *Provider {
	return &Provider{delegates: delegates}
}

func (p *Provider) Name() string { return "system" }
func (p *Provider) Description() string {
	return "system — resolves to first available system package manager"
}

func (p *Provider) Available(ctx context.Context) (bool, error) {
	_, err := p.resolve(ctx)
	if err != nil {
		// resolve returns an error both when no delegate is available (no PM
		// found) and when a delegate probe itself errored. Only the latter is
		// a real error worth propagating; "no PM available" is a normal false.
		// resolve wraps delegate errors with "checking <name> availability:",
		// whereas "system: no package manager available" indicates exhausted
		// delegates with no error. Distinguish by checking the prefix.
		if strings.HasPrefix(err.Error(), "system: no package manager available") {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (p *Provider) Install(ctx context.Context, tool provider.Tool) error {
	d, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	return d.Install(ctx, toolFor(tool, d.Name()))
}

func (p *Provider) Uninstall(ctx context.Context, tool provider.Tool) error {
	d, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	return d.Uninstall(ctx, toolFor(tool, d.Name()))
}

func (p *Provider) Upgrade(ctx context.Context, tool provider.Tool) error {
	d, err := p.resolve(ctx)
	if err != nil {
		return err
	}
	return d.Upgrade(ctx, toolFor(tool, d.Name()))
}

func (p *Provider) IsInstalled(ctx context.Context, tool provider.Tool) (bool, string, error) {
	d, err := p.resolve(ctx)
	if err != nil {
		return false, "", err
	}
	return d.IsInstalled(ctx, toolFor(tool, d.Name()))
}

func (p *Provider) ListInstalled(ctx context.Context) ([]provider.InstalledTool, error) {
	d, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	return d.ListInstalled(ctx)
}

// InstalledMap implements provider.BulkChecker by delegating to the concrete
// system PM's InstalledMap when available. All current system delegates
// (apt, dnf, pacman, brew, apk, zypper) implement BulkChecker, so this
// eliminates the per-tool IsInstalled slow path for system-configured tools.
func (p *Provider) InstalledMap(ctx context.Context) (map[string]string, error) {
	d, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	bc, ok := d.(provider.BulkChecker)
	if !ok {
		return nil, fmt.Errorf("system delegate %s does not implement BulkChecker", d.Name())
	}
	return bc.InstalledMap(ctx)
}

// Describe delegates description lookup to the first available PM that supports it.
func (p *Provider) Describe(ctx context.Context, tool provider.Tool) (string, error) {
	d, err := p.resolve(ctx)
	if err != nil {
		return "", nil //nolint:nilerr // no available delegate is not a description error
	}
	desc, ok := d.(provider.Descriptor)
	if !ok {
		return "", nil
	}
	return desc.Describe(ctx, toolFor(tool, d.Name()))
}

// ResolvedName implements provider.ConcreteResolver.
func (p *Provider) ResolvedName(ctx context.Context) (string, error) {
	d, err := p.resolve(ctx)
	if err != nil {
		return "", err
	}
	return d.Name(), nil
}

func (p *Provider) PrivilegePlan(ctx context.Context, action provider.PrivilegeAction, tool provider.Tool) (provider.PrivilegePlan, error) {
	d, err := p.resolve(ctx)
	if err != nil {
		return provider.PrivilegePlan{}, err
	}
	delegated := toolFor(tool, d.Name())
	if planner, ok := d.(provider.PrivilegePlanner); ok {
		return planner.PrivilegePlan(ctx, action, delegated)
	}
	return provider.PrivilegePlan{}, nil
}

// resolve returns the first available delegate or an error if none are available.
// The result is cached after the first successful resolution to avoid re-probing
// on every call within the same sync cycle.
func (p *Provider) resolve(ctx context.Context) (provider.Provider, error) {
	p.mu.Lock()
	cached := p.resolved
	p.mu.Unlock()
	if cached != nil {
		return cached, nil
	}

	for _, d := range p.delegates {
		ok, err := d.Available(ctx)
		if err != nil {
			return nil, fmt.Errorf("checking %s availability: %w", d.Name(), err)
		}
		if ok {
			p.mu.Lock()
			p.resolved = d
			p.mu.Unlock()
			return d, nil
		}
	}
	return nil, fmt.Errorf("system: no package manager available (tried: %s)", p.delegateNames())
}

func (p *Provider) delegateNames() string {
	names := make([]string, len(p.delegates))
	for i, d := range p.delegates {
		names[i] = d.Name()
	}
	return strings.Join(names, ", ")
}

// toolFor returns a copy of t with Provider set to the delegate's name.
func toolFor(t provider.Tool, providerName string) provider.Tool {
	t.Provider = providerName
	return t
}
