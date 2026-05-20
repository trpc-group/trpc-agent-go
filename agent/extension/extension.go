//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package extension

import (
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Extension is an agent-scoped capability bundle. See the package
// documentation for the full rationale and lifecycle.
type Extension interface {
	// Name identifies the extension for logging, error wrapping
	// and duplicate detection. Must be non-empty and stable for
	// the lifetime of the agent that hosts the extension.
	Name() string

	// Register is called exactly once during agent construction.
	// The Registry passed in accumulates callbacks and tools; the
	// extension MUST NOT keep a reference to it beyond the call.
	Register(r *Registry)
}

// Bundle is the aggregated result of running Register on every
// extension passed to a consuming agent. Fields may be nil/empty
// when no extension contributed to the corresponding surface, so
// callers should nil-check before iterating.
//
// Callbacks are exposed as the standard *agent.Callbacks /
// *model.Callbacks / *tool.Callbacks containers so consuming agents
// can merge them with user-registered callbacks using the same
// helpers they already apply elsewhere. Tools is a plain []tool.Tool
// in install order; name dedup is the consuming agent's
// responsibility because the natural dedup point varies by
// implementation (registerTools vs. InvocationToolSurface vs.
// per-invocation filter).
type Bundle struct {
	AgentCallbacks *agent.Callbacks
	ModelCallbacks *model.Callbacks
	ToolCallbacks  *tool.Callbacks
	Tools          []tool.Tool
}

// Collect runs Register on every extension in install order and
// returns the aggregated Bundle. It enforces two invariants that
// every consuming agent would otherwise have to re-implement:
//
//   - no nil extensions
//   - no two extensions sharing the same Name (case-sensitive)
//
// Both violations are returned as a non-nil error and the partially-
// built Bundle is discarded; consuming agents should treat any
// non-nil error from Collect as fatal-during-construction.
//
// When extensions is empty the function returns (nil, nil) so the
// nil-Bundle short-circuit path on the consumer side stays simple.
func Collect(extensions []Extension) (*Bundle, error) {
	if len(extensions) == 0 {
		return nil, nil
	}
	bundle := &Bundle{
		AgentCallbacks: agent.NewCallbacks(),
		ModelCallbacks: model.NewCallbacks(),
		ToolCallbacks:  tool.NewCallbacks(),
	}
	seen := make(map[string]struct{}, len(extensions))
	for i, e := range extensions {
		if e == nil {
			return nil, fmt.Errorf("extension: nil extension at index %d", i)
		}
		name := e.Name()
		if name == "" {
			return nil, fmt.Errorf(
				"extension: empty name at index %d (%T)", i, e,
			)
		}
		if _, dup := seen[name]; dup {
			return nil, fmt.Errorf(
				"extension: duplicate name %q (extension index %d)", name, i,
			)
		}
		seen[name] = struct{}{}

		r := newRegistry(name, bundle.AgentCallbacks, bundle.ModelCallbacks, bundle.ToolCallbacks)
		e.Register(r)
		bundle.Tools = append(bundle.Tools, r.tools...)
	}
	return bundle, nil
}

// IsEmpty reports whether b carries no contributions. Convenience
// helper for consumers that want to skip the merge pipeline
// entirely when no extension actually populated anything.
func (b *Bundle) IsEmpty() bool {
	if b == nil {
		return true
	}
	if len(b.Tools) > 0 {
		return false
	}
	if hasAgentContent(b.AgentCallbacks) {
		return false
	}
	if hasModelContent(b.ModelCallbacks) {
		return false
	}
	if hasToolContent(b.ToolCallbacks) {
		return false
	}
	return true
}

// hasAgentContent / hasModelContent / hasToolContent are tiny
// content predicates. They live next to Bundle because consumers
// frequently need to ask "did any extension populate this slot?"
// before deciding whether to allocate a merged callback chain on
// the user-side; centralising the answer keeps the per-agent
// merge code free of "is this empty Callbacks worth merging?"
// boilerplate.
func hasAgentContent(c *agent.Callbacks) bool {
	return c != nil && (len(c.BeforeAgent) > 0 || len(c.AfterAgent) > 0)
}

func hasModelContent(c *model.Callbacks) bool {
	return c != nil && (len(c.BeforeModel) > 0 || len(c.AfterModel) > 0)
}

func hasToolContent(c *tool.Callbacks) bool {
	return c != nil &&
		(len(c.BeforeTool) > 0 || len(c.AfterTool) > 0 ||
			c.ToolResultMessages != nil)
}
