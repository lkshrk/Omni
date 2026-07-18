// Package all links every built-in concrete provider so their package init()
// factory registrations run (see provider.RegisterConcrete). Import it for its
// side effects only:
//
//	import _ "github.com/lkshrk/omni/internal/provider/all"
//
// Adding a built-in concrete provider means adding one blank import here — the
// only wiring edit outside the provider's own package and the catalog.
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
