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
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				t.Cleanup(cancel)
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

func TestGetStateValueFromContext(t *testing.T) {
	t.Run("context without invocation", func(t *testing.T) {
		ctx := context.Background()
		val, ok := GetStateValueFromContext[string](ctx, "key")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("context with invocation but key not found", func(t *testing.T) {
		inv := NewInvocation()
		ctx := NewInvocationContext(context.Background(), inv)
		val, ok := GetStateValueFromContext[string](ctx, "nonexistent")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("context with invocation and matching type", func(t *testing.T) {
		inv := NewInvocation()
		inv.SetState("agent:string", "hello")
		inv.SetState("agent:int", 42)
		inv.SetState("agent:time", time.Now())
		ctx := NewInvocationContext(context.Background(), inv)

		// Test string value.
		strVal, ok := GetStateValueFromContext[string](ctx, "agent:string")
		assert.True(t, ok)
		assert.Equal(t, "hello", strVal)

		// Test int value.
		intVal, ok := GetStateValueFromContext[int](ctx, "agent:int")
		assert.True(t, ok)
		assert.Equal(t, 42, intVal)

		// Test time.Time value.
		timeVal, ok := GetStateValueFromContext[time.Time](ctx, "agent:time")
		assert.True(t, ok)
		assert.IsType(t, time.Time{}, timeVal)
	})

	t.Run("context with invocation but type mismatch", func(t *testing.T) {
		inv := NewInvocation()
		inv.SetState("agent:value", "hello")
		ctx := NewInvocationContext(context.Background(), inv)

		// Try to get as int when it's actually string.
		intVal, ok := GetStateValueFromContext[int](ctx, "agent:value")
		assert.False(t, ok)
		assert.Equal(t, 0, intVal)
	})

	t.Run("context with nil invocation", func(t *testing.T) {
		ctx := NewInvocationContext(context.Background(), nil)
		val, ok := GetStateValueFromContext[string](ctx, "key")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("complex struct type", func(t *testing.T) {
		type CustomData struct {
			ID        string
			Timestamp time.Time
			Metadata  map[string]string
		}

		inv := NewInvocation()
		data := CustomData{
			ID:        "test-123",
			Timestamp: time.Now(),
			Metadata: map[string]string{
				"key1": "value1",
			},
		}
		inv.SetState("agent:custom_data", data)
		ctx := NewInvocationContext(context.Background(), inv)

		retrieved, ok := GetStateValueFromContext[CustomData](ctx, "agent:custom_data")
		require.True(t, ok)
		assert.Equal(t, data.ID, retrieved.ID)
		assert.Equal(t, data.Metadata, retrieved.Metadata)
	})
}

func TestGetRuntimeStateValueFromContext(t *testing.T) {
	t.Run("context without invocation", func(t *testing.T) {
		ctx := context.Background()
		val, ok := GetRuntimeStateValueFromContext[string](ctx, "key")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("context with invocation but key not found", func(t *testing.T) {
		inv := NewInvocation()
		ctx := NewInvocationContext(context.Background(), inv)
		val, ok := GetRuntimeStateValueFromContext[string](ctx, "nonexistent")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("context with invocation but nil RuntimeState", func(t *testing.T) {
		inv := NewInvocation()
		ctx := NewInvocationContext(context.Background(), inv)
		val, ok := GetRuntimeStateValueFromContext[string](ctx, "key")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("context with invocation and matching type", func(t *testing.T) {
		inv := NewInvocation(
			WithInvocationRunOptions(RunOptions{
				RuntimeState: map[string]any{
					"user_id": "12345",
					"room_id": 678,
					"config":  true,
					"score":   3.14,
				},
			}),
		)
		ctx := NewInvocationContext(context.Background(), inv)

		// Test string value.
		userID, ok := GetRuntimeStateValueFromContext[string](ctx, "user_id")
		assert.True(t, ok)
		assert.Equal(t, "12345", userID)

		// Test int value.
		roomID, ok := GetRuntimeStateValueFromContext[int](ctx, "room_id")
		assert.True(t, ok)
		assert.Equal(t, 678, roomID)

		// Test bool value.
		config, ok := GetRuntimeStateValueFromContext[bool](ctx, "config")
		assert.True(t, ok)
		assert.Equal(t, true, config)

		// Test float64 value.
		score, ok := GetRuntimeStateValueFromContext[float64](ctx, "score")
		assert.True(t, ok)
		assert.Equal(t, 3.14, score)
	})

	t.Run("context with invocation but type mismatch", func(t *testing.T) {
		inv := NewInvocation(
			WithInvocationRunOptions(RunOptions{
				RuntimeState: map[string]any{
					"value": "hello",
				},
			}),
		)
		ctx := NewInvocationContext(context.Background(), inv)

		// Try to get as int when it's actually string.
		intVal, ok := GetRuntimeStateValueFromContext[int](ctx, "value")
		assert.False(t, ok)
		assert.Equal(t, 0, intVal)
	})

	t.Run("context with nil invocation", func(t *testing.T) {
		ctx := NewInvocationContext(context.Background(), nil)
		val, ok := GetRuntimeStateValueFromContext[string](ctx, "key")
		assert.False(t, ok)
		assert.Equal(t, "", val)
	})

	t.Run("slice type", func(t *testing.T) {
		inv := NewInvocation(
			WithInvocationRunOptions(RunOptions{
				RuntimeState: map[string]any{
					"tags": []string{"tag1", "tag2", "tag3"},
				},
			}),
		)
		ctx := NewInvocationContext(context.Background(), inv)

		tags, ok := GetRuntimeStateValueFromContext[[]string](ctx, "tags")
		assert.True(t, ok)
		assert.Equal(t, []string{"tag1", "tag2", "tag3"}, tags)
	})

	t.Run("map type", func(t *testing.T) {
		inv := NewInvocation(
			WithInvocationRunOptions(RunOptions{
				RuntimeState: map[string]any{
					"metadata": map[string]string{
						"key1": "value1",
						"key2": "value2",
					},
				},
			}),
		)
		ctx := NewInvocationContext(context.Background(), inv)

		metadata, ok := GetRuntimeStateValueFromContext[map[string]string](ctx, "metadata")
		assert.True(t, ok)
		assert.Equal(t, "value1", metadata["key1"])
		assert.Equal(t, "value2", metadata["key2"])
	})

	t.Run("complex struct type", func(t *testing.T) {
		type UserContext struct {
			UserID   string
			RoomID   int
			Metadata map[string]string
		}

		userCtx := UserContext{
			UserID: "user-123",
			RoomID: 456,
			Metadata: map[string]string{
				"key1": "value1",
			},
		}
		inv := NewInvocation(
			WithInvocationRunOptions(RunOptions{
				RuntimeState: map[string]any{
					"user_context": userCtx,
				},
			}),
		)
		ctx := NewInvocationContext(context.Background(), inv)

		retrieved, ok := GetRuntimeStateValueFromContext[UserContext](ctx, "user_context")
		require.True(t, ok)
		assert.Equal(t, userCtx.UserID, retrieved.UserID)
		assert.Equal(t, userCtx.RoomID, retrieved.RoomID)
		assert.Equal(t, userCtx.Metadata, retrieved.Metadata)
	})
}
