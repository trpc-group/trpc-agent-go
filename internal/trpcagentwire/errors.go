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
	// DirectRunErrorLatestTurnReplacementUnsupported identifies an unsupported
	// latest-turn replacement request.
	DirectRunErrorLatestTurnReplacementUnsupported DirectRunErrorKind = "latest_turn_replacement_unsupported"
	// DirectRunErrorLatestTurnReplacementConflict identifies a latest-turn
	// replacement whose expected turn no longer matches.
	DirectRunErrorLatestTurnReplacementConflict DirectRunErrorKind = "latest_turn_replacement_conflict"
	// DirectRunErrorLatestTurnReplacementUnavailable identifies a latest turn
	// that cannot be restored safely.
	DirectRunErrorLatestTurnReplacementUnavailable DirectRunErrorKind = "latest_turn_replacement_unavailable"
)

// DirectRunErrorKindOf returns the wire kind for a recognized direct-run
// error, or the empty kind when the error has no wire representation.
func DirectRunErrorKindOf(err error) DirectRunErrorKind {
	switch {
	case errors.Is(err, session.ErrRewindUnsupported):
		return DirectRunErrorLatestTurnReplacementUnsupported
	case errors.Is(err, session.ErrRewindConflict):
		return DirectRunErrorLatestTurnReplacementConflict
	case errors.Is(err, session.ErrRewindUnavailable):
		return DirectRunErrorLatestTurnReplacementUnavailable
	default:
		return ""
	}
}

// Sentinel returns the error represented by the wire kind, or nil when the
// kind is unknown.
func (k DirectRunErrorKind) Sentinel() error {
	switch k {
	case DirectRunErrorLatestTurnReplacementUnsupported:
		return session.ErrRewindUnsupported
	case DirectRunErrorLatestTurnReplacementConflict:
		return session.ErrRewindConflict
	case DirectRunErrorLatestTurnReplacementUnavailable:
		return session.ErrRewindUnavailable
	default:
		return nil
	}
}
