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
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	memoryinmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

// standardBackends returns the available backends for consistency
// comparison. Currently only InMemory is available in the module;
// additional backends (SQLite, Redis, Postgres, MySQL, ClickHouse)
// can be added when their packages are enabled.
func standardBackends(t *testing.T) []Backend {
	t.Helper()

	inmemSess := sessioninmemory.NewSessionService()
	inmemMem := memoryinmemory.NewMemoryService()

	return []Backend{
		{
			Name:           "inmemory",
			SessionService: inmemSess,
			MemoryService:  inmemMem,
			Setup: func(ctx context.Context) error {
				return nil
			},
			Teardown: func(ctx context.Context) error {
				inmemSess.Close()
				return inmemMem.Close()
			},
		},
		{
			// second InMemory instance to verify cross-backend
			// comparison infrastructure works end-to-end.
			Name:           "inmemory_2",
			SessionService: sessioninmemory.NewSessionService(),
			MemoryService:  memoryinmemory.NewMemoryService(),
			Setup: func(ctx context.Context) error {
				return nil
			},
			Teardown: func(ctx context.Context) error {
				return nil
			},
		},
	}
}

// replayCases returns all 10 replay cases defined by the acceptance
// criteria.
func replayCases() []ReplayCase {
	return []ReplayCase{
		case1SingleTurn(),
		case2MultiTurn(),
		case3ToolCall(),
		case4StateUpdates(),
		case5MemoryRW(),
		case6SummaryUpdate(),
		case7SummaryTruncation(),
		case8TrackEvents(),
		case9OutOfOrderWrites(),
		case10ErrorRecovery(),
	}
}

// --- Case 1: Single-turn dialogue ---
func case1SingleTurn() ReplayCase {
	return ReplayCase{
		Name:      "single_turn_text",
		AppName:   "test-app",
		UserID:    "user-1",
		SessionID: "session-001",
		InitialState: map[string]string{
			"app:welcome": "true",
		},
		Events: []EventSpec{
			{Author: "user", Role: "user", Content: "Hello, who are you?"},
			{Author: "assistant", Role: "assistant", Content: "I am an AI assistant."},
		},
		MemoryWrites: []MemoryWriteSpec{
			{Memory: "User greeted the assistant", Topics: []string{"conversation"}},
		},
		MemoryQueries: []MemoryQuerySpec{
			{Query: "greeting", Limit: 5},
		},
	}
}

// --- Case 2: Multi-turn dialogue ---
func case2MultiTurn() ReplayCase {
	return ReplayCase{
		Name:      "multi_turn_state_updates",
		AppName:   "test-app",
		UserID:    "user-1",
		SessionID: "session-002",
		Events: []EventSpec{
			{Author: "user", Role: "user", Content: "What is my name?"},
			{Author: "assistant", Role: "assistant", Content: "Your name is Bob."},
			{Author: "user", Role: "user", Content: "Remember that I like coffee."},
			{Author: "assistant", Role: "assistant", Content: "I will remember that you like coffee."},
			{Author: "user", Role: "user", Content: "What did I ask you to remember?"},
			{Author: "assistant", Role: "assistant", Content: "You asked me to remember that you like coffee."},
		},
		MemoryWrites: []MemoryWriteSpec{
			{Memory: "User name is Bob", Topics: []string{"identity"}},
			{Memory: "User likes coffee", Topics: []string{"preferences"}},
		},
		MemoryQueries: []MemoryQuerySpec{
			{Query: "Bob", Limit: 5},
			{Query: "coffee", Limit: 5},
		},
	}
}

// --- Case 3: Tool-call dialogue ---
func case3ToolCall() ReplayCase {
	return ReplayCase{
		Name:      "tool_call_roundtrip",
		AppName:   "test-app",
		UserID:    "user-1",
		SessionID: "session-003",
		Events: []EventSpec{
			{Author: "user", Role: "user", Content: "What is the weather?"},
			{
				Author: "assistant", Role: "assistant",
				ToolCalls: []ToolCallSpec{
					{ID: "call-1", Name: "get_weather", Arguments: `{"city":"Beijing"}`},
				},
			},
			{
				Author: "tool", Role: "tool",
				ToolResponse: &ToolResponseSpec{
					ID: "call-1", Name: "get_weather", Content: "Sunny, 25°C",
				},
			},
			{Author: "assistant", Role: "assistant", Content: "The weather in Beijing is sunny, 25°C."},
		},
		MemoryWrites: []MemoryWriteSpec{
			{Memory: "User checked weather for Beijing", Topics: []string{"action"}},
		},
		MemoryQueries: []MemoryQuerySpec{
			{Query: "weather", Limit: 5},
		},
	}
}

// --- Case 4: State updates ---
func case4StateUpdates() ReplayCase {
	return ReplayCase{
		Name:      "scoped_state_overwrite",
		AppName:   "test-app",
		UserID:    "user-1",
		SessionID: "session-004",
		InitialState: map[string]string{
			"user:score": "0",
			"app:round":  "1",
		},
		Events: []EventSpec{
			{Author: "assistant", Role: "assistant", Content: "Starting round 1.",
				StateDelta: map[string]string{"user:score": "10"},
			},
			{Author: "user", Role: "user", Content: "I found the answer."},
			{Author: "assistant", Role: "assistant", Content: "Score updated.",
				StateDelta: map[string]string{"user:score": "25", "app:round": "2"},
			},
		},
	}
}

// --- Case 5: Memory write and read ---
func case5MemoryRW() ReplayCase {
	return ReplayCase{
		Name:      "memory_multi_author_search",
		AppName:   "test-app",
		UserID:    "user-2",
		SessionID: "session-005",
		Events: []EventSpec{
			{Author: "user", Role: "user", Content: "I enjoy hiking on weekends."},
			{Author: "assistant", Role: "assistant", Content: "That's great! Hiking is wonderful exercise."},
			{Author: "user", Role: "user", Content: "Also I prefer tea over coffee."},
			{Author: "assistant", Role: "assistant", Content: "Noted - you prefer tea."},
		},
		MemoryWrites: []MemoryWriteSpec{
			{Memory: "User enjoys hiking", Topics: []string{"hobbies"}},
			{Memory: "User prefers tea", Topics: []string{"preferences"}},
			{Memory: "User is health conscious", Topics: []string{"lifestyle"}},
		},
		MemoryQueries: []MemoryQuerySpec{
			{Query: "hiking", Limit: 3},
			{Query: "tea", Limit: 3},
			{Query: "preferences", Limit: 5},
		},
	}
}

// --- Case 6: Summary generation and update ---
func case6SummaryUpdate() ReplayCase {
	return ReplayCase{
		Name:      "summary_generation",
		AppName:   "test-app",
		UserID:    "user-1",
		SessionID: "session-006",
		Events: []EventSpec{
			{Author: "user", Role: "user", Content: "Help me plan a trip to Shanghai."},
			{Author: "assistant", Role: "assistant", Content: "Sure! When would you like to go?"},
			{Author: "user", Role: "user", Content: "Next month, for 3 days."},
			{Author: "assistant", Role: "assistant", Content: "I recommend visiting the Bund and Yu Garden."},
			{Author: "user", Role: "user", Content: "Great, also I need hotel recommendations."},
			{Author: "assistant", Role: "assistant", Content: "I can search for hotels near the Bund."},
		},
		SummarySteps: []SummaryStep{
			{AfterEventIndex: 6, FilterKey: "", Force: true},
		},
	}
}

// --- Case 7: Summary with event truncation ---
func case7SummaryTruncation() ReplayCase {
	return ReplayCase{
		Name:      "summary_with_truncation",
		AppName:   "test-app",
		UserID:    "user-1",
		SessionID: "session-007",
		Events: []EventSpec{
			{Author: "user", Role: "user", Content: "Message 1: Greetings."},
			{Author: "assistant", Role: "assistant", Content: "Response 1: Hello!"},
			{Author: "user", Role: "user", Content: "Message 2: Question about weather."},
			{Author: "assistant", Role: "assistant", Content: "Response 2: It's sunny."},
			{Author: "user", Role: "user", Content: "Message 3: Thank you."},
			{Author: "assistant", Role: "assistant", Content: "Response 3: You're welcome."},
			{Author: "user", Role: "user", Content: "Message 4: Can you summarize?"},
			{Author: "assistant", Role: "assistant", Content: "Response 4: Here is the summary."},
		},
		SummarySteps: []SummaryStep{
			{AfterEventIndex: 6, FilterKey: "weather", Force: true},
			{AfterEventIndex: 8, FilterKey: "", Force: true},
		},
	}
}

// --- Case 8: Track events ---
func case8TrackEvents() ReplayCase {
	return ReplayCase{
		Name:      "track_events",
		AppName:   "test-app",
		UserID:    "user-1",
		SessionID: "session-008",
		Events: []EventSpec{
			{Author: "user", Role: "user", Content: "Run a calculation."},
			{Author: "assistant", Role: "assistant", Content: "Running calculation..."},
		},
		TrackEvents: []TrackEventSpec{
			{Track: "tool_execution", Payload: `{"tool":"calculator","duration_ms":150,"status":"success"}`},
			{Track: "tool_execution", Payload: `{"tool":"calculator","duration_ms":200,"status":"success"}`},
			{Track: "subtask_status", Payload: `{"subtask":"verification","status":"passed"}`},
		},
	}
}

// --- Case 9: Out-of-order writes ---
func case9OutOfOrderWrites() ReplayCase {
	return ReplayCase{
		Name:      "out_of_order_writes",
		AppName:   "test-app",
		UserID:    "user-3",
		SessionID: "session-009",
		Events: []EventSpec{
			{Author: "user", Role: "user", Content: "Start parallel task A."},
			{Author: "user", Role: "user", Content: "Start parallel task B."},
			{Author: "assistant", Role: "assistant", Content: "Task A result."},
			{Author: "assistant", Role: "assistant", Content: "Task B result."},
			{Author: "user", Role: "user", Content: "Merge results."},
		},
		MemoryWrites: []MemoryWriteSpec{
			{Memory: "Parallel task A completed", Topics: []string{"task"}},
			{Memory: "Parallel task B completed", Topics: []string{"task"}},
		},
	}
}

// --- Case 10: Error recovery ---
func case10ErrorRecovery() ReplayCase {
	return ReplayCase{
		Name:      "error_recovery",
		AppName:   "test-app",
		UserID:    "user-1",
		SessionID: "session-010",
		Events: []EventSpec{
			{Author: "user", Role: "user", Content: "Normal message 1."},
			{Author: "assistant", Role: "assistant", Content: "Normal response 1."},
			{Author: "user", Role: "user", Content: "Duplicate test message."},
			{Author: "user", Role: "user", Content: "Duplicate test message."},
			{Author: "assistant", Role: "assistant", Content: "Response to duplicate."},
			{Author: "user", Role: "user", Content: "Final message."},
			{Author: "assistant", Role: "assistant", Content: "Final response."},
		},
		MemoryWrites: []MemoryWriteSpec{
			{Memory: "Normal operation recorded", Topics: []string{"status"}},
			{Memory: "Normal operation recorded", Topics: []string{"status"}},
		},
	}
}

// =============================================================================
// Tests
// =============================================================================

func TestInMemorySessionReplayEventsStateAndMemoryMatch(t *testing.T) {
	backends := standardBackends(t)
	defer func() {
		for _, b := range backends {
			_ = b.Teardown(context.Background())
		}
	}()

	ctx := context.Background()
	cases := replayCases()
	assert.Len(t, cases, 10, "should have exactly 10 replay cases")

	reports, err := RunReplayMatrix(ctx, backends, cases, nil)
	require.NoError(t, err, "replay matrix should succeed")

	foundUnallowed := false
	for _, r := range reports {
		unallowedDiffs := filterUnallowedDiffs(r.Diffs)
		if len(unallowedDiffs) > 0 {
			foundUnallowed = true
			for _, d := range unallowedDiffs {
				t.Errorf(
					"Case %q: backend pair [%s vs %s]: unallowed diff at %s: %v vs %v",
					r.CaseName, r.BackendA, r.BackendB,
					d.FieldPath, d.ValueA, d.ValueB,
				)
			}
		}
		t.Logf("Case %q [%s vs %s]: %d total diffs, %d unallowed",
			r.CaseName, r.BackendA, r.BackendB,
			len(r.Diffs), len(unallowedDiffs))
	}
	assert.False(t, foundUnallowed, "should have zero unallowed diffs between identical backends")
}

func TestDiffDetectsInjectedInconsistencies(t *testing.T) {
	// Create intentionally different snapshots and verify
	// the comparator detects the differences across all sections.
	left := Snapshot{
		SessionID: "test-session",
		State:     map[string]string{"key1": "value1"},
		Events: []NormalizedEvent{
			{Author: "user", Role: "user", Content: "Hello"},
			{Author: "assistant", Role: "assistant", Content: "Hi there"},
		},
		Memories: []NormalizedMemory{
			{Content: "Test memory", Topics: []string{"test"}},
		},
		Summaries: []NormalizedSummary{
			{FilterKey: "", Summary: "Summary text"},
		},
		Tracks: []NormalizedTrack{
			{Track: "execution", Payload: `{"ok":true}`},
		},
	}

	// Right snapshot has injected differences in every section.
	right := Snapshot{
		SessionID: "test-session",
		State:     map[string]string{"key1": "wrong_value"},
		Events: []NormalizedEvent{
			{Author: "user", Role: "user", Content: "Hello"},
			{Author: "assistant", Role: "assistant", Content: "Different reply"},
		},
		Memories: []NormalizedMemory{
			{Content: "Different content", Topics: []string{"test"}},
		},
		Summaries: []NormalizedSummary{
			{FilterKey: "", Summary: "Wrong summary"},
		},
		Tracks: []NormalizedTrack{
			{Track: "execution", Payload: `{"ok":false}`},
		},
	}

	diffs := CompareSnapshots(left, right, "inmemory", "sqlite", nil)
	assert.NotEmpty(t, diffs, "must detect injected differences")

	// Verify each section has at least one diff.
	sections := make(map[string]bool)
	for _, d := range diffs {
		t.Logf("Detected: %s: %v vs %v", d.FieldPath, d.ValueA, d.ValueB)
		sections[extractSection(d.FieldPath)] = true
	}
	assert.True(t, sections["state"], "state diff must be detected")
	assert.True(t, sections["events"], "events diff must be detected")
	assert.True(t, sections["memories"], "memory diff must be detected")
	assert.True(t, sections["summaries"], "summary diff must be detected")
	assert.True(t, sections["tracks"], "tracks diff must be detected")
}

func TestDiffDetectsSummaryInjections(t *testing.T) {
	// Verify summary-specific diff detection.
	left := Snapshot{
		SessionID: "s1",
		Summaries: []NormalizedSummary{
			{FilterKey: "", Summary: "Correct summary"},
		},
	}

	// Missing summary entirely.
	right := Snapshot{SessionID: "s1"}
	diffs := CompareSnapshots(left, right, "a", "b", nil)
	assert.NotEmpty(t, diffs, "missing summary should be detected")

	// Overwritten text.
	right2 := Snapshot{
		SessionID: "s1",
		Summaries: []NormalizedSummary{
			{FilterKey: "", Summary: "Overwritten text"},
		},
	}
	diffs2 := CompareSnapshots(left, right2, "a", "b", nil)
	found := false
	for _, d := range diffs2 {
		if d.FieldPath == "summaries[0].summary" {
			found = true
			assert.Equal(t, "Correct summary", d.ValueA)
			assert.Equal(t, "Overwritten text", d.ValueB)
		}
	}
	assert.True(t, found, "summary text overwrite must be detected")
}

func TestAllowedDiffRulesMarkOnlyExplicitMatches(t *testing.T) {
	left := Snapshot{
		SessionID: "s1",
		Events:    []NormalizedEvent{{Author: "user", Role: "user", Content: "A"}},
	}
	right := Snapshot{
		SessionID: "s1",
		Events:    []NormalizedEvent{{Author: "user", Role: "user", Content: "B"}},
	}

	allowedDiffs := []AllowedDiffRule{
		{
			Section:     "events",
			PathPattern: "events[0].content",
			Reason:      "Content normalization difference",
		},
	}

	diffs := CompareSnapshots(left, right, "inmemory", "sqlite", allowedDiffs)
	assert.NotEmpty(t, diffs)

	for _, d := range diffs {
		if d.FieldPath == "events[0].content" {
			assert.True(t, d.Allowed, "content diff should be marked allowed")
			assert.Equal(t, "Content normalization difference", d.Reason)
		} else {
			assert.False(t, d.Allowed, "unmatched diffs must remain flagged")
		}
	}
}

func TestReportGeneration(t *testing.T) {
	cases := replayCases()
	ctx := context.Background()
	backends := standardBackends(t)
	defer func() {
		for _, b := range backends {
			_ = b.Teardown(ctx)
		}
	}()

	reports, err := RunReplayMatrix(ctx, backends, cases, nil)
	require.NoError(t, err)

	err = GenerateReport(reports, ReportConfig{
		OutputPath: filepath.Join(t.TempDir(), "session_memory_summary_track_diff_report.json"),
	})
	require.NoError(t, err, "report generation should succeed")
}

// =============================================================================
// Helpers
// =============================================================================

func filterUnallowedDiffs(diffs []FieldDiff) []FieldDiff {
	var result []FieldDiff
	for _, d := range diffs {
		if !d.Allowed {
			result = append(result, d)
		}
	}
	return result
}

func TestCompareMemoriesScoreDiff(t *testing.T) {
	left := Snapshot{
		SessionID: "s1",
		Memories: []NormalizedMemory{
			{Content: "mem1", Score: 0.9},
		},
	}
	right := Snapshot{
		SessionID: "s1",
		Memories: []NormalizedMemory{
			{Content: "mem1", Score: 0.5},
		},
	}
	diffs := CompareSnapshots(left, right, "a", "b", nil)
	found := false
	for _, d := range diffs {
		if strings.Contains(d.FieldPath, "score") {
			found = true
		}
	}
	assert.True(t, found, "score difference must be detected")
}

func TestCompareMemoriesExtraRight(t *testing.T) {
	left := Snapshot{SessionID: "s1"}
	right := Snapshot{
		SessionID: "s1",
		Memories: []NormalizedMemory{
			{Content: "extra", Topics: []string{"test"}},
		},
	}
	diffs := CompareSnapshots(left, right, "a", "b", nil)
	assert.NotEmpty(t, diffs, "extra memory in right should be detected")
}

func TestCompareEventsLengthMismatch(t *testing.T) {
	left := Snapshot{
		SessionID: "s1",
		Events:    []NormalizedEvent{{Author: "user", Role: "user", Content: "A"}, {Author: "assistant", Role: "assistant", Content: "B"}},
	}
	right := Snapshot{
		SessionID: "s1",
		Events:    []NormalizedEvent{{Author: "user", Role: "user", Content: "A"}},
	}
	diffs := CompareSnapshots(left, right, "a", "b", nil)
	assert.NotEmpty(t, diffs)
}

func TestCompareSummariesMismatch(t *testing.T) {
	left := Snapshot{
		SessionID: "s1",
		Summaries: []NormalizedSummary{{FilterKey: "k1", Summary: "s1"}},
	}
	right := Snapshot{
		SessionID: "s1",
		Summaries: []NormalizedSummary{{FilterKey: "k1", Summary: "different"}},
	}
	diffs := CompareSnapshots(left, right, "a", "b", nil)
	assert.NotEmpty(t, diffs)
}

func TestCompareTracksExtraInRight(t *testing.T) {
	left := Snapshot{SessionID: "s1"}
	right := Snapshot{
		SessionID: "s1",
		Tracks:    []NormalizedTrack{{Track: "t1", Payload: "p1"}},
	}
	diffs := CompareSnapshots(left, right, "a", "b", nil)
	assert.NotEmpty(t, diffs)
}

func TestNormalizeJSONBytesInvalid(t *testing.T) {
	result := normalizeJSONBytes([]byte("{invalid}"))
	assert.Equal(t, "{invalid}", result)
}

func TestGenerateReportDefaultPath(t *testing.T) {
	err := GenerateReport(nil, ReportConfig{})
	// Default path goes to os.TempDir() — should succeed.
	assert.NoError(t, err)
}

func TestEventSpecToolResponseNilCheck(t *testing.T) {
	// Verify that a tool-role event with nil ToolResponse
	// produces an error from buildEvent instead of panicking.
	es := EventSpec{Role: "tool"}
	_, err := buildEvent(es, 0, "s1", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "tool response is nil")
}

func TestMatchPathPatternWildcard(t *testing.T) {
	assert.True(t, matchPathPattern("events[0].tool_calls[1].args.city", "events[*].tool_calls[*].args.*"))
	assert.False(t, matchPathPattern("events[0].content", "events[0].content.extra"))
	assert.False(t, matchPathPattern("state.x", "events.x"))
}

func TestNormalizeSummariesNilEntry(t *testing.T) {
	// nil summary entries should be skipped.
	summaries := map[string]*session.Summary{
		"filter1": nil,
		"filter2": {Summary: "valid"},
	}
	result := normalizeSummaries(summaries)
	assert.Len(t, result, 1, "nil entries should be skipped")
	assert.Equal(t, "filter2", result[0].FilterKey)
}

func TestNormalizeSummariesMultipleSorted(t *testing.T) {
	// Multiple summaries should be sorted by filter key.
	summaries := map[string]*session.Summary{
		"z_filter": {Summary: "last"},
		"a_filter": {Summary: "first"},
		"m_filter": {Summary: "middle"},
	}
	result := normalizeSummaries(summaries)
	assert.Len(t, result, 3)
	assert.Equal(t, "a_filter", result[0].FilterKey)
	assert.Equal(t, "m_filter", result[1].FilterKey)
	assert.Equal(t, "z_filter", result[2].FilterKey)
}

func TestNormalizeSummariesEmptyMap(t *testing.T) {
	result := normalizeSummaries(nil)
	assert.Empty(t, result)
}

func TestMatchAllowedDiffBackendPairMatch(t *testing.T) {
	// When BackendA/BackendB are set and match, the rule proceeds to check
	// section and path pattern (which also must match for Allowed=true).
	rule := AllowedDiffRule{
		Section:     "events",
		PathPattern: "events[0].content",
		BackendA:    "inmemory",
		BackendB:    "sqlite",
	}
	diff := FieldDiff{
		FieldPath: "events[0].content",
		ValueA:    "hello",
		ValueB:    "world",
	}
	// Backend pair matches in forward order.
	assert.True(t, matchAllowedDiff(diff, rule, "inmemory", "sqlite"))
	// Backend pair matches in reverse order.
	assert.True(t, matchAllowedDiff(diff, rule, "sqlite", "inmemory"))
}

func TestMatchAllowedDiffBackendPairMismatch(t *testing.T) {
	rule := AllowedDiffRule{
		Section:     "events",
		PathPattern: "events[0].content",
		BackendA:    "inmemory",
		BackendB:    "sqlite",
	}
	diff := FieldDiff{
		FieldPath: "events[0].content",
		ValueA:    "hello",
		ValueB:    "world",
	}
	// Backend pair does not match.
	assert.False(t, matchAllowedDiff(diff, rule, "inmemory", "redis"))
}

func TestMatchAllowedDiffSectionMismatch(t *testing.T) {
	rule := AllowedDiffRule{
		Section:     "state",
		PathPattern: "events[0].content",
	}
	diff := FieldDiff{FieldPath: "events[0].content"}
	assert.False(t, matchAllowedDiff(diff, rule, "a", "b"))
}

func TestCompareEventsRightExtra(t *testing.T) {
	// Right has more events than left — extra events should be flagged.
	left := Snapshot{
		SessionID: "s1",
		Events:    []NormalizedEvent{{Author: "user", Role: "user", Content: "A"}},
	}
	right := Snapshot{
		SessionID: "s1",
		Events: []NormalizedEvent{
			{Author: "user", Role: "user", Content: "A"},
			{Author: "assistant", Role: "assistant", Content: "B"},
		},
	}
	diffs := CompareSnapshots(left, right, "a", "b", nil)
	// Find the diff for events[1] (extra in right).
	found := false
	for _, d := range diffs {
		if d.FieldPath == "events[1]" {
			found = true
			assert.Nil(t, d.ValueA)
			assert.NotNil(t, d.ValueB)
		}
	}
	assert.True(t, found, "extra event in right should be detected")
}

func TestCompareSummariesRightExtra(t *testing.T) {
	left := Snapshot{SessionID: "s1"}
	right := Snapshot{
		SessionID: "s1",
		Summaries: []NormalizedSummary{
			{FilterKey: "k1", Summary: "extra"},
		},
	}
	diffs := CompareSnapshots(left, right, "a", "b", nil)
	assert.NotEmpty(t, diffs, "extra summary in right should be detected")
}

func TestCompareSummariesFilterKeyMismatch(t *testing.T) {
	left := Snapshot{
		SessionID: "s1",
		Summaries: []NormalizedSummary{{FilterKey: "k1", Summary: "same"}},
	}
	right := Snapshot{
		SessionID: "s1",
		Summaries: []NormalizedSummary{{FilterKey: "k2", Summary: "same"}},
	}
	diffs := CompareSnapshots(left, right, "a", "b", nil)
	found := false
	for _, d := range diffs {
		if strings.Contains(d.FieldPath, "filter_key") {
			found = true
		}
	}
	assert.True(t, found, "filter key mismatch should be detected")
}

func TestNormalizeStateNil(t *testing.T) {
	result := normalizeState(nil)
	assert.Nil(t, result)
}

func TestNormalizeTracksNilEvents(t *testing.T) {
	// Track with nil events should be skipped.
	tracks := map[session.Track]*session.TrackEvents{
		"track1": nil,
		"track2": {Events: []session.TrackEvent{{Payload: []byte(`"ok"`)}}},
	}
	result := normalizeTracks(tracks)
	assert.Len(t, result, 1, "nil track events should be skipped")
}

func TestCompareStructsTypeMismatch(t *testing.T) {
	// Different types should produce a diff.
	type TypeA struct{ X string }
	type TypeB struct{ X string }
	diffs := compareStructs(TypeA{X: "a"}, TypeB{X: "b"}, "root", "s1")
	assert.NotEmpty(t, diffs)
	assert.Equal(t, "root", diffs[0].FieldPath)
}

func TestExtractSectionVariants(t *testing.T) {
	// Bracket before dot: bracket is the first separator.
	assert.Equal(t, "events", extractSection("events[0].content"))
	// Dot found first; bracket after dot is not the first separator.
	assert.Equal(t, "events", extractSection("events.field[0]"))
	// No bracket or dot separator.
	assert.Equal(t, "state", extractSection("state"))
	// Only with bracket.
	assert.Equal(t, "events", extractSection("events[0]"))
	// Only with dot.
	assert.Equal(t, "events", extractSection("events.field"))
}

func TestNormalizeJSONBytesEmpty(t *testing.T) {
	result := normalizeJSONBytes(nil)
	assert.Equal(t, "", result)
	result = normalizeJSONBytes([]byte{})
	assert.Equal(t, "", result)
}

func TestBuildEventTextMessage(t *testing.T) {
	es := EventSpec{Role: "user", Content: "hello world"}
	evt, err := buildEvent(es, 0, "s1", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "user", string(evt.Response.Choices[0].Message.Role))
	assert.Equal(t, "hello world", evt.Response.Choices[0].Message.Content)
}

func TestNormalizeMemoriesNilEntry(t *testing.T) {
	// nil memory entries should be skipped.
	memories := []*memory.Entry{
		nil,
		{Memory: &memory.Memory{Memory: "valid", Topics: []string{"test"}}},
	}
	result := normalizeMemories(memories)
	assert.Len(t, result, 1)
	assert.Equal(t, "valid", result[0].Content)
}

func TestNormalizeMemoriesNilMemoryField(t *testing.T) {
	// Entry with nil Memory field should be skipped.
	memories := []*memory.Entry{
		{Memory: nil},
	}
	result := normalizeMemories(memories)
	assert.Empty(t, result)
}

func TestNormalizeMemoriesNilTopics(t *testing.T) {
	// Nil Topics should be normalized to empty slice.
	memories := []*memory.Entry{
		{Memory: &memory.Memory{Memory: "content", Topics: nil}},
	}
	result := normalizeMemories(memories)
	assert.Len(t, result, 1)
	assert.Equal(t, []string{}, result[0].Topics)
}

func TestCompareMemoriesTopicsDiff(t *testing.T) {
	left := Snapshot{
		SessionID: "s1",
		Memories: []NormalizedMemory{
			{Content: "mem1", Topics: []string{"a", "b"}},
		},
	}
	right := Snapshot{
		SessionID: "s1",
		Memories: []NormalizedMemory{
			{Content: "mem1", Topics: []string{"a", "c"}},
		},
	}
	diffs := CompareSnapshots(left, right, "a", "b", nil)
	found := false
	for _, d := range diffs {
		if strings.Contains(d.FieldPath, "topics") {
			found = true
		}
	}
	assert.True(t, found, "topics difference should be detected")
}

func TestBuildEventWithStateDelta(t *testing.T) {
	es := EventSpec{
		Role: "assistant",
		StateDelta: map[string]string{
			"key1": "val1",
		},
	}
	evt, err := buildEvent(es, 0, "s1", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Equal(t, "val1", string(evt.StateDelta["key1"]))
}

func TestBuildEventWithToolCalls(t *testing.T) {
	es := EventSpec{
		Role: "assistant",
		ToolCalls: []ToolCallSpec{
			{ID: "tc1", Name: "search", Arguments: `{"q":"test"}`},
		},
	}
	evt, err := buildEvent(es, 0, "s1", time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	assert.Len(t, evt.Response.Choices[0].Message.ToolCalls, 1)
	assert.Equal(t, "search", evt.Response.Choices[0].Message.ToolCalls[0].Function.Name)
}

// Compile-time interface compliance checks.
var _ session.Service = (*sessioninmemory.SessionService)(nil)
var _ memory.Service = (*memoryinmemory.MemoryService)(nil)
