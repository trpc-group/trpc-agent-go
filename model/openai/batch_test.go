//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openaisdk "github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestBatchRequestInput_JSON(t *testing.T) {
	tests := []struct {
		name     string
		input    *BatchRequestInput
		expected string
	}{
		{
			name: "complete request",
			input: &BatchRequestInput{
				CustomID: "test-1",
				Method:   "POST",
				URL:      "/v1/chat/completions",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleSystem, Content: "You are a helpful assistant."},
							{Role: model.RoleUser, Content: "Hello"},
						},
					},
					Model: "gpt-3.5-turbo",
				},
			},
			expected: `{"custom_id":"test-1","method":"POST","url":"/v1/chat/completions","body":{"messages":[{"role":"system","content":"You are a helpful assistant."},{"role":"user","content":"Hello"}],"generation_config":{"stream":false},"model":"gpt-3.5-turbo"}}`,
		},
		{
			name: "minimal request",
			input: &BatchRequestInput{
				CustomID: "test-2",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "Hi"},
						},
					},
				},
			},
			expected: `{"custom_id":"test-2","method":"","url":"","body":{"messages":[{"role":"user","content":"Hi"}],"generation_config":{"stream":false},"model":""}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.input)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(data))

			// Test unmarshaling back.
			var unmarshaled BatchRequestInput
			err = json.Unmarshal(data, &unmarshaled)
			require.NoError(t, err)
			assert.Equal(t, tt.input.CustomID, unmarshaled.CustomID)
			assert.Equal(t, tt.input.Method, unmarshaled.Method)
			assert.Equal(t, tt.input.URL, unmarshaled.URL)
			assert.Equal(t, tt.input.Body.Model, unmarshaled.Body.Model)
			assert.Equal(t, len(tt.input.Body.Messages), len(unmarshaled.Body.Messages))
		})
	}
}

func TestBatchRequest_JSON(t *testing.T) {
	tests := []struct {
		name     string
		input    BatchRequest
		expected string
	}{
		{
			name: "with model",
			input: BatchRequest{
				Request: model.Request{
					Messages: []model.Message{
						{Role: model.RoleUser, Content: "Hello"},
					},
					GenerationConfig: model.GenerationConfig{
						Temperature: float64Ptr(0.7),
					},
				},
				Model: "gpt-4",
			},
			expected: `{"messages":[{"role":"user","content":"Hello"}],"generation_config":{"temperature":0.7,"stream":false},"model":"gpt-4"}`,
		},
		{
			name: "without model",
			input: BatchRequest{
				Request: model.Request{
					Messages: []model.Message{
						{Role: model.RoleSystem, Content: "You are helpful"},
					},
				},
			},
			expected: `{"messages":[{"role":"system","content":"You are helpful"}],"generation_config":{"stream":false},"model":""}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.input)
			require.NoError(t, err)
			assert.JSONEq(t, tt.expected, string(data))

			// Test unmarshaling back.
			var unmarshaled BatchRequest
			err = json.Unmarshal(data, &unmarshaled)
			require.NoError(t, err)
			assert.Equal(t, tt.input.Model, unmarshaled.Model)
			assert.Equal(t, len(tt.input.Messages), len(unmarshaled.Messages))
		})
	}
}

func TestBatchCreateOptions(t *testing.T) {
	opts := &BatchCreateOptions{
		CompletionWindow: openaisdk.BatchNewParamsCompletionWindow("24h"),
		Metadata: map[string]string{
			"description": "test batch",
			"priority":    "1",
		},
	}

	assert.Equal(t, openaisdk.BatchNewParamsCompletionWindow("24h"), opts.CompletionWindow)
	assert.Equal(t, "test batch", opts.Metadata["description"])
	assert.Equal(t, "1", opts.Metadata["priority"])
}

func TestBatchCreateOption_Functions(t *testing.T) {
	opts := &BatchCreateOptions{}

	// Test WithBatchCreateCompletionWindow
	WithBatchCreateCompletionWindow(openaisdk.BatchNewParamsCompletionWindow("48h"))(opts)
	assert.Equal(t, openaisdk.BatchNewParamsCompletionWindow("48h"), opts.CompletionWindow)

	// Test WithBatchCreateMetadata
	metadata := map[string]string{"test": "value"}
	WithBatchCreateMetadata(metadata)(opts)
	assert.Equal(t, metadata, opts.Metadata)
}

func TestValidateBatchRequests(t *testing.T) {
	tests := []struct {
		name        string
		requests    []*BatchRequestInput
		expectError bool
		errorMsg    string
	}{
		{
			name:        "empty requests",
			requests:    []*BatchRequestInput{},
			expectError: true,
			errorMsg:    "requests cannot be empty",
		},
		{
			name: "nil request",
			requests: []*BatchRequestInput{
				nil,
			},
			expectError: true,
			errorMsg:    "request 0 is nil",
		},
		{
			name: "missing custom_id",
			requests: []*BatchRequestInput{
				{
					CustomID: "",
					Body: BatchRequest{
						Request: model.Request{
							Messages: []model.Message{
								{Role: model.RoleUser, Content: "Hello"},
							},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "custom_id cannot be empty",
		},
		{
			name: "duplicate custom_id",
			requests: []*BatchRequestInput{
				{
					CustomID: "test-1",
					Body: BatchRequest{
						Request: model.Request{
							Messages: []model.Message{
								{Role: model.RoleUser, Content: "Hello"},
							},
						},
					},
				},
				{
					CustomID: "test-1",
					Body: BatchRequest{
						Request: model.Request{
							Messages: []model.Message{
								{Role: model.RoleUser, Content: "World"},
							},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "duplicate custom_id 'test-1'",
		},
		{
			name: "empty messages",
			requests: []*BatchRequestInput{
				{
					CustomID: "test-1",
					Body: BatchRequest{
						Request: model.Request{
							Messages: []model.Message{},
						},
					},
				},
			},
			expectError: true,
			errorMsg:    "body.messages must be non-empty",
		},
		{
			name: "valid requests",
			requests: []*BatchRequestInput{
				{
					CustomID: "test-1",
					Body: BatchRequest{
						Request: model.Request{
							Messages: []model.Message{
								{Role: model.RoleUser, Content: "Hello"},
							},
						},
					},
				},
				{
					CustomID: "test-2",
					Body: BatchRequest{
						Request: model.Request{
							Messages: []model.Message{
								{Role: model.RoleUser, Content: "World"},
							},
						},
					},
				},
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{}
			if tt.name == "empty requests" {
				// Empty requests are checked in CreateBatch before validateBatchRequests.
				_, err := m.CreateBatch(context.Background(), tt.requests)
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
			} else {
				err := m.validateBatchRequests(tt.requests)
				if tt.expectError {
					require.Error(t, err)
					assert.Contains(t, err.Error(), tt.errorMsg)
				} else {
					assert.NoError(t, err)
				}
			}
		})
	}
}

func TestGenerateBatchJSONL(t *testing.T) {
	requests := []*BatchRequestInput{
		{
			CustomID: "test-1",
			Body: BatchRequest{
				Request: model.Request{
					Messages: []model.Message{
						{Role: model.RoleUser, Content: "Hello"},
					},
				},
				Model: "gpt-3.5-turbo",
			},
		},
		{
			CustomID: "test-2",
			Body: BatchRequest{
				Request: model.Request{
					Messages: []model.Message{
						{Role: model.RoleUser, Content: "World"},
					},
				},
			},
		},
	}

	// Create a mock model for testing.
	m := &Model{name: "default-model"}

	jsonlData, err := m.generateBatchJSONL(requests)
	require.NoError(t, err)

	// Verify the generated JSONL.
	lines := string(jsonlData)
	assert.Contains(t, lines, `"custom_id":"test-1"`)
	assert.Contains(t, lines, `"custom_id":"test-2"`)
	assert.Contains(t, lines, `"method":"POST"`)
	assert.Contains(t, lines, `"url":"/v1/chat/completions"`)
	assert.Contains(t, lines, `"model":"gpt-3.5-turbo"`)
	assert.Contains(t, lines, `"model":"default-model"`)

	// Verify normalization.
	for _, r := range requests {
		assert.Equal(t, "POST", r.Method)
		assert.Equal(t, "/v1/chat/completions", r.URL)
		if r.Body.Model == "" {
			assert.Equal(t, "default-model", r.Body.Model)
		}
	}
}

func TestBatchRequestOutput_JSON(t *testing.T) {
	t.Run("successful response", func(t *testing.T) {
		in := &BatchRequestOutput{
			ID:       stringPtr("req-123"),
			CustomID: "test-1",
			Response: BatchResponse{
				StatusCode: 200,
				RequestID:  stringPtr("req-456"),
				Body: openaisdk.ChatCompletion{Choices: []openaisdk.ChatCompletionChoice{{
					Message: openaisdk.ChatCompletionMessage{Content: "Hello"},
				}}},
			},
			Error: nil,
		}

		data, err := json.Marshal(in)
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(data, &got))

		resp := got["response"].(map[string]any)
		assert.Equal(t, float64(200), resp["status_code"])
		assert.Equal(t, "req-456", resp["request_id"])

		body := resp["body"].(map[string]any)
		choices, ok := body["choices"].([]any)
		if ok && len(choices) > 0 {
			msg := choices[0].(map[string]any)["message"].(map[string]any)
			assert.Equal(t, "Hello", msg["content"])
		}

		// error should be null or omitted.
		_, hasError := got["error"]
		if hasError {
			assert.Nil(t, got["error"]) // should be null
		}
	})

	t.Run("error response", func(t *testing.T) {
		in := &BatchRequestOutput{
			ID:       stringPtr("req-124"),
			CustomID: "test-2",
			Response: BatchResponse{
				StatusCode: 400,
				RequestID:  nil,
				Body:       openaisdk.ChatCompletion{},
			},
			Error: &shared.ErrorObject{Type: "invalid_request", Message: "Bad request", Code: "", Param: ""},
		}

		data, err := json.Marshal(in)
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(data, &got))

		resp := got["response"].(map[string]any)
		assert.Equal(t, float64(400), resp["status_code"])
		// request_id may be null; no strict assert here.

		errObj := got["error"].(map[string]any)
		assert.Equal(t, "invalid_request", errObj["type"])
		assert.Equal(t, "Bad request", errObj["message"])
	})
}

func TestBatchResponse_JSON(t *testing.T) {
	t.Run("with request_id", func(t *testing.T) {
		in := BatchResponse{StatusCode: 200, RequestID: stringPtr("req-123"), Body: openaisdk.ChatCompletion{Model: "gpt-4o-mini"}}
		data, err := json.Marshal(in)
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, float64(200), got["status_code"])
		assert.Equal(t, "req-123", got["request_id"])

		body := got["body"].(map[string]any)
		assert.Equal(t, "gpt-4o-mini", body["model"])
	})

	t.Run("without request_id", func(t *testing.T) {
		in := BatchResponse{StatusCode: 500, RequestID: nil, Body: openaisdk.ChatCompletion{Model: "gpt-4o-mini"}}
		data, err := json.Marshal(in)
		require.NoError(t, err)

		var got map[string]any
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, float64(500), got["status_code"])
		// request_id may be null
		_, present := got["request_id"]
		if present {
			assert.Nil(t, got["request_id"])
		}
		body := got["body"].(map[string]any)
		assert.Equal(t, "gpt-4o-mini", body["model"])
	})
}

func TestParseBatchOutput(t *testing.T) {
	tests := []struct {
		name        string
		jsonlInput  string
		expectCount int
		expectError bool
	}{
		{
			name: "valid JSONL",
			jsonlInput: `{"id":"req-1","custom_id":"test-1","response":{"status_code":200,"body":{"model":"gpt-4o-mini"}}}
{"id":"req-2","custom_id":"test-2","response":{"status_code":400,"body":{"model":"gpt-4o-mini"}}}`,
			expectCount: 2,
			expectError: false,
		},
		{
			name: "empty lines",
			jsonlInput: `{"id":"req-1","custom_id":"test-1","response":{"status_code":200,"body":{"model":"gpt-4o-mini"}}}

{"id":"req-2","custom_id":"test-2","response":{"status_code":400,"body":{"model":"gpt-4o-mini"}}}`,
			expectCount: 2,
			expectError: false,
		},
		{
			name: "invalid JSON",
			jsonlInput: `{"id":"req-1","custom_id":"test-1","response":{"status_code":200,"body":{"model":"gpt-4o-mini"}}}
{invalid json}`,
			expectCount: 0,
			expectError: true,
		},
		{
			name:        "empty input",
			jsonlInput:  "",
			expectCount: 0,
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := &Model{}
			results, err := m.ParseBatchOutput(tt.jsonlInput)

			if tt.expectError {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.Len(t, results, tt.expectCount)

			// Verify RawLine is preserved.
			for i, result := range results {
				assert.NotEmpty(t, result.RawLine)
				switch i {
				case 0:
					assert.Contains(t, result.RawLine, "test-1")
				case 1:
					assert.Contains(t, result.RawLine, "test-2")
				}
			}
		})
	}
}

func TestParseBatchOutput_EdgeCases(t *testing.T) {
	m := &Model{}

	// Test with single valid line.
	results, err := m.ParseBatchOutput(`{"custom_id":"single","response":{"status_code":200,"body":{"model":"gpt-4o-mini"}}}`)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, "single", results[0].CustomID)
	assert.Equal(t, 200, results[0].Response.StatusCode)

	// Test with whitespace-only lines.
	results, err = m.ParseBatchOutput("   \n\t\n  ")
	require.NoError(t, err)
	assert.Len(t, results, 0)

	// Test with mixed content.
	results, err = m.ParseBatchOutput(`{"custom_id":"test","response":{"status_code":200,"body":{"model":"gpt-4o-mini"}}}
   
{"custom_id":"test2","response":{"status_code":400,"body":{"model":"gpt-4o-mini"}}}`)
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

// Helper functions to create pointers.
func float64Ptr(f float64) *float64 { return &f }

// TestBatchOperations_EdgeCases tests edge cases and error conditions for batch operations.
func TestBatchOperations_EdgeCases(t *testing.T) {
	t.Run("CreateBatch with invalid requests", func(t *testing.T) {
		m := &Model{name: "test-model"}

		// Test with nil requests slice.
		_, err := m.CreateBatch(context.Background(), nil)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requests cannot be empty")

		// Test with empty requests slice.
		_, err = m.CreateBatch(context.Background(), []*BatchRequestInput{})
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "requests cannot be empty")
	})

	t.Run("CreateBatch with invalid request data", func(t *testing.T) {
		m := &Model{name: "test-model"}

		// Test with request that has empty custom_id.
		requests := []*BatchRequestInput{
			{
				CustomID: "",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "Hello"},
						},
					},
				},
			},
		}

		_, err := m.CreateBatch(context.Background(), requests)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "custom_id cannot be empty")
	})

	t.Run("CreateBatch with duplicate custom_id", func(t *testing.T) {
		m := &Model{name: "test-model"}

		requests := []*BatchRequestInput{
			{
				CustomID: "duplicate",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "First"},
						},
					},
				},
			},
			{
				CustomID: "duplicate", // Same custom_id.
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "Second"},
						},
					},
				},
			},
		}

		_, err := m.CreateBatch(context.Background(), requests)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "duplicate custom_id 'duplicate'")
	})

	t.Run("CreateBatch with empty messages", func(t *testing.T) {
		m := &Model{name: "test-model"}

		requests := []*BatchRequestInput{
			{
				CustomID: "test-1",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{}, // Empty messages.
					},
				},
			},
		}

		_, err := m.CreateBatch(context.Background(), requests)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "body.messages must be non-empty")
	})

	t.Run("CreateBatch with valid requests", func(t *testing.T) {
		m := &Model{name: "test-model"}

		requests := []*BatchRequestInput{
			{
				CustomID: "test-1",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "Hello"},
						},
					},
				},
			},
			{
				CustomID: "test-2",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "World"},
						},
					},
				},
			},
		}

		// This should pass validation but fail at file upload (no client).
		_, err := m.CreateBatch(context.Background(), requests)
		// We expect an error, but it should be after validation passes.
		assert.Error(t, err)
		// The error should not be about validation.
		assert.NotContains(t, err.Error(), "custom_id cannot be empty")
		assert.NotContains(t, err.Error(), "duplicate custom_id")
		assert.NotContains(t, err.Error(), "body.messages must be non-empty")
	})
}

// TestBatchOptions tests batch creation options and their combinations.
func TestBatchOptions(t *testing.T) {
	t.Run("default options", func(t *testing.T) {
		opts := &BatchCreateOptions{}
		assert.Equal(t, openaisdk.BatchNewParamsCompletionWindow(""), opts.CompletionWindow)
		assert.Nil(t, opts.Metadata)
	})

	t.Run("with completion window", func(t *testing.T) {
		opts := &BatchCreateOptions{}
		WithBatchCreateCompletionWindow(openaisdk.BatchNewParamsCompletionWindow("48h"))(opts)
		assert.Equal(t, openaisdk.BatchNewParamsCompletionWindow("48h"), opts.CompletionWindow)
	})

	t.Run("with metadata", func(t *testing.T) {
		opts := &BatchCreateOptions{}
		metadata := map[string]string{"priority": "high", "env": "production"}
		WithBatchCreateMetadata(metadata)(opts)
		assert.Equal(t, metadata, opts.Metadata)
	})

	t.Run("multiple options", func(t *testing.T) {
		opts := &BatchCreateOptions{}
		WithBatchCreateCompletionWindow(openaisdk.BatchNewParamsCompletionWindow("72h"))(opts)
		WithBatchCreateMetadata(map[string]string{"test": "value"})(opts)

		assert.Equal(t, openaisdk.BatchNewParamsCompletionWindow("72h"), opts.CompletionWindow)
		assert.Equal(t, "value", opts.Metadata["test"])
	})
}

// TestBatchMethodSignatures tests the method signatures and basic functionality of all batch methods.
func TestBatchMethodSignatures(t *testing.T) {
	m := &Model{name: "test-model"}
	// Test that the method exists and has the correct signature.
	// Since we can't easily mock the client without API keys,
	// we'll test the method signature and basic error handling.

	t.Run("RetrieveBatch", func(t *testing.T) {
		// This will fail due to no client, but we can verify the method exists.
		_, err := m.RetrieveBatch(context.Background(), "test-id")
		// We expect an error, but the method should exist and be callable.
		assert.Error(t, err)
	})

	t.Run("CancelBatch", func(t *testing.T) {
		// This will fail due to no client, but we can verify the method exists.
		_, err := m.CancelBatch(context.Background(), "test-id")
		// We expect an error, but the method should exist and be callable.
		assert.Error(t, err)
	})

	t.Run("ListBatches", func(t *testing.T) {
		// This will fail due to no client, but we can verify the method exists.
		_, err := m.ListBatches(context.Background(), "", 10)
		// We expect an error, but the method should exist and be callable.
		assert.Error(t, err)
	})

	t.Run("DownloadFileContent", func(t *testing.T) {
		// This will fail due to no client, but we can verify the method exists.
		_, err := m.DownloadFileContent(context.Background(), "test-file-id")
		// We expect an error, but the method should exist and be callable.
		assert.Error(t, err)
	})

	t.Run("validation without client", func(t *testing.T) {
		m := &Model{name: "test-model"}

		// Create test requests.
		requests := []*BatchRequestInput{
			{
				CustomID: "test-1",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "Hello"},
						},
					},
				},
			},
		}

		// Test that validation passes but fails at file upload due to no client.
		_, err := m.CreateBatch(context.Background(), requests)
		// We expect an error, but it should be after validation passes.
		assert.Error(t, err)
		// The error should not be about validation.
		assert.NotContains(t, err.Error(), "custom_id cannot be empty")
		assert.NotContains(t, err.Error(), "duplicate custom_id")
		assert.NotContains(t, err.Error(), "body.messages must be non-empty")
	})
}

// TestBatchRequestValidation_EdgeCases tests additional edge cases for batch request validation.
func TestBatchRequestValidation_EdgeCases(t *testing.T) {
	t.Run("nil request in slice", func(t *testing.T) {
		requests := []*BatchRequestInput{
			{
				CustomID: "valid-1",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "Hello"},
						},
					},
				},
			},
			nil, // This should cause an error.
			{
				CustomID: "valid-2",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "World"},
						},
					},
				},
			},
		}

		m := &Model{name: "test-model"}
		err := m.validateBatchRequests(requests)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "request 1 is nil")
	})

	t.Run("very long custom_id", func(t *testing.T) {
		longCustomID := strings.Repeat("a", 1000)
		requests := []*BatchRequestInput{
			{
				CustomID: longCustomID,
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "Hello"},
						},
					},
				},
			},
		}

		m := &Model{name: "test-model"}
		err := m.validateBatchRequests(requests)
		// Very long custom_id should be valid (no length limit in OpenAI spec).
		assert.NoError(t, err)
	})

	t.Run("special characters in custom_id", func(t *testing.T) {
		specialCustomIDs := []string{
			"test-123",
			"test_123",
			"test.123",
			"test@123",
			"test#123",
			"test$123",
			"test%123",
			"test^123",
			"test&123",
			"test*123",
		}

		for _, customID := range specialCustomIDs {
			t.Run("custom_id: "+customID, func(t *testing.T) {
				requests := []*BatchRequestInput{
					{
						CustomID: customID,
						Body: BatchRequest{
							Request: model.Request{
								Messages: []model.Message{
									{Role: model.RoleUser, Content: "Hello"},
								},
							},
						},
					},
				}

				m := &Model{name: "test-model"}
				err := m.validateBatchRequests(requests)
				// Special characters should be valid in custom_id.
				assert.NoError(t, err)
			})
		}
	})
}

// TestGenerateBatchJSONL_EdgeCases tests edge cases for JSONL generation.
func TestGenerateBatchJSONL_EdgeCases(t *testing.T) {
	t.Run("empty requests slice", func(t *testing.T) {
		m := &Model{name: "test-model"}
		// Empty slice should not cause an error in generateBatchJSONL.
		// The error would occur in CreateBatch during validation.
		result, err := m.generateBatchJSONL([]*BatchRequestInput{})
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("requests with all fields empty", func(t *testing.T) {
		requests := []*BatchRequestInput{
			{
				CustomID: "test-1",
				Body: BatchRequest{
					Request: model.Request{
						Messages: []model.Message{
							{Role: model.RoleUser, Content: "Hello"},
						},
					},
				},
			},
		}

		m := &Model{name: "test-model"}
		jsonlData, err := m.generateBatchJSONL(requests)
		require.NoError(t, err)

		// Verify normalization.
		lines := strings.Split(strings.TrimSpace(string(jsonlData)), "\n")
		assert.Len(t, lines, 1)

		var decoded BatchRequestInput
		err = json.Unmarshal([]byte(lines[0]), &decoded)
		require.NoError(t, err)

		assert.Equal(t, "test-1", decoded.CustomID)
		assert.Equal(t, "POST", decoded.Method)              // Should be normalized to POST.
		assert.Equal(t, "/v1/chat/completions", decoded.URL) // Should be normalized.
		assert.Equal(t, "test-model", decoded.Body.Model)    // Should be filled with model name.
	})

	t.Run("requests with special characters in messages", func(t *testing.T) {
		specialMessages := []string{
			"Hello\nWorld",
			"Hello\tWorld",
			"Hello\r\nWorld",
			"Hello \"World\"",
			"Hello \\ World",
			"Hello \u0000 World", // Null byte.
			"Hello \u2028 World", // Line separator.
			"Hello \u2029 World", // Paragraph separator.
		}

		for i, message := range specialMessages {
			t.Run("message: "+message, func(t *testing.T) {
				requests := []*BatchRequestInput{
					{
						CustomID: fmt.Sprintf("test-%d", i),
						Body: BatchRequest{
							Request: model.Request{
								Messages: []model.Message{
									{Role: model.RoleUser, Content: message},
								},
							},
						},
					},
				}

				m := &Model{name: "test-model"}
				jsonlData, err := m.generateBatchJSONL(requests)
				require.NoError(t, err)

				// Verify the JSONL can be parsed back.
				var decoded BatchRequestInput
				lines := strings.Split(strings.TrimSpace(string(jsonlData)), "\n")
				err = json.Unmarshal([]byte(lines[0]), &decoded)
				require.NoError(t, err)

				assert.Equal(t, fmt.Sprintf("test-%d", i), decoded.CustomID)
				assert.Equal(t, message, decoded.Body.Messages[0].Content)
			})
		}
	})
}

func TestBatchBaseURL_Overrides(t *testing.T) {
	// Mock server implementing minimal batches endpoints.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/files"):
			// File upload: accept multipart and return file id.
			if err := r.ParseMultipartForm(10 << 20); err != nil {
				http.Error(w, "parse", http.StatusBadRequest)
				return
			}
			io.WriteString(w, `{"id":"file_mock","object":"file","bytes":1,"created_at":1,"filename":"batch_input.jsonl","purpose":"batch"}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/files/") && strings.HasSuffix(r.URL.Path, "/content"):
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, "line1\nline2")
		case r.Method == http.MethodPost && r.URL.Path == "/batches":
			io.WriteString(w, `{"id":"batch_x","object":"batch","endpoint":"/v1/chat/completions","input_file_id":"file_mock","completion_window":"24h","status":"validating","created_at":1,"request_counts":{"total":0,"completed":0,"failed":0}}`)
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/batches/") && !strings.HasSuffix(r.URL.Path, "/cancel"):
			io.WriteString(w, `{"id":"batch_x","object":"batch","endpoint":"/v1/chat/completions","input_file_id":"file_mock","completion_window":"24h","status":"completed","created_at":1,"request_counts":{"total":0,"completed":0,"failed":0}}`)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cancel"):
			io.WriteString(w, `{"id":"batch_x","object":"batch","endpoint":"/v1/chat/completions","input_file_id":"file_mock","completion_window":"24h","status":"cancelled","created_at":1,"request_counts":{"total":0,"completed":0,"failed":0}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/batches":
			io.WriteString(w, `{"data":[],"first_id":"","last_id":"","has_more":false}`)
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Model with wrong global base; batch ops should work via WithBatchBaseURL override.
	m := New("gpt-4o-mini", WithAPIKey("k"), WithBaseURL("http://wrong-base"), WithBatchBaseURL(server.URL))

	// Prepare minimal valid request set; UploadFileData uses WithPath("") inside CreateBatch.
	reqs := []*BatchRequestInput{
		{
			CustomID: "1",
			Method:   "POST",
			URL:      string(openaisdk.BatchNewParamsEndpointV1ChatCompletions),
			Body:     BatchRequest{Request: model.Request{Messages: []model.Message{{Role: model.RoleUser, Content: "hi"}}}},
		},
	}

	// Create.
	batch, err := m.CreateBatch(context.Background(), reqs)
	require.NoError(t, err)
	assert.Equal(t, "batch_x", batch.ID)

	// Retrieve.
	got, err := m.RetrieveBatch(context.Background(), "batch_x")
	require.NoError(t, err)
	assert.Equal(t, "batch_x", got.ID)

	// Cancel.
	got, err = m.CancelBatch(context.Background(), "batch_x")
	require.NoError(t, err)
	assert.Equal(t, "batch_x", got.ID)

	// List.
	page, err := m.ListBatches(context.Background(), "", 10)
	require.NoError(t, err)
	assert.False(t, page.HasMore)

	// Download file content should also honor batch base URL override.
	content, err := m.DownloadFileContent(context.Background(), "file_mock")
	require.NoError(t, err)
	assert.NotEmpty(t, content)
}
