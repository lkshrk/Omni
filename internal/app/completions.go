package app

import (
	"context"
	"strings"
)

func (a *App) ToolNameCandidates(ctx context.Context, prefix string) ([]string, error) {
	tools, err := a.ListTools(ctx, "")
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		if strings.HasPrefix(tool.Name, prefix) {
			names = append(names, tool.Name)
		}
	}
	return names, nil
}

func (a *App) GroupNameCandidates(ctx context.Context, prefix string) ([]string, error) {
	groups, err := a.Groups(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(groups))
	for _, group := range groups {
		name := group.GroupName()
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	return names, nil
}

func (a *App) HostNameCandidates(prefix string) ([]string, error) {
	info, err := a.HostStatus()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(info.Hosts))
	for host := range info.Hosts {
		if strings.HasPrefix(host, prefix) {
			names = append(names, host)
		}
	}
	return names, nil
}

func (a *App) ProviderNameCandidates(ctx context.Context, prefix string) ([]string, error) {
	providers, err := a.Providers(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(providers))
	for _, provider := range providers {
		if strings.HasPrefix(provider.Name, prefix) {
			names = append(names, provider.Name)
		}
	}
	return names, nil
}

func (a *App) DotNameCandidates(prefix string) ([]string, error) {
	dots, err := a.DotsList()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(dots))
	for _, dot := range dots {
		if strings.HasPrefix(dot.Name, prefix) {
			names = append(names, dot.Name)
		}
	}
	return names, nil
}

var settingCandidateKeys = []string{
	"auto_import",
	"update_quarantine",
	"dots_repo",
	"dots_disabled",
	"agents_disabled",
	"dots_git.auto_commit",
	"dots_git.auto_push",
	"disabled_providers",
	"provider_priority",
	"provider_update_quarantine",
}

func SettingKeys() []string {
	return append([]string(nil), settingCandidateKeys...)
}

func SettingKeyCandidates(prefix string) []string {
	keys := SettingKeys()
	names := make([]string, 0, len(keys))
	for _, key := range keys {
		if strings.HasPrefix(key, prefix) {
			names = append(names, key)
		}
	}
	return names
}
