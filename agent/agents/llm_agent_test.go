package agents

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// mockModel implements a simple model for testing
type mockModel struct {
	mockResponse *model.Response
	mockError    error
}

func (m *mockModel) Name() string {
	return "mock-model"
}

func (m *mockModel) Provider() string {
	return "mock-provider"
}

func (m *mockModel) Generate(ctx context.Context, prompt string, options model.GenerationOptions) (*model.Response, error) {
	if m.mockError != nil {
		return nil, m.mockError
	}
	return m.mockResponse, nil
}

func (m *mockModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (*model.Response, error) {
	if m.mockError != nil {
		return nil, m.mockError
	}
	return m.mockResponse, nil
}

func TestNewLLMAgent(t *testing.T) {
	// Test with minimal config
	mockMdl := &mockModel{
		mockResponse: &model.Response{
			Text: "Test response",
		},
	}

	config := LLMAgentConfig{
		Name:        "TestAgent",
		Description: "A test LLM agent",
		Model:       mockMdl,
	}

	agent, err := NewLLMAgent(config)
	assert.NoError(t, err)
	assert.NotNil(t, agent)
	assert.Equal(t, "TestAgent", agent.Name())
	assert.Equal(t, "A test LLM agent", agent.Description())

	// Test without model (should fail)
	invalidConfig := LLMAgentConfig{
		Name:        "TestAgent",
		Description: "A test LLM agent",
	}

	_, err = NewLLMAgent(invalidConfig)
	assert.Error(t, err)
	assert.Equal(t, ErrModelRequired, err)
}

func TestLLMAgent_Run(t *testing.T) {
	// Create a model that returns a fixed response
	mockMdl := &mockModel{
		mockResponse: &model.Response{
			Messages: []*message.Message{
				message.NewAssistantMessage("This is a test response"),
			},
		},
	}

	// Create an LLM agent
	agent, err := NewLLMAgent(LLMAgentConfig{
		Name:         "TestAgent",
		Description:  "A test LLM agent",
		Model:        mockMdl,
		SystemPrompt: "You are a test assistant",
	})
	assert.NoError(t, err)

	// Run the agent
	userMsg := message.NewUserMessage("Hello, world!")
	response, err := agent.Run(context.Background(), userMsg)

	// Assert expectations
	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, "This is a test response", response.Content)

	// Test with text response instead of message
	mockMdl.mockResponse = &model.Response{
		Text:     "Text response",
		Messages: nil,
	}

	response, err = agent.Run(context.Background(), userMsg)
	assert.NoError(t, err)
	assert.NotNil(t, response)
	assert.Equal(t, "Text response", response.Content)

	// Test with error
	mockMdl.mockError = assert.AnError
	response, err = agent.Run(context.Background(), userMsg)
	assert.Error(t, err)
	assert.Nil(t, response)
}

func TestLLMAgent_GetConversationHistory(t *testing.T) {
	// Create a memory with some messages
	mem := memory.NewBaseMemory()
	ctx := context.Background()

	// Add messages to memory
	messages := []*message.Message{
		message.NewUserMessage("Hello"),
		message.NewAssistantMessage("Hi there"),
		message.NewUserMessage("How are you?"),
		message.NewAssistantMessage("I'm good"),
	}

	for _, msg := range messages {
		_ = mem.Store(ctx, msg)
	}

	// Create an agent with limited history
	agent, _ := NewLLMAgent(LLMAgentConfig{
		Name:               "TestAgent",
		Description:        "A test LLM agent",
		Model:              &mockModel{},
		Memory:             mem,
		MaxHistoryMessages: 2, // Only get the last 2 messages
	})

	// Get conversation history
	history, err := agent.getConversationHistory(ctx)

	// Verify expectations
	assert.NoError(t, err)
	assert.Len(t, history, 2)
	assert.Equal(t, "How are you?", history[0].Content)
	assert.Equal(t, "I'm good", history[1].Content)
}

func TestLLMAgent_RunAsync(t *testing.T) {
	// Skip test if streaming implementation is not complete
	t.Skip("Streaming implementation pending")
}
