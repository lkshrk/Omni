package rpm_test

import (
	"context"
	"errors"
	"testing"

	"github.com/lkshrk/omni/internal/executor"
	"github.com/lkshrk/omni/internal/provider/rpm"
)

func TestListInstalled_UsesRPMQueryAndTagsProvider(t *testing.T) {
	exec := &executor.MockExecutor{
		Responses: []executor.MockCall{{
			Stdout: "Ripgrep\t14.1.1-4\nbash\t5.2.15-1\ninvalid\n",
		}},
	}

	tools, err := rpm.ListInstalled(context.Background(), exec, "zypper")
	if err != nil {
		t.Fatalf("ListInstalled: %v", err)
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d: %+v", len(tools), tools)
	}
	if tools[0].Name != "Ripgrep" || tools[0].Provider != "zypper" || tools[0].Version != "14.1.1-4" {
		t.Fatalf("unexpected first tool: %+v", tools[0])
	}

	call := exec.Calls[0]
	if call.Name != "rpm" || len(call.Args) != 3 || call.Args[0] != "-qa" || call.Args[1] != "--queryformat" {
		t.Fatalf("unexpected rpm call: %+v", call)
	}
}

func TestInstalledMap_LowercasesNames(t *testing.T) {
	exec := &executor.MockExecutor{
		Responses: []executor.MockCall{{
			Stdout: "Ripgrep\t14.1.1-4\nBash\t5.2.15-1\n",
		}},
	}

	got, err := rpm.InstalledMap(context.Background(), exec)
	if err != nil {
		t.Fatalf("InstalledMap: %v", err)
	}
	if got["ripgrep"] != "14.1.1-4" || got["bash"] != "5.2.15-1" {
		t.Fatalf("unexpected installed map: %v", got)
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
