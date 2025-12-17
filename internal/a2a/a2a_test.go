//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2a

import (
	"testing"
)

func TestGetADKMetadataKey(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		expected string
	}{
		{
			name:     "app_name",
			key:      "app_name",
			expected: "adk_app_name",
		},
		{
			name:     "user_id",
			key:      "user_id",
			expected: "adk_user_id",
		},
		{
			name:     "type",
			key:      "type",
			expected: "adk_type",
		},
		{
			name:     "empty string",
			key:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetADKMetadataKey(tt.key)
			if result != tt.expected {
				t.Errorf("GetADKMetadataKey(%q) = %q, want %q", tt.key, result, tt.expected)
			}
		})
	}
}

func TestGetDataPartType(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]any
		expected string
	}{
		{
			name: "with adk_type",
			metadata: map[string]any{
				"adk_type": "function_call",
			},
			expected: "function_call",
		},
		{
			name: "with type",
			metadata: map[string]any{
				"type": "function_response",
			},
			expected: "function_response",
		},
		{
			name: "adk_type takes precedence",
			metadata: map[string]any{
				"adk_type": "executable_code",
				"type":     "function_call",
			},
			expected: "executable_code",
		},
		{
			name:     "nil metadata",
			metadata: nil,
			expected: "",
		},
		{
			name:     "empty metadata",
			metadata: map[string]any{},
			expected: "",
		},
		{
			name: "non-string type value",
			metadata: map[string]any{
				"type": 123,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetDataPartType(tt.metadata)
			if result != tt.expected {
				t.Errorf("GetDataPartType() = %q, want %q", result, tt.expected)
			}
		})
	}
}
