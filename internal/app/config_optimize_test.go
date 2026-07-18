package app

import (
	"errors"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestRunDoctorFixers_IgnoreCleanupStillRunsAfterOptimizeError(t *testing.T) {
	optimizeErr := errors.New("unsafe dedupe")
	ignoreRan := false
	result := runDoctorFixers(false,
		func(bool) (*config.OptimizeReport, error) { return nil, optimizeErr },
		func() ([]string, error) {
			ignoreRan = true
			return []string{"myapp"}, nil
		},
	)

	if !ignoreRan {
		t.Fatal("ignore cleanup did not run after optimize failure")
	}
	if !errors.Is(result.Err(), optimizeErr) {
		t.Fatalf("result error = %v, want optimize error", result.Err())
	}
	if want := []string{"myapp"}; !reflect.DeepEqual(result.IgnoreModified, want) {
		t.Fatalf("modified = %v, want %v", result.IgnoreModified, want)
	}
}

func TestRunDoctorFixers_DryRunSkipsIgnoreCleanup(t *testing.T) {
	ignoreRan := false
	result := runDoctorFixers(true,
		func(bool) (*config.OptimizeReport, error) { return &config.OptimizeReport{}, nil },
		func() ([]string, error) {
			ignoreRan = true
			return nil, nil
		},
	)

	if ignoreRan {
		t.Fatal("dry-run executed ignore cleanup")
	}
	if err := result.Err(); err != nil {
		t.Fatalf("dry-run result: %v", err)
	}
}
