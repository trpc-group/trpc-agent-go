package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/core/model"
)

// =========================
// BeforeAgent Callback Tests
// =========================

func TestAgentCallbacks_BeforeAgent_NoCallbacks(t *testing.T) {
	callbacks := NewAgentCallbacks()
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.NoError(t, err)
	require.False(t, skip)
	require.Nil(t, resp)
}

func TestAgentCallbacks_BeforeAgent_CustomResponse(t *testing.T) {
	callbacks := NewAgentCallbacks()
	customResponse := &model.Response{ID: "custom-agent-response"}
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error) {
		return customResponse, false, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.NoError(t, err)
	require.False(t, skip)
	require.Equal(t, customResponse, resp)
}

func TestAgentCallbacks_BeforeAgent_Skip(t *testing.T) {
	callbacks := NewAgentCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error) {
		return nil, true, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.NoError(t, err)
	require.True(t, skip)
	require.Nil(t, resp)
}

func TestAgentCallbacks_BeforeAgent_Error(t *testing.T) {
	callbacks := NewAgentCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error) {
		return nil, false, context.DeadlineExceeded
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.Error(t, err)
	require.Nil(t, resp)
	require.False(t, skip)
}

func TestAgentCallbacks_BeforeAgent_MultipleCallbacks(t *testing.T) {
	callbacks := NewAgentCallbacks()
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error) {
		return nil, false, nil
	})
	callbacks.RegisterBeforeAgent(func(ctx context.Context, invocation *Invocation) (*model.Response, bool, error) {
		return &model.Response{ID: "second"}, false, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, skip, err := callbacks.RunBeforeAgent(context.Background(), invocation)
	require.NoError(t, err)
	require.False(t, skip)
	require.NotNil(t, resp)
	require.Equal(t, "second", resp.ID)
}

// =========================
// AfterAgent Callback Tests
// =========================

func TestAgentCallbacks_AfterAgent_NoCallbacks(t *testing.T) {
	callbacks := NewAgentCallbacks()
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, override, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.NoError(t, err)
	require.False(t, override)
	require.Nil(t, resp)
}

func TestAgentCallbacks_AfterAgent_CustomResponseOverride(t *testing.T) {
	callbacks := NewAgentCallbacks()
	customResponse := &model.Response{ID: "custom-after-response"}
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error) {
		return customResponse, true, nil
	})
	invocation := &Invocation{InvocationID: "test-invocation", AgentName: "test-agent", Message: model.Message{Role: model.RoleUser, Content: "Hello"}}
	resp, override, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.NoError(t, err)
	require.True(t, override)
	require.Equal(t, customResponse, resp)
}

func TestAgentCallbacks_AfterAgent_CustomResponseNoOverride(t *testing.T) {
	callbacks := NewAgentCallbacks()
	customResponse := &model.Response{ID: "custom-no-override"}
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error) {
		return customResponse, false, nil
	})
	invocation := &Invocation{InvocationID: "test-invocation", AgentName: "test-agent", Message: model.Message{Role: model.RoleUser, Content: "Hello"}}
	resp, override, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.NoError(t, err)
	require.False(t, override)
	require.Nil(t, resp)
}

func TestAgentCallbacks_AfterAgent_NilResponseWithOverride(t *testing.T) {
	callbacks := NewAgentCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error) {
		return nil, true, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, override, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.NoError(t, err)
	require.False(t, override)
	require.Nil(t, resp)
}

func TestAgentCallbacks_AfterAgent_Error(t *testing.T) {
	callbacks := NewAgentCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error) {
		return nil, false, context.DeadlineExceeded
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, override, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.Error(t, err)
	require.False(t, override)
	require.Nil(t, resp)
}

func TestAgentCallbacks_AfterAgent_WithRunError(t *testing.T) {
	callbacks := NewAgentCallbacks()
	runError := context.DeadlineExceeded
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error) {
		require.Equal(t, runError, runErr)
		return nil, false, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, override, err := callbacks.RunAfterAgent(context.Background(), invocation, runError)
	require.NoError(t, err)
	require.False(t, override)
	require.Nil(t, resp)
}

func TestAgentCallbacks_AfterAgent_MultipleCallbacks(t *testing.T) {
	callbacks := NewAgentCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error) {
		return nil, false, nil
	})
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error) {
		return &model.Response{ID: "second"}, true, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, override, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.NoError(t, err)
	require.True(t, override)
	require.NotNil(t, resp)
	require.Equal(t, "second", resp.ID)
}

func TestAgentCallbacks_AfterAgent_MultipleCallbacksNoOverride(t *testing.T) {
	callbacks := NewAgentCallbacks()
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error) {
		return &model.Response{ID: "first"}, false, nil
	})
	callbacks.RegisterAfterAgent(func(ctx context.Context, invocation *Invocation, runErr error) (*model.Response, bool, error) {
		return &model.Response{ID: "second"}, false, nil
	})
	invocation := &Invocation{
		InvocationID: "test-invocation",
		AgentName:    "test-agent",
		Message:      model.Message{Role: model.RoleUser, Content: "Hello"},
	}
	resp, override, err := callbacks.RunAfterAgent(context.Background(), invocation, nil)
	require.NoError(t, err)
	require.False(t, override)
	require.Nil(t, resp)
}
