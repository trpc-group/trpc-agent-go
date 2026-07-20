//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package extractor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestContainsStateLossRelation(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"Replaced the old laptop.",
		"Moving on from the old tank.",
		"Previously had a five-gallon tank.",
		"No longer owns the old phone.",
	} {
		assert.True(t, containsStateLossRelation(text), text)
	}
	for _, text := range []string{
		"Has an old laptop.",
		"Set up a new desktop.",
		"Has a five-gallon and a twenty-gallon tank.",
		"Monitors ammonia in addition to nitrite.",
		"Uses the desktop alongside the laptop.",
		"Uses tea instead of coffee today.",
		"Start by replacing 1-2 tablespoons of granulated sugar.",
	} {
		assert.False(t, containsStateLossRelation(text), text)
	}
}

func TestExtractorStateRecoveryIgnoresAssistantResultMemory(t *testing.T) {
	resultArgs := mustOperationArgs(t, map[string]any{
		"memory_id": "result-1",
		"memory": "Assistant result: Start by replacing 1-2 tablespoons " +
			"of granulated sugar.",
	})
	m := stateRecoverySequenceModel([]model.ToolCall{
		makeToolCall(memory.UpdateToolName, resultArgs),
	})
	e := NewExtractor(m,
		WithUpdatePolicy(UpdatePolicyHistoryPreserving),
	)

	operations, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("How should I use muscovado in frosting?"),
		model.NewAssistantMessage(
			"Start by replacing 1-2 tablespoons of granulated sugar.",
		),
	}, nil)

	require.NoError(t, err)
	require.Len(t, operations, 1)
	assert.Equal(t, OperationUpdate, operations[0].Type)
	assert.Len(t, m.requests, 1)
}

func TestExtractorRecoversUngroundedStateTransition(t *testing.T) {
	safeArgs := mustOperationArgs(t, map[string]any{
		"memory": "Has experience cycling an aquarium.",
	})
	suspectArgs := mustOperationArgs(t, map[string]any{
		"memory":       "Set up a new 20-gallon tank, moving on from the old 5-gallon tank.",
		"memory_kind":  "episode",
		"event_time":   "2023-05-20",
		"topics":       []string{"aquarium", "setup"},
		"participants": []string{"Finley"},
		"location":     "home",
	})
	recoveredArgs := mustOperationArgs(t, map[string]any{
		"memory": "Set up a new 20-gallon tank.",
	})
	m := stateRecoverySequenceModel(
		[]model.ToolCall{
			makeToolCall(memory.AddToolName, safeArgs),
			makeToolCall(memory.AddToolName, suspectArgs),
		},
		[]model.ToolCall{
			makeToolCall(groundedStateAddToolName, recoveredArgs),
		},
	)
	e := NewExtractor(m,
		WithUpdatePolicy(UpdatePolicyHistoryPreserving),
	)

	operations, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage(
			"I have an old 5-gallon tank. I've since set up a new 20-gallon tank.",
		),
		model.NewAssistantMessage("Congratulations on the new setup."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, operations, 2)
	assert.Equal(t, "Has experience cycling an aquarium.",
		operations[0].Memory)
	assert.Equal(t, "Set up a new 20-gallon tank.",
		operations[1].Memory)
	assert.Equal(t, memory.KindEpisode, operations[1].MemoryKind)
	require.NotNil(t, operations[1].EventTime)
	assert.Equal(t, "2023-05-20", operations[1].EventTime.Format("2006-01-02"))
	assert.Equal(t, []string{"aquarium", "setup"}, operations[1].Topics)
	assert.Equal(t, []string{"Finley"}, operations[1].Participants)
	assert.Equal(t, "home", operations[1].Location)
	require.Len(t, m.requests, 2)
	assert.Len(t, m.requests[1].Tools, 1)
	assert.Contains(t, m.requests[1].Tools, groundedStateAddToolName)
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"<grounded_state_recovery>")
	assert.Contains(t,
		m.requests[1].Messages[len(m.requests[1].Messages)-1].Content,
		"moving on from")
}

func TestExtractorStateRecoveryIgnoresCoexistenceLanguage(t *testing.T) {
	coexistenceArgs := mustOperationArgs(t, map[string]any{
		"memory": "Monitors ammonia in addition to nitrite.",
	})
	m := stateRecoverySequenceModel([]model.ToolCall{
		makeToolCall(memory.AddToolName, coexistenceArgs),
	})
	e := NewExtractor(m,
		WithUpdatePolicy(UpdatePolicyHistoryPreserving),
	)

	operations, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Should I also monitor ammonia?"),
	}, nil)

	require.NoError(t, err)
	require.Len(t, operations, 1)
	assert.Equal(t, "Monitors ammonia in addition to nitrite.",
		operations[0].Memory)
	assert.Len(t, m.requests, 1)
}

func TestExtractorStateRecoveryPreservesParaphrasedExplicitLoss(t *testing.T) {
	explicitArgs := mustOperationArgs(t, map[string]any{
		"memory": "Previously had a five-gallon tank.",
	})
	m := stateRecoverySequenceModel([]model.ToolCall{
		makeToolCall(memory.AddToolName, explicitArgs),
	})
	e := NewExtractor(m,
		WithUpdatePolicy(UpdatePolicyHistoryPreserving),
	)

	operations, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("I used to have a five-gallon tank."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, operations, 1)
	assert.Equal(t, "Previously had a five-gallon tank.",
		operations[0].Memory)
	assert.Len(t, m.requests, 1)
}

func TestExtractorStateRecoveryKeepsDefaultPolicyCompatible(t *testing.T) {
	suspectArgs := mustOperationArgs(t, map[string]any{
		"memory": "Set up a desktop, replacing the old laptop.",
	})
	m := stateRecoverySequenceModel([]model.ToolCall{
		makeToolCall(memory.AddToolName, suspectArgs),
	})
	e := NewExtractor(m)

	operations, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("I have an old laptop and set up a desktop."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, operations, 1)
	assert.Contains(t, operations[0].Memory, "replacing")
	assert.Len(t, m.requests, 1)
}

func TestExtractorStateRecoveryPreservesExplicitTransition(t *testing.T) {
	explicitArgs := mustOperationArgs(t, map[string]any{
		"memory": "Traded in the old laptop and replaced it with a desktop.",
	})
	m := stateRecoverySequenceModel([]model.ToolCall{
		makeToolCall(memory.AddToolName, explicitArgs),
	})
	e := NewExtractor(m,
		WithUpdatePolicy(UpdatePolicyHistoryPreserving),
	)

	operations, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage(
			"I traded in my old laptop and replaced it with a desktop.",
		),
	}, nil)

	require.NoError(t, err)
	require.Len(t, operations, 1)
	assert.Contains(t, operations[0].Memory, "replaced")
	assert.Len(t, m.requests, 1)
}

func TestExtractorStateRecoveryFailurePreservesPrimary(t *testing.T) {
	suspectArgs := mustOperationArgs(t, map[string]any{
		"memory": "Set up a desktop, replacing the old laptop.",
	})
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			toolCallResponse([]model.ToolCall{
				makeToolCall(memory.AddToolName, suspectArgs),
			}),
		},
		errors: []error{nil, errors.New("recovery unavailable")},
	}
	e := NewExtractor(m,
		WithUpdatePolicy(UpdatePolicyHistoryPreserving),
	)

	operations, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("I have an old laptop and set up a desktop."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, operations, 1)
	assert.Contains(t, operations[0].Memory, "replacing")
	assert.Len(t, m.requests, 2)
}

func TestExtractorStateRecoveryRejectsUngroundedCorrection(t *testing.T) {
	suspectArgs := mustOperationArgs(t, map[string]any{
		"memory": "Set up a desktop, replacing the old laptop.",
	})
	stillSuspectArgs := mustOperationArgs(t, map[string]any{
		"memory": "Set up a desktop, moving on from the old laptop.",
	})
	m := stateRecoverySequenceModel(
		[]model.ToolCall{
			makeToolCall(memory.AddToolName, suspectArgs),
		},
		[]model.ToolCall{
			makeToolCall(groundedStateAddToolName, stillSuspectArgs),
		},
	)
	e := NewExtractor(m,
		WithUpdatePolicy(UpdatePolicyHistoryPreserving),
	)

	operations, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("I have an old laptop and set up a desktop."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, operations, 1)
	assert.Equal(t, "Set up a desktop, replacing the old laptop.",
		operations[0].Memory)
}

func mustOperationArgs(t *testing.T, values map[string]any) []byte {
	t.Helper()
	result, err := json.Marshal(values)
	require.NoError(t, err)
	return result
}

func stateRecoverySequenceModel(
	toolCalls ...[]model.ToolCall,
) *sequenceModel {
	responses := make([][]*model.Response, 0, len(toolCalls))
	for _, calls := range toolCalls {
		responses = append(responses, toolCallResponse(calls))
	}
	return &sequenceModel{name: "test-model", responses: responses}
}

func toolCallResponse(calls []model.ToolCall) []*model.Response {
	return []*model.Response{{
		Choices: []model.Choice{{Message: model.Message{
			ToolCalls: calls,
		}}},
	}}
}
