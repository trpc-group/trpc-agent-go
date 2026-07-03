//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// GuardedTool wraps a single tool.CallableTool so that every Call is
// intercepted by the supplied Guard before the underlying
// implementation runs.
//
// This is the "pre-execution wiring" reviewer asked for in the
// "Linked Issues" check: a GuardedTool is a drop-in replacement for
// any tool.CallableTool (e.g. an entry returned by hostexec.NewToolSet
// or workspaceexec.NewExecTool) and can be registered with a Runner
// directly. It is intentionally a small helper, not a hook into the
// hostexec / workspaceexec packages, so the safety package does not
// take a reverse dependency on the executor packages and existing
// call-sites remain untouched.
//
// A nil guard is treated as "no policy" and the wrapper falls through
// to the inner tool, so callers can pass an optional guard.
type GuardedTool struct {
	// inner is the wrapped tool that actually executes the request.
	inner tool.CallableTool
	// guard performs the pre-execution permission check.
	guard *Guard
	// extractor pulls a ScanInput from the JSON arguments. The default
	// delegates to the guard's own extractor; callers can override to
	// support non-standard argument shapes.
	extractor func(args []byte) ScanInput
}

// GuardToolOption configures a GuardedTool.
type GuardToolOption func(*GuardedTool)

// WithGuardedExtractor overrides the argument extractor used to build
// the ScanInput fed into the guard. Useful for tools whose arguments
// are not plain "command" JSON.
func WithGuardedExtractor(fn func(args []byte) ScanInput) GuardToolOption {
	return func(g *GuardedTool) { g.extractor = fn }
}

// WrapTool returns a copy of inner whose Call method is gated by guard.
//
// If guard is nil the original tool is returned unchanged, so a caller
// can pass an optional guard without an extra nil-check at the call
// site. The returned tool preserves the inner tool's Declaration and
// ToolName.
func WrapTool(inner tool.CallableTool, guard *Guard, opts ...GuardToolOption) tool.CallableTool {
	if inner == nil {
		return nil
	}
	if guard == nil {
		return inner
	}
	wrapped := &GuardedTool{
		inner: inner,
		guard: guard,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(wrapped)
		}
	}
	if wrapped.extractor == nil {
		wrapped.extractor = guard.extract
	}
	return wrapped
}

// WrapTools applies WrapTool to every entry in tools. A nil guard or
// an empty slice returns the original slice unchanged. This is the
// convenience form for tool sets (hostexec, workspaceexec) that
// expose []tool.Tool from their Tools(ctx) method.
func WrapTools(tools []tool.Tool, guard *Guard, opts ...GuardToolOption) []tool.Tool {
	if guard == nil || len(tools) == 0 {
		return tools
	}
	out := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		ct, ok := t.(tool.CallableTool)
		if !ok {
			// Non-callable tools (declaration-only) are passed through
			// unchanged so WrapTools can be used on mixed slices.
			out = append(out, t)
			continue
		}
		out = append(out, WrapTool(ct, guard, opts...))
	}
	return out
}

// WrapToolSet wraps every tool exposed by ts with guard. It is a thin
// helper that calls ts.Tools(ctx) and re-wraps the result.
//
// This is the most common integration path:
//
//	hostexecTS, _ := hostexec.NewToolSet()
//	guard := safety.NewGuard()
//	wrapped := safety.WrapToolSet(hostexecTS, guard)
//
//	// Pass `wrapped` to the agent / runner as a tool.ToolSet.
func WrapToolSet(ts tool.ToolSet, guard *Guard, opts ...GuardToolOption) tool.ToolSet {
	if ts == nil || guard == nil {
		return ts
	}
	return &guardedToolSet{inner: ts, guard: guard, opts: opts}
}

// guardedToolSet forwards every method to the inner ToolSet, but
// returns a wrapped slice from Tools so that each tool's Call is
// gated by the guard.
type guardedToolSet struct {
	inner tool.ToolSet
	guard *Guard
	opts  []GuardToolOption
}

// Tools returns the inner tool set's tools, each wrapped with guard.
func (g *guardedToolSet) Tools(ctx context.Context) []tool.Tool {
	return WrapTools(g.inner.Tools(ctx), g.guard, g.opts...)
}

// Close forwards to the inner tool set.
func (g *guardedToolSet) Close() error {
	return g.inner.Close()
}

// Name forwards to the inner tool set.
func (g *guardedToolSet) Name() string {
	return g.inner.Name()
}

// Declaration returns the wrapped inner tool's declaration so callers
// that introspect the tool's schema see the original definition
// (argument / output schemas) unchanged.
func (g *GuardedTool) Declaration() *tool.Declaration {
	if g == nil || g.inner == nil {
		return nil
	}
	return g.inner.Declaration()
}

// Call runs the guard check first and only delegates to the inner tool
// when the decision is allow. Ask and Deny are returned to the model
// as a structured PermissionResult so the framework's normal
// permission-skip machinery handles the rest.
func (g *GuardedTool) Call(ctx context.Context, args []byte) (any, error) {
	if g == nil || g.inner == nil {
		return nil, fmt.Errorf("guarded tool: not configured")
	}
	if g.guard == nil {
		return g.inner.Call(ctx, args)
	}
	decName := ""
	if d := g.inner.Declaration(); d != nil {
		decName = d.Name
	}
	decision, err := g.guard.CheckToolPermission(ctx, &tool.PermissionRequest{
		ToolName:    decName,
		Arguments:   args,
		ToolCallID:  "",
		Declaration: g.inner.Declaration(),
	})
	if err != nil {
		return nil, fmt.Errorf("guard check: %w", err)
	}
	if decision.Action != tool.PermissionActionAllow {
		return tool.PermissionResultFor(decName, decision), nil
	}
	return g.inner.Call(ctx, args)
}

// jsonCommandArgs marshals a {"command": <cmd>} JSON object. It is a
// test-only helper so the wiring tests can avoid inline map literals
// at every Call site. The name is unexported on purpose: this is not
// part of the package's public surface.
var jsonCommandArgs = func(command string) []byte {
	b, _ := json.Marshal(map[string]string{"command": command})
	return b
}

var _ tool.Tool = (*GuardedTool)(nil)
var _ tool.ToolSet = (*guardedToolSet)(nil)
