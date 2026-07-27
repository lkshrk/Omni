package agent

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestSkillsInstallWellKnownSkillMDWithDigest(t *testing.T) {
	skill := []byte("---\nname: remote\ndescription: remote skill\n---\n\nhello\n")
	digest := sha256.Sum256(skill)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/agent-skills/index.json":
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"$schema":"%s","skills":[{"name":"remote","type":"skill-md","description":"remote skill","url":"/remote/SKILL.md","digest":"sha256:%s"}]}`,
				wellKnownSchemaV02, hex.EncodeToString(digest[:]))
		case "/remote/SKILL.md":
			_, _ = w.Write(skill)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	svc, home := newHTTPTestSkills(t, server.Client())
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: server.URL}, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(home, ".test", "skills", "remote", "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestExtractTarGzAcceptsLegacyRegularFileType(t *testing.T) {
	t.Parallel()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	content := []byte("---\nname: archived\ndescription: archived\n---\n")
	if err := tw.WriteHeader(&tar.Header{Name: "SKILL.md", Mode: 0o644, Size: int64(len(content)), Typeflag: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(content); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := extractTarGz(archive.Bytes(), dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "SKILL.md")); err != nil {
		t.Fatal(err)
	}
}

func TestSkillsRejectsDirectStandaloneSkillMDURL(t *testing.T) {
	svc, _ := newHTTPTestSkills(t, http.DefaultClient)
	_, err := svc.Install(
		context.Background(),
		config.SkillPackage{Source: "https://example.com/SKILL.md"},
		[]string{"test"},
	)
	if err == nil || !strings.Contains(err.Error(), "well-known index") {
		t.Fatalf("error = %v, want direct URL rejection", err)
	}
}

func TestSkillsRejectsGitRefForWellKnownCatalog(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/.well-known/agent-skills/index.json" {
			_, _ = fmt.Fprintf(w, `{"$schema":"%s","skills":[]}`, wellKnownSchemaV02)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	svc, _ := newHTTPTestSkills(t, server.Client())
	_, err := svc.Install(
		context.Background(),
		config.SkillPackage{Source: server.URL, Ref: "v1"},
		[]string{"test"},
	)
	if err == nil || !strings.Contains(err.Error(), "do not support Git refs") {
		t.Fatalf("error = %v", err)
	}
}

func TestSkillsInstallWellKnownDigestFailurePreservesInstalledPackage(t *testing.T) {
	oldSkill := []byte("---\nname: remote\ndescription: remote\n---\nold\n")
	oldDigest := sha256.Sum256(oldSkill)
	invalidDigest := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "index.json") {
			digest := hex.EncodeToString(oldDigest[:])
			if invalidDigest {
				digest = strings.Repeat("0", 64)
			}
			fmt.Fprintf(w, `{"$schema":"%s","skills":[{"name":"remote","type":"skill-md","description":"remote skill","url":"/SKILL.md","digest":"sha256:%s"}]}`, wellKnownSchemaV02, digest)
			return
		}
		if invalidDigest {
			_, _ = w.Write([]byte("---\nname: remote\ndescription: remote\n---\nnew\n"))
			return
		}
		_, _ = w.Write(oldSkill)
	}))
	defer server.Close()
	svc, home := newHTTPTestSkills(t, server.Client())
	pkg := config.SkillPackage{Source: server.URL}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	invalidDigest = true
	if _, err := svc.Refresh(context.Background(), pkg, []string{"test"}); err == nil {
		t.Fatal("digest mismatch install succeeded")
	}
	data, err := os.ReadFile(filepath.Join(home, ".test", "skills", "remote", "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "old") {
		t.Fatalf("failed update replaced installed content: %s", data)
	}
}

func TestSkillsRejectsWellKnownIndexWithoutSupportedSchema(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "index.json") {
			fmt.Fprint(w, `{"skills":[{"name":"remote","description":"remote skill","files":["SKILL.md"]}]}`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	svc, _ := newHTTPTestSkills(t, server.Client())
	_, err := svc.Install(context.Background(), config.SkillPackage{Source: server.URL}, []string{"test"})
	if err == nil || !strings.Contains(err.Error(), "unsupported skills index") {
		t.Fatalf("error = %v, want unsupported skills index", err)
	}
}

// An auth-walled host says nothing about whether the source is a catalog, so the Git clone must still be tried and the probe cause still reported.
func TestSkillsFallsBackToGitWhenIndexProbeIsUnauthorized(t *testing.T) {
	var indexRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "index.json") {
			indexRequests++
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	svc, _ := newHTTPTestSkills(t, server.Client())
	_, err := svc.Install(context.Background(), config.SkillPackage{Source: server.URL}, []string{"test"})
	if err == nil {
		t.Fatal("install against an unauthorized index succeeded")
	}
	if indexRequests == 0 {
		t.Fatal("well-known index was never probed")
	}
	if !strings.Contains(err.Error(), "git clone") {
		t.Fatalf("error = %v, want the git fallback to have been attempted", err)
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("error = %v, want the index probe cause named", err)
	}
}

func TestSkillsInstallWellKnownRejectsArchiveTraversal(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	file, err := zw.Create("../escape")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("bad"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(archive.Bytes())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "index.json") {
			fmt.Fprintf(w, `{"$schema":"%s","skills":[{"name":"remote","type":"archive","description":"remote skill","url":"/remote.zip","digest":"sha256:%s"}]}`,
				wellKnownSchemaV02, hex.EncodeToString(digest[:]))
			return
		}
		w.Header().Set("Content-Type", "application/zip")
		_, _ = w.Write(archive.Bytes())
	}))
	defer server.Close()
	svc, _ := newHTTPTestSkills(t, server.Client())
	if _, err := svc.Install(context.Background(), config.SkillPackage{Source: server.URL}, []string{"test"}); err == nil {
		t.Fatal("traversal archive install succeeded")
	}
}

func TestExtractSkillArchiveRejectsExcessiveFiles(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for i := 0; i <= maxArchiveFiles; i++ {
		file, err := zw.Create(fmt.Sprintf("file-%04d", i))
		if err != nil {
			t.Fatal(err)
		}
		_, _ = file.Write(nil)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractSkillArchive(archive.Bytes(), "application/zip", "skill.zip", t.TempDir()); err == nil {
		t.Fatal("archive with excessive files succeeded")
	}
}

func TestExtractSkillArchiveRejectsSymlink(t *testing.T) {
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	header := &zip.FileHeader{Name: "link"}
	header.SetMode(os.ModeSymlink | 0o777)
	file, err := zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("../escape"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractSkillArchive(archive.Bytes(), "application/zip", "skill.zip", t.TempDir()); err == nil {
		t.Fatal("archive symlink succeeded")
	}
}

func TestFetchArtifactRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.CopyN(w, zeroReader{}, maxArtifactBytes+1)
	}))
	defer server.Close()
	svc, _ := newHTTPTestSkills(t, server.Client())
	if _, _, err := svc.fetchArtifact(context.Background(), server.URL); err == nil {
		t.Fatal("oversized artifact succeeded")
	}
}

type zeroReader struct{}

func (zeroReader) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

func newHTTPTestSkills(t *testing.T, client *http.Client) (*Skills, string) {
	t.Helper()
	home := t.TempDir()
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	return NewSkills(registry, home, filepath.Join(home, "data"), client, nil, ""), home
}
