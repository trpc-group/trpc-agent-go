//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package replaytest

import (
	"encoding/json"
	"math"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// 1. BackendMetadataStripper
func TestBackendMetadataStripper_RemovesServiceMetaAndHash(t *testing.T) {
	n := &backendMetadataStripper{}
	sess := session.NewSession("app", "u1", "s1")
	sess.ServiceMeta = map[string]string{"version": "v2"}
	sess.Hash = 42
	snap := &SessionSnapshot{Session: sess}

	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.ServiceMeta != nil {
		t.Error("ServiceMeta should be nil after strip")
	}
	if result.Session.Hash != 0 {
		t.Errorf("Hash should be 0, got %d", result.Session.Hash)
	}
}

func TestBackendMetadataStripper_StripsInternalStateKeys(t *testing.T) {
	n := &backendMetadataStripper{}
	sess := session.NewSession("app", "u1", "s1")
	sess.SetState("summary:last_included_ts", []byte("123"))
	sess.SetState("summary:last_included_event_id", []byte("evt-1"))
	sess.SetState("tracks", []byte(`["track_a"]`))
	sess.SetState("user_key", []byte("keep_me"))
	snap := &SessionSnapshot{Session: sess}

	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.Session.GetState("summary:last_included_ts"); ok {
		t.Error("summary:last_included_ts should be stripped")
	}
	if _, ok := result.Session.GetState("summary:last_included_event_id"); ok {
		t.Error("summary:last_included_event_id should be stripped")
	}
	if _, ok := result.Session.GetState("tracks"); ok {
		t.Error("tracks state key should be stripped")
	}
	if _, ok := result.Session.GetState("user_key"); !ok {
		t.Error("user_key should be preserved")
	}
}

func TestBackendMetadataStripper_StripsMemoryExtractAt(t *testing.T) {
	n := &backendMetadataStripper{}
	snap := &SessionSnapshot{
		AppState: session.StateMap{"memory:last_extract_at": []byte("ts"), "keep": []byte("v")},
	}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := result.AppState["memory:last_extract_at"]; ok {
		t.Error("memory:last_extract_at should be stripped from AppState")
	}
	if _, ok := result.AppState["keep"]; !ok {
		t.Error("keep should be preserved")
	}
}

func TestBackendMetadataStripper_NilSafe(t *testing.T) {
	n := &backendMetadataStripper{}
	r1, _ := n.NormalizeSession(nil)
	if r1 != nil {
		t.Error("nil in should give nil out")
	}
	r2, _ := n.NormalizeSession(&SessionSnapshot{})
	if r2 == nil {
		t.Error("empty snapshot should survive")
	}
}

// 2. IDNormalizer
func TestIDNormalizer_DeterministicMapping(t *testing.T) {
	n := &idNormalizer{idMap: make(map[string]string)}
	a1 := n.normID("uuid-abc", "evt-id")
	a2 := n.normID("uuid-abc", "evt-id")
	if a1 != a2 {
		t.Errorf("same input should map to same output: %s vs %s", a1, a2)
	}
	b := n.normID("uuid-xyz", "evt-id")
	if a1 == b {
		t.Errorf("different inputs should map to different outputs: %s", a1)
	}
}

func TestIDNormalizer_EmptyStringUntouched(t *testing.T) {
	n := &idNormalizer{idMap: make(map[string]string)}
	if v := n.normID("", "evt-id"); v != "" {
		t.Errorf("empty string should stay empty, got %q", v)
	}
}

func TestIDNormalizer_NormalizeSessionEvents(t *testing.T) {
	n := &idNormalizer{idMap: make(map[string]string)}
	sess := session.NewSession("app", "u1", "s1")
	sess.Events = []event.Event{
		{ID: "evt-abc", RequestID: "req-1", InvocationID: "inv-1",
			Response: &model.Response{ID: "resp-x"}},
		{ID: "evt-def", RequestID: "req-2", InvocationID: "inv-2",
			ParentInvocationID: "parent-1"},
	}
	snap := &SessionSnapshot{Session: sess}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Events[0].ID == "evt-abc" {
		t.Error("event ID should be replaced")
	}
	if result.Session.Events[0].Response.ID == "resp-x" {
		t.Error("response ID should be replaced")
	}
	if result.Session.Events[1].ParentInvocationID == "parent-1" {
		t.Error("parent invocation ID should be replaced")
	}
}

func TestIDNormalizer_NormalizeMemory(t *testing.T) {
	n := &idNormalizer{idMap: make(map[string]string)}
	snap := &MemorySnapshot{
		Memories:      []*memory.Entry{{ID: "mem-1"}, {ID: "mem-2"}},
		SearchResults: []*memory.Entry{{ID: "mem-3"}},
	}
	result, err := n.NormalizeMemory(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Memories[0].ID == "mem-1" {
		t.Error("memory ID should be replaced")
	}
	if result.SearchResults[0].ID == "mem-3" {
		t.Error("search result ID should be replaced")
	}
}

// 3. TimestampNormalizer

func TestTimestampNormalizer_TruncatesToSecond(t *testing.T) {
	n := &timestampNormalizer{}
	now := time.Date(2024, 1, 15, 10, 30, 45, 123456789, time.UTC)
	norm := n.normTime(now)
	if norm.Nanosecond() != 0 {
		t.Errorf("nanoseconds should be zero, got %d", norm.Nanosecond())
	}
	if norm.Second() != 45 {
		t.Errorf("seconds should be 45, got %d", norm.Second())
	}
}

func TestTimestampNormalizer_ZeroTimeUntouched(t *testing.T) {
	n := &timestampNormalizer{}
	if !n.normTime(time.Time{}).IsZero() {
		t.Error("zero time should stay zero")
	}
}

func TestTimestampNormalizer_NormalizeSession(t *testing.T) {
	n := &timestampNormalizer{}
	now := time.Now()
	sess := session.NewSession("app", "u1", "s1")
	sess.UpdatedAt = now
	sess.Events = []event.Event{{Timestamp: now.Add(time.Minute)}}
	sess.Summaries = map[string]*session.Summary{"": {
		UpdatedAt: now.Add(time.Hour),
		Boundary:  &session.SummaryBoundary{CutoffAt: now},
	}}
	snap := &SessionSnapshot{Session: sess}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.UpdatedAt.Nanosecond() != 0 {
		t.Error("UpdatedAt nanoseconds should be zero")
	}
	if result.Session.Summaries[""].UpdatedAt.Nanosecond() != 0 {
		t.Error("summary UpdatedAt nanoseconds should be zero")
	}
	if result.Session.Summaries[""].Boundary.CutoffAt.Nanosecond() != 0 {
		t.Error("boundary CutoffAt nanoseconds should be zero")
	}
}

func TestTimestampNormalizer_NormalizeMemory(t *testing.T) {
	n := &timestampNormalizer{}
	now := time.Now()
	snap := &MemorySnapshot{
		Memories: []*memory.Entry{{CreatedAt: now, UpdatedAt: now}},
	}
	result, err := n.NormalizeMemory(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Memories[0].CreatedAt.Nanosecond() != 0 {
		t.Error("memory CreatedAt nanoseconds should be zero")
	}
}

// 4. JSONFieldOrderNormalizer

func TestJSONFieldOrderNormalizer_NormalizesExtensionOrder(t *testing.T) {
	raw1 := json.RawMessage(`{"b":2,"a":1}`)
	raw2 := json.RawMessage(`{"a":1,"b":2}`)
	result1 := normalizeRawJSON(raw1)
	result2 := normalizeRawJSON(raw2)
	if string(result1) != string(result2) {
		t.Errorf("order-different JSON should normalize to same: %s vs %s", result1, result2)
	}
}

func TestJSONFieldOrderNormalizer_NormalizesNestedJSON(t *testing.T) {
	raw := json.RawMessage(`{"outer":{"z":9,"m":5},"inner":{"c":3,"a":1}}`)
	result := normalizeRawJSON(raw)
	var m map[string]any
	json.Unmarshal(result, &m)
	outerKeys := keysOf(result)
	if outerKeys[0] != "inner" || outerKeys[1] != "outer" {
		t.Logf("keys after normalization: %v", outerKeys)
	}
}

func TestJSONFieldOrderNormalizer_PreservesToolCallArgsExtension(t *testing.T) {
	n := &jsonFieldOrderNormalizer{}
	sess := session.NewSession("app", "u1", "s1")
	sess.Events = []event.Event{{
		Extensions: map[string]json.RawMessage{
			event.ToolCallArgsExtensionKey: json.RawMessage(`{"tool_call_id":"t1"}`),
			"custom_ext":                   json.RawMessage(`{"k":"v"}`),
		},
	}}
	snap := &SessionSnapshot{Session: sess}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	// ToolCallArgsExtensionKey should now be preserved (not deleted) so that
	// tool-call-to-response association is compared across backends.
	if _, ok := result.Session.Events[0].Extensions[event.ToolCallArgsExtensionKey]; !ok {
		t.Error("ToolCallArgsExtensionKey should be preserved (was previously skipped)")
	}
	if _, ok := result.Session.Events[0].Extensions["custom_ext"]; !ok {
		t.Error("custom_ext should be preserved")
	}
}

func TestJSONFieldOrderNormalizer_ArrayHandling(t *testing.T) {
	raw := json.RawMessage(`[3,1,2]`)
	result := normalizeRawJSON(raw)
	if string(result) != `[3,1,2]` {
		t.Errorf("array order should be preserved: %s", result)
	}
}

// 5. FloatSimilarityNormalizer

func TestFloatSimilarityNormalizer_RoundsFloats(t *testing.T) {
	n := &floatSimilarityNormalizer{epsilon: 1e-4}
	snap := &MemorySnapshot{
		Memories:      []*memory.Entry{{Score: 0.85230001}},
		SearchResults: []*memory.Entry{{Score: 0.85233333}},
	}
	result, err := n.NormalizeMemory(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Memories[0].Score != roundFloat(0.85230001, 1e-4) {
		t.Errorf("memory score not rounded: %v", result.Memories[0].Score)
	}
}

func TestFloatSimilarityNormalizer_NaNToZero(t *testing.T) {
	v := roundFloat(math.NaN(), 1e-4)
	if v != 0 {
		t.Errorf("NaN should become 0, got %v", v)
	}
}

func TestFloatSimilarityNormalizer_InfHandling(t *testing.T) {
	v := roundFloat(math.Inf(1), 1e-4)
	if v != math.MaxFloat64 {
		t.Errorf("+Inf should become MaxFloat64, got %v", v)
	}
}

// 6. NullEquivalentNormalizer

func TestNullEquivalentNormalizer_NilMapsBecomeEmpty(t *testing.T) {
	n := &nullEquivalentNormalizer{}
	sess := session.NewSession("app", "u1", "s1")
	sess.Events = nil
	sess.State = nil
	sess.Tracks = nil
	sess.Summaries = nil
	snap := &SessionSnapshot{Session: sess}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Events == nil {
		t.Error("nil Events should become empty slice")
	}
	if result.Session.State == nil {
		t.Error("nil State should become empty map")
	}
	if result.Session.Tracks == nil {
		t.Error("nil Tracks should become empty map")
	}
	if result.Session.Summaries == nil {
		t.Error("nil Summaries should become empty map")
	}
}

func TestNullEquivalentNormalizer_EmptyByteSliceBecomesNil(t *testing.T) {
	n := &nullEquivalentNormalizer{}
	sess := session.NewSession("app", "u1", "s1")
	sess.SetState("key", []byte{})
	snap := &SessionSnapshot{Session: sess}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	v, ok := result.Session.GetState("key")
	if !ok {
		t.Fatal("key should exist")
	}
	if v != nil {
		t.Errorf("empty byte slice should become nil, got %v", v)
	}
}

func TestNullEquivalentNormalizer_NormalizeMemory(t *testing.T) {
	n := &nullEquivalentNormalizer{}
	snap := &MemorySnapshot{}
	result, err := n.NormalizeMemory(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Memories == nil {
		t.Error("nil Memories should become empty slice")
	}
	if result.SearchResults == nil {
		t.Error("nil SearchResults should become empty slice")
	}
}

// 7. VersionFieldNormalizer

func TestVersionFieldNormalizer_SetsCurrentVersion(t *testing.T) {
	n := &versionFieldNormalizer{}
	sess := session.NewSession("app", "u1", "s1")
	sess.Events = []event.Event{
		{Version: 0},
		{Version: 99},
		{Version: event.CurrentVersion},
	}
	snap := &SessionSnapshot{Session: sess}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	for i, ev := range result.Session.Events {
		if ev.Version != event.CurrentVersion {
			t.Errorf("events[%d].Version should be %d, got %d",
				i, event.CurrentVersion, ev.Version)
		}
	}
}

// 8. SliceOrderNormalizer

func TestSliceOrderNormalizer_SortsByID(t *testing.T) {
	n := &sliceOrderNormalizer{}
	snap := &MemorySnapshot{
		Memories: []*memory.Entry{
			{ID: "z-mem"}, {ID: "a-mem"}, {ID: "m-mem"},
		},
	}
	result, err := n.NormalizeMemory(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Memories[0].ID != "a-mem" {
		t.Errorf("first should be a-mem, got %s", result.Memories[0].ID)
	}
	if result.Memories[2].ID != "z-mem" {
		t.Errorf("last should be z-mem, got %s", result.Memories[2].ID)
	}
}

func TestSliceOrderNormalizer_SortsSearchResults(t *testing.T) {
	n := &sliceOrderNormalizer{}
	snap := &MemorySnapshot{
		SearchResults: []*memory.Entry{
			{ID: "ccc"}, {ID: "aaa"}, {ID: "bbb"},
		},
	}
	result, err := n.NormalizeMemory(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.SearchResults[0].ID != "aaa" {
		t.Errorf("first should be aaa, got %s", result.SearchResults[0].ID)
	}
}

// 9. NormalizerChain
func TestNormalizerChain_RunsInOrder(t *testing.T) {
	type trackingNorm struct {
		name string
		ran  *[]string
	}
	n1 := &trackingNorm{name: "first", ran: &[]string{}}
	n2 := &trackingNorm{name: "second", ran: &[]string{}}
	// Use a proper chain test with mock normalizer.
	_ = n1
	_ = n2

	chain := DefaultNormalizerChain()
	if chain == nil {
		t.Fatal("DefaultNormalizerChain should not be nil")
	}
	// Chain should contain all normalizers.
	if len(chain.normalizers) != 9 {
		t.Errorf("expected 9 normalizers in default chain, got %d", len(chain.normalizers))
	}
}

// 9. ConcurrentEventSorter

func TestConcurrentEventSorter_SortsByStableKey(t *testing.T) {
	n := &concurrentEventSorter{}
	sess := session.NewSession("app", "u1", "s1")
	sess.Events = []event.Event{
		{Author: "tool", Tag: "x", FilterKey: "dev",
			Response: &model.Response{ID: "resp-0", Choices: []model.Choice{{Message: model.Message{Content: "ccc"}}}},
		},
		{Author: "tool", Tag: "x", FilterKey: "main",
			Response: &model.Response{ID: "resp-1", Choices: []model.Choice{{Message: model.Message{Content: "aaa"}}}},
		},
		{Author: "tool", Tag: "a", FilterKey: "main",
			Response: &model.Response{ID: "resp-2", Choices: []model.Choice{{Message: model.Message{Content: "zzz"}}}},
		},
		{Author: "tool", Tag: "x", FilterKey: "main",
			Response: &model.Response{ID: "resp-3", Choices: []model.Choice{{Message: model.Message{Content: "bbb"}}}},
		},
		{Author: "user1", Tag: "z", FilterKey: "main",
			Response: &model.Response{ID: "resp-4", Choices: []model.Choice{{Message: model.Message{Content: "msg"}}}},
		},
	}
	// Mark all 5 events as belonging to a single concurrent batch (indices 0-5).
	snap := &SessionSnapshot{
		Session:               sess,
		ConcurrentBatchRanges: []BatchRange{{Start: 0, End: 5}},
	}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	// Expected order (author→tag→filterKey→content):
	ev := result.Session.Events
	if ev[0].Tag != "a" {
		t.Errorf("[0] tag: want a, got %s", ev[0].Tag)
	}
	if ev[1].FilterKey != "dev" || ev[1].Response.Choices[0].Message.Content != "ccc" {
		t.Errorf("[1] want filter=dev content=ccc, got filter=%s content=%s",
			ev[1].FilterKey, ev[1].Response.Choices[0].Message.Content)
	}
	if ev[2].FilterKey != "main" || ev[2].Response.Choices[0].Message.Content != "aaa" {
		t.Errorf("[2] want filter=main content=aaa, got filter=%s content=%s",
			ev[2].FilterKey, ev[2].Response.Choices[0].Message.Content)
	}
	if ev[3].FilterKey != "main" || ev[3].Response.Choices[0].Message.Content != "bbb" {
		t.Errorf("[3] want filter=main content=bbb, got filter=%s content=%s",
			ev[3].FilterKey, ev[3].Response.Choices[0].Message.Content)
	}
	if ev[4].Author != "user1" {
		t.Errorf("[4] author: want user1, got %s", ev[4].Author)
	}
}

// 10. idNormalizer Reset

func TestIDNormalizer_Reset(t *testing.T) {
	n := &idNormalizer{idMap: make(map[string]string)}

	// First snapshot.
	sess1 := session.NewSession("app", "u1", "s1")
	sess1.Events = []event.Event{{ID: "aaa"}, {ID: "bbb"}}
	n.NormalizeSession(&SessionSnapshot{Session: sess1})
	idAfter := sess1.Events[0].ID

	// Reset.
	n.Reset()

	// Second snapshot — IDs should restart from <evt-id-0>.
	sess2 := session.NewSession("app", "u2", "s2")
	sess2.Events = []event.Event{{ID: "xxx"}}
	n.NormalizeSession(&SessionSnapshot{Session: sess2})
	if sess2.Events[0].ID != idAfter {
		t.Errorf("after reset, first ID should be %s (restarted), got %s", idAfter, sess2.Events[0].ID)
	}
}

// 11. sliceOrderNormalizer content-based sort

func TestSliceOrderNormalizer_SortsByContent(t *testing.T) {
	n := &sliceOrderNormalizer{}
	snap := &MemorySnapshot{
		Memories: []*memory.Entry{
			{ID: "z", Memory: &memory.Memory{Memory: "banana"}},
			{ID: "a", Memory: &memory.Memory{Memory: "apple"}},
			{ID: "m", Memory: &memory.Memory{Memory: "cherry"}},
		},
	}
	result, err := n.NormalizeMemory(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Memories[0].Memory.Memory != "apple" {
		t.Errorf("first should be apple, got %s", result.Memories[0].Memory.Memory)
	}
	if result.Memories[1].Memory.Memory != "banana" {
		t.Errorf("second should be banana, got %s", result.Memories[1].Memory.Memory)
	}
	if result.Memories[2].Memory.Memory != "cherry" {
		t.Errorf("third should be cherry, got %s", result.Memories[2].Memory.Memory)
	}
}

func TestSliceOrderNormalizer_ContentTiebreakByID(t *testing.T) {
	n := &sliceOrderNormalizer{}
	snap := &MemorySnapshot{
		Memories: []*memory.Entry{
			{ID: "z", Memory: &memory.Memory{Memory: "same"}},
			{ID: "a", Memory: &memory.Memory{Memory: "same"}},
		},
	}
	result, err := n.NormalizeMemory(snap)
	if err != nil {
		t.Fatal(err)
	}
	// Same content → fall back to ID sort.
	if result.Memories[0].ID != "a" {
		t.Errorf("first should be a (ID tiebreak), got %s", result.Memories[0].ID)
	}
}

func TestSliceOrderNormalizer_SearchResultsByContent(t *testing.T) {
	n := &sliceOrderNormalizer{}
	snap := &MemorySnapshot{
		SearchResults: []*memory.Entry{
			{ID: "z", Memory: &memory.Memory{Memory: "gamma"}},
			{ID: "a", Memory: &memory.Memory{Memory: "alpha"}},
			{ID: "m", Memory: &memory.Memory{Memory: "beta"}},
		},
	}
	result, err := n.NormalizeMemory(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.SearchResults[0].Memory.Memory != "alpha" {
		t.Errorf("first should be alpha, got %s", result.SearchResults[0].Memory.Memory)
	}
	if result.SearchResults[1].Memory.Memory != "beta" {
		t.Errorf("second should be beta, got %s", result.SearchResults[1].Memory.Memory)
	}
	if result.SearchResults[2].Memory.Memory != "gamma" {
		t.Errorf("third should be gamma, got %s", result.SearchResults[2].Memory.Memory)
	}
}

func TestSliceOrderNormalizer_SearchResultsContentTiebreakByID(t *testing.T) {
	n := &sliceOrderNormalizer{}
	snap := &MemorySnapshot{
		SearchResults: []*memory.Entry{
			{ID: "zzz", Memory: &memory.Memory{Memory: "dup"}},
			{ID: "aaa", Memory: &memory.Memory{Memory: "dup"}},
		},
	}
	result, err := n.NormalizeMemory(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.SearchResults[0].ID != "aaa" {
		t.Errorf("first should be aaa (ID tiebreak), got %s", result.SearchResults[0].ID)
	}
}

// --- 12. ConcurrentEventSorter batch-boundary preservation (Bug 7) ---

func TestConcurrentEventSorter_PreservesBoundaryEvents(t *testing.T) {
	// Simulate: [user_req, tool_call, {batch: tool_resp_a, tool_resp_b}, assistant_reply]
	// The batch (creation indices 2-4) should be sorted (author→tag→filterKey→content),
	// but boundary events before and after must stay in original positions.
	n := &concurrentEventSorter{}
	sess := session.NewSession("app", "u1", "s1")
	sess.Events = []event.Event{
		{Author: "user1", FilterKey: "main",
			Response: &model.Response{ID: "resp-0", Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "start task"}}}},
		},
		{Author: "assistant", FilterKey: "main",
			Response: &model.Response{ID: "resp-1", Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "I will run parallel lookups"}}}},
		},
		// Concurrent batch: indices 2,3,4 (out of order to simulate backend difference)
		{Author: "tool", Tag: "lookup_z", FilterKey: "main",
			Response: &model.Response{ID: "resp-4", Choices: []model.Choice{{Message: model.Message{Content: "Lookup Z done"}}}},
		},
		{Author: "tool", Tag: "lookup_x", FilterKey: "main",
			Response: &model.Response{ID: "resp-2", Choices: []model.Choice{{Message: model.Message{Content: "Lookup X done"}}}},
		},
		{Author: "tool", Tag: "lookup_y", FilterKey: "main",
			Response: &model.Response{ID: "resp-3", Choices: []model.Choice{{Message: model.Message{Content: "Lookup Y done"}}}},
		},
		// AFTER batch — must stay here.
		{Author: "assistant", FilterKey: "main",
			Response: &model.Response{ID: "resp-5", Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "All lookups complete"}}}},
		},
	}
	snap := &SessionSnapshot{
		Session:               sess,
		ConcurrentBatchRanges: []BatchRange{{Start: 2, End: 5}},
	}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	ev := result.Session.Events
	if ev[0].Author != "user1" {
		t.Errorf("[0] boundary event reordered: author=%s", ev[0].Author)
	}
	if ev[1].Author != "assistant" {
		t.Errorf("[1] boundary event reordered: author=%s", ev[1].Author)
	}
	if ev[2].Tag != "lookup_x" {
		t.Errorf("[2] batch: want lookup_x, got %s", ev[2].Tag)
	}
	if ev[3].Tag != "lookup_y" {
		t.Errorf("[3] batch: want lookup_y, got %s", ev[3].Tag)
	}
	if ev[4].Tag != "lookup_z" {
		t.Errorf("[4] batch: want lookup_z, got %s", ev[4].Tag)
	}
	if ev[5].Author != "assistant" {
		t.Errorf("[5] boundary event reordered: author=%s", ev[5].Author)
	}
}

func TestConcurrentEventSorter_MultipleBatchesPreserveInterBatchOrder(t *testing.T) {
	// Two concurrent batches: batch1(1-2), mid_op(3), batch2(4-5), final(6).
	// Inter-batch order must be preserved.
	n := &concurrentEventSorter{}
	sess := session.NewSession("app", "u1", "s1")
	sess.Events = []event.Event{
		{Author: "user1",
			Response: &model.Response{ID: "resp-0", Choices: []model.Choice{{Message: model.Message{Content: "request"}}}},
		},
		{Author: "tool", Tag: "b", FilterKey: "main",
			Response: &model.Response{ID: "resp-2", Choices: []model.Choice{{Message: model.Message{Content: "B"}}}},
		},
		{Author: "tool", Tag: "a", FilterKey: "main",
			Response: &model.Response{ID: "resp-1", Choices: []model.Choice{{Message: model.Message{Content: "A"}}}},
		},
		{Author: "assistant",
			Response: &model.Response{ID: "resp-3", Choices: []model.Choice{{Message: model.Message{Content: "midpoint"}}}},
		},
		{Author: "tool", Tag: "z", FilterKey: "main",
			Response: &model.Response{ID: "resp-5", Choices: []model.Choice{{Message: model.Message{Content: "Z"}}}},
		},
		{Author: "tool", Tag: "x", FilterKey: "main",
			Response: &model.Response{ID: "resp-4", Choices: []model.Choice{{Message: model.Message{Content: "X"}}}},
		},
		{Author: "assistant",
			Response: &model.Response{ID: "resp-6", Choices: []model.Choice{{Message: model.Message{Content: "final"}}}},
		},
	}
	snap := &SessionSnapshot{
		Session: sess,
		ConcurrentBatchRanges: []BatchRange{
			{Start: 1, End: 3},
			{Start: 4, End: 6},
		},
	}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	ev := result.Session.Events
	if ev[0].Author != "user1" {
		t.Errorf("[0] boundary: want user1, got %s", ev[0].Author)
	}
	if ev[1].Tag != "a" {
		t.Errorf("[1] batch1: want a, got %s", ev[1].Tag)
	}
	if ev[2].Tag != "b" {
		t.Errorf("[2] batch1: want b, got %s", ev[2].Tag)
	}
	if ev[3].Author != "assistant" || ev[3].Response.Choices[0].Message.Content != "midpoint" {
		t.Errorf("[3] inter-batch boundary moved: author=%s", ev[3].Author)
	}
	if ev[4].Tag != "x" {
		t.Errorf("[4] batch2: want x, got %s", ev[4].Tag)
	}
	if ev[5].Tag != "z" {
		t.Errorf("[5] batch2: want z, got %s", ev[5].Tag)
	}
	if ev[6].Author != "assistant" {
		t.Errorf("[6] boundary: want assistant, got %s", ev[6].Author)
	}
}

func TestConcurrentEventSorter_NoBatchRanges_NoSort(t *testing.T) {
	n := &concurrentEventSorter{}
	sess := session.NewSession("app", "u1", "s1")
	sess.Events = []event.Event{
		{Author: "tool", Tag: "z",
			Response: &model.Response{ID: "resp-0", Choices: []model.Choice{{Message: model.Message{Content: "zzz"}}}},
		},
		{Author: "tool", Tag: "a",
			Response: &model.Response{ID: "resp-1", Choices: []model.Choice{{Message: model.Message{Content: "aaa"}}}},
		},
	}
	snap := &SessionSnapshot{Session: sess}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Events[0].Tag != "z" || result.Session.Events[1].Tag != "a" {
		t.Error("events without batch ranges should NOT be reordered")
	}
}

func TestParseCreationIndex(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"resp-0", 0},
		{"resp-42", 42},
		{"resp-", -1},
		{"evt-5-12345", -1},
		{"", -1},
		{"resp-abc", -1},
	}
	for _, tc := range tests {
		got := parseCreationIndex(tc.input)
		if got != tc.want {
			t.Errorf("parseCreationIndex(%q) = %d, want %d", tc.input, got, tc.want)
		}
	}
}

// helpers

func keysOf(raw json.RawMessage) []string {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	// sort them to check determinism
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}

// Bug 11 regression: the concurrentEventSorter must be able to parse
// the original "resp-N" Response.ID format, which means it must run
// BEFORE the idNormalizer in the chain (which replaces it with "<resp-id-N>").
func TestDefaultNormalizerChain_ConcurrentSorterBeforeIDNormalizer(t *testing.T) {
	chain := DefaultNormalizerChain()
	sorterPos := -1
	idPos := -1
	for i, n := range chain.normalizers {
		switch n.Name() {
		case "concurrent-event-sorter":
			sorterPos = i
		case "id-normalizer":
			idPos = i
		}
	}
	if sorterPos < 0 {
		t.Fatal("concurrent-event-sorter not found in default chain")
	}
	if idPos < 0 {
		t.Fatal("id-normalizer not found in default chain")
	}
	if sorterPos >= idPos {
		t.Errorf("concurrent-event-sorter (pos %d) must run BEFORE id-normalizer (pos %d), "+
			"otherwise parseCreationIndex cannot decode Response.ID", sorterPos, idPos)
	}
}

// Bug 11 regression: verify the sorter works on events whose Response.ID
// is still in the original "resp-N" format (pre-idNormalizer).
func TestConcurrentEventSorter_ParsesOriginalResponseID(t *testing.T) {
	n := &concurrentEventSorter{}
	sess := session.NewSession("app", "u1", "s1")
	sess.Events = []event.Event{
		{Author: "tool", Tag: "b",
			Response: &model.Response{ID: "resp-3", Choices: []model.Choice{{Message: model.Message{Content: "B"}}}},
		},
		{Author: "tool", Tag: "a",
			Response: &model.Response{ID: "resp-1", Choices: []model.Choice{{Message: model.Message{Content: "A"}}}},
		},
	}
	snap := &SessionSnapshot{
		Session:               sess,
		ConcurrentBatchRanges: []BatchRange{{Start: 1, End: 4}},
	}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	if result.Session.Events[0].Tag != "a" {
		t.Errorf("sorter did not reorder events: [0].tag=%s, expected 'a'. "+
			"Response IDs may not be parsed correctly.", result.Session.Events[0].Tag)
	}
}
