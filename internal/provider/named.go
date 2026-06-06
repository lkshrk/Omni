package provider

import "context"

// Named returns p with Name overridden. It is intended for concrete aliases
// backed by an existing manager provider, such as npm over the node provider.
func Named(name string, p Provider) Provider {
	return namedProvider{name: name, Provider: p}
}

type namedProvider struct {
	name string
	Provider
}

func (p namedProvider) Name() string { return p.name }

func (p namedProvider) ListInstalled(ctx context.Context) ([]InstalledTool, error) {
	tools, err := p.Provider.ListInstalled(ctx)
	if err != nil {
		return nil, err
	}
	for i := range tools {
		tools[i].Provider = p.name
	}
	return tools, nil
}

func (p namedProvider) Search(ctx context.Context, query string) ([]SearchResult, error) {
	searcher, ok := p.Provider.(Searcher)
	if !ok {
		return nil, nil
	}
	results, err := searcher.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	for i := range results {
		results[i].Provider = p.name
		if results[i].SourceProvider == "" {
			results[i].SourceProvider = p.name
		}
	}
	return results, nil
}

func (p namedProvider) InstalledMap(ctx context.Context) (map[string]string, error) {
	bulk, ok := p.Provider.(BulkChecker)
	if !ok {
		return nil, nil
	}
	return bulk.InstalledMap(ctx)
}

func (p namedProvider) InstalledMetadataMap(ctx context.Context) (map[string]InstalledMetadata, error) {
	bulk, ok := p.Provider.(MetadataBulkChecker)
	if !ok {
		return nil, nil
	}
	return bulk.InstalledMetadataMap(ctx)
}

func (p namedProvider) OutdatedMap(ctx context.Context) (map[string]string, error) {
	outdated, ok := p.Provider.(OutdatedChecker)
	if !ok {
		return nil, nil
	}
	return outdated.OutdatedMap(ctx)
}

func (p namedProvider) OutdatedInfoMap(ctx context.Context) (map[string]OutdatedInfo, error) {
	outdated, ok := p.Provider.(OutdatedInfoChecker)
	if !ok {
		return nil, nil
	}
	return outdated.OutdatedInfoMap(ctx)
}

func (p namedProvider) RefreshMetadata(ctx context.Context) error {
	refresher, ok := p.Provider.(MetadataRefresher)
	if !ok {
		return nil
	}
	return refresher.RefreshMetadata(ctx)
}

func (p namedProvider) Describe(ctx context.Context, tool Tool) (string, error) {
	descriptor, ok := p.Provider.(Descriptor)
	if !ok {
		return "", nil
	}
	return descriptor.Describe(ctx, tool)
}

func (p namedProvider) BulkDescribe(ctx context.Context, tools []Tool) (map[string]string, error) {
	descriptor, ok := p.Provider.(BulkDescriber)
	if !ok {
		return nil, nil
	}
	return descriptor.BulkDescribe(ctx, tools)
}

func (p namedProvider) CLIToolSet(ctx context.Context) (map[string]bool, error) {
	cli, ok := p.Provider.(CLIToolProvider)
	if !ok {
		return nil, nil
	}
	return cli.CLIToolSet(ctx)
}

func (p namedProvider) PrivilegePlan(ctx context.Context, action PrivilegeAction, tool Tool) (PrivilegePlan, error) {
	planner, ok := p.Provider.(PrivilegePlanner)
	if !ok {
		return PrivilegePlan{}, nil
	}
	return planner.PrivilegePlan(ctx, action, tool)
}

func (p namedProvider) PrivilegeCommand(action PrivilegeAction, tool Tool) (cmd string, args []string, ok bool) {
	planner, ok := p.Provider.(PrivilegeCommandPlanner)
	if !ok {
		return "", nil, false
	}
	return planner.PrivilegeCommand(action, tool)
}
