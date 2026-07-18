package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/config"
	"github.com/lkshrk/omni/internal/database"
)

const (
	UpdateBlockQuarantined     = "quarantined"
	UpdateBlockMetadataMissing = "blocked-metadata"
	toolQuarantineExempt       = "exempt"
)

type UpgradeOptions struct {
	Force bool
}

type QuarantinedUpdate struct {
	Name         string
	Provider     string
	Package      string
	Version      string
	Reason       string
	BlockedUntil *time.Time
}

type updateQuarantineDecision struct {
	Blocked      bool
	Reason       string
	BlockedUntil *time.Time
}

func quarantineBlockedError(name string, decision updateQuarantineDecision) error {
	switch decision.Reason {
	case UpdateBlockSelfUpdates:
		return fmt.Errorf("%s updates itself; brew cannot upgrade it, open the app to update", name)
	case UpdateBlockMetadataMissing:
		return fmt.Errorf("%s update blocked: package-manager update metadata unavailable; use --force to bypass quarantine", name)
	case UpdateBlockQuarantined:
		if decision.BlockedUntil != nil {
			return fmt.Errorf("%s update quarantined until %s; use --force to bypass", name, decision.BlockedUntil.Format(time.RFC3339))
		}
		return fmt.Errorf("%s update quarantined; use --force to bypass", name)
	default:
		return fmt.Errorf("%s update blocked by quarantine; use --force to bypass", name)
	}
}

func (a *App) annotateUpdateQuarantine(ctx context.Context, cfg *config.RootConfig, tools []*database.ToolCache) {
	if cfg == nil || len(tools) == 0 {
		return
	}
	now := time.Now()
	for _, tool := range tools {
		decision, _ := a.updateQuarantineDecision(ctx, cfg, tool, now)
		if !decision.Blocked {
			continue
		}
		tool.UpdateBlocked = decision.Reason
		tool.UpdateBlockedUntil = decision.BlockedUntil
	}
}

func (a *App) updateQuarantineDecision(ctx context.Context, cfg *config.RootConfig, tool *database.ToolCache, now time.Time) (updateQuarantineDecision, error) {
	if cfg == nil || tool == nil || !tool.Installed || !tool.Outdated || !tool.LatestVersion.Valid {
		return updateQuarantineDecision{}, nil
	}
	policy, exempt, err := updateQuarantinePolicy(cfg, tool)
	if err != nil {
		return updateQuarantineDecision{}, err
	}
	if exempt || policy <= 0 {
		return updateQuarantineDecision{}, nil
	}
	metadataProvider := updateMetadataProvider(tool)
	if metadataProvider == "" {
		metadataProvider = tool.Provider
	}
	pkg := tool.Package
	if pkg == "" {
		pkg = tool.Name
	}
	metadata, err := a.readDB().GetUpdateMetadata(ctx, metadataProvider, pkg, tool.LatestVersion.String)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return updateQuarantineDecision{Blocked: true, Reason: UpdateBlockMetadataMissing}, nil
		}
		return updateQuarantineDecision{}, err
	}
	availableAt := metadata.AvailableAt
	tool.UpdateAvailableAt = &availableAt
	tool.UpdateDateSource = metadata.DateSource
	blockedUntil := availableAt.Add(policy)
	if now.Before(blockedUntil) {
		return updateQuarantineDecision{Blocked: true, Reason: UpdateBlockQuarantined, BlockedUntil: &blockedUntil}, nil
	}
	return updateQuarantineDecision{}, nil
}

func updateQuarantinePolicy(cfg *config.RootConfig, tool *database.ToolCache) (time.Duration, bool, error) {
	if spec, ok := cfg.Tools[tool.Name]; ok {
		value := strings.TrimSpace(spec.Quarantine)
		if strings.EqualFold(value, toolQuarantineExempt) {
			return 0, true, nil
		}
		if value != "" {
			duration, err := parseQuarantineDuration(value)
			if err != nil {
				return 0, false, fmt.Errorf("tool %q quarantine: %w", tool.Name, err)
			}
			return duration, false, nil
		}
	}
	settings := cfg.Settings
	if concrete := updateMetadataProvider(tool); concrete != "" {
		if value := settings.ProviderUpdateQuarantine[concrete]; strings.TrimSpace(value) != "" {
			duration, err := parseQuarantineDuration(value)
			if err != nil {
				return 0, false, fmt.Errorf("provider %q update quarantine: %w", concrete, err)
			}
			return duration, false, nil
		}
	}
	if tool.Provider != "" {
		if value := settings.ProviderUpdateQuarantine[tool.Provider]; strings.TrimSpace(value) != "" {
			duration, err := parseQuarantineDuration(value)
			if err != nil {
				return 0, false, fmt.Errorf("provider %q update quarantine: %w", tool.Provider, err)
			}
			return duration, false, nil
		}
	}
	if strings.TrimSpace(settings.UpdateQuarantine) == "" {
		return 0, false, nil
	}
	duration, err := parseQuarantineDuration(settings.UpdateQuarantine)
	if err != nil {
		return 0, false, fmt.Errorf("update_quarantine: %w", err)
	}
	return duration, false, nil
}

func parseQuarantineDuration(raw string) (time.Duration, error) {
	value := strings.TrimSpace(strings.ToLower(raw))
	if value == "" || value == "0" || value == "0d" {
		return 0, nil
	}
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days < 0 {
			return 0, fmt.Errorf("invalid duration %q", raw)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", raw)
	}
	if duration < 0 {
		return 0, fmt.Errorf("invalid duration %q", raw)
	}
	return duration, nil
}

func updateMetadataProvider(tool *database.ToolCache) string {
	if tool == nil {
		return ""
	}
	if tool.InstalledWith != "" {
		return tool.InstalledWith
	}
	return tool.Provider
}
