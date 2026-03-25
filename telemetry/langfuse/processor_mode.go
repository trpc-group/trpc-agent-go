//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package langfuse

import (
	"fmt"
	"strings"
)

// SpanProcessorMode configures how Langfuse exports ended spans.
type SpanProcessorMode string

const (
	// SpanProcessorModeBatch keeps the existing asynchronous batch behavior.
	SpanProcessorModeBatch SpanProcessorMode = "batch"
	// SpanProcessorModeSimple exports spans synchronously when they end.
	SpanProcessorModeSimple SpanProcessorMode = "simple"
)

// WithSpanProcessorMode configures which span processor implementation is used.
func WithSpanProcessorMode(mode SpanProcessorMode) Option {
	return func(cfg *config) {
		cfg.spanProcessorMode = normalizeSpanProcessorMode(mode)
	}
}

func normalizeSpanProcessorMode(mode SpanProcessorMode) SpanProcessorMode {
	switch strings.ToLower(strings.TrimSpace(string(mode))) {
	case "", string(SpanProcessorModeBatch):
		return SpanProcessorModeBatch
	case string(SpanProcessorModeSimple):
		return SpanProcessorModeSimple
	default:
		return SpanProcessorMode(strings.ToLower(strings.TrimSpace(string(mode))))
	}
}

func validateSpanProcessorMode(mode SpanProcessorMode) error {
	switch normalizeSpanProcessorMode(mode) {
	case SpanProcessorModeBatch, SpanProcessorModeSimple:
		return nil
	default:
		return fmt.Errorf("langfuse: unsupported span processor mode %q", mode)
	}
}
