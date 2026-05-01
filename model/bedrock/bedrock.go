//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package bedrock provides an AWS Bedrock-compatible model implementation.
// It uses the Bedrock Runtime Converse/ConverseStream APIs to support
// streaming conversation, tool calling, and skill invocation.
package bedrock

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	functionToolType = "function"
)

// BedrockClient defines the interface for the Bedrock Runtime client operations used by this package.
// This allows for easier testing and mocking.
type BedrockClient interface {
	Converse(ctx context.Context, params *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
	ConverseStream(ctx context.Context, params *bedrockruntime.ConverseStreamInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseStreamOutput, error)
}

// Model implements the model.Model interface for AWS Bedrock.
type Model struct {
	client            BedrockClient
	modelID           string
	channelBufferSize int
}

// New creates a new Bedrock model adapter.
func New(modelID string, opts ...Option) *Model {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}

	var client BedrockClient
	if o.client != nil {
		client = o.client
	} else {
		client = bedrockruntime.NewFromConfig(o.awsConfig, o.bedrockOptions...)
	}

	return &Model{
		client:            client,
		modelID:           modelID,
		channelBufferSize: o.channelBufferSize,
	}
}

// Info returns the model information.
func (m *Model) Info() model.Info {
	return model.Info{
		Name: m.modelID,
	}
}

// GetClient returns the underlying Bedrock client.
// This is useful for creating additional Model instances that share the same client.
func (m *Model) GetClient() BedrockClient {
	return m.client
}

// GenerateContent generates content from the model using the Bedrock Converse API.
func (m *Model) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("request cannot be nil")
	}

	responseChan := make(chan *model.Response, m.channelBufferSize)
	go func() {
		defer close(responseChan)
		if request.Stream {
			m.handleStreamingResponse(ctx, request, responseChan)
			return
		}
		m.handleNonStreamingResponse(ctx, request, responseChan)
	}()
	return responseChan, nil
}

// handleNonStreamingResponse sends a non-streaming request to the Bedrock Converse API.
func (m *Model) handleNonStreamingResponse(
	ctx context.Context,
	request *model.Request,
	responseChan chan<- *model.Response,
) {
	input, err := m.buildConverseInput(request)
	if err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeAPIError, err)
		return
	}

	output, err := m.client.Converse(ctx, input)
	if err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeAPIError, err)
		return
	}

	response := m.buildNonStreamingResponse(output)
	select {
	case responseChan <- response:
	case <-ctx.Done():
	}
}

// handleStreamingResponse sends a streaming request to the Bedrock ConverseStream API.
func (m *Model) handleStreamingResponse(
	ctx context.Context,
	request *model.Request,
	responseChan chan<- *model.Response,
) {
	input, err := m.buildConverseStreamInput(request)
	if err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeAPIError, err)
		return
	}

	output, err := m.client.ConverseStream(ctx, input)
	if err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeAPIError, err)
		return
	}

	stream := output.GetStream()
	defer stream.Close()

	// 用于累积工具调用信息
	var (
		toolCalls        []model.ToolCall
		currentToolCall  *model.ToolCall
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		toolCallIndex    int
	)

	for event := range stream.Events() {
		switch ev := event.(type) {
		case *types.ConverseStreamOutputMemberContentBlockStart:
			// 处理内容块开始事件
			if ev.Value.Start != nil {
				switch start := ev.Value.Start.(type) {
				case *types.ContentBlockStartMemberToolUse:
					// 工具调用开始
					currentToolCall = &model.ToolCall{
						Index: func() *int { idx := toolCallIndex; return &idx }(),
						Type:  functionToolType,
						ID:    aws.ToString(start.Value.ToolUseId),
						Function: model.FunctionDefinitionParam{
							Name: aws.ToString(start.Value.Name),
						},
					}
					toolCallIndex++
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockDelta:
			// 处理内容块增量事件
			if ev.Value.Delta != nil {
				switch delta := ev.Value.Delta.(type) {
				case *types.ContentBlockDeltaMemberText:
					// 文本增量
					contentBuilder.WriteString(delta.Value)
					partialResponse := &model.Response{
						Object:    model.ObjectTypeChatCompletionChunk,
						Model:     m.modelID,
						Created:   time.Now().Unix(),
						Timestamp: time.Now(),
						IsPartial: true,
						Choices: []model.Choice{
							{
								Delta: model.Message{
									Role:    model.RoleAssistant,
									Content: delta.Value,
								},
							},
						},
					}
					select {
					case responseChan <- partialResponse:
					case <-ctx.Done():
						return
					}

				case *types.ContentBlockDeltaMemberReasoningContent:
					// 推理内容增量
					switch reasoningDelta := delta.Value.(type) {
					case *types.ReasoningContentBlockDeltaMemberText:
						reasoningBuilder.WriteString(reasoningDelta.Value)
						partialResponse := &model.Response{
							Object:    model.ObjectTypeChatCompletionChunk,
							Model:     m.modelID,
							Created:   time.Now().Unix(),
							Timestamp: time.Now(),
							IsPartial: true,
							Choices: []model.Choice{
								{
									Delta: model.Message{
										Role:             model.RoleAssistant,
										ReasoningContent: reasoningDelta.Value,
									},
								},
							},
						}
						select {
						case responseChan <- partialResponse:
						case <-ctx.Done():
							return
						}
					}

				case *types.ContentBlockDeltaMemberToolUse:
					// 工具调用参数增量
					if currentToolCall != nil && delta.Value.Input != nil {
						currentToolCall.Function.Arguments = append(
							currentToolCall.Function.Arguments,
							[]byte(aws.ToString(delta.Value.Input))...,
						)
					}
				}
			}

		case *types.ConverseStreamOutputMemberContentBlockStop:
			// 内容块结束
			if currentToolCall != nil {
				toolCalls = append(toolCalls, *currentToolCall)
				currentToolCall = nil
			}

		case *types.ConverseStreamOutputMemberMessageStop:
			// 消息结束，构建最终响应
			finishReason := string(ev.Value.StopReason)
			finalResponse := &model.Response{
				Object:    model.ObjectTypeChatCompletion,
				Model:     m.modelID,
				Created:   time.Now().Unix(),
				Timestamp: time.Now(),
				Done:      true,
				Choices: []model.Choice{
					{
						Index: 0,
						Message: model.Message{
							Role:             model.RoleAssistant,
							Content:          contentBuilder.String(),
							ReasoningContent: reasoningBuilder.String(),
							ToolCalls:        toolCalls,
						},
						FinishReason: &finishReason,
					},
				},
			}
			select {
			case responseChan <- finalResponse:
			case <-ctx.Done():
				return
			}

		case *types.ConverseStreamOutputMemberMetadata:
			// 元数据事件，更新 usage 信息
			if ev.Value.Usage != nil {
				// Usage 信息会在最终响应中一起返回
				// 这里可以用于日志记录等
			}
		}
	}

	// 检查流错误
	if err := stream.Err(); err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeStreamError, err)
	}
}

// buildConverseInput 构建 Converse API 的输入参数
func (m *Model) buildConverseInput(request *model.Request) (*bedrockruntime.ConverseInput, error) {
	messages, systemBlocks, err := convertMessages(request.Messages)
	if err != nil {
		return nil, err
	}

	input := &bedrockruntime.ConverseInput{
		ModelId:  aws.String(m.modelID),
		Messages: messages,
	}

	if len(systemBlocks) > 0 {
		input.System = systemBlocks
	}

	// 设置推理配置
	input.InferenceConfig = buildInferenceConfig(request.GenerationConfig)

	// 设置工具配置
	if len(request.Tools) > 0 {
		input.ToolConfig = buildToolConfig(request.Tools)
	}

	return input, nil
}

// buildConverseStreamInput 构建 ConverseStream API 的输入参数
func (m *Model) buildConverseStreamInput(request *model.Request) (*bedrockruntime.ConverseStreamInput, error) {
	messages, systemBlocks, err := convertMessages(request.Messages)
	if err != nil {
		return nil, err
	}

	input := &bedrockruntime.ConverseStreamInput{
		ModelId:  aws.String(m.modelID),
		Messages: messages,
	}

	if len(systemBlocks) > 0 {
		input.System = systemBlocks
	}

	// 设置推理配置
	input.InferenceConfig = buildInferenceConfig(request.GenerationConfig)

	// 设置工具配置
	if len(request.Tools) > 0 {
		input.ToolConfig = buildToolConfig(request.Tools)
	}

	return input, nil
}

// buildNonStreamingResponse 将 Converse API 的输出转换为 model.Response
func (m *Model) buildNonStreamingResponse(output *bedrockruntime.ConverseOutput) *model.Response {
	now := time.Now()
	response := &model.Response{
		Object:    model.ObjectTypeChatCompletion,
		Model:     m.modelID,
		Created:   now.Unix(),
		Timestamp: now,
		Done:      true,
	}

	// 设置 finish reason
	finishReason := string(output.StopReason)
	choice := model.Choice{
		Index:        0,
		FinishReason: &finishReason,
	}

	// 解析输出消息
	if output.Output != nil {
		if msgOutput, ok := output.Output.(*types.ConverseOutputMemberMessage); ok {
			choice.Message = convertOutputMessage(msgOutput.Value)
		}
	}

	response.Choices = []model.Choice{choice}

	// 设置 usage
	if output.Usage != nil {
		response.Usage = &model.Usage{
			PromptTokens:     int(aws.ToInt32(output.Usage.InputTokens)),
			CompletionTokens: int(aws.ToInt32(output.Usage.OutputTokens)),
			TotalTokens:      int(aws.ToInt32(output.Usage.TotalTokens)),
			PromptTokensDetails: model.PromptTokensDetails{
				CachedTokens: int(aws.ToInt32(output.Usage.CacheReadInputTokens)),
			},
		}
	}

	return response
}

// convertOutputMessage 将 Bedrock 消息转换为 model.Message
func convertOutputMessage(msg types.Message) model.Message {
	result := model.Message{
		Role: model.RoleAssistant,
	}

	var (
		textBuilder      strings.Builder
		reasoningBuilder strings.Builder
		toolCalls        []model.ToolCall
		toolCallIndex    int
	)

	for _, content := range msg.Content {
		switch block := content.(type) {
		case *types.ContentBlockMemberText:
			textBuilder.WriteString(block.Value)
		case *types.ContentBlockMemberToolUse:
			// 将工具调用的 Input (document.Interface) 转换为 JSON bytes
			inputBytes := marshalDocumentInterface(block.Value.Input)
			toolCalls = append(toolCalls, model.ToolCall{
				Index: func() *int { idx := toolCallIndex; toolCallIndex++; return &idx }(),
				Type:  functionToolType,
				ID:    aws.ToString(block.Value.ToolUseId),
				Function: model.FunctionDefinitionParam{
					Name:      aws.ToString(block.Value.Name),
					Arguments: inputBytes,
				},
			})
		case *types.ContentBlockMemberReasoningContent:
			if reasoningBlock, ok := block.Value.(*types.ReasoningContentBlockMemberReasoningText); ok {
				if reasoningBlock.Value.Text != nil {
					reasoningBuilder.WriteString(*reasoningBlock.Value.Text)
				}
			}
		}
	}

	result.Content = textBuilder.String()
	result.ReasoningContent = reasoningBuilder.String()
	result.ToolCalls = toolCalls
	return result
}

// convertMessages 将 model.Message 列表转换为 Bedrock 的消息格式
func convertMessages(messages []model.Message) ([]types.Message, []types.SystemContentBlock, error) {
	var (
		bedrockMessages []types.Message
		systemBlocks    []types.SystemContentBlock
	)

	for _, msg := range messages {
		switch msg.Role {
		case model.RoleSystem:
			// 系统消息作为 system prompt
			if msg.Content != "" {
				systemBlocks = append(systemBlocks, &types.SystemContentBlockMemberText{
					Value: msg.Content,
				})
			}
			for _, part := range msg.ContentParts {
				if part.Type == model.ContentTypeText && part.Text != nil {
					systemBlocks = append(systemBlocks, &types.SystemContentBlockMemberText{
						Value: *part.Text,
					})
				}
			}

		case model.RoleUser:
			bedrockMsg := types.Message{
				Role:    types.ConversationRoleUser,
				Content: convertUserContentBlocks(msg),
			}
			bedrockMessages = append(bedrockMessages, bedrockMsg)

		case model.RoleAssistant:
			bedrockMsg := types.Message{
				Role:    types.ConversationRoleAssistant,
				Content: convertAssistantContentBlocks(msg),
			}
			bedrockMessages = append(bedrockMessages, bedrockMsg)

		case model.RoleTool:
			// 工具结果作为 user 消息中的 ToolResult 块
			bedrockMsg := types.Message{
				Role: types.ConversationRoleUser,
				Content: []types.ContentBlock{
					&types.ContentBlockMemberToolResult{
						Value: types.ToolResultBlock{
							ToolUseId: aws.String(msg.ToolID),
							Content: []types.ToolResultContentBlock{
								&types.ToolResultContentBlockMemberText{
									Value: msg.Content,
								},
							},
						},
					},
				},
			}
			bedrockMessages = append(bedrockMessages, bedrockMsg)
		}
	}

	// 合并连续的相同角色消息（Bedrock 要求消息交替出现）
	bedrockMessages = mergeConsecutiveMessages(bedrockMessages)

	return bedrockMessages, systemBlocks, nil
}

// convertUserContentBlocks 将用户消息转换为 Bedrock 内容块
func convertUserContentBlocks(msg model.Message) []types.ContentBlock {
	var blocks []types.ContentBlock

	if msg.Content != "" {
		blocks = append(blocks, &types.ContentBlockMemberText{Value: msg.Content})
	}

	for _, part := range msg.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil {
				blocks = append(blocks, &types.ContentBlockMemberText{Value: *part.Text})
			}
		case model.ContentTypeImage:
			if part.Image != nil {
				imageBlock := convertImageToBlock(part.Image)
				if imageBlock != nil {
					blocks = append(blocks, imageBlock)
				}
			}
		}
	}

	if len(blocks) == 0 {
		blocks = append(blocks, &types.ContentBlockMemberText{Value: ""})
	}

	return blocks
}

// convertAssistantContentBlocks 将助手消息转换为 Bedrock 内容块
func convertAssistantContentBlocks(msg model.Message) []types.ContentBlock {
	var blocks []types.ContentBlock

	if msg.Content != "" {
		blocks = append(blocks, &types.ContentBlockMemberText{Value: msg.Content})
	}

	for _, part := range msg.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			blocks = append(blocks, &types.ContentBlockMemberText{Value: *part.Text})
		}
	}

	// 添加工具调用块
	for _, tc := range msg.ToolCalls {
		inputDoc := unmarshalToDocument(tc.Function.Arguments)
		blocks = append(blocks, &types.ContentBlockMemberToolUse{
			Value: types.ToolUseBlock{
				ToolUseId: aws.String(tc.ID),
				Name:      aws.String(tc.Function.Name),
				Input:     inputDoc,
			},
		})
	}

	if len(blocks) == 0 {
		blocks = append(blocks, &types.ContentBlockMemberText{Value: ""})
	}

	return blocks
}

// convertImageToBlock 将图片数据转换为 Bedrock 图片块
func convertImageToBlock(img *model.Image) types.ContentBlock {
	if img == nil {
		return nil
	}

	if len(img.Data) > 0 {
		format := inferImageFormat(img.Format)
		return &types.ContentBlockMemberImage{
			Value: types.ImageBlock{
				Source: &types.ImageSourceMemberBytes{
					Value: img.Data,
				},
				Format: types.ImageFormat(format),
			},
		}
	}

	// URL 类型的图片 Bedrock 不直接支持，需要下载后传入
	// 这里暂不处理 URL 类型
	return nil
}

// inferImageFormat 推断图片格式
func inferImageFormat(format string) string {
	switch strings.ToLower(format) {
	case "png":
		return "png"
	case "jpg", "jpeg":
		return "jpeg"
	case "gif":
		return "gif"
	case "webp":
		return "webp"
	default:
		return "png"
	}
}

// mergeConsecutiveMessages 合并连续的相同角色消息
// Bedrock API 要求消息必须交替出现（user/assistant）
func mergeConsecutiveMessages(messages []types.Message) []types.Message {
	if len(messages) <= 1 {
		return messages
	}

	merged := make([]types.Message, 0, len(messages))
	merged = append(merged, messages[0])

	for i := 1; i < len(messages); i++ {
		last := &merged[len(merged)-1]
		if last.Role == messages[i].Role {
			// 合并内容块
			last.Content = append(last.Content, messages[i].Content...)
		} else {
			merged = append(merged, messages[i])
		}
	}

	return merged
}

// buildInferenceConfig 构建推理配置
func buildInferenceConfig(config model.GenerationConfig) *types.InferenceConfiguration {
	inferenceConfig := &types.InferenceConfiguration{}
	hasConfig := false

	if config.MaxTokens != nil {
		v := int32(*config.MaxTokens)
		inferenceConfig.MaxTokens = &v
		hasConfig = true
	}

	if config.Temperature != nil {
		v := float32(*config.Temperature)
		inferenceConfig.Temperature = &v
		hasConfig = true
	}

	if config.TopP != nil {
		v := float32(*config.TopP)
		inferenceConfig.TopP = &v
		hasConfig = true
	}

	if len(config.Stop) > 0 {
		inferenceConfig.StopSequences = config.Stop
		hasConfig = true
	}

	if !hasConfig {
		return nil
	}
	return inferenceConfig
}

// buildToolConfig 构建工具配置
func buildToolConfig(tools map[string]tool.Tool) *types.ToolConfiguration {
	if len(tools) == 0 {
		return nil
	}

	// 按名称排序以保证稳定性
	toolNames := make([]string, 0, len(tools))
	for name := range tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	var bedrockTools []types.Tool
	for _, name := range toolNames {
		t := tools[name]
		declaration := t.Declaration()

		// 将 tool.Schema 转换为 document.Interface 作为 JSON schema
		inputSchema := convertSchemaToDocument(declaration.InputSchema)

		toolSpec := types.ToolSpecification{
			Name:        aws.String(declaration.Name),
			Description: aws.String(declaration.Description),
			InputSchema: &types.ToolInputSchemaMemberJson{
				Value: inputSchema,
			},
		}

		bedrockTools = append(bedrockTools, &types.ToolMemberToolSpec{
			Value: toolSpec,
		})
	}

	return &types.ToolConfiguration{
		Tools: bedrockTools,
	}
}

// convertSchemaToDocument 将 tool.Schema 转换为 document.Interface
func convertSchemaToDocument(schema *tool.Schema) document.Interface {
	if schema == nil {
		return document.NewLazyDocument(map[string]interface{}{
			"type": "object",
		})
	}

	schemaMap := schemaToMap(schema)
	return document.NewLazyDocument(schemaMap)
}

// schemaToMap 将 tool.Schema 递归转换为 map
func schemaToMap(schema *tool.Schema) map[string]interface{} {
	if schema == nil {
		return map[string]interface{}{"type": "object"}
	}

	result := make(map[string]interface{})

	if schema.Type != "" {
		result["type"] = schema.Type
	}
	if schema.Description != "" {
		result["description"] = schema.Description
	}
	if len(schema.Required) > 0 {
		result["required"] = schema.Required
	}
	if len(schema.Properties) > 0 {
		props := make(map[string]interface{})
		for k, v := range schema.Properties {
			props[k] = schemaToMap(v)
		}
		result["properties"] = props
	}
	if schema.Items != nil {
		result["items"] = schemaToMap(schema.Items)
	}
	if schema.AdditionalProperties != nil {
		result["additionalProperties"] = schema.AdditionalProperties
	}
	if schema.Default != nil {
		result["default"] = schema.Default
	}
	if len(schema.Enum) > 0 {
		result["enum"] = schema.Enum
	}

	return result
}

// marshalDocumentInterface 将 document.Interface 转换为 JSON bytes
func marshalDocumentInterface(doc document.Interface) []byte {
	if doc == nil {
		return []byte("{}")
	}

	data, err := doc.MarshalSmithyDocument()
	if err != nil {
		return []byte("{}")
	}
	return data
}

// unmarshalToDocument 将 JSON bytes 转换为 document.Interface
func unmarshalToDocument(data []byte) document.Interface {
	if len(data) == 0 {
		return document.NewLazyDocument(map[string]interface{}{})
	}

	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		return document.NewLazyDocument(map[string]interface{}{})
	}

	return document.NewLazyDocument(v)
}

// sendErrorResponse 发送错误响应
func (m *Model) sendErrorResponse(ctx context.Context, responseChan chan<- *model.Response, errType string, err error) {
	errorResponse := &model.Response{
		Error: &model.ResponseError{
			Message: err.Error(),
			Type:    errType,
		},
		Timestamp: time.Now(),
		Done:      true,
	}
	select {
	case responseChan <- errorResponse:
	case <-ctx.Done():
	}
}
