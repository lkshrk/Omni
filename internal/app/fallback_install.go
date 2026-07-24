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
	"strings"
	"time"

	"github.com/ulikunitz/xz"

	"github.com/lkshrk/omni/internal/config"
)

const (
	downloadTimeout = 5 * time.Minute
	downloadRetries = 3
	// maxDownloadBytes caps the asset download to prevent disk-fill from a
	// malicious or corrupted release (512 MiB).
	maxDownloadBytes = 512 << 20
	// maxEntryBytes caps each extracted tar/zip entry individually.
	maxEntryBytes = 512 << 20
)

// nativeGitHubInstallPipeline downloads, checksums, extracts, and installs a
// GitHub release asset using Go's standard library only — no curl, tar, or
// unzip runtime dependencies are required.
//
// Invoked for FallbackRecipeGitHubReleaseAsset recipes with a resolved
// AssetDownloadURL. Returns an error that the caller wraps into status gh!.
func (a *App) nativeGitHubInstallPipeline(ctx context.Context, name string, fallback *config.FallbackSpec) error {
	recipe := fallback.Recipe
	downloadURL := strings.TrimSpace(recipe.AssetDownloadURL)
	if downloadURL == "" {
		return fmt.Errorf("fallback %s: native install requires a resolved asset_download_url", name)
	}
	rawAssetName := strings.TrimSpace(recipe.AssetName)
	if rawAssetName == "" {
		rawAssetName = filepath.Base(downloadURL)
	}
	// Sanitise both untrusted path components before joining into fs paths.
	// filepath.Base strips any directory traversal (../../) sequences; we then
	// reject the degenerate values ".", "..", and "" that Base can still return.
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

// errNoRetry wraps a download error that must not be retried (e.g. 4xx).
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

// downloadFallbackAsset fetches downloadURL to destPath, retrying up to
// downloadRetries times on transient errors (5xx, network). 4xx errors are
// not retried because the resource is definitively absent or forbidden.
func (a *App) downloadFallbackAsset(ctx context.Context, name, downloadURL, destPath string) error {
	client := *a.githubHTTPClient()
	// API calls use a short client timeout. Asset downloads own their longer
	// deadline through downloadToFile's request context instead.
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
		// 4xx responses are definitive — no point retrying.
		var noRetry errNoRetry
		if errors.As(lastErr, &noRetry) {
			break
		}
	}
	return fmt.Errorf("fallback %s: download %s: %w", name, downloadURL, lastErr)
}

// downloadToFile GETs url and writes the response body to destPath atomically
// (write to a temp file then rename).
func downloadToFile(ctx context.Context, client *http.Client, rawURL, destPath string) error {
	dlCtx, cancel := context.WithTimeout(ctx, downloadTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(dlCtx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "omni")
	attachGitHubToken(req, rawURL)

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		err := fmt.Errorf("HTTP %s", resp.Status)
		// 4xx errors are definitive; wrapping in errNoRetry prevents the caller
		// from retrying a request that will never succeed.
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return errNoRetry{err}
		}
		return err
	}
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

// verifyFallbackChecksum fetches the release checksums asset, locates the line
// for assetName, and verifies the SHA-256 of assetPath.
//
// When no checksums asset can be found the function returns nil (best-effort).
// A checksum mismatch is always a hard failure.
// On success the verified hex digest is persisted into fallback.Recipe.Checksum.
func (a *App) verifyFallbackChecksum(ctx context.Context, name string, fallback *config.FallbackSpec, assetPath, assetName string) error {
	// Reuse a stored checksum to skip the fetch only when it was verified
	// against the very asset now being installed. On a version bump or a rotated
	// asset the recorded scope no longer matches, so fall through and re-fetch
	// the authoritative digest for the current release instead of trusting a
	// stale one.
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
		return nil
	}

	digest, err := a.fetchReleaseChecksum(ctx, owner, repo, tagName, assetName)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
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

// fetchReleaseChecksum retrieves the checksums asset for the given release and
// returns the SHA-256 hex digest for assetName.
func (a *App) fetchReleaseChecksum(ctx context.Context, owner, repo, tagName, assetName string) (string, error) {
	release, err := a.fetchGitHubReleaseByTag(ctx, owner, repo, tagName)
	if err != nil {
		return "", err
	}

	client := a.githubHTTPClient()
	var assetErrs []error
	recognized := 0
	for _, asset := range release.Assets {
		if !isChecksumAsset(strings.ToLower(asset.Name)) {
			continue
		}
		recognized++
		if err := ctx.Err(); err != nil {
			return "", err
		}

		checksumURL := asset.BrowserDownloadURL
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
		if err != nil {
			assetErrs = append(assetErrs, fmt.Errorf("checksum asset %q: %w", asset.Name, err))
			continue
		}
		req.Header.Set("User-Agent", "omni")
		attachGitHubToken(req, checksumURL)

		resp, err := client.Do(req)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return "", err
			}
			assetErrs = append(assetErrs, fmt.Errorf("checksum asset %q: %w", asset.Name, err))
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			_ = resp.Body.Close()
			assetErrs = append(assetErrs, fmt.Errorf("checksum asset %q: HTTP %s", asset.Name, resp.Status))
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
	if recognized == 0 {
		return "", fmt.Errorf("no checksums asset in release %s/%s %s", owner, repo, tagName)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no usable checksum entry for %q: %w", assetName, errors.Join(assetErrs...))
}

// fetchGitHubReleaseByTag fetches a specific release by tag from the GitHub API.
func (a *App) fetchGitHubReleaseByTag(ctx context.Context, owner, repo, tagName string) (githubRelease, error) {
	client := a.githubHTTPClient()
	baseURL := a.githubAPIBase()
	apiURL := baseURL + "/repos/" + owner + "/" + repo + "/releases/tags/" + tagName

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "omni")
	attachGitHubToken(req, baseURL)

	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode == http.StatusNotFound {
		return githubRelease{}, fmt.Errorf("release %s/%s %s not found", owner, repo, tagName)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return githubRelease{}, fmt.Errorf("release lookup HTTP %s", resp.Status)
	}

	var release githubRelease
	decoder := json.NewDecoder(resp.Body)
	decoder.UseNumber()
	if err := decoder.Decode(&release); err != nil {
		return githubRelease{}, err
	}
	return release, nil
}

// isChecksumAsset reports whether a (lowercased) filename is a recognised
// SHA-256 checksums asset. Matches the common patterns used by Go, Rust, and
// other release tooling: SHA256SUMS, *_checksums.txt, *.sha256sum(s).
func isChecksumAsset(name string) bool {
	return name == "sha256sums" ||
		name == "sha256sums.txt" ||
		name == "checksums.txt" ||
		strings.HasSuffix(name, "_checksums.txt") ||
		strings.HasSuffix(name, "-checksums.txt") ||
		strings.HasSuffix(name, ".sha256sum") ||
		strings.HasSuffix(name, ".sha256sums")
}

// extractChecksumForFile parses a "hexdigest  filename" checksums file and
// returns the SHA-256 hex digest matching targetName.
func extractChecksumForFile(r io.Reader, targetName string) (string, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// Format: "<hex>  <filename>" or "<hex> <filename>"
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		// GNU sha256sum uses a leading '*' to mark binary-mode input.
		filename := filepath.Base(strings.TrimPrefix(parts[len(parts)-1], "*"))
		if filename != targetName {
			continue
		}
		digest := strings.ToLower(parts[0])
		if len(digest) != sha256.Size*2 {
			return "", fmt.Errorf("invalid SHA-256 digest for %q: got %d hex characters", targetName, len(digest))
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return "", fmt.Errorf("invalid SHA-256 digest for %q: %w", targetName, err)
		}
		return digest, nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("reading checksums: %w", err)
	}
	return "", fmt.Errorf("no checksum entry for %q in checksums file", targetName)
}

// verifyFileChecksum computes the SHA-256 of path and compares it to the
// expected hex string. Returns a hard error on mismatch.
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

// extractAndInstall extracts binaryName from archivePath and writes it
// atomically to destPath with mode 0755.
//
// archiveName drives format detection: .zip, tar archives, .gz, or raw.
// Within an archive, binaryPath is tried as an exact entry name first;
// otherwise the first entry whose base name equals binaryName is used.
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

// extractTarStream iterates over tr and installs the first matching entry.
// Exact binaryPath match wins; otherwise the first entry whose base name
// equals binaryName is buffered and installed after the full iteration.
func extractTarStream(tr *tar.Reader, binaryName, binaryPath, destPath string) error {
	// Buffer the first name-matched entry to a temp file so we can continue
	// iterating (in case an exact-path match appears later in the archive).
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
		// Only regular files are eligible; symlinks, devices, and directories
		// must not be selected as the install target.
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

// writeExecutable writes r to destPath atomically (temp file + rename) and
// sets permissions to 0755.
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
	// Callers pass a LimitReader(r, maxEntryBytes+1); if we read more than
	// the cap the stream was larger than allowed.
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

// githubHTTPClient returns the test-injected client or a fresh default one.
// The default client strips the Authorization header on every redirect and caps
// redirect depth at 10 to prevent token leakage to non-GitHub hosts.
func (a *App) githubHTTPClient() *http.Client {
	if a.githubClient != nil {
		return a.githubClient
	}
	return &http.Client{
		Timeout:       30 * time.Second,
		CheckRedirect: stripAuthOnRedirect,
	}
}

// stripAuthOnRedirect removes the Authorization header before following any
// redirect so that GITHUB_TOKEN is never forwarded to a non-GitHub host.
// It also enforces a hard cap of 10 redirects to match Go's default behaviour.
func stripAuthOnRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects")
	}
	req.Header.Del("Authorization")
	return nil
}

// githubAPIBase returns the configured or default GitHub API base URL.
func (a *App) githubAPIBase() string {
	if base := strings.TrimRight(a.githubAPI, "/"); base != "" {
		return base
	}
	if base := strings.TrimRight(os.Getenv("OMNI_GITHUB_API_BASE"), "/"); base != "" {
		return base
	}
	return defaultGitHubAPIBase
}

// attachGitHubToken adds an Authorization header when rawURL targets a GitHub
// host and GITHUB_TOKEN is set. Prevents credential leakage to non-GitHub hosts.
func attachGitHubToken(req *http.Request, rawURL string) {
	token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	if token == "" {
		return
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || !isGitHubHost(parsed.Host) {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
}
