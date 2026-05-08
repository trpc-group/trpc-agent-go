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
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/document"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"trpc.group/trpc-go/trpc-agent-go/log"
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
	// Early check: if context is already cancelled, send error directly to avoid select race.
	if ctx.Err() != nil {
		responseChan <- &model.Response{
			Error: &model.ResponseError{
				Message: ctx.Err().Error(),
				Type:    model.ErrorTypeCancelled,
			},
			Timestamp: time.Now(),
			Done:      true,
		}
		return
	}

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
// This method accepts a bedrockruntime.ConverseStreamOutputReader interface, allowing mock injection during testing.
func (m *Model) processStreamEvents(
	ctx context.Context,
	stream bedrockruntime.ConverseStreamOutputReader,
	responseChan chan<- *model.Response,
) {
	// Variables for accumulating tool call information
	var (
		toolCalls        []model.ToolCall
		currentToolCall  *model.ToolCall
		contentBuilder   strings.Builder
		reasoningBuilder strings.Builder
		signatureBuilder strings.Builder
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
						case *types.ReasoningContentBlockDeltaMemberSignature:
							// Accumulate signature for round-tripping in multi-turn conversations.
							signatureBuilder.WriteString(reasoningDelta.Value)
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
								Role:               model.RoleAssistant,
								Content:            contentBuilder.String(),
								ReasoningContent:   reasoningBuilder.String(),
								ReasoningSignature: signatureBuilder.String(),
								ToolCalls:          toolCalls,
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

	// Set additional model request fields (thinking/reasoning configuration)
	input.AdditionalModelRequestFields = buildAdditionalModelRequestFields(request.GenerationConfig)

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

	// Set additional model request fields (thinking/reasoning configuration)
	input.AdditionalModelRequestFields = buildAdditionalModelRequestFields(request.GenerationConfig)

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
		signatureBuilder strings.Builder
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
				if reasoningBlock.Value.Signature != nil {
					signatureBuilder.WriteString(*reasoningBlock.Value.Signature)
				}
			}
		}
	}

	result.Content = textBuilder.String()
	result.ReasoningContent = reasoningBuilder.String()
	result.ReasoningSignature = signatureBuilder.String()
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
				if part.Type == model.ContentTypeText && part.Text != nil && *part.Text != "" {
					systemBlocks = append(systemBlocks, &types.SystemContentBlockMemberText{
						Value: *part.Text,
					})
				}
			}

		case model.RoleUser:
			content, err := convertUserContentBlocks(msg)
			if err != nil {
				return nil, nil, err
			}
			if len(content) > 0 {
				bedrockMessages = append(bedrockMessages, types.Message{
					Role:    types.ConversationRoleUser,
					Content: content,
				})
			}

		case model.RoleAssistant:
			content, err := convertAssistantContentBlocks(msg)
			if err != nil {
				return nil, nil, err
			}
			if len(content) > 0 {
				bedrockMessages = append(bedrockMessages, types.Message{
					Role:    types.ConversationRoleAssistant,
					Content: content,
				})
			}

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
// Returns an error if an unsupported content type is encountered.
func convertUserContentBlocks(msg model.Message) ([]types.ContentBlock, error) {
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
				imageBlock, err := convertImageToBlock(part.Image)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, imageBlock)
			}
		case model.ContentTypeFile:
			if part.File != nil {
				fileBlock, err := convertFileToBlock(part.File)
				if err != nil {
					return nil, err
				}
				blocks = append(blocks, fileBlock)
			}
		default:
			return nil, fmt.Errorf("bedrock: unsupported content part type %q", part.Type)
		}
	}

	return blocks, nil
}

// convertAssistantContentBlocks converts assistant messages to Bedrock content blocks.
// Returns an error if an unsupported content type is encountered.
func convertAssistantContentBlocks(msg model.Message) ([]types.ContentBlock, error) {
	var blocks []types.ContentBlock

	// Re-emit reasoning content block with both Text and Signature for round-tripping.
	if msg.ReasoningContent != "" {
		reasoningTextBlock := types.ReasoningTextBlock{
			Text: aws.String(msg.ReasoningContent),
		}
		if msg.ReasoningSignature != "" {
			reasoningTextBlock.Signature = aws.String(msg.ReasoningSignature)
		}
		blocks = append(blocks, &types.ContentBlockMemberReasoningContent{
			Value: &types.ReasoningContentBlockMemberReasoningText{
				Value: reasoningTextBlock,
			},
		})
	}

	if msg.Content != "" {
		blocks = append(blocks, &types.ContentBlockMemberText{Value: msg.Content})
	}

	for _, part := range msg.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil && *part.Text != "" {
				blocks = append(blocks, &types.ContentBlockMemberText{Value: *part.Text})
			}
		default:
			return nil, fmt.Errorf("bedrock: unsupported content part type %q in assistant message", part.Type)
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

	return blocks, nil
}

// convertImageToBlock converts image data to a Bedrock image block.
// Returns an error if the image uses a URL source (not supported by Bedrock) or has no data.
func convertImageToBlock(img *model.Image) (types.ContentBlock, error) {
	if img == nil {
		return nil, errors.New("bedrock: image content part has nil image data")
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
		}, nil
	}

	if img.URL != "" {
		return nil, fmt.Errorf("bedrock: URL-based images are not supported, please provide image data directly (url: %s)", img.URL)
	}

	return nil, errors.New("bedrock: image content part has neither data nor URL")
}

// convertFileToBlock converts file data to a Bedrock document block.
func convertFileToBlock(file *model.File) (types.ContentBlock, error) {
	if file == nil {
		return nil, errors.New("bedrock: file content part has nil file data")
	}
	if len(file.Data) == 0 && file.FileID == "" {
		return nil, errors.New("bedrock: file content part has neither data nor file ID")
	}
	if len(file.Data) == 0 {
		return nil, fmt.Errorf("bedrock: file ID-based files are not supported, please provide file data directly (file_id: %s)", file.FileID)
	}

	format := inferDocumentFormatFromMimeType(file.MimeType)
	name := file.Name
	if name == "" {
		name = "file." + format
	}
	return &types.ContentBlockMemberDocument{
		Value: types.DocumentBlock{
			Format: types.DocumentFormat(format),
			Name:   aws.String(name),
			Source: &types.DocumentSourceMemberBytes{
				Value: file.Data,
			},
		},
	}, nil
}

// inferDocumentFormatFromMimeType infers the Bedrock document format from a MIME type.
// Covers all DocumentFormat enum values: pdf, csv, doc, docx, xls, xlsx, html, txt, md.
func inferDocumentFormatFromMimeType(mimeType string) string {
	switch strings.ToLower(mimeType) {
	case "application/pdf":
		return "pdf"
	case "text/csv":
		return "csv"
	case "application/msword":
		return "doc"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return "docx"
	case "application/vnd.ms-excel":
		return "xls"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return "xlsx"
	case "text/html":
		return "html"
	case "text/plain":
		return "txt"
	case "text/markdown":
		return "md"
	default:
		// Try to extract format from mime type (e.g., "application/pdf" -> "pdf")
		if idx := strings.LastIndex(mimeType, "/"); idx >= 0 {
			suffix := mimeType[idx+1:]
			if suffix != "" {
				return suffix
			}
		}
		return "txt"
	}
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

// buildAdditionalModelRequestFields builds the AdditionalModelRequestFields for
// thinking/reasoning configuration. Bedrock models (e.g. Claude) use this field
// to enable extended thinking and set reasoning effort.
//
// The mapping follows the Bedrock Converse API specification:
//   - ThinkingEnabled=true  → {"thinking": {"type": "enabled", "budget_tokens": N}}
//   - ThinkingEnabled=false → {"thinking": {"type": "disabled"}}
//   - ReasoningEffort       → {"reasoning_effort": "<value>"}
//
// Returns nil if no thinking/reasoning fields are configured.
func buildAdditionalModelRequestFields(config model.GenerationConfig) document.Interface {
	fields := make(map[string]any)

	// Map ThinkingEnabled and ThinkingTokens to the "thinking" object.
	if config.ThinkingEnabled != nil {
		if *config.ThinkingEnabled {
			thinking := map[string]any{
				"type": "enabled",
			}
			if config.ThinkingTokens != nil && *config.ThinkingTokens > 0 {
				thinking["budget_tokens"] = *config.ThinkingTokens
			}
			fields["thinking"] = thinking
		} else {
			fields["thinking"] = map[string]any{
				"type": "disabled",
			}
		}
	}

	// Map ReasoningEffort to the "reasoning_effort" field.
	if config.ReasoningEffort != nil && *config.ReasoningEffort != "" {
		fields["reasoning_effort"] = *config.ReasoningEffort
	}

	if len(fields) == 0 {
		return nil
	}
	return document.NewLazyDocument(fields)
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
			Description: aws.String(buildToolDescription(declaration)),
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

// buildToolDescription builds the description for a tool.
// When OutputSchema is present, it appends the serialized output schema to the
// description so the model knows the expected result structure.
func buildToolDescription(declaration *tool.Declaration) string {
	desc := declaration.Description
	if declaration.OutputSchema == nil {
		return desc
	}
	schemaJSON, err := json.Marshal(declaration.OutputSchema)
	if err != nil {
		log.Errorf("marshal output schema for tool %s: %v", declaration.Name, err)
		return desc
	}
	desc += "\nOutput schema: " + string(schemaJSON)
	return desc
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

// schemaToMap converts tool.Schema to a map via JSON round-trip, preserving all
// JSON Schema fields (including $ref, $defs, etc.) that the hand-written
// whitelist approach would miss.
func schemaToMap(schema *tool.Schema) map[string]any {
	if schema == nil {
		return map[string]any{"type": "object"}
	}

	data, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{"type": "object"}
	}

	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return map[string]any{"type": "object"}
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
	// Try non-blocking send first to avoid dropping error responses when context is cancelled
	// but the buffered channel still has capacity.
	select {
	case responseChan <- errorResponse:
		return
	default:
	}
	// If channel is full, wait for either send or context cancellation.
	select {
	case responseChan <- errorResponse:
	case <-ctx.Done():
	}
}
