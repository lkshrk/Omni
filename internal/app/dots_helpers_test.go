package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/dots"
)

func TestAppendUniqueStringValue(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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
				if err := os.Symlink(filepath.Join(sourceDir, "correct.txt"), filepath.Join(targetDir, "correct.txt")); err != nil {
					t.Fatal(err)
				}
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
			got := dots.ConflictIsManagedStowLink(entry, stowPath)
			if got != tc.want {
				t.Fatalf("dots.ConflictIsManagedStowLink = %v, want %v", got, tc.want)
			}
		})
	}
}
