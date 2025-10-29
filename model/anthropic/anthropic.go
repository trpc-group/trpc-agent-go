//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package anthropic provides Anthropic model implementations.
package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	anthropic "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultChannelBufferSize = 256
	functionToolType         = "function"
)

// Option configures the Anthropic model adapter.
type Option func(*options)

type options struct {
	channelBufferSize int
	clientOptions     []option.RequestOption
	requestOptions    []option.RequestOption
}

// WithAPIKey sets the API key for Anthropic.
func WithAPIKey(key string) Option {
	return func(o *options) {
		if key == "" {
			return
		}
		o.clientOptions = append(o.clientOptions, option.WithAPIKey(key))
	}
}

// WithBaseURL sets the base URL for Anthropic API requests.
func WithBaseURL(url string) Option {
	return func(o *options) {
		if url == "" {
			return
		}
		o.clientOptions = append(o.clientOptions, option.WithBaseURL(url))
	}
}

// WithChannelBufferSize overrides the response channel buffer size.
func WithChannelBufferSize(size int) Option {
	return func(o *options) {
		if size <= 0 {
			size = defaultChannelBufferSize
		}
		o.channelBufferSize = size
	}
}

// WithClientOptions forwards custom request options to the Anthropic client.
func WithClientOptions(opts ...option.RequestOption) Option {
	return func(o *options) {
		o.clientOptions = append(o.clientOptions, opts...)
	}
}

// WithRequestOptions adds per-request options for Anthropic calls.
func WithRequestOptions(opts ...option.RequestOption) Option {
	return func(o *options) {
		o.requestOptions = append(o.requestOptions, opts...)
	}
}

// Model implements model.Model using anthropic-sdk-go.
type Model struct {
	name              string
	client            anthropic.Client
	channelBufferSize int
	requestOptions    []option.RequestOption
}

// New creates a new Anthropic model adapter.
func New(name string, opts ...Option) *Model {
	o := &options{
		channelBufferSize: defaultChannelBufferSize,
	}
	for _, opt := range opts {
		opt(o)
	}

	client := anthropic.NewClient(o.clientOptions...)

	return &Model{
		name:              name,
		client:            client,
		channelBufferSize: o.channelBufferSize,
		requestOptions:    o.requestOptions,
	}
}

// Info implements the model.Model interface.
func (m *Model) Info() model.Info {
	return model.Info{
		Name: m.name,
	}
}

// GenerateContent implements the model.Model interface.
func (m *Model) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("request cannot be nil")
	}

	messages, systemPrompts, err := convertMessages(request.Messages)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("request must include at least one supported message")
	}

	params := anthropic.MessageNewParams{
		Model:    anthropic.Model(m.name),
		Messages: messages,
		Tools:    convertTools(request.Tools),
	}

	if len(systemPrompts) > 0 {
		params.System = systemPrompts
	}
	if request.MaxTokens != nil {
		params.MaxTokens = int64(*request.MaxTokens)
	}
	if request.Temperature != nil {
		params.Temperature = anthropic.Float(*request.Temperature)
	}
	if request.TopP != nil {
		params.TopP = anthropic.Float(*request.TopP)
	}
	if len(request.Stop) > 0 {
		params.StopSequences = append(params.StopSequences, request.Stop...)
	}

	responseChan := make(chan *model.Response, m.channelBufferSize)

	go func() {
		defer close(responseChan)
		if request.Stream {
			m.handleStreamingResponse(ctx, params, responseChan)
			return
		}
		m.handleNonStreamingResponse(ctx, params, responseChan)
	}()

	return responseChan, nil
}

func (m *Model) handleNonStreamingResponse(
	ctx context.Context,
	body anthropic.MessageNewParams,
	responseChan chan<- *model.Response,
) {
	message, err := m.client.Messages.New(ctx, body, m.requestOptions...)
	if err != nil {
		errorResponse := &model.Response{
			Error: &model.ResponseError{
				Message: err.Error(),
				Type:    model.ErrorTypeAPIError,
			},
			Timestamp: time.Now(),
			Done:      true,
		}

		select {
		case responseChan <- errorResponse:
		case <-ctx.Done():
		}
		return
	}

	now := time.Now()
	response := &model.Response{
		ID:        message.ID,
		Object:    model.ObjectTypeChatCompletion,
		Created:   now.Unix(),
		Model:     string(message.Model),
		Timestamp: now,
		Done:      true,
	}

	assistantMessage := convertMessage(message.Content)
	response.Choices = []model.Choice{
		{
			Index:   0,
			Message: assistantMessage,
		},
	}

	// Handle finish reason - FinishReason is a plain string.
	if finishReason := strings.TrimSpace(string(message.StopReason)); finishReason != "" {
		response.Choices[0].FinishReason = &finishReason
	}

	// Convert usage information.
	if message.Usage.InputTokens > 0 || message.Usage.OutputTokens > 0 {
		response.Usage = &model.Usage{
			PromptTokens:     int(message.Usage.InputTokens),
			CompletionTokens: int(message.Usage.OutputTokens),
			TotalTokens:      int(message.Usage.InputTokens + message.Usage.OutputTokens),
		}
	}

	select {
	case responseChan <- response:
	case <-ctx.Done():
	}
}

func (m *Model) handleStreamingResponse(
	ctx context.Context,
	params anthropic.MessageNewParams,
	responseChan chan<- *model.Response,
) {
	stream := m.client.Messages.NewStreaming(ctx, params, m.requestOptions...)
	defer stream.Close()

	acc := anthropic.Message{}

	for stream.Next() {
		chunk := stream.Current()

		if err := acc.Accumulate(chunk); err != nil {
			m.sendErrorResponse(ctx, responseChan, err)
			return
		}

		if shouldSuppressChunk(chunk) {
			continue
		}

		now := time.Now()
		response := &model.Response{
			ID:        acc.ID,
			Object:    model.ObjectTypeChatCompletionChunk,
			Created:   now.Unix(),
			Model:     string(acc.Model),
			Timestamp: now,
			Done:      false,
			IsPartial: true,
			Choices: []model.Choice{
				{
					Delta: model.Message{
						Role: model.RoleAssistant,
					},
				},
			},
		}
		switch event := chunk.AsAny().(type) {
		case anthropic.ContentBlockDeltaEvent:
			switch delta := event.Delta.AsAny().(type) {
			case anthropic.TextDelta:
				response.Choices[0].Delta.Content = delta.Text
			case anthropic.ThinkingDelta:
				response.Choices[0].Delta.ReasoningContent = delta.Thinking
			default:
				m.sendErrorResponse(ctx, responseChan, fmt.Errorf("unexpected delta type: %T", delta))
				return
			}
		case anthropic.MessageDeltaEvent:
			finishReason := string(event.Delta.StopReason)
			response.Choices[0].FinishReason = &finishReason
		default:
			m.sendErrorResponse(ctx, responseChan, fmt.Errorf("unexpected chunk type: %T", chunk))
			return
		}
		select {
		case responseChan <- response:
		case <-ctx.Done():
			return
		}
	}

	if err := stream.Err(); err != nil {
		// Send error response.
		errorResponse := &model.Response{
			Error: &model.ResponseError{
				Message: stream.Err().Error(),
				Type:    model.ErrorTypeStreamError,
			},
			Timestamp: time.Now(),
			Done:      true,
		}

		select {
		case responseChan <- errorResponse:
		case <-ctx.Done():
		}
		return
	}

	// Check accumulated tool calls (batch processing after streaming is complete).
	var accumulatedToolCalls []model.ToolCall
	accumulatedContent := ""
	accumulatedReasoningContent := ""

	index := 0
	for _, content := range acc.Content {
		switch block := content.AsAny().(type) {
		case anthropic.ToolUseBlock:
			accumulatedToolCalls = append(accumulatedToolCalls, model.ToolCall{
				Index: func() *int { idx := index; index++; return &idx }(),
				Type:  functionToolType,
				ID:    block.ID,
				Function: model.FunctionDefinitionParam{
					Name:      block.Name,
					Arguments: block.Input,
				},
			})
		case anthropic.TextBlock:
			accumulatedContent += block.Text
		case anthropic.ThinkingBlock:
			accumulatedReasoningContent += block.Thinking
		}
	}

	now := time.Now()
	finalResponse := &model.Response{
		Object:  model.ObjectTypeChatCompletion,
		ID:      acc.ID,
		Created: now.Unix(),
		Model:   string(acc.Model),
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:             model.RoleAssistant,
					Content:          accumulatedContent,
					ReasoningContent: accumulatedReasoningContent,
					ToolCalls:        accumulatedToolCalls,
				},
			},
		},
		Usage: &model.Usage{
			PromptTokens:     int(acc.Usage.InputTokens),
			CompletionTokens: int(acc.Usage.OutputTokens),
			TotalTokens:      int(acc.Usage.InputTokens + acc.Usage.OutputTokens),
		},
		Timestamp: now,
		Done:      true,
		IsPartial: false,
	}

	select {
	case responseChan <- finalResponse:
	case <-ctx.Done():
	}
}

func (m *Model) sendErrorResponse(ctx context.Context, responseChan chan<- *model.Response, err error) {
	errorResponse := &model.Response{
		Error: &model.ResponseError{
			Message: err.Error(),
			Type:    model.ErrorTypeAPIError,
		},
		Timestamp: time.Now(),
		Done:      true,
	}
	select {
	case responseChan <- errorResponse:
	case <-ctx.Done():
	}
}

func shouldSuppressChunk(chunk anthropic.MessageStreamEventUnion) bool {
	switch event := chunk.AsAny().(type) {
	case anthropic.MessageDeltaEvent:
		return event.Delta.StopReason == ""
	case anthropic.MessageStartEvent, anthropic.MessageStopEvent:
		return true
	case anthropic.ContentBlockStartEvent, anthropic.ContentBlockStopEvent:
		return true
	case anthropic.ContentBlockDeltaEvent:
		switch delta := event.Delta.AsAny().(type) {
		case anthropic.TextDelta:
			return delta.Text == ""
		case anthropic.ThinkingDelta:
			return delta.Thinking == ""
		case anthropic.InputJSONDelta:
			return true
		default:
			return true
		}
	default:
		return true
	}
}

func convertMessage(contents []anthropic.ContentBlockUnion) model.Message {
	var (
		textBuilder      strings.Builder
		reasoningBuilder strings.Builder
		toolCalls        []model.ToolCall
	)
	for _, content := range contents {
		switch block := content.AsAny().(type) {
		case anthropic.TextBlock:
			textBuilder.WriteString(block.Text)
		case anthropic.ThinkingBlock:
			reasoningBuilder.WriteString(block.Thinking)
		case anthropic.ToolUseBlock:
			toolCall := model.ToolCall{
				Type: functionToolType,
				ID:   block.ID,
				Function: model.FunctionDefinitionParam{
					Name:      block.Name,
					Arguments: block.Input,
				},
			}
			toolCalls = append(toolCalls, toolCall)
		}
	}
	return model.Message{
		Role:             model.RoleAssistant,
		Content:          textBuilder.String(),
		ReasoningContent: reasoningBuilder.String(),
		ToolCalls:        toolCalls,
	}
}

func convertTools(tools map[string]tool.Tool) []anthropic.ToolUnionParam {
	var result []anthropic.ToolUnionParam
	for _, tool := range tools {
		declaration := tool.Declaration()
		result = append(result, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        declaration.Name,
				Description: anthropic.String(declaration.Description),
				InputSchema: anthropic.ToolInputSchemaParam{
					Type:       constant.Object(declaration.InputSchema.Type),
					Properties: declaration.InputSchema.Properties,
					Required:   declaration.InputSchema.Required,
				},
			},
		})
	}
	return result
}

func convertMessages(messages []model.Message) (
	[]anthropic.MessageParam,
	[]anthropic.TextBlockParam,
	error,
) {
	conversation := make([]anthropic.MessageParam, 0, len(messages))
	systemPrompts := make([]anthropic.TextBlockParam, 0)
	for _, message := range messages {
		switch message.Role {
		case model.RoleSystem:
			systemPrompts = append(systemPrompts, convertSystemMessageContent(message)...)
		case model.RoleAssistant:
			conversation = append(conversation, convertAssistantMessageContent(message))
		case model.RoleTool:
			conversation = append(conversation, convertToolResult(message))
		case model.RoleUser:
			conversation = append(conversation, convertUserMessage(message))
		default:
			conversation = append(conversation, convertUserMessage(message))
		}
	}
	uniqueConversation := conversation[:0]
	isToolResult := func(toolResult *anthropic.ToolResultBlockParam) bool {
		return toolResult != nil && !param.IsOmitted(toolResult)
	}
	for l, r := 0, -1; l < len(conversation); l = r + 1 {
		if !isToolResult(conversation[l].Content[0].OfToolResult) {
			uniqueConversation = append(uniqueConversation, conversation[l])
			r = l
			continue
		}
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(conversation[l].Content))
		for r+1 < len(conversation) && isToolResult(conversation[r+1].Content[0].OfToolResult) {
			toolResult := conversation[r+1].Content[0].OfToolResult
			blocks = append(blocks, anthropic.NewToolResultBlock(toolResult.ToolUseID, toolResult.Content[0].OfText.Text, toolResult.IsError.Value))
			r++
		}
		uniqueConversation = append(uniqueConversation, anthropic.NewUserMessage(blocks...))
	}
	return uniqueConversation, systemPrompts, nil
}

func convertUserMessage(message model.Message) anthropic.MessageParam {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(message.ContentParts))
	if message.Content != "" {
		blocks = append(blocks, anthropic.NewTextBlock(message.Content))
	}
	for _, part := range message.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			blocks = append(blocks, anthropic.NewTextBlock(*part.Text))
		}
	}
	return anthropic.NewUserMessage(blocks...)
}

func convertAssistantMessageContent(message model.Message) anthropic.MessageParam {
	blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(message.ContentParts)+len(message.ToolCalls))
	if message.Content != "" {
		blocks = append(blocks, anthropic.NewTextBlock(message.Content))
	}
	for _, part := range message.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			blocks = append(blocks, anthropic.NewTextBlock(*part.Text))
		}
	}
	for _, toolCall := range message.ToolCalls {
		toolUse := anthropic.NewToolUseBlock(toolCall.ID, decodeToolArguments(toolCall.Function.Arguments), toolCall.Function.Name)
		blocks = append(blocks, toolUse)
	}
	return anthropic.NewAssistantMessage(blocks...)
}

func decodeToolArguments(args []byte) any {
	if len(args) == 0 {
		return map[string]any{}
	}
	var decoded any
	if err := json.Unmarshal(args, &decoded); err != nil {
		return map[string]any{}
	}
	return decoded
}

func convertToolResult(message model.Message) anthropic.MessageParam {
	return anthropic.NewUserMessage(anthropic.NewToolResultBlock(message.ToolID, message.Content, false))
}

// convertSystemMessageContent converts message content to system message content union.
func convertSystemMessageContent(message model.Message) []anthropic.TextBlockParam {
	blocks := make([]anthropic.TextBlockParam, 0, 1+len(message.ContentParts))
	if message.Content != "" {
		blocks = append(blocks, anthropic.TextBlockParam{
			Text: message.Content,
		})
	}
	for _, part := range message.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			blocks = append(blocks, anthropic.TextBlockParam{
				Text: *part.Text,
			})
		}
	}
	return blocks
}
