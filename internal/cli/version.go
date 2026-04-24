package cli

// Version is set at build time via -ldflags "-X github.com/lkshrk/omni/internal/cli.Version=<tag>".
// Falls back to "dev" for local builds.
var Version = "dev"
