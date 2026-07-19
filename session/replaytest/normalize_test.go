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
	"reflect"
	"testing"
	"time"
)

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
