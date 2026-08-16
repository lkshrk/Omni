package rpm_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider"
	"github.com/lkshrk/omni/internal/provider/rpm"
)

func TestSummaries_UsesRPMQueryAndKeepsPartialOutput(t *testing.T) {
	exec := &executor.MockExecutor{
		Responses: []executor.MockCall{{
			Stdout: "package missing is not installed\nBash\tThe GNU shell\ncurl\tCommand line URL tool\n",
			Err:    errors.New("exit 1"),
		}},
	}

	got, err := rpm.Summaries(context.Background(), exec, []provider.Tool{
		{Name: "Bash", Package: "bash"},
		{Name: "missing", Package: "missing"},
		{Name: "curl", Package: "curl"},
	})
	if err != nil {
		t.Fatalf("Summaries: %v", err)
	}
	if got["Bash"] != "The GNU shell" || got["curl"] != "Command line URL tool" {
		t.Fatalf("unexpected summaries: %v", got)
	}
	if _, ok := got["missing"]; ok {
		t.Fatalf("missing package should not have a summary: %v", got)
	}

	call := exec.Calls[0]
	if call.Name != "rpm" || len(call.Args) < 4 || call.Args[0] != "-q" || call.Args[1] != "--queryformat" {
		t.Fatalf("unexpected rpm call: %+v", call)
	}
	if call.Args[len(call.Args)-3] != "bash" || call.Args[len(call.Args)-2] != "missing" || call.Args[len(call.Args)-1] != "curl" {
		t.Fatalf("unexpected packages: %+v", call)
	}
}

func TestSummary_ReturnsEmptyForRPMMiss(t *testing.T) {
	exec := &executor.MockExecutor{
		Responses: []executor.MockCall{{
			Stdout: "package missing is not installed\n",
			Err:    errors.New("exit 1"),
		}},
	}

	got, err := rpm.Summary(context.Background(), exec, "missing")
	if err != nil {
		t.Fatalf("Summary: %v", err)
	}
	if got != "" {
		t.Fatalf("Summary() = %q, want empty", got)
	}
}

func TestIsInstalled_NotInstalledOnRPMMissOrEmptyVersion(t *testing.T) {
	tests := []struct {
		name string
		resp executor.MockCall
	}{
		{name: "rpm miss", resp: executor.MockCall{Err: errors.New("exit 1")}},
		{name: "empty version", resp: executor.MockCall{Stdout: " \n"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			exec := &executor.MockExecutor{Responses: []executor.MockCall{tc.resp}}
			ok, version, err := rpm.IsInstalled(context.Background(), exec, "ripgrep")
			if err != nil || ok || version != "" {
				t.Fatalf("IsInstalled() = (%v, %q, %v), want false, empty, nil", ok, version, err)
			}
		})
	}
}

func TestParseInfoSummaries_MapsPackageNamesToFirstSummary(t *testing.T) {
	output := `
Name        : ripgrep
Summary     : recursively search directories
Summary     : duplicate ignored

Name        : curl
Summary     : transfer tool
`

	got := rpm.ParseInfoSummaries(output)
	if got["ripgrep"] != "recursively search directories" || got["curl"] != "transfer tool" {
		t.Fatalf("unexpected summaries: %v", got)
	}
}

func TestParseInfoSummary_ReturnsSummaryField(t *testing.T) {
	got := rpm.ParseInfoSummary("Name: ripgrep\nSummary: recursively search directories\n")
	if got != "recursively search directories" {
		t.Fatalf("ParseInfoSummary() = %q", got)
	}
}

func TestSummarySurfacesCommandOutputDetail(t *testing.T) {
	sentinel := errors.New("exit status 1")
	exec := &executor.MockExecutor{Responses: []executor.MockCall{{Err: sentinel, Stderr: "boom: repo unreachable\n"}}}
	if _, err := rpm.Summary(context.Background(), exec, "ripgrep"); err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("Summary() error = %v, want wrapped sentinel", err)
	} else if !strings.Contains(err.Error(), "boom: repo unreachable") {
		t.Fatalf("Summary() error = %v, want stderr detail", err)
	}
}

func TestSummarySurfacesStdoutDetailWhenStderrEmpty(t *testing.T) {
	sentinel := errors.New("exit status 1")
	exec := &executor.MockExecutor{Responses: []executor.MockCall{{Err: sentinel, Stdout: "fail written to stdout\n"}}}
	if _, err := rpm.Summary(context.Background(), exec, "ripgrep"); err == nil || !errors.Is(err, sentinel) {
		t.Fatalf("Summary() error = %v, want wrapped sentinel", err)
	} else if !strings.Contains(err.Error(), "fail written to stdout") {
		t.Fatalf("Summary() error = %v, want stdout detail", err)
	}
}
