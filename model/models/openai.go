// Package models provides implementations of the model interface.
package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
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
		return nil, errors.New("OpenAI API key is required")
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

	// Log the raw response for debugging
	log.Debugf("### Raw OpenAI API response ###\n%s\n### End of raw OpenAI API response ###", string(body))

	// Define a comprehensive struct that captures both content and tool calls
	type OpenAIResponse struct {
		Choices []struct {
			Message struct {
				Role      string `json:"role"`
				Content   string `json:"content"`
				ToolCalls []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}

	// Try to parse the response using the comprehensive struct
	var openaiResp OpenAIResponse
	if err := json.Unmarshal(body, &openaiResp); err != nil {
		log.Warnf("Failed to unmarshal response as comprehensive format: %v", err)

		// Try a more generic approach for different API implementations
		var genericResp map[string]interface{}
		if jsonErr := json.Unmarshal(body, &genericResp); jsonErr != nil {
			return nil, fmt.Errorf("failed to unmarshal response in any format: %w", jsonErr)
		}

		// Log the structure of the response
		log.Debugf("Response structure: %+v", genericResp)

		// Create a fallback response
		fallbackContent := extractContentFromGenericResponse(genericResp)
		if fallbackContent == "" {
			fallbackContent = "No content was generated. Please try again."
		}

		return &model.Response{
			Text: fallbackContent,
			Messages: []*message.Message{
				message.NewAssistantMessage(fallbackContent),
			},
			FinishReason: "fallback",
			Usage: &model.Usage{
				PromptTokens:     100, // Estimate
				CompletionTokens: len(strings.Split(fallbackContent, " ")),
				TotalTokens:      100 + len(strings.Split(fallbackContent, " ")),
			},
		}, nil
	}

	if len(openaiResp.Choices) == 0 {
		log.Warnf("API returned no choices")

		// Create a generic response for no choices
		genericResponse := "I need to analyze this further."
		return &model.Response{
			Text: genericResponse,
			Messages: []*message.Message{
				message.NewAssistantMessage(genericResponse),
			},
			FinishReason: "no_choices",
		}, nil
	}

	// Extract content and role
	content := openaiResp.Choices[0].Message.Content
	role := openaiResp.Choices[0].Message.Role

	// Extract tool calls if present
	var toolCalls []model.ToolCall
	if len(openaiResp.Choices[0].Message.ToolCalls) > 0 {
		for _, call := range openaiResp.Choices[0].Message.ToolCalls {
			toolCall := model.ToolCall{
				ID: call.ID,
				Function: model.FunctionCall{
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			}
			// Generate ID if not provided by API
			if toolCall.ID == "" {
				toolCall.ID = fmt.Sprintf("call_%s", generateShortID())
			}
			toolCalls = append(toolCalls, toolCall)
			log.Debugf("Extracted tool call: %s(%s)", toolCall.Function.Name, toolCall.Function.Arguments)
		}
	}

	// Create response messages (if we have content)
	var responseMessages []*message.Message
	if content != "" {
		responseMessages = append(responseMessages, message.NewMessage(
			message.Role(role),
			content,
		))
	}

	// Create model response combining both content and tool calls
	response := &model.Response{
		Text:         content,
		Messages:     responseMessages,
		ToolCalls:    toolCalls,
		FinishReason: openaiResp.Choices[0].FinishReason,
		Usage: &model.Usage{
			PromptTokens:     openaiResp.Usage.PromptTokens,
			CompletionTokens: openaiResp.Usage.CompletionTokens,
			TotalTokens:      openaiResp.Usage.TotalTokens,
		},
	}

	return response, nil
}

// extractContentFromGenericResponse attempts to find any text content in a generic response
func extractContentFromGenericResponse(resp map[string]interface{}) string {
	// Try several common paths for content in different API implementations

	// Check for OpenAI-like structure
	if choices, ok := resp["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if message, ok := choice["message"].(map[string]interface{}); ok {
				if content, ok := message["content"].(string); ok && content != "" {
					return content
				}
			}
			// Some APIs put content directly in the choice
			if content, ok := choice["content"].(string); ok && content != "" {
				return content
			}
		}
	}

	// Check for direct content field (some API implementations)
	if content, ok := resp["content"].(string); ok && content != "" {
		return content
	}

	// Check for text field (some API implementations)
	if text, ok := resp["text"].(string); ok && text != "" {
		return text
	}

	// Check for message field at top level
	if message, ok := resp["message"].(map[string]interface{}); ok {
		if content, ok := message["content"].(string); ok && content != "" {
			return content
		}
	}

	// Check for completion field (older APIs)
	if completion, ok := resp["completion"].(string); ok && completion != "" {
		return completion
	}

	// As a last resort, try to find any string field that looks like it might contain content
	for _, value := range resp {
		if strValue, ok := value.(string); ok && len(strValue) > 20 {
			// If we find a reasonably long string, use it
			return strValue
		}
	}

	return ""
}

// generateShortID generates a short random ID for tool calls
func generateShortID() string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	result := make([]byte, 8)
	for i := range result {
		result[i] = chars[rand.Intn(len(chars))]
	}
	return string(result)
}

// SupportsToolCalls implements the model.Model interface.
func (m *OpenAIModel) SupportsToolCalls() bool {
	return true
}

// RegisterTools implements the model.ToolCallSupportingModel interface.
func (m *OpenAIModel) RegisterTools(toolDefs []*tool.ToolDefinition) error {
	// Convert tool.ToolDefinition to model.ToolDefinition
	tools := make([]model.ToolDefinition, 0, len(toolDefs))
	for _, def := range toolDefs {
		// Convert tool.Property map to interface map
		params := make(map[string]interface{})
		if def.Parameters != nil {
			// Create a simple JSON schema object
			params["type"] = "object"
			properties := make(map[string]interface{})
			required := []string{}

			for name, prop := range def.Parameters {
				if prop.Required {
					required = append(required, name)
				}

				// Convert property to map
				propMap := map[string]interface{}{
					"type":        prop.Type,
					"description": prop.Description,
				}

				// Add enum if present
				if len(prop.Enum) > 0 {
					propMap["enum"] = prop.Enum
				}

				// Add default if present
				if prop.Default != nil {
					propMap["default"] = prop.Default
				}

				properties[name] = propMap
			}

			params["properties"] = properties
			if len(required) > 0 {
				params["required"] = required
			}
		}

		tools = append(tools, model.ToolDefinition{
			Name:        def.Name,
			Description: def.Description,
			Parameters:  params,
		})
	}
	m.tools = tools
	return nil
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
