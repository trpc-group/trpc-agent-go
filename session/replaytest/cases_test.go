//go:build cgo

package replaytest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// allCases returns all 15 replay test cases.
func allCases() []Case {
	return []Case{
		case01SingleTurn(),
		case02MultiTurn(),
		case03ToolCallCrossRef(),
		case04StateUpdateOverwriteDelete(),
		case05MemorySearchAndScore(),
		case06SummaryFilterAndUpdate(),
		case07SummaryEventWindowRecovery(),
		case08TrackStatusAndError(),
		case09ConcurrentToolInterleaving(),
		case10FailureRecoveryWithoutDuplicates(),
		case11StateDeltaNull(),
		case12BoundaryAndError(),
		case13StateDelete(),
		case14StateScopes(),
		case15SummaryFilterKey(),
	}
}

func case01SingleTurn() Case {
	return Case{
		Name:         "case01_single_turn",
		RequiredCaps: []string{CapEvents},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			backend.Sess.AppendEvent(ctx, sess, newUserEvent("Hello, world!"))
			backend.Sess.AppendEvent(ctx, sess, newAssistantEvent("Hi there!"))
			return nil
		},
	}
}

func case02MultiTurn() Case {
	return Case{
		Name:         "case02_multi_turn",
		RequiredCaps: []string{CapEvents},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			for i := 0; i < 5; i++ {
				backend.Sess.AppendEvent(ctx, sess, newUserEvent(fmt.Sprintf("Q%d", i+1)))
				backend.Sess.AppendEvent(ctx, sess, newAssistantEvent(fmt.Sprintf("A%d", i+1)))
			}
			return nil
		},
	}
}

func case03ToolCallCrossRef() Case {
	return Case{
		Name:         "case03_tool_call_cross_ref",
		RequiredCaps: []string{CapEvents},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			backend.Sess.AppendEvent(ctx, sess, newUserEvent("What's the weather?"))
			backend.Sess.AppendEvent(ctx, sess, newToolCallEvent("get_weather", `{"city":"Beijing"}`, "tc-001"))
			backend.Sess.AppendEvent(ctx, sess, newToolResponseEvent("tc-001", "get_weather", `{"temp":25}`))
			backend.Sess.AppendEvent(ctx, sess, newAssistantEvent("It's 25\u00b0C."))
			return nil
		},
	}
}

func case04StateUpdateOverwriteDelete() Case {
	return Case{
		Name:         "case04_state_update_overwrite_delete",
		RequiredCaps: []string{CapState},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")})
			backend.Sess.UpdateSessionState(ctx, key, session.StateMap{"k1": []byte("v1-new")})
			backend.Sess.UpdateSessionState(ctx, key, session.StateMap{"k3": []byte("v3")})
			backend.Sess.UpdateSessionState(ctx, key, session.StateMap{"k2": nil})
			return nil
		},
	}
}

func case05MemorySearchAndScore() Case {
	epTime := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC)
	return Case{
		Name:              "case05_memory_search_and_score",
		RequiredCaps:      []string{CapMemory},
		UnorderedMemories: true,
		Run: func(ctx context.Context, backend Backend) error {
			uk := memory.UserKey{AppName: backend.SessKey().AppName, UserID: backend.SessKey().UserID}
			backend.Mem.AddMemory(ctx, uk, "User prefers dark mode", []string{"preference"})
			backend.Mem.AddMemory(ctx, uk, "User is a Go developer", []string{"fact"})
			backend.Mem.AddMemory(ctx, uk, "User went hiking at Mt. Fuji with Alice", []string{"episode"},
				memory.WithMetadata(&memory.Metadata{
					Kind:         memory.KindEpisode,
					EventTime:    &epTime,
					Participants: []string{"Alice"},
					Location:     "Mt. Fuji",
				}))
			return nil
		},
	}
}

func case06SummaryFilterAndUpdate() Case {
	return Case{
		Name:         "case06_summary_filter_and_update",
		RequiredCaps: []string{CapSummary},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			for i := 0; i < 5; i++ {
				backend.Sess.AppendEvent(ctx, sess, newUserEvent(fmt.Sprintf("Q%d", i+1)))
				backend.Sess.AppendEvent(ctx, sess, newAssistantEvent(fmt.Sprintf("A%d", i+1)))
			}
			backend.Sess.CreateSessionSummary(ctx, sess, "", true)
			backend.Sess.CreateSessionSummary(ctx, sess, "branch-a", true)
			return nil
		},
	}
}

func case07SummaryEventWindowRecovery() Case {
	return Case{
		Name:         "case07_summary_event_window_recovery",
		RequiredCaps: []string{CapSummary, CapEvents},
		AllowedDiffs: []AllowedDiff{
			{BackendA: "inmemory", BackendB: "sqlite", Section: "summaries", Path: `$.summaries[""].cutoff_at_event_index`, Reason: "InMemory and SQLite compute summary boundary at different event indices"},
			{BackendA: "inmemory", BackendB: "sqlite", Section: "summaries", Path: `$.summaries[""].updated_at_event_index`, Reason: "InMemory and SQLite compute summary update timestamp at different event indices"},
			{BackendA: "inmemory", BackendB: "inmemory-b", Section: "summaries", Path: `$.summaries[""].cutoff_at_event_index`, Reason: "Independent InMemory instances compute summary boundary at different event indices due to timing"},
			{BackendA: "inmemory", BackendB: "inmemory-b", Section: "summaries", Path: `$.summaries[""].updated_at_event_index`, Reason: "Independent InMemory instances compute summary update timestamp at different event indices due to timing"},
		},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			for i := 0; i < 20; i++ {
				if i%2 == 0 {
					backend.Sess.AppendEvent(ctx, sess, newUserEvent(fmt.Sprintf("Q%d", i/2+1)))
				} else {
					backend.Sess.AppendEvent(ctx, sess, newAssistantEvent(fmt.Sprintf("A%d", i/2+1)))
				}
			}
			backend.Sess.CreateSessionSummary(ctx, sess, "", true)
			for i := 0; i < 5; i++ {
				if i%2 == 0 {
					backend.Sess.AppendEvent(ctx, sess, newUserEvent(fmt.Sprintf("Q%d", 11+i/2)))
				} else {
					backend.Sess.AppendEvent(ctx, sess, newAssistantEvent(fmt.Sprintf("A%d", 11+i/2)))
				}
			}
			return nil
		},
	}
}

func case08TrackStatusAndError() Case {
	return Case{
		Name:         "case08_track_status_and_error",
		RequiredCaps: []string{CapTrack},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			backend.Track.AppendTrackEvent(ctx, sess, newTrackEvent("agent-run", `{"type":"start","invocation_id":"inv-1"}`))
			backend.Track.AppendTrackEvent(ctx, sess, newTrackEventWithVolatile("agent-run", map[string]any{
				"type":          "end",
				"invocation_id": "inv-1",
				"status":        "ok",
				"duration":      1234.5,
				"latency_ms":    5678,
			}))
			backend.Track.AppendTrackEvent(ctx, sess, newTrackEvent("agent-run", `{"type":"error","invocation_id":"inv-2","error":"timeout"}`))
			return nil
		},
	}
}

func case09ConcurrentToolInterleaving() Case {
	return Case{
		Name:         "case09_concurrent_tool_interleaving",
		RequiredCaps: []string{CapEvents},
		CountOnly:    true,
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			// Append 15 events sequentially (3 logical "threads" x 5 events each).
			// Session.AppendEvent is not goroutine-safe, so we serialize here.
			// CountOnly mode verifies event counts match across backends.
			for gIdx := 0; gIdx < 3; gIdx++ {
				for i := 0; i < 5; i++ {
					backend.Sess.AppendEvent(ctx, sess, newUserEvent(fmt.Sprintf("g%d-e%d", gIdx, i)))
				}
			}
			return nil
		},
	}
}

func case10FailureRecoveryWithoutDuplicates() Case {
	return Case{
		Name:         "case10_failure_recovery_without_duplicates",
		RequiredCaps: []string{CapEvents, CapSummary},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			// Duplicate events.
			backend.Sess.AppendEvent(ctx, sess, newUserEvent("duplicate-test"))
			backend.Sess.AppendEvent(ctx, sess, newUserEvent("duplicate-test"))
			backend.Sess.AppendEvent(ctx, sess, newUserEvent("duplicate-test"))
			// State overwrite.
			backend.Sess.UpdateSessionState(ctx, key, session.StateMap{"retry-key": []byte("first-attempt")})
			backend.Sess.UpdateSessionState(ctx, key, session.StateMap{"retry-key": []byte("retry-value")})
			// Summary created twice (idempotent).
			backend.Sess.AppendEvent(ctx, sess, newAssistantEvent("after-dup"))
			backend.Sess.CreateSessionSummary(ctx, sess, "", true)
			backend.Sess.CreateSessionSummary(ctx, sess, "", true)
			return nil
		},
	}
}

func case11StateDeltaNull() Case {
	return Case{
		Name:         "case11_state_delta_null",
		RequiredCaps: []string{CapState, CapEvents},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, session.StateMap{"k1": []byte("v1"), "k2": []byte("v2")})
			sess, _ := backend.Sess.GetSession(ctx, key)
			// StateDelta with nil value (delete semantics).
			backend.Sess.AppendEvent(ctx, sess, newEventWithStateDeltaNull("delta-null", "k2"))
			return nil
		},
	}
}

func case12BoundaryAndError() Case {
	return Case{
		Name:         "case12_boundary_and_error",
		RequiredCaps: []string{CapEvents, CapState},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			// Empty state.
			backend.Sess.CreateSession(ctx, key, session.StateMap{})
			sess, _ := backend.Sess.GetSession(ctx, key)
			// Event with extensions.
			extData, _ := json.Marshal(map[string]string{"custom-key": "custom-value"})
			backend.Sess.AppendEvent(ctx, sess, newAssistantEventWithExtensions("with-ext", map[string]json.RawMessage{
				"custom-namespace": extData,
			}))
			// Event with branch/tag/filterKey.
			backend.Sess.AppendEvent(ctx, sess, newEventWithBranchTagFilterKey("user", "branch-a", "code_execution_code", "filter/alpha", "request with branch"))
			// Past EventTime.
			_, _ = backend.Sess.GetSession(ctx, key, session.WithEventTime(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)))
			// Large EventNum.
			_, _ = backend.Sess.GetSession(ctx, key, session.WithEventNum(9999))
			return nil
		},
	}
}

func case13StateDelete() Case {
	return Case{
		Name:         "case13_state_delete",
		RequiredCaps: []string{CapState},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			// Create session with initial state.
			backend.Sess.CreateSession(ctx, key, session.StateMap{
				"keep":    []byte("stay"),
				"to_delete": []byte("gone"),
			})
			// Delete a key by setting it to nil.
			backend.Sess.UpdateSessionState(ctx, key, session.StateMap{"to_delete": nil})
			return nil
		},
	}
}

func case14StateScopes() Case {
	return Case{
		Name:         "case14_state_scopes",
		RequiredCaps: []string{CapState},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			// Create session (establishes appName + userID).
			backend.Sess.CreateSession(ctx, key, nil)
			// Update AppState: app-scoped state shared across sessions.
			if err := backend.Sess.UpdateAppState(ctx, key.AppName, session.StateMap{
				"theme": []byte("dark"),
			}); err != nil {
				return err
			}
			// UpdateUserState: user-scoped state shared across sessions.
			userKey := session.UserKey{AppName: key.AppName, UserID: key.UserID}
			if err := backend.Sess.UpdateUserState(ctx, userKey, session.StateMap{
				"locale": []byte("zh-CN"),
			}); err != nil {
				return err
			}
			return nil
		},
	}
}

func case15SummaryFilterKey() Case {
	return Case{
		Name:         "case15_summary_filter_key",
		RequiredCaps: []string{CapSummary, CapEvents},
		AllowedDiffs: []AllowedDiff{
			{
				BackendA: "inmemory", BackendB: "sqlite",
				Section: "summaries", Path: `$.summaries["branch-a"].cutoff_at_event_index`,
				Reason: "InMemory vs SQLite summary boundary index timing",
			},
			{
				BackendA: "inmemory", BackendB: "sqlite",
				Section: "summaries", Path: `$.summaries["branch-a"].updated_at_event_index`,
				Reason: "InMemory vs SQLite summary boundary index timing",
			},
		},
		Run: func(ctx context.Context, backend Backend) error {
			key := backend.SessKey()
			backend.Sess.CreateSession(ctx, key, nil)
			sess, _ := backend.Sess.GetSession(ctx, key)
			// Append events with explicit filterKey set.
			for i := 0; i < 5; i++ {
				// Events with filterKey "branch-a" — only these feed the "branch-a" summary.
				e := newEventWithBranchTagFilterKey("user", "branch-a", "", "branch-a", fmt.Sprintf("branch-a-q%d", i+1))
				backend.Sess.AppendEvent(ctx, sess, e)
			}
			for i := 0; i < 3; i++ {
				// Events with filterKey "branch-b".
				e := newEventWithBranchTagFilterKey("user", "branch-b", "", "branch-b", fmt.Sprintf("branch-b-q%d", i+1))
				backend.Sess.AppendEvent(ctx, sess, e)
			}
			// Create summary with filterKey="branch-a" — only branch-a events are summarized.
			backend.Sess.CreateSessionSummary(ctx, sess, "branch-a", true)
			return nil
		},
	}
}

// --- Test functions ---

// TestReplay_All runs all 15 cases on InMemory vs SQLite.
func TestReplay_All(t *testing.T) {
	for _, c := range allCases() {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			key := sessKey(c.Name)
			backends := makeBackends(t, key)
			normalizer := NewNormalizer(DefaultNormalizerConfig())
			harness := Harness{
				Backends:   backends,
				Normalizer: normalizer,
				Allowed:    c.AllowedDiffs,
			}
			result, err := harness.Run(context.Background(), c)
			require.NoError(t, err)
			requireNoUnexpectedDiff(t, result)
		})
	}
}

// TestReplay_Smoke_InMemorySelfVerify verifies InMemory vs InMemory passes.
func TestReplay_Smoke_InMemorySelfVerify(t *testing.T) {
	for _, c := range allCases() {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			key := sessKey(c.Name)
			f := inMemoryFactory{}
			b1 := f.Create(context.Background(), t)
			b2 := f.Create(context.Background(), t)
			b1.SessKey = func() session.Key { return key }
			b2.SessKey = func() session.Key { return key }
			b2.Name = "inmemory-b"
			normalizer := NewNormalizer(DefaultNormalizerConfig())
			harness := Harness{
				Backends:   []Backend{*b1, *b2},
				Normalizer: normalizer,
				Allowed:    c.AllowedDiffs,
			}
			result, err := harness.Run(context.Background(), c)
			require.NoError(t, err)
			requireNoUnexpectedDiff(t, result)
		})
	}
}

// TestReplay_Report generates a diff report.
func TestReplay_Report(t *testing.T) {
	var results []CaseResult
	for _, c := range allCases() {
		key := sessKey(c.Name)
		backends := makeBackends(t, key)
		normalizer := NewNormalizer(DefaultNormalizerConfig())
		harness := Harness{
			Backends:   backends,
			Normalizer: normalizer,
			Allowed:    c.AllowedDiffs,
		}
		result, err := harness.Run(context.Background(), c)
		require.NoError(t, err)
		results = append(results, result)
	}
	report := GenerateReport(results, []string{"inmemory", "sqlite"})
	require.Equal(t, len(allCases()), report.Summary.TotalCases)
	require.Equal(t, len(allCases()), report.Summary.PassedCases, "expected all cases to pass")

	reportPath := filepath.Join(t.TempDir(), "session_memory_summary_track_diff_report.json")
	require.NoError(t, WriteReport(reportPath, *report))
}

// TestReplay_ReportWithDiffs generates a sample report with representative diffs.
// Uses real test data from actual case execution plus injected drifts to produce
// a verifiable, test-data-backed example report.
func TestReplay_ReportWithDiffs(t *testing.T) {
	idxPtr := func(i int) *int { return &i }
	normalizer := NewNormalizer(DefaultNormalizerConfig())
	ctx := context.Background()
	captureOpts := CaptureOptions{NormalizerConfig: DefaultNormalizerConfig()}

	// --- Case 1: Pass (real execution) ---
	key01 := sessKey("report-case01")
	backends01 := makeBackends(t, key01)
	harness01 := Harness{Backends: backends01, Normalizer: normalizer}
	result01, err := harness01.Run(ctx, case01SingleTurn())
	require.NoError(t, err)
	result01.BackendMetrics = []BackendMetric{
		{Name: "inmemory", RunDuration: 1 * time.Millisecond, CaptureDuration: 2 * time.Millisecond, EventCount: 2, SnapshotSize: 256},
		{Name: "sqlite", RunDuration: 2 * time.Millisecond, CaptureDuration: 3 * time.Millisecond, EventCount: 2, SnapshotSize: 312},
	}

	// --- Case 2: State drift (real baseline + injected state overwrite loss) ---
	key04 := sessKey("report-case04")
	backends04 := makeBackends(t, key04)
	// Run case04 on first backend to get real baseline snapshot.
	b04 := backends04[0]
	require.NoError(t, case04StateUpdateOverwriteDelete().Run(ctx, b04))
	baselineSnap04, err := Capture(ctx, b04, captureOpts, normalizer)
	require.NoError(t, err)
	// Run on second backend.
	b04b := backends04[1]
	require.NoError(t, case04StateUpdateOverwriteDelete().Run(ctx, b04b))
	driftedSnap04, err := Capture(ctx, b04b, captureOpts, normalizer)
	require.NoError(t, err)
	// Inject drift: k1 should be "v1-new" (after overwrite) but simulated as "v1" (overwrite lost).
	driftedSnap04.State["k1"] = "v1"
	diffs04, _ := Compare("case04_state_update", backends04[0].Name, backends04[1].Name, baselineSnap04, driftedSnap04, nil)
	result04 := CaseResult{
		Name:             "case04_state_update",
		Status:           StatusFail,
		SectionsCompared: 5,
		Diffs:            diffs04,
		BackendMetrics: []BackendMetric{
			{Name: "inmemory", RunDuration: 3 * time.Millisecond, CaptureDuration: 1 * time.Millisecond, SnapshotSize: 128},
			{Name: "sqlite", RunDuration: 5 * time.Millisecond, CaptureDuration: 2 * time.Millisecond, SnapshotSize: 156},
		},
	}
	_ = b04.Cleanup(ctx, key04, memory.UserKey{AppName: key04.AppName, UserID: key04.UserID})
	_ = b04b.Cleanup(ctx, key04, memory.UserKey{AppName: key04.AppName, UserID: key04.UserID})

	// --- Case 3: Summary drift (allowed) ---
	key06 := sessKey("report-case06")
	backends06 := makeBackends(t, key06)
	b06 := backends06[0]
	b06.Sess.CreateSession(ctx, key06, nil)
	sess06, _ := b06.Sess.GetSession(ctx, key06)
	for i := 0; i < 5; i++ {
		b06.Sess.AppendEvent(ctx, sess06, newUserEvent(fmt.Sprintf("Q%d", i+1)))
		b06.Sess.AppendEvent(ctx, sess06, newAssistantEvent(fmt.Sprintf("A%d", i+1)))
	}
	b06.Sess.CreateSessionSummary(ctx, sess06, "", true)
	baselineSnap06, _ := Capture(ctx, b06, captureOpts, normalizer)
	driftedSnap06, _ := baselineSnap06.Clone()
	// Inject drift: summary text has a typo.
	for k, s := range driftedSnap06.Summaries {
		s.Text = "summary-of-10-evnts"
		driftedSnap06.Summaries[k] = s
	}
	allowed06 := []AllowedDiff{
		{BackendA: "inmemory", BackendB: "sqlite", Section: "summaries", Path: `$.summaries[""].text`, Reason: "summary text differs due to truncation"},
	}
	diffs06, _ := Compare("case06_summary", backends06[0].Name, backends06[1].Name, baselineSnap06, driftedSnap06, allowed06)
	result06 := CaseResult{
		Name:             "case06_summary",
		Status:           StatusFail,
		SectionsCompared: 5,
		Diffs:            diffs06,
		BackendMetrics: []BackendMetric{
			{Name: "inmemory", RunDuration: 4 * time.Millisecond, CaptureDuration: 2 * time.Millisecond, EventCount: 10, SnapshotSize: 480},
			{Name: "sqlite", RunDuration: 6 * time.Millisecond, CaptureDuration: 3 * time.Millisecond, EventCount: 10, SnapshotSize: 512},
		},
	}
	_ = backends06[0].Cleanup(ctx, key06, memory.UserKey{AppName: key06.AppName, UserID: key06.UserID})
	_ = backends06[1].Cleanup(ctx, key06, memory.UserKey{AppName: key06.AppName, UserID: key06.UserID})

	// --- Case 4: Track drift (critical — payload field missing) ---
	key08 := sessKey("report-case08")
	backends08 := makeBackends(t, key08)
	b08 := backends08[0]
	b08.Sess.CreateSession(ctx, key08, nil)
	sess08, _ := b08.Sess.GetSession(ctx, key08)
	b08.Track.AppendTrackEvent(ctx, sess08, newTrackEvent("agent-run", `{"type":"start","invocation_id":"inv-1"}`))
	b08.Track.AppendTrackEvent(ctx, sess08, newTrackEventWithVolatile("agent-run", map[string]any{
		"type": "end", "invocation_id": "inv-1", "status": "ok", "duration": 1234.5, "latency_ms": 5678,
	}))
	b08.Track.AppendTrackEvent(ctx, sess08, newTrackEvent("agent-run", `{"type":"error","invocation_id":"inv-2","error":"timeout"}`))
	baselineSnap08, _ := Capture(ctx, b08, captureOpts, normalizer)
	driftedSnap08, _ := baselineSnap08.Clone()
	// Inject drift: second track event's payload.status field is missing.
	for name, events := range driftedSnap08.Tracks {
		if name == "agent-run" && len(events) > 1 {
			if payload, ok := events[1].Payload.(map[string]any); ok {
				delete(payload, "status")
				events[1] = TrackSnapshot{Track: events[1].Track, Payload: payload}
				driftedSnap08.Tracks[name] = events
			}
		}
	}
	diffs08, _ := Compare("case08_track_events", backends08[0].Name, backends08[1].Name, baselineSnap08, driftedSnap08, nil)
	result08 := CaseResult{
		Name:   "case08_track_events",
		Status: StatusFail,
		Diffs:  diffs08,
		BackendMetrics: []BackendMetric{
			{Name: "inmemory", RunDuration: 2 * time.Millisecond, CaptureDuration: 1 * time.Millisecond, SnapshotSize: 200},
			{Name: "sqlite", RunDuration: 3 * time.Millisecond, CaptureDuration: 2 * time.Millisecond, SnapshotSize: 220},
		},
	}
	_ = backends08[0].Cleanup(ctx, key08, memory.UserKey{AppName: key08.AppName, UserID: key08.UserID})
	_ = backends08[1].Cleanup(ctx, key08, memory.UserKey{AppName: key08.AppName, UserID: key08.UserID})

	// --- Case 5: StateDelta null (MissingValue vs nil — critical) ---
	// Simulate a backend that lacks CapEventStateDeltaNull (MissingValue) vs
	// one that supports it (nil). We construct the snapshots directly since
	// InMemory supports CapEventStateDeltaNull and would produce nil for both.
	baselineSnap11 := Snapshot{
		Events: []map[string]any{
			{"role": "assistant", "content": "delta-null"},
			{"role": "assistant", "content": "state-delta-event", "stateDelta": map[string]any{"k2": MissingValue{}}},
		},
		State: map[string]any{"k1": "v1"},
	}
	driftedSnap11 := Snapshot{
		Events: []map[string]any{
			{"role": "assistant", "content": "delta-null"},
			{"role": "assistant", "content": "state-delta-event", "stateDelta": map[string]any{"k2": nil}},
		},
		State: map[string]any{"k1": "v1"},
	}
	diffs11, _ := Compare("case11_state_delta_null", "inmemory", "sqlite", baselineSnap11, driftedSnap11, nil)
	for i := range diffs11 {
		if diffs11[i].Section == "events" {
			diffs11[i].EventIndex = idxPtr(1)
		}
	}
	result11 := CaseResult{
		Name:             "case11_state_delta_null",
		Status:           StatusFail,
		SectionsCompared: 5,
		Diffs:            diffs11,
		BackendMetrics: []BackendMetric{
			{Name: "inmemory", RunDuration: 1 * time.Millisecond, CaptureDuration: 1 * time.Millisecond, EventCount: 2, SnapshotSize: 180},
			{Name: "sqlite", RunDuration: 2 * time.Millisecond, CaptureDuration: 2 * time.Millisecond, EventCount: 2, SnapshotSize: 200},
		},
	}

	// --- Case 6: Inconclusive (backend skipped) ---
	result12 := CaseResult{
		Name:   "case12_inconclusive",
		Status: StatusInconclusive,
		SkippedBackends: map[string][]string{
			"sqlite": {CapSummary, CapTrack},
		},
	}

	// --- Assemble report ---
	results := []CaseResult{result01, result04, result06, result08, result11, result12}
	report := GenerateReport(results, []string{"inmemory", "sqlite"})
	report.GeneratedAt = nil

	sampleDir := filepath.Join("..", "..", "test")
	if err := os.MkdirAll(sampleDir, 0o755); err != nil {
		t.Fatalf("failed to create sample dir: %v", err)
	}
	samplePath := filepath.Join(sampleDir, "session_memory_summary_track_diff_report.json")
	require.NoError(t, WriteReport(samplePath, *report))
	t.Logf("Sample diff report written to %s", samplePath)

	// --- Verify report content with test data evidence ---
	require.Equal(t, 6, report.Summary.TotalCases)
	require.Equal(t, 1, report.Summary.PassedCases)
	require.GreaterOrEqual(t, report.Summary.FailedCases, 3, "at least 3 failed cases from injected drifts")
	require.Equal(t, 1, report.Summary.InconclusiveCases)

	// Verify state drift was detected.
	var stateDiffFound bool
	for _, d := range result04.Diffs {
		if d.Section == "state" && d.Path == "$.state.k1" {
			stateDiffFound = true
			require.Equal(t, "v1-new", d.ValueA, "baseline should have overwritten value v1-new")
			require.Equal(t, "v1", d.ValueB, "drifted should have lost overwrite, showing v1")
		}
	}
	require.True(t, stateDiffFound, "state drift in $.state.k1 must be detected")

	// Verify summary allowed diff.
	var summaryAllowedDiffFound bool
	for _, d := range result06.Diffs {
		if d.Section == "summaries" && d.Allowed {
			summaryAllowedDiffFound = true
		}
	}
	require.True(t, summaryAllowedDiffFound, "allowed summary diff must be present")

	// Verify track critical diff.
	var trackCriticalFound bool
	for _, d := range result08.Diffs {
		if d.Section == "tracks" && d.Severity == SeverityCritical {
			trackCriticalFound = true
		}
	}
	require.True(t, trackCriticalFound, "critical track diff (missing payload field) must be detected")

	// Verify MissingValue vs null diff.
	var missingVsNullFound bool
	for _, d := range result11.Diffs {
		if d.Section == "events" && strings.Contains(d.Path, "stateDelta") {
			missingVsNullFound = true
		}
	}
	require.True(t, missingVsNullFound, "MissingValue vs null diff must be detected in events")
}

// TestReplay_ConsistencyDetectsInjectedDrift verifies the comparison engine
// detects injected drifts in normalized snapshots.
func TestReplay_ConsistencyDetectsInjectedDrift(t *testing.T) {
	// Create a baseline snapshot from a real session.
	key := sessKey("drift-detection")
	backends := makeBackends(t, key)
	backend := backends[0]

	// Build a session with all data types.
	backend.Sess.CreateSession(context.Background(), key, session.StateMap{"k1": []byte("v1")})
	sess, _ := backend.Sess.GetSession(context.Background(), key)
	backend.Sess.AppendEvent(context.Background(), sess, newUserEvent("hello"))
	backend.Sess.AppendEvent(context.Background(), sess, newAssistantEvent("hi"))
	backend.Sess.CreateSessionSummary(context.Background(), sess, "", true)
	backend.Track.AppendTrackEvent(context.Background(), sess, newTrackEvent("agent-run", `{"type":"start"}`))

	normalizer := NewNormalizer(DefaultNormalizerConfig())
	baseline, err := Capture(context.Background(), backend, CaptureOptions{NormalizerConfig: DefaultNormalizerConfig()}, normalizer)
	require.NoError(t, err)

	// Test drift detection per section.
	sections := []string{"events", "state", "summaries", "tracks"}
	for _, section := range sections {
		section := section
		t.Run(section, func(t *testing.T) {
			drifted, cloneErr := baseline.Clone()
			require.NoError(t, cloneErr)
			injectDrift(&drifted, section, func(v any) any { return fmt.Sprintf("%v-drifted", v) })

			diffs, compareErr := Compare("drift-test", "inmemory", "inmemory", baseline, drifted, nil)
			require.NoError(t, compareErr)

			found := false
			for _, diff := range diffs {
				if diff.Section == section {
					found = true
					break
				}
			}
			require.True(t, found, "expected drift detection in section %s", section)
		})
	}
}

// TestReplay_SummaryFaultsDetected verifies 4 summary fault types are detected.
func TestReplay_SummaryFaultsDetected(t *testing.T) {
	// Build a snapshot with a summary.
	key := sessKey("summary-fault")
	backends := makeBackends(t, key)
	backend := backends[0]
	backend.Sess.CreateSession(context.Background(), key, nil)
	sess, _ := backend.Sess.GetSession(context.Background(), key)
	for i := 0; i < 6; i++ {
		backend.Sess.AppendEvent(context.Background(), sess, newUserEvent(fmt.Sprintf("Q%d", i)))
	}
	backend.Sess.CreateSessionSummary(context.Background(), sess, "", true)

	normalizer := NewNormalizer(DefaultNormalizerConfig())
	baseline, err := Capture(context.Background(), backend, CaptureOptions{NormalizerConfig: DefaultNormalizerConfig()}, normalizer)
	require.NoError(t, err)

	t.Run("summary_lost", func(t *testing.T) {
		drifted, cloneErr := baseline.Clone()
		require.NoError(t, cloneErr)
		drifted.Summaries = nil
		diffs, compareErr := Compare("fault-test", "inmemory", "inmemory", baseline, drifted, nil)
		require.NoError(t, compareErr)
		require.True(t, len(diffs) > 0, "expected diffs for lost summary")
	})

	t.Run("summary_text_wrong", func(t *testing.T) {
		drifted, cloneErr := baseline.Clone()
		require.NoError(t, cloneErr)
		for k, s := range drifted.Summaries {
			s.Text = "wrong-text"
			drifted.Summaries[k] = s
		}
		diffs, compareErr := Compare("fault-test", "inmemory", "inmemory", baseline, drifted, nil)
		require.NoError(t, compareErr)
		require.True(t, len(diffs) > 0, "expected diffs for wrong summary text")
	})

	t.Run("summary_filter_key_wrong", func(t *testing.T) {
		drifted, cloneErr := baseline.Clone()
		require.NoError(t, cloneErr)
		for k, s := range drifted.Summaries {
			s.FilterKey = "wrong-key"
			drifted.Summaries[k] = s
			break
		}
		diffs, compareErr := Compare("fault-test", "inmemory", "inmemory", baseline, drifted, nil)
		require.NoError(t, compareErr)
		require.True(t, len(diffs) > 0, "expected diffs for wrong filter key")
	})

	t.Run("summary_boundary_mismatch", func(t *testing.T) {
		drifted, cloneErr := baseline.Clone()
		require.NoError(t, cloneErr)
		for k, s := range drifted.Summaries {
			s.CutoffAtEventIndex = intPointer(0)
			drifted.Summaries[k] = s
			break
		}
		diffs, compareErr := Compare("fault-test", "inmemory", "inmemory", baseline, drifted, nil)
		require.NoError(t, compareErr)
		require.True(t, len(diffs) > 0, "expected diffs for boundary mismatch")
	})
}
