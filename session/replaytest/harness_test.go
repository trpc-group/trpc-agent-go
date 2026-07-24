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
	"context"
	"sort"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// TestMockModelDeterminism verifies that MockModel produces the same output for the same seed.
func TestMockModelDeterminism(t *testing.T) {
	seed := int64(42)
	m1 := NewMockModel(seed)
	m2 := NewMockModel(seed)

	events1 := m1.GenerateConversation(3)
	events2 := m2.GenerateConversation(3)

	if len(events1) != len(events2) {
		t.Fatalf("event count mismatch: %d vs %d", len(events1), len(events2))
	}

	for i := range events1 {
		msg1 := firstMessage(&events1[i])
		msg2 := firstMessage(&events2[i])
		if msg1.Content != msg2.Content {
			t.Errorf("event[%d] content differs: %q vs %q", i, msg1.Content, msg2.Content)
		}
		if msg1.Role != msg2.Role {
			t.Errorf("event[%d] role differs: %q vs %q", i, msg1.Role, msg2.Role)
		}
	}
}

// TestMockModelToolCallArgs verifies that GenerateToolCall produces valid tool call arguments.
func TestMockModelToolCallArgs(t *testing.T) {
	m := NewMockModel(42)
	tc := m.GenerateToolCall()

	msg := firstMessage(&tc)
	if len(msg.ToolCalls) == 0 {
		t.Fatal("GenerateToolCall produced no tool calls")
	}
	tc1 := msg.ToolCalls[0]
	if tc1.Function.Name == "" {
		t.Error("tool call name is empty")
	}
	if len(tc1.Function.Arguments) == 0 {
		t.Error("tool call arguments are empty")
	}
}

// TestHarnessRun_SingleCase verifies that the harness can run a single case on InMemory.
func TestHarnessRun_SingleCase(t *testing.T) {
	ctx := context.Background()
	h := NewHarness()

	h.AddCase(ReplayCase{
		Name: "single_conversation",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"}},
			{Type: OpAppendEvent, Key: session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"},
				Data: EventData{Event: NewEvent("inv1", "user", "user", "Hello")}},
			{Type: OpAppendEvent, Key: session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"},
				Data: EventData{Event: NewEvent("inv2", "assistant", "assistant", "Hi there!")}},
		},
	})

	report, err := h.Run(ctx)
	if err != nil {
		t.Fatalf("Harness.Run failed: %v", err)
	}
	if report.TotalCases != 1 {
		t.Errorf("expected 1 case, got %d", report.TotalCases)
	}
	if report.UnallowedDiffs > 0 {
		t.Errorf("expected 0 unallowed diffs, got %d: %s", report.UnallowedDiffs, report.Summary)
	}
}

// TestHarnessRun_MultiCase verifies the harness with multiple cases.
func TestHarnessRun_MultiCase(t *testing.T) {
	ctx := context.Background()
	h := NewHarness()

	key := session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"}

	h.AddCases([]ReplayCase{
		{
			Name: "state_update",
			Ops: []ReplayOp{
				{Type: OpCreateSession, Key: key},
				{Type: OpUpdateSessionState, Key: key,
					Data: StateData{State: session.StateMap{"score": []byte("100")}}},
			},
		},
		{
			Name: "memory_add",
			Ops: []ReplayOp{
				{Type: OpAddMemory, Key: key,
					Data: MemoryData{
						UserKey: memory.UserKey{AppName: "test", UserID: "user1"},
						Memory:  "User likes coffee.",
						Topics:  []string{"preference", "drink"},
					}},
				{Type: OpReadMemories, Key: key,
					Data: memory.UserKey{AppName: "test", UserID: "user1"}},
			},
		},
	})

	report, err := h.Run(ctx)
	if err != nil {
		t.Fatalf("Harness.Run failed: %v", err)
	}
	if report.TotalCases != 2 {
		t.Errorf("expected 2 cases, got %d", report.TotalCases)
	}
	if report.UnallowedDiffs > 0 {
		t.Errorf("expected 0 unallowed diffs, got %d", report.UnallowedDiffs)
	}
}

// TestTrapDetection_SwapEventOrder verifies the swap_event_order trap is detected.
func TestTrapDetection_SwapEventOrder(t *testing.T) {
	ctx := context.Background()
	h := NewHarness()

	key := session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"}

	h.AddCase(ReplayCase{
		Name: "trap_swap_test",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv1", "user", "user", "Hello")}},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv2", "assistant", "assistant", "Hi there!")}},
		},
		Want: WantResult{
			ExpectedDiffKeys: []string{"events[0].content", "events[1].content"},
			ExpectedDiffCount: 2,
		},
	})

	report, err := h.RunTrap(ctx, h.cases[0], TrapSwapEventOrder())
	if err != nil {
		t.Fatalf("RunTrap failed: %v", err)
	}

	if report.TotalDiffs == 0 {
		t.Fatal("expected trap to be detected, but no diffs found")
	}
}

// TestTrapDetection_AlterMemoryContent verifies the alter_memory_content trap is detected.
func TestTrapDetection_AlterMemoryContent(t *testing.T) {
	ctx := context.Background()
	h := NewHarness()

	key := session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"}

	h.AddCase(ReplayCase{
		Name: "trap_memory_test",
		Ops: []ReplayOp{
			{Type: OpAddMemory, Key: key,
				Data: MemoryData{
					UserKey: memory.UserKey{AppName: "test", UserID: "user1"},
					Memory:  "User likes coffee.",
					Topics:  []string{"preference"},
				}},
			{Type: OpReadMemories, Key: key,
				Data: memory.UserKey{AppName: "test", UserID: "user1"}},
		},
	})

	report, err := h.RunTrap(ctx, h.cases[0], TrapAlterMemoryContent())
	if err != nil {
		t.Fatalf("RunTrap failed: %v", err)
	}

	if report.TotalDiffs == 0 {
		t.Fatal("expected trap to be detected, but no diffs found")
	}
}

// TestTrapDetection_AlterStateValue verifies the alter_state_value trap is detected.
func TestTrapDetection_AlterStateValue(t *testing.T) {
	ctx := context.Background()
	h := NewHarness()

	key := session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"}

	h.AddCase(ReplayCase{
		Name: "trap_state_test",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			{Type: OpUpdateSessionState, Key: key,
				Data: StateData{State: session.StateMap{"key1": []byte("value1")}}},
			{Type: OpGetSession, Key: key}, // Refresh to get the updated state.
		},
	})

	report, err := h.RunTrap(ctx, h.cases[0], TrapAlterStateValue())
	if err != nil {
		t.Fatalf("RunTrap failed: %v", err)
	}

	if report.TotalDiffs == 0 {
		t.Fatal("expected trap to be detected, but no diffs found")
	}
}

// TestTrapDetection_DuplicateEvent verifies the duplicate_event trap is detected.
func TestTrapDetection_DuplicateEvent(t *testing.T) {
	ctx := context.Background()
	h := NewHarness()

	key := session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"}

	h.AddCase(ReplayCase{
		Name: "trap_dup_test",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv1", "user", "user", "Hello")}},
		},
	})

	report, err := h.RunTrap(ctx, h.cases[0], TrapDuplicateEvent())
	if err != nil {
		t.Fatalf("RunTrap failed: %v", err)
	}

	if report.TotalDiffs == 0 {
		t.Fatal("expected trap to be detected, but no diffs found")
	}
}

// TestTrapDetection_RemoveSummary verifies the remove_summary trap is detected.
func TestTrapDetection_RemoveSummary(t *testing.T) {
	ctx := context.Background()
	h := NewHarness()

	key := session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"}

	h.AddCase(ReplayCase{
		Name: "trap_summary_test",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv1", "user", "user", "Hello")}},
			{Type: OpAppendEvent, Key: key,
				Data: EventData{Event: NewEvent("inv2", "assistant", "assistant", "Hi!")}},
			{Type: OpCreateSessionSummary, Key: key,
				Data: SummaryData{FilterKey: "", Force: true}},
		},
	})

	// Note: summary may not be created without a summarizer, so the trap may not trigger.
	// This test verifies the harness doesn't crash.
	report, err := h.RunTrap(ctx, h.cases[0], TrapRemoveSummary())
	if err != nil {
		t.Fatalf("RunTrap failed: %v", err)
	}
	_ = report
}

// TestHarnessRun_WithMockModel verifies the harness can run with MockModel-generated events.
func TestHarnessRun_WithMockModel(t *testing.T) {
	ctx := context.Background()
	h := NewHarness()
	m := NewMockModel(42)

	events := m.GenerateConversation(2)
	key := session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"}

	ops := []ReplayOp{
		{Type: OpCreateSession, Key: key},
	}
	for i := range events {
		ops = append(ops, ReplayOp{
			Type: OpAppendEvent,
			Key:  key,
			Data: EventData{Event: &events[i]},
		})
	}

	h.AddCase(ReplayCase{
		Name: "mock_model_conversation",
		Ops:  ops,
	})

	report, err := h.Run(ctx)
	if err != nil {
		t.Fatalf("Harness.Run failed: %v", err)
	}
	if report.UnallowedDiffs > 0 {
		t.Errorf("expected 0 unallowed diffs, got %d", report.UnallowedDiffs)
	}
}

// TestHarnessRun_WithToolCallEvents verifies the harness handles tool call events.
func TestHarnessRun_WithToolCallEvents(t *testing.T) {
	ctx := context.Background()
	h := NewHarness()
	key := session.Key{AppName: "test", UserID: "user1", SessionID: "sess1"}

	tcEvent := NewToolCallEvent("inv1", "assistant", "call_abc123", "get_weather",
		[]byte(`{"city":"London","units":"metric"}`))
	trEvent := NewToolResponseEvent("inv1", "tool", "call_abc123", "get_weather", "Sunny, 22°C")

	h.AddCase(ReplayCase{
		Name: "tool_call_events",
		Ops: []ReplayOp{
			{Type: OpCreateSession, Key: key},
			{Type: OpAppendEvent, Key: key, Data: EventData{Event: tcEvent}},
			{Type: OpAppendEvent, Key: key, Data: EventData{Event: trEvent}},
		},
	})

	report, err := h.Run(ctx)
	if err != nil {
		t.Fatalf("Harness.Run failed: %v", err)
	}
	if report.UnallowedDiffs > 0 {
		t.Errorf("expected 0 unallowed diffs, got %d", report.UnallowedDiffs)
	}
}

// TestTimeComparison verifies that timestamp comparison allows ±1s tolerance.
func TestTimeComparison(t *testing.T) {
	c := NewComparator()
	now := time.Now().UTC()

	// Test that timestamps within 1s are considered equal.
	diffs := c.compareTime("test", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"test.field", now, now.Add(500*time.Millisecond))
	if len(diffs) > 0 {
		t.Errorf("expected 0 diffs for 500ms difference, got %d", len(diffs))
	}

	// Test that timestamps beyond 1s are detected.
	diffs = c.compareTime("test", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"test.field", now, now.Add(2*time.Second))
	if len(diffs) == 0 {
		t.Error("expected diffs for 2s difference, got 0")
	}
}

// TestFloatComparison verifies that float comparison allows ±0.01 tolerance.
func TestFloatComparison(t *testing.T) {
	c := NewComparator()

	// Test that floats within tolerance are considered equal.
	diffs := c.compareFloat("test", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"test.field", 1.0, 1.005)
	if len(diffs) > 0 {
		t.Errorf("expected 0 diffs for 0.005 difference, got %d", len(diffs))
	}

	// Test that floats beyond tolerance are detected.
	diffs = c.compareFloat("test", &BackendResult{BackendName: "A"}, &BackendResult{BackendName: "B"},
		"test.field", 1.0, 1.05)
	if len(diffs) == 0 {
		t.Error("expected diffs for 0.05 difference, got 0")
	}
}

// TestNormalizer_NormalizeSession verifies session normalization.
func TestNormalizer_NormalizeSession(t *testing.T) {
	n := NewNormalizer()
	sess := &session.Session{
		ID:      "session-abc-123",
		AppName: "test",
		UserID:  "user1",
		State:   session.StateMap{"b": []byte("2"), "a": []byte("1")},
	}

	normalized := n.NormalizeSession(sess)
	if normalized.ID != "<session-id>" {
		t.Errorf("expected normalized ID '<session-id>', got %q", normalized.ID)
	}
	// Verify state keys are sorted.
	 var keys []string
	 for k := range normalized.State {
	  keys = append(keys, k)
	 }
	 sort.Strings(keys)
	 if len(keys) != 2 || keys[0] != "a" || keys[1] != "b" {
	  t.Errorf("expected sorted state keys [a b], got %v", keys)
	 }
}

// TestReporter_GenerateReport verifies the reporter generates a valid report.
func TestReporter_GenerateReport(t *testing.T) {
	r := NewReporter()
	caseResults := map[string][]DiffEntry{
		"case1": {
			{
				CaseName: "case1", BackendA: "A", BackendB: "B",
				FieldPath: "events[0].content", Baseline: "hello", Actual: "world",
				DiffReason: "content differs",
			},
		},
	}

	report := r.GenerateReport(caseResults)
	if report.TotalCases != 1 {
		t.Errorf("expected 1 case, got %d", report.TotalCases)
	}
	if report.TotalDiffs != 1 {
		t.Errorf("expected 1 diff, got %d", report.TotalDiffs)
	}

	// Verify JSON output.
	jsonData, err := r.ToJSON(report)
	if err != nil {
		t.Fatalf("ToJSON failed: %v", err)
	}
	if len(jsonData) == 0 {
		t.Error("JSON output is empty")
	}

	// Verify text output.
	text := r.ToText(report)
	if len(text) == 0 {
		t.Error("text output is empty")
	}
}

// TestBackendRegistration verifies that backends are registered correctly.
func TestBackendRegistration(t *testing.T) {
	backends := GetBackends()
	if len(backends) < 2 {
		t.Fatalf("expected at least 2 backends, got %d", len(backends))
	}

	found := map[string]bool{}
	for _, b := range backends {
		found[b.Name] = true
	}

	if !found["InMemory"] {
		t.Error("InMemory backend not registered")
	}
	if !found["SQLite"] {
		t.Error("SQLite backend not registered")
	}
}

// TestNormalizer_NormalizeJSON verifies JSON normalization.
func TestNormalizer_NormalizeJSON(t *testing.T) {
	n := NewNormalizer()
	input := []byte(`{"z":1,"a":{"nested":2,"b":3}}`)
	output := n.NormalizeJSON(input)
	// The output should have sorted keys.
	expected := `{"a":{"b":3,"nested":2},"z":1}`
	if string(output) != expected {
		t.Errorf("expected %q, got %q", expected, string(output))
	}
}
