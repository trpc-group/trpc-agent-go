//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package anthropic provides Anthropic-compatible model implementations.
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
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const functionToolType = "function"

// Model implements the model.Model interface for Anthropic API.
type Model struct {
	client                     anthropic.Client
	name                       string
	baseURL                    string
	apiKey                     string
	channelBufferSize          int
	anthropicRequestOptions    []option.RequestOption
	chatRequestCallback        ChatRequestCallbackFunc
	chatResponseCallback       ChatResponseCallbackFunc
	chatChunkCallback          ChatChunkCallbackFunc
	chatStreamCompleteCallback ChatStreamCompleteCallbackFunc
	enableTokenTailoring       bool                    // Enable automatic token tailoring.
	maxInputTokens             int                     // Max input tokens for token tailoring.
	tokenCounter               model.TokenCounter      // Token counter for token tailoring.
	tailoringStrategy          model.TailoringStrategy // Tailoring strategy for token tailoring.
	// Token tailoring budget parameters (instance-level overrides).
	protocolOverheadTokens int
	reserveOutputTokens    int
	inputTokensFloor       int
	outputTokensFloor      int
	safetyMarginRatio      float64
	maxInputTokensRatio    float64
}

// New creates a new Anthropic model adapter.
func New(name string, opts ...Option) *Model {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}

	var clientOpts []option.RequestOption
	if o.apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(o.apiKey))
	}
	if o.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(o.baseURL))
	}
	clientOpts = append(clientOpts, option.WithHTTPClient(model.DefaultNewHTTPClient(o.httpClientOptions...)))
	clientOpts = append(clientOpts, o.anthropicClientOptions...)
	client := anthropic.NewClient(clientOpts...)

	if o.tailoringStrategy == nil {
		o.tailoringStrategy = model.NewMiddleOutStrategy(o.tokenCounter)
	}

	return &Model{
		client:                     client,
		name:                       name,
		baseURL:                    o.baseURL,
		apiKey:                     o.apiKey,
		channelBufferSize:          o.channelBufferSize,
		anthropicRequestOptions:    o.anthropicRequestOptions,
		chatRequestCallback:        o.chatRequestCallback,
		chatResponseCallback:       o.chatResponseCallback,
		chatChunkCallback:          o.chatChunkCallback,
		chatStreamCompleteCallback: o.chatStreamCompleteCallback,
		enableTokenTailoring:       o.enableTokenTailoring,
		tokenCounter:               o.tokenCounter,
		tailoringStrategy:          o.tailoringStrategy,
		maxInputTokens:             o.maxInputTokens,
		protocolOverheadTokens:     o.tokenTailoringConfig.ProtocolOverheadTokens,
		reserveOutputTokens:        o.tokenTailoringConfig.ReserveOutputTokens,
		inputTokensFloor:           o.tokenTailoringConfig.InputTokensFloor,
		outputTokensFloor:          o.tokenTailoringConfig.OutputTokensFloor,
		safetyMarginRatio:          o.tokenTailoringConfig.SafetyMarginRatio,
		maxInputTokensRatio:        o.tokenTailoringConfig.MaxInputTokensRatio,
	}
}

// Info returns the model information.
func (m *Model) Info() model.Info {
	return model.Info{
		Name: m.name,
	}
}

// GenerateContent generates content from the model.
func (m *Model) GenerateContent(
	ctx context.Context,
	request *model.Request,
) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("request cannot be nil")
	}

	// Apply token tailoring if configured.
	m.applyTokenTailoring(ctx, request)

	chatRequest, err := m.buildChatRequest(request)
	if err != nil {
		return nil, fmt.Errorf("build chat request: %w", err)
	}
	// Send chat request and handle response.
	responseChan := make(chan *model.Response, m.channelBufferSize)
	go func() {
		defer close(responseChan)
		if m.chatRequestCallback != nil {
			m.chatRequestCallback(ctx, chatRequest)
		}
		if request.Stream {
			m.handleStreamingResponse(ctx, *chatRequest, responseChan)
			return
		}
		m.handleNonStreamingResponse(ctx, *chatRequest, responseChan)
	}()
	return responseChan, nil
}

// applyTokenTailoring performs best-effort token tailoring if configured.
// It uses the token tailoring strategy defined in imodel package.
func (m *Model) applyTokenTailoring(ctx context.Context, request *model.Request) {
	// Early return if token tailoring is disabled or no messages to process.
	if !m.enableTokenTailoring || len(request.Messages) == 0 {
		return
	}

	// Determine max input tokens using priority: user config > auto calculation > default.
	maxInputTokens := m.maxInputTokens
	if maxInputTokens <= 0 {
		// Auto-calculate based on model context window with custom or default parameters.
		contextWindow := imodel.ResolveContextWindow(m.name)
		if m.protocolOverheadTokens > 0 || m.reserveOutputTokens > 0 {
			// Use custom parameters if any are set.
			maxInputTokens = imodel.CalculateMaxInputTokensWithParams(
				contextWindow,
				m.protocolOverheadTokens,
				m.reserveOutputTokens,
				m.inputTokensFloor,
				m.safetyMarginRatio,
				m.maxInputTokensRatio,
			)
		} else {
			// Use default parameters.
			maxInputTokens = imodel.CalculateMaxInputTokens(contextWindow)
		}
		log.DebugfContext(
			ctx,
			"auto-calculated max input tokens: model=%s, "+
				"contextWindow=%d, maxInputTokens=%d",
			m.name,
			contextWindow,
			maxInputTokens,
		)
	}

	// Apply token tailoring.
	tailored, err := m.tailoringStrategy.TailorMessages(ctx, request.Messages, maxInputTokens)
	if err != nil {
		log.WarnContext(
			ctx,
			"token tailoring failed in anthropic.Model",
			err,
		)
		return
	}

	request.Messages = tailored

	// Calculate remaining tokens for output based on context window.
	usedTokens, err := m.tokenCounter.CountTokensRange(ctx, request.Messages, 0, len(request.Messages))
	if err != nil {
		log.WarnContext(
			ctx,
			"failed to count tokens after tailoring",
			err,
		)
		return
	}

	// Set max output tokens only if user hasn't specified it.
	// This respects user's explicit configuration while providing a safe default.
	if request.GenerationConfig.MaxTokens == nil {
		contextWindow := imodel.ResolveContextWindow(m.name)
		var maxOutputTokens int
		if m.protocolOverheadTokens > 0 || m.outputTokensFloor > 0 {
			// Use custom parameters if any are set.
			maxOutputTokens = imodel.CalculateMaxOutputTokensWithParams(
				contextWindow,
				usedTokens,
				m.protocolOverheadTokens,
				m.outputTokensFloor,
				m.safetyMarginRatio,
			)
		} else {
			// Use default parameters.
			maxOutputTokens = imodel.CalculateMaxOutputTokens(contextWindow, usedTokens)
		}
		if maxOutputTokens > 0 {
			request.GenerationConfig.MaxTokens = &maxOutputTokens
			log.DebugfContext(
				ctx,
				"token tailoring: contextWindow=%d, usedTokens=%d, "+
					"maxOutputTokens=%d",
				contextWindow,
				usedTokens,
				maxOutputTokens,
			)
		}
	}
}

// buildChatRequest builds the chat request for the Anthropic API.
func (m *Model) buildChatRequest(request *model.Request) (*anthropic.MessageNewParams, error) {
	// Convert messages to Anthropic format.
	messages, systemPrompts, err := convertMessages(request.Messages)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("request must include at least one message")
	}
	// Build chat request.
	chatRequest := &anthropic.MessageNewParams{
		Model:    anthropic.Model(m.name),
		Messages: messages,
		Tools:    convertTools(request.Tools),
	}
	if len(systemPrompts) > 0 {
		chatRequest.System = systemPrompts
	}
	if request.MaxTokens != nil {
		chatRequest.MaxTokens = int64(*request.MaxTokens)
	}
	if request.Temperature != nil {
		chatRequest.Temperature = anthropic.Float(*request.Temperature)
	}
	if request.TopP != nil {
		chatRequest.TopP = anthropic.Float(*request.TopP)
	}
	if len(request.Stop) > 0 {
		chatRequest.StopSequences = append(chatRequest.StopSequences, request.Stop...)
	}
	if request.ThinkingEnabled != nil && *request.ThinkingEnabled && request.ThinkingTokens != nil {
		chatRequest.Thinking = anthropic.ThinkingConfigParamOfEnabled(int64(*request.ThinkingTokens))
	}
	return chatRequest, nil
}

// handleNonStreamingResponse sends a non-streaming request to the Anthropic API and emits exactly one final response.
func (m *Model) handleNonStreamingResponse(
	ctx context.Context,
	chatRequest anthropic.MessageNewParams,
	responseChan chan<- *model.Response,
) {
	// Issue non-streaming request.
	message, err := m.client.Messages.New(ctx, chatRequest, m.anthropicRequestOptions...)
	if err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeAPIError, err)
		return
	}
	if m.chatResponseCallback != nil {
		m.chatResponseCallback(ctx, &chatRequest, message)
	}
	// Build final response payload.
	now := time.Now()
	response := &model.Response{
		ID:        message.ID,
		Object:    model.ObjectTypeChatCompletion,
		Created:   now.Unix(),
		Model:     string(message.Model),
		Timestamp: now,
		Done:      true,
	}
	// Convert assistant content blocks.
	assistantMessage := convertContentBlock(message.Content)
	response.Choices = []model.Choice{
		{
			Index:   0,
			Message: assistantMessage,
		},
	}
	// Set finish reason.
	if finishReason := strings.TrimSpace(string(message.StopReason)); finishReason != "" {
		response.Choices[0].FinishReason = &finishReason
	}
	// Set usage.
	if message.Usage.InputTokens > 0 || message.Usage.OutputTokens > 0 {
		response.Usage = &model.Usage{
			PromptTokens:     int(message.Usage.InputTokens),
			CompletionTokens: int(message.Usage.OutputTokens),
			TotalTokens:      int(message.Usage.InputTokens + message.Usage.OutputTokens),
		}
	}
	// Emit final response.
	select {
	case responseChan <- response:
	case <-ctx.Done():
	}
}

// handleStreamingResponse sends a streaming request to the Anthropic API and emits partial deltas
// followed by a final response.
func (m *Model) handleStreamingResponse(
	ctx context.Context,
	chatRequest anthropic.MessageNewParams,
	responseChan chan<- *model.Response,
) {
	// Issue streaming request.
	stream := m.client.Messages.NewStreaming(ctx, chatRequest, m.anthropicRequestOptions...)
	defer stream.Close()
	// Accumulator to build final response.
	acc := anthropic.Message{}
	for stream.Next() {
		chunk := stream.Current()
		// Accumulate into accumulator.
		if err := acc.Accumulate(chunk); err != nil {
			m.sendErrorResponse(ctx, responseChan, model.ErrorTypeStreamError, err)
			return
		}
		if m.chatChunkCallback != nil {
			m.chatChunkCallback(ctx, &chatRequest, &chunk)
		}
		// Build partial response.
		response, err := buildStreamingPartialResponse(acc, chunk)
		if err != nil {
			m.sendErrorResponse(ctx, responseChan, model.ErrorTypeStreamError, err)
			return
		}
		if response == nil {
			continue
		}
		// Emit partial response.
		select {
		case responseChan <- response:
		case <-ctx.Done():
			return
		}
	}
	// Propagate stream error.
	if err := stream.Err(); err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeStreamError, err)
		return
	}
	// Emit final response built from the accumulator.
	finalResponse := buildStreamingFinalResponse(acc)
	select {
	case responseChan <- finalResponse:
	case <-ctx.Done():
	}
	// Call the stream complete callback after final response is sent.
	if m.chatStreamCompleteCallback != nil {
		var callbackAcc *anthropic.Message
		if stream.Err() == nil {
			callbackAcc = &acc
		}
		m.chatStreamCompleteCallback(ctx, &chatRequest, callbackAcc, stream.Err())
	}
}

// buildStreamingPartialResponse builds a partial streaming response for a chunk.
// Returns nil if the chunk should be skipped.
func buildStreamingPartialResponse(acc anthropic.Message,
	chunk anthropic.MessageStreamEventUnion) (*model.Response, error) {
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
				Delta: model.Message{Role: model.RoleAssistant},
			},
		},
	}
	// Branch by event type.
	switch event := chunk.AsAny().(type) {
	case anthropic.ContentBlockDeltaEvent:
		switch delta := event.Delta.AsAny().(type) {
		case anthropic.TextDelta:
			if delta.Text == "" {
				return nil, nil
			}
			response.Choices[0].Delta.Content = delta.Text
		case anthropic.ThinkingDelta:
			if delta.Thinking == "" {
				return nil, nil
			}
			response.Choices[0].Delta.ReasoningContent = delta.Thinking
		default:
			return nil, nil
		}
	case anthropic.MessageDeltaEvent:
		if event.Delta.StopReason == "" {
			return nil, nil
		}
		finishReason := string(event.Delta.StopReason)
		response.Choices[0].FinishReason = &finishReason
	default:
		return nil, nil
	}
	return response, nil
}

// buildStreamingFinalResponse builds a final streaming response from the accumulator.
func buildStreamingFinalResponse(acc anthropic.Message) *model.Response {
	var (
		accumulatedToolCalls []model.ToolCall
		accumulatedContent   string
		accumulatedReasoning string
		index                int
	)
	// Aggregate all blocks into final assistant message.
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
			accumulatedReasoning += block.Thinking
		}
	}
	// Build final response.
	now := time.Now()
	return &model.Response{
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
					ReasoningContent: accumulatedReasoning,
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
}

// sendErrorResponse sends an error response through the channel.
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

// convertContentBlock builds a single assistant message from Anthropic content blocks.
func convertContentBlock(contents []anthropic.ContentBlockUnion) model.Message {
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

// convertTools maps our tool declarations to Anthropic tool parameters.
func convertTools(tools map[string]tool.Tool) []anthropic.ToolUnionParam {
	var result []anthropic.ToolUnionParam
	for _, tool := range tools {
		declaration := tool.Declaration()
		result = append(result, anthropic.ToolUnionParam{
			OfTool: &anthropic.ToolParam{
				Name:        declaration.Name,
				Description: anthropic.String(buildToolDescription(declaration)),
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

// buildToolDescription builds the description for a tool.
// It appends the output schema to the description.
func buildToolDescription(declaration *tool.Declaration) string {
	desc := declaration.Description
	if declaration.OutputSchema == nil {
		return desc
	}
	schemaJSON, err := json.Marshal(declaration.OutputSchema)
	if err != nil {
		log.Debugf("marshal output schema for tool %s: %v", declaration.Name, err)
		return desc
	}
	desc += "Output schema: " + string(schemaJSON)
	return desc
}

// convertMessages builds Anthropic message parameters and system prompts from trpc-agent-go messages.
// Merges consecutive tool results into a single user message and drops empty-content messages.
func convertMessages(messages []model.Message) ([]anthropic.MessageParam, []anthropic.TextBlockParam, error) {
	// Convert messages by role and collect system prompts.
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
	// Merge consecutive tool result messages into a single user message to support parallel tool invocation.
	mergedConversation := conversation[:0]
	isToolResult := func(message anthropic.MessageParam) bool {
		return len(message.Content) > 0 &&
			message.Content[0].OfToolResult != nil &&
			!param.IsOmitted(message.Content[0].OfToolResult)
	}
	for l, r := 0, -1; l < len(conversation); l = r + 1 {
		// Skip empty content messages.
		if len(conversation[l].Content) == 0 {
			r++
			continue
		}
		// Forward non-tool result messages.
		if !isToolResult(conversation[l]) {
			mergedConversation = append(mergedConversation, conversation[l])
			r++
			continue
		}
		// Gather contiguous tool results and wrap into a single user message to support parallel tool invocation.
		blocks := make([]anthropic.ContentBlockParamUnion, 0, len(conversation[l].Content))
		for r+1 < len(conversation) && isToolResult(conversation[r+1]) {
			toolResult := conversation[r+1].Content[0].OfToolResult
			blocks = append(blocks, anthropic.NewToolResultBlock(
				toolResult.ToolUseID,
				toolResult.Content[0].OfText.Text,
				toolResult.IsError.Value,
			))
			r++
		}
		mergedConversation = append(mergedConversation, anthropic.NewUserMessage(blocks...))
	}
	return mergedConversation, systemPrompts, nil
}

// convertUserMessage converts a user message by keeping only supported text parts.
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

// convertAssistantMessageContent converts an assistant message including tool calls into Anthropic format.
func convertAssistantMessageContent(message model.Message) anthropic.MessageParam {
	// Append text blocks.
	blocks := make([]anthropic.ContentBlockParamUnion, 0, 1+len(message.ContentParts)+len(message.ToolCalls))
	if message.Content != "" {
		blocks = append(blocks, anthropic.NewTextBlock(message.Content))
	}
	for _, part := range message.ContentParts {
		if part.Type == model.ContentTypeText && part.Text != nil {
			blocks = append(blocks, anthropic.NewTextBlock(*part.Text))
		}
	}
	// Append tool use blocks.
	for _, toolCall := range message.ToolCalls {
		toolUse := anthropic.NewToolUseBlock(
			toolCall.ID,
			decodeToolArguments(toolCall.Function.Arguments),
			toolCall.Function.Name,
		)
		blocks = append(blocks, toolUse)
	}
	return anthropic.NewAssistantMessage(blocks...)
}

// decodeToolArguments parses JSON bytes into any, returning an empty object on failure.
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

// convertToolResult wraps a tool result into a user message with a ToolResult block.
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
