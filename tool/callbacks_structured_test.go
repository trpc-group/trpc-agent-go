//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallbacksStructured_BeforeTool(t *testing.T) {
	tests := []struct {
		name             string
		callbacks        []BeforeToolCallbackStructured
		wantCustomResult bool
		wantModifiedArgs bool
		wantErr          bool
	}{
		{
			name:             "no callbacks",
			callbacks:        nil,
			wantCustomResult: false,
			wantErr:          false,
		},
		{
			name: "callback returns nil",
			callbacks: []BeforeToolCallbackStructured{
				func(ctx context.Context, args *BeforeToolArgs) (
					*BeforeToolResult, error,
				) {
					return nil, nil
				},
			},
			wantCustomResult: false,
			wantErr:          false,
		},
		{
			name: "callback returns custom result",
			callbacks: []BeforeToolCallbackStructured{
				func(ctx context.Context, args *BeforeToolArgs) (
					*BeforeToolResult, error,
				) {
					return &BeforeToolResult{
						CustomResult: "custom",
					}, nil
				},
			},
			wantCustomResult: true,
			wantErr:          false,
		},
		{
			name: "callback returns modified arguments",
			callbacks: []BeforeToolCallbackStructured{
				func(ctx context.Context, args *BeforeToolArgs) (
					*BeforeToolResult, error,
				) {
					return &BeforeToolResult{
						ModifiedArguments: []byte(`{"modified":true}`),
					}, nil
				},
			},
			wantCustomResult: false,
			wantModifiedArgs: true,
			wantErr:          false,
		},
		{
			name: "callback returns error",
			callbacks: []BeforeToolCallbackStructured{
				func(ctx context.Context, args *BeforeToolArgs) (
					*BeforeToolResult, error,
				) {
					return nil, errors.New("test error")
				},
			},
			wantCustomResult: false,
			wantErr:          true,
		},
		{
			name: "multiple callbacks, first returns custom result",
			callbacks: []BeforeToolCallbackStructured{
				func(ctx context.Context, args *BeforeToolArgs) (
					*BeforeToolResult, error,
				) {
					return &BeforeToolResult{
						CustomResult: "custom",
					}, nil
				},
				func(ctx context.Context, args *BeforeToolArgs) (
					*BeforeToolResult, error,
				) {
					t.Error("second callback should not be called")
					return nil, nil
				},
			},
			wantCustomResult: true,
			wantErr:          false,
		},
		{
			name: "callback returns result without custom result or modified args",
			callbacks: []BeforeToolCallbackStructured{
				func(ctx context.Context, args *BeforeToolArgs) (
					*BeforeToolResult, error,
				) {
					return &BeforeToolResult{}, nil
				},
			},
			wantCustomResult: false,
			wantModifiedArgs: false,
			wantErr:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCallbacksStructured()
			for _, cb := range tt.callbacks {
				c.RegisterBeforeTool(cb)
			}

			result, err := c.RunBeforeTool(
				context.Background(),
				"test_tool",
				&Declaration{},
				[]byte(`{}`),
			)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			hasCustomResult := result != nil && result.CustomResult != nil
			assert.Equal(t, tt.wantCustomResult, hasCustomResult)

			hasModifiedArgs := result != nil &&
				result.ModifiedArguments != nil
			assert.Equal(t, tt.wantModifiedArgs, hasModifiedArgs)
		})
	}
}

func TestCallbacksStructured_AfterTool(t *testing.T) {
	tests := []struct {
		name             string
		callbacks        []AfterToolCallbackStructured
		wantCustomResult bool
		wantErr          bool
	}{
		{
			name:             "no callbacks",
			callbacks:        nil,
			wantCustomResult: false,
			wantErr:          false,
		},
		{
			name: "callback returns nil",
			callbacks: []AfterToolCallbackStructured{
				func(ctx context.Context, args *AfterToolArgs) (
					*AfterToolResult, error,
				) {
					return nil, nil
				},
			},
			wantCustomResult: false,
			wantErr:          false,
		},
		{
			name: "callback returns custom result",
			callbacks: []AfterToolCallbackStructured{
				func(ctx context.Context, args *AfterToolArgs) (
					*AfterToolResult, error,
				) {
					return &AfterToolResult{
						CustomResult: "custom",
					}, nil
				},
			},
			wantCustomResult: true,
			wantErr:          false,
		},
		{
			name: "callback returns error",
			callbacks: []AfterToolCallbackStructured{
				func(ctx context.Context, args *AfterToolArgs) (
					*AfterToolResult, error,
				) {
					return nil, errors.New("test error")
				},
			},
			wantCustomResult: false,
			wantErr:          true,
		},
		{
			name: "callback can access tool error from args",
			callbacks: []AfterToolCallbackStructured{
				func(ctx context.Context, args *AfterToolArgs) (
					*AfterToolResult, error,
				) {
					if args.Error == nil {
						t.Error("expected error in args")
					}
					return nil, nil
				},
			},
			wantCustomResult: false,
			wantErr:          false,
		},
		{
			name: "multiple callbacks, first returns custom result",
			callbacks: []AfterToolCallbackStructured{
				func(ctx context.Context, args *AfterToolArgs) (
					*AfterToolResult, error,
				) {
					return &AfterToolResult{
						CustomResult: "custom",
					}, nil
				},
				func(ctx context.Context, args *AfterToolArgs) (
					*AfterToolResult, error,
				) {
					t.Error("second callback should not be called")
					return nil, nil
				},
			},
			wantCustomResult: true,
			wantErr:          false,
		},
		{
			name: "callback returns result without custom result",
			callbacks: []AfterToolCallbackStructured{
				func(ctx context.Context, args *AfterToolArgs) (
					*AfterToolResult, error,
				) {
					return &AfterToolResult{}, nil
				},
			},
			wantCustomResult: false,
			wantErr:          false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCallbacksStructured()
			for _, cb := range tt.callbacks {
				c.RegisterAfterTool(cb)
			}

			result, err := c.RunAfterTool(
				context.Background(),
				"test_tool",
				&Declaration{},
				[]byte(`{}`),
				"original_result",
				errors.New("tool error"),
			)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			hasCustomResult := result != nil && result.CustomResult != nil
			assert.Equal(t, tt.wantCustomResult, hasCustomResult)
		})
	}
}
