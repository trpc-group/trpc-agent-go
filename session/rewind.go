//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
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
	// ErrRewindUnsupported indicates that a session service cannot rewind a
	// session projection.
	ErrRewindUnsupported = errors.New("session rewind is unsupported")
	// ErrRewindConflict indicates that the active session head no longer
	// matches the head observed by the caller, or that an idempotency key was
	// reused for a different rewind request.
	ErrRewindConflict = errors.New("session rewind conflict")
	// ErrRewindUnavailable indicates that the requested boundary is not
	// retained or cannot be restored without risking an inconsistent session
	// projection.
	ErrRewindUnavailable = errors.New("session rewind is unavailable")
)

// RewindRequest identifies a session boundary and the head from which it may
// be restored. All fields are required.
type RewindRequest struct {
	// Key identifies the session to rewind.
	Key Key
	// TargetRequestID identifies the request whose pre-request boundary is
	// restored. Request IDs must be unique within a Session;
	// an implementation must fail closed when reuse makes the target ambiguous.
	TargetRequestID string
	// ExpectedHeadRequestID identifies the latest request observed by the
	// caller. A different active head causes ErrRewindConflict.
	ExpectedHeadRequestID string
	// IdempotencyKey uniquely identifies this rewind operation and must not be
	// recycled for another operation. Retrying an outcome-unknown call requires
	// the same complete request, including this key. While its idempotency record
	// is retained, reuse with different parameters causes ErrRewindConflict.
	IdempotencyKey string
}

// RewindResult contains the authoritative active projection after a rewind.
type RewindResult struct {
	// Session is a fresh, non-nil projection on success.
	Session *Session
}

// RewindService is the optional session capability for atomically restoring a
// retained pre-request boundary.
//
// A successful call restores the Session-owned projection, including Events,
// session-scoped State, Summaries, Tracks, and timestamps, while retaining
// app- and user-scoped state and the Session's remaining TTL. Implementations
// must reject framework-owned writes that carry a superseded revision so they
// cannot repopulate the restored history. The caller must discard older
// Session values and continue with RewindResult.Session. That Session must
// fence its first persistable event append against the authoritative revision
// returned by Rewind: if another writer changes the revision first, the append
// must make no partial mutation and return ErrRewindConflict.
//
// Repeating the exact same request after an outcome-unknown error must not
// apply the rewind twice. Implementations may retain only a bounded set of
// boundaries and idempotency records; a missing target returns
// ErrRewindUnavailable. RewindService intentionally does not embed Service so
// existing custom Service implementations remain source compatible.
type RewindService interface {
	Rewind(context.Context, RewindRequest) (*RewindResult, error)
}
