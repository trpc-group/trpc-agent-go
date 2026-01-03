//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package adapter_test

import (
	"encoding/json"
	"testing"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
)

func TestRunAgentInputJSONUnmarshal(t *testing.T) {
	raw := `{
		"threadId": "thread-123",
		"runId": "run-456",
		"messages": [
			{"role": "user", "content": "hi there"}
		],
		"state": {"cursor": 1, "flags": ["a", "b"]},
		"forwardedProps": {"userId": "alice", "metadata": {"traceId": "trace-01"}}
	}`

	var input adapter.RunAgentInput
	assert.NoError(t, json.Unmarshal([]byte(raw), &input))

	assert.Equal(t, "thread-123", input.ThreadID)
	assert.Equal(t, "run-456", input.RunID)
	assert.Len(t, input.Messages, 1)
	assert.Equal(t, types.RoleUser, input.Messages[0].Role)
	assert.Equal(t, "hi there", input.Messages[0].Content)

	assert.Equal(t, map[string]any{"cursor": float64(1), "flags": []any{"a", "b"}}, input.State)
	forwardedProps, ok := input.ForwardedProps.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "alice", forwardedProps["userId"])

	metadata, ok := forwardedProps["metadata"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "trace-01", metadata["traceId"])
}

func TestRunAgentInputJSONMarshal(t *testing.T) {
	input := adapter.RunAgentInput{
		ThreadID: "thread-xyz",
		RunID:    "run-999",
		Messages: []types.Message{{Role: types.RoleAssistant, Content: "result"}},
		State:    map[string]any{"step": 2},
		ForwardedProps: map[string]any{
			"userId": "bob",
			"tags":   []string{"x", "y"},
		},
	}

	data, err := json.Marshal(input)
	assert.NoError(t, err)

	var decoded map[string]any
	assert.NoError(t, json.Unmarshal(data, &decoded))

	assert.Equal(t, "thread-xyz", decoded["threadId"])
	assert.Equal(t, "run-999", decoded["runId"])

	msgs, ok := decoded["messages"].([]any)
	assert.True(t, ok)
	assert.Len(t, msgs, 1)
	first, ok := msgs[0].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "assistant", first["role"])
	assert.Equal(t, "result", first["content"])

	assert.Equal(t, map[string]any{"step": float64(2)}, decoded["state"])

	props, ok := decoded["forwardedProps"].(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, "bob", props["userId"])
	assert.ElementsMatch(t, []any{"x", "y"}, props["tags"].([]any))
}
