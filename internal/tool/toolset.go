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
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// NamedToolSet wraps a ToolSet to qualify tool names with the ToolSet name by
// default. A ToolSet may opt into exposing its original tool names.
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

// Tools returns tools with model-facing names according to the ToolSet's name
// mode. The ToolSet name is retained separately for runtime policy checks.
func (s *NamedToolSet) Tools(ctx context.Context) []tool.Tool {
	tools := s.toolSet.Tools(ctx)

	toolSetName := s.toolSet.Name()
	if toolSetName == "" {
		return tools
	}
	nameMode := toolSetNameMode(s.toolSet)

	// Create tools with model-facing names while retaining the source ToolSet
	// name for runtime policy and tracing checks.
	namedTools := make([]tool.Tool, 0, len(tools))
	for _, t := range tools {
		namedTool := &NamedTool{
			original:    t,
			toolSetName: toolSetName,
		}
		if nameMode == tool.ToolNameModeQualified {
			namedTool.name = toolSetName
		}
		namedTools = append(namedTools, namedTool)
	}

	return namedTools
}

func toolSetNameMode(toolSet tool.ToolSet) tool.ToolNameMode {
	return tool.ToolNameModeOf(toolSet)
}

// Close implements the ToolSet interface.
func (s *NamedToolSet) Close() error {
	return s.toolSet.Close()
}

// Name implements the ToolSet interface.
func (s *NamedToolSet) Name() string {
	return s.toolSet.Name()
}

// ToolNameMode reports the model-facing naming mode of the wrapped ToolSet.
func (s *NamedToolSet) ToolNameMode() tool.ToolNameMode {
	return toolSetNameMode(s.toolSet)
}

// NamedTool wraps an original tool with a model-facing name and retains the
// source ToolSet identity for runtime policy checks.
type NamedTool struct {
	original    tool.Tool
	name        string
	toolSetName string
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

// ResolveDeclaration unwraps framework declaration overlays.
func ResolveDeclaration(t tool.Tool) tool.Tool {
	switch current := t.(type) {
	case nil:
		return nil
	case declarationWrapper:
		return ResolveDeclaration(current.originalTool())
	default:
		return t
	}
}

// ResolveSemantic unwraps framework wrappers for semantic capability checks.
func ResolveSemantic(t tool.Tool) tool.Tool {
	switch current := t.(type) {
	case nil:
		return nil
	case declarationWrapper:
		return ResolveSemantic(current.originalTool())
	case *NamedTool:
		return ResolveSemantic(current.Original())
	default:
		return t
	}
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

// Declaration returns the tool declaration with the configured model-facing
// name.
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

// ToolMetadata delegates to the original tool.
func (t *NamedTool) ToolMetadata() tool.ToolMetadata {
	return tool.MetadataOf(t.original)
}

// IsConcurrencySafe delegates to the original tool.
func (t *NamedTool) IsConcurrencySafe() bool {
	return tool.MetadataOf(t.original).ConcurrencySafe
}

// ShouldDefer delegates to the original tool.
func (t *NamedTool) ShouldDefer(ctx context.Context) bool {
	return tool.ShouldDefer(ctx, t.original)
}

// CheckPermission delegates to the original tool when it implements
// tool.PermissionChecker. Wrappers keep the model-visible declaration and name
// in the request.
func (t *NamedTool) CheckPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	checker, ok := t.original.(tool.PermissionChecker)
	if !ok {
		return tool.AllowPermission(), nil
	}
	return checker.CheckPermission(ctx, req)
}

// ToolSetName returns the source ToolSet name for runtime policy checks.
func (t *NamedTool) ToolSetName() string {
	return t.toolSetName
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

// SkipSummarization delegates to the original tool when it implements
// a SkipSummarization() bool preference; otherwise returns false.
func (t *NamedTool) SkipSummarization() bool {
	type skipper interface{ SkipSummarization() bool }
	if s, ok := t.original.(skipper); ok {
		return s.SkipSummarization()
	}
	return false
}
