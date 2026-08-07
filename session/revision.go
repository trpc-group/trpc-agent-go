//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package session

import (
	"context"
	"errors"
)

var (
	// ErrLatestTurnReplacementUnsupported indicates that a session service
	// cannot replace the latest persisted turn.
	ErrLatestTurnReplacementUnsupported = errors.New(
		"latest-turn replacement is unsupported",
	)
	// ErrLatestTurnReplacementConflict indicates that the active latest turn
	// no longer matches the turn observed by the caller.
	ErrLatestTurnReplacementConflict = errors.New(
		"latest-turn replacement conflict",
	)
	// ErrLatestTurnReplacementUnavailable indicates that the latest turn cannot
	// be replaced without risking an inconsistent session projection.
	ErrLatestTurnReplacementUnavailable = errors.New(
		"latest-turn replacement is unavailable",
	)
)

// LatestTurnReplacementRequest describes the backend transition requested by
// Runner when a caller edits and resends the latest persisted turn. Key and
// both request identifiers are required. IdempotencyKey identifies the new run
// and must be reused when confirming an outcome-unknown replacement.
type LatestTurnReplacementRequest struct {
	Key               Key
	ExpectedRequestID string
	IdempotencyKey    string
}

// LatestTurnReplacementResult describes the authoritative active projection
// after a latest-turn replacement. ActiveSession is always a fresh, non-nil
// session on success. Applied is false only for a durable idempotent replay.
type LatestTurnReplacementResult struct {
	ActiveSession *Session
	Applied       bool
}

// LatestTurnReplacer is an optional storage capability used by Runner to
// restore the checkpoint immediately before the latest persisted turn.
// Implementations must replace events, session state, summaries, and tracks as
// one logical transition, fence writes from the discarded projection, and
// retain enough durable state to confirm retries by IdempotencyKey.
//
// Application code should normally use Runner.Run with
// agent.WithLatestTurnReplacement instead of invoking this SPI directly.
type LatestTurnReplacer interface {
	ReplaceLatestTurn(
		context.Context,
		LatestTurnReplacementRequest,
	) (*LatestTurnReplacementResult, error)
}
