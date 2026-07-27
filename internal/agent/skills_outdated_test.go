package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/config"
)

func newOutdatedTestSkills(t *testing.T, client *http.Client) (*Skills, *memorySkillState) {
	t.Helper()
	home := t.TempDir()
	registry, err := newRegistry([]Target{{ID: "test", configDir: ".test"}})
	if err != nil {
		t.Fatal(err)
	}
	state := memorySkillState{}
	return NewSkills(registry, home, filepath.Join(home, "data"), client, &state, ""), &state
}

func checkOutdated(t *testing.T, svc *Skills, pkg config.SkillPackage, force bool) *bool {
	t.Helper()
	return svc.CheckOutdated(context.Background(), pkg, force)
}

func wantOutdated(t *testing.T, got *bool, want bool, what string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s: verdict is unknown, want %v", what, want)
	}
	if *got != want {
		t.Fatalf("%s: verdict = %v, want %v", what, *got, want)
	}
}

func TestCheckOutdatedLocalDirTracksSourceContent(t *testing.T) {
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "one")
	svc, _ := newOutdatedTestSkills(t, nil)
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}

	wantOutdated(t, checkOutdated(t, svc, pkg, true), false, "unchanged local source")

	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "two")
	wantOutdated(t, checkOutdated(t, svc, pkg, true), true, "mutated local source")

	if _, err := svc.Refresh(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	wantOutdated(t, svc.OutdatedState(context.Background(), source), false, "after refresh")
}

func TestCheckOutdatedGitBranchAndPinnedTag(t *testing.T) {
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "one")
	runGit(t, source, "init", "-q", "-b", "main")
	runGit(t, source, "config", "user.email", "test@example.com")
	runGit(t, source, "config", "user.name", "test")
	runGit(t, source, "add", ".")
	runGit(t, source, "commit", "-qm", "one")
	runGit(t, source, "tag", "v1")

	svc, _ := newOutdatedTestSkills(t, nil)
	branch := config.SkillPackage{Source: source, Ref: "main"}
	pinned := config.SkillPackage{Source: source, Ref: "v1"}
	if _, err := svc.Install(context.Background(), branch, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	wantOutdated(t, checkOutdated(t, svc, branch, true), false, "branch at install commit")
	wantOutdated(t, checkOutdated(t, svc, pinned, true), false, "tag at install commit")

	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "two")
	runGit(t, source, "commit", "-qam", "two")

	wantOutdated(t, checkOutdated(t, svc, branch, true), true, "branch moved past install commit")
	// The pin did not move, so the package is still exactly what v1 names.
	wantOutdated(t, checkOutdated(t, svc, pinned, true), false, "tag still at install commit")

	runGit(t, source, "tag", "-f", "v1")
	wantOutdated(t, checkOutdated(t, svc, pinned, true), true, "tag repointed")
}

func TestCheckOutdatedWithoutRecordedBaselineIsUnknown(t *testing.T) {
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "one")
	svc, state := newOutdatedTestSkills(t, nil)
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}

	key := metadataStateKey(svc.packageDir(source))
	var metadata skillMetadata
	if err := json.Unmarshal([]byte((*state)[key]), &metadata); err != nil {
		t.Fatal(err)
	}
	metadata.SourceProbe = ""
	// A local tree has no definitional fallback like a Git sha, so a pre-probe install stays unknown until reacquired.
	metadata.SourceFingerprint = ""
	metadata.Outdated = nil
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	(*state)[key] = string(raw)

	if verdict := checkOutdated(t, svc, pkg, true); verdict != nil {
		t.Fatalf("verdict = %v, want unknown", *verdict)
	}
}

func TestCheckOutdatedUnknownForPackageWithNoMetadata(t *testing.T) {
	svc, _ := newOutdatedTestSkills(t, nil)
	if verdict := checkOutdated(t, svc, config.SkillPackage{Source: "owner/repo"}, true); verdict != nil {
		t.Fatalf("verdict = %v, want unknown for a package that was never installed", *verdict)
	}
}

func TestCheckOutdatedGitSubpathHasNoCheapIdentity(t *testing.T) {
	svc, _ := newOutdatedTestSkills(t, nil)
	// A repo HEAD moves for commits that never touch the subpath, so it is no identity for one.
	if _, kind := svc.probeSource(context.Background(), config.SkillPackage{Source: "owner/repo/nested/path"}); kind != skillProbeNone {
		t.Fatalf("probe kind = %v, want skillProbeNone for a Git subpath", kind)
	}
}

// Counts index requests so a test can assert exactly how often the source was probed.
func wellKnownIndexServer(t *testing.T, body *atomic.Pointer[[]byte]) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var indexHits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		skill := *body.Load()
		switch r.URL.Path {
		case "/.well-known/agent-skills/index.json":
			indexHits.Add(1)
			digest := sha256.Sum256(skill)
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprintf(w, `{"$schema":"%s","skills":[{"name":"remote","type":"skill-md","description":"remote skill","url":"/remote/SKILL.md","digest":"sha256:%s"}]}`,
				wellKnownSchemaV02, hex.EncodeToString(digest[:]))
		case "/remote/SKILL.md":
			_, _ = w.Write(skill)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server, &indexHits
}

func TestCheckOutdatedWellKnownIndexDigestAndRecheckCadence(t *testing.T) {
	var body atomic.Pointer[[]byte]
	one := []byte("---\nname: remote\ndescription: remote skill\n---\n\none\n")
	body.Store(&one)
	server, indexHits := wellKnownIndexServer(t, &body)

	svc, _ := newOutdatedTestSkills(t, server.Client())
	pkg := config.SkillPackage{Source: server.URL}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}

	installHits := indexHits.Load()
	wantOutdated(t, checkOutdated(t, svc, pkg, true), false, "unchanged catalog")
	if got := indexHits.Load(); got != installHits+1 {
		t.Fatalf("forced check made %d index requests, want 1", got-installHits)
	}

	two := []byte("---\nname: remote\ndescription: remote skill\n---\n\ntwo\n")
	body.Store(&two)

	// Within the recheck interval the recorded verdict answers, so no request leaves the process.
	before := indexHits.Load()
	wantOutdated(t, checkOutdated(t, svc, pkg, false), false, "cadence-guarded check")
	if got := indexHits.Load(); got != before {
		t.Fatalf("cadence-guarded check made %d index requests, want 0", got-before)
	}

	wantOutdated(t, checkOutdated(t, svc, pkg, true), true, "forced check after digest change")
	if got := indexHits.Load(); got != before+1 {
		t.Fatalf("forced recheck made %d index requests, want 1", got-before)
	}
}

func TestCheckOutdatedRechecksOnceIntervalElapsed(t *testing.T) {
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "one")
	svc, state := newOutdatedTestSkills(t, nil)
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "two")
	wantOutdated(t, checkOutdated(t, svc, pkg, false), false, "verdict inside the recheck interval")

	key := metadataStateKey(svc.packageDir(source))
	var metadata skillMetadata
	if err := json.Unmarshal([]byte((*state)[key]), &metadata); err != nil {
		t.Fatal(err)
	}
	metadata.OutdatedCheckedAt = time.Now().Add(-2 * skillOutdatedRecheckInterval)
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	(*state)[key] = string(raw)

	wantOutdated(t, checkOutdated(t, svc, pkg, false), true, "verdict once the interval elapsed")
}

func TestInventoryReportsRecordedOutdatedVerdict(t *testing.T) {
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "one")
	svc, _ := newOutdatedTestSkills(t, nil)
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "two")
	checkOutdated(t, svc, pkg, true)

	inventory, err := svc.Inventory(context.Background(), source, []string{"test"})
	if err != nil {
		t.Fatal(err)
	}
	wantOutdated(t, inventory.Outdated, true, "inventory verdict")
	if inventory.OutdatedCheckedAt.IsZero() {
		t.Fatal("inventory did not carry the check time")
	}
}

func TestEntryStatesNameTheReasonPerEntry(t *testing.T) {
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "one")
	svc, _ := newOutdatedTestSkills(t, nil)
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}

	states, err := svc.EntryStates(source, []string{"test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Kind != SkillEntryOwnedLink {
		t.Fatalf("states = %+v, want one owned-link entry", states)
	}
	if states[0].Link == "" {
		t.Fatal("owned link entry did not report its target")
	}

	entry := filepath.Join(svc.home, ".test", "skills", "demo")
	if err := os.Remove(entry); err != nil {
		t.Fatal(err)
	}
	writeTestSkill(t, entry, "demo", "someone else's content")
	states, err = svc.EntryStates(source, []string{"test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Kind != SkillEntryDrifted {
		t.Fatalf("states = %+v, want one drifted entry", states)
	}

	if err := os.RemoveAll(entry); err != nil {
		t.Fatal(err)
	}
	states, err = svc.EntryStates(source, []string{"test"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].Kind != SkillEntryMissing {
		t.Fatalf("states = %+v, want one missing entry", states)
	}
}

func TestPackageStoreReportsDirAndHash(t *testing.T) {
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "one")
	svc, _ := newOutdatedTestSkills(t, nil)
	pkg := config.SkillPackage{Source: source}

	dir, hash, err := svc.PackageStore(source)
	if err != nil {
		t.Fatal(err)
	}
	if dir == "" || hash != "" {
		t.Fatalf("PackageStore before install = (%q, %q), want a dir and an empty hash", dir, hash)
	}

	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	dir, hash, err = svc.PackageStore(source)
	if err != nil {
		t.Fatal(err)
	}
	if dir != svc.packageDir(source) || hash == "" {
		t.Fatalf("PackageStore after install = (%q, %q)", dir, hash)
	}
}

func TestCheckOutdatedIgnoresBaselineFromAnotherProbeKind(t *testing.T) {
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "one")
	svc, state := newOutdatedTestSkills(t, nil)
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}
	wantOutdated(t, checkOutdated(t, svc, pkg, true), false, "unchanged local tree")

	runGit(t, source, "init", "-q", "-b", "main")
	runGit(t, source, "config", "user.email", "test@example.com")
	runGit(t, source, "config", "user.name", "test")
	runGit(t, source, "add", ".")
	runGit(t, source, "commit", "-qm", "one")

	if verdict := checkOutdated(t, svc, pkg, true); verdict != nil {
		t.Fatalf("verdict = %v, want unknown once the probe kind changed", *verdict)
	}

	key := metadataStateKey(svc.packageDir(source))
	var metadata skillMetadata
	if err := json.Unmarshal([]byte((*state)[key]), &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.SourceProbeKind != "git-remote" || !isGitSha(metadata.SourceProbe) {
		t.Fatalf("baseline = %q/%q, want a re-baselined git sha", metadata.SourceProbeKind, metadata.SourceProbe)
	}
	wantOutdated(t, checkOutdated(t, svc, pkg, true), false, "verdict against the re-baselined probe")
}

func TestCheckOutdatedTrustsLegacyGitBaselineWithoutRecordedKind(t *testing.T) {
	source := t.TempDir()
	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "one")
	runGit(t, source, "init", "-q", "-b", "main")
	runGit(t, source, "config", "user.email", "test@example.com")
	runGit(t, source, "config", "user.name", "test")
	runGit(t, source, "add", ".")
	runGit(t, source, "commit", "-qm", "one")
	svc, state := newOutdatedTestSkills(t, nil)
	pkg := config.SkillPackage{Source: source}
	if _, err := svc.Install(context.Background(), pkg, []string{"test"}); err != nil {
		t.Fatal(err)
	}

	key := metadataStateKey(svc.packageDir(source))
	var metadata skillMetadata
	if err := json.Unmarshal([]byte((*state)[key]), &metadata); err != nil {
		t.Fatal(err)
	}
	metadata.SourceProbeKind = ""
	raw, err := json.Marshal(metadata)
	if err != nil {
		t.Fatal(err)
	}
	(*state)[key] = string(raw)

	wantOutdated(t, checkOutdated(t, svc, pkg, true), false, "legacy sha baseline at the installed commit")

	writeTestSkill(t, filepath.Join(source, "skills", "demo"), "demo", "two")
	runGit(t, source, "commit", "-qam", "two")
	wantOutdated(t, checkOutdated(t, svc, pkg, true), true, "legacy sha baseline behind the remote")
}
