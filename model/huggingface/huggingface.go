//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package huggingface provides HuggingFace-compatible model implementations.
package huggingface

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Model implements the model.Model interface for HuggingFace API.
type Model struct {
	name                       string
	baseURL                    string
	apiKey                     string
	httpClient                 *http.Client
	channelBufferSize          int
	chatRequestCallback        ChatRequestCallbackFunc
	chatResponseCallback       ChatResponseCallbackFunc
	chatChunkCallback          ChatChunkCallbackFunc
	chatStreamCompleteCallback ChatStreamCompleteCallbackFunc
	extraHeaders               map[string]string
	extraFields                map[string]any
	useTRPC                    bool
	trpcServiceName            string
	trpcTimeout                int
	enableTokenTailoring       bool
	maxInputTokens             int
	tokenCounter               model.TokenCounter
	tailoringStrategy          model.TailoringStrategy
	tokenTailoringConfig       *model.TokenTailoringConfig
}

// New creates a new HuggingFace model instance.
// modelName: The name of the HuggingFace model to use (e.g., "meta-llama/Llama-2-7b-chat-hf")
// opts: Optional configuration options
func New(modelName string, opts ...Option) (*Model, error) {
	if modelName == "" {
		return nil, errors.New("model name cannot be empty")
	}

	// Apply default options
	options := defaultOptions

	// Apply user-provided options
	for _, opt := range opts {
		opt(&options)
	}

	// Get API key from options or environment variable
	apiKey := options.APIKey
	if apiKey == "" {
		apiKey = os.Getenv(defaultAPIKeyEnvVar)
	}
	if apiKey == "" {
		return nil, fmt.Errorf("API key is required. Set it via WithAPIKey() or %s environment variable", defaultAPIKeyEnvVar)
	}

	// Use default HTTP client if not provided
	httpClient := options.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{}
	}

	// Initialize token tailoring strategy if enabled
	var tailoringStrategy model.TailoringStrategy
	if options.EnableTokenTailoring || options.MaxInputTokens > 0 {
		if options.TailoringStrategy != nil {
			tailoringStrategy = options.TailoringStrategy
		} else {
			tailoringStrategy = model.NewMiddleOutStrategy()
		}
	}

	m := &Model{
		name:                       modelName,
		baseURL:                    options.BaseURL,
		apiKey:                     apiKey,
		httpClient:                 httpClient,
		channelBufferSize:          options.ChannelBufferSize,
		chatRequestCallback:        options.ChatRequestCallback,
		chatResponseCallback:       options.ChatResponseCallback,
		chatChunkCallback:          options.ChatChunkCallback,
		chatStreamCompleteCallback: options.ChatStreamCompleteCallback,
		extraHeaders:               options.ExtraHeaders,
		extraFields:                options.ExtraFields,
		useTRPC:                    options.UseTRPC,
		trpcServiceName:            options.TRPCServiceName,
		trpcTimeout:                options.TRPCTimeout,
		enableTokenTailoring:       options.EnableTokenTailoring || options.MaxInputTokens > 0,
		maxInputTokens:             options.MaxInputTokens,
		tokenCounter:               options.TokenCounter,
		tailoringStrategy:          tailoringStrategy,
		tokenTailoringConfig:       options.TokenTailoringConfig,
	}

	return m, nil
}

// GenerateContent generates content from the given request.
func (m *Model) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	if request == nil {
		return nil, errors.New("request cannot be nil")
	}

	// Convert model.Request to HuggingFace ChatCompletionRequest
	hfRequest, err := m.convertRequest(request)
	if err != nil {
		return nil, fmt.Errorf("failed to convert request: %w", err)
	}

	// Apply token tailoring if enabled
	if m.enableTokenTailoring && m.maxInputTokens > 0 {
		if err := m.applyTokenTailoring(request, hfRequest); err != nil {
			log.Warnf(ctx, "failed to apply token tailoring: %v", err)
		}
	}

	// Create response channel
	responseChan := make(chan *model.Response, m.channelBufferSize)

	// Handle streaming vs non-streaming
	if request.Stream {
		go m.handleStreamingRequest(ctx, request, hfRequest, responseChan)
	} else {
		go m.handleNonStreamingRequest(ctx, request, hfRequest, responseChan)
	}

	return responseChan, nil
}

// Info returns basic information about the model.
func (m *Model) Info() model.Info {
	return model.Info{
		Name: m.name,
	}
}

// handleNonStreamingRequest handles non-streaming chat completion requests.
func (m *Model) handleNonStreamingRequest(
	ctx context.Context,
	originalRequest *model.Request,
	hfRequest *ChatCompletionRequest,
	responseChan chan<- *model.Response,
) {
	defer close(responseChan)

	// Call request callback if provided
	if m.chatRequestCallback != nil {
		m.chatRequestCallback(ctx, hfRequest)
	}

	// Make HTTP request
	hfResponse, err := m.makeRequest(ctx, hfRequest)
	if err != nil {
		responseChan <- &model.Response{
			Error: &model.Error{
				Message: fmt.Sprintf("failed to make request: %v", err),
			},
		}
		return
	}

	// Call response callback if provided
	if m.chatResponseCallback != nil {
		m.chatResponseCallback(ctx, hfRequest, hfResponse)
	}

	// Convert HuggingFace response to model.Response
	response := m.convertResponse(hfResponse)
	responseChan <- response
}

// handleStreamingRequest handles streaming chat completion requests.
func (m *Model) handleStreamingRequest(
	ctx context.Context,
	originalRequest *model.Request,
	hfRequest *ChatCompletionRequest,
	responseChan chan<- *model.Response,
) {
	defer close(responseChan)

	var streamErr error
	defer func() {
		if m.chatStreamCompleteCallback != nil {
			m.chatStreamCompleteCallback(ctx, hfRequest, streamErr)
		}
	}()

	// Call request callback if provided
	if m.chatRequestCallback != nil {
		m.chatRequestCallback(ctx, hfRequest)
	}

	// Make streaming HTTP request
	hfRequest.Stream = true
	resp, err := m.makeStreamingRequest(ctx, hfRequest)
	if err != nil {
		streamErr = err
		responseChan <- &model.Response{
			Error: &model.Error{
				Message: fmt.Sprintf("failed to make streaming request: %v", err),
			},
		}
		return
	}
	defer resp.Body.Close()

	// Read and process streaming response
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		line = strings.TrimSpace(line)

		// Skip empty lines and comments
		if line == "" || !strings.HasPrefix(line, "data: ") {
			continue
		}

		// Remove "data: " prefix
		data := strings.TrimPrefix(line, "data: ")

		// Check for stream end
		if data == "[DONE]" {
			break
		}

		// Parse chunk
		var chunk ChatCompletionChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			log.Warnf(ctx, "failed to parse chunk: %v, data: %s", err, data)
			continue
		}

		// Call chunk callback if provided
		if m.chatChunkCallback != nil {
			m.chatChunkCallback(ctx, hfRequest, &chunk)
		}

		// Convert chunk to model.Response
		response := m.convertChunk(&chunk)
		responseChan <- response
	}

	if err := scanner.Err(); err != nil {
		streamErr = err
		responseChan <- &model.Response{
			Error: &model.Error{
				Message: fmt.Sprintf("error reading stream: %v", err),
			},
		}
	}
}

// makeRequest makes a non-streaming HTTP request to the HuggingFace API.
func (m *Model) makeRequest(ctx context.Context, hfRequest *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	// TODO: Implement tRPC client support
	if m.useTRPC {
		return nil, errors.New("tRPC client support not yet implemented")
	}

	// Marshal request to JSON
	requestBody, err := m.marshalRequest(hfRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := fmt.Sprintf("%s/models/%s/v1/chat/completions", m.baseURL, m.name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	m.setHeaders(req)

	// Make request
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}
	defer resp.Body.Close()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for error response
	if resp.StatusCode != http.StatusOK {
		var errResp ErrorResponse
		if err := json.Unmarshal(body, &errResp); err != nil {
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("API error: %s", errResp.Error.Message)
	}

	// Parse response
	var hfResponse ChatCompletionResponse
	if err := json.Unmarshal(body, &hfResponse); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	return &hfResponse, nil
}

// makeStreamingRequest makes a streaming HTTP request to the HuggingFace API.
func (m *Model) makeStreamingRequest(ctx context.Context, hfRequest *ChatCompletionRequest) (*http.Response, error) {
	// TODO: Implement tRPC client support
	if m.useTRPC {
		return nil, errors.New("tRPC client support not yet implemented")
	}

	// Marshal request to JSON
	requestBody, err := m.marshalRequest(hfRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	// Create HTTP request
	url := fmt.Sprintf("%s/models/%s/v1/chat/completions", m.baseURL, m.name)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	m.setHeaders(req)
	req.Header.Set("Accept", "text/event-stream")

	// Make request
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to make request: %w", err)
	}

	// Check for error response
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		var errResp ErrorResponse
		if err := json.Unmarshal(body, &errResp); err != nil {
			return nil, fmt.Errorf("API error (status %d): %s", resp.StatusCode, string(body))
		}
		return nil, fmt.Errorf("API error: %s", errResp.Error.Message)
	}

	return resp, nil
}

// setHeaders sets the HTTP headers for the request.
func (m *Model) setHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", m.apiKey))

	// Add extra headers
	for k, v := range m.extraHeaders {
		req.Header.Set(k, v)
	}
}

// marshalRequest marshals the request to JSON, including extra fields.
func (m *Model) marshalRequest(hfRequest *ChatCompletionRequest) ([]byte, error) {
	// Marshal the base request
	baseJSON, err := json.Marshal(hfRequest)
	if err != nil {
		return nil, err
	}

	// If no extra fields, return base JSON
	if len(m.extraFields) == 0 && len(hfRequest.ExtraFields) == 0 {
		return baseJSON, nil
	}

	// Unmarshal to map to merge extra fields
	var requestMap map[string]interface{}
	if err := json.Unmarshal(baseJSON, &requestMap); err != nil {
		return nil, err
	}

	// Merge model-level extra fields
	for k, v := range m.extraFields {
		requestMap[k] = v
	}

	// Merge request-level extra fields (takes precedence)
	for k, v := range hfRequest.ExtraFields {
		requestMap[k] = v
	}

	// Marshal back to JSON
	return json.Marshal(requestMap)
}

// applyTokenTailoring applies token tailoring to the request.
func (m *Model) applyTokenTailoring(originalRequest *model.Request, hfRequest *ChatCompletionRequest) error {
	if m.tokenCounter == nil || m.tailoringStrategy == nil {
		return errors.New("token counter or tailoring strategy not initialized")
	}

	// Calculate current token count
	currentTokens := m.tokenCounter.CountTokens(originalRequest)

	// Check if tailoring is needed
	if currentTokens <= m.maxInputTokens {
		return nil
	}

	// Apply tailoring strategy
	tailoredRequest, err := m.tailoringStrategy.Tailor(originalRequest, m.maxInputTokens, m.tokenCounter)
	if err != nil {
		return fmt.Errorf("failed to tailor request: %w", err)
	}

	// Convert tailored request back to HuggingFace format
	tailoredHFRequest, err := m.convertRequest(tailoredRequest)
	if err != nil {
		return fmt.Errorf("failed to convert tailored request: %w", err)
	}

	// Update the request
	*hfRequest = *tailoredHFRequest

	return nil
}
