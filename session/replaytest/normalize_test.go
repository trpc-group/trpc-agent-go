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
	"encoding/json"
	"math"
	"reflect"
	"testing"
	"time"
)

type userInvalidRawJSONMethodValue string

func (value userInvalidRawJSONMethodValue) InvalidRawJSON() string {
	return string(value)
}

func TestNormalizeSnapshotRemovesOnlyBackendNoise(t *testing.T) {
	baseline := normalizationFixture(
		"event-a", "memory-a", "invocation-a",
		normalizationObservation{timestamp: time.Unix(10, 0), score: 0.8123451},
	)
	actual := normalizationFixture(
		"event-b", "memory-b", "invocation-b",
		normalizationObservation{timestamp: time.Unix(20, 0), score: 0.8123451},
	)
	actual.Sessions[0].State = map[string]StateValueSnapshot{
		"profile": JSONStateValue(map[string]any{"level": int64(2), "active": true}),
	}
	actual.Sessions[0].Events[0].ToolCalls[0].Arguments =
		map[string]any{"count": int64(2), "query": "weather"}

	options := DefaultNormalizeOptions()
	options.NormalizeInvocationIDs = true
	gotBaseline := NormalizeSnapshot(baseline, options)
	gotActual := NormalizeSnapshot(actual, options)
	if !reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatalf("normalized snapshots differ:\nbaseline: %#v\nactual: %#v", gotBaseline, gotActual)
	}
}

func TestNormalizeSnapshotPreservesSemanticDifferences(t *testing.T) {
	baseline := normalizationFixture(
		"event-a", "memory-a", "invocation-a",
		normalizationObservation{timestamp: time.Unix(10, 0), score: 0.8},
	)
	actual := baseline
	actual.Sessions = append([]SessionSnapshot(nil), baseline.Sessions...)
	actual.Sessions[0].Events = append(
		[]EventSnapshot(nil), baseline.Sessions[0].Events...,
	)
	actual.Sessions[0].Events[0].Content = "different answer"

	normalizedBaseline := NormalizeSnapshot(baseline, DefaultNormalizeOptions())
	normalizedActual := NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if reflect.DeepEqual(normalizedBaseline, normalizedActual) {
		t.Fatal("semantic event content difference was normalized away")
	}
}

func TestNormalizeJSONLikeRejectsTrailingData(t *testing.T) {
	got := normalizeJSONLike(`{"ok":true} trailing`, DefaultNormalizeOptions())
	if got != `{"ok":true} trailing` {
		t.Fatalf("trailing data should remain text, got %#v", got)
	}
}

func TestNormalizeJSONLikePreservesLargeIntegerPrecision(t *testing.T) {
	const (
		largeIntegerText  = "9007199254740993"
		largeIntegerValue = int64(9007199254740993)
	)
	got := normalizeJSONLike(json.Number(largeIntegerText), DefaultNormalizeOptions())
	if got != largeIntegerValue {
		t.Fatalf("large integer = %#v, want %d", got, largeIntegerValue)
	}
	if _, converted := got.(float64); converted {
		t.Fatalf("large integer was converted to float64: %#v", got)
	}
}

func TestNormalizeJSONLikePreservesArbitraryPrecisionNumbers(t *testing.T) {
	const (
		firstDecimal  = "1.0000000000000000001"
		secondDecimal = "1.0000000000000000002"
		largeInteger  = "18446744073709551616"
	)
	first := normalizeJSONLike(json.Number(firstDecimal), DefaultNormalizeOptions())
	second := normalizeJSONLike(json.Number(secondDecimal), DefaultNormalizeOptions())
	if reflect.DeepEqual(first, second) {
		t.Fatalf("high-precision decimals collapsed: %#v", first)
	}
	if got, ok := first.(json.Number); !ok || got.String() != firstDecimal {
		t.Fatalf("first decimal = %#v", first)
	}
	large := normalizeJSONLike(json.Number(largeInteger), DefaultNormalizeOptions())
	if got, ok := large.(json.Number); !ok || got.String() != largeInteger {
		t.Fatalf("large integer = %#v", large)
	}
	for _, input := range []any{
		json.RawMessage(`{"value":1.0000000000000000001}`),
		[]byte(`{"value":1.0000000000000000001}`),
	} {
		got := normalizeJSONLike(input, DefaultNormalizeOptions()).(map[string]any)["value"]
		if number, ok := got.(json.Number); !ok || number.String() != firstDecimal {
			t.Fatalf("normalizeJSONLike(%T) precision = %#v", input, got)
		}
	}
}

func TestNormalizeJSONLikeCanonicalizesEquivalentNumbers(t *testing.T) {
	options := DefaultNormalizeOptions()
	values := []any{
		json.Number("1"), json.Number("1.0"), json.Number("10e-1"),
		float64(1), uint64(1),
	}
	want := normalizeJSONLike(values[0], options)
	for _, value := range values[1:] {
		if got := normalizeJSONLike(value, options); !reflect.DeepEqual(got, want) {
			t.Fatalf("normalizeJSONLike(%#v) = %#v, want %#v", value, got, want)
		}
	}
	for _, value := range []json.Number{"-0", "0.0", "0e100000"} {
		if got := normalizeJSONLike(value, options); !reflect.DeepEqual(got, int64(0)) {
			t.Fatalf("normalizeJSONLike(%q) = %#v, want zero", value, got)
		}
	}
}

func TestNormalizeSnapshotCanonicalizesNumericSortKeys(t *testing.T) {
	baseline := Snapshot{Memories: []MemorySnapshot{{
		Content: "same", Metadata: map[string]any{"rank": json.Number("1e0")},
	}}}
	actual := Snapshot{Memories: []MemorySnapshot{{
		Content: "same", Metadata: map[string]any{"rank": float64(1)},
	}}}
	if got, want := NormalizeSnapshot(actual, DefaultNormalizeOptions()),
		NormalizeSnapshot(baseline, DefaultNormalizeOptions()); !reflect.DeepEqual(got, want) {
		t.Fatalf("numeric representations normalize differently:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestNormalizeSnapshotDoesNotMutateInput(t *testing.T) {
	input := normalizationFixture(
		"event-a", "memory-a", "invocation-a",
		normalizationObservation{timestamp: time.Unix(10, 0), score: 0.8},
	)
	want := normalizationFixture(
		"event-a", "memory-a", "invocation-a",
		normalizationObservation{timestamp: time.Unix(10, 0), score: 0.8},
	)
	got := NormalizeSnapshot(input, DefaultNormalizeOptions())
	if len(got.Sessions) == 0 {
		t.Fatal("NormalizeSnapshot() returned no sessions")
	}
	if !reflect.DeepEqual(input, want) {
		t.Fatalf("NormalizeSnapshot() mutated input:\ngot:  %#v\nwant: %#v", input, want)
	}
}

func TestNormalizeSnapshotPreservesStateTypesAndKeys(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{{
		State: map[string]StateValueSnapshot{"timestamp": TextStateValue("1")},
	}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{
		State: map[string]StateValueSnapshot{"timestamp": JSONStateValue(1)},
	}}}
	gotBaseline := NormalizeSnapshot(baseline, DefaultNormalizeOptions())
	gotActual := NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatal("semantic state type difference was normalized away")
	}
}

func TestNormalizeSnapshotPreservesBinaryBytes(t *testing.T) {
	want := []byte(`{"looks":"json"}`)
	input := Snapshot{Sessions: []SessionSnapshot{{
		State: map[string]StateValueSnapshot{"binary": BinaryStateValue(want)},
	}}}
	got := NormalizeSnapshot(input, DefaultNormalizeOptions())
	value, ok := got.Sessions[0].State["binary"].Value.([]byte)
	if !ok || !reflect.DeepEqual(value, want) {
		t.Fatalf("normalized binary = %#v, want %#v", value, want)
	}
	value[0]++
	inputValue := input.Sessions[0].State["binary"].Value.([]byte)
	if reflect.DeepEqual(value, inputValue) {
		t.Fatal("normalized binary aliases input bytes")
	}
}

func TestNormalizeSnapshotPreservesMemorySearchOrder(t *testing.T) {
	baseline := Snapshot{MemorySearches: []MemorySearchSnapshot{{
		AppName: "app",
		UserID:  "user",
		Query:   "query",
		Results: []MemorySnapshot{{Content: "first"}, {Content: "second"}},
	}}}
	actual := Snapshot{MemorySearches: []MemorySearchSnapshot{{
		AppName: "app",
		UserID:  "user",
		Query:   "query",
		Results: []MemorySnapshot{{Content: "second"}, {Content: "first"}},
	}}}
	gotBaseline := NormalizeSnapshot(baseline, DefaultNormalizeOptions())
	gotActual := NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatal("memory search order difference was normalized away")
	}
}

func TestNormalizeSnapshotTreatsNilAndEmptyMemorySearchResultsAsEquivalent(t *testing.T) {
	baseline := Snapshot{MemorySearches: []MemorySearchSnapshot{{
		AppName: "app",
		UserID:  "user",
		Query:   "missing",
	}}}
	actual := Snapshot{MemorySearches: []MemorySearchSnapshot{{
		AppName: "app",
		UserID:  "user",
		Query:   "missing",
		Results: []MemorySnapshot{},
	}}}
	gotBaseline := NormalizeSnapshot(baseline, DefaultNormalizeOptions())
	gotActual := NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if !reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatalf("nil and empty memory search results differ:\nbaseline: %#v\nactual: %#v", gotBaseline, gotActual)
	}
	if gotBaseline.MemorySearches[0].Results == nil {
		t.Fatal("nil memory search results were not canonicalized")
	}
}

func TestNormalizeSnapshotPreservesToolCallIDsByDefault(t *testing.T) {
	baseline := normalizationFixture(
		"event-a", "memory-a", "invocation-a",
		normalizationObservation{timestamp: time.Unix(10, 0), score: 0.8},
	)
	actual := normalizationFixture(
		"event-a", "memory-a", "invocation-a",
		normalizationObservation{timestamp: time.Unix(10, 0), score: 0.8},
	)
	actual.Sessions[0].Events[0].ToolCalls[0].ID = "wrong-call"
	if reflect.DeepEqual(
		NormalizeSnapshot(baseline, DefaultNormalizeOptions()),
		NormalizeSnapshot(actual, DefaultNormalizeOptions()),
	) {
		t.Fatal("tool call ID difference was normalized away by default")
	}
}

func TestNormalizeSnapshotRewritesToolCallArgsExtensionKeys(t *testing.T) {
	baseline := toolCallArgsExtensionSnapshot("baseline-call")
	actual := toolCallArgsExtensionSnapshot("actual-call")
	options := DefaultNormalizeOptions()
	options.NormalizeToolCallIDs = true
	gotBaseline := NormalizeSnapshot(baseline, options)
	gotActual := NormalizeSnapshot(actual, options)
	if !reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatalf("tool call args extension keys differ:\nbaseline: %#v\nactual: %#v", gotBaseline, gotActual)
	}
	extension, ok := gotActual.Sessions[0].Events[0].
		Extensions[toolCallArgsExtensionKey].(map[string]any)
	if !ok {
		t.Fatalf("tool args extension has type %T", gotActual.Sessions[0].Events[0].
			Extensions[toolCallArgsExtensionKey])
	}
	if _, ok := extension["tool-call-0001"]; !ok {
		t.Fatalf("tool args extension did not use logical ID: %#v", extension)
	}
	if _, ok := extension["actual-call"]; ok {
		t.Fatalf("tool args extension kept backend ID: %#v", extension)
	}
}

func TestNormalizeSnapshotScopesToolCallIDsBySession(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{
		toolCallArgsExtensionSession("session-1", "baseline-call-a"),
		toolCallArgsExtensionSession("session-2", "baseline-call-b"),
	}}
	actual := Snapshot{Sessions: []SessionSnapshot{
		toolCallArgsExtensionSession("session-1", "call-1"),
		toolCallArgsExtensionSession("session-2", "call-1"),
	}}
	options := DefaultNormalizeOptions()
	options.NormalizeToolCallIDs = true
	gotBaseline := NormalizeSnapshot(baseline, options)
	gotActual := NormalizeSnapshot(actual, options)
	if !reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatalf("tool call IDs are not scoped per session:\nbaseline: %#v\nactual: %#v", gotBaseline, gotActual)
	}
	for _, session := range gotActual.Sessions {
		extension := session.Events[0].Extensions[toolCallArgsExtensionKey].(map[string]any)
		if _, ok := extension["tool-call-0001"]; !ok {
			t.Fatalf("session tool args extension did not share scoped ID map: %#v", session)
		}
	}
}

func TestNormalizeSnapshotPreservesUnknownToolCallArgsExtensionKeys(t *testing.T) {
	baseline := toolCallArgsExtensionSnapshot("call-1")
	baselineArgs := baseline.Sessions[0].Events[0].
		Extensions[toolCallArgsExtensionKey].(map[string]any)
	baselineArgs["stale-a"] = map[string]any{"query": "old"}
	actual := toolCallArgsExtensionSnapshot("call-1")
	actualArgs := actual.Sessions[0].Events[0].
		Extensions[toolCallArgsExtensionKey].(map[string]any)
	actualArgs["stale-b"] = map[string]any{"query": "old"}

	options := DefaultNormalizeOptions()
	options.NormalizeToolCallIDs = true
	gotBaseline := NormalizeSnapshot(baseline, options)
	gotActual := NormalizeSnapshot(actual, options)
	if reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatal("unknown tool args extension keys were normalized away")
	}
	extension := gotActual.Sessions[0].Events[0].Extensions[toolCallArgsExtensionKey].(map[string]any)
	if _, ok := extension["stale-b"]; !ok {
		t.Fatalf("unknown extension key was not preserved: %#v", extension)
	}
	if _, ok := extension["tool-call-0002"]; ok {
		t.Fatalf("unknown extension key allocated a logical ID: %#v", extension)
	}
}

func TestNormalizeSnapshotPreservesToolCallArgsExtensionKeyCollisions(t *testing.T) {
	baseline := toolCallArgsExtensionSnapshot("baseline-call")
	baselineArgs := baseline.Sessions[0].Events[0].
		Extensions[toolCallArgsExtensionKey].(map[string]any)
	baselineArgs["tool-call-0001"] = map[string]any{"query": "stale"}
	actual := toolCallArgsExtensionSnapshot("actual-call")
	actualArgs := actual.Sessions[0].Events[0].
		Extensions[toolCallArgsExtensionKey].(map[string]any)
	actualArgs["tool-call-0001"] = map[string]any{"query": "stale"}

	options := DefaultNormalizeOptions()
	options.NormalizeToolCallIDs = true
	gotBaseline := NormalizeSnapshot(baseline, options)
	gotActual := NormalizeSnapshot(actual, options)
	if !reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatalf("tool args collision normalized differently:\nbaseline: %#v\nactual: %#v",
			gotBaseline, gotActual)
	}
	entries, ok := gotActual.Sessions[0].Events[0].
		Extensions[toolCallArgsExtensionKey].([]map[string]any)
	if !ok {
		t.Fatalf("tool args collision was not represented as tagged entries: %#v",
			gotActual.Sessions[0].Events[0].Extensions[toolCallArgsExtensionKey])
	}
	if len(entries) != 2 {
		t.Fatalf("tool args collision lost entries: %#v", entries)
	}
	if entries[0][toolCallArgsEntryKnown] != true ||
		entries[1][toolCallArgsEntryKnown] != false {
		t.Fatalf("tool args collision did not distinguish known and unknown keys: %#v", entries)
	}
}

func TestNormalizeSnapshotPreservesSummaryBoundaryIDSpace(t *testing.T) {
	snapshot := normalizationFixture(
		"event-a", "memory-a", "invocation-a",
		normalizationObservation{timestamp: time.Unix(10, 0), score: 0.8},
	)
	snapshot.Sessions[0].Summaries[0].Boundary = map[string]any{"last_event_id": "event-a"}
	options := DefaultNormalizeOptions()
	options.PreserveEventIDs = true
	got := NormalizeSnapshot(snapshot, options)
	if got.Sessions[0].Events[0].ID != "event-a" ||
		got.Sessions[0].Summaries[0].Boundary["last_event_id"] != "event-a" {
		t.Fatalf("event ID space is inconsistent: %#v", got.Sessions[0])
	}
}

func TestNormalizeSnapshotAssignsSummaryBoundaryIDsAfterSorting(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{{
		ID: "session-1",
		Summaries: []SummarySnapshot{
			{FilterKey: "branch/b", Boundary: map[string]any{"last_event_id": "event-b"}},
			{FilterKey: "branch/a", Boundary: map[string]any{"last_event_id": "event-a"}},
		},
	}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{
		ID: "session-1",
		Summaries: []SummarySnapshot{
			{FilterKey: "branch/a", Boundary: map[string]any{"last_event_id": "event-a"}},
			{FilterKey: "branch/b", Boundary: map[string]any{"last_event_id": "event-b"}},
		},
	}}}

	gotBaseline := NormalizeSnapshot(baseline, DefaultNormalizeOptions())
	gotActual := NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if !reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatalf("summary boundary IDs depend on backend order:\nbaseline: %#v\nactual: %#v", gotBaseline, gotActual)
	}
}

func TestNormalizeSnapshotScopesEventIDsBySession(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{
		scopedEventSession("session-1", "baseline-event-a", "first"),
		scopedEventSession("session-2", "baseline-event-b", "second"),
	}}
	actual := Snapshot{Sessions: []SessionSnapshot{
		scopedEventSession("session-1", "1", "first"),
		scopedEventSession("session-2", "1", "second"),
	}}

	gotBaseline := NormalizeSnapshot(baseline, DefaultNormalizeOptions())
	gotActual := NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if !reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatalf("event IDs are not scoped per session:\nbaseline: %#v\nactual: %#v", gotBaseline, gotActual)
	}
	for _, session := range gotActual.Sessions {
		if session.Events[0].ID != "event-0001" ||
			session.Summaries[0].Boundary["last_event_id"] != "event-0001" {
			t.Fatalf("session summary did not share event ID scope: %#v", session)
		}
	}
}

func TestNormalizeSnapshotScopesMemoryIDsByMemoryScopeAndSearchResults(t *testing.T) {
	firstScope := MemoryScope{AppName: "replay", UserID: "user-1"}
	secondScope := MemoryScope{AppName: "replay", UserID: "user-2"}
	baseline := Snapshot{
		Memories: []MemorySnapshot{
			scopedMemory("baseline-memory-a", firstScope, "prefers concise replies"),
			scopedMemory("baseline-memory-b", secondScope, "prefers detailed replies"),
		},
		MemorySearches: []MemorySearchSnapshot{
			scopedMemorySearch(firstScope, scopedMemory("baseline-memory-a", firstScope, "prefers concise replies")),
			scopedMemorySearch(secondScope, scopedMemory("baseline-memory-b", secondScope, "prefers detailed replies")),
		},
	}
	actual := Snapshot{
		Memories: []MemorySnapshot{
			scopedMemory("1", firstScope, "prefers concise replies"),
			scopedMemory("1", secondScope, "prefers detailed replies"),
		},
		MemorySearches: []MemorySearchSnapshot{
			scopedMemorySearch(firstScope, scopedMemory("1", firstScope, "prefers concise replies")),
			scopedMemorySearch(secondScope, scopedMemory("1", secondScope, "prefers detailed replies")),
		},
	}

	gotBaseline := NormalizeSnapshot(baseline, DefaultNormalizeOptions())
	gotActual := NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if !reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatalf("memory IDs are not scoped per MemoryScope:\nbaseline: %#v\nactual: %#v", gotBaseline, gotActual)
	}
	for _, search := range gotActual.MemorySearches {
		if len(search.Results) != 1 || search.Results[0].ID != "memory-0001" {
			t.Fatalf("memory search did not share scoped memory ID map: %#v", search)
		}
	}
}

func TestNormalizeSnapshotSupportsExplicitIDAndOrderingPolicies(t *testing.T) {
	snapshot := normalizationFixture(
		"event-z", "memory-z", "invocation-z",
		normalizationObservation{timestamp: time.Unix(10, 0), score: 0.8},
	)
	snapshot.Sessions[0].Events[0].ToolCalls[0].ID = "call-z"
	snapshot.Sessions[0].Events[0].ToolResponse = &ToolResponse{ToolCallID: "call-z"}
	snapshot.Memories = append(snapshot.Memories, MemorySnapshot{
		ID: "memory-a", AppName: "replay", UserID: "user-1", Content: "aaa",
	})
	options := DefaultNormalizeOptions()
	options.PreserveEventIDs = true
	options.PreserveMemoryIDs = true
	options.NormalizeToolCallIDs = true
	got := NormalizeSnapshot(snapshot, options)
	if got.Sessions[0].Events[0].ID != "event-z" || got.Memories[0].Content != "aaa" {
		t.Fatalf("explicit normalization policies were ignored: %#v", got)
	}
	call := got.Sessions[0].Events[0].ToolCalls[0].ID
	if call != "tool-call-0001" || got.Sessions[0].Events[0].ToolResponse.ToolCallID != call {
		t.Fatalf("tool call references are inconsistent: %#v", got.Sessions[0].Events[0])
	}
}

func TestNormalizeSnapshotSortsMemoriesBySemanticFieldsBeforeID(t *testing.T) {
	tests := []struct {
		name   string
		first  MemorySnapshot
		second MemorySnapshot
	}{
		{
			name: "topics",
			first: MemorySnapshot{
				ID: "backend-z", AppName: "replay", UserID: "user-1",
				Scope:   MemoryScope{AppName: "replay", UserID: "user-1"},
				Content: "same", Topics: []string{"alpha"},
			},
			second: MemorySnapshot{
				ID: "backend-a", AppName: "replay", UserID: "user-1",
				Scope:   MemoryScope{AppName: "replay", UserID: "user-1"},
				Content: "same", Topics: []string{"beta"},
			},
		},
		{
			name: "metadata",
			first: MemorySnapshot{
				ID: "backend-z", AppName: "replay", UserID: "user-1",
				Scope:    MemoryScope{AppName: "replay", UserID: "user-1"},
				Content:  "same",
				Metadata: map[string]any{"rank": 1},
			},
			second: MemorySnapshot{
				ID: "backend-a", AppName: "replay", UserID: "user-1",
				Scope:    MemoryScope{AppName: "replay", UserID: "user-1"},
				Content:  "same",
				Metadata: map[string]any{"rank": 2},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline := Snapshot{Memories: []MemorySnapshot{test.first, test.second}}
			actualFirst, actualSecond := test.first, test.second
			actualFirst.ID, actualSecond.ID = test.second.ID, test.first.ID
			actual := Snapshot{Memories: []MemorySnapshot{actualFirst, actualSecond}}

			gotBaseline := NormalizeSnapshot(baseline, DefaultNormalizeOptions())
			gotActual := NormalizeSnapshot(actual, DefaultNormalizeOptions())
			if !reflect.DeepEqual(gotBaseline, gotActual) {
				t.Fatalf("memory ordering depends on generated IDs:\nbaseline: %#v\nactual: %#v", gotBaseline, gotActual)
			}
		})
	}
}

func TestNormalizeJSONLikeHandlesRawRepresentations(t *testing.T) {
	options := DefaultNormalizeOptions()
	tests := []struct {
		name  string
		value any
	}{
		{name: "raw message", value: json.RawMessage(`{"value":2}`)},
		{name: "bytes", value: []byte(`{"value":2}`)},
		{name: "typed map", value: map[string]int{"value": 2}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := normalizeJSONLike(test.value, options)
			want := map[string]any{"value": int64(2)}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("normalizeJSONLike() = %#v, want %#v", got, want)
			}
		})
	}
}

func TestNormalizeJSONLikeHandlesFallbackRepresentations(t *testing.T) {
	options := DefaultNormalizeOptions()
	if got := normalizeJSONLike(nil, options); got != nil {
		t.Fatalf("normalizeJSONLike(nil) = %#v", got)
	}
	for _, value := range []any{
		json.RawMessage(`{"invalid"`),
		[]byte(`{"invalid"`),
	} {
		if _, ok := normalizeInvalidRawJSON(normalizeJSONLike(value, options)); !ok {
			t.Fatalf("invalid JSON representation was not preserved as invalid raw: %#v", value)
		}
	}
	if _, ok := normalizeInvalidRawJSON(
		normalizeJSONLike(userInvalidRawJSONMethodValue("business"), options),
	); ok {
		t.Fatal("business value with InvalidRawJSON method was treated as invalid raw JSON")
	}
	if _, ok := normalizeJSONLike(make(chan int), options).(string); !ok {
		t.Fatal("unencodable value was not converted to diagnostic text")
	}

	scalars := []struct {
		value any
		want  any
	}{
		{value: uint64(42), want: int64(42)},
		{value: float32(1.5), want: json.Number("1.5")},
	}
	for _, scalar := range scalars {
		if got := normalizeJSONLike(scalar.value, options); !reflect.DeepEqual(got, scalar.want) {
			t.Fatalf("normalizeJSONLike(%T) = %#v, want %#v", scalar.value, got, scalar.want)
		}
	}
}

func TestNormalizeJSONLikePreservesInvalidJSONFloatsForEncodingErrors(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		got := normalizeJSONLike(value, DefaultNormalizeOptions())
		if _, err := json.Marshal(got); err == nil {
			t.Fatalf("normalizeJSONLike(%v) became JSON encodable: %#v", value, got)
		}
	}
}

func TestNormalizeJSONNumbersPreservesInvalidNumbers(t *testing.T) {
	tests := []json.Number{"1e+", "not-a-number"}
	for _, value := range tests {
		if got := normalizeJSONNumbers(value); got != value.String() {
			t.Fatalf("normalizeJSONNumbers(%q) = %#v", value, got)
		}
	}
}

func TestCloneJSONLikeCopiesReferenceValues(t *testing.T) {
	raw := json.RawMessage(`{"value":1}`)
	bytesValue := []byte("bytes")
	input := []any{raw, bytesValue, []any{map[string]any{"key": "value"}}}
	cloned := cloneJSONLike(input).([]any)

	cloned[0].(json.RawMessage)[0]++
	cloned[1].([]byte)[0]++
	cloned[2].([]any)[0].(map[string]any)["key"] = "changed"
	if reflect.DeepEqual(input, cloned) {
		t.Fatal("cloneJSONLike() retained aliases to reference values")
	}
	if string(raw) != `{"value":1}` || string(bytesValue) != "bytes" ||
		input[2].([]any)[0].(map[string]any)["key"] != "value" {
		t.Fatalf("cloneJSONLike() mutated input: %#v", input)
	}
}

type typedClonePayload struct {
	Labels map[string]string
	Items  []string
	Nested *typedClonePayload
}

func TestCloneJSONLikeCopiesTypedStructsAndPreservesReferences(t *testing.T) {
	shared := &typedClonePayload{
		Labels: map[string]string{"key": "value"},
		Items:  []string{"item"},
	}
	shared.Nested = shared
	input := []any{shared, shared}

	cloned := cloneJSONLike(input).([]any)
	first := cloned[0].(*typedClonePayload)
	second := cloned[1].(*typedClonePayload)
	if first == shared || second != first || first.Nested != first {
		t.Fatalf("clone topology = %#v, %#v", first, second)
	}
	first.Labels["key"] = "changed"
	first.Items[0] = "changed"
	if shared.Labels["key"] != "value" || shared.Items[0] != "item" {
		t.Fatalf("typed clone retained input aliases: %#v", shared)
	}
}

func TestNormalizeSnapshotFlagsInvalidSessionMetadataOrder(t *testing.T) {
	created := time.Unix(20, 0)
	snapshot := Snapshot{Sessions: []SessionSnapshot{{
		CreatedAt: created,
		UpdatedAt: created.Add(-time.Second),
	}}}
	got := NormalizeSnapshot(snapshot, DefaultNormalizeOptions())
	if !got.Sessions[0].UpdatedAt.Before(got.Sessions[0].CreatedAt) {
		t.Fatalf("invalid metadata order was normalized away: %#v", got.Sessions[0])
	}
}

func TestNormalizeSnapshotPreservesTemporalSemantics(t *testing.T) {
	base := time.Unix(100, 0)
	baseline := Snapshot{Sessions: []SessionSnapshot{{
		Events: []EventSnapshot{
			{Timestamp: base},
			{Timestamp: base.Add(time.Second)},
		},
		Tracks: []TrackSnapshot{{Events: []TrackEventSnapshot{
			{Duration: 10 * time.Millisecond},
			{Duration: 20 * time.Millisecond},
		}}},
	}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{
		Events: []EventSnapshot{
			{Timestamp: base.Add(10 * time.Second)},
			{Timestamp: base.Add(11 * time.Second)},
		},
		Tracks: []TrackSnapshot{{Events: []TrackEventSnapshot{
			{Duration: 100 * time.Millisecond},
			{Duration: 200 * time.Millisecond},
		}}},
	}}}
	gotBaseline := NormalizeSnapshot(baseline, DefaultNormalizeOptions())
	gotActual := NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatal("duration magnitude was normalized away")
	}

	actual.Sessions[0].Tracks[0].Events[0].Duration = 10 * time.Millisecond
	actual.Sessions[0].Tracks[0].Events[1].Duration = 20 * time.Millisecond
	gotActual = NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if !reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatalf("equivalent absolute durations differ:\n%#v\n%#v", gotBaseline, gotActual)
	}

	actual.Sessions[0].Events[0].Timestamp = base.Add(12 * time.Second)
	actual.Sessions[0].Tracks[0].Events[0].Duration = -time.Millisecond
	gotActual = NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatal("reversed timestamp and negative duration were normalized away")
	}
}

func TestNormalizeSnapshotToleratesBackendTimePrecision(t *testing.T) {
	base := time.Unix(100, 0)
	baseline := Snapshot{Sessions: []SessionSnapshot{{
		Events: []EventSnapshot{
			{Timestamp: base},
			{Timestamp: base.Add(500 * time.Microsecond)},
		},
		Tracks: []TrackSnapshot{{Events: []TrackEventSnapshot{
			{Duration: 10 * time.Millisecond},
			{Duration: 10*time.Millisecond + 500*time.Microsecond},
		}}},
	}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{
		Events: []EventSnapshot{{Timestamp: base}, {Timestamp: base}},
		Tracks: []TrackSnapshot{{Events: []TrackEventSnapshot{
			{Duration: 10 * time.Millisecond},
			{Duration: 10 * time.Millisecond},
		}}},
	}}}
	options := DefaultNormalizeOptions()
	options.NormalizeInvocationIDs = true
	got := NormalizeSnapshot(actual, options)
	want := NormalizeSnapshot(baseline, options)
	differences, err := CompareSnapshots(CompareInput{
		Case: "time-precision", Backend: "actual", Baseline: want, Actual: got,
		Options: DefaultCompareOptions(),
	})
	if err != nil {
		t.Fatalf("CompareSnapshots() error = %v", err)
	}
	if len(differences) != 0 {
		t.Fatalf("precision-truncated timing differs: %#v", differences)
	}
}

func TestNormalizeSnapshotToleratesPrecisionTruncatedBursts(t *testing.T) {
	base := time.Unix(100, 0)
	baseline := Snapshot{Sessions: []SessionSnapshot{{
		Events: []EventSnapshot{
			{Timestamp: base},
			{Timestamp: base.Add(600 * time.Microsecond)},
			{Timestamp: base.Add(1200 * time.Microsecond)},
		},
	}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{
		Events: []EventSnapshot{
			{Timestamp: base},
			{Timestamp: base},
			{Timestamp: base.Add(time.Millisecond)},
		},
	}}}
	gotBaseline := NormalizeSnapshot(baseline, DefaultNormalizeOptions())
	gotActual := NormalizeSnapshot(actual, DefaultNormalizeOptions())
	if !reflect.DeepEqual(gotBaseline, gotActual) {
		t.Fatalf("precision-truncated burst differs:\nbaseline: %#v\nactual: %#v", gotBaseline, gotActual)
	}
}

func TestNormalizeTimesStartsNewRankAfterPrecisionGap(t *testing.T) {
	first := time.Unix(100, 0)
	second := first.Add(time.Millisecond + time.Nanosecond)
	third := second.Add(500 * time.Microsecond)
	normalizeTimes([]*time.Time{&first, &second, &third}, time.Millisecond)
	if first.Equal(second) {
		t.Fatalf("gap larger than precision used same rank: %v, %v", first, second)
	}
	if !second.Equal(third) {
		t.Fatalf("adjacent values within precision got different ranks: %v, %v", second, third)
	}
}

func TestNormalizeTimesBoundsChainedTolerance(t *testing.T) {
	base := time.Unix(100, 0)
	values := []time.Time{
		base,
		base.Add(900 * time.Microsecond),
		base.Add(1800 * time.Microsecond),
		base.Add(2700 * time.Microsecond),
	}
	pointers := make([]*time.Time, len(values))
	for i := range values {
		pointers[i] = &values[i]
	}
	normalizeTimes(pointers, time.Millisecond)
	if !values[0].Equal(values[1]) || !values[2].Equal(values[3]) ||
		values[0].Equal(values[2]) {
		t.Fatalf("chained ranks are unbounded: %v", values)
	}
}

func TestNormalizeTimesUsesInclusiveClusterBoundary(t *testing.T) {
	base := time.Unix(100, 0)
	first := base.Add(2 * time.Millisecond)
	second := base
	third := base.Add(time.Millisecond)
	normalizeTimes([]*time.Time{&first, &second, &third}, time.Millisecond)
	if got := []int64{first.UnixNano(), second.UnixNano(), third.UnixNano()}; !reflect.DeepEqual(got, []int64{3, 1, 2}) {
		t.Fatalf("inclusive ranks = %v, want [3 1 2]", got)
	}
}

func TestNormalizeSnapshotBoundsAllTimestampCollections(t *testing.T) {
	base := time.Unix(100, 0)
	snapshot := Snapshot{
		Sessions: []SessionSnapshot{{
			Events: []EventSnapshot{
				{Timestamp: base},
				{Timestamp: base.Add(900 * time.Microsecond)},
			},
			Summaries: []SummarySnapshot{{
				UpdatedAt: base.Add(1800 * time.Microsecond),
			}},
			Tracks: []TrackSnapshot{{Events: []TrackEventSnapshot{
				{Timestamp: base},
				{Timestamp: base.Add(900 * time.Microsecond)},
				{Timestamp: base.Add(1800 * time.Microsecond)},
			}}},
		}},
		Memories: []MemorySnapshot{{
			CreatedAt: base, UpdatedAt: base.Add(time.Millisecond),
		}},
	}
	got := NormalizeSnapshot(snapshot, DefaultNormalizeOptions())
	events := got.Sessions[0].Events
	if !events[0].Timestamp.Equal(events[1].Timestamp) ||
		events[0].Timestamp.Equal(got.Sessions[0].Summaries[0].UpdatedAt) {
		t.Fatalf("conversation ranks = %#v", got.Sessions[0])
	}
	trackEvents := got.Sessions[0].Tracks[0].Events
	if !trackEvents[0].Timestamp.Equal(trackEvents[1].Timestamp) ||
		trackEvents[0].Timestamp.Equal(trackEvents[2].Timestamp) {
		t.Fatalf("track ranks = %#v", trackEvents)
	}
	if got.Memories[0].CreatedAt.Equal(got.Memories[0].UpdatedAt) {
		t.Fatalf("memory boundary shared one rank: %#v", got.Memories[0])
	}
}

func TestNormalizeSnapshotNormalizesSummaryBoundaryCutoffPrecision(t *testing.T) {
	base := time.Unix(100, 0).UTC()
	baseline := summaryCutoffSnapshot(base, base.Add(500*time.Microsecond))
	pointerCutoff := base
	tests := []struct {
		name   string
		cutoff any
	}{
		{name: "time", cutoff: base},
		{name: "time pointer", cutoff: &pointerCutoff},
		{name: "rfc3339 string", cutoff: base.Format(time.RFC3339)},
		{
			name:   "rfc3339nano string",
			cutoff: base.Add(500 * time.Microsecond).Format(time.RFC3339Nano),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := summaryCutoffSnapshot(base, test.cutoff)
			if got, want := NormalizeSnapshot(actual, DefaultNormalizeOptions()),
				NormalizeSnapshot(baseline, DefaultNormalizeOptions()); !reflect.DeepEqual(got, want) {
				t.Fatalf("precision-truncated cutoff differs:\ngot:  %#v\nwant: %#v", got, want)
			}
		})
	}

	actual := summaryCutoffSnapshot(
		base, base.Add(2*time.Millisecond).Format(time.RFC3339Nano),
	)
	if got, want := NormalizeSnapshot(actual, DefaultNormalizeOptions()),
		NormalizeSnapshot(baseline, DefaultNormalizeOptions()); reflect.DeepEqual(got, want) {
		t.Fatalf("material cutoff shift was normalized away:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func summaryCutoffSnapshot(base time.Time, cutoff any) Snapshot {
	return Snapshot{Sessions: []SessionSnapshot{{
		Events: []EventSnapshot{{Timestamp: base}},
		Summaries: []SummarySnapshot{{
			FilterKey: "branch/main",
			UpdatedAt: base.Add(2 * time.Millisecond),
			Boundary: map[string]any{
				"cutoff_at":     cutoff,
				"last_event_id": "event-1",
			},
		}},
	}}}
}

func TestNormalizeSnapshotNormalizesMemoryEventTimePrecision(t *testing.T) {
	scope := MemoryScope{AppName: "replay", UserID: "user-1"}
	base := time.Unix(100, 123456789).UTC()
	second := base.Add(2 * time.Millisecond)
	baseline := memoryEventTimeSnapshot(scope, base, second)
	actual := memoryEventTimeSnapshot(
		scope,
		base.Add(500*time.Microsecond).Format(time.RFC3339Nano),
		second,
	)
	if got, want := NormalizeSnapshot(actual, DefaultNormalizeOptions()),
		NormalizeSnapshot(baseline, DefaultNormalizeOptions()); !reflect.DeepEqual(got, want) {
		t.Fatalf("precision-truncated memory event_time differs:\ngot:  %#v\nwant: %#v", got, want)
	}

	actual = memoryEventTimeSnapshot(
		scope,
		base.Add(1500*time.Microsecond).Format(time.RFC3339Nano),
		second,
	)
	if got, want := NormalizeSnapshot(actual, DefaultNormalizeOptions()),
		NormalizeSnapshot(baseline, DefaultNormalizeOptions()); reflect.DeepEqual(got, want) {
		t.Fatalf("material memory event_time shift was normalized away:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestNormalizeSnapshotAssignsInvocationIDsAfterTrackSorting(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{{Tracks: []TrackSnapshot{
		{Name: "b", Events: []TrackEventSnapshot{{InvocationID: "baseline-b"}}},
		{Name: "a", Events: []TrackEventSnapshot{{InvocationID: "baseline-a"}}},
	}}}}
	actual := Snapshot{Sessions: []SessionSnapshot{{Tracks: []TrackSnapshot{
		{Name: "a", Events: []TrackEventSnapshot{{InvocationID: "actual-a"}}},
		{Name: "b", Events: []TrackEventSnapshot{{InvocationID: "actual-b"}}},
	}}}}
	options := DefaultNormalizeOptions()
	options.NormalizeInvocationIDs = true
	if got, want := NormalizeSnapshot(actual, options), NormalizeSnapshot(baseline, options); !reflect.DeepEqual(got, want) {
		t.Fatalf("sorted tracks have unstable invocation IDs:\ngot:  %#v\nwant: %#v", got, want)
	}
}

func TestNormalizeSnapshotScopesInvocationIDsBySession(t *testing.T) {
	baseline := Snapshot{Sessions: []SessionSnapshot{
		invocationScopedSession("session-1", "baseline-event-a", "baseline-track-a", "first"),
		invocationScopedSession("session-2", "baseline-event-b", "baseline-track-b", "second"),
	}}
	actual := Snapshot{Sessions: []SessionSnapshot{
		invocationScopedSession("session-1", "invocation-1", "invocation-2", "first"),
		invocationScopedSession("session-2", "invocation-1", "invocation-2", "second"),
	}}
	options := DefaultNormalizeOptions()
	options.NormalizeInvocationIDs = true
	if got, want := NormalizeSnapshot(actual, options), NormalizeSnapshot(baseline, options); !reflect.DeepEqual(got, want) {
		t.Fatalf("invocation IDs are not scoped per session:\ngot:  %#v\nwant: %#v", got, want)
	}
	for _, session := range NormalizeSnapshot(actual, options).Sessions {
		if session.Events[0].InvocationID != "invocation-0001" ||
			session.Tracks[0].Events[0].InvocationID != "invocation-0002" {
			t.Fatalf("session events and tracks did not share invocation ID scope: %#v", session)
		}
	}
}

func toolCallArgsExtensionSnapshot(callID string) Snapshot {
	return Snapshot{Sessions: []SessionSnapshot{
		toolCallArgsExtensionSession("session-1", callID),
	}}
}

func toolCallArgsExtensionSession(sessionID string, callID string) SessionSnapshot {
	return SessionSnapshot{
		ID:      sessionID,
		AppName: "replay",
		UserID:  "user-1",
		Events: []EventSnapshot{{
			ID:   "event-1",
			Role: "assistant",
			ToolCalls: []ToolCallSnapshot{{
				ID:        callID,
				Name:      "lookup",
				Arguments: map[string]any{"query": "weather"},
			}},
			ToolResponse: &ToolResponse{ToolCallID: callID, Name: "lookup"},
			Extensions: map[string]any{
				toolCallArgsExtensionKey: map[string]any{
					callID: map[string]any{"query": "weather"},
				},
			},
		}},
	}
}

func scopedEventSession(sessionID, eventID, content string) SessionSnapshot {
	return SessionSnapshot{
		ID:      sessionID,
		AppName: "replay",
		UserID:  "user-1",
		Events: []EventSnapshot{{
			ID:      eventID,
			Author:  "user",
			Role:    "user",
			Content: content,
		}},
		Summaries: []SummarySnapshot{{
			SessionID: sessionID,
			FilterKey: "branch/main",
			Text:      "summary " + content,
			Boundary:  map[string]any{"last_event_id": eventID},
		}},
	}
}

func invocationScopedSession(sessionID, eventInvocationID, trackInvocationID, content string) SessionSnapshot {
	return SessionSnapshot{
		ID:      sessionID,
		AppName: "replay",
		UserID:  "user-1",
		Events: []EventSnapshot{{
			ID:           "event-1",
			InvocationID: eventInvocationID,
			Author:       "assistant",
			Role:         "assistant",
			Content:      content,
		}},
		Tracks: []TrackSnapshot{{
			Name: "tool",
			Events: []TrackEventSnapshot{{
				EventType:    "completed",
				InvocationID: trackInvocationID,
			}},
		}},
	}
}

func memoryEventTimeSnapshot(
	scope MemoryScope,
	firstEventTime any,
	secondEventTime any,
) Snapshot {
	first := scopedMemory("memory-1", scope, "first")
	first.Metadata = map[string]any{memoryEventTimeMetadataKey: firstEventTime}
	second := scopedMemory("memory-2", scope, "second")
	second.Metadata = map[string]any{memoryEventTimeMetadataKey: secondEventTime}
	return Snapshot{
		Memories: []MemorySnapshot{first, second},
		MemorySearches: []MemorySearchSnapshot{{
			AppName: scope.AppName,
			UserID:  scope.UserID,
			Query:   "first",
			Results: []MemorySnapshot{first},
		}},
	}
}

func scopedMemory(id string, scope MemoryScope, content string) MemorySnapshot {
	return MemorySnapshot{
		ID:      id,
		AppName: scope.AppName,
		UserID:  scope.UserID,
		Scope:   scope,
		Content: content,
	}
}

func scopedMemorySearch(scope MemoryScope, result MemorySnapshot) MemorySearchSnapshot {
	return MemorySearchSnapshot{
		AppName: scope.AppName,
		UserID:  scope.UserID,
		Query:   result.Content,
		Results: []MemorySnapshot{result},
	}
}

type normalizationObservation struct {
	timestamp time.Time
	score     float64
}

func normalizationFixture(
	eventID string,
	memoryID string,
	invocationID string,
	observation normalizationObservation,
) Snapshot {
	timestamp := observation.timestamp
	return Snapshot{
		Sessions: []SessionSnapshot{{
			ID:        "session-1",
			AppName:   "replay",
			UserID:    "user-1",
			CreatedAt: timestamp,
			UpdatedAt: timestamp.Add(time.Second),
			State: map[string]StateValueSnapshot{
				"profile": JSONStateValue(map[string]any{"active": true, "level": 2}),
			},
			Events: []EventSnapshot{{
				ID:           eventID,
				InvocationID: invocationID,
				Author:       "assistant",
				Role:         "assistant",
				Content:      "done",
				Object:       "chat.completion",
				Done:         true,
				Timestamp:    timestamp,
				ToolCalls: []ToolCallSnapshot{{
					ID:        "call-1",
					Name:      "lookup",
					Arguments: `{"query":"weather","count":2}`,
				}},
			}},
			Summaries: []SummarySnapshot{{
				SessionID: "session-1",
				FilterKey: "branch/main",
				Text:      "summary",
				Version:   1,
				UpdatedAt: timestamp,
			}},
			Tracks: []TrackSnapshot{{
				Name: "tool",
				Events: []TrackEventSnapshot{{
					EventType:    "completed",
					InvocationID: invocationID,
					Duration:     35 * time.Millisecond,
					Timestamp:    timestamp,
					Payload: map[string]any{
						"latency_ms": 35,
						"status":     "ok",
					},
				}},
			}},
		}},
		Memories: []MemorySnapshot{{
			ID:        memoryID,
			AppName:   "replay",
			UserID:    "user-1",
			Scope:     MemoryScope{AppName: "replay", UserID: "user-1"},
			Content:   "prefers concise replies",
			Topics:    []string{"style", "preference"},
			Score:     observation.score,
			CreatedAt: timestamp,
			UpdatedAt: timestamp.Add(time.Second),
			Metadata: map[string]any{
				"kind":             "fact",
				"backend_metadata": "private",
			},
		}},
	}
}
