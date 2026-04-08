//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

type options struct {
	observer Observer
}

// Option configures one advanced PromptIter run behavior.
type Option func(*options)

// WithObserver appends one runtime observer to the run.
func WithObserver(observer Observer) Option {
	return func(opts *options) {
		opts.observer = observer
	}
}

func newOptions(opts ...Option) *options {
	options := &options{}
	for _, opt := range opts {
		if opt != nil {
			opt(options)
		}
	}
	return options
}
