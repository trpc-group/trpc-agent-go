//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package ollama provides Ollama-compatible model implementations.
package ollama

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ollama/ollama/api"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Model implements the model.Model interface for Ollama API.
type Model struct {
	client                     *api.Client
	name                       string
	host                       string
	contextWindow              int
	httpClient                 *http.Client
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
	// Additional options for Ollama API. such as temperature/top_p
	options   map[string]any
	keepAlive *api.Duration
}

// New creates a new Ollama model adapter.
func New(name string, opts ...Option) *Model {
	o := defaultOptions
	if ollamaHost := os.Getenv(OllamaHost); ollamaHost != "" {
		o.host = ollamaHost
	}
	for _, opt := range opts {
		opt(&o)
	}
	s := strings.TrimSpace(o.host)
	scheme, hostport, ok := strings.Cut(s, "://")
	switch {
	case !ok:
		scheme, hostport = "http", s
		if s == "ollama.com" {
			scheme, hostport = "https", "ollama.com:443"
		}
	case scheme == "http":
		defaultPort = "80"
	case scheme == "https":
		defaultPort = "443"
	}

	hostport, path, _ := strings.Cut(hostport, "/")
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host, port = "127.0.0.1", defaultPort
		if ip := net.ParseIP(strings.Trim(hostport, "[]")); ip != nil {
			host = ip.String()
		} else if hostport != "" {
			host = hostport
		}
	}

	if n, err := strconv.ParseInt(port, 10, 32); err != nil || n > 65535 || n < 0 {
		port = defaultPort
	}

	baseURL := &url.URL{
		Scheme: scheme,
		Host:   net.JoinHostPort(host, port),
		Path:   path,
	}
	o.host = fmt.Sprintf("%s://%s", scheme, baseURL.Host)

	// Create Ollama API client.
	client := api.NewClient(baseURL, o.httpClient)

	if o.tailoringStrategy == nil {
		o.tailoringStrategy = model.NewMiddleOutStrategy(o.tokenCounter)
	}
	m := &Model{
		client:                     client,
		name:                       name,
		host:                       o.host,
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
		options:                    o.options,
		keepAlive:                  o.keepAlive,
	}
	m.contextWindow, err = m.getContextWindow()
	if err != nil {
		log.Warnf(
			"failed to get context window for %s: %v",
			m.name,
			err,
		)
		m.contextWindow = imodel.ResolveContextWindow(m.name)
	}
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
			m.handleStreamingResponse(ctx, *chatRequest, responseChan)
			return
		}
		m.handleNonStreamingResponse(ctx, *chatRequest, responseChan)
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
			"token tailoring failed in ollama.Model",
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
			log.DebugfContext(
				ctx,
				"token tailoring: contextWindow=%d, usedTokens=%d, "+
					"maxOutputTokens=%d",
				m.contextWindow,
				usedTokens,
				maxOutputTokens,
			)
		}
	}
}

// buildChatRequest builds the chat request for the Ollama API.
func (m *Model) buildChatRequest(request *model.Request) (*api.ChatRequest, error) {
	// Convert messages to Ollama format.
	messages, err := convertMessages(request.Messages)
	if err != nil {
		return nil, err
	}
	if len(messages) == 0 {
		return nil, fmt.Errorf("request must include at least one message")
	}

	// Build chat request.
	chatRequest := &api.ChatRequest{
		Model:    m.name,
		Messages: messages,
		Tools:    convertTools(request.Tools),
		Options:  m.options,
	}
	if chatRequest.Options == nil {
		chatRequest.Options = make(map[string]any)
	}

	// Set stream option.
	chatRequest.Stream = &request.Stream

	// Set generation parameters.
	if request.Temperature != nil {
		chatRequest.Options["temperature"] = *request.Temperature
	}
	if request.TopP != nil {
		chatRequest.Options["top_p"] = *request.TopP
	}
	if len(request.Stop) > 0 {
		chatRequest.Options["stop"] = request.Stop
	}
	if request.MaxTokens != nil {
		chatRequest.Options["num_predict"] = *request.MaxTokens
	}
	if request.ThinkingEnabled != nil {
		chatRequest.Think = &api.ThinkValue{
			Value: *request.ThinkingEnabled,
		}
	}

	// Set keep alive if configured.
	if m.keepAlive != nil {
		chatRequest.KeepAlive = m.keepAlive
	}

	return chatRequest, nil
}

// handleNonStreamingResponse sends a non-streaming request to the Ollama API.
func (m *Model) handleNonStreamingResponse(
	ctx context.Context,
	chatRequest api.ChatRequest,
	responseChan chan<- *model.Response,
) {
	// Issue non-streaming request.
	var chatResponse api.ChatResponse
	err := m.client.Chat(ctx, &chatRequest, func(resp api.ChatResponse) error {
		chatResponse = resp
		return nil
	})
	if err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeAPIError, err)
		return
	}

	if m.chatResponseCallback != nil {
		m.chatResponseCallback(ctx, &chatRequest, &chatResponse)
	}

	response, err := convertChatResponse(chatResponse)
	if err != nil {
		m.sendErrorResponse(ctx, responseChan, model.ErrorTypeAPIError, err)
		return
	}

	// Emit final response.
	select {
	case responseChan <- response:
	case <-ctx.Done():
	}
}

// handleStreamingResponse sends a streaming request to the Ollama API.
func (m *Model) handleStreamingResponse(
	ctx context.Context,
	chatRequest api.ChatRequest,
	responseChan chan<- *model.Response,
) {
	var streamErr error

	err := m.client.Chat(ctx, &chatRequest, func(chunk api.ChatResponse) error {
		if m.chatChunkCallback != nil {
			m.chatChunkCallback(ctx, &chatRequest, &chunk)
		}

		response, err := convertChatResponse(chunk)
		if err != nil {
			return err
		}

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
		m.chatStreamCompleteCallback(ctx, &chatRequest, streamErr)
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

// getContextWindow retrieves the context window size for the model.
// for example, ollama /api/show show model info, and get context_length
//
//	{
//	   "license": "xxx",
//	   "modelfile": "xxx",
//	   "parameters": "xxx",
//	   "template": "xxx",
//	   "details": {
//	   },
//	   "model_info": {
//	       "general.architecture": "llama",
//	       "general.basename": "DeepSeek-R1-Distill-Llama",
//	       "general.file_type": 15,
//	       "general.parameter_count": 8030261312,
//	       "general.quantization_version": 2,
//	       "general.size_label": "8B",
//	       "general.type": "model",
//	       "llama.attention.head_count": 32,
//	       "llama.attention.head_count_kv": 8,
//	       "llama.attention.layer_norm_rms_epsilon": 0.00001,
//	       "llama.block_count": 32,
//	       "llama.context_length": 131072,
//	       "llama.embedding_length": 4096,
//	       "llama.feed_forward_length": 14336,
//	       "llama.rope.dimension_count": 128,
//	       "llama.rope.freq_base": 500000,
//	       "llama.vocab_size": 128256,
//	       "tokenizer.ggml.add_bos_token": true,
//	       "tokenizer.ggml.add_eos_token": false,
//	       "tokenizer.ggml.bos_token_id": 128000,
//	       "tokenizer.ggml.eos_token_id": 128001,
//	       "tokenizer.ggml.merges": null,
//	       "tokenizer.ggml.model": "gpt2",
//	       "tokenizer.ggml.padding_token_id": 128001,
//	       "tokenizer.ggml.pre": "llama-bpe",
//	       "tokenizer.ggml.token_type": null,
//	       "tokenizer.ggml.tokens": null
//	   }
//	}
//
// llama.context_length is the context window size
// ref: https://github.com/ollama/ollama/blob/main/docs/api.md#show-model-information
func (m *Model) getContextWindow() (int, error) {
	resp, err := m.client.Show(context.Background(), &api.ShowRequest{
		Model: m.name,
	})
	if err != nil {
		return 0, err
	}
	for key, val := range resp.ModelInfo {
		if strings.HasSuffix(key, "context_length") {
			window, ok := val.(int)
			if ok {
				return window, nil
			}
			float64Window, ok := val.(float64)
			if ok {
				return int(float64Window), nil
			}
			int64Window, ok := val.(int64)
			if ok {
				return int(int64Window), nil
			}

			return 0, fmt.Errorf("context_length is not an int")
		}
	}
	return 0, fmt.Errorf("context_length not found")
}

// convertMessages converts model messages to Ollama messages.
func convertMessages(messages []model.Message) ([]api.Message, error) {
	result := make([]api.Message, 0, len(messages))
	for _, msg := range messages {
		oMsg, err := convertMessage(msg)
		if err != nil {
			return nil, err
		}
		result = append(result, oMsg)
	}
	return result, nil
}

func convertChatResponse(resp api.ChatResponse) (*model.Response, error) {
	var toolCalls []model.ToolCall
	for _, toolCall := range resp.Message.ToolCalls {
		args, err := json.Marshal(toolCall.Function.Arguments)
		if err != nil {
			return nil, err
		}
		toolCalls = append(toolCalls, model.ToolCall{
			Type: functionToolType,
			ID:   toolCall.ID,
			Function: model.FunctionDefinitionParam{
				Name:      toolCall.Function.Name,
				Arguments: args,
			},
		})
	}
	choice := model.Choice{}
	done := resp.Done
	obj := model.ObjectTypeChatCompletionChunk
	var usage *model.Usage
	if done {
		obj = model.ObjectTypeChatCompletion
		choice.Message = model.Message{
			Role:             model.RoleAssistant,
			ReasoningContent: resp.Message.Thinking,
			Content:          resp.Message.Content,
			ToolCalls:        toolCalls,
		}
		if resp.DoneReason != "" {
			choice.FinishReason = &[]string{resp.DoneReason}[0]
		}
		usage = &model.Usage{
			PromptTokens:     resp.PromptEvalCount,
			CompletionTokens: resp.EvalCount,
			TotalTokens:      resp.PromptEvalCount + resp.EvalCount,
		}
	} else {
		choice.Delta = model.Message{
			Role:             model.RoleAssistant,
			ReasoningContent: resp.Message.Thinking,
			Content:          resp.Message.Content,
			ToolCalls:        toolCalls,
		}
	}
	now := time.Now()
	msg := &model.Response{
		Object:    obj,
		Created:   now.Unix(),
		Timestamp: now,
		IsPartial: !done,
		Choices: []model.Choice{
			choice,
		},
		Model: resp.Model,
		Done:  done,
		Usage: usage,
	}
	return msg, nil
}

// convertMessage converts a model message to an Ollama message.
// ollama only support system/user/assistant role msg
func convertMessage(msg model.Message) (api.Message, error) {
	var toolCalls []api.ToolCall
	for i, toolCall := range msg.ToolCalls {
		args, err := argsToObject(toolCall.Function.Arguments)
		if err != nil {
			return api.Message{}, err
		}
		toolCalls = append(toolCalls, api.ToolCall{
			Function: api.ToolCallFunction{
				Index:     i,
				Name:      toolCall.Function.Name,
				Arguments: args,
			},
		})
	}
	// ollama role ("system", "user", or "assistant")
	var role string
	switch msg.Role {
	case model.RoleSystem:
		role = "system"
	case model.RoleUser, model.RoleTool:
		role = "user"
	case model.RoleAssistant:
		role = "assistant"
	default:
		role = "user"
	}
	oMsg := api.Message{
		Role:      role,
		Thinking:  msg.ReasoningContent,
		ToolCalls: toolCalls,
	}
	if len(msg.ContentParts) == 0 {
		oMsg.Content = msg.Content
		return oMsg, nil
	}

	var content string
	var images []api.ImageData
	for _, part := range msg.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil {
				content += *part.Text
			}
		case model.ContentTypeImage:
			if part.Image != nil && part.Image.Data != nil {
				images = append(images, []byte(imageToURLOrBase64(part.Image)))
			}
		default:
		}
	}
	oMsg.Content = content
	oMsg.Images = images
	return oMsg, nil
}

// convertTools converts our tool declarations to Ollama tool parameters.
func convertTools(tools map[string]tool.Tool) []api.Tool {
	var result []api.Tool
	for _, tl := range tools {
		properties := make(map[string]api.ToolProperty)
		var required []string

		decl := tl.Declaration()
		if decl.InputSchema != nil && decl.InputSchema.Properties != nil {
			for name, prop := range decl.InputSchema.Properties {
				properties[name] = api.ToolProperty{
					Type:        api.PropertyType{prop.Type},
					Description: prop.Description,
					Items:       prop.Items,
					Enum:        prop.Enum,
				}
				required = append(required, prop.Required...)
			}
		}
		result = append(result, api.Tool{
			Type: functionToolType,
			Function: api.ToolFunction{
				Name:        decl.Name,
				Description: buildToolDescription(decl),
				Parameters: api.ToolFunctionParameters{
					Type:       "object",
					Properties: properties,
					Required:   required,
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

func imageToURLOrBase64(image *model.Image) string {
	if len(image.Data) == 0 {
		return ""
	}
	return base64.StdEncoding.EncodeToString(image.Data)
}

func argsToObject(args []byte) (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal(args, &result); err != nil {
		return nil, fmt.Errorf("unmarshal tool call arguments: %w", err)
	}
	return result, nil
}
