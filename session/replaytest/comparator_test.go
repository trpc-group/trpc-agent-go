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
	require.Empty(t, result.Diffs)
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

func TestRequiredCapabilities(t *testing.T) {
	profile := InMemoryProfile()
	profile.SupportsTrack = false
	unsupported := MissingCapabilities(RequiredCapabilities{NeedsTrack: true}, profile)
	require.Len(t, unsupported, 1)
	require.Equal(t, "track", unsupported[0].Feature)
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
