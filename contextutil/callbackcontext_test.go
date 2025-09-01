//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package contextutil

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"trpc.group/trpc-go/trpc-agent-go/agent"
)

func TestNewCallbackContext(t *testing.T) {
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
			ctx: NewInvocationContext(context.Background(), &agent.Invocation{
				AgentName: "test-agent",
			}),
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cc, err := NewCallbackContext(tt.ctx)

			if tt.expectError {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tt.errorMsg)
				assert.Nil(t, cc)
			} else {
				assert.NoError(t, err)
				assert.NotNil(t, cc)
				assert.Equal(t, tt.ctx, cc.Context)
			}
		})
	}
}
