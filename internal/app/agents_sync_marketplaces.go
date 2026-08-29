package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// apm.yml cannot express marketplaces, so the template declares them as trailing `# apm marketplace add`
// comments. Render and parse live together because they are one grammar; a round-trip test pins them.
type marketplaceDecl struct {
	name   string
	source string
	args   []string
}

func (d marketplaceDecl) Render() string {
	return fmt.Sprintf("apm marketplace add %s --name %s", d.source, d.name)
}

func parseMarketplaceDecl(line string) (marketplaceDecl, bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "#") {
		return marketplaceDecl{}, false
	}
	fields := strings.Fields(strings.TrimPrefix(line, "#"))
	if len(fields) < 4 || fields[0] != "apm" || fields[1] != "marketplace" || fields[2] != "add" {
		return marketplaceDecl{}, false
	}
	args := fields[1:]
	decl := marketplaceDecl{source: fields[3], args: args}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--name" {
			decl.name = args[i+1]
		}
	}
	return decl, decl.name != ""
}

func parseTemplateMarketplaces(template []byte) []marketplaceDecl {
	var decls []marketplaceDecl
	for _, line := range strings.Split(string(template), "\n") {
		if decl, ok := parseMarketplaceDecl(line); ok {
			decls = append(decls, decl)
		}
	}
	return decls
}

func registeredMarketplaceNames(workspaceDir string) map[string]bool {
	data, err := os.ReadFile(filepath.Join(workspaceDir, "marketplaces.json"))
	if err != nil {
		return nil
	}
	var doc struct {
		Marketplaces []struct {
			Name string `json:"name"`
		} `json:"marketplaces"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil
	}
	names := make(map[string]bool, len(doc.Marketplaces))
	for _, entry := range doc.Marketplaces {
		if entry.Name != "" {
			names[entry.Name] = true
		}
	}
	return names
}

// Registers declared marketplaces apm does not know yet; extras stay registered because removal is a manual decision.
func (a *App) ensureMarketplaces(ctx context.Context, workspaceDir string, opts AgentsSyncAllOptions) error {
	tmplPath, err := AgentsTemplatePath()
	if err != nil {
		return err
	}
	template, err := os.ReadFile(tmplPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read agents template: %w", err)
	}
	registered := registeredMarketplaceNames(workspaceDir)
	var errs []error
	for _, decl := range parseTemplateMarketplaces(template) {
		if registered[decl.name] {
			continue
		}
		if opts.Progress != nil {
			verb := "Registering"
			if opts.DryRun {
				verb = "Would register"
			}
			opts.Progress(verb + " marketplace " + decl.name + "...")
		}
		if opts.DryRun {
			continue
		}
		if _, err := a.RunAPM(ctx, decl.args...); err != nil {
			errs = append(errs, fmt.Errorf("register marketplace %s: %w", decl.name, err))
		}
	}
	return errors.Join(errs...)
}
