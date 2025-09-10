//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package event

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewEvent(t *testing.T) {
	const (
		invocationID = "invocation-123"
		author       = "tester"
	)

	evt := New(invocationID, author)
	require.NotNil(t, evt)
	require.Equal(t, invocationID, evt.InvocationID)
	require.Equal(t, author, evt.Author)
	require.NotEmpty(t, evt.ID)
	require.WithinDuration(t, time.Now(), evt.Timestamp, 2*time.Second)
}

func TestNewErrorEvent(t *testing.T) {
	const (
		invocationID = "invocation-err"
		author       = "tester"
		errType      = model.ErrorTypeAPIError
		errMsg       = "something went wrong"
	)

	evt := NewErrorEvent(invocationID, author, errType, errMsg)
	require.NotNil(t, evt.Error)
	require.Equal(t, model.ObjectTypeError, evt.Object)
	require.Equal(t, errType, evt.Error.Type)
	require.Equal(t, errMsg, evt.Error.Message)
	require.True(t, evt.Done)
}

func TestNewResponseEvent(t *testing.T) {
	const (
		invocationID = "invocation-resp"
		author       = "tester"
	)

	resp := &model.Response{
		Object: "chat.completion",
		Done:   true,
	}

	evt := NewResponseEvent(invocationID, author, resp)
	require.Equal(t, resp, evt.Response)
	require.Equal(t, invocationID, evt.InvocationID)
	require.Equal(t, author, evt.Author)
}

func TestEvent_WithOptions_And_Clone(t *testing.T) {
	resp := &model.Response{
		Object:  "chat.completion",
		Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "hi"}}},
		Usage:   &model.Usage{PromptTokens: 1, CompletionTokens: 2, TotalTokens: 3},
		Error:   &model.ResponseError{Message: "", Type: ""},
	}

	sd := map[string][]byte{"k": []byte("v")}
	sevt := New("inv-1", "author",
		WithBranch("b1"),
		WithResponse(resp),
		WithObject("obj-x"),
		WithStateDelta(sd),
		WithStructuredOutputPayload(map[string]any{"x": 1}),
		WithSkipSummarization(),
	)

	require.Equal(t, "b1", sevt.Branch)
	require.Equal(t, "obj-x", sevt.Object)
	require.NotNil(t, sevt.Actions)
	require.True(t, sevt.Actions.SkipSummarization)
	require.NotNil(t, sevt.StructuredOutput)
	require.NotNil(t, sevt.StateDelta)
	require.Equal(t, "v", string(sevt.StateDelta["k"]))

	// LongRunningToolIDs prepared for clone coverage
	sevt.LongRunningToolIDs = map[string]struct{}{"id1": {}}

	// Clone and verify deep copy of Response, maps
	clone := sevt.Clone()
	require.NotNil(t, clone)
	require.NotSame(t, sevt, clone)
	require.Equal(t, sevt.InvocationID, clone.InvocationID)
	require.Equal(t, sevt.Author, clone.Author)
	require.NotNil(t, clone.Response)
	require.NotSame(t, sevt.Response, clone.Response)
	// mutate source maps and ensure clone is unaffected
	sevt.StateDelta["k"][0] = 'X'
	sevt.LongRunningToolIDs["id2"] = struct{}{}
	require.Equal(t, "v", string(clone.StateDelta["k"]))
	if _, ok := clone.LongRunningToolIDs["id2"]; ok {
		t.Fatalf("clone should not contain id2")
	}
}
