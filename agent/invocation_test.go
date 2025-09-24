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

func TestInvocation_Clone(t *testing.T) {
	inv := NewInvocation(
		WithInvocationID("test-invocation"),
		WithInvocationMessage(model.Message{Role: model.RoleUser, Content: "Hello"}),
	)

	subAgent := &mockAgent{name: "test-agent"}
	subInv := inv.Clone(WithInvocationAgent(subAgent))
	require.NotNil(t, subInv)
	require.NotEqual(t, "test-invocation", subInv.InvocationID)
	require.Equal(t, "test-agent", subInv.AgentName)
	require.Equal(t, "Hello", subInv.Message.Content)
	require.Equal(t, inv.noticeChanMap, subInv.noticeChanMap)
	require.Equal(t, inv.noticeMu, subInv.noticeMu)
}

func TestInvocation_AddNoticeChannel(t *testing.T) {
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

func TestInvocation_AddNoticeChannelAndWait(t *testing.T) {
	type execTime struct {
		min time.Duration
		max time.Duration
	}
	tests := []struct {
		name        string
		ctxDelay    time.Duration
		noticeKey   string
		waitTimeout time.Duration
		errType     int // 0: no error, 1: timeout error, 2: context error
		mainSleep   time.Duration
		execTime    execTime
	}{
		{
			name:        "wait_with_context_cancel_error",
			ctxDelay:    50 * time.Millisecond,
			noticeKey:   "test-channel-1",
			waitTimeout: 100 * time.Millisecond,
			errType:     2,
			mainSleep:   300 * time.Millisecond,
			execTime: execTime{
				min: 50 * time.Millisecond,
				max: 150 * time.Millisecond,
			},
		},
		{
			name:        "wait_with_timeout_err",
			ctxDelay:    0,
			noticeKey:   "test-channel-2",
			errType:     1,
			waitTimeout: 100 * time.Millisecond,
			mainSleep:   300 * time.Millisecond,
			execTime: execTime{
				min: 100 * time.Millisecond,
				max: 300 * time.Millisecond,
			},
		},
		{
			name:        "wait_normal_case_1",
			ctxDelay:    0,
			noticeKey:   "test-channel-3",
			errType:     0,
			waitTimeout: 1 * time.Second,
			mainSleep:   300 * time.Millisecond,
			execTime: execTime{
				min: 30 * time.Millisecond,
				max: 1 * time.Second,
			},
		},
		{
			name:        "wait_normal_case_4",
			ctxDelay:    2 * time.Second,
			noticeKey:   "test-channel-4",
			errType:     0,
			waitTimeout: 1 * time.Second,
			mainSleep:   300 * time.Millisecond,
			execTime: execTime{
				min: 300 * time.Millisecond,
				max: 1 * time.Second,
			},
		},
	}

	inv := NewInvocation()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			if tt.ctxDelay > 0 {
				innerCtx, cancel := context.WithTimeout(ctx, tt.ctxDelay)
				defer cancel()
				ctx = innerCtx
			}
			complete := false
			startTime := time.Now()
			go func() {
				startTime := time.Now()
				err := inv.AddNoticeChannelAndWait(ctx, tt.noticeKey, tt.waitTimeout)
				duration := time.Since(startTime)
				complete = true
				require.True(t, duration > tt.execTime.min && duration < tt.execTime.max)

				switch tt.errType {
				case 0:
					require.NoError(t, err)
				case 1:
					require.Error(t, err)
					_, isWaitNoticeTimeoutError := AsWaitNoticeTimeoutError(err)
					require.True(t, isWaitNoticeTimeoutError)
				case 2:
					require.Error(t, err)
					_, isWaitNoticeTimeoutError := AsWaitNoticeTimeoutError(err)
					require.False(t, isWaitNoticeTimeoutError)
				}
			}()
			time.Sleep(tt.mainSleep)
			inv.NotifyCompletion(ctx, tt.noticeKey)
			require.Equal(t, 0, len(inv.noticeChanMap))
			for {
				if complete {
					break
				}
			}
			duration := time.Since(startTime)
			require.True(t, duration > tt.mainSleep)
		})
	}
}

func TestInvocation_NotifyCompletion(t *testing.T) {
	inv := NewInvocation()
	noticeKey := "test-channel-1"
	err := inv.NotifyCompletion(context.Background(), noticeKey)
	require.Error(t, err)
	require.Equal(t, 0, len(inv.noticeChanMap))
}

func TestInvocation_CleanupNotice(t *testing.T) {
	inv := NewInvocation()
	ch := inv.AddNoticeChannel(context.Background(), "test-channel-1")
	require.Equal(t, 1, len(inv.noticeChanMap))

	// Cleanup notice channel
	inv.CleanupNotice(context.Background())
	<-ch
	require.Equal(t, 0, len(inv.noticeChanMap))
}

func TestInvocation_AddNoticeChannel_Panic(t *testing.T) {
	inv := &Invocation{}

	ch := inv.AddNoticeChannel(context.Background(), "test-key")
	require.Nil(t, ch)
}

func TestInvocation_AddNoticeChannelAndWait_Panic(t *testing.T) {
	inv := &Invocation{}

	err := inv.AddNoticeChannelAndWait(context.Background(), "test-key", 2*time.Second)
	require.Error(t, err)
}
