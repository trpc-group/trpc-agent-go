// Package models provides implementations of the model interface.
package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const (
	defaultOpenAITimeout            = 120 * time.Second
	defaultOpenAIBaseURL            = "https://api.openai.com/v1"
	defaultOpenAICompletionEndpoint = "/completions"
	defaultOpenAIChatEndpoint       = "/chat/completions"
)

// OpenAIModel implements the Model interface for OpenAI API.
type OpenAIModel struct {
	name           string
	apiKey         string
	baseURL        string
	client         *http.Client
	defaultOptions model.GenerationOptions
	tools          []model.ToolDefinition
}

// OpenAIModelOption is a function that configures an OpenAIModel.
type OpenAIModelOption func(*OpenAIModel)

// WithOpenAIAPIKey sets the API key for the OpenAI model.
func WithOpenAIAPIKey(apiKey string) OpenAIModelOption {
	return func(m *OpenAIModel) {
		m.apiKey = apiKey
	}
}

// WithOpenAIBaseURL sets the base URL for the OpenAI API.
func WithOpenAIBaseURL(baseURL string) OpenAIModelOption {
	return func(m *OpenAIModel) {
		m.baseURL = baseURL
	}
}

// WithOpenAIClient sets the HTTP client for the OpenAI model.
func WithOpenAIClient(client *http.Client) OpenAIModelOption {
	return func(m *OpenAIModel) {
		m.client = client
	}
}

// WithOpenAIDefaultOptions sets the default generation options for the OpenAI model.
func WithOpenAIDefaultOptions(options model.GenerationOptions) OpenAIModelOption {
	return func(m *OpenAIModel) {
		m.defaultOptions = options
	}
}

// NewOpenAIModel creates a new OpenAIModel with the given options.
func NewOpenAIModel(name string, opts ...OpenAIModelOption) *OpenAIModel {
	m := &OpenAIModel{
		name:           name,
		baseURL:        defaultOpenAIBaseURL,
		client:         &http.Client{Timeout: defaultOpenAITimeout},
		defaultOptions: model.DefaultOptions(),
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Name returns the name of the model.
func (m *OpenAIModel) Name() string {
	return m.name
}

// Provider returns the provider of the model.
func (m *OpenAIModel) Provider() string {
	return "openai"
}

// Generate generates a completion for the given prompt.
func (m *OpenAIModel) Generate(ctx context.Context, prompt string, options model.GenerationOptions) (*model.Response, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}

	mergedOptions := m.mergeOptions(options)

	// Prepare the request body
	reqBody := map[string]interface{}{
		"model":             m.name,
		"prompt":            prompt,
		"temperature":       mergedOptions.Temperature,
		"max_tokens":        mergedOptions.MaxTokens,
		"top_p":             mergedOptions.TopP,
		"frequency_penalty": mergedOptions.FrequencyPenalty,
		"presence_penalty":  mergedOptions.PresencePenalty,
	}

	if len(mergedOptions.StopSequences) > 0 {
		reqBody["stop"] = mergedOptions.StopSequences
	}

	// Create JSON body
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.baseURL+defaultOpenAICompletionEndpoint, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	// Send request
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var openaiResp struct {
		Choices []struct {
			Text         string `json:"text"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &openaiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(openaiResp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI API returned no choices")
	}

	// Create model response
	response := &model.Response{
		Text:         openaiResp.Choices[0].Text,
		FinishReason: openaiResp.Choices[0].FinishReason,
		Usage: &model.Usage{
			PromptTokens:     openaiResp.Usage.PromptTokens,
			CompletionTokens: openaiResp.Usage.CompletionTokens,
			TotalTokens:      openaiResp.Usage.TotalTokens,
		},
	}

	return response, nil
}

// GenerateWithMessages generates a completion for the given messages.
func (m *OpenAIModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (*model.Response, error) {
	if m.apiKey == "" {
		return nil, fmt.Errorf("OpenAI API key is required")
	}

	mergedOptions := m.mergeOptions(options)

	// Convert our messages to OpenAI messages format
	openaiMessages := make([]map[string]interface{}, 0, len(messages))
	for _, msg := range messages {
		openaiMessage := map[string]interface{}{
			"role":    string(msg.Role),
			"content": msg.Content,
		}
		openaiMessages = append(openaiMessages, openaiMessage)
	}

	// Prepare the request body
	reqBody := map[string]interface{}{
		"model":             m.name,
		"messages":          openaiMessages,
		"temperature":       mergedOptions.Temperature,
		"max_tokens":        mergedOptions.MaxTokens,
		"top_p":             mergedOptions.TopP,
		"frequency_penalty": mergedOptions.FrequencyPenalty,
		"presence_penalty":  mergedOptions.PresencePenalty,
	}

	if len(mergedOptions.StopSequences) > 0 {
		reqBody["stop"] = mergedOptions.StopSequences
	}

	// Add tools if needed
	if mergedOptions.EnableToolCalls && len(m.tools) > 0 {
		tools := make([]map[string]interface{}, 0, len(m.tools))
		for _, tool := range m.tools {
			tools = append(tools, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        tool.Name,
					"description": tool.Description,
					"parameters":  tool.Parameters,
				},
			})
		}
		reqBody["tools"] = tools
	}

	// Create JSON body
	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	// Create request
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		m.baseURL+defaultOpenAIChatEndpoint, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.apiKey)

	// Send request
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenAI API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Parse response
	var openaiResp struct {
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	if err := json.Unmarshal(body, &openaiResp); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %w", err)
	}

	if len(openaiResp.Choices) == 0 {
		return nil, fmt.Errorf("OpenAI API returned no choices")
	}

	// Create response messages
	responseMessages := []*message.Message{
		message.NewMessage(
			message.Role(openaiResp.Choices[0].Message.Role),
			openaiResp.Choices[0].Message.Content,
		),
	}

	// Create model response
	response := &model.Response{
		Text:         openaiResp.Choices[0].Message.Content,
		Messages:     responseMessages,
		FinishReason: openaiResp.Choices[0].FinishReason,
		Usage: &model.Usage{
			PromptTokens:     openaiResp.Usage.PromptTokens,
			CompletionTokens: openaiResp.Usage.CompletionTokens,
			TotalTokens:      openaiResp.Usage.TotalTokens,
		},
	}

	return response, nil
}

// SetTools implements the ToolCallSupportingModel interface.
func (m *OpenAIModel) SetTools(tools []model.ToolDefinition) error {
	m.tools = tools
	return nil
}

// mergeOptions merges the provided options with the default options.
func (m *OpenAIModel) mergeOptions(options model.GenerationOptions) model.GenerationOptions {
	result := m.defaultOptions

	// Only override non-zero values
	if options.Temperature != 0 {
		result.Temperature = options.Temperature
	}
	if options.MaxTokens != 0 {
		result.MaxTokens = options.MaxTokens
	}
	if options.TopP != 0 {
		result.TopP = options.TopP
	}
	if options.TopK != 0 {
		result.TopK = options.TopK
	}
	if options.PresencePenalty != 0 {
		result.PresencePenalty = options.PresencePenalty
	}
	if options.FrequencyPenalty != 0 {
		result.FrequencyPenalty = options.FrequencyPenalty
	}
	if len(options.StopSequences) > 0 {
		result.StopSequences = options.StopSequences
	}
	if options.EnableToolCalls {
		result.EnableToolCalls = true
	}

	return result
}
