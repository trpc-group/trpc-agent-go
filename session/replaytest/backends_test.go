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
	"os"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestBackendRegistration_DefaultBackends(t *testing.T) {
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

func TestBackendRegistration_EnvControl(t *testing.T) {
	// Save and restore env vars.
	oldSQLite := os.Getenv("REPLAYTEST_SQLITE_ENABLED")
	oldInMem := os.Getenv("REPLAYTEST_INMEMORY_ENABLED")
	defer func() {
		os.Setenv("REPLAYTEST_SQLITE_ENABLED", oldSQLite)
		os.Setenv("REPLAYTEST_INMEMORY_ENABLED", oldInMem)
	}()

	// Unset env vars to test defaults.
	os.Unsetenv("REPLAYTEST_SQLITE_ENABLED")
	os.Unsetenv("REPLAYTEST_INMEMORY_ENABLED")

	// Env not set → should default to true for InMemory and SQLite.
	if !envEnabled("INMEMORY", true) {
		t.Error("expected INMEMORY to default to true")
	}
	if !envEnabled("SQLITE", true) {
		t.Error("expected SQLITE to default to true")
	}
}

func TestBackendRegistration_OptionalBackendsDisabled(t *testing.T) {
	// Optional backends should default to disabled.
	if envEnabled("REDIS", false) {
		t.Error("expected REDIS to default to disabled")
	}
	if envEnabled("POSTGRES", false) {
		t.Error("expected POSTGRES to default to disabled")
	}
	if envEnabled("MYSQL", false) {
		t.Error("expected MYSQL to default to disabled")
	}
	if envEnabled("CLICKHOUSE", false) {
		t.Error("expected CLICKHOUSE to default to disabled")
	}
}

func TestEnvVarName(t *testing.T) {
	name := envVarName("SQLITE")
	expected := "REPLAYTEST_SQLITE_ENABLED"
	if name != expected {
		t.Errorf("expected %q, got %q", expected, name)
	}
}

func TestInMemoryBackend_Create(t *testing.T) {
	backends := GetBackends()
	var inmem BackendFactory
	found := false
	for _, b := range backends {
		if b.Name == "InMemory" {
			inmem = b
			found = true
			break
		}
	}
	if !found {
		t.Skip("InMemory backend not found")
	}
	if !inmem.Enabled {
		t.Skip("InMemory backend not enabled")
	}

	sessSvc, memSvc, err := inmem.New()
	if err != nil {
		t.Fatalf("failed to create InMemory backend: %v", err)
	}
	defer sessSvc.Close()
	defer memSvc.Close()

	if sessSvc == nil {
		t.Error("session service is nil")
	}
	if memSvc == nil {
		t.Error("memory service is nil")
	}
}

func TestSQLiteBackend_Create(t *testing.T) {
	backends := GetBackends()
	var sqlite BackendFactory
	found := false
	for _, b := range backends {
		if b.Name == "SQLite" {
			sqlite = b
			found = true
			break
		}
	}
	if !found {
		t.Skip("SQLite backend not found")
	}
	if !sqlite.Enabled {
		t.Skip("SQLite backend not enabled")
	}

	sessSvc, memSvc, err := sqlite.New()
	if err != nil {
		t.Fatalf("failed to create SQLite backend: %v", err)
	}
	defer sessSvc.Close()
	defer memSvc.Close()

	if sessSvc == nil {
		t.Error("session service is nil")
	}
	if memSvc == nil {
		t.Error("memory service is nil")
	}
}

func TestBackendFactory_Fields(t *testing.T) {
	f := BackendFactory{
		Name:    "test-backend",
		Enabled: true,
	}
	if f.Name != "test-backend" {
		t.Errorf("expected Name 'test-backend', got %q", f.Name)
	}
	if !f.Enabled {
		t.Error("expected Enabled to be true")
	}
}

func TestBackendCapabilities_KnownBackends(t *testing.T) {
	tests := []struct {
		name       string
		wantPaging bool
		wantTrack  bool
		wantFilter bool
		wantSearch bool
		wantTTL    bool
	}{
		{"InMemory", false, true, true, true, false},
		{"SQLite", false, true, true, true, false},
		{"Redis", false, true, true, true, true},
		{"Postgres", true, true, true, true, false},
		{"MySQL", true, true, true, true, false},
		{"ClickHouse", false, false, false, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			caps := BackendCapabilities(tt.name)
			if caps[CapEventPaging] != tt.wantPaging {
				t.Errorf("CapEventPaging: got %v, want %v", caps[CapEventPaging], tt.wantPaging)
			}
			if caps[CapTrack] != tt.wantTrack {
				t.Errorf("CapTrack: got %v, want %v", caps[CapTrack], tt.wantTrack)
			}
			if caps[CapSummaryFilterKey] != tt.wantFilter {
				t.Errorf("CapSummaryFilterKey: got %v, want %v", caps[CapSummaryFilterKey], tt.wantFilter)
			}
			if caps[CapMemorySearch] != tt.wantSearch {
				t.Errorf("CapMemorySearch: got %v, want %v", caps[CapMemorySearch], tt.wantSearch)
			}
			if caps[CapTTL] != tt.wantTTL {
				t.Errorf("CapTTL: got %v, want %v", caps[CapTTL], tt.wantTTL)
			}
		})
	}
}

func TestBackendCapabilities_UnknownBackend(t *testing.T) {
	caps := BackendCapabilities("Unknown")
	if caps[CapEventPaging] {
		t.Error("expected CapEventPaging false for unknown backend")
	}
	if caps[CapTrack] {
		t.Error("expected CapTrack false for unknown backend")
	}
	if caps[CapSummaryFilterKey] {
		t.Error("expected CapSummaryFilterKey false for unknown backend")
	}
	if caps[CapMemorySearch] {
		t.Error("expected CapMemorySearch false for unknown backend")
	}
	if caps[CapTTL] {
		t.Error("expected CapTTL false for unknown backend")
	}
}

// TestExecuteOp_ErrorPaths verifies that executeOp returns errors for invalid inputs.
func TestExecuteOp_ErrorPaths(t *testing.T) {
	backends := GetBackends()
	var inmem BackendFactory
	for _, b := range backends {
		if b.Name == "InMemory" {
			inmem = b
			break
		}
	}
	if !inmem.Enabled {
		t.Skip("InMemory backend not enabled")
	}

	ctx := context.Background()
	key := session.Key{AppName: "test", UserID: "u1", SessionID: "sess-error"}

	// Tests that do NOT require an existing session.
	directTests := []struct {
		name string
		op   ReplayOp
	}{
		{
			name: "CreateSession_invalid_data_type",
			op:   ReplayOp{Type: OpCreateSession, Key: key, Data: "wrong_type"},
		},
		{
			name: "AppendEvent_no_session",
			op:   ReplayOp{Type: OpAppendEvent, Key: key, Data: EventData{Event: NewEvent("inv1", "user", "user", "hi")}},
		},
		{
			name: "UpdateSessionState_no_session",
			op:   ReplayOp{Type: OpUpdateSessionState, Key: key, Data: StateData{State: session.StateMap{"k": []byte("v")}}},
		},
		{
			name: "AddMemory_invalid_data_type",
			op:   ReplayOp{Type: OpAddMemory, Key: key, Data: "wrong_type"},
		},
		{
			name: "UpdateMemory_invalid_data_type",
			op:   ReplayOp{Type: OpUpdateMemory, Key: key, Data: "wrong_type"},
		},
		{
			name: "DeleteMemory_invalid_data_type",
			op:   ReplayOp{Type: OpDeleteMemory, Key: key, Data: "wrong_type"},
		},
		{
			name: "ClearMemories_invalid_data_type",
			op:   ReplayOp{Type: OpClearMemories, Key: key, Data: "wrong_type"},
		},
		{
			name: "CreateSessionSummary_no_session",
			op:   ReplayOp{Type: OpCreateSessionSummary, Key: key, Data: SummaryData{}},
		},
		{
			name: "GetSessionSummaryText_no_session",
			op:   ReplayOp{Type: OpGetSessionSummaryText, Key: key, Data: SummaryData{}},
		},
		{
			name: "AppendTrackEvent_no_session",
			op:   ReplayOp{Type: OpAppendTrackEvent, Key: key, Data: TrackEventData{Event: &session.TrackEvent{Track: "t1"}}},
		},
		{
			name: "ConcurrentAppendEvents_no_session",
			op:   ReplayOp{Type: OpConcurrentAppendEvents, Key: key, Data: ConcurrentEventData{}},
		},
		{
			name: "ReadMemories_invalid_data_type",
			op:   ReplayOp{Type: OpReadMemories, Key: key, Data: "wrong_type"},
		},
		{
			name: "SearchMemories_invalid_data_type",
			op:   ReplayOp{Type: OpSearchMemories, Key: key, Data: "wrong_type"},
		},
		{
			name: "Unknown_op_type",
			op:   ReplayOp{Type: "NonExistentOp", Key: key},
		},
	}

	for _, tt := range directTests {
		t.Run(tt.name, func(t *testing.T) {
			sessSvc, memSvc, err := inmem.New()
			if err != nil {
				t.Fatalf("create services: %v", err)
			}
			defer sessSvc.Close()
			defer memSvc.Close()

			result := &BackendResult{}
			err = executeOp(ctx, sessSvc, memSvc, tt.op, result)
			if err == nil {
				t.Error("expected error but got nil")
			}
		})
	}

	// Tests that require an existing session first, then pass invalid data type.
	// These cover the "invalid data type" check AFTER the nil session check.
	withSessionTests := []struct {
		name string
		op   ReplayOp
	}{
		{
			name: "AppendEvent_invalid_data_type",
			op:   ReplayOp{Type: OpAppendEvent, Key: key, Data: "wrong_type"},
		},
		{
			name: "UpdateSessionState_invalid_data_type",
			op:   ReplayOp{Type: OpUpdateSessionState, Key: key, Data: "wrong_type"},
		},
		{
			name: "CreateSessionSummary_invalid_data_type",
			op:   ReplayOp{Type: OpCreateSessionSummary, Key: key, Data: "wrong_type"},
		},
		{
			name: "AppendTrackEvent_invalid_data_type",
			op:   ReplayOp{Type: OpAppendTrackEvent, Key: key, Data: "wrong_type"},
		},
		{
			name: "ConcurrentAppendEvents_invalid_data_type",
			op:   ReplayOp{Type: OpConcurrentAppendEvents, Key: key, Data: "wrong_type"},
		},
	}

	for _, tt := range withSessionTests {
		t.Run(tt.name, func(t *testing.T) {
			sessSvc, memSvc, err := inmem.New()
			if err != nil {
				t.Fatalf("create services: %v", err)
			}
			defer sessSvc.Close()
			defer memSvc.Close()

			// Create a session first so the nil-session check passes.
			result := &BackendResult{}
			sess, err := sessSvc.CreateSession(ctx, key, nil)
			if err != nil {
				t.Fatalf("create session: %v", err)
			}
			result.Session = sess

			// Now execute the op with invalid data type.
			err = executeOp(ctx, sessSvc, memSvc, tt.op, result)
			if err == nil {
				t.Error("expected error but got nil")
			}
		})
	}
}
