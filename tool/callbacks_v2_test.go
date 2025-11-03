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

func TestCallbacksV2_BeforeTool(t *testing.T) {
	tests := []struct {
		name              string
		callbacks         []BeforeToolCallbackV2
		wantCustomResult  bool
		wantModifiedArgs  bool
		wantErr           bool
	}{
		{
			name:             "no callbacks",
			callbacks:        nil,
			wantCustomResult: false,
			wantErr:          false,
		},
		{
			name: "callback returns nil",
			callbacks: []BeforeToolCallbackV2{
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
			callbacks: []BeforeToolCallbackV2{
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
			callbacks: []BeforeToolCallbackV2{
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
			callbacks: []BeforeToolCallbackV2{
				func(ctx context.Context, args *BeforeToolArgs) (
					*BeforeToolResult, error,
				) {
					return nil, errors.New("test error")
				},
			},
			wantCustomResult: false,
			wantErr:          true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCallbacksV2()
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

func TestCallbacksV2_AfterTool(t *testing.T) {
	tests := []struct {
		name             string
		callbacks        []AfterToolCallbackV2
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
			callbacks: []AfterToolCallbackV2{
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
			callbacks: []AfterToolCallbackV2{
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
			callbacks: []AfterToolCallbackV2{
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
			callbacks: []AfterToolCallbackV2{
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCallbacksV2()
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
