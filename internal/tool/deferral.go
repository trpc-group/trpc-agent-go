//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Deferral is an explicit decision about which tools a provider may
// load on demand. Within its scope it replaces the tools' own
// tool.DeferredTool preference.
type Deferral struct {
	// Filter selects deferred tools by their original declaration. A nil
	// Filter defers every tool in scope.
	Filter tool.FilterFunc
}

// Defers reports whether t is deferred under d.
func (d *Deferral) Defers(ctx context.Context, t tool.Tool) bool {
	return d.Filter == nil || d.Filter(ctx, t)
}

// WithDeferral exposes t with the deferral decision d. A nil d returns t.
// The wrapper keeps the declaration and execution methods of t, and
// ResolveDeclaration and ResolveSemantic unwrap it for capability checks.
func WithDeferral(t tool.Tool, d *Deferral) tool.Tool {
	if t == nil || d == nil {
		return t
	}
	wrapped := &deferralTool{base: t, deferral: d}
	callable, hasCallable := t.(tool.CallableTool)
	streamable, hasStreamable := t.(tool.StreamableTool)
	switch {
	case hasCallable && hasStreamable:
		return &callableStreamableDeferralTool{
			deferralTool: wrapped,
			callable:     callable,
			streamable:   streamable,
		}
	case hasCallable:
		return &callableDeferralTool{deferralTool: wrapped, callable: callable}
	case hasStreamable:
		return &streamableDeferralTool{deferralTool: wrapped, streamable: streamable}
	default:
		return wrapped
	}
}

// ToolSetMember reports the ToolSet identity of a request tool: the ToolSet
// name, the member's original name inside it, and the ToolSet description.
// It reports false for tools that do not come from a named ToolSet.
func ToolSetMember(t tool.Tool) (toolSetName, memberName, description string, ok bool) {
	named, isNamed := toolSetIdentity(t).(*NamedTool)
	if !isNamed || named.toolSetName == "" {
		return "", "", "", false
	}
	decl := named.original.Declaration()
	if decl == nil {
		return "", "", "", false
	}
	return named.toolSetName, decl.Name, named.toolSetDescription, true
}

// toolSetIdentity follows wrappers only to find the owning ToolSet. Callers
// must still use the outer tool's Declaration, which may augment its schema.
func toolSetIdentity(t tool.Tool) tool.Tool {
	for t != nil {
		switch wrapped := t.(type) {
		case *NamedTool:
			return wrapped
		case declarationWrapper:
			t = wrapped.originalTool()
		case interface{ Original() tool.Tool }:
			t = wrapped.Original()
		default:
			return t
		}
	}
	return nil
}

type deferralTool struct {
	base     tool.Tool
	deferral *Deferral
}

func (t *deferralTool) Declaration() *tool.Declaration {
	return t.base.Declaration()
}

func (t *deferralTool) ToolMetadata() tool.ToolMetadata {
	return tool.MetadataOf(t.base)
}

func (t *deferralTool) IsConcurrencySafe() bool {
	return tool.MetadataOf(t.base).ConcurrencySafe
}

// CheckPermission preserves tool-owned checks for callers that inspect the
// wrapper directly instead of using ResolveSemantic.
func (t *deferralTool) CheckPermission(ctx context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	if checker, ok := t.base.(tool.PermissionChecker); ok {
		return checker.CheckPermission(ctx, req)
	}
	return tool.AllowPermission(), nil
}

// SkipSummarization preserves the wrapped tool's completion preference.
func (t *deferralTool) SkipSummarization() bool {
	if pref, ok := t.base.(interface{ SkipSummarization() bool }); ok {
		return pref.SkipSummarization()
	}
	return false
}

// StreamInner keeps stream routing separate from the StreamableTool interface.
func (t *deferralTool) StreamInner() bool {
	return isReallyStreamable(t.base)
}

func (t *deferralTool) originalTool() tool.Tool {
	return t.base
}

// Original lets extensions inspect the wrapped tool's execution capabilities.
func (t *deferralTool) Original() tool.Tool {
	return t.base
}

// ShouldDefer applies the configured decision to the wrapped tool.
func (t *deferralTool) ShouldDefer(ctx context.Context) bool {
	return t.deferral.Defers(ctx, t.base)
}

type callableDeferralTool struct {
	*deferralTool
	callable tool.CallableTool
}

func (t *callableDeferralTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return t.callable.Call(ctx, jsonArgs)
}

type streamableDeferralTool struct {
	*deferralTool
	streamable tool.StreamableTool
}

func (t *streamableDeferralTool) StreamableCall(
	ctx context.Context,
	jsonArgs []byte,
) (*tool.StreamReader, error) {
	return t.streamable.StreamableCall(ctx, jsonArgs)
}

type callableStreamableDeferralTool struct {
	*deferralTool
	callable   tool.CallableTool
	streamable tool.StreamableTool
}

func (t *callableStreamableDeferralTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	return t.callable.Call(ctx, jsonArgs)
}

func (t *callableStreamableDeferralTool) StreamableCall(
	ctx context.Context,
	jsonArgs []byte,
) (*tool.StreamReader, error) {
	return t.streamable.StreamableCall(ctx, jsonArgs)
}
