//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package memory

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestMemory_JSONTags(t *testing.T) {
	now := time.Now()
	memory := &Memory{
		Memory:      "test memory",
		Topics:      []string{"topic1", "topic2"},
		LastUpdated: &now,
	}

	// Basic checks to ensure the JSON tags are correct.
	assert.NotEmpty(t, memory.Memory, "Memory field should not be empty.")
	assert.Len(t, memory.Topics, 2, "Topics should have 2 elements.")
	assert.NotNil(t, memory.LastUpdated, "LastUpdated should not be nil.")
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

	// Basic checks to ensure the JSON tags are correct.
	assert.NotEmpty(t, entry.ID, "ID field should not be empty.")
	assert.NotEmpty(t, entry.AppName, "AppName field should not be empty.")
	assert.NotNil(t, entry.Memory, "Memory field should not be nil.")
	assert.NotEmpty(t, entry.UserID, "UserID field should not be empty.")
	assert.False(t, entry.CreatedAt.IsZero(), "CreatedAt should not be zero.")
	assert.False(t, entry.UpdatedAt.IsZero(), "UpdatedAt should not be zero.")
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
			if tt.expectErr {
				require.Error(t, err, "Expected error but got none.")
			} else {
				require.NoError(t, err, "Expected no error but got one.")
			}

			// Check specific error types for invalid cases.
			if tt.expectErr {
				// When multiple fields are empty, the function returns the first error it encounters.
				// The order is: AppName -> UserID -> MemoryID
				if tt.key.AppName == "" {
					assert.Equal(t, ErrAppNameRequired, err,
						"Expected ErrAppNameRequired for empty app name.")
				} else if tt.key.UserID == "" {
					assert.Equal(t, ErrUserIDRequired, err,
						"Expected ErrUserIDRequired for empty user id.")
				} else if tt.key.MemoryID == "" {
					assert.Equal(t, ErrMemoryIDRequired, err,
						"Expected ErrMemoryIDRequired for empty memory id.")
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
			if tt.expectErr {
				require.Error(t, err, "Expected error but got none.")
			} else {
				require.NoError(t, err, "Expected no error but got one.")
			}

			// Check specific error types for invalid cases.
			if tt.expectErr {
				// When multiple fields are empty, the function returns the first error it encounters.
				// The order is: AppName -> UserID
				if tt.key.AppName == "" {
					assert.Equal(t, ErrAppNameRequired, err,
						"Expected ErrAppNameRequired for empty app name.")
				} else if tt.key.UserID == "" {
					assert.Equal(t, ErrUserIDRequired, err,
						"Expected ErrUserIDRequired for empty user id.")
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
			if tt.expectErr {
				require.Error(t, err, "Expected error but got none.")
			} else {
				require.NoError(t, err, "Expected no error but got one.")
			}

			// Check specific error types for invalid cases.
			if tt.expectErr {
				// When multiple fields are empty, the function returns the first error it encounters.
				// The order is: AppName -> UserID
				if tt.userKey.AppName == "" {
					assert.Equal(t, ErrAppNameRequired, err,
						"Expected ErrAppNameRequired for empty app name.")
				} else if tt.userKey.UserID == "" {
					assert.Equal(t, ErrUserIDRequired, err,
						"Expected ErrUserIDRequired for empty user id.")
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
		assert.NotEmpty(t, name, "Tool name should not be empty.")
	}

	// Test that tool names are unique.
	seen := make(map[string]bool)
	for _, name := range toolNames {
		assert.False(t, seen[name], "Duplicate tool name: %s.", name)
		seen[name] = true
	}
}

func TestErrorConstants(t *testing.T) {
	// Test that error constants are not nil and have messages.
	assert.NotNil(t, ErrAppNameRequired, "ErrAppNameRequired should not be nil.")
	assert.NotNil(t, ErrUserIDRequired, "ErrUserIDRequired should not be nil.")
	assert.NotNil(t, ErrMemoryIDRequired, "ErrMemoryIDRequired should not be nil.")

	assert.NotEmpty(t, ErrAppNameRequired.Error(), "ErrAppNameRequired message should not be empty.")
	assert.NotEmpty(t, ErrUserIDRequired.Error(), "ErrUserIDRequired message should not be empty.")
	assert.NotEmpty(t, ErrMemoryIDRequired.Error(), "ErrMemoryIDRequired message should not be empty.")
}

func TestToolCreator(t *testing.T) {
	// Test that ToolCreator is a function type.
	var creator ToolCreator
	assert.Nil(t, creator, "Zero value for ToolCreator should be nil.")

	// Test that we can assign a function to ToolCreator.
	creator = func(service Service) tool.Tool {
		return nil
	}
	assert.NotNil(t, creator, "ToolCreator should accept function assignment.")
}
