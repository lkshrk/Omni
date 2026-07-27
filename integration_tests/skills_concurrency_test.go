//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lkshrk/omni/internal/app"
)

const skillsConcurrencyConfig = `{"version":20,"settings":{"agents_use":["codex"]},` +
	`"agents":{"packages":[{"source":"./source"}]}}`

const skillsConcurrencyManifest = `---
name: myskill
description: Cross-process skill store lock test.
---

myskill
`

type skillsStoreEnv struct {
	bin        string
	dir        string
	configPath string
	home       string
	dataDir    string
	env        []string
}

func newSkillsStoreEnv(t *testing.T) skillsStoreEnv {
	t.Helper()
	bin := buildOmniBinary(t)
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	data := filepath.Join(dir, "data")
	skill := filepath.Join(dir, "source", "skills", "myskill")
	for _, path := range []string{home, data, skill} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(skill, "SKILL.md"), []byte(skillsConcurrencyManifest), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(configPath, []byte(skillsConcurrencyConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	return skillsStoreEnv{
		bin:        bin,
		dir:        dir,
		configPath: configPath,
		home:       home,
		dataDir:    filepath.Join(data, "omni", "skills"),
		env: []string{
			"HOME=" + home,
			"XDG_DATA_HOME=" + data,
			"OMNI_CACHE_DIR=" + filepath.Join(dir, ".omni-cache"),
			"OMNI_HOSTNAME=testhost",
			"PATH=" + minimalTestPATH,
		},
	}
}

func (e skillsStoreEnv) command(ctx context.Context, args []string) (*exec.Cmd, *bytes.Buffer, *bytes.Buffer) {
	cmd := exec.CommandContext(ctx, e.bin, append([]string{"--config", e.configPath}, args...)...)
	cmd.Dir = e.dir
	cmd.Env = e.env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	return cmd, &stdout, &stderr
}

type omniRun struct {
	args   []string
	stdout string
	stderr string
	err    error
}

func (r omniRun) String() string {
	return "omni " + strings.Join(r.args, " ") + "\nstdout:\n" + r.stdout + "stderr:\n" + r.stderr
}

func (e skillsStoreEnv) run(t *testing.T, args ...string) omniRun {
	t.Helper()
	return e.race(t, args)[0]
}

// Starts every invocation before waiting, so the processes contend for the store lock instead of running back to back.
func (e skillsStoreEnv) race(t *testing.T, argsets ...[]string) []omniRun {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	runs := make([]omniRun, len(argsets))
	var wait sync.WaitGroup
	for i, args := range argsets {
		cmd, stdout, stderr := e.command(ctx, args)
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting omni %s: %v", strings.Join(args, " "), err)
		}
		wait.Add(1)
		go func() {
			defer wait.Done()
			err := cmd.Wait()
			runs[i] = omniRun{args: args, stdout: stdout.String(), stderr: stderr.String(), err: err}
		}()
	}
	wait.Wait()
	return runs
}

// Long enough that a store op which never blocks is certain to have finished inside it.
const storeLockBlockWindow = 20 * time.Second

func TestSkillsConcurrency(t *testing.T) {
	// The racing subtests below only assert convergence; without this one they also pass with the flock removed.
	t.Run("store lock blocks a second process", func(t *testing.T) {
		env := newSkillsStoreEnv(t)
		if run := env.run(t, "agents", "skills", "restore"); run.err != nil {
			t.Fatalf("seeding the store: %v\n%s", run.err, run)
		}

		release := holdStoreLock(t, filepath.Join(env.dataDir, ".lock"))
		released := false
		defer func() {
			if !released {
				release()
			}
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		args := []string{"--yes", "agents", "skills", "remove", "./source", "--purge"}
		cmd, stdout, stderr := env.command(ctx, args)
		if err := cmd.Start(); err != nil {
			t.Fatalf("starting the blocked remove: %v", err)
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		blocked := func() omniRun {
			return omniRun{args: args, stdout: stdout.String(), stderr: stderr.String()}
		}

		select {
		case err := <-done:
			t.Fatalf("remove finished while the store lock was held (err=%v), so nothing serializes store writes across processes\n%s",
				err, blocked())
		case <-time.After(storeLockBlockWindow):
		}

		release()
		released = true

		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("remove failed after the store lock was released: %v\n%s", err, blocked())
			}
		case <-time.After(time.Minute):
			t.Fatal("remove never finished after the store lock was released")
		}

		assertSkillStoreClean(t, env)
	})

	t.Run("concurrent restore", func(t *testing.T) {
		env := newSkillsStoreEnv(t)
		for _, run := range env.race(t,
			[]string{"agents", "skills", "restore"},
			[]string{"agents", "skills", "restore"},
		) {
			if run.err != nil {
				t.Fatalf("concurrent restore failed: %v\n%s", run.err, run)
			}
			if !strings.Contains(run.stdout, "0 failed") {
				t.Fatalf("concurrent restore did not converge:\n%s", run)
			}
		}
		if !skillLinkState(t, env) {
			t.Fatal("concurrent restore left the skill uninstalled")
		}
		assertSkillStoreClean(t, env)
		assertNoDoctorStoreFindings(t, env)
	})

	t.Run("restore races uninstall", func(t *testing.T) {
		env := newSkillsStoreEnv(t)
		if run := env.run(t, "agents", "skills", "restore"); run.err != nil {
			t.Fatalf("seeding the store: %v\n%s", run.err, run)
		}
		for _, run := range env.race(t,
			[]string{"agents", "skills", "restore"},
			[]string{"agents", "skills", "uninstall", "./source"},
		) {
			if run.err != nil {
				t.Fatalf("racing store operation failed: %v\n%s", run.err, run)
			}
		}
		// Either op may win the lock, so a fully installed or fully removed store is correct; a partial one is not.
		skillLinkState(t, env)
		assertSkillStoreClean(t, env)
		assertNoDoctorStoreFindings(t, env)
	})
}

func skillLinkState(t *testing.T, env skillsStoreEnv) bool {
	t.Helper()
	link := filepath.Join(env.home, ".codex", "skills", "myskill")
	info, err := os.Lstat(link)
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		t.Fatalf("inspecting %s: %v", link, err)
	}
	if info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("%s is not a link into the package store", link)
	}
	resolved, err := filepath.EvalSymlinks(link)
	if err != nil {
		t.Fatalf("resolving %s: %v", link, err)
	}
	packages, err := filepath.EvalSymlinks(filepath.Join(env.dataDir, "packages"))
	if err != nil {
		t.Fatalf("resolving the package store: %v", err)
	}
	if !strings.HasPrefix(resolved, packages+string(os.PathSeparator)) {
		t.Fatalf("%s resolves outside the package store: %s", link, resolved)
	}
	if _, err := os.Stat(filepath.Join(resolved, "SKILL.md")); err != nil {
		t.Fatalf("installed SKILL.md: %v", err)
	}
	return true
}

func assertSkillStoreClean(t *testing.T, env skillsStoreEnv) {
	t.Helper()
	packages := filepath.Join(env.dataDir, "packages")
	for _, name := range dirEntryNames(t, packages) {
		if strings.HasSuffix(name, ".previous") || strings.HasSuffix(name, ".removing") ||
			strings.HasPrefix(name, ".install-") || strings.HasPrefix(name, ".adopt-") {
			t.Errorf("interrupted-operation debris in %s: %s", packages, name)
		}
	}
	skills := filepath.Join(env.home, ".codex", "skills")
	for _, name := range dirEntryNames(t, skills) {
		if strings.Contains(name, ".omni-") {
			t.Errorf("interrupted-operation debris in %s: %s", skills, name)
		}
	}
}

func dirEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func assertNoDoctorStoreFindings(t *testing.T, env skillsStoreEnv) {
	t.Helper()
	// doctor exits non-zero on any failed check, so the report is read regardless of exit status.
	run := env.run(t, "doctor", "--format", "json")
	var result app.DoctorResult
	if err := json.Unmarshal([]byte(run.stdout), &result); err != nil {
		t.Fatalf("parsing doctor JSON: %v\n%s", err, run)
	}
	for _, check := range result.Checks {
		for _, group := range check.Groups {
			for _, item := range group.Items {
				if strings.HasPrefix(item, "store: ") {
					t.Errorf("doctor reported a skill store finding: %s", item)
				}
			}
		}
	}
}
