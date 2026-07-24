//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestReplayOp_CreateSession(t *testing.T) {
	op := ReplayOp{
		Type: OpCreateSession,
		Key:  session.Key{AppName: "test", UserID: "u1", SessionID: "s1"},
	}
	if op.Type != OpCreateSession {
		t.Errorf("expected OpCreateSession, got %s", op.Type)
	}
	if op.Key.AppName != "test" {
		t.Errorf("expected AppName 'test', got %q", op.Key.AppName)
	}
}

func TestReplayOp_AppendEvent(t *testing.T) {
	op := ReplayOp{
		Type: OpAppendEvent,
		Key:  session.Key{AppName: "test", UserID: "u1", SessionID: "s1"},
		Data: EventData{Event: NewEvent("inv1", "user", "user", "hello")},
	}
	if op.Type != OpAppendEvent {
		t.Errorf("expected OpAppendEvent, got %s", op.Type)
	}
}

func TestReplayCase_AllOpTypes(t *testing.T) {
	allTypes := []OpType{
		OpCreateSession, OpAppendEvent, OpUpdateSessionState,
		OpDeleteSessionState, OpAddMemory, OpUpdateMemory,
		OpDeleteMemory, OpClearMemories, OpCreateSessionSummary,
		OpGetSession, OpAppendTrackEvent, OpGetSessionSummaryText,
		OpReadMemories, OpSearchMemories,
	}

	seen := make(map[OpType]bool)
	for _, ot := range allTypes {
		if seen[ot] {
			t.Errorf("duplicate OpType: %s", ot)
		}
		seen[ot] = true
	}

	// Verify the count matches our defined ops
	if len(allTypes) != 14 {
		t.Errorf("expected 14 OpTypes, got %d", len(allTypes))
	}
}

func TestBackendResult_Fields(t *testing.T) {
	r := &BackendResult{
		BackendName:  "test",
		SummaryTexts: map[string]string{"": "summary"},
	}
	if r.BackendName != "test" {
		t.Errorf("expected BackendName 'test', got %q", r.BackendName)
	}
	if r.SummaryTexts[""] != "summary" {
		t.Errorf("expected summary text 'summary', got %q", r.SummaryTexts[""])
	}
}
