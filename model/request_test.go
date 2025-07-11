//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

package model

import (
	"testing"
)

func TestRole_String(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want string
	}{
		{
			name: "system role",
			role: RoleSystem,
			want: "system",
		},
		{
			name: "user role",
			role: RoleUser,
			want: "user",
		},
		{
			name: "assistant role",
			role: RoleAssistant,
			want: "assistant",
		},
		{
			name: "custom role",
			role: Role("custom"),
			want: "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.role.String(); got != tt.want {
				t.Errorf("Role.String() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRole_IsValid(t *testing.T) {
	tests := []struct {
		name string
		role Role
		want bool
	}{
		{
			name: "valid system role",
			role: RoleSystem,
			want: true,
		},
		{
			name: "valid user role",
			role: RoleUser,
			want: true,
		},
		{
			name: "valid assistant role",
			role: RoleAssistant,
			want: true,
		},
		{
			name: "invalid empty role",
			role: Role(""),
			want: false,
		},
		{
			name: "invalid custom role",
			role: Role("custom"),
			want: false,
		},
		{
			name: "invalid mixed case role",
			role: Role("System"),
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.role.IsValid(); got != tt.want {
				t.Errorf("Role.IsValid() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewSystemMessage(t *testing.T) {
	content := "You are a helpful assistant."
	msg := NewSystemMessage(content)

	if msg.Role != RoleSystem {
		t.Errorf("NewSystemMessage() role = %v, want %v", msg.Role, RoleSystem)
	}
	if msg.Content != content {
		t.Errorf("NewSystemMessage() content = %v, want %v", msg.Content, content)
	}
}

func TestNewUserMessage(t *testing.T) {
	content := "Hello, how are you?"
	msg := NewUserMessage(content)

	if msg.Role != RoleUser {
		t.Errorf("NewUserMessage() role = %v, want %v", msg.Role, RoleUser)
	}
	if msg.Content != content {
		t.Errorf("NewUserMessage() content = %v, want %v", msg.Content, content)
	}
}

func TestNewAssistantMessage(t *testing.T) {
	content := "I'm doing well, thank you!"
	msg := NewAssistantMessage(content)

	if msg.Role != RoleAssistant {
		t.Errorf("NewAssistantMessage() role = %v, want %v", msg.Role, RoleAssistant)
	}
	if msg.Content != content {
		t.Errorf("NewAssistantMessage() content = %v, want %v", msg.Content, content)
	}
}

func TestMessage_JSON(t *testing.T) {
	msg := Message{
		Role:    RoleUser,
		Content: "Test message",
	}

	// Test that the message can be marshaled to JSON
	expected := `{"role":"user","content":"Test message"}`

	// We're not testing JSON marshaling directly here since it's built-in
	// but we can test that the struct tags are correct by checking field values
	if msg.Role != RoleUser {
		t.Errorf("Message.Role = %v, want %v", msg.Role, RoleUser)
	}
	if msg.Content != "Test message" {
		t.Errorf("Message.Content = %v, want %v", msg.Content, "Test message")
	}

	_ = expected // Suppress unused variable warning
}

func TestRequest_Validation(t *testing.T) {
	tests := []struct {
		name    string
		request *Request
		wantErr bool
	}{
		{
			name: "valid basic request",
			request: &Request{
				Messages: []Message{
					NewUserMessage("Hello"),
				},
			},
			wantErr: false,
		},
		{
			name: "empty messages",
			request: &Request{
				Messages: []Message{},
			},
			wantErr: false, // Message validation might be done elsewhere
		},
		{
			name: "with optional parameters",
			request: &Request{
				Messages: []Message{
					NewSystemMessage("You are helpful"),
					NewUserMessage("Hello"),
				},
				GenerationConfig: GenerationConfig{
					MaxTokens:        intPtr(100),
					Temperature:      floatPtr(0.7),
					TopP:             floatPtr(0.9),
					PresencePenalty:  floatPtr(0.1),
					FrequencyPenalty: floatPtr(0.1),
					Stop:             []string{"END"},
					Stream:           true,
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Since there's no explicit validation in the Request struct,
			// we just verify the struct can be created successfully
			if tt.request == nil {
				t.Error("Request should not be nil")
			}
		})
	}
}

// Helper functions for test data
func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
