//go:build integration

package integration_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDoctorFixBreaksOutdatedAPMOnboardingRecoveryCycle(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	bin := filepath.Join(root, "bin")
	configPath := filepath.Join(home, ".config", "omni", "settings.json")
	cacheDir := filepath.Join(root, "cache")
	stateDir := filepath.Join(root, "state")
	versionFile := filepath.Join(root, "apm-version")
	manifestPath := filepath.Join(home, ".apm", "apm.yml")
	manifest := []byte("name: recovery-test\nversion: 1.0.0\n")

	writeIntegrationFile(t, configPath, `{"version":24,"hosts":{"recovery-test":[]},"groups":[{"name":"recovery-test","special":"host"}]}`)
	writeIntegrationFile(t, manifestPath, string(manifest))
	writeIntegrationFile(t, filepath.Join(home, ".apm", "apm.lock.yaml"), "lockfileVersion: 1\n")
	writeIntegrationFile(t, versionFile, "0.28.0+omni.8\n")
	writeIntegrationFile(t, filepath.Join(bin, "apm"), `#!/bin/sh
case "$1" in
  --version) IFS= read -r version < "$APM_VERSION_FILE"; printf 'Agent Package Manager (APM) CLI version %s\n' "$version" ;;
  targets) printf '[]\n' ;;
  *) printf '{}\n' ;;
esac
`)
	writeIntegrationFile(t, filepath.Join(bin, "uv"), `#!/bin/sh
printf '0.28.0+omni.8\n' > "$APM_VERSION_FILE"
`)
	for _, name := range []string{"apm", "uv"} {
		if err := os.Chmod(filepath.Join(bin, name), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	omni, err := exec.LookPath("omni")
	if err != nil {
		t.Fatalf("find integration omni subprocess: %v", err)
	}
	env := append(os.Environ(),
		"HOME="+home,
		"USERPROFILE="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_CACHE_HOME="+filepath.Join(root, "xdg-cache"),
		"XDG_STATE_HOME="+stateDir,
		"OMNI_HOSTNAME=recovery-test",
		"APM_VERSION_FILE="+versionFile,
		"PATH="+bin+string(os.PathListSeparator)+filepath.Dir(omni)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(omni, append([]string{"--config", configPath, "--cache-dir", cacheDir}, args...)...)
		cmd.Env = env
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("omni %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		return string(output)
	}

	planPath := filepath.Join(root, "plan.json")
	run("agents", "onboard", "--plan-json", planPath)
	var plan struct {
		PlanID         string `json:"plan_id"`
		ResolutionID   string `json:"resolution_id"`
		OperationID    string `json:"operation_id"`
		CandidateSetID string `json:"candidate_set_id"`
		PreimageSet    string `json:"preimage_set"`
	}
	planData, err := os.ReadFile(planPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(planData, &plan); err != nil {
		t.Fatal(err)
	}
	opDir := filepath.Join(stateDir, "omni", "onboarding", plan.OperationID)
	writeIntegrationFile(t, filepath.Join(opDir, "plan.json"), string(planData))
	hash := sha256.Sum256(manifest)
	emptyHash := sha256.Sum256(nil)
	journal := map[string]any{
		"schema_version": 1, "operation_id": plan.OperationID, "plan_id": plan.PlanID,
		"resolution_id": plan.ResolutionID, "candidate_set_id": plan.CandidateSetID,
		"preimage_set": plan.PreimageSet, "phase": "preflighted", "documents": []any{},
		"manifest_path": manifestPath, "manifest_data": base64.StdEncoding.EncodeToString(manifest),
		"manifest_existed": true, "manifest_mode": 0o644, "manifest_hash": hex.EncodeToString(hash[:]),
		"proposed_manifest_hash": hex.EncodeToString(hash[:]), "marketplace_existed": false,
		"marketplace_hash": hex.EncodeToString(emptyHash[:]),
	}
	journalData, err := json.Marshal(journal)
	if err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(opDir, "journal.json")
	writeIntegrationFile(t, journalPath, string(journalData))
	for _, dir := range []string{stateDir, filepath.Join(stateDir, "omni"), filepath.Join(stateDir, "omni", "onboarding"), opDir} {
		if err := os.Chmod(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeIntegrationFile(t, versionFile, "0.28.0+omni.7\n")
	blocked := exec.Command(omni, "--config", configPath, "--cache-dir", cacheDir, "agents", "onboard")
	blocked.Env = env
	blockedOutput, blockedErr := blocked.CombinedOutput()
	if blockedErr == nil || !strings.Contains(string(blockedOutput), "onboarding operation") {
		t.Fatalf("fresh onboarding during recovery: err=%v output=%s", blockedErr, blockedOutput)
	}

	configBefore, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	journalBefore, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	doctorOutput := run("doctor", "--fix")
	if !strings.Contains(doctorOutput, "upgraded APM via:") || !strings.Contains(doctorOutput, "agents onboard resume") {
		t.Fatalf("doctor --fix output = %q", doctorOutput)
	}
	configAfter, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	journalAfter, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(configAfter, configBefore) || !bytes.Equal(journalAfter, journalBefore) {
		t.Fatal("doctor --fix mutated onboarding state")
	}
	version := strings.TrimSpace(runAPM(t, env, bin, "--version"))
	if !strings.HasSuffix(version, "0.28.0+omni.8") {
		t.Fatalf("doctor --fix left stale APM: %s", version)
	}
	run("agents", "onboard", "resume", "--operation", plan.OperationID)
	journalData, err = os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(journalData, &journal); err != nil {
		t.Fatal(err)
	}
	if journal["phase"] != "complete" {
		t.Fatalf("resume left onboarding phase %v", journal["phase"])
	}
	run("tools", "list")
}

func runAPM(t *testing.T, env []string, bin string, args ...string) string {
	t.Helper()
	cmd := exec.Command(filepath.Join(bin, "apm"), args...)
	cmd.Env = env
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("apm %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}
