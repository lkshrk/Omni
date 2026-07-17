// Package actions defines omni's user-visible capabilities independently of
// the surfaces that expose them.
package actions

// ID is the stable semantic identifier for a user-visible action.
type ID string

const (
	Reconcile                      ID = "reconcile"
	ToolSync                       ID = "tools.sync"
	ToolInstall                    ID = "tools.install"
	ToolDelete                     ID = "tools.delete"
	ToolUpdate                     ID = "tools.update"
	ToolUpdateAll                  ID = "tools.update_all"
	ToolSyncAll                    ID = "tools.sync_all"
	ToolClaim                      ID = "tools.claim"
	ToolIgnore                     ID = "tools.ignore"
	ToolChangeGroup                ID = "tools.change_group"
	ToolPinProvider                ID = "tools.pin_provider"
	ToolReinstallDefault           ID = "tools.reinstall_default"
	ToolMigrateNvm                 ID = "tools.migrate_nvm"
	ToolRefresh                    ID = "tools.refresh"
	ToolConsolidate                ID = "tools.consolidate"
	ToolSetSpec                    ID = "tools.set_spec"
	ToolFallback                   ID = "tools.fallback"
	ToolDeleteSpec                 ID = "tools.delete_spec"
	ToolNormalizeProviderOverrides ID = "tools.normalize_provider_overrides"
	ToolHealBrewTaps               ID = "tools.heal_brew_taps"
	ToolImport                     ID = "tools.import"
	ToolSwitchProvider             ID = "tools.switch_provider"
	DotsSync                       ID = "dots.sync"
	DotsRefresh                    ID = "dots.refresh"
	DotsAdd                        ID = "dots.add"
	DotsEditGroups                 ID = "dots.edit_groups"
	DotsVariant                    ID = "dots.variant"
	DotsDelete                     ID = "dots.delete"
	DotsResolveUseRepo             ID = "dots.resolve_use_repo"
	DotsResolveUseLocal            ID = "dots.resolve_use_local"
	DotsResolveAllUseRepo          ID = "dots.resolve_all_use_repo"
	DotsResolveAllUseLocal         ID = "dots.resolve_all_use_local"
	DotsIgnore                     ID = "dots.ignore"
	DotsEnable                     ID = "dots.enable"
	DotsDisable                    ID = "dots.disable"
	DotsPull                       ID = "dots.pull"
	DotsCommit                     ID = "dots.commit"
	DotsPush                       ID = "dots.push"
	DotsReminder                   ID = "dots.reminder"
	DotsReminderCheck              ID = "dots.reminder.check"
	DotsReminderRun                ID = "dots.reminder.run"
	DotsReminderStatus             ID = "dots.reminder.status"
	DotsWatch                      ID = "dots.watch"
	DotsWatchRun                   ID = "dots.watch.run"
	DotsWatchStatus                ID = "dots.watch.status"
	DotsServicesStatus             ID = "dots.services.status"
	DotsHistory                    ID = "dots.history"
	GroupCreate                    ID = "groups.create"
	GroupRename                    ID = "groups.rename"
	GroupDelete                    ID = "groups.delete"
	GroupEditTools                 ID = "groups.edit_tools"
	GroupEditDots                  ID = "groups.edit_dots"
	HostCreate                     ID = "hosts.create"
	HostCopy                       ID = "hosts.copy"
	HostDelete                     ID = "hosts.delete"
	HostEditGroups                 ID = "hosts.edit_groups"
	SettingsSet                    ID = "settings.set"
	SettingsProvider               ID = "settings.provider"
	SettingsReset                  ID = "settings.reset"
	SettingsResetCache             ID = "settings.reset_cache"
	SettingsMigrateHostOverrides   ID = "settings.migrate_host_overrides"
	SettingsExtract                ID = "settings.extract"
	SetupInit                      ID = "setup.init"
	Doctor                         ID = "doctor"
)

// Scope describes whether an action targets one row, a whole tab/domain, or
// another app-wide context.
type Scope string

const (
	ScopeRow    Scope = "row"
	ScopeGlobal Scope = "global"
)

// Requirement describes explicit user input an action needs under the logical
// tool model. CLI adapters must provide these through flags/args or prompts;
// TUI adapters must provide them through pickers/popups/inputs.
type Requirement string

const (
	RequiresToolName          Requirement = "tool_name"
	RequiresGroupAssignment   Requirement = "group_assignment"
	RequiresEcosystemProvider Requirement = "ecosystem_provider"
	RequiresInstallWith       Requirement = "install_with"
	RequiresIgnoreScope       Requirement = "ignore_scope"
	RequiresProviderScope     Requirement = "provider_scope"
	RequiresDeleteFallback    Requirement = "delete_fallback"
)

// TUIBinding records the default TUI exposure for an action without depending
// on Bubble Tea key types.
type TUIBinding struct {
	KeyMapField        string
	DefaultKey         string
	Label              string
	Description        string
	ConfirmDescription string
}

// CLIBinding records the CLI exposure for an action without depending on Cobra.
type CLIBinding struct {
	Command []string
	Flags   []string
}

// PaletteBinding records how an action appears in the TUI command palette.
type PaletteBinding struct {
	Command           []string
	Description       string
	DescriptionFormat string
}

// Action is the shared capability metadata used by CLI, TUI, help, and parity
// tests. Execution still lives in the app layer.
type Action struct {
	ID                 ID
	Domain             string
	Scope              Scope
	Label              string
	Description        string
	LongDescription    string
	Mutates            bool
	RequiresConfirm    bool
	ConfirmDescription string
	Requirements       []Requirement
	TUI                *TUIBinding
	CLI                []CLIBinding
	Palette            *PaletteBinding
	PaletteEligible    bool
	CLIOnlyReason      string
}

// Lifecycle is the canonical catalog for app-wide lifecycle actions.
var Lifecycle = []Action{
	{
		ID:                 Reconcile,
		Domain:             "lifecycle",
		Scope:              ScopeGlobal,
		Label:              "reconcile",
		Description:        "Sync tools, upgrade tools, sync dotfiles, and commit dotfile changes.",
		LongDescription:    "Run the host lifecycle in one command: add discovered tools and install missing configured tools, upgrade outdated tools, repair managed dotfile symlinks, and commit pending dotfile repository changes.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "Reconcile this host: sync tools, upgrade tools, sync dotfiles, and commit dotfile changes?",
		TUI:                &TUIBinding{KeyMapField: "Reconcile", DefaultKey: "A", Label: "reconcile all", Description: "Review and run all safe host lifecycle fixes.", ConfirmDescription: "reconcile all"},
		CLI:                []CLIBinding{{Command: []string{"reconcile"}, Flags: []string{"--message", "--skip-privileged", "--force"}}},
		Palette:            &PaletteBinding{Command: []string{"reconcile"}, Description: "sync, upgrade, repair dotfiles, and commit changes"},
		PaletteEligible:    true,
	},
}

// Requires reports whether this action declares requirement.
func (a Action) Requires(requirement Requirement) bool {
	for _, r := range a.Requirements {
		if r == requirement {
			return true
		}
	}
	return false
}

// Tools is the canonical catalog for tool actions.
var Tools = []Action{
	{
		ID:              ToolSync,
		Domain:          "tools",
		Scope:           ScopeGlobal,
		Label:           "sync",
		Description:     "Install missing tools from config.",
		LongDescription: "Install configured tools that are missing locally.",
		Mutates:         true,
		CLI:             []CLIBinding{{Command: []string{"tools", "sync"}, Flags: []string{"--dry-run", "--prune", "--provider", "--group", "--retry-failed", "--allow-weak"}}},
		Palette:         &PaletteBinding{Command: []string{"tools", "sync"}, Description: "sync tools from config"},
		PaletteEligible: true,
	},
	{
		ID:              ToolInstall,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           "install",
		Description:     "Install one missing tool.",
		LongDescription: "Install one missing logical tool using its resolved package and install-with target. Configured tools stay in the config.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName},
		TUI:             &TUIBinding{KeyMapField: "Install", DefaultKey: "i", Label: "install", Description: "Install the selected missing tool."},
		CLI:             []CLIBinding{{Command: []string{"tools", "install"}, Flags: []string{"--provider", "--group", "--force", "--allow-weak"}}},
	},
	{
		ID:                 ToolDelete,
		Domain:             "tools",
		Scope:              ScopeRow,
		Label:              LabelDelete,
		Description:        "Delete one tool and its config entry.",
		LongDescription:    "Delete removes the selected logical tool from config. If it is installed locally, omni also uninstalls it with the resolved provider first.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: ConfirmDelete,
		Requirements:       []Requirement{RequiresToolName},
		TUI:                &TUIBinding{KeyMapField: "Delete", DefaultKey: "d", Label: LabelDelete, Description: "Delete the selected tool and config entry.", ConfirmDescription: ConfirmDelete},
		CLI:                []CLIBinding{{Command: []string{"tools", "delete"}, Flags: []string{"--provider"}}},
	},
	{
		ID:              ToolUpdate,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           "upgrade",
		Description:     "Upgrade one outdated tool.",
		LongDescription: "Upgrade one outdated installed tool using the logical provider and installed-with owner recorded in cache.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName},
		TUI:             &TUIBinding{KeyMapField: "Upgrade", DefaultKey: "u", Label: "upgrade", Description: "Upgrade the selected outdated tool."},
		CLI:             []CLIBinding{{Command: []string{"tools", "upgrade"}, Flags: []string{"--force"}}},
	},
	{
		ID:              ToolUpdateAll,
		Domain:          "tools",
		Scope:           ScopeGlobal,
		Label:           "upgrade all",
		Description:     "Upgrade every outdated tool.",
		LongDescription: "Upgrade every outdated installed tool currently tracked in the local cache.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "UpgradeAll", DefaultKey: "U", Label: "upgrade all", Description: "Upgrade every visible outdated tool."},
		CLI:             []CLIBinding{{Command: []string{"tools", "upgrade"}, Flags: []string{"--all", "--force"}}},
	},
	{
		ID:                 ToolSyncAll,
		Domain:             "tools",
		Scope:              ScopeGlobal,
		Label:              "sync all",
		Description:        "Add discovered installed tools to config and install configured missing tools.",
		LongDescription:    "Add locally discovered installed tools to this machine's hostname group, then install configured tools that are missing locally.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: ConfirmSyncAll,
		TUI:                &TUIBinding{KeyMapField: "SyncAll", DefaultKey: "S", Label: "sync all", Description: "Add discovered tools and install missing tools.", ConfirmDescription: ConfirmSyncAll},
		CLI:                []CLIBinding{{Command: []string{"tools", "sync"}, Flags: []string{"--all", "--allow-weak"}}},
	},
	{
		ID:              ToolClaim,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           "add to config",
		Description:     "Add a discovered installed tool to config.",
		LongDescription: "Add a locally discovered installed tool to a config group so it becomes managed by omni.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresGroupAssignment, RequiresEcosystemProvider},
		TUI:             &TUIBinding{KeyMapField: "Claim", DefaultKey: "c", Label: "add to config", Description: "Add the selected discovered tool to config."},
		CLI:             []CLIBinding{{Command: []string{"tools", "add"}, Flags: []string{"--name", "--group", "--provider", "--install-with"}}},
	},
	{
		ID:              ToolIgnore,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           LabelIgnore,
		Description:     "Toggle whether a scope skips this logical tool.",
		LongDescription: "Toggle a tool-level ignore entry. Ignored tools remain in config but are skipped during sync.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresIgnoreScope},
		TUI:             &TUIBinding{KeyMapField: "Ignore", DefaultKey: "x", Label: LabelIgnore, Description: "Ignore or unignore the selected tool."},
		CLI: []CLIBinding{
			{Command: []string{"tools", "ignore"}},
			{Command: []string{"tools", "unignore"}},
			{Command: []string{"groups", "ignore-tool"}},
			{Command: []string{"groups", "unignore-tool"}},
		},
	},
	{
		ID:              ToolChangeGroup,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           LabelMoveGroup,
		Description:     "Move a logical tool to another group.",
		LongDescription: "Move a logical tool's single group assignment without changing its installed state or logical tool spec.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresGroupAssignment},
		TUI:             &TUIBinding{KeyMapField: "MoveGroup", DefaultKey: "g", Label: LabelMoveGroup, Description: "Move the selected tool to another group."},
		CLI: []CLIBinding{
			{Command: []string{"groups", "move-tool"}},
			{Command: []string{"groups", "remove-tool"}},
		},
	},
	{
		ID:              ToolPinProvider,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           "pin provider",
		Description:     "Set or remove a provider override for a selected scope.",
		LongDescription: "Set a host override, provider family host manager, or explicit logical tool install-with override, or remove an existing override when explicitly chosen.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresProviderScope},
		TUI:             &TUIBinding{KeyMapField: "PinProvider", DefaultKey: "p", Label: "pin provider", Description: "Pin or remove provider settings for the selected tool."},
		CLI: []CLIBinding{
			{Command: []string{"tools", "set"}, Flags: []string{"--provider", "--install-with", "--global", "--host", "--quarantine"}},
		},
	},
	{
		ID:                 ToolReinstallDefault,
		Domain:             "tools",
		Scope:              ScopeRow,
		Label:              "reinstall",
		Description:        "Reinstall a wrong-provider tool with its preferred provider.",
		LongDescription:    "Uninstall the current provider installation, then reinstall the tool with the preferred provider family default.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: ConfirmReinstall,
		Requirements:       []Requirement{RequiresToolName},
		TUI:                &TUIBinding{KeyMapField: "Install", DefaultKey: "i", Label: "reinstall", Description: "Uninstall the current provider and reinstall with the preferred provider.", ConfirmDescription: ConfirmReinstall},
		CLI:                []CLIBinding{{Command: []string{"tools", "reinstall"}, Flags: []string{"--reinstall-default", "--provider"}}},
	},
	{
		ID:                 ToolMigrateNvm,
		Domain:             "tools",
		Scope:              ScopeRow,
		Label:              "migrate nvm-managed",
		Description:        "Move nvm-managed system-provider tools to the node package manager.",
		LongDescription:    "Migrate configured system-provider tools whose active binaries resolve via nvm onto the effective node package manager, or remove the Node runtime from omni config when nvm owns it.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "Migrate nvm-managed system-provider tool(s)?",
		Requirements:       []Requirement{RequiresToolName},
		TUI:                &TUIBinding{KeyMapField: "MigrateProvider", DefaultKey: "r", Label: "migrate nvm-managed", Description: "Move the selected nvm-managed tool off the system provider.", ConfirmDescription: "Migrate nvm-managed tool off system provider?"},
		CLI: []CLIBinding{
			{Command: []string{"tools", "migrate-nvm"}, Flags: []string{"--all"}},
		},
		Palette:         &PaletteBinding{Command: []string{"tools", "migrate-nvm"}, Description: "migrate nvm-managed tools to node manager", DescriptionFormat: "migrate nvm-managed %s"},
		PaletteEligible: true,
	},
	{
		ID:              ToolRefresh,
		Domain:          "tools",
		Scope:           ScopeGlobal,
		Label:           "refresh",
		Description:     "Refresh cached tool state and metadata.",
		LongDescription: "Refresh omni's local cache of installed, outdated, discovered, and description metadata without installing or uninstalling packages.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Refresh", DefaultKey: "R", Label: "refresh", Description: "Rescan installed, outdated, discovered, and description state."},
		CLI:             []CLIBinding{{Command: []string{"tools", "refresh"}}},
	},
	{
		ID:              ToolConsolidate,
		Domain:          "tools",
		Scope:           ScopeGlobal,
		Label:           "consolidate",
		Description:     "Move an ecosystem to one manager.",
		LongDescription: "Consolidate all tools in an ecosystem to the selected manager.",
		Mutates:         true,
		CLI:             []CLIBinding{{Command: []string{"tools", "consolidate"}, Flags: []string{"--dry-run", "--to"}}},
		Palette:         &PaletteBinding{Command: []string{"tools", "consolidate"}, Description: "use selected manager for ecosystem tools", DescriptionFormat: "use %s for %s tools"},
		PaletteEligible: true,
	},
	{
		ID:              ToolSetSpec,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           "set tool spec",
		Description:     "Create or update a logical tool spec.",
		LongDescription: "Create or update a logical tool spec, including concrete provider candidates, package name, quarantine, or legacy host override.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresEcosystemProvider},
		TUI:             &TUIBinding{KeyMapField: "PinProvider", DefaultKey: "p", Label: "set tool spec", Description: "Edit provider and package settings for the selected tool."},
		CLI:             []CLIBinding{{Command: []string{"tools", "set"}, Flags: []string{"--provider", "--package", "--install-with", "--host", "--global", "--quarantine"}}},
	},
	{
		ID:              ToolFallback,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           "set fallback",
		Description:     "Configure a fallback source for a logical system tool.",
		LongDescription: "Configure a fallback source for a logical system tool when the target machine's native package manager cannot provide it.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName},
		TUI:             &TUIBinding{KeyMapField: "Fallback", DefaultKey: "f", Label: "set fallback", Description: "Configure a fallback source for the selected system tool."},
		CLI:             []CLIBinding{{Command: []string{"tools", "fallback"}, Flags: []string{"--from-github"}}},
	},
	{
		ID:                 ToolDeleteSpec,
		Domain:             "tools",
		Scope:              ScopeRow,
		Label:              "delete tool spec",
		Description:        "Delete a logical tool spec and all memberships.",
		LongDescription:    "Delete a logical tool spec, all group memberships, group ignores, and tracked cache rows without relying on group membership state.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm delete tool spec",
		Requirements:       []Requirement{RequiresToolName},
		TUI:                &TUIBinding{KeyMapField: "Delete", DefaultKey: "d", Label: "delete tool spec", Description: "Delete the selected tool spec and memberships.", ConfirmDescription: "confirm delete tool spec"},
		CLI:                []CLIBinding{{Command: []string{"tools", "delete-spec"}}},
	},
	{
		ID:                 ToolNormalizeProviderOverrides,
		Domain:             "tools",
		Scope:              ScopeGlobal,
		Label:              "normalize provider overrides",
		Description:        "Remove no-op default provider overrides.",
		LongDescription:    "Remove install-with overrides that only restate the currently resolved ecosystem defaults, including global logical tool specs and this host's overrides.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm normalize provider overrides",
		CLI:                []CLIBinding{{Command: []string{"tools", "normalize"}, Flags: []string{"--default-overrides", "--dry-run"}}},
		CLIOnlyReason:      "TUI Settings danger-zone cleanup is being added separately; sync all already auto-cleans current-host no-op overrides.",
	},
	{
		ID:                 ToolHealBrewTaps,
		Domain:             "tools",
		Scope:              ScopeGlobal,
		Label:              "heal brew taps",
		Description:        "Backfill tap-qualified brew package names.",
		LongDescription:    "Rewrite brew tool entries stored with a bare package name to their tap-qualified form and record the tap, so Homebrew tap-trust no longer hides them from the scan.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm heal brew taps",
		CLI:                []CLIBinding{{Command: []string{"tools", "heal-taps"}, Flags: []string{"--dry-run"}}},
		CLIOnlyReason:      "Config-hygiene cleanup is CLI-only; runs ad hoc, not part of the TUI danger zone yet.",
	},
	{
		ID:              ToolImport,
		Domain:          "tools",
		Scope:           ScopeGlobal,
		Label:           "import installed tools",
		Description:     "Import installed tools into config.",
		LongDescription: "Discover locally installed tools and add them to config, optionally scoped by provider and destination group.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter", Label: "import installed tools", Description: "Import installed tools into the selected group."},
		CLI:             []CLIBinding{{Command: []string{"tools", "import"}, Flags: []string{"--provider", "--group", "--dry-run"}}},
	},
	{
		ID:              ToolSwitchProvider,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           "move provider",
		Description:     "Move one tool between explicit providers.",
		LongDescription: "Install one tool with an explicit target provider, remove the old provider installation best-effort, and rewrite config/cache ownership.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName},
		CLI:             []CLIBinding{{Command: []string{"tools", "reinstall"}, Flags: []string{"--from", "--to"}}},
		CLIOnlyReason:   "explicit --from/--to provider migration is a legacy CLI repair path; TUI exposes scoped provider pinning and reinstall-with-default instead",
	},
}

// Dots is the canonical catalog for dotfile actions.
var Dots = []Action{
	{
		ID:              DotsSync,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots sync",
		Description:     "Repair dotfile symlinks.",
		LongDescription: "Repair managed dotfile symlinks without pulling or pushing git changes.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Sync", DefaultKey: "s", Label: "sync dotfiles", Description: "Repair managed dotfile symlinks."},
		CLI:             []CLIBinding{{Command: []string{"dots", "sync"}, Flags: []string{"--dry-run", "--use-repo", "--use-local"}}},
		Palette:         &PaletteBinding{Command: []string{"dots", "sync"}, Description: "repair dotfile symlinks (no git)"},
		PaletteEligible: true,
	},
	{
		ID:              DotsRefresh,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "refresh",
		Description:     "Refresh dotfile status and discover candidates.",
		LongDescription: "Re-scan all dot entries, refresh git status, and discover untracked dotfile candidates without mutating config, the repo, or local files.",
		Mutates:         false,
		TUI:             &TUIBinding{KeyMapField: "DotRefresh", DefaultKey: "R", Label: "refresh", Description: "Refresh status and discover candidates."},
		CLI:             []CLIBinding{{Command: []string{"dots", "discover"}, Flags: []string{"--format"}}},
	},
	{
		ID:              DotsAdd,
		Domain:          "dots",
		Scope:           ScopeRow,
		Label:           LabelAdd,
		Description:     "Add a path to dotfile management.",
		LongDescription: "Add or adopt a config path into the dots repo and register it with an explicit group assignment.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresGroupAssignment},
		TUI:             &TUIBinding{KeyMapField: "DotAdd", DefaultKey: "a", Label: LabelAdd, Description: "Add the selected path to dotfile sync."},
		CLI:             []CLIBinding{{Command: []string{"dots", "add"}, Flags: []string{"--name", "--group", "--adopt", "--ignore", "--discovered"}}},
	},
	{
		ID:              DotsEditGroups,
		Domain:          "dots",
		Scope:           ScopeRow,
		Label:           LabelMoveGroup,
		Description:     "Move a dots entry to another group.",
		LongDescription: "Show or move a managed dots entry's single group assignment without changing its repo or local files.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresGroupAssignment},
		TUI:             &TUIBinding{KeyMapField: "MoveGroup", DefaultKey: "g", Label: LabelMoveGroup, Description: "Move the selected dotfile entry to another group."},
		CLI: []CLIBinding{
			{Command: []string{"dots", "groups"}, Flags: []string{"--move", "--remove"}},
			{Command: []string{"dots", "extract"}, Flags: []string{"--group", "--name"}},
		},
	},
	{
		ID:              DotsVariant,
		Domain:          "dots",
		Scope:           ScopeRow,
		Label:           "variant",
		Description:     "Use a host-specific package for one dots entry.",
		LongDescription: "Create or remove this host's package variant for a managed dots entry, then resync the selected entry.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName},
		TUI:             &TUIBinding{KeyMapField: "DotVariant", DefaultKey: "v", Label: "variant", Description: "Create or remove this host's package variant."},
		CLI: []CLIBinding{
			{Command: []string{"dots", "variant", "add"}, Flags: []string{"--host", "--package", "--sync"}},
			{Command: []string{"dots", "variant", "remove"}, Flags: []string{"--host"}},
		},
	},
	{
		ID:                 DotsDelete,
		Domain:             "dots",
		Scope:              ScopeRow,
		Label:              LabelDelete,
		Description:        "Delete one dots entry from management.",
		LongDescription:    "Delete a dots entry from config and remove its managed symlinks from this host.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: ConfirmDelete,
		Requirements:       []Requirement{RequiresToolName},
		TUI:                &TUIBinding{KeyMapField: "DotDelete", DefaultKey: "d", Label: LabelDelete, Description: "Delete the selected dotfile entry from sync.", ConfirmDescription: ConfirmDelete},
		CLI:                []CLIBinding{{Command: []string{"dots", "delete"}, Flags: []string{"--keep-local"}}},
	},
	{
		ID:                 DotsResolveUseRepo,
		Domain:             "dots",
		Scope:              ScopeRow,
		Label:              "use repo",
		Description:        "Resolve a dots conflict with the repo version.",
		LongDescription:    "Replace the local target for a conflicting dots entry with the repository version, backing up local content first.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm use repo",
		Requirements:       []Requirement{RequiresToolName},
		TUI:                &TUIBinding{KeyMapField: "DotUseRepo", DefaultKey: "u", Label: "use repo", Description: "Resolve the selected conflict with the repo version.", ConfirmDescription: "confirm use repo"},
		CLI:                []CLIBinding{{Command: []string{"dots", "resolve"}, Flags: []string{"--use-repo"}}},
	},
	{
		ID:                 DotsResolveUseLocal,
		Domain:             "dots",
		Scope:              ScopeRow,
		Label:              "use local",
		Description:        "Resolve a dots conflict with the local version.",
		LongDescription:    "Copy the local target for a conflicting dots entry into the repository, then relink the managed target.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm use local",
		Requirements:       []Requirement{RequiresToolName},
		TUI:                &TUIBinding{KeyMapField: "DotUseLocal", DefaultKey: "l", Label: "use local", Description: "Resolve the selected conflict with the local version.", ConfirmDescription: "confirm use local"},
		CLI:                []CLIBinding{{Command: []string{"dots", "resolve"}, Flags: []string{"--use-local"}}},
	},
	{
		ID:                 DotsResolveAllUseRepo,
		Domain:             "dots",
		Scope:              ScopeGlobal,
		Label:              "use repo (all)",
		Description:        "Force-resolve every dots conflict with the repo version.",
		LongDescription:    "Run a sync that relinks the repo version over the local target for every conflicting dots entry, backing up local content first.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm use repo for all",
		TUI:                &TUIBinding{KeyMapField: "DotUseRepoAll", DefaultKey: "U", Label: "use repo (all)", Description: "Force-resolve every conflict with the repo version.", ConfirmDescription: "confirm use repo for all"},
		CLI:                []CLIBinding{{Command: []string{"dots", "sync"}, Flags: []string{"--use-repo"}}},
	},
	{
		ID:                 DotsResolveAllUseLocal,
		Domain:             "dots",
		Scope:              ScopeGlobal,
		Label:              "use local (all)",
		Description:        "Force-resolve every dots conflict with the local version.",
		LongDescription:    "Run a sync that adopts the local content into the repository for every conflicting dots entry, then relinks the managed targets.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm use local for all",
		TUI:                &TUIBinding{KeyMapField: "DotUseLocalAll", DefaultKey: "L", Label: "use local (all)", Description: "Force-resolve every conflict with the local version.", ConfirmDescription: "confirm use local for all"},
		CLI:                []CLIBinding{{Command: []string{"dots", "sync"}, Flags: []string{"--use-local"}}},
	},
	{
		ID:              DotsIgnore,
		Domain:          "dots",
		Scope:           ScopeRow,
		Label:           LabelIgnore,
		Description:     "Ignore a dots entry or a path pattern within it.",
		LongDescription: "Persist a whole dots entry as ignored, or add a per-entry ignore glob so matching files are skipped while syncing that dots entry.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName},
		TUI:             &TUIBinding{KeyMapField: "DotIgnore", DefaultKey: "x", Label: LabelIgnore, Description: "Ignore or unignore the selected dotfile entry."},
		CLI: []CLIBinding{
			{Command: []string{"dots", "ignore"}, Flags: []string{"--entry", "--path"}},
			{Command: []string{"dots", "unignore"}},
		},
	},
	{
		ID:              DotsEnable,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "enable dots",
		Description:     "Enable dotfile sync for this host.",
		LongDescription: "Clear the host dots-disabled setting and restore managed symlinks.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter", Label: "enable dots", Description: "Enable dotfile sync on this machine."},
		CLI:             []CLIBinding{{Command: []string{"dots", "enable"}}},
	},
	{
		ID:                 DotsDisable,
		Domain:             "dots",
		Scope:              ScopeGlobal,
		Label:              "disable dots",
		Description:        "Disable dotfile sync for this host.",
		LongDescription:    "Set the host dots-disabled flag and replace managed symlinks with local files.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm disable dots",
		TUI:                &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter", Label: "disable dots", Description: "Disable dotfile sync on this machine.", ConfirmDescription: "confirm disable dots"},
		CLI:                []CLIBinding{{Command: []string{"dots", "disable"}, Flags: []string{"--overwrite", "--remove-local"}}},
	},
	{
		ID:              DotsPull,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots pull",
		Description:     "Pull dotfile changes and resync links.",
		LongDescription: "Pull the dotfiles repository, then refresh managed symlinks.",
		Mutates:         true,
		CLI:             []CLIBinding{{Command: []string{"dots", "pull"}}},
		Palette:         &PaletteBinding{Command: []string{"dots", "pull"}, Description: "git pull + resync dotfile symlinks"},
		PaletteEligible: true,
	},
	{
		ID:              DotsCommit,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots commit",
		Description:     "Commit dotfile changes.",
		LongDescription: "Stage and commit pending dotfile repository changes without pushing.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter", Label: "commit dotfiles", Description: "Commit pending dotfile repository changes."},
		CLI:             []CLIBinding{{Command: []string{"dots", "commit"}, Flags: []string{"--message"}}},
		Palette:         &PaletteBinding{Command: []string{"dots", "commit"}, Description: "commit dotfile changes without pushing"},
		PaletteEligible: true,
	},
	{
		ID:              DotsPush,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots push",
		Description:     "Commit and push dotfile changes.",
		LongDescription: "Commit managed dotfile changes when needed, then push the dotfiles repository.",
		Mutates:         true,
		CLI:             []CLIBinding{{Command: []string{"dots", "push"}, Flags: []string{"--message"}}},
		Palette:         &PaletteBinding{Command: []string{"dots", "push"}, Description: "commit + push dotfile changes"},
		PaletteEligible: true,
	},
	{
		ID:              DotsReminder,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots reminder",
		Description:     "Install or remove periodic dotfile sync reminders.",
		LongDescription: "Install or uninstall a native user timer that periodically checks whether dotfiles need attention.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Toggle", DefaultKey: "space", Label: "toggle reminders", Description: "Enable or disable native dotfile reminder notifications from Settings."},
		CLI: []CLIBinding{
			{Command: []string{"dots", "reminder", "install"}, Flags: []string{"--interval", "--notify"}},
			{Command: []string{"dots", "reminder", "uninstall"}},
		},
	},
	{
		ID:              DotsReminderCheck,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots reminder check",
		Description:     "Check whether dotfiles need attention.",
		LongDescription: "Inspect dotfile sync health and git state without mutating config, repo, local files, or native services.",
		Mutates:         false,
		CLI:             []CLIBinding{{Command: []string{"dots", "reminder", "check"}, Flags: []string{"--format"}}},
		CLIOnlyReason:   "TUI surfaces dotfile health in the Dots tab instead of a separate reminder-check action.",
	},
	{
		ID:              DotsReminderRun,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots reminder run",
		Description:     "Run one dotfile reminder check.",
		LongDescription: "Run the reminder check once, optionally sending a desktop notification when dotfiles need attention.",
		Mutates:         false,
		CLI:             []CLIBinding{{Command: []string{"dots", "reminder", "run"}, Flags: []string{"--format", "--notify"}}},
		CLIOnlyReason:   "Native reminder timers call this helper command; TUI uses service toggles and the Dots tab instead.",
	},
	{
		ID:              DotsReminderStatus,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots reminder status",
		Description:     "Show reminder service status.",
		LongDescription: "Inspect the native dotfile reminder service files and parsed timer settings without changing them.",
		Mutates:         false,
		CLI:             []CLIBinding{{Command: []string{"dots", "reminder", "status"}, Flags: []string{"--format"}}},
		CLIOnlyReason:   "TUI renders reminder service status in Settings instead of a separate status action.",
	},
	{
		ID:              DotsWatch,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots watch",
		Description:     "Install or remove automatic dotfile watch sync.",
		LongDescription: "Install or uninstall a native user service that keeps configured dotfiles synced after local changes.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Toggle", DefaultKey: "space", Label: "toggle watch", Description: "Enable or disable native dotfile watch sync from Settings."},
		CLI: []CLIBinding{
			{Command: []string{"dots", "watch", "install"}, Flags: []string{"--debounce"}},
			{Command: []string{"dots", "watch", "uninstall"}},
		},
	},
	{
		ID:              DotsWatchRun,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots watch run",
		Description:     "Run the dotfile watcher in the foreground.",
		LongDescription: "Watch active dotfile source and target paths in the foreground and run dotfile sync after filesystem changes.",
		Mutates:         true,
		CLI:             []CLIBinding{{Command: []string{"dots", "watch", "run"}, Flags: []string{"--debounce"}}},
		CLIOnlyReason:   "Foreground watch is a long-running CLI/service helper; TUI exposes the native service toggle instead.",
	},
	{
		ID:              DotsWatchStatus,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots watch status",
		Description:     "Show watch service status.",
		LongDescription: "Inspect the native dotfile watch service file and parsed debounce setting without changing it.",
		Mutates:         false,
		CLI:             []CLIBinding{{Command: []string{"dots", "watch", "status"}, Flags: []string{"--format"}}},
		CLIOnlyReason:   "TUI renders watch service status in Settings instead of a separate status action.",
	},
	{
		ID:              DotsServicesStatus,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots services status",
		Description:     "Show dotfile service status.",
		LongDescription: "Inspect the native dotfile reminder and watch service files and parsed timing settings without changing them.",
		Mutates:         false,
		CLI:             []CLIBinding{{Command: []string{"dots", "services", "status"}, Flags: []string{"--format"}}},
		CLIOnlyReason:   "TUI renders the combined service dashboard in the Status tab.",
	},
	{
		ID:              DotsHistory,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "dots history",
		Description:     "Show recent dotfile operation history.",
		LongDescription: "Show recent machine-local dotfile operation records from the cache DB, including operation status, affected entry, summary, and low-level dotfile ops for recovery context.",
		Mutates:         false,
		CLI:             []CLIBinding{{Command: []string{"dots", "history"}, Flags: []string{"--limit", "--format"}}},
		CLIOnlyReason:   "TUI renders recent history in Dashboard and the Dots tab instead of exposing a separate key action.",
	},
}

// Groups is the canonical catalog for group-management actions.
var Groups = []Action{
	{
		ID:              GroupCreate,
		Domain:          "groups",
		Scope:           ScopeGlobal,
		Label:           LabelNewGroup,
		Description:     "Create an empty group.",
		LongDescription: "Create an empty group that can receive logical tool and dot memberships.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "NewGroup", DefaultKey: "n", Label: LabelNewGroup, Description: "Create a new group."},
		CLI:             []CLIBinding{{Command: []string{"groups", "create"}}},
	},
	{
		ID:              GroupRename,
		Domain:          "groups",
		Scope:           ScopeRow,
		Label:           LabelRename,
		Description:     "Rename a group.",
		LongDescription: "Rename a group and update host assignments to the new name.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Rename", DefaultKey: "r", Label: LabelRename, Description: "Rename the selected group."},
		CLI:             []CLIBinding{{Command: []string{"groups", "rename"}}},
	},
	{
		ID:                 GroupDelete,
		Domain:             "groups",
		Scope:              ScopeRow,
		Label:              LabelDelete,
		Description:        "Delete a group.",
		LongDescription:    "Delete a group, moving or deleting logical tools that would otherwise lose their final membership.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: ConfirmDelete,
		Requirements:       []Requirement{RequiresDeleteFallback},
		TUI:                &TUIBinding{KeyMapField: "Delete", DefaultKey: "d", Label: LabelDelete, Description: "Delete the selected group.", ConfirmDescription: ConfirmDelete},
		CLI:                []CLIBinding{{Command: []string{"groups", "delete"}, Flags: []string{"--move-to", "--delete-tools"}}},
	},
	{
		ID:              GroupEditTools,
		Domain:          "groups",
		Scope:           ScopeRow,
		Label:           LabelEditTools,
		Description:     "Edit group tool assignments and ignores.",
		LongDescription: "Toggle logical tool assignment and group-level ignore state for one group.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresGroupAssignment, RequiresIgnoreScope},
		TUI:             &TUIBinding{KeyMapField: "GroupTools", DefaultKey: "t", Label: LabelEditTools, Description: "Edit tools in the selected group."},
		CLI: []CLIBinding{
			{Command: []string{"groups", "move-tool"}},
			{Command: []string{"groups", "remove-tool"}},
			{Command: []string{"groups", "ignore-tool"}},
			{Command: []string{"groups", "unignore-tool"}},
		},
	},
	{
		ID:              GroupEditDots,
		Domain:          "groups",
		Scope:           ScopeRow,
		Label:           LabelEditDots,
		Description:     "Edit group dotfile assignments.",
		LongDescription: "Move configured dotfile entries into or out of one group.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresGroupAssignment},
		TUI:             &TUIBinding{KeyMapField: "GroupDots", DefaultKey: "f", Label: LabelEditDots, Description: "Edit dotfiles in the selected group."},
		CLI:             []CLIBinding{{Command: []string{"dots", "groups"}, Flags: []string{"--move", "--remove"}}},
	},
}

// Hosts is the canonical catalog for host-management actions.
var Hosts = []Action{
	{
		ID:              HostCreate,
		Domain:          "hosts",
		Scope:           ScopeGlobal,
		Label:           LabelNewHost,
		Description:     "Create a host.",
		LongDescription: "Create a host assignment and its protected host group.",
		Mutates:         true,
		CLIOnlyReason:   "TUI creates the current host during onboarding; manual remote host creation is intentionally CLI-only.",
		CLI:             []CLIBinding{{Command: []string{"hosts", "ensure"}}},
	},
	{
		ID:              HostCopy,
		Domain:          "hosts",
		Scope:           ScopeGlobal,
		Label:           "copy host config",
		Description:     "Copy one host's config to another host.",
		LongDescription: "Copy reusable group assignments, host settings, provider overrides, and reusable dotfile variants from one host to another.",
		Mutates:         true,
		CLIOnlyReason:   "TUI exposes host config copy only during onboarding for a missing current host.",
		CLI:             []CLIBinding{{Command: []string{"hosts", "copy"}}},
	},
	{
		ID:                 HostDelete,
		Domain:             "hosts",
		Scope:              ScopeRow,
		Label:              LabelDelete,
		Description:        "Delete a host.",
		LongDescription:    "Delete a host assignment and its protected host group.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: ConfirmDelete,
		TUI:                &TUIBinding{KeyMapField: "Delete", DefaultKey: "d", Label: LabelDelete, Description: "Delete the selected host.", ConfirmDescription: ConfirmDelete},
		CLI:                []CLIBinding{{Command: []string{"hosts", "remove"}}},
	},
	{
		ID:              HostEditGroups,
		Domain:          "hosts",
		Scope:           ScopeRow,
		Label:           LabelEditGroups,
		Description:     "Edit a host's reusable group list.",
		LongDescription: "Set, add, or remove reusable groups assigned to a host.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresGroupAssignment},
		TUI:             &TUIBinding{KeyMapField: "HostGroups", DefaultKey: "g", Label: LabelEditGroups, Description: "Edit groups assigned to the selected host."},
		CLI: []CLIBinding{
			{Command: []string{"hosts", "set-groups"}},
			{Command: []string{"hosts", "add-group"}},
			{Command: []string{"hosts", "remove-group"}},
		},
	},
}

// Settings is the canonical catalog for settings actions.
var Settings = []Action{
	{
		ID:              SettingsSet,
		Domain:          "settings",
		Scope:           ScopeGlobal,
		Label:           "set setting",
		Description:     "Set an omni setting.",
		LongDescription: "Set one host-effective setting such as managers, system priority, dots repo, or git automation.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Toggle", DefaultKey: "space", Label: "change setting", Description: "Change the selected setting."},
		CLI:             []CLIBinding{{Command: []string{"settings", "set"}}},
	},
	{
		ID:              SettingsProvider,
		Domain:          "settings",
		Scope:           ScopeGlobal,
		Label:           "toggle provider family",
		Description:     "Enable or disable a provider family.",
		LongDescription: "Enable or disable a provider family for this host.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Toggle", DefaultKey: "space", Label: "toggle provider", Description: "Enable or disable the selected provider."},
		CLI: []CLIBinding{
			{Command: []string{"settings", "disable-provider"}},
			{Command: []string{"settings", "enable-provider"}},
		},
	},
	{
		ID:                 SettingsReset,
		Domain:             "settings",
		Scope:              ScopeGlobal,
		Label:              "reset settings",
		Description:        "Reset settings to defaults.",
		LongDescription:    "Reset host/global settings while preserving tools, groups, and hosts.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm reset settings",
		TUI:                &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter", Label: "reset settings", Description: "Reset settings from the danger zone.", ConfirmDescription: "confirm reset settings"},
		CLI:                []CLIBinding{{Command: []string{"settings", "reset"}}},
	},
	{
		ID:                 SettingsResetCache,
		Domain:             "settings",
		Scope:              ScopeGlobal,
		Label:              "reset cache",
		Description:        "Clear and reinitialise the tool cache.",
		LongDescription:    "Drop and recreate the disposable tool cache database.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm reset cache",
		TUI:                &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter", Label: "reset cache", Description: "Clear the local tool cache.", ConfirmDescription: "confirm reset cache"},
		CLI:                []CLIBinding{{Command: []string{"settings", "reset-cache"}}},
	},
	{
		ID:              SettingsMigrateHostOverrides,
		Domain:          "settings",
		Scope:           ScopeGlobal,
		Label:           "migrate host overrides",
		Description:     "Fold tools.*.hosts install overrides into providers[].",
		LongDescription: "Move deprecated per-host install overrides into the providers[] list and remove empty hosts maps.",
		Mutates:         true,
		CLIOnlyReason:   "One-time config migration for deprecated tools.*.hosts install overrides; exposed as a CLI repair command.",
		CLI:             []CLIBinding{{Command: []string{"settings", "migrate-host-overrides"}}},
	},
	{
		ID:              SettingsExtract,
		Domain:          "settings",
		Scope:           ScopeGlobal,
		Label:           "extract settings fragments",
		Description:     "Split settings.json into settings.d fragments.",
		LongDescription: "Move agents, tools, groups, and dot entries into settings.d/agents.json, tools.json, groups.json, and dots.json, removing the duplicated keys from settings.json.",
		Mutates:         true,
		CLIOnlyReason:   "Config layout migration; exposed as a CLI repair command.",
		CLI:             []CLIBinding{{Command: []string{"settings", "extract"}}},
	},
}

// Setup is the canonical catalog for onboarding/setup actions.
var Setup = []Action{
	{
		ID:              SetupInit,
		Domain:          "setup",
		Scope:           ScopeGlobal,
		Label:           "bootstrap host",
		Description:     "Create or activate omni's config for this host.",
		LongDescription: "Run guided bootstrap: choose provider families, host groups, and optional tools or dots activation.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter", Label: "run bootstrap", Description: "Run the guided bootstrap flow."},
		CLI:             []CLIBinding{{Command: []string{"bootstrap"}, Flags: []string{"--import", "--no-import", "--import-config"}}},
	},
}

// Diagnostics is the canonical catalog for read-only health checks.
var Diagnostics = []Action{
	{
		ID:              Doctor,
		Domain:          "diagnostics",
		Scope:           ScopeGlobal,
		Label:           "doctor",
		Description:     "Run read-only health checks.",
		LongDescription: "Run diagnostics for config, host setup, providers, dotfiles, native services, and local cache state.",
		Mutates:         false,
		TUI:             &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter", Label: "run doctor", Description: "Run read-only diagnostics from the Status tab."},
		CLI:             []CLIBinding{{Command: []string{"doctor"}, Flags: []string{"--format"}}},
	},
}

var byID = map[ID]Action{}

func init() {
	for _, action := range All() {
		byID[action.ID] = action
	}
}

// All returns every user-visible action cataloged across domains.
func All() []Action {
	out := make([]Action, 0, len(Lifecycle)+len(Tools)+len(Dots)+len(Groups)+len(Hosts)+len(Settings)+len(Setup)+len(Diagnostics))
	out = append(out, Lifecycle...)
	out = append(out, Tools...)
	out = append(out, Dots...)
	out = append(out, Groups...)
	out = append(out, Hosts...)
	out = append(out, Settings...)
	out = append(out, Setup...)
	out = append(out, Diagnostics...)
	return out
}

// Get returns action metadata by ID.
func Get(id ID) (Action, bool) {
	action, ok := byID[id]
	return action, ok
}

// MustTUILabel returns the TUI-specific compact label for id. It falls back to
// the canonical label so palette-only actions can share short nouns safely.
func MustTUILabel(id ID) string {
	action, ok := Get(id)
	if !ok {
		panic("unknown action: " + string(id))
	}
	if action.TUI != nil && action.TUI.Label != "" {
		return action.TUI.Label
	}
	return action.Label
}

// MustDescription returns the short catalog description for id.
func MustDescription(id ID) string {
	action, ok := Get(id)
	if !ok {
		panic("unknown action: " + string(id))
	}
	return action.Description
}

// MustLongDescription returns the long catalog description for id. It falls
// back to Description so callers can adopt long help incrementally.
func MustLongDescription(id ID) string {
	action, ok := Get(id)
	if !ok {
		panic("unknown action: " + string(id))
	}
	if action.LongDescription != "" {
		return action.LongDescription
	}
	return action.Description
}

// MustConfirmDescription returns the catalog confirmation label for id.
func MustConfirmDescription(id ID) string {
	action, ok := Get(id)
	if !ok {
		panic("unknown action: " + string(id))
	}
	return action.ConfirmDescription
}

// MustTUIConfirmDescription returns TUI-specific confirmation copy for id.
func MustTUIConfirmDescription(id ID) string {
	action, ok := Get(id)
	if !ok {
		panic("unknown action: " + string(id))
	}
	if action.TUI != nil && action.TUI.ConfirmDescription != "" {
		return action.TUI.ConfirmDescription
	}
	return action.ConfirmDescription
}

// MustPalette returns the palette binding for id.
func MustPalette(id ID) PaletteBinding {
	action, ok := Get(id)
	if !ok {
		panic("unknown action: " + string(id))
	}
	if action.Palette == nil {
		panic("action has no palette binding: " + string(id))
	}
	return *action.Palette
}
