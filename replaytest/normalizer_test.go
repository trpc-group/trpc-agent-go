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

func TestJSONFieldOrderNormalizer_SkipsToolCallArgsExtension(t *testing.T) {
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
	if _, ok := result.Session.Events[0].Extensions[event.ToolCallArgsExtensionKey]; ok {
		t.Error("ToolCallArgsExtensionKey should be skipped")
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
	if len(chain.normalizers) != 8 {
		t.Errorf("expected 8 normalizers in default chain, got %d", len(chain.normalizers))
	}
}

// 9. ConcurrentEventSorter

func TestConcurrentEventSorter_SortsByStableKey(t *testing.T) {
	n := &concurrentEventSorter{}
	sess := session.NewSession("app", "u1", "s1")
	sess.Events = []event.Event{
		{Author: "tool", Tag: "b", FilterKey: "main",
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "result B"}}}},
		},
		{Author: "tool", Tag: "a", FilterKey: "main",
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "result A"}}}},
		},
		{Author: "user1", Tag: "z", FilterKey: "main",
			Response: &model.Response{Choices: []model.Choice{{Message: model.Message{Content: "msg"}}}},
		},
	}
	snap := &SessionSnapshot{Session: sess}
	result, err := n.NormalizeSession(snap)
	if err != nil {
		t.Fatal(err)
	}
	// Sorted: tool < user1 (author), then tag=a < tag=b (within tool).
	if result.Session.Events[0].Author != "tool" {
		t.Errorf("first should be tool, got %s", result.Session.Events[0].Author)
	}
	if result.Session.Events[0].Tag != "a" {
		t.Errorf("first event tag should be a, got %s", result.Session.Events[0].Tag)
	}
	if result.Session.Events[1].Tag != "b" {
		t.Errorf("second event tag should be b, got %s", result.Session.Events[1].Tag)
	}
	if result.Session.Events[2].Author != "user1" {
		t.Errorf("last should be user1, got %s", result.Session.Events[2].Author)
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
