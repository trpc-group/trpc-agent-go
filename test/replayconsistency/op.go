//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replayconsistency

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Op is one step of a replay script.
//
// Ops are declarative values rather than closures so that the same script can
// be printed, reasoned about, and replayed against every backend. The apply
// method is unexported, which keeps the set of operations closed to this
// package and keeps the exported surface to the data itself.
type Op interface {
	// Describe returns a short label used in progress output and in the
	// failure message when an op returns an error.
	Describe() string
	apply(ctx context.Context, tgt *target) error
}

// CreateSession creates a session, optionally seeding its state.
type CreateSession struct {
	Ref   SessionRef
	State map[string][]byte
}

// Describe implements Op.
func (o CreateSession) Describe() string { return "create session " + o.Ref.String() }

func (o CreateSession) apply(ctx context.Context, tgt *target) error {
	_, err := tgt.session.CreateSession(ctx, o.Ref.Key(), session.StateMap(o.State))
	return err
}

// DeleteSession removes a session and everything scoped to it.
type DeleteSession struct {
	Ref SessionRef
}

// Describe implements Op.
func (o DeleteSession) Describe() string { return "delete session " + o.Ref.String() }

func (o DeleteSession) apply(ctx context.Context, tgt *target) error {
	return tgt.session.DeleteSession(ctx, o.Ref.Key())
}

// AppendEvent appends one event to a session.
//
// The session value is re-read before every append rather than cached, so the
// harness exercises each backend's real read path and never lets a stale
// in-process copy paper over a persistence bug.
type AppendEvent struct {
	Ref   SessionRef
	Event EventSpec
}

// Describe implements Op.
func (o AppendEvent) Describe() string {
	return fmt.Sprintf("append event %s to %s", o.Event.ID, o.Ref)
}

func (o AppendEvent) apply(ctx context.Context, tgt *target) error {
	sess, err := tgt.getSession(ctx, o.Ref)
	if err != nil {
		return err
	}
	evt, err := o.Event.Build(tgt.base)
	if err != nil {
		return err
	}
	return tgt.session.AppendEvent(ctx, sess, evt)
}

// UpdateSessionState writes session-scoped state keys.
//
// The session service rejects the app: and user: prefixes here on purpose;
// those scopes have their own operations.
type UpdateSessionState struct {
	Ref   SessionRef
	State map[string][]byte
}

// Describe implements Op.
func (o UpdateSessionState) Describe() string { return "update session state " + o.Ref.String() }

func (o UpdateSessionState) apply(ctx context.Context, tgt *target) error {
	return tgt.session.UpdateSessionState(ctx, o.Ref.Key(), session.StateMap(o.State))
}

// UpdateAppState writes app-scoped state shared by every user of the app.
type UpdateAppState struct {
	Ref   SessionRef
	State map[string][]byte
}

// Describe implements Op.
func (o UpdateAppState) Describe() string { return "update app state " + o.Ref.AppName }

func (o UpdateAppState) apply(ctx context.Context, tgt *target) error {
	return tgt.session.UpdateAppState(ctx, o.Ref.AppName, session.StateMap(o.State))
}

// DeleteAppState removes one app-scoped key. Unlike a nil state delta, this
// removes the key rather than storing a nil value under it.
type DeleteAppState struct {
	Ref SessionRef
	Key string
}

// Describe implements Op.
func (o DeleteAppState) Describe() string { return "delete app state " + o.Key }

func (o DeleteAppState) apply(ctx context.Context, tgt *target) error {
	return tgt.session.DeleteAppState(ctx, o.Ref.AppName, o.Key)
}

// UpdateUserState writes user-scoped state shared by every session of a user.
type UpdateUserState struct {
	Ref   SessionRef
	State map[string][]byte
}

// Describe implements Op.
func (o UpdateUserState) Describe() string { return "update user state " + o.Ref.UserID }

func (o UpdateUserState) apply(ctx context.Context, tgt *target) error {
	return tgt.session.UpdateUserState(ctx, o.Ref.UserKey(), session.StateMap(o.State))
}

// DeleteUserState removes one user-scoped key.
type DeleteUserState struct {
	Ref SessionRef
	Key string
}

// Describe implements Op.
func (o DeleteUserState) Describe() string { return "delete user state " + o.Key }

func (o DeleteUserState) apply(ctx context.Context, tgt *target) error {
	return tgt.session.DeleteUserState(ctx, o.Ref.UserKey(), o.Key)
}

// AppendTrackEvent appends an entry to an observability track. Backends that
// do not implement session.TrackService report the track capability as
// unsupported and skip the op instead of failing.
type AppendTrackEvent struct {
	Ref   SessionRef
	Event TrackEventSpec
}

// Describe implements Op.
func (o AppendTrackEvent) Describe() string {
	return fmt.Sprintf("append track event %s to %s", o.Event.Track, o.Ref)
}

func (o AppendTrackEvent) apply(ctx context.Context, tgt *target) error {
	tracker, ok := tgt.session.(session.TrackService)
	if !ok {
		return nil
	}
	sess, err := tgt.getSession(ctx, o.Ref)
	if err != nil {
		return err
	}
	evt, err := o.Event.Build(tgt.base)
	if err != nil {
		return err
	}
	return tracker.AppendTrackEvent(ctx, sess, evt)
}

// CreateSummary drives summary generation through the synchronous service
// path.
//
// EnqueueSummaryJob is deliberately not used: it dispatches to a worker pool,
// and racing the pool would make the comparison flaky for reasons unrelated to
// backend behavior. The text a summary receives comes from the scenario's
// deterministic summarizer, so no model API key is required.
type CreateSummary struct {
	Ref     SessionRef
	Summary SummarySpec
}

// Describe implements Op.
func (o CreateSummary) Describe() string {
	return fmt.Sprintf("summarize %s filterKey=%q", o.Ref, o.Summary.FilterKey)
}

func (o CreateSummary) apply(ctx context.Context, tgt *target) error {
	sess, err := tgt.getSession(ctx, o.Ref)
	if err != nil {
		return err
	}
	tgt.summarizer.setSpec(o.Summary)
	return tgt.session.CreateSessionSummary(ctx, sess, o.Summary.FilterKey, o.Summary.Force)
}

// AddMemory writes one memory for the user.
type AddMemory struct {
	Ref    SessionRef
	Memory MemorySpec
}

// Describe implements Op.
func (o AddMemory) Describe() string { return "add memory " + o.Memory.Content }

func (o AddMemory) apply(ctx context.Context, tgt *target) error {
	if tgt.memory == nil {
		return nil
	}
	return tgt.memory.AddMemory(ctx, o.Ref.MemoryUserKey(), o.Memory.Content, o.Memory.Topics)
}

// UpdateMemory rewrites the memory whose current content equals MatchContent.
//
// Memory identifiers are content-derived hashes produced inside the memory
// package, so a script cannot name one up front. Resolving the target by
// content keeps the script declarative and exercises the read path as a side
// effect. Updating content rotates the identifier, which is itself a behavior
// worth comparing across backends.
type UpdateMemory struct {
	Ref          SessionRef
	MatchContent string
	Memory       MemorySpec
}

// Describe implements Op.
func (o UpdateMemory) Describe() string { return "update memory " + o.MatchContent }

func (o UpdateMemory) apply(ctx context.Context, tgt *target) error {
	if tgt.memory == nil {
		return nil
	}
	id, err := tgt.resolveMemoryID(ctx, o.Ref, o.MatchContent)
	if err != nil {
		return err
	}
	key := memory.Key{AppName: o.Ref.AppName, UserID: o.Ref.UserID, MemoryID: id}
	return tgt.memory.UpdateMemory(ctx, key, o.Memory.Content, o.Memory.Topics)
}

// DeleteMemory removes the memory whose current content equals MatchContent.
type DeleteMemory struct {
	Ref          SessionRef
	MatchContent string
}

// Describe implements Op.
func (o DeleteMemory) Describe() string { return "delete memory " + o.MatchContent }

func (o DeleteMemory) apply(ctx context.Context, tgt *target) error {
	if tgt.memory == nil {
		return nil
	}
	id, err := tgt.resolveMemoryID(ctx, o.Ref, o.MatchContent)
	if err != nil {
		return err
	}
	key := memory.Key{AppName: o.Ref.AppName, UserID: o.Ref.UserID, MemoryID: id}
	return tgt.memory.DeleteMemory(ctx, key)
}
