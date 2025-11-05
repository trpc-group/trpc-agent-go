//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package service

import (
	"context"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetinmemory "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
)

// Options holds the options for the evaluation service.
type Options struct {
	EvalSetManager    evalset.Manager                  // EvalSetManager is used to store and retrieve eval set.
	EvalResultManager evalresult.Manager               // EvalResultManager is used to store and retrieve eval results.
	Registry          registry.Registry                // Registry is used to store and retrieve evaluator.
	SessionIDSupplier func(ctx context.Context) string // SessionIDSupplier is used to generate session IDs.
}

// Option defines a function type for configuring the evaluation service.
type Option func(*Options)

// NewOptions creates a new Options with the default values.
func NewOptions(opt ...Option) *Options {
	opts := &Options{
		EvalSetManager:    evalsetinmemory.New(),
		EvalResultManager: evalresultinmemory.New(),
		Registry:          registry.New(),
		SessionIDSupplier: func(ctx context.Context) string {
			return uuid.New().String()
		},
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// WithEvalSetManager sets the eval set manager.
// InMemory eval set manager is used by default.
func WithEvalSetManager(m evalset.Manager) Option {
	return func(o *Options) {
		o.EvalSetManager = m
	}
}

// WithEvalResultManager sets the eval result manager.
// InMemory eval result manager is used by default.
func WithEvalResultManager(m evalresult.Manager) Option {
	return func(o *Options) {
		o.EvalResultManager = m
	}
}

// WithRegistry sets the evaluator registry.
// Default evaluator registry is used by default.
func WithRegistry(r registry.Registry) Option {
	return func(o *Options) {
		o.Registry = r
	}
}

// WithSessionIDSupplier sets the function used to generate session IDs.
// UUID generator is used by default.
func WithSessionIDSupplier(s func(ctx context.Context) string) Option {
	return func(o *Options) {
		o.SessionIDSupplier = s
	}
}
