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
	"runtime/debug"

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

// Contributions is the aggregated result of running Register on
// every extension passed to a consuming agent.
//
// The concrete storage is intentionally opaque. Consuming agents
// should use the accessor methods below instead of depending on
// the internal representation, which leaves this package room to
// evolve how extension contributions are stored without changing
// the public Extension / Registry authoring contract.
type Contributions struct {
	agentCallbacks *agent.Callbacks
	modelCallbacks *model.Callbacks
	toolCallbacks  *tool.Callbacks
	tools          []tool.Tool
}

// Collect runs Register on every extension in install order and
// returns the aggregated Contributions. It enforces two invariants that
// every consuming agent would otherwise have to re-implement:
//
//   - no nil extensions
//   - no two extensions sharing the same Name (case-sensitive)
//
// Both violations are returned as a non-nil error and the partially-
// built Contributions is discarded; consuming agents should treat any
// non-nil error from Collect as fatal-during-construction.
// A panic from Extension.Register is also converted into an error
// that includes the extension name, index and stack trace; later
// extensions are not registered after such a panic.
//
// When extensions is empty the function returns (nil, nil) so the
// nil-Contributions short-circuit path on the consumer side stays simple.
func Collect(extensions []Extension) (*Contributions, error) {
	if len(extensions) == 0 {
		return nil, nil
	}
	contrib := &Contributions{
		agentCallbacks: agent.NewCallbacks(),
		modelCallbacks: model.NewCallbacks(),
		toolCallbacks:  tool.NewCallbacks(),
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

		r := newRegistry(name, contrib.agentCallbacks, contrib.modelCallbacks, contrib.toolCallbacks)
		if err := safeRegister(e, r, name, i); err != nil {
			return nil, err
		}
		contrib.tools = append(contrib.tools, r.tools...)
	}
	return contrib, nil
}

func safeRegister(
	e Extension,
	r *Registry,
	name string,
	index int,
) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf(
				"extension: panic during register %q at index %d: %v\n%s",
				name,
				index,
				recovered,
				string(debug.Stack()),
			)
		}
	}()
	e.Register(r)
	return nil
}

// AgentCallbacks returns an independent copy of the contributed
// agent callback chain, or nil when no extension contributed agent
// callbacks.
func (c *Contributions) AgentCallbacks() *agent.Callbacks {
	if c == nil || !hasAgentContent(c.agentCallbacks) {
		return nil
	}
	return c.agentCallbacks.Clone()
}

// ModelCallbacks returns an independent copy of the contributed
// model callback chain, or nil when no extension contributed model
// callbacks.
func (c *Contributions) ModelCallbacks() *model.Callbacks {
	if c == nil || !hasModelContent(c.modelCallbacks) {
		return nil
	}
	return c.modelCallbacks.Clone()
}

// ToolCallbacks returns an independent copy of the contributed tool
// callback chain, or nil when no extension contributed tool
// callbacks.
func (c *Contributions) ToolCallbacks() *tool.Callbacks {
	if c == nil || !hasToolContent(c.toolCallbacks) {
		return nil
	}
	return c.toolCallbacks.Clone()
}

// Tools returns a shallow copy of the contributed tools in install
// order. Tool values themselves are not cloned; consuming agents
// should treat tool.Tool implementations as immutable after
// construction.
func (c *Contributions) Tools() []tool.Tool {
	if c == nil || len(c.tools) == 0 {
		return nil
	}
	return append([]tool.Tool(nil), c.tools...)
}

// IsEmpty reports whether c carries no contributions. Convenience
// helper for consumers that want to skip the merge pipeline
// entirely when no extension actually populated anything.
func (c *Contributions) IsEmpty() bool {
	if c == nil {
		return true
	}
	if len(c.tools) > 0 {
		return false
	}
	if hasAgentContent(c.agentCallbacks) {
		return false
	}
	if hasModelContent(c.modelCallbacks) {
		return false
	}
	if hasToolContent(c.toolCallbacks) {
		return false
	}
	return true
}

// hasAgentContent / hasModelContent / hasToolContent are tiny
// content predicates. They live next to Contributions because consumers
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
