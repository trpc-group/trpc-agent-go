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
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestWithMessagesOption_SetsRunOptions(t *testing.T) {
	msgs := []model.Message{
		model.NewSystemMessage("s"),
		model.NewUserMessage("hi"),
	}
	var ro RunOptions
	WithMessages(msgs)(&ro)

	require.Equal(t, 2, len(ro.Messages))
	require.Equal(t, msgs[0].Role, ro.Messages[0].Role)
	require.Equal(t, msgs[1].Content, ro.Messages[1].Content)
}

func TestWithRuntimeState(t *testing.T) {
	state := map[string]any{
		"user_id": "12345",
		"room_id": 678,
		"config":  true,
	}

	var ro RunOptions
	WithRuntimeState(state)(&ro)

	require.NotNil(t, ro.RuntimeState)
	require.Equal(t, state, ro.RuntimeState)
	require.Equal(t, "12345", ro.RuntimeState["user_id"])
	require.Equal(t, 678, ro.RuntimeState["room_id"])
	require.Equal(t, true, ro.RuntimeState["config"])
}

func TestWithKnowledgeFilter(t *testing.T) {
	filter := map[string]any{
		"category": "tech",
		"tags":     []string{"golang", "testing"},
	}

	var ro RunOptions
	WithKnowledgeFilter(filter)(&ro)

	require.NotNil(t, ro.KnowledgeFilter)
	require.Equal(t, filter, ro.KnowledgeFilter)
	require.Equal(t, "tech", ro.KnowledgeFilter["category"])
}

func TestWithRequestID(t *testing.T) {
	tests := []struct {
		name      string
		requestID string
	}{
		{
			name:      "normal request ID",
			requestID: "req-123-456-789",
		},
		{
			name:      "empty request ID",
			requestID: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ro RunOptions
			WithRequestID(tt.requestID)(&ro)

			require.Equal(t, tt.requestID, ro.RequestID)
		})
	}
}

func TestWithA2ARequestOptions(t *testing.T) {
	tests := []struct {
		name string
		opts []any
	}{
		{
			name: "single option",
			opts: []any{"option1"},
		},
		{
			name: "multiple options",
			opts: []any{"option1", "option2", "option3"},
		},
		{
			name: "empty options",
			opts: []any{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ro RunOptions
			WithA2ARequestOptions(tt.opts...)(&ro)

			require.Equal(t, len(tt.opts), len(ro.A2ARequestOptions))
			for i, opt := range tt.opts {
				require.Equal(t, opt, ro.A2ARequestOptions[i])
			}
		})
	}
}

func TestMultipleRunOptions(t *testing.T) {
	msgs := []model.Message{
		model.NewUserMessage("test"),
	}
	state := map[string]any{"key": "value"}
	filter := map[string]any{"filter": "test"}

	var ro RunOptions
	WithMessages(msgs)(&ro)
	WithRuntimeState(state)(&ro)
	WithKnowledgeFilter(filter)(&ro)
	WithResume(true)(&ro)
	WithRequestID("multi-req-123")(&ro)
	WithA2ARequestOptions("opt1", "opt2")(&ro)

	require.Equal(t, msgs, ro.Messages)
	require.Equal(t, state, ro.RuntimeState)
	require.Equal(t, filter, ro.KnowledgeFilter)
	require.True(t, ro.Resume)
	require.Equal(t, "multi-req-123", ro.RequestID)
	require.Equal(t, 2, len(ro.A2ARequestOptions))
}

func TestWithResume(t *testing.T) {
	var ro RunOptions
	WithResume(true)(&ro)
	require.True(t, ro.Resume)

	WithResume(false)(&ro)
	require.False(t, ro.Resume)
}
