//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package cases

import (
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// StateOverwriteDeleteClear is case 4: repeated writes to the same key
// (last write wins), deletion at app/user scope, an empty-map write
// (no-op), clearing every written key back to an empty state, and
// temp:/app:/user: scoped views merged into the session.
// It guards overwrite order, delete semantics, clear-to-empty and the
// merged final state.
//
// Scope note: session.Service exposes no session-level state deletion
// (only UpdateSessionState plus app/user-scope deletes), so "clear" is
// exercised at the app and user scopes by deleting every remaining key;
// session-scope keys can only be overwritten, not removed.
func StateOverwriteDeleteClear() replaytest.Case {
	return replaytest.Case{
		Name: "state/overwrite_delete_clear",
		Description: "state overwrite order, delete semantics, empty write, " +
			"clear-to-empty at app/user scope, temp/app/user scoped merge views",
		NeedCaps: replaytest.Capability{Session: true, State: true},
		Steps: []replaytest.Step{
			createSessionWithState("state-1", map[string]string{
				"init": "1",
			}),
			{Op: replaytest.OpUpdateState, SessionID: "state-1", State: map[string]string{
				"counter":   "1",
				"temp:note": `"draft"`,
			}},
			{Op: replaytest.OpUpdateState, SessionID: "state-1", State: map[string]string{
				"counter": "2",
			}},
			{Op: replaytest.OpUpdateState, SessionID: "state-1", State: map[string]string{
				"counter": "3",
				"profile": `{"name":"lai","tags":["go","llm"]}`,
			}},
			// Empty write must be a consistent no-op.
			{Op: replaytest.OpUpdateState, SessionID: "state-1", State: map[string]string{}},
			{Op: replaytest.OpUpdateAppState, State: map[string]string{
				"cfg:theme": `"dark"`,
				"cfg:lang":  `"zh"`,
			}},
			{Op: replaytest.OpDeleteAppState, StateKeys: []string{"cfg:lang"}},
			{Op: replaytest.OpUpdateUserState, State: map[string]string{
				"level": "7",
			}},
			{Op: replaytest.OpUpdateUserState, State: map[string]string{
				"level": "8",
			}},
			{Op: replaytest.OpDeleteUserState, StateKeys: []string{"level"}},
			// Clear: delete every remaining app/user key so both scopes read
			// back empty. This is the only "clear" the service interface
			// exposes (per-key delete at app/user scope).
			{Op: replaytest.OpDeleteAppState, StateKeys: []string{"cfg:theme"}},
			{Op: replaytest.OpUpdateUserState, State: map[string]string{
				"tier": `"gold"`,
			}},
			{Op: replaytest.OpDeleteUserState, StateKeys: []string{"tier"}},
			// An event-carried state delta must merge into the session
			// state. (Only the final state is compared, so this verifies
			// the delta landed, not how many times it was applied.) The
			// session must retain a leading user message (read-side
			// filtering drops events before the first user message).
			userMsg("state-1", "inv-s1-0", "记一个状态"),
			{
				Op:        replaytest.OpAppendEvent,
				SessionID: "state-1",
				Event: &replaytest.EventSpec{
					Author:       "assistant",
					Role:         "assistant",
					Content:      "state delta applied",
					InvocationID: "inv-s1-1",
					StateDelta: map[string]string{
						"delta:key": `"v1"`,
					},
				},
			},
		},
	}
}
