//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestNewToolContext(t *testing.T) {
	tests := []struct {
		name        string
		ctx         context.Context
		expectError bool
		errorMsg    string
	}{
		{
			name:        "context without invocation",
			ctx:         context.Background(),
			expectError: true,
			errorMsg:    "invocation not found in context",
		},
		{
			name:        "context with nil invocation",
			ctx:         NewInvocationContext(context.Background(), nil),
			expectError: true,
			errorMsg:    "invocation not found in context",
		},
		{
			name: "context with valid invocation",
			ctx: NewInvocationContext(context.Background(), &Invocation{
				AgentName: "test-agent",
				Session: &session.Session{
					AppName: "test-app",
					UserID:  "test-user",
					ID:      "test-session",
				},
			}),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tc, err := NewToolContext(tt.ctx)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, tc)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, tc)
				assert.NotNil(t, tc.CallbackContext)
				// Verify that the underlying context is preserved
				assert.Equal(t, tt.ctx, tc.CallbackContext.Context)
			}
		})
	}
}
