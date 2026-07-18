package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/lkshrk/omni/internal/database"
	"github.com/lkshrk/omni/internal/provider"
)

type PrivilegedToolCommand struct {
	Action        provider.PrivilegeAction
	Name          string
	ProviderName  string
	Package       string
	InstalledWith string
	Options       map[string]string
	Reason        string
	Command       string
	Args          []string
	Display       string
}

type PrivilegeQueueRequest struct {
	RowErrors    map[string]string
	Actions      map[string]provider.PrivilegeAction
	VisibleTools []*ToolView
	Tools        []*ToolView
}

type PrivilegeQueuePlan struct {
	Items     []PrivilegeQueueItem
	RowErrors map[string]string
}

type PrivilegeQueueItem struct {
	Key             string
	Tool            *database.ToolCache
	Action          provider.PrivilegeAction
	Plan            provider.PrivilegePlan
	Command         PrivilegedToolCommand
	ApprovalMessage string
}

func (a *App) ToolPrivilegePlan(ctx context.Context, view *ToolView, action provider.PrivilegeAction) (provider.PrivilegePlan, error) {
	t := toolCacheFromView(view)
	if t == nil {
		return provider.PrivilegePlan{}, nil
	}
	cached := provider.PrivilegePlan{}
	if t.Privilege != "" {
		cached = provider.PrivilegePlan{
			Requirement: provider.PrivilegeRequirement(t.Privilege),
			Reason:      t.PrivilegeReason.String,
		}
	}
	providerName := t.Provider
	manager := ""
	if (action == provider.PrivilegeActionUninstall || action == provider.PrivilegeActionUpgrade) && t.InstalledWith != "" {
		providerName = t.InstalledWith
	} else if t.InstalledWith != "" && t.InstalledWith != t.Provider {
		manager = t.InstalledWith
	}
	if cached.RequiresPrivilege() && providerName != "brew" {
		return cached, nil
	}
	prov, opProvider, _, ok := a.lifecycleProvider(providerName, manager)
	if !ok {
		return cached, nil
	}
	tool := provider.Tool{Name: t.Name, Provider: opProvider, Package: t.Package}
	if tool.Package == "" {
		tool.Package = t.Name
	}
	planner, ok := prov.(provider.PrivilegePlanner)
	if !ok {
		return cached, nil
	}
	plan, err := planner.PrivilegePlan(ctx, action, tool)
	if err != nil {
		if cached.RequiresPrivilege() {
			return cached, nil
		}
		return provider.PrivilegePlan{}, err
	}
	if plan.RequiresPrivilege() {
		return plan, nil
	}
	return cached, nil
}

func (a *App) PrivilegeQueuePlan(ctx context.Context, req PrivilegeQueueRequest) (PrivilegeQueuePlan, error) {
	out := PrivilegeQueuePlan{}
	if a == nil || len(req.RowErrors) == 0 || len(req.Actions) == 0 {
		return out, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for _, t := range privilegeQueueCandidates(req.RowErrors, toolCachesFromView(req.VisibleTools), toolCachesFromView(req.Tools)) {
		key := privilegeQueueToolKey(t.Name, t.Provider)
		action, ok := req.Actions[key]
		if !ok || !privilegeQueueActionValid(action) || skipPrivilegeQueueCandidate(t, action) {
			continue
		}
		if action == provider.PrivilegeActionUninstall {
			if err := a.ValidateToolDelete(t.Name); err != nil {
				out.setRowError(key, err.Error())
				continue
			}
		}
		message := req.RowErrors[key]
		rowPlan, classified := privilegePlanFromRowError(message)
		if !classified && !cachedToolRequiresPrivilege(t) && !isPrivilegedInstallFailure(message) {
			continue
		}
		plan := cachedPrivilegePlan(t)
		if shouldPreferRowPrivilegePlan(plan, rowPlan) && !isGenericPrivilegeReason(rowPlan.Reason) {
			plan = rowPlan
		} else {
			providerPlan, err := a.ToolPrivilegePlan(ctx, toolViewFromCache(t), action)
			if err != nil || !providerPlan.RequiresPrivilege() {
				plan = rowPlan
			} else if shouldPreferRowPrivilegePlan(providerPlan, rowPlan) {
				plan = rowPlan
			} else {
				plan = providerPlan
			}
		}
		if !plan.RequiresPrivilege() {
			continue
		}
		if plan.Reason == "" {
			plan.Reason = "package manager needs sudo/root access"
		}
		command, ok := a.PrivilegedToolCommand(ctx, toolViewFromCache(t), action, plan)
		if !ok {
			out.setRowError(key, "requires admin terminal; unsupported provider command")
			continue
		}
		out.Items = append(out.Items, PrivilegeQueueItem{
			Key:             key,
			Tool:            t,
			Action:          action,
			Plan:            plan,
			Command:         command,
			ApprovalMessage: privilegedApprovalRowMessage(action),
		})
	}
	return out, nil
}

func (p *PrivilegeQueuePlan) setRowError(key, message string) {
	if key == "" || message == "" {
		return
	}
	if p.RowErrors == nil {
		p.RowErrors = make(map[string]string)
	}
	p.RowErrors[key] = message
}

func privilegeQueueToolKey(name, providerName string) string {
	return name + "\x00" + providerName
}

func privilegeQueueCandidates(rowErrors map[string]string, visibleTools, tools []*database.ToolCache) []*database.ToolCache {
	candidates := make([]*database.ToolCache, 0, len(rowErrors))
	seen := make(map[string]bool, len(rowErrors))
	add := func(items []*database.ToolCache) {
		for _, t := range items {
			if t == nil {
				continue
			}
			key := privilegeQueueToolKey(t.Name, t.Provider)
			if seen[key] {
				continue
			}
			if _, ok := rowErrors[key]; !ok {
				continue
			}
			seen[key] = true
			candidates = append(candidates, t)
		}
	}
	add(visibleTools)
	add(tools)
	for key := range rowErrors {
		if seen[key] {
			continue
		}
		name, providerName, ok := strings.Cut(key, "\x00")
		if !ok || name == "" || providerName == "" {
			continue
		}
		seen[key] = true
		candidates = append(candidates, &database.ToolCache{Name: name, Provider: providerName, Package: name})
	}
	return candidates
}

func privilegeQueueActionValid(action provider.PrivilegeAction) bool {
	switch action {
	case provider.PrivilegeActionInstall, provider.PrivilegeActionUninstall, provider.PrivilegeActionUpgrade:
		return true
	default:
		return false
	}
}

func skipPrivilegeQueueCandidate(t *database.ToolCache, action provider.PrivilegeAction) bool {
	if t == nil {
		return true
	}
	switch action {
	case provider.PrivilegeActionInstall:
		return t.Installed
	case provider.PrivilegeActionUninstall, provider.PrivilegeActionUpgrade:
		return false
	default:
		return true
	}
}

func privilegedApprovalRowMessage(action provider.PrivilegeAction) string {
	switch action {
	case provider.PrivilegeActionUpgrade:
		return "admin approval required to upgrade"
	case provider.PrivilegeActionUninstall:
		return "admin approval required to uninstall"
	default:
		return "admin approval required to install"
	}
}

func PrivilegeFailureRequiresApproval(message string) bool {
	return isPrivilegedInstallFailure(message)
}

func isPrivilegedInstallFailure(message string) bool {
	plan, ok := privilegePlanFromRowError(message)
	return ok && plan.RequiresPrivilege()
}

// privilegePlanFromRowError is the single classifier for whether a stored or
// serialized error message indicates a privilege requirement. It is a superset
// of every prior ad-hoc keyword check: the "requires X:" wire prefix (with its
// specific reason), the package-manager markers, provider.ClassifyPrivilegeError
// (raw external stderr), and the bare "requires sudo/root/admin"/"administrator"
// wire phrases. Callers must route through this rather than re-matching keywords,
// so the lists cannot drift and a dropped phrase can't silently skip a sudo
// prompt.
func privilegePlanFromRowError(message string) (provider.PrivilegePlan, bool) {
	message = strings.TrimSpace(message)
	if reason := privilegeReasonFromRowError(message); reason != "" {
		return provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: reason}, true
	}
	if plan, ok := provider.ClassifyPrivilegeError(errors.New(message)); ok {
		return plan, true
	}
	// Bare omni wire phrases carrying no ":" reason suffix (e.g. an event message
	// "install requires root"). ClassifyPrivilegeError targets raw package-manager
	// stderr and misses these, so catch them here.
	lower := strings.ToLower(message)
	for _, phrase := range []string{"requires sudo", "requires root", "requires admin", "administrator"} {
		if strings.Contains(lower, phrase) {
			return provider.PrivilegePlan{Requirement: provider.PrivilegeRequired, Reason: "package manager needs privileged access"}, true
		}
	}
	return provider.PrivilegePlan{}, false
}

func shouldPreferRowPrivilegePlan(plan, rowPlan provider.PrivilegePlan) bool {
	if !rowPlan.RequiresPrivilege() || rowPlan.Reason == "" {
		return false
	}
	if !plan.RequiresPrivilege() || plan.Reason == "" {
		return true
	}
	return isGenericPrivilegeReason(plan.Reason) && !isGenericPrivilegeReason(rowPlan.Reason)
}

func isGenericPrivilegeReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	return reason == "" ||
		reason == "package manager needs sudo/root access" ||
		reason == "package manager needs privileged access" ||
		reason == "package manager needs administrator access"
}

func privilegeReasonFromRowError(message string) string {
	lower := strings.ToLower(message)
	for _, prefix := range []string{
		"requires sudo:",
		"requires root:",
		"requires admin:",
		"requires administrator:",
	} {
		if strings.HasPrefix(lower, prefix) {
			return strings.TrimSpace(message[len(prefix):])
		}
	}
	for _, marker := range []string{
		"brew cask ",
		"apt install ",
		"apk add ",
		"dnf install ",
		"pacman install ",
		"zypper install ",
	} {
		if idx := strings.Index(lower, marker); idx >= 0 {
			return strings.TrimSpace(message[idx:])
		}
	}
	return ""
}

func cachedToolRequiresPrivilege(t *database.ToolCache) bool {
	return cachedPrivilegePlan(t).RequiresPrivilege()
}

func cachedPrivilegePlan(t *database.ToolCache) provider.PrivilegePlan {
	if t == nil || t.Privilege == "" {
		return provider.PrivilegePlan{}
	}
	return provider.PrivilegePlan{
		Requirement: provider.PrivilegeRequirement(t.Privilege),
		Reason:      t.PrivilegeReason.String,
	}
}

func (a *App) recordPrivilegeError(ctx context.Context, name, prov, pkg string, err error) {
	plan, ok := provider.ClassifyPrivilegeError(err)
	if !ok || !plan.RequiresPrivilege() {
		return
	}
	if pkg == "" {
		pkg = name
	}
	db := a.readDB()
	if db == nil {
		return // DB not initialised; skip best-effort privilege recording
	}
	if err := db.MarkPrivilegeRequired(context.WithoutCancel(ctx), name, prov, pkg, string(plan.Requirement), plan.Reason); err != nil {
		fmt.Fprintf(os.Stderr, "warning: omni: record privilege for %s: %v\n", name, err)
	}
}

func (a *App) MarkToolPrivilegeRequired(ctx context.Context, name, prov, pkg, requirement, reason string) error {
	if pkg == "" {
		pkg = name
	}
	return a.readDB().MarkPrivilegeRequired(ctx, name, prov, pkg, requirement, reason)
}

func (a *App) PrivilegedToolCommand(ctx context.Context, view *ToolView, action provider.PrivilegeAction, plan provider.PrivilegePlan) (PrivilegedToolCommand, bool) {
	t := toolCacheFromView(view)
	if a == nil || a.registry == nil || t == nil || !plan.RequiresPrivilege() {
		return PrivilegedToolCommand{}, false
	}
	concrete := a.privilegedToolCommandProvider(ctx, t, action, plan)
	if concrete == "" {
		return PrivilegedToolCommand{}, false
	}
	if concrete == "brew" && !brewCaskMayPromptForPassword(plan) {
		return PrivilegedToolCommand{}, false
	}
	prov, ok := a.registry.Get(concrete)
	if !ok {
		return PrivilegedToolCommand{}, false
	}
	commander, ok := prov.(provider.PrivilegeCommandPlanner)
	if !ok {
		return PrivilegedToolCommand{}, false
	}
	pkg := effectiveToolPackage(t)
	rawCmd, rawArgs, ok := commander.PrivilegeCommand(action, provider.Tool{
		Name:     t.Name,
		Provider: concrete,
		Package:  pkg,
		Options:  cloneOptionMap(t.Options),
	})
	if !ok || rawCmd == "" {
		return PrivilegedToolCommand{}, false
	}
	command, args := rawCmd, append([]string(nil), rawArgs...)
	if concrete != "brew" {
		// The package name and option values originate from settings.json, which
		// may be git-synced from a less-trusted source. Refuse to build a sudo
		// escalation when either looks like an injected flag rather than a
		// package identifier — a legitimate package/option value never leads
		// with "-".
		if configTokenLooksLikeFlag(pkg, t.Options) {
			return PrivilegedToolCommand{}, false
		}
		privCmd, privArgs, ok := interactivePrivilegedCommand(rawCmd, rawArgs...)
		if !ok {
			// Not an allow-listed package manager — refuse to escalate.
			return PrivilegedToolCommand{}, false
		}
		command, args = privCmd, privArgs
	}
	installedWith := t.InstalledWith
	if installedWith == "" && provider.BuiltinIsEcosystem(t.Provider) && concrete != t.Provider {
		installedWith = concrete
	}
	return PrivilegedToolCommand{
		Action:        action,
		Name:          t.Name,
		ProviderName:  t.Provider,
		Package:       pkg,
		InstalledWith: installedWith,
		Options:       cloneOptionMap(t.Options),
		Reason:        plan.Reason,
		Command:       command,
		Args:          args,
		Display:       shellJoin(append([]string{command}, args...)),
	}, true
}

func (a *App) privilegedToolCommandProvider(ctx context.Context, t *database.ToolCache, action provider.PrivilegeAction, plan provider.PrivilegePlan) string {
	if t.InstalledWith != "" && t.InstalledWith != t.Provider {
		return t.InstalledWith
	}
	if (action == provider.PrivilegeActionUninstall || action == provider.PrivilegeActionUpgrade) && t.InstalledWith != "" {
		return t.InstalledWith
	}
	if t.Provider == provider.EcosystemSystem {
		if concrete := a.EffectiveSystemManager(ctx); concrete != "" {
			return concrete
		}
		return provider.SystemPrivilegePlanProvider(plan)
	}
	return t.Provider
}

func brewCaskMayPromptForPassword(plan provider.PrivilegePlan) bool {
	reason := strings.ToLower(plan.Reason)
	return strings.Contains(reason, "brew cask") &&
		(strings.Contains(reason, "pkgutil") ||
			strings.Contains(reason, "installer") ||
			strings.Contains(reason, "launchctl"))
}

// configTokenLooksLikeFlag reports whether an untrusted, config-sourced token
// (a package name or option value from settings.json) could be misparsed as a
// command-line flag when spliced into a privileged invocation. Legitimate
// package names and option values never begin with "-".
func configTokenLooksLikeFlag(pkg string, options map[string]string) bool {
	if strings.HasPrefix(strings.TrimSpace(pkg), "-") {
		return true
	}
	for _, v := range options {
		if strings.HasPrefix(strings.TrimSpace(v), "-") {
			return true
		}
	}
	return false
}

// privilegedCommandAllowlist is the set of package-manager binaries omni will
// escalate with sudo. Restricting the escalation to known managers is defense in
// depth against the thin trust boundary around a git-synced settings.json: the
// command reaching this path is built by a provider, but a future or compromised
// provider returning an unexpected binary must never be silently run as root.
// (brew never reaches here — it is handled without sudo by the caller.)
var privilegedCommandAllowlist = map[string]bool{
	"apk":     true,
	"apt-get": true,
	"dnf":     true,
	"pacman":  true,
	"zypper":  true,
}

// interactivePrivilegedCommand wraps cmd for privileged execution, returning
// ok=false when cmd is not an allow-listed package manager. Callers must treat
// ok=false as "refuse to run" rather than falling back to the raw command.
func interactivePrivilegedCommand(cmd string, args ...string) (string, []string, bool) {
	if !privilegedCommandAllowlist[cmd] {
		return "", nil, false
	}
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		return cmd, args, true
	}
	return "sudo", append([]string{cmd}, args...), true
}

func effectiveToolPackage(t *database.ToolCache) string {
	if t == nil {
		return ""
	}
	if t.Package != "" {
		return t.Package
	}
	return t.Name
}

type CompleteExternalToolActionOptions struct {
	Action        provider.PrivilegeAction
	Name          string
	ProviderName  string
	Package       string
	InstalledWith string
	Options       map[string]string
	AddToConfig   bool
	GroupName     string
	AssignHosts   []string
}

type CompleteExternalToolActionStateResult struct {
	Tools      []*ToolView
	GroupState *ToolGroupState
}

// CompleteExternalToolAction reconciles app state after a lifecycle action was
// run outside the normal provider executor, for example by a TUI-owned terminal
// session that can answer sudo prompts.
func (a *App) CompleteExternalToolAction(ctx context.Context, action provider.PrivilegeAction, name, providerName, pkg, installedWith string) error {
	return a.completeExternalToolAction(ctx, CompleteExternalToolActionOptions{
		Action:        action,
		Name:          name,
		ProviderName:  providerName,
		Package:       pkg,
		InstalledWith: installedWith,
	})
}

func (a *App) completeExternalToolAction(ctx context.Context, opts CompleteExternalToolActionOptions) error {
	name := opts.Name
	pkg := opts.Package
	providerName := opts.ProviderName
	installedWith := opts.InstalledWith
	if pkg == "" {
		pkg = name
	}
	switch opts.Action {
	case provider.PrivilegeActionInstall, provider.PrivilegeActionUpgrade:
		return a.completeExternalInstalledToolAction(ctx, opts.Action, name, providerName, pkg, installedWith, opts.Options)
	case provider.PrivilegeActionUninstall:
		if err := a.rejectProviderToolDelete(name); err != nil {
			return err
		}
		if err := a.removeToolFromConfig(name, providerName); err != nil {
			return err
		}
		if err := a.readDB().Delete(ctx, name, providerName, pkg); err != nil {
			return fmt.Errorf("delete cache after external uninstall for %s/%s: %w", providerName, name, err)
		}
		return nil
	default:
		return fmt.Errorf("unsupported external tool action %q", opts.Action)
	}
}

func (a *App) CompleteExternalToolActionWithState(ctx context.Context, opts CompleteExternalToolActionOptions) (*CompleteExternalToolActionStateResult, error) {
	if err := a.completeExternalToolAction(ctx, opts); err != nil {
		return nil, err
	}
	result := &CompleteExternalToolActionStateResult{}
	if opts.AddToConfig && opts.Action == provider.PrivilegeActionInstall {
		state, err := a.addCompletedExternalInstallToConfig(ctx, opts)
		if state != nil {
			result.Tools = state.Tools
			result.GroupState = state.State
		}
		return result, err
	}
	if opts.Action == provider.PrivilegeActionUninstall {
		state, err := a.toolGroupMutationState(ctx)
		if state != nil {
			result.Tools = state.Tools
			result.GroupState = state.State
		}
		return result, err
	}
	tools, err := a.ListToolsForView(ctx, "")
	if err != nil {
		return result, fmt.Errorf("list tools: %w", err)
	}
	result.Tools = tools
	return result, nil
}

func (a *App) addCompletedExternalInstallToConfig(ctx context.Context, opts CompleteExternalToolActionOptions) (*ToolGroupMutationState, error) {
	name := opts.Name
	if name == "" {
		name = opts.Package
	}
	pkg := opts.Package
	if pkg == "" {
		pkg = name
	}
	if err := a.Add(ctx, opts.ProviderName, pkg, name, opts.GroupName, opts.InstalledWith, optionMapArg(opts.Options)...); err != nil {
		state, stateErr := a.toolGroupMutationState(ctx)
		err = fmt.Errorf("installed %s but config save failed: %w", name, err)
		if stateErr != nil {
			return nil, errors.Join(err, stateErr)
		}
		return state, err
	}
	hostErr := a.assignToolGroupHosts(opts.GroupName, opts.AssignHosts)
	state, stateErr := a.toolGroupMutationState(ctx)
	if stateErr != nil {
		return nil, errors.Join(hostErr, stateErr)
	}
	if hostErr != nil {
		return state, fmt.Errorf("installed %s and added to config but host update failed: %w", name, hostErr)
	}
	return state, nil
}

func (a *App) completeExternalInstalledToolAction(ctx context.Context, action provider.PrivilegeAction, name, providerName, pkg, installedWith string, options map[string]string) error {
	prov, opProvider, manager, ok := a.lifecycleProvider(providerName, installedWith)
	if !ok {
		return fmt.Errorf("unknown provider %q", providerName)
	}
	tool := provider.Tool{Name: name, Provider: opProvider, Package: pkg, Options: cloneOptionMap(options)}
	installed, ver, err := isInstalledTool(ctx, prov, tool, manager)
	if err != nil {
		return fmt.Errorf("verify %s after external %s: %w", name, externalToolActionVerb(action), err)
	}
	if !installed {
		return fmt.Errorf("verify %s after external %s: not installed", name, externalToolActionVerb(action))
	}
	cacheInstalledWith := installedWithForOperation(ctx, prov, opProvider, installedWith)
	if cacheInstalledWith == "" {
		cacheInstalledWith = installedWithForLifecycle(opProvider, manager)
	}
	if err := a.readDB().Upsert(ctx, &database.ToolCache{
		Name:          name,
		Provider:      providerName,
		Package:       pkg,
		Installed:     true,
		InstalledWith: cacheInstalledWith,
		Version:       sql.NullString{String: ver, Valid: ver != ""},
		LastChecked:   time.Now(),
	}); err != nil {
		return fmt.Errorf("update cache after external %s for %s/%s: %w", externalToolActionVerb(action), providerName, name, err)
	}
	if action == provider.PrivilegeActionUpgrade {
		if err := a.readDB().UpdateOutdated(ctx, name, providerName, pkg, false, ""); err != nil {
			return fmt.Errorf("clear outdated after external upgrade for %s/%s: %w", providerName, name, err)
		}
	}
	return nil
}

func externalToolActionVerb(action provider.PrivilegeAction) string {
	switch action {
	case provider.PrivilegeActionInstall:
		return "install"
	case provider.PrivilegeActionUninstall:
		return "uninstall"
	case provider.PrivilegeActionUpgrade:
		return "upgrade"
	default:
		return string(action)
	}
}

func privilegeReason(plan provider.PrivilegePlan) string {
	if plan.Reason != "" {
		return plan.Reason
	}
	return "package manager needs privileged access"
}

func shellJoin(parts []string) string {
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = shellQuote(part)
	}
	return strings.Join(quoted, " ")
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	if strings.IndexFunc(s, func(r rune) bool {
		return !(r == '-' || r == '_' || r == '.' || r == '/' || r == ':' || r == '+' || r == '@' || r == '=' || r == ',' || r == '%' || r >= '0' && r <= '9' || r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z')
	}) == -1 {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
