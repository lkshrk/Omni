package app

import "github.com/lkshrk/omni/internal/dots"

// Type aliases so App's own dots-typed signatures accept them unchanged; the Dot prefix disambiguates from App's state types.

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

type (
	DotSyncOptions       = dots.SyncOptions
	DotSyncProgressEvent = dots.SyncProgressEvent
)

var DotExpandPath = dots.ExpandPath
