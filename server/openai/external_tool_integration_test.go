//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	trunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	integrationToolName    = "client_search"
	integrationToolCallID  = "call-1"
	integrationSessionID   = "thread-integration"
	integrationUserMessage = "search go"
	integrationToolResult  = `{"items":["go docs"]}`
)

type recordedModelRequest struct {
	messages  []model.Message
	toolNames []string
}

type recordingModel struct {
	responses []*model.Response

	mu       sync.Mutex
	requests []recordedModelRequest
	nextIdx  int
}

func (m *recordingModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if req != nil {
		toolNames := make([]string, 0, len(req.Tools))
		for name := range req.Tools {
			toolNames = append(toolNames, name)
		}
		sort.Strings(toolNames)
		m.requests = append(m.requests, recordedModelRequest{
			messages:  append([]model.Message(nil), req.Messages...),
			toolNames: toolNames,
		})
	}

	if m.nextIdx >= len(m.responses) {
		return nil, fmt.Errorf("unexpected model call %d", m.nextIdx)
	}
	resp := m.responses[m.nextIdx]
	m.nextIdx++

	ch := make(chan *model.Response, 1)
	ch <- resp
	close(ch)
	return ch, nil
}

func (m *recordingModel) Info() model.Info {
	return model.Info{Name: "recording-model"}
}

func (m *recordingModel) Requests() []recordedModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]recordedModelRequest(nil), m.requests...)
}

func nonSystemMessages(messages []model.Message) []model.Message {
	filtered := make([]model.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == model.RoleSystem {
			continue
		}
		filtered = append(filtered, msg)
	}
	return filtered
}

func TestOpenAIExternalToolFlowThroughRealRunner(t *testing.T) {
	toolCallFinishReason := finishReasonToolCalls
	modelStub := &recordingModel{
		responses: []*model.Response{
			{
				Done: true,
				Choices: []model.Choice{
					{
						Message: model.Message{
							Role: model.RoleAssistant,
							ToolCalls: []model.ToolCall{
								{
									ID:   integrationToolCallID,
									Type: "function",
									Function: model.FunctionDefinitionParam{
										Name:      integrationToolName,
										Arguments: []byte(`{"query":"go"}`),
									},
								},
							},
						},
						FinishReason: &toolCallFinishReason,
					},
				},
			},
			{
				Done: true,
				Choices: []model.Choice{
					{
						Message: model.NewAssistantMessage("final answer"),
					},
				},
			},
		},
	}

	const appName = "openai-integration-app"
	sessionService := inmemory.NewSessionService()
	ag := llmagent.New("integration-agent", llmagent.WithModel(modelStub))
	baseRunner := trunner.NewRunner(
		appName,
		ag,
		trunner.WithSessionService(sessionService),
	)
	t.Cleanup(func() {
		require.NoError(t, baseRunner.Close())
	})

	s, err := New(
		WithRunner(baseRunner),
		WithAppName(appName),
		WithSessionService(sessionService),
	)
	require.NoError(t, err)

	round1Body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "user", Content: integrationUserMessage},
		},
		Tools: []openAITool{
			{
				Type: "function",
				Function: openAIFunction{
					Name:        integrationToolName,
					Description: "Search a frontend-owned source.",
					Parameters:  json.RawMessage(`{"type":"object"}`),
				},
			},
		},
	})
	require.NoError(t, err)

	round1Req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(round1Body),
	)
	round1Req.Header.Set(headerSessionID, integrationSessionID)
	round1W := httptest.NewRecorder()
	s.handleChatCompletions(round1W, round1Req)

	require.Equal(t, http.StatusOK, round1W.Code)
	var round1Resp openAIResponse
	require.NoError(t, json.NewDecoder(round1W.Body).Decode(&round1Resp))
	require.NotNil(t, round1Resp.Choices[0].FinishReason)
	assert.Equal(t, finishReasonToolCalls, *round1Resp.Choices[0].FinishReason)
	require.Len(t, round1Resp.Choices[0].Message.ToolCalls, 1)
	assert.Equal(t, integrationToolCallID, round1Resp.Choices[0].Message.ToolCalls[0].ID)

	round1Requests := modelStub.Requests()
	require.Len(t, round1Requests, 1)
	assert.Equal(t, []string{integrationToolName}, round1Requests[0].toolNames)
	round1Messages := nonSystemMessages(round1Requests[0].messages)
	require.Len(t, round1Messages, 1)
	assert.Equal(t, integrationUserMessage, round1Messages[0].Content)

	round2Body, err := json.Marshal(openAIRequest{
		Model: "gpt-3.5-turbo",
		Messages: []openAIMessage{
			{Role: "user", Content: integrationUserMessage},
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []openAIToolCall{
					{
						ID:   integrationToolCallID,
						Type: "function",
						Function: openAIToolCallFunction{
							Name:      integrationToolName,
							Arguments: `{"query":"go"}`,
						},
					},
				},
			},
			{
				Role:       "tool",
				ToolCallID: integrationToolCallID,
				Name:       integrationToolName,
				Content:    integrationToolResult,
			},
		},
	})
	require.NoError(t, err)

	round2Req := httptest.NewRequest(
		http.MethodPost,
		"/v1/chat/completions",
		bytes.NewReader(round2Body),
	)
	round2Req.Header.Set(headerSessionID, integrationSessionID)
	round2W := httptest.NewRecorder()
	s.handleChatCompletions(round2W, round2Req)

	require.Equal(t, http.StatusOK, round2W.Code)
	var round2Resp openAIResponse
	require.NoError(t, json.NewDecoder(round2W.Body).Decode(&round2Resp))
	assert.Equal(t, "final answer", round2Resp.Choices[0].Message.Content)

	round2Requests := modelStub.Requests()
	require.Len(t, round2Requests, 2)
	round2Messages := nonSystemMessages(round2Requests[1].messages)
	require.Len(t, round2Messages, 3)
	assert.Equal(t, model.RoleUser, round2Messages[0].Role)
	assert.Equal(t, integrationUserMessage, round2Messages[0].Content)
	assert.Equal(t, model.RoleAssistant, round2Messages[1].Role)
	require.Len(t, round2Messages[1].ToolCalls, 1)
	assert.Equal(t, integrationToolCallID, round2Messages[1].ToolCalls[0].ID)
	assert.Equal(t, model.RoleTool, round2Messages[2].Role)
	assert.Equal(t, integrationToolResult, round2Messages[2].Content)
	assert.Equal(t, integrationToolCallID, round2Messages[2].ToolID)

	sess, err := sessionService.GetSession(
		context.Background(),
		session.Key{
			AppName:   appName,
			UserID:    defaultUserID,
			SessionID: integrationSessionID,
		},
	)
	require.NoError(t, err)
	require.NotNil(t, sess)
	require.GreaterOrEqual(t, len(sess.Events), 4)
}
