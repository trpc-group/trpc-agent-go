// Package model provides implementations of the model interface.
package model

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/core/message"
	"trpc.group/trpc-go/trpc-agent-go/log"
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
func (m *OpenAIStreamingModel) GenerateStream(ctx context.Context, prompt string, options GenerationOptions) (<-chan *Response, error) {
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
	respCh := make(chan *Response)

	// Send request and process stream
	go func() {
		defer close(respCh)

		// Send request
		resp, err := m.client.Do(req)
		if err != nil {
			respCh <- &Response{
				FinishReason: "error",
				Text:         fmt.Sprintf("Error: %v", err),
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			respCh <- &Response{
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
				respCh <- &Response{
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
			respCh <- &Response{
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
func (m *OpenAIStreamingModel) GenerateStreamWithMessages(ctx context.Context, messages []*message.Message, options GenerationOptions) (<-chan *Response, error) {
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
	log.Debugf("=== OpenAI Streaming Request Body ===\n%s\n=== End of OpenAI Streaming Request Body ===\n", string(jsonBody))
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
	respCh := make(chan *Response)

	// Send request and process stream
	go func() {
		defer close(respCh)

		// Send request
		resp, err := m.client.Do(req)
		if err != nil {
			log.Errorf("OpenAI API request failed: %v", err)
			respCh <- &Response{
				FinishReason: "error",
				Text:         fmt.Sprintf("Error: %v", err),
			}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			bodyBytes, _ := io.ReadAll(resp.Body)
			log.Errorf("OpenAI API returned status %d: %s", resp.StatusCode, string(bodyBytes))
			respCh <- &Response{
				FinishReason: "error",
				Text:         fmt.Sprintf("OpenAI API returned status %d: %s", resp.StatusCode, string(bodyBytes)),
			}
			return
		}
		// Process the stream
		reader := bufio.NewReader(resp.Body)
		// Add state for tracking tool calls
		toolCallsBuffer := make(map[int]*ToolCall)
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

			log.Debugf("Received line: %s", line)

			// Skip the "data: " prefix
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			// Check for stream end
			if data == "[DONE]" {
				respCh <- &Response{
					FinishReason: "stop",
				}
				return
			}

			// Parse the JSON
			var streamResp struct {
				Choices []struct {
					Delta struct {
						Role      string `json:"role,omitempty"`
						Content   string `json:"content,omitempty"`
						ToolCalls []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls,omitempty"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason,omitempty"`
				} `json:"choices"`
			}

			if err := json.Unmarshal([]byte(data), &streamResp); err != nil {
				// Skip malformed JSON
				log.Errorf("Failed to unmarshal OpenAI stream response: %v", err)
				continue
			}
			if len(streamResp.Choices) == 0 {
				continue
			}

			// Check for tool calls in the delta and accumulate them
			if len(streamResp.Choices[0].Delta.ToolCalls) > 0 {
				for _, call := range streamResp.Choices[0].Delta.ToolCalls {
					// Check if this is a new tool call (with ID) or continuation of existing one
					if call.ID != "" {
						// This is a new tool call (first chunk)
						toolCallsBuffer[call.Index] = &ToolCall{
							ID:   call.ID,
							Type: "function",
							Function: FunctionCall{
								Name:      call.Function.Name,
								Arguments: call.Function.Arguments,
							},
						}
						log.Debugf("Initializing new tool call buffer for Index: %d, ID: %s", call.Index, call.ID)
					} else if existingCall, exists := toolCallsBuffer[call.Index]; exists {
						// This is a continuation chunk for an existing tool call
						// Append to name and arguments
						if call.Function.Name != "" {
							existingCall.Function.Name += call.Function.Name
						}
						if call.Function.Arguments != "" {
							existingCall.Function.Arguments += call.Function.Arguments
						}
						log.Debugf("Updated tool call buffer for Index %d: name=%s, args=%s",
							call.Index,
							existingCall.Function.Name,
							existingCall.Function.Arguments)
					} else {
						log.Warnf("Received continuation for unknown tool call at index %d - this shouldn't happen", call.Index)
					}
				}
			}

			// Send completed tool calls if we have a tool_calls finish reason
			if streamResp.Choices[0].FinishReason == "tool_calls" {
				// Convert the buffered tool calls to a slice
				var completedToolCalls []ToolCall
				for _, toolCall := range toolCallsBuffer {
					// Attempt to validate JSON for debugging
					if toolCall.Function.Arguments != "" {
						var jsonTest map[string]interface{}
						if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &jsonTest); err != nil {
							log.Warnf("Completed tool call has invalid JSON arguments: %v", err)
						} else {
							log.Debugf("Valid JSON arguments in completed tool call: %+v", jsonTest)
						}
					}

					completedToolCalls = append(completedToolCalls, *toolCall)
				}

				if len(completedToolCalls) > 0 {
					log.Debugf("Sending %d completed tool calls", len(completedToolCalls))
					respCh <- &Response{
						ToolCalls:    completedToolCalls,
						FinishReason: "tool_calls",
					}
				}

				// Clear the buffer after sending
				toolCallsBuffer = make(map[int]*ToolCall)
			}

			// Only send non-empty content
			if streamResp.Choices[0].Delta.Content != "" {
				respMsg := message.NewMessage(
					message.Role(streamResp.Choices[0].Delta.Role),
					streamResp.Choices[0].Delta.Content,
				)

				respCh <- &Response{
					Text:     streamResp.Choices[0].Delta.Content,
					Messages: []*message.Message{respMsg},
				}
			}

			// If we have a stop finish reason, we're done
			if streamResp.Choices[0].FinishReason == "stop" {
				respCh <- &Response{
					FinishReason: "stop",
				}
				return
			}
		}
	}()

	return respCh, nil
}
