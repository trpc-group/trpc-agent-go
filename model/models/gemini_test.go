package models

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/message"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Mock server for Gemini API
func setupGeminiMockServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		
		// Basic response for any request
		resp := `{
			"candidates": [
				{
					"content": {
						"parts": [
							{"text": "This is a mock response from Gemini API"}
						],
						"role": "model"
					},
					"finishReason": "STOP",
					"safetyRatings": []
				}
			]
		}`
		
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(resp))
	}))
}

func TestNewGeminiModel(t *testing.T) {
	// Test with API key option
	t.Run("WithAPIKeyOption", func(t *testing.T) {
		m, err := NewGeminiModel("gemini-test", WithGeminiAPIKey("test-api-key"))
		require.NoError(t, err)
		assert.Equal(t, "gemini-test", m.Name())
		assert.Equal(t, "google", m.Provider())
		assert.Equal(t, "test-api-key", m.apiKey)
	})

	// Test with default options
	t.Run("WithDefaultOptions", func(t *testing.T) {
		customOptions := model.GenerationOptions{
			Temperature:      0.5,
			MaxTokens:        100,
			TopP:             0.9,
			TopK:             40,
			PresencePenalty:  0.2,
			FrequencyPenalty: 0.3,
			StopSequences:    []string{"stop1", "stop2"},
		}

		m, err := NewGeminiModel(
			"gemini-test",
			WithGeminiAPIKey("test-api-key"),
			WithGeminiDefaultOptions(customOptions),
		)

		require.NoError(t, err)
		assert.Equal(t, customOptions.Temperature, m.defaultOptions.Temperature)
		assert.Equal(t, customOptions.MaxTokens, m.defaultOptions.MaxTokens)
		assert.Equal(t, customOptions.TopP, m.defaultOptions.TopP)
		assert.Equal(t, customOptions.TopK, m.defaultOptions.TopK)
		assert.Equal(t, customOptions.PresencePenalty, m.defaultOptions.PresencePenalty)
		assert.Equal(t, customOptions.FrequencyPenalty, m.defaultOptions.FrequencyPenalty)
		assert.Equal(t, customOptions.StopSequences, m.defaultOptions.StopSequences)
	})

	// Test with environment variable
	t.Run("WithEnvironmentVariable", func(t *testing.T) {
		// Save original env var if it exists
		originalKey := os.Getenv("GOOGLE_API_KEY")
		defer os.Setenv("GOOGLE_API_KEY", originalKey)

		// Set test value
		os.Setenv("GOOGLE_API_KEY", "env-api-key")

		m, err := NewGeminiModel("gemini-test")
		require.NoError(t, err)
		assert.Equal(t, "env-api-key", m.apiKey)
	})

	// Test with missing API key
	t.Run("WithMissingAPIKey", func(t *testing.T) {
		// Save original env var if it exists
		originalKey := os.Getenv("GOOGLE_API_KEY")
		defer os.Setenv("GOOGLE_API_KEY", originalKey)

		// Clear environment variable
		os.Setenv("GOOGLE_API_KEY", "")

		_, err := NewGeminiModel("gemini-test")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "API key is required")
	})
}

// MockGeminiModel is a test implementation of the GeminiModel
type MockGeminiModel struct {
	GeminiModel
}

// Generate overrides the real implementation for testing
func (m *MockGeminiModel) Generate(ctx context.Context, prompt string, options model.GenerationOptions) (*model.Response, error) {
	// Create a mock response
	responseText := "This is a mock response from Gemini API"
	promptTokens := len(prompt) / 4  // Simple estimation
	responseTokens := len(responseText) / 4
	
	return &model.Response{
		Text: responseText,
		Usage: &model.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: responseTokens,
			TotalTokens:      promptTokens + responseTokens,
		},
		FinishReason: "stop",
	}, nil
}

// GenerateWithMessages overrides the real implementation for testing
func (m *MockGeminiModel) GenerateWithMessages(ctx context.Context, messages []*message.Message, options model.GenerationOptions) (*model.Response, error) {
	// Create a mock response
	responseText := "This is a mock response from Gemini API"
	responseMessage := message.NewAssistantMessage(responseText)
	
	// Calculate token usage (simple estimation)
	promptTokens := 0
	for _, msg := range messages {
		promptTokens += len(msg.Content) / 4
	}
	responseTokens := len(responseText) / 4
	
	return &model.Response{
		Text: responseText,
		Messages: []*message.Message{
			responseMessage,
		},
		Usage: &model.Usage{
			PromptTokens:     promptTokens,
			CompletionTokens: responseTokens,
			TotalTokens:      promptTokens + responseTokens,
		},
		FinishReason: "stop",
	}, nil
}

// createTestGeminiModel creates a mock model for testing
func createTestGeminiModel() *MockGeminiModel {
	return &MockGeminiModel{
		GeminiModel: GeminiModel{
			name:           "gemini-test",
			apiKey:         "test-api-key",
			defaultOptions: model.DefaultOptions(),
		},
	}
}

func TestGeminiModelGenerate(t *testing.T) {
	m := createTestGeminiModel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test Generate method
	t.Run("Generate", func(t *testing.T) {
		prompt := "Hello, Gemini!"
		resp, err := m.Generate(ctx, prompt, model.GenerationOptions{})

		require.NoError(t, err)
		assert.NotEmpty(t, resp.Text)
		assert.Equal(t, "This is a mock response from Gemini API", resp.Text)
		assert.Equal(t, "stop", resp.FinishReason)
		assert.NotNil(t, resp.Usage)
	})

	// Test with custom options
	t.Run("GenerateWithCustomOptions", func(t *testing.T) {
		prompt := "Test with options"
		options := model.GenerationOptions{
			Temperature: 0.7,
			MaxTokens:   50,
		}

		resp, err := m.Generate(ctx, prompt, options)

		require.NoError(t, err)
		assert.NotEmpty(t, resp.Text)
		assert.Equal(t, "This is a mock response from Gemini API", resp.Text)
	})
}

func TestGeminiModelGenerateWithMessages(t *testing.T) {
	m := createTestGeminiModel()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Test with messages
	t.Run("WithMessages", func(t *testing.T) {
		messages := []*message.Message{
			message.NewUserMessage("Hello"),
			message.NewAssistantMessage("Hi there"),
			message.NewUserMessage("How are you?"),
		}

		resp, err := m.GenerateWithMessages(ctx, messages, model.GenerationOptions{})

		require.NoError(t, err)
		assert.NotEmpty(t, resp.Text)
		assert.Equal(t, "This is a mock response from Gemini API", resp.Text)
		assert.NotNil(t, resp.Messages)
		assert.Equal(t, 1, len(resp.Messages))
		assert.Equal(t, message.RoleAssistant, resp.Messages[0].Role)
	})

	// Test with empty messages
	t.Run("WithEmptyMessages", func(t *testing.T) {
		resp, err := m.GenerateWithMessages(ctx, []*message.Message{}, model.GenerationOptions{})

		require.NoError(t, err)
		assert.NotEmpty(t, resp.Text)
		assert.Equal(t, "This is a mock response from Gemini API", resp.Text)
	})

	// Test with custom options
	t.Run("WithCustomOptions", func(t *testing.T) {
		messages := []*message.Message{
			message.NewUserMessage("Test with options"),
		}

		options := model.GenerationOptions{
			Temperature: 0.3,
			MaxTokens:   100,
		}

		resp, err := m.GenerateWithMessages(ctx, messages, options)

		require.NoError(t, err)
		assert.NotEmpty(t, resp.Text)
		assert.Equal(t, "This is a mock response from Gemini API", resp.Text)
	})
}

func TestGeminiModelSetTools(t *testing.T) {
	m, err := NewGeminiModel("gemini-test", WithGeminiAPIKey("test-api-key"))
	require.NoError(t, err)

	// Define some test tools
	tools := []model.ToolDefinition{
		{
			Name:        "calculator",
			Description: "A calculator tool",
			Parameters: map[string]interface{}{
				"operation": map[string]interface{}{
					"type":        "string",
					"description": "The operation to perform",
					"required":    true,
				},
			},
		},
	}

	// Test setting tools
	err = m.SetTools(tools)
	require.NoError(t, err)
	assert.Equal(t, tools, m.tools)
}

func TestGeminiModelMergeOptions(t *testing.T) {
	defaultOptions := model.GenerationOptions{
		Temperature:      0.7,
		MaxTokens:        1000,
		TopP:             1.0,
		TopK:             50,
		PresencePenalty:  0.0,
		FrequencyPenalty: 0.0,
		StopSequences:    []string{"stop1"},
		EnableToolCalls:  false,
	}

	m := &GeminiModel{
		name:           "gemini-test",
		apiKey:         "test-api-key",
		defaultOptions: defaultOptions,
	}

	// Test with empty options (should return defaults)
	t.Run("EmptyOptions", func(t *testing.T) {
		merged := m.mergeOptions(model.GenerationOptions{})
		assert.Equal(t, defaultOptions, merged)
	})

	// Test with partial options
	t.Run("PartialOptions", func(t *testing.T) {
		customOptions := model.GenerationOptions{
			Temperature:     0.5,
			MaxTokens:       500,
			EnableToolCalls: true,
		}

		merged := m.mergeOptions(customOptions)

		assert.Equal(t, customOptions.Temperature, merged.Temperature)
		assert.Equal(t, customOptions.MaxTokens, merged.MaxTokens)
		assert.Equal(t, defaultOptions.TopP, merged.TopP)
		assert.Equal(t, defaultOptions.TopK, merged.TopK)
		assert.Equal(t, defaultOptions.PresencePenalty, merged.PresencePenalty)
		assert.Equal(t, defaultOptions.FrequencyPenalty, merged.FrequencyPenalty)
		assert.Equal(t, defaultOptions.StopSequences, merged.StopSequences)
		assert.Equal(t, true, merged.EnableToolCalls)
	})

	// Test with complete override
	t.Run("CompleteOverride", func(t *testing.T) {
		customOptions := model.GenerationOptions{
			Temperature:      0.2,
			MaxTokens:        200,
			TopP:             0.9,
			TopK:             10,
			PresencePenalty:  0.1,
			FrequencyPenalty: 0.2,
			StopSequences:    []string{"stop2", "stop3"},
			EnableToolCalls:  true,
		}

		merged := m.mergeOptions(customOptions)
		assert.Equal(t, customOptions, merged)
	})
}
