//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package replayconsistency

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// ---------------------------------------------------------------------------
// replaySummarizer tests
// ---------------------------------------------------------------------------

func TestReplaySummarizer_ShouldSummarize(t *testing.T) {
	s := replaySummarizer{}
	assert.False(t, s.ShouldSummarize(nil))
	sess := &session.Session{}
	assert.False(t, s.ShouldSummarize(sess))
	sess.Events = []event.Event{{}}
	assert.True(t, s.ShouldSummarize(sess))
}

func TestReplaySummarizer_Summarize(t *testing.T) {
	s := replaySummarizer{}
	_, err := s.Summarize(context.Background(), nil)
	assert.ErrorIs(t, err, session.ErrNilSession)

	summary, err := s.Summarize(context.Background(), &session.Session{ID: "s1"})
	require.NoError(t, err)
	assert.Contains(t, summary, "s1")
}

func TestReplaySummarizer_SetPrompt(t *testing.T) {
	s := replaySummarizer{}
	s.SetPrompt("test")
}

func TestReplaySummarizer_SetModel(t *testing.T) {
	s := replaySummarizer{}
	s.SetModel(nil)
}

func TestReplaySummarizer_Metadata(t *testing.T) {
	s := replaySummarizer{}
	m := s.Metadata()
	assert.Equal(t, "replay_summarizer", m["name"])
}

// ---------------------------------------------------------------------------
// replayBackend tests
// ---------------------------------------------------------------------------

func TestReplayBackend_Kind(t *testing.T) {
	b := &replayBackend{kind: BackendKindSession}
	assert.Equal(t, BackendKindSession, b.Kind())
}

func TestReplayBackend_Supports(t *testing.T) {
	b := &replayBackend{trackSupport: true, sessionSvc: nil, memorySvc: nil}
	assert.True(t, b.Supports("track"))
	assert.False(t, b.Supports("summary"))
	assert.False(t, b.Supports("memory"))
	assert.True(t, b.Supports("unknown_feature"))

	b2 := &replayBackend{trackSupport: false, sessionSvc: struct{ session.Service }{nil}, memorySvc: struct{ memory.Service }{nil}}
	assert.False(t, b2.Supports("track"))
}

func TestReplayBackend_Close_NoServices(t *testing.T) {
	b := &replayBackend{}
	assert.NoError(t, b.Close())
}

// ---------------------------------------------------------------------------
// enabled / getEnvOrDefault tests
// ---------------------------------------------------------------------------

func TestEnabled(t *testing.T) {
	assert.False(t, enabled("NONEXISTENT_ENV_VAR_12345"))

	os.Setenv("TEST_ENABLED_TRUE", "1")
	assert.True(t, enabled("TEST_ENABLED_TRUE"))
	os.Unsetenv("TEST_ENABLED_TRUE")

	os.Setenv("TEST_ENABLED_YES", "yes")
	assert.True(t, enabled("TEST_ENABLED_YES"))
	os.Unsetenv("TEST_ENABLED_YES")

	os.Setenv("TEST_ENABLED_FALSE", "false")
	assert.False(t, enabled("TEST_ENABLED_FALSE"))
	os.Unsetenv("TEST_ENABLED_FALSE")

	os.Setenv("TEST_ENABLED_ZERO", "0")
	assert.False(t, enabled("TEST_ENABLED_ZERO"))
	os.Unsetenv("TEST_ENABLED_ZERO")

	os.Setenv("TEST_ENABLED_NO", "no")
	assert.False(t, enabled("TEST_ENABLED_NO"))
	os.Unsetenv("TEST_ENABLED_NO")
}

func TestGetEnvOrDefault(t *testing.T) {
	assert.Equal(t, "default", getEnvOrDefault("NONEXISTENT_ENV_VAR_12345", "default"))

	os.Setenv("TEST_GETENV_VAL", "custom")
	assert.Equal(t, "custom", getEnvOrDefault("TEST_GETENV_VAL", "default"))
	os.Unsetenv("TEST_GETENV_VAL")
}

// ---------------------------------------------------------------------------
// ValidateBackendConfig tests
// ---------------------------------------------------------------------------

func TestValidateBackendConfig(t *testing.T) {
	assert.NoError(t, ValidateBackendConfig("TEST", "value"))
	assert.Error(t, ValidateBackendConfig("TEST", ""))
}

// ---------------------------------------------------------------------------
// newDefaultReplayBackends tests
// ---------------------------------------------------------------------------

func TestNewDefaultReplayBackends_LightMode(t *testing.T) {
	backends, err := newDefaultReplayBackends(HarnessOptions{LightMode: true})
	require.NoError(t, err)
	require.Len(t, backends, 2)
	assert.Equal(t, "inmemory", backends[0].Name())
	assert.Equal(t, "sqlite", backends[1].Name())
	for _, b := range backends {
		b.Close()
	}
}

func TestNewDefaultReplayBackends_SkipEnv(t *testing.T) {
	backends, err := newDefaultReplayBackends(HarnessOptions{SkipEnv: true})
	require.NoError(t, err)
	require.Len(t, backends, 2)
	assert.Equal(t, "inmemory", backends[0].Name())
	assert.Equal(t, "sqlite", backends[1].Name())
	for _, b := range backends {
		b.Close()
	}
}

// ---------------------------------------------------------------------------
// NormalizeTrackEvent tests
// ---------------------------------------------------------------------------

func TestNormalizeTrackEvent(t *testing.T) {
	assert.Equal(t, NormalizedTrack{}, NormalizeTrackEvent(nil))

	evt := &session.TrackEvent{
		Track:     "tool-exec",
		Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Payload:   json.RawMessage(`{"status":"ok"}`),
	}
	nt := NormalizeTrackEvent(evt)
	assert.Equal(t, "tool-exec", nt.Track)
	assert.NotEmpty(t, nt.Timestamp)
	assert.Equal(t, `{"status":"ok"}`, nt.Payload)
}

// ---------------------------------------------------------------------------
// NormalizeEvent tests
// ---------------------------------------------------------------------------

func TestNormalizeEvent_Nil(t *testing.T) {
	assert.Equal(t, NormalizedEvent{}, NormalizeEvent(nil))
}

func TestNormalizeEvent_WithStateDeltaAndExtensions(t *testing.T) {
	evt := &event.Event{
		ID:         "evt-1",
		Author:     "user",
		Timestamp:  time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Branch:     "main",
		FilterKey:  "app-a",
		StateDelta: map[string][]byte{"key1": []byte(`"value1"`)},
		Extensions: map[string]json.RawMessage{"ext_key": json.RawMessage(`{"a":1}`)},
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleUser, Content: "hello"},
			}},
		},
	}
	ne := NormalizeEvent(evt)
	assert.Equal(t, "evt-1", ne.ID)
	assert.Len(t, ne.StateDelta, 1)
	assert.Equal(t, "key1", ne.StateDelta[0].Key)
	assert.Len(t, ne.Extensions, 1)
	assert.Equal(t, "ext_key", ne.Extensions[0].Key)
}

func TestNormalizeEvent_DeltaPath(t *testing.T) {
	evt := &event.Event{
		ID:        "evt-delta",
		Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletionChunk,
			Choices: []model.Choice{{
				Delta: model.Message{
					Role:    model.RoleAssistant,
					Content: "streamed chunk",
					ToolCalls: []model.ToolCall{{
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "search",
							Arguments: []byte(`{"q":"test"}`),
						},
					}},
				},
			}},
		},
	}
	ne := NormalizeEvent(evt)
	assert.Equal(t, "assistant", ne.Role)
	assert.Equal(t, "streamed chunk", ne.Content)
	assert.Equal(t, "search", ne.ToolCall)
}

func TestNormalizeEvent_NoChoices(t *testing.T) {
	evt := &event.Event{
		ID:        "evt-no-choice",
		Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
		},
	}
	ne := NormalizeEvent(evt)
	assert.Equal(t, "evt-no-choice", ne.ID)
	assert.Equal(t, model.ObjectTypeChatCompletion, ne.Object)
}

// ---------------------------------------------------------------------------
// NormalizeState tests
// ---------------------------------------------------------------------------

func TestNormalizeState_Empty(t *testing.T) {
	assert.Nil(t, NormalizeState(nil))
	assert.Nil(t, NormalizeState(session.StateMap{}))
}

// ---------------------------------------------------------------------------
// NormalizeMemoryEntry tests
// ---------------------------------------------------------------------------

func TestNormalizeMemoryEntry_Nil(t *testing.T) {
	assert.Equal(t, NormalizedMemory{}, NormalizeMemoryEntry(nil))
}

func TestNormalizeMemoryEntry_NilMemory(t *testing.T) {
	entry := &memory.Entry{
		ID:        "m1",
		AppName:   "app",
		UserID:    "user",
		UpdatedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
	}
	nm := NormalizeMemoryEntry(entry)
	assert.Equal(t, "m1", nm.ID)
	assert.Equal(t, "", nm.Content)
}

// ---------------------------------------------------------------------------
// NormalizeSummary tests
// ---------------------------------------------------------------------------

func TestNormalizeSummary_Nil(t *testing.T) {
	ns := NormalizeSummary("s1", "f1", nil)
	assert.Equal(t, "s1", ns.SessionID)
	assert.Equal(t, "f1", ns.FilterKey)
}

func TestNormalizeSummary_NoBoundary(t *testing.T) {
	sum := &session.Summary{
		Summary: "test summary",
	}
	ns := NormalizeSummary("s1", "fk", sum)
	assert.Equal(t, "test summary", ns.Summary)
	assert.Equal(t, "0", ns.Version)
	// UpdatedAt is zero, so CutoffBoundary() returns nil → no boundary string
	assert.Empty(t, ns.Boundary)
}

func TestNormalizeSummary_WithBoundary(t *testing.T) {
	boundary := session.NewSummaryBoundaryWithEventID("fk", time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC), "evt-last")
	sum := &session.Summary{
		Summary:   "test summary",
		UpdatedAt: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Boundary:  boundary,
	}
	ns := NormalizeSummary("s1", "fk", sum)
	assert.Equal(t, "test summary", ns.Summary)
	assert.NotEmpty(t, ns.Boundary)
	assert.Contains(t, ns.Boundary, "evt-last")
}

// ---------------------------------------------------------------------------
// normalizeBytes tests
// ---------------------------------------------------------------------------

func TestNormalizeBytes_Empty(t *testing.T) {
	assert.Equal(t, "", normalizeBytes(nil))
	assert.Equal(t, "", normalizeBytes([]byte{}))
}

func TestNormalizeBytes_PlainText(t *testing.T) {
	assert.Equal(t, "hello", normalizeBytes([]byte("hello")))
}

func TestNormalizeBytes_JSON(t *testing.T) {
	assert.Equal(t, `{"a":1}`, normalizeBytes([]byte(`{"a":1}`)))
}

func TestNormalizeBytes_Binary(t *testing.T) {
	result := normalizeBytes([]byte{0x00, 0x01, 0x02})
	assert.Contains(t, result, "hex:")
}

// ---------------------------------------------------------------------------
// canonicalizeRawJSON tests
// ---------------------------------------------------------------------------

func TestCanonicalizeRawJSON_Empty(t *testing.T) {
	assert.Equal(t, "", canonicalizeRawJSON(nil))
	assert.Equal(t, "", canonicalizeRawJSON([]byte{}))
}

func TestCanonicalizeRawJSON_Invalid(t *testing.T) {
	assert.Equal(t, "not-json", canonicalizeRawJSON([]byte("not-json")))
}

// ---------------------------------------------------------------------------
// isMostlyPrintable tests
// ---------------------------------------------------------------------------

func TestIsMostlyPrintable_NonPrintable(t *testing.T) {
	assert.False(t, isMostlyPrintable("hello\x00world"))
}

// ---------------------------------------------------------------------------
// normalizeMemoryMetadata tests
// ---------------------------------------------------------------------------

func TestNormalizeMemoryMetadata_Nil(t *testing.T) {
	assert.Equal(t, "", normalizeMemoryMetadata(nil))
}

// ---------------------------------------------------------------------------
// firstChoice / evtObject tests
// ---------------------------------------------------------------------------

func TestFirstChoice_NilEvent(t *testing.T) {
	view := firstChoice(nil)
	assert.Equal(t, "", view.object)
}

func TestFirstChoice_NilResponse(t *testing.T) {
	evt := &event.Event{}
	view := firstChoice(evt)
	assert.Equal(t, "", view.object)
}

func TestFirstChoice_NoChoices(t *testing.T) {
	evt := &event.Event{
		Response: &model.Response{Object: "test.object"},
	}
	view := firstChoice(evt)
	assert.Equal(t, "test.object", view.object)
}

// ---------------------------------------------------------------------------
// summaryBoundaryVersion tests
// ---------------------------------------------------------------------------

func TestSummaryBoundaryVersion_Nil(t *testing.T) {
	assert.Equal(t, 0, summaryBoundaryVersion(nil))
	assert.Equal(t, 0, summaryBoundaryVersion(&session.Summary{}))
}

// ---------------------------------------------------------------------------
// Compare snapshots edge case tests
// ---------------------------------------------------------------------------

func TestCompareEvents_MissingAndExtra(t *testing.T) {
	baseline := []NormalizedEvent{{Index: 0, ID: "a", Content: "hello"}}
	actual := []NormalizedEvent{}
	diffs := compareEvents("c1", "be", baseline, actual)
	require.Len(t, diffs, 1)
	assert.Contains(t, diffs[0].Path, "events[0]")
	assert.False(t, diffs[0].AllowedDiff)

	diffs2 := compareEvents("c1", "be", actual, baseline)
	require.Len(t, diffs2, 1)
	assert.Contains(t, diffs2[0].Path, "events[0]")
}

func TestCompareStates_MissingAndExtra(t *testing.T) {
	baseline := []NormalizedState{{Key: "k1", Value: "v1"}}
	actual := []NormalizedState{}
	diffs := compareStates("c1", "be", baseline, actual)
	require.Len(t, diffs, 1)
	assert.Contains(t, diffs[0].Path, "state[0]")

	diffs2 := compareStates("c1", "be", actual, baseline)
	require.Len(t, diffs2, 1)

	// Key mismatch (same value, only key diff is produced)
	baseline2 := []NormalizedState{{Key: "k1", Value: "v1"}}
	actual2 := []NormalizedState{{Key: "k2", Value: "v1"}}
	diffs3 := compareStates("c1", "be", baseline2, actual2)
	assert.Len(t, diffs3, 1) // key changed diff

	// Key same, value mismatch
	baseline3 := []NormalizedState{{Key: "k1", Value: "v1"}}
	actual3 := []NormalizedState{{Key: "k1", Value: "v2"}}
	diffs4 := compareStates("c1", "be", baseline3, actual3)
	assert.Len(t, diffs4, 1) // value changed diff
}

func TestCompareMemories_MissingAndExtra(t *testing.T) {
	baseline := []NormalizedMemory{{ID: "m1", Content: "c1"}}
	actual := []NormalizedMemory{}
	diffs := compareMemories("c1", "be", baseline, actual)
	require.Len(t, diffs, 1)
	assert.Contains(t, diffs[0].Path, "memories[0]")

	diffs2 := compareMemories("c1", "be", actual, baseline)
	require.Len(t, diffs2, 1)
}

func TestCompareSummaries_MissingAndExtra(t *testing.T) {
	baseline := []NormalizedSummary{{SessionID: "s1", FilterKey: "fk", Summary: "text"}}
	actual := []NormalizedSummary{}
	diffs := compareSummaries("c1", "be", baseline, actual)
	require.Len(t, diffs, 1)
	assert.Contains(t, diffs[0].Path, "summaries[0]")

	diffs2 := compareSummaries("c1", "be", actual, baseline)
	require.Len(t, diffs2, 1)
}

func TestCompareTracks_MissingAndExtra(t *testing.T) {
	baseline := []NormalizedTrack{{Track: "t1", Payload: "p1"}}
	actual := []NormalizedTrack{}
	diffs := compareTracks("c1", "be", baseline, actual)
	require.Len(t, diffs, 1)
	assert.Contains(t, diffs[0].Path, "tracks[0]")

	diffs2 := compareTracks("c1", "be", actual, baseline)
	require.Len(t, diffs2, 1)
}

func TestMaxLen(t *testing.T) {
	assert.Equal(t, 5, maxLen(5, 3))
	assert.Equal(t, 5, maxLen(3, 5))
	assert.Equal(t, 3, maxLen(3, 3))
}

// ---------------------------------------------------------------------------
// compareKVList tests
// ---------------------------------------------------------------------------

func TestCompareKVList_Direct(t *testing.T) {
	var diffs []Diff
	baseline := []NormalizedKV{{Key: "k1", Value: "v1"}}
	actual := []NormalizedKV{{Key: "k2", Value: "v2"}}
	compareKVList("c1", "be", "path", baseline, actual, &diffs, false, "explain")
	assert.Len(t, diffs, 2)

	// Extra in actual
	diffs = nil
	compareKVList("c1", "be", "path", nil, baseline, &diffs, false, "explain")
	assert.Len(t, diffs, 1)

	// Missing from actual
	diffs = nil
	compareKVList("c1", "be", "path", baseline, nil, &diffs, false, "explain")
	assert.Len(t, diffs, 1)
}

// ---------------------------------------------------------------------------
// diffMatchesPattern tests
// ---------------------------------------------------------------------------

func TestDiffMatchesPattern(t *testing.T) {
	assert.False(t, diffMatchesPattern("", ""))

	assert.True(t, diffMatchesPattern("events[0].id", "events[0].id"))

	assert.True(t, diffMatchesPattern("events[0].id", "events*"))
	assert.True(t, diffMatchesPattern("events[0].id", "events[*"))

	assert.False(t, diffMatchesPattern("events[0].id", "memories*"))

	assert.True(t, diffMatchesPattern("events[0].id", "events"))
}

// ---------------------------------------------------------------------------
// applyAllowedDiffPatterns tests
// ---------------------------------------------------------------------------

func TestApplyAllowedDiffPatterns(t *testing.T) {
	diffs := []Diff{
		newDiff("c1", "be", "events[0].id", "a", "b", false, ""),
		newDiff("c1", "be", "state[0].key", "x", "y", false, ""),
	}
	result := applyAllowedDiffPatterns(diffs, nil)
	assert.False(t, result[0].AllowedDiff)

	result = applyAllowedDiffPatterns(diffs, []string{"events*"})
	assert.True(t, result[0].AllowedDiff)
	assert.False(t, result[1].AllowedDiff)

	// Empty diffs
	assert.Equal(t, diffs, applyAllowedDiffPatterns(diffs, []string{}))
}

// ---------------------------------------------------------------------------
// collapseUnsupportedFeatureDiffs tests
// ---------------------------------------------------------------------------

func TestCollapseUnsupportedFeatureDiffs_NoTrack(t *testing.T) {
	b := &replayBackend{trackSupport: false}
	diffs := []Diff{
		newDiff("c1", "be", "events[0].id", "a", "b", false, ""),
		newDiff("c1", "be", "tracks[0].payload", "p1", "p2", false, ""),
	}
	result := collapseUnsupportedFeatureDiffs(ReplayCase{Name: "c1"}, b,
		NormalizedSnapshot{Tracks: []NormalizedTrack{{Track: "t1"}}},
		NormalizedSnapshot{Tracks: []NormalizedTrack{{Track: "t2"}}},
		diffs)
	// tracks diff should be filtered, one allowed diff for track feature added
	require.Len(t, result, 2)
	assert.Equal(t, "events[0].id", result[0].Path)
	assert.True(t, result[1].AllowedDiff)
}

func TestCollapseUnsupportedFeatureDiffs_SupportsTrack(t *testing.T) {
	b := &replayBackend{trackSupport: true}
	diffs := []Diff{newDiff("c1", "be", "events[0].id", "a", "b", false, "")}
	result := collapseUnsupportedFeatureDiffs(ReplayCase{Name: "c1"}, b,
		NormalizedSnapshot{}, NormalizedSnapshot{}, diffs)
	assert.Equal(t, diffs, result)
}

func TestCollapseUnsupportedFeatureDiffs_NoTracks(t *testing.T) {
	b := &replayBackend{trackSupport: false}
	diffs := []Diff{newDiff("c1", "be", "events[0].id", "a", "b", false, "")}
	result := collapseUnsupportedFeatureDiffs(ReplayCase{Name: "c1"}, b,
		NormalizedSnapshot{}, NormalizedSnapshot{}, diffs)
	assert.Equal(t, diffs, result)
}

// ---------------------------------------------------------------------------
// normalizeMap / normalizeRawMap tests
// ---------------------------------------------------------------------------

func TestNormalizeMap_Empty(t *testing.T) {
	assert.Nil(t, normalizeMap(nil))
	assert.Nil(t, normalizeMap(map[string][]byte{}))
}

func TestNormalizeRawMap_Empty(t *testing.T) {
	assert.Nil(t, normalizeRawMap(nil))
	assert.Nil(t, normalizeRawMap(map[string]json.RawMessage{}))
}

// ---------------------------------------------------------------------------
// NormalizeSnapshot full tests
// ---------------------------------------------------------------------------

func TestNormalizeSnapshot_Full(t *testing.T) {
	snapshot := NormalizedSnapshot{
		Events:    []NormalizedEvent{{Index: 2}, {Index: 1}},
		State:     []NormalizedState{{Key: "b"}, {Key: "a"}},
		Memories:  []NormalizedMemory{{ID: "m2"}, {ID: "m1"}},
		Summaries: []NormalizedSummary{{SessionID: "s2", FilterKey: "a"}, {SessionID: "s1", FilterKey: "b"}, {SessionID: "s1", FilterKey: "a"}},
		Tracks:    []NormalizedTrack{{Track: "t2", Timestamp: "2"}, {Track: "t1", Timestamp: "1"}, {Track: "t1", Timestamp: "0"}},
	}
	normalized := NormalizeSnapshot(snapshot)
	assert.Equal(t, 1, normalized.Events[0].Index)
	assert.Equal(t, "a", normalized.State[0].Key)
	assert.Equal(t, "m1", normalized.Memories[0].ID)
	assert.Equal(t, "s1", normalized.Summaries[0].SessionID)
	assert.Equal(t, "a", normalized.Summaries[0].FilterKey)
	assert.Equal(t, "t1", normalized.Tracks[0].Track)
	assert.Equal(t, "0", normalized.Tracks[0].Timestamp)
}

// ---------------------------------------------------------------------------
// CompareSnapshots full tests
// ---------------------------------------------------------------------------

func TestCompareSnapshots_Full(t *testing.T) {
	baseline := NormalizedSnapshot{
		Events:    []NormalizedEvent{{Index: 0, ID: "e1", Content: "hello"}, {Index: 1, ID: "e2", Content: "world"}},
		State:     []NormalizedState{{Key: "k1", Value: "v1"}},
		Memories:  []NormalizedMemory{{ID: "m1", Content: "c1"}},
		Summaries: []NormalizedSummary{{SessionID: "s1", FilterKey: "fk", Summary: "text"}},
		Tracks:    []NormalizedTrack{{Track: "t1", Payload: "p1"}},
	}
	actual := NormalizedSnapshot{
		Events:    []NormalizedEvent{{Index: 0, ID: "e3", Content: "hello world"}, {Index: 1, ID: "e4", Content: "world v2"}},
		State:     []NormalizedState{{Key: "k1", Value: "v2"}},
		Memories:  []NormalizedMemory{{ID: "m1", Content: "c2"}},
		Summaries: []NormalizedSummary{{SessionID: "s1", FilterKey: "fk", Summary: "text2"}},
		Tracks:    []NormalizedTrack{{Track: "t1", Payload: "p2"}},
	}
	diffs := CompareSnapshots("c1", "be", baseline, actual)
	assert.NotEmpty(t, diffs)
}

// ---------------------------------------------------------------------------
// Harness helper function tests
// ---------------------------------------------------------------------------

func TestReplayHarness_Run_WithDefaultBackends(t *testing.T) {
	harness := ReplayHarness{
		Options: HarnessOptions{LightMode: true, MaxCases: 1},
	}
	report, err := harness.Run(contextBackground())
	require.NoError(t, err)
	assert.Equal(t, 1, report.Summary.CasesRun)
}

func TestReplayCaseKey(t *testing.T) {
	assert.Equal(t, "test-case-with-multiple-separators", replayCaseKey("Test/Case:With_Multiple-Separators"))
}

func TestReplaySessionForCase(t *testing.T) {
	sid := replaySessionForCase("test-case")
	assert.Contains(t, sid, replaySessionID)
	assert.Contains(t, sid, "test-case")
}

func TestReplayUserForCase(t *testing.T) {
	uid := replayUserForCase("test-case")
	assert.Contains(t, uid, replayBaseUser)
	assert.Contains(t, uid, "test-case")
}

func TestCloseBackends(t *testing.T) {
	b1, _ := newInMemoryReplayBackend()
	b2, _ := newInMemoryReplayBackend()
	closeBackends([]Backend{b1, b2})
}

func TestFindBackend(t *testing.T) {
	b1, _ := newInMemoryReplayBackend()
	defer b1.Close()
	backends := []Backend{b1}
	assert.NotNil(t, findBackend(backends, "inmemory"))
	assert.Nil(t, findBackend(backends, "nonexistent"))
}

// ---------------------------------------------------------------------------
// Sample helper tests
// ---------------------------------------------------------------------------

func TestSampleSummary(t *testing.T) {
	sum := sampleSummary("sess", "fk", "text")
	assert.Equal(t, "text", sum.Summary)
	assert.NotNil(t, sum.Boundary)
}

func TestSortStrings(t *testing.T) {
	vals := []string{"c", "a", "b"}
	sortStrings(vals)
	assert.Equal(t, []string{"a", "b", "c"}, vals)
}

// ---------------------------------------------------------------------------
// openTempSQLiteDB tests
// ---------------------------------------------------------------------------

func TestOpenTempSQLiteDB_Success(t *testing.T) {
	db, cleanup, err := openTempSQLiteDB("replay-test-*.db")
	require.NoError(t, err)
	assert.NotNil(t, db)
	assert.NotNil(t, cleanup)
	assert.NoError(t, cleanup())
}

// ---------------------------------------------------------------------------
// State scope integration tests
// ---------------------------------------------------------------------------

func TestStateOperations_AllScopes(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	cases := []ReplayCase{
		{
			Name: "state_app_scope",
			Operations: []Operation{
				{Kind: OperationKindUpdateState, Scope: StateScopeApp, StatePatch: session.StateMap{"app-v1": []byte("x")}},
				{Kind: OperationKindDeleteState, Scope: StateScopeApp, StateDelete: []string{"app-v1"}},
			},
		},
		{
			Name: "state_user_scope",
			Operations: []Operation{
				{Kind: OperationKindUpdateState, Scope: StateScopeUser, StatePatch: session.StateMap{"user-v1": []byte("y")}},
				{Kind: OperationKindDeleteState, Scope: StateScopeUser, StateDelete: []string{"user-v1"}},
			},
		},
		{
			Name: "state_session_clear_implicit",
			Operations: []Operation{
				{Kind: OperationKindUpdateState, StatePatch: session.StateMap{"k1": []byte("v1")}},
				{Kind: OperationKindClearState},
			},
		},
		{
			Name: "state_app_clear_implicit",
			Operations: []Operation{
				{Kind: OperationKindUpdateState, Scope: StateScopeApp, StatePatch: session.StateMap{"app-k1": []byte("v1")}},
				{Kind: OperationKindClearState, Scope: StateScopeApp},
			},
		},
		{
			Name: "state_user_clear_implicit",
			Operations: []Operation{
				{Kind: OperationKindUpdateState, Scope: StateScopeUser, StatePatch: session.StateMap{"user-k1": []byte("v1")}},
				{Kind: OperationKindClearState, Scope: StateScopeUser},
			},
		},
		{
			Name: "memory_delete_flow",
			Operations: []Operation{
				{Kind: OperationKindAddMemory, MemoryAdd: sampleMemoryWrite("user-m", "mem-del", "content", []string{"test"})},
				{Kind: OperationKindDeleteMemory, MemoryDelete: &memory.Key{AppName: "app-a", UserID: "user-m"}},
			},
		},
	}

	harness := ReplayHarness{
		Backends: []Backend{backend},
		Cases:    cases,
	}
	report, err := harness.Run(contextBackground())
	require.NoError(t, err)
	for _, result := range report.Results {
		assert.Empty(t, result.Error, "unexpected error in case %s", result.CaseName)
	}
}

// ---------------------------------------------------------------------------
// Track unsupported backend test
// ---------------------------------------------------------------------------

type noTrackBackend struct {
	*replayBackend
}

func (b *noTrackBackend) Supports(feature string) bool {
	if feature == "track" {
		return false
	}
	return b.replayBackend.Supports(feature)
}

func (b *noTrackBackend) SessionService() session.Service {
	return b.replayBackend.sessionSvc
}

func (b *noTrackBackend) MemoryService() memory.Service {
	return b.replayBackend.memorySvc
}

func TestTrackUnsupportedBackend(t *testing.T) {
	inner, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer inner.Close()

	rb, ok := inner.(*replayBackend)
	require.True(t, ok)

	backend := &noTrackBackend{replayBackend: rb}

	cases := []ReplayCase{{
		Name: "track_unsupported",
		Operations: []Operation{
			{Kind: OperationKindAppendTrackEvent, Track: sampleTrackEvent("evt-1", `{"x":1}`)},
		},
	}}

	harness := ReplayHarness{
		Backends: []Backend{backend},
		Cases:    cases,
	}
	report, err := harness.Run(contextBackground())
	require.NoError(t, err)
	require.NotEmpty(t, report.Results)
	// Should produce a diff about unsupported tracks.
	assert.Equal(t, 1, report.Summary.CasesRun)
}

// ---------------------------------------------------------------------------
// Summarize with state test
// ---------------------------------------------------------------------------

func TestSummarizeWithState(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	cases := []ReplayCase{{
		Name: "summary_with_state",
		Operations: []Operation{
			{Kind: OperationKindUpdateState, StatePatch: session.StateMap{"k": []byte("v")}},
			{Kind: OperationKindAppendEvent, Event: sampleUserEvent("evt-s", "hello", "session-a", "user-a", "app-a")},
			{Kind: OperationKindCreateSummary, FilterKey: ""},
		},
	}}

	harness := ReplayHarness{
		Backends: []Backend{backend},
		Cases:    cases,
	}
	report, err := harness.Run(contextBackground())
	require.NoError(t, err)
	for _, result := range report.Results {
		assert.Empty(t, result.Error, "unexpected error: %s", result.CaseName)
	}
	assert.Equal(t, 1, report.Summary.CasesRun)
}

// ---------------------------------------------------------------------------
// Close with services test
// ---------------------------------------------------------------------------

func TestReplayBackend_Close_WithServices(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	assert.NotNil(t, backend)
	// Close should be a no-op after already closed, but Close is idempotent.
	assert.NoError(t, backend.Close())
}

// ---------------------------------------------------------------------------
// backendSessionService / backendMemoryService interface paths
// ---------------------------------------------------------------------------

type testSessionProvider struct {
	*replayBackend
}

func (b *testSessionProvider) SessionService() session.Service {
	return b.replayBackend.sessionSvc
}

func (b *testSessionProvider) MemoryService() memory.Service {
	return b.replayBackend.memorySvc
}

func TestBackendServiceProviders(t *testing.T) {
	inner, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer inner.Close()

	rb, ok := inner.(*replayBackend)
	require.True(t, ok)

	provider := &testSessionProvider{replayBackend: rb}

	svc := backendSessionService(provider)
	assert.NotNil(t, svc)

	memSvc := backendMemoryService(provider)
	assert.NotNil(t, memSvc)
}

type dummyBackend struct{}

func (d *dummyBackend) Name() string         { return "dummy" }
func (d *dummyBackend) Kind() BackendKind    { return BackendKindSession }
func (d *dummyBackend) Supports(string) bool { return false }
func (d *dummyBackend) Close() error         { return nil }

func TestBackendSessionService_NilFallback(t *testing.T) {
	// When the backend doesn't implement the interface AND is not *replayBackend
	dummy := &dummyBackend{}
	assert.Nil(t, backendSessionService(dummy))
	assert.Nil(t, backendMemoryService(dummy))
}

func TestCreateReplaySession_ErrorPath(t *testing.T) {
	// createReplaySession with a backend that has no session service
	_, err := createReplaySession(context.Background(), &replayBackend{}, session.Key{})
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// applyStateUpdate with scope paths
// ---------------------------------------------------------------------------

func TestApplyStateUpdate_UnknownScope(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	err = applyStateUpdate(context.Background(), backend,
		session.Key{AppName: "app", UserID: "user", SessionID: "sess"},
		session.UserKey{AppName: "app", UserID: "user"},
		Operation{Kind: OperationKindUpdateState, Scope: "unknown", StatePatch: session.StateMap{"k": []byte("v")}},
		nil)
	assert.Error(t, err)
}

func TestApplyStateDelete_UnknownScope(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	err = applyStateDelete(context.Background(), backend,
		session.Key{AppName: "app", UserID: "user", SessionID: "sess"},
		session.UserKey{AppName: "app", UserID: "user"},
		Operation{Kind: OperationKindDeleteState, Scope: "unknown", StateDelete: []string{"k"}},
		nil)
	assert.Error(t, err)
}

func TestApplyStateClear_UnknownScope(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	err = applyStateClear(context.Background(), backend,
		session.Key{AppName: "app", UserID: "user", SessionID: "sess"},
		session.UserKey{AppName: "app", UserID: "user"},
		Operation{Kind: OperationKindClearState, Scope: "unknown"},
		nil)
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// More compare edge cases
// ---------------------------------------------------------------------------

func TestCompareEvents_DifferentFields(t *testing.T) {
	baseline := []NormalizedEvent{{Index: 0, ID: "e1", Author: "user", Role: "user", Content: "hello"}}
	actual := []NormalizedEvent{{Index: 0, ID: "e2", Author: "assistant", Role: "assistant", Content: "goodbye"}}
	diffs := compareEvents("c1", "be", baseline, actual)
	assert.NotEmpty(t, diffs)
	// id changed (allowed), author changed, role changed, content changed
	paths := make(map[string]bool, len(diffs))
	for _, d := range diffs {
		paths[d.Path] = d.AllowedDiff
	}
	assert.True(t, paths["events[0].id"], "id diff should be allowed")
	assert.False(t, paths["events[0].author"])
	assert.False(t, paths["events[0].role"])
	assert.False(t, paths["events[0].content"])
}

func TestCompareMemories_FieldDiffs(t *testing.T) {
	baseline := []NormalizedMemory{{ID: "m1", Content: "c1", Score: "1.0000", Metadata: "{}"}}
	actual := []NormalizedMemory{{ID: "m2", Content: "c2", Score: "2.0000", Metadata: `{"kind":""}`}}
	diffs := compareMemories("c1", "be", baseline, actual)
	paths := make(map[string]bool, len(diffs))
	for _, d := range diffs {
		paths[d.Path] = d.AllowedDiff
	}
	assert.False(t, paths["memories[0].id"])
	assert.False(t, paths["memories[0].content"])
	assert.True(t, paths["memories[0].score"])
	assert.True(t, paths["memories[0].metadata"])
}

func TestCompareSummaries_FieldDiffs(t *testing.T) {
	baseline := []NormalizedSummary{{SessionID: "s1", FilterKey: "fk1", Summary: "t1", Version: "1"}}
	actual := []NormalizedSummary{{SessionID: "s2", FilterKey: "fk2", Summary: "t2", Version: "2"}}
	diffs := compareSummaries("c1", "be", baseline, actual)
	paths := make(map[string]bool, len(diffs))
	for _, d := range diffs {
		paths[d.Path] = d.AllowedDiff
	}
	assert.False(t, paths["summaries[0].session_id"])
	assert.False(t, paths["summaries[0].filter_key"])
	assert.False(t, paths["summaries[0].summary"])
	assert.False(t, paths["summaries[0].version"])
}

func TestCompareTracks_FieldDiffs(t *testing.T) {
	baseline := []NormalizedTrack{{Track: "t1", Payload: "p1", Type: "t"}}
	actual := []NormalizedTrack{{Track: "t2", Payload: "p2", Type: "u"}}
	diffs := compareTracks("c1", "be", baseline, actual)
	paths := make(map[string]bool, len(diffs))
	for _, d := range diffs {
		paths[d.Path] = d.AllowedDiff
	}
	assert.False(t, paths["tracks[0].track"])
	assert.False(t, paths["tracks[0].payload"])
	assert.True(t, paths["tracks[0].type"])
}

// ---------------------------------------------------------------------------
// Diff struct test
// ---------------------------------------------------------------------------

func TestDiffStruct(t *testing.T) {
	d := newDiff("case", "backend", "path", "base", "actual", true, "explanation")
	assert.Equal(t, "case", d.CaseName)
	assert.Equal(t, "backend", d.Backend)
	assert.Equal(t, "path", d.Path)
	assert.Equal(t, "base", d.Baseline)
	assert.Equal(t, "actual", d.Actual)
	assert.True(t, d.AllowedDiff)
	assert.Equal(t, "explanation", d.Explanation)
}

// ---------------------------------------------------------------------------
// StableFloat test
// ---------------------------------------------------------------------------

func TestStableFloat(t *testing.T) {
	assert.Equal(t, "1.5000", stableFloat(1.5))
	assert.Equal(t, "0.0000", stableFloat(0))
}

// ---------------------------------------------------------------------------
// canonicizeRawJSON marshal error test (edge case)
// ---------------------------------------------------------------------------

func TestCanonicalizeRawJSON_MarshalError(t *testing.T) {
	// NaN and Inf can't be marshaled with json.Marshal, triggering the error path.
	result := canonicalizeRawJSON([]byte(`{"v":NaN}`))
	assert.NotEmpty(t, result)
}

// ---------------------------------------------------------------------------
// Run baseline backend selection
// ---------------------------------------------------------------------------

func TestRun_CustomBaseline(t *testing.T) {
	b1, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer b1.Close()

	b2, err := newSQLiteReplayBackend()
	require.NoError(t, err)
	defer b2.Close()

	harness := ReplayHarness{
		Backends: []Backend{b1, b2},
		Options:  HarnessOptions{BaselineBackend: "sqlite", MaxCases: 1},
	}
	report, err := harness.Run(contextBackground())
	require.NoError(t, err)
	assert.Equal(t, 1, report.Summary.CasesRun)
}

func TestRun_BaselineNotFound(t *testing.T) {
	b1, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer b1.Close()

	harness := ReplayHarness{
		Backends: []Backend{b1},
		Options:  HarnessOptions{BaselineBackend: "nonexistent"},
	}
	_, err = harness.Run(contextBackground())
	assert.Error(t, err)
}

// ---------------------------------------------------------------------------
// Apply state empty patch test
// ---------------------------------------------------------------------------

func TestApplyStateUpdate_EmptyPatch(t *testing.T) {
	err := applyStateUpdate(context.Background(), nil, session.Key{}, session.UserKey{}, Operation{}, nil)
	assert.NoError(t, err)
}

func TestApplyStateDelete_EmptyDelete(t *testing.T) {
	err := applyStateDelete(context.Background(), nil, session.Key{}, session.UserKey{}, Operation{}, nil)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Summarize with state (covers the stateSize assignment branch)
// ---------------------------------------------------------------------------

func TestSummarize_DirectWithState(t *testing.T) {
	s := replaySummarizer{}
	sess := &session.Session{ID: "s1", Events: []event.Event{{}}}
	sess.State = session.StateMap{"key1": []byte("val1")}
	summary, err := s.Summarize(context.Background(), sess)
	require.NoError(t, err)
	assert.Contains(t, summary, "state=1")
}

// ---------------------------------------------------------------------------
// normalizeMemoryMetadata with EventTime
// ---------------------------------------------------------------------------

func TestNormalizeMemoryMetadata_WithEventTime(t *testing.T) {
	et := time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)
	mem := &memory.Memory{
		Memory:       "content",
		Topics:       []string{"a"},
		EventTime:    &et,
		Participants: []string{"p1", "p2"},
		Location:     "loc",
	}
	result := normalizeMemoryMetadata(mem)
	assert.Contains(t, result, "event_time")
	assert.Contains(t, result, "2026-06-15T12:00:00Z")
}

// ---------------------------------------------------------------------------
// Env-based backend constructor error paths
// These tests cover the early error returns when env vars are set but
// required connection parameters are missing.
// ---------------------------------------------------------------------------

func TestNewRedisReplayBackend_MissingAddr(t *testing.T) {
	t.Setenv(replayEnableRedis, "true")
	_, err := newRedisReplayBackend()
	assert.ErrorContains(t, err, "REDIS_ADDR")
}

func TestNewPostgresReplayBackend_MissingHost(t *testing.T) {
	t.Setenv(replayEnablePostgres, "true")
	_, err := newPostgresReplayBackend()
	assert.ErrorContains(t, err, "PG_HOST")
}

func TestNewMySQLReplayBackend_MissingHost(t *testing.T) {
	t.Setenv(replayEnableMySQL, "true")
	_, err := newMySQLReplayBackend()
	assert.ErrorContains(t, err, "MYSQL_HOST")
}

func TestNewClickHouseReplayBackend_MissingHost(t *testing.T) {
	t.Setenv(replayEnableClickHouse, "true")
	_, err := newClickHouseReplayBackend()
	assert.ErrorContains(t, err, "CLICKHOUSE_HOST")
}

// ---------------------------------------------------------------------------
// newDefaultReplayBackends with env-based backend enabled (error path)
// ---------------------------------------------------------------------------

func TestNewDefaultReplayBackends_RedisEnabledNoAddr(t *testing.T) {
	t.Setenv(replayEnableRedis, "true")
	_, err := newDefaultReplayBackends(HarnessOptions{})
	assert.ErrorContains(t, err, "REDIS_ADDR")
}

func TestNewDefaultReplayBackends_PostgresEnabledNoHost(t *testing.T) {
	t.Setenv(replayEnablePostgres, "true")
	_, err := newDefaultReplayBackends(HarnessOptions{})
	assert.ErrorContains(t, err, "PG_HOST")
}

func TestNewDefaultReplayBackends_MySQLEnabledNoHost(t *testing.T) {
	t.Setenv(replayEnableMySQL, "true")
	_, err := newDefaultReplayBackends(HarnessOptions{})
	assert.ErrorContains(t, err, "MYSQL_HOST")
}

func TestNewDefaultReplayBackends_ClickHouseEnabledNoHost(t *testing.T) {
	t.Setenv(replayEnableClickHouse, "true")
	_, err := newDefaultReplayBackends(HarnessOptions{})
	assert.ErrorContains(t, err, "CLICKHOUSE_HOST")
}

// ---------------------------------------------------------------------------
// applyStateClear with explicit delete for app/user scope
// ---------------------------------------------------------------------------

func TestApplyStateClear_AppScopeExplicit(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	err = applyStateClear(context.Background(), backend,
		session.Key{AppName: "app", UserID: "user", SessionID: "sess"},
		session.UserKey{AppName: "app", UserID: "user"},
		Operation{Kind: OperationKindClearState, Scope: StateScopeApp, StateDelete: []string{"app-k"}},
		nil)
	assert.NoError(t, err)
}

func TestApplyStateClear_UserScopeExplicit(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	err = applyStateClear(context.Background(), backend,
		session.Key{AppName: "app", UserID: "user", SessionID: "sess"},
		session.UserKey{AppName: "app", UserID: "user"},
		Operation{Kind: OperationKindClearState, Scope: StateScopeUser, StateDelete: []string{"user-k"}},
		nil)
	assert.NoError(t, err)
}

// ---------------------------------------------------------------------------
// Compare events with tool_call field
// ---------------------------------------------------------------------------

func TestCompareEvents_WithToolCall(t *testing.T) {
	baseline := []NormalizedEvent{{Index: 0, ToolCall: "search", ToolName: "s", ToolID: "t1"}}
	actual := []NormalizedEvent{{Index: 0, ToolCall: "lookup", ToolName: "l", ToolID: "t2"}}
	diffs := compareEvents("c1", "be", baseline, actual)
	paths := make(map[string]bool, len(diffs))
	for _, d := range diffs {
		paths[d.Path] = d.AllowedDiff
	}
	assert.False(t, paths["events[0].tool_call"])
	assert.False(t, paths["events[0].tool_name"])
	assert.True(t, paths["events[0].tool_id"])
}

// ---------------------------------------------------------------------------
// runReplayCase with nil event / nil memory / nil track operations
// ---------------------------------------------------------------------------

func TestRunReplayCase_NilOperationPayloads(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	cases := []ReplayCase{{
		Name: "nil_ops",
		Operations: []Operation{
			{Kind: OperationKindAppendEvent},
			{Kind: OperationKindAddMemory},
			{Kind: OperationKindUpdateMemory},
			{Kind: OperationKindDeleteMemory},
			{Kind: OperationKindAppendTrackEvent},
		},
	}}

	harness := ReplayHarness{
		Backends: []Backend{backend},
		Cases:    cases,
	}
	report, err := harness.Run(contextBackground())
	require.NoError(t, err)
	for _, result := range report.Results {
		assert.Empty(t, result.Error, "unexpected error: %s", result.CaseName)
	}
}

// ---------------------------------------------------------------------------
// Compare events with branch/tag/filter_key changes
// ---------------------------------------------------------------------------

func TestCompareEvents_AllFields(t *testing.T) {
	baseline := []NormalizedEvent{{Index: 0, Branch: "main", Tag: "v1", FilterKey: "app-a", Timestamp: "ts1", Object: "obj1"}}
	actual := []NormalizedEvent{{Index: 0, Branch: "dev", Tag: "v2", FilterKey: "app-b", Timestamp: "ts2", Object: "obj2"}}
	diffs := compareEvents("c1", "be", baseline, actual)
	paths := make(map[string]bool, len(diffs))
	for _, d := range diffs {
		paths[d.Path] = d.AllowedDiff
	}
	assert.False(t, paths["events[0].branch"])
	assert.False(t, paths["events[0].tag"])
	assert.False(t, paths["events[0].filter_key"])
	// timestamp is allowed (true)
	assert.True(t, paths["events[0].timestamp"])
	assert.False(t, paths["events[0].object"])
}

// ---------------------------------------------------------------------------
// ReplayHarness Run with MaxCases=0 (no limit)
// ---------------------------------------------------------------------------

func TestReplayHarness_Run_MaxCasesZero(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	harness := ReplayHarness{
		Backends: []Backend{backend},
		Options:  HarnessOptions{MaxCases: 0},
	}
	report, err := harness.Run(contextBackground())
	require.NoError(t, err)
	assert.Equal(t, 10, report.Summary.CasesRun)
}

// ---------------------------------------------------------------------------
// Targeted tests for remaining uncovered statements
// ---------------------------------------------------------------------------

func TestNormalizeEvent_ToolNameAndToolID(t *testing.T) {
	evt := &event.Event{
		ID:        "evt-1",
		Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					Content:  "result",
					ToolName: "my-tool",
					ToolID:   "call-123",
				},
			}},
		},
	}
	ne := NormalizeEvent(evt)
	assert.Equal(t, "my-tool", ne.ToolName)
	assert.Equal(t, "call-123", ne.ToolID)
	assert.Equal(t, "result", ne.Content)
}

func TestNormalizeEvent_ToolCallInMessage(t *testing.T) {
	evt := &event.Event{
		ID:        "evt-2",
		Timestamp: time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC),
		Response: &model.Response{
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "I'll search for that",
					ToolCalls: []model.ToolCall{{
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "search",
							Arguments: []byte(`{"q":"test"}`),
						},
					}},
				},
			}},
		},
	}
	ne := NormalizeEvent(evt)
	assert.Equal(t, "search", ne.ToolCall)
	assert.Equal(t, "I'll search for that", ne.Content)
}

func TestRun_DefaultBackendsError(t *testing.T) {
	t.Setenv(replayEnableRedis, "true")
	harness := ReplayHarness{}
	_, err := harness.Run(contextBackground())
	assert.ErrorContains(t, err, "REDIS_ADDR")
}

func TestRun_ContextCancelled(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	harness := ReplayHarness{
		Backends: []Backend{backend},
		Options:  HarnessOptions{MaxCases: 1},
	}
	_, err = harness.Run(ctx)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestRun_BaselineCaseError(t *testing.T) {
	harness := ReplayHarness{
		Backends: []Backend{&dummyBackend{}},
		Cases:    []ReplayCase{{Name: "test"}},
		Options:  HarnessOptions{BaselineBackend: "dummy"},
	}
	_, err := harness.Run(context.Background())
	assert.ErrorContains(t, err, "nil")
}

// ---------------------------------------------------------------------------
// DeleteMemory with explicit MemoryID (no prior AddMemory)
// Covers the path where lastMemoryID is empty and the MemoryID comes
// from op.MemoryDelete.MemoryID (harness.go delete memory key construction).
// ---------------------------------------------------------------------------

func TestDeleteMemory_ExplicitID_NoPriorAdd(t *testing.T) {
	backend, err := newInMemoryReplayBackend()
	require.NoError(t, err)
	defer backend.Close()

	_, err = runReplayCase(context.Background(), ReplayCase{
		Name: "delete_explicit_id",
		Operations: []Operation{
			{Kind: OperationKindDeleteMemory, MemoryDelete: &memory.Key{MemoryID: "explicit-mem-id"}},
		},
	}, backend)
	// The delete will fail because the user doesn't exist, but the code path
	// (harness.go line 153) is exercised regardless.
	assert.Error(t, err)
}
