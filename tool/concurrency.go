//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tool

// ConcurrencyConfig configures limits for parallel tool execution.
//
// MaxConcurrency limits all active tool calls within one invocation of the
// owning agent or Tools node. Concurrent invocations have independent limits.
// A non-positive value leaves overall concurrency unlimited.
//
// Groups apply additional shared limits to the named tools. Tool names that do
// not appear in a group are constrained only by MaxConcurrency. A tool name
// must appear in at most one positive-limit group. LLMAgent and Graph options
// reject configurations with duplicate group membership.
//
// Limits apply only to calls executed directly by the owning agent or Tools
// node invocation. If a tool runs a child agent, that child invocation uses a
// new limiter built from the child's concurrency configuration.
//
// ConcurrencyConfig only affects execution when parallel tools are enabled.
// Its zero value preserves unrestricted parallel execution.
type ConcurrencyConfig struct {
	// MaxConcurrency limits all active tool calls within one invocation. A
	// non-positive value leaves overall concurrency unlimited.
	MaxConcurrency int
	// Groups contains additional shared limits for selected tool names.
	Groups []ConcurrencyGroup
}

// ConcurrencyGroup defines a shared concurrency limit for a set of tools.
//
// Limit is the maximum combined number of active calls for all ToolNames in
// the group within one invocation. A non-positive Limit and empty tool names
// are ignored. Tool names may refer to tools that are resolved dynamically at
// runtime.
type ConcurrencyGroup struct {
	// ToolNames lists the effective tool names that share this group.
	ToolNames []string
	// Limit is the maximum combined number of active calls in this group.
	Limit int
}
