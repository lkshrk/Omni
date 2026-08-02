package app

import (
	"archive/tar"
	"archive/zip"
	"bufio"
	"compress/bzip2"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ulikunitz/xz"

	"github.com/lkshrk/omni/internal/config"
)

const (
	downloadTimeout = 5 * time.Minute
	downloadRetries = 3
	// Caps the asset download to prevent disk-fill from a malicious or corrupted release.
	maxDownloadBytes = 512 << 20
	// maxEntryBytes caps each extracted tar/zip entry individually.
	maxEntryBytes = 512 << 20
	// Keeps remote error pages useful without flooding tool-sync output.
	maxGitHubErrorTextBytes = 4 << 10
	// A remote rate-limit response must not stall a sync indefinitely.
	maxGitHubRetryDelay = 30 * time.Second
)

// Shared by the native download and the generated curl command so one policy governs both fetch paths.
var errAssetDownloadURLScheme = errors.New("asset_download_url must use https; the asset is downloaded and made executable, so plain http lets an on-path attacker choose the binary")

var errRedirectSchemeDowngrade = errors.New("refusing an https to plain-http redirect; the response is installed as an executable or trusted as a checksum")

// The redirect guard only covers later hops; a digest an on-path attacker can rewrite is not a digest.
var errChecksumURLScheme = errors.New("checksums asset URL must use https; a checksum fetched over plain http can be rewritten by an on-path attacker")

// Over plain http an on-path attacker owns this trust root and picks the asset the pipeline downloads.
var errGitHubAPIBaseScheme = errors.New("github api base must use https; the release metadata it returns names the asset that gets downloaded and executed")

// Standard library only: no curl, tar, or unzip runtime dependencies.
func (a *App) nativeGitHubInstallPipeline(ctx context.Context, name string, fallback *config.FallbackSpec) error {
	recipe := fallback.Recipe
	downloadURL := strings.TrimSpace(recipe.AssetDownloadURL)
	if downloadURL == "" {
		return fmt.Errorf("fallback %s: native install requires a resolved asset_download_url", name)
	}
	// Ahead of every dial and every mkdir: the asset is chmod +x'd and executed, so a plain-http fetch lets an on-path attacker choose the binary.
	if !config.IsHTTPSURL(downloadURL) {
		return fmt.Errorf("fallback %s: %w: %q", name, errAssetDownloadURLScheme, downloadURL)
	}
	rawAssetName := strings.TrimSpace(recipe.AssetName)
	if rawAssetName == "" {
		rawAssetName = filepath.Base(downloadURL)
	}
	// filepath.Base strips traversal; the degenerate ".", ".." and "" it can still return are rejected below.
	assetName := filepath.Base(rawAssetName)
	if assetName == "" || assetName == "." || assetName == ".." {
		return fmt.Errorf("fallback %s: asset_name %q is not a valid filename", name, rawAssetName)
	}

	cacheDir, err := a.fallbackCacheDir()
	if err != nil {
		return err
	}
	binDir, err := a.fallbackBinDir(fallback, cacheDir)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return fmt.Errorf("fallback %s: create cache dir: %w", name, err)
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return fmt.Errorf("fallback %s: create bin dir: %w", name, err)
	}

	assetPath := filepath.Join(cacheDir, assetName)
	// Containment assertion: the resolved path must remain inside cacheDir.
	if !strings.HasPrefix(assetPath, cacheDir+string(filepath.Separator)) {
		return fmt.Errorf("fallback %s: asset path %q escapes cache dir", name, assetPath)
	}
	if err := a.downloadFallbackAsset(ctx, name, downloadURL, assetPath); err != nil {
		return err
	}

	// Checksum verification: best-effort fetch; mismatch is a hard failure.
	if err := a.verifyFallbackChecksum(ctx, name, fallback, assetPath, assetName); err != nil {
		return err
	}

	rawBinary := strings.TrimSpace(fallback.Binary)
	if rawBinary == "" {
		rawBinary = name
	}
	binary := filepath.Base(rawBinary)
	if binary == "" || binary == "." || binary == ".." {
		return fmt.Errorf("fallback %s: binary %q is not a valid filename", name, rawBinary)
	}
	destPath := filepath.Join(binDir, binary)
	// Containment assertion: the binary destination must remain inside binDir.
	if !strings.HasPrefix(destPath, binDir+string(filepath.Separator)) {
		return fmt.Errorf("fallback %s: binary path %q escapes bin dir", name, destPath)
	}
	if err := extractAndInstall(assetPath, assetName, binary, fallback.Recipe.BinaryPath, destPath); err != nil {
		return fmt.Errorf("fallback %s: %w", name, err)
	}
	return nil
}

type errNoRetry struct{ cause error }

func (e errNoRetry) Error() string { return e.cause.Error() }
func (e errNoRetry) Unwrap() error { return e.cause }

type nativeFallbackBinaryBackup struct {
	destination string
	path        string
	existed     bool
}

func (a *App) backupNativeFallbackBinary(name string, fallback *config.FallbackSpec) (*nativeFallbackBinaryBackup, error) {
	cacheDir, err := a.fallbackCacheDir()
	if err != nil {
		return nil, err
	}
	binDir, err := a.fallbackBinDir(fallback, cacheDir)
	if err != nil {
		return nil, err
	}
	rawBinary := strings.TrimSpace(fallback.Binary)
	if rawBinary == "" {
		rawBinary = name
	}
	binary := filepath.Base(rawBinary)
	if binary == "" || binary == "." || binary == ".." {
		return nil, fmt.Errorf("fallback %s: binary %q is not a valid filename", name, rawBinary)
	}
	destination := filepath.Join(binDir, binary)
	backup := &nativeFallbackBinaryBackup{destination: destination}
	src, err := os.Open(destination)
	if os.IsNotExist(err) {
		return backup, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open existing fallback binary: %w", err)
	}
	defer src.Close() //nolint:errcheck
	info, err := src.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat existing fallback binary: %w", err)
	}
	tmp, err := os.CreateTemp(binDir, ".omni-upgrade-backup-*")
	if err != nil {
		return nil, fmt.Errorf("create fallback binary backup: %w", err)
	}
	backup.path = tmp.Name()
	if _, err := io.Copy(tmp, src); err != nil {
		_ = tmp.Close()
		backup.cleanup()
		return nil, fmt.Errorf("copy fallback binary backup: %w", err)
	}
	if err := tmp.Chmod(info.Mode().Perm()); err != nil {
		_ = tmp.Close()
		backup.cleanup()
		return nil, fmt.Errorf("set fallback binary backup mode: %w", err)
	}
	if err := tmp.Close(); err != nil {
		backup.cleanup()
		return nil, fmt.Errorf("close fallback binary backup: %w", err)
	}
	backup.existed = true
	return backup, nil
}

func (b *nativeFallbackBinaryBackup) restore() error {
	if b == nil {
		return nil
	}
	if !b.existed {
		if err := os.Remove(b.destination); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	if err := os.Rename(b.path, b.destination); err != nil {
		return err
	}
	b.path = ""
	return nil
}

func (b *nativeFallbackBinaryBackup) cleanup() {
	if b != nil && b.path != "" {
		_ = os.Remove(b.path)
		b.path = ""
	}
}

// A 4xx is not retried because the resource is definitively absent or forbidden.
func (a *App) downloadFallbackAsset(ctx context.Context, name, downloadURL, destPath string) error {
	client := *a.githubHTTPClient()
	// Asset downloads own their longer deadline through downloadToFile's request context.
	client.Timeout = 0
	var lastErr error
	for attempt := range downloadRetries {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("fallback %s: download cancelled: %w", name, ctx.Err())
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		lastErr = downloadToFile(ctx, &client, downloadURL, destPath)
		if lastErr == nil {
			return nil
		}
		var noRetry errNoRetry
		if errors.As(lastErr, &noRetry) {
			break
		}
	}
	return fmt.Errorf("fallback %s: download %s: %w", name, downloadURL, lastErr)
}

// Writes atomically: temp file then rename.
func downloadToFile(ctx context.Context, client *http.Client, rawURL, destPath string) error {
	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "omni")
	attachGitHubToken(req, rawURL)

	resp, err := doGitHubRequest(dlCtx, client, req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode == http.StatusNoContent {
		return errNoRetry{fmt.Errorf("HTTP %s: response has no content", resp.Status)}
	}
	if contentType := strings.TrimSpace(resp.Header.Get("Content-Type")); strings.HasPrefix(strings.ToLower(contentType), "text/html") {
		return errNoRetry{fmt.Errorf("HTTP %s: unexpected Content-Type %q", resp.Status, contentType)}
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".omni-dl-*")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	// Remove the temp file on error; no-op after successful rename.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()

	// Guard against a runaway response body filling the disk.
	limited := io.LimitReader(resp.Body, maxDownloadBytes+1)
	n, err := io.Copy(tmp, limited)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write download: %w", err)
	}
	if n > maxDownloadBytes {
		_ = tmp.Close()
		return fmt.Errorf("download exceeds %d MiB limit", maxDownloadBytes>>20)
	}
	if n == 0 {
		_ = tmp.Close()
		return errNoRetry{fmt.Errorf("empty download")}
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return fmt.Errorf("install download: %w", err)
	}
	success = true
	return nil
}

// A missing checksums asset is best-effort nil; a mismatch is always a hard failure.
func (a *App) verifyFallbackChecksum(ctx context.Context, name string, fallback *config.FallbackSpec, assetPath, assetName string) error {
	checksumAsset := fallbackChecksumAssetName(name, fallback)
	required := checksumAsset != ""
	// A stored digest is reused only when its recorded asset scope still matches the asset being installed.
	assetID := strings.TrimSpace(fallback.Recipe.AssetID)
	if stored := strings.TrimSpace(fallback.Recipe.Checksum); stored != "" {
		scope := strings.TrimSpace(fallback.Recipe.ChecksumAssetID)
		if scope != "" && assetID != "" && scope == assetID {
			return verifyFileChecksum(assetPath, stored, name)
		}
	}

	owner := strings.TrimSpace(fallback.Source.Owner)
	repo := strings.TrimSpace(fallback.Source.Repo)
	tagName := strings.TrimSpace(fallback.Recipe.TagName)
	if owner == "" || repo == "" || tagName == "" {
		if required {
			return fmt.Errorf("fallback %s: checksum manifest requires GitHub owner, repo, and tag", name)
		}
		return nil
	}

	digest, err := a.fetchReleaseChecksum(ctx, owner, repo, tagName, assetName, checksumAsset)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return fmt.Errorf("fallback %s: checksum fetch: %w", name, err)
		}
		if required {
			return fmt.Errorf("fallback %s: checksum fetch: %w", name, err)
		}
		// Best-effort: absent or unreachable checksums do not block install.
		return nil
	}

	if err := verifyFileChecksum(assetPath, digest, name); err != nil {
		return err
	}

	// Persist so future installs skip the network fetch, scoped to this asset.
	fallback.Recipe.Checksum = digest
	fallback.Recipe.ChecksumAssetID = assetID
	return nil
}

func fallbackChecksumAssetName(name string, fallback *config.FallbackSpec) string {
	pattern := strings.TrimSpace(fallback.Recipe.ChecksumAssetPattern)
	if pattern == "" {
		return ""
	}
	binary := strings.TrimSpace(fallback.Binary)
	if binary == "" {
		binary = name
	}
	version := strings.TrimPrefix(strings.TrimSpace(fallback.Recipe.TagName), "v")
	pattern = strings.ReplaceAll(pattern, "{binary}", binary)
	pattern = strings.ReplaceAll(pattern, "{version}", version)
	pattern = strings.ReplaceAll(pattern, "{os}", runtime.GOOS)
	return strings.ReplaceAll(pattern, "{arch}", runtime.GOARCH)
}

func (a *App) fetchReleaseChecksum(ctx context.Context, owner, repo, tagName, assetName string, checksumAssetPattern ...string) (string, error) {
	release, err := a.fetchGitHubReleaseByTag(ctx, owner, repo, tagName)
	if err != nil {
		return "", err
	}

	client := a.githubHTTPClient()
	var assetErrs []error
	recognized := 0
	configuredAsset := ""
	if len(checksumAssetPattern) > 0 {
		configuredAsset = strings.TrimSpace(checksumAssetPattern[0])
	}
	for _, asset := range release.Assets {
		if configuredAsset != "" && asset.Name != configuredAsset {
			continue
		}
		if configuredAsset == "" && !isChecksumAsset(strings.ToLower(asset.Name)) {
			continue
		}
		recognized++
		if err := ctx.Err(); err != nil {
			return "", err
		}

		checksumURL := asset.BrowserDownloadURL
		if !config.IsHTTPSURL(checksumURL) {
			assetErrs = append(assetErrs, fmt.Errorf("checksum asset %q: %w: %q", asset.Name, errChecksumURLScheme, checksumURL))
			continue
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
		if err != nil {
			assetErrs = append(assetErrs, fmt.Errorf("checksum asset %q: %w", asset.Name, err))
			continue
		}
		req.Header.Set("User-Agent", "omni")
		attachGitHubToken(req, checksumURL)

		resp, err := doGitHubRequest(ctx, client, req)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", err
			}
			assetErrs = append(assetErrs, fmt.Errorf("checksum asset %q: %w", asset.Name, err))
			continue
		}
		digest, parseErr := extractChecksumForFile(resp.Body, assetName)
		_ = resp.Body.Close()
		if parseErr == nil {
			return digest, nil
		}
		if errors.Is(parseErr, context.Canceled) || errors.Is(parseErr, context.DeadlineExceeded) {
			return "", parseErr
		}
		assetErrs = append(assetErrs, fmt.Errorf("checksum asset %q: %w", asset.Name, parseErr))
	}
	if recognized == 0 && configuredAsset != "" {
		return "", fmt.Errorf("checksum asset %q not found in release %s/%s %s", configuredAsset, owner, repo, tagName)
	}
	if recognized == 0 {
		return "", fmt.Errorf("no checksums asset in release %s/%s %s", owner, repo, tagName)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no usable checksum entry for %q: %w", assetName, errors.Join(assetErrs...))
}

func (a *App) fetchGitHubReleaseByTag(ctx context.Context, owner, repo, tagName string) (githubRelease, error) {
	client := a.githubHTTPClient()
	req, err := a.newGitHubAPIRequest(ctx, "/repos/"+owner+"/"+repo+"/releases/tags/"+tagName)
	if err != nil {
		return githubRelease{}, err
	}

	resp, err := doGitHubRequest(ctx, client, req)
	if err != nil {
		var statusErr *githubHTTPError
		if errors.As(err, &statusErr) && statusErr.statusCode == http.StatusNotFound {
			return githubRelease{}, fmt.Errorf("release %s/%s %s not found", owner, repo, tagName)
		}
		return githubRelease{}, fmt.Errorf("release lookup: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	var release githubRelease
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

// Matches SHA256SUMS, *_checksums.txt and *.sha256sum(s).
func isChecksumAsset(name string) bool {
	return name == "sha256sums" ||
		name == "sha256sums.txt" ||
		name == "checksums.txt" ||
		strings.HasSuffix(name, "_checksums.txt") ||
		strings.HasSuffix(name, "-checksums.txt") ||
		strings.HasSuffix(name, ".sha256sum") ||
		strings.HasSuffix(name, ".sha256sums")
}

func extractChecksumForFile(r io.Reader, targetName string) (string, error) {
	scanner := bufio.NewScanner(r)
	var matches []string
	for scanner.Scan() {
		line := strings.TrimSuffix(scanner.Text(), "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		separator := strings.IndexByte(line, ' ')
		if separator < 0 || len(line) < separator+2 {
			continue
		}
		marker := line[separator : separator+2]
		if marker != "  " && marker != " *" {
			continue
		}
		filename := line[separator+2:]
		if filename != targetName {
			continue
		}
		matches = append(matches, strings.ToLower(line[:separator]))
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading checksums: %w", err)
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no checksum entry for %q in checksums file", targetName)
	}
	if len(matches) != 1 {
		return "", fmt.Errorf("duplicate checksum entries for %q in checksums file", targetName)
	}
	digest := matches[0]
	if len(digest) != sha256.Size*2 {
		return "", fmt.Errorf("invalid SHA-256 digest for %q: got %d hex characters", targetName, len(digest))
	}
	if _, err := hex.DecodeString(digest); err != nil {
		return "", fmt.Errorf("invalid SHA-256 digest for %q: %w", targetName, err)
	}
	return digest, nil
}

func verifyFileChecksum(path, expectedHex, name string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("fallback %s: open for checksum: %w", name, err)
	}
	defer f.Close() //nolint:errcheck

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("fallback %s: compute checksum: %w", name, err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	expected := strings.ToLower(strings.TrimSpace(expectedHex))
	if got != expected {
		return fmt.Errorf("fallback %s: checksum mismatch for %s: got %s, want %s",
			name, filepath.Base(path), got, expected)
	}
	return nil
}

// binaryPath is tried as an exact entry name first, otherwise the first entry whose base name matches.
func extractAndInstall(archivePath, archiveName, binaryName, binaryPath, destPath string) error {
	lower := strings.ToLower(archiveName)
	switch {
	case strings.HasSuffix(lower, ".zip"):
		return extractZip(archivePath, binaryName, binaryPath, destPath)
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz"):
		return extractTarGz(archivePath, binaryName, binaryPath, destPath)
	case strings.HasSuffix(lower, ".tar.bz2"):
		return extractTarBz2(archivePath, binaryName, binaryPath, destPath)
	case strings.HasSuffix(lower, ".tar.xz"):
		return extractTarXz(archivePath, binaryName, binaryPath, destPath)
	case strings.HasSuffix(lower, ".gz"):
		return extractGzipBinary(archivePath, destPath)
	default:
		return installRawBinary(archivePath, destPath)
	}
}

func extractZip(archivePath, binaryName, binaryPath, destPath string) error {
	r, err := zip.OpenReader(archivePath)
	if err != nil {
		return fmt.Errorf("open zip: %w", err)
	}
	defer r.Close() //nolint:errcheck

	var fallbackFile *zip.File
	for _, f := range r.File {
		if !f.Mode().IsRegular() {
			continue
		}
		if binaryPath != "" && f.Name == binaryPath {
			return installFromZipFile(f, destPath)
		}
		if filepath.Base(f.Name) == binaryName && fallbackFile == nil {
			fallbackFile = f
		}
	}
	if fallbackFile != nil {
		return installFromZipFile(fallbackFile, destPath)
	}
	return fmt.Errorf("binary %q not found in zip archive", binaryName)
}

func installFromZipFile(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("open zip entry: %w", err)
	}
	defer rc.Close() //nolint:errcheck
	return writeExecutable(io.LimitReader(rc, maxEntryBytes+1), destPath)
}

func extractTarGz(archivePath, binaryName, binaryPath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close() //nolint:errcheck

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close() //nolint:errcheck

	return extractTarStream(tar.NewReader(gz), binaryName, binaryPath, destPath)
}

func extractTarBz2(archivePath, binaryName, binaryPath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close() //nolint:errcheck

	return extractTarStream(tar.NewReader(bzip2.NewReader(f)), binaryName, binaryPath, destPath)
}

func extractTarXz(archivePath, binaryName, binaryPath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close() //nolint:errcheck

	xzr, err := xz.NewReader(f)
	if err != nil {
		return fmt.Errorf("xz reader: %w", err)
	}

	return extractTarStream(tar.NewReader(xzr), binaryName, binaryPath, destPath)
}

func extractGzipBinary(archivePath, destPath string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close() //nolint:errcheck

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close() //nolint:errcheck

	return writeExecutable(io.LimitReader(gz, maxEntryBytes+1), destPath)
}

// Exact binaryPath match wins; otherwise the first name match is buffered and installed after iteration.
func extractTarStream(tr *tar.Reader, binaryName, binaryPath, destPath string) error {
	// Buffered so iteration can continue in case an exact-path match appears later in the archive.
	tmpDir := filepath.Dir(destPath)
	var bufPath string
	defer func() {
		if bufPath != "" {
			_ = os.Remove(bufPath)
		}
	}()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("read tar: %w", err)
		}
		// Symlinks, devices and directories must never be selected as the install target.
		if hdr.Typeflag != tar.TypeReg {
			continue
		}

		base := filepath.Base(hdr.Name)
		if binaryPath != "" && hdr.Name == binaryPath {
			return writeExecutable(io.LimitReader(tr, maxEntryBytes+1), destPath)
		}
		if base == binaryName && bufPath == "" {
			tmp, err := os.CreateTemp(tmpDir, ".omni-tar-*")
			if err != nil {
				return fmt.Errorf("buffer tar entry: %w", err)
			}
			bufPath = tmp.Name()
			n, err := io.Copy(tmp, io.LimitReader(tr, maxEntryBytes+1))
			if err != nil {
				_ = tmp.Close()
				return fmt.Errorf("copy tar entry: %w", err)
			}
			if n > maxEntryBytes {
				_ = tmp.Close()
				return fmt.Errorf("tar entry exceeds %d MiB limit", maxEntryBytes>>20)
			}
			if err := tmp.Close(); err != nil {
				return fmt.Errorf("close tar buffer: %w", err)
			}
		}
	}

	if bufPath != "" {
		tmp, err := os.Open(bufPath)
		if err != nil {
			return fmt.Errorf("reopen tar buffer: %w", err)
		}
		defer tmp.Close() //nolint:errcheck
		return writeExecutable(tmp, destPath)
	}
	return fmt.Errorf("binary %q not found in tar archive", binaryName)
}

func installRawBinary(srcPath, destPath string) error {
	src, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("open raw binary: %w", err)
	}
	defer src.Close() //nolint:errcheck
	return writeExecutable(src, destPath)
}

// Atomic: temp file then rename, mode 0755.
func writeExecutable(r io.Reader, destPath string) error {
	tmp, err := os.CreateTemp(filepath.Dir(destPath), ".omni-install-*")
	if err != nil {
		return fmt.Errorf("create install temp: %w", err)
	}
	tmpName := tmp.Name()
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()

	n, err := io.Copy(tmp, r)
	if err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write binary: %w", err)
	}
	// Callers pass a LimitReader(r, maxEntryBytes+1), so reading more means the stream exceeded the cap.
	if n > maxEntryBytes {
		_ = tmp.Close()
		return fmt.Errorf("entry exceeds %d MiB limit", maxEntryBytes>>20)
	}
	if n == 0 {
		_ = tmp.Close()
		return fmt.Errorf("binary is empty")
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close install temp: %w", err)
	}
	if err := os.Chmod(tmpName, 0o755); err != nil {
		return fmt.Errorf("chmod binary: %w", err)
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return fmt.Errorf("install binary: %w", err)
	}
	success = true
	return nil
}

// The default client refuses an https→http hop, strips Authorization on redirects, and caps redirect depth.
func (a *App) githubHTTPClient() *http.Client {
	if a.httpClient != nil {
		// An injected client carries a transport, not a fetch policy. Copying it and forcing the guard keeps the redirect rule out of reach of anything that can hand this App a client.
		client := *a.httpClient
		client.CheckRedirect = guardGitHubRedirect
		return &client
	}
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: guardGitHubRedirect,
	}
}

// The https-only check on a download URL governs the first hop only: without this, an https origin
// answering 302 with an http location gets followed, written, chmod 0755'd and renamed into bin_dir.
// Also prevents GITHUB_TOKEN reaching a non-GitHub host, and caps redirects at 10 to match Go's default.
func guardGitHubRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects")
	}
	if len(via) > 0 && strings.EqualFold(via[len(via)-1].URL.Scheme, "https") && !strings.EqualFold(req.URL.Scheme, "https") {
		return fmt.Errorf("%w: %s", errRedirectSchemeDowngrade, req.URL.Redacted())
	}
	req.Header.Del("Authorization")
	return nil
}

func (a *App) githubAPIBase() string {
	if base := strings.TrimRight(a.githubAPI, "/"); base != "" {
		return base
	}
	if base := strings.TrimRight(os.Getenv("OMNI_GITHUB_API_BASE"), "/"); base != "" {
		return base
	}
	return defaultGitHubAPIBase
}

// Single gate for every GitHub API call; the rejection names the full URL so a bad base stays diagnosable.
func (a *App) newGitHubAPIRequest(ctx context.Context, pathSuffix string) (*http.Request, error) {
	base := a.githubAPIBase()
	apiURL := base + pathSuffix
	if !config.IsHTTPSURL(base) {
		return nil, fmt.Errorf("%w: %q", errGitHubAPIBaseScheme, apiURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "omni")
	attachGitHubToken(req, base)
	return req, nil
}

// Prevents credential leakage to non-GitHub hosts.
func attachGitHubToken(req *http.Request, rawURL string) {
	req.Header.Del("Authorization")
	token := strings.TrimSpace(os.Getenv("GH_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if token == "" {
		return
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !isGitHubHost(parsed.Host) {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
}

type githubHTTPError struct {
	statusCode int
	status     string
	body       string
	headers    http.Header
}

func (e *githubHTTPError) Error() string {
	parts := make([]string, 0, 3)
	for _, name := range []string{"X-RateLimit-Limit", "X-RateLimit-Remaining", "X-RateLimit-Reset"} {
		if value := strings.TrimSpace(e.headers.Get(name)); value != "" {
			parts = append(parts, name+"="+value)
		}
	}
	message := "HTTP " + e.status
	if e.body != "" {
		message += ": " + e.body
	}
	if len(parts) > 0 {
		message += " (" + strings.Join(parts, ", ") + ")"
	}
	return message
}

func doGitHubRequest(ctx context.Context, client *http.Client, req *http.Request) (*http.Response, error) {
	var lastErr error
	for attempt := range downloadRetries {
		resp, err := client.Do(req.Clone(ctx))
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return nil, ctxErr
			}
			lastErr = err
		} else if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return resp, nil
		} else {
			body := githubResponseText(resp.Body)
			_ = resp.Body.Close()
			status := resp.Status
			if status == "" {
				status = fmt.Sprintf("%d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
			}
			statusErr := &githubHTTPError{
				statusCode: resp.StatusCode,
				status:     strings.TrimSpace(status),
				body:       body,
				headers:    resp.Header.Clone(),
			}
			lastErr = statusErr
			if !githubResponseRetryable(resp.StatusCode, resp.Header, body) {
				if resp.StatusCode >= 400 && resp.StatusCode < 500 {
					return nil, errNoRetry{statusErr}
				}
				return nil, statusErr
			}
		}

		if attempt == downloadRetries-1 {
			break
		}
		delay := time.Duration(attempt+1) * 500 * time.Millisecond
		if resp != nil {
			delay = githubRetryDelay(resp.Header, time.Now(), attempt)
		}
		if err := waitForGitHubRetry(ctx, delay); err != nil {
			return nil, err
		}
	}
	return nil, errNoRetry{lastErr}
}

func githubResponseRetryable(status int, header http.Header, body string) bool {
	if status == http.StatusTooManyRequests {
		return true
	}
	if status != http.StatusForbidden {
		return false
	}
	body = strings.ToLower(body)
	return strings.TrimSpace(header.Get("Retry-After")) != "" ||
		strings.TrimSpace(header.Get("X-RateLimit-Remaining")) == "0" ||
		strings.Contains(body, "rate limit") || strings.Contains(body, "rate-limit")
}

func githubRetryDelay(header http.Header, now time.Time, attempt int) time.Duration {
	if raw := strings.TrimSpace(header.Get("Retry-After")); raw != "" {
		if seconds, err := strconv.ParseInt(raw, 10, 64); err == nil && seconds >= 0 {
			if seconds >= int64(maxGitHubRetryDelay/time.Second) {
				return maxGitHubRetryDelay
			}
			return time.Duration(seconds) * time.Second
		}
		if retryAt, err := http.ParseTime(raw); err == nil {
			return min(max(retryAt.Sub(now), 0), maxGitHubRetryDelay)
		}
	}
	if reset, err := strconv.ParseInt(strings.TrimSpace(header.Get("X-RateLimit-Reset")), 10, 64); err == nil {
		return min(max(time.Unix(reset, 0).Sub(now), 0), maxGitHubRetryDelay)
	}
	return time.Duration(attempt+1) * 500 * time.Millisecond
}

func waitForGitHubRetry(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func githubResponseText(r io.Reader) string {
	body, _ := io.ReadAll(io.LimitReader(r, maxGitHubErrorTextBytes+1))
	truncated := len(body) > maxGitHubErrorTextBytes
	if truncated {
		body = body[:maxGitHubErrorTextBytes]
	}
	text := strings.ToValidUTF8(string(body), "")
	text = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	tokens := []string{strings.TrimSpace(os.Getenv("GH_TOKEN")), strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))}
	if len(tokens[1]) > len(tokens[0]) {
		tokens[0], tokens[1] = tokens[1], tokens[0]
	}
	for _, token := range tokens {
		if token != "" {
			text = strings.ReplaceAll(text, token, "[redacted]")
		}
	}
	if truncated {
		text += "…"
	}
	return text
}
