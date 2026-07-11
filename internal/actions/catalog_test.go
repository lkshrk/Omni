package actions

import (
	"strings"
	"testing"
)

func TestGet_Found(t *testing.T) {
	got, ok := Get(ToolSync)
	if !ok {
		t.Fatal("ToolSync should be registered")
	}
	if got.ID != ToolSync {
		t.Errorf("Get(ToolSync).ID = %q, want %q", got.ID, ToolSync)
	}
}

func TestGet_NotFound(t *testing.T) {
	if _, ok := Get(ID("does.not.exist")); ok {
		t.Error("Get of unknown ID should return ok=false")
	}
}

func TestAll_NonEmptyAndNoDuplicates(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("All() returned empty")
	}
	seen := make(map[ID]bool, len(all))
	for _, a := range all {
		if seen[a.ID] {
			t.Errorf("duplicate ID in All(): %q", a.ID)
		}
		seen[a.ID] = true
	}
}

func TestAction_Requires(t *testing.T) {
	install, ok := Get(ToolInstall)
	if !ok {
		t.Fatal("ToolInstall should exist")
	}
	if !install.Requires(RequiresToolName) {
		t.Error("ToolInstall should require RequiresToolName")
	}
	if install.Requires(Requirement("nonexistent")) {
		t.Error("ToolInstall should not require an unknown Requirement")
	}
	// An action with no Requirements never reports a requirement.
	zero := Action{}
	if zero.Requires(RequiresToolName) {
		t.Error("zero-value Action should not declare any requirement")
	}
}

func TestMustTUICopy_UsesTUIOverrides(t *testing.T) {
	if got := MustTUILabel(DotsSync); got != "sync dotfiles" {
		t.Fatalf("MustTUILabel(DotsSync) = %q, want TUI override", got)
	}
	if got := MustTUIConfirmDescription(ToolReinstallDefault); got != ConfirmReinstall {
		t.Fatalf("MustTUIConfirmDescription(ToolReinstallDefault) = %q, want %q", got, ConfirmReinstall)
	}
}

func TestToolSetSpecCopyUsesProviderListTerminology(t *testing.T) {
	action, ok := Get(ToolSetSpec)
	if !ok {
		t.Fatal("ToolSetSpec should exist")
	}
	if !strings.Contains(action.LongDescription, "provider candidates") {
		t.Fatalf("LongDescription = %q, want provider candidates terminology", action.LongDescription)
	}
	stale := []string{"ecosystem provider", "concrete install-with target"}
	for _, term := range stale {
		if strings.Contains(action.LongDescription, term) {
			t.Fatalf("LongDescription = %q should not contain stale term %q", action.LongDescription, term)
		}
	}
}

func TestToolProviderActionCopyUsesProviderFamilyTerminology(t *testing.T) {
	for _, id := range []ID{ToolPinProvider, ToolReinstallDefault} {
		action, ok := Get(id)
		if !ok {
			t.Fatalf("%s should exist", id)
		}
		if !strings.Contains(action.LongDescription, "provider family") {
			t.Fatalf("%s LongDescription = %q, want provider family terminology", id, action.LongDescription)
		}
		stale := []string{"ecosystem provider", "ecosystem-provider", "ecosystem host manager", "concrete-provider"}
		for _, term := range stale {
			if strings.Contains(action.LongDescription, term) {
				t.Fatalf("%s LongDescription = %q should not contain stale term %q", id, action.LongDescription, term)
			}
		}
	}
}

func TestMustDescription_PanicsOnUnknown(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unknown ID")
		}
	}()
	_ = MustDescription(ID("does.not.exist"))
}

func TestMustLongDescription_FallsBackToDescription(t *testing.T) {
	// Construct a synthetic action with no long description and verify the
	// fallback path. The catalog test guarantees real entries always set both.
	stub := Action{ID: ID("test.fallback"), Description: "short"}
	byID[stub.ID] = stub
	defer delete(byID, stub.ID)

	if got := MustLongDescription(stub.ID); got != "short" {
		t.Errorf("MustLongDescription fallback = %q, want %q", got, "short")
	}
}

func TestMustLongDescription_PanicsOnUnknown(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unknown ID")
		}
	}()
	_ = MustLongDescription(ID("does.not.exist"))
}

func TestMustConfirmDescription_PanicsOnUnknown(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unknown ID")
		}
	}()
	_ = MustConfirmDescription(ID("does.not.exist"))
}

func TestMustPalette_PanicsOnMissingBinding(t *testing.T) {
	// Inject an action with no Palette to exercise the second panic branch.
	stub := Action{ID: ID("test.no_palette")}
	byID[stub.ID] = stub
	defer delete(byID, stub.ID)

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic for missing palette binding")
		}
		msg, _ := r.(string)
		if !strings.Contains(msg, "no palette binding") {
			t.Errorf("panic = %v, want containing 'no palette binding'", r)
		}
	}()
	_ = MustPalette(stub.ID)
}

func TestMustPalette_PanicsOnUnknownID(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic on unknown ID")
		}
	}()
	_ = MustPalette(ID("does.not.exist"))
}

func TestMustPalette_ReturnsBindingForRegistered(t *testing.T) {
	// Find any action with a Palette binding to verify the success path.
	var withPalette Action
	for _, a := range All() {
		if a.Palette != nil {
			withPalette = a
			break
		}
	}
	if withPalette.ID == "" {
		t.Skip("no actions have palette bindings; skipping success-path test")
	}
	got := MustPalette(withPalette.ID)
	if len(got.Command) == 0 {
		t.Errorf("MustPalette(%s) returned empty command", withPalette.ID)
	}
}

func TestActionsHaveSurfaceBindings(t *testing.T) {
	for _, action := range All() {
		if action.ID == "" {
			t.Fatal("action has empty ID")
		}
		if action.Label == "" {
			t.Fatalf("%s has empty label", action.ID)
		}
		if action.Description == "" {
			t.Fatalf("%s has empty description", action.ID)
		}
		if action.LongDescription == "" {
			t.Fatalf("%s has empty long description", action.ID)
		}
		if action.TUI == nil && action.Palette == nil && action.CLIOnlyReason == "" {
			t.Fatalf("%s has neither TUI key binding nor palette binding", action.ID)
		}
		if action.CLIOnlyReason != "" && (action.TUI != nil || action.Palette != nil) {
			t.Fatalf("%s declares CLI-only reason but also has non-CLI bindings", action.ID)
		}
		if action.TUI != nil && (action.TUI.KeyMapField == "" || action.TUI.DefaultKey == "") {
			t.Fatalf("%s has incomplete TUI binding: %+v", action.ID, action.TUI)
		}
		if action.TUI != nil && (action.TUI.Label == "" || action.TUI.Description == "") {
			t.Fatalf("%s has incomplete TUI copy: %+v", action.ID, action.TUI)
		}
		if action.TUI != nil && action.RequiresConfirm && action.TUI.ConfirmDescription == "" {
			t.Fatalf("%s requires confirmation without TUI confirm text", action.ID)
		}
		if action.Palette != nil {
			if len(action.Palette.Command) == 0 || action.Palette.Description == "" {
				t.Fatalf("%s has incomplete palette binding: %+v", action.ID, action.Palette)
			}
			if action.Palette.DescriptionFormat != "" && action.Palette.DescriptionFormat == action.Palette.Description {
				t.Fatalf("%s palette description format should add runtime detail: %+v", action.ID, action.Palette)
			}
		}
		if action.PaletteEligible && action.Palette == nil {
			t.Fatalf("%s is palette-eligible but has no palette binding", action.ID)
		}
		if action.Palette != nil && !action.PaletteEligible {
			t.Fatalf("%s has a palette binding but is not marked palette-eligible", action.ID)
		}
		if len(action.CLI) == 0 {
			t.Fatalf("%s has no CLI binding", action.ID)
		}
		if action.RequiresConfirm && action.ConfirmDescription == "" {
			t.Fatalf("%s requires confirmation without confirm text", action.ID)
		}
	}
}

func TestActionCatalogIncludesDurableDomains(t *testing.T) {
	for _, id := range []ID{
		ToolSetSpec,
		ToolDeleteSpec,
		ToolNormalizeProviderOverrides,
		ToolHealBrewTaps,
		ToolImport,
		ToolSwitchProvider,
		DotsRefresh,
		DotsAdd,
		DotsEditGroups,
		DotsVariant,
		DotsDelete,
		DotsResolveUseRepo,
		DotsResolveUseLocal,
		DotsIgnore,
		DotsEnable,
		DotsDisable,
		GroupCreate,
		GroupRename,
		GroupDelete,
		GroupEditTools,
		GroupEditDots,
		HostCreate,
		HostCopy,
		HostDelete,
		HostEditGroups,
		SettingsSet,
		SettingsProvider,
		SettingsReset,
		SettingsResetCache,
		SettingsMigrateHostOverrides,
		SetupInit,
	} {
		if _, ok := Get(id); !ok {
			t.Fatalf("missing durable action %s", id)
		}
	}
}

func TestToolActionIDsAreUnique(t *testing.T) {
	seen := map[ID]bool{}
	for _, action := range All() {
		if seen[action.ID] {
			t.Fatalf("duplicate action ID %s", action.ID)
		}
		seen[action.ID] = true
	}
}

func TestLogicalToolActionContracts(t *testing.T) {
	claim := mustAction(t, ToolClaim)
	for _, req := range []Requirement{RequiresGroupAssignment, RequiresEcosystemProvider} {
		if !claim.Requires(req) {
			t.Fatalf("%s missing requirement %s", claim.ID, req)
		}
	}
	if !hasCLIFlag(claim, "--group") || !hasCLIFlag(claim, "--provider") || !hasCLIFlag(claim, "--install-with") {
		t.Fatalf("%s CLI bindings must expose --group, --provider, and --install-with: %+v", claim.ID, claim.CLI)
	}

	changeGroup := mustAction(t, ToolChangeGroup)
	for _, command := range [][]string{{"groups", "move-tool"}, {"groups", "remove-tool"}} {
		if !hasCLICommand(changeGroup, command) {
			t.Fatalf("%s missing CLI command %v in %+v", changeGroup.ID, command, changeGroup.CLI)
		}
	}
	if hasCLICommand(changeGroup, []string{"groups", "add-tool"}) {
		t.Fatalf("%s must not expose legacy add-tool command: %+v", changeGroup.ID, changeGroup.CLI)
	}

	pin := mustAction(t, ToolPinProvider)
	if !pin.Requires(RequiresProviderScope) {
		t.Fatalf("%s must declare provider scope requirement", pin.ID)
	}
	if !hasCLICommand(pin, []string{"tools", "set"}) || !hasCLIFlag(pin, "--install-with") || !hasCLIFlag(pin, "--host") || !hasCLIFlag(pin, "--global") {
		t.Fatalf("%s must expose logical tool install-with mutation and scope through tools set: %+v", pin.ID, pin.CLI)
	}
	if hasCLICommand(pin, []string{"switch"}) {
		t.Fatalf("%s must not expose legacy switch repair flags for provider pinning: %+v", pin.ID, pin.CLI)
	}
}

func TestDotsActionContracts(t *testing.T) {
	editGroups := mustAction(t, DotsEditGroups)
	for _, req := range []Requirement{RequiresToolName, RequiresGroupAssignment} {
		if !editGroups.Requires(req) {
			t.Fatalf("%s missing requirement %s", editGroups.ID, req)
		}
	}
	if !hasCLICommand(editGroups, []string{"dots", "groups"}) {
		t.Fatalf("%s missing dots groups CLI binding: %+v", editGroups.ID, editGroups.CLI)
	}
	for _, flag := range []string{"--move", "--remove"} {
		if !hasCLIFlag(editGroups, flag) {
			t.Fatalf("%s missing %s CLI flag: %+v", editGroups.ID, flag, editGroups.CLI)
		}
	}
	if editGroups.TUI == nil || editGroups.TUI.KeyMapField != "MoveGroup" {
		t.Fatalf("%s should use shared group membership picker binding, got %+v", editGroups.ID, editGroups.TUI)
	}

	resolveRepo := mustAction(t, DotsResolveUseRepo)
	if !resolveRepo.Requires(RequiresToolName) || !hasCLICommand(resolveRepo, []string{"dots", "resolve"}) || !hasCLIFlag(resolveRepo, "--use-repo") {
		t.Fatalf("%s must expose dots resolve --use-repo for one entry: %+v", resolveRepo.ID, resolveRepo)
	}
	if resolveRepo.TUI == nil || resolveRepo.TUI.KeyMapField != "DotUseRepo" {
		t.Fatalf("%s should use DotUseRepo binding, got %+v", resolveRepo.ID, resolveRepo.TUI)
	}
	resolveLocal := mustAction(t, DotsResolveUseLocal)
	if !resolveLocal.Requires(RequiresToolName) || !hasCLICommand(resolveLocal, []string{"dots", "resolve"}) || !hasCLIFlag(resolveLocal, "--use-local") {
		t.Fatalf("%s must expose dots resolve --use-local for one entry: %+v", resolveLocal.ID, resolveLocal)
	}
	if resolveLocal.TUI == nil || resolveLocal.TUI.KeyMapField != "DotUseLocal" {
		t.Fatalf("%s should use DotUseLocal binding, got %+v", resolveLocal.ID, resolveLocal.TUI)
	}

	reminderService := mustAction(t, DotsReminder)
	for _, command := range [][]string{
		{"dots", "reminder", "install"},
		{"dots", "reminder", "uninstall"},
	} {
		if !hasCLICommand(reminderService, command) {
			t.Fatalf("%s missing service mutation command %v in %+v", reminderService.ID, command, reminderService.CLI)
		}
	}
	for _, command := range [][]string{
		{"dots", "reminder", "check"},
		{"dots", "reminder", "run"},
		{"dots", "reminder", "status"},
	} {
		if hasCLICommand(reminderService, command) {
			t.Fatalf("%s must not collapse read-only command %v into mutating service action: %+v", reminderService.ID, command, reminderService.CLI)
		}
	}

	for _, id := range []ID{
		ID("dots.reminder.check"),
		ID("dots.reminder.run"),
		ID("dots.reminder.status"),
		ID("dots.watch.status"),
		ID("dots.services.status"),
		ID("dots.history"),
	} {
		action := mustAction(t, id)
		if action.Mutates {
			t.Fatalf("%s should be read-only/non-persistent, got mutates=true", action.ID)
		}
	}

	watchService := mustAction(t, DotsWatch)
	for _, command := range [][]string{
		{"dots", "watch", "install"},
		{"dots", "watch", "uninstall"},
	} {
		if !hasCLICommand(watchService, command) {
			t.Fatalf("%s missing service mutation command %v in %+v", watchService.ID, command, watchService.CLI)
		}
	}
	for _, command := range [][]string{
		{"dots", "watch", "run"},
		{"dots", "watch", "status"},
	} {
		if hasCLICommand(watchService, command) {
			t.Fatalf("%s must not collapse command %v into service toggle action: %+v", watchService.ID, command, watchService.CLI)
		}
	}
	if watchRun := mustAction(t, ID("dots.watch.run")); !watchRun.Mutates {
		t.Fatalf("%s should be mutating because it runs sync after filesystem changes", watchRun.ID)
	}
	servicesStatus := mustAction(t, DotsServicesStatus)
	if !hasCLICommand(servicesStatus, []string{"dots", "services", "status"}) {
		t.Fatalf("%s missing combined services status CLI binding: %+v", servicesStatus.ID, servicesStatus.CLI)
	}
	history := mustAction(t, DotsHistory)
	if !hasCLICommand(history, []string{"dots", "history"}) || !hasCLIFlag(history, "--limit") || !hasCLIFlag(history, "--format") {
		t.Fatalf("%s missing dots history CLI binding: %+v", history.ID, history.CLI)
	}
	doctor := mustAction(t, Doctor)
	if doctor.Mutates {
		t.Fatalf("%s should be read-only", doctor.ID)
	}
	if !hasCLICommand(doctor, []string{"doctor"}) {
		t.Fatalf("%s missing doctor CLI binding: %+v", doctor.ID, doctor.CLI)
	}
}

func TestMutatingToolActionsHaveCLIAndTUIParity(t *testing.T) {
	for _, action := range Tools {
		if !action.Mutates {
			continue
		}
		if action.TUI == nil && action.Palette == nil {
			if action.CLIOnlyReason != "" {
				continue
			}
			t.Fatalf("%s mutates state but has no TUI or palette binding", action.ID)
		}
		if len(action.CLI) == 0 {
			t.Fatalf("%s mutates state but has no CLI binding", action.ID)
		}
		if action.Requires(RequiresGroupAssignment) && !hasAnyCLICommand(action, [][]string{{"tools", "add"}, {"groups", "move-tool"}, {"groups", "remove-tool"}}) {
			t.Fatalf("%s requires group assignment but has no explicit group CLI: %+v", action.ID, action.CLI)
		}
		if action.Requires(RequiresIgnoreScope) && !hasAnyCLICommand(action, [][]string{{"tools", "ignore"}, {"groups", "ignore-tool"}}) {
			t.Fatalf("%s requires ignore scope but has no scoped ignore CLI: %+v", action.ID, action.CLI)
		}
		if action.Requires(RequiresProviderScope) && !hasCLICommand(action, []string{"tools", "set"}) {
			t.Fatalf("%s requires provider scope but has no logical tools CLI: %+v", action.ID, action.CLI)
		}
	}
}

func TestMutatingDotsActionsHaveCLIAndTUIParity(t *testing.T) {
	for _, action := range Dots {
		if !action.Mutates {
			continue
		}
		if action.TUI == nil && action.Palette == nil {
			if action.CLIOnlyReason != "" {
				continue
			}
			t.Fatalf("%s mutates state but has no TUI or palette binding", action.ID)
		}
		if len(action.CLI) == 0 {
			t.Fatalf("%s mutates state but has no CLI binding", action.ID)
		}
		if action.Requires(RequiresGroupAssignment) && !hasAnyCLICommand(action, [][]string{{"dots", "add"}, {"dots", "groups"}}) {
			t.Fatalf("%s requires group assignment but has no explicit dots group CLI: %+v", action.ID, action.CLI)
		}
	}
}

func hasCLICommand(action Action, command []string) bool {
	for _, binding := range action.CLI {
		if len(binding.Command) != len(command) {
			continue
		}
		matches := true
		for i := range command {
			if binding.Command[i] != command[i] {
				matches = false
				break
			}
		}
		if matches {
			return true
		}
	}
	return false
}

func hasAnyCLICommand(action Action, commands [][]string) bool {
	for _, command := range commands {
		if hasCLICommand(action, command) {
			return true
		}
	}
	return false
}

func mustAction(t *testing.T, id ID) Action {
	t.Helper()
	action, ok := Get(id)
	if !ok {
		t.Fatalf("missing action %s", id)
	}
	return action
}

func hasCLIFlag(action Action, flag string) bool {
	for _, binding := range action.CLI {
		for _, f := range binding.Flags {
			if f == flag {
				return true
			}
		}
	}
	return false
}
