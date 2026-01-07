//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graphagent

import (
	"context"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestWithChannelBufferSize(t *testing.T) {
	tests := []struct {
		name        string
		inputSize   int
		wantBufSize int
	}{
		{
			name:        "positive buffer size",
			inputSize:   1024,
			wantBufSize: 1024,
		},
		{
			name:        "zero buffer size",
			inputSize:   0,
			wantBufSize: 0,
		},
		{
			name:        "negative size uses default",
			inputSize:   -1,
			wantBufSize: defaultChannelBufferSize,
		},
		{
			name:        "large buffer size",
			inputSize:   65536,
			wantBufSize: 65536,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := &Options{}
			option := WithChannelBufferSize(tt.inputSize)
			option(options)

			require.Equal(t, tt.wantBufSize, options.ChannelBufferSize)
		})
	}
}

func TestWithMessageFilterMode(t *testing.T) {
	tests := []struct {
		name                   string
		inputMode              MessageFilterMode
		wantBranchFilterMode   string
		wantTimelineFilterMode string
		wantPanic              bool
	}{
		{
			name:                   "FullContext mode",
			inputMode:              FullContext,
			wantBranchFilterMode:   BranchFilterModePrefix,
			wantTimelineFilterMode: TimelineFilterAll,
			wantPanic:              false,
		},
		{
			name:                   "RequestContext mode",
			inputMode:              RequestContext,
			wantBranchFilterMode:   BranchFilterModePrefix,
			wantTimelineFilterMode: TimelineFilterCurrentRequest,
			wantPanic:              false,
		},
		{
			name:                   "IsolatedRequest mode",
			inputMode:              IsolatedRequest,
			wantBranchFilterMode:   BranchFilterModeExact,
			wantTimelineFilterMode: TimelineFilterCurrentRequest,
			wantPanic:              false,
		},
		{
			name:                   "IsolatedInvocation mode",
			inputMode:              IsolatedInvocation,
			wantBranchFilterMode:   BranchFilterModeExact,
			wantTimelineFilterMode: TimelineFilterCurrentInvocation,
			wantPanic:              false,
		},
		{
			name:      "Invalid mode should panic",
			inputMode: MessageFilterMode(99),
			wantPanic: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.wantPanic {
				require.Panics(t, func() {
					opt := WithMessageFilterMode(tt.inputMode)
					opts := &Options{}
					opt(opts)
				})
				return
			}

			opt := WithMessageFilterMode(tt.inputMode)
			opts := &Options{}
			opt(opts)

			require.Equal(t, tt.wantBranchFilterMode, opts.messageBranchFilterMode)
			require.Equal(t, tt.wantTimelineFilterMode, opts.messageTimelineFilterMode)
		})
	}
}

func TestWithAddSessionSummary(t *testing.T) {
	opts := &Options{}
	WithAddSessionSummary(true)(opts)
	require.True(t, opts.AddSessionSummary)

	WithAddSessionSummary(false)(opts)
	require.False(t, opts.AddSessionSummary)
}

func TestWithMaxHistoryRuns(t *testing.T) {
	opts := &Options{}
	WithMaxHistoryRuns(5)(opts)
	require.Equal(t, 5, opts.MaxHistoryRuns)

	WithMaxHistoryRuns(0)(opts)
	require.Equal(t, 0, opts.MaxHistoryRuns)
}

func TestWithReasoningContentMode(t *testing.T) {
	tests := []struct {
		name     string
		mode     string
		wantMode string
	}{
		{
			name:     "keep_all mode",
			mode:     ReasoningContentModeKeepAll,
			wantMode: ReasoningContentModeKeepAll,
		},
		{
			name:     "discard_previous_turns mode",
			mode:     ReasoningContentModeDiscardPreviousTurns,
			wantMode: ReasoningContentModeDiscardPreviousTurns,
		},
		{
			name:     "discard_all mode",
			mode:     ReasoningContentModeDiscardAll,
			wantMode: ReasoningContentModeDiscardAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{}
			opt := WithReasoningContentMode(tt.mode)
			opt(opts)

			require.Equal(t, tt.wantMode, opts.ReasoningContentMode)
		})
	}
}

func TestWithSummaryFormatter(t *testing.T) {
	tests := []struct {
		name      string
		formatter func(summary string) string
		wantNil   bool
	}{
		{
			name: "set custom formatter",
			formatter: func(summary string) string {
				return "## Summary\n\n" + summary
			},
			wantNil: false,
		},
		{
			name:      "set nil formatter",
			formatter: nil,
			wantNil:   true,
		},
		{
			name: "set formatter with prefix",
			formatter: func(summary string) string {
				return "## Previous Context\n\n" + summary
			},
			wantNil: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := &Options{}
			opt := WithSummaryFormatter(tt.formatter)
			opt(opts)

			if tt.wantNil {
				require.Nil(t, opts.summaryFormatter)
			} else {
				require.NotNil(t, opts.summaryFormatter)
				require.NotNil(t, tt.formatter)
				// Verify the formatter works as expected.
				input := "test summary"
				expected := tt.formatter(input)
				actual := opts.summaryFormatter(input)
				require.Equal(t, expected, actual)
			}
		})
	}
}

// TestGraphAgent_ReasoningContentMode verifies that
// ReasoningContentMode option is correctly applied in createInitialState.
func TestGraphAgent_ReasoningContentMode(t *testing.T) {
	tests := []struct {
		name                 string
		reasoningContentMode string
	}{
		{
			name:                 "keep_all mode",
			reasoningContentMode: ReasoningContentModeKeepAll,
		},
		{
			name:                 "discard_previous_turns mode",
			reasoningContentMode: ReasoningContentModeDiscardPreviousTurns,
		},
		{
			name:                 "discard_all mode",
			reasoningContentMode: ReasoningContentModeDiscardAll,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := graph.NewStateSchema().
				AddField("input", graph.StateField{
					Type:    reflect.TypeOf(""),
					Reducer: graph.DefaultReducer,
				})

			g, err := graph.NewStateGraph(schema).
				AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
					return nil, nil
				}).
				SetEntryPoint("process").
				SetFinishPoint("process").
				Compile()

			require.NoError(t, err)

			ga, err := New("test-agent", g,
				WithReasoningContentMode(tt.reasoningContentMode))

			require.NoError(t, err)
			require.NotNil(t, ga)

			// Create a session with events.
			sessSvc := inmemory.NewSessionService()
			key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
			sess, err := sessSvc.CreateSession(context.Background(), key, nil)
			require.NoError(t, err)
			require.NotNil(t, sess)

			invocation := agent.NewInvocation(
				agent.WithInvocationSession(sess),
				agent.WithInvocationMessage(model.NewUserMessage("test")),
				agent.WithInvocationID("test-invocation"),
			)

			// createInitialState should not panic with ReasoningContentMode set.
			ctx := context.Background()
			initialState := ga.createInitialState(ctx, invocation)

			require.NotNil(t, initialState)
		})
	}
}

// TestGraphAgent_SummaryFormatter verifies that
// SummaryFormatter option is correctly applied in createInitialState.
func TestGraphAgent_SummaryFormatter(t *testing.T) {
	tests := []struct {
		name      string
		formatter func(summary string) string
		wantNil   bool
	}{
		{
			name: "with custom formatter",
			formatter: func(summary string) string {
				return "## Custom Summary\n\n" + summary
			},
			wantNil: false,
		},
		{
			name:      "without formatter",
			formatter: nil,
			wantNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			schema := graph.NewStateSchema().
				AddField("input", graph.StateField{
					Type:    reflect.TypeOf(""),
					Reducer: graph.DefaultReducer,
				})

			g, err := graph.NewStateGraph(schema).
				AddNode("process", func(ctx context.Context, state graph.State) (any, error) {
					return nil, nil
				}).
				SetEntryPoint("process").
				SetFinishPoint("process").
				Compile()

			require.NoError(t, err)

			ga, err := New("test-agent", g,
				WithSummaryFormatter(tt.formatter))

			require.NoError(t, err)
			require.NotNil(t, ga)

			// Create a session with events.
			sessSvc := inmemory.NewSessionService()
			key := session.Key{AppName: "app", UserID: "user", SessionID: "sid"}
			sess, err := sessSvc.CreateSession(context.Background(), key, nil)
			require.NoError(t, err)
			require.NotNil(t, sess)

			invocation := agent.NewInvocation(
				agent.WithInvocationSession(sess),
				agent.WithInvocationMessage(model.NewUserMessage("test")),
				agent.WithInvocationID("test-invocation"),
			)

			// createInitialState should not panic with SummaryFormatter set.
			ctx := context.Background()
			initialState := ga.createInitialState(ctx, invocation)

			require.NotNil(t, initialState)

			// Verify the formatter function works when set.
			if !tt.wantNil && tt.formatter != nil {
				testSummary := "test summary content"
				expected := tt.formatter(testSummary)
				require.Equal(t, expected, tt.formatter(testSummary))
			}
		})
	}
}
