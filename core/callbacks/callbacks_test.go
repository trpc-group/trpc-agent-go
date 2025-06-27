package callbacks

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/core/agent"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
	"trpc.group/trpc-go/trpc-agent-go/core/tool"
)

// mockTool implements tool.Tool for testing.
type mockTool struct {
	name        string
	description string
	shouldError bool
}

func (m *mockTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if m.shouldError {
		return nil, errors.New("mock tool error")
	}
	return map[string]string{
		"result": "mock result",
		"tool":   m.name,
	}, nil
}

func (m *mockTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        m.name,
		Description: m.description,
		InputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"test": {
					Type:        "string",
					Description: "Test parameter",
				},
			},
		},
	}
}

func TestNewCallbackRegistry(t *testing.T) {
	registry := NewCallbackRegistry()
	assert.NotNil(t, registry)
	assert.Empty(t, registry.BeforeAgent)
	assert.Empty(t, registry.AfterAgent)
	assert.Empty(t, registry.BeforeModel)
	assert.Empty(t, registry.AfterModel)
	assert.Empty(t, registry.BeforeTool)
	assert.Empty(t, registry.AfterTool)
}

func TestAddBeforeAgent(t *testing.T) {
	registry := NewCallbackRegistry()

	callback := func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
		return nil, false, nil
	}

	registry.AddBeforeAgent(callback)
	assert.Len(t, registry.BeforeAgent, 1)

	// Add another callback
	registry.AddBeforeAgent(callback)
	assert.Len(t, registry.BeforeAgent, 2)
}

func TestAddAfterAgent(t *testing.T) {
	registry := NewCallbackRegistry()

	callback := func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
		return nil, false, nil
	}

	registry.AddAfterAgent(callback)
	assert.Len(t, registry.AfterAgent, 1)

	// Add another callback
	registry.AddAfterAgent(callback)
	assert.Len(t, registry.AfterAgent, 2)
}

func TestAddBeforeModel(t *testing.T) {
	registry := NewCallbackRegistry()

	callback := func(ctx context.Context, req *model.Request) (*model.Response, bool, error) {
		return nil, false, nil
	}

	registry.AddBeforeModel(callback)
	assert.Len(t, registry.BeforeModel, 1)

	// Add another callback
	registry.AddBeforeModel(callback)
	assert.Len(t, registry.BeforeModel, 2)
}

func TestAddAfterModel(t *testing.T) {
	registry := NewCallbackRegistry()

	callback := func(ctx context.Context, resp *model.Response, modelErr error) (*model.Response, bool, error) {
		return nil, false, nil
	}

	registry.AddAfterModel(callback)
	assert.Len(t, registry.AfterModel, 1)

	// Add another callback
	registry.AddAfterModel(callback)
	assert.Len(t, registry.AfterModel, 2)
}

func TestAddBeforeTool(t *testing.T) {
	registry := NewCallbackRegistry()

	callback := func(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error) {
		return nil, false, nil, nil
	}

	registry.AddBeforeTool(callback)
	assert.Len(t, registry.BeforeTool, 1)

	// Add another callback
	registry.AddBeforeTool(callback)
	assert.Len(t, registry.BeforeTool, 2)
}

func TestAddAfterTool(t *testing.T) {
	registry := NewCallbackRegistry()

	callback := func(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error) {
		return nil, false, nil
	}

	registry.AddAfterTool(callback)
	assert.Len(t, registry.AfterTool, 1)

	// Add another callback
	registry.AddAfterTool(callback)
	assert.Len(t, registry.AfterTool, 2)
}

func TestRunBeforeAgent_Empty(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	customResponse, skip, err := registry.RunBeforeAgent(ctx, invocation)

	assert.Nil(t, customResponse)
	assert.False(t, skip)
	assert.NoError(t, err)
}

func TestRunBeforeAgent_Skip(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	// Add callback that returns skip=true
	registry.AddBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
		return nil, true, nil
	})

	customResponse, skip, err := registry.RunBeforeAgent(ctx, invocation)

	assert.Nil(t, customResponse)
	assert.True(t, skip)
	assert.NoError(t, err)
}

func TestRunBeforeAgent_CustomResponse(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	expectedResponse := &model.Response{
		ID:    "custom-response",
		Model: "test-model",
		Done:  true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Custom response from callback",
				},
			},
		},
	}

	// Add callback that returns custom response
	registry.AddBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
		return expectedResponse, false, nil
	})

	customResponse, skip, err := registry.RunBeforeAgent(ctx, invocation)

	assert.Equal(t, expectedResponse, customResponse)
	assert.False(t, skip)
	assert.NoError(t, err)
}

func TestRunBeforeAgent_Error(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	invocation := &agent.Invocation{}
	expectedErr := errors.New("callback error")

	// Add callback that returns error
	registry.AddBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
		return nil, false, expectedErr
	})

	customResponse, skip, err := registry.RunBeforeAgent(ctx, invocation)

	assert.Nil(t, customResponse)
	assert.False(t, skip)
	assert.Equal(t, expectedErr, err)
}

func TestRunBeforeAgent_MultipleCallbacks(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	callCount := 0

	// Add first callback that continues
	registry.AddBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
		callCount++
		return nil, false, nil
	})

	// Add second callback that skips
	registry.AddBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
		callCount++
		return nil, true, nil
	})

	// Add third callback that should not be called
	registry.AddBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
		callCount++
		return nil, false, nil
	})

	customResponse, skip, err := registry.RunBeforeAgent(ctx, invocation)

	assert.Nil(t, customResponse)
	assert.True(t, skip)
	assert.NoError(t, err)
	assert.Equal(t, 2, callCount, "Only first two callbacks should be called")
}

func TestRunAfterAgent_Empty(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	customResponse, override, err := registry.RunAfterAgent(ctx, invocation, nil)

	assert.Nil(t, customResponse)
	assert.False(t, override)
	assert.NoError(t, err)
}

func TestRunAfterAgent_Override(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	expectedResponse := &model.Response{
		ID:    "override-response",
		Model: "test-model",
		Done:  true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Override response from callback",
				},
			},
		},
	}

	// Add callback that returns override=true
	registry.AddAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
		return expectedResponse, true, nil
	})

	customResponse, override, err := registry.RunAfterAgent(ctx, invocation, nil)

	assert.Equal(t, expectedResponse, customResponse)
	assert.True(t, override)
	assert.NoError(t, err)
}

func TestRunAfterAgent_NoOverride(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	// Add callback that returns override=false
	registry.AddAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
		return &model.Response{}, false, nil
	})

	customResponse, override, err := registry.RunAfterAgent(ctx, invocation, nil)

	assert.Nil(t, customResponse)
	assert.False(t, override)
	assert.NoError(t, err)
}

func TestRunBeforeModel_Empty(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	req := &model.Request{}

	customResponse, skip, err := registry.RunBeforeModel(ctx, req)

	assert.Nil(t, customResponse)
	assert.False(t, skip)
	assert.NoError(t, err)
}

func TestRunBeforeModel_Skip(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	req := &model.Request{}

	// Add callback that returns skip=true
	registry.AddBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, bool, error) {
		return nil, true, nil
	})

	customResponse, skip, err := registry.RunBeforeModel(ctx, req)

	assert.Nil(t, customResponse)
	assert.True(t, skip)
	assert.NoError(t, err)
}

func TestRunBeforeModel_CustomResponse(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	req := &model.Request{}

	expectedResponse := &model.Response{
		ID:    "custom-model-response",
		Model: "test-model",
		Done:  true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Custom model response from callback",
				},
			},
		},
	}

	// Add callback that returns custom response
	registry.AddBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, bool, error) {
		return expectedResponse, false, nil
	})

	customResponse, skip, err := registry.RunBeforeModel(ctx, req)

	assert.Equal(t, expectedResponse, customResponse)
	assert.False(t, skip)
	assert.NoError(t, err)
}

func TestRunAfterModel_Empty(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	resp := &model.Response{}

	customResponse, override, err := registry.RunAfterModel(ctx, resp, nil)

	assert.Nil(t, customResponse)
	assert.False(t, override)
	assert.NoError(t, err)
}

func TestRunAfterModel_Override(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	resp := &model.Response{}

	expectedResponse := &model.Response{
		ID:    "override-model-response",
		Model: "test-model",
		Done:  true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Override model response from callback",
				},
			},
		},
	}

	// Add callback that returns override=true
	registry.AddAfterModel(func(ctx context.Context, resp *model.Response, modelErr error) (*model.Response, bool, error) {
		return expectedResponse, true, nil
	})

	customResponse, override, err := registry.RunAfterModel(ctx, resp, nil)

	assert.Equal(t, expectedResponse, customResponse)
	assert.True(t, override)
	assert.NoError(t, err)
}

func TestRunBeforeTool_Empty(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)

	customResult, skip, newArgs, err := registry.RunBeforeTool(ctx, mockTool, args)

	assert.Nil(t, customResult)
	assert.False(t, skip)
	assert.Equal(t, args, newArgs)
	assert.NoError(t, err)
}

func TestRunBeforeTool_Skip(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)

	// Add callback that returns skip=true
	registry.AddBeforeTool(func(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error) {
		return nil, true, nil, nil
	})

	customResult, skip, newArgs, err := registry.RunBeforeTool(ctx, mockTool, args)

	assert.Nil(t, customResult)
	assert.True(t, skip)
	assert.Nil(t, newArgs)
	assert.NoError(t, err)
}

func TestRunBeforeTool_CustomResult(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)

	expectedResult := map[string]string{"custom": "result"}

	// Add callback that returns custom result
	registry.AddBeforeTool(func(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error) {
		return expectedResult, false, args, nil
	})

	customResult, skip, newArgs, err := registry.RunBeforeTool(ctx, mockTool, args)

	assert.Equal(t, expectedResult, customResult)
	assert.False(t, skip)
	assert.Equal(t, args, newArgs)
	assert.NoError(t, err)
}

func TestRunBeforeTool_ModifiedArgs(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	originalArgs := []byte(`{"test": "value"}`)
	modifiedArgs := []byte(`{"test": "modified"}`)

	// Add callback that modifies args
	registry.AddBeforeTool(func(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error) {
		return nil, false, modifiedArgs, nil
	})

	customResult, skip, newArgs, err := registry.RunBeforeTool(ctx, mockTool, originalArgs)

	assert.Nil(t, customResult)
	assert.False(t, skip)
	assert.Equal(t, modifiedArgs, newArgs)
	assert.NoError(t, err)
}

func TestRunBeforeTool_MultipleCallbacks(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)

	callCount := 0

	// Add first callback that modifies args
	registry.AddBeforeTool(func(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error) {
		callCount++
		return nil, false, []byte(`{"modified": "1"}`), nil
	})

	// Add second callback that skips
	registry.AddBeforeTool(func(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error) {
		callCount++
		return nil, true, nil, nil
	})

	// Add third callback that should not be called
	registry.AddBeforeTool(func(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error) {
		callCount++
		return nil, false, nil, nil
	})

	customResult, skip, newArgs, err := registry.RunBeforeTool(ctx, mockTool, args)

	assert.Nil(t, customResult)
	assert.True(t, skip)
	assert.Nil(t, newArgs)
	assert.NoError(t, err)
	assert.Equal(t, 2, callCount, "Only first two callbacks should be called")
}

func TestRunAfterTool_Empty(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)
	result := map[string]string{"result": "test"}

	customResult, override, err := registry.RunAfterTool(ctx, mockTool, args, result, nil)

	assert.Nil(t, customResult)
	assert.False(t, override)
	assert.NoError(t, err)
}

func TestRunAfterTool_Override(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"result": "original"}
	expectedResult := map[string]string{"result": "override"}

	// Add callback that returns override=true
	registry.AddAfterTool(func(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error) {
		return expectedResult, true, nil
	})

	customResult, override, err := registry.RunAfterTool(ctx, mockTool, args, originalResult, nil)

	assert.Equal(t, expectedResult, customResult)
	assert.True(t, override)
	assert.NoError(t, err)
}

func TestRunAfterTool_NoOverride(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)
	originalResult := map[string]string{"result": "original"}

	// Add callback that returns override=false
	registry.AddAfterTool(func(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error) {
		return map[string]string{"modified": "result"}, false, nil
	})

	customResult, override, err := registry.RunAfterTool(ctx, mockTool, args, originalResult, nil)

	assert.Nil(t, customResult)
	assert.False(t, override)
	assert.NoError(t, err)
}

func TestRunAfterTool_WithError(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)
	result := map[string]string{"result": "test"}
	toolErr := errors.New("tool execution error")

	// Add callback that handles error
	registry.AddAfterTool(func(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error) {
		if toolErr != nil {
			return map[string]string{"error": "handled"}, true, nil
		}
		return nil, false, nil
	})

	customResult, override, err := registry.RunAfterTool(ctx, mockTool, args, result, toolErr)

	assert.Equal(t, map[string]string{"error": "handled"}, customResult)
	assert.True(t, override)
	assert.NoError(t, err)
}

func TestCallbackRegistry_Integration(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()

	// Test all callback types in sequence
	invocation := &agent.Invocation{AgentName: "test-agent"}
	req := &model.Request{Messages: []model.Message{{Content: "test"}}}
	resp := &model.Response{ID: "test-response"}
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)
	result := map[string]string{"result": "test"}

	// Add callbacks that modify state
	registry.AddBeforeAgent(func(ctx context.Context, invocation *agent.Invocation) (*model.Response, bool, error) {
		invocation.AgentName = "modified-agent"
		return nil, false, nil
	})

	registry.AddBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, bool, error) {
		req.Messages = append(req.Messages, model.Message{Content: "added by callback"})
		return nil, false, nil
	})

	registry.AddAfterModel(func(ctx context.Context, resp *model.Response, modelErr error) (*model.Response, bool, error) {
		resp.ID = "modified-response"
		return resp, false, nil
	})

	registry.AddBeforeTool(func(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error) {
		args = append(args, []byte(`{"added": "by callback"}`)...)
		return nil, false, args, nil
	})

	registry.AddAfterTool(func(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error) {
		if resultMap, ok := result.(map[string]string); ok {
			resultMap["modified"] = "by callback"
		}
		return result, false, nil
	})

	// Run all callbacks
	_, _, err := registry.RunBeforeAgent(ctx, invocation)
	require.NoError(t, err)
	assert.Equal(t, "modified-agent", invocation.AgentName)

	_, _, err = registry.RunBeforeModel(ctx, req)
	require.NoError(t, err)
	assert.Len(t, req.Messages, 2)
	assert.Equal(t, "added by callback", req.Messages[1].Content)

	_, _, err = registry.RunAfterModel(ctx, resp, nil)
	require.NoError(t, err)
	assert.Equal(t, "modified-response", resp.ID)

	_, _, newArgs, err := registry.RunBeforeTool(ctx, mockTool, args)
	require.NoError(t, err)
	assert.Contains(t, string(newArgs), "added")

	_, _, err = registry.RunAfterTool(ctx, mockTool, args, result, nil)
	require.NoError(t, err)
	assert.Equal(t, "by callback", result["modified"])
}

// Additional test cases to improve coverage

func TestRunAfterAgent_Error(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	invocation := &agent.Invocation{}
	expectedErr := errors.New("after agent callback error")

	// Add callback that returns error
	registry.AddAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
		return nil, false, expectedErr
	})

	customResponse, override, err := registry.RunAfterAgent(ctx, invocation, nil)

	assert.Nil(t, customResponse)
	assert.False(t, override)
	assert.Equal(t, expectedErr, err)
}

func TestRunAfterAgent_MultipleCallbacks(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	invocation := &agent.Invocation{}

	callCount := 0

	// Add first callback that continues
	registry.AddAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
		callCount++
		return nil, false, nil
	})

	// Add second callback that overrides
	registry.AddAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
		callCount++
		return &model.Response{ID: "override"}, true, nil
	})

	// Add third callback that should not be called
	registry.AddAfterAgent(func(ctx context.Context, invocation *agent.Invocation, runErr error) (*model.Response, bool, error) {
		callCount++
		return nil, false, nil
	})

	customResponse, override, err := registry.RunAfterAgent(ctx, invocation, nil)

	assert.Equal(t, "override", customResponse.ID)
	assert.True(t, override)
	assert.NoError(t, err)
	assert.Equal(t, 2, callCount, "Only first two callbacks should be called")
}

func TestRunBeforeModel_Error(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	req := &model.Request{}
	expectedErr := errors.New("before model callback error")

	// Add callback that returns error
	registry.AddBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, bool, error) {
		return nil, false, expectedErr
	})

	customResponse, skip, err := registry.RunBeforeModel(ctx, req)

	assert.Nil(t, customResponse)
	assert.False(t, skip)
	assert.Equal(t, expectedErr, err)
}

func TestRunBeforeModel_MultipleCallbacks(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	req := &model.Request{}

	callCount := 0

	// Add first callback that continues
	registry.AddBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, bool, error) {
		callCount++
		return nil, false, nil
	})

	// Add second callback that returns custom response
	registry.AddBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, bool, error) {
		callCount++
		return &model.Response{ID: "custom"}, false, nil
	})

	// Add third callback that should not be called
	registry.AddBeforeModel(func(ctx context.Context, req *model.Request) (*model.Response, bool, error) {
		callCount++
		return nil, false, nil
	})

	customResponse, skip, err := registry.RunBeforeModel(ctx, req)

	assert.Equal(t, "custom", customResponse.ID)
	assert.False(t, skip)
	assert.NoError(t, err)
	assert.Equal(t, 2, callCount, "Only first two callbacks should be called")
}

func TestRunAfterModel_Error(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	resp := &model.Response{}
	expectedErr := errors.New("after model callback error")

	// Add callback that returns error
	registry.AddAfterModel(func(ctx context.Context, resp *model.Response, modelErr error) (*model.Response, bool, error) {
		return nil, false, expectedErr
	})

	customResponse, override, err := registry.RunAfterModel(ctx, resp, nil)

	assert.Nil(t, customResponse)
	assert.False(t, override)
	assert.Equal(t, expectedErr, err)
}

func TestRunAfterModel_MultipleCallbacks(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	resp := &model.Response{}

	callCount := 0

	// Add first callback that continues
	registry.AddAfterModel(func(ctx context.Context, resp *model.Response, modelErr error) (*model.Response, bool, error) {
		callCount++
		return nil, false, nil
	})

	// Add second callback that overrides
	registry.AddAfterModel(func(ctx context.Context, resp *model.Response, modelErr error) (*model.Response, bool, error) {
		callCount++
		return &model.Response{ID: "override"}, true, nil
	})

	// Add third callback that should not be called
	registry.AddAfterModel(func(ctx context.Context, resp *model.Response, modelErr error) (*model.Response, bool, error) {
		callCount++
		return nil, false, nil
	})

	customResponse, override, err := registry.RunAfterModel(ctx, resp, nil)

	assert.Equal(t, "override", customResponse.ID)
	assert.True(t, override)
	assert.NoError(t, err)
	assert.Equal(t, 2, callCount, "Only first two callbacks should be called")
}

func TestRunBeforeTool_Error(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)
	expectedErr := errors.New("before tool callback error")

	// Add callback that returns error
	registry.AddBeforeTool(func(ctx context.Context, t tool.Tool, args []byte) (any, bool, []byte, error) {
		return nil, false, nil, expectedErr
	})

	customResult, skip, newArgs, err := registry.RunBeforeTool(ctx, mockTool, args)

	assert.Nil(t, customResult)
	assert.False(t, skip)
	assert.Nil(t, newArgs)
	assert.Equal(t, expectedErr, err)
}

func TestRunAfterTool_Error(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)
	result := map[string]string{"result": "test"}
	expectedErr := errors.New("after tool callback error")

	// Add callback that returns error
	registry.AddAfterTool(func(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error) {
		return nil, false, expectedErr
	})

	customResult, override, err := registry.RunAfterTool(ctx, mockTool, args, result, nil)

	assert.Nil(t, customResult)
	assert.False(t, override)
	assert.Equal(t, expectedErr, err)
}

func TestRunAfterTool_MultipleCallbacks(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()
	mockTool := &mockTool{name: "test-tool"}
	args := []byte(`{"test": "value"}`)
	result := map[string]string{"result": "test"}

	callCount := 0

	// Add first callback that continues
	registry.AddAfterTool(func(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error) {
		callCount++
		return nil, false, nil
	})

	// Add second callback that overrides
	registry.AddAfterTool(func(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error) {
		callCount++
		return map[string]string{"override": "result"}, true, nil
	})

	// Add third callback that should not be called
	registry.AddAfterTool(func(ctx context.Context, t tool.Tool, args []byte, result any, toolErr error) (any, bool, error) {
		callCount++
		return nil, false, nil
	})

	customResult, override, err := registry.RunAfterTool(ctx, mockTool, args, result, nil)

	assert.Equal(t, map[string]string{"override": "result"}, customResult)
	assert.True(t, override)
	assert.NoError(t, err)
	assert.Equal(t, 2, callCount, "Only first two callbacks should be called")
}

func TestMockTool(t *testing.T) {
	// Test mockTool implementation
	mockTool := &mockTool{name: "test-tool", description: "A test tool"}

	// Test Declaration
	decl := mockTool.Declaration()
	assert.Equal(t, "test-tool", decl.Name)
	assert.Equal(t, "A test tool", decl.Description)
	assert.NotNil(t, decl.InputSchema)

	// Test Call with success
	ctx := context.Background()
	result, err := mockTool.Call(ctx, []byte(`{"test": "value"}`))
	assert.NoError(t, err)
	assert.NotNil(t, result)

	// Test Call with error
	mockTool.shouldError = true
	result, err = mockTool.Call(ctx, []byte(`{"test": "value"}`))
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Equal(t, "mock tool error", err.Error())
}

func TestCallbackRegistry_EdgeCases(t *testing.T) {
	registry := NewCallbackRegistry()
	ctx := context.Background()

	// Test with nil context (should not panic)
	invocation := &agent.Invocation{}
	_, _, err := registry.RunBeforeAgent(nil, invocation)
	assert.NoError(t, err)

	// Test with nil invocation (should not panic)
	_, _, err = registry.RunBeforeAgent(ctx, nil)
	assert.NoError(t, err)

	// Test with nil request (should not panic)
	_, _, err = registry.RunBeforeModel(ctx, nil)
	assert.NoError(t, err)

	// Test with nil response (should not panic)
	_, _, err = registry.RunAfterModel(ctx, nil, nil)
	assert.NoError(t, err)

	// Test with nil tool (should not panic)
	_, _, _, err = registry.RunBeforeTool(ctx, nil, nil)
	assert.NoError(t, err)

	// Test with nil tool (should not panic)
	_, _, err = registry.RunAfterTool(ctx, nil, nil, nil, nil)
	assert.NoError(t, err)
}
