//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package okf

// Option configures a ToolSet built by NewToolSet.
type Option func(*toolSet)

// WithNamePrefix prefixes every tool name with "<prefix>_" (e.g. "paydocs" ->
// "paydocs_okf_read"). Useful when an agent mounts several OKF bundles. The
// prefix must match the tool-name charset [a-zA-Z0-9_-] and keep the resulting
// tool names within 64 characters. NewToolSet returns an error otherwise.
func WithNamePrefix(prefix string) Option {
	return func(t *toolSet) { t.namePrefix = prefix }
}

// WithMaxBodyBytes caps the body returned by okf_read; longer bodies are
// truncated and Concept.Truncated is set. 0 (default) means no cap.
// NewToolSet rejects negative values.
func WithMaxBodyBytes(n int) Option {
	return func(t *toolSet) { t.maxBodyBytes = n }
}
