package tui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/lkshrk/omni/internal/app"
	"github.com/lkshrk/omni/internal/testguard"
)

const testObservationEnv = "OMNI_TEST_TUI_OBSERVATION"

var (
	createTestObservationTemp = os.CreateTemp
	renameTestObservation     = os.Rename
)

type testObservation struct {
	Tools  []testToolObservation `json:"tools,omitempty"`
	Dots   *testDotsObservation  `json:"dots,omitempty"`
	Doctor *app.DoctorResult     `json:"doctor,omitempty"`
}

type testToolObservation struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Package       string `json:"package"`
	Installed     bool   `json:"installed"`
	InstalledWith string `json:"installed_with,omitempty"`
	Version       string `json:"version,omitempty"`
	LatestVersion string `json:"latest_version,omitempty"`
	Tracked       bool   `json:"tracked"`
}

type testDotsObservation struct {
	Entries   []app.DotStatus `json:"entries"`
	GitStatus string          `json:"git_status"`
}

func observeTestTools(tools []*app.ToolView) {
	rows := make([]testToolObservation, 0, len(tools))
	for _, tool := range tools {
		if tool == nil {
			continue
		}
		rows = append(rows, testToolObservation{
			Name: tool.Name, Provider: tool.Provider, Package: tool.Package, Installed: tool.Installed,
			InstalledWith: tool.InstalledWith, Version: tool.Version, LatestVersion: tool.LatestVersion, Tracked: tool.Tracked,
		})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Name != rows[j].Name {
			return rows[i].Name < rows[j].Name
		}
		return rows[i].Provider < rows[j].Provider
	})
	updateTestObservation(func(packet *testObservation) { packet.Tools = rows })
}

func observeTestDots(entries []app.DotStatus, gitStatus string) {
	rows := append([]app.DotStatus(nil), entries...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	updateTestObservation(func(packet *testObservation) {
		packet.Dots = &testDotsObservation{Entries: rows, GitStatus: gitStatus}
	})
}

func observeTestDoctor(result *app.DoctorResult) {
	updateTestObservation(func(packet *testObservation) { packet.Doctor = result })
}

func updateTestObservation(update func(*testObservation)) {
	path := os.Getenv(testObservationEnv)
	if path == "" {
		return
	}
	if !testguard.Isolated() {
		panic("TUI test observation requires an isolated test sandbox")
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			panic(fmt.Sprintf("unsafe TUI test observation target %q", path))
		}
		if err := testguard.RequireTempPath("TUI test observation", path); err != nil {
			panic(err)
		}
	} else if os.IsNotExist(err) {
		if err := testguard.RequireTempEntryPath("TUI test observation", path); err != nil {
			panic(err)
		}
	} else {
		panic(err)
	}
	var packet testObservation
	if raw, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(raw, &packet); err != nil {
			panic(fmt.Errorf("decode TUI test observation: %w", err))
		}
	} else if !os.IsNotExist(err) {
		panic(err)
	}
	update(&packet)
	raw, err := json.Marshal(packet)
	if err != nil {
		panic(err)
	}
	dir := filepath.Dir(path)
	tmp, err := createTestObservationTemp(dir, ".tui-observation-*")
	if err != nil {
		panic(err)
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		panic(err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		panic(err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		panic(err)
	}
	if err := tmp.Close(); err != nil {
		panic(err)
	}
	if err := renameTestObservation(tmpPath, path); err != nil {
		panic(err)
	}
}
