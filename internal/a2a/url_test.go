//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2a

import "testing"

func TestNormalizeURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
		{
			name:     "http URL - no change",
			input:    "http://example.com",
			expected: "http://example.com",
		},
		{
			name:     "https URL - no change",
			input:    "https://example.com",
			expected: "https://example.com",
		},
		{
			name:     "custom scheme - no change",
			input:    "grpc://service:9090",
			expected: "grpc://service:9090",
		},
		{
			name:     "host only - add http",
			input:    "localhost:8080",
			expected: "http://localhost:8080",
		},
		{
			name:     "domain only - add http",
			input:    "example.com",
			expected: "http://example.com",
		},
		{
			name:     "IP with port - add http",
			input:    "192.168.1.1:8080",
			expected: "http://192.168.1.1:8080",
		},
		{
			name:     "http URL with path",
			input:    "http://example.com/api/v1",
			expected: "http://example.com/api/v1",
		},
		{
			name:     "host with path - add http",
			input:    "localhost:8080/api",
			expected: "http://localhost:8080/api",
		},
		{
			name:     "custom scheme with path",
			input:    "custom://service.namespace/path",
			expected: "custom://service.namespace/path",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := NormalizeURL(tt.input)
			if result != tt.expected {
				t.Errorf("NormalizeURL(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}
