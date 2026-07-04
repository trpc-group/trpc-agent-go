package replaytest

import (
	"context"
	"testing"

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
		case9ConcurrentWrites(),
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

// --- Case 9: Concurrent or out-of-order writes ---
func case9ConcurrentWrites() ReplayCase {
	return ReplayCase{
		Name:      "concurrent_out_of_order_writes",
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
		OutputPath: "session_memory_summary_track_diff_report.json",
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

// Compile-time interface compliance checks.
var _ session.Service = (*sessioninmemory.SessionService)(nil)
var _ memory.Service = (*memoryinmemory.MemoryService)(nil)
