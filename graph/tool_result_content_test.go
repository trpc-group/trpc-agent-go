//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestToolsNode_ToolResultContent(t *testing.T) {
	t.Run("text-only result", func(t *testing.T) {
		first, second := "hello ", "world"
		result := model.ToolResultContent{
			{Type: model.ContentTypeText, Text: &first},
			{Type: model.ContentTypeText, Text: &second},
		}
		update := runToolsNodeUpdate(t, result)
		got := toolNodeMessage(t, update)

		require.Equal(t, model.RoleTool, got.Role)
		require.Equal(t, "call-result", got.ToolID)
		require.Equal(t, "result", got.ToolName)
		require.Equal(t, "hello world", got.Content)
		require.Empty(t, got.ContentParts)
		assertToolNodeResponseOutput(t, update, mustJSON(t, result))
		assertToolNodeLastResponse(t, update, mustJSON(t, result))
	})

	t.Run("mixed content preserves parts and copies source", func(t *testing.T) {
		before, after := "before", "after"
		imageData := []byte("image-data")
		content := model.ToolResultContent{
			{Type: model.ContentTypeText, Text: &before},
			{Type: model.ContentTypeImage, Image: &model.Image{Data: imageData, Detail: "high"}},
			{Type: model.ContentTypeText, Text: &after},
			{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.test/image.png"}},
		}
		update := runToolsNodeUpdate(t, content)
		got := toolNodeMessage(t, update)
		wantOutput := mustJSON(t, content)

		require.Equal(t, model.RoleTool, got.Role)
		require.Equal(t, "call-result", got.ToolID)
		require.Equal(t, "result", got.ToolName)
		require.Empty(t, got.Content)
		require.Equal(t, []model.ContentPart(content), got.ContentParts)
		assertToolNodeResponseOutput(t, update, wantOutput)
		assertToolNodeLastResponse(t, update, wantOutput)

		content[0].Text = nil
		content[1].Image.Data[0] = 'X'
		content[1].Image.Detail = "low"
		content[2] = model.ContentPart{Type: model.ContentTypeText}
		require.Equal(t, "before", *got.ContentParts[0].Text)
		require.Equal(t, []byte("image-data"), got.ContentParts[1].Image.Data)
		require.Equal(t, "high", got.ContentParts[1].Image.Detail)
		require.Equal(t, "after", *got.ContentParts[2].Text)
	})

	t.Run("typed nil result is an empty receipt", func(t *testing.T) {
		var content model.ToolResultContent
		update := runToolsNodeUpdate(t, content)
		got := toolNodeMessage(t, update)

		require.Equal(t, model.RoleTool, got.Role)
		require.Equal(t, "call-result", got.ToolID)
		require.Equal(t, "result", got.ToolName)
		require.Empty(t, got.Content)
		require.Len(t, got.ContentParts, 1)
		require.Equal(t, model.ContentTypeText, got.ContentParts[0].Type)
		require.NotNil(t, got.ContentParts[0].Text)
		require.Empty(t, *got.ContentParts[0].Text)
		assertToolNodeResponseOutput(t, update, "null")
		assertToolNodeLastResponse(t, update, "null")
	})

	t.Run("bare content part slice keeps JSON serialization", func(t *testing.T) {
		text := "legacy"
		result := []model.ContentPart{{
			Type: model.ContentTypeText,
			Text: &text,
		}}
		update := runToolsNodeUpdate(t, result)
		got := toolNodeMessage(t, update)

		require.Equal(t, model.RoleTool, got.Role)
		require.Equal(t, "[{\"type\":\"text\",\"text\":\"legacy\"}]", got.Content)
		require.Empty(t, got.ContentParts)
		assertToolNodeResponseOutput(t, update, mustJSON(t, result))
		assertToolNodeLastResponse(t, update, mustJSON(t, result))
	})

	t.Run("valid JSON-looking text remains a marker result", func(t *testing.T) {
		text := "123"
		content := model.ToolResultContent{{
			Type: model.ContentTypeText,
			Text: &text,
		}}
		markerUpdate := runToolsNodeUpdate(t, content)
		numericUpdate := runToolsNodeUpdate(t, 123)

		assertToolNodeResponseOutput(t, markerUpdate, mustJSON(t, content))
		assertToolNodeResponseOutput(t, numericUpdate, "123")
		assertToolNodeLastResponse(t, markerUpdate, mustJSON(t, content))
		assertToolNodeLastResponse(t, numericUpdate, "123")
	})
}

func TestToolsNode_ToolResultContentAfterToolReplacement(t *testing.T) {
	replacement := "replaced"
	callbacks := tool.NewCallbacks().RegisterAfterTool(func(
		_ context.Context,
		_ string,
		_ *tool.Declaration,
		_ []byte,
		_ any,
		_ error,
	) (any, error) {
		return model.ToolResultContent{{
			Type: model.ContentTypeText,
			Text: &replacement,
		}}, nil
	})

	content := model.ToolResultContent{{
		Type: model.ContentTypeText,
		Text: &replacement,
	}}
	update := runToolsNodeUpdate(t, "original", WithToolCallbacks(callbacks))
	got := toolNodeMessage(t, update)

	require.Equal(t, model.RoleTool, got.Role)
	require.Equal(t, "call-result", got.ToolID)
	require.Equal(t, "result", got.ToolName)
	require.Equal(t, "replaced", got.Content)
	require.Empty(t, got.ContentParts)
	assertToolNodeResponseOutput(t, update, mustJSON(t, content))
	assertToolNodeLastResponse(t, update, mustJSON(t, content))
}

func TestToolsNode_ToolResultContentLastResponseWithoutNodeID(t *testing.T) {
	content := model.ToolResultContent{{Type: model.ContentTypeImage, Image: &model.Image{
		URL: "https://example.test/image.png",
	}}}
	toolsNode := NewToolsNodeFunc(map[string]tool.Tool{
		"result": &toolResultContentTestTool{name: "result", result: content},
	})
	value, err := toolsNode(context.Background(), State{
		StateKeyMessages: []model.Message{{
			Role:      model.RoleAssistant,
			ToolCalls: []model.ToolCall{resultToolCall("call-result", "result")},
		}},
		StateKeyExecContext: &ExecutionContext{InvocationID: "inv-result"},
	})
	require.NoError(t, err)
	update, ok := value.(State)
	require.True(t, ok)
	assertToolNodeLastResponse(t, update, mustJSON(t, content))
}

func TestToolsNode_ToolResultContentNodeResponsesKeepParallelOrder(t *testing.T) {
	ready := make(chan struct{})
	text := "first"
	calls := []model.ToolCall{
		resultToolCall("call-slow", "slow"),
		resultToolCall("call-fast", "fast"),
	}
	tools := map[string]tool.Tool{
		"slow": &toolResultContentTestTool{
			name:    "slow",
			result:  model.ToolResultContent{{Type: model.ContentTypeText, Text: &text}},
			waitFor: ready,
		},
		"fast": &toolResultContentTestTool{
			name:   "fast",
			result: 2,
			signal: ready,
		},
	}

	update := runToolsNodeWithTools(t, calls, tools, WithEnableParallelTools(true))
	responses := toolNodeResponses(t, update)
	require.Len(t, responses, 2)
	require.Equal(t, "call-slow", responses[0].ToolID)
	require.Equal(t, "slow", responses[0].ToolName)
	require.JSONEq(t, mustJSON(t, tools["slow"].(*toolResultContentTestTool).result), string(responses[0].Output))
	require.Equal(t, "call-fast", responses[1].ToolID)
	require.Equal(t, "fast", responses[1].ToolName)
	require.JSONEq(t, "2", string(responses[1].Output))
}

func TestToolsNode_NodeResponsesCachedMessageFallback(t *testing.T) {
	text := "cached text"
	parts := []model.ContentPart{{Type: model.ContentTypeText, Text: &text}}
	for _, tc := range []struct {
		name    string
		message model.Message
		want    string
	}{
		{
			name:    "valid JSON keeps its raw value",
			message: model.NewToolMessage("call-result", "result", `{"ok":true}`),
			want:    `{"ok":true}`,
		},
		{
			name: "content parts remain visible",
			message: model.Message{
				Role:         model.RoleTool,
				ToolID:       "call-result",
				ToolName:     "result",
				ContentParts: parts,
			},
			want: mustJSON(t, parts),
		},
		{
			name:    "plain text is encoded as a JSON string",
			message: model.NewToolMessage("call-result", "result", "cached text"),
			want:    `"cached text"`,
		},
		{
			name:    "empty text is encoded as a JSON string",
			message: model.NewToolMessage("call-result", "result", ""),
			want:    `""`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			update := runToolsNodeWithCachedMessage(t, tc.message)
			assertToolNodeResponseOutput(t, update, tc.want)
		})
	}
}

func TestToolsNode_CachedMarkerRestoresLastToolResponse(t *testing.T) {
	text := "123"
	content := model.ToolResultContent{{
		Type: model.ContentTypeText,
		Text: &text,
	}}
	record := completedToolMessageRecord{
		Message:       model.NewToolMessage("call-result", "result", text),
		ResultContent: json.RawMessage(mustJSON(t, content)),
	}
	update := runToolsNodeWithCachedValue(t, record)

	assertToolNodeResponseOutput(t, update, mustJSON(t, content))
	assertToolNodeLastResponse(t, update, mustJSON(t, content))
}

func TestToolsNode_ToolResultContentInterruptResume(t *testing.T) {
	const lineageID = "tool-result-interrupt-resume"
	firstText, finalText := "123", "resumed"
	calls := []model.ToolCall{
		resultToolCall("call-marker", "marker"),
		resultToolCall("call-empty", "empty"),
		resultToolCall("call-later", "later"),
	}
	var markerCalls, emptyCalls, laterCalls int
	tools := map[string]tool.Tool{
		"marker": &toolResultContentTestTool{
			name: "marker",
			callFn: func(context.Context, []byte) (any, error) {
				markerCalls++
				return model.ToolResultContent{{
					Type: model.ContentTypeText,
					Text: &firstText,
				}}, nil
			},
		},
		"empty": &toolResultContentTestTool{
			name: "empty",
			callFn: func(context.Context, []byte) (any, error) {
				emptyCalls++
				var empty model.ToolResultContent
				return empty, nil
			},
		},
		"later": &toolResultContentTestTool{
			name: "later",
			callFn: func(context.Context, []byte) (any, error) {
				laterCalls++
				if laterCalls == 1 {
					return nil, NewInterruptError("continue")
				}
				return model.ToolResultContent{{
					Type: model.ContentTypeText,
					Text: &finalText,
				}}, nil
			},
		},
	}

	g, err := NewStateGraph(MessagesStateSchema()).
		AddToolsNode("tools", tools, WithEnableParallelTools(false)).
		SetEntryPoint("tools").
		SetFinishPoint("tools").
		Compile()
	require.NoError(t, err)
	saver := newMockSaver()
	executor, err := NewExecutor(g, WithCheckpointSaver(saver))
	require.NoError(t, err)
	initial := State{
		CfgKeyLineageID: lineageID,
		StateKeyMessages: []model.Message{{
			Role:      model.RoleAssistant,
			ToolCalls: calls,
		}},
	}
	firstRun, err := executor.Execute(
		context.Background(),
		initial,
		&agent.Invocation{InvocationID: lineageID},
	)
	require.NoError(t, err)
	for range firstRun {
	}
	require.Equal(t, 1, markerCalls)
	require.Equal(t, 1, emptyCalls)
	require.Equal(t, 1, laterCalls)

	interruptTuple := latestInterruptTuple(t, saver, lineageID)
	encodedCheckpoint, err := json.Marshal(interruptTuple.Checkpoint)
	require.NoError(t, err)
	var restoredCheckpoint Checkpoint
	require.NoError(t, json.Unmarshal(encodedCheckpoint, &restoredCheckpoint))
	interruptTuple.Checkpoint = &restoredCheckpoint

	resumeState := State{
		CfgKeyLineageID:    lineageID,
		CfgKeyCheckpointID: restoredCheckpoint.ID,
	}
	resumedEvents, err := executor.Execute(
		context.Background(),
		resumeState,
		&agent.Invocation{InvocationID: lineageID},
	)
	require.NoError(t, err)
	var finalDelta map[string][]byte
	for evt := range resumedEvents {
		if evt.Done && evt.StateDelta != nil {
			finalDelta = evt.StateDelta
		}
	}
	require.NotNil(t, finalDelta)
	require.Equal(t, 1, markerCalls, "completed marker tool must not run again")
	require.Equal(t, 1, emptyCalls, "completed empty tool must not run again")
	require.Equal(t, 2, laterCalls, "interrupted tool should run again on resume")

	var lastToolResponse string
	require.NoError(t, json.Unmarshal(finalDelta[StateKeyLastToolResponse], &lastToolResponse))
	require.JSONEq(t, mustJSON(t, model.ToolResultContent{{
		Type: model.ContentTypeText,
		Text: &finalText,
	}}), lastToolResponse)
	var nodeResponses map[string]string
	require.NoError(t, json.Unmarshal(finalDelta[StateKeyNodeResponses], &nodeResponses))
	var toolResponses []toolNodeResponseForTest
	require.NoError(t, json.Unmarshal([]byte(nodeResponses["tools"]), &toolResponses))
	require.Len(t, toolResponses, 3)
	require.Equal(t, "call-marker", toolResponses[0].ToolID)
	require.JSONEq(t, mustJSON(t, model.ToolResultContent{{
		Type: model.ContentTypeText,
		Text: &firstText,
	}}), string(toolResponses[0].Output))
	require.Equal(t, "call-empty", toolResponses[1].ToolID)
	require.JSONEq(t, "null", string(toolResponses[1].Output))
	require.Equal(t, "call-later", toolResponses[2].ToolID)
	require.JSONEq(t, mustJSON(t, model.ToolResultContent{{
		Type: model.ContentTypeText,
		Text: &finalText,
	}}), string(toolResponses[2].Output))
}

func runToolsNodeUpdate(t *testing.T, result any, opts ...Option) State {
	return runToolsNodeWithTools(t, []model.ToolCall{resultToolCall("call-result", "result")}, map[string]tool.Tool{
		"result": &toolResultContentTestTool{name: "result", result: result},
	}, opts...)
}

func runToolsNodeWithCachedMessage(t *testing.T, message model.Message) State {
	return runToolsNodeWithCachedValue(t, message)
}

func runToolsNodeWithCachedValue(t *testing.T, cached any) State {
	t.Helper()
	call := resultToolCall("call-result", "result")
	state := State{
		StateKeyMessages: []model.Message{{
			Role:      model.RoleAssistant,
			ToolCalls: []model.ToolCall{call},
		}},
		StateKeyExecContext:   &ExecutionContext{InvocationID: "inv-result"},
		StateKeyCurrentNodeID: "tools-result",
		stateKeyCompletedToolMessages: map[string]any{
			"tools-result": map[string]any{
				completedToolMessageKey(0, call): cached,
			},
		},
	}
	toolsNode := NewToolsNodeFunc(nil)
	value, err := toolsNode(context.Background(), state)
	require.NoError(t, err)
	update, ok := value.(State)
	require.True(t, ok)
	return update
}

func runToolsNodeWithTools(
	t *testing.T,
	calls []model.ToolCall,
	tools map[string]tool.Tool,
	opts ...Option,
) State {
	t.Helper()

	state := State{
		StateKeyMessages: []model.Message{{
			Role:      model.RoleAssistant,
			ToolCalls: calls,
		}},
		StateKeyExecContext:   &ExecutionContext{InvocationID: "inv-result"},
		StateKeyCurrentNodeID: "tools-result",
	}
	toolsNode := NewToolsNodeFunc(tools, opts...)

	value, err := toolsNode(context.Background(), state)
	require.NoError(t, err)
	update, ok := value.(State)
	require.True(t, ok)
	return update
}

func toolNodeMessage(t *testing.T, update State) model.Message {
	t.Helper()
	messages, ok := update[StateKeyMessages].([]model.Message)
	require.True(t, ok)
	require.Len(t, messages, 1)
	return messages[0]
}

func assertToolNodeResponseOutput(t *testing.T, update State, want string) {
	t.Helper()
	responses := toolNodeResponses(t, update)
	require.Len(t, responses, 1)
	require.JSONEq(t, want, string(responses[0].Output))
}

func assertToolNodeLastResponse(t *testing.T, update State, want string) {
	t.Helper()
	got, ok := update[StateKeyLastToolResponse].(string)
	require.True(t, ok)
	require.JSONEq(t, want, got)
}

type toolNodeResponseForTest struct {
	ToolID   string          `json:"tool_id"`
	ToolName string          `json:"tool_name"`
	Output   json.RawMessage `json:"output"`
}

func toolNodeResponses(t *testing.T, update State) []toolNodeResponseForTest {
	t.Helper()
	responses, ok := update[StateKeyNodeResponses].(map[string]any)
	require.True(t, ok)
	raw, ok := responses["tools-result"].(string)
	require.True(t, ok)
	var decoded []toolNodeResponseForTest
	require.NoError(t, json.Unmarshal([]byte(raw), &decoded))
	return decoded
}

func mustJSON(t *testing.T, value any) string {
	t.Helper()
	content, err := json.Marshal(value)
	require.NoError(t, err)
	return string(content)
}

func resultToolCall(id, name string) model.ToolCall {
	return model.ToolCall{
		ID: id,
		Function: model.FunctionDefinitionParam{
			Name:      name,
			Arguments: []byte(`{}`),
		},
	}
}

type toolResultContentTestTool struct {
	name    string
	result  any
	callFn  func(context.Context, []byte) (any, error)
	waitFor <-chan struct{}
	signal  chan<- struct{}
}

func (t *toolResultContentTestTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: t.name}
}

func (t *toolResultContentTestTool) Call(ctx context.Context, args []byte) (any, error) {
	if t.signal != nil {
		close(t.signal)
	}
	if t.waitFor != nil {
		select {
		case <-t.waitFor:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if t.callFn != nil {
		return t.callFn(ctx, args)
	}
	return t.result, nil
}
