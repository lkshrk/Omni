package provider

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type PrivilegeAction string

const (
	PrivilegeActionInstall   PrivilegeAction = "install"
	PrivilegeActionUninstall PrivilegeAction = "uninstall"
	PrivilegeActionUpgrade   PrivilegeAction = "upgrade"
)

type PrivilegeRequirement string

const (
	PrivilegeNone     PrivilegeRequirement = ""
	PrivilegeMaybe    PrivilegeRequirement = "maybe"
	PrivilegeRequired PrivilegeRequirement = "required"
)

type PrivilegePlan struct {
	Requirement PrivilegeRequirement
	Reason      string
}

func (p PrivilegePlan) RequiresPrivilege() bool {
	return p.Requirement == PrivilegeRequired || p.Requirement == PrivilegeMaybe
}

type PrivilegePlanner interface {
	PrivilegePlan(ctx context.Context, action PrivilegeAction, tool Tool) (PrivilegePlan, error)
}

// PrivilegeCommandPlanner returns the raw package-manager command for an
// interactive privileged action. Callers own sudo policy.
type PrivilegeCommandPlanner interface {
	PrivilegeCommand(action PrivilegeAction, tool Tool) (cmd string, args []string, ok bool)
}

func SystemPrivilegePlan(providerName string, action PrivilegeAction, tool Tool) PrivilegePlan {
	pkg := tool.EffectivePackage()
	reason := providerName + " " + string(action)
	if pkg != "" {
		reason += " " + pkg
	}
	return PrivilegePlan{Requirement: PrivilegeRequired, Reason: reason}
}

func SystemPrivilegePlanProvider(plan PrivilegePlan) string {
	fields := strings.Fields(plan.Reason)
	if len(fields) == 0 {
		return ""
	}
	switch fields[0] {
	case "apt", "apk", "dnf", "pacman", "zypper", "brew":
		return fields[0]
	default:
		return ""
	}
}

func ClassifyPrivilegeError(err error) (PrivilegePlan, bool) {
	if err == nil || errors.Is(err, context.Canceled) {
		return PrivilegePlan{}, false
	}
	if HasErrorCode(err, ErrorExternallyManagedPython) {
		return PrivilegePlan{}, false
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "externally-managed-environment"):
		return PrivilegePlan{}, false
	case strings.Contains(msg, "sudo"),
		strings.Contains(msg, "administrator privileges"),
		strings.Contains(msg, "administrator access"),
		strings.Contains(msg, "admin privileges"),
		strings.Contains(msg, "admin access"),
		strings.Contains(msg, "password is required"),
		strings.Contains(msg, "a terminal is required"),
		strings.Contains(msg, "no tty present"),
		strings.Contains(msg, "requires a password"),
		strings.Contains(msg, "authentication is required"),
		strings.Contains(msg, "must be run as root"),
		strings.Contains(msg, "need to be root"),
		strings.Contains(msg, "are you root"),
		strings.Contains(msg, "permission denied"),
		strings.Contains(msg, "operation not permitted"):
		return PrivilegePlan{Requirement: PrivilegeRequired, Reason: "package manager needs sudo/root access"}, true
	default:
		return PrivilegePlan{}, false
	}
}

func PrivilegedCommand(cmd string, args ...string) (string, []string) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 || runningUnderGoTest() {
		return cmd, args
	}
	return "sudo", append([]string{"-n", cmd}, args...)
}

func SudoValidationCommand(ctx context.Context) *exec.Cmd {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 || runningUnderGoTest() {
		return nil
	}
	return exec.CommandContext(ctx, "sudo", "-v")
}

func runningUnderGoTest() bool {
	base := filepath.Base(os.Args[0])
	return strings.HasSuffix(base, ".test") || strings.Contains(base, ".test.")
}
