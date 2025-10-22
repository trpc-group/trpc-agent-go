//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckContextCancelled(t *testing.T) {
	tests := []struct {
		name      string
		setupCtx  func() context.Context
		expectErr bool
	}{
		{
			name: "context not cancelled",
			setupCtx: func() context.Context {
				return context.Background()
			},
			expectErr: false,
		},
		{
			name: "context cancelled",
			setupCtx: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			expectErr: true,
		},
		{
			name: "context with timeout expired",
			setupCtx: func() context.Context {
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
				defer cancel()
				time.Sleep(10 * time.Millisecond)
				return ctx
			},
			expectErr: true,
		},
		{
			name: "context with timeout not expired",
			setupCtx: func() context.Context {
				ctx, _ := context.WithTimeout(context.Background(), 1*time.Second)
				return ctx
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setupCtx()
			err := CheckContextCancelled(ctx)
			if tt.expectErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInvocationFromContext(t *testing.T) {
	tests := []struct {
		name      string
		ctx       context.Context
		expectOK  bool
		expectInv *Invocation
	}{
		{
			name:      "context without invocation",
			ctx:       context.Background(),
			expectOK:  false,
			expectInv: nil,
		},
		{
			name:      "context with invocation",
			ctx:       NewInvocationContext(context.Background(), &Invocation{InvocationID: "test-123"}),
			expectOK:  true,
			expectInv: &Invocation{InvocationID: "test-123"},
		},
		{
			name:      "context with nil invocation",
			ctx:       NewInvocationContext(context.Background(), nil),
			expectOK:  true, // context.WithValue returns true even for nil value
			expectInv: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inv, ok := InvocationFromContext(tt.ctx)
			assert.Equal(t, tt.expectOK, ok)
			if tt.expectOK && tt.expectInv != nil {
				require.NotNil(t, inv)
				assert.Equal(t, tt.expectInv.InvocationID, inv.InvocationID)
			} else {
				assert.Nil(t, inv)
			}
		})
	}
}
