//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package hunyuan provides Hunyuan-compatible model implementations.
package hunyuan

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/hunyuan/internal/hunyuan"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultChannelBufferSize = 256
	functionToolType         = "function"
)

var (
	protocolOverheadTokens = imodel.DefaultProtocolOverheadTokens
	reserveOutputTokens    = imodel.DefaultReserveOutputTokens
	inputTokensFloor       = imodel.DefaultInputTokensFloor
	outputTokensFloor      = imodel.DefaultOutputTokensFloor
	safetyMarginRatio      = imodel.DefaultSafetyMarginRatio
	maxInputTokensRatio    = imodel.DefaultMaxInputTokensRatio
)

// Model implements the model.Model interface for Hunyuan API.
type Model struct {
	client                     *hunyuan.Client
	name                       string
	contextWindow              int
	channelBufferSize          int
	chatRequestCallback        ChatRequestCallbackFunc
	chatResponseCallback       ChatResponseCallbackFunc
	chatChunkCallback          ChatChunkCallbackFunc
	chatStreamCompleteCallback ChatStreamCompleteCallbackFunc
	enableTokenTailoring       bool                    // Enable automatic token tailoring.
	maxInputTokens             int                     // Max input tokens for token tailoring.
	tokenCounterOnce           sync.Once               // sync.Once for lazy initialization of tokenCounter.
	tokenCounter               model.TokenCounter      // Token counter for token tailoring.
	tailoringStrategyOnce      sync.Once               // sync.Once for lazy initialization of tailoringStrategy.
	tailoringStrategy          model.TailoringStrategy // Tailoring strategy for token tailoring.
	// Token tailoring budget parameters (instance-level overrides).
	protocolOverheadTokens int
	reserveOutputTokens    int
	inputTokensFloor       int
	outputTokensFloor      int
	safetyMarginRatio      float64
	maxInputTokensRatio    float64
}

// ChatRequestCallbackFunc is the function type for the chat request callback.
type ChatRequestCallbackFunc func(
	ctx context.Context,
	chatRequest *hunyuan.ChatCompletionNewParams,
)

// ChatResponseCallbackFunc is the function type for the chat response callback.
type ChatResponseCallbackFunc func(
	ctx context.Context,
	chatRequest *hunyuan.ChatCompletionNewParams,
	chatResponse *hunyuan.ChatCompletionResponse,
)

// ChatChunkCallbackFunc is the function type for the chat chunk callback.
type ChatChunkCallbackFunc func(
	ctx context.Context,
	chatRequest *hunyuan.ChatCompletionNewParams,
	chatChunk *hunyuan.ChatCompletionResponse,
)

// ChatStreamCompleteCallbackFunc is the function type for the chat stream completion callback.
type ChatStreamCompleteCallbackFunc func(
	ctx context.Context,
	chatRequest *hunyuan.ChatCompletionNewParams,
	streamErr error,
)

// options contains configuration options for creating a Hunyuan model.
type options struct {
	// SecretId for Hunyuan API authentication.
	secretId string
	// SecretKey for Hunyuan API authentication.
	secretKey string
	// Base URL for the Hunyuan server.
	baseUrl string
	// Host for the Hunyuan server.
	host string
	// HTTP client for the Hunyuan client.
	httpClient *http.Client
	// Buffer size for response channels (default: 256)
	channelBufferSize int
	// Callback for the chat request.
	chatRequestCallback ChatRequestCallbackFunc
	// Callback for the chat response.
	chatResponseCallback ChatResponseCallbackFunc
	// Callback for the chat chunk.
	chatChunkCallback ChatChunkCallbackFunc
	// Callback for the chat stream completion.
	chatStreamCompleteCallback ChatStreamCompleteCallbackFunc
	// enableTokenTailoring enables automatic token tailoring based on model context window.
	enableTokenTailoring bool
	// tokenCounter count tokens for token tailoring.
	tokenCounter model.TokenCounter
	// tailoringStrategy defines the strategy for token tailoring.
	tailoringStrategy model.TailoringStrategy
	// maxInputTokens is the max input tokens for token tailoring.
	maxInputTokens int
	// tokenTailoringConfig allows customization of token tailoring parameters.
	tokenTailoringConfig *model.TokenTailoringConfig
}

// New creates a new Hunyuan model adapter.
func New(name string, opts ...Option) *Model {
	o := defaultOptions

	for _, opt := range opts {
		opt(&o)
	}

	// Build client options.
	var clientOpts []hunyuan.Option
	if o.secretId != "" {
		clientOpts = append(clientOpts, hunyuan.WithSecretId(o.secretId))
	}
	if o.secretKey != "" {
		clientOpts = append(clientOpts, hunyuan.WithSecretKey(o.secretKey))
	}
	if o.baseUrl != "" {
		clientOpts = append(clientOpts, hunyuan.WithBaseUrl(o.baseUrl))
	}
	if o.host != "" {
		clientOpts = append(clientOpts, hunyuan.WithHost(o.host))
	}
	if o.httpClient != nil {
		clientOpts = append(clientOpts, hunyuan.WithHttpClient(o.httpClient))
	}

	// Create Hunyuan API client.
	client := hunyuan.NewClient(clientOpts...)

	if o.tailoringStrategy == nil {
		o.tailoringStrategy = model.NewMiddleOutStrategy(o.tokenCounter)
	}

	m := &Model{
		client:                     client,
		name:                       name,
		channelBufferSize:          o.channelBufferSize,
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

	// Resolve context window for the model.
	m.contextWindow = imodel.ResolveContextWindow(m.name)

	return m
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
			m.handleStreamingResponse(ctx, chatRequest, responseChan)
			return
		}
		m.handleNonStreamingResponse(ctx, chatRequest, responseChan)
	}()
	return responseChan, nil
}

// applyTokenTailoring performs best-effort token tailoring if configured.
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
		log.Debugf("auto-calculated max input tokens: model=%s, contextWindow=%d, maxInputTokens=%d",
			m.name, contextWindow, maxInputTokens)
	}

	// Apply token tailoring.
	tailored, err := m.tailoringStrategy.TailorMessages(ctx, request.Messages, maxInputTokens)
	if err != nil {
		log.Warn("token tailoring failed in hunyuan.Model", err)
		return
	}

	request.Messages = tailored

	// Calculate remaining tokens for output based on context window.
	usedTokens, err := m.tokenCounter.CountTokensRange(ctx, request.Messages, 0, len(request.Messages))
	if err != nil {
		log.Warn("failed to count tokens after tailoring", err)
		return
	}

	// Set max output tokens only if user hasn't specified it.
	if request.GenerationConfig.MaxTokens == nil {
		var maxOutputTokens int
		if m.protocolOverheadTokens > 0 || m.outputTokensFloor > 0 {
			// Use custom parameters if any are set.
			maxOutputTokens = imodel.CalculateMaxOutputTokensWithParams(
				m.contextWindow,
				usedTokens,
				m.protocolOverheadTokens,
				m.outputTokensFloor,
				m.safetyMarginRatio,
			)
		} else {
			// Use default parameters.
			maxOutputTokens = imodel.CalculateMaxOutputTokens(m.contextWindow, usedTokens)
		}
		if maxOutputTokens > 0 {
			request.GenerationConfig.MaxTokens = &maxOutputTokens
			log.Debugf("token tailoring: contextWindow=%d, usedTokens=%d, maxOutputTokens=%d",
				m.contextWindow, usedTokens, maxOutputTokens)
		}
	}
}

// buildChatRequest builds the chat request for the Hunyuan API.
func (m *Model) buildChatRequest(request *model.Request) (*hunyuan.ChatCompletionNewParams, error) {
	// Convert messages to Hunyuan format.
	messages, err := convertMessages(request.Messages)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("request must include at least one message")
	}

	// Build chat request.
	chatRequest := &hunyuan.ChatCompletionNewParams{
		Model:    m.name,
		Messages: messages,
		Stream:   request.Stream,
	}

	// Convert tools if present.
	if len(request.Tools) > 0 {
		chatRequest.Tools = convertTools(request.Tools)
	}

	// Set generation parameters.
	if request.Temperature != nil {
		chatRequest.Temperature = *request.Temperature
	}
	if request.TopP != nil {
		chatRequest.TopP = *request.TopP
	}
	if len(request.Stop) > 0 {
		chatRequest.Stop = request.Stop
	}
	if request.MaxTokens != nil {
		// Note: Hunyuan doesn't have a direct MaxTokens parameter in the API
		// This would need to be handled differently based on Hunyuan's API capabilities
		log.Debugf("MaxTokens parameter not directly supported by Hunyuan API: %d", *request.MaxTokens)
	}
	if request.ThinkingEnabled != nil && *request.ThinkingEnabled {
		chatRequest.EnableThinking = true
	}

	return chatRequest, nil
}

// handleNonStreamingResponse sends a non-streaming request to the Hunyuan API.
func (m *Model) handleNonStreamingResponse(
	ctx context.Context,
	chatRequest *hunyuan.ChatCompletionNewParams,
	responseChan chan<- *model.Response,
) {
	// Issue non-streaming request.
	chatResponse, err := m.client.ChatCompletion(ctx, chatRequest)
	if err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeAPIError, err)
		return
	}

	if m.chatResponseCallback != nil {
		m.chatResponseCallback(ctx, chatRequest, chatResponse)
	}

	response, err := convertChatResponse(chatResponse)
	if err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeAPIError, err)
		return
	}
	response.Model = m.name

	// Emit final response.
	select {
	case responseChan <- response:
	case <-ctx.Done():
	}
}

// handleStreamingResponse sends a streaming request to the Hunyuan API.
func (m *Model) handleStreamingResponse(
	ctx context.Context,
	chatRequest *hunyuan.ChatCompletionNewParams,
	responseChan chan<- *model.Response,
) {
	var streamErr error

	err := m.client.ChatCompletionStream(ctx, chatRequest, func(chunk *hunyuan.ChatCompletionResponse) error {
		if m.chatChunkCallback != nil {
			m.chatChunkCallback(ctx, chatRequest, chunk)
		}

		response, err := convertChatResponse(chunk)
		if err != nil {
			return err
		}
		response.Model = m.name

		// Emit partial response.
		select {
		case responseChan <- response:
		case <-ctx.Done():
			return ctx.Err()
		}

		return nil
	})

	if err != nil {
		streamErr = err
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeStreamError, err)
	}

	// Call the stream complete callback.
	if m.chatStreamCompleteCallback != nil {
		m.chatStreamCompleteCallback(ctx, chatRequest, streamErr)
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

// convertChatResponse converts Hunyuan chat response to model response.
func convertChatResponse(resp *hunyuan.ChatCompletionResponse) (*model.Response, error) {
	if resp == nil {
		return nil, fmt.Errorf("response is nil")
	}

	var choices []model.Choice
	var done bool
	for _, choice := range resp.Choices {
		var toolCalls []model.ToolCall
		if choice.FinishReason != "" || choice.Delta == nil {
			done = true
		}

		// Handle tool calls from message or delta
		var sourceToolCalls []*hunyuan.ChatCompletionMessageToolCall
		if choice.Message != nil && choice.Message.ToolCalls != nil {
			sourceToolCalls = choice.Message.ToolCalls
		} else if choice.Delta != nil && choice.Delta.ToolCalls != nil {
			sourceToolCalls = choice.Delta.ToolCalls
		}

		for _, tc := range sourceToolCalls {
			if tc.Function != nil {
				toolCalls = append(toolCalls, model.ToolCall{
					Type: functionToolType,
					ID:   tc.Id,
					Function: model.FunctionDefinitionParam{
						Name:      tc.Function.Name,
						Arguments: []byte(tc.Function.Arguments),
					},
				})
			}
		}

		c := model.Choice{}
		if choice.Message != nil {
			c.Message = model.Message{
				Role:             model.Role(choice.Message.Role),
				Content:          choice.Message.Content,
				ReasoningContent: choice.Message.ReasoningContent,
				ToolCalls:        toolCalls,
			}
		}
		if choice.Delta != nil {
			c.Delta = model.Message{
				Role:             model.Role(choice.Delta.Role),
				Content:          choice.Delta.Content,
				ReasoningContent: choice.Delta.ReasoningContent,
				ToolCalls:        toolCalls,
			}
		}
		if choice.FinishReason != "" {
			c.FinishReason = &choice.FinishReason
		}
		choices = append(choices, c)
	}

	now := time.Now()
	obj := model.ObjectTypeChatCompletionChunk
	var usage *model.Usage
	if done {
		obj = model.ObjectTypeChatCompletion
		usage = &model.Usage{
			PromptTokens:     int(resp.Usage.PromptTokens),
			CompletionTokens: int(resp.Usage.CompletionTokens),
			TotalTokens:      int(resp.Usage.TotalTokens),
		}
	}

	response := &model.Response{
		ID:        resp.Id,
		Object:    obj,
		Created:   resp.Created,
		Timestamp: now,
		IsPartial: !done,
		Choices:   choices,
		Done:      done,
		Usage:     usage,
	}

	return response, nil
}

// convertMessages converts model messages to Hunyuan messages.
func convertMessages(messages []model.Message) ([]*hunyuan.ChatCompletionMessageParam, error) {
	result := make([]*hunyuan.ChatCompletionMessageParam, 0, len(messages))
	for _, msg := range messages {
		hMsg, err := convertMessage(msg)
		if err != nil {
			return nil, err
		}
		result = append(result, hMsg)
	}
	return result, nil
}

// convertMessage converts a model message to a Hunyuan message.
func convertMessage(msg model.Message) (*hunyuan.ChatCompletionMessageParam, error) {
	hMsg := &hunyuan.ChatCompletionMessageParam{
		Role:             msg.Role.String(),
		Content:          msg.Content,
		ToolCallId:       msg.ToolID,
		ReasoningContent: msg.ReasoningContent,
	}

	// Convert tool calls
	if len(msg.ToolCalls) > 0 {
		for _, tc := range msg.ToolCalls {
			hMsg.ToolCalls = append(hMsg.ToolCalls, &hunyuan.ChatCompletionMessageToolCall{
				Id:   tc.ID,
				Type: tc.Type,
				Function: &hunyuan.ChatCompletionMessageToolCallFunction{
					Name:      tc.Function.Name,
					Arguments: string(tc.Function.Arguments),
				},
			})
		}
	}

	// Convert content parts (multimodal content)
	if len(msg.ContentParts) > 0 {
		var contents []*hunyuan.ChatCompletionMessageContentParam
		for _, part := range msg.ContentParts {
			switch part.Type {
			case model.ContentTypeText:
				if part.Text != nil {
					contents = append(contents, &hunyuan.ChatCompletionMessageContentParam{
						Type: "text",
						Text: *part.Text,
					})
				}
				// hunyuan image example https://cloud.tencent.com/document/api/1729/105701#.E7.A4.BA.E4.BE.8B9-.E5.9B.BE.E7.89.87.E7.90.86.E8.A7.A3.E7.A4.BA.E4.BE.8B
			case model.ContentTypeImage:
				if part.Image != nil {
					imageUrl := imageToURLOrBase64(part.Image)
					contents = append(contents, &hunyuan.ChatCompletionMessageContentParam{
						Type: "image_url",
						ImageUrl: &hunyuan.ChatCompletionContentImageUrlParam{
							Url: imageUrl,
						},
					})
				}
			case model.ContentTypeAudio:
				if part.Audio != nil {
					contents = append(contents, &hunyuan.ChatCompletionMessageContentParam{
						Type: "audio_url",
						VideoUrl: &hunyuan.ChatCompletionContentVideoUrlParam{
							Url: audioToBase64(part.Audio),
						},
					})
				}
			default:

			}
		}
		if len(contents) > 0 {
			hMsg.Contents = contents
			hMsg.Content = "" // Clear simple content when using structured content
		}
	}

	return hMsg, nil
}

// convertTools converts our tool declarations to Hunyuan tool parameters.
func convertTools(tools map[string]tool.Tool) []*hunyuan.ChatCompletionMessageTool {
	var result []*hunyuan.ChatCompletionMessageTool
	for _, tl := range tools {
		decl := tl.Declaration()

		schemaBytes, err := json.Marshal(decl.InputSchema)
		if err != nil {
			log.Errorf("failed to marshal tool schema for %s: %v", decl.Name, err)
			continue
		}

		result = append(result, &hunyuan.ChatCompletionMessageTool{
			Type: functionToolType,
			Function: &hunyuan.ChatCompletionMessageToolFunction{
				Name:        decl.Name,
				Parameters:  string(schemaBytes),
				Description: buildToolDescription(decl),
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

func imageToURLOrBase64(image *model.Image) string {
	if image.URL != "" {
		return image.URL
	}
	return "data:image/" + image.Format + ";base64," + base64.StdEncoding.EncodeToString(image.Data)
}

func audioToBase64(audio *model.Audio) string {
	return "data:" + audio.Format + ";base64," + base64.StdEncoding.EncodeToString(audio.Data)
}
