//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package recorder

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type marshalToObjectString string

func (m marshalToObjectString) MarshalJSON() ([]byte, error) {
	return []byte(`{"value":"x"}`), nil
}

type invalidStateJSON struct{}

func (invalidStateJSON) MarshalJSON() ([]byte, error) {
	return []byte("{"), nil
}

func TestAccumulator_CoversGuardBranches(t *testing.T) {
	acc := newAccumulator()
	ctxMsgs := []model.Message{model.NewSystemMessage("ctx")}
	acc.captureRunInputs(map[string]any{
		"plain":        "value",
		"marshal_fail": make(chan int),
		"decode_fail":  invalidStateJSON{},
		"nil":          nil,
	}, ctxMsgs)
	acc.captureRunInputs(map[string]any{"ignored": true}, []model.Message{model.NewSystemMessage("later")})
	require.Equal(t, "value", acc.sessionInputState["plain"])
	require.IsType(t, "", acc.sessionInputState["marshal_fail"])
	require.Equal(t, "{}", acc.sessionInputState["decode_fail"])
	require.Nil(t, acc.sessionInputState["nil"])
	require.Len(t, acc.contextMessages, 1)
	assert.Equal(t, "ctx", acc.contextMessages[0].Content)
	acc.setUserContent(model.Message{Role: model.RoleUser})
	acc.setUserContent(model.NewUserMessage("user-1"))
	acc.setUserContent(model.NewUserMessage("user-2"))
	assert.Equal(t, "user-1", acc.userContent.Content)
	acc.setFinalResponse(model.Message{Role: model.RoleAssistant})
	acc.setFinalResponse(model.NewAssistantMessage("final-1"))
	acc.setFinalResponse(model.NewAssistantMessage("final-2"))
	assert.Equal(t, "final-2", acc.finalResponse.Content)
	acc.addIntermediateResponse(model.NewUserMessage("ignored"))
	acc.addIntermediateResponse(model.Message{Role: model.RoleAssistant})
	acc.addIntermediateResponse(model.NewAssistantMessage("mid"))
	require.Len(t, acc.intermediateResponses, 1)
	assert.Equal(t, "mid", acc.intermediateResponses[0].Content)
	acc.addToolCall(model.ToolCall{})
	acc.addToolCall(model.ToolCall{ID: "tool-1", Function: model.FunctionDefinitionParam{Arguments: []byte(" ")}})
	acc.addToolCall(model.ToolCall{ID: "tool-1", Function: model.FunctionDefinitionParam{Name: "ignored", Arguments: []byte(`{"x":2}`)}})
	acc.addToolCall(model.ToolCall{ID: "tool-2", Function: model.FunctionDefinitionParam{Name: "search", Arguments: []byte("not-json")}})
	acc.addToolResult("", "ignored", `{"skip":true}`)
	acc.addToolResult("tool-1", "calc", `{"done":true}`)
	acc.addToolResult("tool-3", "lookup", "not-json")
	require.Len(t, acc.tools, 3)
	assert.Equal(t, "calc", acc.tools[0].Name)
	assert.Equal(t, map[string]any{}, acc.tools[0].Arguments)
	assert.Equal(t, map[string]any{"done": true}, acc.tools[0].Result)
	assert.Equal(t, "not-json", acc.tools[1].Arguments)
	assert.Equal(t, "not-json", acc.tools[2].Result)
	snapshot := acc.finalizeAndSnapshot()
	require.True(t, snapshot.finalized)
	require.Len(t, snapshot.tools, 3)
	acc.setRunError(model.ResponseError{Type: "ignored", Message: "ignored"})
	acc.addIntermediateResponse(model.NewAssistantMessage("after-finalize"))
	acc.addToolCall(model.ToolCall{ID: "tool-4"})
	acc.addToolResult("tool-1", "ignored", `{"skip":false}`)
	assert.True(t, acc.isFinalized())
	assert.Equal(t, map[string]any{}, cloneStateMap(nil))
	ch := make(chan int)
	assert.Equal(t, ch, cloneValue("channel", ch))
	assert.Equal(t, marshalToObjectString("value"), cloneValue("marshal-only", marshalToObjectString("value")))
	assert.Nil(t, normalizeStateValue(nil))
	assert.IsType(t, "", normalizeStateValue(make(chan int)))
	assert.Equal(t, "{}", normalizeStateValue(invalidStateJSON{}))
	assert.Equal(t, map[string]any{}, parseToolCallArguments([]byte(" ")))
	assert.Equal(t, "bad-json", parseToolCallArguments([]byte("bad-json")))
	assert.Equal(t, "", parseToolResultContent(""))
	assert.Equal(t, "bad-json", parseToolResultContent("bad-json"))
}
