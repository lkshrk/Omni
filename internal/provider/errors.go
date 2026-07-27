package provider

import (
	"errors"
	"fmt"
	"strings"
)

type ErrorCode string

const (
	// pip refused to mutate a Python installation PEP 668 marks externally managed.
	ErrorExternallyManagedPython ErrorCode = "externally_managed_python"
)

type ErrorSolutionAction string

const (
	// ErrorSolutionActionSwitchProvider reinstalls one tool with TargetProvider.
	ErrorSolutionActionSwitchProvider ErrorSolutionAction = "switch_provider"
)

type ErrorSolution struct {
	Label   string
	Command string
	Detail  string
	Action  ErrorSolutionAction
	// TargetProvider is the provider or manager name accepted by app.Switch.
	TargetProvider string
}

// ActionError — Error returns Summary so CLI/TUI/status paths stay concise.
type ActionError struct {
	Code      ErrorCode
	Provider  string
	Manager   string
	Action    string
	Package   string
	Summary   string
	Detail    string
	Solutions []ErrorSolution
	Cause     error
}

func (e *ActionError) Error() string {
	if e == nil {
		return ""
	}
	if e.Summary != "" {
		return e.Summary
	}
	if e.Code != "" {
		return string(e.Code)
	}
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return "provider action failed"
}

func (e *ActionError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func ActionErrorFrom(err error) (*ActionError, bool) {
	if err == nil {
		return nil, false
	}
	var actionErr *ActionError
	if errors.As(err, &actionErr) {
		return actionErr, true
	}
	return nil, false
}

func HasErrorCode(err error, code ErrorCode) bool {
	actionErr, ok := ActionErrorFrom(err)
	return ok && actionErr.Code == code
}

// IsExternallyManagedPythonOutput detects pip's PEP 668 failure marker.
func IsExternallyManagedPythonOutput(stderr string) bool {
	return strings.Contains(strings.ToLower(stderr), "externally-managed-environment")
}

func NewExternallyManagedPythonError(manager, action string, tool Tool, cause error, stderr string, solutions []ErrorSolution) error {
	if manager == "" {
		manager = "pip"
	}
	pkg := tool.EffectivePackage()
	summary := fmt.Sprintf("%s is externally managed", manager)
	if pkg != "" && action != "" {
		summary = fmt.Sprintf("%s cannot %s %s in an externally managed Python", manager, action, pkg)
	}
	if len(solutions) > 0 && solutions[0].Command != "" {
		summary += "; try `" + solutions[0].Command + "`"
	}
	return &ActionError{
		Code:      ErrorExternallyManagedPython,
		Provider:  EcosystemPython,
		Manager:   manager,
		Action:    action,
		Package:   pkg,
		Summary:   summary,
		Detail:    strings.TrimSpace(stderr),
		Solutions: append([]ErrorSolution(nil), solutions...),
		Cause:     cause,
	}
}
