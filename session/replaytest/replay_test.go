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
	"context"
	"encoding/json"
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
// Normalizer unit tests
// ---------------------------------------------------------------------------

func TestNormalizeEvents(t *testing.T) {
	events := []event.Event{
		{
			Author:    "user",
			ID:        "id-1",
			Timestamp: time.Now(),
			Response: &model.Response{
				Object: model.ObjectTypeChatCompletion,
				Choices: []model.Choice{
					{Message: model.Message{Role: model.RoleUser, Content: "Hello"}},
				},
			},
		},
	}
	norm := NormalizeEvents(events)
	require.Len(t, norm, 1)
	assert.Equal(t, "user", norm[0].Author)
	assert.Equal(t, "user", norm[0].Role)
	assert.Equal(t, "Hello", norm[0].Content)
}

func TestNormalizeSummaries(t *testing.T) {
	summaries := map[string]*session.Summary{
		"": {
			Summary:   "A conversation about Go.",
			Topics:    []string{"go", "programming"},
			UpdatedAt: time.Now(),
			Boundary: &session.SummaryBoundary{
				Version:   1,
				FilterKey: "",
				CutoffAt:  time.Now().UTC(),
			},
		},
	}
	norm := NormalizeSummaries(summaries)
	require.Contains(t, norm, "")
	assert.Equal(t, "A conversation about Go.", norm[""].Text)
	assert.Equal(t, 1, norm[""].BoundaryVersion)
}

func TestNormalizeMemories(t *testing.T) {
	entries := []*memory.Entry{
		{
			ID:      "mem-1",
			AppName: "app",
			UserID:  "user",
			Memory: &memory.Memory{
				Memory: "User likes Go",
				Topics: []string{"preference"},
			},
		},
	}
	norm := NormalizeMemories(entries)
	require.Len(t, norm, 1)
	assert.Equal(t, "User likes Go", norm[0].Content)
	assert.Equal(t, "app:user", norm[0].Scope)
}

func TestNormalizeTrackEvents(t *testing.T) {
	events := []session.TrackEvent{
		{
			Track:   "tool_exec",
			Payload: json.RawMessage(`{"tool":"search","duration_ms":100}`),
		},
	}
	norm := NormalizeTrackEvents(events)
	require.Len(t, norm, 1)
	assert.Equal(t, "tool_exec", norm[0].Track)
	assert.Contains(t, norm[0].Payload, "search")
}

func TestNormalizeState(t *testing.T) {
	state := session.StateMap{
		"counter": []byte("42"),
		"mode":    []byte("fast"),
	}
	norm := NormalizeState(state)
	assert.Equal(t, "42", norm["counter"])
	assert.Equal(t, "fast", norm["mode"])
}

// ---------------------------------------------------------------------------
// Comparator unit tests
// ---------------------------------------------------------------------------

func TestComparatorIdenticalSnapshots(t *testing.T) {
	snap := &BackendSnapshot{
		BackendName: "inmemory",
		SessionID:   "sess-1",
		Events: []event.Event{
			{
				Author: "user", ID: "e1", Timestamp: time.Now(),
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.Message{Role: model.RoleUser, Content: "Hi"}},
					},
				},
			},
		},
		State: session.StateMap{"key": []byte("val")},
	}
	cmp := NewComparator("inmemory")
	diffs := cmp.Compare(snap, snap)
	// When comparing identical snapshots, there should be no non-allowed diffs.
	for _, d := range diffs {
		assert.True(t, d.AllowedDiff, "unexpected real diff: %+v", d)
	}
}

func TestComparatorDetectsEventMismatch(t *testing.T) {
	base := &BackendSnapshot{
		BackendName: "base",
		SessionID:   "sess-1",
		Events: []event.Event{
			{
				Author: "user", ID: "e1", Timestamp: time.Now(),
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.Message{Role: model.RoleUser, Content: "Hello"}},
					},
				},
			},
		},
	}
	other := &BackendSnapshot{
		BackendName: "other",
		SessionID:   "sess-1",
		Events: []event.Event{
			{
				Author: "user", ID: "e2", Timestamp: time.Now(),
				Response: &model.Response{
					Choices: []model.Choice{
						{Message: model.Message{Role: model.RoleUser, Content: "DIFFERENT"}},
					},
				},
			},
		},
	}
	cmp := NewComparator("base")
	diffs := cmp.Compare(base, other)
	hasContentDiff := false
	for _, d := range diffs {
		if d.FieldPath == "events[0].content" {
			hasContentDiff = true
		}
	}
	assert.True(t, hasContentDiff, "should detect content mismatch")
}

func TestComparatorDetectsStateMismatch(t *testing.T) {
	base := &BackendSnapshot{
		BackendName: "base",
		SessionID:   "sess-1",
		State:       session.StateMap{"x": []byte("1")},
	}
	other := &BackendSnapshot{
		BackendName: "other",
		SessionID:   "sess-1",
		State:       session.StateMap{"x": []byte("2")},
	}
	cmp := NewComparator("base")
	diffs := cmp.Compare(base, other)
	hasStateDiff := false
	for _, d := range diffs {
		if d.FieldPath == "state[x]" {
			hasStateDiff = true
		}
	}
	assert.True(t, hasStateDiff, "should detect state value mismatch")
}

func TestComparatorDetectsSummaryMissing(t *testing.T) {
	base := &BackendSnapshot{
		BackendName: "base",
		SessionID:   "sess-1",
		Summaries: map[string]*session.Summary{
			"": {Summary: "summary text"},
		},
	}
	other := &BackendSnapshot{
		BackendName: "other",
		SessionID:   "sess-1",
		Summaries:   map[string]*session.Summary{},
	}
	cmp := NewComparator("base")
	diffs := cmp.Compare(base, other)
	hasMissingSummary := false
	for _, d := range diffs {
		if d.FieldPath == "summaries[]" && d.Explanation == "summary missing in other backend" {
			hasMissingSummary = true
		}
	}
	assert.True(t, hasMissingSummary, "should detect missing summary")
}

// ---------------------------------------------------------------------------
// Report unit tests
// ---------------------------------------------------------------------------

func TestBuildReport(t *testing.T) {
	results := []CaseResult{
		{CaseName: "case1", HasDiff: false, DiffCount: 0},
		{CaseName: "case2", HasDiff: true, DiffCount: 3, AllowedDiffCount: 1},
	}
	report := BuildReport(results, []string{"base", "other"})
	assert.Equal(t, 2, report.TotalCases)
	assert.Equal(t, 1, report.PassCases)
	assert.Equal(t, 1, report.FailCases)
	assert.Equal(t, 3, report.TotalDiffs)
	assert.Equal(t, 1, report.AllowedDiffs)
	assert.True(t, HasFailures(report))
}

// ---------------------------------------------------------------------------
// Harness integration test (InMemory vs InMemory)
// ---------------------------------------------------------------------------

func TestHarnessInMemoryBaseline(t *testing.T) {
	// Create two independent InMemory backends to compare.
	factory1 := BackendFactory{
		Name: "inmemory-A",
		CreateSession: func() (session.Service, error) {
			return NewInMemoryBackend().CreateSession()
		},
		CreateTrack: func() (session.TrackService, error) {
			return NewInMemoryBackend().CreateTrack()
		},
		CreateMemory: func() (memory.Service, error) {
			return NewInMemoryBackend().CreateMemory()
		},
	}
	factory2 := BackendFactory{
		Name: "inmemory-B",
		CreateSession: func() (session.Service, error) {
			return NewInMemoryBackend().CreateSession()
		},
		CreateTrack: func() (session.TrackService, error) {
			return NewInMemoryBackend().CreateTrack()
		},
		CreateMemory: func() (memory.Service, error) {
			return NewInMemoryBackend().CreateMemory()
		},
	}

	harness := NewHarness(WithBackends(factory1, factory2))
	cases := AllReplayCases()

	ctx := context.Background()
	report, err := harness.Run(ctx, cases)
	require.NoError(t, err)

	// Two identical backend implementations should produce no real diffs.
	assert.Equal(t, len(cases), report.TotalCases)
	assert.Equal(t, 0, report.FailCases, "expected no failures, got: %s", report.Summary())

	// Write report for inspection.
	reportPath := t.TempDir() + "/report.json"
	err = WriteReport(reportPath, report)
	require.NoError(t, err)
	t.Logf("Report: %s", report.Summary())
}

// TestHarnessDetectsInjectedDiff verifies that the framework detects
// intentionally injected inconsistencies.
//
// tamperingService wraps a session.Service and corrupts observable
// artifacts (assistant content, state values, track payloads) so the
// comparison must flag a real difference in every replay case.
type tamperingService struct {
	session.Service
}

func (s *tamperingService) AppendEvent(
	ctx context.Context,
	sess *session.Session,
	e *event.Event,
	opts ...session.Option,
) error {
	if e != nil && e.Response != nil {
		// Tamper a clone: the replay harness shares one event pointer
		// across backends, so in-place mutation would corrupt the base
		// snapshot as well and hide the injected difference.
		cloned := *e
		rsp := *e.Response
		choices := make([]model.Choice, len(rsp.Choices))
		copy(choices, rsp.Choices)
		for i := range choices {
			if choices[i].Message.Role == model.RoleAssistant {
				choices[i].Message.Content = "TAMPERED: " + choices[i].Message.Content
			}
		}
		rsp.Choices = choices
		cloned.Response = &rsp
		return s.Service.AppendEvent(ctx, sess, &cloned, opts...)
	}
	return s.Service.AppendEvent(ctx, sess, e, opts...)
}

func (s *tamperingService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	corrupted := make(session.StateMap, len(state))
	for k, v := range state {
		corrupted[k] = append([]byte("TAMPERED:"), v...)
	}
	return s.Service.UpdateSessionState(ctx, key, corrupted)
}

func (s *tamperingService) AppendTrackEvent(
	ctx context.Context,
	sess *session.Session,
	te *session.TrackEvent,
	opts ...session.Option,
) error {
	ts, ok := s.Service.(session.TrackService)
	if !ok {
		return session.ErrTrackEventsNotFound
	}
	if te != nil {
		cloned := *te
		cloned.Payload = []byte(`{"tampered":true}`)
		return ts.AppendTrackEvent(ctx, sess, &cloned, opts...)
	}
	return ts.AppendTrackEvent(ctx, sess, te, opts...)
}

// tamperingMemory corrupts memory content on write.
type tamperingMemory struct {
	memory.Service
}

func (m *tamperingMemory) AddMemory(
	ctx context.Context,
	userKey memory.UserKey,
	content string,
	topics []string,
	opts ...memory.AddOption,
) error {
	return m.Service.AddMemory(ctx, userKey, "TAMPERED: "+content, topics, opts...)
}

// ClearMemories silently skips the clear, simulating a backend that loses
// clear semantics. This makes clear-terminated replay cases detectable.
func (m *tamperingMemory) ClearMemories(
	ctx context.Context,
	userKey memory.UserKey,
) error {
	return nil
}

func newTamperedBackend() BackendFactory {
	base := NewInMemoryBackend()
	return BackendFactory{
		Name: "tampered",
		CreateSession: func() (session.Service, error) {
			svc, err := base.CreateSession()
			if err != nil {
				return nil, err
			}
			return &tamperingService{Service: svc}, nil
		},
		CreateMemory: func() (memory.Service, error) {
			svc, err := base.CreateMemory()
			if err != nil {
				return nil, err
			}
			return &tamperingMemory{Service: svc}, nil
		},
	}
}

func TestHarnessDetectsInjectedDiff(t *testing.T) {
	simpleCase := ReplayCase{
		Name:         "injected_diff_test",
		Description:  "Detects tampered assistant content",
		SkipMemories: true,
		Operations: []ReplayOperation{
			{Type: OpAppendEvent, Event: mkUserEvent("Hello")},
			{Type: OpAppendEvent, Event: mkAssistantEvent("Hi there!")},
		},
	}

	harness := NewHarness(WithBackends(NewInMemoryBackend(), newTamperedBackend()))
	report, err := harness.Run(context.Background(), []ReplayCase{simpleCase})
	require.NoError(t, err)
	assert.True(t, HasFailures(report), "tampered backend must be detected")

	// The diff must be located at event level with a content field path.
	var found bool
	for _, cr := range report.CaseResults {
		for _, d := range cr.Differences {
			if d.EventIndex > 0 && d.FieldPath == "events[1].content" {
				found = true
			}
		}
	}
	assert.True(t, found, "expected event-level content diff locator")
}

// TestAllCasesDetectInjectedDifferences verifies the acceptance requirement
// that every public replay case detects an intentionally injected
// inconsistency with 100% detection rate.
func TestAllCasesDetectInjectedDifferences(t *testing.T) {
	harness := NewHarness(WithBackends(NewInMemoryBackend(), newTamperedBackend()))
	cases := AllReplayCases()
	report, err := harness.Run(context.Background(), cases)
	require.NoError(t, err)

	detected := 0
	for _, cr := range report.CaseResults {
		if cr.HasDiff {
			detected++
		}
	}
	assert.Equal(t, len(cases), detected,
		"every replay case must detect the injected inconsistency")
	assert.Equal(t, len(cases), report.FailCases)
}

func TestComparatorDetectsSummaryOverwrite(t *testing.T) {
	base := &BackendSnapshot{
		BackendName: "base",
		SessionID:   "sess-1",
		Summaries: map[string]*session.Summary{
			"": {Summary: "original summary"},
		},
	}
	other := &BackendSnapshot{
		BackendName: "other",
		SessionID:   "sess-1",
		Summaries: map[string]*session.Summary{
			"": {Summary: "overwritten with different text"},
		},
	}
	cmp := NewComparator("base")
	diffs := cmp.Compare(base, other)
	var found bool
	for _, d := range diffs {
		if d.FieldPath == "summaries[].text" && d.SummaryFilterKey == "" {
			found = true
		}
	}
	assert.True(t, found, "should detect summary text overwrite")
}

func TestComparatorDetectsSummaryFilterKeyMismatch(t *testing.T) {
	// The summary exists in both backends but is attributed to different
	// filter keys, i.e. the summary ownership is wrong.
	base := &BackendSnapshot{
		BackendName: "base",
		SessionID:   "sess-1",
		Summaries: map[string]*session.Summary{
			"branch-a": {Summary: "branch summary"},
		},
	}
	other := &BackendSnapshot{
		BackendName: "other",
		SessionID:   "sess-1",
		Summaries: map[string]*session.Summary{
			"": {Summary: "branch summary"},
		},
	}
	cmp := NewComparator("base")
	diffs := cmp.Compare(base, other)
	var missing, extra bool
	for _, d := range diffs {
		if d.SummaryFilterKey == "branch-a" && d.CompareValue == "missing" {
			missing = true
		}
		if d.SummaryFilterKey == "" && d.CompareValue == "present" {
			extra = true
		}
	}
	assert.True(t, missing && extra, "should detect summary filter-key misattribution")
}

// ---------------------------------------------------------------------------
// Case count verification
// ---------------------------------------------------------------------------

func TestAllReplayCasesCount(t *testing.T) {
	cases := AllReplayCases()
	assert.GreaterOrEqual(t, len(cases), 10, "must have at least 10 replay cases")
}

// ---------------------------------------------------------------------------
// Example report generation
// ---------------------------------------------------------------------------

func TestGenerateExampleReport(t *testing.T) {
	factory1 := BackendFactory{
		Name: "inmemory-A",
		CreateSession: func() (session.Service, error) {
			return NewInMemoryBackend().CreateSession()
		},
		CreateTrack: func() (session.TrackService, error) {
			return NewInMemoryBackend().CreateTrack()
		},
		CreateMemory: func() (memory.Service, error) {
			return NewInMemoryBackend().CreateMemory()
		},
	}
	factory2 := BackendFactory{
		Name: "inmemory-B",
		CreateSession: func() (session.Service, error) {
			return NewInMemoryBackend().CreateSession()
		},
		CreateTrack: func() (session.TrackService, error) {
			return NewInMemoryBackend().CreateTrack()
		},
		CreateMemory: func() (memory.Service, error) {
			return NewInMemoryBackend().CreateMemory()
		},
	}

	harness := NewHarness(WithBackends(factory1, factory2))
	cases := AllReplayCases()

	ctx := context.Background()
	report, err := harness.Run(ctx, cases)
	require.NoError(t, err)

	reportPath := t.TempDir() + "/session_memory_summary_track_diff_report.json"
	err = WriteReport(reportPath, report)
	require.NoError(t, err)
	t.Logf("Example report: %s", report.Summary())
}
