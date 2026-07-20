//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package tool

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// ErrToolWrapperTraversalExhausted indicates the wrapper chain could not be
// fully traversed within the depth bound (an overly deep or cyclic chain).
// Security decisions must fail closed when they see this error rather than
// treating "not found" as "allow".
var ErrToolWrapperTraversalExhausted = errors.New("tool wrapper traversal exhausted")

// NamedToolSet wraps a ToolSet to automatically prefix tool names with the toolset name.
// This prevents tool name conflicts when multiple toolsets provide tools with the same name.
type NamedToolSet struct {
	toolSet tool.ToolSet
}

// NewNamedToolSet creates a new named toolset wrapper.
// If the toolSet is already a NamedToolSet, it returns itself to avoid double-wrapping.
func NewNamedToolSet(toolSet tool.ToolSet) *NamedToolSet {
	if t, ok := toolSet.(*NamedToolSet); ok {
		return t
	}
	return &NamedToolSet{
		toolSet: toolSet,
	}
}

// Tools returns tools with names prefixed by the toolset name to avoid conflicts.
func (s *NamedToolSet) Tools(ctx context.Context) []tool.Tool {
	tools := s.toolSet.Tools(ctx)

	toolSetName := s.toolSet.Name()
	if toolSetName == "" {
		return tools
	}

	// Create tools with prefixed names to avoid conflicts
	prefixedTools := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		prefixedTool := &NamedTool{
			original: t,
			name:     toolSetName,
		}
		prefixedTools = append(prefixedTools, prefixedTool)
	}

	return prefixedTools
}

// Close implements the ToolSet interface.
func (s *NamedToolSet) Close() error {
	return s.toolSet.Close()
}

// Name implements the ToolSet interface.
func (s *NamedToolSet) Name() string {
	return s.toolSet.Name()
}

// NamedTool wraps an original tool with a prefixed name to avoid conflicts.
type NamedTool struct {
	original tool.Tool
	name     string
}

// NewUnprefixedNamedTool wraps a tool as a NamedTool without adding any name
// prefix. This is useful for ToolSets whose tools should be recognized as user
// tools (e.g. for filtering) while keeping their original names.
func NewUnprefixedNamedTool(t tool.Tool) *NamedTool {
	return &NamedTool{original: t}
}

type declarationWrapper interface {
	originalTool() tool.Tool
}

// ApplyDeclarations overlays model-facing declarations onto matching tools.
func ApplyDeclarations(base []tool.Tool, declarations []tool.Declaration) []tool.Tool {
	if len(base) == 0 || len(declarations) == 0 {
		return base
	}
	declarationByName := make(map[string]tool.Declaration, len(declarations))
	for _, declaration := range declarations {
		if declaration.Name == "" {
			continue
		}
		declarationByName[declaration.Name] = declaration
	}
	if len(declarationByName) == 0 {
		return base
	}
	out := make([]tool.Tool, len(base))
	for i, candidate := range base {
		out[i] = candidate
		name := toolName(candidate)
		if name == "" {
			continue
		}
		declaration, ok := declarationByName[name]
		if !ok {
			continue
		}
		out[i] = wrapDeclarationTool(candidate, declaration)
	}
	return out
}

// ResolveDeclaration unwraps framework declaration overlays and explicitly
// transparent wrappers. The traversal is depth-bounded so a cyclic wrapper chain
// cannot cause unbounded recursion. A wrapper that only implements a generic
// errors.Unwrap() is not followed, so a renaming wrapper keeps its declaration.
func ResolveDeclaration(t tool.Tool) tool.Tool {
	for i := 0; i < maxToolUnwrapDepth; i++ {
		switch current := t.(type) {
		case nil:
			return nil
		case declarationWrapper:
			t = current.originalTool()
		case transparentTool:
			t = current.TransparentUnwrap()
		default:
			return t
		}
	}
	return t
}

// ResolveSemantic unwraps framework wrappers and explicitly transparent wrappers
// for semantic capability checks. The traversal is depth-bounded so a cyclic
// wrapper chain cannot cause unbounded recursion. A wrapper that only implements
// a generic errors.Unwrap() is not followed, so its own hooks are preserved.
func ResolveSemantic(t tool.Tool) tool.Tool {
	for i := 0; i < maxToolUnwrapDepth; i++ {
		switch current := t.(type) {
		case nil:
			return nil
		case declarationWrapper:
			t = current.originalTool()
		case *NamedTool:
			t = current.Original()
		case transparentTool:
			t = current.TransparentUnwrap()
		default:
			return t
		}
	}
	return t
}

// ResolvePermissionChecker returns the outermost tool.PermissionChecker in the
// wrapper chain. Permission must be resolved from the outside in: unwrapping past
// a transparent wrapper (for example resultcodec.Wrap or any tool that exposes
// TransparentUnwrap) to reach an inner tool would otherwise skip an intermediate
// wrapper's own permission decision and bypass it.
//
// It fails closed: a return of (nil, nil) means the chain was fully traversed
// and no checker exists (safe to allow); a non-nil error
// (ErrToolWrapperTraversalExhausted) means the chain could not be fully
// traversed within the depth bound (overly deep or cyclic), so callers must not
// treat it as "no checker" and must deny.
//
// Purely-delegating framework wrappers (declarationWrapper, NamedTool) carry no
// permission policy of their own and are unwrapped before anything is treated as
// a checker; otherwise their shallow delegating CheckPermission would
// short-circuit resolution and could hide a deeper deny.
func ResolvePermissionChecker(t tool.Tool) (tool.PermissionChecker, error) {
	for i := 0; i < maxToolUnwrapDepth; i++ {
		if t == nil {
			return nil, nil
		}
		switch current := t.(type) {
		case declarationWrapper:
			t = current.originalTool()
			continue
		case *NamedTool:
			t = current.Original()
			continue
		}
		// A tool with its own PermissionChecker is authoritative at this layer.
		if checker, ok := t.(tool.PermissionChecker); ok {
			return checker, nil
		}
		// A transparent wrapper without its own checker: keep unwrapping.
		if u, ok := t.(transparentTool); ok {
			t = u.TransparentUnwrap()
			continue
		}
		return nil, nil
	}
	return nil, ErrToolWrapperTraversalExhausted
}

// walkToolCapabilities visits the tools in t's wrapper chain from outermost to
// innermost, honoring a capability declared by an intermediate transparent
// wrapper before the wrapped tool (outermost-wins). Framework structural
// wrappers (declaration overlays and NamedTool) are unwrapped without being
// treated as capability sources, since they only delegate. visit is called until
// it returns true or the chain ends; the return reports whether traversal hit the
// depth bound (a cyclic or over-deep chain) without terminating, so callers can
// fail closed. This mirrors resultcodec.codecTool's walkBase so capability
// resolution is consistent whether a tool is wrapped by NamedTool or the codec.
func walkToolCapabilities(t tool.Tool, visit func(tool.Tool) bool) (exhausted bool) {
	for i := 0; i < maxToolUnwrapDepth && t != nil; i++ {
		switch cur := t.(type) {
		case declarationWrapper:
			t = cur.originalTool()
			continue
		case *NamedTool:
			t = cur.Original()
			continue
		}
		if visit(t) {
			return false
		}
		tw, ok := t.(transparentTool)
		if !ok {
			return false
		}
		t = tw.TransparentUnwrap()
	}
	return true
}

// ResolveMetadata resolves a tool's effective metadata across framework and
// transparent wrappers, outermost-first: the first MetadataProvider terminates
// the walk, while the nearest ConcurrencyAware value is overlaid. This honors an
// outer wrapper's own capability declaration (for example a wrapper that marks
// the composite as not concurrency-safe, or as destructive) instead of skipping
// straight to the innermost tool. If the chain cannot be fully traversed (overly
// deep or cyclic) and no provider was found, it fails closed with conservative
// Destructive/OpenWorld flags rather than reporting a benign zero value.
func ResolveMetadata(t tool.Tool) tool.ToolMetadata {
	var (
		meta            tool.ToolMetadata
		concurrency     bool
		haveConcurrency bool
		foundProvider   bool
	)
	exhausted := walkToolCapabilities(t, func(cur tool.Tool) bool {
		if !haveConcurrency {
			if aware, ok := cur.(tool.ConcurrencyAware); ok {
				concurrency = aware.IsConcurrencySafe()
				haveConcurrency = true
			}
		}
		if provider, ok := cur.(tool.MetadataProvider); ok {
			meta = provider.ToolMetadata()
			foundProvider = true
			return true
		}
		return false
	})
	if exhausted && !foundProvider {
		meta.Destructive = true
		meta.OpenWorld = true
	}
	if haveConcurrency {
		meta.ConcurrencySafe = concurrency
	}
	return meta
}

type declarationTool struct {
	decl tool.Declaration
	base tool.Tool
}

func (t *declarationTool) Declaration() *tool.Declaration {
	return &t.decl
}

func (t *declarationTool) originalTool() tool.Tool {
	return t.base
}

type callableDeclarationTool struct {
	*declarationTool
	callable tool.CallableTool
}

func (t *callableDeclarationTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return t.callable.Call(ctx, jsonArgs)
}

type streamableDeclarationTool struct {
	*declarationTool
	streamable tool.StreamableTool
}

func (t *streamableDeclarationTool) StreamableCall(
	ctx context.Context,
	jsonArgs []byte,
) (*tool.StreamReader, error) {
	return t.streamable.StreamableCall(ctx, jsonArgs)
}

type callableStreamableDeclarationTool struct {
	*declarationTool
	callable   tool.CallableTool
	streamable tool.StreamableTool
}

func (t *callableStreamableDeclarationTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	return t.callable.Call(ctx, jsonArgs)
}

func (t *callableStreamableDeclarationTool) StreamableCall(
	ctx context.Context,
	jsonArgs []byte,
) (*tool.StreamReader, error) {
	return t.streamable.StreamableCall(ctx, jsonArgs)
}

func wrapDeclarationTool(base tool.Tool, declaration tool.Declaration) tool.Tool {
	wrapped := &declarationTool{
		decl: declaration,
		base: base,
	}
	callable, hasCallable := base.(tool.CallableTool)
	streamable, hasStreamable := base.(tool.StreamableTool)
	hasStreamable = hasStreamable && isReallyStreamable(base)
	switch {
	case hasCallable && hasStreamable:
		return &callableStreamableDeclarationTool{
			declarationTool: wrapped,
			callable:        callable,
			streamable:      streamable,
		}
	case hasCallable:
		return &callableDeclarationTool{
			declarationTool: wrapped,
			callable:        callable,
		}
	case hasStreamable:
		return &streamableDeclarationTool{
			declarationTool: wrapped,
			streamable:      streamable,
		}
	default:
		return wrapped
	}
}

func isReallyStreamable(t tool.Tool) bool {
	candidate := ResolveSemantic(t)
	if pref, ok := candidate.(interface{ StreamInner() bool }); ok && !pref.StreamInner() {
		return false
	}
	_, ok := candidate.(tool.StreamableTool)
	return ok
}

func toolName(tl tool.Tool) string {
	if tl == nil {
		return ""
	}
	decl := tl.Declaration()
	if decl == nil {
		return ""
	}
	return decl.Name
}

// Declaration returns the tool declaration with a prefixed name.
func (t *NamedTool) Declaration() *tool.Declaration {
	decl := t.original.Declaration()
	name := decl.Name
	if t.name != "" {
		name = t.name + "_" + name
	}

	return &tool.Declaration{
		Name:         name,
		Description:  decl.Description,
		InputSchema:  decl.InputSchema,
		OutputSchema: decl.OutputSchema,
	}
}

// Original returns the underlying Tool instance wrapped by the NamedTool.
func (t *NamedTool) Original() tool.Tool {
	return t.original
}

// ToolMetadata resolves metadata across the wrapper chain outermost-first, so an
// intermediate transparent wrapper's own declaration (for example a tightened
// ConcurrencySafe or a Destructive marker) is honored rather than skipped in
// favor of the innermost tool. See ResolveMetadata.
func (t *NamedTool) ToolMetadata() tool.ToolMetadata {
	return ResolveMetadata(t.original)
}

// IsConcurrencySafe resolves concurrency safety outermost-first across the
// wrapper chain.
func (t *NamedTool) IsConcurrencySafe() bool {
	return ResolveMetadata(t.original).ConcurrencySafe
}

// ShouldDefer resolves the deferred-loading preference outermost-first across the
// wrapper chain, so the first DeferredTool declaration wins.
func (t *NamedTool) ShouldDefer(ctx context.Context) bool {
	deferred := false
	walkToolCapabilities(t.original, func(cur tool.Tool) bool {
		if d, ok := cur.(tool.DeferredTool); ok {
			deferred = d.ShouldDefer(ctx)
			return true
		}
		return false
	})
	return deferred
}

// CheckPermission delegates to the original tool when it implements
// tool.PermissionChecker. Wrappers keep the model-visible declaration and name
// in the request.
func (t *NamedTool) CheckPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	// Resolve through the full wrapper chain, not just the direct original, so a
	// deny behind a transparent wrapper is not missed. Fail closed if the chain
	// cannot be fully traversed.
	checker, err := ResolvePermissionChecker(t.original)
	if err != nil {
		return tool.DenyPermission(
			"tool permission could not be resolved: " + err.Error(),
		), nil
	}
	if checker == nil {
		return tool.AllowPermission(), nil
	}
	return checker.CheckPermission(ctx, req)
}

// ToolSetName returns the source ToolSet name for runtime policy checks.
func (t *NamedTool) ToolSetName() string {
	return t.name
}

// Call delegates to the original tool's Call method.
func (t *NamedTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	if callable, ok := t.original.(tool.CallableTool); ok {
		return callable.Call(ctx, jsonArgs)
	}
	return nil, fmt.Errorf("tool is not callable")
}

// StreamableCall delegates to the original tool's StreamableCall method.
func (t *NamedTool) StreamableCall(ctx context.Context, jsonArgs []byte) (*tool.StreamReader, error) {
	if streamable, ok := t.original.(tool.StreamableTool); ok {
		return streamable.StreamableCall(ctx, jsonArgs)
	}
	return nil, fmt.Errorf("tool is not streamable")
}

// SkipSummarization resolves the preference outermost-first across the wrapper
// chain: the first tool publishing a SkipSummarization() bool preference wins;
// otherwise it returns false. Resolving per-capability (rather than fully
// unwrapping first) keeps an intermediate transparent wrapper's own preference
// from being skipped.
func (t *NamedTool) SkipSummarization() bool {
	type skipper interface{ SkipSummarization() bool }
	skip := false
	walkToolCapabilities(t.original, func(cur tool.Tool) bool {
		if s, ok := cur.(skipper); ok {
			skip = s.SkipSummarization()
			return true
		}
		return false
	})
	return skip
}
