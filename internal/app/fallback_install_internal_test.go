package app

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ulikunitz/xz"
)

// --- isChecksumAsset ---

func TestIsChecksumAsset(t *testing.T) {
	yes := []string{
		"sha256sums",
		"SHA256SUMS",
		"sha256sums.txt",
		"SHA256SUMS.TXT",
		"checksums.txt",
		"fd_checksums.txt",
		"fd-checksums.txt",
		"tool_v1.0.0_checksums.txt",
		"binary.sha256sum",
		"binary.sha256sums",
	}
	no := []string{
		"fd.tar.gz",
		"fd.zip",
		"fd.tar.xz",
		"fd.sig",
		"fd.asc",
		"fd_README.md",
	}
	for _, name := range yes {
		if !isChecksumAsset(strings.ToLower(name)) {
			t.Errorf("isChecksumAsset(%q) = false, want true", name)
		}
	}
	for _, name := range no {
		if isChecksumAsset(strings.ToLower(name)) {
			t.Errorf("isChecksumAsset(%q) = true, want false", name)
		}
	}
}

// --- extractChecksumForFile ---

func TestExtractChecksumForFile_MatchesFilename(t *testing.T) {
	content := "abc123  fd_1.0_darwin_arm64.tar.gz\ndef456  fd_1.0_linux_amd64.tar.gz\n"
	got, err := extractChecksumForFile(strings.NewReader(content), "fd_1.0_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc123" {
		t.Errorf("got %q, want abc123", got)
	}
}

func TestExtractChecksumForFile_StripsLeadingPath(t *testing.T) {
	content := "abc123  ./dist/fd_1.0_darwin_arm64.tar.gz\n"
	got, err := extractChecksumForFile(strings.NewReader(content), "fd_1.0_darwin_arm64.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc123" {
		t.Errorf("got %q, want abc123", got)
	}
}

func TestExtractChecksumForFile_NotFound(t *testing.T) {
	content := "abc123  other_file.tar.gz\n"
	_, err := extractChecksumForFile(strings.NewReader(content), "fd_1.0_darwin_arm64.tar.gz")
	if err == nil {
		t.Fatal("expected error for missing entry, got nil")
	}
}

func TestExtractChecksumForFile_SkipsComments(t *testing.T) {
	content := "# comment\nabc123  target.tar.gz\n"
	got, err := extractChecksumForFile(strings.NewReader(content), "target.tar.gz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "abc123" {
		t.Errorf("got %q, want abc123", got)
	}
}

// --- verifyFileChecksum ---

func TestVerifyFileChecksum_Match(t *testing.T) {
	content := []byte("hello world")
	sum := sha256.Sum256(content)
	expected := hex.EncodeToString(sum[:])

	f, err := os.CreateTemp(t.TempDir(), "checkfile-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write(content); err != nil {
		t.Fatal(err)
	}
	f.Close()

	if err := verifyFileChecksum(f.Name(), expected, "test"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestVerifyFileChecksum_Mismatch(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "checkfile-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("real content")); err != nil {
		t.Fatal(err)
	}
	f.Close()

	err = verifyFileChecksum(f.Name(), "deadbeef", "test")
	if err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
	if !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("error %q does not mention checksum mismatch", err)
	}
}

// --- extractAndInstall: zip ---

func TestExtractAndInstall_Zip(t *testing.T) {
	dir := t.TempDir()
	content := []byte("#!/bin/sh\nexit 0\n")
	archivePath := filepath.Join(dir, "tool_v1.0_darwin_arm64.zip")

	// Build a zip with a nested path.
	zf, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("tool_v1.0_darwin_arm64/mytool")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(content); err != nil {
		t.Fatal(err)
	}
	zw.Close()
	zf.Close()

	destPath := filepath.Join(dir, "mytool")
	if err := extractAndInstall(archivePath, "tool_v1.0_darwin_arm64.zip", "mytool", "", destPath); err != nil {
		t.Fatalf("extractAndInstall: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("installed content = %q, want %q", got, content)
	}
	info, err := os.Stat(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("installed binary is not executable")
	}
}

func TestExtractAndInstall_ZipExactPath(t *testing.T) {
	dir := t.TempDir()
	content := []byte("correct binary")
	wrong := []byte("wrong binary")
	archivePath := filepath.Join(dir, "archive.zip")

	zf, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	// Write a file with the same base name but different path.
	w1, _ := zw.Create("other/mytool")
	w1.Write(wrong) //nolint:errcheck
	// Write the exact binaryPath target.
	w2, _ := zw.Create("exact/path/mytool")
	w2.Write(content) //nolint:errcheck
	zw.Close()
	zf.Close()

	destPath := filepath.Join(dir, "mytool")
	if err := extractAndInstall(archivePath, "archive.zip", "mytool", "exact/path/mytool", destPath); err != nil {
		t.Fatalf("extractAndInstall: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("exact path: got %q, want %q", got, content)
	}
}

func TestExtractAndInstall_ZipBinaryNotFound(t *testing.T) {
	dir := t.TempDir()
	archivePath := filepath.Join(dir, "archive.zip")
	zf, _ := os.Create(archivePath)
	zw := zip.NewWriter(zf)
	w, _ := zw.Create("other_binary")
	w.Write([]byte("data")) //nolint:errcheck
	zw.Close()
	zf.Close()

	err := extractAndInstall(archivePath, "archive.zip", "mytool", "", filepath.Join(dir, "mytool"))
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

// --- extractAndInstall: tar.gz ---

func TestExtractAndInstall_TarGz(t *testing.T) {
	dir := t.TempDir()
	content := []byte("#!/bin/sh\nexit 0\n")
	archivePath := filepath.Join(dir, "tool.tar.gz")
	writeTarGz(t, archivePath, "dir/mytool", content)

	destPath := filepath.Join(dir, "mytool")
	if err := extractAndInstall(archivePath, "tool.tar.gz", "mytool", "", destPath); err != nil {
		t.Fatalf("extractAndInstall tar.gz: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("tar.gz: got %q, want %q", got, content)
	}
	info, _ := os.Stat(destPath)
	if info.Mode()&0o111 == 0 {
		t.Error("tar.gz: installed binary is not executable")
	}
}

// --- extractAndInstall: tar.xz ---

func TestExtractAndInstall_TarXz(t *testing.T) {
	dir := t.TempDir()
	content := []byte("#!/bin/sh\nexit 0\n")
	archivePath := filepath.Join(dir, "tool.tar.xz")
	writeTarXz(t, archivePath, "dir/mytool", content)

	destPath := filepath.Join(dir, "mytool")
	if err := extractAndInstall(archivePath, "tool.tar.xz", "mytool", "", destPath); err != nil {
		t.Fatalf("extractAndInstall tar.xz: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("tar.xz: got %q, want %q", got, content)
	}
	info, _ := os.Stat(destPath)
	if info.Mode()&0o111 == 0 {
		t.Error("tar.xz: installed binary is not executable")
	}
}

// --- extractAndInstall: raw binary ---

func TestExtractAndInstall_RawBinary(t *testing.T) {
	dir := t.TempDir()
	content := []byte("ELF binary data")
	srcPath := filepath.Join(dir, "mytool-v1.0-raw")
	if err := os.WriteFile(srcPath, content, 0o644); err != nil {
		t.Fatal(err)
	}

	destPath := filepath.Join(dir, "mytool")
	if err := extractAndInstall(srcPath, "mytool-v1.0-raw", "mytool", "", destPath); err != nil {
		t.Fatalf("extractAndInstall raw: %v", err)
	}
	got, err := os.ReadFile(destPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Errorf("raw: got %q, want %q", got, content)
	}
	info, _ := os.Stat(destPath)
	if info.Mode()&0o111 == 0 {
		t.Error("raw: installed binary is not executable")
	}
}

// --- helpers ---

func writeTarGz(t *testing.T, path, entryName string, content []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	gw := gzip.NewWriter(f)
	defer gw.Close() //nolint:errcheck
	tw := tar.NewWriter(gw)
	defer tw.Close() //nolint:errcheck
	hdr := &tar.Header{
		Name: entryName,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
}

func writeTarXz(t *testing.T, path, entryName string, content []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck
	xw, err := xz.NewWriter(f)
	if err != nil {
		t.Fatalf("xz.NewWriter: %v", err)
	}
	defer xw.Close() //nolint:errcheck
	tw := tar.NewWriter(xw)
	defer tw.Close() //nolint:errcheck
	hdr := &tar.Header{
		Name: entryName,
		Mode: 0o755,
		Size: int64(len(content)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
}
