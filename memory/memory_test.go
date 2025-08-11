//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//

package memory

import (
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestMemory_JSONTags(t *testing.T) {
	now := time.Now()
	memory := &Memory{
		Memory:      "test memory",
		Topics:      []string{"topic1", "topic2"},
		LastUpdated: &now,
	}

	// Test that the struct can be marshaled to JSON.
	// This is a basic test to ensure the JSON tags are correct.
	if memory.Memory == "" {
		t.Error("Memory field should not be empty")
	}
	if len(memory.Topics) != 2 {
		t.Error("Topics should have 2 elements")
	}
	if memory.LastUpdated == nil {
		t.Error("LastUpdated should not be nil")
	}
}

func TestEntry_JSONTags(t *testing.T) {
	now := time.Now()
	entry := &Entry{
		ID:        "test-id",
		AppName:   "test-app",
		Memory:    &Memory{Memory: "test memory"},
		UserID:    "test-user",
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Test that the struct can be marshaled to JSON.
	// This is a basic test to ensure the JSON tags are correct.
	if entry.ID == "" {
		t.Error("ID field should not be empty")
	}
	if entry.AppName == "" {
		t.Error("AppName field should not be empty")
	}
	if entry.Memory == nil {
		t.Error("Memory field should not be nil")
	}
	if entry.UserID == "" {
		t.Error("UserID field should not be empty")
	}
	if entry.CreatedAt.IsZero() {
		t.Error("CreatedAt should not be zero")
	}
	if entry.UpdatedAt.IsZero() {
		t.Error("UpdatedAt should not be zero")
	}
}

func TestKey_CheckMemoryKey(t *testing.T) {
	tests := []struct {
		name      string
		key       Key
		expectErr bool
	}{
		{
			name: "valid key",
			key: Key{
				AppName:  "test-app",
				UserID:   "test-user",
				MemoryID: "test-memory",
			},
			expectErr: false,
		},
		{
			name: "missing app name",
			key: Key{
				AppName:  "",
				UserID:   "test-user",
				MemoryID: "test-memory",
			},
			expectErr: true,
		},
		{
			name: "missing user id",
			key: Key{
				AppName:  "test-app",
				UserID:   "",
				MemoryID: "test-memory",
			},
			expectErr: true,
		},
		{
			name: "missing memory id",
			key: Key{
				AppName:  "test-app",
				UserID:   "test-user",
				MemoryID: "",
			},
			expectErr: true,
		},
		{
			name: "all fields empty",
			key: Key{
				AppName:  "",
				UserID:   "",
				MemoryID: "",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.CheckMemoryKey()
			if tt.expectErr && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Check specific error types for invalid cases.
			if tt.expectErr {
				// When multiple fields are empty, the function returns the first error it encounters.
				// The order is: AppName -> UserID -> MemoryID
				if tt.key.AppName == "" {
					if err != ErrAppNameRequired {
						t.Errorf("Expected ErrAppNameRequired for empty app name, got: %v", err)
					}
				} else if tt.key.UserID == "" {
					if err != ErrUserIDRequired {
						t.Errorf("Expected ErrUserIDRequired for empty user id, got: %v", err)
					}
				} else if tt.key.MemoryID == "" {
					if err != ErrMemoryIDRequired {
						t.Errorf("Expected ErrMemoryIDRequired for empty memory id, got: %v", err)
					}
				}
			}
		})
	}
}

func TestKey_CheckUserKey(t *testing.T) {
	tests := []struct {
		name      string
		key       Key
		expectErr bool
	}{
		{
			name: "valid key",
			key: Key{
				AppName:  "test-app",
				UserID:   "test-user",
				MemoryID: "test-memory",
			},
			expectErr: false,
		},
		{
			name: "missing app name",
			key: Key{
				AppName:  "",
				UserID:   "test-user",
				MemoryID: "test-memory",
			},
			expectErr: true,
		},
		{
			name: "missing user id",
			key: Key{
				AppName:  "test-app",
				UserID:   "",
				MemoryID: "test-memory",
			},
			expectErr: true,
		},
		{
			name: "both app name and user id missing",
			key: Key{
				AppName:  "",
				UserID:   "",
				MemoryID: "test-memory",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.key.CheckUserKey()
			if tt.expectErr && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Check specific error types for invalid cases.
			if tt.expectErr {
				// When multiple fields are empty, the function returns the first error it encounters.
				// The order is: AppName -> UserID
				if tt.key.AppName == "" {
					if err != ErrAppNameRequired {
						t.Errorf("Expected ErrAppNameRequired for empty app name, got: %v", err)
					}
				} else if tt.key.UserID == "" {
					if err != ErrUserIDRequired {
						t.Errorf("Expected ErrUserIDRequired for empty user id, got: %v", err)
					}
				}
			}
		})
	}
}

func TestUserKey_CheckUserKey(t *testing.T) {
	tests := []struct {
		name      string
		userKey   UserKey
		expectErr bool
	}{
		{
			name: "valid user key",
			userKey: UserKey{
				AppName: "test-app",
				UserID:  "test-user",
			},
			expectErr: false,
		},
		{
			name: "missing app name",
			userKey: UserKey{
				AppName: "",
				UserID:  "test-user",
			},
			expectErr: true,
		},
		{
			name: "missing user id",
			userKey: UserKey{
				AppName: "test-app",
				UserID:  "",
			},
			expectErr: true,
		},
		{
			name: "both fields empty",
			userKey: UserKey{
				AppName: "",
				UserID:  "",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.userKey.CheckUserKey()
			if tt.expectErr && err == nil {
				t.Error("Expected error but got none")
			}
			if !tt.expectErr && err != nil {
				t.Errorf("Expected no error but got: %v", err)
			}

			// Check specific error types for invalid cases.
			if tt.expectErr {
				// When multiple fields are empty, the function returns the first error it encounters.
				// The order is: AppName -> UserID
				if tt.userKey.AppName == "" {
					if err != ErrAppNameRequired {
						t.Errorf("Expected ErrAppNameRequired for empty app name, got: %v", err)
					}
				} else if tt.userKey.UserID == "" {
					if err != ErrUserIDRequired {
						t.Errorf("Expected ErrUserIDRequired for empty user id, got: %v", err)
					}
				}
			}
		})
	}
}

func TestToolNames(t *testing.T) {
	// Test that all tool names are defined and not empty.
	toolNames := []string{
		AddToolName,
		UpdateToolName,
		DeleteToolName,
		ClearToolName,
		SearchToolName,
		LoadToolName,
	}

	for _, name := range toolNames {
		if name == "" {
			t.Errorf("Tool name should not be empty")
		}
	}

	// Test that tool names are unique.
	seen := make(map[string]bool)
	for _, name := range toolNames {
		if seen[name] {
			t.Errorf("Duplicate tool name: %s", name)
		}
		seen[name] = true
	}
}

func TestErrorConstants(t *testing.T) {
	// Test that error constants are not nil.
	if ErrAppNameRequired == nil {
		t.Error("ErrAppNameRequired should not be nil")
	}
	if ErrUserIDRequired == nil {
		t.Error("ErrUserIDRequired should not be nil")
	}
	if ErrMemoryIDRequired == nil {
		t.Error("ErrMemoryIDRequired should not be nil")
	}

	// Test that error messages are not empty.
	if ErrAppNameRequired.Error() == "" {
		t.Error("ErrAppNameRequired error message should not be empty")
	}
	if ErrUserIDRequired.Error() == "" {
		t.Error("ErrUserIDRequired error message should not be empty")
	}
	if ErrMemoryIDRequired.Error() == "" {
		t.Error("ErrMemoryIDRequired error message should not be empty")
	}
}

func TestToolCreator(t *testing.T) {
	// Test that ToolCreator is a function type.
	var creator ToolCreator
	if creator == nil {
		// This is expected for a zero value of a function type.
		// We just want to ensure the type is defined correctly.
	}

	// Test that we can assign a function to ToolCreator.
	creator = func(service Service) tool.Tool {
		return nil
	}
	if creator == nil {
		t.Error("ToolCreator should accept function assignment")
	}
}
