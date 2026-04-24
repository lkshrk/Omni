// Package actions defines omni's user-visible capabilities independently of
// the surfaces that expose them.
package actions

// ID is the stable semantic identifier for a user-visible action.
type ID string

const (
	ToolSync             ID = "tools.sync"
	ToolInstall          ID = "tools.install"
	ToolDelete           ID = "tools.delete"
	ToolUpdate           ID = "tools.update"
	ToolUpdateAll        ID = "tools.update_all"
	ToolSyncAll          ID = "tools.sync_all"
	ToolClaim            ID = "tools.claim"
	ToolIgnore           ID = "tools.ignore"
	ToolChangeGroup      ID = "tools.change_group"
	ToolPinProvider      ID = "tools.pin_provider"
	ToolReinstallDefault ID = "tools.reinstall_default"
	ToolRefresh          ID = "tools.refresh"
	ToolConsolidate      ID = "tools.consolidate"
	ToolSetSpec          ID = "tools.set_spec"
	ToolDeleteSpec       ID = "tools.delete_spec"
	ToolImport           ID = "tools.import"
	ToolSwitchProvider   ID = "tools.switch_provider"
	DotsSync             ID = "dots.sync"
	DotsDiscover         ID = "dots.discover"
	DotsAdd              ID = "dots.add"
	DotsEditGroups       ID = "dots.edit_groups"
	DotsDelete           ID = "dots.delete"
	DotsIgnore           ID = "dots.ignore"
	DotsEnable           ID = "dots.enable"
	DotsDisable          ID = "dots.disable"
	DotsPull             ID = "dots.pull"
	DotsPush             ID = "dots.push"
	GroupCreate          ID = "groups.create"
	GroupRename          ID = "groups.rename"
	GroupDelete          ID = "groups.delete"
	GroupEditTools       ID = "groups.edit_tools"
	GroupEditDots        ID = "groups.edit_dots"
	ProfileCreate        ID = "profiles.create"
	ProfileRename        ID = "profiles.rename"
	ProfileDelete        ID = "profiles.delete"
	ProfileEditGroups    ID = "profiles.edit_groups"
	ProfileEditHosts     ID = "profiles.edit_hosts"
	SettingsSet          ID = "settings.set"
	SettingsProvider     ID = "settings.provider"
	SettingsReset        ID = "settings.reset"
	SettingsResetCache   ID = "settings.reset_cache"
	SetupInit            ID = "setup.init"
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
	KeyMapField string
	DefaultKey  string
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
		CLI:             []CLIBinding{{Command: []string{"sync"}, Flags: []string{"--dry-run", "--prune", "--provider", "--group", "--profile", "--retry-failed"}}},
		Palette:         &PaletteBinding{Command: []string{"sync"}, Description: "sync tools from config"},
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
		TUI:             &TUIBinding{KeyMapField: "Install", DefaultKey: "i"},
		CLI:             []CLIBinding{{Command: []string{"install"}, Flags: []string{"--provider"}}},
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
		TUI:                &TUIBinding{KeyMapField: "Delete", DefaultKey: "d"},
		CLI:                []CLIBinding{{Command: []string{"delete"}, Flags: []string{"--provider"}}},
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
		TUI:             &TUIBinding{KeyMapField: "Upgrade", DefaultKey: "u"},
		CLI:             []CLIBinding{{Command: []string{"upgrade"}, Flags: []string{"--provider"}}},
	},
	{
		ID:              ToolUpdateAll,
		Domain:          "tools",
		Scope:           ScopeGlobal,
		Label:           "upgrade all",
		Description:     "Upgrade every outdated tool.",
		LongDescription: "Upgrade every outdated installed tool currently tracked in the local cache.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "UpgradeAll", DefaultKey: "U"},
		CLI:             []CLIBinding{{Command: []string{"upgrade"}, Flags: []string{"--all"}}},
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
		TUI:                &TUIBinding{KeyMapField: "SyncAll", DefaultKey: "S"},
		CLI:                []CLIBinding{{Command: []string{"sync"}, Flags: []string{"--all"}}},
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
		TUI:             &TUIBinding{KeyMapField: "Claim", DefaultKey: "c"},
		CLI:             []CLIBinding{{Command: []string{"add"}, Flags: []string{"--group", "--provider", "--install-with"}}},
	},
	{
		ID:              ToolIgnore,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           LabelIgnore,
		Description:     "Toggle whether a scope skips this logical tool.",
		LongDescription: "Toggle a tool-level or group-level ignore entry. Ignored tools remain in config but are skipped during sync for the selected scope.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresIgnoreScope},
		TUI:             &TUIBinding{KeyMapField: "Ignore", DefaultKey: "x"},
		CLI: []CLIBinding{
			{Command: []string{"tools", "ignore"}},
			{Command: []string{"tools", "unignore"}},
			{Command: []string{"groups", "ignore-tool"}},
			{Command: []string{"groups", "unignore-tool"}},
			{Command: []string{"profile", "ignore", "add"}},
			{Command: []string{"profile", "ignore", "remove"}},
		},
	},
	{
		ID:              ToolChangeGroup,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           LabelEditGroups,
		Description:     "Change logical tool group memberships.",
		LongDescription: "Add, remove, or move a logical tool membership without changing its installed state or logical tool spec.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresGroupAssignment},
		TUI:             &TUIBinding{KeyMapField: "MoveGroup", DefaultKey: "g"},
		CLI: []CLIBinding{
			{Command: []string{"groups", "add-tool"}},
			{Command: []string{"groups", "remove-tool"}},
		},
	},
	{
		ID:              ToolPinProvider,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           "pin provider",
		Description:     "Set or remove a provider override for a selected scope.",
		LongDescription: "Set a host override, ecosystem host manager, or explicit logical tool install-with override, or remove an existing override when explicitly chosen.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresProviderScope},
		TUI:             &TUIBinding{KeyMapField: "PinProvider", DefaultKey: "p"},
		CLI: []CLIBinding{
			{Command: []string{"tools", "set"}, Flags: []string{"--provider", "--install-with", "--global", "--host"}},
		},
	},
	{
		ID:                 ToolReinstallDefault,
		Domain:             "tools",
		Scope:              ScopeRow,
		Label:              "reinstall with default",
		Description:        "Reinstall a wrong-provider tool with its configured provider.",
		LongDescription:    "Reinstall a wrong-provider tool with the configured ecosystem-provider default, then remove the old concrete-provider installation.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: ConfirmReinstall,
		Requirements:       []Requirement{RequiresToolName},
		TUI:                &TUIBinding{KeyMapField: "MigrateProvider", DefaultKey: "r"},
		CLI:                []CLIBinding{{Command: []string{"switch"}, Flags: []string{"--reinstall-default", "--provider"}}},
	},
	{
		ID:              ToolRefresh,
		Domain:          "tools",
		Scope:           ScopeGlobal,
		Label:           "refresh",
		Description:     "Refresh cached installed and outdated state.",
		LongDescription: "Refresh omni's local cache of installed and outdated tools without installing or uninstalling packages.",
		TUI:             &TUIBinding{KeyMapField: "Refresh", DefaultKey: "R"},
		CLI:             []CLIBinding{{Command: []string{"list"}}},
	},
	{
		ID:              ToolConsolidate,
		Domain:          "tools",
		Scope:           ScopeGlobal,
		Label:           "consolidate",
		Description:     "Move an ecosystem to one manager.",
		LongDescription: "Consolidate all tools in an ecosystem to the selected manager.",
		Mutates:         true,
		CLI:             []CLIBinding{{Command: []string{"consolidate"}}},
		Palette:         &PaletteBinding{Command: []string{"consolidate"}, Description: "use selected manager for ecosystem tools", DescriptionFormat: "use %s for %s tools"},
		PaletteEligible: true,
	},
	{
		ID:              ToolSetSpec,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           "set tool spec",
		Description:     "Create or update a logical tool spec.",
		LongDescription: "Create or update a logical tool spec, including package name, ecosystem provider, concrete install-with target, or host override.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresEcosystemProvider},
		TUI:             &TUIBinding{KeyMapField: "PinProvider", DefaultKey: "p"},
		CLI:             []CLIBinding{{Command: []string{"tools", "set"}, Flags: []string{"--provider", "--package", "--install-with", "--host", "--global"}}},
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
		TUI:                &TUIBinding{KeyMapField: "Delete", DefaultKey: "d"},
		CLI:                []CLIBinding{{Command: []string{"tools", "delete"}}},
	},
	{
		ID:              ToolImport,
		Domain:          "tools",
		Scope:           ScopeGlobal,
		Label:           "import installed tools",
		Description:     "Import installed tools into config.",
		LongDescription: "Discover locally installed tools and add them to config, optionally scoped by provider and destination group.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter"},
		CLI:             []CLIBinding{{Command: []string{"import"}, Flags: []string{"--provider", "--group", "--dry-run"}}},
	},
	{
		ID:              ToolSwitchProvider,
		Domain:          "tools",
		Scope:           ScopeRow,
		Label:           "switch provider",
		Description:     "Move one tool between explicit providers.",
		LongDescription: "Install one tool with an explicit target provider, remove the old provider installation best-effort, and rewrite config/cache ownership.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName},
		CLI:             []CLIBinding{{Command: []string{"switch"}, Flags: []string{"--from", "--to"}}},
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
		TUI:             &TUIBinding{KeyMapField: "Sync", DefaultKey: "s"},
		CLI:             []CLIBinding{{Command: []string{"dots", "sync"}, Flags: []string{"--dry-run"}}},
		Palette:         &PaletteBinding{Command: []string{"dots", "sync"}, Description: "repair dotfile symlinks (no git)"},
		PaletteEligible: true,
	},
	{
		ID:              DotsDiscover,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           LabelDiscover,
		Description:     "List untracked dotfile candidates.",
		LongDescription: "Discover repo, local config, and well-known dotfile candidates without mutating config, the repo, or local files.",
		Mutates:         false,
		TUI:             &TUIBinding{KeyMapField: "DotDiscover", DefaultKey: "D"},
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
		TUI:             &TUIBinding{KeyMapField: "DotAdd", DefaultKey: "a"},
		CLI:             []CLIBinding{{Command: []string{"dots", "add"}, Flags: []string{"--name", "--group", "--adopt", "--ignore", "--discovered"}}},
	},
	{
		ID:              DotsEditGroups,
		Domain:          "dots",
		Scope:           ScopeRow,
		Label:           LabelEditGroups,
		Description:     "Change dots entry group memberships.",
		LongDescription: "Add, remove, or replace group memberships for a managed dots entry without changing its repo or local files.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresToolName, RequiresGroupAssignment},
		TUI:             &TUIBinding{KeyMapField: "MoveGroup", DefaultKey: "g"},
		CLI:             []CLIBinding{{Command: []string{"dots", "groups"}, Flags: []string{"--set", "--add", "--remove"}}},
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
		TUI:                &TUIBinding{KeyMapField: "DotDelete", DefaultKey: "d"},
		CLI:                []CLIBinding{{Command: []string{"dots", "delete"}}},
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
		TUI:             &TUIBinding{KeyMapField: "DotIgnore", DefaultKey: "x"},
		CLI:             []CLIBinding{{Command: []string{"dots", "ignore"}}, {Command: []string{"dots", "unignore"}}},
	},
	{
		ID:              DotsEnable,
		Domain:          "dots",
		Scope:           ScopeGlobal,
		Label:           "enable dots",
		Description:     "Enable dotfile sync for this host.",
		LongDescription: "Clear the host dots-disabled setting and restore managed symlinks.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter"},
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
		TUI:                &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter"},
		CLI:                []CLIBinding{{Command: []string{"dots", "disable"}, Flags: []string{"--overwrite"}}},
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
		TUI:             &TUIBinding{KeyMapField: "NewGroup", DefaultKey: "n"},
		CLI:             []CLIBinding{{Command: []string{"groups", "create"}}},
	},
	{
		ID:              GroupRename,
		Domain:          "groups",
		Scope:           ScopeRow,
		Label:           LabelRename,
		Description:     "Rename a group.",
		LongDescription: "Rename a group and update profile references to the new name.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Rename", DefaultKey: "r"},
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
		TUI:                &TUIBinding{KeyMapField: "Delete", DefaultKey: "d"},
		CLI:                []CLIBinding{{Command: []string{"groups", "delete"}, Flags: []string{"--move-to", "--delete-tools"}}},
	},
	{
		ID:              GroupEditTools,
		Domain:          "groups",
		Scope:           ScopeRow,
		Label:           LabelEditTools,
		Description:     "Edit group tool memberships and ignores.",
		LongDescription: "Toggle logical tool membership and group-level ignore state for one group.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresGroupAssignment, RequiresIgnoreScope},
		TUI:             &TUIBinding{KeyMapField: "GroupTools", DefaultKey: "t"},
		CLI: []CLIBinding{
			{Command: []string{"groups", "add-tool"}},
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
		Description:     "Edit group dotfile memberships.",
		LongDescription: "Toggle configured dotfile membership for one group.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresGroupAssignment},
		TUI:             &TUIBinding{KeyMapField: "GroupDots", DefaultKey: "f"},
		CLI:             []CLIBinding{{Command: []string{"dots", "groups"}, Flags: []string{"--set", "--add", "--remove"}}},
	},
}

// Profiles is the canonical catalog for profile-management actions.
var Profiles = []Action{
	{
		ID:              ProfileCreate,
		Domain:          "profiles",
		Scope:           ScopeGlobal,
		Label:           LabelNewProfile,
		Description:     "Create a profile.",
		LongDescription: "Create or replace a profile with an explicit list of groups.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "NewProfile", DefaultKey: "p"},
		CLI:             []CLIBinding{{Command: []string{"profile", "add"}}},
	},
	{
		ID:              ProfileRename,
		Domain:          "profiles",
		Scope:           ScopeRow,
		Label:           LabelRename,
		Description:     "Rename a profile.",
		LongDescription: "Rename a profile and update hostname mappings that point at it.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Rename", DefaultKey: "r"},
		CLI:             []CLIBinding{{Command: []string{"profile", "rename"}}},
	},
	{
		ID:                 ProfileDelete,
		Domain:             "profiles",
		Scope:              ScopeRow,
		Label:              LabelDelete,
		Description:        "Delete a profile.",
		LongDescription:    "Delete a profile and remove hostname mappings that point at it.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: ConfirmDelete,
		TUI:                &TUIBinding{KeyMapField: "Delete", DefaultKey: "d"},
		CLI:                []CLIBinding{{Command: []string{"profile", "delete"}}},
	},
	{
		ID:              ProfileEditGroups,
		Domain:          "profiles",
		Scope:           ScopeRow,
		Label:           LabelEditGroups,
		Description:     "Edit a profile's group list.",
		LongDescription: "Toggle which groups are active when a profile is selected.",
		Mutates:         true,
		Requirements:    []Requirement{RequiresGroupAssignment},
		TUI:             &TUIBinding{KeyMapField: "ProfileGroups", DefaultKey: "g"},
		CLI: []CLIBinding{
			{Command: []string{"profile", "add-group"}},
			{Command: []string{"profile", "remove-group"}},
		},
	},
	{
		ID:              ProfileEditHosts,
		Domain:          "profiles",
		Scope:           ScopeRow,
		Label:           LabelEditHosts,
		Description:     "Edit hostname mappings for a profile.",
		LongDescription: "Assign or remove existing hostname mappings for one profile.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "EditHosts", DefaultKey: "h"},
		CLI: []CLIBinding{
			{Command: []string{"profile", "set-hostname"}},
			{Command: []string{"profile", "remove-hostname"}},
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
		TUI:             &TUIBinding{KeyMapField: "Toggle", DefaultKey: "space"},
		CLI:             []CLIBinding{{Command: []string{"settings", "set"}}},
	},
	{
		ID:              SettingsProvider,
		Domain:          "settings",
		Scope:           ScopeGlobal,
		Label:           "toggle ecosystem provider",
		Description:     "Enable or disable an ecosystem provider.",
		LongDescription: "Enable or disable an ecosystem provider for this host.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Toggle", DefaultKey: "space"},
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
		LongDescription:    "Reset host/global settings while preserving tools, groups, and profiles.",
		Mutates:            true,
		RequiresConfirm:    true,
		ConfirmDescription: "confirm reset settings",
		TUI:                &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter"},
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
		TUI:                &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter"},
		CLI:                []CLIBinding{{Command: []string{"settings", "reset-cache"}}},
	},
}

// Setup is the canonical catalog for onboarding/setup actions.
var Setup = []Action{
	{
		ID:              SetupInit,
		Domain:          "setup",
		Scope:           ScopeGlobal,
		Label:           "initialise config",
		Description:     "Create or update omni's config.",
		LongDescription: "Run first-time setup: choose ecosystem providers, profile mapping, and optional dots configuration.",
		Mutates:         true,
		TUI:             &TUIBinding{KeyMapField: "Confirm", DefaultKey: "enter"},
		CLI:             []CLIBinding{{Command: []string{"init"}}},
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
	out := make([]Action, 0, len(Tools)+len(Dots)+len(Groups)+len(Profiles)+len(Settings)+len(Setup))
	out = append(out, Tools...)
	out = append(out, Dots...)
	out = append(out, Groups...)
	out = append(out, Profiles...)
	out = append(out, Settings...)
	out = append(out, Setup...)
	return out
}

// Get returns action metadata by ID.
func Get(id ID) (Action, bool) {
	action, ok := byID[id]
	return action, ok
}

// MustLabel returns the catalog label for id. It panics for programmer errors
// during startup/tests, making stale key/help references obvious.
func MustLabel(id ID) string {
	action, ok := Get(id)
	if !ok {
		panic("unknown action: " + string(id))
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
