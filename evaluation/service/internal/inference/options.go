//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inference

import "trpc.group/trpc-go/trpc-agent-go/runner"

// ToolMockMode selects which tool mock entries apply to an inference run.
type ToolMockMode string

const (
	// ToolMockModeActual applies actual tool mock entries.
	ToolMockModeActual ToolMockMode = "actual"
	// ToolMockModeExpected applies expected tool mock entries.
	ToolMockModeExpected ToolMockMode = "expected"
)

type options struct {
	toolMockRunner runner.Runner
	toolMockMode   ToolMockMode
}

// Option configures inference execution.
type Option func(*options)

// WithToolMockRunner sets the runner used by LLM-generated tool mocks.
func WithToolMockRunner(r runner.Runner) Option {
	return func(o *options) {
		o.toolMockRunner = r
	}
}

// WithToolMockMode sets which tool mock entries apply.
func WithToolMockMode(mode ToolMockMode) Option {
	return func(o *options) {
		o.toolMockMode = mode
	}
}

func newOptions(opt ...Option) *options {
	opts := &options{}
	for _, o := range opt {
		if o != nil {
			o(opts)
		}
	}
	return opts
}
