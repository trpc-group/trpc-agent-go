//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestWrapAgentWithCandidateSelector_EdgeCases(t *testing.T) {
	selector := &fixedCandidateSelector{winner: 0}
	assert.Nil(t, wrapAgentWithCandidateSelector(nil, selector, candidateSelectOptions{}))

	ag := &candidateScriptAgent{name: "candidate"}
	assert.Same(t, ag, wrapAgentWithCandidateSelector(ag, nil, candidateSelectOptions{}))

	wrapped := wrapAgentWithCandidateSelector(ag, selector, candidateSelectOptions{attempts: 2})
	assert.IsType(t, &candidateSelectorAgent{}, wrapped)
	assert.Same(t, wrapped, wrapAgentWithCandidateSelector(wrapped, selector, candidateSelectOptions{attempts: 2}))
}

func TestCandidateSelectorAgent_NilSurface(t *testing.T) {
	var ag *candidateSelectorAgent
	assert.Empty(t, ag.Info())
	assert.Nil(t, ag.Tools())
	assert.Nil(t, ag.SubAgents())
	assert.Nil(t, ag.FindSubAgent("missing"))

	_, err := ag.Run(context.Background(), &agent.Invocation{})
	assert.Error(t, err)
}

func TestRunOptionsResumeCheckpoint(t *testing.T) {
	assert.False(t, runOptionsResumeCheckpoint(agent.RunOptions{}))
	assert.False(t, runOptionsResumeCheckpoint(agent.RunOptions{
		RuntimeState: map[string]any{graph.CfgKeyConfigurable: "not-a-map"},
	}))
	assert.True(t, runOptionsResumeCheckpoint(agent.RunOptions{
		RuntimeState: map[string]any{graph.CfgKeyCheckpointID: "checkpoint"},
	}))
	assert.True(t, runOptionsResumeCheckpoint(agent.RunOptions{
		RuntimeState: map[string]any{
			graph.CfgKeyConfigurable: map[string]any{
				graph.CfgKeyCheckpointID: "checkpoint",
			},
		},
	}))
}

func TestCandidateSelectorHelpers(t *testing.T) {
	assert.Equal(t, 3, candidateSelectOptions{parallelism: 3}.effectiveParallelism())
	assert.GreaterOrEqual(t, candidateSelectOptions{}.effectiveParallelism(), 1)

	attempt := &CandidateAttempt{Index: 2}
	assert.Same(t, attempt, findCandidateAttempt([]*CandidateAttempt{nil, attempt}, 2))
	assert.Nil(t, findCandidateAttempt([]*CandidateAttempt{attempt}, 1))

	recorder := newCandidateEventRecorder()
	recorder.Add(nil)
	evt := event.New("invocation", "agent")
	recorder.Add(evt)
	recorder.Add(evt)
	assert.Len(t, recorder.Events(), 1)

	events := appendCandidateStateUpdate(
		&agent.Invocation{InvocationID: "invocation", AgentName: "agent"},
		[]*event.Event{nil},
		map[string][]byte{"k": []byte("v")},
	)
	requireEvent := events[len(events)-1]
	assert.Equal(t, model.ObjectTypeStateUpdate, requireEvent.Object)
	assert.Equal(t, "v", string(requireEvent.StateDelta["k"]))

	existing := event.New("invocation", "agent")
	events = appendCandidateStateUpdate(
		&agent.Invocation{InvocationID: "invocation", AgentName: "agent"},
		[]*event.Event{existing},
		map[string][]byte{"k2": []byte("v2")},
	)
	assert.Same(t, existing, events[0])
	assert.Equal(t, "v2", string(existing.StateDelta["k2"]))
}
