// Package all is imported for side effects: its blank imports run each provider's init() registration.
package all

import (
	_ "github.com/lkshrk/omni/internal/provider/apk"
	_ "github.com/lkshrk/omni/internal/provider/apt"
	_ "github.com/lkshrk/omni/internal/provider/aptrepo"
	_ "github.com/lkshrk/omni/internal/provider/brew"
	_ "github.com/lkshrk/omni/internal/provider/cargo"
	_ "github.com/lkshrk/omni/internal/provider/dnf"
	_ "github.com/lkshrk/omni/internal/provider/pacman"
	_ "github.com/lkshrk/omni/internal/provider/pip"
	_ "github.com/lkshrk/omni/internal/provider/script"
	_ "github.com/lkshrk/omni/internal/provider/zypper"
)
