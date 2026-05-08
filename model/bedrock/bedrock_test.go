//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ============================================================================
// Mock / Stub Types
// ============================================================================

// stubTool implements the tool.Tool interface for testing.
type stubTool struct{ decl *tool.Declaration }

func (s stubTool) Call(_ context.Context, _ []byte) (any, error) { return nil, nil }
func (s stubTool) Declaration() *tool.Declaration                { return s.decl }

// mockBedrockClient implements the BedrockClient interface for unit testing.
type mockBedrockClient struct {
	converseFunc       func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
	converseStreamFunc func(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

func (m *mockBedrockClient) Converse(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	if m.converseFunc != nil {
		return m.converseFunc(ctx, params, optFns...)
	}
	return nil, errors.New("converse not implemented")
}

func (m *mockBedrockClient) ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
	if m.converseStreamFunc != nil {
		return m.converseStreamFunc(ctx, params, optFns...)
	}
	return nil, errors.New("converseStream not implemented")
}

// mockEventStreamReader mocks the ConverseStreamOutputReader interface.
type mockEventStreamReader struct {
	events []types.ConverseStreamOutput
	ch     chan types.ConverseStreamOutput
	once   sync.Once
	err    error
}

func newMockEventStreamReader(events []types.ConverseStreamOutput) *mockEventStreamReader {
	ch := make(chan types.ConverseStreamOutput, len(events))
	for _, e := range events {
		ch <- e
	}
	close(ch)
	return &mockEventStreamReader{events: events, ch: ch}
}

func (m *mockEventStreamReader) Events() <-chan types.ConverseStreamOutput {
	return m.ch
}

func (m *mockEventStreamReader) Close() error { return nil }
func (m *mockEventStreamReader) Err() error   { return m.err }

// ============================================================================
// Basic Tests
// ============================================================================

func TestModel_Info(t *testing.T) {
	m := &Model{modelID: "mistral.mistral-large-3-675b-instruct"}
	info := m.Info()
	assert.Equal(t, "mistral.mistral-large-3-675b-instruct", info.Name)
}

func TestNew_WithClient(t *testing.T) {
	mock := &mockBedrockClient{}
	m := New("test-model", WithClient(mock), WithChannelBufferSize(128))
	assert.Equal(t, "test-model", m.modelID)
	assert.Equal(t, 128, m.channelBufferSize)
	assert.Equal(t, mock, m.client)
}

func TestNew_DefaultChannelBufferSize(t *testing.T) {
	mock := &mockBedrockClient{}
	m := New("test-model", WithClient(mock))
	assert.Equal(t, defaultChannelBufferSize, m.channelBufferSize)
}

func TestWithChannelBufferSize_InvalidValue(t *testing.T) {
	mock := &mockBedrockClient{}
	m := New("test-model", WithClient(mock), WithChannelBufferSize(-1))
	assert.Equal(t, defaultChannelBufferSize, m.channelBufferSize)
}

func TestGenerateContent_NilRequest(t *testing.T) {
	mock := &mockBedrockClient{}
	m := New("test-model", WithClient(mock))
	ch, err := m.GenerateContent(context.Background(), nil)
	assert.Nil(t, ch)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "request cannot be nil")
}

// ============================================================================
// Non-Streaming Conversation Tests
// ============================================================================

func TestGenerateContent_NonStreaming_SimpleText(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			// Verify request parameters
			assert.Equal(t, "test-model", aws.ToString(params.ModelId))
			assert.Len(t, params.Messages, 1)
			assert.Equal(t, types.ConversationRoleUser, params.Messages[0].Role)

			return &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonEndTurn,
				Output: &types.ConverseOutputMemberMessage{
					Value: types.Message{
						Role: types.ConversationRoleAssistant,
						Content: []types.ContentBlock{
							&types.ContentBlockMemberText{Value: "Hello! How can I help you?"},
						},
					},
				},
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(10),
					OutputTokens: aws.Int32(8),
					TotalTokens:  aws.Int32(18),
				},
			}, nil
		},
	}

	m := New("test-model", WithClient(mock))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Hi"),
		},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}

	require.Len(t, responses, 1)
	resp := responses[0]
	assert.True(t, resp.Done)
	assert.Equal(t, model.ObjectTypeChatCompletion, resp.Object)
	assert.Equal(t, "test-model", resp.Model)
	require.Len(t, resp.Choices, 1)
	assert.Equal(t, "Hello! How can I help you?", resp.Choices[0].Message.Content)
	assert.Equal(t, model.RoleAssistant, resp.Choices[0].Message.Role)
	assert.NotNil(t, resp.Choices[0].FinishReason)
	assert.Equal(t, "end_turn", *resp.Choices[0].FinishReason)

	// Verify usage
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 10, resp.Usage.PromptTokens)
	assert.Equal(t, 8, resp.Usage.CompletionTokens)
	assert.Equal(t, 18, resp.Usage.TotalTokens)
}

func TestGenerateContent_NonStreaming_WithSystemMessage(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			// Verify system messages are correctly extracted
			require.NotNil(t, params.System)
			require.Len(t, params.System, 1)
			sysBlock, ok := params.System[0].(*types.SystemContentBlockMemberText)
			require.True(t, ok)
			assert.Equal(t, "You are a helpful assistant.", sysBlock.Value)

			// Verify user messages
			require.Len(t, params.Messages, 1)

			return &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonEndTurn,
				Output: &types.ConverseOutputMemberMessage{
					Value: types.Message{
						Role: types.ConversationRoleAssistant,
						Content: []types.ContentBlock{
							&types.ContentBlockMemberText{Value: "I'm here to help!"},
						},
					},
				},
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(20),
					OutputTokens: aws.Int32(5),
					TotalTokens:  aws.Int32(25),
				},
			}, nil
		},
	}

	m := New("test-model", WithClient(mock))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a helpful assistant."),
			model.NewUserMessage("Hello"),
		},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}
	require.Len(t, responses, 1)
	assert.Equal(t, "I'm here to help!", responses[0].Choices[0].Message.Content)
}

func TestGenerateContent_NonStreaming_APIError(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			return nil, errors.New("throttling exception: rate limit exceeded")
		},
	}

	m := New("test-model", WithClient(mock))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("Hi")},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}
	require.Len(t, responses, 1)
	assert.NotNil(t, responses[0].Error)
	assert.Equal(t, model.ErrorTypeAPIError, responses[0].Error.Type)
	assert.Contains(t, responses[0].Error.Message, "throttling exception")
}

func TestGenerateContent_NonStreaming_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			return nil, ctx.Err()
		},
	}

	m := New("test-model", WithClient(mock))
	ch, err := m.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{model.NewUserMessage("Hi")},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}
	// A pre-cancelled context should produce exactly one error response with ErrorTypeCancelled
	require.Len(t, responses, 1)
	require.NotNil(t, responses[0].Error)
	assert.Equal(t, model.ErrorTypeCancelled, responses[0].Error.Type)
}

// ============================================================================
// Non-Streaming Tool Call Tests
// ============================================================================

func TestGenerateContent_NonStreaming_ToolCall(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			// Verify tool configuration
			require.NotNil(t, params.ToolConfig)
			require.Len(t, params.ToolConfig.Tools, 1)
			toolSpec, ok := params.ToolConfig.Tools[0].(*types.ToolMemberToolSpec)
			require.True(t, ok)
			assert.Equal(t, "get_weather", aws.ToString(toolSpec.Value.Name))

			return &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonToolUse,
				Output: &types.ConverseOutputMemberMessage{
					Value: types.Message{
						Role: types.ConversationRoleAssistant,
						Content: []types.ContentBlock{
							&types.ContentBlockMemberToolUse{
								Value: types.ToolUseBlock{
									ToolUseId: aws.String("tool_001"),
									Name:      aws.String("get_weather"),
									Input:     document.NewLazyDocument(map[string]any{"city": "Beijing"}),
								},
							},
						},
					},
				},
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(50),
					OutputTokens: aws.Int32(30),
					TotalTokens:  aws.Int32(80),
				},
			}, nil
		},
	}

	m := New("test-model", WithClient(mock))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("What's the weather in Beijing?"),
		},
		Tools: map[string]tool.Tool{
			"get_weather": stubTool{decl: &tool.Declaration{
				Name:        "get_weather",
				Description: "Get weather information for a city",
				InputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"city": {Type: "string", Description: "City name"},
					},
					Required: []string{"city"},
				},
			}},
		},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}

	require.Len(t, responses, 1)
	resp := responses[0]
	assert.Equal(t, "tool_use", *resp.Choices[0].FinishReason)
	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)

	tc := resp.Choices[0].Message.ToolCalls[0]
	assert.Equal(t, "tool_001", tc.ID)
	assert.Equal(t, "get_weather", tc.Function.Name)
	assert.Equal(t, functionToolType, tc.Type)

	// Verify arguments
	var args map[string]any
	err = json.Unmarshal(tc.Function.Arguments, &args)
	require.NoError(t, err)
	assert.Equal(t, "Beijing", args["city"])
}

func TestGenerateContent_NonStreaming_ToolResult(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			// Verify tool result messages are correctly converted
			// Tool results should be sent as ToolResult blocks with user role
			found := false
			for _, msg := range params.Messages {
				for _, block := range msg.Content {
					if tr, ok := block.(*types.ContentBlockMemberToolResult); ok {
						assert.Equal(t, "tool_001", aws.ToString(tr.Value.ToolUseId))
						found = true
					}
				}
			}
			assert.True(t, found, "tool result block should be present")

			return &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonEndTurn,
				Output: &types.ConverseOutputMemberMessage{
					Value: types.Message{
						Role: types.ConversationRoleAssistant,
						Content: []types.ContentBlock{
							&types.ContentBlockMemberText{Value: "The weather in Beijing is sunny, 25°C."},
						},
					},
				},
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(80),
					OutputTokens: aws.Int32(15),
					TotalTokens:  aws.Int32(95),
				},
			}, nil
		},
	}

	m := New("test-model", WithClient(mock))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("What's the weather in Beijing?"),
			{
				Role:    model.RoleAssistant,
				Content: "",
				ToolCalls: []model.ToolCall{
					{
						Type: functionToolType,
						ID:   "tool_001",
						Function: model.FunctionDefinitionParam{
							Name:      "get_weather",
							Arguments: []byte(`{"city":"Beijing"}`),
						},
					},
				},
			},
			model.NewToolMessage("tool_001", "get_weather", `{"temperature": "25°C", "condition": "sunny"}`),
		},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}

	require.Len(t, responses, 1)
	assert.Equal(t, "The weather in Beijing is sunny, 25°C.", responses[0].Choices[0].Message.Content)
}

// ============================================================================
// Streaming Conversation Tests
// ============================================================================

func TestGenerateContent_Streaming_SimpleText(t *testing.T) {
	events := []types.ConverseStreamOutput{
		&types.ConverseStreamOutputMemberMessageStart{
			Value: types.MessageStartEvent{Role: types.ConversationRoleAssistant},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: aws.Int32(0),
				Delta:             &types.ContentBlockDeltaMemberText{Value: "Hello"},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: aws.Int32(0),
				Delta:             &types.ContentBlockDeltaMemberText{Value: " World!"},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockStop{
			Value: types.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn},
		},
		&types.ConverseStreamOutputMemberMetadata{
			Value: types.ConverseStreamMetadataEvent{
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(5),
					OutputTokens: aws.Int32(3),
					TotalTokens:  aws.Int32(8),
				},
			},
		},
	}

	reader := newMockEventStreamReader(events)
	m := &Model{modelID: "test-model", channelBufferSize: 256}

	// Use production code processStreamEvents to handle stream events
	responseChan := make(chan *model.Response, 256)
	go func() {
		defer close(responseChan)
		m.processStreamEvents(context.Background(), reader, responseChan)
	}()

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// Should have 2 deltas + 1 final response (with usage)
	require.Len(t, responses, 3)

	// Verify delta responses
	assert.True(t, responses[0].IsPartial)
	assert.Equal(t, "Hello", responses[0].Choices[0].Delta.Content)
	assert.True(t, responses[1].IsPartial)
	assert.Equal(t, " World!", responses[1].Choices[0].Delta.Content)

	// Verify final response (usage merged into finalResponse)
	assert.True(t, responses[2].Done)
	assert.Equal(t, "Hello World!", responses[2].Choices[0].Message.Content)
	assert.Equal(t, "end_turn", *responses[2].Choices[0].FinishReason)
	assert.NotNil(t, responses[2].Usage)
	assert.Equal(t, 5, responses[2].Usage.PromptTokens)
	assert.Equal(t, 3, responses[2].Usage.CompletionTokens)
	assert.Equal(t, 8, responses[2].Usage.TotalTokens)
}

func TestGenerateContent_Streaming_ToolCall(t *testing.T) {
	events := []types.ConverseStreamOutput{
		&types.ConverseStreamOutputMemberMessageStart{
			Value: types.MessageStartEvent{Role: types.ConversationRoleAssistant},
		},
		&types.ConverseStreamOutputMemberContentBlockStart{
			Value: types.ContentBlockStartEvent{
				ContentBlockIndex: aws.Int32(0),
				Start: &types.ContentBlockStartMemberToolUse{
					Value: types.ToolUseBlockStart{
						ToolUseId: aws.String("tool_stream_001"),
						Name:      aws.String("get_weather"),
					},
				},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: aws.Int32(0),
				Delta: &types.ContentBlockDeltaMemberToolUse{
					Value: types.ToolUseBlockDelta{Input: aws.String(`{"city":`)},
				},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: aws.Int32(0),
				Delta: &types.ContentBlockDeltaMemberToolUse{
					Value: types.ToolUseBlockDelta{Input: aws.String(`"Beijing"}`)},
				},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockStop{
			Value: types.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{StopReason: types.StopReasonToolUse},
		},
	}

	reader := newMockEventStreamReader(events)
	m := &Model{modelID: "test-model", channelBufferSize: 256}

	// Use production code processStreamEvents to handle stream events
	responseChan := make(chan *model.Response, 256)
	go func() {
		defer close(responseChan)
		m.processStreamEvents(context.Background(), reader, responseChan)
	}()

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// Should have 1 final response (with tool calls)
	require.Len(t, responses, 1)

	finalResponse := responses[0]
	require.NotNil(t, finalResponse)
	assert.True(t, finalResponse.Done)
	assert.Equal(t, "tool_use", *finalResponse.Choices[0].FinishReason)
	require.Len(t, finalResponse.Choices[0].Message.ToolCalls, 1)

	tc := finalResponse.Choices[0].Message.ToolCalls[0]
	assert.Equal(t, "tool_stream_001", tc.ID)
	assert.Equal(t, "get_weather", tc.Function.Name)
	assert.Equal(t, `{"city":"Beijing"}`, string(tc.Function.Arguments))
}

func TestGenerateContent_Streaming_APIError(t *testing.T) {
	mock := &mockBedrockClient{
		converseStreamFunc: func(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error) {
			return nil, errors.New("service unavailable")
		},
	}

	m := New("test-model", WithClient(mock))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("Hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}
	require.Len(t, responses, 1)
	assert.NotNil(t, responses[0].Error)
	assert.Equal(t, model.ErrorTypeAPIError, responses[0].Error.Type)
	assert.Contains(t, responses[0].Error.Message, "service unavailable")
}

// ============================================================================
// Message Conversion Tests
// ============================================================================

func TestConvertMessages_AllRoles(t *testing.T) {
	messages := []model.Message{
		model.NewSystemMessage("Be helpful"),
		model.NewUserMessage("Hello"),
		model.NewAssistantMessage("Hi there"),
		model.NewUserMessage("How are you?"),
	}

	bedrockMsgs, systemBlocks, err := convertMessages(messages)
	require.NoError(t, err)

	// System messages should be extracted to systemBlocks
	require.Len(t, systemBlocks, 1)
	sysBlock, ok := systemBlocks[0].(*types.SystemContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "Be helpful", sysBlock.Value)

	// Remaining messages should alternate
	require.Len(t, bedrockMsgs, 3)
	assert.Equal(t, types.ConversationRoleUser, bedrockMsgs[0].Role)
	assert.Equal(t, types.ConversationRoleAssistant, bedrockMsgs[1].Role)
	assert.Equal(t, types.ConversationRoleUser, bedrockMsgs[2].Role)
}

func TestConvertMessages_ToolMessages(t *testing.T) {
	messages := []model.Message{
		model.NewUserMessage("What's the weather?"),
		{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{
					Type: functionToolType,
					ID:   "tc_001",
					Function: model.FunctionDefinitionParam{
						Name:      "get_weather",
						Arguments: []byte(`{"city":"Shanghai"}`),
					},
				},
			},
		},
		model.NewToolMessage("tc_001", "get_weather", `{"temp":"30°C"}`),
	}

	bedrockMsgs, _, err := convertMessages(messages)
	require.NoError(t, err)

	// user -> assistant -> user(tool_result), should be 3 after merge
	// tool result has user role, won't merge with the preceding assistant
	require.Len(t, bedrockMsgs, 3)

	// Verify the last message is a tool result
	lastMsg := bedrockMsgs[2]
	assert.Equal(t, types.ConversationRoleUser, lastMsg.Role)
	require.Len(t, lastMsg.Content, 1)
	toolResult, ok := lastMsg.Content[0].(*types.ContentBlockMemberToolResult)
	require.True(t, ok)
	assert.Equal(t, "tc_001", aws.ToString(toolResult.Value.ToolUseId))
}

func TestMergeConsecutiveMessages(t *testing.T) {
	messages := []types.Message{
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: "msg1"},
		}},
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: "msg2"},
		}},
		{Role: types.ConversationRoleAssistant, Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: "reply"},
		}},
	}

	merged := mergeConsecutiveMessages(messages)
	require.Len(t, merged, 2)
	assert.Equal(t, types.ConversationRoleUser, merged[0].Role)
	assert.Len(t, merged[0].Content, 2) // Two user messages merged
	assert.Equal(t, types.ConversationRoleAssistant, merged[1].Role)
}

func TestMergeConsecutiveMessages_Empty(t *testing.T) {
	merged := mergeConsecutiveMessages(nil)
	assert.Nil(t, merged)
}

func TestMergeConsecutiveMessages_Single(t *testing.T) {
	messages := []types.Message{
		{Role: types.ConversationRoleUser, Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: "hello"},
		}},
	}
	merged := mergeConsecutiveMessages(messages)
	require.Len(t, merged, 1)
}

// ============================================================================
// Inference Configuration Tests
// ============================================================================

func TestBuildInferenceConfig_AllFields(t *testing.T) {
	maxTokens := 1024
	temp := 0.7
	topP := 0.9
	config := model.GenerationConfig{
		MaxTokens:   &maxTokens,
		Temperature: &temp,
		TopP:        &topP,
		Stop:        []string{"END", "STOP"},
	}

	result := buildInferenceConfig(config)
	require.NotNil(t, result)
	assert.Equal(t, int32(1024), *result.MaxTokens)
	assert.InDelta(t, float32(0.7), *result.Temperature, 0.001)
	assert.InDelta(t, float32(0.9), *result.TopP, 0.001)
	assert.Equal(t, []string{"END", "STOP"}, result.StopSequences)
}

func TestBuildInferenceConfig_NoFields(t *testing.T) {
	config := model.GenerationConfig{}
	result := buildInferenceConfig(config)
	assert.Nil(t, result)
}

func TestBuildInferenceConfig_PartialFields(t *testing.T) {
	maxTokens := 512
	config := model.GenerationConfig{
		MaxTokens: &maxTokens,
	}
	result := buildInferenceConfig(config)
	require.NotNil(t, result)
	assert.Equal(t, int32(512), *result.MaxTokens)
	assert.Nil(t, result.Temperature)
	assert.Nil(t, result.TopP)
}

// ============================================================================
// Additional Model Request Fields Tests
// ============================================================================

func TestBuildAdditionalModelRequestFields_ThinkingEnabled(t *testing.T) {
	enabled := true
	tokens := 2048
	config := model.GenerationConfig{
		ThinkingEnabled: &enabled,
		ThinkingTokens:  &tokens,
	}
	result := buildAdditionalModelRequestFields(config)
	require.NotNil(t, result)

	data, err := result.MarshalSmithyDocument()
	require.NoError(t, err)

	var fields map[string]any
	err = json.Unmarshal(data, &fields)
	require.NoError(t, err)

	thinking, ok := fields["thinking"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "enabled", thinking["type"])
	assert.Equal(t, float64(2048), thinking["budget_tokens"])
}

func TestBuildAdditionalModelRequestFields_ThinkingDisabled(t *testing.T) {
	disabled := false
	config := model.GenerationConfig{
		ThinkingEnabled: &disabled,
	}
	result := buildAdditionalModelRequestFields(config)
	require.NotNil(t, result)

	data, err := result.MarshalSmithyDocument()
	require.NoError(t, err)

	var fields map[string]any
	err = json.Unmarshal(data, &fields)
	require.NoError(t, err)

	thinking, ok := fields["thinking"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "disabled", thinking["type"])
	_, hasBudget := thinking["budget_tokens"]
	assert.False(t, hasBudget)
}

func TestBuildAdditionalModelRequestFields_ThinkingEnabledNoBudget(t *testing.T) {
	enabled := true
	config := model.GenerationConfig{
		ThinkingEnabled: &enabled,
	}
	result := buildAdditionalModelRequestFields(config)
	require.NotNil(t, result)

	data, err := result.MarshalSmithyDocument()
	require.NoError(t, err)

	var fields map[string]any
	err = json.Unmarshal(data, &fields)
	require.NoError(t, err)

	thinking, ok := fields["thinking"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "enabled", thinking["type"])
	_, hasBudget := thinking["budget_tokens"]
	assert.False(t, hasBudget)
}

func TestBuildAdditionalModelRequestFields_ReasoningEffort(t *testing.T) {
	effort := "high"
	config := model.GenerationConfig{
		ReasoningEffort: &effort,
	}
	result := buildAdditionalModelRequestFields(config)
	require.NotNil(t, result)

	data, err := result.MarshalSmithyDocument()
	require.NoError(t, err)

	var fields map[string]any
	err = json.Unmarshal(data, &fields)
	require.NoError(t, err)

	assert.Equal(t, "high", fields["reasoning_effort"])
}

func TestBuildAdditionalModelRequestFields_ThinkingAndReasoning(t *testing.T) {
	enabled := true
	tokens := 4096
	effort := "medium"
	config := model.GenerationConfig{
		ThinkingEnabled: &enabled,
		ThinkingTokens:  &tokens,
		ReasoningEffort: &effort,
	}
	result := buildAdditionalModelRequestFields(config)
	require.NotNil(t, result)

	data, err := result.MarshalSmithyDocument()
	require.NoError(t, err)

	var fields map[string]any
	err = json.Unmarshal(data, &fields)
	require.NoError(t, err)

	thinking, ok := fields["thinking"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "enabled", thinking["type"])
	assert.Equal(t, float64(4096), thinking["budget_tokens"])
	assert.Equal(t, "medium", fields["reasoning_effort"])
}

func TestBuildAdditionalModelRequestFields_NoConfig(t *testing.T) {
	config := model.GenerationConfig{}
	result := buildAdditionalModelRequestFields(config)
	assert.Nil(t, result)
}

func TestBuildAdditionalModelRequestFields_EmptyReasoningEffort(t *testing.T) {
	empty := ""
	config := model.GenerationConfig{
		ReasoningEffort: &empty,
	}
	result := buildAdditionalModelRequestFields(config)
	assert.Nil(t, result)
}

func TestBuildAdditionalModelRequestFields_ZeroThinkingTokens(t *testing.T) {
	enabled := true
	zero := 0
	config := model.GenerationConfig{
		ThinkingEnabled: &enabled,
		ThinkingTokens:  &zero,
	}
	result := buildAdditionalModelRequestFields(config)
	require.NotNil(t, result)

	data, err := result.MarshalSmithyDocument()
	require.NoError(t, err)

	var fields map[string]any
	err = json.Unmarshal(data, &fields)
	require.NoError(t, err)

	thinking, ok := fields["thinking"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "enabled", thinking["type"])
	// Zero budget_tokens should not be included
	_, hasBudget := thinking["budget_tokens"]
	assert.False(t, hasBudget)
}

// ============================================================================
// Tool Configuration Tests
// ============================================================================

func TestBuildToolConfig(t *testing.T) {
	tools := map[string]tool.Tool{
		"calculator": stubTool{decl: &tool.Declaration{
			Name:        "calculator",
			Description: "Perform calculations",
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"expression": {Type: "string", Description: "Math expression"},
				},
				Required: []string{"expression"},
			},
		}},
		"search": stubTool{decl: &tool.Declaration{
			Name:        "search",
			Description: "Search the web",
			InputSchema: &tool.Schema{
				Type: "object",
				Properties: map[string]*tool.Schema{
					"query": {Type: "string", Description: "Search query"},
				},
				Required: []string{"query"},
			},
		}},
	}

	config := buildToolConfig(tools)
	require.NotNil(t, config)
	require.Len(t, config.Tools, 2)

	// Verify sorted by name
	tool0, ok := config.Tools[0].(*types.ToolMemberToolSpec)
	require.True(t, ok)
	assert.Equal(t, "calculator", aws.ToString(tool0.Value.Name))

	tool1, ok := config.Tools[1].(*types.ToolMemberToolSpec)
	require.True(t, ok)
	assert.Equal(t, "search", aws.ToString(tool1.Value.Name))
}

func TestBuildToolConfig_Empty(t *testing.T) {
	config := buildToolConfig(nil)
	assert.Nil(t, config)

	config = buildToolConfig(map[string]tool.Tool{})
	assert.Nil(t, config)
}

func TestBuildToolConfig_NilSchema(t *testing.T) {
	tools := map[string]tool.Tool{
		"simple": stubTool{decl: &tool.Declaration{
			Name:        "simple",
			Description: "A simple tool",
			InputSchema: nil,
		}},
	}

	config := buildToolConfig(tools)
	require.NotNil(t, config)
	require.Len(t, config.Tools, 1)
}

// ============================================================================
// Schema Conversion Tests
// ============================================================================

func TestSchemaToMap_Complete(t *testing.T) {
	schema := &tool.Schema{
		Type:        "object",
		Description: "Test schema",
		Required:    []string{"name"},
		Properties: map[string]*tool.Schema{
			"name": {Type: "string", Description: "Name field"},
			"age":  {Type: "number", Description: "Age field"},
			"tags": {
				Type:  "array",
				Items: &tool.Schema{Type: "string"},
			},
		},
	}

	result := schemaToMap(schema)
	assert.Equal(t, "object", result["type"])
	assert.Equal(t, "Test schema", result["description"])
	assert.Equal(t, []any{"name"}, result["required"])

	props, ok := result["properties"].(map[string]any)
	require.True(t, ok)
	assert.Len(t, props, 3)

	nameSchema, ok := props["name"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "string", nameSchema["type"])
}

func TestSchemaToMap_Nil(t *testing.T) {
	result := schemaToMap(nil)
	assert.Equal(t, "object", result["type"])
}

func TestSchemaToMap_WithEnum(t *testing.T) {
	schema := &tool.Schema{
		Type: "string",
		Enum: []any{"red", "green", "blue"},
	}
	result := schemaToMap(schema)
	assert.Equal(t, "string", result["type"])
	assert.Equal(t, []any{"red", "green", "blue"}, result["enum"])
}

func TestSchemaToMap_WithDefault(t *testing.T) {
	schema := &tool.Schema{
		Type:    "number",
		Default: 42,
	}
	result := schemaToMap(schema)
	assert.Equal(t, float64(42), result["default"])
}

// ============================================================================
// Document Conversion Tests
// ============================================================================

func TestMarshalDocumentInterface(t *testing.T) {
	doc := document.NewLazyDocument(map[string]any{
		"key": "value",
		"num": 42,
	})
	result := marshalDocumentInterface(doc)
	assert.NotEqual(t, "{}", string(result))

	var parsed map[string]any
	err := json.Unmarshal(result, &parsed)
	require.NoError(t, err)
	assert.Equal(t, "value", parsed["key"])
	// json.Number is used for numbers from JSON decoder
	assert.Contains(t, string(result), "42")
}

func TestMarshalDocumentInterface_Nil(t *testing.T) {
	result := marshalDocumentInterface(nil)
	assert.Equal(t, "{}", string(result))
}

func TestUnmarshalToDocument(t *testing.T) {
	data := []byte(`{"city":"Tokyo","temp":25}`)
	doc := unmarshalToDocument(data)
	assert.NotNil(t, doc)

	// Verify document can be correctly serialized back to JSON
	resultBytes, err := doc.MarshalSmithyDocument()
	require.NoError(t, err)

	var v map[string]any
	err = json.Unmarshal(resultBytes, &v)
	require.NoError(t, err)
	assert.Equal(t, "Tokyo", v["city"])
}

func TestUnmarshalToDocument_Empty(t *testing.T) {
	doc := unmarshalToDocument(nil)
	assert.NotNil(t, doc)
}

func TestUnmarshalToDocument_InvalidJSON(t *testing.T) {
	doc := unmarshalToDocument([]byte("not json"))
	assert.NotNil(t, doc)
}

// ============================================================================
// Image Conversion Tests
// ============================================================================

func TestConvertImageToBlock_WithData(t *testing.T) {
	img := &model.Image{
		Data:   []byte{0x89, 0x50, 0x4E, 0x47}, // PNG magic bytes
		Format: "png",
	}
	block, err := convertImageToBlock(img)
	require.NoError(t, err)
	require.NotNil(t, block)

	imgBlock, ok := block.(*types.ContentBlockMemberImage)
	require.True(t, ok)
	assert.Equal(t, types.ImageFormat("png"), imgBlock.Value.Format)
}

func TestConvertImageToBlock_Nil(t *testing.T) {
	_, err := convertImageToBlock(nil)
	assert.Error(t, err)
}

func TestConvertImageToBlock_URLOnly(t *testing.T) {
	img := &model.Image{
		URL: "https://example.com/image.png",
	}
	_, err := convertImageToBlock(img)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "URL-based images are not supported")
}

func TestInferImageFormat(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"png", "png"},
		{"PNG", "png"},
		{"jpg", "jpeg"},
		{"jpeg", "jpeg"},
		{"JPEG", "jpeg"},
		{"gif", "gif"},
		{"webp", "webp"},
		{"unknown", "png"}, // Default png
		{"", "png"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, inferImageFormat(tt.input))
		})
	}
}

// ============================================================================
// User Content Block Conversion Tests
// ============================================================================

func TestConvertUserContentBlocks_TextOnly(t *testing.T) {
	msg := model.Message{Content: "Hello world"}
	blocks, err := convertUserContentBlocks(msg)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	textBlock, ok := blocks[0].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "Hello world", textBlock.Value)
}

func TestConvertUserContentBlocks_Empty(t *testing.T) {
	msg := model.Message{}
	blocks, err := convertUserContentBlocks(msg)
	require.NoError(t, err)
	require.Len(t, blocks, 0) // Empty message should not produce any content blocks
}

func TestConvertUserContentBlocks_WithContentParts(t *testing.T) {
	text := "Check this image"
	msg := model.Message{
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeText, Text: &text},
			{Type: model.ContentTypeImage, Image: &model.Image{
				Data:   []byte{0x89, 0x50, 0x4E, 0x47},
				Format: "png",
			}},
		},
	}
	blocks, err := convertUserContentBlocks(msg)
	require.NoError(t, err)
	require.Len(t, blocks, 2)
}

// ============================================================================
// Assistant Content Block Conversion Tests
// ============================================================================

func TestConvertAssistantContentBlocks_WithToolCalls(t *testing.T) {
	msg := model.Message{
		Content: "Let me check that for you.",
		ToolCalls: []model.ToolCall{
			{
				Type: functionToolType,
				ID:   "tc_001",
				Function: model.FunctionDefinitionParam{
					Name:      "search",
					Arguments: []byte(`{"query":"test"}`),
				},
			},
		},
	}
	blocks, err := convertAssistantContentBlocks(msg)
	require.NoError(t, err)
	require.Len(t, blocks, 2) // 1 text + 1 tool_use

	textBlock, ok := blocks[0].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "Let me check that for you.", textBlock.Value)

	toolBlock, ok := blocks[1].(*types.ContentBlockMemberToolUse)
	require.True(t, ok)
	assert.Equal(t, "tc_001", aws.ToString(toolBlock.Value.ToolUseId))
	assert.Equal(t, "search", aws.ToString(toolBlock.Value.Name))
}

func TestConvertAssistantContentBlocks_Empty(t *testing.T) {
	msg := model.Message{}
	blocks, err := convertAssistantContentBlocks(msg)
	require.NoError(t, err)
	require.Len(t, blocks, 0) // Empty message should not produce any content blocks
}

// ============================================================================
// Output Message Conversion Tests
// ============================================================================

func TestConvertOutputMessage_TextAndToolUse(t *testing.T) {
	msg := types.Message{
		Role: types.ConversationRoleAssistant,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: "I'll search for that."},
			&types.ContentBlockMemberToolUse{
				Value: types.ToolUseBlock{
					ToolUseId: aws.String("tc_002"),
					Name:      aws.String("web_search"),
					Input:     document.NewLazyDocument(map[string]any{"q": "golang"}),
				},
			},
		},
	}

	result := convertOutputMessage(msg)
	assert.Equal(t, model.RoleAssistant, result.Role)
	assert.Equal(t, "I'll search for that.", result.Content)
	require.Len(t, result.ToolCalls, 1)
	assert.Equal(t, "tc_002", result.ToolCalls[0].ID)
	assert.Equal(t, "web_search", result.ToolCalls[0].Function.Name)
}

func TestConvertOutputMessage_MultipleTextBlocks(t *testing.T) {
	msg := types.Message{
		Role: types.ConversationRoleAssistant,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberText{Value: "Part 1. "},
			&types.ContentBlockMemberText{Value: "Part 2."},
		},
	}

	result := convertOutputMessage(msg)
	assert.Equal(t, "Part 1. Part 2.", result.Content)
}

func TestConvertOutputMessage_ReasoningWithSignature(t *testing.T) {
	msg := types.Message{
		Role: types.ConversationRoleAssistant,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberReasoningContent{
				Value: &types.ReasoningContentBlockMemberReasoningText{
					Value: types.ReasoningTextBlock{
						Text:      aws.String("Let me think step by step..."),
						Signature: aws.String("sig_abc123"),
					},
				},
			},
			&types.ContentBlockMemberText{Value: "The answer is 42."},
		},
	}

	result := convertOutputMessage(msg)
	assert.Equal(t, model.RoleAssistant, result.Role)
	assert.Equal(t, "The answer is 42.", result.Content)
	assert.Equal(t, "Let me think step by step...", result.ReasoningContent)
	assert.Equal(t, "sig_abc123", result.ReasoningSignature)
}

func TestConvertOutputMessage_ReasoningWithoutSignature(t *testing.T) {
	msg := types.Message{
		Role: types.ConversationRoleAssistant,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberReasoningContent{
				Value: &types.ReasoningContentBlockMemberReasoningText{
					Value: types.ReasoningTextBlock{
						Text: aws.String("Reasoning without signature"),
					},
				},
			},
			&types.ContentBlockMemberText{Value: "Result."},
		},
	}

	result := convertOutputMessage(msg)
	assert.Equal(t, "Reasoning without signature", result.ReasoningContent)
	assert.Equal(t, "", result.ReasoningSignature)
}

func TestConvertAssistantContentBlocks_WithReasoningAndSignature(t *testing.T) {
	msg := model.Message{
		Content:            "The answer is 42.",
		ReasoningContent:   "Let me think step by step...",
		ReasoningSignature: "sig_abc123",
	}
	blocks, err := convertAssistantContentBlocks(msg)
	require.NoError(t, err)
	require.Len(t, blocks, 2) // 1 reasoning + 1 text

	// First block should be reasoning content with signature
	reasoningBlock, ok := blocks[0].(*types.ContentBlockMemberReasoningContent)
	require.True(t, ok)
	reasoningText, ok := reasoningBlock.Value.(*types.ReasoningContentBlockMemberReasoningText)
	require.True(t, ok)
	assert.Equal(t, "Let me think step by step...", aws.ToString(reasoningText.Value.Text))
	assert.Equal(t, "sig_abc123", aws.ToString(reasoningText.Value.Signature))

	// Second block should be text
	textBlock, ok := blocks[1].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "The answer is 42.", textBlock.Value)
}

func TestConvertAssistantContentBlocks_WithReasoningNoSignature(t *testing.T) {
	msg := model.Message{
		Content:          "The answer is 42.",
		ReasoningContent: "Reasoning text only",
	}
	blocks, err := convertAssistantContentBlocks(msg)
	require.NoError(t, err)
	require.Len(t, blocks, 2) // 1 reasoning + 1 text

	// First block should be reasoning content without signature
	reasoningBlock, ok := blocks[0].(*types.ContentBlockMemberReasoningContent)
	require.True(t, ok)
	reasoningText, ok := reasoningBlock.Value.(*types.ReasoningContentBlockMemberReasoningText)
	require.True(t, ok)
	assert.Equal(t, "Reasoning text only", aws.ToString(reasoningText.Value.Text))
	assert.Nil(t, reasoningText.Value.Signature)

	// Second block should be text
	textBlock, ok := blocks[1].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "The answer is 42.", textBlock.Value)
}

func TestConvertAssistantContentBlocks_ReasoningRoundTrip(t *testing.T) {
	// Simulate a round-trip: output message -> model.Message -> assistant content blocks
	outputMsg := types.Message{
		Role: types.ConversationRoleAssistant,
		Content: []types.ContentBlock{
			&types.ContentBlockMemberReasoningContent{
				Value: &types.ReasoningContentBlockMemberReasoningText{
					Value: types.ReasoningTextBlock{
						Text:      aws.String("Step 1: analyze. Step 2: conclude."),
						Signature: aws.String("sig_roundtrip_xyz"),
					},
				},
			},
			&types.ContentBlockMemberText{Value: "Final answer."},
		},
	}

	// Convert output to model.Message
	modelMsg := convertOutputMessage(outputMsg)
	assert.Equal(t, "Step 1: analyze. Step 2: conclude.", modelMsg.ReasoningContent)
	assert.Equal(t, "sig_roundtrip_xyz", modelMsg.ReasoningSignature)

	// Convert back to Bedrock content blocks
	blocks, err := convertAssistantContentBlocks(modelMsg)
	require.NoError(t, err)
	require.Len(t, blocks, 2)

	// Verify reasoning block preserves both text and signature
	reasoningBlock, ok := blocks[0].(*types.ContentBlockMemberReasoningContent)
	require.True(t, ok)
	reasoningText, ok := reasoningBlock.Value.(*types.ReasoningContentBlockMemberReasoningText)
	require.True(t, ok)
	assert.Equal(t, "Step 1: analyze. Step 2: conclude.", aws.ToString(reasoningText.Value.Text))
	assert.Equal(t, "sig_roundtrip_xyz", aws.ToString(reasoningText.Value.Signature))

	// Verify text block
	textBlock, ok := blocks[1].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "Final answer.", textBlock.Value)
}

// ============================================================================
// Options Tests
// ============================================================================

func TestWithAWSConfig(t *testing.T) {
	cfg := aws.Config{Region: "us-east-1"}
	o := &options{}
	WithAWSConfig(cfg)(o)
	assert.Equal(t, "us-east-1", o.awsConfig.Region)
}

func TestWithBedrockOptions(t *testing.T) {
	o := &options{}
	opt := func(bo *bedrockruntime.Options) {
		bo.Region = "us-west-2"
	}
	WithBedrockOptions(opt)(o)
	assert.Len(t, o.bedrockOptions, 1)
}

func TestWithClient(t *testing.T) {
	mock := &mockBedrockClient{}
	o := &options{}
	WithClient(mock)(o)
	assert.Equal(t, mock, o.client)
}

// ============================================================================
// Error Response Tests
// ============================================================================

func TestSendErrorResponse(t *testing.T) {
	m := &Model{modelID: "test-model"}
	ch := make(chan *model.Response, 1)
	m.sendErrorResponse(context.Background(), ch, model.ErrorTypeStreamError, errors.New("test error"))

	resp := <-ch
	require.NotNil(t, resp.Error)
	assert.Equal(t, "test error", resp.Error.Message)
	assert.Equal(t, model.ErrorTypeStreamError, resp.Error.Type)
	assert.True(t, resp.Done)
}

func TestSendErrorResponse_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	m := &Model{modelID: "test-model"}
	ch := make(chan *model.Response) // Unbuffered, write will block
	// Should not panic
	m.sendErrorResponse(ctx, ch, model.ErrorTypeAPIError, errors.New("test"))
}

// ============================================================================
// Integration Tests (requires real AWS credentials, enable via BEDROCK_INTEGRATION_TEST=1)
// Test model: mistral.mistral-large-3-675b-instruct
// Test region: us-east-1
// ============================================================================

const (
	integrationTestModelID = "mistral.mistral-large-3-675b-instruct"
	integrationTestRegion  = "us-east-1"
)

func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("BEDROCK_INTEGRATION_TEST") != "1" {
		t.Skip("Skipping integration test: set BEDROCK_INTEGRATION_TEST=1 and configure AWS credentials to enable")
	}
}

func newIntegrationModel(t *testing.T) *Model {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(integrationTestRegion))
	require.NoError(t, err, "Failed to load AWS config, please ensure AWS credentials are configured")
	return New(integrationTestModelID, WithAWSConfig(cfg))
}

func TestIntegration_MistralLarge_NonStreaming(t *testing.T) {
	skipIfNoIntegration(t)

	m := newIntegrationModel(t)
	info := m.Info()
	assert.Equal(t, integrationTestModelID, info.Name)

	maxTokens := 256
	temp := 0.7
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a helpful assistant. Keep responses brief."),
			model.NewUserMessage("Say hello in French, just one sentence."),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temp,
		},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}
	require.NotEmpty(t, responses)

	lastResp := responses[len(responses)-1]
	require.Nil(t, lastResp.Error, "API returned error: %v", lastResp.Error)
	assert.True(t, lastResp.Done)
	require.NotEmpty(t, lastResp.Choices)
	assert.NotEmpty(t, lastResp.Choices[0].Message.Content)
	assert.Equal(t, model.RoleAssistant, lastResp.Choices[0].Message.Role)
	assert.NotNil(t, lastResp.Choices[0].FinishReason)

	t.Logf("Model: %s", integrationTestModelID)
	t.Logf("Region: %s", integrationTestRegion)
	t.Logf("Response: %s", lastResp.Choices[0].Message.Content)
	t.Logf("Finish reason: %s", *lastResp.Choices[0].FinishReason)
	if lastResp.Usage != nil {
		t.Logf("Token usage: prompt=%d, completion=%d, total=%d",
			lastResp.Usage.PromptTokens, lastResp.Usage.CompletionTokens, lastResp.Usage.TotalTokens)
	}
}

func TestIntegration_MistralLarge_Streaming(t *testing.T) {
	skipIfNoIntegration(t)

	m := newIntegrationModel(t)

	maxTokens := 256
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Count from 1 to 5, one number per line."),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &maxTokens,
			Stream:    true,
		},
	})
	require.NoError(t, err)

	var (
		responses    []*model.Response
		partialCount int
		finalResp    *model.Response
	)
	for resp := range ch {
		responses = append(responses, resp)
		if resp.IsPartial {
			partialCount++
		}
		if resp.Done {
			finalResp = resp
		}
	}

	require.NotEmpty(t, responses)
	assert.Greater(t, partialCount, 0, "Should receive at least one delta response")
	require.NotNil(t, finalResp, "Should receive a final response")
	require.Nil(t, finalResp.Error, "API returned error: %v", finalResp.Error)
	assert.NotEmpty(t, finalResp.Choices[0].Message.Content)

	t.Logf("Model: %s (streaming)", integrationTestModelID)
	t.Logf("Region: %s", integrationTestRegion)
	t.Logf("Delta response count: %d", partialCount)
	t.Logf("Final response: %s", finalResp.Choices[0].Message.Content)
	t.Logf("Finish reason: %s", *finalResp.Choices[0].FinishReason)
}

func TestIntegration_MistralLarge_ToolCall(t *testing.T) {
	skipIfNoIntegration(t)

	m := newIntegrationModel(t)

	maxTokens := 512
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("What's the current weather in Tokyo? Use the get_weather tool."),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &maxTokens,
		},
		Tools: map[string]tool.Tool{
			"get_weather": stubTool{decl: &tool.Declaration{
				Name:        "get_weather",
				Description: "Get the current weather for a specified city. Returns temperature and conditions.",
				InputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"city": {
							Type:        "string",
							Description: "The city name to get weather for",
						},
						"unit": {
							Type:        "string",
							Description: "Temperature unit: celsius or fahrenheit",
							Enum:        []any{"celsius", "fahrenheit"},
						},
					},
					Required: []string{"city"},
				},
			}},
		},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}
	require.NotEmpty(t, responses)

	lastResp := responses[len(responses)-1]
	require.Nil(t, lastResp.Error, "API returned error: %v", lastResp.Error)
	assert.True(t, lastResp.Done)

	t.Logf("Model: %s (tool call)", integrationTestModelID)
	t.Logf("Region: %s", integrationTestRegion)
	t.Logf("Finish reason: %s", *lastResp.Choices[0].FinishReason)

	if len(lastResp.Choices[0].Message.ToolCalls) > 0 {
		assert.Equal(t, "tool_use", *lastResp.Choices[0].FinishReason)
		tc := lastResp.Choices[0].Message.ToolCalls[0]
		t.Logf("Tool call ID: %s", tc.ID)
		t.Logf("Tool name: %s", tc.Function.Name)
		t.Logf("Tool arguments: %s", string(tc.Function.Arguments))
		assert.Equal(t, "get_weather", tc.Function.Name)

		// Verify arguments contain city
		var args map[string]any
		err = json.Unmarshal(tc.Function.Arguments, &args)
		require.NoError(t, err)
		assert.Contains(t, strings.ToLower(fmt.Sprintf("%v", args["city"])), "tokyo")
	} else {
		t.Logf("Model did not call tool, returned text: %s", lastResp.Choices[0].Message.Content)
	}
}

func TestIntegration_MistralLarge_StreamingToolCall(t *testing.T) {
	skipIfNoIntegration(t)

	m := newIntegrationModel(t)

	maxTokens := 512
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Calculate 15 * 37 using the calculator tool."),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens: &maxTokens,
			Stream:    true,
		},
		Tools: map[string]tool.Tool{
			"calculator": stubTool{decl: &tool.Declaration{
				Name:        "calculator",
				Description: "Perform mathematical calculations. Returns the result of the expression.",
				InputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"expression": {
							Type:        "string",
							Description: "The mathematical expression to evaluate",
						},
					},
					Required: []string{"expression"},
				},
			}},
		},
	})
	require.NoError(t, err)

	var (
		responses []*model.Response
		finalResp *model.Response
	)
	for resp := range ch {
		responses = append(responses, resp)
		if resp.Done {
			finalResp = resp
		}
	}

	require.NotEmpty(t, responses)
	require.NotNil(t, finalResp)
	require.Nil(t, finalResp.Error, "API returned error: %v", finalResp.Error)

	t.Logf("Model: %s (streaming tool call)", integrationTestModelID)
	t.Logf("Region: %s", integrationTestRegion)
	t.Logf("Total responses: %d", len(responses))
	t.Logf("Finish reason: %s", *finalResp.Choices[0].FinishReason)

	if len(finalResp.Choices[0].Message.ToolCalls) > 0 {
		tc := finalResp.Choices[0].Message.ToolCalls[0]
		t.Logf("Tool call: %s(%s)", tc.Function.Name, string(tc.Function.Arguments))
		assert.Equal(t, "calculator", tc.Function.Name)
	}
}

func TestIntegration_MistralLarge_MultiTurn(t *testing.T) {
	skipIfNoIntegration(t)

	m := newIntegrationModel(t)

	maxTokens := 256

	// First turn
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a math tutor. Be concise."),
			model.NewUserMessage("What is 2+2?"),
		},
		GenerationConfig: model.GenerationConfig{MaxTokens: &maxTokens},
	})
	require.NoError(t, err)

	var firstResp *model.Response
	for resp := range ch {
		firstResp = resp
	}
	require.NotNil(t, firstResp)
	require.Nil(t, firstResp.Error)
	t.Logf("First turn reply: %s", firstResp.Choices[0].Message.Content)

	// Second turn (with context)
	ch, err = m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a math tutor. Be concise."),
			model.NewUserMessage("What is 2+2?"),
			model.NewAssistantMessage(firstResp.Choices[0].Message.Content),
			model.NewUserMessage("Now multiply that result by 3."),
		},
		GenerationConfig: model.GenerationConfig{MaxTokens: &maxTokens},
	})
	require.NoError(t, err)

	var secondResp *model.Response
	for resp := range ch {
		secondResp = resp
	}
	require.NotNil(t, secondResp)
	require.Nil(t, secondResp.Error)
	assert.NotEmpty(t, secondResp.Choices[0].Message.Content)
	t.Logf("Second turn reply: %s", secondResp.Choices[0].Message.Content)
}

// ============================================================================
// Concurrency Safety Tests
// ============================================================================

func TestGenerateContent_ConcurrentRequests(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			time.Sleep(10 * time.Millisecond) // Simulate network latency
			return &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonEndTurn,
				Output: &types.ConverseOutputMemberMessage{
					Value: types.Message{
						Role: types.ConversationRoleAssistant,
						Content: []types.ContentBlock{
							&types.ContentBlockMemberText{Value: "response"},
						},
					},
				},
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(5),
					OutputTokens: aws.Int32(1),
					TotalTokens:  aws.Int32(6),
				},
			}, nil
		},
	}

	m := New("test-model", WithClient(mock))

	var wg sync.WaitGroup
	errs := make([]error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			ch, err := m.GenerateContent(context.Background(), &model.Request{
				Messages: []model.Message{
					model.NewUserMessage(fmt.Sprintf("Request %d", idx)),
				},
			})
			if err != nil {
				errs[idx] = err
				return
			}
			for resp := range ch {
				assert.NotNil(t, resp)
			}
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		require.NoError(t, err, "worker %d failed", i)
	}

	mu.Lock()
	assert.Equal(t, 10, callCount)
	mu.Unlock()
}

// ============================================================================
// Boundary Condition Tests
// ============================================================================

func TestConvertMessages_EmptyMessages(t *testing.T) {
	bedrockMsgs, systemBlocks, err := convertMessages(nil)
	require.NoError(t, err)
	assert.Empty(t, bedrockMsgs)
	assert.Empty(t, systemBlocks)
}

func TestConvertMessages_MultipleSystemMessages(t *testing.T) {
	messages := []model.Message{
		model.NewSystemMessage("Rule 1"),
		model.NewSystemMessage("Rule 2"),
		model.NewUserMessage("Hello"),
	}

	_, systemBlocks, err := convertMessages(messages)
	require.NoError(t, err)
	assert.Len(t, systemBlocks, 2)
}

func TestConvertSchemaToDocument_NilSchema(t *testing.T) {
	doc := convertSchemaToDocument(nil)
	assert.NotNil(t, doc)

	// Verify document can be correctly serialized
	resultBytes, err := doc.MarshalSmithyDocument()
	require.NoError(t, err)

	var v map[string]any
	err = json.Unmarshal(resultBytes, &v)
	require.NoError(t, err)
	assert.Equal(t, "object", v["type"])
}

func TestBuildNonStreamingResponse_NilUsage(t *testing.T) {
	m := &Model{modelID: "test-model"}
	output := &bedrockruntime.ConverseOutput{
		StopReason: types.StopReasonEndTurn,
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{Value: "test"},
				},
			},
		},
	}

	resp := m.buildNonStreamingResponse(output)
	assert.Nil(t, resp.Usage)
	assert.Equal(t, "test", resp.Choices[0].Message.Content)
}

func TestBuildNonStreamingResponse_WithCacheTokens(t *testing.T) {
	m := &Model{modelID: "test-model"}
	output := &bedrockruntime.ConverseOutput{
		StopReason: types.StopReasonEndTurn,
		Output: &types.ConverseOutputMemberMessage{
			Value: types.Message{
				Role: types.ConversationRoleAssistant,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberText{Value: "cached response"},
				},
			},
		},
		Usage: &types.TokenUsage{
			InputTokens:          aws.Int32(100),
			OutputTokens:         aws.Int32(20),
			TotalTokens:          aws.Int32(120),
			CacheReadInputTokens: aws.Int32(80),
		},
	}

	resp := m.buildNonStreamingResponse(output)
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 80, resp.Usage.PromptTokensDetails.CachedTokens)
}

// ============================================================================
// Multi-Turn Conversation Tests
// ============================================================================

func TestGenerateContent_MultiTurnConversation(t *testing.T) {
	turnCount := 0
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			turnCount++
			// Verify message count increases with conversation turns
			assert.GreaterOrEqual(t, len(params.Messages), turnCount)

			return &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonEndTurn,
				Output: &types.ConverseOutputMemberMessage{
					Value: types.Message{
						Role: types.ConversationRoleAssistant,
						Content: []types.ContentBlock{
							&types.ContentBlockMemberText{Value: fmt.Sprintf("Turn %d response", turnCount)},
						},
					},
				},
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(int32(10 * turnCount)),
					OutputTokens: aws.Int32(5),
					TotalTokens:  aws.Int32(int32(10*turnCount + 5)),
				},
			}, nil
		},
	}

	m := New("test-model", WithClient(mock))

	// First turn
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Hello"),
		},
	})
	require.NoError(t, err)
	var resp1 *model.Response
	for r := range ch {
		resp1 = r
	}
	require.NotNil(t, resp1)
	assert.Contains(t, resp1.Choices[0].Message.Content, "Turn 1")

	// Second turn
	ch, err = m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Hello"),
			model.NewAssistantMessage(resp1.Choices[0].Message.Content),
			model.NewUserMessage("Tell me more"),
		},
	})
	require.NoError(t, err)
	var resp2 *model.Response
	for r := range ch {
		resp2 = r
	}
	require.NotNil(t, resp2)
	assert.Contains(t, resp2.Choices[0].Message.Content, "Turn 2")
}

// ============================================================================
// Mistral Model Specific Tests (Using Mock)
// ============================================================================

func TestMistralLarge_NonStreaming_Mock(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			assert.Equal(t, "mistral.mistral-large-3-675b-instruct", aws.ToString(params.ModelId))
			return &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonEndTurn,
				Output: &types.ConverseOutputMemberMessage{
					Value: types.Message{
						Role: types.ConversationRoleAssistant,
						Content: []types.ContentBlock{
							&types.ContentBlockMemberText{Value: "Bonjour! Comment puis-je vous aider?"},
						},
					},
				},
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(15),
					OutputTokens: aws.Int32(10),
					TotalTokens:  aws.Int32(25),
				},
			}, nil
		},
	}

	m := New("mistral.mistral-large-3-675b-instruct", WithClient(mock))
	info := m.Info()
	assert.Equal(t, "mistral.mistral-large-3-675b-instruct", info.Name)

	maxTokens := 256
	temp := 0.7
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("You are a helpful assistant. Respond in French."),
			model.NewUserMessage("Say hello"),
		},
		GenerationConfig: model.GenerationConfig{
			MaxTokens:   &maxTokens,
			Temperature: &temp,
		},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}
	require.Len(t, responses, 1)
	assert.True(t, responses[0].Done)
	assert.Contains(t, responses[0].Choices[0].Message.Content, "Bonjour")
}

func TestMistralLarge_Streaming_Mock(t *testing.T) {
	events := []types.ConverseStreamOutput{
		&types.ConverseStreamOutputMemberMessageStart{
			Value: types.MessageStartEvent{Role: types.ConversationRoleAssistant},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: aws.Int32(0),
				Delta:             &types.ContentBlockDeltaMemberText{Value: "Bonjour"},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: aws.Int32(0),
				Delta:             &types.ContentBlockDeltaMemberText{Value: "! Comment"},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockDelta{
			Value: types.ContentBlockDeltaEvent{
				ContentBlockIndex: aws.Int32(0),
				Delta:             &types.ContentBlockDeltaMemberText{Value: " allez-vous?"},
			},
		},
		&types.ConverseStreamOutputMemberContentBlockStop{
			Value: types.ContentBlockStopEvent{ContentBlockIndex: aws.Int32(0)},
		},
		&types.ConverseStreamOutputMemberMessageStop{
			Value: types.MessageStopEvent{StopReason: types.StopReasonEndTurn},
		},
		&types.ConverseStreamOutputMemberMetadata{
			Value: types.ConverseStreamMetadataEvent{
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(12),
					OutputTokens: aws.Int32(8),
					TotalTokens:  aws.Int32(20),
				},
			},
		},
	}

	reader := newMockEventStreamReader(events)
	m := &Model{modelID: "mistral.mistral-large-3-675b-instruct", channelBufferSize: 256}

	// Use production code processStreamEvents to handle stream events
	responseChan := make(chan *model.Response, 256)
	go func() {
		defer close(responseChan)
		m.processStreamEvents(context.Background(), reader, responseChan)
	}()

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// Should have 3 deltas + 1 final response (with usage)
	require.Len(t, responses, 4)

	// Verify delta responses
	assert.True(t, responses[0].IsPartial)
	assert.Equal(t, "Bonjour", responses[0].Choices[0].Delta.Content)
	assert.True(t, responses[1].IsPartial)
	assert.Equal(t, "! Comment", responses[1].Choices[0].Delta.Content)
	assert.True(t, responses[2].IsPartial)
	assert.Equal(t, " allez-vous?", responses[2].Choices[0].Delta.Content)

	// Verify final response (usage merged into finalResponse)
	assert.True(t, responses[3].Done)
	assert.Equal(t, "Bonjour! Comment allez-vous?", responses[3].Choices[0].Message.Content)
	assert.Equal(t, "end_turn", *responses[3].Choices[0].FinishReason)
	assert.NotNil(t, responses[3].Usage)
	assert.Equal(t, 12, responses[3].Usage.PromptTokens)
	assert.Equal(t, 8, responses[3].Usage.CompletionTokens)
	assert.Equal(t, 20, responses[3].Usage.TotalTokens)
}

func TestMistralLarge_ToolCall_Mock(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			assert.Equal(t, "mistral.mistral-large-3-675b-instruct", aws.ToString(params.ModelId))
			require.NotNil(t, params.ToolConfig)

			return &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonToolUse,
				Output: &types.ConverseOutputMemberMessage{
					Value: types.Message{
						Role: types.ConversationRoleAssistant,
						Content: []types.ContentBlock{
							&types.ContentBlockMemberText{Value: "Let me check the weather for you."},
							&types.ContentBlockMemberToolUse{
								Value: types.ToolUseBlock{
									ToolUseId: aws.String("mistral_tc_001"),
									Name:      aws.String("get_weather"),
									Input:     document.NewLazyDocument(map[string]any{"location": "Paris", "unit": "celsius"}),
								},
							},
						},
					},
				},
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(60),
					OutputTokens: aws.Int32(40),
					TotalTokens:  aws.Int32(100),
				},
			}, nil
		},
	}

	m := New("mistral.mistral-large-3-675b-instruct", WithClient(mock))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("What's the weather in Paris?"),
		},
		Tools: map[string]tool.Tool{
			"get_weather": stubTool{decl: &tool.Declaration{
				Name:        "get_weather",
				Description: "Get current weather for a location",
				InputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"location": {Type: "string", Description: "City name"},
						"unit":     {Type: "string", Description: "Temperature unit", Enum: []any{"celsius", "fahrenheit"}},
					},
					Required: []string{"location"},
				},
			}},
		},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}

	require.Len(t, responses, 1)
	resp := responses[0]
	assert.Equal(t, "tool_use", *resp.Choices[0].FinishReason)
	assert.Equal(t, "Let me check the weather for you.", resp.Choices[0].Message.Content)
	require.Len(t, resp.Choices[0].Message.ToolCalls, 1)

	tc := resp.Choices[0].Message.ToolCalls[0]
	assert.Equal(t, "mistral_tc_001", tc.ID)
	assert.Equal(t, "get_weather", tc.Function.Name)

	var args map[string]any
	err = json.Unmarshal(tc.Function.Arguments, &args)
	require.NoError(t, err)
	assert.Equal(t, "Paris", args["location"])
	assert.Equal(t, "celsius", args["unit"])
}

func TestMistralLarge_SkillInvocation_Mock(t *testing.T) {
	// Test Skill invocation scenario: model returns multiple tool calls
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			return &bedrockruntime.ConverseOutput{
				StopReason: types.StopReasonToolUse,
				Output: &types.ConverseOutputMemberMessage{
					Value: types.Message{
						Role: types.ConversationRoleAssistant,
						Content: []types.ContentBlock{
							&types.ContentBlockMemberToolUse{
								Value: types.ToolUseBlock{
									ToolUseId: aws.String("skill_001"),
									Name:      aws.String("code_interpreter"),
									Input:     document.NewLazyDocument(map[string]any{"code": "print('hello')"}),
								},
							},
							&types.ContentBlockMemberToolUse{
								Value: types.ToolUseBlock{
									ToolUseId: aws.String("skill_002"),
									Name:      aws.String("web_search"),
									Input:     document.NewLazyDocument(map[string]any{"query": "golang bedrock sdk"}),
								},
							},
						},
					},
				},
				Usage: &types.TokenUsage{
					InputTokens:  aws.Int32(50),
					OutputTokens: aws.Int32(60),
					TotalTokens:  aws.Int32(110),
				},
			}, nil
		},
	}

	m := New("mistral.mistral-large-3-675b-instruct", WithClient(mock))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("Run this code and search for bedrock docs"),
		},
		Tools: map[string]tool.Tool{
			"code_interpreter": stubTool{decl: &tool.Declaration{
				Name:        "code_interpreter",
				Description: "Execute Python code",
				InputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"code": {Type: "string", Description: "Python code to execute"},
					},
					Required: []string{"code"},
				},
			}},
			"web_search": stubTool{decl: &tool.Declaration{
				Name:        "web_search",
				Description: "Search the web",
				InputSchema: &tool.Schema{
					Type: "object",
					Properties: map[string]*tool.Schema{
						"query": {Type: "string", Description: "Search query"},
					},
					Required: []string{"query"},
				},
			}},
		},
	})
	require.NoError(t, err)

	var responses []*model.Response
	for resp := range ch {
		responses = append(responses, resp)
	}

	require.Len(t, responses, 1)
	resp := responses[0]
	require.Len(t, resp.Choices[0].Message.ToolCalls, 2)

	// Verify both tool calls
	assert.Equal(t, "skill_001", resp.Choices[0].Message.ToolCalls[0].ID)
	assert.Equal(t, "code_interpreter", resp.Choices[0].Message.ToolCalls[0].Function.Name)
	assert.Equal(t, "skill_002", resp.Choices[0].Message.ToolCalls[1].ID)
	assert.Equal(t, "web_search", resp.Choices[0].Message.ToolCalls[1].Function.Name)
}

// TestBuildToolDescription_NoOutputSchema verifies that when OutputSchema is nil,
// the description is returned unchanged.
func TestBuildToolDescription_NoOutputSchema(t *testing.T) {
	decl := &tool.Declaration{
		Name:        "example_tool",
		Description: "A simple tool",
	}

	desc := buildToolDescription(decl)

	assert.Equal(t, "A simple tool", desc)
}

// TestBuildToolDescription_AppendsOutputSchema verifies that when OutputSchema is present,
// the serialized schema is appended to the description.
func TestBuildToolDescription_AppendsOutputSchema(t *testing.T) {
	outputSchema := &tool.Schema{
		Type: "object",
		Properties: map[string]*tool.Schema{
			"result": {Type: "string", Description: "The result value"},
		},
	}
	decl := &tool.Declaration{
		Name:         "example_tool",
		Description:  "A tool with output",
		OutputSchema: outputSchema,
	}

	desc := buildToolDescription(decl)

	assert.Contains(t, desc, "A tool with output")
	assert.Contains(t, desc, "Output schema:")
	assert.Contains(t, desc, `"result"`)
	assert.Contains(t, desc, `"string"`)
}

// TestBuildToolDescription_EmptyDescription verifies that output schema is still appended
// even when the base description is empty.
func TestBuildToolDescription_EmptyDescription(t *testing.T) {
	decl := &tool.Declaration{
		Name:        "empty_desc_tool",
		Description: "",
		OutputSchema: &tool.Schema{
			Type: "object",
			Properties: map[string]*tool.Schema{
				"count": {Type: "integer"},
			},
		},
	}

	desc := buildToolDescription(decl)

	assert.Contains(t, desc, "Output schema:")
	assert.Contains(t, desc, `"count"`)
}

// TestBuildToolDescription_MarshalError verifies that when OutputSchema cannot be marshaled,
// the original description is returned as fallback.
func TestBuildToolDescription_MarshalError(t *testing.T) {
	decl := &tool.Declaration{
		Name:        "bad_schema_tool",
		Description: "fallback desc",
		OutputSchema: &tool.Schema{
			Type:                 "object",
			AdditionalProperties: func() {}, // functions cannot be marshaled
		},
	}

	desc := buildToolDescription(decl)

	assert.Equal(t, "fallback desc", desc, "should fall back to original description on marshal error")
}

// ============================================================================
// Unsupported Content Type Error Tests
// ============================================================================

// TestConvertUserContentBlocks_UnsupportedType verifies that truly unsupported content types
// return an error instead of being silently dropped.
func TestConvertUserContentBlocks_UnsupportedType(t *testing.T) {
	msg := model.Message{
		ContentParts: []model.ContentPart{
			{Type: model.ContentType("unknown_type")},
		},
	}
	_, err := convertUserContentBlocks(msg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported content part type")
	assert.Contains(t, err.Error(), "unknown_type")
}

// TestConvertUserContentBlocks_FileWithData verifies that file content parts are converted correctly.
func TestConvertUserContentBlocks_FileWithData(t *testing.T) {
	msg := model.Message{
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeFile, File: &model.File{
				Name:     "report.pdf",
				Data:     []byte("fake-pdf-data"),
				MimeType: "application/pdf",
			}},
		},
	}
	blocks, err := convertUserContentBlocks(msg)
	require.NoError(t, err)
	require.Len(t, blocks, 1)
	docBlock, ok := blocks[0].(*types.ContentBlockMemberDocument)
	require.True(t, ok)
	assert.Equal(t, types.DocumentFormat("pdf"), docBlock.Value.Format)
	assert.Equal(t, "report.pdf", aws.ToString(docBlock.Value.Name))
}

// TestConvertUserContentBlocks_FileNilData verifies that file with nil File field is skipped.
func TestConvertUserContentBlocks_FileNilData(t *testing.T) {
	msg := model.Message{
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeFile, File: nil},
		},
	}
	blocks, err := convertUserContentBlocks(msg)
	require.NoError(t, err)
	require.Len(t, blocks, 0)
}

// TestConvertAssistantContentBlocks_UnsupportedType verifies that unsupported content types
// in assistant messages return an error.
func TestConvertAssistantContentBlocks_UnsupportedType(t *testing.T) {
	msg := model.Message{
		ContentParts: []model.ContentPart{
			{Type: model.ContentTypeImage},
		},
	}
	_, err := convertAssistantContentBlocks(msg)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported content part type")
}

// TestConvertImageToBlock_EmptyImage verifies that an image with neither data nor URL returns an error.
func TestConvertImageToBlock_EmptyImage(t *testing.T) {
	img := &model.Image{}
	_, err := convertImageToBlock(img)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "neither data nor URL")
}

// TestConvertMessages_UnsupportedContentTypeReturnsError verifies that convertMessages
// propagates errors from unsupported content types to the caller.
func TestConvertMessages_UnsupportedContentTypeReturnsError(t *testing.T) {
	messages := []model.Message{
		{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{Type: model.ContentType("unknown_type")},
			},
		},
	}
	_, _, err := convertMessages(messages)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported content part type")
}

// TestConvertMessages_URLImageReturnsError verifies that a user message with only a URL image
// returns an error instead of silently dropping the message.
func TestConvertMessages_URLImageReturnsError(t *testing.T) {
	messages := []model.Message{
		{
			Role: model.RoleUser,
			ContentParts: []model.ContentPart{
				{Type: model.ContentTypeImage, Image: &model.Image{URL: "https://example.com/img.png"}},
			},
		},
	}
	_, _, err := convertMessages(messages)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "URL-based images are not supported")
}

// TestConvertFileToBlock_Success verifies successful file to document block conversion.
func TestConvertFileToBlock_Success(t *testing.T) {
	file := &model.File{
		Name:     "doc.pdf",
		Data:     []byte("pdf-content"),
		MimeType: "application/pdf",
	}
	block, err := convertFileToBlock(file)
	require.NoError(t, err)
	require.NotNil(t, block)

	docBlock, ok := block.(*types.ContentBlockMemberDocument)
	require.True(t, ok)
	assert.Equal(t, types.DocumentFormat("pdf"), docBlock.Value.Format)
	assert.Equal(t, "doc.pdf", aws.ToString(docBlock.Value.Name))
	src, ok := docBlock.Value.Source.(*types.DocumentSourceMemberBytes)
	require.True(t, ok)
	assert.Equal(t, []byte("pdf-content"), src.Value)
}

// TestConvertFileToBlock_Nil verifies that nil file returns an error.
func TestConvertFileToBlock_Nil(t *testing.T) {
	_, err := convertFileToBlock(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nil file data")
}

// TestConvertFileToBlock_NoDataNoFileID verifies that file with neither data nor file ID returns an error.
func TestConvertFileToBlock_NoDataNoFileID(t *testing.T) {
	file := &model.File{Name: "empty.txt"}
	_, err := convertFileToBlock(file)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "neither data nor file ID")
}

// TestConvertFileToBlock_FileIDOnly verifies that file with only file ID returns an error.
func TestConvertFileToBlock_FileIDOnly(t *testing.T) {
	file := &model.File{FileID: "file-123"}
	_, err := convertFileToBlock(file)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "file ID-based files are not supported")
}

// TestConvertFileToBlock_NoName verifies that file without name gets a default name.
func TestConvertFileToBlock_NoName(t *testing.T) {
	file := &model.File{
		Data:     []byte("content"),
		MimeType: "text/plain",
	}
	block, err := convertFileToBlock(file)
	require.NoError(t, err)
	docBlock, ok := block.(*types.ContentBlockMemberDocument)
	require.True(t, ok)
	assert.Equal(t, "file.txt", aws.ToString(docBlock.Value.Name))
}

// TestInferDocumentFormatFromMimeType verifies MIME type to document format inference.
func TestInferDocumentFormatFromMimeType(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		// All DocumentFormat enum values covered
		{"application/pdf", "pdf"},
		{"text/csv", "csv"},
		{"application/msword", "doc"},
		{"application/vnd.openxmlformats-officedocument.wordprocessingml.document", "docx"},
		{"application/vnd.ms-excel", "xls"},
		{"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "xlsx"},
		{"text/html", "html"},
		{"text/plain", "txt"},
		{"text/markdown", "md"},
		// Fallback: extract suffix from MIME type
		{"application/json", "json"},
		// Empty string defaults to txt
		{"", "txt"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, inferDocumentFormatFromMimeType(tt.input))
		})
	}
}
