package app

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const bootstrapCompleteValue = "complete"

// BootstrapRequired reports whether the active host has not yet completed the
// local bootstrap flow for this config.
func (a *App) BootstrapRequired(ctx context.Context) (bool, error) {
	info, err := a.HostStatus()
	if err != nil {
		return false, err
	}
	if info == nil || info.Active == "" {
		return false, nil
	}
	completed, err := a.HostBootstrapCompleted(ctx, info.Active)
	if err != nil {
		return false, err
	}
	return !completed, nil
}

// HostBootstrapCompleted reports whether host bootstrap has been marked
// complete in the local cache DB.
func (a *App) HostBootstrapCompleted(ctx context.Context, host string) (bool, error) {
	host = strings.TrimSpace(machineGroupName(host))
	if host == "" {
		return false, fmt.Errorf("hostname is required")
	}
	db := a.readDB()
	if db == nil {
		return false, fmt.Errorf("database is not initialised")
	}
	value, err := db.GetState(ctx, a.bootstrapStateKey(host))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return value == bootstrapCompleteValue, nil
}

// MarkHostBootstrapCompleted records that the local machine has passed the
// bootstrap activation flow for the current config and host.
func (a *App) MarkHostBootstrapCompleted(ctx context.Context, host string) error {
	host = strings.TrimSpace(machineGroupName(host))
	if host == "" {
		return fmt.Errorf("hostname is required")
	}
	db := a.readDB()
	if db == nil {
		return fmt.Errorf("database is not initialised")
	}
	return db.SetState(ctx, a.bootstrapStateKey(host), bootstrapCompleteValue)
}

func (a *App) bootstrapStateKey(host string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(a.ConfigPath)))
	return fmt.Sprintf("bootstrap.completed.%s.%x", host, sum[:16])
}
