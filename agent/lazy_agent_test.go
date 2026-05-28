//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	lazyAgentTestName        = "lazy-agent"
	lazyAgentTestChildName   = "lazy-child"
	lazyAgentTestDescription = "created only when invoked"
	lazyAgentTestRequestID   = "lazy-request"
	lazyAgentTestFactoryErr  = "factory failed"
)

type lazyAgentTestAgent struct {
	name      string
	ran       bool
	subAgents []Agent
	onRun     func(*Invocation)
}

func (a *lazyAgentTestAgent) Run(_ context.Context, inv *Invocation) (<-chan *event.Event, error) {
	a.ran = true
	if a.onRun != nil {
		a.onRun(inv)
	}
	ch := make(chan *event.Event)
	close(ch)
	return ch, nil
}

func (a *lazyAgentTestAgent) Tools() []tool.Tool { return nil }

func (a *lazyAgentTestAgent) Info() Info {
	return Info{Name: a.name}
}

func (a *lazyAgentTestAgent) SubAgents() []Agent {
	return append([]Agent(nil), a.subAgents...)
}

func (a *lazyAgentTestAgent) FindSubAgent(name string) Agent {
	for _, subAgent := range a.subAgents {
		if subAgent.Info().Name == name {
			return subAgent
		}
	}
	return nil
}

func TestNewLazyAgent_ExposesInfoWithoutCallingFactory(t *testing.T) {
	called := false
	lazy := NewLazyAgent(
		Info{
			Name:        lazyAgentTestName,
			Description: lazyAgentTestDescription,
		},
		func(context.Context, RunOptions) (Agent, error) {
			called = true
			return &lazyAgentTestAgent{name: lazyAgentTestName}, nil
		},
	)

	assert.Equal(t, lazyAgentTestName, lazy.Info().Name)
	assert.Equal(t, lazyAgentTestDescription, lazy.Info().Description)
	assert.Nil(t, lazy.Tools())
	assert.Nil(t, lazy.SubAgents())
	assert.Nil(t, lazy.FindSubAgent(lazyAgentTestName))
	assert.False(t, called)
}

func TestNewLazyAgent_RunBuildsAndDelegates(t *testing.T) {
	created := &lazyAgentTestAgent{name: lazyAgentTestName}
	var gotRunOptions RunOptions
	lazy := NewLazyAgent(
		Info{Name: lazyAgentTestName},
		func(_ context.Context, ro RunOptions) (Agent, error) {
			gotRunOptions = ro
			return created, nil
		},
	)
	inv := NewInvocation(WithInvocationRunOptions(RunOptions{
		RequestID: lazyAgentTestRequestID,
	}))

	ch, err := lazy.Run(context.Background(), inv)
	require.NoError(t, err)
	for range ch {
	}

	assert.True(t, created.ran)
	assert.Equal(t, lazyAgentTestRequestID, gotRunOptions.RequestID)
}

func TestNewLazyAgent_RunUpdatesInvocationAgentBeforeDelegating(t *testing.T) {
	child := &lazyAgentTestAgent{name: lazyAgentTestChildName}
	var created *lazyAgentTestAgent
	created = &lazyAgentTestAgent{
		name:      lazyAgentTestName,
		subAgents: []Agent{child},
		onRun: func(inv *Invocation) {
			require.NotNil(t, inv)
			assert.Same(t, created, inv.Agent)
			assert.Equal(t, lazyAgentTestName, inv.AgentName)
			assert.Same(t, child, inv.Agent.FindSubAgent(lazyAgentTestChildName))
		},
	}
	lazy := NewLazyAgent(
		Info{Name: lazyAgentTestName},
		func(context.Context, RunOptions) (Agent, error) {
			return created, nil
		},
	)

	ch, err := lazy.Run(context.Background(), NewInvocation())
	require.NoError(t, err)
	for range ch {
	}
}

func TestNewLazyAgent_RunReturnsFactoryErrors(t *testing.T) {
	factoryErr := errors.New(lazyAgentTestFactoryErr)
	lazy := NewLazyAgent(
		Info{Name: lazyAgentTestName},
		func(context.Context, RunOptions) (Agent, error) {
			return nil, factoryErr
		},
	)

	_, err := lazy.Run(context.Background(), NewInvocation())

	require.Error(t, err)
	assert.ErrorIs(t, err, factoryErr)
}

func TestNewLazyAgent_RunRejectsInvalidFactories(t *testing.T) {
	tests := []struct {
		name    string
		lazy    Agent
		wantErr string
	}{
		{
			name:    "nil factory",
			lazy:    NewLazyAgent(Info{Name: lazyAgentTestName}, nil),
			wantErr: errLazyAgentNilFactory,
		},
		{
			name: "nil agent",
			lazy: NewLazyAgent(
				Info{Name: lazyAgentTestName},
				func(context.Context, RunOptions) (Agent, error) {
					return nil, nil
				},
			),
			wantErr: errLazyAgentFactoryNilAgent,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.lazy.Run(context.Background(), NewInvocation())
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}
