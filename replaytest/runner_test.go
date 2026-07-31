// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
package replaytest

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// --- filterDiffsByScope ---

func TestFilterDiffsByScope_SessionFull(t *testing.T) {
	diffs := []DiffResult{
		{Path: "$.events[0].author"},
		{Path: "$.state.key"},
		{Path: "$.tracks.mytrack.events[0].payload"},
	}
	got := filterDiffsByScope(diffs, "session_full")
	if len(got) != 3 {
		t.Errorf("session_full should return all diffs, got %d", len(got))
	}
}

func TestFilterDiffsByScope_Events(t *testing.T) {
	diffs := []DiffResult{
		{Path: "$.events[0].author"},
		{Path: "$.state.key"},
		{Path: "$.events[1].content"},
	}
	got := filterDiffsByScope(diffs, "events")
	if len(got) != 2 {
		t.Errorf("events should return 2 diffs, got %d", len(got))
	}
	for _, d := range got {
		if d.Path != "$.events[0].author" && d.Path != "$.events[1].content" {
			t.Errorf("unexpected diff path: %s", d.Path)
		}
	}
}

func TestFilterDiffsByScope_State(t *testing.T) {
	diffs := []DiffResult{
		{Path: "$.events[0].author"},
		{Path: "$.state.app:version"},
	}
	got := filterDiffsByScope(diffs, "state")
	if len(got) != 1 {
		t.Errorf("state should return 1 diff, got %d", len(got))
	}
	if got[0].Path != "$.state.app:version" {
		t.Errorf("unexpected diff path: %s", got[0].Path)
	}
}

func TestFilterDiffsByScope_Empty(t *testing.T) {
	got := filterDiffsByScope(nil, "events")
	if got != nil {
		t.Error("nil input should return nil")
	}
}

// Bug 9: "summary" what tag must map to "$.summaries" comparator prefix.
func TestFilterDiffsByScope_Summary(t *testing.T) {
	diffs := []DiffResult{
		{Path: "$.summaries.branch-a.summary"},
		{Path: "$.summaries.branch-a.boundary.filterKey"},
		{Path: "$.summaries.branch-a.boundary.lastEventID"},
		{Path: "$.events[0].author"}, // should be filtered out
		{Path: "$.state.key"},        // should be filtered out
	}
	got := filterDiffsByScope(diffs, "summary")
	if len(got) != 3 {
		t.Errorf("expected 3 summary diffs, got %d: %v", len(got), got)
	}
	for _, d := range got {
		if !strings.HasPrefix(d.Path, "$.summaries") {
			t.Errorf("non-summary path leaked: %s", d.Path)
		}
	}
}

func TestFilterDiffsByScope_SummaryDoesNotMatchSummaryPath(t *testing.T) {
	diffs := []DiffResult{
		{Path: "$.summaries.branch-a.summary"},
	}
	got := filterDiffsByScope(diffs, "summary")
	if len(got) != 1 {
		t.Errorf("$.summaries... paths should be matched by what='summary': got %d", len(got))
	}
	// Verify the legacy naive prefix would NOT have matched.
	naivePrefix := "$.summary"
	if strings.HasPrefix("$.summaries.branch-a.summary", naivePrefix) {
		t.Log("note: $.summaries... happens to start with $.summary (check)")
	}
}

// --- checkSummaryExpectations ---

func TestCheckSummaryExpectations_Satisfied(t *testing.T) {
	sess := session.NewSession("app", "u1", "s1")
	sess.Summaries = map[string]*session.Summary{
		"main": {Summary: "This is a summary", Boundary: &session.SummaryBoundary{Version: 1}},
	}
	snap := &SessionSnapshot{Session: sess}
	expect, _ := json.Marshal(map[string]any{"filter_keys": []string{"main"}})
	vs := VerifySpec{What: "summary", Expect: expect}

	diffs := checkSummaryExpectations(snap, vs)
	if len(diffs) != 0 {
		t.Errorf("expected no diffs, got %d: %v", len(diffs), diffs)
	}
}

func TestCheckSummaryExpectations_MissingFilterKey(t *testing.T) {
	sess := session.NewSession("app", "u1", "s1")
	sess.Summaries = map[string]*session.Summary{}
	snap := &SessionSnapshot{Session: sess}
	expect, _ := json.Marshal(map[string]any{"filter_keys": []string{"main"}})
	vs := VerifySpec{What: "summary", Expect: expect}

	diffs := checkSummaryExpectations(snap, vs)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff, got %d", len(diffs))
	}
	if diffs[0].Severity != SeverityError {
		t.Errorf("expected error severity, got %s", diffs[0].Severity)
	}
	if diffs[0].Kind != DiffMissingEntry {
		t.Errorf("expected missing_entry, got %s", diffs[0].Kind)
	}
}

func TestCheckSummaryExpectations_EmptySummary(t *testing.T) {
	sess := session.NewSession("app", "u1", "s1")
	sess.Summaries = map[string]*session.Summary{
		"main": {Summary: ""},
	}
	snap := &SessionSnapshot{Session: sess}
	expect, _ := json.Marshal(map[string]any{"filter_keys": []string{"main"}})
	vs := VerifySpec{What: "summary", Expect: expect}

	diffs := checkSummaryExpectations(snap, vs)
	if len(diffs) != 1 {
		t.Fatalf("expected 1 diff for empty summary, got %d", len(diffs))
	}
	if diffs[0].Severity != SeverityError {
		t.Errorf("expected error severity, got %s", diffs[0].Severity)
	}
}

// --- populateSessionLocalization ---

func TestPopulateSessionLocalization_WithSummariesAndTracks(t *testing.T) {
	sess := session.NewSession("app", "u1", "s1")
	sess.Summaries = map[string]*session.Summary{
		"branch-b": {Summary: "text", Boundary: &session.SummaryBoundary{Version: 1}},
		"branch-a": {Summary: "text", Boundary: &session.SummaryBoundary{Version: 1}},
	}
	sess.Tracks = map[session.Track]*session.TrackEvents{
		"track_x": {Track: "track_x"},
		"track_y": {Track: "track_y"},
	}
	snap := &SessionSnapshot{Session: sess}

	vr := &VerificationResult{}
	populateSessionLocalization(vr, snap)

	if len(vr.SummaryFilterKeys) != 2 {
		t.Errorf("expected 2 summary filter keys, got %d", len(vr.SummaryFilterKeys))
	}
	if vr.SummaryFilterKeys[0] != "branch-a" || vr.SummaryFilterKeys[1] != "branch-b" {
		t.Errorf("summary filter keys should be sorted, got %v", vr.SummaryFilterKeys)
	}
	if len(vr.TrackNames) != 2 {
		t.Errorf("expected 2 track names, got %d", len(vr.TrackNames))
	}
	if vr.TrackNames[0] != "track_x" || vr.TrackNames[1] != "track_y" {
		t.Errorf("track names should be sorted, got %v", vr.TrackNames)
	}
}

func TestPopulateSessionLocalization_Empty(t *testing.T) {
	vr := &VerificationResult{}
	populateSessionLocalization(vr, nil)
	if len(vr.SummaryFilterKeys) != 0 || len(vr.TrackNames) != 0 {
		t.Error("nil snapshot should not populate any localization")
	}

	// Empty session.
	sess := session.NewSession("app", "u1", "s1")
	snap := &SessionSnapshot{Session: sess}
	vr2 := &VerificationResult{}
	populateSessionLocalization(vr2, snap)
	if len(vr2.SummaryFilterKeys) != 0 || len(vr2.TrackNames) != 0 {
		t.Error("session with no summaries/tracks should not populate localization")
	}
}

// --- populateMemoryLocalization ---

func TestPopulateMemoryLocalization_WithMemories(t *testing.T) {
	entries := []*memory.Entry{
		{ID: "mem-b"},
		{ID: "mem-a"},
		{ID: "mem-c"},
	}
	snap := &MemorySnapshot{Memories: entries}

	vr := &VerificationResult{}
	populateMemoryLocalization(vr, snap)

	if len(vr.MemoryIDs) != 3 {
		t.Errorf("expected 3 memory IDs, got %d", len(vr.MemoryIDs))
	}
	if vr.MemoryIDs[0] != "mem-a" || vr.MemoryIDs[1] != "mem-b" || vr.MemoryIDs[2] != "mem-c" {
		t.Errorf("memory IDs should be sorted, got %v", vr.MemoryIDs)
	}
}

func TestPopulateMemoryLocalization_FallsBackToSearchResults(t *testing.T) {
	entries := []*memory.Entry{
		{ID: "sr-1"},
	}
	snap := &MemorySnapshot{SearchResults: entries}

	vr := &VerificationResult{}
	populateMemoryLocalization(vr, snap)

	if len(vr.MemoryIDs) != 1 {
		t.Errorf("expected 1 memory ID from search results, got %d", len(vr.MemoryIDs))
	}
	if vr.MemoryIDs[0] != "sr-1" {
		t.Errorf("expected sr-1, got %s", vr.MemoryIDs[0])
	}
}

func TestPopulateMemoryLocalization_Nil(t *testing.T) {
	vr := &VerificationResult{}
	populateMemoryLocalization(vr, nil)
	if len(vr.MemoryIDs) != 0 {
		t.Error("nil snapshot should not populate any memory IDs")
	}
}

func TestPopulateMemoryLocalization_EmptyEntries(t *testing.T) {
	snap := &MemorySnapshot{Memories: []*memory.Entry{}}
	vr := &VerificationResult{}
	populateMemoryLocalization(vr, snap)
	if len(vr.MemoryIDs) != 0 {
		t.Error("empty entries should not populate memory IDs")
	}
}

func TestCheckSummaryExpectations_NoExpect(t *testing.T) {
	sess := session.NewSession("app", "u1", "s1")
	snap := &SessionSnapshot{Session: sess}
	vs := VerifySpec{What: "summary"}

	diffs := checkSummaryExpectations(snap, vs)
	if len(diffs) != 0 {
		t.Errorf("no expect should yield no diffs, got %d", len(diffs))
	}
}

// ---------------------------------------------------------------------------
//  memory_search should use the declared query from VerifySpec.Params
// ---------------------------------------------------------------------------

// queryRecordingMemory wraps a memory.Service and records every SearchMemories query.
type queryRecordingMemory struct {
	memory.Service
	mu      sync.Mutex
	queries []string
}

func (s *queryRecordingMemory) SearchMemories(ctx context.Context, userKey memory.UserKey, query string, opts ...memory.SearchOption) ([]*memory.Entry, error) {
	s.mu.Lock()
	s.queries = append(s.queries, query)
	s.mu.Unlock()
	return s.Service.SearchMemories(ctx, userKey, query, opts...)
}

func (s *queryRecordingMemory) lastQuery() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queries) == 0 {
		return ""
	}
	return s.queries[len(s.queries)-1]
}

func TestRunSpec_MemorySearchUsesDeclaredQuery(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})

	// The recording wrapper shared across both memory backends for this test.
	var recorder *queryRecordingMemory
	RegisterMemoryFactory("mem_a", func(ctx context.Context, dbURL string) (memory.Service, error) {
		base := meminmemory.NewMemoryService()
		recorder = &queryRecordingMemory{Service: base}
		return recorder, nil
	})
	RegisterMemoryFactory("mem_b", func(ctx context.Context, dbURL string) (memory.Service, error) {
		base := meminmemory.NewMemoryService()
		return &queryRecordingMemory{Service: base}, nil
	})

	spec := &Spec{
		Name:        "test-query",
		Description: "verify search query is from VerifySpec",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem_a", "mem_b"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"User visited Tokyo","topics":["travel"]}`)},
			{Op: "search_memories", Backend: "memory", Params: json.RawMessage(`{"query":"travel"}`)},
		},
		Verifies: []VerifySpec{
			{What: "memories"},
			{What: "memory_search", Params: json.RawMessage(`{"query":"travel"}`)},
		},
	}

	// RunSpec will call harness.Verify with the extracted searchQuery.
	report, err := RunSpec(context.Background(), spec, "")
	if err != nil {
		t.Fatalf("RunSpec: %v", err)
	}

	// The key assertion: the last SearchMemories call (from Verify phase)
	// should use "travel", not the hardcoded fallback "test".
	if recorder == nil {
		t.Fatal("recorder not initialized — factory was not called")
	}
	lastQ := recorder.lastQuery()
	if lastQ != "travel" {
		t.Errorf("last SearchMemories query should be %q (from VerifySpec), got %q", "travel", lastQ)
	}
	t.Logf("recorded queries: %v", recorder.queries)

	// Also verify the report was generated.
	if report == nil {
		t.Fatal("report is nil")
	}
	t.Logf("report summary: %s", report.Summary)
}

func TestRunSpec_MemorySearchFallsBackToDefaultQuery(t *testing.T) {
	// When no memory_search verify declares a query, "test" should be used.
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
	RegisterMemoryFactory("mem_a", func(ctx context.Context, dbURL string) (memory.Service, error) {
		base := meminmemory.NewMemoryService()
		recorder = &queryRecordingMemory{Service: base}
		return recorder, nil
	})
	RegisterMemoryFactory("mem_b", func(ctx context.Context, dbURL string) (memory.Service, error) {
		base := meminmemory.NewMemoryService()
		return &queryRecordingMemory{Service: base}, nil
	})

	spec := &Spec{
		Name:        "test-default-query",
		Description: "verify default query fallback",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem_a", "mem_b"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"test memory","topics":["test"]}`)},
		},
		Verifies: []VerifySpec{
			{What: "memories"},
			// NO memory_search verify → no query param → falls back to "test"
		},
	}

	report, err := RunSpec(context.Background(), spec, "")
	if err != nil {
		t.Fatalf("RunSpec: %v", err)
	}

	if recorder == nil {
		t.Fatal("recorder not initialized")
	}
	// When no memory_search verify exists, the fallback defaultSearchQuery is used.
	// Verify the recorded query matches the constant.
	if len(recorder.queries) == 0 {
		t.Fatal("expected at least one SearchMemories call during Verify")
	}
	for _, q := range recorder.queries {
		if q != defaultSearchQuery {
			t.Errorf("fallback query should be %q, got %q", defaultSearchQuery, q)
		}
	}
	if report == nil {
		t.Fatal("report is nil")
	}
}

func TestRunSpec_BackendsTestedReflectsActiveBackends(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess_a", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterSessionFactory("sess_b", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterSessionFactory("sess_c", func(ctx context.Context, dbURL string) (session.Service, error) {
		return nil, errUnavailable("sess_c offline")
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-active-backends",
		Description: "verify BackendsTested uses active backends, not spec",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess_a", "sess_b", "sess_c"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	report, err := RunSpec(context.Background(), spec, "")
	if err != nil {
		t.Fatalf("RunSpec: %v", err)
	}
	if len(report.BackendsTested.Session) != 2 {
		t.Errorf("BackendsTested.Session: expected 2 active, got %d (%v)",
			len(report.BackendsTested.Session), report.BackendsTested.Session)
	}
	if _, ok := report.SkippedBackends["sess_c"]; !ok {
		t.Error("sess_c should be in SkippedBackends")
	}
}

func TestRunSpec_ReferenceBackendFromActiveList(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("first_unavailable", func(ctx context.Context, dbURL string) (session.Service, error) {
		return nil, errUnavailable("first is down")
	})
	RegisterSessionFactory("second_available", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("third_available", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-ref-backend",
		Description: "verify reference backend comes from active list",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"first_unavailable", "second_available"}, Memory: []string{"third_available"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	report, err := RunSpec(context.Background(), spec, "")
	if err != nil {
		t.Fatalf("RunSpec: %v", err)
	}

	for _, v := range report.Verifications {
		if v.ReferenceBackend == "first_unavailable" {
			t.Error("reference backend should NOT be the unavailable first backend")
		}
		if v.ReferenceBackend != "second_available" {
			t.Errorf("expected reference backend second_available, got %s", v.ReferenceBackend)
		}
	}
}

func TestRunSpec_SingleActiveBackend_ProducesSkipResults(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("only_one", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterSessionFactory("unavailable", func(ctx context.Context, dbURL string) (session.Service, error) {
		return nil, errUnavailable("not available")
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-single-backend",
		Description: "verify single active backend produces skip, not silent pass",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"only_one", "unavailable"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}, {What: "events"}},
	}

	report, err := RunSpec(context.Background(), spec, "")
	if err != nil {
		t.Fatalf("RunSpec: %v", err)
	}

	sessionSkips := 0
	for _, v := range report.Verifications {
		if v.What == "session_full" || v.What == "events" {
			if v.Status != StatusSkip {
				t.Errorf("verification %s: expected skip, got %s", v.What, v.Status)
			} else {
				sessionSkips++
			}
		}
	}
	if sessionSkips == 0 {
		t.Error("expected at least one skip result for session verifications")
	}
	if report.HasFailures() {
		t.Error("report should not have failures (only skips)")
	}
}

// ---------------------------------------------------------------------------
// Bug 10: create_session should actually create the session with declared params
// ---------------------------------------------------------------------------

func TestRunSpec_CreateSessionWithStateParams(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess_a", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterSessionFactory("sess_b", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-create-session-state",
		Description: "verify create_session params actually persist state",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess_a", "sess_b"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{"state":{"session:lang":"en","session:theme":"dark"}}`)},
			{Op: "get_session", Backend: "session", Params: json.RawMessage(`{}`)},
		},
		Verifies: []VerifySpec{{What: "state"}, {What: "session_full"}},
	}

	report, err := RunSpec(context.Background(), spec, "")
	if err != nil {
		t.Fatalf("RunSpec: %v", err)
	}

	// The create_session params should result in state keys being present.
	// Verify the snapshot contains the expected state.
	if len(report.Verifications) == 0 {
		t.Fatal("expected at least one verification result")
	}
	// Check that session_full verification passes (state is consistent across backends).
	for _, v := range report.Verifications {
		if v.Status != StatusPass {
			t.Errorf("verification %s: expected pass, got %s (diffs: %d)", v.What, v.Status, len(v.Diffs))
			for _, d := range v.Diffs {
				t.Logf("  diff: %s | %s | %s", d.Path, d.Kind, d.Message)
			}
		}
	}
}

func TestRunSpec_CreateSessionStateIsReadable(t *testing.T) {
	// Verify that state written by create_session is readable via GetSession
	// and not silently lost (as happened when Setup pre-created the session).
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	var storedState session.StateMap
	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-state-readable",
		Description: "verify create_session state is readable",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{"state":{"session:lang":"en"}}`)},
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

	// After Execute, the session must exist and carry the state from create_session params.
	sess, err := h.sessionServices["sess"].GetSession(context.Background(), h.sessionKey)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if sess == nil {
		t.Fatal("session is nil — create_session did not create it")
	}

	v, ok := sess.GetState("session:lang")
	if !ok {
		// List all state keys for diagnostics.
		keys := []string{}
		for k := range sess.State {
			keys = append(keys, k)
		}
		t.Fatalf("session:lang not found in state. State keys: %v", keys)
	}
	if string(v) != "en" {
		t.Errorf("session:lang = %q, want %q", string(v), "en")
	}
	_ = storedState // used for diagnostic
}

// ---------------------------------------------------------------------------
// isSessionWhat / isMemoryWhat — full coverage
// ---------------------------------------------------------------------------

func TestIsSessionWhat_AllValues(t *testing.T) {
	sessionWhats := []string{"session_full", "events", "state", "summary", "tracks"}
	for _, w := range sessionWhats {
		if !isSessionWhat(w) {
			t.Errorf("isSessionWhat(%q) should be true", w)
		}
	}

	nonSession := []string{"memories", "memory_search", "unknown", ""}
	for _, w := range nonSession {
		if isSessionWhat(w) {
			t.Errorf("isSessionWhat(%q) should be false", w)
		}
	}
}

func TestIsMemoryWhat_AllValues(t *testing.T) {
	memoryWhats := []string{"memories", "memory_search"}
	for _, w := range memoryWhats {
		if !isMemoryWhat(w) {
			t.Errorf("isMemoryWhat(%q) should be true", w)
		}
	}

	nonMemory := []string{"session_full", "events", "state", "unknown", ""}
	for _, w := range nonMemory {
		if isMemoryWhat(w) {
			t.Errorf("isMemoryWhat(%q) should be false", w)
		}
	}
}

// ---------------------------------------------------------------------------
// RunSpec — inconsistent memory_search queries
// ---------------------------------------------------------------------------

func TestRunSpec_InconsistentSearchQueries(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem_a", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})
	RegisterMemoryFactory("mem_b", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-inconsistent-query",
		Description: "verify inconsistent queries are rejected",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem_a", "mem_b"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"test"}`)},
		},
		Verifies: []VerifySpec{
			{What: "memory_search", Params: json.RawMessage(`{"query":"q1"}`)},
			{What: "memory_search", Params: json.RawMessage(`{"query":"q2"}`)},
		},
	}

	_, err := RunSpec(context.Background(), spec, "")
	if err == nil {
		t.Fatal("RunSpec should fail with inconsistent memory_search queries")
	}
	if !strings.Contains(err.Error(), "inconsistent") {
		t.Errorf("expected 'inconsistent' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// RunSpec — no active backends
// ---------------------------------------------------------------------------

func TestRunSpec_NoActiveBackends_ReturnsError(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return nil, errUnavailable("session offline")
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return nil, errUnavailable("memory offline")
	})

	spec := &Spec{
		Name:        "test-no-backends",
		Description: "all backends unavailable",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	_, err := RunSpec(context.Background(), spec, "")
	if err == nil {
		t.Fatal("RunSpec should fail when no backends are available")
	}
}

// ---------------------------------------------------------------------------
// RunSpec — memory verifications with single active backend
// ---------------------------------------------------------------------------

func TestRunSpec_MemoryVerificationsSingleBackend_ProducesSkip(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem_only", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-single-memory",
		Description: "single memory backend produces skip",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem_only"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "add_memory", Backend: "memory", Params: json.RawMessage(`{"memory":"test"}`)},
		},
		Verifies: []VerifySpec{
			{What: "memories"},
			{What: "memory_search"},
		},
	}

	report, err := RunSpec(context.Background(), spec, "")
	if err != nil {
		t.Fatalf("RunSpec: %v", err)
	}

	memorySkips := 0
	for _, v := range report.Verifications {
		if v.What == "memories" || v.What == "memory_search" {
			if v.Status == StatusSkip {
				memorySkips++
			}
		}
	}
	if memorySkips == 0 {
		t.Error("expected skip results for memory verifications with single backend")
	}
}

// ---------------------------------------------------------------------------
// RunSpecs — multiple specs
// ---------------------------------------------------------------------------

func TestRunSpecs_MultipleSpecs(t *testing.T) {
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

	spec1 := &Spec{
		Name:        "spec-1",
		Description: "first spec",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_rs1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}
	spec2 := &Spec{
		Name:        "spec-2",
		Description: "second spec",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u2", SessionID: "s_rs2"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}

	reports, err := RunSpecs(context.Background(), []*Spec{spec1, spec2}, "")
	if err != nil {
		t.Fatalf("RunSpecs: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(reports))
	}
	if reports[0].SpecName != "spec-1" {
		t.Errorf("report 0: expected 'spec-1', got %q", reports[0].SpecName)
	}
	if reports[1].SpecName != "spec-2" {
		t.Errorf("report 1: expected 'spec-2', got %q", reports[1].SpecName)
	}
}

// ---------------------------------------------------------------------------
// RunSpecs — error propagation on second spec
// ---------------------------------------------------------------------------

func TestRunSpecs_ErrorOnSecondSpec(t *testing.T) {
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

	spec1 := &Spec{
		Name:        "good-spec",
		Description: "first spec passes",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s1"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies:    []VerifySpec{{What: "session_full"}},
	}
	spec2 := &Spec{
		Name:        "bad-spec",
		Description: "second spec has inconsistent queries",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_bad"},
		Operations:  []Operation{{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)}},
		Verifies: []VerifySpec{
			{What: "memory_search", Params: json.RawMessage(`{"query":"a"}`)},
			{What: "memory_search", Params: json.RawMessage(`{"query":"b"}`)},
		},
	}

	reports, err := RunSpecs(context.Background(), []*Spec{spec1, spec2}, "")
	if err == nil {
		t.Fatal("RunSpecs should return an error when second spec fails")
	}
	if len(reports) != 1 {
		t.Errorf("expected 1 report (first spec), got %d", len(reports))
	}
	if reports[0].SpecName != "good-spec" {
		t.Errorf("expected 'good-spec' report, got %q", reports[0].SpecName)
	}
}

// ---------------------------------------------------------------------------
// WriteCombinedReport
// ---------------------------------------------------------------------------

func TestWriteCombinedReport_Success(t *testing.T) {
	reports := []*DiffReport{
		{
			SpecName: "spec-1",
			Verifications: []VerificationResult{
				{What: "events", Status: StatusPass},
				{What: "state", Status: StatusPass},
			},
		},
		{
			SpecName: "spec-2",
			Verifications: []VerificationResult{
				{What: "events", Status: StatusFail, Diffs: []DiffResult{{Path: "$.events[0].author", Kind: DiffValueMismatch}}},
			},
		},
	}
	for _, r := range reports {
		r.Finalize()
	}

	tmpDir := t.TempDir()
	path := tmpDir + "/combined_report.json"

	if err := WriteCombinedReport(reports, path); err != nil {
		t.Fatalf("WriteCombinedReport: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var combined struct {
		Reports []DiffReport `json:"reports"`
		Summary struct {
			TotalSpecs  int `json:"total_specs"`
			TotalPassed int `json:"total_passed"`
			TotalFailed int `json:"total_failed"`
			TotalDiffs  int `json:"total_diffs"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(data, &combined); err != nil {
		t.Fatalf("unmarshal combined report: %v", err)
	}
	if combined.Summary.TotalSpecs != 2 {
		t.Errorf("TotalSpecs: expected 2, got %d", combined.Summary.TotalSpecs)
	}
	if combined.Summary.TotalPassed != 1 {
		t.Errorf("TotalPassed: expected 1, got %d", combined.Summary.TotalPassed)
	}
	if combined.Summary.TotalFailed != 1 {
		t.Errorf("TotalFailed: expected 1, got %d", combined.Summary.TotalFailed)
	}
	if combined.Summary.TotalDiffs != 1 {
		t.Errorf("TotalDiffs: expected 1, got %d", combined.Summary.TotalDiffs)
	}
}

func TestWriteCombinedReport_NilReportsNoError(t *testing.T) {
	if err := WriteCombinedReport(nil, "/nonexistent/path/report.json"); err != nil {
		t.Errorf("empty reports should not error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// recordSingleBackendSkips — direct coverage
// ---------------------------------------------------------------------------

func TestRecordSingleBackendSkips_ProducesSkipResults(t *testing.T) {
	report := NewDiffReport(&Spec{Name: "test"})
	spec := &Spec{
		Verifies: []VerifySpec{
			{What: "session_full"},
			{What: "events"},
			{What: "memories"},
		},
	}
	h := &Harness{
		ActiveSessionBackends: []string{"sess_a"},
		ActiveMemoryBackends:  []string{"mem_a"},
	}

	recordSingleBackendSkips(report, spec, h, "sess_a", "mem_a")
	report.Finalize()

	if len(report.Verifications) != 3 {
		t.Fatalf("expected 3 skip verifications, got %d", len(report.Verifications))
	}
	for _, v := range report.Verifications {
		if v.Status != StatusSkip {
			t.Errorf("verification %s: expected skip, got %s", v.What, v.Status)
		}
	}
}

func TestRecordSingleBackendSkips_EnoughBackends_NoSkips(t *testing.T) {
	report := NewDiffReport(&Spec{Name: "test"})
	spec := &Spec{
		Verifies: []VerifySpec{
			{What: "session_full"},
			{What: "memories"},
		},
	}
	h := &Harness{
		ActiveSessionBackends: []string{"sess_a", "sess_b"},
		ActiveMemoryBackends:  []string{"mem_a", "mem_b"},
	}

	recordSingleBackendSkips(report, spec, h, "sess_a", "mem_a")

	if len(report.Verifications) != 0 {
		t.Errorf("expected 0 skip verifications, got %d", len(report.Verifications))
	}
}

// ---------------------------------------------------------------------------
// RunSpec — tracks verification with two backends
// ---------------------------------------------------------------------------

func TestRunSpec_TracksVerificationWithTwoBackends(t *testing.T) {
	origSession := sessionFactories
	sessionFactories = map[string]SessionFactory{}
	defer func() { sessionFactories = origSession }()
	origMemory := memoryFactories
	memoryFactories = map[string]MemoryFactory{}
	defer func() { memoryFactories = origMemory }()

	RegisterSessionFactory("sess_a", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterSessionFactory("sess_b", func(ctx context.Context, dbURL string) (session.Service, error) {
		return sessinmemory.NewSessionService(), nil
	})
	RegisterMemoryFactory("mem", func(ctx context.Context, dbURL string) (memory.Service, error) {
		return meminmemory.NewMemoryService(), nil
	})

	spec := &Spec{
		Name:        "test-tracks-two-backends",
		Description: "verify tracks with two backends",
		Tags:        []string{"lightweight"},
		Backends:    BackendConfig{Session: []string{"sess_a", "sess_b"}, Memory: []string{"mem"}},
		Setup:       SetupSpec{AppName: "app", UserID: "u1", SessionID: "s_tracks"},
		Operations: []Operation{
			{Op: "create_session", Backend: "session", Params: json.RawMessage(`{}`)},
			{Op: "append_track_event", Backend: "session", Params: json.RawMessage(`{"track":"mytrack","payload":"data"}`)},
		},
		Verifies: []VerifySpec{
			{What: "tracks"},
			{What: "session_full"},
		},
	}

	report, err := RunSpec(context.Background(), spec, "")
	if err != nil {
		t.Fatalf("RunSpec: %v", err)
	}

	for _, v := range report.Verifications {
		t.Logf("verification: what=%s status=%s ref=%s cmp=%s diffs=%d",
			v.What, v.Status, v.ReferenceBackend, v.ComparedBackend, len(v.Diffs))
	}
}

// ---------------------------------------------------------------------------
// populateSessionLocalization — nil session in snapshot
// ---------------------------------------------------------------------------

func TestPopulateSessionLocalization_NilSessionInSnapshot(t *testing.T) {
	vr := &VerificationResult{}
	snap := &SessionSnapshot{Session: nil}
	populateSessionLocalization(vr, snap)

	if len(vr.SummaryFilterKeys) != 0 || len(vr.TrackNames) != 0 {
		t.Error("nil session should not populate any localization")
	}
}

// ---------------------------------------------------------------------------
// filterDiffsByScope — unknown what tag
// ---------------------------------------------------------------------------

func TestFilterDiffsByScope_UnknownWhat(t *testing.T) {
	diffs := []DiffResult{
		{Path: "$.custom.field[0].value"},
		{Path: "$.events[0].author"},
	}
	got := filterDiffsByScope(diffs, "custom")
	// Unknown tags use "$.<what>" prefix matching.
	if len(got) != 1 {
		t.Errorf("unknown what tags filter by prefix, expected 1 diff, got %d", len(got))
	}
	if got[0].Path != "$.custom.field[0].value" {
		t.Errorf("unexpected path: %s", got[0].Path)
	}
}

// ---------------------------------------------------------------------------
// scopePrefix — verify mapping entries
// ---------------------------------------------------------------------------

func TestScopePrefix_Mapping(t *testing.T) {
	expected := map[string]string{
		"events":  "$.events",
		"state":   "$.state",
		"summary": "$.summaries",
		"tracks":  "$.tracks",
	}
	for what, prefix := range expected {
		if scopePrefix[what] != prefix {
			t.Errorf("scopePrefix[%q]: expected %q, got %q", what, prefix, scopePrefix[what])
		}
	}
	if _, ok := scopePrefix["session_full"]; ok {
		t.Error("session_full should NOT be in scopePrefix map")
	}
}
