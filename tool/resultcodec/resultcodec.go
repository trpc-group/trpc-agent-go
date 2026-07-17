//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package resultcodec provides per-tool encoders that turn a final tool result
// into the model-visible text of a tool result message.
//
// A Codec has a single responsibility: encode a final tool result into a
// model-visible string. It does not create, add, remove, or reorder model
// messages and does not change tool-call control flow. The framework remains
// responsible for building the protocol-correct tool result message, including
// the tool role, tool name, and tool call ID pairing.
//
// When no codec is configured the framework keeps its default JSON behavior,
// byte-for-byte compatible with previous versions.
package resultcodec

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Codec encodes the final result of a single tool call into model-visible text.
//
// Implementations must be deterministic for identical input and safe for
// concurrent use when a single instance is shared across tools. Built-in codecs
// return an error instead of panicking.
type Codec interface {
	// Encode converts a final tool result into the string used as the tool
	// result message content.
	Encode(ctx context.Context, result any) (string, error)
}

// Wrap binds a codec to an existing tool without changing its declaration,
// arguments, or execution behavior. It is intended for tools whose construction
// the caller cannot modify (for example tools produced by a ToolSet). The
// returned tool preserves the callable and/or streamable capabilities of t and
// exposes the codec to the framework.
//
// Wrapping is transparent: framework capability checks (long-running,
// permission, streaming preference, summarization) continue to observe the
// underlying tool. Wrap returns nil when t is nil.
func Wrap(t tool.Tool, codec Codec) tool.Tool {
	if t == nil {
		return nil
	}
	base := &codecTool{base: t, codec: codec}
	callable, hasCallable := t.(tool.CallableTool)
	streamable, hasStreamable := t.(tool.StreamableTool)
	hasStreamable = hasStreamable && isReallyStreamable(t)
	switch {
	case hasCallable && hasStreamable:
		return &callableStreamableCodecTool{
			codecTool:  base,
			callable:   callable,
			streamable: streamable,
		}
	case hasCallable:
		return &callableCodecTool{codecTool: base, callable: callable}
	case hasStreamable:
		return &streamableCodecTool{codecTool: base, streamable: streamable}
	default:
		return base
	}
}

// isReallyStreamable reports whether t should be treated as streamable,
// honoring an optional StreamInner opt-out preference. It mirrors the
// framework's streamable detection so wrapping does not change streaming
// behavior.
func isReallyStreamable(t tool.Tool) bool {
	if pref, ok := t.(interface{ StreamInner() bool }); ok && !pref.StreamInner() {
		return false
	}
	_, ok := t.(tool.StreamableTool)
	return ok
}

// codecTool carries the codec and the wrapped tool. It delegates the
// declaration to the base tool and exposes discovery and unwrap hooks used by
// the framework.
type codecTool struct {
	base  tool.Tool
	codec Codec
}

// Declaration delegates to the wrapped tool.
func (t *codecTool) Declaration() *tool.Declaration {
	return t.base.Declaration()
}

// ResultCodec returns the bound codec so the framework can discover it.
func (t *codecTool) ResultCodec() Codec {
	return t.codec
}

// Unwrap returns the wrapped tool so framework capability checks can observe
// the underlying tool. It follows the errors.Unwrap convention.
func (t *codecTool) Unwrap() tool.Tool {
	return t.base
}

type callableCodecTool struct {
	*codecTool
	callable tool.CallableTool
}

// Call delegates to the wrapped callable tool.
func (t *callableCodecTool) Call(ctx context.Context, jsonArgs []byte) (any, error) {
	return t.callable.Call(ctx, jsonArgs)
}

type streamableCodecTool struct {
	*codecTool
	streamable tool.StreamableTool
}

// StreamableCall delegates to the wrapped streamable tool.
func (t *streamableCodecTool) StreamableCall(
	ctx context.Context,
	jsonArgs []byte,
) (*tool.StreamReader, error) {
	return t.streamable.StreamableCall(ctx, jsonArgs)
}

type callableStreamableCodecTool struct {
	*codecTool
	callable   tool.CallableTool
	streamable tool.StreamableTool
}

// Call delegates to the wrapped callable tool.
func (t *callableStreamableCodecTool) Call(
	ctx context.Context,
	jsonArgs []byte,
) (any, error) {
	return t.callable.Call(ctx, jsonArgs)
}

// StreamableCall delegates to the wrapped streamable tool.
func (t *callableStreamableCodecTool) StreamableCall(
	ctx context.Context,
	jsonArgs []byte,
) (*tool.StreamReader, error) {
	return t.streamable.StreamableCall(ctx, jsonArgs)
}
