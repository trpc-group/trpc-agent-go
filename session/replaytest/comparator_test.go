//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package replaytest

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestComparatorSequentialAndAllowedDiff(t *testing.T) {
	a := testSnapshot("a", "hello", "answer")
	b := testSnapshot("b", "hello", "changed")

	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
	require.NotEmpty(t, result.Diffs)
	require.Contains(t, result.Diffs[0].Path, "events")

	result = NewComparator().Compare(a, b, []AllowedDiff{{
		Path:      "events[*].response",
		Reason:    "known text drift",
		MatchRule: MatchRuleIgnore,
	}}, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusPassed, result.Status)
	require.NotEmpty(t, result.Diffs)
	require.Equal(t, SeverityAllowed, result.Diffs[0].Severity)
	require.Equal(t, MatchRuleIgnore, result.Diffs[0].AllowedDiff)
	require.Equal(t, "known text drift", result.Diffs[0].Explanation)
}

func TestComparatorAllowedAndErrorDiffsFailWithAllowedDiffVisible(t *testing.T) {
	a := testSnapshot("a", "hello", "answer")
	b := testSnapshot("b", "hello", "changed")
	b.Session.UserID = "other-user"

	result := NewComparator().Compare(a, b, []AllowedDiff{{
		Path:      "events[*].response",
		Reason:    "known text drift",
		MatchRule: MatchRuleIgnore,
	}}, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
	requireDiffSeverity(t, result.Diffs, "events[c1.assistant.1].response", SeverityAllowed)
	requireDiffSeverity(t, result.Diffs, "session.user_id", SeverityError)
}

func TestComparatorConcurrent(t *testing.T) {
	a := testSnapshotWithEvents("a", []event.Event{
		*testEvent("c10.agent_x.step_1", "agent_x", "one"),
		*testEvent("c10.agent_x.step_2", "agent_x", "two"),
		*testEvent("c10.agent_y.step_1", "agent_y", "other"),
	})
	b := testSnapshotWithEvents("b", []event.Event{
		*testEvent("c10.agent_y.step_1", "agent_y", "other"),
		*testEvent("c10.agent_x.step_1", "agent_x", "one"),
		*testEvent("c10.agent_x.step_2", "agent_x", "two"),
	})
	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusPassed, result.Status)

	b.Session.Events = []event.Event{
		b.Session.Events[2],
		b.Session.Events[0],
		b.Session.Events[1],
	}
	result = NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
	require.Contains(t, result.Diffs[0].Path, "order")
}

func TestComparatorDetectsDuplicateEventKeyAcrossBranches(t *testing.T) {
	a := testSnapshotWithEvents("a", []event.Event{
		*testEvent("c13.user.1", "agent_x", "same"),
	})
	b := testSnapshotWithEvents("b", []event.Event{
		*testEvent("c13.user.1", "agent_z", "same"),
		*testEvent("c13.user.1", "agent_x", "same"),
	})

	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
	require.NotEmpty(t, result.Diffs)
	require.Contains(t, result.Diffs[0].Path, "count")
}

func TestComparatorDetectsEventOnlyInSecondSnapshot(t *testing.T) {
	a := testSnapshotWithEvents("a", nil)
	b := testSnapshotWithEvents("b", []event.Event{
		*testEvent("only.in.b", "assistant", "new event"),
	})

	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
	requireDiff(t, result.Diffs, "events[only.in.b]", "missing", "present")
}

func TestComparatorDetectsEventStateDeltaDiff(t *testing.T) {
	a := testSnapshotWithEvents("a", []event.Event{
		*testEvent("state.delta", "assistant", "same"),
	})
	b := testSnapshotWithEvents("b", []event.Event{
		*testEvent("state.delta", "assistant", "same"),
	})
	a.Session.Events[0].StateDelta = map[string][]byte{"color": []byte("blue")}

	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
	requireDiff(t, result.Diffs, "events[state.delta].state_delta[color]", "blue", "")
}

func TestComparatorDetectsEventFilterKeyDiff(t *testing.T) {
	a := testSnapshotWithEvents("a", []event.Event{
		*testEvent("filter.key", "assistant", "same"),
	})
	b := testSnapshotWithEvents("b", []event.Event{
		*testEvent("filter.key", "assistant", "same"),
	})
	a.Session.Events[0].FilterKey = "branch"

	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
	requireDiff(t, result.Diffs, "events[filter.key].filter_key", "branch", "")
}

func TestComparatorDetectsPublicEventExtensionDiff(t *testing.T) {
	a := testSnapshotWithEvents("a", []event.Event{
		*testEvent("extension", "assistant", "same"),
	})
	b := testSnapshotWithEvents("b", []event.Event{
		*testEvent("extension", "assistant", "same"),
	})
	a.Session.Events[0].Extensions = map[string]json.RawMessage{
		replayEventKeyExtension: json.RawMessage(`"extension"`),
		"public":                json.RawMessage(`{"enabled":true}`),
	}
	b.Session.Events[0].Extensions = map[string]json.RawMessage{
		replayEventKeyExtension: json.RawMessage(`"extension"`),
	}

	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
	requireDiffPathPrefix(t, result.Diffs, "events[extension].extensions[public]")
}

func TestComparatorDetectsScopedStateDiffs(t *testing.T) {
	a := &SessionSnapshot{
		BackendName: "a",
		AppStates:   session.StateMap{"app_key": []byte("a")},
		UserStates:  session.StateMap{"user_key": []byte("u")},
	}
	b := &SessionSnapshot{
		BackendName: "b",
		AppStates:   session.StateMap{"app_key": []byte("changed")},
		UserStates:  session.StateMap{"user_key": []byte("changed")},
	}

	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
	requireDiffPathPrefix(t, result.Diffs, "app_states")
	requireDiffPathPrefix(t, result.Diffs, "user_states")
}

func TestComparatorMemoryProfile(t *testing.T) {
	a := &SessionSnapshot{BackendName: "a", Memories: []*memory.Entry{
		{ID: "target", Score: 0.80, Memory: &memory.Memory{Memory: "likes Go"}},
	}}
	b := &SessionSnapshot{BackendName: "b", Memories: []*memory.Entry{
		{ID: "target", Score: 0.805, Memory: &memory.Memory{Memory: "likes Go"}},
	}}
	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusPassed, result.Status)

	vector := InMemoryProfile()
	vector.RetrievalProfile.Algorithm = "cosine_vector"
	b.Memories = []*memory.Entry{{ID: "other", Memory: &memory.Memory{Memory: "other"}}}
	b.MemSearchResults = []*memory.Entry{{ID: "target", Memory: &memory.Memory{Memory: "likes Go"}}}
	result = NewComparator().Compare(a, b, nil, InMemoryProfile(), vector)
	require.Equal(t, StatusPassed, result.Status)
}

func TestComparatorNonStrictMemoryReportsMissingTarget(t *testing.T) {
	a := &SessionSnapshot{BackendName: "a", Memories: []*memory.Entry{
		{ID: "target", Memory: &memory.Memory{Memory: "likes Go"}},
	}}
	b := &SessionSnapshot{BackendName: "b"}
	vector := InMemoryProfile()
	vector.RetrievalProfile.Algorithm = "cosine_vector"

	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), vector)
	require.Equal(t, StatusFailed, result.Status)
	requireDiff(t, result.Diffs, "memory_search[target]", "target present", "target missing")
	require.False(t, containsMemoryID([]*memory.Entry{nil, &memory.Entry{ID: "other"}}, "target"))
}

func TestAllowedDiffMatchRules(t *testing.T) {
	tests := []struct {
		name    string
		diff    DiffResult
		rule    AllowedDiff
		allowed bool
	}{
		{
			name: "within delta",
			diff: DiffResult{Path: "memory_search[target].score", ValueA: 0.90, ValueB: float32(0.91)},
			rule: AllowedDiff{
				Path:      "memory_search[*].score",
				MatchRule: MatchRuleWithinDelta,
				Delta:     0.02,
			},
			allowed: true,
		},
		{
			name: "within delta rejects non numeric",
			diff: DiffResult{Path: "memory_search[target].score", ValueA: "0.90", ValueB: 0.91},
			rule: AllowedDiff{
				Path:      "memory_search[*].score",
				MatchRule: MatchRuleWithinDelta,
				Delta:     0.02,
			},
		},
		{
			name: "not empty",
			diff: DiffResult{Path: "summaries", ValueA: "summary", ValueB: []byte("summary")},
			rule: AllowedDiff{
				Path:      "summaries",
				MatchRule: MatchRuleNotEmpty,
			},
			allowed: true,
		},
		{
			name: "not empty rejects empty side",
			diff: DiffResult{Path: "summaries", ValueA: "summary", ValueB: ""},
			rule: AllowedDiff{
				Path:      "summaries",
				MatchRule: MatchRuleNotEmpty,
			},
		},
		{
			name: "same type",
			diff: DiffResult{Path: "state[color]", ValueA: []byte("blue"), ValueB: []byte("green")},
			rule: AllowedDiff{
				Path:      "state[color]",
				MatchRule: MatchRuleSameType,
			},
			allowed: true,
		},
		{
			name: "same type rejects different type",
			diff: DiffResult{Path: "state[color]", ValueA: []byte("blue"), ValueB: "green"},
			rule: AllowedDiff{
				Path:      "state[color]",
				MatchRule: MatchRuleSameType,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterAllowedDiffs([]DiffResult{tc.diff}, []AllowedDiff{tc.rule})
			if tc.allowed {
				require.True(t, allowed(tc.diff, []AllowedDiff{tc.rule}))
				require.Len(t, got, 1)
				require.Equal(t, SeverityAllowed, got[0].Severity)
				require.Equal(t, allowedDiffSummary(tc.rule), got[0].AllowedDiff)
				return
			}
			require.False(t, allowed(tc.diff, []AllowedDiff{tc.rule}))
			require.Equal(t, []DiffResult{tc.diff}, got)
		})
	}
}

func TestAsFloat(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want float64
		ok   bool
	}{
		{name: "float64", in: 1.25, want: 1.25, ok: true},
		{name: "float32", in: float32(1.5), want: 1.5, ok: true},
		{name: "int", in: 2, want: 2, ok: true},
		{name: "string", in: "2", ok: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := asFloat(tc.in)
			require.Equal(t, tc.ok, ok)
			if tc.ok {
				require.Equal(t, tc.want, got)
			}
		})
	}
}

func TestIsEmpty(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want bool
	}{
		{name: "nil", in: nil, want: true},
		{name: "empty string", in: "", want: true},
		{name: "non empty string", in: "x", want: false},
		{name: "empty slice", in: []string{}, want: true},
		{name: "non empty slice", in: []string{"x"}, want: false},
		{name: "empty map", in: map[string]string{}, want: true},
		{name: "int", in: 0, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, isEmpty(tc.in))
		})
	}
}

func TestRequiredCapabilities(t *testing.T) {
	profile := InMemoryProfile()
	profile.SupportsTrack = false
	unsupported := MissingCapabilities(RequiredCapabilities{NeedsTrack: true}, profile)
	require.Len(t, unsupported, 1)
	require.Equal(t, "track", unsupported[0].Feature)
}

func requireDiffPathPrefix(t *testing.T, diffs []DiffResult, prefix string) {
	t.Helper()
	for _, diff := range diffs {
		if strings.HasPrefix(diff.Path, prefix) {
			return
		}
	}
	t.Fatalf("diff prefix %q not found in %#v", prefix, diffs)
}

func requireDiff(t *testing.T, diffs []DiffResult, path string, valueA, valueB any) {
	t.Helper()
	for _, diff := range diffs {
		if diff.Path == path && diff.ValueA == valueA && diff.ValueB == valueB {
			return
		}
	}
	t.Fatalf("diff %q with values %#v/%#v not found in %#v", path, valueA, valueB, diffs)
}

func requireDiffSeverity(t *testing.T, diffs []DiffResult, path, severity string) {
	t.Helper()
	for _, diff := range diffs {
		if diff.Path == path && diff.Severity == severity {
			return
		}
	}
	t.Fatalf("diff %q with severity %q not found in %#v", path, severity, diffs)
}

func testSnapshot(backend, userText, assistantText string) *SessionSnapshot {
	return testSnapshotWithEvents(backend, []event.Event{
		*testEvent("c1.user.1", "user", userText),
		*testEvent("c1.assistant.1", "assistant", assistantText),
	})
}

func testSnapshotWithEvents(backend string, events []event.Event) *SessionSnapshot {
	sess := session.NewSession("app", "user", "sess", session.WithSessionEvents(events))
	sess.CreatedAt = time.Time{}
	sess.UpdatedAt = time.Time{}
	norm, err := NewNormalizer().Normalize(&SessionSnapshot{BackendName: backend, Session: sess})
	if err != nil {
		panic(err)
	}
	return norm
}

func testEvent(key, branch, content string) *event.Event {
	evt := event.NewResponseEvent("inv", branch, &model.Response{
		Choices: []model.Choice{{Message: model.NewAssistantMessage(content)}},
	}, event.WithTag(key))
	evt.Branch = branch
	evt.Timestamp = time.Time{}
	return evt
}

func TestMissingCapabilitiesAllBranches(t *testing.T) {
	tests := []struct {
		name     string
		required RequiredCapabilities
		mut      func(*BackendProfile)
		feature  string
	}{
		{"window", RequiredCapabilities{NeedsWindow: true}, func(p *BackendProfile) { p.SupportsWindow = false }, "window"},
		{"search", RequiredCapabilities{NeedsSearch: true}, func(p *BackendProfile) { p.SupportsSearch = false }, "search"},
		{"memory", RequiredCapabilities{NeedsMemory: true}, func(p *BackendProfile) { p.RetrievalProfile.Algorithm = ""; p.Name = "test" }, "memory"},
		{"async_summary", RequiredCapabilities{NeedsAsyncSummary: true}, func(p *BackendProfile) { p.SupportsAsyncSummary = false }, "async_summary"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := InMemoryProfile()
			tc.mut(&p)
			unsupported := MissingCapabilities(tc.required, p)
			require.Len(t, unsupported, 1)
			require.Equal(t, tc.feature, unsupported[0].Feature)
		})
	}
}

func TestCompareSessionsMismatch(t *testing.T) {
	a := &SessionSnapshot{BackendName: "a", Session: session.NewSession("app-a", "user-a", "id-a")}
	b := &SessionSnapshot{BackendName: "b", Session: session.NewSession("app-b", "user-b", "id-b")}
	result := NewComparator().Compare(a, b, nil, InMemoryProfile(), InMemoryProfile())
	require.Equal(t, StatusFailed, result.Status)
	requireDiffPathPrefix(t, result.Diffs, "session.id")
	requireDiffPathPrefix(t, result.Diffs, "session.app_name")
	requireDiffPathPrefix(t, result.Diffs, "session.user_id")
}

func TestBackendNameFallback(t *testing.T) {
	require.Equal(t, "snap", backendName(&SessionSnapshot{BackendName: "snap"}, BackendProfile{Name: "prof"}))
	require.Equal(t, "prof", backendName(nil, BackendProfile{Name: "prof"}))
}

func TestCompareScopedStatesWithNil(t *testing.T) {
	result := NewComparator().Compare(
		&SessionSnapshot{BackendName: "a"},
		&SessionSnapshot{BackendName: "b"},
		nil, InMemoryProfile(), InMemoryProfile(),
	)
	require.Equal(t, StatusPassed, result.Status)
}
