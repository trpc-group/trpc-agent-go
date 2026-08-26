//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package trpcagentwire contains contracts shared by the tRPC-Agent client
// and server implementations.
package trpcagentwire

import (
	"errors"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

// DirectRunErrorKind identifies an error returned before a remote run starts.
type DirectRunErrorKind string

const (
	directRunErrorLatestTurnReplacementInvalid     DirectRunErrorKind = "latest_turn_replacement_invalid"
	directRunErrorLatestTurnReplacementUnsupported DirectRunErrorKind = "latest_turn_replacement_unsupported"
	directRunErrorLatestTurnReplacementConflict    DirectRunErrorKind = "latest_turn_replacement_conflict"
	directRunErrorLatestTurnReplacementUnavailable DirectRunErrorKind = "latest_turn_replacement_unavailable"
)

// DirectRunErrorKindOf returns the wire kind for a recognized direct-run
// error, or the empty kind when the error has no wire representation.
func DirectRunErrorKindOf(err error) DirectRunErrorKind {
	switch {
	case errors.Is(err, session.ErrInvalidRewindRequest):
		return directRunErrorLatestTurnReplacementInvalid
	case errors.Is(err, session.ErrRewindUnsupported):
		return directRunErrorLatestTurnReplacementUnsupported
	case errors.Is(err, session.ErrRewindConflict):
		return directRunErrorLatestTurnReplacementConflict
	case errors.Is(err, session.ErrRewindUnavailable):
		return directRunErrorLatestTurnReplacementUnavailable
	default:
		return ""
	}
}

// Sentinel returns the error represented by the wire kind, or nil when the
// kind is unknown.
func (k DirectRunErrorKind) Sentinel() error {
	switch k {
	case directRunErrorLatestTurnReplacementInvalid:
		return session.ErrInvalidRewindRequest
	case directRunErrorLatestTurnReplacementUnsupported:
		return session.ErrRewindUnsupported
	case directRunErrorLatestTurnReplacementConflict:
		return session.ErrRewindConflict
	case directRunErrorLatestTurnReplacementUnavailable:
		return session.ErrRewindUnavailable
	default:
		return nil
	}
}
