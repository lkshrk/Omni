package flock

import (
	"os"
	"os/exec"
	"testing"
	"time"

	_ "github.com/lkshrk/omni/internal/testguard"
)

func TestSharedLockAllowsAnotherProcessAndBlocksExclusiveProcess(t *testing.T) {
	path := t.TempDir() + "/state.lock"
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := RLock(file); err != nil {
		t.Fatal(err)
	}

	shared := flockHelper(t, path, "shared")
	if out, err := shared.CombinedOutput(); err != nil {
		t.Fatalf("second process shared lock: %v\n%s", err, out)
	}

	exclusive := flockHelper(t, path, "exclusive")
	if err := exclusive.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- exclusive.Wait() }()
	select {
	case err := <-done:
		t.Fatalf("exclusive process bypassed shared lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err := Unlock(file); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		_ = exclusive.Process.Kill()
		t.Fatal("exclusive process did not acquire released lock")
	}
}

func flockHelper(t *testing.T, path, mode string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestFlockHelperProcess$")
	cmd.Env = append(os.Environ(), "OMNI_TEST_HELPER_FLOCK=1", "OMNI_TEST_HELPER_FLOCK_PATH="+path, "OMNI_TEST_HELPER_FLOCK_MODE="+mode)
	return cmd
}

func TestFlockHelperProcess(t *testing.T) {
	if os.Getenv("OMNI_TEST_HELPER_FLOCK") != "1" {
		return
	}
	file, err := os.OpenFile(os.Getenv("OMNI_TEST_HELPER_FLOCK_PATH"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		os.Exit(2)
	}
	if os.Getenv("OMNI_TEST_HELPER_FLOCK_MODE") == "shared" {
		err = RLock(file)
	} else {
		err = Lock(file)
	}
	if err != nil {
		os.Exit(3)
	}
	_ = Unlock(file)
	_ = file.Close()
}
