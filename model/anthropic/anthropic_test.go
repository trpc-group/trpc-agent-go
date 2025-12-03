//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package anthropic

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	anthropicopt "github.com/anthropics/anthropic-sdk-go/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	agentlog "trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// stubTool implements tool.Tool for testing purposes.
type stubTool struct{ decl *tool.Declaration }

// Call implements tool.Tool for testing.
func (s stubTool) Call(_ context.Context, _ []byte) (any, error) { return nil, nil }

// Declaration returns the tool declaration.
func (s stubTool) Declaration() *tool.Declaration { return s.decl }

type stubLogger struct {
	debugfCalled bool
	debugfMsg    string
}

func (stubLogger) Debug(args ...any) {}
func (l *stubLogger) Debugf(format string, args ...any) {
	l.debugfCalled = true
	l.debugfMsg = fmt.Sprintf(format, args...)
}
func (stubLogger) Info(args ...any)                  {}
func (stubLogger) Infof(format string, args ...any)  {}
func (stubLogger) Warn(args ...any)                  {}
func (stubLogger) Warnf(format string, args ...any)  {}
func (stubLogger) Error(args ...any)                 {}
func (stubLogger) Errorf(format string, args ...any) {}
func (stubLogger) Fatal(args ...any)                 {}
func (stubLogger) Fatalf(format string, args ...any) {}

func Test_Model_Info(t *testing.T) {
	m := New("claude-3-5-sonnet-latest")
	info := m.Info()
	assert.Equal(t, "claude-3-5-sonnet-latest", info.Name)
}

func TestWithHeaders_AppendsOptions(t *testing.T) {
	o := &options{}
	headers := map[string]string{
		"X-Custom":  "custom-value",
		"X-TraceID": "trace-123",
	}

	WithHeaders(headers)(o)
	assert.Len(t, o.anthropicClientOptions, 2, "expected headers to expand into options")

	headers["X-Custom"] = "changed"
	WithHeaders(map[string]string{"User-Agent": "test-agent"})(o)
	assert.Len(t, o.anthropicClientOptions, 3, "expected additional headers to append")

	opts1 := &options{}
	WithHeaders(nil)(opts1)
	assert.Len(t, opts1.anthropicClientOptions, 0, "expected no headers to be applied")
}

func Test_Model_GenerateContent_NilRequest(t *testing.T) {
	m := New("claude-3-5-sonnet-latest")
	ctx := context.Background()
	ch, err := m.GenerateContent(ctx, nil)
	assert.Error(t, err)
	assert.Nil(t, ch)
}

func Test_convertUserMessage(t *testing.T) {
	p1 := "part-1"
	p2 := "part-2"
	msg := model.Message{
		Role:    model.RoleUser,
		Content: "head",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &p1},
			{Type: model.ContentTypeText, Text: &p2},
		},
	}
	out := convertUserMessage(msg)
	assert.Equal(t, 3, len(out.Content))
	// Validate text blocks order and content.
	assert.NotNil(t, out.Content[0].OfText)
	assert.Equal(t, "head", out.Content[0].OfText.Text)
	assert.NotNil(t, out.Content[1].OfText)
	assert.Equal(t, p1, out.Content[1].OfText.Text)
	assert.NotNil(t, out.Content[2].OfText)
	assert.Equal(t, p2, out.Content[2].OfText.Text)
}

func Test_convertAssistantMessageContent(t *testing.T) {
	p := "assistant-part"
	msg := model.Message{
		Role:    model.RoleAssistant,
		Content: "assistant-head",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &p},
		},
		ToolCalls: []model.ToolCall{
			{
				ID:   "call-1",
				Type: functionToolType,
				Function: model.FunctionDefinitionParam{
					Name:      "fn",
					Arguments: []byte(`{"x":1}`),
				},
			},
		},
	}
	out := convertAssistantMessageContent(msg)
	// Expect: 1 head text + 1 part text + 1 tool use.
	assert.Equal(t, 3, len(out.Content))
	assert.NotNil(t, out.Content[0].OfText)
	assert.Equal(t, "assistant-head", out.Content[0].OfText.Text)
	assert.NotNil(t, out.Content[1].OfText)
	assert.Equal(t, p, out.Content[1].OfText.Text)
	// Last block should be a tool use block.
	assert.NotNil(t, out.Content[2].OfToolUse)
}

func Test_convertSystemMessageContent(t *testing.T) {
	p := "sys-part"
	msg := model.Message{
		Role:    model.RoleSystem,
		Content: "sys",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &p},
		},
	}
	blocks := convertSystemMessageContent(msg)
	assert.Equal(t, 2, len(blocks))
	assert.Equal(t, "sys", blocks[0].Text)
	assert.Equal(t, p, blocks[1].Text)
}

func Test_convertTools(t *testing.T) {
	toolsMap := map[string]tool.Tool{
		"t1": stubTool{decl: &tool.Declaration{
			Name:        "t1",
			Description: "desc",
			InputSchema: &tool.Schema{Type: "object"},
		}},
	}
	params := convertTools(toolsMap)
	assert.Equal(t, 1, len(params))
	assert.NotNil(t, params[0].OfTool)
	assert.Equal(t, "t1", params[0].OfTool.Name)
}

func Test_buildToolDescription_AppendsOutputSchema(t *testing.T) {
	schema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"status": {Type: "string"},
		},
	}
	decl := &tool.Declaration{
		Name:         "foo",
		Description:  "desc",
		OutputSchema: schema,
	}

	desc := buildToolDescription(decl)

	assert.Contains(t, desc, "desc", "expected base description to remain")
	assert.Contains(t, desc, "Output schema:", "expected output schema label to be present")
	assert.Contains(t, desc, `"status"`, "expected output schema to be embedded in description")
}

func Test_buildToolDescription_MarshalError(t *testing.T) {
	logger := &stubLogger{}
	original := agentlog.Default
	agentlog.Default = logger
	defer func() { agentlog.Default = original }()

	decl := &tool.Declaration{
		Name:        "foo",
		Description: "desc",
		OutputSchema: &tool.Schema{
			Type:                 "object",
			AdditionalProperties: func() {},
		},
	}

	desc := buildToolDescription(decl)

	assert.Equal(t, "desc", desc, "description should fall back when marshal fails")
	assert.True(t, logger.debugfCalled, "expected marshal error to be logged")
	assert.Contains(t, logger.debugfMsg, "marshal output schema", "expected marshal error message")
}

func Test_buildToolDescription_NoOutputSchema(t *testing.T) {
	decl := &tool.Declaration{
		Name:        "foo",
		Description: "bar",
	}

	desc := buildToolDescription(decl)

	assert.Equal(t, "bar", desc, "description should stay unchanged when no output schema")
}

func Test_convertTools_UsesOutputSchemaDescription(t *testing.T) {
	outputSchema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"count": {Type: "integer"},
		},
	}
	decl := &tool.Declaration{
		Name:         "tool_with_out",
		Description:  "tool desc",
		InputSchema:  &tool.Schema{Type: "object"},
		OutputSchema: outputSchema,
	}

	params := convertTools(map[string]tool.Tool{
		decl.Name: stubTool{decl: decl},
	})

	require.Len(t, params, 1)
	require.NotNil(t, params[0].OfTool)
	expected := buildToolDescription(decl)
	assert.True(t, params[0].OfTool.Description.Valid(), "description should be set")
	assert.Equal(t, expected, params[0].OfTool.Description.Value)
	assert.Contains(t, params[0].OfTool.Description.Value, `"count"`, "output schema JSON should appear in description")
}

func Test_decodeToolArguments(t *testing.T) {
	// Empty -> empty map.
	v := decodeToolArguments(nil)
	_, ok := v.(map[string]any)
	assert.True(t, ok)

	// Invalid -> empty map.
	v2 := decodeToolArguments([]byte("not-json"))
	_, ok = v2.(map[string]any)
	assert.True(t, ok)

	// Valid -> parsed map.
	v3 := decodeToolArguments([]byte(`{"a":1,"b":"x"}`))
	m, ok := v3.(map[string]any)
	assert.True(t, ok)
	assert.Equal(t, float64(1), m["a"]) // JSON numbers are float64.
	assert.Equal(t, "x", m["b"])
}

func Test_convertToolResult(t *testing.T) {
	msg := model.Message{Role: model.RoleTool, ToolID: "tool-1", Content: "payload"}
	out := convertToolResult(msg)
	assert.Equal(t, 1, len(out.Content))
	assert.NotNil(t, out.Content[0].OfToolResult)
	assert.Equal(t, "tool-1", out.Content[0].OfToolResult.ToolUseID)
	// Note: Tool result content text is SDK-specific; we avoid asserting nested content here.
}

func Test_convertMessages_MergeToolResultsAndDropEmpty(t *testing.T) {
	// Prepare messages: user(A), tool(id1), tool(id2), user(B).
	msgs := []model.Message{
		model.NewUserMessage("A"),
		{Role: model.RoleTool, Content: "r1", ToolID: "id1"},
		{Role: model.RoleTool, Content: "r2", ToolID: "id2"},
		model.NewUserMessage("B"),
	}

	converted, systemPrompts, err := convertMessages(msgs)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(systemPrompts))

	// Expect: A, merged(tool id1+id2), B.
	assert.Equal(t, 3, len(converted))

	// First: text A.
	assert.True(t, len(converted[0].Content) >= 1)
	assert.NotNil(t, converted[0].Content[0].OfText)
	assert.Equal(t, "A", converted[0].Content[0].OfText.Text)

	// Second: two tool result blocks with ids id1, id2.
	assert.Equal(t, 2, len(converted[1].Content))
	assert.NotNil(t, converted[1].Content[0].OfToolResult)
	assert.Equal(t, "id1", converted[1].Content[0].OfToolResult.ToolUseID)
	assert.NotNil(t, converted[1].Content[1].OfToolResult)
	assert.Equal(t, "id2", converted[1].Content[1].OfToolResult.ToolUseID)

	// Third: text B.
	assert.True(t, len(converted[2].Content) >= 1)
	assert.NotNil(t, converted[2].Content[0].OfText)
	assert.Equal(t, "B", converted[2].Content[0].OfText.Text)
}

func Test_convertMessages_SystemPrompts(t *testing.T) {
	p := "extra"
	msgs := []model.Message{
		{
			Role:    model.RoleSystem,
			Content: "sys",
			ContentParts: []model.ContentPart{
				{
					Type: model.ContentTypeText,
					Text: &p,
				},
			},
		},
		model.NewUserMessage("U"),
	}
	converted, systemPrompts, err := convertMessages(msgs)
	assert.NoError(t, err)
	assert.Equal(t, 1, len(converted))
	assert.Equal(t, 2, len(systemPrompts))
	assert.Equal(t, "sys", systemPrompts[0].Text)
	assert.Equal(t, p, systemPrompts[1].Text)
}

func Test_convertMessages_StartingWithToolResults(t *testing.T) {
	msgs := []model.Message{
		{Role: model.RoleTool, Content: "r1", ToolID: "t1"},
		{Role: model.RoleTool, Content: "r2", ToolID: "t2"},
		model.NewUserMessage("X"),
	}

	converted, systemPrompts, err := convertMessages(msgs)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(systemPrompts))
	assert.Equal(t, 2, len(converted))

	// First should be merged tool results.
	assert.Equal(t, 2, len(converted[0].Content))
	assert.NotNil(t, converted[0].Content[0].OfToolResult)
	assert.Equal(t, "t1", converted[0].Content[0].OfToolResult.ToolUseID)
	assert.NotNil(t, converted[0].Content[1].OfToolResult)
	assert.Equal(t, "t2", converted[0].Content[1].OfToolResult.ToolUseID)

	// Second is user text X.
	assert.True(t, len(converted[1].Content) >= 1)
	assert.NotNil(t, converted[1].Content[0].OfText)
	assert.Equal(t, "X", converted[1].Content[0].OfText.Text)
}

func Test_convertMessages_UnknownRoleFallbackUser(t *testing.T) {
	msgs := []model.Message{
		{Role: "unknown", Content: "hello"},
	}
	converted, systemPrompts, err := convertMessages(msgs)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(systemPrompts))
	assert.Equal(t, 1, len(converted))
	assert.True(t, len(converted[0].Content) >= 1)
	assert.NotNil(t, converted[0].Content[0].OfText)
	assert.Equal(t, "hello", converted[0].Content[0].OfText.Text)
}

func Test_convertMessages_AllEmptyDropped(t *testing.T) {
	p := "img"
	msgs := []model.Message{
		{
			Role:    model.RoleUser,
			Content: "",
		},
		{
			Role:    model.RoleUser,
			Content: "",
			ContentParts: []model.ContentPart{
				{
					Type: model.ContentTypeImage,
					Text: nil,
				},
			},
		},
		{
			Role:    model.RoleSystem,
			Content: "",
		},
		{
			Role:    model.RoleSystem,
			Content: "",
			ContentParts: []model.ContentPart{
				{
					Type: model.ContentTypeText,
					Text: &p,
				},
			},
		},
	}
	converted, systemPrompts, err := convertMessages(msgs)
	assert.NoError(t, err)
	// Only system prompts should exist.
	assert.Equal(t, 0, len(converted))
	assert.Equal(t, 1, len(systemPrompts))
	assert.Equal(t, p, systemPrompts[0].Text)
}

func Test_convertAssistantMessageContent_TwoToolCalls(t *testing.T) {
	msg := model.Message{
		Role:    model.RoleAssistant,
		Content: "A",
		ToolCalls: []model.ToolCall{
			{
				ID:   "c1",
				Type: functionToolType,
				Function: model.FunctionDefinitionParam{
					Name:      "f1",
					Arguments: []byte("{}"),
				},
			},
			{
				ID:   "c2",
				Type: functionToolType,
				Function: model.FunctionDefinitionParam{
					Name:      "f2",
					Arguments: []byte("{}"),
				},
			},
		},
	}
	out := convertAssistantMessageContent(msg)
	// 1 head text + 2 tool uses.
	assert.Equal(t, 3, len(out.Content))
	assert.NotNil(t, out.Content[0].OfText)
	assert.NotNil(t, out.Content[1].OfToolUse)
	assert.NotNil(t, out.Content[2].OfToolUse)
}

func Test_convertUserMessage_OnlyTextParts(t *testing.T) {
	a := "A"
	b := "B"
	msg := model.Message{
		Role:    model.RoleUser,
		Content: "",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &a},
			{Type: model.ContentTypeText, Text: &b},
		},
	}
	out := convertUserMessage(msg)
	assert.Equal(t, 2, len(out.Content))
	assert.NotNil(t, out.Content[0].OfText)
	assert.Equal(t, a, out.Content[0].OfText.Text)
	assert.NotNil(t, out.Content[1].OfText)
	assert.Equal(t, b, out.Content[1].OfText.Text)
}

func Test_convertUserMessage_NonTextPartsIgnored(t *testing.T) {
	msg := model.Message{
		Role:    model.RoleUser,
		Content: "",
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeImage},
		},
	}
	out := convertUserMessage(msg)
	assert.Equal(t, 0, len(out.Content))
}

func Test_convertSystemMessageContent_OnlyParts(t *testing.T) {
	a := "sysA"
	b := "sysB"
	msg := model.Message{
		Role:    model.RoleSystem,
		Content: "",
		ContentParts: []model.ContentPart{
			{
				Type: model.ContentTypeText,
				Text: &a,
			},
			{
				Type: model.ContentTypeText,
				Text: &b,
			},
		},
	}
	blocks := convertSystemMessageContent(msg)
	assert.Equal(t, 2, len(blocks))
	assert.Equal(t, a, blocks[0].Text)
	assert.Equal(t, b, blocks[1].Text)
}

func Test_convertSystemMessageContent_Empty(t *testing.T) {
	msg := model.Message{Role: model.RoleSystem}
	blocks := convertSystemMessageContent(msg)
	assert.Equal(t, 0, len(blocks))
}

func Test_convertMessages_ToolClustersSeparated(t *testing.T) {
	msgs := []model.Message{
		{Role: model.RoleTool, Content: "r1", ToolID: "t1"},
		{Role: model.RoleTool, Content: "r2", ToolID: "t2"},
		model.NewAssistantMessage("mid"),
		{Role: model.RoleTool, Content: "r3", ToolID: "t3"},
	}
	converted, systemPrompts, err := convertMessages(msgs)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(systemPrompts))
	assert.Equal(t, 3, len(converted))
	// First: merged t1+t2.
	assert.Equal(t, 2, len(converted[0].Content))
	assert.NotNil(t, converted[0].Content[0].OfToolResult)
	assert.Equal(t, "t1", converted[0].Content[0].OfToolResult.ToolUseID)
	assert.NotNil(t, converted[0].Content[1].OfToolResult)
	assert.Equal(t, "t2", converted[0].Content[1].OfToolResult.ToolUseID)
	// Second: assistant text.
	assert.True(t, len(converted[1].Content) >= 1)
	assert.NotNil(t, converted[1].Content[0].OfText)
	assert.Equal(t, "mid", converted[1].Content[0].OfText.Text)
	// Third: merged single t3.
	assert.Equal(t, 1, len(converted[2].Content))
	assert.NotNil(t, converted[2].Content[0].OfToolResult)
	assert.Equal(t, "t3", converted[2].Content[0].OfToolResult.ToolUseID)
}

func Test_convertContentBlock_AllVariants(t *testing.T) {
	// Text block.
	textJSON := `{"type":"text","text":"hello"}`
	text := anthropicContentUnion("text", textJSON)
	// Thinking block.
	thinkingJSON := `{"type":"thinking","signature":"sig","thinking":"reason"}`
	thinking := anthropicContentUnion("thinking", thinkingJSON)
	// Tool use block.
	toolJSON := `{"type":"tool_use","id":"id1","name":"fn","input":{}}`
	tool := anthropicContentUnion("tool_use", toolJSON)

	out := convertContentBlock([]anthropic.ContentBlockUnion{text, thinking, tool})
	assert.Equal(t, model.RoleAssistant, out.Role)
	assert.Equal(t, "hello", out.Content)
	assert.Equal(t, "reason", out.ReasoningContent)
	assert.Equal(t, 1, len(out.ToolCalls))
	assert.Equal(t, "id1", out.ToolCalls[0].ID)
}

func Test_buildStreamingPartialResponse_TextAndThinkingAndStop(t *testing.T) {
	var acc anthropic.Message
	acc.ID = "acc1"
	acc.Model = anthropic.Model("claude-test")

	// Text delta empty -> skip.
	e1 := anthropicStreamEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":""}}`)
	resp, err := buildStreamingPartialResponse(acc, e1)
	assert.NoError(t, err)
	assert.Nil(t, resp)

	// Text delta non-empty -> content delta.
	e2 := anthropicStreamEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"abc"}}`)
	resp, err = buildStreamingPartialResponse(acc, e2)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "abc", resp.Choices[0].Delta.Content)

	// Thinking delta non-empty -> reasoning delta.
	e3 := anthropicStreamEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think"}}`)
	resp, err = buildStreamingPartialResponse(acc, e3)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, "think", resp.Choices[0].Delta.ReasoningContent)

	// Message delta with stop_reason -> finish reason set.
	e4 := anthropicStreamEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":""},"usage":{"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"input_tokens":0,"output_tokens":0,"server_tool_use":{"web_search_requests":0}}}`)
	resp, err = buildStreamingPartialResponse(acc, e4)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotNil(t, resp.Choices[0].FinishReason)

	// Unknown type should be skipped.
	e5 := anthropicStreamEvent("unknown", `{"type":"unknown"}`)
	resp, err = buildStreamingPartialResponse(acc, e5)
	assert.NoError(t, err)
	assert.Nil(t, resp)

	// Content block delta with input_json_delta should be skipped.
	e6 := anthropicStreamEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{}"}}`)
	resp, err = buildStreamingPartialResponse(acc, e6)
	assert.NoError(t, err)
	assert.Nil(t, resp)

	// Thinking delta empty should be skipped.
	e7 := anthropicStreamEvent("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":""}}`)
	resp, err = buildStreamingPartialResponse(acc, e7)
	assert.NoError(t, err)
	assert.Nil(t, resp)

	// Message delta with empty stop_reason should be skipped.
	e8 := anthropicStreamEvent("message_delta", `{"type":"message_delta","delta":{"stop_reason":"","stop_sequence":""},"usage":{"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"input_tokens":0,"output_tokens":0,"server_tool_use":{"web_search_requests":0}}}`)
	resp, err = buildStreamingPartialResponse(acc, e8)
	assert.NoError(t, err)
	assert.Nil(t, resp)
}

func Test_buildStreamingFinalResponse_Aggregation(t *testing.T) {
	// Tool use + text + thinking accumulate into final assistant message.
	tool := anthropicContentUnion("tool_use", `{"type":"tool_use","id":"id1","name":"fn","input":{}}`)
	text := anthropicContentUnion("text", `{"type":"text","text":"T"}`)
	think := anthropicContentUnion("thinking", `{"type":"thinking","signature":"s","thinking":"R"}`)
	acc := anthropic.Message{Content: []anthropic.ContentBlockUnion{tool, text, think}}
	final := buildStreamingFinalResponse(acc)
	assert.Equal(t, model.ObjectTypeChatCompletion, final.Object)
	assert.Equal(t, 1, len(final.Choices))
	m := final.Choices[0].Message
	assert.Equal(t, "T", m.Content)
	assert.Equal(t, "R", m.ReasoningContent)
	assert.Equal(t, 1, len(m.ToolCalls))
	assert.Equal(t, "id1", m.ToolCalls[0].ID)
}

func Test_buildChatRequest_AllBranchesAndErrors(t *testing.T) {
	m := New("claude-test")
	temp := 0.7
	topP := 0.4
	maxTokens := 16
	thinking := true
	thinkingTokens := 1024
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("sys"),
			model.NewUserMessage("u"),
		},
		GenerationConfig: model.GenerationConfig{
			Temperature:     &temp,
			TopP:            &topP,
			MaxTokens:       &maxTokens,
			Stream:          false,
			ThinkingEnabled: &thinking,
			ThinkingTokens:  &thinkingTokens,
		},
		Tools: map[string]tool.Tool{},
	}
	chatReq, err := m.buildChatRequest(req)
	assert.NoError(t, err)
	assert.Equal(t, anthropic.Model("claude-test"), chatReq.Model)
	assert.True(t, chatReq.Temperature.Valid())
	assert.True(t, chatReq.TopP.Valid())
	assert.Equal(t, int64(maxTokens), chatReq.MaxTokens)
	// Error when no messages are present in conversation.
	req2 := &model.Request{Messages: []model.Message{model.NewSystemMessage("s")}}
	chatReq, err = m.buildChatRequest(req2)
	assert.Error(t, err)
	assert.Nil(t, chatReq)

	// Stop sequences propagate.
	reqStop := &model.Request{
		Messages: []model.Message{model.NewUserMessage("u")},
		GenerationConfig: model.GenerationConfig{
			Stop: []string{"<END>"},
		},
	}
	chatReq, err = m.buildChatRequest(reqStop)
	assert.NoError(t, err)
	assert.True(t, len(chatReq.StopSequences) == 1)
}

func Test_New_WithAPIKeyAndBaseURL(t *testing.T) {
	m := New("claude-test", WithAPIKey("k"), WithBaseURL("http://x"))
	// Internal fields are checked within the same package.
	assert.Equal(t, "k", m.apiKey)
	assert.Equal(t, "http://x", m.baseURL)
}

func Test_buildChatRequest_ThinkingIgnoredWhenTokensNil(t *testing.T) {
	m := New("claude-test")
	thinking := true
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("u")},
		GenerationConfig: model.GenerationConfig{
			ThinkingEnabled: &thinking,
		},
	}
	chatReq, err := m.buildChatRequest(req)
	assert.NoError(t, err)
	// When tokens are nil, thinking should not be set.
	// The SDK union has both enabled/disabled variants omitted by default.
	// We assert nothing and only ensure no error and a valid request.
	_ = chatReq
}

func Test_convertTools_Multiple(t *testing.T) {
	toolsMap := map[string]tool.Tool{
		"t1": stubTool{
			decl: &tool.Declaration{
				Name:        "t1",
				Description: "d1",
				InputSchema: &tool.Schema{Type: "object"},
			},
		},
		"t2": stubTool{
			decl: &tool.Declaration{
				Name:        "t2",
				Description: "d2",
				InputSchema: &tool.Schema{Type: "object"},
			},
		},
	}
	params := convertTools(toolsMap)
	assert.Equal(t, 2, len(params))
}

func Test_sendErrorResponse(t *testing.T) {
	m := New("claude-test")
	ctx := context.Background()
	ch := make(chan *model.Response, 1)
	m.sendErrorResponse(ctx, ch, model.ErrorTypeAPIError, fmt.Errorf("boom"))
	resp := <-ch
	assert.NotNil(t, resp.Error)
	assert.Equal(t, model.ErrorTypeAPIError, resp.Error.Type)
	assert.True(t, resp.Done)
}

// rtFunc is a helper RoundTripper for mocking HTTP responses.
type rtFunc func(*http.Request) (*http.Response, error)

// RoundTrip implements http.RoundTripper.
func (f rtFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func Test_HandleNonStreamingResponse_EndToEnd_NoNetwork(t *testing.T) {
	// Mock HTTP client to return a fixed Anthropic message JSON body.
	orig := model.DefaultNewHTTPClient
	t.Cleanup(func() { model.DefaultNewHTTPClient = orig })
	model.DefaultNewHTTPClient = func(_ ...HTTPClientOption) model.HTTPClient {
		return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			body := `{
                "id":"msg1",
                "model":"claude-3-sonnet",
                "role":"assistant",
                "stop_reason":"end_turn",
                "stop_sequence":"",
                "type":"message",
                "usage":{"cache_creation_input_tokens":1,"cache_read_input_tokens":2,"input_tokens":3,"output_tokens":4,"server_tool_use":{"web_search_requests":0}},
                "content":[{"type":"text","text":"hello"}]
            }`
			h := make(http.Header)
			h.Set("Content-Type", "application/json")
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader(body)),
				Header:     h,
			}, nil
		})}
	}
	// Capture callbacks.
	var calledRequest, calledResponse bool
	m := New(
		"claude-test",
		WithHTTPClientOptions(),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			_ = ctx
			if req != nil {
				calledRequest = true
			}
		}),
		WithChatResponseCallback(func(ctx context.Context, req *anthropic.MessageNewParams, resp *anthropic.Message) {
			_ = ctx
			if req != nil && resp != nil {
				calledResponse = true
			}
		}),
	)
	ctx := context.Background()
	req := &model.Request{Messages: []model.Message{model.NewUserMessage("U")}}
	ch, err := m.GenerateContent(ctx, req)
	assert.NoError(t, err)
	var got *model.Response
	select {
	case got = <-ch:
	case <-ctx.Done():
	}
	// Validate the mapped response.
	assert.NotNil(t, got)
	assert.True(t, got.Done)
	assert.Nil(t, got.Error)
	assert.Equal(t, "hello", got.Choices[0].Message.Content)
	assert.NotNil(t, got.Usage)
	assert.Equal(t, 3, got.Usage.PromptTokens)
	assert.Equal(t, 4, got.Usage.CompletionTokens)
	assert.True(t, calledRequest)
	assert.True(t, calledResponse)
}

func Test_HandleStreamingResponse_EndToEnd_NoNetwork(t *testing.T) {
	// Mock SSE stream with a minimal sequence covering start, text delta, stop.
	sse := strings.Join([]string{
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg_sse_1","type":"message","role":"assistant","model":"claude-3-sonnet","content":[]}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":""},"usage":{"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"input_tokens":0,"output_tokens":0,"server_tool_use":{"web_search_requests":0}}}`,
		"",
		"event: message_stop",
		`data: {"type":"message_stop"}`,
		"",
	}, "\n")

	orig := model.DefaultNewHTTPClient
	t.Cleanup(func() { model.DefaultNewHTTPClient = orig })
	model.DefaultNewHTTPClient = func(_ ...HTTPClientOption) model.HTTPClient {
		return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			h := make(http.Header)
			h.Set("Content-Type", "text/event-stream")
			return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(sse))}, nil
		})}
	}

	var chunkCalled, streamCompleteCalled bool
	m := New(
		"claude-test",
		WithHTTPClientOptions(),
		WithChatChunkCallback(func(_ context.Context, _ *anthropic.MessageNewParams,
			_ *anthropic.MessageStreamEventUnion) {
			chunkCalled = true
		}),
		WithChatStreamCompleteCallback(func(_ context.Context, _ *anthropic.MessageNewParams,
			_ *anthropic.Message, _ error) {
			streamCompleteCalled = true
		}),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("U")},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}
	ctx := context.Background()
	ch, err := m.GenerateContent(ctx, req)
	assert.NoError(t, err)
	// Expect at least one partial and one final response.
	var partials int
	var final *model.Response
	for resp := range ch {
		if resp.Done {
			final = resp
			break
		}
		if resp.IsPartial {
			partials++
		}
	}
	assert.True(t, partials >= 1)
	assert.NotNil(t, final)
	assert.True(t, final.Done)
	assert.True(t, chunkCalled)
	assert.True(t, streamCompleteCalled)
}

func Test_HTTPClientOptions_AndAnthropicClientOptions(t *testing.T) {
	// Use WithHTTPClientOptions to inject custom Transport without overriding DefaultNewHTTPClient.
	// Also call WithHTTPClientName and WithAnthropicClientOptions to cover these paths.
	rt := rtFunc(func(r *http.Request) (*http.Response, error) {
		h := make(http.Header)
		h.Set("Content-Type", "application/json")
		body := `{
            "id":"msg1",
            "model":"claude-3-sonnet",
            "role":"assistant",
            "stop_reason":"end_turn",
            "stop_sequence":"",
            "type":"message",
            "usage":{"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"input_tokens":1,"output_tokens":2,"server_tool_use":{"web_search_requests":0}},
            "content":[{"type":"text","text":"world"}]
        }`
		return &http.Response{StatusCode: 200, Header: h, Body: io.NopCloser(strings.NewReader(body))}, nil
	})

	m := New("claude-test",
		WithHTTPClientOptions(WithHTTPClientName("test-client"), WithHTTPClientTransport(rt)),
		// Exercise client options append path.
		WithAnthropicClientOptions(anthropicopt.WithAPIKey("dummy-key-2")),
	)

	ctx := context.Background()
	req := &model.Request{Messages: []model.Message{model.NewUserMessage("U")}}
	ch, err := m.GenerateContent(ctx, req)
	assert.NoError(t, err)
	resp := <-ch
	assert.NotNil(t, resp)
	assert.Nil(t, resp.Error)
	assert.True(t, resp.Done)
	assert.Equal(t, "world", resp.Choices[0].Message.Content)
}

func Test_HandleNonStreamingResponse_ErrorPath_NoNetwork(t *testing.T) {
	orig := model.DefaultNewHTTPClient
	t.Cleanup(func() { model.DefaultNewHTTPClient = orig })
	model.DefaultNewHTTPClient = func(_ ...HTTPClientOption) model.HTTPClient {
		return &http.Client{Transport: rtFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: 500,
				Body:       io.NopCloser(strings.NewReader("oops")),
				Header:     http.Header{"Content-Type": []string{"text/plain"}},
			}, nil
		})}
	}
	m := New("claude-test", WithHTTPClientOptions())
	ctx := context.Background()
	req := &model.Request{Messages: []model.Message{model.NewUserMessage("U")}}
	ch, err := m.GenerateContent(ctx, req)
	assert.NoError(t, err)
	got := <-ch
	assert.NotNil(t, got)
	assert.NotNil(t, got.Error)
	assert.True(t, got.Done)
}

// anthropicContentUnion creates a ContentBlockUnion with a raw JSON payload.
func anthropicContentUnion(_ string, raw string) anthropic.ContentBlockUnion {
	var u anthropic.ContentBlockUnion
	_ = json.Unmarshal([]byte(raw), &u)
	return u
}

// anthropicStreamEvent creates a MessageStreamEventUnion with a raw JSON payload.
func anthropicStreamEvent(_ string, raw string) anthropic.MessageStreamEventUnion {
	var u anthropic.MessageStreamEventUnion
	_ = json.Unmarshal([]byte(raw), &u)
	return u
}

func Test_New_WithChannelBufferSizeAndRequestOptions(t *testing.T) {
	optSize := 1024
	m := New("claude-test", WithChannelBufferSize(optSize), WithAnthropicRequestOptions())
	assert.Equal(t, optSize, m.channelBufferSize)
	assert.Equal(t, 0, len(m.anthropicRequestOptions))

	// Non-positive size falls back to default.
	m2 := New("claude-test", WithChannelBufferSize(0))
	assert.Equal(t, defaultChannelBufferSize, m2.channelBufferSize)
}

func Test_New_WithRequestOptions_Appends(t *testing.T) {
	m := New("claude-test")
	assert.Equal(t, 0, len(m.anthropicRequestOptions))

	m = New("claude-test",
		WithAnthropicRequestOptions(anthropicopt.WithAPIKey("k1")),
		WithAnthropicRequestOptions(anthropicopt.WithAPIKey("k2")),
	)
	assert.Equal(t, 2, len(m.anthropicRequestOptions))
}

func Test_convertMessages_AssistantWithToolCalls(t *testing.T) {
	msgs := []model.Message{
		model.NewUserMessage("U"),
		{
			Role:    model.RoleAssistant,
			Content: "A",
			ToolCalls: []model.ToolCall{
				{
					ID:   "c1",
					Type: functionToolType,
					Function: model.FunctionDefinitionParam{
						Name:      "f",
						Arguments: []byte("{}"),
					},
				},
			},
		},
		{
			Role:    model.RoleTool,
			Content: "r1",
			ToolID:  "t1",
		},
	}
	converted, systemPrompts, err := convertMessages(msgs)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(systemPrompts))
	assert.Equal(t, 3, len(converted))
	// Assistant should remain a separate message, not merged with neighboring tool results.
	assert.True(t, len(converted[1].Content) >= 1)
	assert.NotNil(t, converted[1].Content[0].OfText)
}

// testStubCounter is a stub TokenCounter for testing token tailoring.
type testStubCounter struct{}

func (testStubCounter) CountTokens(
	ctx context.Context,
	message model.Message,
) (int, error) {
	return 1, nil
}

func (testStubCounter) CountTokensRange(
	ctx context.Context,
	messages []model.Message,
	start,
	end int,
) (int, error) {
	if start < 0 || end > len(messages) || start >= end {
		return 0, fmt.Errorf("invalid range: start=%d, end=%d, len=%d", start, end, len(messages))
	}
	return end - start, nil
}

// testStubStrategy is a stub TailoringStrategy for testing.
type testStubStrategy struct{}

func (testStubStrategy) TailorMessages(
	ctx context.Context,
	messages []model.Message,
	maxTokens int,
) ([]model.Message, error) {
	if len(messages) <= 1 {
		return messages, nil
	}
	// Drop the second message to make tailoring observable.
	return append([]model.Message{messages[0]}, messages[2:]...), nil
}

// TestWithTokenTailoring tests token tailoring functionality in Anthropic.
func TestWithTokenTailoring(t *testing.T) {
	// Capture the built Anthropic request to check messages count reflects tailoring.
	var captured *anthropic.MessageNewParams
	m := New("test-model",
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(100),
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	// Two user messages; strategy will drop the second one.
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("B"),
	}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// After tailoring, messages should be reduced (1 message after tailoring).
	require.Len(t, captured.Messages, 1, "expected 1 message after tailoring, got %d", len(captured.Messages))
}

// TestWithEnableTokenTailoring_SimpleMode tests simple token tailoring mode.
func TestWithEnableTokenTailoring_SimpleMode(t *testing.T) {
	// Capture the built Anthropic request to check messages count reflects tailoring.
	var captured *anthropic.MessageNewParams
	m := New("claude-3-5-sonnet", // Known model with 200000 context window
		WithEnableTokenTailoring(true),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	// Create many messages to trigger tailoring.
	// With claude-3-5-sonnet (contextWindow=200000), maxInputTokens=130000 (65% ratio).
	messages := []model.Message{model.NewSystemMessage("You are a helpful assistant.")}
	for i := 0; i < 500; i++ {
		messages = append(messages, model.NewUserMessage(fmt.Sprintf("Message %d: %s", i, strings.Repeat("lorem ipsum ", 100))))
	}

	req := &model.Request{Messages: messages}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// After tailoring, messages should be reduced.
	require.Less(t, len(captured.Messages), len(messages), "expected messages to be tailored, got %d (original: %d)", len(captured.Messages), len(messages))
}

// TestWithEnableTokenTailoring_AdvancedMode tests advanced token tailoring mode.
func TestWithEnableTokenTailoring_AdvancedMode(t *testing.T) {
	// Capture the built Anthropic request to check messages count reflects tailoring.
	var captured *anthropic.MessageNewParams
	m := New("claude-3-5-sonnet",
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(1000), // Custom max input tokens
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	// Two user messages; strategy will drop the second one.
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("B"),
	}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// After tailoring, messages should be reduced to 1 message.
	require.Len(t, captured.Messages, 1, "expected 1 message after tailoring, got %d", len(captured.Messages))
}

// TestWithEnableTokenTailoring_Disabled tests disabled token tailoring.
func TestWithEnableTokenTailoring_Disabled(t *testing.T) {
	// Capture the built Anthropic request to check messages count reflects tailoring.
	var captured *anthropic.MessageNewParams
	m := New("claude-3-5-sonnet",
		WithEnableTokenTailoring(false),
		WithMaxInputTokens(100), // This should be ignored when tailoring is disabled
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	// Two user messages; should NOT be tailored when disabled.
	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("B"),
	}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// When tailoring disabled, messages should be unchanged (2 messages).
	require.Len(t, captured.Messages, 2, "expected 2 messages when tailoring disabled, got %d", len(captured.Messages))
}

// TestWithEnableTokenTailoring_UnknownModel tests tailoring with unknown model.
func TestWithEnableTokenTailoring_UnknownModel(t *testing.T) {
	// Capture the built Anthropic request.
	var captured *anthropic.MessageNewParams
	m := New("unknown-model-xyz", // Unknown model should fallback to default context window
		WithEnableTokenTailoring(true),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	// Create many messages to trigger tailoring.
	messages := []model.Message{model.NewSystemMessage("You are a helpful assistant.")}
	for i := 0; i < 50; i++ {
		messages = append(messages, model.NewUserMessage(fmt.Sprintf("Message %d: %s", i, strings.Repeat("lorem ipsum ", 50))))
	}

	req := &model.Request{Messages: messages}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// Messages should be tailored even with unknown model (using fallback context window).
	require.Less(t, len(captured.Messages), len(messages), "expected messages to be tailored with unknown model, got %d (original: %d)", len(captured.Messages), len(messages))
}

// TestWithEnableTokenTailoring_SafetyMarginAndRatioLimit tests safety margin and ratio limit.
func TestWithEnableTokenTailoring_SafetyMarginAndRatioLimit(t *testing.T) {
	// Capture the built Anthropic request.
	var captured *anthropic.MessageNewParams
	m := New("deepseek-chat", // Known model with 131072 context window
		WithEnableTokenTailoring(true),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	// Create many messages to trigger aggressive tailoring.
	messages := []model.Message{model.NewSystemMessage("You are a helpful assistant.")}
	for i := 0; i < 1200; i++ {
		messages = append(messages, model.NewUserMessage(fmt.Sprintf("Message %d: %s", i, strings.Repeat("lorem ipsum ", 40))))
	}

	req := &model.Request{Messages: messages}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	// Drain once to trigger request path; may error due to no API key.
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// After tailoring, messages should be significantly reduced.
	require.Less(t, len(captured.Messages), len(messages), "expected messages to be tailored, got %d (original: %d)", len(captured.Messages), len(messages))
	// With 100% ratio limit and safety margin (10%), protocol overhead (512), reserve output (2048),
	// we expect roughly 88-90% of the original messages to be kept.
	require.GreaterOrEqual(t, len(captured.Messages), int(float64(len(messages))*0.70), "expected at least 70%% messages to be kept, got %d (original: %d)", len(captured.Messages), len(messages))
	require.LessOrEqual(t, len(captured.Messages), int(float64(len(messages))*0.95), "expected at most 95%% messages to be kept due to safety margin, got %d (original: %d)", len(captured.Messages), len(messages))
}

// errorStrategy always returns error for testing error paths.
type errorStrategy struct{}

func (errorStrategy) TailorMessages(
	ctx context.Context,
	messages []model.Message,
	maxTokens int,
) ([]model.Message, error) {
	return nil, fmt.Errorf("tailoring error")
}

// TestWithEnableTokenTailoring_ErrorInTailoring tests error handling in tailoring.
func TestWithEnableTokenTailoring_ErrorInTailoring(t *testing.T) {
	var captured *anthropic.MessageNewParams
	m := New("claude-3-5-sonnet",
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(100),
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(errorStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("B"),
	}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// When tailoring fails, original messages should be preserved.
	require.Len(t, captured.Messages, 2, "expected original messages when tailoring fails, got %d", len(captured.Messages))
}

// errorCounter always returns error for testing error paths.
type errorCounter struct{}

func (errorCounter) CountTokens(
	ctx context.Context,
	message model.Message,
) (int, error) {
	return 0, fmt.Errorf("count error")
}

func (errorCounter) CountTokensRange(
	ctx context.Context,
	messages []model.Message,
	start,
	end int,
) (int, error) {
	return 0, fmt.Errorf("count range error")
}

// TestWithEnableTokenTailoring_ErrorInCountTokens tests error handling in token counting.
func TestWithEnableTokenTailoring_ErrorInCountTokens(t *testing.T) {
	var captured *anthropic.MessageNewParams
	m := New("claude-3-5-sonnet",
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(100),
		WithTokenCounter(errorCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("B"),
	}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// Tailoring succeeds but token counting fails, messages should be tailored.
	require.Len(t, captured.Messages, 1, "expected tailored messages even when token counting fails, got %d", len(captured.Messages))
	// MaxTokens should not be set when token counting fails.
	require.Equal(t, int64(0), captured.MaxTokens, "expected MaxTokens to be 0 when token counting fails")
}

// zeroTokenCounter always returns 0 for testing edge cases.
type zeroTokenCounter struct{}

func (zeroTokenCounter) CountTokens(
	ctx context.Context,
	message model.Message,
) (int, error) {
	return 0, nil
}

func (zeroTokenCounter) CountTokensRange(
	ctx context.Context,
	messages []model.Message,
	start,
	end int,
) (int, error) {
	return 0, nil
}

// TestWithEnableTokenTailoring_RemainingTokensNegative tests remainingTokens <= 0 case.
func TestWithEnableTokenTailoring_RemainingTokensNegative(t *testing.T) {
	var captured *anthropic.MessageNewParams
	m := New("claude-3-5-sonnet",
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(1), // Very small limit to trigger negative remaining tokens.
		WithTokenCounter(&zeroTokenCounter{}),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("B"),
	}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// With maxInputTokens=1 and usedTokens=0 (zeroTokenCounter), remaining tokens
	// should be positive, so MaxTokens should be set to at least the floor (256).
	// The actual calculation: contextWindow - 0 - 512 - safetyMargin
	// For claude-3-5-sonnet (200000): 200000 - 0 - 512 - 20000 = 179488
	// So MaxTokens should be max(179488, 256) = 179488
	require.Greater(t, captured.MaxTokens, int64(0), "expected MaxTokens to be set when sufficient tokens available")
}

// TestWithEnableTokenTailoring_AutoSetMaxTokens tests automatic MaxTokens setting.
func TestWithEnableTokenTailoring_AutoSetMaxTokens(t *testing.T) {
	var captured *anthropic.MessageNewParams
	m := New("claude-3-5-sonnet",
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(10000),
		WithTokenCounter(zeroTokenCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	req := &model.Request{Messages: []model.Message{
		model.NewUserMessage("A"),
		model.NewUserMessage("B"),
	}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// MaxTokens should be auto-set when not specified by user.
	require.Greater(t, captured.MaxTokens, int64(0), "expected MaxTokens > 0")
}

// TestWithEnableTokenTailoring_UserSpecifiedMaxTokens tests user-specified MaxTokens is preserved.
func TestWithEnableTokenTailoring_UserSpecifiedMaxTokens(t *testing.T) {
	var captured *anthropic.MessageNewParams
	m := New("claude-3-5-sonnet",
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(10000),
		WithTokenCounter(zeroTokenCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	userMaxTokens := 2048
	req := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("A"),
			model.NewUserMessage("B"),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &userMaxTokens,
		},
	}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// User-specified MaxTokens should be preserved.
	require.Equal(t, int64(userMaxTokens), captured.MaxTokens, "expected user-specified MaxTokens to be preserved")
}

// TestWithEnableTokenTailoring_LazyInitialization tests lazy initialization of counter and strategy.
func TestWithEnableTokenTailoring_LazyInitialization(t *testing.T) {
	var captured *anthropic.MessageNewParams
	m := New("claude-3-5-sonnet",
		WithEnableTokenTailoring(true),
		// No counter or strategy provided, should lazy-initialize.
		WithChatRequestCallback(func(ctx context.Context, req *anthropic.MessageNewParams) {
			captured = req
		}),
	)

	messages := []model.Message{model.NewSystemMessage("System")}
	for i := 0; i < 100; i++ {
		messages = append(messages, model.NewUserMessage(fmt.Sprintf("Message %d: %s", i, strings.Repeat("lorem ipsum ", 20))))
	}

	req := &model.Request{Messages: messages}

	ch, err := m.GenerateContent(context.Background(), req)
	require.NoError(t, err, "GenerateContent: %v", err)
	select {
	case <-ch:
	case <-time.After(100 * time.Millisecond):
	}

	require.NotNil(t, captured, "expected request callback to capture request")
	// Should have tailored messages using lazy-initialized counter/strategy.
	require.Less(t, len(captured.Messages), len(messages), "expected messages to be tailored with lazy-initialized components, got %d (original: %d)", len(captured.Messages), len(messages))
}

// TestWithEnableTokenTailoring_EmptyMessages tests empty messages edge case.
func TestWithEnableTokenTailoring_EmptyMessages(t *testing.T) {
	m := New("claude-3-5-sonnet",
		WithEnableTokenTailoring(true),
		WithMaxInputTokens(100),
		WithTokenCounter(testStubCounter{}),
		WithTailoringStrategy(testStubStrategy{}),
	)

	req := &model.Request{Messages: []model.Message{}}

	ch, err := m.GenerateContent(context.Background(), req)
	require.Error(t, err, "GenerateContent should fail with empty messages")
	require.Nil(t, ch, "expected nil channel with empty messages")
}
