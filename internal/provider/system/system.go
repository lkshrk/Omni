// Package system delegates to apt, dnf, pacman, then brew, so distro-native packages win over brew.
package system

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/lkshrk/omni/internal/provider"
)

// Distinct from a delegate probe error so Available can return false without a misleading error.
var errNoPMAvailable = errors.New("system: no package manager available")

type Provider struct {
	delegates []provider.Provider
	mu        sync.Mutex
	resolved  provider.Provider // cached after first successful resolution
}

// New — Delegates are tried in order; the first one reporting Available=true is used.
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
		if errors.Is(err, errNoPMAvailable) {
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

// InstalledMap — Every system delegate implements BulkChecker, so this avoids the per-tool IsInstalled slow path.
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

// Cached after the first success to avoid re-probing on every call in a sync cycle.
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
	return nil, fmt.Errorf("%w (tried: %s)", errNoPMAvailable, p.delegateNames())
}

func (p *Provider) delegateNames() string {
	names := make([]string, len(p.delegates))
	for i, d := range p.delegates {
		names[i] = d.Name()
	}
	return strings.Join(names, ", ")
}

func toolFor(t provider.Tool, providerName string) provider.Tool {
	t.Provider = providerName
	return t
}
