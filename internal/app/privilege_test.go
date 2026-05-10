package app

import (
	"context"
	"errors"
	"testing"
)

func TestRecordPrivilegeError_NilDB(t *testing.T) {
	// App with no DB initialised — readDB() returns nil.
	// recordPrivilegeError must not panic.
	a := &App{}
	err := errors.New("sudo: a password is required")
	a.recordPrivilegeError(context.Background(), "vim", "apt", "vim", err)
	// If we get here without panic, the nil-guard works.
}

func TestRecordPrivilegeError_NonPrivilegeError(t *testing.T) {
	// Non-privilege errors should be silently ignored (no DB access).
	a := &App{} // nil DB — would panic if it tried to access DB
	err := errors.New("network timeout")
	a.recordPrivilegeError(context.Background(), "vim", "apt", "vim", err)
}
