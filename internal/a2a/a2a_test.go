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

	"github.com/stretchr/testify/assert"
)

func TestGetADKMetadataKey(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normal key",
			input:    "app_name",
			expected: "adk_app_name",
		},
		{
			name:     "type key",
			input:    "type",
			expected: "adk_type",
		},
		{
			name:     "user_id key",
			input:    "user_id",
			expected: "adk_user_id",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "single character",
			input:    "a",
			expected: "adk_a",
		},
		{
			name:     "key with special characters",
			input:    "key-with-dash",
			expected: "adk_key-with-dash",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := GetADKMetadataKey(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
