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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// mockModel is a mock implementation of model.Model for testing.
type mockModel struct {
	name      string
	responses []*model.Response
	err       error

	called      int
	lastRequest *model.Request
}

func (m *mockModel) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	m.called++
	m.lastRequest = request
	if m.err != nil {
		return nil, m.err
	}
	ch := make(chan *model.Response, len(m.responses))
	for _, rsp := range m.responses {
		ch <- rsp
	}
	close(ch)
	return ch, nil
}

func (m *mockModel) Info() model.Info {
	return model.Info{Name: m.name}
}

// newMockModelWithToolCalls creates a mock model that returns tool calls.
func newMockModelWithToolCalls(toolCalls []model.ToolCall) *mockModel {
	return &mockModel{
		name: "test-model",
		responses: []*model.Response{
			{
				Choices: []model.Choice{
					{
						Message: model.Message{
							ToolCalls: toolCalls,
						},
					},
				},
			},
		},
	}
}

// makeToolCall creates a ToolCall with the given name and arguments.
func makeToolCall(name string, args []byte) model.ToolCall {
	return model.ToolCall{
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      name,
			Arguments: args,
		},
	}
}

func TestNewExtractor(t *testing.T) {
	m := &mockModel{name: "test-model"}

	t.Run("default prompt", func(t *testing.T) {
		e := NewExtractor(m)
		require.NotNil(t, e)

		// Check metadata.
		meta := e.Metadata()
		assert.Equal(t, "test-model", meta[metadataKeyModelName])
		assert.True(t, meta[metadataKeyModelAvailable].(bool))
	})

	t.Run("custom prompt", func(t *testing.T) {
		customPrompt := "Custom extraction prompt."
		e := NewExtractor(m, WithPrompt(customPrompt))
		require.NotNil(t, e)

		// Verify the extractor was created with custom prompt.
		extractor := e.(*memoryExtractor)
		assert.Equal(t, customPrompt, extractor.prompt)
	})

	t.Run("empty prompt ignored", func(t *testing.T) {
		e := NewExtractor(m, WithPrompt(""))
		require.NotNil(t, e)

		// Verify default prompt is used.
		extractor := e.(*memoryExtractor)
		assert.Equal(t, defaultPrompt, extractor.prompt)
	})
}

func TestExtractor_Extract_NoModel(t *testing.T) {
	e := NewExtractor(nil)
	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no model configured")
	assert.Nil(t, ops)
}

func TestExtractor_Extract_EmptyMessages(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)

	ops, err := e.Extract(context.Background(), nil, nil)

	assert.NoError(t, err)
	assert.Nil(t, ops)
}

func TestExtractor_Extract_ModelError(t *testing.T) {
	m := &mockModel{
		name: "test-model",
		err:  errors.New("model error"),
	}
	e := NewExtractor(m)

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model call failed")
	assert.Nil(t, ops)
}

func TestExtractor_Extract_ResponseError(t *testing.T) {
	m := &mockModel{
		name: "test-model",
		responses: []*model.Response{
			{
				Error: &model.ResponseError{
					Message: "API error",
				},
			},
		},
	}
	e := NewExtractor(m)

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "model error")
	assert.Nil(t, ops)
}

func TestExtractor_Extract_BeforeModelCallback_ModifiesRequest(t *testing.T) {
	m := &mockModel{
		name: "test-model",
		responses: []*model.Response{{
			Choices: []model.Choice{{
				Message: model.Message{},
			}},
		}},
	}

	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(_ context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			args.Request.Messages = append(
				args.Request.Messages,
				model.NewUserMessage("sentinel"),
			)
			return nil, nil
		},
	)
	e := NewExtractor(m, WithModelCallbacks(callbacks))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	require.NoError(t, err)
	assert.Nil(t, ops)
	require.NotNil(t, m.lastRequest)
	require.Greater(t, len(m.lastRequest.Messages), 0)
	last := m.lastRequest.Messages[len(m.lastRequest.Messages)-1]
	assert.Equal(t, "sentinel", last.Content)
}

func TestExtractor_Extract_BeforeModelCallback_ShortCircuit(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"memory": "User likes coffee.",
	})
	customResp := &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				ToolCalls: []model.ToolCall{makeToolCall(memory.AddToolName, args)},
			},
		}},
	}
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(_ context.Context, _ *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return &model.BeforeModelResult{CustomResponse: customResp}, nil
		},
	)

	m := &mockModel{name: "test-model", err: errors.New("should not call")}
	e := NewExtractor(m, WithModelCallbacks(callbacks))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, OperationAdd, ops[0].Type)
	assert.Equal(t, "User likes coffee.", ops[0].Memory)
	assert.Equal(t, 0, m.called)
}

func TestExtractor_Extract_BeforeModelCallback_Error(t *testing.T) {
	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(_ context.Context, _ *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			return nil, errors.New("before failed")
		},
	)

	m := &mockModel{name: "test-model"}
	e := NewExtractor(m, WithModelCallbacks(callbacks))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "before model callback failed")
	assert.Nil(t, ops)
	assert.Equal(t, 0, m.called)
}

func TestExtractor_Extract_AfterModelCallback_OverridesError(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"memory": "User likes tea.",
	})

	m := &mockModel{
		name: "test-model",
		responses: []*model.Response{{
			Error: &model.ResponseError{Message: "API error"},
		}},
	}

	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(_ context.Context, _ *model.AfterModelArgs) (*model.AfterModelResult, error) {
			return &model.AfterModelResult{CustomResponse: &model.Response{
				Choices: []model.Choice{{
					Message: model.Message{
						ToolCalls: []model.ToolCall{makeToolCall(memory.AddToolName, args)},
					},
				}},
			}}, nil
		},
	)
	e := NewExtractor(m, WithModelCallbacks(callbacks))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, OperationAdd, ops[0].Type)
	assert.Equal(t, "User likes tea.", ops[0].Memory)
}

func TestExtractor_Extract_AfterModelCallback_Error(t *testing.T) {
	m := &mockModel{
		name: "test-model",
		responses: []*model.Response{{
			Choices: []model.Choice{{
				Message: model.Message{},
			}},
		}},
	}

	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(_ context.Context, _ *model.AfterModelArgs) (*model.AfterModelResult, error) {
			return nil, errors.New("after failed")
		},
	)
	e := NewExtractor(m, WithModelCallbacks(callbacks))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "after model callback failed")
	assert.Nil(t, ops)
}

func TestExtractor_Extract_AddOperation(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"memory": "User likes coffee.",
		"topics": []string{"preferences", "food"},
	})
	m := newMockModelWithToolCalls([]model.ToolCall{
		makeToolCall(memory.AddToolName, args),
	})
	e := NewExtractor(m)

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("I love coffee."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, OperationAdd, ops[0].Type)
	assert.Equal(t, "User likes coffee.", ops[0].Memory)
	assert.Equal(t, []string{"preferences", "food"}, ops[0].Topics)
}

func TestExtractor_Extract_UpdateOperation(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"memory_id": "mem-123",
		"memory":    "User now prefers tea.",
		"topics":    []string{"preferences"},
	})
	m := newMockModelWithToolCalls([]model.ToolCall{
		makeToolCall(memory.UpdateToolName, args),
	})
	e := NewExtractor(m)

	existing := []*memory.Entry{
		{
			ID:      "mem-123",
			Memory:  &memory.Memory{Memory: "User likes coffee."},
			AppName: "test-app",
			UserID:  "user-1",
		},
	}
	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Actually, I prefer tea now."),
	}, existing)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, OperationUpdate, ops[0].Type)
	assert.Equal(t, "mem-123", ops[0].MemoryID)
	assert.Equal(t, "User now prefers tea.", ops[0].Memory)
}

func TestExtractor_Extract_DeleteOperation(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"memory_id": "mem-456",
	})
	m := newMockModelWithToolCalls([]model.ToolCall{
		makeToolCall(memory.DeleteToolName, args),
	})
	e := NewExtractor(m)

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Please forget my coffee preference."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, OperationDelete, ops[0].Type)
	assert.Equal(t, "mem-456", ops[0].MemoryID)
}

func TestExtractor_Extract_MultipleOperations(t *testing.T) {
	addArgs, _ := json.Marshal(map[string]any{
		"memory": "User works as a software engineer.",
	})
	updateArgs, _ := json.Marshal(map[string]any{
		"memory_id": "mem-1",
		"memory":    "User lives in Beijing.",
	})
	m := newMockModelWithToolCalls([]model.ToolCall{
		makeToolCall(memory.AddToolName, addArgs),
		makeToolCall(memory.UpdateToolName, updateArgs),
	})
	e := NewExtractor(m)

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("I'm a software engineer living in Beijing."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 2)
	assert.Equal(t, OperationAdd, ops[0].Type)
	assert.Equal(t, OperationUpdate, ops[1].Type)
}

func TestExtractor_Extract_EmptyChoices(t *testing.T) {
	m := &mockModel{
		name: "test-model",
		responses: []*model.Response{
			{Choices: nil},
		},
	}
	e := NewExtractor(m)

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	assert.NoError(t, err)
	assert.Nil(t, ops)
}

func TestExtractor_SetPrompt(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)
	extractor := e.(*memoryExtractor)

	// Set new prompt.
	newPrompt := "New extraction prompt."
	e.SetPrompt(newPrompt)
	assert.Equal(t, newPrompt, extractor.prompt)

	// Empty prompt should be ignored.
	e.SetPrompt("")
	assert.Equal(t, newPrompt, extractor.prompt)
}

func TestExtractor_SetModel(t *testing.T) {
	m1 := &mockModel{name: "model-1"}
	m2 := &mockModel{name: "model-2"}
	e := NewExtractor(m1)

	// Set new model.
	e.SetModel(m2)
	meta := e.Metadata()
	assert.Equal(t, "model-2", meta[metadataKeyModelName])

	// Nil model should be ignored.
	e.SetModel(nil)
	meta = e.Metadata()
	assert.Equal(t, "model-2", meta[metadataKeyModelName])
}

func TestExtractor_Metadata_NoModel(t *testing.T) {
	e := NewExtractor(nil)
	meta := e.Metadata()

	assert.Equal(t, "", meta[metadataKeyModelName])
	assert.False(t, meta[metadataKeyModelAvailable].(bool))
}

func TestExtractor_BuildSystemPrompt_WithExistingMemories(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)
	extractor := e.(*memoryExtractor)

	refDate := time.Date(2023, 5, 8, 0, 0, 0, 0, time.UTC)
	existing := []*memory.Entry{
		{
			ID:     "mem-1",
			Memory: &memory.Memory{Memory: "User likes coffee."},
		},
		{
			ID:     "mem-2",
			Memory: &memory.Memory{Memory: "User is 30 years old."},
		},
	}

	prompt := extractor.buildSystemPrompt(refDate, existing)

	assert.Contains(t, prompt, "You are a Memory Manager")
	assert.NotContains(t, prompt, currentDatePlaceholder)
	assert.Contains(t, prompt, "Today's date is 2023-05-08.")
	assert.Contains(t, prompt, "<existing_memories>")
	assert.Contains(t, prompt,
		"[mem-1] User likes coffee.")
	assert.Contains(t, prompt,
		"[mem-2] User is 30 years old.")
	assert.Contains(t, prompt, "</existing_memories>")
}

func TestExtractor_BuildSystemPrompt_EmptyExisting(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)
	extractor := e.(*memoryExtractor)

	refDate := time.Date(2023, 5, 8, 0, 0, 0, 0, time.UTC)
	prompt := extractor.buildSystemPrompt(refDate, nil)

	// Prompt always includes available_actions now.
	assert.Contains(t, prompt, "You are a Memory Manager")
	assert.Contains(t, prompt, "Today's date is 2023-05-08.")
	assert.NotContains(t, prompt, currentDatePlaceholder)
	assert.Contains(t, prompt, "<available_actions>")
	assert.Contains(t, prompt, "</available_actions>")
	assert.NotContains(t, prompt, "<existing_memories>")
}

func TestExtractor_BuildSystemPrompt_NilMemory(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)
	extractor := e.(*memoryExtractor)

	refDate := time.Date(2023, 5, 8, 0, 0, 0, 0, time.UTC)
	existing := []*memory.Entry{
		{
			ID:     "mem-1",
			Memory: nil, // Nil memory should be skipped.
		},
		{
			ID:     "mem-2",
			Memory: &memory.Memory{Memory: "Valid memory."},
		},
	}

	prompt := extractor.buildSystemPrompt(refDate, existing)

	assert.Contains(t, prompt,
		"[mem-2] Valid memory.")
	assert.NotContains(t, prompt, "[mem-1]")
}

func TestExtractor_ParseToolCall_InvalidJSON(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)
	extractor := e.(*memoryExtractor)

	call := model.ToolCall{
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      memory.AddToolName,
			Arguments: []byte("invalid json"),
		},
	}

	op := extractor.parseToolCall(context.Background(), call)
	assert.Nil(t, op)
}

func TestExtractor_ParseToolCall_UnknownTool(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)
	ext := e.(*memoryExtractor)

	args, _ := json.Marshal(map[string]any{
		"memory": "test",
	})
	call := model.ToolCall{
		Type: "function",
		Function: model.FunctionDefinitionParam{
			Name:      "unknown_tool",
			Arguments: args,
		},
	}

	op := ext.parseToolCall(context.Background(), call)
	assert.Nil(t, op)
}

func TestExtractor_SetEnabledTools(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)
	ext := e.(*memoryExtractor)

	t.Run("set enabled tools", func(t *testing.T) {
		enabled := map[string]struct{}{
			memory.AddToolName: {},
		}
		ext.SetEnabledTools(enabled)
		_, hasAdd := ext.enabledTools[memory.AddToolName]
		_, hasClear := ext.enabledTools[memory.ClearToolName]
		assert.True(t, hasAdd)
		assert.False(t, hasClear)
	})

	t.Run("copies map to prevent mutation", func(t *testing.T) {
		orig := map[string]struct{}{
			memory.AddToolName: {},
		}
		ext.SetEnabledTools(orig)
		// Mutate the original map.
		delete(orig, memory.AddToolName)
		// The extractor's copy should be unchanged.
		_, hasAdd := ext.enabledTools[memory.AddToolName]
		assert.True(t, hasAdd)
	})

	t.Run("nil resets", func(t *testing.T) {
		ext.SetEnabledTools(map[string]struct{}{
			memory.AddToolName: {},
		})
		assert.NotNil(t, ext.enabledTools)
		ext.SetEnabledTools(nil)
		assert.Nil(t, ext.enabledTools)
	})
}

func TestFilterTools(t *testing.T) {
	// Use the package-level backgroundTools map.
	all := backgroundTools

	t.Run("nil enabled returns all", func(t *testing.T) {
		result := filterTools(all, nil)
		assert.Equal(t, all, result)
	})

	t.Run("empty enabled returns none", func(t *testing.T) {
		result := filterTools(all, map[string]struct{}{})
		assert.Empty(t, result)
	})

	t.Run("filters disabled tools", func(t *testing.T) {
		enabled := map[string]struct{}{
			memory.AddToolName:    {},
			memory.UpdateToolName: {},
		}
		result := filterTools(all, enabled)
		assert.Len(t, result, 2)
		assert.Contains(t, result, memory.AddToolName)
		assert.Contains(t, result, memory.UpdateToolName)
		assert.NotContains(t, result, memory.DeleteToolName)
		assert.NotContains(t, result, memory.ClearToolName)
	})

	t.Run("missing keys treated as disabled", func(t *testing.T) {
		enabled := map[string]struct{}{
			memory.AddToolName: {},
		}
		result := filterTools(all, enabled)
		assert.Len(t, result, 1)
		assert.Contains(t, result, memory.AddToolName)
	})
}

func TestExtractor_AvailableActionsBlock(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)
	ext := e.(*memoryExtractor)

	t.Run("all tools enabled by default", func(t *testing.T) {
		block := ext.availableActionsBlock()
		assert.Contains(t, block, memory.AddToolName)
		assert.Contains(t, block, memory.UpdateToolName)
		assert.Contains(t, block, memory.DeleteToolName)
		assert.Contains(t, block, memory.ClearToolName)
	})

	t.Run("only enabled tools shown", func(t *testing.T) {
		ext.SetEnabledTools(map[string]struct{}{
			memory.AddToolName:    {},
			memory.UpdateToolName: {},
		})
		block := ext.availableActionsBlock()
		assert.Contains(t, block, memory.AddToolName)
		assert.Contains(t, block, memory.UpdateToolName)
		assert.NotContains(t, block, memory.DeleteToolName)
		assert.NotContains(t, block, memory.ClearToolName)
		// Reset.
		ext.SetEnabledTools(nil)
	})

	t.Run("no tools enabled", func(t *testing.T) {
		ext.SetEnabledTools(map[string]struct{}{})
		block := ext.availableActionsBlock()
		assert.Contains(t, block, "No actions available.")
		// Reset.
		ext.SetEnabledTools(nil)
	})

	t.Run("tool in order but not in descriptions", func(t *testing.T) {
		// Temporarily add a name to toolActionOrder that has no description.
		origOrder := toolActionOrder
		toolActionOrder = append([]string{"nonexistent_tool"}, origOrder...)
		defer func() { toolActionOrder = origOrder }()

		// Enable the nonexistent tool so it passes the enabledTools check.
		ext.SetEnabledTools(map[string]struct{}{
			"nonexistent_tool":    {},
			memory.AddToolName:    {},
			memory.UpdateToolName: {},
		})
		block := ext.availableActionsBlock()
		// The nonexistent tool should be skipped (no description).
		assert.NotContains(t, block, "nonexistent_tool")
		// But real tools should still appear.
		assert.Contains(t, block, memory.AddToolName)
		assert.Contains(t, block, memory.UpdateToolName)
		// Reset.
		ext.SetEnabledTools(nil)
	})
}

func TestExtractor_Extract_FilteredTools(t *testing.T) {
	args, _ := json.Marshal(map[string]any{
		"memory": "User likes coffee.",
	})
	m := newMockModelWithToolCalls([]model.ToolCall{
		makeToolCall(memory.AddToolName, args),
	})
	e := NewExtractor(m)
	ext := e.(*memoryExtractor)

	// Only enable add tool.
	ext.SetEnabledTools(map[string]struct{}{
		memory.AddToolName: {},
	})

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("I love coffee."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)

	// Verify the model request only contains enabled tools.
	require.NotNil(t, m.lastRequest)
	assert.Len(t, m.lastRequest.Tools, 1)
	for name := range m.lastRequest.Tools {
		assert.Equal(t, memory.AddToolName, name)
	}
}

func TestExtractor_EnabledToolsConfigurer(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)

	// enabledToolsConfigurer is the local interface for testing.
	type enabledToolsConfigurer interface {
		SetEnabledTools(enabled map[string]struct{})
	}

	// Verify the concrete type implements enabledToolsConfigurer.
	configurer, ok := e.(enabledToolsConfigurer)
	require.True(t, ok)

	enabled := map[string]struct{}{
		memory.AddToolName: {},
	}
	configurer.SetEnabledTools(enabled)

	// Verify through the internal state.
	ext := e.(*memoryExtractor)
	_, hasAdd := ext.enabledTools[memory.AddToolName]
	assert.True(t, hasAdd)
}

func TestInferReferenceDate(t *testing.T) {
	t.Run("uses context reference date", func(t *testing.T) {
		refDate := time.Date(
			2023, 5, 8, 0, 0, 0, 0, time.UTC,
		)
		ctx := WithReferenceDate(
			context.Background(), refDate,
		)
		d := referenceDate(ctx)
		assert.Equal(t, 2023, d.Year())
		assert.Equal(t, time.May, d.Month())
		assert.Equal(t, 8, d.Day())
	})

	t.Run("falls back to now without context", func(t *testing.T) {
		d := referenceDate(context.Background())
		assert.InDelta(t,
			float64(time.Now().UTC().Unix()),
			float64(d.Unix()), 5,
		)
	})
}

func TestExtractor_BuildSystemPrompt_WithTopics(t *testing.T) {
	m := &mockModel{name: "test-model"}
	e := NewExtractor(m)
	ext := e.(*memoryExtractor)

	existing := []*memory.Entry{
		{
			ID: "mem-1",
			Memory: &memory.Memory{
				Memory: "Likes coffee.",
				Topics: []string{"preferences", "food"},
			},
		},
		{
			ID: "mem-2",
			Memory: &memory.Memory{
				Memory: "Works at Tencent.",
				Topics: nil, // No topics.
			},
		},
	}

	prompt := ext.buildSystemPrompt(time.Now(), existing)

	// mem-1 should have topics displayed.
	assert.Contains(t, prompt, "[mem-1] Likes coffee. (topics: preferences, food)")
	// mem-2 should not have topics section.
	assert.Contains(t, prompt, "[mem-2] Works at Tencent.")
	assert.NotContains(t, prompt, "[mem-2] Works at Tencent. (topics:")
}

func TestExtractor_BeforeModelCallback_UpdatesContext(t *testing.T) {
	type ctxKey struct{}
	m := &mockModel{
		name: "test-model",
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{}}},
		}},
	}

	callbacks := model.NewCallbacks().RegisterBeforeModel(
		func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
			newCtx := context.WithValue(ctx, ctxKey{}, "injected")
			return &model.BeforeModelResult{Context: newCtx}, nil
		},
	)
	e := NewExtractor(m, WithModelCallbacks(callbacks))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	require.NoError(t, err)
	assert.Nil(t, ops)
}

func TestExtractor_AfterModelCallback_UpdatesContext(t *testing.T) {
	type ctxKey struct{}
	m := &mockModel{
		name: "test-model",
		responses: []*model.Response{{
			Choices: []model.Choice{{Message: model.Message{}}},
		}},
	}

	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(ctx context.Context, args *model.AfterModelArgs) (*model.AfterModelResult, error) {
			newCtx := context.WithValue(ctx, ctxKey{}, "injected")
			return &model.AfterModelResult{Context: newCtx}, nil
		},
	)
	e := NewExtractor(m, WithModelCallbacks(callbacks))

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	require.NoError(t, err)
	assert.Nil(t, ops)
}

func TestExtractor_Extract_NilResponseInStream(t *testing.T) {
	m := &mockModel{
		name: "test-model",
		responses: []*model.Response{
			nil,
			{Choices: []model.Choice{{Message: model.Message{}}}},
		},
	}
	e := NewExtractor(m)

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("hello"),
	}, nil)

	assert.NoError(t, err)
	assert.Nil(t, ops)
}

func TestExtractor_Extract_ClearOperation(t *testing.T) {
	args, _ := json.Marshal(map[string]any{})
	m := newMockModelWithToolCalls([]model.ToolCall{
		makeToolCall(memory.ClearToolName, args),
	})
	e := NewExtractor(m)

	ops, err := e.Extract(context.Background(), []model.Message{
		model.NewUserMessage("Forget everything about me."),
	}, nil)

	require.NoError(t, err)
	require.Len(t, ops, 1)
	assert.Equal(t, OperationClear, ops[0].Type)
}

func TestExtractor_WithChecker(t *testing.T) {
	m := &mockModel{name: "test-model"}

	t.Run("nil checker is ignored", func(t *testing.T) {
		e := NewExtractor(m, WithChecker(nil))
		ext := e.(*memoryExtractor)
		assert.Len(t, ext.checkers, 0)
	})

	t.Run("checker added", func(t *testing.T) {
		checker := func(ctx *ExtractionContext) bool { return true }
		e := NewExtractor(m, WithChecker(checker))
		ext := e.(*memoryExtractor)
		assert.Len(t, ext.checkers, 1)
	})
}

func TestExtractor_WithCheckersAny(t *testing.T) {
	m := &mockModel{name: "test-model"}

	t.Run("empty checkers is no-op", func(t *testing.T) {
		e := NewExtractor(m, WithCheckersAny())
		ext := e.(*memoryExtractor)
		assert.Len(t, ext.checkers, 0)
	})

	t.Run("combined with OR logic", func(t *testing.T) {
		alwaysFail := func(ctx *ExtractionContext) bool { return false }
		alwaysPass := func(ctx *ExtractionContext) bool { return true }
		e := NewExtractor(m, WithCheckersAny(alwaysFail, alwaysPass))
		assert.True(t, e.ShouldExtract(&ExtractionContext{}))
	})
}

func TestModelErrFromResponse(t *testing.T) {
	t.Run("nil response", func(t *testing.T) {
		assert.Nil(t, modelErrFromResponse(nil))
	})
	t.Run("nil error", func(t *testing.T) {
		assert.Nil(t, modelErrFromResponse(&model.Response{}))
	})
	t.Run("with error", func(t *testing.T) {
		resp := &model.Response{
			Error: &model.ResponseError{
				Type:    "invalid_request",
				Message: "bad input",
			},
		}
		err := modelErrFromResponse(resp)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid_request")
		assert.Contains(t, err.Error(), "bad input")
	})
}

func TestBuildMessages_TrailingAssistantSuffix(t *testing.T) {
	m := &mockModel{name: "test-model"}
	ext := NewExtractor(m).(*memoryExtractor)

	t.Run("ends with assistant appends user", func(t *testing.T) {
		msgs := []model.Message{
			model.NewUserMessage("hello"),
			model.NewAssistantMessage("hi there"),
		}
		result := ext.buildMessages(
			context.Background(), msgs, nil,
		)
		// system + user + assistant + trailing user.
		require.Len(t, result, 4)
		assert.Equal(t, model.RoleSystem,
			result[0].Role)
		assert.Equal(t, model.RoleUser,
			result[len(result)-1].Role)
		assert.Equal(t, extractionUserSuffix,
			result[len(result)-1].Content)
	})

	t.Run("ends with user no suffix", func(t *testing.T) {
		msgs := []model.Message{
			model.NewUserMessage("hello"),
		}
		result := ext.buildMessages(
			context.Background(), msgs, nil,
		)
		// system + user.
		require.Len(t, result, 2)
		assert.Equal(t, model.RoleUser,
			result[len(result)-1].Role)
		assert.Equal(t, "hello",
			result[len(result)-1].Content)
	})

	t.Run("ends with tool no suffix", func(t *testing.T) {
		msgs := []model.Message{
			model.NewUserMessage("search for X"),
			model.NewToolMessage("t1", "search", "result"),
		}
		result := ext.buildMessages(
			context.Background(), msgs, nil,
		)
		// system + user + tool.
		require.Len(t, result, 3)
		assert.Equal(t, model.RoleTool,
			result[len(result)-1].Role)
	})

	t.Run("assistant with tool_calls no suffix",
		func(t *testing.T) {
			// When the trailing assistant message carries
			// tool_calls, appending a user message would
			// break the tool-call → tool-result ordering.
			msgs := []model.Message{
				model.NewUserMessage("check weather"),
				{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						Type: "function",
						ID:   "call_1",
						Function: model.FunctionDefinitionParam{
							Name: "get_weather",
							Arguments: []byte(
								`{"city":"Beijing"}`),
						},
					}},
				},
			}
			result := ext.buildMessages(
				context.Background(), msgs, nil,
			)
			// system + user + assistant(tool_calls).
			// No trailing user message appended.
			require.Len(t, result, 3)
			assert.Equal(t, model.RoleAssistant,
				result[len(result)-1].Role)
			assert.NotEmpty(t,
				result[len(result)-1].ToolCalls)
		})

	t.Run("extract with trailing assistant", func(t *testing.T) {
		// Verify the full Extract path sends a request
		// whose last message is user.
		args, _ := json.Marshal(map[string]any{
			"memory": "User said hello.",
			"topics": []string{"greeting"},
		})
		mm := newMockModelWithToolCalls([]model.ToolCall{
			makeToolCall(memory.AddToolName, args),
		})
		e := NewExtractor(mm)

		msgs := []model.Message{
			model.NewUserMessage("hello"),
			model.NewAssistantMessage("hi there"),
		}
		ops, err := e.Extract(
			context.Background(), msgs, nil,
		)
		require.NoError(t, err)
		require.Len(t, ops, 1)

		// Verify the request sent to the model ends
		// with a user message.
		last := mm.lastRequest.Messages[len(mm.lastRequest.Messages)-1]
		assert.Equal(t, model.RoleUser, last.Role)
	})
}
