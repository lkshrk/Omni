package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func importBackupApp(t *testing.T) (*App, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("OMNI_STATE_DIR", filepath.Join(home, "state"))
	stateDir := filepath.Join(home, "state")
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	template := filepath.Join(home, ".config", "omni", "apm.yml")
	writeFile(t, template, agentsMigrationMarker+"\nname: before\nversion: 1.0.0\ndependencies: {}\n")
	return &App{StateDir: stateDir}, home, template
}

func TestCreateImportBackupRecordsContentAndAbsence(t *testing.T) {
	a, _, template := importBackupApp(t)

	dir, err := a.CreateImportBackup("testhost")
	if err != nil {
		t.Fatalf("CreateImportBackup: %v", err)
	}
	backup, err := readImportBackup(dir)
	if err != nil {
		t.Fatalf("readImportBackup: %v", err)
	}

	var sawTemplate, sawAbsent bool
	for _, e := range backup.Entries {
		if e.Path == template {
			sawTemplate = true
			if e.Absent || e.SHA256 == "" {
				t.Fatalf("template entry = %+v; want present with a hash", e)
			}
		}
		if e.Absent {
			sawAbsent = true
		}
	}
	if !sawTemplate {
		t.Fatalf("entries %+v do not cover the template", backup.Entries)
	}
	if !sawAbsent {
		t.Fatal("no absent entry recorded; the state marker does not exist yet and must be recorded as absent")
	}
	if err := VerifyImportBackup(dir); err != nil {
		t.Fatalf("VerifyImportBackup: %v", err)
	}
}

func TestRestoreImportBackupPutsContentBackAndRemovesWhatWasAbsent(t *testing.T) {
	a, _, template := importBackupApp(t)
	dir, err := a.CreateImportBackup("testhost")
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(a.StateDir, templateStateName)

	writeFile(t, template, agentsMigrationMarker+"\nname: after\nversion: 2.0.0\ndependencies: {}\n")
	writeFile(t, marker, "deadbeef\n")

	if err := RestoreImportBackup(dir); err != nil {
		t.Fatalf("RestoreImportBackup: %v", err)
	}
	raw, err := os.ReadFile(template)
	if err != nil || !strings.Contains(string(raw), "name: before") {
		t.Fatalf("template = %q, %v; want the pre-import content", raw, err)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker still present (%v); a file absent at backup time must be absent after restore", err)
	}
}

func TestVerifyImportBackupRejectsTamperedCopy(t *testing.T) {
	a, _, _ := importBackupApp(t)
	dir, err := a.CreateImportBackup("testhost")
	if err != nil {
		t.Fatal(err)
	}
	backup, err := readImportBackup(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range backup.Entries {
		if e.Absent {
			continue
		}
		writeFile(t, filepath.Join(dir, e.Copy), "tampered\n")
		break
	}
	if err := VerifyImportBackup(dir); err == nil {
		t.Fatal("VerifyImportBackup accepted a tampered copy")
	}
	if err := RestoreImportBackup(dir); err == nil {
		t.Fatal("RestoreImportBackup restored from an unverified backup")
	}
}

func TestCreateImportBackupPinsOnlyTheFirst(t *testing.T) {
	a, _, _ := importBackupApp(t)
	first, err := a.CreateImportBackup("testhost")
	if err != nil {
		t.Fatal(err)
	}
	second, err := a.CreateImportBackup("testhost")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two backups collapsed into one directory")
	}
	if _, err := os.Lstat(filepath.Join(first, importBackupPinned)); err != nil {
		t.Fatalf("first backup is not pinned: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(second, importBackupPinned)); !os.IsNotExist(err) {
		t.Fatalf("second backup was pinned (%v); only the pre-import state is pinned", err)
	}
}

func TestAssertBackupDisjointRejectsABackupInsideTheBoundary(t *testing.T) {
	guarded := []string{"/home/u/.apm", "/home/u/.config/omni"}
	for _, bad := range []string{"/home/u/.apm/backups", "/home/u/.config/omni/backups", "/home/u/.apm"} {
		if err := assertBackupDisjoint(bad, guarded); err == nil {
			t.Fatalf("accepted a backup root inside a managed tree: %s", bad)
		}
	}
	if err := assertBackupDisjoint("/home/u/.local/state/omni", guarded); err != nil {
		t.Fatalf("rejected the state dir, which is where backups belong: %v", err)
	}
}

func TestCreateImportBackupRootIsPrivate(t *testing.T) {
	a, _, _ := importBackupApp(t)
	dir, err := a.CreateImportBackup("testhost")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o700 {
		t.Fatalf("backup dir mode = %04o, want 0700", perm)
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := VerifyImportBackup(dir); err == nil {
		t.Fatal("VerifyImportBackup accepted a world-readable backup holding credentials-adjacent state")
	}
}
