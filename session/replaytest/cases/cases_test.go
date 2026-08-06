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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
)

// TestAllReturnsElevenWellFormedCases verifies the public suite size and
// that every case is uniquely named and carries at least one step.
func TestAllReturnsElevenWellFormedCases(t *testing.T) {
	all := All()
	require.Len(t, all, 11)
	seen := make(map[string]bool, len(all))
	for _, c := range all {
		assert.NotEmpty(t, c.Name)
		assert.NotEmpty(t, c.Description, "case %s", c.Name)
		assert.NotEmpty(t, c.Steps, "case %s", c.Name)
		assert.False(t, seen[c.Name], "duplicate case name %q", c.Name)
		seen[c.Name] = true
	}
}

// TestAllMatchesDocumentedOrder pins the slice order to the README's case
// numbering (memory/scope_isolation is case 11 and comes last): RunPair
// preserves this order in reports, so a silent reorder would renumber every
// later case in the public documentation.
func TestAllMatchesDocumentedOrder(t *testing.T) {
	want := []string{
		"basic/single_turn",
		"basic/multi_turn_order",
		"toolcall/full_cycle",
		"state/overwrite_delete_clear",
		"memory/write_read",
		"summary/generate_update",
		"summary/truncation_retain",
		"track/tool_and_subtask",
		"concurrency/interleaved_append",
		"recovery/dirty_retry",
		"memory/scope_isolation",
	}
	all := All()
	require.Len(t, all, len(want))
	for i, c := range all {
		assert.Equal(t, want[i], c.Name, "case %d", i+1)
	}
}

// TestAllConstructorsDeterministic calls every constructor twice and
// requires a deep-equal result: cases must be pure functions.
func TestAllConstructorsDeterministic(t *testing.T) {
	assert.Equal(t, All(), All())
	assert.Equal(t, SingleTurn(), SingleTurn())
	assert.Equal(t, MultiTurnOrder(), MultiTurnOrder())
	assert.Equal(t, ToolCallFullCycle(), ToolCallFullCycle())
	assert.Equal(t, StateOverwriteDeleteClear(), StateOverwriteDeleteClear())
	assert.Equal(t, MemoryWriteRead(), MemoryWriteRead())
	assert.Equal(t, MemoryScopeIsolation(), MemoryScopeIsolation())
	assert.Equal(t, SummaryGenerateUpdate(), SummaryGenerateUpdate())
	assert.Equal(t, SummaryTruncationRetain(), SummaryTruncationRetain())
	assert.Equal(t, TrackToolAndSubtask(), TrackToolAndSubtask())
	assert.Equal(t, ConcurrencyInterleavedAppend(), ConcurrencyInterleavedAppend())
	assert.Equal(t, RecoveryDirtyRetry(), RecoveryDirtyRetry())
}

// TestAllStepOpsAreRegistered ensures every step in every public case uses
// one of the OpKind constants the runner understands.
func TestAllStepOpsAreRegistered(t *testing.T) {
	registered := map[replaytest.OpKind]bool{
		replaytest.OpCreateSession:    true,
		replaytest.OpAppendEvent:      true,
		replaytest.OpUpdateState:      true,
		replaytest.OpUpdateAppState:   true,
		replaytest.OpDeleteAppState:   true,
		replaytest.OpUpdateUserState:  true,
		replaytest.OpDeleteUserState:  true,
		replaytest.OpAddMemory:        true,
		replaytest.OpUpdateMemory:     true,
		replaytest.OpDeleteMemory:     true,
		replaytest.OpClearMemories:    true,
		replaytest.OpSummary:          true,
		replaytest.OpAppendTrack:      true,
		replaytest.OpConcurrentEvents: true,
	}
	for _, c := range All() {
		for i, st := range c.Steps {
			assert.True(t, registered[st.Op],
				"case %s step %d uses unregistered op %q", c.Name, i, st.Op)
		}
	}
}

// TestStepBuilders covers the step-construction helpers directly.
func TestStepBuilders(t *testing.T) {
	st := createSession("s1")
	assert.Equal(t, replaytest.OpCreateSession, st.Op)
	assert.Equal(t, "s1", st.SessionID)
	assert.Nil(t, st.State)

	st = createSessionWithState("s2", map[string]string{"k": "1"})
	assert.Equal(t, replaytest.OpCreateSession, st.Op)
	assert.Equal(t, "s2", st.SessionID)
	assert.Equal(t, map[string]string{"k": "1"}, st.State)

	u := userMsg("s1", "inv-1", "hello")
	assert.Equal(t, replaytest.OpAppendEvent, u.Op)
	assert.Equal(t, "s1", u.SessionID)
	require.NotNil(t, u.Event)
	assert.Equal(t, "user", u.Event.Author)
	assert.Equal(t, "user", u.Event.Role)
	assert.Equal(t, "hello", u.Event.Content)
	assert.Equal(t, "inv-1", u.Event.InvocationID)

	a := assistantMsg("s1", "inv-1", "hi")
	assert.Equal(t, replaytest.OpAppendEvent, a.Op)
	require.NotNil(t, a.Event)
	assert.Equal(t, "assistant", a.Event.Author)
	assert.Equal(t, "assistant", a.Event.Role)
	assert.Equal(t, "hi", a.Event.Content)
	assert.Equal(t, "stop", a.Event.FinishReason)

	s := summaryStep("s1", "fk")
	assert.Equal(t, replaytest.OpSummary, s.Op)
	assert.Equal(t, "s1", s.SessionID)
	require.NotNil(t, s.Summary)
	assert.Equal(t, "fk", s.Summary.FilterKey)
}

// TestSeqEvents verifies the alternating user/assistant turn generator.
func TestSeqEvents(t *testing.T) {
	assert.Empty(t, seqEvents("s1", 0))

	steps := seqEvents("s1", 3)
	require.Len(t, steps, 6)
	for i, st := range steps {
		assert.Equal(t, replaytest.OpAppendEvent, st.Op)
		assert.Equal(t, "s1", st.SessionID)
		require.NotNil(t, st.Event)
		wantAuthor := "user"
		if i%2 == 1 {
			wantAuthor = "assistant"
		}
		assert.Equal(t, wantAuthor, st.Event.Author, "step %d", i)
	}
	assert.Equal(t, "inv-s1-01", steps[0].Event.InvocationID)
	assert.Equal(t, "seq-01-user", steps[0].Event.Content)
	assert.Equal(t, "seq-01-assistant", steps[1].Event.Content)
}
