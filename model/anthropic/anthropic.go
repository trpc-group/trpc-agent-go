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
	"net/http"
	"strings"
	"sync"
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

// HTTPClient is the interface for the HTTP client.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPClientNewFunc is the function type for creating a new HTTP client.
type HTTPClientNewFunc func(opts ...HTTPClientOption) HTTPClient

// DefaultNewHTTPClient is the default HTTP client for Anthropic.
var DefaultNewHTTPClient HTTPClientNewFunc = func(opts ...HTTPClientOption) HTTPClient {
	options := &HTTPClientOptions{}
	for _, opt := range opts {
		opt(options)
	}
	return &http.Client{
		Transport: options.Transport,
	}
}

// HTTPClientOption is the option for the HTTP client.
type HTTPClientOption func(*HTTPClientOptions)

// WithHTTPClientName is the option for the HTTP client name.
func WithHTTPClientName(name string) HTTPClientOption {
	return func(options *HTTPClientOptions) {
		options.Name = name
	}
}

// WithHTTPClientTransport is the option for the HTTP client transport.
func WithHTTPClientTransport(transport http.RoundTripper) HTTPClientOption {
	return func(options *HTTPClientOptions) {
		options.Transport = transport
	}
}

// HTTPClientOptions is the options for the HTTP client.
type HTTPClientOptions struct {
	Name      string
	Transport http.RoundTripper
}

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
	chatRequest *anthropic.MessageNewParams,
)

// ChatResponseCallbackFunc is the function type for the chat response callback.
type ChatResponseCallbackFunc func(
	ctx context.Context,
	chatRequest *anthropic.MessageNewParams,
	chatResponse *anthropic.Message,
)

// ChatChunkCallbackFunc is the function type for the chat chunk callback.
type ChatChunkCallbackFunc func(
	ctx context.Context,
	chatRequest *anthropic.MessageNewParams,
	chatChunk *anthropic.MessageStreamEventUnion,
)

// ChatStreamCompleteCallbackFunc is the function type for the chat stream completion callback.
// This callback is invoked when streaming is completely finished (success or error).
type ChatStreamCompleteCallbackFunc func(
	ctx context.Context,
	chatRequest *anthropic.MessageNewParams,
	accumulator *anthropic.Message, // nil if streamErr is not nil
	streamErr error, // nil if streaming completed successfully
)

// options contains configuration options for creating an Anthropic model.
type options struct {
	// API key for the Anthropic client.
	apiKey string
	// Base URL for the Anthropic client.
	baseURL string
	// Buffer size for response channels (default: 256)
	channelBufferSize int
	// Options for the HTTP client.
	httpClientOptions []HTTPClientOption
	// Options for building Anthropic client.
	anthropicClientOptions []option.RequestOption
	// Options for building Anthropic request.
	anthropicRequestOptions []option.RequestOption
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

// Option is a function that configures an Anthropic model.
type Option func(*options)

// WithAPIKey sets the API key for the Anthropic client.
func WithAPIKey(key string) Option {
	return func(o *options) {
		o.apiKey = key
	}
}

// WithBaseURL sets the base URL for the Anthropic client.
func WithBaseURL(url string) Option {
	return func(o *options) {
		o.baseURL = url
	}
}

// WithChannelBufferSize sets the channel buffer size for the Anthropic client, 256 by default.
func WithChannelBufferSize(size int) Option {
	return func(o *options) {
		if size <= 0 {
			size = defaultChannelBufferSize
		}
		o.channelBufferSize = size
	}
}

// WithAnthropicClientOptions appends custom request options for the Anthropic client.
func WithAnthropicClientOptions(opts ...option.RequestOption) Option {
	return func(o *options) {
		o.anthropicClientOptions = append(o.anthropicClientOptions, opts...)
	}
}

// WithHeaders appends static HTTP headers to all Anthropic requests.
func WithHeaders(headers map[string]string) Option {
	return func(o *options) {
		if len(headers) == 0 {
			return
		}
		for k, v := range headers {
			o.anthropicClientOptions = append(o.anthropicClientOptions, option.WithHeader(k, v))
		}
	}
}

// WithAnthropicRequestOptions appends per-request options for the Anthropic client.
func WithAnthropicRequestOptions(opts ...option.RequestOption) Option {
	return func(o *options) {
		o.anthropicRequestOptions = append(o.anthropicRequestOptions, opts...)
	}
}

// WithChatRequestCallback sets the function to be called before sending a chat request.
func WithChatRequestCallback(fn ChatRequestCallbackFunc) Option {
	return func(opts *options) {
		opts.chatRequestCallback = fn
	}
}

// WithChatResponseCallback sets the function to be called after receiving a chat response.
// Used for non-streaming responses.
func WithChatResponseCallback(fn ChatResponseCallbackFunc) Option {
	return func(opts *options) {
		opts.chatResponseCallback = fn
	}
}

// WithChatChunkCallback sets the function to be called after receiving a chat chunk.
// Used for streaming responses.
func WithChatChunkCallback(fn ChatChunkCallbackFunc) Option {
	return func(opts *options) {
		opts.chatChunkCallback = fn
	}
}

// WithChatStreamCompleteCallback sets the function to be called when streaming is completed.
// Called for both successful and failed streaming completions.
func WithChatStreamCompleteCallback(fn ChatStreamCompleteCallbackFunc) Option {
	return func(opts *options) {
		opts.chatStreamCompleteCallback = fn
	}
}

// WithHTTPClientOptions sets the HTTP client options for the Anthropic client.
func WithHTTPClientOptions(httpOpts ...HTTPClientOption) Option {
	return func(opts *options) {
		opts.httpClientOptions = httpOpts
	}
}

// WithEnableTokenTailoring enables automatic token tailoring based on model context window.
// When enabled, the system will automatically calculate max input tokens using the model's
// context window minus reserved tokens and protocol overhead.
func WithEnableTokenTailoring(enabled bool) Option {
	return func(opts *options) {
		opts.enableTokenTailoring = enabled
	}
}

// WithMaxInputTokens sets only the input token limit for token tailoring.
// The counter/strategy will be lazily initialized if not provided.
// Defaults to SimpleTokenCounter and MiddleOutStrategy.
func WithMaxInputTokens(limit int) Option {
	return func(opts *options) {
		opts.maxInputTokens = limit
	}
}

// WithTokenCounter sets the TokenCounter used for token tailoring.
// If not provided and token limit is enabled, a SimpleTokenCounter will be used.
func WithTokenCounter(counter model.TokenCounter) Option {
	return func(opts *options) {
		opts.tokenCounter = counter
	}
}

// WithTailoringStrategy sets the TailoringStrategy used for token tailoring.
// If not provided and token limit is enabled, a MiddleOutStrategy will be used.
func WithTailoringStrategy(strategy model.TailoringStrategy) Option {
	return func(opts *options) {
		opts.tailoringStrategy = strategy
	}
}

// WithTokenTailoringConfig sets custom token tailoring budget parameters.
// This allows advanced users to fine-tune the token allocation strategy.
//
// Example:
//
//	anthropic.WithTokenTailoringConfig(&model.TokenTailoringConfig{
//	    ProtocolOverheadTokens: 1024,
//	    ReserveOutputTokens:    4096,
//	    SafetyMarginRatio:      0.15,
//	})
//
// Note: It is recommended to use the default values unless you have specific
// requirements.
func WithTokenTailoringConfig(config *model.TokenTailoringConfig) Option {
	return func(opts *options) {
		opts.tokenTailoringConfig = config
	}
}

// New creates a new Anthropic model adapter.
func New(name string, opts ...Option) *Model {
	o := &options{
		channelBufferSize: defaultChannelBufferSize,
	}
	for _, opt := range opts {
		opt(o)
	}

	// Initialize token tailoring budget parameters with defaults.
	protocolOverhead := protocolOverheadTokens
	reserveOutput := reserveOutputTokens
	inputFloor := inputTokensFloor
	outputFloor := outputTokensFloor
	safetyMargin := safetyMarginRatio
	maxInputRatio := maxInputTokensRatio

	// Apply custom token tailoring config if provided.
	if o.tokenTailoringConfig != nil {
		if o.tokenTailoringConfig.ProtocolOverheadTokens > 0 {
			protocolOverhead = o.tokenTailoringConfig.ProtocolOverheadTokens
		}
		if o.tokenTailoringConfig.ReserveOutputTokens > 0 {
			reserveOutput = o.tokenTailoringConfig.ReserveOutputTokens
		}
		if o.tokenTailoringConfig.InputTokensFloor > 0 {
			inputFloor = o.tokenTailoringConfig.InputTokensFloor
		}
		if o.tokenTailoringConfig.OutputTokensFloor > 0 {
			outputFloor = o.tokenTailoringConfig.OutputTokensFloor
		}
		if o.tokenTailoringConfig.SafetyMarginRatio > 0 {
			safetyMargin = o.tokenTailoringConfig.SafetyMarginRatio
		}
		if o.tokenTailoringConfig.MaxInputTokensRatio > 0 {
			maxInputRatio = o.tokenTailoringConfig.MaxInputTokensRatio
		}
	}

	var clientOpts []option.RequestOption
	if o.apiKey != "" {
		clientOpts = append(clientOpts, option.WithAPIKey(o.apiKey))
	}
	if o.baseURL != "" {
		clientOpts = append(clientOpts, option.WithBaseURL(o.baseURL))
	}
	clientOpts = append(clientOpts, option.WithHTTPClient(DefaultNewHTTPClient(o.httpClientOptions...)))
	clientOpts = append(clientOpts, o.anthropicClientOptions...)
	client := anthropic.NewClient(clientOpts...)

	// Provide defaults at construction time when token tailoring is enabled.
	// These are best-effort defaults; user-provided counter/strategy always take priority.
	if o.maxInputTokens > 0 {
		if o.tokenCounter == nil {
			o.tokenCounter = model.NewSimpleTokenCounter()
		}
		if o.tailoringStrategy == nil {
			o.tailoringStrategy = model.NewMiddleOutStrategy(o.tokenCounter)
		}
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
		protocolOverheadTokens:     protocolOverhead,
		reserveOutputTokens:        reserveOutput,
		inputTokensFloor:           inputFloor,
		outputTokensFloor:          outputFloor,
		safetyMarginRatio:          safetyMargin,
		maxInputTokensRatio:        maxInputRatio,
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
		log.Debugf("auto-calculated max input tokens: model=%s, contextWindow=%d, maxInputTokens=%d",
			m.name, contextWindow, maxInputTokens)
	}

	// Determine token counter using priority: user config > default.
	tokenCounter := m.tokenCounter
	if tokenCounter == nil {
		m.tokenCounterOnce.Do(func() {
			if m.tokenCounter == nil {
				m.tokenCounter = model.NewSimpleTokenCounter()
			}
		})
		tokenCounter = m.tokenCounter
	}

	// Determine tailoring strategy using priority: user config > default.
	tailoringStrategy := m.tailoringStrategy
	if tailoringStrategy == nil {
		m.tailoringStrategyOnce.Do(func() {
			if m.tailoringStrategy == nil {
				m.tailoringStrategy = model.NewMiddleOutStrategy(tokenCounter)
			}
		})
		tailoringStrategy = m.tailoringStrategy
	}

	// Apply token tailoring.
	tailored, err := tailoringStrategy.TailorMessages(ctx, request.Messages, maxInputTokens)
	if err != nil {
		log.Warn("token tailoring failed in anthropic.Model", err)
		return
	}

	request.Messages = tailored

	// Calculate remaining tokens for output based on context window.
	usedTokens, err := tokenCounter.CountTokensRange(ctx, request.Messages, 0, len(request.Messages))
	if err != nil {
		log.Warn("failed to count tokens after tailoring", err)
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
			log.Debugf("token tailoring: contextWindow=%d, usedTokens=%d, maxOutputTokens=%d",
				contextWindow, usedTokens, maxOutputTokens)
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
