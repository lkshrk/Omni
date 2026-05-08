package provider_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestNewExternallyManagedPythonError(t *testing.T) {
	cause := errors.New("exit status 1")
	solutions := []provider.ErrorSolution{
		{
			Label:          "Reinstall this tool with uv",
			Command:        "omni switch pip --from python --to uv",
			Detail:         "isolated tools",
			Action:         provider.ErrorSolutionActionSwitchProvider,
			TargetProvider: "uv",
		},
	}
	err := provider.NewExternallyManagedPythonError("pip3", "upgrade", provider.Tool{Name: "pip", Provider: "python"}, cause, "error: externally-managed-environment\nfull stderr", solutions)

	actionErr, ok := provider.ActionErrorFrom(err)
	if !ok {
		t.Fatalf("ActionErrorFrom ok = false for %T", err)
	}
	if actionErr.Code != provider.ErrorExternallyManagedPython {
		t.Fatalf("Code = %q, want %q", actionErr.Code, provider.ErrorExternallyManagedPython)
	}
	if actionErr.Solutions[0].Command != "omni switch pip --from python --to uv" {
		t.Fatalf("solution command = %q", actionErr.Solutions[0].Command)
	}
	if actionErr.Solutions[0].Action != provider.ErrorSolutionActionSwitchProvider || actionErr.Solutions[0].TargetProvider != "uv" {
		t.Fatalf("solution action = %#v, want switch to uv", actionErr.Solutions[0])
	}
	if !errors.Is(err, cause) {
		t.Fatal("ActionError should unwrap the original command failure")
	}
	if !strings.Contains(err.Error(), "omni switch pip --from python --to uv") {
		t.Fatalf("summary should include the primary remediation, got %q", err.Error())
	}
	if strings.Contains(err.Error(), "full stderr") {
		t.Fatalf("summary should not include raw stderr: %q", err.Error())
	}
}

func TestIsExternallyManagedPythonOutput(t *testing.T) {
	if !provider.IsExternallyManagedPythonOutput("ERROR: Externally-Managed-Environment") {
		t.Fatal("expected case-insensitive match")
	}
	if provider.IsExternallyManagedPythonOutput("permission denied") {
		t.Fatal("unexpected externally-managed match")
	}
}
