package app

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// apmVersionPin cannot distinguish the pinned commit from any other build of the same release,
// so the installer's own receipt is the only local evidence of provenance.
type apmInstallReceipt struct {
	URL     string `json:"url"`
	VCSInfo struct {
		CommitID          string `json:"commit_id"`
		RequestedRevision string `json:"requested_revision"`
	} `json:"vcs_info"`
}

func parseAPMPackagePin(pin string) (url, ref string) {
	return splitAPMGitRef(strings.TrimPrefix(pin, "git+"))
}

func locateAPMInstallReceipt() string {
	bin, err := exec.LookPath("apm")
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(bin); err == nil {
		bin = resolved
	}
	root := filepath.Dir(filepath.Dir(bin))
	for _, dir := range []string{
		filepath.Join("lib", "python*", "site-packages"),
		filepath.Join("lib", "site-packages"),
		filepath.Join("Lib", "site-packages"),
	} {
		matches, _ := filepath.Glob(filepath.Join(root, dir, "apm_cli-*.dist-info", "direct_url.json"))
		if len(matches) > 0 {
			return matches[0]
		}
	}
	return ""
}

var apmReceiptLocator = locateAPMInstallReceipt

type apmProvenance struct {
	Installed string
	Pinned    bool
}

func parseAPMProvenance(data []byte) (apmProvenance, error) {
	var receipt apmInstallReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		return apmProvenance{}, err
	}
	url, ref := parseAPMPackagePin(apmPackagePin)
	commit := receipt.VCSInfo.CommitID
	if commit == "" {
		commit = receipt.VCSInfo.RequestedRevision
	}
	installed := receipt.URL
	if commit != "" {
		installed += "@" + commit
	}
	return apmProvenance{Installed: installed, Pinned: receipt.URL == url && commit == ref}, nil
}

// known is false for installs with no readable receipt, which must not be reinstalled on suspicion alone.
func apmProvenanceMatchesPin() (matches, known bool) {
	path := apmReceiptLocator()
	if path == "" {
		return false, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	provenance, err := parseAPMProvenance(data)
	if err != nil {
		return false, false
	}
	return provenance.Pinned, true
}

func checkAPMInstallReceipt(result *DoctorResult, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	url, ref := parseAPMPackagePin(apmPackagePin)
	pinned := url + "@" + ref

	provenance, err := parseAPMProvenance(data)
	if err != nil {
		result.addCheck("apm-pin", "APM provenance", DoctorStatusWarn,
			"apm install receipt could not be read", err.Error(), "pinned "+pinned, path)
		return
	}
	if provenance.Pinned {
		result.addCheck("apm-pin", "APM provenance", DoctorStatusOK, "apm was installed from the pinned commit", pinned, path)
		return
	}
	result.addCheck("apm-pin", "APM provenance", DoctorStatusWarn,
		"apm was not installed from the pinned source",
		"installed "+provenance.Installed, "pinned "+pinned, apmVersionFixHint, path)
}

func (a *App) doctorAPMPin(result *DoctorResult) {
	if !a.APMAvailable() {
		return
	}
	if receipt := apmReceiptLocator(); receipt != "" {
		checkAPMInstallReceipt(result, receipt)
	}
}
