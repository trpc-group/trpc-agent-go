//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent

import (
	"testing"

	"github.com/stretchr/testify/require"
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

func TestWithMaxLimits_OnOptions(t *testing.T) {
	opts := &Options{}

	WithMaxLLMCalls(3)(opts)
	WithMaxToolIterations(4)(opts)

	if opts.MaxLLMCalls != 3 {
		t.Fatalf("expected MaxLLMCalls=3, got %d", opts.MaxLLMCalls)
	}
	if opts.MaxToolIterations != 4 {
		t.Fatalf("expected MaxToolIterations=4, got %d", opts.MaxToolIterations)
	}
}

func TestWithSkillRunAllowedCommands_CopiesSlice(t *testing.T) {
	in := []string{"echo", "ls"}
	opts := &Options{}
	WithSkillRunAllowedCommands(in...)(opts)

	in[0] = "rm"
	require.Equal(t, []string{"echo", "ls"}, opts.skillRunAllowedCommands)
}

func TestWithSkillRunDeniedCommands_CopiesSlice(t *testing.T) {
	in := []string{"echo", "ls"}
	opts := &Options{}
	WithSkillRunDeniedCommands(in...)(opts)

	in[0] = "rm"
	require.Equal(t, []string{"echo", "ls"}, opts.skillRunDeniedCommands)
}

func TestWithSummaryFormatter(t *testing.T) {
	tests := []struct {
		name      string
		formatter func(summary string) string
		wantNil   bool
	}{
		{
			name:      "set custom formatter",
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
