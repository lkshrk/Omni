package app

import "github.com/lkshrk/omni/internal/dots"

// Re-exports of the internal/dots symbols the TUI needs, so the view layer
// depends only on internal/app (layering finding model.go:202). Type aliases
// (identical types), so App's own dots-typed signatures accept them unchanged.
// The Dot* prefix disambiguates from App's own state types (DotsState,
// ToolGroupState) — DotState/DotAction are one dot entry's sync classification
// and its resolution action, not a subsystem snapshot.

// DotState is one dot entry's sync classification.
type DotState = dots.State

const (
	DotStateSynced            = dots.StateSynced
	DotStateMissing           = dots.StateMissing
	DotStateBroken            = dots.StateBroken
	DotStateConflict          = dots.StateConflict
	DotStateModified          = dots.StateModified
	DotStateLocalOnly         = dots.StateLocalOnly
	DotStateRepoOnly          = dots.StateRepoOnly
	DotStateNoSource          = dots.StateNoSource
	DotStateUntrackedLinked   = dots.StateUntrackedLinked
	DotStateUntrackedConflict = dots.StateUntrackedConflict
	DotStateIgnored           = dots.StateIgnored
	DotStateInactive          = dots.StateInactive
	DotStateDisabled          = dots.StateDisabled
	DotStateAmbiguous         = dots.StateAmbiguous
)

// DotAction is a resolution action offered for an out-of-sync dot entry.
type DotAction = dots.Action

const (
	DotActionSync     = dots.ActionSync
	DotActionUseRepo  = dots.ActionUseRepo
	DotActionUseLocal = dots.ActionUseLocal
	DotActionRemove   = dots.ActionRemove
	DotActionIgnore   = dots.ActionIgnore
	DotActionUnignore = dots.ActionUnignore
	DotActionActivate = dots.ActionActivate
	DotActionEnable   = dots.ActionEnable
)

// DotSyncOptions / DotSyncProgressEvent carry a dots sync request and its
// per-entry progress callbacks.
type (
	DotSyncOptions       = dots.SyncOptions
	DotSyncProgressEvent = dots.SyncProgressEvent
)

// DotExpandPath expands a leading ~ and env vars in a dot entry path.
var DotExpandPath = dots.ExpandPath
