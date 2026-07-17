# Omni Launch Update Design

**Decision:** Reuse the normal launch refresh and script-provider lifecycle.
Do not build a second GitHub release checker, update journal, downloader, or
scheduler for Omni itself.

## Why this is enough

The TUI already refreshes configured tools after loading its cached startup
snapshot:

```text
handleToolsLoadedMsg
  -> startPostLoadBackgroundTasks
  -> startCurrentProviderScans
  -> doScanProvider
  -> RefreshProviderInstalledWithProgress
  -> handleProviderScannedMsg
  -> startProviderOutdatedChecks
  -> RefreshProviderOutdated
  -> doFetchOutdatedTools
  -> handleOutdatedProvidersDoneMsg
```

The final `outdatedProvidersDoneMsg` contains a consistent tool snapshot after
installed-version and outdated-version checks have completed. That is the seam
for deciding whether the configured logical tool named `omni` is outdated.

The separate updater design would duplicate behavior the tool lifecycle already
owns:

- installed-version detection
- latest-version/outdated detection
- provider-specific upgrade commands
- progress and error handling
- cache persistence
- post-upgrade verification
- CLI/TUI rendering

## Dependency on the script-provider work

This design assumes the concurrent script-provider change supplies the normal
provider contracts needed by refresh:

1. The script recipe can report the installed version.
2. The script recipe can determine the latest version or otherwise report that
   the installed version is outdated.
3. `RefreshProviderOutdated` persists `Outdated` and `LatestVersion` for script
   tools through the same database path as other providers.
4. The script upgrade path runs the configured upgrade command and verifies the
   installed version afterward.

The self-update work should integrate only after that contract lands. It should
not invent a parallel version parser or GitHub client while the provider work is
still in flight.

## Omni is a normal configured tool

Omni must be present in the resolved tool set as logical tool `omni` with a
script provider. Its recipe defines the correct installation channel for that
host.

Conceptually:

```json
{
  "tools": {
    "omni": {
      "providers": [
        {
          "provider": "script",
          "options": {
            "detect": "omni",
            "version": "omni --version",
            "latest": "<channel-specific latest-version command>",
            "upgrade": "<channel-specific upgrade command>"
          }
        }
      ]
    }
  }
}
```

The exact option names come from the script-provider implementation and should
not be duplicated here.

This explicit recipe is also the update-authority boundary:

- Homebrew installs can use `brew upgrade omni`.
- Direct installs can invoke the maintained install script or a verified direct
  download command.
- Other package managers can use their own command.
- Hosts without a valid recipe do not self-update.

Omni should not infer its installation channel from `os.Executable`, path
writability, or directory names. The user-configured script recipe already says
how this tool is owned and upgraded.

## Launch behavior

Extend `handleOutdatedProvidersDoneMsg` after it applies `msg.tools`:

```go
if omni, ok := findTool(msg.tools, "omni"); ok && omni.Outdated {
    // notify, or enqueue the existing single-tool upgrade path
}
```

The actual implementation should use an app helper rather than teaching the TUI
how to interpret provider rows:

```go
type SelfUpdateStatus struct {
    Configured    bool
    Installed     bool
    Outdated      bool
    Current       string
    Latest        string
    CanUpgrade    bool
}

func SelfUpdateStatusFromTools(tools []*database.ToolCache) SelfUpdateStatus
```

The helper is pure and consumes the already-refreshed snapshot. It performs no
I/O and makes no network request.

### Notify mode

When `omni` is outdated, render a non-modal TUI notice such as:

```text
Omni v1.3.0 -> v1.4.0 available
```

The normal tool row remains the source of detailed status and the existing
single-tool upgrade action remains the manual apply path.

### Auto mode

If automatic self-update is enabled, enqueue the existing single-tool upgrade
operation for logical tool `omni` after the launch refresh settles.

Do not call the script command directly from the message handler. Reuse the
same app lifecycle operation used by `tools upgrade omni`, including progress,
privilege handling, cache refresh, version verification, and error reporting.

The running process may continue at its old mapped version. A successful
upgrade takes effect on the next Omni launch. The TUI should say so explicitly:

```text
Updated Omni to v1.4.0; restart to use it
```

## Policy

The smallest policy is host-specific:

```json
{
  "host_settings": {
    "workstation": {
      "omni_update": "notify"
    }
  }
}
```

Accepted values:

- `off`: do nothing after refresh
- `notify`: show the outdated result; default
- `auto`: run the configured script-provider upgrade after refresh

`auto` is meaningful only when the `omni` tool has a usable script upgrade
recipe. Missing configuration degrades to no action, not an error.

If adding a setting is premature, ship notification first with no new schema
field and keep auto-update for a follow-up.

## Guardrails

- Wait for the normal outdated refresh; never issue an extra launch-time version
  request.
- Do not add a second update cache or TTL. Provider refresh owns freshness.
- Do not add install-provenance detection. The configured script recipe owns
  upgrade behavior.
- Do not automatically add or rewrite the user's `omni` tool recipe on launch.
- Do not auto-update `dev`, dirty, malformed, or versionless builds.
- Do not begin auto-update during onboarding, while a provider refresh is still
  active, or when the refreshed snapshot has an error.
- Run at most once per launch generation and prevent collision with another
  active tool operation.
- Never place update notices on stdout/stderr of ordinary CLI commands. This
  integration is TUI-only; `tools upgrade omni` remains the explicit CLI path.
- Respect the existing privilege flow. A script recipe that requires elevation
  must not bypass the Admin Terminal or confirmation behavior.
- A failed self-update is an ordinary tool-operation error and must leave the
  TUI running.

## Implementation shape

The self-update-specific code should stay small:

1. A pure app helper that extracts Omni update status from a refreshed tool
   snapshot.
2. TUI state for one launch notification / auto-update attempt.
3. A hook in the final outdated-snapshot handler.
4. A call into the existing single-tool upgrade lifecycle when policy is
   `auto`.
5. Focused settings/schema work only if `off|notify|auto` ships now.

No new release client, downloader, scheduler, lock, update journal, rollback
engine, or installer abstraction is required.

## Tests

### Refresh integration

- A configured script-provider `omni` row participates in the normal launch
  installed and outdated scans.
- The self-update check runs only after the final refreshed snapshot arrives.
- Current, outdated, missing, unconfigured, ignored, and versionless Omni rows
  produce the expected status.
- A failed provider outdated scan does not trigger auto-update.

### TUI behavior

- `notify` renders one notice with current and latest versions.
- `off` renders nothing.
- `auto` enqueues exactly one existing upgrade operation per launch generation.
- Auto-update waits when another tool operation is active.
- Successful upgrade reports restart-required state.
- Failed upgrade reports the existing tool error and keeps the TUI usable.
- Onboarding and no-config launches never attempt self-update.

### Regression coverage

- Launch performs no additional HTTP/process call beyond the normal provider
  refresh.
- Existing provider refresh ordering and progress remain unchanged.
- Existing JSON CLI output remains byte-for-byte unchanged.
- `--version`, help, completion, doctor, and non-TUI commands gain no automatic
  network behavior.
- Manual `tools upgrade omni` and automatic launch upgrade use the same app
  lifecycle and post-upgrade verification.

## Recommendation

Wait for the script-provider version/outdated work, then implement notification
from the final launch refresh snapshot. Add `auto` only by invoking the existing
single-tool upgrade lifecycle for the configured `omni` script tool. This is a
small integration, not a new updater subsystem.
