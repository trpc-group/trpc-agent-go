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
// prefix must match the tool-name charset [a-zA-Z0-9_-].
func WithNamePrefix(prefix string) Option {
	return func(t *toolSet) { t.namePrefix = prefix }
}

// WithListEnabled toggles the okf_list tool (default true).
func WithListEnabled(enabled bool) Option {
	return func(t *toolSet) { t.listEnabled = enabled }
}

// WithReadEnabled toggles the okf_read tool (default true).
func WithReadEnabled(enabled bool) Option {
	return func(t *toolSet) { t.readEnabled = enabled }
}

// WithFindEnabled toggles the okf_find tool (default true). Disable it for a
// backend that cannot search, or when locating is delegated to the knowledge
// module's semantic retrieval.
func WithFindEnabled(enabled bool) Option {
	return func(t *toolSet) { t.findEnabled = enabled }
}

// WithMaxBodyBytes caps the body returned by okf_read; longer bodies are
// truncated and Concept.Truncated is set. 0 (default) means no cap.
func WithMaxBodyBytes(n int) Option {
	return func(t *toolSet) { t.maxBodyBytes = n }
}

// WithFindLimit sets the default max number of hits okf_find returns when the
// caller does not specify a limit (default 10).
func WithFindLimit(n int) Option {
	return func(t *toolSet) { t.findLimit = n }
}
