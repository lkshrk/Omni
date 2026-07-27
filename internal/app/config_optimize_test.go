package app

import (
	"errors"
	"reflect"
	"testing"

	"github.com/lkshrk/omni/internal/config"
)

func TestRunDoctorFixers_IgnoreCleanupStillRunsAfterOptimizeError(t *testing.T) {
	t.Parallel()
	optimizeErr := errors.New("unsafe dedupe")
	ignoreRan := false
	storeRan := false
	result := runDoctorFixers(false,
		func(bool) (*config.OptimizeReport, error) { return nil, optimizeErr },
		func() ([]string, error) {
			ignoreRan = true
			return []string{"myapp"}, nil
		},
		func(bool) (SkillStoreFixReport, error) {
			storeRan = true
			return SkillStoreFixReport{Debris: []string{"/store/.install-1"}}, nil
		},
	)

	if !ignoreRan {
		t.Fatal("ignore cleanup did not run after optimize failure")
	}
	if !storeRan {
		t.Fatal("skill store cleanup did not run after optimize failure")
	}
	if !errors.Is(result.Err(), optimizeErr) {
		t.Fatalf("result error = %v, want optimize error", result.Err())
	}
	if want := []string{"myapp"}; !reflect.DeepEqual(result.IgnoreModified, want) {
		t.Fatalf("modified = %v, want %v", result.IgnoreModified, want)
	}
	if want := []string{"/store/.install-1"}; !reflect.DeepEqual(result.SkillStore.Debris, want) {
		t.Fatalf("skill store debris = %v, want %v", result.SkillStore.Debris, want)
	}
}

func TestRunDoctorFixers_DryRunSkipsIgnoreCleanup(t *testing.T) {
	t.Parallel()
	ignoreRan := false
	storeDryRun := false
	result := runDoctorFixers(true,
		func(bool) (*config.OptimizeReport, error) { return &config.OptimizeReport{}, nil },
		func() ([]string, error) {
			ignoreRan = true
			return nil, nil
		},
		func(dryRun bool) (SkillStoreFixReport, error) {
			storeDryRun = dryRun
			return SkillStoreFixReport{}, nil
		},
	)

	if ignoreRan {
		t.Fatal("dry-run executed ignore cleanup")
	}
	if !storeDryRun {
		t.Fatal("skill store fixer did not receive the dry-run flag")
	}
	if err := result.Err(); err != nil {
		t.Fatalf("dry-run result: %v", err)
	}
}
