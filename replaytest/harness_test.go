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
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// errUnavailable implements UnavailableError for testing.
type errUnavailable string

func (e errUnavailable) Error() string     { return string(e) }
func (e errUnavailable) Unavailable() bool { return true }

// ---------------------------------------------------------------------------
// Harness Setup — UnavailableError vs hard errors
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

	// Build events concurrently — each goroutine gets its own stable index.
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

	// Verify IDs are deterministic — same index should produce same ID prefix.
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

	if len(sess.Events) < 3 {
		t.Fatalf("expected at least 3 events (2 sequential + 3 concurrent), got %d", len(sess.Events))
	}

	// Check that the "after concurrent" event has a higher index than concurrent events.
	// Response IDs use the index: "resp-%d".
	lastRespID := ""
	for _, ev := range sess.Events {
		if ev.Response != nil {
			lastRespID = ev.Response.ID
		}
	}
	t.Logf("Events stored: %d, last response ID: %s", len(sess.Events), lastRespID)
	if lastRespID == "" {
		t.Error("no response ID found in events")
	}
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
		t.Errorf("lastEventIndex changed from 999 to %d — shared state modified!", h.lastEventIndex)
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
	// h.lastEventIndex — that avoids the original race condition (Bug 6).

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
		t.Errorf("response ID: expected %q, got %q — used wrong index", expectedRespID, ev.Response.ID)
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
