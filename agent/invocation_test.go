//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// =========================
// BeforeAgent Callback Tests
// =========================

func TestNewInvocation(t *testing.T) {
	inv := NewInvocation(
		WithInvocationID("test-invocation"),
		WithInvocationMessage(model.Message{Role: model.RoleUser, Content: "Hello"}),
	)
	require.NotNil(t, inv)
	require.Equal(t, "test-invocation", inv.InvocationID)
	require.Equal(t, "Hello", inv.Message.Content)
}

type mockAgent struct {
	name string
}

func (a *mockAgent) Run(ctx context.Context, invocation *Invocation) (<-chan *event.Event, error) {
	return nil, nil
}

func (a *mockAgent) Tools() []tool.Tool {
	return nil
}

func (a *mockAgent) Info() Info {
	return Info{
		Name: a.name,
	}
}

func (a *mockAgent) SubAgents() []Agent {
	return nil
}

func (m *mockAgent) FindSubAgent(name string) Agent {
	return nil
}

func TestCreateBranchInvocation(t *testing.T) {
	inv := NewInvocation(
		WithInvocationID("test-invocation"),
		WithInvocationMessage(model.Message{Role: model.RoleUser, Content: "Hello"}),
	)

	subAgent := &mockAgent{name: "test-agent"}
	subInv := inv.CreateBranchInvocation(subAgent)
	require.NotNil(t, subInv)
	require.Equal(t, "test-invocation", subInv.InvocationID)
	require.Equal(t, "test-agent", subInv.AgentName)
	require.Equal(t, "Hello", subInv.Message.Content)
	require.Equal(t, inv.noticeChanMap, subInv.noticeChanMap)
	require.Equal(t, inv.noticeMu, subInv.noticeMu)
}

func TestAddNoticeChannel(t *testing.T) {
	inv := NewInvocation()
	ctx := context.Background()
	ch := inv.AddNoticeChannel(ctx, "test-channel")

	require.NotNil(t, ch)
	require.Equal(t, 1, len(inv.noticeChanMap))
	// Adding the same channel again should return the existing channel
	ch2 := inv.AddNoticeChannel(ctx, "test-channel")
	require.Equal(t, ch, ch2)
	require.Equal(t, 1, len(inv.noticeChanMap))

	err := inv.NotifyCompletion(ctx, "test-channel")
	require.NoError(t, err)
	require.Equal(t, 0, len(inv.noticeChanMap))
}

func TestAddNoticeChannelAndWait(t *testing.T) {
	inv := NewInvocation()
	ctx := context.Background()
	// Wait for the channel to be closed
	complete := false
	startTime := time.Now()
	go func() {
		err := inv.AddNoticeChannelAndWait(ctx, "test-channel", 500*time.Millisecond)
		require.NoError(t, err)
		complete = true
	}()
	time.Sleep(100 * time.Millisecond)
	inv.NotifyCompletion(ctx, "test-channel")
	require.Equal(t, 0, len(inv.noticeChanMap))
	for {
		if complete {
			break
		}
	}
	duration := time.Since(startTime)
	require.True(t, duration > 100*time.Millisecond && duration < 500*time.Millisecond)
}
