//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package openai provides OpenAI-compatible model implementations.
package openai

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	openai "github.com/openai/openai-go"
	openaiopt "github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	functionToolType string = "function"

	defaultChannelBufferSize = 256
)

// HTTPClient is the interface for the HTTP client.
type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

// HTTPClientNewFunc is the function type for creating a new HTTP client.
type HTTPClientNewFunc func(opts ...HTTPClientOption) HTTPClient

// DefaultNewHTTPClient is the default HTTP client for OpenAI.
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

// Model implements the model.Model interface for OpenAI API.
type Model struct {
	client               openai.Client
	name                 string
	baseURL              string
	apiKey               string
	channelBufferSize    int
	chatRequestCallback  ChatRequestCallbackFunc
	chatResponseCallback ChatResponseCallbackFunc
	chatChunkCallback    ChatChunkCallbackFunc
	extraFields          map[string]interface{}
}

// ChatRequestCallbackFunc is the function type for the chat request callback.
type ChatRequestCallbackFunc func(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
)

// ChatResponseCallbackFunc is the function type for the chat response callback.
type ChatResponseCallbackFunc func(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
	chatResponse *openai.ChatCompletion,
)

// ChatChunkCallbackFunc is the function type for the chat chunk callback.
type ChatChunkCallbackFunc func(
	ctx context.Context,
	chatRequest *openai.ChatCompletionNewParams,
	chatChunk *openai.ChatCompletionChunk,
)

// options contains configuration options for creating a Model.
type options struct {
	// API key for the OpenAI client.
	APIKey string
	// Base URL for the OpenAI client. It is optional for OpenAI-compatible APIs.
	BaseURL string
	// Buffer size for response channels (default: 256)
	ChannelBufferSize int
	// Options for the HTTP client.
	HTTPClientOptions []HTTPClientOption
	// Callback for the chat request.
	ChatRequestCallback ChatRequestCallbackFunc
	// Callback for the chat response.
	ChatResponseCallback ChatResponseCallbackFunc
	// Callback for the chat chunk.
	ChatChunkCallback ChatChunkCallbackFunc
	// Options for the OpenAI client.
	OpenAIOptions []openaiopt.RequestOption
	// Extra fields to be added to the HTTP request body.
	ExtraFields map[string]interface{}
}

// Option is a function that configures an OpenAI model.
type Option func(*options)

// WithAPIKey sets the API key for the OpenAI client.
func WithAPIKey(key string) Option {
	return func(opts *options) {
		opts.APIKey = key
	}
}

// WithBaseURL sets the base URL for the OpenAI client.
func WithBaseURL(url string) Option {
	return func(opts *options) {
		opts.BaseURL = url
	}
}

// WithChannelBufferSize sets the channel buffer size for the OpenAI client.
func WithChannelBufferSize(size int) Option {
	return func(opts *options) {
		opts.ChannelBufferSize = size
	}
}

// WithChatRequestCallback sets the function to be called before sending a chat request.
func WithChatRequestCallback(fn ChatRequestCallbackFunc) Option {
	return func(opts *options) {
		opts.ChatRequestCallback = fn
	}
}

// WithChatResponseCallback sets the function to be called after receiving a chat response.
// Used for non-streaming responses.
func WithChatResponseCallback(fn ChatResponseCallbackFunc) Option {
	return func(opts *options) {
		opts.ChatResponseCallback = fn
	}
}

// WithChatChunkCallback sets the function to be called after receiving a chat chunk.
// Used for streaming responses.
func WithChatChunkCallback(fn ChatChunkCallbackFunc) Option {
	return func(opts *options) {
		opts.ChatChunkCallback = fn
	}
}

// WithHTTPClientOptions sets the HTTP client options for the OpenAI client.
func WithHTTPClientOptions(httpOpts ...HTTPClientOption) Option {
	return func(opts *options) {
		opts.HTTPClientOptions = httpOpts
	}
}

// WithOpenAIOptions sets the OpenAI options for the OpenAI client.
// E.g. use its middleware option:
//
//	import (
//		openai "github.com/openai/openai-go"
//		openaiopt "github.com/openai/openai-go/option"
//	)
//
//	WithOpenAIOptions(openaiopt.WithMiddleware(
//		func(req *http.Request, next openaiopt.MiddlewareNext) (*http.Response, error) {
//			// do something
//			return next(req)
//		}
//	)))
func WithOpenAIOptions(openaiOpts ...openaiopt.RequestOption) Option {
	return func(opts *options) {
		opts.OpenAIOptions = append(opts.OpenAIOptions, openaiOpts...)
	}
}

// WithExtraFields sets extra fields to be added to the HTTP request body.
// These fields will be included in every chat completion request.
// E.g.:
//
//	WithExtraFields(map[string]interface{}{
//		"custom_metadata": map[string]string{
//			"session_id": "abc",
//		},
//	})
//
// and "session_id" : "abc" will be added to the HTTP request json body.
func WithExtraFields(extraFields map[string]interface{}) Option {
	return func(opts *options) {
		if opts.ExtraFields == nil {
			opts.ExtraFields = make(map[string]interface{})
		}
		for k, v := range extraFields {
			opts.ExtraFields[k] = v
		}
	}
}

// New creates a new OpenAI-like model.
func New(name string, opts ...Option) *Model {
	o := &options{}
	for _, opt := range opts {
		opt(o)
	}
	var clientOpts []openaiopt.RequestOption

	if o.APIKey != "" {
		clientOpts = append(clientOpts, openaiopt.WithAPIKey(o.APIKey))
	}

	if o.BaseURL != "" {
		clientOpts = append(clientOpts, openaiopt.WithBaseURL(o.BaseURL))
	}

	clientOpts = append(clientOpts, openaiopt.WithHTTPClient(DefaultNewHTTPClient(o.HTTPClientOptions...)))
	clientOpts = append(clientOpts, o.OpenAIOptions...)

	client := openai.NewClient(clientOpts...)

	// Set default channel buffer size if not specified.
	channelBufferSize := o.ChannelBufferSize
	if channelBufferSize <= 0 {
		channelBufferSize = defaultChannelBufferSize
	}

	return &Model{
		client:               client,
		name:                 name,
		baseURL:              o.BaseURL,
		apiKey:               o.APIKey,
		channelBufferSize:    channelBufferSize,
		chatRequestCallback:  o.ChatRequestCallback,
		chatResponseCallback: o.ChatResponseCallback,
		chatChunkCallback:    o.ChatChunkCallback,
		extraFields:          o.ExtraFields,
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

	responseChan := make(chan *model.Response, m.channelBufferSize)

	// Convert our request format to OpenAI format.
	chatRequest := openai.ChatCompletionNewParams{
		Model:    shared.ChatModel(m.name),
		Messages: m.convertMessages(request.Messages),
		Tools:    m.convertTools(request.Tools),
	}

	// Set optional parameters if provided.
	if request.MaxTokens != nil {
		chatRequest.MaxTokens = openai.Int(int64(*request.MaxTokens)) // Convert to int64
	}
	if request.Temperature != nil {
		chatRequest.Temperature = openai.Float(*request.Temperature)
	}
	if request.TopP != nil {
		chatRequest.TopP = openai.Float(*request.TopP)
	}
	if len(request.Stop) > 0 {
		// Use the first stop string for simplicity.
		chatRequest.Stop = openai.ChatCompletionNewParamsStopUnion{
			OfString: openai.String(request.Stop[0]),
		}
	}
	if request.PresencePenalty != nil {
		chatRequest.PresencePenalty = openai.Float(*request.PresencePenalty)
	}
	if request.FrequencyPenalty != nil {
		chatRequest.FrequencyPenalty = openai.Float(*request.FrequencyPenalty)
	}
	if request.ReasoningEffort != nil {
		chatRequest.ReasoningEffort = shared.ReasoningEffort(*request.ReasoningEffort)
	}
	var opts []openaiopt.RequestOption
	if request.ThinkingEnabled != nil {
		opts = append(opts, openaiopt.WithJSONSet(model.ThinkingEnabledKey, *request.ThinkingEnabled))
	}
	if request.ThinkingTokens != nil {
		opts = append(opts, openaiopt.WithJSONSet(model.ThinkingTokensKey, *request.ThinkingTokens))
	}

	// Add extra fields to the request
	for key, value := range m.extraFields {
		opts = append(opts, openaiopt.WithJSONSet(key, value))
	}

	// Add streaming options if needed.
	if request.Stream {
		chatRequest.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}
	}

	go func() {
		defer close(responseChan)

		if m.chatRequestCallback != nil {
			m.chatRequestCallback(ctx, &chatRequest)
		}

		if request.Stream {
			m.handleStreamingResponse(ctx, chatRequest, responseChan, opts...)
		} else {
			m.handleNonStreamingResponse(ctx, chatRequest, responseChan, opts...)
		}
	}()

	return responseChan, nil
}

// convertMessages converts our Message format to OpenAI's format.
func (m *Model) convertMessages(messages []model.Message) []openai.ChatCompletionMessageParamUnion {
	result := make([]openai.ChatCompletionMessageParamUnion, len(messages))

	for i, msg := range messages {
		switch msg.Role {
		case model.RoleSystem:
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ChatCompletionSystemMessageParamContentUnion{
						OfString: openai.String(msg.Content),
					},
				},
			}
		case model.RoleUser:
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: openai.String(msg.Content),
					},
				},
			}
		case model.RoleAssistant:
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Content: openai.ChatCompletionAssistantMessageParamContentUnion{
						OfString: openai.String(msg.Content),
					},
					ToolCalls: m.convertToolCalls(msg.ToolCalls),
				},
			}
		case model.RoleTool:
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfTool: &openai.ChatCompletionToolMessageParam{
					Content: openai.ChatCompletionToolMessageParamContentUnion{
						OfString: openai.String(msg.Content),
					},
					ToolCallID: msg.ToolID,
				},
			}
		default:
			// Default to user message if role is unknown.
			result[i] = openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.ChatCompletionUserMessageParamContentUnion{
						OfString: openai.String(msg.Content),
					},
				},
			}
		}
	}

	return result
}

func (m *Model) convertToolCalls(toolCalls []model.ToolCall) []openai.ChatCompletionMessageToolCallParam {
	var result []openai.ChatCompletionMessageToolCallParam
	for _, toolCall := range toolCalls {
		result = append(result, openai.ChatCompletionMessageToolCallParam{
			ID: toolCall.ID,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{
				Name:      toolCall.Function.Name,
				Arguments: string(toolCall.Function.Arguments),
			},
		})
	}
	return result
}

func (m *Model) convertTools(tools map[string]tool.Tool) []openai.ChatCompletionToolParam {
	var result []openai.ChatCompletionToolParam
	for _, tool := range tools {
		declaration := tool.Declaration()
		// Convert the InputSchema to JSON to correctly map to OpenAI's expected format
		schemaBytes, err := json.Marshal(declaration.InputSchema)
		if err != nil {
			log.Errorf("failed to marshal tool schema for %s: %v", declaration.Name, err)
			continue
		}
		var parameters shared.FunctionParameters
		if err := json.Unmarshal(schemaBytes, &parameters); err != nil {
			log.Errorf("failed to unmarshal tool schema for %s: %v", declaration.Name, err)
			continue
		}
		result = append(result, openai.ChatCompletionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        declaration.Name,
				Description: openai.String(declaration.Description),
				Parameters:  parameters,
			},
		})
	}
	return result
}

// handleStreamingResponse handles streaming chat completion responses.
func (m *Model) handleStreamingResponse(
	ctx context.Context,
	chatRequest openai.ChatCompletionNewParams,
	responseChan chan<- *model.Response,
	opts ...openaiopt.RequestOption,
) {
	stream := m.client.Chat.Completions.NewStreaming(
		ctx, chatRequest, opts...)
	defer stream.Close()

	acc := openai.ChatCompletionAccumulator{}
	// Track ID -> Index mapping.
	idToIndexMap := make(map[string]int)

	for stream.Next() {
		chunk := stream.Current()

		// Record ID -> Index mapping when ID is present (first chunk of each tool call).
		if len(chunk.Choices) > 0 && len(chunk.Choices[0].Delta.ToolCalls) > 0 {
			toolCall := chunk.Choices[0].Delta.ToolCalls[0]
			index := int(toolCall.Index)
			if toolCall.ID != "" {
				idToIndexMap[toolCall.ID] = index
			}
		}

		acc.AddChunk(chunk)

		if m.chatChunkCallback != nil {
			m.chatChunkCallback(ctx, &chatRequest, &chunk)
		}

		response := &model.Response{
			ID:        chunk.ID,
			Object:    string(chunk.Object), // Convert constant to string
			Created:   chunk.Created,
			Model:     chunk.Model,
			Timestamp: time.Now(),
			Done:      false,
			IsPartial: true,
		}

		// Convert choices for partial responses (content streaming).
		if len(chunk.Choices) > 0 {
			if response.Choices == nil {
				response.Choices = make([]model.Choice, 1)
			}
			response.Choices[0].Delta = model.Message{
				Role:    model.RoleAssistant,
				Content: chunk.Choices[0].Delta.Content,
			}

			// Handle finish reason - FinishReason is a plain string.
			if chunk.Choices[0].FinishReason != "" {
				finishReason := chunk.Choices[0].FinishReason
				response.Choices[0].FinishReason = &finishReason
			}
		}

		select {
		case responseChan <- response:
		case <-ctx.Done():
			return
		}
	}

	// Send final response with usage information if available.
	if stream.Err() == nil {
		// Check accumulated tool calls (batch processing after streaming is complete).
		var hasToolCall bool
		var accumulatedToolCalls []model.ToolCall

		if len(acc.Choices) > 0 && len(acc.Choices[0].Message.ToolCalls) > 0 {
			hasToolCall = true
			accumulatedToolCalls = make([]model.ToolCall, 0, len(acc.Choices[0].Message.ToolCalls))

			for i, toolCall := range acc.Choices[0].Message.ToolCalls {
				// if openai return function tool call start with index 1 or more
				// ChatCompletionAccumulator will return empty tool call for index like 0, skip it.
				if toolCall.Function.Name == "" && toolCall.ID == "" {
					continue
				}

				// Use the original index from ID->Index mapping if available, otherwise use loop index.
				originalIndex := i
				if toolCall.ID != "" {
					if mappedIndex, exists := idToIndexMap[toolCall.ID]; exists {
						originalIndex = mappedIndex
					}
				}

				accumulatedToolCalls = append(accumulatedToolCalls, model.ToolCall{
					Index: func() *int { idx := originalIndex; return &idx }(),
					ID:    toolCall.ID,
					Type:  functionToolType, // openapi only supports a function type for now.
					Function: model.FunctionDefinitionParam{
						Name:      toolCall.Function.Name,
						Arguments: []byte(toolCall.Function.Arguments),
					},
				})
			}
		}

		finalResponse := &model.Response{
			ID:      acc.ID,
			Created: acc.Created,
			Model:   acc.Model,
			Choices: make([]model.Choice, len(acc.Choices)),
			Usage: &model.Usage{
				PromptTokens:     int(acc.Usage.PromptTokens),
				CompletionTokens: int(acc.Usage.CompletionTokens),
				TotalTokens:      int(acc.Usage.TotalTokens),
			},
			Timestamp: time.Now(),
			Done:      !hasToolCall,
			IsPartial: false,
		}
		for i, choice := range acc.Choices {
			finalResponse.Choices[i] = model.Choice{
				Index: int(choice.Index),
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: choice.Message.Content,
				},
			}

			// If there are tool calls, add them to the final response.
			if hasToolCall && i == 0 { // Usually only the first choice contains tool calls.
				finalResponse.Choices[i].Message.ToolCalls = accumulatedToolCalls
			}
		}

		select {
		case responseChan <- finalResponse:
		case <-ctx.Done():
		}
	} else {
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
	}
}

// handleNonStreamingResponse handles non-streaming chat completion responses.
func (m *Model) handleNonStreamingResponse(
	ctx context.Context,
	chatRequest openai.ChatCompletionNewParams,
	responseChan chan<- *model.Response,
	opts ...openaiopt.RequestOption,
) {
	chatCompletion, err := m.client.Chat.Completions.New(
		ctx, chatRequest, opts...)
	if m.chatResponseCallback != nil {
		m.chatResponseCallback(ctx, &chatRequest, chatCompletion)
	}
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

	response := &model.Response{
		ID:        chatCompletion.ID,
		Object:    string(chatCompletion.Object), // Convert constant to string
		Created:   chatCompletion.Created,
		Model:     chatCompletion.Model,
		Timestamp: time.Now(),
		Done:      true,
	}

	// Convert choices.
	if len(chatCompletion.Choices) > 0 {
		response.Choices = make([]model.Choice, len(chatCompletion.Choices))
		for i, choice := range chatCompletion.Choices {
			response.Choices[i] = model.Choice{
				Index: int(choice.Index),
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: choice.Message.Content,
				},
			}

			response.Choices[i].Message.ToolCalls = make([]model.ToolCall, len(choice.Message.ToolCalls))
			for j, toolCall := range choice.Message.ToolCalls {
				response.Choices[i].Message.ToolCalls[j] = model.ToolCall{
					ID:   toolCall.ID,
					Type: string(toolCall.Type),
					Function: model.FunctionDefinitionParam{
						Name:      toolCall.Function.Name,
						Arguments: []byte(toolCall.Function.Arguments),
					},
				}
			}

			// Handle finish reason - FinishReason is a plain string.
			if choice.FinishReason != "" {
				finishReason := choice.FinishReason
				response.Choices[i].FinishReason = &finishReason
			}
		}
	}

	// Convert usage information.
	if chatCompletion.Usage.PromptTokens > 0 || chatCompletion.Usage.CompletionTokens > 0 {
		response.Usage = &model.Usage{
			PromptTokens:     int(chatCompletion.Usage.PromptTokens),
			CompletionTokens: int(chatCompletion.Usage.CompletionTokens),
			TotalTokens:      int(chatCompletion.Usage.TotalTokens),
		}
	}

	// Set system fingerprint if available.
	if chatCompletion.SystemFingerprint != "" {
		response.SystemFingerprint = &chatCompletion.SystemFingerprint
	}

	select {
	case responseChan <- response:
	case <-ctx.Done():
	}
}
