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

// GuardedStreamableTool wraps a single tool.StreamableTool so that every
// StreamableCall is intercepted by the supplied Guard.
//
// Streamable-only tools (for example tool/skill.ExecTool) are valid
// tools in this repository; wrapping them preserves the streaming
// execution path while applying the same safety policy as callable
// tools.
type GuardedStreamableTool struct {
	inner     tool.StreamableTool
	guard     *Guard
	extractor func(args []byte) ScanInput
}

// GuardedCombinedTool wraps a tool that implements both tool.CallableTool
// and tool.StreamableTool. It preserves both execution paths and gates
// each of them with the supplied Guard, so the framework can choose
// either Call or StreamableCall without bypassing the safety boundary.
type GuardedCombinedTool struct {
	inner     tool.CallableTool
	streamer  tool.StreamableTool
	guard     *Guard
	extractor func(args []byte) ScanInput
}

// GuardToolOption configures a guarded tool wrapper.
type GuardToolOption func(*guardConfig)

type guardConfig struct {
	extractor func(args []byte) ScanInput
}

// WithGuardedExtractor overrides the argument extractor used to build
// the ScanInput fed into the guard. Useful for tools whose arguments
// are not plain "command" JSON.
func WithGuardedExtractor(fn func(args []byte) ScanInput) GuardToolOption {
	return func(c *guardConfig) { c.extractor = fn }
}

func applyGuardOptions(opts []GuardToolOption) guardConfig {
	c := guardConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&c)
		}
	}
	return c
}

// runGuardCheck executes the guard against the supplied arguments and
// returns the resulting permission decision. It is shared by all
// wrapper variants so that Call and StreamableCall behave identically.
func runGuardCheck(
	ctx context.Context,
	g *Guard,
	extractor func(args []byte) ScanInput,
	args []byte,
	decName string,
	declaration *tool.Declaration,
) (tool.PermissionDecision, error) {
	if g == nil {
		return tool.AllowPermission(), nil
	}

	var decision tool.PermissionDecision
	var err error
	if extractor != nil {
		// Use the tool-level extractor to pull ScanInput from
		// non-standard argument shapes, then scan via the guard's
		// scanner directly.
		input := extractor(args)
		res := g.scanner.Scan(input)
		switch res.Decision {
		case DecisionAllow:
			decision = tool.AllowPermission()
		case DecisionDeny:
			decision = tool.DenyPermission(res.Reason)
		case DecisionAsk:
			decision = tool.AskPermission(res.Reason)
		default:
			// Decision is an exported string type and Rule is a public
			// extension point; unknown values must be denied so the
			// safety boundary never fails open.
			decision = tool.DenyPermission(fmt.Sprintf("unknown safety decision %q", res.Decision))
		}
	} else {
		decision, err = g.CheckToolPermission(ctx, &tool.PermissionRequest{
			ToolName:    decName,
			Arguments:   args,
			ToolCallID:  "",
			Declaration: declaration,
		})
		if err != nil {
			return tool.PermissionDecision{}, fmt.Errorf("guard check: %w", err)
		}
	}
	return decision, nil
}

// WrapTool returns a copy of inner whose Call method is gated by guard.
//
// If guard is nil the original tool is returned unchanged, so a caller
// can pass an optional guard without an extra nil-check at the call
// site. The returned tool preserves the inner tool's Declaration and
// ToolName.
//
// If inner also implements tool.StreamableTool, WrapTool returns a
// GuardedCombinedTool so that both Call and StreamableCall are gated.
func WrapTool(inner tool.CallableTool, guard *Guard, opts ...GuardToolOption) tool.CallableTool {
	if inner == nil {
		return nil
	}
	if guard == nil {
		return inner
	}
	c := applyGuardOptions(opts)
	if streamer, ok := inner.(tool.StreamableTool); ok {
		return &GuardedCombinedTool{
			inner:     inner,
			streamer:  streamer,
			guard:     guard,
			extractor: c.extractor,
		}
	}
	return &GuardedTool{
		inner:     inner,
		guard:     guard,
		extractor: c.extractor,
	}
}

// WrapTools applies the appropriate wrapper to every entry in tools. A
// nil guard or an empty slice returns the original slice unchanged.
//
// It recognizes three categories:
//   - callable-only tools are wrapped as GuardedTool;
//   - streamable-only tools are wrapped as GuardedStreamableTool;
//   - tools implementing both interfaces are wrapped as GuardedCombinedTool.
//
// Declaration-only tools are passed through unchanged so WrapTools can
// be used on mixed slices.
func WrapTools(tools []tool.Tool, guard *Guard, opts ...GuardToolOption) []tool.Tool {
	if guard == nil || len(tools) == 0 {
		return tools
	}
	c := applyGuardOptions(opts)
	out := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		ct, callable := t.(tool.CallableTool)
		st, streamable := t.(tool.StreamableTool)
		switch {
		case callable && streamable:
			out = append(out, &GuardedCombinedTool{
				inner:     ct,
				streamer:  st,
				guard:     guard,
				extractor: c.extractor,
			})
		case callable:
			out = append(out, &GuardedTool{
				inner:     ct,
				guard:     guard,
				extractor: c.extractor,
			})
		case streamable:
			out = append(out, &GuardedStreamableTool{
				inner:     st,
				guard:     guard,
				extractor: c.extractor,
			})
		default:
			// Non-callable, non-streamable tools (declaration-only)
			// are passed through unchanged.
			out = append(out, t)
		}
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

// toolName returns the wrapped tool's name, if available.
func toolName(t tool.Tool) string {
	if d := t.Declaration(); d != nil {
		return d.Name
	}
	return ""
}

// --- GuardedTool (callable-only) ---

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
	decision, err := runGuardCheck(ctx, g.guard, g.extractor, args, toolName(g.inner), g.inner.Declaration())
	if err != nil {
		return nil, err
	}
	if decision.Action != tool.PermissionActionAllow {
		return tool.PermissionResultFor(toolName(g.inner), decision), nil
	}
	return g.inner.Call(ctx, args)
}

// CheckPermission forwards to the inner tool if it implements
// tool.PermissionChecker, preserving the inner tool's non-negotiable
// permission contract.
func (g *GuardedTool) CheckPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	if g == nil || g.inner == nil {
		return tool.PermissionDecision{}, fmt.Errorf("guarded tool: not configured")
	}
	if checker, ok := g.inner.(tool.PermissionChecker); ok {
		return checker.CheckPermission(ctx, req)
	}
	return tool.AllowPermission(), nil
}

// ToolMetadata forwards to the inner tool if it publishes metadata.
func (g *GuardedTool) ToolMetadata() tool.ToolMetadata {
	if g == nil || g.inner == nil {
		return tool.ToolMetadata{}
	}
	return tool.MetadataOf(g.inner)
}

// --- GuardedStreamableTool (streamable-only) ---

// Declaration returns the wrapped inner tool's declaration.
func (g *GuardedStreamableTool) Declaration() *tool.Declaration {
	if g == nil || g.inner == nil {
		return nil
	}
	return g.inner.Declaration()
}

// StreamableCall runs the guard check first and only delegates to the
// inner tool when the decision is allow.
func (g *GuardedStreamableTool) StreamableCall(ctx context.Context, args []byte) (*tool.StreamReader, error) {
	if g == nil || g.inner == nil {
		return nil, fmt.Errorf("guarded streamable tool: not configured")
	}
	decision, err := runGuardCheck(ctx, g.guard, g.extractor, args, toolName(g.inner), g.inner.Declaration())
	if err != nil {
		return nil, err
	}
	if decision.Action != tool.PermissionActionAllow {
		return nil, fmt.Errorf("guarded streamable tool: %s", decision.Reason)
	}
	return g.inner.StreamableCall(ctx, args)
}

// CheckPermission forwards to the inner tool if it implements
// tool.PermissionChecker.
func (g *GuardedStreamableTool) CheckPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	if g == nil || g.inner == nil {
		return tool.PermissionDecision{}, fmt.Errorf("guarded streamable tool: not configured")
	}
	if checker, ok := g.inner.(tool.PermissionChecker); ok {
		return checker.CheckPermission(ctx, req)
	}
	return tool.AllowPermission(), nil
}

// ToolMetadata forwards to the inner tool if it publishes metadata.
func (g *GuardedStreamableTool) ToolMetadata() tool.ToolMetadata {
	if g == nil || g.inner == nil {
		return tool.ToolMetadata{}
	}
	return tool.MetadataOf(g.inner)
}

// --- GuardedCombinedTool (callable + streamable) ---

// Declaration returns the wrapped inner tool's declaration.
func (g *GuardedCombinedTool) Declaration() *tool.Declaration {
	if g == nil || g.inner == nil {
		return nil
	}
	return g.inner.Declaration()
}

// Call runs the guard check first and only delegates to the inner tool
// when the decision is allow.
func (g *GuardedCombinedTool) Call(ctx context.Context, args []byte) (any, error) {
	if g == nil || g.inner == nil {
		return nil, fmt.Errorf("guarded combined tool: not configured")
	}
	decision, err := runGuardCheck(ctx, g.guard, g.extractor, args, toolName(g.inner), g.inner.Declaration())
	if err != nil {
		return nil, err
	}
	if decision.Action != tool.PermissionActionAllow {
		return tool.PermissionResultFor(toolName(g.inner), decision), nil
	}
	return g.inner.Call(ctx, args)
}

// StreamableCall runs the guard check first and only delegates to the
// inner tool when the decision is allow.
func (g *GuardedCombinedTool) StreamableCall(ctx context.Context, args []byte) (*tool.StreamReader, error) {
	if g == nil || g.streamer == nil {
		return nil, fmt.Errorf("guarded combined tool: not configured")
	}
	decision, err := runGuardCheck(ctx, g.guard, g.extractor, args, toolName(g.inner), g.inner.Declaration())
	if err != nil {
		return nil, err
	}
	if decision.Action != tool.PermissionActionAllow {
		return nil, fmt.Errorf("guarded combined tool: %s", decision.Reason)
	}
	return g.streamer.StreamableCall(ctx, args)
}

// CheckPermission forwards to the inner tool if it implements
// tool.PermissionChecker.
func (g *GuardedCombinedTool) CheckPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	if g == nil || g.inner == nil {
		return tool.PermissionDecision{}, fmt.Errorf("guarded combined tool: not configured")
	}
	if checker, ok := g.inner.(tool.PermissionChecker); ok {
		return checker.CheckPermission(ctx, req)
	}
	return tool.AllowPermission(), nil
}

// ToolMetadata forwards to the inner tool if it publishes metadata.
func (g *GuardedCombinedTool) ToolMetadata() tool.ToolMetadata {
	if g == nil || g.inner == nil {
		return tool.ToolMetadata{}
	}
	return tool.MetadataOf(g.inner)
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
var _ tool.CallableTool = (*GuardedTool)(nil)
var _ tool.Tool = (*GuardedStreamableTool)(nil)
var _ tool.StreamableTool = (*GuardedStreamableTool)(nil)
var _ tool.Tool = (*GuardedCombinedTool)(nil)
var _ tool.CallableTool = (*GuardedCombinedTool)(nil)
var _ tool.StreamableTool = (*GuardedCombinedTool)(nil)
