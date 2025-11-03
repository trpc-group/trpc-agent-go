//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package llmagent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// TestWithCallbacks_Generic tests that the generic callback functions work
// with both legacy and structured callbacks.
func TestWithCallbacks_Generic(t *testing.T) {
	t.Run("WithAgentCallbacks accepts legacy callbacks", func(t *testing.T) {
		legacyCallbacks := agent.NewCallbacks()
		legacyCallbacks.RegisterBeforeAgent(
			func(ctx context.Context, inv *agent.Invocation) (
				*model.Response, error,
			) {
				return nil, nil
			},
		)

		opts := &Options{}
		opt := WithAgentCallbacks(legacyCallbacks)
		opt(opts)

		assert.Equal(t, legacyCallbacks, opts.AgentCallbacks)
		assert.Nil(t, opts.AgentCallbacksStructured)
	})

	t.Run("WithAgentCallbacks accepts structured callbacks", func(t *testing.T) {
		structuredCallbacks := agent.NewCallbacksStructured()
		structuredCallbacks.RegisterBeforeAgent(
			func(ctx context.Context, args *agent.BeforeAgentArgs) (
				*agent.BeforeAgentResult, error,
			) {
				return nil, nil
			},
		)

		opts := &Options{}
		opt := WithAgentCallbacks(structuredCallbacks)
		opt(opts)

		assert.Nil(t, opts.AgentCallbacks)
		assert.Equal(t, structuredCallbacks, opts.AgentCallbacksStructured)
	})

	t.Run("WithModelCallbacks accepts legacy callbacks", func(t *testing.T) {
		legacyCallbacks := model.NewCallbacks()
		legacyCallbacks.RegisterBeforeModel(
			func(ctx context.Context, req *model.Request) (
				*model.Response, error,
			) {
				return nil, nil
			},
		)

		opts := &Options{}
		opt := WithModelCallbacks(legacyCallbacks)
		opt(opts)

		assert.Equal(t, legacyCallbacks, opts.ModelCallbacks)
		assert.Nil(t, opts.ModelCallbacksStructured)
	})

	t.Run("WithModelCallbacks accepts structured callbacks", func(t *testing.T) {
		structuredCallbacks := model.NewCallbacksStructured()
		structuredCallbacks.RegisterBeforeModel(
			func(ctx context.Context, args *model.BeforeModelArgs) (
				*model.BeforeModelResult, error,
			) {
				return nil, nil
			},
		)

		opts := &Options{}
		opt := WithModelCallbacks(structuredCallbacks)
		opt(opts)

		assert.Nil(t, opts.ModelCallbacks)
		assert.Equal(t, structuredCallbacks, opts.ModelCallbacksStructured)
	})

	t.Run("WithToolCallbacks accepts legacy callbacks", func(t *testing.T) {
		legacyCallbacks := tool.NewCallbacks()
		legacyCallbacks.RegisterBeforeTool(
			func(ctx context.Context, toolName string,
				toolDeclaration *tool.Declaration, jsonArgs *[]byte) (
				any, error,
			) {
				return nil, nil
			},
		)

		opts := &Options{}
		opt := WithToolCallbacks(legacyCallbacks)
		opt(opts)

		assert.Equal(t, legacyCallbacks, opts.ToolCallbacks)
		assert.Nil(t, opts.ToolCallbacksStructured)
	})

	t.Run("WithToolCallbacks accepts structured callbacks", func(t *testing.T) {
		structuredCallbacks := tool.NewCallbacksStructured()
		structuredCallbacks.RegisterBeforeTool(
			func(ctx context.Context, args *tool.BeforeToolArgs) (
				*tool.BeforeToolResult, error,
			) {
				return nil, nil
			},
		)

		opts := &Options{}
		opt := WithToolCallbacks(structuredCallbacks)
		opt(opts)

		assert.Nil(t, opts.ToolCallbacks)
		assert.Equal(t, structuredCallbacks, opts.ToolCallbacksStructured)
	})
}
