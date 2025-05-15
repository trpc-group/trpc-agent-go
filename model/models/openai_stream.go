// Package models provides implementations of the model interface.
package models

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// OpenAIStreamingModel implements the StreamingModel interface for OpenAI.
type OpenAIStreamingModel struct {
	OpenAIModel
}

// NewOpenAIStreamingModel creates a new OpenAIStreamingModel.
func NewOpenAIStreamingModel(name string, opts ...OpenAIModelOption) *OpenAIStreamingModel {
	baseModel := NewOpenAIModel(name, opts...)
	return &OpenAIStreamingModel{
		OpenAIModel: *baseModel,
	}
}

// GenerateStream streams a completion for the given prompt.
func (m *OpenAIStreamingModel) GenerateStream(ctx context.Context, prompt string, options model.GenerationOptions) (<-chan *model.Response, error) {
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
		"stream":            true,
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
	req.Header.Set("Accept", "text/event-stream")

	// Create response channel
	respCh := make(chan *model.Response)

	// Send request and process stream
	go func() {
		defer close(respCh)

		// Send request
		resp, err := m.client.Do(req)
		if err != nil {
			respCh <- &model.Response{
				FinishReason: "error",
				Text:         fmt.Sprintf("Error: %v", err),
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respCh <- &model.Response{
				FinishReason: "error",
				Text:         fmt.Sprintf("OpenAI API returned status %d", resp.StatusCode),
			}
			return
		}

		// Process the stream
		reader := bufio.NewReader(resp.Body)
		for {
			// Check if context is done
			select {
			case <-ctx.Done():
				return
			default:
				// Continue
			}

			// Read a line
			line, err := reader.ReadString('\n')
			if err != nil {
				// End of stream or error
				return
			}

			// Skip empty lines
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Skip the "data: " prefix
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			// Check for stream end
			if data == "[DONE]" {
				respCh <- &model.Response{
					FinishReason: "stop",
				}
				return
			}

			// Parse the JSON
			var streamResp struct {
				Choices []struct {
					Text         string `json:"text"`
					FinishReason string `json:"finish_reason,omitempty"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
				// Skip malformed JSON
				continue
			}

			if len(streamResp.Choices) == 0 {
				continue
			}

			// Send the response chunk
			respCh <- &model.Response{
				Text:         streamResp.Choices[0].Text,
				FinishReason: streamResp.Choices[0].FinishReason,
			}

			// If we have a finish reason, we're done
			if streamResp.Choices[0].FinishReason != "" {
				return
			}
		}
	}()

	return respCh, nil
}

// GenerateStreamWithMessages streams a completion for the given messages.
func (m *OpenAIStreamingModel) GenerateStreamWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (<-chan *model.Response, error) {
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
		"stream":            true,
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
	req.Header.Set("Accept", "text/event-stream")

	// Create response channel
	respCh := make(chan *model.Response)

	// Send request and process stream
	go func() {
		defer close(respCh)

		// Send request
		resp, err := m.client.Do(req)
		if err != nil {
			respCh <- &model.Response{
				FinishReason: "error",
				Text:         fmt.Sprintf("Error: %v", err),
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respCh <- &model.Response{
				FinishReason: "error",
				Text:         fmt.Sprintf("OpenAI API returned status %d", resp.StatusCode),
			}
			return
		}

		// Process the stream
		reader := bufio.NewReader(resp.Body)
		for {
			// Check if context is done
			select {
			case <-ctx.Done():
				return
			default:
				// Continue
			}

			// Read a line
			line, err := reader.ReadString('\n')
			if err != nil {
				// End of stream or error
				return
			}

			// Skip empty lines
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			// Skip the "data: " prefix
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			// Check for stream end
			if data == "[DONE]" {
				respCh <- &model.Response{
					FinishReason: "stop",
				}
				return
			}

			// Parse the JSON
			var streamResp struct {
				Choices []struct {
					Delta struct {
						Role    string `json:"role,omitempty"`
						Content string `json:"content,omitempty"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason,omitempty"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
				// Skip malformed JSON
				continue
			}

			if len(streamResp.Choices) == 0 {
				continue
			}

			// Only send non-empty content
			if streamResp.Choices[0].Delta.Content != "" {
				respMsg := message.NewMessage(
					message.Role(streamResp.Choices[0].Delta.Role),
					streamResp.Choices[0].Delta.Content,
				)

				respCh <- &model.Response{
					Text:     streamResp.Choices[0].Delta.Content,
					Messages: []*message.Message{respMsg},
				}
			}

			// If we have a finish reason, we're done
			if streamResp.Choices[0].FinishReason != "" {
				respCh <- &model.Response{
					FinishReason: streamResp.Choices[0].FinishReason,
				}
				return
			}
		}
	}()

	return respCh, nil
}
