package cli

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/provider"
)

func TestPrintProviderErrorAdvice(t *testing.T) {
	err := provider.NewExternallyManagedPythonError("pip3", "upgrade", provider.Tool{Name: "pip", Provider: "python"}, errors.New("exit 1"), "raw stderr", []provider.ErrorSolution{
		{
			Label:          "Reinstall this tool with uv",
			Command:        "omni switch pip --from python --to uv",
			Detail:         "uv installs Python CLI tools into isolated tool environments.",
			Action:         provider.ErrorSolutionActionSwitchProvider,
			TargetProvider: "uv",
		},
	})
	var b bytes.Buffer
	printProviderErrorAdvice(&b, err)

	out := b.String()
	for _, want := range []string{"suggestion: Reinstall this tool with uv", "omni switch pip --from python --to uv", "isolated tool environments"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output %q missing %q", out, want)
		}
	}
	if strings.Contains(out, "raw stderr") {
		t.Fatalf("advice output should not print raw provider stderr: %q", out)
	}
}

func TestPrintProviderErrorAdvice_NoActionError(t *testing.T) {
	var b bytes.Buffer
	printProviderErrorAdvice(&b, errors.New("plain failure"))
	if b.Len() != 0 {
		t.Fatalf("output = %q, want empty", b.String())
	}
}
