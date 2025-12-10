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

			if options.ChannelBufferSize != tt.wantBufSize {
				t.Errorf("got buf size %d, want %d", options.ChannelBufferSize, tt.wantBufSize)
			}
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
				defer func() {
					if r := recover(); r == nil {
						t.Error("Expected panic but did not get one")
					}
				}()
			}

			opt := WithMessageFilterMode(tt.inputMode)
			opts := &Options{}
			opt(opts)

			if !tt.wantPanic {
				if opts.messageBranchFilterMode != tt.wantBranchFilterMode {
					t.Errorf("BranchFilterMode got = %v, want %v",
						opts.messageBranchFilterMode, tt.wantBranchFilterMode)
				}
				if opts.messageTimelineFilterMode != tt.wantTimelineFilterMode {
					t.Errorf("TimelineFilterMode got = %v, want %v",
						opts.messageTimelineFilterMode, tt.wantTimelineFilterMode)
				}
			}
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
