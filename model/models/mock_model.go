// Package models provides implementations of the model interface.
package models

import (
	"context"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// MockModel is a mock implementation of the Model interface for testing.
type MockModel struct {
	name                 string
	provider             string
	responseText         string
	responseMessages     []*message.Message
	responseShouldError  bool
	responseError        error
	tools                []model.ToolDefinition
	supportToolCalls     bool
	lastGeneratePrompt   string
	lastGenerateMessages []*message.Message
	lastGenerateOptions  model.GenerationOptions
}

// MockModelOption is a function that configures a MockModel.
type MockModelOption func(*MockModel)

// WithResponseText sets the response text for the mock model.
func WithResponseText(text string) MockModelOption {
	return func(m *MockModel) {
		m.responseText = text
	}
}

// WithResponseMessages sets the response messages for the mock model.
func WithResponseMessages(messages []*message.Message) MockModelOption {
	return func(m *MockModel) {
		m.responseMessages = messages
	}
}

// WithResponseError sets the response to return an error.
func WithResponseError(err error) MockModelOption {
	return func(m *MockModel) {
		m.responseShouldError = true
		m.responseError = err
	}
}

// WithToolCallSupport configures the mock model to support tool calls.
func WithToolCallSupport() MockModelOption {
	return func(m *MockModel) {
		m.supportToolCalls = true
	}
}

// NewMockModel creates a new MockModel with the given options.
func NewMockModel(name, provider string, opts ...MockModelOption) *MockModel {
	m := &MockModel{
		name:             name,
		provider:         provider,
		responseText:     "This is a response from the mock model.",
		responseMessages: nil,
	}

	for _, opt := range opts {
		opt(m)
	}

	return m
}

// Name returns the name of the model.
func (m *MockModel) Name() string {
	return m.name
}

// Provider returns the provider of the model.
func (m *MockModel) Provider() string {
	return m.provider
}

// Generate generates a completion for the given prompt.
func (m *MockModel) Generate(ctx context.Context, prompt string, options model.GenerationOptions) (*model.Response, error) {
	m.lastGeneratePrompt = prompt
	m.lastGenerateOptions = options

	if m.responseShouldError {
		return nil, m.responseError
	}

	// Create a simple response based on the prompt
	responseText := m.responseText
	if responseText == "" {
		responseText = fmt.Sprintf("Response to: %s", prompt)
	}

	resp := &model.Response{
		Text: responseText,
		Usage: &model.Usage{
			PromptTokens:     len(strings.Split(prompt, " ")),
			CompletionTokens: len(strings.Split(responseText, " ")),
		},
		FinishReason: "stop",
	}
	resp.Usage.TotalTokens = resp.Usage.PromptTokens + resp.Usage.CompletionTokens

	return resp, nil
}

// GenerateWithMessages generates a completion for the given messages.
func (m *MockModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (*model.Response, error) {
	m.lastGenerateMessages = messages
	m.lastGenerateOptions = options

	if m.responseShouldError {
		return nil, m.responseError
	}

	// If responseMessages is set, use that
	if m.responseMessages != nil && len(m.responseMessages) > 0 {
		resp := &model.Response{
			Messages: m.responseMessages,
			Usage: &model.Usage{
				PromptTokens:     100, // Mock values
				CompletionTokens: 50,
				TotalTokens:      150,
			},
			FinishReason: "stop",
		}
		return resp, nil
	}

	// Otherwise, create a simple response based on the messages
	var lastContent string
	if len(messages) > 0 {
		lastContent = messages[len(messages)-1].Content
	}

	responseText := m.responseText
	if responseText == "" {
		responseText = fmt.Sprintf("Response to messages, last message: %s", lastContent)
	}

	resp := &model.Response{
		Text: responseText,
		Messages: []*message.Message{
			message.NewAssistantMessage(responseText),
		},
		Usage: &model.Usage{
			PromptTokens:     100, // Mock values
			CompletionTokens: 50,
			TotalTokens:      150,
		},
		FinishReason: "stop",
	}

	return resp, nil
}

// SetTools implements the ToolCallSupportingModel interface.
func (m *MockModel) SetTools(tools []model.ToolDefinition) error {
	if !m.supportToolCalls {
		return fmt.Errorf("mock model does not support tool calls")
	}
	m.tools = tools
	return nil
}

// MockStreamingModel extends MockModel to implement StreamingModel.
type MockStreamingModel struct {
	MockModel
	streamDelay        int // Delay in milliseconds between stream chunks
	streamChunkSize    int // Number of characters per chunk
	streamShouldError  bool
	streamError        error
	streamNumChunks    int  // If > 0, limit to this many chunks
	streamRandomizeEnd bool // If true, randomly end streaming early
}

// MockStreamingModelOption is a function that configures a MockStreamingModel.
type MockStreamingModelOption func(*MockStreamingModel)

// WithStreamDelay sets the delay between stream chunks.
func WithStreamDelay(delay int) MockStreamingModelOption {
	return func(m *MockStreamingModel) {
		m.streamDelay = delay
	}
}

// WithStreamChunkSize sets the size of stream chunks.
func WithStreamChunkSize(size int) MockStreamingModelOption {
	return func(m *MockStreamingModel) {
		m.streamChunkSize = size
	}
}

// WithStreamError sets the stream to return an error.
func WithStreamError(err error) MockStreamingModelOption {
	return func(m *MockStreamingModel) {
		m.streamShouldError = true
		m.streamError = err
	}
}

// NewMockStreamingModel creates a new MockStreamingModel.
func NewMockStreamingModel(name, provider string, mockOpts []MockModelOption, streamOpts ...MockStreamingModelOption) *MockStreamingModel {
	mockModel := NewMockModel(name, provider, mockOpts...)

	m := &MockStreamingModel{
		MockModel:       *mockModel,
		streamDelay:     0,
		streamChunkSize: 10,
	}

	for _, opt := range streamOpts {
		opt(m)
	}

	return m
}

// GenerateStream streams a completion for the given prompt.
func (m *MockStreamingModel) GenerateStream(ctx context.Context, prompt string, options model.GenerationOptions) (<-chan *model.Response, error) {
	m.lastGeneratePrompt = prompt
	m.lastGenerateOptions = options

	if m.streamShouldError {
		return nil, m.streamError
	}

	// Create the response channel
	respCh := make(chan *model.Response)

	// Start a goroutine to send chunks through the channel
	go func() {
		defer close(respCh)

		// Get the full response text
		responseText := m.responseText
		if responseText == "" {
			responseText = fmt.Sprintf("Response to: %s", prompt)
		}

		// Stream the response in chunks
		for i := 0; i < len(responseText); i += m.streamChunkSize {
			// Check if context is canceled
			select {
			case <-ctx.Done():
				return
			default:
				// Continue
			}

			end := i + m.streamChunkSize
			if end > len(responseText) {
				end = len(responseText)
			}

			chunk := responseText[i:end]
			respCh <- &model.Response{
				Text: chunk,
				Usage: &model.Usage{
					PromptTokens:     0,
					CompletionTokens: len(strings.Split(chunk, " ")),
					TotalTokens:      len(strings.Split(chunk, " ")),
				},
			}
		}

		// Send a final chunk with the finish reason
		respCh <- &model.Response{
			FinishReason: "stop",
		}
	}()

	return respCh, nil
}

// GenerateStreamWithMessages streams a completion for the given messages.
func (m *MockStreamingModel) GenerateStreamWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (<-chan *model.Response, error) {
	m.lastGenerateMessages = messages
	m.lastGenerateOptions = options

	if m.streamShouldError {
		return nil, m.streamError
	}

	// Create the response channel
	respCh := make(chan *model.Response)

	// Start a goroutine to send chunks through the channel
	go func() {
		defer close(respCh)

		// Get the content to stream
		var contentToStream string
		if m.responseText != "" {
			contentToStream = m.responseText
		} else if len(messages) > 0 {
			contentToStream = fmt.Sprintf("Response to messages, last message: %s", messages[len(messages)-1].Content)
		} else {
			contentToStream = "Response to empty message list"
		}

		// Stream the response in chunks
		for i := 0; i < len(contentToStream); i += m.streamChunkSize {
			// Check if context is canceled
			select {
			case <-ctx.Done():
				return
			default:
				// Continue
			}

			end := i + m.streamChunkSize
			if end > len(contentToStream) {
				end = len(contentToStream)
			}

			chunk := contentToStream[i:end]
			msg := message.NewAssistantMessage(chunk)

			respCh <- &model.Response{
				Text:     chunk,
				Messages: []*message.Message{msg},
			}
		}

		// Send a final chunk with the finish reason
		respCh <- &model.Response{
			FinishReason: "stop",
		}
	}()

	return respCh, nil
}
