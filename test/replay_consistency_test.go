//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package e2e

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	memsqlite "trpc.group/trpc-go/trpc-agent-go/memory/sqlite"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessinmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest"
	sesssqlite "trpc.group/trpc-go/trpc-agent-go/session/sqlite"
)

const replaySummaryFilter = "agent/tool"

type deterministicSummarizer struct{}

func (deterministicSummarizer) ShouldSummarize(*session.Session) bool { return true }

func (deterministicSummarizer) Summarize(_ context.Context, sess *session.Session) (string, error) {
	return fmt.Sprintf("summary of %d events", len(sess.Events)), nil
}

func (deterministicSummarizer) SetPrompt(string) {}

func (deterministicSummarizer) SetModel(model.Model) {}

func (deterministicSummarizer) Metadata() map[string]any {
	return map[string]any{"name": "replay-test"}
}

func TestReplayConsistencyMatrix(t *testing.T) {
	ctx := context.Background()
	var reports []replaytest.CaseReport
	for _, replayCase := range replayCases() {
		replayCase := replayCase
		t.Run(replayCase.Name, func(t *testing.T) {
			backends := newReplayBackends(t, replayCase.Name)
			harness := replaytest.Harness{Backends: backends, Normalizer: replaytest.DefaultNormalizer()}
			report, err := harness.Run(ctx, replayCase)
			require.NoError(t, err)
			require.False(t, replaytest.HasUnexpectedDiff(report), "diffs: %+v", report.Diffs)
			reports = append(reports, report)
		})
	}
	if path := os.Getenv("TRPC_AGENT_REPLAY_REPORT_PATH"); path != "" {
		require.NoError(t, replaytest.WriteReport(path, replaytest.Report{Cases: reports}))
	}
}

func TestReplayConsistencyDetectsInjectedDrift(t *testing.T) {
	ctx := context.Background()
	backends := newReplayBackends(t, "injected-drift")
	for _, backend := range backends {
		require.NoError(t, replayDriftFixture(ctx, backend))
	}
	baseline, err := replaytest.Capture(ctx, backends[0], replaytest.CaptureOptions{})
	require.NoError(t, err)
	healthy, err := replaytest.Capture(ctx, backends[1], replaytest.CaptureOptions{})
	require.NoError(t, err)
	healthyDiffs, err := replaytest.Compare("healthy", "inmemory", "sqlite", baseline, healthy, nil)
	require.NoError(t, err)
	require.Empty(t, healthyDiffs)

	tests := []struct {
		name   string
		mutate func(*replaytest.Snapshot)
	}{
		{"event content", func(s *replaytest.Snapshot) { s.Events[0]["author"] = "other" }},
		{"event loss", func(s *replaytest.Snapshot) { s.Events = nil }},
		{"event duplication", func(s *replaytest.Snapshot) { s.Events = append(s.Events, s.Events[0]) }},
		{"state pollution", func(s *replaytest.Snapshot) { s.State["dirty"] = true }},
		{"state deletion", func(s *replaytest.Snapshot) { delete(s.State, "counter") }},
		{"state null", func(s *replaytest.Snapshot) { s.State["counter"] = nil }},
		{"memory content", func(s *replaytest.Snapshot) { s.Memories[0].Content = "changed" }},
		{"memory duplication", func(s *replaytest.Snapshot) { s.Memories = append(s.Memories, s.Memories[0]) }},
		{"memory scope", func(s *replaytest.Snapshot) { s.Memories[0].UserID = "other-user" }},
		{"memory rank", func(s *replaytest.Snapshot) {
			s.Memories[0], s.Memories[1] = s.Memories[1], s.Memories[0]
		}},
		{"memory score", func(s *replaytest.Snapshot) { s.Memories[0].Score = 0.75 }},
		{"summary loss", func(s *replaytest.Snapshot) { delete(s.Summaries, replaySummaryFilter) }},
		{"summary overwrite", func(s *replaytest.Snapshot) {
			item := s.Summaries[replaySummaryFilter]
			item.Text = "stale summary"
			s.Summaries[replaySummaryFilter] = item
		}},
		{"summary ownership", func(s *replaytest.Snapshot) {
			item := s.Summaries[replaySummaryFilter]
			item.SessionID = "wrong-session"
			s.Summaries[replaySummaryFilter] = item
		}},
		{"summary filter key", func(s *replaytest.Snapshot) {
			item := s.Summaries[replaySummaryFilter]
			item.FilterKey = "wrong"
			s.Summaries[replaySummaryFilter] = item
		}},
		{"summary boundary filter key", func(s *replaytest.Snapshot) {
			item := s.Summaries[replaySummaryFilter]
			item.BoundaryFilterKey = "wrong"
			s.Summaries[replaySummaryFilter] = item
		}},
		{"summary version", func(s *replaytest.Snapshot) {
			item := s.Summaries[replaySummaryFilter]
			item.Version++
			s.Summaries[replaySummaryFilter] = item
		}},
		{"summary update time", func(s *replaytest.Snapshot) {
			item := s.Summaries[replaySummaryFilter]
			item.UpdatedAtEventIndex = replayInt(-1)
			s.Summaries[replaySummaryFilter] = item
		}},
		{"summary boundary", func(s *replaytest.Snapshot) {
			item := s.Summaries[replaySummaryFilter]
			item.LastEventIndex = replayInt(0)
			s.Summaries[replaySummaryFilter] = item
		}},
		{"track payload", func(s *replaytest.Snapshot) {
			s.Tracks["tool"][0].Payload = map[string]any{"status": "failed"}
		}},
		{"track ownership", func(s *replaytest.Snapshot) { s.Tracks["tool"][0].Track = "other" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			compared := cloneReplaySnapshot(t, healthy)
			test.mutate(&compared)
			diffs, err := replaytest.Compare(test.name, "inmemory", "injected", baseline, compared, nil)
			require.NoError(t, err)
			require.NotEmpty(t, diffs)
			require.False(t, diffs[0].Allowed)
		})
	}
}

func TestReplayConsistencyEveryCaseDetectsInjectedBackendDrift(t *testing.T) {
	ctx := context.Background()
	for _, replayCase := range replayCases() {
		replayCase := replayCase
		t.Run(replayCase.Name, func(t *testing.T) {
			backends := newReplayBackends(t, replayCase.Name)
			originalLoad := backends[1].Load
			backends[1].Load = func(
				ctx context.Context,
				backend replaytest.Backend,
			) (*session.Session, []*memory.Entry, error) {
				sess, memories, err := loadReplayForInjection(ctx, backend, originalLoad)
				if err != nil {
					return nil, nil, err
				}
				if err := injectReplayCaseDrift(replayCase.Name, sess, &memories); err != nil {
					return nil, nil, err
				}
				return sess, memories, nil
			}
			report, err := (replaytest.Harness{
				Backends: backends, Normalizer: replaytest.DefaultNormalizer(),
			}).Run(ctx, replayCase)
			require.NoError(t, err)
			require.True(t, replaytest.HasUnexpectedDiff(report))
			require.NotEmpty(t, report.Diffs)
			assertExpectedReplayCaseDrift(t, replayCase.Name, report.Diffs)
		})
	}
}

func loadReplayForInjection(
	ctx context.Context,
	backend replaytest.Backend,
	originalLoad func(context.Context, replaytest.Backend) (*session.Session, []*memory.Entry, error),
) (*session.Session, []*memory.Entry, error) {
	if originalLoad != nil {
		return originalLoad(ctx, backend)
	}
	sess, err := backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return nil, nil, err
	}
	var memories []*memory.Entry
	if replaytest.Supports(backend, replaytest.CapabilityMemory) {
		memories, err = backend.Memory.ReadMemories(ctx, replayMemoryUser(backend), 0)
		if err != nil {
			return nil, nil, err
		}
	}
	return sess, memories, nil
}

func injectReplayCaseDrift(
	caseName string,
	sess *session.Session,
	memories *[]*memory.Entry,
) error {
	switch caseName {
	case "single_turn":
		if len(sess.Events) == 0 {
			return fmt.Errorf("single-turn injection found no events")
		}
		sess.Events[0].Author = "injected-author"
	case "multi_turn":
		if len(sess.Events) < 2 {
			return fmt.Errorf("event-loss injection found too few events")
		}
		sess.Events = sess.Events[:len(sess.Events)-1]
	case "concurrent_tool_event_interleaving":
		for i := range sess.Events {
			if sess.Events[i].Author != "tool-5" {
				continue
			}
			sess.Events[i].Author = "injected-author"
			return nil
		}
		return fmt.Errorf("concurrent injection found no tool-5 event")
	case "tool_call_and_response":
		for i := range sess.Events {
			if len(sess.Events[i].GetToolCallIDs()) == 0 {
				continue
			}
			sess.Events[i].Response.Choices[0].Message.ToolCalls[0].Function.Arguments =
				[]byte(`{"city":"wrong"}`)
			return nil
		}
		return fmt.Errorf("tool injection found no tool call")
	case "state_update_overwrite_delete":
		sess.State["counter"] = []byte(`99`)
	case "session_state_direct_round_trip":
		sess.State["phase"] = []byte(`"injected"`)
	case "memory_search_order_and_score":
		if len(*memories) == 0 {
			return fmt.Errorf("memory score injection found no results")
		}
		(*memories)[0].Score += 0.25
	case "memory_update_and_delete":
		if len(*memories) == 0 || (*memories)[0].Memory == nil {
			return fmt.Errorf("memory content injection found no result")
		}
		(*memories)[0].Memory.Memory = "injected memory"
	case "summary_filter_and_update":
		summary := sess.Summaries[replaySummaryFilter]
		if summary == nil {
			return fmt.Errorf("summary injection found no filter-key summary")
		}
		summary.Summary = "injected summary"
	case "summary_event_window_recovery":
		summary := sess.Summaries[""]
		if summary == nil {
			return fmt.Errorf("summary injection found no full-session summary")
		}
		summary.Summary = "injected summary"
	case "track_status_and_error":
		history := sess.Tracks["tool"]
		if history == nil || len(history.Events) == 0 {
			return fmt.Errorf("track injection found no events")
		}
		history.Events[0].Payload = json.RawMessage(`{"type":"injected"}`)
	case "failure_recovery_without_duplicates":
		if len(*memories) == 0 {
			return fmt.Errorf("recovery injection found no memory")
		}
		*memories = append(*memories, (*memories)[0])
	default:
		return fmt.Errorf("no drift injection for replay case %q", caseName)
	}
	return nil
}

func assertExpectedReplayCaseDrift(
	t *testing.T,
	caseName string,
	diffs []replaytest.Diff,
) {
	t.Helper()
	expected := map[string]replaytest.Diff{
		"single_turn": {
			Section: "events", Path: "$.events[0].author", EventIndex: replayInt(0),
		},
		"multi_turn": {
			Section: "events", Path: "$.events[5]", EventIndex: replayInt(5),
		},
		"tool_call_and_response": {
			Section:    "events",
			Path:       "$.events[1].choices[0].message.tool_calls[0].function.arguments.city",
			EventIndex: replayInt(1),
		},
		"state_update_overwrite_delete": {
			Section: "state", Path: "$.state.counter",
		},
		"session_state_direct_round_trip": {
			Section: "state", Path: "$.state.phase",
		},
		"memory_search_order_and_score": {
			Section: "memories", Path: "$.memories[0].score", MemoryID: "memory-000",
		},
		"memory_update_and_delete": {
			Section: "memories", Path: "$.memories[0].content", MemoryID: "memory-000",
		},
		"summary_filter_and_update": {
			Section: "summaries", Path: `$.summaries["agent/tool"].text`,
			SummaryKey: replayString(replaySummaryFilter),
		},
		"summary_event_window_recovery": {
			Section: "summaries", Path: `$.summaries[""].text`,
			SummaryKey: replayString(""),
		},
		"track_status_and_error": {
			Section: "tracks", Path: "$.tracks.tool[0].payload.type", TrackName: "tool",
		},
		"concurrent_tool_event_interleaving": {
			Section: "events", Path: "$.events[6].author", EventIndex: replayInt(6),
		},
		"failure_recovery_without_duplicates": {
			Section: "memories", Path: "$.memories[1]", MemoryID: "memory-001",
		},
	}[caseName]
	if expected.Section == "" {
		t.Fatalf("no expected drift registered for replay case %q", caseName)
	}
	diff := requireReplayDiff(t, diffs, expected.Section, expected.Path)
	require.False(t, diff.Allowed)
	require.Equal(t, expected.EventIndex, diff.EventIndex)
	require.Equal(t, expected.MemoryID, diff.MemoryID)
	require.Equal(t, expected.SummaryKey, diff.SummaryKey)
	require.Equal(t, expected.TrackName, diff.TrackName)
}

func requireReplayDiff(
	t *testing.T,
	diffs []replaytest.Diff,
	section string,
	path string,
) replaytest.Diff {
	t.Helper()
	for _, diff := range diffs {
		if diff.Section == section && diff.Path == path {
			return diff
		}
	}
	t.Fatalf("missing %s diff at %s: %+v", section, path, diffs)
	return replaytest.Diff{}
}

func TestReplaySummaryBackendFaultsAreDetectedAfterNormalization(t *testing.T) {
	faults := []struct {
		name       string
		path       string
		filterKey  string
		comparedID string
		mutate     func(*session.Session)
	}{
		{"missing", `$.summaries["agent/tool"]`, replaySummaryFilter, "", func(sess *session.Session) {
			delete(sess.Summaries, replaySummaryFilter)
		}},
		{"overwrite", `$.summaries["agent/tool"].text`, replaySummaryFilter, "", func(sess *session.Session) {
			sess.Summaries[replaySummaryFilter].Summary = "stale summary"
		}},
		{"wrong session ownership", `$.summaries["agent/tool"].session_id`, replaySummaryFilter, "wrong-session", func(sess *session.Session) {
			sess.ID = "wrong-session"
		}},
		{"wrong filter key", `$.summaries["agent/tool"]`, replaySummaryFilter, "", func(sess *session.Session) {
			summary := sess.Summaries[replaySummaryFilter]
			delete(sess.Summaries, replaySummaryFilter)
			sess.Summaries["wrong/filter"] = summary
		}},
	}
	for _, fault := range faults {
		fault := fault
		t.Run(fault.name, func(t *testing.T) {
			ctx := context.Background()
			backends := newReplayBackends(t, "summary-fault-"+strings.ReplaceAll(fault.name, " ", "-"))
			for _, backend := range backends {
				require.NoError(t, replaySummaryUpdate(ctx, backend))
			}
			baseline, err := replaytest.Capture(ctx, backends[0], replaytest.CaptureOptions{})
			require.NoError(t, err)
			injected := backends[1]
			injected.Load = func(
				ctx context.Context,
				backend replaytest.Backend,
			) (*session.Session, []*memory.Entry, error) {
				sess, memories, err := loadReplayForInjection(ctx, backend, nil)
				if err != nil {
					return nil, nil, err
				}
				fault.mutate(sess)
				return sess, memories, nil
			}
			compared, err := replaytest.Capture(ctx, injected, replaytest.CaptureOptions{})
			require.NoError(t, err)
			diffs, err := replaytest.Compare(fault.name, "inmemory", "sqlite", baseline, compared, nil)
			require.NoError(t, err)
			require.NotEmpty(t, diffs)
			diff := requireReplayDiff(t, diffs, "summaries", fault.path)
			require.False(t, diff.Allowed)
			require.NotNil(t, diff.SummaryKey)
			require.Equal(t, fault.filterKey, *diff.SummaryKey)
			if fault.comparedID != "" {
				require.Equal(t, fault.comparedID, diff.Compared)
			}
		})
	}
}

func TestReplayConsistencySkipsAllowedUnsupportedSections(t *testing.T) {
	tests := []struct {
		capability string
		replayCase replaytest.Case
	}{
		{replaytest.CapabilityMemory, replaytest.Case{
			Name: "unsupported-memory", RequiredCapabilities: []string{replaytest.CapabilityMemory}, Run: replayMemoryLifecycle,
		}},
		{replaytest.CapabilitySummary, replaytest.Case{
			Name: "unsupported-summary", RequiredCapabilities: []string{replaytest.CapabilitySummary}, Run: replaySummaryUpdate,
		}},
		{replaytest.CapabilityTracks, replaytest.Case{
			Name: "unsupported-tracks", RequiredCapabilities: []string{replaytest.CapabilityTracks}, Run: replayTracks,
		}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.capability, func(t *testing.T) {
			backends := newReplayBackends(t, test.replayCase.Name)
			capabilities := make(map[string]replaytest.Capability, len(backends[1].Capabilities))
			for name, capability := range backends[1].Capabilities {
				capabilities[name] = capability
			}
			capabilities[test.capability] = replaytest.Capability{
				Supported: false, Reason: "not implemented by this fixture", AllowedDiff: true,
			}
			backends[1].Capabilities = capabilities
			if test.capability == replaytest.CapabilityMemory {
				backends[1].Memory = nil
			}
			report, err := (replaytest.Harness{Backends: backends}).Run(
				context.Background(), test.replayCase,
			)
			require.NoError(t, err)
			require.True(t, report.Inconclusive)
			require.True(t, replaytest.HasUnexpectedDiff(report))
			require.False(t, report.Capabilities["sqlite"][test.capability].Supported)
			require.True(t, report.Capabilities["sqlite"][test.capability].AllowedDiff)
			require.Equal(t, []string{test.capability}, report.SkippedBackends["sqlite"])
		})
	}
}

func TestValidateAllowedReplaySkips(t *testing.T) {
	allowed := replaytest.CaseReport{
		SkippedBackends: map[string][]string{"candidate": {replaytest.CapabilityTracks}},
		Capabilities: map[string]map[string]replaytest.Capability{
			"candidate": {
				replaytest.CapabilityTracks: {
					Supported: false, Reason: "not implemented", AllowedDiff: true,
				},
			},
		},
	}
	require.NoError(t, validateAllowedReplaySkips(allowed))

	disallowed := allowed
	disallowed.Capabilities = map[string]map[string]replaytest.Capability{
		"candidate": {
			replaytest.CapabilityTracks: {Supported: false, Reason: "not implemented"},
		},
	}
	require.ErrorContains(t, validateAllowedReplaySkips(disallowed), "skipped disallowed capability")
	require.ErrorContains(t, validateAllowedReplaySkips(replaytest.CaseReport{}), "no skipped backends")
}

func TestReplayClickHouseCapabilities(t *testing.T) {
	capabilities := replayClickHouseCapabilities()
	for _, name := range []string{
		replaytest.CapabilityEvents,
		replaytest.CapabilityEventStateDeltaNull,
		replaytest.CapabilitySummary,
		replaytest.CapabilityTracks,
	} {
		capability, exists := capabilities[name]
		require.True(t, exists)
		require.False(t, capability.Supported)
		require.NotEmpty(t, capability.Reason)
		require.True(t, capability.AllowedDiff)
	}
	require.True(t, capabilities[replaytest.CapabilityState].Supported)
	require.True(t, capabilities[replaytest.CapabilityMemory].Supported)
}

func TestReplayConsistencyAllowedDiffAndReport(t *testing.T) {
	left := replayFixtureSnapshot()
	right := cloneReplaySnapshot(t, left)
	right.Tracks["tool"][0].Payload = map[string]any{"status": "ok", "backend_note": "sqlite"}
	rules := []replaytest.AllowedDiff{{
		Section: "tracks", Path: "$.tracks.tool[0].payload.backend_note", BackendA: "inmemory", BackendB: "sqlite",
		Reason: "SQLite exposes a backend-only diagnostic note",
	}}
	diffs, err := replaytest.Compare("allowed", "inmemory", "sqlite", left, right, rules)
	require.NoError(t, err)
	require.Len(t, diffs, 1)
	require.True(t, diffs[0].Allowed)

	path := filepath.Join(t.TempDir(), "report.json")
	err = replaytest.WriteReport(path, replaytest.Report{Cases: []replaytest.CaseReport{{
		Name: "allowed", SessionID: left.SessionID, Backends: []string{"inmemory", "sqlite"}, Diffs: diffs,
	}}})
	require.NoError(t, err)
	var report replaytest.Report
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &report))
	require.Equal(t, 1, report.Version)
	require.Equal(t, "tracks", report.Cases[0].Diffs[0].Section)
}

func replayCases() []replaytest.Case {
	return []replaytest.Case{
		{Name: "single_turn", RequiredCapabilities: []string{replaytest.CapabilityEvents}, Run: func(ctx context.Context, backend replaytest.Backend) error {
			sess, err := createReplaySession(ctx, backend, nil)
			if err != nil {
				return err
			}
			return appendReplayMessages(ctx, backend, sess, "hello", "hi")
		}},
		{Name: "multi_turn", RequiredCapabilities: []string{replaytest.CapabilityEvents}, Run: func(ctx context.Context, backend replaytest.Backend) error {
			sess, err := createReplaySession(ctx, backend, nil)
			if err != nil {
				return err
			}
			for _, turn := range [][2]string{{"one", "first"}, {"two", "second"}, {"three", "third"}} {
				if err := appendReplayMessages(ctx, backend, sess, turn[0], turn[1]); err != nil {
					return err
				}
			}
			return nil
		}},
		{Name: "tool_call_and_response", RequiredCapabilities: []string{replaytest.CapabilityEvents}, Run: replayToolCall},
		{Name: "state_update_overwrite_delete", RequiredCapabilities: []string{
			replaytest.CapabilityEvents, replaytest.CapabilityState,
		}, Run: replayStateLifecycle},
		{Name: "session_state_direct_round_trip", RequiredCapabilities: []string{
			replaytest.CapabilityState,
		}, Run: replayDirectStateRoundTrip},
		{Name: "memory_search_order_and_score", RequiredCapabilities: []string{replaytest.CapabilityMemory}, Run: replayMemoryKinds},
		{Name: "memory_update_and_delete", RequiredCapabilities: []string{replaytest.CapabilityMemory}, Run: replayMemoryLifecycle},
		{Name: "summary_filter_and_update", RequiredCapabilities: []string{
			replaytest.CapabilityEvents, replaytest.CapabilitySummary,
		}, Run: replaySummaryUpdate},
		{Name: "summary_event_window_recovery", RequiredCapabilities: []string{
			replaytest.CapabilityEvents, replaytest.CapabilitySummary,
		}, Run: replaySummaryFollowup},
		{Name: "track_status_and_error", RequiredCapabilities: []string{replaytest.CapabilityTracks}, Run: replayTracks},
		{
			Name: "concurrent_tool_event_interleaving", Run: replayConcurrentToolEvents,
			RequiredCapabilities: []string{replaytest.CapabilityEvents}, OrderEventsByTimestamp: true,
		},
		{Name: "failure_recovery_without_duplicates", RequiredCapabilities: []string{
			replaytest.CapabilityEvents, replaytest.CapabilityState,
			replaytest.CapabilityMemory, replaytest.CapabilitySummary,
		}, Run: replayFailureRecovery},
	}
}

func replayToolCall(ctx context.Context, backend replaytest.Backend) error {
	sess, err := createReplaySession(ctx, backend, nil)
	if err != nil {
		return err
	}
	user := replayEvent("user", model.RoleUser, "weather in Shenzhen")
	user.FilterKey = replaySummaryFilter
	if err := backend.Session.AppendEvent(ctx, sess, user); err != nil {
		return err
	}
	call := replayEvent("assistant", model.RoleAssistant, "")
	call.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
		Type: "function", ID: "call-weather", Function: model.FunctionDefinitionParam{Name: "weather", Arguments: []byte(`{"city":"Shenzhen"}`)},
	}}
	call.FilterKey = replaySummaryFilter
	if err := backend.Session.AppendEvent(ctx, sess, call); err != nil {
		return err
	}
	result := replayEvent("tool", model.RoleTool, `{"temperature":30}`)
	result.Response.Choices[0].Message.ToolID = "call-weather"
	result.Response.Choices[0].Message.ToolName = "weather"
	result.FilterKey = replaySummaryFilter
	if err := event.SetExtension(result, event.ToolCallArgsExtensionKey, map[string]any{"call-weather": map[string]any{"city": "Shenzhen"}}); err != nil {
		return err
	}
	return backend.Session.AppendEvent(ctx, sess, result)
}

func replayStateLifecycle(ctx context.Context, backend replaytest.Backend) error {
	sess, err := createReplaySession(ctx, backend, session.StateMap{"counter": []byte(`1`), "remove_me": []byte(`true`)})
	if err != nil {
		return err
	}
	if !replaytest.Supports(backend, replaytest.CapabilityState) {
		return nil
	}
	if err := backend.Session.UpdateAppState(ctx, backend.SessionKey.AppName, session.StateMap{"temporary": []byte(`true`)}); err != nil {
		return err
	}
	appState, err := backend.Session.ListAppStates(ctx, backend.SessionKey.AppName)
	if err != nil || string(appState["temporary"]) != "true" {
		return fmt.Errorf("app state update was not observable: %w", err)
	}
	if err := backend.Session.DeleteAppState(ctx, backend.SessionKey.AppName, "temporary"); err != nil {
		return err
	}
	appState, err = backend.Session.ListAppStates(ctx, backend.SessionKey.AppName)
	if err != nil {
		return err
	}
	if _, exists := appState["temporary"]; exists {
		return fmt.Errorf("app state delete left temporary key")
	}
	userKey := session.UserKey{AppName: backend.SessionKey.AppName, UserID: backend.SessionKey.UserID}
	if err := backend.Session.UpdateUserState(ctx, userKey, session.StateMap{"temporary": []byte(`true`)}); err != nil {
		return err
	}
	userState, err := backend.Session.ListUserStates(ctx, userKey)
	if err != nil || string(userState["temporary"]) != "true" {
		return fmt.Errorf("user state update was not observable: %w", err)
	}
	if err := backend.Session.DeleteUserState(ctx, userKey, "temporary"); err != nil {
		return err
	}
	userState, err = backend.Session.ListUserStates(ctx, userKey)
	if err != nil {
		return err
	}
	if _, exists := userState["temporary"]; exists {
		return fmt.Errorf("user state delete left temporary key")
	}
	if err := backend.Session.UpdateSessionState(ctx, backend.SessionKey, session.StateMap{"counter": []byte(`2`), "nested": []byte(`{"b":2,"a":1}`)}); err != nil {
		return err
	}
	stored, err := backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil || string(stored.State["counter"]) != "2" {
		return fmt.Errorf("session state overwrite was not observable: %w", err)
	}
	change := replayEvent("assistant", model.RoleAssistant, "state changed")
	change.StateDelta = session.StateMap{"counter": []byte(`3`), "remove_me": nil}
	if err := backend.Session.AppendEvent(ctx, sess, change); err != nil {
		return err
	}
	stored, err = backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	if string(stored.State["counter"]) != "3" {
		return fmt.Errorf("event state delta was not applied: %v", stored.State)
	}
	removeMe, exists := stored.State["remove_me"]
	if !exists {
		return fmt.Errorf("event state delta dropped explicit null: %v", stored.State)
	}
	if replaytest.Supports(backend, replaytest.CapabilityEventStateDeltaNull) {
		if removeMe != nil {
			return fmt.Errorf("event state delta null was not applied: %v", stored.State)
		}
	} else if string(removeMe) != "true" {
		return fmt.Errorf("unsupported event state delta null did not preserve the previous value: %v", stored.State)
	}
	return nil
}

func replayDirectStateRoundTrip(ctx context.Context, backend replaytest.Backend) error {
	if _, err := createReplaySession(ctx, backend, session.StateMap{
		"phase": []byte(`"created"`), "counter": []byte(`1`),
	}); err != nil {
		return err
	}
	if !replaytest.Supports(backend, replaytest.CapabilityState) {
		return nil
	}
	if err := backend.Session.UpdateSessionState(ctx, backend.SessionKey, session.StateMap{
		"phase": []byte(`"updated"`), "counter": []byte(`2`),
		"nested": []byte(`{"enabled":true,"retries":3}`),
	}); err != nil {
		return err
	}
	stored, err := backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	if string(stored.State["phase"]) != `"updated"` ||
		string(stored.State["counter"]) != "2" ||
		string(stored.State["nested"]) == "" {
		return fmt.Errorf("direct session state round-trip is inconsistent: %v", stored.State)
	}
	return nil
}

func replayMemoryKinds(ctx context.Context, backend replaytest.Backend) error {
	if _, err := createReplaySession(ctx, backend, nil); err != nil {
		return err
	}
	if !replaytest.Supports(backend, replaytest.CapabilityMemory) {
		return nil
	}
	user := replayMemoryUser(backend)
	if err := backend.Memory.AddMemory(ctx, user, "prefers concise answers", []string{"preference"}, memory.WithMetadata(&memory.Metadata{Kind: memory.KindFact})); err != nil {
		return err
	}
	eventTime := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	if err := backend.Memory.AddMemory(ctx, user, "visited Shenzhen", []string{"travel"}, memory.WithMetadata(&memory.Metadata{
		Kind: memory.KindEpisode, EventTime: &eventTime, Participants: []string{"user", "Alex"}, Location: "Shenzhen",
	})); err != nil {
		return err
	}
	if err := backend.Memory.AddMemory(ctx, user, "plans another Shenzhen trip", []string{"travel"}); err != nil {
		return err
	}
	entries, err := backend.Memory.SearchMemories(ctx, user, "Shenzhen")
	if err != nil {
		return err
	}
	if len(entries) < 2 {
		return fmt.Errorf("memory search returned %d entries, want at least 2", len(entries))
	}
	return nil
}

func replayMemoryLifecycle(ctx context.Context, backend replaytest.Backend) error {
	if _, err := createReplaySession(ctx, backend, nil); err != nil {
		return err
	}
	if !replaytest.Supports(backend, replaytest.CapabilityMemory) {
		return nil
	}
	user := replayMemoryUser(backend)
	if err := backend.Memory.AddMemory(ctx, user, "draft preference", []string{"draft"}); err != nil {
		return err
	}
	if err := backend.Memory.AddMemory(ctx, user, "temporary fact", []string{"temp"}); err != nil {
		return err
	}
	entries, err := backend.Memory.ReadMemories(ctx, user, 0)
	if err != nil {
		return err
	}
	if len(entries) != 2 {
		return fmt.Errorf("memory setup stored %d entries, want 2", len(entries))
	}
	updated, deleted := false, false
	for _, entry := range entries {
		key := memory.Key{AppName: user.AppName, UserID: user.UserID, MemoryID: entry.ID}
		switch entry.Memory.Memory {
		case "draft preference":
			if err := backend.Memory.UpdateMemory(ctx, key, "final preference", []string{"preference"}); err != nil {
				return err
			}
			updated = true
		case "temporary fact":
			if err := backend.Memory.DeleteMemory(ctx, key); err != nil {
				return err
			}
			deleted = true
		}
	}
	if !updated || !deleted {
		return fmt.Errorf("memory lifecycle did not find both setup entries")
	}
	entries, err = backend.Memory.ReadMemories(ctx, user, 0)
	if err != nil {
		return err
	}
	if len(entries) != 1 || entries[0].Memory == nil ||
		entries[0].Memory.Memory != "final preference" {
		return fmt.Errorf("memory lifecycle final entries are inconsistent: %v", entries)
	}
	return nil
}

func replaySummaryUpdate(ctx context.Context, backend replaytest.Backend) error {
	sess, err := createReplaySession(ctx, backend, nil)
	if err != nil {
		return err
	}
	for i, content := range []string{"start", "tool work", "result"} {
		author, role := "assistant", model.RoleAssistant
		if i == 0 {
			author, role = "user", model.RoleUser
		}
		evt := replayEvent(author, role, content)
		evt.FilterKey = replaySummaryFilter
		if err := backend.Session.AppendEvent(ctx, sess, evt); err != nil {
			return err
		}
	}
	summarySupported := replaytest.Supports(backend, replaytest.CapabilitySummary)
	sess, err = backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	var firstUpdatedAt time.Time
	var firstText string
	if summarySupported {
		if err := backend.Session.CreateSessionSummary(ctx, sess, replaySummaryFilter, true); err != nil {
			return err
		}
		sess, err = backend.Session.GetSession(ctx, backend.SessionKey)
		if err != nil {
			return err
		}
		firstSummary := sess.Summaries[replaySummaryFilter]
		if firstSummary == nil || firstSummary.Summary != "summary of 3 events" ||
			firstSummary.Boundary == nil || firstSummary.Boundary.FilterKey != replaySummaryFilter {
			return fmt.Errorf("first summary did not preserve filter and boundary: %v", firstSummary)
		}
		firstUpdatedAt = firstSummary.UpdatedAt
		firstText = firstSummary.Summary
	}
	evt := replayEvent("assistant", model.RoleAssistant, "updated result")
	evt.FilterKey = replaySummaryFilter
	if err := backend.Session.AppendEvent(ctx, sess, evt); err != nil {
		return err
	}
	if !summarySupported {
		return nil
	}
	sess, err = backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	if err := backend.Session.CreateSessionSummary(ctx, sess, replaySummaryFilter, true); err != nil {
		return err
	}
	sess, err = backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	updatedSummary := sess.Summaries[replaySummaryFilter]
	if updatedSummary == nil || updatedSummary.Summary == "" || updatedSummary.Summary == firstText ||
		updatedSummary.UpdatedAt.Before(firstUpdatedAt) {
		return fmt.Errorf("updated summary did not overwrite the first summary: %v", updatedSummary)
	}
	return nil
}

func replaySummaryFollowup(ctx context.Context, backend replaytest.Backend) error {
	sess, err := createReplaySession(ctx, backend, nil)
	if err != nil {
		return err
	}
	for i := 0; i < 12; i++ {
		author, role := "assistant", model.RoleAssistant
		if i == 0 {
			author, role = "user", model.RoleUser
		}
		if err := backend.Session.AppendEvent(ctx, sess, replayEvent(author, role, fmt.Sprintf("history-%02d", i))); err != nil {
			return err
		}
	}
	if !replaytest.Supports(backend, replaytest.CapabilitySummary) {
		return appendReplayMessages(ctx, backend, sess, "follow up", "answer after summary")
	}
	sess, err = backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	if err := backend.Session.CreateSessionSummary(ctx, sess, "", true); err != nil {
		return err
	}
	sess, err = backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	return appendReplayMessages(ctx, backend, sess, "follow up", "answer after summary")
}

func replayTracks(ctx context.Context, backend replaytest.Backend) error {
	sess, err := createReplaySession(ctx, backend, nil)
	if err != nil {
		return err
	}
	if !replaytest.Supports(backend, replaytest.CapabilityTracks) {
		return nil
	}
	trackService, ok := backend.Session.(session.TrackService)
	if !ok {
		return fmt.Errorf("backend %s does not support tracks", backend.Name)
	}
	for _, payload := range []string{`{"type":"started","invocation":"invocation-main","duration_ms":3}`, `{"type":"failed","error":"timeout","duration_ms":9}`} {
		if err := trackService.AppendTrackEvent(ctx, sess, &session.TrackEvent{Track: "tool", Payload: json.RawMessage(payload), Timestamp: time.Now()}); err != nil {
			return err
		}
	}
	stored, err := backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	history := stored.Tracks["tool"]
	if history == nil || len(history.Events) != 2 {
		return fmt.Errorf("track replay stored an invalid history: %v", history)
	}
	return nil
}

func replayConcurrentToolEvents(ctx context.Context, backend replaytest.Backend) error {
	sess, err := createReplaySession(ctx, backend, nil)
	if err != nil {
		return err
	}
	baseTime := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	userEvent := replayEvent("user", model.RoleUser, "run tools")
	userEvent.Timestamp = baseTime.Add(-time.Second)
	if err := backend.Session.AppendEvent(ctx, sess, userEvent); err != nil {
		return err
	}
	const workers = 6
	start := make(chan struct{})
	ready := make(chan struct{}, workers)
	results := make(chan error, workers)
	sessionCopies := make([]*session.Session, workers)
	for i := range sessionCopies {
		sessionCopies[i] = sess.Clone()
	}
	var group sync.WaitGroup
	for i := 0; i < workers; i++ {
		index := i
		group.Add(1)
		go func() {
			defer group.Done()
			ready <- struct{}{}
			<-start
			evt := replayEvent(fmt.Sprintf("tool-%d", index), model.RoleAssistant, "")
			evt.InvocationID = fmt.Sprintf("parallel-invocation-%d", index)
			evt.Timestamp = baseTime.Add(time.Duration(index) * time.Second)
			evt.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
				Type: "function", ID: fmt.Sprintf("parallel-call-%d", index),
				Function: model.FunctionDefinitionParam{
					Name: "worker", Arguments: []byte(fmt.Sprintf(`{"index":%d}`, index)),
				},
			}}
			results <- backend.Session.AppendEvent(ctx, sessionCopies[index], evt)
		}()
	}
	for i := 0; i < workers; i++ {
		<-ready
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			return err
		}
	}
	stored, err := backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	if len(stored.Events) != workers+1 {
		return fmt.Errorf("concurrent replay stored %d events, want %d", len(stored.Events), workers+1)
	}
	authors := make(map[string]int, workers)
	for i := 1; i < len(stored.Events); i++ {
		authors[stored.Events[i].Author]++
	}
	for i := 0; i < workers; i++ {
		if authors[fmt.Sprintf("tool-%d", i)] != 1 {
			return fmt.Errorf("concurrent replay did not persist tool-%d exactly once", i)
		}
	}
	return nil
}

func replayFailureRecovery(ctx context.Context, backend replaytest.Backend) error {
	sess, err := createReplaySession(ctx, backend, session.StateMap{
		"phase": []byte(`"started"`), "pending": []byte(`true`),
	})
	if err != nil {
		return err
	}
	if err := backend.Session.AppendEvent(ctx, sess, replayEvent("user", model.RoleUser, "start recovery")); err != nil {
		return err
	}
	evt := replayEvent("assistant", model.RoleAssistant, "committed after retry")
	evt.ID = "recovery-event"
	evt.StateDelta = session.StateMap{"phase": []byte(`"committed"`), "pending": nil}
	eventAttempts := 0
	appendEvent := func() error {
		eventAttempts++
		if eventAttempts == 1 {
			return errReplayTransient
		}
		return backend.Session.AppendEvent(ctx, sess, evt)
	}
	if err := appendEvent(); !errors.Is(err, errReplayTransient) {
		return fmt.Errorf("simulate transient event failure: %w", err)
	}
	if err := loseAcknowledgement(appendEvent); !errors.Is(err, errReplayAcknowledgementLost) {
		return fmt.Errorf("simulate event acknowledgement loss: %w", err)
	}
	if eventAttempts != 2 {
		return fmt.Errorf("event recovery used %d attempts, want 2", eventAttempts)
	}
	stored, err := backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	count := countReplayEvents(stored.Events, evt.ID)
	if count == 0 {
		if err := backend.Session.AppendEvent(ctx, stored, evt); err != nil {
			return err
		}
		count = 1
	}
	if count != 1 {
		return fmt.Errorf("event recovery produced %d copies", count)
	}

	if replaytest.Supports(backend, replaytest.CapabilityMemory) {
		if err := replayRecoverMemory(ctx, backend); err != nil {
			return err
		}
	}
	stored, err = backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	if replaytest.Supports(backend, replaytest.CapabilityState) {
		if string(stored.State["phase"]) != `"committed"` {
			return fmt.Errorf("event recovery did not commit state: %v", stored.State)
		}
		pending, exists := stored.State["pending"]
		if !exists {
			return fmt.Errorf("event recovery dropped explicit null state: %v", stored.State)
		}
		if replaytest.Supports(backend, replaytest.CapabilityEventStateDeltaNull) {
			if pending != nil {
				return fmt.Errorf("event recovery left dirty state: %v", stored.State)
			}
		} else if string(pending) != "true" {
			return fmt.Errorf("unsupported event state delta null did not preserve pending state: %v", stored.State)
		}
	}
	if !replaytest.Supports(backend, replaytest.CapabilitySummary) {
		return nil
	}
	return backend.Session.CreateSessionSummary(ctx, stored, "", true)
}

func replayRecoverMemory(ctx context.Context, backend replaytest.Backend) error {
	user := replayMemoryUser(backend)
	memoryAttempts := 0
	addMemory := func() error {
		memoryAttempts++
		if memoryAttempts == 1 {
			return errReplayTransient
		}
		return backend.Memory.AddMemory(ctx, user, "recovered fact", []string{"recovery"})
	}
	if err := addMemory(); !errors.Is(err, errReplayTransient) {
		return fmt.Errorf("simulate transient memory failure: %w", err)
	}
	if err := loseAcknowledgement(addMemory); !errors.Is(err, errReplayAcknowledgementLost) {
		return fmt.Errorf("simulate memory acknowledgement loss: %w", err)
	}
	if memoryAttempts != 2 {
		return fmt.Errorf("memory recovery used %d attempts, want 2", memoryAttempts)
	}
	memories, err := backend.Memory.ReadMemories(ctx, user, 0)
	if err != nil {
		return err
	}
	memoryCount := 0
	for _, entry := range memories {
		if entry != nil && entry.Memory != nil && entry.Memory.Memory == "recovered fact" {
			memoryCount++
		}
	}
	if memoryCount == 0 {
		if err := backend.Memory.AddMemory(ctx, user, "recovered fact", []string{"recovery"}); err != nil {
			return err
		}
		memoryCount = 1
	}
	if memoryCount != 1 {
		return fmt.Errorf("memory recovery produced %d copies", memoryCount)
	}
	return nil
}

var (
	errReplayTransient           = errors.New("simulated transient failure")
	errReplayAcknowledgementLost = errors.New("simulated acknowledgement loss")
)

func loseAcknowledgement(operation func() error) error {
	if err := operation(); err != nil {
		return err
	}
	return errReplayAcknowledgementLost
}

func countReplayEvents(events []event.Event, id string) int {
	count := 0
	for i := range events {
		if events[i].ID == id {
			count++
		}
	}
	return count
}

func replayDriftFixture(ctx context.Context, backend replaytest.Backend) error {
	sess, err := createReplaySession(ctx, backend, session.StateMap{
		"counter": []byte(`9007199254740993`),
	})
	if err != nil {
		return err
	}
	userEvent := replayEvent("user", model.RoleUser, "check weather")
	userEvent.FilterKey = replaySummaryFilter
	if err := backend.Session.AppendEvent(ctx, sess, userEvent); err != nil {
		return err
	}
	call := replayEvent("assistant", model.RoleAssistant, "")
	call.FilterKey = replaySummaryFilter
	call.Response.Choices[0].Message.ToolCalls = []model.ToolCall{{
		Type: "function", ID: "fixture-call", Function: model.FunctionDefinitionParam{
			Name: "weather", Arguments: []byte(`{"city":"Shenzhen","days":2}`),
		},
	}}
	if err := backend.Session.AppendEvent(ctx, sess, call); err != nil {
		return err
	}
	result := replayEvent("tool", model.RoleTool, `{"temperature":30,"unit":"c"}`)
	result.FilterKey = replaySummaryFilter
	result.Response.Choices[0].Message.ToolID = "fixture-call"
	result.Response.Choices[0].Message.ToolName = "weather"
	if err := event.SetExtension(result, event.ToolCallArgsExtensionKey, map[string]any{
		"fixture-call": map[string]any{"city": "Shenzhen", "days": 2},
	}); err != nil {
		return err
	}
	if err := backend.Session.AppendEvent(ctx, sess, result); err != nil {
		return err
	}
	user := replayMemoryUser(backend)
	if err := backend.Memory.AddMemory(ctx, user, "prefers metric units", []string{"preference"}); err != nil {
		return err
	}
	if err := backend.Memory.AddMemory(ctx, user, "lives in Shenzhen", []string{"profile"}); err != nil {
		return err
	}
	trackService, ok := backend.Session.(session.TrackService)
	if !ok {
		return fmt.Errorf("backend %s does not support tracks", backend.Name)
	}
	if err := trackService.AppendTrackEvent(ctx, sess, &session.TrackEvent{
		Track: "tool", Timestamp: time.Now(),
		Payload: json.RawMessage(`{"status":"ok","invocation":"invocation-main","tool_call_id":"fixture-call","duration_ms":5}`),
	}); err != nil {
		return err
	}
	stored, err := backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return err
	}
	return backend.Session.CreateSessionSummary(ctx, stored, replaySummaryFilter, true)
}

func newReplayBackends(t *testing.T, caseName string) []replaytest.Backend {
	t.Helper()
	safeName := strings.ReplaceAll(caseName, "_", "-")
	key := session.Key{AppName: "replay-" + safeName, UserID: "user", SessionID: "session"}
	summarizer := deterministicSummarizer{}
	inmemorySession := sessinmemory.NewSessionService(
		sessinmemory.WithSummarizer(summarizer), sessinmemory.WithSummaryFilterAllowlist(replaySummaryFilter), sessinmemory.WithCascadeFullSessionSummary(false),
	)
	inmemoryMemory := meminmemory.NewMemoryService()

	dbPath := filepath.Join(t.TempDir(), "replay.db")
	db, err := sql.Open("sqlite3", "file:"+dbPath+"?_busy_timeout=5000&_journal_mode=WAL")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	sqliteSession, err := sesssqlite.NewService(db,
		sesssqlite.WithSummarizer(summarizer), sesssqlite.WithSummaryFilterAllowlist(replaySummaryFilter), sesssqlite.WithCascadeFullSessionSummary(false),
	)
	require.NoError(t, err)
	sqliteMemory, err := memsqlite.NewService(db)
	require.NoError(t, err)
	t.Cleanup(func() {
		require.NoError(t, inmemoryMemory.Close())
		require.NoError(t, inmemorySession.Close())
		require.NoError(t, sqliteMemory.Close())
		require.NoError(t, sqliteSession.Close())
		require.NoError(t, db.Close())
	})
	capabilities := map[string]replaytest.Capability{
		replaytest.CapabilityEvents:              {Supported: true},
		replaytest.CapabilityState:               {Supported: true},
		replaytest.CapabilityEventStateDeltaNull: {Supported: true},
		replaytest.CapabilityMemory:              {Supported: true},
		replaytest.CapabilitySummary:             {Supported: true},
		replaytest.CapabilityTracks:              {Supported: true},
	}
	backends := []replaytest.Backend{
		{Name: "inmemory", Session: inmemorySession, Memory: inmemoryMemory, SessionKey: key, Capabilities: capabilities},
		{Name: "sqlite", Session: sqliteSession, Memory: sqliteMemory, SessionKey: key, Capabilities: capabilities},
	}
	for i := range backends {
		switch caseName {
		case "summary_event_window_recovery":
			backends[i].Load = loadReplaySummaryWindow
		case "memory_search_order_and_score":
			backends[i].Load = loadReplayMemorySearch
		}
	}
	return backends
}

func loadReplayMemorySearch(
	ctx context.Context,
	backend replaytest.Backend,
) (*session.Session, []*memory.Entry, error) {
	sess, err := backend.Session.GetSession(ctx, backend.SessionKey)
	if err != nil {
		return nil, nil, err
	}
	memories, err := backend.Memory.SearchMemories(ctx, replayMemoryUser(backend), "Shenzhen")
	if err != nil {
		return nil, nil, err
	}
	if len(memories) < 2 {
		return nil, nil, fmt.Errorf("memory search load returned %d entries, want at least 2", len(memories))
	}
	return sess, memories, nil
}

func loadReplaySummaryWindow(
	ctx context.Context,
	backend replaytest.Backend,
) (*session.Session, []*memory.Entry, error) {
	sess, err := backend.Session.GetSession(ctx, backend.SessionKey, session.WithEventNum(2))
	if err != nil {
		return nil, nil, err
	}
	if len(sess.Events) != 2 {
		return nil, nil, fmt.Errorf("summary recovery loaded %d tail events, want 2", len(sess.Events))
	}
	summary := sess.Summaries[session.SummaryFilterKeyAllContents]
	if summary == nil || summary.Summary != "summary of 12 events" {
		return nil, nil, fmt.Errorf("summary recovery did not load the expected summary")
	}
	memories, err := backend.Memory.ReadMemories(ctx, replayMemoryUser(backend), 0)
	if err != nil {
		return nil, nil, err
	}
	return sess, memories, nil
}

func createReplaySession(ctx context.Context, backend replaytest.Backend, state session.StateMap) (*session.Session, error) {
	return backend.Session.CreateSession(ctx, backend.SessionKey, state)
}

func appendReplayMessages(ctx context.Context, backend replaytest.Backend, sess *session.Session, user, assistant string) error {
	if err := backend.Session.AppendEvent(ctx, sess, replayEvent("user", model.RoleUser, user)); err != nil {
		return err
	}
	return backend.Session.AppendEvent(ctx, sess, replayEvent("assistant", model.RoleAssistant, assistant))
}

func replayEvent(author string, role model.Role, content string) *event.Event {
	return event.New("invocation-main", author, event.WithResponse(&model.Response{
		Object: model.ObjectTypeChatCompletion, Done: true,
		Choices: []model.Choice{{Index: 0, Message: model.Message{Role: role, Content: content}}},
	}))
}

func replayMemoryUser(backend replaytest.Backend) memory.UserKey {
	return memory.UserKey{AppName: backend.SessionKey.AppName, UserID: backend.SessionKey.UserID}
}

func replayFixtureSnapshot() replaytest.Snapshot {
	return replaytest.Snapshot{
		SessionID: "session",
		Events:    []map[string]any{{"id": "event-000", "author": "assistant"}},
		State:     map[string]any{"counter": int64(1)},
		Memories:  []replaytest.MemorySnapshot{{ID: "memory-000", Content: "fact"}},
		Summaries: map[string]replaytest.SummarySnapshot{replaySummaryFilter: {
			Text: "summary", FilterKey: replaySummaryFilter, Version: 1, LastEventIndex: replayInt(1),
		}},
		Tracks: map[string][]replaytest.TrackSnapshot{"tool": {
			{Track: "tool", Payload: map[string]any{"status": "ok"}},
		}},
	}
}

func cloneReplaySnapshot(t *testing.T, snapshot replaytest.Snapshot) replaytest.Snapshot {
	t.Helper()
	result, err := snapshot.Clone()
	require.NoError(t, err)
	return result
}

func replayInt(value int) *int { return &value }

func replayString(value string) *string { return &value }
