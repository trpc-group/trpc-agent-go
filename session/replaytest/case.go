//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

// OpKind identifies the kind of a Step operation.
type OpKind string

const (
	// OpCreateSession creates a session with optional initial state.
	OpCreateSession OpKind = "create_session"
	// OpAppendEvent appends one event built from Step.Event.
	OpAppendEvent OpKind = "append_event"
	// OpUpdateState merges Step.State into the session state.
	OpUpdateState OpKind = "update_session_state"
	// OpUpdateAppState merges Step.State into the app state.
	OpUpdateAppState OpKind = "update_app_state"
	// OpDeleteAppState deletes Step.StateKeys from the app state.
	OpDeleteAppState OpKind = "delete_app_state"
	// OpUpdateUserState merges Step.State into the user state.
	OpUpdateUserState OpKind = "update_user_state"
	// OpDeleteUserState deletes Step.StateKeys from the user state.
	OpDeleteUserState OpKind = "delete_user_state"
	// OpAddMemory adds one memory from Step.Memory.
	OpAddMemory OpKind = "add_memory"
	// OpUpdateMemory updates the memory whose content matches
	// Step.Memory.MatchContent.
	OpUpdateMemory OpKind = "update_memory"
	// OpDeleteMemory deletes the memory whose content matches
	// Step.Memory.MatchContent.
	OpDeleteMemory OpKind = "delete_memory"
	// OpClearMemories clears all memories of the case user.
	OpClearMemories OpKind = "clear_memories"
	// OpSummary forces a deterministic summary write for Step.Summary.
	OpSummary OpKind = "create_summary"
	// OpAppendTrack appends one track event from Step.Track.
	OpAppendTrack OpKind = "append_track"
	// OpConcurrentEvents appends events from several writers concurrently.
	OpConcurrentEvents OpKind = "concurrent_events"
)

// Case is a deterministic script replayed against every target.
//
// Cases must be fully deterministic: no wall clock, no randomness and no
// real LLM calls. The runner assigns deterministic event IDs and timestamps.
type Case struct {
	// Name is the stable case ID, e.g. "summary/overwrite_filterkey".
	Name string
	// Description documents the consistency risk this case targets.
	Description string
	// NeedCaps declares the capabilities required to run the case.
	NeedCaps Capability
	// UnorderedEvents switches event comparison to multiset plus
	// per-branch partial order (for concurrent writers).
	UnorderedEvents bool
	// FloatDelta, when positive, tolerates numeric differences up to this
	// absolute delta inside compared JSON values (tool call args, state,
	// state delta, extensions, track payloads). Tolerated differences are
	// reported as allowed notes, not failures. Zero (the default) compares
	// numbers exactly after normalization. Use it for values with inherent
	// float round-trip noise, e.g. numbers persisted through a REAL column.
	FloatDelta float64
	// WindowEventNum, when positive, makes the snapshot include a second
	// read-back of every created session with session.WithEventNum — the
	// truncated context-window view used when replaying a compacted
	// conversation. It appears as an extra session whose ID carries a
	// "@last<N>" suffix and is compared like any other session.
	WindowEventNum int
	// SearchQuery, when non-empty and the target supports memory search,
	// makes the snapshot include SearchMemories results for this query.
	SearchQuery string
	// Steps are applied in order.
	Steps []Step
}

// Step is one deterministic operation against the target.
//
// Field contract: Op is always required. For each Op exactly one field
// group is consumed and the others are ignored (nil is the normal,
// expected value for them):
//
//   - OpCreateSession: SessionID (required), State (optional initial state)
//   - OpAppendEvent: SessionID, Event (required, must be non-nil)
//   - OpUpdateState/OpUpdateAppState/OpUpdateUserState: SessionID, State
//   - OpDeleteAppState/OpDeleteUserState: StateKeys
//   - OpAddMemory/OpUpdateMemory/OpDeleteMemory/OpClearMemories: Memory
//     (required, must be non-nil)
//   - OpSummary: SessionID (must name a session created by an earlier
//     step), Summary (required, must be non-nil)
//   - OpAppendTrack: SessionID (created earlier), Track (required)
//   - OpConcurrentEvents: SessionID (created earlier), Concurrent
//
// Violating a requirement fails the case run with an error naming the
// step; it never produces a silent pass.
type Step struct {
	Op OpKind
	// SessionID identifies the session inside the case (app and user are
	// fixed by the runner per case execution).
	SessionID string
	// Event is used by OpAppendEvent.
	Event *EventSpec
	// State carries raw JSON values (strings without JSON syntax are
	// encoded as JSON strings) for the state operations.
	State map[string]string
	// StateKeys lists keys to delete for the delete operations.
	StateKeys []string
	// Memory is used by the memory operations.
	Memory *MemorySpec
	// Summary is used by OpSummary.
	Summary *SummarySpec
	// Track is used by OpAppendTrack.
	Track *TrackSpec
	// Concurrent is used by OpConcurrentEvents.
	Concurrent []WriterSpec
}

// EventSpec describes one event to append. Zero values pass through as
// empty fields; no field is required for a well-formed append, though
// cases normally set Author, Role and Content.
type EventSpec struct {
	Author       string
	Role         string // "user", "assistant", "tool", ...
	Content      string
	ToolCalls    []ToolCallSpec
	ToolID       string // tool response: referenced tool call ID
	ToolName     string // tool response: tool name
	Branch       string
	Tag          string
	FilterKey    string
	StateDelta   map[string]string // raw JSON values
	Extensions   map[string]string // raw JSON values
	InvocationID string
	RequestID    string
	FinishReason string
	// FailTimes routes the append through a service wrapper that fails this
	// many times with a transient error before the real write, exercising
	// the client fail/retry path through the service interface.
	FailTimes int
	// ExpectError records the backend error class instead of failing the
	// case when the append returns an error (e.g. unknown session).
	ExpectError bool
}

// ToolCallSpec describes one tool call inside an assistant event.
type ToolCallSpec struct {
	// ID is the tool call ID. It is symbolized (call#N) during
	// normalization, so any stable per-case value works; tool responses
	// reference it via EventSpec.ToolID.
	ID   string
	Name string
	Args string // raw JSON
}

// MemorySpec describes a memory write. MatchContent identifies an existing
// memory by content for update/delete operations.
type MemorySpec struct {
	// UserID overrides the runner default user for this step, letting a
	// case exercise (app, user) scope isolation. Empty means CaseUserID.
	UserID       string
	MatchContent string
	Content      string
	Topics       []string
	// Metadata carries episodic fields (kind/participants/location).
	Metadata *MetadataSpec
}

// MetadataSpec describes episodic memory metadata.
type MetadataSpec struct {
	Kind         string
	Participants []string
	Location     string
}

// SummarySpec describes a forced summary write.
type SummarySpec struct {
	// FilterKey selects the summary scope; empty means the full-session
	// summary.
	FilterKey string
}

// TrackSpec describes one track event.
type TrackSpec struct {
	Track   string
	Payload string // raw JSON
}

// WriterSpec describes one concurrent writer goroutine.
type WriterSpec struct {
	Branch string
	// Author defaults to "replay" when empty.
	Author string
	Prefix string
	// Count is the number of events this writer appends; values <= 0
	// produce no events.
	Count int
}
