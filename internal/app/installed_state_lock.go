package app

import (
	"fmt"
	"os"

	"github.com/lkshrk/omni/internal/flock"
	"github.com/lkshrk/omni/internal/testguard"
)

func (a *App) lockInstalledStateFile(shared bool) (func(), error) {
	if a.DBPath == "" {
		return nil, fmt.Errorf("installed-state lock requires an initialized database")
	}
	path := a.DBPath + ".installed.lock"
	if err := testguard.RequireTempPath("installed-state lock", path); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening installed-state lock: %w", err)
	}
	if shared {
		err = flock.RLock(file)
	} else {
		err = flock.Lock(file)
	}
	if err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("locking installed state: %w", err)
	}
	return func() {
		_ = flock.Unlock(file)
		_ = file.Close()
	}, nil
}
