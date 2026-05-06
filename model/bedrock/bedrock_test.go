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
// Mock / Stub 类型
// ============================================================================

// stubTool 实现 tool.Tool 接口，用于测试。
type stubTool struct{ decl *tool.Declaration }

func (s stubTool) Call(_ context.Context, _ []byte) (any, error) { return nil, nil }
func (s stubTool) Declaration() *tool.Declaration                { return s.decl }

// mockBedrockClient 实现 BedrockClient 接口，用于单元测试。
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

// mockEventStreamReader 模拟 ConverseStreamOutputReader 接口。
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
// 基础测试
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
// 非流式对话测试
// ============================================================================

func TestGenerateContent_NonStreaming_SimpleText(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			// 验证请求参数
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

	// 验证 usage
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 10, resp.Usage.PromptTokens)
	assert.Equal(t, 8, resp.Usage.CompletionTokens)
	assert.Equal(t, 18, resp.Usage.TotalTokens)
}

func TestGenerateContent_NonStreaming_WithSystemMessage(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			// 验证系统消息被正确提取
			require.NotNil(t, params.System)
			require.Len(t, params.System, 1)
			sysBlock, ok := params.System[0].(*types.SystemContentBlockMemberText)
			require.True(t, ok)
			assert.Equal(t, "You are a helpful assistant.", sysBlock.Value)

			// 验证用户消息
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
	cancel() // 立即取消

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
	// 应该收到错误响应或通道直接关闭
	if len(responses) > 0 {
		assert.NotNil(t, responses[0].Error)
		assert.Equal(t, model.ErrorTypeCancelled, responses[0].Error.Type)
	}
}

// ============================================================================
// 非流式工具调用测试
// ============================================================================

func TestGenerateContent_NonStreaming_ToolCall(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			// 验证工具配置
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

	// 验证参数
	var args map[string]any
	err = json.Unmarshal(tc.Function.Arguments, &args)
	require.NoError(t, err)
	assert.Equal(t, "Beijing", args["city"])
}

func TestGenerateContent_NonStreaming_ToolResult(t *testing.T) {
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			// 验证工具结果消息被正确转换
			// 工具结果应该作为 user 角色的 ToolResult 块
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
// 流式对话测试
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

	// 使用生产代码 processStreamEvents 处理流式事件
	responseChan := make(chan *model.Response, 256)
	go func() {
		defer close(responseChan)
		m.processStreamEvents(context.Background(), reader, responseChan)
	}()

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// 应该有 2 个增量 + 1 个最终响应（包含 usage）
	require.Len(t, responses, 3)

	// 验证增量响应
	assert.True(t, responses[0].IsPartial)
	assert.Equal(t, "Hello", responses[0].Choices[0].Delta.Content)
	assert.True(t, responses[1].IsPartial)
	assert.Equal(t, " World!", responses[1].Choices[0].Delta.Content)

	// 验证最终响应（usage 已合并到 finalResponse 中）
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

	// 使用生产代码 processStreamEvents 处理流式事件
	responseChan := make(chan *model.Response, 256)
	go func() {
		defer close(responseChan)
		m.processStreamEvents(context.Background(), reader, responseChan)
	}()

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// 应该有 1 个最终响应（包含工具调用）
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
// 消息转换测试
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

	// 系统消息应该被提取到 systemBlocks
	require.Len(t, systemBlocks, 1)
	sysBlock, ok := systemBlocks[0].(*types.SystemContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "Be helpful", sysBlock.Value)

	// 其余消息应该交替出现
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

	// user -> assistant -> user(tool_result)，合并后应该是 3 条
	// 但 tool result 是 user 角色，不会与前面的 assistant 合并
	require.Len(t, bedrockMsgs, 3)

	// 验证最后一条是工具结果
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
	assert.Len(t, merged[0].Content, 2) // 两条 user 消息合并
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
// 推理配置测试
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
// 工具配置测试
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

	// 验证按名称排序
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
// Schema 转换测试
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
	assert.Equal(t, []string{"name"}, result["required"])

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
	assert.Equal(t, 42, result["default"])
}

// ============================================================================
// Document 转换测试
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

	// 验证 document 可以正确序列化回 JSON
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
// 图片转换测试
// ============================================================================

func TestConvertImageToBlock_WithData(t *testing.T) {
	img := &model.Image{
		Data:   []byte{0x89, 0x50, 0x4E, 0x47}, // PNG magic bytes
		Format: "png",
	}
	block := convertImageToBlock(img)
	require.NotNil(t, block)

	imgBlock, ok := block.(*types.ContentBlockMemberImage)
	require.True(t, ok)
	assert.Equal(t, types.ImageFormat("png"), imgBlock.Value.Format)
}

func TestConvertImageToBlock_Nil(t *testing.T) {
	block := convertImageToBlock(nil)
	assert.Nil(t, block)
}

func TestConvertImageToBlock_URLOnly(t *testing.T) {
	img := &model.Image{
		URL: "https://example.com/image.png",
	}
	block := convertImageToBlock(img)
	assert.Nil(t, block) // URL 类型暂不支持
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
		{"unknown", "png"}, // 默认 png
		{"", "png"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.expected, inferImageFormat(tt.input))
		})
	}
}

// ============================================================================
// 用户内容块转换测试
// ============================================================================

func TestConvertUserContentBlocks_TextOnly(t *testing.T) {
	msg := model.Message{Content: "Hello world"}
	blocks := convertUserContentBlocks(msg)
	require.Len(t, blocks, 1)
	textBlock, ok := blocks[0].(*types.ContentBlockMemberText)
	require.True(t, ok)
	assert.Equal(t, "Hello world", textBlock.Value)
}

func TestConvertUserContentBlocks_Empty(t *testing.T) {
	msg := model.Message{}
	blocks := convertUserContentBlocks(msg)
	require.Len(t, blocks, 1) // 应该有一个空文本块
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
	blocks := convertUserContentBlocks(msg)
	require.Len(t, blocks, 2)
}

// ============================================================================
// 助手内容块转换测试
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
	blocks := convertAssistantContentBlocks(msg)
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
	blocks := convertAssistantContentBlocks(msg)
	require.Len(t, blocks, 1) // 应该有一个空文本块
}

// ============================================================================
// 输出消息转换测试
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

// ============================================================================
// Options 测试
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
// 错误响应测试
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
	ch := make(chan *model.Response) // 无缓冲，写入会阻塞
	// 不应该 panic
	m.sendErrorResponse(ctx, ch, model.ErrorTypeAPIError, errors.New("test"))
}

// ============================================================================
// 集成测试（需要真实 AWS 凭证，通过环境变量 BEDROCK_INTEGRATION_TEST=1 启用）
// 测试模型: mistral.mistral-large-3-675b-instruct
// 测试区域: us-east-1 (美东1区)
// ============================================================================

const (
	integrationTestModelID = "mistral.mistral-large-3-675b-instruct"
	integrationTestRegion  = "us-east-1"
)

func skipIfNoIntegration(t *testing.T) {
	t.Helper()
	if os.Getenv("BEDROCK_INTEGRATION_TEST") != "1" {
		t.Skip("跳过集成测试: 设置 BEDROCK_INTEGRATION_TEST=1 并配置 AWS 凭证以启用")
	}
}

func newIntegrationModel(t *testing.T) *Model {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background(), config.WithRegion(integrationTestRegion))
	require.NoError(t, err, "加载 AWS 配置失败，请确保已配置 AWS 凭证")
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
	require.Nil(t, lastResp.Error, "API 返回错误: %v", lastResp.Error)
	assert.True(t, lastResp.Done)
	require.NotEmpty(t, lastResp.Choices)
	assert.NotEmpty(t, lastResp.Choices[0].Message.Content)
	assert.Equal(t, model.RoleAssistant, lastResp.Choices[0].Message.Role)
	assert.NotNil(t, lastResp.Choices[0].FinishReason)

	t.Logf("模型: %s", integrationTestModelID)
	t.Logf("区域: %s", integrationTestRegion)
	t.Logf("响应: %s", lastResp.Choices[0].Message.Content)
	t.Logf("结束原因: %s", *lastResp.Choices[0].FinishReason)
	if lastResp.Usage != nil {
		t.Logf("Token 使用: prompt=%d, completion=%d, total=%d",
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
	assert.Greater(t, partialCount, 0, "应该收到至少一个增量响应")
	require.NotNil(t, finalResp, "应该收到最终响应")
	require.Nil(t, finalResp.Error, "API 返回错误: %v", finalResp.Error)
	assert.NotEmpty(t, finalResp.Choices[0].Message.Content)

	t.Logf("模型: %s (流式)", integrationTestModelID)
	t.Logf("区域: %s", integrationTestRegion)
	t.Logf("增量响应数: %d", partialCount)
	t.Logf("最终响应: %s", finalResp.Choices[0].Message.Content)
	t.Logf("结束原因: %s", *finalResp.Choices[0].FinishReason)
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
	require.Nil(t, lastResp.Error, "API 返回错误: %v", lastResp.Error)
	assert.True(t, lastResp.Done)

	t.Logf("模型: %s (工具调用)", integrationTestModelID)
	t.Logf("区域: %s", integrationTestRegion)
	t.Logf("结束原因: %s", *lastResp.Choices[0].FinishReason)

	if len(lastResp.Choices[0].Message.ToolCalls) > 0 {
		assert.Equal(t, "tool_use", *lastResp.Choices[0].FinishReason)
		tc := lastResp.Choices[0].Message.ToolCalls[0]
		t.Logf("工具调用 ID: %s", tc.ID)
		t.Logf("工具名称: %s", tc.Function.Name)
		t.Logf("工具参数: %s", string(tc.Function.Arguments))
		assert.Equal(t, "get_weather", tc.Function.Name)

		// 验证参数中包含 city
		var args map[string]any
		err = json.Unmarshal(tc.Function.Arguments, &args)
		require.NoError(t, err)
		assert.Contains(t, strings.ToLower(fmt.Sprintf("%v", args["city"])), "tokyo")
	} else {
		t.Logf("模型未调用工具，返回文本: %s", lastResp.Choices[0].Message.Content)
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
	require.Nil(t, finalResp.Error, "API 返回错误: %v", finalResp.Error)

	t.Logf("模型: %s (流式工具调用)", integrationTestModelID)
	t.Logf("区域: %s", integrationTestRegion)
	t.Logf("总响应数: %d", len(responses))
	t.Logf("结束原因: %s", *finalResp.Choices[0].FinishReason)

	if len(finalResp.Choices[0].Message.ToolCalls) > 0 {
		tc := finalResp.Choices[0].Message.ToolCalls[0]
		t.Logf("工具调用: %s(%s)", tc.Function.Name, string(tc.Function.Arguments))
		assert.Equal(t, "calculator", tc.Function.Name)
	}
}

func TestIntegration_MistralLarge_MultiTurn(t *testing.T) {
	skipIfNoIntegration(t)

	m := newIntegrationModel(t)

	maxTokens := 256

	// 第一轮对话
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
	t.Logf("第一轮回复: %s", firstResp.Choices[0].Message.Content)

	// 第二轮对话（带上下文）
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
	t.Logf("第二轮回复: %s", secondResp.Choices[0].Message.Content)
}

// ============================================================================
// 并发安全测试
// ============================================================================

func TestGenerateContent_ConcurrentRequests(t *testing.T) {
	callCount := 0
	var mu sync.Mutex

	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			mu.Lock()
			callCount++
			mu.Unlock()
			time.Sleep(10 * time.Millisecond) // 模拟网络延迟
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
// 边界条件测试
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

	// 验证 document 可以正确序列化
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
// 多轮对话测试
// ============================================================================

func TestGenerateContent_MultiTurnConversation(t *testing.T) {
	turnCount := 0
	mock := &mockBedrockClient{
		converseFunc: func(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
			turnCount++
			// 验证消息数量随对话轮次增加
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

	// 第一轮
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

	// 第二轮
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
// Mistral 模型特定测试（使用 Mock）
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

	// 使用生产代码 processStreamEvents 处理流式事件
	responseChan := make(chan *model.Response, 256)
	go func() {
		defer close(responseChan)
		m.processStreamEvents(context.Background(), reader, responseChan)
	}()

	var responses []*model.Response
	for resp := range responseChan {
		responses = append(responses, resp)
	}

	// 应该有 3 个增量 + 1 个最终响应（包含 usage）
	require.Len(t, responses, 4)

	// 验证增量响应
	assert.True(t, responses[0].IsPartial)
	assert.Equal(t, "Bonjour", responses[0].Choices[0].Delta.Content)
	assert.True(t, responses[1].IsPartial)
	assert.Equal(t, "! Comment", responses[1].Choices[0].Delta.Content)
	assert.True(t, responses[2].IsPartial)
	assert.Equal(t, " allez-vous?", responses[2].Choices[0].Delta.Content)

	// 验证最终响应（usage 已合并到 finalResponse 中）
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
	// 测试 Skill 调用场景：模型返回多个工具调用
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

	// 验证两个工具调用
	assert.Equal(t, "skill_001", resp.Choices[0].Message.ToolCalls[0].ID)
	assert.Equal(t, "code_interpreter", resp.Choices[0].Message.ToolCalls[0].Function.Name)
	assert.Equal(t, "skill_002", resp.Choices[0].Message.ToolCalls[1].ID)
	assert.Equal(t, "web_search", resp.Choices[0].Message.ToolCalls[1].Function.Name)
}
