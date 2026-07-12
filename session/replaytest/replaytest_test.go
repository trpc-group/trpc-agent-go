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
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestNormalizerNormalize(t *testing.T) {
	firstTime := time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Minute)
	first := event.New("generated-a", "user", event.WithResponse(&model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleUser, Content: "hello"}}},
	}))
	first.ID, first.Timestamp = "backend-event-a", firstTime
	first.StateDelta = session.StateMap{"count": []byte(`1`)}
	second := event.New("generated-a", "assistant", event.WithResponse(&model.Response{
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hi"}}},
	}))
	second.ID, second.Timestamp = "backend-event-b", secondTime
	require.NoError(t, event.SetExtension(second, "example", map[string]any{"b": 2, "a": 1}))

	episodeTime := firstTime.Add(-time.Hour)
	memories := []*memory.Entry{
		{ID: "random-b", Memory: &memory.Memory{Memory: "z fact", Topics: []string{"z", "a"}}, Score: 0.4},
		{ID: "random-a", Memory: &memory.Memory{Memory: "a episode", Kind: memory.KindEpisode, EventTime: &episodeTime, Participants: []string{"B", "A"}}},
	}
	sess := session.NewSession("app", "user", "session",
		session.WithSessionEvents([]event.Event{*first, *second}),
		session.WithSessionState(session.StateMap{"json": []byte(`{"b":2,"a":1}`), "plain": []byte("text")}),
		session.WithSessionSummaries(map[string]*session.Summary{"branch": {
			Summary: "summary", Topics: []string{"z", "a"}, Boundary: session.NewSummaryBoundaryWithEventID("branch", secondTime, second.ID),
		}}),
	)
	sess.Tracks = map[session.Track]*session.TrackEvents{"tool": {
		Track: "tool", Events: []session.TrackEvent{{Track: "tool", Timestamp: time.Now(), Payload: json.RawMessage(`{"status":"ok","duration_ms":12}`)}},
	}}

	snapshot, err := DefaultNormalizer().Normalize(sess, memories, map[string]Capability{
		"events": {Supported: true}, "ttl": {Supported: false, Reason: "not configured"},
	})
	require.NoError(t, err)
	require.Equal(t, "event-000", snapshot.Events[0]["id"])
	require.Equal(t, "invocation-000", snapshot.Events[1]["invocationId"])
	require.NotContains(t, snapshot.Events[0], "timestamp")
	require.Equal(t, map[string]any{"a": int64(1), "b": int64(2)}, snapshot.State["json"])
	require.Equal(t, "z fact", snapshot.Memories[0].Content)
	require.Equal(t, []string{"a", "z"}, snapshot.Memories[0].Topics)
	branchSummary := snapshot.Summaries["branch"]
	require.Equal(t, "session", branchSummary.SessionID)
	require.Equal(t, "app", branchSummary.AppName)
	require.Equal(t, "user", branchSummary.UserID)
	require.Equal(t, "branch", branchSummary.FilterKey)
	require.Equal(t, 1, *branchSummary.LastEventIndex)
	require.Equal(t, map[string]any{"status": "ok"}, snapshot.Tracks["tool"][0].Payload)
	require.Equal(t, "not configured", snapshot.Unsupported["ttl"])
}

func TestNormalizerToolIdentifiersAndJSONPayloads(t *testing.T) {
	const largeInteger int64 = 9007199254740993
	toolCallID := "backend-tool-call"
	invocationID := "backend-invocation"
	parentInvocationID := "backend-parent-invocation"

	toolCallEvent := event.New(invocationID, "assistant", event.WithResponse(&model.Response{
		Choices: []model.Choice{{Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				Type: "function",
				ID:   toolCallID,
				Function: model.FunctionDefinitionParam{
					Name:      "lookup",
					Arguments: []byte(`{"large":9007199254740993,"nested":{"tool_call_id":"backend-tool-call"}}`),
				},
			}},
		}}},
	}))
	toolCallEvent.ParentInvocationID = parentInvocationID
	toolCallEvent.ParentMetadata = &event.ParentInvocationMetadata{
		TriggerType: event.TriggerTypeToolCall,
		TriggerID:   toolCallID,
		TriggerName: "lookup",
	}
	toolCallEvent.LongRunningToolIDs = map[string]struct{}{toolCallID: {}}
	require.NoError(t, event.SetExtension(toolCallEvent, event.ToolCallArgsExtensionKey, map[string]any{
		toolCallID: map[string]any{"large": largeInteger},
	}))
	require.NoError(t, event.SetExtension(toolCallEvent, "identifiers", map[string]any{
		"invocation_id": parentInvocationID,
		"triggerId":     toolCallID,
		"tool_call_id":  largeInteger,
	}))

	toolResponseEvent := event.New(invocationID, "tool", event.WithResponse(&model.Response{
		Choices: []model.Choice{{Message: model.Message{
			Role:    model.RoleTool,
			ToolID:  toolCallID,
			Content: `{"ok":true,"large":9007199254740993}`,
		}}},
	}))

	sess := session.NewSession("app", "user", "tools",
		session.WithSessionEvents([]event.Event{*toolCallEvent, *toolResponseEvent}),
	)
	snapshot, err := DefaultNormalizer().Normalize(sess, nil, nil)
	require.NoError(t, err)
	require.Len(t, snapshot.Events, 2)

	require.Equal(t, "invocation-000", snapshot.Events[0]["invocationId"])
	require.Equal(t, "invocation-001", snapshot.Events[0]["parentInvocationId"])
	parentMetadata := snapshot.Events[0]["parentMetadata"].(map[string]any)
	require.Equal(t, "tool-call-000", parentMetadata["triggerId"])

	choices := snapshot.Events[0]["choices"].([]any)
	choice := choices[0].(map[string]any)
	message := choice["message"].(map[string]any)
	toolCalls := message["tool_calls"].([]any)
	toolCall := toolCalls[0].(map[string]any)
	require.Equal(t, "tool-call-000", toolCall["id"])
	function := toolCall["function"].(map[string]any)
	arguments := function["arguments"].(map[string]any)
	require.Equal(t, largeInteger, arguments["large"])
	nestedArguments := arguments["nested"].(map[string]any)
	require.Equal(t, "tool-call-000", nestedArguments["tool_call_id"])

	longRunning := snapshot.Events[0]["longRunningToolIDs"].(map[string]any)
	require.Contains(t, longRunning, "tool-call-000")
	extensions := snapshot.Events[0]["extensions"].(map[string]any)
	toolArgs := extensions[event.ToolCallArgsExtensionKey].(map[string]any)
	require.Equal(t, largeInteger, toolArgs["tool-call-000"].(map[string]any)["large"])
	identifiers := extensions["identifiers"].(map[string]any)
	require.Equal(t, "invocation-001", identifiers["invocation_id"])
	require.Equal(t, "tool-call-000", identifiers["triggerId"])
	require.Equal(t, largeInteger, identifiers["tool_call_id"])
	compared, err := snapshot.Clone()
	require.NoError(t, err)
	comparedExtensions := compared.Events[0]["extensions"].(map[string]any)
	comparedIdentifiers := comparedExtensions["identifiers"].(map[string]any)
	comparedIdentifiers["tool_call_id"] = largeInteger + 1
	diffs, err := Compare("non-string-identifier", "baseline", "compared", snapshot, compared, nil)
	require.NoError(t, err)
	requireDiffAtPath(t, diffs, "$.events[0].extensions.identifiers.tool_call_id")

	responseChoices := snapshot.Events[1]["choices"].([]any)
	responseMessage := responseChoices[0].(map[string]any)["message"].(map[string]any)
	require.Equal(t, "tool-call-000", responseMessage["tool_id"])
	responseContent := responseMessage["content"].(map[string]any)
	require.Equal(t, true, responseContent["ok"])
	require.Equal(t, largeInteger, responseContent["large"])
}

func TestNormalizeMemoriesOrderedAndUnordered(t *testing.T) {
	entries := []*memory.Entry{
		{
			ID: "backend-b", AppName: "app", UserID: "user", Score: 0.25,
			Memory: &memory.Memory{Memory: "z memory", Topics: []string{"z", "a"}},
		},
		{
			ID: "backend-a", AppName: "app", UserID: "user", Score: 0.75,
			Memory: &memory.Memory{Memory: "a memory"},
		},
	}

	ordered, err := normalizeMemories(entries, false)
	require.NoError(t, err)
	require.Equal(t, []string{"z memory", "a memory"}, []string{ordered[0].Content, ordered[1].Content})
	require.Equal(t, []int{0, 1}, []int{ordered[0].Rank, ordered[1].Rank})
	require.Equal(t, []float64{0.25, 0.75}, []float64{ordered[0].Score, ordered[1].Score})
	require.Equal(t, "app", ordered[0].AppName)
	require.Equal(t, "user", ordered[0].UserID)
	require.Equal(t, []string{"a", "z"}, ordered[0].Topics)

	reversed := []*memory.Entry{entries[1], entries[0]}
	orderedReversed, err := normalizeMemories(reversed, false)
	require.NoError(t, err)
	require.NotEqual(t, ordered, orderedReversed)

	unordered, err := normalizeMemories(entries, true)
	require.NoError(t, err)
	unorderedReversed, err := normalizeMemories(reversed, true)
	require.NoError(t, err)
	require.Equal(t, unordered, unorderedReversed)
	for _, item := range unordered {
		require.Equal(t, -1, item.Rank)
	}
}

func TestNormalizeMemoryScore(t *testing.T) {
	tests := []struct {
		name  string
		score float64
		want  float64
	}{
		{name: "zero", score: 0, want: 0},
		{name: "negative zero", score: -0.0000004, want: 0},
		{name: "round down", score: 0.1234564, want: 0.123456},
		{name: "round up", score: 0.1234566, want: 0.123457},
		{name: "remove backend drift", score: 1.0000004, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeMemoryScore(tt.score)
			require.InDelta(t, tt.want, got, 1e-12)
			if got == 0 {
				require.False(t, math.Signbit(got), "normalized zero must not retain a negative sign")
			}
		})
	}

	memories, err := normalizeMemories([]*memory.Entry{{
		Score: 0.33333349, Memory: &memory.Memory{Memory: "rounded"},
	}}, false)
	require.NoError(t, err)
	require.Equal(t, 0.333333, memories[0].Score)
}

func TestOrderEventsByTimestampInCaptureAndHarness(t *testing.T) {
	early := time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC)
	late := early.Add(time.Minute)
	earlyA := event.Event{ID: "early-a", Author: "early-a", Timestamp: early}
	earlyB := event.Event{ID: "early-b", Author: "early-b", Timestamp: early}
	lateEvent := event.Event{ID: "late", Author: "late", Timestamp: late}

	makeBackend := func(name string, events []event.Event) Backend {
		sess := session.NewSession("app", "user", "ordered",
			session.WithSessionEvents(events),
		)
		return Backend{
			Name: name, Session: stubSessionService{sess: sess}, Memory: stubMemoryService{},
			SessionKey: session.Key{AppName: "app", UserID: "user", SessionID: "ordered"},
		}
	}
	storedOrder := []event.Event{lateEvent, earlyA, earlyB}
	backendA := makeBackend("a", storedOrder)
	backendB := makeBackend("b", []event.Event{earlyA, earlyB, lateEvent})

	unsorted, err := Capture(context.Background(), backendA, CaptureOptions{})
	require.NoError(t, err)
	require.Equal(t, []any{"late", "early-a", "early-b"}, []any{
		unsorted.Events[0]["author"], unsorted.Events[1]["author"], unsorted.Events[2]["author"],
	})

	sorted, err := Capture(context.Background(), backendA, CaptureOptions{OrderEventsByTimestamp: true})
	require.NoError(t, err)
	require.Equal(t, []any{"early-a", "early-b", "late"}, []any{
		sorted.Events[0]["author"], sorted.Events[1]["author"], sorted.Events[2]["author"],
	})
	storedSession, err := backendA.Session.GetSession(context.Background(), backendA.SessionKey)
	require.NoError(t, err)
	require.Equal(t, "late", storedSession.Events[0].Author, "capture must sort a clone")

	harness := Harness{Backends: []Backend{backendA, backendB}}
	unsortedReport, err := harness.Run(context.Background(), Case{
		Name: "stored order", Run: func(context.Context, Backend) error { return nil },
	})
	require.NoError(t, err)
	require.True(t, HasUnexpectedDiff(unsortedReport))
	sortedReport, err := harness.Run(context.Background(), Case{
		Name: "timestamp order", OrderEventsByTimestamp: true,
		Run: func(context.Context, Backend) error { return nil },
	})
	require.NoError(t, err)
	require.False(t, HasUnexpectedDiff(sortedReport))
}

func TestSupportsDefaultsAndOverrides(t *testing.T) {
	require.True(t, Supports(Backend{}, CapabilityEvents))
	require.True(t, Supports(Backend{Capabilities: map[string]Capability{
		CapabilityEvents: {Supported: true},
	}}, CapabilityEvents))
	require.False(t, Supports(Backend{Capabilities: map[string]Capability{
		CapabilityEvents: {Supported: false, Reason: "disabled"},
	}}, CapabilityEvents))
	require.True(t, Supports(Backend{Capabilities: map[string]Capability{
		CapabilityEvents: {Supported: false, Reason: "disabled"},
	}}, CapabilityTracks), "omitted capabilities remain supported")
}

func TestNormalizeStateSkipsReservedTracksKey(t *testing.T) {
	evt := event.Event{
		ID: "event", Author: "user", Timestamp: time.Now(),
		StateDelta: session.StateMap{
			"tracks": []byte(`["internal"]`),
			"Tracks": []byte(`"user-value"`),
			"value":  []byte(`1`),
		},
	}
	sess := session.NewSession("app", "user", "state",
		session.WithSessionEvents([]event.Event{evt}),
		session.WithSessionState(session.StateMap{
			"tracks": []byte(`["internal"]`),
			"Tracks": []byte(`"user-value"`),
			"value":  []byte(`1`),
		}),
	)

	snapshot, err := DefaultNormalizer().Normalize(sess, nil, nil)
	require.NoError(t, err)
	require.NotContains(t, snapshot.State, "tracks")
	require.Equal(t, "user-value", snapshot.State["Tracks"])
	require.Equal(t, int64(1), snapshot.State["value"])
	stateDelta := snapshot.Events[0]["stateDelta"].(map[string]any)
	require.NotContains(t, stateDelta, "tracks")
	require.Equal(t, "user-value", stateDelta["Tracks"])
	require.Equal(t, int64(1), stateDelta["value"])
}

func TestCompareAndAllowedDiffValidation(t *testing.T) {
	left := Snapshot{SessionID: "s", State: map[string]any{"value": 1}, Memories: []MemorySnapshot{{ID: "memory-000", Content: "left"}}, Tracks: map[string][]TrackSnapshot{"tool": {{Track: "tool", Payload: map[string]any{"status": "ok"}}}}}
	right := Snapshot{SessionID: "s", State: map[string]any{"value": 2}, Memories: []MemorySnapshot{{ID: "memory-000", Content: "right"}}, Tracks: map[string][]TrackSnapshot{"tool": {{Track: "tool", Payload: map[string]any{"status": "failed"}}}}}
	rules := []AllowedDiff{{Section: "state", Path: "$.state.value", BackendA: "sqlite", BackendB: "inmemory", Reason: "documented conversion"}}
	diffs, err := Compare("case", "inmemory", "sqlite", left, right, rules)
	require.NoError(t, err)
	require.Len(t, diffs, 3)
	require.True(t, diffs[1].Allowed)
	require.Equal(t, "memory-000", diffs[0].MemoryID)
	require.Equal(t, "tool", diffs[2].TrackName)

	_, err = Compare("case", "a", "b", left, right, []AllowedDiff{{Section: "*", Path: "$.*", BackendA: "a", BackendB: "b", Reason: "broad"}})
	require.ErrorContains(t, err, "exact")
	_, err = Compare("case", "a", "b", left, right, []AllowedDiff{{Section: "state"}})
	require.ErrorContains(t, err, "requires")
}

func TestCompareMissingNullEventAndSpecialKeyContext(t *testing.T) {
	specialSummaryKey := `branch."quoted"`
	specialTrackKey := "tool/trace"
	left := Snapshot{
		SessionID: "session",
		Events:    []map[string]any{{"status": "left"}},
		State: map[string]any{
			"null-on-left": nil,
			"a.b":          "left",
		},
		Summaries: map[string]SummarySnapshot{
			specialSummaryKey: {FilterKey: specialSummaryKey, Text: "left"},
			"":                {FilterKey: "", Text: "full left"},
		},
		Tracks: map[string][]TrackSnapshot{
			specialTrackKey: {{Track: specialTrackKey, Payload: map[string]any{"status": "left"}}},
		},
	}
	right := Snapshot{
		SessionID: "session",
		Events:    []map[string]any{{"status": "right"}},
		State: map[string]any{
			"null-on-right": nil,
			"a.b":           "right",
		},
		Summaries: map[string]SummarySnapshot{
			specialSummaryKey: {FilterKey: specialSummaryKey, Text: "right"},
			"":                {FilterKey: "", Text: "full right"},
		},
		Tracks: map[string][]TrackSnapshot{
			specialTrackKey: {{Track: specialTrackKey, Payload: map[string]any{"status": "right"}}},
		},
	}

	diffs, err := Compare("special", "left", "right", left, right, nil)
	require.NoError(t, err)

	eventDiff := requireDiffAtPath(t, diffs, "$.events[0].status")
	require.Equal(t, 0, *eventDiff.EventIndex)
	leftMissing := requireDiffAtPath(t, diffs, `$.state["null-on-left"]`)
	require.True(t, leftMissing.BaselinePresent)
	require.False(t, leftMissing.ComparedPresent)
	require.Nil(t, leftMissing.Baseline)
	require.Equal(t, MissingValue{Missing: true}, leftMissing.Compared)
	rightMissing := requireDiffAtPath(t, diffs, `$.state["null-on-right"]`)
	require.False(t, rightMissing.BaselinePresent)
	require.True(t, rightMissing.ComparedPresent)
	require.Equal(t, MissingValue{Missing: true}, rightMissing.Baseline)
	require.Nil(t, rightMissing.Compared)
	requireDiffAtPath(t, diffs, `$.state["a.b"]`)

	summaryDiff := requireDiffAtPath(t, diffs, `$.summaries["branch.\"quoted\""].text`)
	require.NotNil(t, summaryDiff.SummaryKey)
	require.Equal(t, specialSummaryKey, *summaryDiff.SummaryKey)
	fullSummaryDiff := requireDiffAtPath(t, diffs, `$.summaries[""].text`)
	require.NotNil(t, fullSummaryDiff.SummaryKey)
	require.Equal(t, "", *fullSummaryDiff.SummaryKey)
	trackDiff := requireDiffAtPath(t, diffs, `$.tracks["tool/trace"][0].payload.status`)
	require.Equal(t, specialTrackKey, trackDiff.TrackName)
}

func TestCompareIgnoresUnsupportedSections(t *testing.T) {
	left := Snapshot{
		SessionID: "session", AppName: "app", UserID: "user",
		Events:   []map[string]any{{"value": "left"}},
		State:    map[string]any{"value": "left"},
		Memories: []MemorySnapshot{{ID: "memory-000", Content: "left"}},
		Summaries: map[string]SummarySnapshot{
			"branch": {FilterKey: "branch", Text: "left"},
		},
		Tracks: map[string][]TrackSnapshot{
			"tool": {{Track: "tool", Payload: "left"}},
		},
		Unsupported: map[string]string{
			CapabilityEvents:  "not supported",
			CapabilityState:   "not supported",
			CapabilityMemory:  "not supported",
			CapabilitySummary: "not supported",
			CapabilityTracks:  "not supported",
		},
	}
	right := Snapshot{
		SessionID: "session", AppName: "app", UserID: "user",
		Events:   []map[string]any{{"value": "right"}},
		State:    map[string]any{"value": "right"},
		Memories: []MemorySnapshot{{ID: "memory-000", Content: "right"}},
		Summaries: map[string]SummarySnapshot{
			"branch": {FilterKey: "branch", Text: "right"},
		},
		Tracks: map[string][]TrackSnapshot{
			"tool": {{Track: "tool", Payload: "right"}},
		},
	}

	diffs, err := Compare("unsupported", "left", "right", left, right, nil)
	require.NoError(t, err)
	require.Empty(t, diffs)

	left.Unsupported = map[string]string{"custom": "not supported"}
	diffs, err = Compare("custom", "left", "right", left, right, nil)
	require.NoError(t, err)
	require.NotEmpty(t, diffs)

	expectedSections := map[string]string{
		CapabilityEvents: "events", CapabilityState: "state",
		CapabilityMemory: "memories", CapabilitySummary: "summaries",
		CapabilityTracks: "tracks", CapabilityEventStateDeltaNull: "", "custom": "",
	}
	for capability, want := range expectedSections {
		require.Equal(t, want, sectionForCapability(capability))
	}
}

func TestFineGrainedCapabilityRequiresExactAllowedDiff(t *testing.T) {
	const reason = "event state delta null is unsupported"
	left := Snapshot{
		SessionID: "session",
		State:     map[string]any{"pending": nil},
	}
	right := Snapshot{
		SessionID: "session",
		State:     map[string]any{"pending": true},
		Unsupported: map[string]string{
			CapabilityEventStateDeltaNull: reason,
		},
	}
	capabilities := map[string]map[string]Capability{
		"redis": {
			CapabilityEventStateDeltaNull: {
				Supported: false, Reason: reason, AllowedDiff: true,
			},
		},
	}

	diffs, err := Compare("recovery", "inmemory", "redis", left, right, nil)
	require.NoError(t, err)
	require.Len(t, diffs, 1)
	require.False(t, diffs[0].Allowed)
	require.True(t, HasUnexpectedDiff(CaseReport{
		Capabilities: capabilities,
		Diffs:        diffs,
	}))

	rules := []AllowedDiff{{
		Section: "state", Path: "$.state.pending",
		BackendA: "inmemory", BackendB: "redis", Reason: reason,
	}}
	diffs, err = Compare("recovery", "inmemory", "redis", left, right, rules)
	require.NoError(t, err)
	require.Len(t, diffs, 1)
	require.True(t, diffs[0].Allowed)
	require.Equal(t, reason, diffs[0].Explanation)
	require.False(t, HasUnexpectedDiff(CaseReport{
		Capabilities: capabilities,
		Diffs:        diffs,
	}))
}

func TestHarnessRunAndWriteReport(t *testing.T) {
	ctx := context.Background()
	sess := session.NewSession("app", "user", "session")
	entries := []*memory.Entry{{ID: "id", Memory: &memory.Memory{Memory: "fact"}}}
	backends := []Backend{
		{Name: "a", Session: stubSessionService{sess: sess}, Memory: stubMemoryService{entries: entries}, SessionKey: session.Key{AppName: "app", UserID: "user", SessionID: "session"}},
		{Name: "b", Session: stubSessionService{sess: sess.Clone()}, Memory: stubMemoryService{entries: entries}, SessionKey: session.Key{AppName: "app", UserID: "user", SessionID: "session"}},
	}
	report, err := (Harness{Backends: backends}).Run(ctx, Case{Name: "same", Run: func(context.Context, Backend) error { return nil }})
	require.NoError(t, err)
	require.False(t, HasUnexpectedDiff(report))

	path := filepath.Join(t.TempDir(), "nested", "report.json")
	require.NoError(t, WriteReport(path, Report{Cases: []CaseReport{report}}))
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	var decoded Report
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, 1, decoded.Version)

	_, err = (Harness{Backends: backends[:1]}).Run(ctx, Case{Name: "bad", Run: func(context.Context, Backend) error { return nil }})
	require.ErrorContains(t, err, "at least two")
	require.Error(t, WriteReport("", Report{}))
}

func TestSnapshotCloneCaptureAndBackendValidation(t *testing.T) {
	original := Snapshot{
		SessionID: "session",
		Events: []map[string]any{{
			"nested": map[string]any{"value": "original"},
		}},
		State: map[string]any{"value": "original"},
	}
	cloned, err := original.Clone()
	require.NoError(t, err)
	cloned.Events[0]["nested"].(map[string]any)["value"] = "changed"
	cloned.State["value"] = "changed"
	require.Equal(t, "original", original.Events[0]["nested"].(map[string]any)["value"])
	require.Equal(t, "original", original.State["value"])

	_, err = (Snapshot{State: map[string]any{"bad": make(chan int)}}).Clone()
	require.ErrorContains(t, err, "marshal snapshot clone")

	ctx := context.Background()
	sess := session.NewSession("app", "user", "capture")
	sess.Tracks = map[session.Track]*session.TrackEvents{
		"tool": {
			Track: "tool",
			Events: []session.TrackEvent{{
				Track:   "tool",
				Payload: json.RawMessage(`{"status":"ok","duration_ms":12}`),
			}},
		},
	}
	entries := []*memory.Entry{
		{AppName: "app", UserID: "user", Score: 0.2, Memory: &memory.Memory{Memory: "z"}},
		{AppName: "app", UserID: "user", Score: 0.8, Memory: &memory.Memory{Memory: "a"}},
	}
	backend := Backend{
		Name:    "capture",
		Session: stubSessionService{sess: sess},
		Memory:  stubMemoryService{entries: entries},
		SessionKey: session.Key{
			AppName: "app", UserID: "user", SessionID: "capture",
		},
	}

	defaultSnapshot, err := Capture(ctx, backend, CaptureOptions{})
	require.NoError(t, err)
	defaultPayload := defaultSnapshot.Tracks["tool"][0].Payload.(map[string]any)
	require.NotContains(t, defaultPayload, "duration_ms")

	customSnapshot, err := Capture(ctx, backend, CaptureOptions{
		Normalizer:        Normalizer{VolatilePayloadKeys: map[string]struct{}{}},
		UnorderedMemories: true,
	})
	require.NoError(t, err)
	require.Equal(t, -1, customSnapshot.Memories[0].Rank)
	customPayload := customSnapshot.Tracks["tool"][0].Payload.(map[string]any)
	require.Equal(t, int64(12), customPayload["duration_ms"])

	_, err = Capture(ctx, Backend{}, CaptureOptions{})
	require.ErrorContains(t, err, "invalid backend configuration")
	loadErr := errors.New("capture load failed")
	loadFailure := backend
	loadFailure.Session = stubSessionService{err: loadErr}
	_, err = Capture(ctx, loadFailure, CaptureOptions{})
	require.ErrorIs(t, err, loadErr)
	normalizeFailure := backend
	normalizeFailure.Load = func(context.Context, Backend) (*session.Session, []*memory.Entry, error) {
		return nil, nil, nil
	}
	_, err = Capture(ctx, normalizeFailure, CaptureOptions{})
	require.ErrorContains(t, err, "session is nil")

	sessionOnly := Backend{
		Name:       "session-only",
		Session:    stubSessionService{sess: sess},
		SessionKey: backend.SessionKey,
		Capabilities: map[string]Capability{
			CapabilityMemory: {Supported: false, Reason: "not configured"},
		},
	}
	sessionOnlySnapshot, err := Capture(ctx, sessionOnly, CaptureOptions{})
	require.NoError(t, err)
	require.Nil(t, sessionOnlySnapshot.Memories)
	require.Equal(t, "not configured", sessionOnlySnapshot.Unsupported[CapabilityMemory])

	validationTests := []struct {
		name    string
		backend Backend
		wantErr string
	}{
		{name: "blank name", backend: Backend{Session: stubSessionService{sess: sess}}, wantErr: "invalid backend"},
		{name: "nil session", backend: Backend{Name: "nil-session"}, wantErr: "invalid backend"},
		{name: "missing memory", backend: Backend{Name: "missing-memory", Session: stubSessionService{sess: sess}}, wantErr: "requires a memory service"},
		{name: "memory explicitly unsupported", backend: sessionOnly},
		{
			name: "empty capability name",
			backend: Backend{
				Name: "empty-capability", Session: stubSessionService{sess: sess},
				Memory: stubMemoryService{}, Capabilities: map[string]Capability{" ": {Supported: true}},
			},
			wantErr: "empty capability name",
		},
		{
			name: "unsupported without reason",
			backend: Backend{
				Name: "missing-reason", Session: stubSessionService{sess: sess},
				Memory: stubMemoryService{}, Capabilities: map[string]Capability{CapabilityTracks: {Supported: false}},
			},
			wantErr: "requires a reason",
		},
	}
	for _, tt := range validationTests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateBackend(tt.backend)
			if tt.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorContains(t, err, tt.wantErr)
		})
	}

	require.ErrorContains(t, validateBackends([]Backend{backend, backend}), "duplicate backend name")
	capabilities := map[string]Capability{CapabilityTracks: {Supported: true}}
	capabilityClone := cloneCapabilities(capabilities)
	capabilityClone[CapabilityTracks] = Capability{Supported: false, Reason: "changed"}
	require.True(t, capabilities[CapabilityTracks].Supported)

	require.True(t, HasUnexpectedDiff(CaseReport{
		SkippedBackends: map[string][]string{"backend": {CapabilityTracks}},
		Capabilities: map[string]map[string]Capability{
			"backend": {CapabilityTracks: {Supported: false, Reason: "missing"}},
		},
	}))
	require.False(t, HasUnexpectedDiff(CaseReport{
		SkippedBackends: map[string][]string{"backend": {CapabilityTracks}},
		Capabilities: map[string]map[string]Capability{
			"backend": {CapabilityTracks: {Supported: false, Reason: "missing", AllowedDiff: true}},
		},
	}))
	require.False(t, HasUnexpectedDiff(CaseReport{
		RequiredCapabilities: []string{CapabilityEvents},
		Capabilities: map[string]map[string]Capability{
			"backend": {
				CapabilityEvents: {Supported: true},
				CapabilityTracks: {Supported: false, Reason: "unrelated to this case"},
			},
		},
	}))
	require.True(t, HasUnexpectedDiff(CaseReport{Inconclusive: true}))
	require.True(t, HasUnexpectedDiff(CaseReport{
		SkippedBackends: map[string][]string{"missing": {CapabilityTracks}},
	}))
}

func TestNormalizerBoundaryAndErrorCases(t *testing.T) {
	normalizer := DefaultNormalizer()

	_, err := normalizer.Normalize(nil, nil, nil)
	require.ErrorContains(t, err, "session is nil")

	badEventSession := session.NewSession("app", "user", "bad-event",
		session.WithSessionEvents([]event.Event{{
			Extensions: map[string]json.RawMessage{"broken": json.RawMessage(`{`)},
		}}),
	)
	_, err = normalizer.Normalize(badEventSession, nil, nil)
	require.ErrorContains(t, err, "marshal event 0")

	firstTime := time.Date(2026, 7, 1, 1, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Minute)
	first := event.Event{ID: "first", Timestamp: firstTime, Author: "user"}
	second := event.Event{ID: "second", Timestamp: secondTime, Author: "assistant"}
	sess := session.NewSession("app", "user", "boundary",
		session.WithSessionEvents([]event.Event{first, second}),
		session.WithSessionState(session.StateMap{
			"nil":   nil,
			"array": []byte(`[1,{"value":2}]`),
		}),
		session.WithSessionSummaries(map[string]*session.Summary{
			"nil": nil,
			"legacy": {
				Summary:   "legacy summary",
				UpdatedAt: firstTime.Add(30 * time.Second),
			},
			"missing-event": {
				Summary: "before all events",
				Boundary: session.NewSummaryBoundaryWithEventID(
					"missing-event", firstTime.Add(-time.Minute), "unknown",
				),
			},
			"no-boundary": {Summary: "no boundary"},
		}),
	)
	sess.Tracks = map[session.Track]*session.TrackEvents{
		"raw": {
			Track: "raw",
			Events: []session.TrackEvent{{
				Track:   "raw",
				Payload: json.RawMessage(`{not-json`),
			}},
		},
	}

	_, err = normalizer.Normalize(sess, []*memory.Entry{nil}, nil)
	require.ErrorContains(t, err, "memory 0 is nil")
	_, err = normalizer.Normalize(sess, []*memory.Entry{{}}, nil)
	require.ErrorContains(t, err, "content is nil")
	snapshot, err := normalizer.Normalize(sess, []*memory.Entry{
		{Memory: &memory.Memory{Memory: "kept"}},
	}, nil)
	require.NoError(t, err)
	require.Nil(t, snapshot.State["nil"])
	require.Equal(t, []any{int64(1), map[string]any{"value": int64(2)}}, snapshot.State["array"])
	require.Len(t, snapshot.Memories, 1)
	require.Equal(t, 0, *snapshot.Summaries["legacy"].UpdatedAtEventIndex)
	require.Nil(t, snapshot.Summaries["legacy"].LastEventIndex)
	require.Equal(t, -1, *snapshot.Summaries["missing-event"].LastEventIndex)
	require.Equal(t, -1, *snapshot.Summaries["missing-event"].CutoffAtEventIndex)
	require.Nil(t, snapshot.Summaries["no-boundary"].LastEventIndex)
	require.Equal(t, "{not-json", snapshot.Tracks["raw"][0].Payload)
	nilTracks := normalizer.normalizeTracks(
		map[session.Track]*session.TrackEvents{"nil": nil},
		map[string]string{}, map[string]string{},
	)
	require.Contains(t, nilTracks, "nil")
	require.Nil(t, nilTracks["nil"])

	require.Equal(t, int64(42), normalizeJSON(json.Number("42"), nil))
	require.Equal(t, json.Number("1.5"), normalizeJSON(json.Number("1.5"), nil))
	require.Equal(t, json.Number("invalid"), normalizeJSON(json.Number("invalid"), nil))
}

func TestCompareErrorAndContextCases(t *testing.T) {
	bad := Snapshot{State: map[string]any{"unsupported": make(chan int)}}
	_, err := Compare("case", "a", "b", bad, Snapshot{}, nil)
	require.ErrorContains(t, err, "marshal snapshot")
	_, err = Compare("case", "a", "b", Snapshot{}, bad, nil)
	require.ErrorContains(t, err, "marshal snapshot")

	left := Snapshot{
		SessionID: "session",
		Memories:  []MemorySnapshot{{ID: "memory-000", Content: "same"}},
		Summaries: map[string]SummarySnapshot{"branch": {Text: "left"}},
	}
	right := Snapshot{
		SessionID: "session",
		Memories: []MemorySnapshot{
			{ID: "memory-000", Content: "same"},
			{ID: "memory-001", Content: "right only"},
		},
		Summaries: map[string]SummarySnapshot{"branch": {Text: "right"}},
	}
	diffs, err := Compare("case", "a", "b", left, right, nil)
	require.NoError(t, err)
	require.Len(t, diffs, 2)
	require.Equal(t, "memory-001", diffs[0].MemoryID)
	require.NotNil(t, diffs[1].SummaryKey)
	require.Equal(t, "branch", *diffs[1].SummaryKey)
	require.True(t, HasUnexpectedDiff(CaseReport{Diffs: diffs}))
	require.False(t, HasUnexpectedDiff(CaseReport{Diffs: []Diff{{Allowed: true}}}))

	require.Equal(t, "state", sectionForPath("$.state"))
	name, ok := contextPathKey(`$.summaries["branch.name"].text`, "$.summaries")
	require.True(t, ok)
	require.Equal(t, "branch.name", name)
	_, ok = indexedPath("$.memories[invalid]", "$.memories[")
	require.False(t, ok)
}

func TestLoadBackendAndHarnessErrorCases(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	sess := session.NewSession("app", "user", "session")
	entries := []*memory.Entry{{Memory: &memory.Memory{Memory: "fact"}}}

	customCalled := false
	loadedSession, loadedMemories, err := loadBackend(ctx, Backend{
		Name:       "custom",
		SessionKey: key,
		Load: func(context.Context, Backend) (*session.Session, []*memory.Entry, error) {
			customCalled = true
			return sess, entries, nil
		},
	})
	require.NoError(t, err)
	require.True(t, customCalled)
	require.Same(t, sess, loadedSession)
	require.Equal(t, entries, loadedMemories)

	readErr := errors.New("read failed")
	_, _, err = loadBackend(ctx, Backend{
		Session:    stubSessionService{err: readErr},
		Memory:     stubMemoryService{},
		SessionKey: key,
	})
	require.ErrorIs(t, err, readErr)
	_, _, err = loadBackend(ctx, Backend{
		Session:    stubSessionService{sess: sess},
		Memory:     stubMemoryService{err: readErr},
		SessionKey: key,
	})
	require.ErrorIs(t, err, readErr)

	validBackend := func(name string) Backend {
		return Backend{
			Name:       name,
			Session:    stubSessionService{sess: sess},
			Memory:     stubMemoryService{entries: entries},
			SessionKey: key,
		}
	}
	backends := []Backend{validBackend("a"), validBackend("b")}
	runOK := func(context.Context, Backend) error { return nil }

	_, err = (Harness{Backends: backends}).Run(ctx, Case{})
	require.ErrorContains(t, err, "requires name and run function")
	_, err = (Harness{Backends: backends}).Run(ctx, Case{
		Name: "empty-required", RequiredCapabilities: []string{" "}, Run: runOK,
	})
	require.ErrorContains(t, err, "empty required capability")
	_, err = (Harness{Backends: backends}).Run(ctx, Case{
		Name: "duplicate-required", RequiredCapabilities: []string{CapabilityEvents, CapabilityEvents}, Run: runOK,
	})
	require.ErrorContains(t, err, "duplicate required capability")
	_, err = (Harness{Backends: backends}).Run(ctx, Case{
		Name: "missing-required", RequiredCapabilities: []string{CapabilityEvents}, Run: runOK,
	})
	require.ErrorContains(t, err, "must declare required capability")
	declaredBackends := append([]Backend(nil), backends...)
	for i := range declaredBackends {
		declaredBackends[i].Capabilities = map[string]Capability{
			CapabilityEvents: {Supported: true},
		}
	}
	requiredReport, err := (Harness{Backends: declaredBackends}).Run(ctx, Case{
		Name: "declared-required", RequiredCapabilities: []string{CapabilityEvents}, Run: runOK,
	})
	require.NoError(t, err)
	require.Equal(t, []string{CapabilityEvents}, requiredReport.RequiredCapabilities)
	skippedBackends := append([]Backend(nil), declaredBackends...)
	skippedBackends[1].Capabilities = map[string]Capability{
		CapabilityEvents: {Supported: false, Reason: "not configured", AllowedDiff: true},
	}
	runs := make(map[string]int)
	skippedReport, err := (Harness{Backends: skippedBackends}).Run(ctx, Case{
		Name: "skip-unsupported", RequiredCapabilities: []string{CapabilityEvents},
		Run: func(_ context.Context, backend Backend) error {
			runs[backend.Name]++
			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, 1, runs["a"])
	require.Zero(t, runs["b"])
	require.Equal(t, []string{CapabilityEvents}, skippedReport.SkippedBackends["b"])
	require.True(t, skippedReport.Inconclusive)
	require.True(t, HasUnexpectedDiff(skippedReport))
	baselineUnsupported := append([]Backend(nil), skippedBackends...)
	baselineUnsupported[0], baselineUnsupported[1] = baselineUnsupported[1], baselineUnsupported[0]
	_, err = (Harness{Backends: baselineUnsupported}).Run(ctx, Case{
		Name: "unsupported-baseline", RequiredCapabilities: []string{CapabilityEvents}, Run: runOK,
	})
	require.ErrorContains(t, err, "baseline backend")

	invalidBackends := append([]Backend(nil), backends...)
	invalidBackends[1].Memory = nil
	_, err = (Harness{Backends: invalidBackends}).Run(ctx, Case{Name: "invalid", Run: runOK})
	require.ErrorContains(t, err, "requires a memory service")

	runErr := errors.New("run failed")
	_, err = (Harness{Backends: backends}).Run(ctx, Case{
		Name: "run-error",
		Run:  func(context.Context, Backend) error { return runErr },
	})
	require.ErrorIs(t, err, runErr)

	loadErrorBackends := append([]Backend(nil), backends...)
	loadErrorBackends[0].Session = stubSessionService{err: readErr}
	_, err = (Harness{Backends: loadErrorBackends}).Run(ctx, Case{Name: "load-error", Run: runOK})
	require.ErrorIs(t, err, readErr)

	nilSessionBackends := append([]Backend(nil), backends...)
	nilSessionBackends[0].Load = func(context.Context, Backend) (*session.Session, []*memory.Entry, error) {
		return nil, nil, nil
	}
	_, err = (Harness{Backends: nilSessionBackends}).Run(ctx, Case{Name: "normalize-error", Run: runOK})
	require.ErrorContains(t, err, "session is nil")

	_, err = (Harness{
		Backends:   backends,
		Normalizer: Normalizer{VolatilePayloadKeys: map[string]struct{}{}},
		Allowed: []AllowedDiff{{
			Section: "*", Path: "$.*", BackendA: "a", BackendB: "b", Reason: "too broad",
		}},
	}).Run(ctx, Case{Name: "compare-error", Run: runOK})
	require.ErrorContains(t, err, "exact")
}

func TestWriteReportFailureCases(t *testing.T) {
	t.Run("marshal report", func(t *testing.T) {
		err := WriteReport(filepath.Join(t.TempDir(), "report.json"), Report{
			Cases: []CaseReport{{
				Diffs: []Diff{{Baseline: make(chan int)}},
			}},
		})
		require.ErrorContains(t, err, "marshal replay report")
	})

	t.Run("create directory", func(t *testing.T) {
		blocker := filepath.Join(t.TempDir(), "blocker")
		require.NoError(t, os.WriteFile(blocker, []byte("file"), 0o600))
		err := WriteReport(filepath.Join(blocker, "report.json"), Report{})
		require.ErrorContains(t, err, "create report directory")
	})

	t.Run("write temporary file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "report.json")
		require.NoError(t, os.Mkdir(path+".tmp", 0o755))
		err := WriteReport(path, Report{})
		require.ErrorContains(t, err, "write replay report")
	})

	t.Run("publish report", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "report")
		require.NoError(t, os.Mkdir(path, 0o755))
		err := WriteReport(path, Report{})
		require.ErrorContains(t, err, "publish replay report")
	})

	t.Run("preserve version", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "report.json")
		require.NoError(t, WriteReport(path, Report{Version: 2}))
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		var report Report
		require.NoError(t, json.Unmarshal(raw, &report))
		require.Equal(t, 2, report.Version)
	})
}

func TestExampleReportDeclaresRequiredCapabilities(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(
		"testdata", "session_memory_summary_track_diff_report.json",
	))
	require.NoError(t, err)
	var report Report
	require.NoError(t, json.Unmarshal(raw, &report))

	for _, replayCase := range report.Cases {
		for _, backend := range replayCase.Backends {
			capabilities, exists := replayCase.Capabilities[backend]
			require.Truef(t, exists, "case %q is missing backend %q", replayCase.Name, backend)
			for _, required := range replayCase.RequiredCapabilities {
				require.Containsf(
					t, capabilities, required,
					"case %q backend %q is missing required capability %q",
					replayCase.Name, backend, required,
				)
			}
		}
	}
}

func requireDiffAtPath(t *testing.T, diffs []Diff, path string) Diff {
	t.Helper()
	for _, diff := range diffs {
		if diff.Path == path {
			return diff
		}
	}
	t.Fatalf("diff path %q not found in %+v", path, diffs)
	return Diff{}
}

type stubSessionService struct {
	session.Service
	sess *session.Session
	err  error
}

func (s stubSessionService) GetSession(context.Context, session.Key, ...session.Option) (*session.Session, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.sess == nil {
		return nil, nil
	}
	return s.sess.Clone(), nil
}

type stubMemoryService struct {
	memory.Service
	entries []*memory.Entry
	err     error
}

func (s stubMemoryService) ReadMemories(context.Context, memory.UserKey, int) ([]*memory.Entry, error) {
	if s.err != nil {
		return nil, s.err
	}
	return s.entries, nil
}
