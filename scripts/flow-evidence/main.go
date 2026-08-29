package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/lkshrk/omni/internal/testflow"
)

func main() {
	catalogPath := flag.String("catalog", "test/flows.json", "flow catalog path")
	evidenceDir := flag.String("evidence", ".test-evidence", "collected evidence directory")
	flag.Parse()
	if err := run(*catalogPath, *evidenceDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(catalogPath, evidenceDir string) error {
	catalog, err := testflow.Load(catalogPath)
	if err != nil {
		return err
	}
	report, err := testflow.VerifyEvidence(catalog, evidenceDir)
	if err != nil {
		return err
	}
	fmt.Printf("verified %d required evidence references; %d declared gaps\n", report.Verified, len(report.Gaps))
	for _, gap := range report.Gaps {
		fmt.Printf("gap %s %s (%s): %s\n", gap.FlowID, gap.Level, gap.TargetStage, gap.Reason)
	}
	return nil
}
