//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package updatepolicy carries the built-in extractor's update policy across
// the memory package boundary without exposing policy discovery publicly.
package updatepolicy

import "context"

// Value identifies an update policy configured by a built-in extractor.
type Value string

type provider interface {
	ConfiguredUpdatePolicy() Value
}

type workerConfigurationKey struct{}

// From returns the policy carried by a built-in extractor.
func From(value any) Value {
	provider, ok := value.(provider)
	if !ok {
		return ""
	}
	return provider.ConfiguredUpdatePolicy()
}

// WithWorkerConfiguration records the policy selected by an Auto memory
// worker for one extraction call.
func WithWorkerConfiguration(ctx context.Context, policy Value) context.Context {
	return context.WithValue(ctx, workerConfigurationKey{}, policy)
}

// WorkerConfiguration returns the policy supplied by an Auto memory worker.
// The second result is false for direct extractor calls outside a worker.
func WorkerConfiguration(ctx context.Context) (Value, bool) {
	policy, ok := ctx.Value(workerConfigurationKey{}).(Value)
	return policy, ok
}
