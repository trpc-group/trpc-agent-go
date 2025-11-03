//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCallbacksStructured_BeforeModel(t *testing.T) {
	tests := []struct {
		name          string
		callbacks     []BeforeModelCallbackStructured
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
			callbacks: []BeforeModelCallbackStructured{
				func(ctx context.Context, args *BeforeModelArgs) (
					*BeforeModelResult, error,
				) {
					return nil, nil
				},
			},
			wantCustomRsp: false,
			wantErr:       false,
		},
		{
			name: "callback returns custom response",
			callbacks: []BeforeModelCallbackStructured{
				func(ctx context.Context, args *BeforeModelArgs) (
					*BeforeModelResult, error,
				) {
					return &BeforeModelResult{
						CustomResponse: &Response{},
					}, nil
				},
			},
			wantCustomRsp: true,
			wantErr:       false,
		},
		{
			name: "callback returns error",
			callbacks: []BeforeModelCallbackStructured{
				func(ctx context.Context, args *BeforeModelArgs) (
					*BeforeModelResult, error,
				) {
					return nil, errors.New("test error")
				},
			},
			wantCustomRsp: false,
			wantErr:       true,
		},
		{
			name: "multiple callbacks, first returns custom response",
			callbacks: []BeforeModelCallbackStructured{
				func(ctx context.Context, args *BeforeModelArgs) (
					*BeforeModelResult, error,
				) {
					return &BeforeModelResult{
						CustomResponse: &Response{},
					}, nil
				},
				func(ctx context.Context, args *BeforeModelArgs) (
					*BeforeModelResult, error,
				) {
					t.Error("second callback should not be called")
					return nil, nil
				},
			},
			wantCustomRsp: true,
			wantErr:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCallbacksStructured()
			for _, cb := range tt.callbacks {
				c.RegisterBeforeModel(cb)
			}

			req := &Request{}
			result, err := c.RunBeforeModel(context.Background(), req)

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

func TestCallbacksStructured_AfterModel(t *testing.T) {
	tests := []struct {
		name          string
		callbacks     []AfterModelCallbackStructured
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
			callbacks: []AfterModelCallbackStructured{
				func(ctx context.Context, args *AfterModelArgs) (
					*AfterModelResult, error,
				) {
					return nil, nil
				},
			},
			wantCustomRsp: false,
			wantErr:       false,
		},
		{
			name: "callback returns custom response",
			callbacks: []AfterModelCallbackStructured{
				func(ctx context.Context, args *AfterModelArgs) (
					*AfterModelResult, error,
				) {
					return &AfterModelResult{
						CustomResponse: &Response{},
					}, nil
				},
			},
			wantCustomRsp: true,
			wantErr:       false,
		},
		{
			name: "callback returns error",
			callbacks: []AfterModelCallbackStructured{
				func(ctx context.Context, args *AfterModelArgs) (
					*AfterModelResult, error,
				) {
					return nil, errors.New("test error")
				},
			},
			wantCustomRsp: false,
			wantErr:       true,
		},
		{
			name: "callback can access error from args",
			callbacks: []AfterModelCallbackStructured{
				func(ctx context.Context, args *AfterModelArgs) (
					*AfterModelResult, error,
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := NewCallbacksStructured()
			for _, cb := range tt.callbacks {
				c.RegisterAfterModel(cb)
			}

			req := &Request{}
			rsp := &Response{}
			modelErr := errors.New("model error")
			result, err := c.RunAfterModel(
				context.Background(), req, rsp, modelErr,
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
