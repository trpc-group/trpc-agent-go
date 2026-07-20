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

func TestHasStructuredAssistantResultCandidate(t *testing.T) {
	t.Parallel()
	contentPartText := "- Alpha\n- Beta\n- Gamma"

	assert.True(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewAssistantMessage("1. Alpha\n2. Beta\n3. Gamma"),
	}))
	assert.True(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewAssistantMessage("* Alpha\n* Beta\n* Gamma"),
	}))
	assert.True(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewAssistantMessage("\u2022 Alpha\n\u2022 Beta\n\u2022 Gamma"),
	}))
	assert.True(t, hasStructuredAssistantResultCandidate([]model.Message{{
		Role: model.RoleAssistant,
		ContentParts: []model.ContentPart{{
			Type: model.ContentTypeText,
			Text: &contentPartText,
		}},
	}}))
	assert.False(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewAssistantMessage("- Alpha\n- Beta"),
	}))
	assert.False(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewUserMessage("1. Alpha\n2. Beta\n3. Gamma"),
	}))
	assert.False(t, hasStructuredAssistantResultCandidate([]model.Message{
		model.NewAssistantMessage("Evolution is the selected entity."),
	}))
}

func TestExtractor_RecoversStructuredAssistantResult(t *testing.T) {
	primaryArgs, err := json.Marshal(map[string]any{
		"memory": "Requested entity prediction for an article.",
	})
	require.NoError(t, err)
	resultArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: Predicted entities include " +
			"Dr. Arati Prabhakar, ITER, and Livermore National Laboratory.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(memory.AddToolName, primaryArgs),
			}}}}}},
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, resultArgs),
			}}}}}},
		},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true)).(*memoryExtractor)

	primary, assistantResults, err := e.ExtractOperationStages(
		context.Background(),
		[]model.Message{
			model.NewUserMessage("Predict the entities in this article."),
			model.NewAssistantMessage("* Dr. Arati Prabhakar\n* ITER\n" +
				"* Livermore National Laboratory"),
		},
		nil,
	)

	require.NoError(t, err)
	require.Len(t, primary, 1)
	require.Len(t, assistantResults, 1)
	assert.Contains(t, assistantResults[0].Memory, "Dr. Arati Prabhakar")
	require.Len(t, m.requests, 2)
	assert.Len(t, m.requests[1].Tools, 1)
	assert.Contains(t, m.requests[1].Tools, assistantResultAddToolName)
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"<assistant_result_recovery>")
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"every substantive result claim")
	assert.Contains(t, m.requests[1].Messages[0].Content,
		"Ruby, Python, or PHP")
	assert.Equal(t, model.RoleUser,
		m.requests[1].Messages[len(m.requests[1].Messages)-1].Role)
}

func TestExtractor_DoesNotRecoverWhenCombinedPassHasResult(t *testing.T) {
	resultArgs, err := json.Marshal(map[string]any{
		"memory": "Assistant result: Recommended Alpha, Beta, and Gamma.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(assistantResultAddToolName, resultArgs),
			}}}}}},
		},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Which options should I use?"),
		model.NewAssistantMessage("- Alpha\n- Beta\n- Gamma"),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Len(t, m.requests, 1)
}

func TestExtractor_DoesNotRecoverUnstructuredAssistantResult(t *testing.T) {
	m := &sequenceModel{name: "test-model", responses: [][]*model.Response{nil}}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("What does eventual consistency mean?"),
		model.NewAssistantMessage("It means replicas may converge over time."),
	}, nil)

	require.NoError(t, err)
	assert.Empty(t, ops)
	assert.Len(t, m.requests, 1)
}

func TestExtractor_StructuredRecoveryMayEmitNoResult(t *testing.T) {
	m := &sequenceModel{
		name:      "test-model",
		responses: [][]*model.Response{nil, nil},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Brainstorm cache invalidation options."),
		model.NewAssistantMessage("- TTL\n- Version keys\n- Event invalidation"),
	}, nil)

	require.NoError(t, err)
	assert.Empty(t, ops)
	assert.Len(t, m.requests, 2)
}

func TestExtractor_StructuredRecoveryFailurePreservesPrimary(t *testing.T) {
	primaryArgs, err := json.Marshal(map[string]any{
		"memory": "Is evaluating cache invalidation options.",
	})
	require.NoError(t, err)
	m := &sequenceModel{
		name: "test-model",
		responses: [][]*model.Response{
			{{Choices: []model.Choice{{Message: model.Message{ToolCalls: []model.ToolCall{
				makeToolCall(memory.AddToolName, primaryArgs),
			}}}}}},
		},
		errors: []error{nil, errors.New("recovery unavailable")},
	}
	e := NewExtractor(m, WithAssistantResultExtraction(true))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Compare the cache invalidation options."),
		model.NewAssistantMessage("- TTL\n- Version keys\n- Event invalidation"),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, "Is evaluating cache invalidation options.", ops[0].Memory)
	assert.Len(t, m.requests, 2)
}
