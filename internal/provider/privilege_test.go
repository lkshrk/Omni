package provider

import (
	"context"
	"errors"
	"testing"
)

func TestSystemPrivilegePlan_RequiresPrivilege(t *testing.T) {
	plan := SystemPrivilegePlan("apt", PrivilegeActionInstall, Tool{Name: "vim"})
	if plan.Requirement != PrivilegeRequired {
		t.Fatalf("Requirement = %q, want %q", plan.Requirement, PrivilegeRequired)
	}
	if plan.Reason != "apt install vim" {
		t.Fatalf("Reason = %q, want apt install vim", plan.Reason)
	}
}

func TestSystemPrivilegePlanProvider(t *testing.T) {
	if got := SystemPrivilegePlanProvider(SystemPrivilegePlan("dnf", PrivilegeActionInstall, Tool{Name: "vim"})); got != "dnf" {
		t.Fatalf("SystemPrivilegePlanProvider = %q, want dnf", got)
	}
	if got := SystemPrivilegePlanProvider(PrivilegePlan{Reason: "package manager needs sudo/root access"}); got != "" {
		t.Fatalf("SystemPrivilegePlanProvider = %q, want empty for generic reason", got)
	}
}

func TestClassifyPrivilegeError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "sudo password", err: errors.New("sudo: a password is required"), want: true},
		{name: "root required", err: errors.New("this command must be run as root"), want: true},
		{name: "administrator privileges", err: errors.New("installer requires administrator privileges"), want: true},
		{name: "admin access", err: errors.New("operation needs admin access"), want: true},
		{name: "permission denied", err: errors.New("open /Applications/foo: permission denied"), want: true},
		{name: "externally managed python", err: errors.New("externally-managed-environment"), want: false},
		{name: "typed externally managed python", err: NewExternallyManagedPythonError("pip3", "install", Tool{Name: "black"}, errors.New("exit 1"), "externally-managed-environment", nil), want: false},
		{name: "context canceled", err: context.Canceled, want: false},
		{name: "regular failure", err: errors.New("package not found"), want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan, ok := ClassifyPrivilegeError(tc.err)
			if ok != tc.want {
				t.Fatalf("ClassifyPrivilegeError ok = %v, want %v", ok, tc.want)
			}
			if ok && plan.Requirement != PrivilegeRequired {
				t.Fatalf("Requirement = %q, want %q", plan.Requirement, PrivilegeRequired)
			}
		})
	}
}

func TestPrivilegedCommand_BypassesSudoInTests(t *testing.T) {
	cmd, args := PrivilegedCommand("apt-get", "install", "vim")
	if cmd != "apt-get" {
		t.Fatalf("cmd = %q, want apt-get", cmd)
	}
	if len(args) != 2 || args[0] != "install" || args[1] != "vim" {
		t.Fatalf("args = %v, want [install vim]", args)
	}
}
