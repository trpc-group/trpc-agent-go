package llmflow

import (
	"context"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// mockModel is a mock implementation of the Model interface for testing
type mockModel struct {
	name     string
	provider string
}

// Name implements the model.Model interface
func (m *mockModel) Name() string {
	return m.name
}

// Provider implements the model.Model interface
func (m *mockModel) Provider() string {
	return m.provider
}

// Generate implements the model.Model interface
func (m *mockModel) Generate(ctx context.Context, prompt string, options model.GenerationOptions) (*model.Response, error) {
	// Just return a dummy response
	return &model.Response{
		Text: "Generated response to: " + prompt,
	}, nil
}

// GenerateWithMessages implements the model.Model interface
func (m *mockModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (*model.Response, error) {
	// Just return a dummy response
	return &model.Response{
		Text: "Generated response to messages",
		Messages: []*message.Message{
			message.NewAssistantMessage("Response to messages"),
		},
	}, nil
}

// NewMockModel creates a new mock model for testing
func NewMockModel() model.Model {
	return &mockModel{
		name:     "mock-model",
		provider: "mock-provider",
	}
}

func TestNewLLMFlow(t *testing.T) {
	name := "test-llm-flow"
	desc := "Test LLM flow"
	mockModel := NewMockModel()

	flow := NewLLMFlow(name, desc, mockModel)

	if flow.Name() != name {
		t.Errorf("Expected name to be %s, got %s", name, flow.Name())
	}

	if flow.Description() != desc {
		t.Errorf("Expected description to be %s, got %s", desc, flow.Description())
	}

	if flow.model == nil {
		t.Error("Expected model to be set")
	}

	if flow.streaming {
		t.Error("Expected streaming to be false by default")
	}

	if flow.systemMessage != "" {
		t.Error("Expected system message to be empty by default")
	}
}

func TestNewLLMFlow_WithOptions(t *testing.T) {
	name := "test-llm-flow"
	desc := "Test LLM flow"
	mockModel := NewMockModel()
	systemMsg := "System prompt"

	flow := NewLLMFlow(name, desc, mockModel,
		WithSystemMessage(systemMsg),
		WithStreaming(true),
	)

	if flow.systemMessage != systemMsg {
		t.Errorf("Expected system message to be %s, got %s", systemMsg, flow.systemMessage)
	}

	if !flow.streaming {
		t.Error("Expected streaming to be true")
	}
}

func TestLLMFlow_Run(t *testing.T) {
	mockModel := NewMockModel()
	flow := NewLLMFlow("test", "test", mockModel)

	input := message.NewUserMessage("Test input")
	resp, err := flow.Run(context.Background(), input)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if resp == nil {
		t.Fatal("Expected response, got nil")
	}

	if resp.Role != message.RoleAssistant {
		t.Errorf("Expected role to be assistant, got %s", resp.Role)
	}

	if resp.Content == "" {
		t.Error("Expected response content to be non-empty")
	}
}
