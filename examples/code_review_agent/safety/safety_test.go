//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package safety

import (
	"testing"
)

func TestPermissionPolicy_Evaluate(t *testing.T) {
	policy := NewPermissionPolicy()

	tests := []struct {
		name     string
		command  string
		expected string
	}{
		{
			name:     "go vet allowed",
			command:  "go vet ./...",
			expected: "allow",
		},
		{
			name:     "go test allowed",
			command:  "go test -v ./...",
			expected: "allow",
		},
		{
			name:     "curl needs review",
			command:  "curl https://example.com",
			expected: "needs_human_review",
		},
		{
			name:     "rm -rf denied",
			command:  "rm -rf /",
			expected: "deny",
		},
		{
			name:     "sudo denied",
			command:  "sudo apt-get install",
			expected: "deny",
		},
		{
			name:     "unknown command needs review",
			command:  "python script.py",
			expected: "needs_human_review",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := policy.Evaluate(tt.command)
			if result.Decision != tt.expected {
				t.Errorf("Evaluate(%q) = %v, want %v", tt.command, result.Decision, tt.expected)
			}
		})
	}
}

func TestSecretDetector_Detect(t *testing.T) {
	detector := NewSecretDetector()

	tests := []struct {
		name     string
		text     string
		expected bool
	}{
		{
			name:     "api key",
			text:     `api_key = "sk-1234567890abcdef1234567890abcdef"`,
			expected: true,
		},
		{
			name:     "password",
			text:     `password = "my_secret_password_123"`,
			expected: true,
		},
		{
			name:     "no secrets",
			text:     `fmt.Println("Hello, World!")`,
			expected: false,
		},
		{
			name:     "private key",
			text:     `-----BEGIN RSA PRIVATE KEY-----`,
			expected: true,
		},
		{
			name:     "bearer token",
			text:     `Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456`,
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.Detect(tt.text)
			if result != tt.expected {
				t.Errorf("Detect() = %v, want %v", result, tt.expected)
			}
		})
	}
}

func TestPermissionPolicy_EvaluateTool(t *testing.T) {
	policy := NewPermissionPolicy()
	if got := policy.EvaluateTool("skill_load", []byte(`{"skill":"code-review"}`)); got.Decision != "allow" {
		t.Fatalf("skill_load decision = %q, want allow", got.Decision)
	}
	if got := policy.EvaluateTool("workspace_exec", []byte(`{"command":"curl https://example.com"}`)); got.Decision != "needs_human_review" {
		t.Fatalf("workspace_exec decision = %q, want needs_human_review", got.Decision)
	}
	if got := policy.EvaluateTool("workspace_exec", []byte(`{"command":"rm -rf /"}`)); got.Decision != "deny" {
		t.Fatalf("workspace_exec decision = %q, want deny", got.Decision)
	}
}

func TestSecretDetector_RedactText(t *testing.T) {
	detector := NewSecretDetector()

	tests := []struct {
		name     string
		input    string
		contains string
	}{
		{
			name:     "api key redacted",
			input:    `api_key = "sk-1234567890abcdef1234567890abcdef"`,
			contains: "<redacted>",
		},
		{
			name:     "normal text unchanged",
			input:    `fmt.Println("Hello")`,
			contains: "fmt.Println",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detector.RedactText(tt.input)
			if tt.contains == "<redacted>" {
				if result == tt.input {
					t.Error("expected text to be redacted")
				}
			} else {
				if result != tt.input {
					t.Error("expected text to be unchanged")
				}
			}
		})
	}
}
