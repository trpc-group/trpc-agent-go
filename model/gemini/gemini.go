//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package gemini provides Gemini-compatible model implementations.
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Model implements the model.Model interface for Gemini API.
type Model struct {
	client                     Client
	name                       string
	channelBufferSize          int
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

// New creates a new Gemini-like model.
func New(ctx context.Context, name string, opts ...Option) (*Model, error) {
	o := defaultOptions
	for _, opt := range opts {
		opt(&o)
	}
	if o.tailoringStrategy == nil {
		o.tailoringStrategy = model.NewMiddleOutStrategy(o.tokenCounter)
	}
	client, err := genai.NewClient(ctx, o.geminiClientConfig)
	if err != nil {
		return nil, err
	}
	return &Model{
		client:                     &clientWrapper{client: client},
		name:                       name,
		protocolOverheadTokens:     o.tokenTailoringConfig.ProtocolOverheadTokens,
		reserveOutputTokens:        o.tokenTailoringConfig.ReserveOutputTokens,
		inputTokensFloor:           o.tokenTailoringConfig.InputTokensFloor,
		outputTokensFloor:          o.tokenTailoringConfig.OutputTokensFloor,
		safetyMarginRatio:          o.tokenTailoringConfig.SafetyMarginRatio,
		maxInputTokensRatio:        o.tokenTailoringConfig.MaxInputTokensRatio,
		maxInputTokens:             o.maxInputTokens,
		chatRequestCallback:        o.chatRequestCallback,
		chatResponseCallback:       o.chatResponseCallback,
		chatChunkCallback:          o.chatChunkCallback,
		chatStreamCompleteCallback: o.chatStreamCompleteCallback,
	}, nil
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
	// Apply token tailoring if configured.
	m.applyTokenTailoring(ctx, request)
	chatRequest := m.convertMessages(request.Messages)
	generateConfig := m.buildChatConfig(request)
	responseChan := make(chan *model.Response, m.channelBufferSize)
	go func() {
		defer close(responseChan)

		if m.chatRequestCallback != nil {
			m.chatRequestCallback(ctx, chatRequest)
		}

		if request.Stream {
			m.handleStreamingResponse(ctx, chatRequest, responseChan, generateConfig)
		} else {
			m.handleNonStreamingResponse(ctx, chatRequest, responseChan, generateConfig)
		}
	}()

	return responseChan, nil
}

// handleNonStreamingResponse handles non-streaming chat completion responses.
func (m *Model) handleNonStreamingResponse(
	ctx context.Context,
	chatRequest []*genai.Content,
	responseChan chan<- *model.Response,
	generateConfig *genai.GenerateContentConfig,
) {
	chatCompletion, err := m.client.Models().GenerateContent(
		ctx, m.name, chatRequest, generateConfig)
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
	// Call response callback on successful completion.
	if m.chatResponseCallback != nil {
		m.chatResponseCallback(ctx, chatRequest, generateConfig, chatCompletion)
	}
	response := m.buildResponse(chatCompletion)
	select {
	case responseChan <- response:
	case <-ctx.Done():
	}
}

// handleStreamingResponse handles streaming chat completion responses.
func (m *Model) handleStreamingResponse(
	ctx context.Context,
	chatRequest []*genai.Content,
	responseChan chan<- *model.Response,
	generateConfig *genai.GenerateContentConfig,
) {
	chatCompletion := m.client.Models().GenerateContentStream(
		ctx, m.name, chatRequest, generateConfig)
	acc := &Accumulator{}
	for chunk := range chatCompletion {
		response := m.buildResponse(chunk)
		acc.Accumulate(response)
		response.Object = model.ObjectTypeChatCompletionChunk
		response.IsPartial = true
		if m.chatChunkCallback != nil {
			m.chatChunkCallback(ctx, chatRequest, generateConfig, chunk)
		}
		if m.chatChunkCallback != nil {
			m.chatChunkCallback(ctx, chatRequest, generateConfig, chunk)
		}
		select {
		case responseChan <- response:
		case <-ctx.Done():
			return
		}
	}
	finalResponse := acc.BuildResponse()
	if m.chatStreamCompleteCallback != nil {
		m.chatStreamCompleteCallback(ctx, chatRequest, generateConfig, finalResponse)
	}
	select {
	case responseChan <- finalResponse:
	case <-ctx.Done():
		return
	}
}

// convertContentBlock builds a single assistant message from Gemini Candidate.
func (m *Model) convertContentBlock(candidates []*genai.Candidate) (model.Message, string) {
	var (
		textBuilder      strings.Builder
		reasoningBuilder strings.Builder
		toolCalls        []model.ToolCall
		finishReason     string
	)
	for _, candidate := range candidates {
		if candidate.FinishReason != "" {
			finishReason = string(candidate.FinishReason)
		}
		if candidate.Content != nil {
			for _, part := range candidate.Content.Parts {
				if len(part.Text) == 0 {
					continue
				}
				if part.Thought {
					reasoningBuilder.WriteString(part.Text)
				} else {
					textBuilder.WriteString(part.Text)
				}
				if part.FunctionCall != nil {
					args, _ := json.Marshal(part.FunctionCall.Args)
					toolCalls = append(toolCalls, model.ToolCall{
						ID: part.FunctionCall.ID,
						Function: model.FunctionDefinitionParam{
							Name:      part.FunctionCall.Name,
							Arguments: args,
						},
					})
				}
			}
		}
	}
	return model.Message{
		Role:             model.RoleAssistant,
		Content:          textBuilder.String(),
		ReasoningContent: reasoningBuilder.String(),
		ToolCalls:        toolCalls,
	}, finishReason
}

// buildResponse builds a partial streaming response for a chunk.
// Returns nil if the chunk should be skipped.
func (m *Model) buildResponse(chatCompletion *genai.GenerateContentResponse) *model.Response {
	if chatCompletion == nil {
		return &model.Response{}
	}
	response := &model.Response{
		ID:        chatCompletion.ResponseID,
		Created:   chatCompletion.CreateTime.Unix(),
		Model:     chatCompletion.ModelVersion,
		Timestamp: chatCompletion.CreateTime,
	}
	message, finishReason := m.convertContentBlock(chatCompletion.Candidates)
	response.Choices = []model.Choice{
		{
			Index:   0,
			Message: message,
			Delta:   message,
		},
	}
	// Set finish reason.
	if finishReason != "" {
		response.Choices[0].FinishReason = &finishReason
	}
	// Convert usage information.
	response.Usage = m.completionUsageToModelUsage(chatCompletion.UsageMetadata)
	return response
}

// completionUsageToModelUsage converts genai.GenerateContentResponseUsageMetadata to model.Usage.
func (m *Model) completionUsageToModelUsage(usage *genai.GenerateContentResponseUsageMetadata) *model.Usage {
	if usage == nil {
		return nil
	}
	return &model.Usage{
		PromptTokens:     int(usage.PromptTokenCount),
		CompletionTokens: int(usage.CandidatesTokenCount),
		TotalTokens:      int(usage.TotalTokenCount),
		PromptTokensDetails: model.PromptTokensDetails{
			CachedTokens: int(usage.CachedContentTokenCount),
		},
	}
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
			"token tailoring failed in openai.Model",
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

// buildChatConfig converts our Request to Gemini request config.
func (m *Model) buildChatConfig(request *model.Request) *genai.GenerateContentConfig {
	chatRequest := &genai.GenerateContentConfig{
		Tools: m.convertTools(request.Tools),
	}

	// Set response_format for native structured outputs when requested.
	if request.StructuredOutput != nil &&
		request.StructuredOutput.Type == model.StructuredOutputJSONSchema &&
		request.StructuredOutput.JSONSchema != nil {
		chatRequest.ResponseJsonSchema = request.StructuredOutput.JSONSchema
	}

	if request.MaxTokens != nil {
		chatRequest.MaxOutputTokens = int32(*request.MaxTokens)
	}
	if request.Temperature != nil {
		chatRequest.Temperature = genai.Ptr(float32(*request.Temperature))
	}
	if request.TopP != nil {
		chatRequest.TopP = genai.Ptr(float32(*request.TopP))
	}
	if len(request.Stop) > 0 {
		chatRequest.StopSequences = request.Stop
	}
	if request.PresencePenalty != nil {
		chatRequest.PresencePenalty = genai.Ptr(float32(*request.PresencePenalty))
	}
	if request.FrequencyPenalty != nil {
		chatRequest.FrequencyPenalty = genai.Ptr(float32(*request.FrequencyPenalty))
	}
	chatRequest.ThinkingConfig = m.buildThinkingConfig(request)
	return chatRequest
}

// buildThinkingConfig converts our Request to Gemini request ThinkingConfig
func (m *Model) buildThinkingConfig(request *model.Request) *genai.ThinkingConfig {
	res := &genai.ThinkingConfig{}
	if request.ThinkingTokens != nil {
		res.ThinkingBudget = genai.Ptr(int32(*request.ThinkingTokens))
	}
	if request.ThinkingEnabled != nil {
		res.IncludeThoughts = *request.ThinkingEnabled
	}
	return res
}

// convertMessages converts our Message format to OpenAI's format.
func (m *Model) convertMessages(messages []model.Message) []*genai.Content {
	result := make([]*genai.Content, 0, len(messages))

	for _, msg := range messages {
		result = append(result, m.convertMessageContent(msg)...)
	}
	return result
}

// convertMessageContent converts message content to user message content union.
func (m *Model) convertMessageContent(
	msg model.Message,
) []*genai.Content {
	var (
		contentParts []*genai.Content
	)
	role := genai.RoleUser
	if msg.Role == model.RoleAssistant {
		role = genai.RoleModel
	}
	// Add Content as a text part if present.
	if msg.Content != "" {
		contentParts = append(
			contentParts,
			genai.NewContentFromText(msg.Content, genai.Role(role)),
		)
	}
	for _, part := range msg.ContentParts {
		contentPart := m.convertContentPart(part)
		if contentPart == nil {
			continue
		}
		// For non-file or non-skipped file types, add to contentParts.
		contentParts = append(contentParts, genai.NewContentFromParts([]*genai.Part{contentPart}, genai.Role(role)))
	}
	return contentParts
}

func (m *Model) convertTools(tools map[string]tool.Tool) []*genai.Tool {
	var result []*genai.Tool
	for _, t := range tools {
		result = append(result, &genai.Tool{
			FunctionDeclarations: []*genai.FunctionDeclaration{
				{
					Description:          t.Declaration().Description,
					Name:                 t.Declaration().Name,
					ParametersJsonSchema: t.Declaration().InputSchema,
					ResponseJsonSchema:   t.Declaration().OutputSchema,
				},
			},
		})
	}
	return result
}

// convertContentPart converts a single content part to Gemini format.
func (m *Model) convertContentPart(part model.ContentPart) *genai.Part {
	switch part.Type {
	case model.ContentTypeText:
		if part.Text != nil {
			return &genai.Part{
				Text: *part.Text,
			}
		}
	case model.ContentTypeImage:
		if part.Image == nil {
			return nil
		}
		if part.Image.URL != "" {
			return genai.NewPartFromURI(part.Image.URL, part.Image.Format)
		}
		if len(part.Image.Data) != 0 {
			return genai.NewPartFromBytes(part.Image.Data, part.Image.Format)
		}
	case model.ContentTypeAudio:
		if part.Audio == nil {
			return nil
		}
		return genai.NewPartFromBytes(part.Audio.Data, part.Audio.Format)
	case model.ContentTypeFile:
		if part.File == nil {
			return nil
		}
		return genai.NewPartFromBytes(part.File.Data, part.File.MimeType)
	}
	return nil
}
