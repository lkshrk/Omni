package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestAppendUniqueStringValue(t *testing.T) {
	values := []string{"base", "work"}
	if got := appendUniqueStringValue(values, "work"); !reflect.DeepEqual(got, values) {
		t.Fatalf("append existing = %v, want %v", got, values)
	}
	got := appendUniqueStringValue(values, "personal")
	want := []string{"base", "work", "personal"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("append new = %v, want %v", got, want)
	}
}

func TestCompactDotGroupLabel(t *testing.T) {
	tests := []struct {
		name   string
		groups []string
		want   string
	}{
		{name: "none", want: ""},
		{name: "one", groups: []string{"base"}, want: "base"},
		{name: "two", groups: []string{"base", "work"}, want: "base,work"},
		{name: "many", groups: []string{"base", "work", "personal", "laptop"}, want: "base,work,+2"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := compactDotGroupLabel(tt.groups); got != tt.want {
				t.Fatalf("compactDotGroupLabel(%v) = %q, want %q", tt.groups, got, tt.want)
			}
		})
	}
}

func TestDotConflictIsManagedStowLink(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, tmp string) (entry dots.ResolvedEntry, stowPath string)
		want  bool
	}{
		{
			name: "symlink to stow path is managed",
			setup: func(t *testing.T, tmp string) (dots.ResolvedEntry, string) {
				stowDir := filepath.Join(tmp, "stow")
				stowPkg := filepath.Join(stowDir, "pkg")
				if err := os.MkdirAll(stowPkg, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(stowPkg, "file.txt"), []byte("stow"), 0o644); err != nil {
					t.Fatal(err)
				}
				sourceDir := filepath.Join(tmp, "source")
				if err := os.MkdirAll(sourceDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("src"), 0o644); err != nil {
					t.Fatal(err)
				}
				targetPath := filepath.Join(tmp, "target")
				if err := os.Symlink(stowPkg, targetPath); err != nil {
					t.Fatal(err)
				}
				entry := dots.ResolvedEntry{Name: "test", SourcePath: sourceDir, TargetPath: targetPath}
				return entry, stowDir
			},
			want: true,
		},
		{
			name: "symlink to unrelated path is not managed",
			setup: func(t *testing.T, tmp string) (dots.ResolvedEntry, string) {
				unrelatedDir := filepath.Join(tmp, "unrelated")
				if err := os.MkdirAll(unrelatedDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(unrelatedDir, "file.txt"), []byte("unrelated"), 0o644); err != nil {
					t.Fatal(err)
				}
				sourceDir := filepath.Join(tmp, "source")
				if err := os.MkdirAll(sourceDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("src"), 0o644); err != nil {
					t.Fatal(err)
				}
				targetPath := filepath.Join(tmp, "target")
				if err := os.Symlink(unrelatedDir, targetPath); err != nil {
					t.Fatal(err)
				}
				stowDir := filepath.Join(tmp, "stow")
				if err := os.MkdirAll(stowDir, 0o755); err != nil {
					t.Fatal(err)
				}
				entry := dots.ResolvedEntry{Name: "test", SourcePath: sourceDir, TargetPath: targetPath}
				return entry, stowDir
			},
			want: false,
		},
		{
			name: "missing target returns false",
			setup: func(t *testing.T, tmp string) (dots.ResolvedEntry, string) {
				sourceDir := filepath.Join(tmp, "source")
				if err := os.MkdirAll(sourceDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("src"), 0o644); err != nil {
					t.Fatal(err)
				}
				targetPath := filepath.Join(tmp, "nonexistent")
				stowDir := filepath.Join(tmp, "stow")
				if err := os.MkdirAll(stowDir, 0o755); err != nil {
					t.Fatal(err)
				}
				entry := dots.ResolvedEntry{Name: "test", SourcePath: sourceDir, TargetPath: targetPath}
				return entry, stowDir
			},
			want: false,
		},
		{
			name: "expected link returns false",
			setup: func(t *testing.T, tmp string) (dots.ResolvedEntry, string) {
				sourceDir := filepath.Join(tmp, "source")
				if err := os.MkdirAll(sourceDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("src"), 0o644); err != nil {
					t.Fatal(err)
				}
				targetPath := filepath.Join(tmp, "target")
				// correct link: target → source (not a wrong link)
				if err := os.Symlink(sourceDir, targetPath); err != nil {
					t.Fatal(err)
				}
				stowDir := filepath.Join(tmp, "stow")
				if err := os.MkdirAll(stowDir, 0o755); err != nil {
					t.Fatal(err)
				}
				entry := dots.ResolvedEntry{Name: "test", SourcePath: sourceDir, TargetPath: targetPath}
				return entry, stowDir
			},
			want: false,
		},
		{
			name: "directory with managed stow links",
			setup: func(t *testing.T, tmp string) (dots.ResolvedEntry, string) {
				stowDir := filepath.Join(tmp, "stow")
				stowPkg := filepath.Join(stowDir, "pkg")
				if err := os.MkdirAll(stowPkg, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(stowPkg, "file.txt"), []byte("stow"), 0o644); err != nil {
					t.Fatal(err)
				}
				sourceDir := filepath.Join(tmp, "source")
				if err := os.MkdirAll(sourceDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sourceDir, "wrong.txt"), []byte("src"), 0o644); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sourceDir, "correct.txt"), []byte("ok"), 0o644); err != nil {
					t.Fatal(err)
				}
				targetDir := filepath.Join(tmp, "targetdir")
				if err := os.MkdirAll(targetDir, 0o755); err != nil {
					t.Fatal(err)
				}
				// correctly linked to source (alphabetically first → visited before wrong link)
				if err := os.Symlink(filepath.Join(sourceDir, "correct.txt"), filepath.Join(targetDir, "correct.txt")); err != nil {
					t.Fatal(err)
				}
				// wrong-linked to old stow location
				if err := os.Symlink(filepath.Join(stowPkg, "file.txt"), filepath.Join(targetDir, "wrong.txt")); err != nil {
					t.Fatal(err)
				}
				entry := dots.ResolvedEntry{Name: "test", SourcePath: sourceDir, TargetPath: targetDir}
				return entry, stowDir
			},
			want: true,
		},
		{
			name: "directory with all links to old stow",
			setup: func(t *testing.T, tmp string) (dots.ResolvedEntry, string) {
				stowDir := filepath.Join(tmp, "stow")
				stowPkg := filepath.Join(stowDir, "pkg")
				if err := os.MkdirAll(stowPkg, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(stowPkg, "file.txt"), []byte("stow"), 0o644); err != nil {
					t.Fatal(err)
				}
				sourceDir := filepath.Join(tmp, "source")
				if err := os.MkdirAll(sourceDir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(sourceDir, "file.txt"), []byte("src"), 0o644); err != nil {
					t.Fatal(err)
				}
				targetDir := filepath.Join(tmp, "targetdir")
				if err := os.MkdirAll(targetDir, 0o755); err != nil {
					t.Fatal(err)
				}
				// all links point to old stow — zero correct links to source
				if err := os.Symlink(filepath.Join(stowPkg, "file.txt"), filepath.Join(targetDir, "file.txt")); err != nil {
					t.Fatal(err)
				}
				entry := dots.ResolvedEntry{Name: "test", SourcePath: sourceDir, TargetPath: targetDir}
				return entry, stowDir
			},
			want: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tmp := t.TempDir()
			entry, stowPath := tc.setup(t, tmp)
			got := dotConflictIsManagedStowLink(entry, stowPath)
			if got != tc.want {
				t.Fatalf("dotConflictIsManagedStowLink = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRestoreDotTargetAfterFailedRestow(t *testing.T) {
	t.Run("restores file backup", func(t *testing.T) {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "home")
		t.Setenv("HOME", home)

		backupPath := filepath.Join(tmp, "backup.txt")
		if err := os.WriteFile(backupPath, []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}
		targetPath := filepath.Join(tmp, "target.txt")
		if err := os.WriteFile(targetPath, []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}

		entry := dots.ResolvedEntry{Name: "test", TargetPath: targetPath}
		prep := preparedDotTarget{backupPath: backupPath, preservedDirectory: false}

		if err := restoreDotTargetAfterFailedRestow(entry, prep); err != nil {
			t.Fatalf("restoreDotTargetAfterFailedRestow: %v", err)
		}
		got, err := os.ReadFile(targetPath)
		if err != nil {
			t.Fatalf("read target: %v", err)
		}
		if string(got) != "original" {
			t.Fatalf("target content = %q, want %q", got, "original")
		}
	})

	t.Run("restores directory backup", func(t *testing.T) {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "home")
		t.Setenv("HOME", home)

		backupPath := filepath.Join(tmp, "backup")
		if err := os.MkdirAll(filepath.Join(backupPath, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(backupPath, "sub", "file.txt"), []byte("original"), 0o644); err != nil {
			t.Fatal(err)
		}

		targetDir := filepath.Join(tmp, "targetdir")
		if err := os.MkdirAll(targetDir, 0o755); err != nil {
			t.Fatal(err)
		}
		// partial content from failed restow
		if err := os.WriteFile(filepath.Join(targetDir, "partial.txt"), []byte("partial"), 0o644); err != nil {
			t.Fatal(err)
		}

		entry := dots.ResolvedEntry{Name: "test", TargetPath: targetDir}
		prep := preparedDotTarget{
			backupPath:          backupPath,
			preservedDirectory:  true,
			removedManagedPaths: true,
		}

		if err := restoreDotTargetAfterFailedRestow(entry, prep); err != nil {
			t.Fatalf("restoreDotTargetAfterFailedRestow: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(targetDir, "sub", "file.txt"))
		if err != nil {
			t.Fatalf("read restored file: %v", err)
		}
		if string(got) != "original" {
			t.Fatalf("restored file content = %q, want %q", got, "original")
		}
	})

	t.Run("no-op when no backup and preservedDirectory", func(t *testing.T) {
		tmp := t.TempDir()
		home := filepath.Join(tmp, "home")
		t.Setenv("HOME", home)

		entry := dots.ResolvedEntry{Name: "test", TargetPath: filepath.Join(tmp, "target")}
		prep := preparedDotTarget{
			backupPath:          "",
			preservedDirectory:  true,
			removedManagedPaths: false,
		}

		if err := restoreDotTargetAfterFailedRestow(entry, prep); err != nil {
			t.Fatalf("restoreDotTargetAfterFailedRestow: %v", err)
		}
	})
}
