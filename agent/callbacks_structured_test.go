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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestCallbacksStructured_BeforeAgent(t *testing.T) {
	tests := []struct {
		name          string
		callbacks     []BeforeAgentCallbackStructured
		wantCustomRsp bool
		wantErr       bool
	}{
		{
			name:          "no callbacks",
			callbacks:     nil,
			wantCustomRsp: false,
			wantErr:       false,
		},
		{
			name: "callback returns nil",
			callbacks: []BeforeAgentCallbackStructured{
				func(ctx context.Context, args *BeforeAgentArgs) (
					*BeforeAgentResult, error,
				) {
					return nil, nil
				},
			},
			wantCustomRsp: false,
			wantErr:       false,
		},
		{
			name: "callback returns custom response",
			callbacks: []BeforeAgentCallbackStructured{
				func(ctx context.Context, args *BeforeAgentArgs) (
					*BeforeAgentResult, error,
				) {
					return &BeforeAgentResult{
						CustomResponse: &model.Response{},
					}, nil
				},
			},
			wantCustomRsp: true,
			wantErr:       false,
		},
		{
			name: "callback returns error",
			callbacks: []BeforeAgentCallbackStructured{
				func(ctx context.Context, args *BeforeAgentArgs) (
					*BeforeAgentResult, error,
				) {
					return nil, errors.New("test error")
				},
			},
			wantCustomRsp: false,
			wantErr:       true,
		},
		{
			name: "callback can access invocation from args",
			callbacks: []BeforeAgentCallbackStructured{
				func(ctx context.Context, args *BeforeAgentArgs) (
					*BeforeAgentResult, error,
				) {
					if args.Invocation == nil {
						t.Error("expected invocation in args")
					}
					return nil, nil
				},
			},
			wantCustomRsp: false,
			wantErr:       false,
		},
		{
			name: "multiple callbacks, first returns custom response",
			callbacks: []BeforeAgentCallbackStructured{
				func(ctx context.Context, args *BeforeAgentArgs) (
					*BeforeAgentResult, error,
				) {
					return &BeforeAgentResult{
						CustomResponse: &model.Response{},
					}, nil
				},
				func(ctx context.Context, args *BeforeAgentArgs) (
					*BeforeAgentResult, error,
				) {
					t.Error("second callback should not be called")
					return nil, nil
				},
			},
			wantCustomRsp: true,
			wantErr:       false,
		},
		{
			name: "callback returns result without custom response",
			callbacks: []BeforeAgentCallbackStructured{
				func(ctx context.Context, args *BeforeAgentArgs) (
					*BeforeAgentResult, error,
				) {
					return &BeforeAgentResult{}, nil
				},
			},
			wantCustomRsp: false,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCallbacksStructured()
			for _, cb := range tt.callbacks {
				c.RegisterBeforeAgent(cb)
			}

			invocation := &Invocation{}
			result, err := c.RunBeforeAgent(context.Background(), invocation)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			hasCustomRsp := result != nil && result.CustomResponse != nil
			assert.Equal(t, tt.wantCustomRsp, hasCustomRsp)
		})
	}
}

func TestCallbacksStructured_AfterAgent(t *testing.T) {
	tests := []struct {
		name          string
		callbacks     []AfterAgentCallbackStructured
		wantCustomRsp bool
		wantErr       bool
	}{
		{
			name:          "no callbacks",
			callbacks:     nil,
			wantCustomRsp: false,
			wantErr:       false,
		},
		{
			name: "callback returns nil",
			callbacks: []AfterAgentCallbackStructured{
				func(ctx context.Context, args *AfterAgentArgs) (
					*AfterAgentResult, error,
				) {
					return nil, nil
				},
			},
			wantCustomRsp: false,
			wantErr:       false,
		},
		{
			name: "callback returns custom response",
			callbacks: []AfterAgentCallbackStructured{
				func(ctx context.Context, args *AfterAgentArgs) (
					*AfterAgentResult, error,
				) {
					return &AfterAgentResult{
						CustomResponse: &model.Response{},
					}, nil
				},
			},
			wantCustomRsp: true,
			wantErr:       false,
		},
		{
			name: "callback returns error",
			callbacks: []AfterAgentCallbackStructured{
				func(ctx context.Context, args *AfterAgentArgs) (
					*AfterAgentResult, error,
				) {
					return nil, errors.New("test error")
				},
			},
			wantCustomRsp: false,
			wantErr:       true,
		},
		{
			name: "callback can access error from args",
			callbacks: []AfterAgentCallbackStructured{
				func(ctx context.Context, args *AfterAgentArgs) (
					*AfterAgentResult, error,
				) {
					if args.Error == nil {
						t.Error("expected error in args")
					}
					return nil, nil
				},
			},
			wantCustomRsp: false,
			wantErr:       false,
		},
		{
			name: "multiple callbacks, first returns custom response",
			callbacks: []AfterAgentCallbackStructured{
				func(ctx context.Context, args *AfterAgentArgs) (
					*AfterAgentResult, error,
				) {
					return &AfterAgentResult{
						CustomResponse: &model.Response{},
					}, nil
				},
				func(ctx context.Context, args *AfterAgentArgs) (
					*AfterAgentResult, error,
				) {
					t.Error("second callback should not be called")
					return nil, nil
				},
			},
			wantCustomRsp: true,
			wantErr:       false,
		},
		{
			name: "callback returns result without custom response",
			callbacks: []AfterAgentCallbackStructured{
				func(ctx context.Context, args *AfterAgentArgs) (
					*AfterAgentResult, error,
				) {
					return &AfterAgentResult{}, nil
				},
			},
			wantCustomRsp: false,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCallbacksStructured()
			for _, cb := range tt.callbacks {
				c.RegisterAfterAgent(cb)
			}

			invocation := &Invocation{}
			runErr := errors.New("agent error")
			result, err := c.RunAfterAgent(
				context.Background(), invocation, runErr,
			)

			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)

			hasCustomRsp := result != nil && result.CustomResponse != nil
			assert.Equal(t, tt.wantCustomRsp, hasCustomRsp)
		})
	}
}
