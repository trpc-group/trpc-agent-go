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

// StreamReader defines the stream event reader interface, decoupling stream processing logic for testing.
type StreamReader interface {
	Events() <-chan types.ConverseStreamOutput
	Close() error
	Err() error
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
		m.sendErrorResponse(ctx, responseChan, classifyError(ctx, err, model.ErrorTypeAPIError), err)
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
		m.sendErrorResponse(ctx, responseChan, classifyError(ctx, err, model.ErrorTypeAPIError), err)
		return
	}

	stream := output.GetStream()
	defer stream.Close()

	m.processStreamEvents(ctx, stream, responseChan)
}

// processStreamEvents processes stream events and sends responses to responseChan.
// This method accepts a StreamReader interface, allowing mock injection during testing.
func (m *Model) processStreamEvents(
	ctx context.Context,
	stream StreamReader,
	responseChan chan<- *model.Response,
) {
	// Variables for accumulating tool call information
	var (
		toolCalls        []model.ToolCall
		currentToolCall  *model.ToolCall
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		toolCallIndex    int
		finalResponse    *model.Response
		usage            *model.Usage
	)

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-stream.Events():
			if !ok {
				goto streamDone
			}
			switch ev := event.(type) {
			case *types.ConverseStreamOutputMemberContentBlockStart:
				// Handle content block start event
				if ev.Value.Start != nil {
					switch start := ev.Value.Start.(type) {
					case *types.ContentBlockStartMemberToolUse:
						// Tool call start
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
				// Handle content block delta event
				if ev.Value.Delta != nil {
					switch delta := ev.Value.Delta.(type) {
					case *types.ContentBlockDeltaMemberText:
						// Text delta
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
						// Reasoning content delta
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
						// Tool call arguments delta
						if currentToolCall != nil && delta.Value.Input != nil {
							currentToolCall.Function.Arguments = append(
								currentToolCall.Function.Arguments,
								[]byte(aws.ToString(delta.Value.Input))...,
							)
						}
					}
				}

			case *types.ConverseStreamOutputMemberContentBlockStop:
				// Content block end
				if currentToolCall != nil {
					toolCalls = append(toolCalls, *currentToolCall)
					currentToolCall = nil
				}

			case *types.ConverseStreamOutputMemberMessageStop:
				// Message end, build final response (deferred send, waiting for metadata event to populate usage)
				finishReason := string(ev.Value.StopReason)
				finalResponse = &model.Response{
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

			case *types.ConverseStreamOutputMemberMetadata:
				// Metadata event, parse usage information
				if ev.Value.Usage != nil {
					usage = &model.Usage{
						PromptTokens:     int(aws.ToInt32(ev.Value.Usage.InputTokens)),
						CompletionTokens: int(aws.ToInt32(ev.Value.Usage.OutputTokens)),
						TotalTokens:      int(aws.ToInt32(ev.Value.Usage.TotalTokens)),
						PromptTokensDetails: model.PromptTokensDetails{
							CachedTokens:        int(aws.ToInt32(ev.Value.Usage.CacheReadInputTokens)),
							CacheCreationTokens: int(aws.ToInt32(ev.Value.Usage.CacheWriteInputTokens)),
							CacheReadTokens:     int(aws.ToInt32(ev.Value.Usage.CacheReadInputTokens)),
						},
					}
				}
			}
		}
	}

streamDone:

	// Check stream error
	if err := stream.Err(); err != nil {
		m.sendErrorResponse(ctx, responseChan, classifyError(ctx, err, model.ErrorTypeStreamError), err)
		return
	}

	// Send final response after stream ends, merging usage into finalResponse
	if finalResponse != nil {
		if usage != nil {
			finalResponse.Usage = usage
		}
		select {
		case responseChan <- finalResponse:
		case <-ctx.Done():
		}
	}
}

// buildConverseInput builds the input parameters for the Converse API.
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

	// Set inference configuration
	input.InferenceConfig = buildInferenceConfig(request.GenerationConfig)

	// Set tool configuration
	if len(request.Tools) > 0 {
		input.ToolConfig = buildToolConfig(request.Tools)
	}

	return input, nil
}

// buildConverseStreamInput builds the input parameters for the ConverseStream API.
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

	// Set inference configuration
	input.InferenceConfig = buildInferenceConfig(request.GenerationConfig)

	// Set tool configuration
	if len(request.Tools) > 0 {
		input.ToolConfig = buildToolConfig(request.Tools)
	}

	return input, nil
}

// buildNonStreamingResponse converts the Converse API output to model.Response.
func (m *Model) buildNonStreamingResponse(output *bedrockruntime.ConverseOutput) *model.Response {
	now := time.Now()
	response := &model.Response{
		Object:    model.ObjectTypeChatCompletion,
		Model:     m.modelID,
		Created:   now.Unix(),
		Timestamp: now,
		Done:      true,
	}

	// Set finish reason
	finishReason := string(output.StopReason)
	choice := model.Choice{
		Index:        0,
		FinishReason: &finishReason,
	}

	// Parse output message
	if output.Output != nil {
		if msgOutput, ok := output.Output.(*types.ConverseOutputMemberMessage); ok {
			choice.Message = convertOutputMessage(msgOutput.Value)
		}
	}

	response.Choices = []model.Choice{choice}

	// Set usage
	if output.Usage != nil {
		response.Usage = &model.Usage{
			PromptTokens:     int(aws.ToInt32(output.Usage.InputTokens)),
			CompletionTokens: int(aws.ToInt32(output.Usage.OutputTokens)),
			TotalTokens:      int(aws.ToInt32(output.Usage.TotalTokens)),
			PromptTokensDetails: model.PromptTokensDetails{
				CachedTokens:        int(aws.ToInt32(output.Usage.CacheReadInputTokens)),
				CacheCreationTokens: int(aws.ToInt32(output.Usage.CacheWriteInputTokens)),
				CacheReadTokens:     int(aws.ToInt32(output.Usage.CacheReadInputTokens)),
			},
		}
	}

	return response
}

// convertOutputMessage converts a Bedrock message to model.Message.
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
			// Convert tool call Input (document.Interface) to JSON bytes
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

// convertMessages converts a list of model.Message to Bedrock message format.
func convertMessages(messages []model.Message) ([]types.Message, []types.SystemContentBlock, error) {
	var (
		bedrockMessages []types.Message
		systemBlocks    []types.SystemContentBlock
	)

	for _, msg := range messages {
		switch msg.Role {
		case model.RoleSystem:
			// System messages are used as system prompt
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
			// Tool results are sent as ToolResult blocks in a user message
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

	// Merge consecutive messages with the same role (Bedrock requires alternating messages)
	bedrockMessages = mergeConsecutiveMessages(bedrockMessages)

	return bedrockMessages, systemBlocks, nil
}

// convertUserContentBlocks converts user messages to Bedrock content blocks.
func convertUserContentBlocks(msg model.Message) []types.ContentBlock {
	var blocks []types.ContentBlock

	if msg.Content != "" {
		blocks = append(blocks, &types.ContentBlockMemberText{Value: msg.Content})
	}

	for _, part := range msg.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil && *part.Text != "" {
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

	return blocks
}

// convertAssistantContentBlocks converts assistant messages to Bedrock content blocks.
func convertAssistantContentBlocks(msg model.Message) []types.ContentBlock {
	var blocks []types.ContentBlock

	if msg.Content != "" {
		blocks = append(blocks, &types.ContentBlockMemberText{Value: msg.Content})
	}

	for _, part := range msg.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil && *part.Text != "" {
			blocks = append(blocks, &types.ContentBlockMemberText{Value: *part.Text})
		}
	}

	// Add tool call blocks
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

	return blocks
}

// convertImageToBlock converts image data to a Bedrock image block.
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

	// Bedrock does not directly support URL-based images; they need to be downloaded first.
	// URL type is not handled here for now.
	return nil
}

// inferImageFormat infers the image format.
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

// mergeConsecutiveMessages merges consecutive messages with the same role.
// The Bedrock API requires messages to alternate (user/assistant).
func mergeConsecutiveMessages(messages []types.Message) []types.Message {
	if len(messages) <= 1 {
		return messages
	}

	merged := make([]types.Message, 0, len(messages))
	merged = append(merged, messages[0])

	for i := 1; i < len(messages); i++ {
		last := &merged[len(merged)-1]
		if last.Role == messages[i].Role {
			// Merge content blocks
			last.Content = append(last.Content, messages[i].Content...)
		} else {
			merged = append(merged, messages[i])
		}
	}

	return merged
}

// buildInferenceConfig builds the inference configuration.
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

// buildToolConfig builds the tool configuration.
func buildToolConfig(tools map[string]tool.Tool) *types.ToolConfiguration {
	if len(tools) == 0 {
		return nil
	}

	// Sort by name for stability
	toolNames := make([]string, 0, len(tools))
	for name := range tools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)

	var bedrockTools []types.Tool
	for _, name := range toolNames {
		t := tools[name]
		declaration := t.Declaration()

		// Convert tool.Schema to document.Interface as JSON schema
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

// convertSchemaToDocument converts tool.Schema to document.Interface.
func convertSchemaToDocument(schema *tool.Schema) document.Interface {
	if schema == nil {
		return document.NewLazyDocument(map[string]any{
			"type": "object",
		})
	}

	schemaMap := schemaToMap(schema)
	return document.NewLazyDocument(schemaMap)
}

// schemaToMap recursively converts tool.Schema to a map.
func schemaToMap(schema *tool.Schema) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object"}
	}

	result := make(map[string]any)

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
		props := make(map[string]any)
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

// marshalDocumentInterface converts document.Interface to JSON bytes.
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

// unmarshalToDocument converts JSON bytes to document.Interface.
func unmarshalToDocument(data []byte) document.Interface {
	if len(data) == 0 {
		return document.NewLazyDocument(map[string]any{})
	}

	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		return document.NewLazyDocument(map[string]any{})
	}

	return document.NewLazyDocument(v)
}

// classifyError determines whether the error is a caller cancellation/timeout.
// If so, it returns ErrorTypeCancelled; otherwise it returns the fallback error type.
func classifyError(ctx context.Context, err error, fallback string) string {
	if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return model.ErrorTypeCancelled
	}
	return fallback
}

// sendErrorResponse sends an error response.
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
