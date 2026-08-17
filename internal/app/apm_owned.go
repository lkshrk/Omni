package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"

	"github.com/lkshrk/omni/internal/apm"
)

// apmOwnedSidecarName records which manifest entries the last generation wrote. Without it a config entry deleted by hand reads as an entry omni never declared, so the merge preserves it as foreign forever.
const apmOwnedSidecarName = ".omni-managed.json"

type apmSurfaceIdentities struct {
	Packages []string `json:"packages"`
	Mcp      []string `json:"mcp"`
}

// Rendered is what omni last wrote into the manifest, Applied what an install then deployed; a failed install leaves them apart, and only Applied gates a prune, so the retry the failure owes is still outstanding.
type apmOwnedIdentities struct {
	Rendered apmSurfaceIdentities `json:"rendered"`
	Applied  apmSurfaceIdentities `json:"applied"`
}

// apmOwnedSidecarFile decodes both layouts; the pre-split record listed one set per surface, which is what its installs had applied.
type apmOwnedSidecarFile struct {
	Packages []string              `json:"packages"`
	Mcp      []string              `json:"mcp"`
	Rendered *apmSurfaceIdentities `json:"rendered"`
	Applied  *apmSurfaceIdentities `json:"applied"`
}

func apmOwnedSidecarPath(manifestPath string) string {
	return filepath.Join(filepath.Dir(manifestPath), apmOwnedSidecarName)
}

// A missing or unreadable sidecar reports nothing: the worst case is the previous behaviour, not a failed sync.
func readAPMOwnedIdentities(manifestPath string) apmOwnedIdentities {
	raw, err := os.ReadFile(apmOwnedSidecarPath(manifestPath))
	if err != nil {
		return apmOwnedIdentities{}
	}
	var file apmOwnedSidecarFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return apmOwnedIdentities{}
	}
	if file.Rendered == nil && file.Applied == nil {
		return apmOwnedIdentities{
			Rendered: apmSurfaceIdentities{Packages: slices.Clone(file.Packages), Mcp: slices.Clone(file.Mcp)},
			Applied:  apmSurfaceIdentities{Packages: slices.Clone(file.Packages), Mcp: slices.Clone(file.Mcp)},
		}
	}
	var owned apmOwnedIdentities
	if file.Rendered != nil {
		owned.Rendered = *file.Rendered
	}
	if file.Applied != nil {
		owned.Applied = *file.Applied
	}
	return owned
}

// ownedPackages / ownedMcp answer whose an entry is: one omni rendered but never installed is still omni's to retire, or the config dropping it would strand it in the manifest as foreign forever.
func (o apmOwnedIdentities) ownedPackages() []string {
	return append(slices.Clone(o.Rendered.Packages), o.Applied.Packages...)
}

func (o apmOwnedIdentities) ownedMcp() []string {
	return append(slices.Clone(o.Rendered.Mcp), o.Applied.Mcp...)
}

func (o apmOwnedIdentities) empty() bool {
	return len(o.ownedPackages()) == 0 && len(o.ownedMcp()) == 0
}

// advanceAPMApplied records one surface's identities once its install succeeded. The record is re-read first, so
// the surface that failed earlier in the same sync keeps the identities its prune still has to retire.
func advanceAPMApplied(manifestPath, surface string, identities []string) error {
	owned := readAPMOwnedIdentities(manifestPath)
	if surface == apm.SurfaceMcp {
		owned.Applied.Mcp = identities
	} else {
		owned.Applied.Packages = identities
	}
	return writeAPMOwnedIdentities(manifestPath, owned)
}

// advanceAPMRendered records what the manifest write just declared, whether or not the install that follows succeeds.
func advanceAPMRendered(manifestPath string, rendered apmSurfaceIdentities, packages, mcp bool) error {
	owned := readAPMOwnedIdentities(manifestPath)
	if packages {
		owned.Rendered.Packages = rendered.Packages
	}
	if mcp {
		owned.Rendered.Mcp = rendered.Mcp
	}
	return writeAPMOwnedIdentities(manifestPath, owned)
}

func writeAPMOwnedIdentities(manifestPath string, owned apmOwnedIdentities) error {
	for _, surface := range []*apmSurfaceIdentities{&owned.Rendered, &owned.Applied} {
		sort.Strings(surface.Packages)
		sort.Strings(surface.Mcp)
	}
	raw, err := json.MarshalIndent(owned, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the APM ownership record: %w", err)
	}
	path := apmOwnedSidecarPath(manifestPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating APM manifest directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), apmOwnedSidecarName+".*")
	if err != nil {
		return fmt.Errorf("writing the APM ownership record: %w", err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("writing the APM ownership record: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("writing the APM ownership record: %w", err)
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return fmt.Errorf("writing the APM ownership record: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("writing the APM ownership record: %w", err)
	}
	return nil
}

func identitySet(names []string) map[string]bool {
	set := make(map[string]bool, len(names))
	for _, name := range names {
		if name != "" {
			set[name] = true
		}
	}
	return set
}

// unionIdentities — an entry the previous generation owned stays omni's even after the config drops it, so the next render retires it instead of adopting it as someone else's.
func unionIdentities(current map[string]bool, previous []string) map[string]bool {
	merged := make(map[string]bool, len(current)+len(previous))
	for name := range current {
		merged[name] = true
	}
	for _, name := range previous {
		if name != "" {
			merged[name] = true
		}
	}
	return merged
}
