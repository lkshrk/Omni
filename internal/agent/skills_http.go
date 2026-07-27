package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
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

	"github.com/lkshrk/omni/internal/config"
)

const (
	wellKnownSchemaV02 = "https://schemas.agentskills.io/discovery/0.2.0/schema.json"
	maxArtifactBytes   = 50 << 20
	maxArchiveFiles    = 1000
)

var errNoWellKnown = errors.New("well-known skills index not found")

func defaultSkillsClient() *http.Client {
	return &http.Client{Timeout: 60 * time.Second, CheckRedirect: refuseDowngradeRedirect}
}

// Artifact URLs and their digests both come from the final index URL, so a downgrade there hands the whole verified chain to the plaintext hop.
func refuseDowngradeRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("too many redirects")
	}
	if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && req.URL.Scheme != "https" {
		return fmt.Errorf("refusing redirect from HTTPS to %s", req.URL.Redacted())
	}
	return nil
}

func checkIndexScheme(requested, actual string) error {
	from, err := url.Parse(requested)
	if err != nil {
		return err
	}
	to, err := url.Parse(actual)
	if err != nil {
		return err
	}
	if to.Scheme != "http" && to.Scheme != "https" {
		return fmt.Errorf("unsupported skills index protocol %q", to.Scheme)
	}
	if from.Scheme == "https" && to.Scheme != "https" {
		return fmt.Errorf("skills index %s was redirected to plaintext %s", requested, actual)
	}
	return nil
}

type wellKnownSkill struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Digest      string `json:"digest"`
}

type wellKnownIndex struct {
	Schema string           `json:"$schema"`
	Skills []wellKnownSkill `json:"skills"`
}

// The returned flag reports an index that was fetched and parsed; every failure to obtain one is unflagged so the caller can still try a Git clone, because an auth-walled or flaky host says nothing about whether the source is a catalog.
func (s *Skills) acquireWellKnown(ctx context.Context, pkg config.SkillPackage, dst string) (bool, error) {
	indexURL, err := discoveryIndexURL(pkg.Source, false)
	if err != nil {
		return false, err
	}
	index, actualURL, err := s.fetchIndex(ctx, indexURL)
	if errors.Is(err, errNoWellKnown) {
		legacyURL, legacyErr := discoveryIndexURL(pkg.Source, true)
		if legacyErr != nil {
			return false, legacyErr
		}
		indexURL = legacyURL
		index, actualURL, err = s.fetchIndex(ctx, indexURL)
	}
	if err != nil {
		return false, err
	}
	if index.Schema != wellKnownSchemaV02 {
		return true, fmt.Errorf("unsupported skills index at %s: schema %q", indexURL, index.Schema)
	}
	if pkg.Ref != "" {
		return true, fmt.Errorf("http skill catalogs do not support Git refs")
	}
	entries, err := selectWellKnownSkills(index, pkg.Skills)
	if err != nil {
		return true, err
	}
	for _, entry := range entries {
		if err := validateWellKnownSkill(entry); err != nil {
			return true, err
		}
		artifactURL, err := url.Parse(entry.URL)
		if err != nil {
			return true, err
		}
		base, err := url.Parse(actualURL)
		if err != nil {
			return true, fmt.Errorf("parsing skills index URL %s: %w", actualURL, err)
		}
		artifactURL = base.ResolveReference(artifactURL)
		if artifactURL.Scheme != "http" && artifactURL.Scheme != "https" {
			return true, fmt.Errorf("unsupported skill artifact protocol %q", artifactURL.Scheme)
		}
		data, contentType, err := s.fetchArtifact(ctx, artifactURL.String())
		if err != nil {
			return true, err
		}
		if err := verifyDigest(data, entry.Digest); err != nil {
			return true, fmt.Errorf("skill %s: %w", entry.Name, err)
		}
		skillDir := filepath.Join(dst, "skills", entry.Name)
		switch entry.Type {
		case "skill-md":
			if err := os.MkdirAll(skillDir, 0o755); err != nil {
				return true, err
			}
			if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), data, 0o644); err != nil {
				return true, err
			}
		case "archive":
			if err := extractSkillArchive(data, contentType, artifactURL.Path, skillDir); err != nil {
				return true, fmt.Errorf("skill %s: %w", entry.Name, err)
			}
		default:
			return true, fmt.Errorf("unsupported skill artifact type %q", entry.Type)
		}
		name, err := validateSkillDir(skillDir)
		if err != nil {
			return true, err
		}
		if name != entry.Name {
			return true, fmt.Errorf("skill artifact name %q does not match index name %q", name, entry.Name)
		}
	}
	return true, nil
}

func discoveryIndexURL(source string, legacy bool) (string, error) {
	u, err := url.Parse(source)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("invalid HTTP skill source %q", source)
	}
	if strings.HasSuffix(u.Path, "/index.json") {
		return u.String(), nil
	}
	if legacy {
		u.Path = "/.well-known/skills/index.json"
	} else {
		u.Path = "/.well-known/agent-skills/index.json"
	}
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), nil
}

func (s *Skills) fetchIndex(ctx context.Context, indexURL string) (wellKnownIndex, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, indexURL, nil)
	if err != nil {
		return wellKnownIndex{}, "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return wellKnownIndex{}, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	actual := indexURL
	if resp.Request != nil && resp.Request.URL != nil {
		actual = resp.Request.URL.String()
	}
	if err := checkIndexScheme(indexURL, actual); err != nil {
		return wellKnownIndex{}, "", err
	}
	if resp.StatusCode == http.StatusNotFound {
		return wellKnownIndex{}, "", errNoWellKnown
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return wellKnownIndex{}, "", fmt.Errorf("fetching %s: HTTP %s", indexURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil {
		return wellKnownIndex{}, "", err
	}
	if len(data) > 1<<20 {
		return wellKnownIndex{}, "", fmt.Errorf("skills index exceeds 1 MiB")
	}
	var index wellKnownIndex
	if err := json.Unmarshal(data, &index); err != nil {
		return wellKnownIndex{}, "", fmt.Errorf("invalid skills index: %w", err)
	}
	if index.Skills == nil {
		return wellKnownIndex{}, "", fmt.Errorf("skills index has no skills array")
	}
	return index, actual, nil
}

func selectWellKnownSkills(index wellKnownIndex, requested []string) ([]wellKnownSkill, error) {
	seen := make(map[string]struct{}, len(index.Skills))
	for _, skill := range index.Skills {
		if _, exists := seen[skill.Name]; exists {
			return nil, fmt.Errorf("duplicate skill %q in skills index", skill.Name)
		}
		seen[skill.Name] = struct{}{}
	}
	if len(requested) == 0 {
		return index.Skills, nil
	}
	byName := make(map[string]wellKnownSkill, len(index.Skills))
	for _, skill := range index.Skills {
		byName[skill.Name] = skill
	}
	out := make([]wellKnownSkill, 0, len(requested))
	for _, name := range requested {
		skill, ok := byName[name]
		if !ok {
			return nil, fmt.Errorf("requested skill %q was not found", name)
		}
		out = append(out, skill)
	}
	return out, nil
}

func validateWellKnownSkill(skill wellKnownSkill) error {
	if !validSkillName(skill.Name) {
		return fmt.Errorf("invalid skill name %q", skill.Name)
	}
	if strings.TrimSpace(skill.Description) == "" || len(skill.Description) > 1024 {
		return fmt.Errorf("invalid description for skill %q", skill.Name)
	}
	if skill.Type != "skill-md" && skill.Type != "archive" {
		return fmt.Errorf("invalid artifact type for skill %q", skill.Name)
	}
	if strings.TrimSpace(skill.URL) == "" {
		return fmt.Errorf("missing artifact URL for skill %q", skill.Name)
	}
	if err := validateDigest(skill.Digest); err != nil {
		return fmt.Errorf("skill %s: %w", skill.Name, err)
	}
	return nil
}

func (s *Skills) fetchArtifact(ctx context.Context, artifactURL string) ([]byte, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifactURL, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, "", fmt.Errorf("fetching %s: HTTP %s", artifactURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxArtifactBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxArtifactBytes {
		return nil, "", fmt.Errorf("skill artifact exceeds 50 MiB")
	}
	return data, resp.Header.Get("Content-Type"), nil
}

func validateDigest(digest string) error {
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		return fmt.Errorf("invalid SHA-256 digest")
	}
	raw := strings.TrimPrefix(digest, "sha256:")
	if raw != strings.ToLower(raw) {
		return fmt.Errorf("invalid SHA-256 digest")
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return fmt.Errorf("invalid SHA-256 digest")
	}
	return nil
}

func verifyDigest(data []byte, digest string) error {
	if err := validateDigest(digest); err != nil {
		return err
	}
	sum := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(sum[:]) != digest {
		return fmt.Errorf("SHA-256 digest mismatch")
	}
	return nil
}

func extractSkillArchive(data []byte, contentType, path, dst string) error {
	switch {
	case strings.Contains(contentType, "zip") || strings.HasSuffix(strings.ToLower(path), ".zip"):
		return extractZip(data, dst)
	case strings.Contains(contentType, "gzip") || strings.HasSuffix(strings.ToLower(path), ".tar.gz") || strings.HasSuffix(strings.ToLower(path), ".tgz"):
		return extractTarGz(data, dst)
	default:
		return fmt.Errorf("unsupported archive format")
	}
}

func extractZip(data []byte, dst string) error {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if len(reader.File) > maxArchiveFiles {
		return fmt.Errorf("archive extraction limit exceeded")
	}
	var files int
	var total uint64
	for _, file := range reader.File {
		target, err := safeArchiveTarget(dst, file.Name)
		if err != nil {
			return err
		}
		if file.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("archive links are not allowed")
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		files++
		total += file.UncompressedSize64
		if files > maxArchiveFiles || total > maxArtifactBytes {
			return fmt.Errorf("archive extraction limit exceeded")
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		in, err := file.Open()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = in.Close()
			return err
		}
		remaining := int64(maxArtifactBytes) - int64(total-file.UncompressedSize64)
		written, copyErr := io.CopyN(out, in, remaining+1)
		inCloseErr := in.Close()
		closeErr := out.Close()
		if written > remaining {
			return fmt.Errorf("archive extraction limit exceeded")
		}
		if errors.Is(copyErr, io.EOF) {
			copyErr = nil
		}
		if copyErr != nil {
			return copyErr
		}
		if inCloseErr != nil {
			return inCloseErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
	if !fileExists(filepath.Join(dst, "SKILL.md")) {
		return fmt.Errorf("archive has no root SKILL.md")
	}
	return nil
}

func extractTarGz(data []byte, dst string) error {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return err
	}
	defer func() { _ = gz.Close() }()
	reader := tar.NewReader(gz)
	var files int
	var entries int
	var total int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		entries++
		if entries > maxArchiveFiles {
			return fmt.Errorf("archive extraction limit exceeded")
		}
		target, err := safeArchiveTarget(dst, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, 0:
			files++
			total += header.Size
			if files > maxArchiveFiles || total > maxArtifactBytes {
				return fmt.Errorf("archive extraction limit exceeded")
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(out, reader)
			closeErr := out.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("archive links and special files are not allowed")
		}
	}
	if !fileExists(filepath.Join(dst, "SKILL.md")) {
		return fmt.Errorf("archive has no root SKILL.md")
	}
	return nil
}

func safeArchiveTarget(root, name string) (string, error) {
	if strings.Contains(name, `\`) {
		return "", fmt.Errorf("archive path %q uses an unsafe separator", name)
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("archive contains absolute path %q", name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("archive path %q escapes extraction root", name)
	}
	target := filepath.Join(root, clean)
	if !pathWithin(root, target) {
		return "", fmt.Errorf("archive path %q escapes extraction root", name)
	}
	return target, nil
}
