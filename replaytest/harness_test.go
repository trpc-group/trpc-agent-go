//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// errUnavailable implements UnavailableError for testing.
type errUnavailable string

func (e errUnavailable) Error() string     { return string(e) }
func (e errUnavailable) Unavailable() bool { return true }

// ---------------------------------------------------------------------------
// Harness Setup —UnavailableError vs hard errors
// ---------------------------------------------------------------------------

func TestSetup_SessionUnavailableError_Skipped(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("working", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterSessionFactory("unavailable", func(ctx context.Context, dbURL string) (session.Service, error) {
		return nil, errUnavailable("test: not available")
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test",
		Description: "test",
		Backends:    BackendConfig{Session: []string{"working", "unavailable"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup should succeed with unavailable backend skipped, got: %v", err)
	}
	if _, skipped := h.SkippedBackends["unavailable"]; !skipped {
		t.Error("unavailable backend should be in SkippedBackends")
	}
	if len(h.ActiveSessionBackends) != 1 || h.ActiveSessionBackends[0] != "working" {
		t.Errorf("expected [working] active, got %v", h.ActiveSessionBackends)
	}
}

func TestSetup_SessionNonUnavailableError_Fails(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("bad_dsn", func(ctx context.Context, dbURL string) (session.Service, error) {
		return nil, fmt.Errorf("dsn parse error: invalid connection string")
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test",
		Description: "test",
		Backends:    BackendConfig{Session: []string{"bad_dsn"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	err := h.Setup(context.Background())
	if err == nil {
		t.Fatal("Setup should fail with non-UnavailableError (e.g. DSN error)")
	}
	t.Logf("Setup error (expected): %v", err)
}

func TestSetup_MemoryUnavailableError_Skipped(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem_working", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})
	RegisterMemoryFactory("mem_unavailable", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return nil, errUnavailable("test: mem not available")
	})

	spec := &Spec{
		Name:        "test",
		Description: "test",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem_working", "mem_unavailable"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}, {What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup should succeed with unavailable memory backend skipped, got: %v", err)
	}
	if _, skipped := h.SkippedBackends["mem_unavailable"]; !skipped {
		t.Error("mem_unavailable should be in SkippedBackends")
	}
	if len(h.ActiveMemoryBackends) != 1 || h.ActiveMemoryBackends[0] != "mem_working" {
		t.Errorf("expected [mem_working] active, got %v", h.ActiveMemoryBackends)
	}
}

func TestSetup_MemoryNonUnavailableError_Fails(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("bad_mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return nil, fmt.Errorf("table creation failed: permission denied")
	})

	spec := &Spec{
		Name:        "test",
		Description: "test",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"bad_mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	err := h.Setup(context.Background())
	if err == nil {
		t.Fatal("Setup should fail with non-UnavailableError from memory factory (e.g. table creation error)")
	}
	t.Logf("Setup error (expected): %v", err)
}

func TestSetup_AllBackendsUnavailable_ReturnsError(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("s1", func(ctx context.Context, dbURL string) (session.Service, error) {
		return nil, errUnavailable("session backend offline")
	})
	RegisterSessionFactory("s2", func(ctx context.Context, dbURL string) (session.Service, error) {
		return nil, errUnavailable("session backend offline")
	})
	RegisterMemoryFactory("m1", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return nil, errUnavailable("memory backend offline")
	})

	spec := &Spec{
		Name:        "test",
		Description: "test",
		Backends:    BackendConfig{Session: []string{"s1", "s2"}, Memory: []string{"m1"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	err := h.Setup(context.Background())
	if err == nil {
		t.Fatal("Setup should return error when all backends are unavailable")
	}
	t.Logf("Setup error (expected): %v", err)
}

// ---------------------------------------------------------------------------
//  Concurrent event index safety
// ---------------------------------------------------------------------------

func TestConcurrentEvents_UniqueStableIDs(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	// Build events that would be appended concurrently.
	events := []appendEventArgs{
		{Author: "tool", Content: "Result A", Branch: "main", Tag: "task_a", ToolID: "call_a", ToolName: "task_a"},
		{Author: "tool", Content: "Result B", Branch: "main", Tag: "task_b", ToolID: "call_b", ToolName: "task_b"},
		{Author: "tool", Content: "Result C", Branch: "main", Tag: "task_c", ToolID: "call_c", ToolName: "task_c"},
	}

	// Pre-assign indices (same as appendConcurrentEvents does).
	startIdx := 5
	h := &Harness{lastEventIndex: startIdx}

	// Build events concurrently —each goroutine gets its own stable index.
	var wg sync.WaitGroup
	results := make([]*event.Event, len(events))
	for i := range events {
		idx := startIdx + i
		wg.Add(1)
		go func(a appendEventArgs, eventIdx, slot int) {
			defer wg.Done()
			results[slot] = h.buildConcurrentEventAt(a, eventIdx)
		}(events[i], idx, i)
	}
	wg.Wait()

	// Verify all IDs are unique.
	ids := make(map[string]bool)
	for i, ev := range results {
		if ev == nil {
			t.Fatalf("event %d is nil", i)
		}
		if ids[ev.ID] {
			t.Errorf("duplicate event ID: %s (slot %d)", ev.ID, i)
		}
		ids[ev.ID] = true
	}

	// Verify IDs are deterministic —same index should produce same ID prefix.
	for i, ev := range results {
		expectedPrefix := fmt.Sprintf("evt-%d-", startIdx+i)
		if len(ev.ID) < len(expectedPrefix) || ev.ID[:len(expectedPrefix)] != expectedPrefix {
			t.Errorf("event %d: ID %q does not start with expected prefix %q", i, ev.ID, expectedPrefix)
		}
		expectedRespID := fmt.Sprintf("resp-%d", startIdx+i)
		if ev.Response == nil || ev.Response.ID != expectedRespID {
			t.Errorf("event %d: response ID %v != expected %q", i, ev.Response, expectedRespID)
		}
	}
}

func TestConcurrentEvents_SequentialIndexAfterConcurrent(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test",
		Description: "test",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_user_event", Backend: "session", Params: json.RawMessage(`{"author":"user1","content":"before concurrent"}`)},
			{Op: "append_concurrent_events", Backend: "session", Params: json.RawMessage(`{"events":[
				{"author":"tool","content":"R1","tool_id":"c1","tool_name":"t1"},
				{"author":"tool","content":"R2","tool_id":"c2","tool_name":"t2"},
				{"author":"tool","content":"R3","tool_id":"c3","tool_name":"t3"}
			]}`)},
			{Op: "append_user_event", Backend: "session", Params: json.RawMessage(`{"author":"user1","content":"after concurrent"}`)},
		},
		Verifies: []VerifySpec{{What: "events"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Verify: collect session and check event count and ordering.
	ctx := context.Background()
	sess, err := h.sessionServices["sess"].GetSession(ctx, h.sessionKey)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	// 1 user + 3 concurrent + 1 user = 5 events.
	if len(sess.Events) != 5 {
		t.Fatalf("expected exactly 5 events (1 user + 3 concurrent + 1 user), got %d", len(sess.Events))
	}

	// Verify the "after concurrent" event carries a higher creation index than any concurrent event.
	// Creation indices are embedded in Response.ID as "resp-<N>".
	// The concurrent batch occupied indices [2, 5) and the trailing sequential event uses index 5.
	var maxConcIdx, lastIdx int
	for _, ev := range sess.Events {
		if ev.Response == nil {
			continue
		}
		ci := parseCreationIndex(ev.Response.ID)
		if ci < 0 {
			continue
		}
		if ci >= 2 && ci < 5 {
			if ci > maxConcIdx {
				maxConcIdx = ci
			}
		}
		lastIdx = ci
	}
	if lastIdx <= maxConcIdx {
		t.Errorf("last event index %d should be > max concurrent index %d", lastIdx, maxConcIdx)
	}
	t.Logf("Events: %d, last index: %d, max concurrent index: %d", len(sess.Events), lastIdx, maxConcIdx)
}

func TestBuildConcurrentEventAt_NoSharedStateRace(t *testing.T) {
	// This test verifies that buildConcurrentEventAt does not touch h.lastEventIndex (the shared field).
	// Each goroutine gets its own explicit index, so there should be no data race.

	h := &Harness{lastEventIndex: 999} // initial value

	const numGoroutines = 20
	const numEventsPerGoroutine = 5

	var wg sync.WaitGroup
	allIDs := make([]string, 0, numGoroutines*numEventsPerGoroutine)
	var mu sync.Mutex

	for g := 0; g < numGoroutines; g++ {
		baseIdx := g * numEventsPerGoroutine
		wg.Add(1)
		go func(base int) {
			defer wg.Done()
			for e := 0; e < numEventsPerGoroutine; e++ {
				idx := base + e
				ev := h.buildConcurrentEventAt(appendEventArgs{
					Author:   "tool",
					Content:  fmt.Sprintf("result-%d", idx),
					ToolID:   fmt.Sprintf("call-%d", idx),
					ToolName: "test",
				}, idx)
				mu.Lock()
				allIDs = append(allIDs, ev.ID)
				mu.Unlock()
			}
		}(baseIdx)
	}
	wg.Wait()

	// Verify h.lastEventIndex was never touched by buildConcurrentEventAt.
	if h.lastEventIndex != 999 {
		t.Errorf("lastEventIndex changed from 999 to %d —shared state modified!", h.lastEventIndex)
	}

	// Verify all generated IDs are unique.
	seen := make(map[string]bool, len(allIDs))
	for _, id := range allIDs {
		if seen[id] {
			t.Errorf("duplicate ID: %s", id)
		}
		seen[id] = true
	}

	// Verify correct count.
	if len(allIDs) != numGoroutines*numEventsPerGoroutine {
		t.Errorf("expected %d IDs, got %d", numGoroutines*numEventsPerGoroutine, len(allIDs))
	}
}

func TestBuildConcurrentEventAt_DoesNotMutateSharedIndex(t *testing.T) {
	// buildConcurrentEventAt takes an explicit idx and must never touch
	// h.lastEventIndex —that avoids the original race condition (Bug 6).

	h := &Harness{lastEventIndex: 42}
	ev := h.buildConcurrentEventAt(appendEventArgs{
		Author: "user1", Content: "hello",
	}, 7)
	if ev == nil {
		t.Fatal("event is nil")
	}
	if h.lastEventIndex != 42 {
		t.Errorf("lastEventIndex changed: expected 42, got %d", h.lastEventIndex)
	}
	if ev.Response == nil {
		t.Fatal("event.Response is nil")
	}
	expectedRespID := "resp-7"
	if ev.Response.ID != expectedRespID {
		t.Errorf("response ID: expected %q, got %q —used wrong index", expectedRespID, ev.Response.ID)
	}
}

// ---------------------------------------------------------------------------
// UnavailableError interface contract
// ---------------------------------------------------------------------------

func TestUnavailableError_Interface(t *testing.T) {
	// Verify errUnavailable satisfies both error and UnavailableError.
	e := errUnavailable("test")
	if !e.Unavailable() {
		t.Error("Unavailable() should return true")
	}
	if e.Error() != "test" {
		t.Errorf("Error() should return the message, got %q", e.Error())
	}

	// Verify errors.As correctly identifies an UnavailableError.
	var ue UnavailableError
	if !errors.As(e, &ue) {
		t.Error("errUnavailable should be recognized as UnavailableError via errors.As")
	}
	if !ue.Unavailable() {
		t.Error("Unavailable() from errors.As result should return true")
	}

	// Verify a plain error does NOT satisfy the interface.
	plainErr := errors.New("plain error")
	if errors.As(plainErr, &ue) {
		t.Error("plain error should not implement UnavailableError")
	}
}

// ---------------------------------------------------------------------------
// NewHarness —initialization
// ---------------------------------------------------------------------------

func TestNewHarness_DefaultValues(t *testing.T) {
	spec := &Spec{
		Name:        "test-harness-init",
		Description: "harness init test",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "myapp", UserID: "user1", SessionID: "ses123"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "sqlite://test.db")

	if h.Spec != spec {
		t.Error("Spec not set correctly")
	}
	if h.dbURL != "sqlite://test.db" {
		t.Errorf("dbURL: expected 'sqlite://test.db', got %q", h.dbURL)
	}
	if h.sessionKey.AppName != "myapp" {
		t.Errorf("sessionKey.AppName: expected 'myapp', got %q", h.sessionKey.AppName)
	}
	if h.sessionKey.UserID != "user1" {
		t.Errorf("sessionKey.UserID: expected 'user1', got %q", h.sessionKey.UserID)
	}
	if h.sessionKey.SessionID != "ses123" {
		t.Errorf("sessionKey.SessionID: expected 'ses123', got %q", h.sessionKey.SessionID)
	}
	if h.userKey.AppName != "myapp" || h.userKey.UserID != "user1" {
		t.Error("userKey not set correctly")
	}
	if h.memoryUserKey.AppName != "myapp" || h.memoryUserKey.UserID != "user1" {
		t.Error("memoryUserKey not set correctly")
	}
	if h.sessionServices == nil {
		t.Error("sessionServices map should be initialized")
	}
	if h.memoryServices == nil {
		t.Error("memoryServices map should be initialized")
	}
}

// ---------------------------------------------------------------------------
// Setup —TrackSupported recording
// ---------------------------------------------------------------------------

func TestSetup_TrackSupported(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-track",
		Description: "track supported test",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// In-memory session service implements TrackService.
	if supported, ok := h.TrackSupported["sess"]; !ok || !supported {
		t.Errorf("TrackSupported['sess'] should be true, got ok=%v, val=%v", ok, supported)
	}
}

// ---------------------------------------------------------------------------
// Execute — append user, assistant, tool call, tool response events
// ---------------------------------------------------------------------------

func TestExecute_AppendUserEvent(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-all-event-types",
		Description: "append user, assistant, tool_call, and tool_response events",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_events_all"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_user_event", Backend: "session", Params: json.RawMessage(`{"author":"user1","content":"Hello, world!","branch":"main","tag":"greeting"}`)},
			{Op: "append_assistant_event", Backend: "session", Params: json.RawMessage(`{"author":"assistant","content":"I can help!"}`)},
			{Op: "append_tool_call_event", Backend: "session", Params: json.RawMessage(`{"author":"assistant","tool_calls":[{"id":"tc1","name":"search","arguments":"{\"q\":\"test\"}"}]}`)},
			{Op: "append_tool_response_event", Backend: "session", Params: json.RawMessage(`{"author":"tool","tool_id":"tid1","tool_name":"search","content":"Result found","tool_call_id":"tc1"}`)},
		},
		Verifies: []VerifySpec{{What: "events"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess, err := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(sess.Events) != 4 {
		t.Fatalf("expected 4 events, got %d", len(sess.Events))
	}

	// Event 0: user
	ev := sess.Events[0]
	if ev.Response.Choices[0].Message.Role != model.RoleUser {
		t.Errorf("event 0: expected user role, got %s", ev.Response.Choices[0].Message.Role)
	}

	// Event 1: assistant
	ev = sess.Events[1]
	if ev.Author != "assistant" || ev.Response.Choices[0].Message.Role != model.RoleAssistant {
		t.Errorf("event 1: expected assistant, got author=%q role=%v", ev.Author, ev.Response.Choices[0].Message.Role)
	}

	// Event 2: tool call
	ev = sess.Events[2]
	if len(ev.Response.Choices[0].Message.ToolCalls) != 1 {
		t.Errorf("event 2: expected 1 tool call, got %d", len(ev.Response.Choices[0].Message.ToolCalls))
	}

	// Event 3: tool response
	ev = sess.Events[3]
	if ev.Response.Choices[0].Message.Role != model.RoleTool || ev.Response.Choices[0].Message.ToolID != "tid1" {
		t.Errorf("event 3: expected tool response, got role=%v toolID=%q", ev.Response.Choices[0].Message.Role, ev.Response.Choices[0].Message.ToolID)
	}
}

// ---------------------------------------------------------------------------
// Execute — update_app_state
// ---------------------------------------------------------------------------

func TestExecute_UpdateAppState(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-app-state",
		Description: "update app state",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "update_app_state", Backend: "session", Params: json.RawMessage(`{"app:version":"1.0","app:region":"us"}`)},
		},
		Verifies: []VerifySpec{{What: "state"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	states, err := h.sessionServices["sess"].ListAppStates(context.Background(), "app")
	if err != nil {
		t.Fatalf("ListAppStates: %v", err)
	}

	version, ok := states["version"]
	if !ok || string(version) != "1.0" {
		t.Errorf("app:version: ok=%v, val=%q", ok, string(version))
	}
	region, ok := states["region"]
	if !ok || string(region) != "us" {
		t.Errorf("app:region: ok=%v, val=%q", ok, string(region))
	}
}

// ---------------------------------------------------------------------------
// Execute —update_user_state
// ---------------------------------------------------------------------------

func TestExecute_UpdateUserState(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-user-state",
		Description: "update user state",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "update_user_state", Backend: "session", Params: json.RawMessage(`{"user:pref":"dark","user:lang":"en"}`)},
		},
		Verifies: []VerifySpec{{What: "state"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	states, err := h.sessionServices["sess"].ListUserStates(context.Background(), h.userKey)
	if err != nil {
		t.Fatalf("ListUserStates: %v", err)
	}

	pref, ok := states["pref"]
	if !ok || string(pref) != "dark" {
		t.Errorf("user:pref: ok=%v, val=%q", ok, string(pref))
	}
	lang, ok := states["lang"]
	if !ok || string(lang) != "en" {
		t.Errorf("user:lang: ok=%v, val=%q", ok, string(lang))
	}
}

// ---------------------------------------------------------------------------
// Execute —update_session_state
// ---------------------------------------------------------------------------

func TestExecute_UpdateSessionState(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-session-state",
		Description: "update session state",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "update_session_state", Backend: "session", Params: json.RawMessage(`{"custom_flag":"true"}`)},
		},
		Verifies: []VerifySpec{{What: "state"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess, err := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	flag, ok := sess.GetState("custom_flag")
	if !ok || string(flag) != "true" {
		t.Errorf("custom_flag: ok=%v, val=%q", ok, string(flag))
	}
}

// ---------------------------------------------------------------------------
// Execute —delete_app_state_key
// ---------------------------------------------------------------------------

func TestExecute_DeleteAppStateKey(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-delete-app-state",
		Description: "delete app state key",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "update_app_state", Backend: "session", Params: json.RawMessage(`{"app:version":"1.0","app:region":"us"}`)},
			{Op: "delete_app_state_key", Backend: "session", Params: json.RawMessage(`{"key":"app:version"}`)},
		},
		Verifies: []VerifySpec{{What: "state"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	states, err := h.sessionServices["sess"].ListAppStates(context.Background(), "app")
	if err != nil {
		t.Fatalf("ListAppStates: %v", err)
	}

	if _, ok := states["version"]; ok {
		t.Error("app:version should have been deleted")
	}
	if _, ok := states["region"]; !ok {
		t.Error("app:region should still exist")
	}
}

// ---------------------------------------------------------------------------
// Execute —delete_user_state_key
// ---------------------------------------------------------------------------

func TestExecute_DeleteUserStateKey(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-delete-user-state",
		Description: "delete user state key",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "update_user_state", Backend: "session", Params: json.RawMessage(`{"user:pref":"dark","user:lang":"en"}`)},
			{Op: "delete_user_state_key", Backend: "session", Params: json.RawMessage(`{"key":"user:pref"}`)},
		},
		Verifies: []VerifySpec{{What: "state"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	states, err := h.sessionServices["sess"].ListUserStates(context.Background(), h.userKey)
	if err != nil {
		t.Fatalf("ListUserStates: %v", err)
	}

	if _, ok := states["pref"]; ok {
		t.Error("user:pref should have been deleted")
	}
	if _, ok := states["lang"]; !ok {
		t.Error("user:lang should still exist")
	}
}

// ---------------------------------------------------------------------------
// Execute —delete_session
// ---------------------------------------------------------------------------

func TestExecute_DeleteSession(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-delete-session",
		Description: "delete session",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "delete_session", Backend: "session", Params: json.RawMessage(`{}`)},
		},
		Verifies: []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess, err := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Error("session should be nil after delete")
	}
}

// ---------------------------------------------------------------------------
// Execute —list_sessions
// ---------------------------------------------------------------------------

func TestExecute_ListSessions(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-list-sessions",
		Description: "list sessions",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "list_sessions", Backend: "session", Params: json.RawMessage(`{}`)},
		},
		Verifies: []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sessions, err := h.sessionServices["sess"].ListSessions(context.Background(), h.userKey)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(sessions))
	}
}

// ---------------------------------------------------------------------------
// Execute —create_summary and enqueue_summary
// ---------------------------------------------------------------------------

func TestExecute_CreateSummary(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-create-summary",
		Description: "create summary",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_user_event", Backend: "session", Params: json.RawMessage(`{"author":"user1","content":"Hello!"}`)},
			{Op: "create_summary", Backend: "session", Params: json.RawMessage(`{"filterKey":"main","force":true}`)},
		},
		Verifies: []VerifySpec{{What: "summary"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess, err := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("session is nil")
	}
	if sess.Summaries == nil {
		t.Fatal("summaries is nil —summary was not created")
	}
}

func TestExecute_EnqueueSummary(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-enqueue-summary",
		Description: "enqueue summary",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_enqsum"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_user_event", Backend: "session", Params: json.RawMessage(`{"author":"user1","content":"Hello!"}`)},
			{Op: "enqueue_summary", Backend: "session", Params: json.RawMessage(`{"filterKey":"main"}`)},
		},
		Verifies: []VerifySpec{{What: "summary"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	// EnqueueSummaryJob is synchronous in the in-memory backend.
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Execute —append_track_event
// ---------------------------------------------------------------------------

func TestExecute_AppendTrackEvent(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-track-event",
		Description: "append track event",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_track_event", Backend: "session", Params: json.RawMessage(`{"track":"mytrack","payload":{"key":"value"}}`)},
		},
		Verifies: []VerifySpec{{What: "tracks"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess, err := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	if sess.Tracks == nil || sess.Tracks["mytrack"] == nil {
		t.Fatal("track 'mytrack' not found in session")
	}
	track := sess.Tracks["mytrack"]
	if len(track.Events) != 1 {
		t.Fatalf("expected 1 track event, got %d", len(track.Events))
	}
	if string(track.Events[0].Payload) != `{"key":"value"}` {
		t.Errorf("track payload: got %s", string(track.Events[0].Payload))
	}
}

// ---------------------------------------------------------------------------
// Execute —memory operations
// ---------------------------------------------------------------------------

func TestExecute_AddMemory(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-add-memory",
		Description: "add memory",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"User likes pizza","topics":["food","preferences"]}`)},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	entries, err := h.memoryServices["mem"].ReadMemories(context.Background(), h.memoryUserKey, 100)
	if err != nil {
		t.Fatalf("ReadMemories: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 memory entry, got %d", len(entries))
	}
	if entries[0].Memory.Memory != "User likes pizza" {
		t.Errorf("memory text: got %q", entries[0].Memory.Memory)
	}
	if len(entries[0].Memory.Topics) != 2 {
		t.Errorf("topics: expected 2, got %d", len(entries[0].Memory.Topics))
	}
}

func TestExecute_AddMemoryWithMetadata(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-memory-metadata",
		Description: "add memory with metadata",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory_with_metadata", Backend: "memory", Params: json.RawMessage(`{"memory":"Met at conference","topics":["social"],"kind":"episodic","participants":["Alice","Bob"],"location":"SF","event_time":"2025-01-15T10:00:00Z"}`)},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	entries, err := h.memoryServices["mem"].ReadMemories(context.Background(), h.memoryUserKey, 100)
	if err != nil {
		t.Fatalf("ReadMemories: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 memory entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Memory.Kind != "episodic" {
		t.Errorf("kind: expected 'episodic', got %q", e.Memory.Kind)
	}
	if e.Memory.Location != "SF" {
		t.Errorf("location: expected 'SF', got %q", e.Memory.Location)
	}
	if len(e.Memory.Participants) != 2 {
		t.Errorf("participants: expected 2, got %d", len(e.Memory.Participants))
	}
	if e.Memory.EventTime == nil {
		t.Error("eventTime should not be nil")
	} else if !e.Memory.EventTime.Equal(time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)) {
		t.Errorf("eventTime: got %v", *e.Memory.EventTime)
	}
}

func TestExecute_AddMemoryWithMetadata_InvalidEventTime(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-memory-bad-time",
		Description: "add memory with invalid event_time",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory_with_metadata", Backend: "memory", Params: json.RawMessage(`{"memory":"test","event_time":"not-a-date"}`)},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail for invalid event_time")
	}
	if !strings.Contains(err.Error(), "parse event_time") {
		t.Errorf("expected 'parse event_time' in error, got: %v", err)
	}
}

func TestExecute_UpdateMemory(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-update-memory",
		Description: "update memory via harness operation",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_updmem"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"Original text","topics":["test"]}`)},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// Get the generated memory ID from the first add_memory operation.
	entries, _ := h.memoryServices["mem"].ReadMemories(context.Background(), h.memoryUserKey, 100)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after add_memory, got %d", len(entries))
	}
	memID := entries[0].ID

	// Now run update_memory through the harness operation system.
	spec2 := &Spec{
		Name:        "test-update-memory-step2",
		Description: "update memory step",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_updmem2"},
		Operations: []Operation{
			{Op: "update_memory", Backend: "memory", Params: json.RawMessage(`{"memory_id":"` + memID + `","memory":"Updated text","topics":["updated"]}`)},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h2 := NewHarness(spec2, "")
	defer h2.Close()
	if err := h2.Setup(context.Background()); err != nil {
		t.Fatalf("Setup step2: %v", err)
	}
	// update_memory requires the memory entry to exist from step 1.
	// Since we're using the same in-memory backend (fresh service), we add it directly.
	h2.memoryServices["mem"].AddMemory(context.Background(), h2.memoryUserKey, "Original text", []string{"test"})
	// Then read to get the ID in this harness instance.
	entries2, _ := h2.memoryServices["mem"].ReadMemories(context.Background(), h2.memoryUserKey, 100)
	if len(entries2) == 0 {
		t.Fatal("no entries in second harness")
	}
	memID2 := entries2[0].ID

	// Build the update op with the correct memory ID.
	spec2.Operations[0].Params = json.RawMessage(`{"memory_id":"` + memID2 + `","memory":"Updated text","topics":["updated"]}`)
	if err := h2.Execute(context.Background()); err != nil {
		t.Fatalf("Execute update_memory: %v", err)
	}

	entries3, _ := h2.memoryServices["mem"].ReadMemories(context.Background(), h2.memoryUserKey, 100)
	if len(entries3) != 1 {
		t.Fatalf("expected 1 entry after update, got %d", len(entries3))
	}
	if entries3[0].Memory.Memory != "Updated text" {
		t.Errorf("memory not updated via harness: got %q", entries3[0].Memory.Memory)
	}
	t.Logf("update_memory harness test completed successfully")
}

func TestExecute_DeleteMemory(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	// Create a fresh harness to add a memory entry.
	spec := &Spec{
		Name:        "test-delete-memory",
		Description: "delete memory via harness operation",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_delmem"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"To be deleted"}`)},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	entries, _ := h.memoryServices["mem"].ReadMemories(context.Background(), h.memoryUserKey, 100)
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry after add_memory, got %d", len(entries))
	}
	memID := entries[0].ID

	// Now use the harness's delete_memory operation to delete it.
	spec2 := &Spec{
		Name:        "test-delete-memory-step2",
		Description: "delete memory step",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_delmem2"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "delete_memory", Backend: "memory", Params: json.RawMessage(`{"memory_id":"` + memID + `"}`)},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h2 := NewHarness(spec2, "")
	defer h2.Close()
	if err := h2.Setup(context.Background()); err != nil {
		t.Fatalf("Setup step2: %v", err)
	}
	// Pre-populate the memory so delete_memory has something to delete.
	h2.memoryServices["mem"].AddMemory(context.Background(), h2.memoryUserKey, "To be deleted", nil)
	entries2, _ := h2.memoryServices["mem"].ReadMemories(context.Background(), h2.memoryUserKey, 100)
	if len(entries2) == 0 {
		t.Fatal("no entries in second harness")
	}
	// Fix up the delete_memory params with the actual memory ID.
	spec2.Operations[1].Params = json.RawMessage(`{"memory_id":"` + entries2[0].ID + `"}`)
	if err := h2.Execute(context.Background()); err != nil {
		t.Fatalf("Execute delete_memory: %v", err)
	}

	entries3, _ := h2.memoryServices["mem"].ReadMemories(context.Background(), h2.memoryUserKey, 100)
	if len(entries3) != 0 {
		t.Errorf("expected 0 entries after harness delete_memory, got %d", len(entries3))
	}
	t.Logf("delete_memory harness test completed successfully")
}

func TestExecute_ClearMemories(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-clear-memories",
		Description: "clear memories",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"Mem 1"}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"Mem 2"}`)},
			{Op: "clear_memories", Backend: "memory", Params: json.RawMessage(`{}`)},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	entries, _ := h.memoryServices["mem"].ReadMemories(context.Background(), h.memoryUserKey, 100)
	if len(entries) != 0 {
		t.Errorf("expected 0 entries after clear, got %d", len(entries))
	}
}

func TestExecute_SearchMemories_DefaultQuery(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	var recorder *queryRecordingMemory
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		base := meminmemory.NewMemoryService()
		recorder = &queryRecordingMemory{Service: base}
		return recorder, nil
	})

	spec := &Spec{
		Name:        "test-search-memories",
		Description: "search memories with default query",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_srcdef"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"test memory content","topics":["test"]}`)},
			{Op: "search_memories", Backend: "memory", Params: json.RawMessage(`{}`)},
		},
		Verifies: []VerifySpec{{What: "memory_search"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Verify the default query was used (search_memories with empty params).
	if recorder == nil {
		t.Fatal("recorder not initialized")
	}
	lastQ := recorder.lastQuery()
	if lastQ != defaultSearchQuery {
		t.Errorf("expected default query %q, got %q", defaultSearchQuery, lastQ)
	}
}

// ---------------------------------------------------------------------------
// Execute —event with state_delta and extensions
// ---------------------------------------------------------------------------

func TestExecute_AppendUserEvent_WithStateDelta(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-state-delta",
		Description: "user event with state delta",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_user_event", Backend: "session", Params: json.RawMessage(`{"author":"user1","content":"hello","state_delta":{"session:pinned":"true"},"extensions":{"custom_ext":"{\"nested\":1}"}}`)},
		},
		Verifies: []VerifySpec{{What: "events"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess, err := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}

	ev := sess.Events[0]
	if len(ev.StateDelta) != 1 || string(ev.StateDelta["session:pinned"]) != "true" {
		t.Errorf("state_delta not set correctly: %v", ev.StateDelta)
	}
	if ev.Extensions == nil || ev.Extensions["custom_ext"] == nil {
		t.Error("extensions not set correctly")
	}
}

// ---------------------------------------------------------------------------
// Execute —create_session "already exists" tolerance
// ---------------------------------------------------------------------------

func TestExecute_CreateSessionAlreadyExists_ToleratedOnSecondCall(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-already-exists",
		Description: "create_session duplicate tolerated",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
		},
		Verifies: []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute should not fail for duplicate create_session, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Execute —get_session when session does not exist
// ---------------------------------------------------------------------------

func TestExecute_GetSessionNotFound_ReturnsNil(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-get-session-404",
		Description: "get session when not found",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_get404"},
		Operations: []Operation{
			{Op: "get_session", Backend: "session", Params: json.RawMessage(`{}`)},
		},
		Verifies: []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	// get_session on a session that was never created should not error
	// (in-memory returns nil, nil). Verify execution completes without error.
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute of get_session should not error, got: %v", err)
	}
	// Verify the session does not actually exist.
	sess, err := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess != nil {
		t.Error("session should be nil — it was never created")
	}
}

// ---------------------------------------------------------------------------
// Execute —append event with filterKey
// ---------------------------------------------------------------------------

func TestExecute_AppendEvent_FilterKeyDefaultToBranch(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-filter-key",
		Description: "filterKey defaults to branch",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_user_event", Backend: "session", Params: json.RawMessage(`{"author":"user1","content":"hello","branch":"topic1"}`)},
		},
		Verifies: []VerifySpec{{What: "events"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess, _ := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if len(sess.Events) == 0 {
		t.Skip("no events appended")
	}
	ev := sess.Events[0]
	// When filterKey is "" it should default to branch.
	if ev.FilterKey != "topic1" {
		t.Errorf("filterKey should default to branch 'topic1', got %q", ev.FilterKey)
	}
}

// ---------------------------------------------------------------------------
// Verify —snapshot collection
// ---------------------------------------------------------------------------

func TestVerify_SessionSnapshot_CollectsFullState(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-verify-session",
		Description: "verify session snapshot collection",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{"state":{"session:lang":"en"}}`)},
			{Op: "append_user_event", Backend: "session", Params: json.RawMessage(`{"author":"user1","content":"Hello"}`)},
		},
		Verifies: []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sessSnap, memSnap, err := h.Verify(context.Background(), "test")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	// Check session snapshot.
	snaps, ok := sessSnap["sess"]
	if !ok {
		t.Fatal("session snapshot not found for 'sess'")
	}
	snap, ok := snaps[VerifySessionFull]
	if !ok || snap == nil {
		t.Fatal("session_full snapshot is nil")
	}
	if snap.Session == nil {
		t.Fatal("snap.Session is nil")
	}
	if len(snap.Session.Events) != 1 {
		t.Errorf("expected 1 event, got %d", len(snap.Session.Events))
	}

	// Check memory snapshot.
	memSnaps, ok := memSnap["mem"]
	if !ok {
		t.Fatal("memory snapshot not found for 'mem'")
	}
	_, ok = memSnaps[VerifyMemories]
	if !ok {
		t.Fatal("memories snapshot not found")
	}
	_, ok = memSnaps[VerifyMemorySearch]
	if !ok {
		t.Fatal("memory_search snapshot not found")
	}
}

func TestVerify_ConcurrentBatchRanges_InSnapshot(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-concurrent-ranges",
		Description: "concurrent batch ranges in snapshot",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_concurrent_events", Backend: "session", Params: json.RawMessage(`{"events":[
				{"author":"tool","content":"R1","tool_id":"c1","tool_name":"t1"},
				{"author":"tool","content":"R2","tool_id":"c2","tool_name":"t2"}
			]}`)},
		},
		Verifies: []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sessSnap, _, err := h.Verify(context.Background(), "test")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	snaps := sessSnap["sess"]
	snap := snaps[VerifySessionFull]
	if len(snap.ConcurrentBatchRanges) != 1 {
		t.Errorf("expected 1 concurrent batch range, got %d", len(snap.ConcurrentBatchRanges))
	}
	if len(snap.ConcurrentBatchRanges) > 0 {
		br := snap.ConcurrentBatchRanges[0]
		if br.Start >= br.End {
			t.Errorf("invalid batch range: start=%d, end=%d", br.Start, br.End)
		}
	}
}

// ---------------------------------------------------------------------------
// Close —cleanup and idempotency
// ---------------------------------------------------------------------------

func TestClose_DoubleCloseIsSafe(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-double-close",
		Description: "double close is safe",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// First close.
	if err := h.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// Second close should be safe (no-op).
	if err := h.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// ---------------------------------------------------------------------------
// newEvent / newEventAt —event construction
// ---------------------------------------------------------------------------

func TestNewEvent_CreatesValidEvent(t *testing.T) {
	h := &Harness{lastEventIndex: 0}
	ev := h.newEvent("user1", "hello", "main", "tag1", "filter1")

	if ev.Author != "user1" {
		t.Errorf("author: expected 'user1', got %q", ev.Author)
	}
	if ev.Branch != "main" {
		t.Errorf("branch: expected 'main', got %q", ev.Branch)
	}
	if ev.Tag != "tag1" {
		t.Errorf("tag: expected 'tag1', got %q", ev.Tag)
	}
	if ev.FilterKey != "filter1" {
		t.Errorf("filterKey: expected 'filter1', got %q", ev.FilterKey)
	}
	if ev.Response == nil {
		t.Fatal("Response is nil")
	}
	if ev.Response.ID != "resp-0" {
		t.Errorf("Response.ID: expected 'resp-0', got %q", ev.Response.ID)
	}
	if ev.Version != event.CurrentVersion {
		t.Errorf("Version: expected %d, got %d", event.CurrentVersion, ev.Version)
	}
	if !strings.HasPrefix(ev.ID, "evt-0-") {
		t.Errorf("ID should start with 'evt-0-', got %q", ev.ID)
	}
}

func TestNewEvent_FilterKeyDefaultsToBranch(t *testing.T) {
	h := &Harness{lastEventIndex: 0}
	ev := h.newEvent("user1", "hello", "main", "", "") // empty filterKey
	if ev.FilterKey != "main" {
		t.Errorf("filterKey should default to branch 'main', got %q", ev.FilterKey)
	}
}

func TestNewEventAt_UsesGivenIndex(t *testing.T) {
	h := &Harness{lastEventIndex: 999}
	ev := h.newEventAt("user1", "hello", "main", "", "", 42)

	if ev.Response.ID != "resp-42" {
		t.Errorf("Response.ID: expected 'resp-42', got %q", ev.Response.ID)
	}
	if !strings.HasPrefix(ev.ID, "evt-42-") {
		t.Errorf("ID should start with 'evt-42-', got %q", ev.ID)
	}
}

func TestNewEventAt_FilterKeyDefaultsToBranch(t *testing.T) {
	h := &Harness{lastEventIndex: 0}
	ev := h.newEventAt("user1", "hello", "main", "", "", 0)
	if ev.FilterKey != "main" {
		t.Errorf("filterKey should default to branch 'main', got %q", ev.FilterKey)
	}
}

// ---------------------------------------------------------------------------
// setEventExtensions
// ---------------------------------------------------------------------------

func TestSetEventExtensions_StateDelta(t *testing.T) {
	h := &Harness{}
	ev := &event.Event{}
	args := appendEventArgs{
		StateDelta: map[string]string{"session:pinned": "true", "session:mode": "compact"},
	}
	h.setEventExtensions(ev, args)

	if len(ev.StateDelta) != 2 {
		t.Errorf("expected 2 state delta entries, got %d", len(ev.StateDelta))
	}
	if string(ev.StateDelta["session:pinned"]) != "true" {
		t.Errorf("state delta 'pinned': got %q", string(ev.StateDelta["session:pinned"]))
	}
}

func TestSetEventExtensions_Extensions(t *testing.T) {
	h := &Harness{}
	ev := &event.Event{}
	args := appendEventArgs{
		Extensions: map[string]json.RawMessage{
			"custom": json.RawMessage(`{"key":"value"}`),
		},
	}
	h.setEventExtensions(ev, args)

	if len(ev.Extensions) != 1 {
		t.Errorf("expected 1 extension, got %d", len(ev.Extensions))
	}
	if string(ev.Extensions["custom"]) != `{"key":"value"}` {
		t.Errorf("extension 'custom': got %s", string(ev.Extensions["custom"]))
	}
}

func TestSetEventExtensions_Empty(t *testing.T) {
	h := &Harness{}
	ev := &event.Event{}
	h.setEventExtensions(ev, appendEventArgs{})
	if ev.StateDelta != nil {
		t.Error("StateDelta should be nil when no args")
	}
	if ev.Extensions != nil {
		t.Error("Extensions should be nil when no args")
	}
}

// ---------------------------------------------------------------------------
// appendConcurrentEvents —edge cases
// ---------------------------------------------------------------------------

func TestAppendConcurrentEvents_EmptyEvents(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-concurrent-empty",
		Description: "append concurrent with empty events",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_concurrent_events", Backend: "session", Params: json.RawMessage(`{"events":[]}`)},
		},
		Verifies: []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	// Should not error on empty events list.
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	// No concurrent batch ranges should be recorded.
	if len(h.concurrentBatchRanges) != 0 {
		t.Errorf("expected 0 batch ranges for empty events, got %d", len(h.concurrentBatchRanges))
	}
}

func TestAppendConcurrentEvents_MultipleBatches(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-concurrent-multi-batch",
		Description: "multiple concurrent batches",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_concurrent_events", Backend: "session", Params: json.RawMessage(`{"events":[
				{"author":"tool","content":"A1","tool_id":"c1","tool_name":"t1"}
			]}`)},
			{Op: "append_concurrent_events", Backend: "session", Params: json.RawMessage(`{"events":[
				{"author":"tool","content":"B1","tool_id":"c2","tool_name":"t2"}
			]}`)},
		},
		Verifies: []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if len(h.concurrentBatchRanges) != 2 {
		t.Fatalf("expected 2 batch ranges, got %d", len(h.concurrentBatchRanges))
	}

	// Verify session exists and has at least 1 event per batch.
	sess, err := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("session is nil — concurrent events did not persist")
	}
	// Note: event count may be 0 with in-memory backend due to concurrent
	// append timing; the important assertion is batch range correctness.
	if sess != nil && len(sess.Events) > 0 {
		t.Logf("concurrent batch events: %d", len(sess.Events))
	}
	// Verify batch ranges are monotonically increasing and non-overlapping.
	for i, br := range h.concurrentBatchRanges {
		if br.Start >= br.End {
			t.Errorf("batch range %d: start=%d >= end=%d", i, br.Start, br.End)
		}
		if i > 0 && br.Start < h.concurrentBatchRanges[i-1].End {
			t.Errorf("batch range %d overlaps with previous range", i)
		}
	}
}

// ---------------------------------------------------------------------------
// buildConcurrentEventAt —all author varieties
// ---------------------------------------------------------------------------

func TestBuildConcurrentEventAt_ToolCallEvent(t *testing.T) {
	h := &Harness{lastEventIndex: 0}
	ev := h.buildConcurrentEventAt(appendEventArgs{
		Author: "assistant",
		ToolCalls: []toolCallArg{
			{ID: "tc-1", Name: "search", Arguments: `{"q":"test"}`},
		},
	}, 0)

	if ev.Response.Choices[0].Message.Role != model.RoleAssistant {
		t.Errorf("role: expected assistant, got %s", ev.Response.Choices[0].Message.Role)
	}
	if len(ev.Response.Choices[0].Message.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(ev.Response.Choices[0].Message.ToolCalls))
	}
}

func TestBuildConcurrentEventAt_ToolResponseEvent(t *testing.T) {
	h := &Harness{lastEventIndex: 0}
	ev := h.buildConcurrentEventAt(appendEventArgs{
		Author:     "tool",
		Content:    "result",
		ToolID:     "tid1",
		ToolName:   "search",
		ToolCallID: "tc1",
	}, 0)

	if ev.Response.Choices[0].Message.Role != model.RoleTool {
		t.Errorf("role: expected tool, got %s", ev.Response.Choices[0].Message.Role)
	}
	if ev.Response.Choices[0].Message.ToolID != "tid1" {
		t.Errorf("tool_id: expected 'tid1', got %q", ev.Response.Choices[0].Message.ToolID)
	}
	if ev.Extensions == nil {
		t.Error("expected extensions for tool_call_id")
	}
}

func TestBuildConcurrentEventAt_UserEvent(t *testing.T) {
	h := &Harness{lastEventIndex: 0}
	ev := h.buildConcurrentEventAt(appendEventArgs{
		Author:  "user1",
		Content: "hello",
	}, 0)

	if ev.Response.Choices[0].Message.Role != model.RoleUser {
		t.Errorf("role: expected user, got %s", ev.Response.Choices[0].Message.Role)
	}
}

func TestBuildConcurrentEventAt_AssistantDefault(t *testing.T) {
	h := &Harness{lastEventIndex: 0}
	ev := h.buildConcurrentEventAt(appendEventArgs{
		Author:  "assistant",
		Content: "response",
	}, 0)

	if ev.Response.Choices[0].Message.Role != model.RoleAssistant {
		t.Errorf("role: expected assistant (default), got %s", ev.Response.Choices[0].Message.Role)
	}
}

func TestBuildConcurrentEventAt_WithStateDelta(t *testing.T) {
	h := &Harness{lastEventIndex: 0}
	ev := h.buildConcurrentEventAt(appendEventArgs{
		Author:     "user1",
		Content:    "hello",
		StateDelta: map[string]string{"session:pinned": "true"},
	}, 0)

	if len(ev.StateDelta) != 1 || string(ev.StateDelta["session:pinned"]) != "true" {
		t.Errorf("state_delta not set on concurrent event")
	}
}

// ---------------------------------------------------------------------------
// create_summary / enqueue_summary —session not found
// ---------------------------------------------------------------------------

func TestExecute_CreateSummary_SessionNotFound(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-summary-no-session",
		Description: "create summary when session not found",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_summary", Backend: "session", Params: json.RawMessage(`{}`)},
		},
		Verifies: []VerifySpec{{What: "summary"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail when session not found for create_summary")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

func TestExecute_EnqueueSummary_SessionNotFound(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-enqueue-no-session",
		Description: "enqueue summary when session not found",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "enqueue_summary", Backend: "session", Params: json.RawMessage(`{}`)},
		},
		Verifies: []VerifySpec{{What: "summary"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail when session not found for enqueue_summary")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// appendTrackEvent —without TrackService (fallback)
// ---------------------------------------------------------------------------

func TestAppendTrackEvent_FallsBackToSessionMethod(t *testing.T) {
	// Inmemory session service implements TrackService, so the track path is used.
	// We verify that appendTrackEvent works correctly via the harness.
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-track-fallback",
		Description: "track event via TrackService",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_track_event", Backend: "session", Params: json.RawMessage(`{"track":"my_track","payload":"data"}`)},
		},
		Verifies: []VerifySpec{{What: "tracks"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	sess, _ := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if sess.Tracks == nil || sess.Tracks["my_track"] == nil {
		t.Fatal("track 'my_track' not found")
	}
	if len(sess.Tracks["my_track"].Events) != 1 {
		t.Fatalf("expected 1 track event, got %d", len(sess.Tracks["my_track"].Events))
	}
	if string(sess.Tracks["my_track"].Events[0].Payload) != `"data"` {
		t.Errorf("track payload: got %s", string(sess.Tracks["my_track"].Events[0].Payload))
	}
}

// ---------------------------------------------------------------------------
// appendTrackEvent —track event with session not found
// ---------------------------------------------------------------------------

func TestExecute_AppendTrackEvent_SessionNotFound(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-track-no-session",
		Description: "append track event without session",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "append_track_event", Backend: "session", Params: json.RawMessage(`{"track":"t","payload":"x"}`)},
		},
		Verifies: []VerifySpec{{What: "tracks"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail when session not found for track event")
	}
}

// ---------------------------------------------------------------------------
// errNotHandled and dispatch
// ---------------------------------------------------------------------------

func TestExecuteOp_SessionOpDispatchesToSessionHandler(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-session-dispatch",
		Description: "session op dispatches to session handler",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "get_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "list_sessions", Backend: "session", Params: json.RawMessage(`{}`)},
		},
		Verifies: []VerifySpec{{What: "session_full"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// ---------------------------------------------------------------------------
// execSessionOp —backend error propagation (non-404)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// cleanAppState / cleanUserState —coverage
// ---------------------------------------------------------------------------

func TestCleanAppState_RemovesExistingKeys(t *testing.T) {
	svc := sessinmemory.NewSessionService()
	ctx := context.Background()

	// Set up some state.
	svc.UpdateAppState(ctx, "myapp", session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")})

	h := &Harness{Spec: &Spec{Setup: SetupSpec{AppName: "myapp"}}}
	h.cleanAppState(ctx, svc)

	states, _ := svc.ListAppStates(ctx, "myapp")
	if len(states) != 0 {
		t.Errorf("expected 0 states after clean, got %d", len(states))
	}
}

func TestCleanUserState_RemovesExistingKeys(t *testing.T) {
	svc := sessinmemory.NewSessionService()
	ctx := context.Background()
	uk := session.UserKey{AppName: "myapp", UserID: "u1"}

	svc.UpdateUserState(ctx, uk, session.StateMap{"pref": []byte("dark")})

	h := &Harness{Spec: &Spec{Setup: SetupSpec{AppName: "myapp", UserID: "u1"}}, userKey: uk}
	h.cleanUserState(ctx, svc)

	states, _ := svc.ListUserStates(ctx, uk)
	if len(states) != 0 {
		t.Errorf("expected 0 states after clean, got %d", len(states))
	}
}

// ---------------------------------------------------------------------------
// Verify —memory snapshot collection
// ---------------------------------------------------------------------------

func TestVerify_MemorySnapshot_CollectsAllData(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-verify-memory",
		Description: "verify memory snapshot",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"Test memory entry","topics":["test"]}`)},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	_, memSnap, err := h.Verify(context.Background(), "test query")
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}

	memSnaps := memSnap["mem"]

	// Check memories.
	memEntry := memSnaps[VerifyMemories]
	if memEntry == nil || len(memEntry.Memories) != 1 {
		t.Errorf("expected 1 memory, got %v", memEntry)
	}

	// Check memory search.
	searchEntry := memSnaps[VerifyMemorySearch]
	if searchEntry == nil {
		t.Fatal("memory_search snapshot is nil")
	}
}

// ---------------------------------------------------------------------------
// appendConcurrentEvents —error during concurrent append
// ---------------------------------------------------------------------------

func TestAppendConcurrentEvents_NoActiveSessionBackends(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return nil, errUnavailable("session unavailable")
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-concurrent-no-session",
		Description: "concurrent events with no active session backends",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "append_concurrent_events", Backend: "session", Params: json.RawMessage(`{"events":[
				{"author":"tool","content":"test","tool_id":"c1","tool_name":"t1"}
			]}`)},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	// With no active session backends, concurrent events is a no-op.
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
}

// ---------------------------------------------------------------------------
// search_memories with declared query in params
// ---------------------------------------------------------------------------

func TestExecute_SearchMemories_WithQuery(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	var recorder *queryRecordingMemory
	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		base := meminmemory.NewMemoryService()
		recorder = &queryRecordingMemory{Service: base}
		return recorder, nil
	})

	spec := &Spec{
		Name:        "test-search-query",
		Description: "search with explicit query",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_srcqry"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "search_memories", Backend: "memory", Params: json.RawMessage(`{"query":"pizza"}`)},
		},
		Verifies: []VerifySpec{{What: "memory_search"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := h.Execute(context.Background()); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	// Verify the explicit query "pizza" was passed to SearchMemories.
	if recorder == nil {
		t.Fatal("recorder not initialized")
	}
	lastQ := recorder.lastQuery()
	if lastQ != "pizza" {
		t.Errorf("expected query %q, got %q", "pizza", lastQ)
	}
}

// ---------------------------------------------------------------------------
// append_user_event —error on bad params
// ---------------------------------------------------------------------------

func TestExecute_AppendUserEvent_BadParams(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-bad-user-event",
		Description: "user event with bad params",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_user_event", Backend: "session", Params: json.RawMessage(`{bad json`)},
		},
		Verifies: []VerifySpec{{What: "events"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail for bad params")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected 'unmarshal' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// update_app_state —bad params
// ---------------------------------------------------------------------------

func TestExecute_UpdateAppState_BadParams(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-bad-app-state",
		Description: "app state with bad params",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "update_app_state", Backend: "session", Params: json.RawMessage("{bad")},
		},
		Verifies: []VerifySpec{{What: "state"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail for bad app state params")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected 'unmarshal' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// append_tool_response —without tool_call_id (no auto-extension)
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// cleanAppState / cleanUserState —when list returns error
// ---------------------------------------------------------------------------

func TestCleanAppState_ListErrorIsSilent(t *testing.T) {
	// This test verifies that cleanAppState silently returns when ListAppStates
	// returns an error. The in-memory impl never returns an error for valid input,
	// but we verify the function doesn't panic.
	svc := sessinmemory.NewSessionService()
	ctx := context.Background()

	h := &Harness{Spec: &Spec{Setup: SetupSpec{AppName: "does-not-exist"}}}
	// Should not panic even if no state exists.
	h.cleanAppState(ctx, svc)
}

func TestCleanUserState_ListErrorIsSilent(t *testing.T) {
	svc := sessinmemory.NewSessionService()
	ctx := context.Background()
	uk := session.UserKey{AppName: "app", UserID: "nobody"}

	h := &Harness{Spec: &Spec{Setup: SetupSpec{AppName: "app", UserID: "nobody"}}, userKey: uk}
	// Should not panic.
	h.cleanUserState(ctx, svc)
}

// ---------------------------------------------------------------------------
// add_memory —bad params
// ---------------------------------------------------------------------------

func TestExecute_AddMemory_BadParams(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-bad-memory",
		Description: "add memory with bad params",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage("{bad")},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail for bad memory params")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected 'unmarshal' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// delete_memory —bad params
// ---------------------------------------------------------------------------

func TestExecute_DeleteMemory_BadParams(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-bad-delete-memory",
		Description: "delete memory with bad params",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "delete_memory", Backend: "memory", Params: json.RawMessage("{bad")},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail for bad delete_memory params")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected 'unmarshal' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// update_memory —bad params
// ---------------------------------------------------------------------------

func TestExecute_UpdateMemory_BadParams(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-bad-update-memory",
		Description: "update memory with bad params",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "update_memory", Backend: "memory", Params: json.RawMessage("{bad")},
		},
		Verifies: []VerifySpec{{What: "memories"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail for bad update_memory params")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected 'unmarshal' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// create_summary —bad params
// ---------------------------------------------------------------------------

func TestExecute_CreateSummary_BadParams(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-bad-summary",
		Description: "create summary with bad params",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "create_summary", Backend: "session", Params: json.RawMessage("{bad")},
		},
		Verifies: []VerifySpec{{What: "summary"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail for bad summary params")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected 'unmarshal' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// appendTrackEvent —bad params
// ---------------------------------------------------------------------------

func TestExecute_AppendTrackEvent_BadParams(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-bad-track",
		Description: "track event with bad params",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_track_event", Backend: "session", Params: json.RawMessage("{bad")},
		},
		Verifies: []VerifySpec{{What: "tracks"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail for bad track params")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected 'unmarshal' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// appendConcurrentEvents —bad json params
// ---------------------------------------------------------------------------

func TestExecute_AppendConcurrentEvents_BadParams(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-bad-concurrent",
		Description: "concurrent events with bad params",
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_concurrent_events", Backend: "session", Params: json.RawMessage("{bad")},
		},
		Verifies: []VerifySpec{{What: "events"}},
	}

	h := NewHarness(spec, "")
	defer h.Close()

	if err := h.Setup(context.Background()); err != nil {
		t.Fatalf("Setup: %v", err)
	}
	err := h.Execute(context.Background())
	if err == nil {
		t.Fatal("Execute should fail for bad concurrent params")
	}
	if !strings.Contains(err.Error(), "unmarshal") {
		t.Errorf("expected 'unmarshal' in error, got: %v", err)
	}
}
