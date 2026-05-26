//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package toolpipe

import "trpc.group/trpc-go/trpc-agent-go/tool"

// OpType identifies a supported filter operation.
type OpType string

const (
	// OpGrep filters lines matching a pattern.
	OpGrep OpType = "grep"
	// OpHead keeps only the first N lines.
	OpHead OpType = "head"
	// OpTail keeps only the last N lines.
	OpTail OpType = "tail"
	// OpJQ applies a jq expression to JSON content.
	OpJQ OpType = "jq"
)

// Option configures a ToolPipe extension.
type Option func(*config)

type config struct {
	allowedNames map[string]bool
	predicate    func(tool.Tool) bool
	filterField  string
	allowedOps   map[OpType]bool
	maxInput     int64
	maxOutput    int64
	customPrompt *string // nil = use default; empty string = disable; non-empty = override
}

func defaultConfig() *config {
	return &config{
		allowedNames: make(map[string]bool),
		filterField:  "result_filter",
		allowedOps: map[OpType]bool{
			OpGrep: true,
			OpHead: true,
			OpTail: true,
		},
		maxInput:  2 << 20,  // 2MB
		maxOutput: 64 << 10, // 64KB
	}
}

// WithToolNames specifies which tools (by name) are eligible for
// result filtering. Only tools whose Declaration().Name is in this
// allowlist will have their schema augmented and results filtered.
// This is the primary safety boundary: tools not listed here are
// never wrapped.
func WithToolNames(names ...string) Option {
	return func(c *config) {
		for _, n := range names {
			c.allowedNames[n] = true
		}
	}
}

// WithToolScope adds a function-based tool selector that defines
// the scope of tools eligible for result filtering. A tool is
// wrapped if it matches either the name allowlist (WithToolNames)
// OR the scope function returns true.
//
// This is useful for dynamic tool sources like MCP ToolSets where
// tool names are not known at compile time:
//
//	toolpipe.WithToolScope(func(t tool.Tool) bool {
//	    return strings.HasPrefix(t.Declaration().Name, "mcp_")
//	})
func WithToolScope(fn func(tool.Tool) bool) Option {
	return func(c *config) {
		c.predicate = fn
	}
}

// WithFilterField sets the JSON field name injected into eligible
// tools' input schemas. Defaults to "result_filter".
func WithFilterField(field string) Option {
	return func(c *config) {
		if field != "" {
			c.filterField = field
		}
	}
}

// WithAllowedOps sets the operations the filter DSL supports.
// Defaults to grep, head, tail. Pass OpJQ to enable jq support.
func WithAllowedOps(ops ...OpType) Option {
	return func(c *config) {
		c.allowedOps = make(map[OpType]bool, len(ops))
		for _, op := range ops {
			c.allowedOps[op] = true
		}
	}
}

// WithMaxInputBytes sets the maximum input size in bytes that
// will be processed by the filter engine. Larger inputs are
// truncated before filtering. Defaults to 2MB.
func WithMaxInputBytes(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.maxInput = n
		}
	}
}

// WithMaxOutputBytes sets the maximum output size in bytes
// returned to the model after filtering. Defaults to 64KB.
func WithMaxOutputBytes(n int64) Option {
	return func(c *config) {
		if n > 0 {
			c.maxOutput = n
		}
	}
}

// WithPrompt controls the guidance text injected into the model context.
//   - WithPrompt("custom text") — fully replaces the default prompt
//   - WithPrompt("") — disables prompt injection entirely
//   - Not called — uses the built-in default prompt
//
// Use pipe.Prompt() to retrieve the default prompt content for reference
// when building your own instruction.
func WithPrompt(prompt string) Option {
	return func(c *config) {
		c.customPrompt = &prompt
	}
}
