//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package manager

import (
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/store"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/store/inmemory"
)

// Option configures the PromptIter run manager.
type Option func(*options)

type options struct {
	store store.Store
}

func newOptions(opts ...Option) *options {
	options := &options{
		store: inmemory.New(),
	}
	for _, opt := range opts {
		opt(options)
	}
	return options
}

// WithStore sets the store used to persist PromptIter runs.
func WithStore(store store.Store) Option {
	return func(opts *options) {
		opts.store = store
	}
}
