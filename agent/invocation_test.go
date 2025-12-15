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

	"github.com/stretchr/testify/assert"
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
	require.Equal(t, inv.noticeChannels, subInv.noticeChannels)
	require.Equal(t, inv.noticeMu, subInv.noticeMu)
}

func TestInvocation_AddNoticeChannel(t *testing.T) {
	inv := NewInvocation()
	defer inv.CleanupNotice(context.Background())
	ctx := context.Background()
	ch := inv.AddNoticeChannel(ctx, "test-channel")

	require.NotNil(t, ch)
	require.Equal(t, 1, len(inv.noticeChannels))
	// Adding the same channel again should return the existing channel
	ch2 := inv.AddNoticeChannel(ctx, "test-channel")
	require.Equal(t, ch, ch2)
	require.Equal(t, 1, len(inv.noticeChannels))

	err := inv.NotifyCompletion(ctx, "test-channel")
	require.NoError(t, err)
	require.Equal(t, 1, len(inv.noticeChannels))
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
			ctxDelay:    100 * time.Millisecond,
			noticeKey:   "test-channel-1",
			waitTimeout: 200 * time.Millisecond,
			errType:     2,
			mainSleep:   500 * time.Millisecond,
			execTime: execTime{
				min: 80 * time.Millisecond,
				max: 300 * time.Millisecond,
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
				min: 80 * time.Millisecond,
				max: 300 * time.Millisecond,
			},
		},
		{
			name:        "wait_normal_case_1",
			ctxDelay:    0,
			noticeKey:   "test-channel-3",
			errType:     0,
			waitTimeout: 1 * time.Second,
			mainSleep:   100 * time.Millisecond,
			execTime: execTime{
				min: 80 * time.Millisecond,
				max: 500 * time.Millisecond,
			},
		},
		{
			name:        "wait_normal_case_4",
			ctxDelay:    2 * time.Second,
			noticeKey:   "test-channel-4",
			errType:     0,
			waitTimeout: 1 * time.Second,
			mainSleep:   100 * time.Millisecond,
			execTime: execTime{
				min: 80 * time.Millisecond,
				max: 500 * time.Millisecond,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv := NewInvocation()
			ctx := context.Background()
			defer inv.CleanupNotice(ctx)
			if tt.ctxDelay > 0 {
				innerCtx, cancel := context.WithTimeout(ctx, tt.ctxDelay)
				defer cancel()
				ctx = innerCtx
			}

			done := make(chan struct{})
			errCh := make(chan error, 1)
			durationCh := make(chan time.Duration, 1)
			startTime := time.Now()

			go func() {
				defer close(done)
				goroutineStart := time.Now()
				err := inv.AddNoticeChannelAndWait(ctx, tt.noticeKey, tt.waitTimeout)
				durationCh <- time.Since(goroutineStart)
				errCh <- err
			}()

			// Wait for the expected trigger condition
			if tt.errType == 0 {
				// For success cases, notify before timeout/context cancel
				time.Sleep(tt.mainSleep)
				inv.NotifyCompletion(context.Background(), tt.noticeKey)
			} else {
				// For error cases, let timeout or context cancel happen naturally
				// No notification needed
			}

			// Wait for goroutine to complete
			<-done

			duration := <-durationCh
			err := <-errCh

			// Verify execution time with more tolerance
			require.GreaterOrEqual(t, duration, tt.execTime.min,
				"execution time %v should be >= %v", duration, tt.execTime.min)
			require.LessOrEqual(t, duration, tt.execTime.max,
				"execution time %v should be <= %v", duration, tt.execTime.max)

			// Verify error type
			switch tt.errType {
			case 0:
				require.NoError(t, err, "expected no error but got: %v", err)
			case 1:
				require.Error(t, err, "expected timeout error but got no error")
				_, isWaitNoticeTimeoutError := AsWaitNoticeTimeoutError(err)
				require.True(t, isWaitNoticeTimeoutError, "expected WaitNoticeTimeoutError but got: %T", err)
			case 2:
				require.Error(t, err, "expected context error but got no error")
				_, isWaitNoticeTimeoutError := AsWaitNoticeTimeoutError(err)
				require.False(t, isWaitNoticeTimeoutError, "expected context error but got WaitNoticeTimeoutError")
			}

			// Verify channel cleanup
			if tt.errType == 0 {
				require.Equal(t, 1, len(inv.noticeChannels), "notice channel should be cleaned up")
			}

			// Verify main execution time
			mainDuration := time.Since(startTime)
			if tt.errType == 0 {
				require.GreaterOrEqual(t, mainDuration, tt.mainSleep,
					"main execution time %v should be >= sleep time %v", mainDuration, tt.mainSleep)
			}
		})
	}
}

func TestInvocation_AddNoticeChannelAndWait_after_notify(t *testing.T) {
	inv := NewInvocation()
	key := "test-channel-1"

	err := inv.NotifyCompletion(context.Background(), key)
	require.NoError(t, err)

	startTime := time.Now()
	err = inv.AddNoticeChannelAndWait(context.Background(), key, 2*time.Second)
	require.NoError(t, err)
	require.Less(t, time.Since(startTime), 2*time.Second)
}

func TestInvocation_AddNoticeChannelAndWait_before_notify(t *testing.T) {
	inv := NewInvocation()
	defer inv.CleanupNotice(context.Background())
	key := "test-channel-1"

	startTime := time.Now()
	err := inv.AddNoticeChannelAndWait(context.Background(), key, 2*time.Second)
	// timeout after 2s
	require.Error(t, err)
	require.Greater(t, time.Since(startTime), 2*time.Second)

	err = inv.NotifyCompletion(context.Background(), key)
	require.NoError(t, err)
	err = inv.NotifyCompletion(context.Background(), key)
	require.NoError(t, err)

	startTime = time.Now()
	err = inv.AddNoticeChannelAndWait(context.Background(), key, 2*time.Second)
	require.NoError(t, err)
	require.Less(t, time.Since(startTime), 2*time.Second)
}

func TestInvocation_NotifyCompletion(t *testing.T) {
	inv := NewInvocation()
	inv.noticeChannels = nil
	defer inv.CleanupNotice(context.Background())
	noticeKey := "test-channel-1"
	err := inv.NotifyCompletion(context.Background(), noticeKey)
	require.NoError(t, err)
	require.Equal(t, 1, len(inv.noticeChannels))

	inv.AddNoticeChannel(context.Background(), "test-channel-1")
	require.Equal(t, 1, len(inv.noticeChannels))
	err = inv.NotifyCompletion(context.Background(), noticeKey)
	require.NoError(t, err)
}

func TestInvocation_CleanupNotice(t *testing.T) {
	inv := NewInvocation()
	inv.noticeChannels = nil
	ch := inv.AddNoticeChannel(context.Background(), "test-channel-1")
	require.Equal(t, 1, len(inv.noticeChannels))

	ch2 := inv.AddNoticeChannel(context.Background(), "test-channel-2")
	require.Equal(t, 2, len(inv.noticeChannels))
	require.NotNil(t, ch2)
	inv.NotifyCompletion(context.Background(), "test-channel-2")

	ch3 := inv.AddNoticeChannel(context.Background(), "test-channel-3")
	require.Equal(t, 3, len(inv.noticeChannels))
	require.NotNil(t, ch3)

	go func() {
		ch <- 1
	}()
	go func() {
		ch3 <- 1
	}()

	time.Sleep(500 * time.Microsecond)
	inv.NotifyCompletion(context.Background(), "test-channel-3")
	// Cleanup notice channel
	inv.CleanupNotice(context.Background())
	<-ch
	require.Equal(t, 0, len(inv.noticeChannels))
}

func TestInvocation_AddNoticeChannel_Panic(t *testing.T) {
	inv := &Invocation{}

	ch := inv.AddNoticeChannel(context.Background(), "test-key")
	require.Nil(t, ch)
}

func TestInvocation_NotifyCompletion_Panic(t *testing.T) {
	inv := &Invocation{}

	err := inv.NotifyCompletion(context.Background(), "test-key")
	require.Error(t, err)
	require.Contains(t, err.Error(), "noticeMu is uninitialized")
}

func TestInvocation_AddNoticeChannelAndWait_Panic(t *testing.T) {
	inv := &Invocation{}

	err := inv.AddNoticeChannelAndWait(context.Background(), "test-key", 2*time.Second)
	require.Error(t, err)
}

func TestInvocation_AddNoticeChannelAndWait_NoTimeoutUsesContext(t *testing.T) {
	inv := NewInvocation()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- inv.AddNoticeChannelAndWait(
			ctx,
			"test-key",
			WaitNoticeWithoutTimeout,
		)
	}()

	cancel()

	err := <-done
	require.Error(t, err)
}

func TestInvocation_AddNoticeChannel_nil(t *testing.T) {
	var inv *Invocation

	ch := inv.AddNoticeChannel(context.Background(), "test-key")
	require.Nil(t, ch)
}

func TestInvocation_AddNoticeChannelAndWait_nil(t *testing.T) {
	var inv *Invocation

	err := inv.AddNoticeChannelAndWait(context.Background(), "test-key", 2*time.Second)
	require.Error(t, err)
}

func TestInvocation_CleanupNotice_NilInvocation(t *testing.T) {
	var inv *Invocation

	require.NotPanics(t, func() {
		inv.CleanupNotice(context.Background())
	})
}

func TestInvocation_cloneState(t *testing.T) {
	t.Run("nil invocation", func(t *testing.T) {
		var inv *Invocation
		require.Nil(t, inv.cloneState())
	})

	t.Run("nil state map", func(t *testing.T) {
		inv := &Invocation{}
		require.Nil(t, inv.cloneState())
	})

	t.Run("copies allowed keys only", func(t *testing.T) {
		inv := &Invocation{
			state: map[string]any{
				flusherStateKey: "flush-holder",
				barrierStateKey: "barrier-holder",
				"other":         "skip",
			},
		}
		cloned := inv.cloneState()
		require.NotNil(t, cloned)
		require.Len(t, cloned, 2)
		require.Equal(t, "flush-holder", cloned[flusherStateKey])
		require.Equal(t, "barrier-holder", cloned[barrierStateKey])
		assert.NotContains(t, cloned, "other")
	})
}

func TestInvocation_GetEventFilterKey(t *testing.T) {
	tests := []struct {
		name      string
		inv       *Invocation
		expectKey string
	}{
		{
			name:      "nil invocation",
			inv:       nil,
			expectKey: "",
		},
		{
			name:      "invocation without filter key",
			inv:       NewInvocation(),
			expectKey: "",
		},
		{
			name:      "invocation with filter key",
			inv:       NewInvocation(WithInvocationEventFilterKey("test-filter-key")),
			expectKey: "test-filter-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := tt.inv.GetEventFilterKey()
			require.Equal(t, tt.expectKey, key)
		})
	}
}

func TestInjectIntoEvent(t *testing.T) {
	tests := []struct {
		name     string
		inv      *Invocation
		event    *event.Event
		validate func(*testing.T, *event.Event)
	}{
		{
			name:  "nil event",
			inv:   NewInvocation(WithInvocationID("test-id")),
			event: nil,
			validate: func(t *testing.T, e *event.Event) {
				// Nothing to validate, should not panic
			},
		},
		{
			name:  "nil invocation",
			inv:   nil,
			event: &event.Event{},
			validate: func(t *testing.T, e *event.Event) {
				require.Equal(t, "", e.InvocationID)
			},
		},
		{
			name: "inject invocation info",
			inv: NewInvocation(
				WithInvocationID("test-inv-id"),
				WithInvocationBranch("test-branch"),
				WithInvocationEventFilterKey("test-filter"),
				WithInvocationRunOptions(RunOptions{RequestID: "test-request-id"}),
			),
			event: &event.Event{},
			validate: func(t *testing.T, e *event.Event) {
				require.Equal(t, "test-inv-id", e.InvocationID)
				require.Equal(t, "test-branch", e.Branch)
				require.Equal(t, "test-filter", e.FilterKey)
				require.Equal(t, "test-request-id", e.RequestID)
			},
		},
		{
			name: "inject with parent invocation",
			inv: func() *Invocation {
				parent := NewInvocation(WithInvocationID("parent-id"))
				child := parent.Clone(WithInvocationID("child-id"))
				return child
			}(),
			event: &event.Event{},
			validate: func(t *testing.T, e *event.Event) {
				require.Equal(t, "child-id", e.InvocationID)
				require.Equal(t, "parent-id", e.ParentInvocationID)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			InjectIntoEvent(tt.inv, tt.event)
			tt.validate(t, tt.event)
		})
	}
}

func TestEmitEvent(t *testing.T) {
	tests := []struct {
		name      string
		inv       *Invocation
		ch        chan *event.Event
		event     *event.Event
		expectErr bool
	}{
		{
			name:      "nil channel",
			inv:       NewInvocation(),
			ch:        nil,
			event:     &event.Event{},
			expectErr: false,
		},
		{
			name:      "nil event",
			inv:       NewInvocation(),
			ch:        make(chan *event.Event, 1),
			event:     nil,
			expectErr: false,
		},
		{
			name:      "successful emit",
			inv:       NewInvocation(WithInvocationID("test-id")),
			ch:        make(chan *event.Event, 1),
			event:     &event.Event{ID: "event-1"},
			expectErr: false,
		},
		{
			name:      "emit with nil invocation",
			inv:       nil,
			ch:        make(chan *event.Event, 1),
			event:     &event.Event{ID: "event-2"},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			err := EmitEvent(ctx, tt.inv, tt.ch, tt.event)
			if tt.expectErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGetAppendEventNoticeKey(t *testing.T) {
	tests := []struct {
		name     string
		eventID  string
		expected string
	}{
		{
			name:     "normal event ID",
			eventID:  "event-123",
			expected: AppendEventNoticeKeyPrefix + "event-123",
		},
		{
			name:     "empty event ID",
			eventID:  "",
			expected: AppendEventNoticeKeyPrefix,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := GetAppendEventNoticeKey(tt.eventID)
			require.Equal(t, tt.expected, key)
		})
	}
}

func TestWithCustomAgentConfigs(t *testing.T) {
	configs := map[string]any{"custom-llm": "test-config"}
	opts := &RunOptions{}
	WithCustomAgentConfigs(configs)(opts)

	// Verify config was set by retrieving it
	require.NotNil(t, opts.CustomAgentConfigs)
	require.Equal(t, "test-config", opts.CustomAgentConfigs["custom-llm"])
}

func TestInvocation_GetCustomAgentConfig(t *testing.T) {
	// Test get existing config - use WithCustomAgentConfigs to set it
	opts := &RunOptions{}
	WithCustomAgentConfigs(map[string]any{"custom-llm": "test-config"})(opts)

	inv := &Invocation{
		RunOptions: *opts,
	}
	require.Equal(t, "test-config", inv.GetCustomAgentConfig("custom-llm"))
	require.Nil(t, inv.GetCustomAgentConfig("non-existing"))

	// Test nil cases
	var nilInv *Invocation
	require.Nil(t, nilInv.GetCustomAgentConfig("custom-llm"))

	invWithNilConfigs := &Invocation{RunOptions: RunOptions{}}
	require.Nil(t, invWithNilConfigs.GetCustomAgentConfig("custom-llm"))
}

func TestCustomAgentConfigs_Integration(t *testing.T) {
	// Create RunOptions with configs using the proper setter
	opts := &RunOptions{}
	WithCustomAgentConfigs(map[string]any{"custom-llm": "test-config"})(opts)

	inv := NewInvocation(WithInvocationRunOptions(*opts))

	require.Equal(t, "test-config", inv.GetCustomAgentConfig("custom-llm"))

	// Test Clone preserves configs
	clonedInv := inv.Clone()
	require.Equal(t, "test-config", clonedInv.GetCustomAgentConfig("custom-llm"))
}

func TestWithModel(t *testing.T) {
	mockModel := &mockModel{name: "test-model"}
	opts := &RunOptions{}
	WithModel(mockModel)(opts)

	require.NotNil(t, opts.Model)
	require.Equal(t, "test-model", opts.Model.Info().Name)
}

func TestWithModelName(t *testing.T) {
	opts := &RunOptions{}
	WithModelName("gpt-4")(opts)

	require.Equal(t, "gpt-4", opts.ModelName)
}

func TestWithModel_Integration(t *testing.T) {
	mockModel := &mockModel{name: "custom-model"}

	// Test WithModel sets the model in RunOptions.
	inv := NewInvocation(
		WithInvocationRunOptions(RunOptions{
			Model: mockModel,
		}),
	)

	require.NotNil(t, inv.RunOptions.Model)
	require.Equal(t, "custom-model", inv.RunOptions.Model.Info().Name)
}

func TestWithModelName_Integration(t *testing.T) {
	// Test WithModelName sets the model name in RunOptions.
	inv := NewInvocation(
		WithInvocationRunOptions(RunOptions{
			ModelName: "gpt-4-turbo",
		}),
	)

	require.Equal(t, "gpt-4-turbo", inv.RunOptions.ModelName)
}

func TestInvocation_IncLLMCallCount_NoLimitOrNil(t *testing.T) {
	// nil invocation should be a no-op
	var nilInv *Invocation
	require.NoError(t, nilInv.IncLLMCallCount())

	// MaxLLMCalls <= 0 should be treated as "no limit"
	inv := &Invocation{}
	err := inv.IncLLMCallCount()
	require.NoError(t, err)
	require.Equal(t, 0, inv.llmCallCount, "counter should not increment when no limit is configured")
}

func TestInvocation_IncLLMCallCount_WithLimitAndOverflow(t *testing.T) {
	inv := &Invocation{
		MaxLLMCalls: 2,
	}

	// First call within limit.
	err := inv.IncLLMCallCount()
	require.NoError(t, err)
	require.Equal(t, 1, inv.llmCallCount)

	// Second call still within limit.
	err = inv.IncLLMCallCount()
	require.NoError(t, err)
	require.Equal(t, 2, inv.llmCallCount)

	// Third call exceeds limit and should return a StopError.
	err = inv.IncLLMCallCount()
	require.Error(t, err)
	stopErr, ok := AsStopError(err)
	require.True(t, ok, "expected StopError when LLM call limit exceeded")
	require.Contains(t, stopErr.Message, "max LLM calls (2) exceeded")
	require.Equal(t, 3, inv.llmCallCount, "counter should still increment on overflow check")
}

func TestInvocation_IncToolIteration_NoLimitOrNil(t *testing.T) {
	// nil invocation should be a no-op and report not exceeded.
	var nilInv *Invocation
	require.False(t, nilInv.IncToolIteration())

	// MaxToolIterations <= 0 should be treated as "no limit".
	inv := &Invocation{}
	exceeded := inv.IncToolIteration()
	require.False(t, exceeded)
	require.Equal(t, 0, inv.toolIterationCount, "counter should not increment when no limit is configured")
}

func TestInvocation_IncToolIteration_WithLimitAndOverflow(t *testing.T) {
	inv := &Invocation{
		MaxToolIterations: 2,
	}

	// First iteration within limit.
	exceeded := inv.IncToolIteration()
	require.False(t, exceeded)
	require.Equal(t, 1, inv.toolIterationCount)

	// Second iteration still within limit.
	exceeded = inv.IncToolIteration()
	require.False(t, exceeded)
	require.Equal(t, 2, inv.toolIterationCount)

	// Third iteration exceeds limit and should report true.
	exceeded = inv.IncToolIteration()
	require.True(t, exceeded, "expected true when tool iteration limit is exceeded")
	require.Equal(t, 3, inv.toolIterationCount)
}
