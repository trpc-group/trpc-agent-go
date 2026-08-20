//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package assistantmemory carries the built-in extractor's assistant-episode
// configuration across memory package boundaries.
package assistantmemory

import (
	"context"
)

// Prefix labels assistant-provided conversation episodes stored as ordinary
// episodic memories. It is descriptive content and does not carry provenance.
const Prefix = "Assistant-provided conversation episode: "

type provider interface {
	ConfiguredAssistantEpisodeExtraction() Value
}

// Value identifies whether assistant-episode extraction was configured on a
// built-in extractor.
type Value bool

type workerConfigurationKey struct{}

// Enabled reports whether a built-in extractor has assistant-episode
// extraction enabled. External extractors cannot accidentally implement this
// capability because its interface is internal to this module.
func Enabled(value any) bool {
	provider, ok := value.(provider)
	return ok && bool(provider.ConfiguredAssistantEpisodeExtraction())
}

// WithWorkerConfiguration records the assistant-episode setting captured by
// an Auto memory worker for one extraction call.
func WithWorkerConfiguration(ctx context.Context, enabled bool) context.Context {
	return context.WithValue(ctx, workerConfigurationKey{}, enabled)
}

// WorkerConfiguration returns the setting supplied by an Auto memory worker.
// The second result is false for direct extractor calls outside a worker.
func WorkerConfiguration(ctx context.Context) (bool, bool) {
	enabled, ok := ctx.Value(workerConfigurationKey{}).(bool)
	return enabled, ok
}
