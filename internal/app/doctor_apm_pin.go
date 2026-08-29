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

func checkAPMInstallReceipt(result *DoctorResult, path string) {
	data, err := os.ReadFile(path)
	if err != nil {
		return
	}
	url, ref := parseAPMPackagePin(apmPackagePin)
	pinned := url + "@" + ref

	var receipt apmInstallReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		result.addCheck("apm-pin", "APM provenance", DoctorStatusWarn,
			"apm install receipt could not be read", err.Error(), "pinned "+pinned, path)
		return
	}
	commit := receipt.VCSInfo.CommitID
	if commit == "" {
		commit = receipt.VCSInfo.RequestedRevision
	}
	if receipt.URL == url && commit == ref {
		result.addCheck("apm-pin", "APM provenance", DoctorStatusOK, "apm was installed from the pinned commit", pinned, path)
		return
	}
	installed := receipt.URL
	if commit != "" {
		installed += "@" + commit
	}
	result.addCheck("apm-pin", "APM provenance", DoctorStatusWarn,
		"apm was not installed from the pinned source",
		"installed "+installed, "pinned "+pinned, apmVersionFixHint, path)
}

func (a *App) doctorAPMPin(result *DoctorResult) {
	if !a.APMAvailable() {
		return
	}
	if receipt := locateAPMInstallReceipt(); receipt != "" {
		checkAPMInstallReceipt(result, receipt)
	}
}
