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
//
// There are exactly two supported ways to bind a codec to a tool:
//
//   - function.WithResultCodec(codec) when constructing a function tool; and
//   - resultcodec.Wrap(tool, codec) for tools whose construction cannot be
//     modified (for example tools produced by a ToolSet).
//
// The framework discovers the codec through an internal ResultCodec() method on
// these wrappers. That method is a discovery detail, not a third configuration
// path; do not rely on implementing it on your own tool types.
package resultcodec

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// recoverCodecPanic converts a panic during encoding into an error tagged with
// the codec name. Built-in codecs ultimately call business code (MarshalJSON via
// encoding/json, MarshalText) that may panic; recovering keeps the documented
// "built-in codecs return an error instead of panicking" contract for direct
// Encode callers, not only the framework's flow path.
func recoverCodecPanic(name string, errp *error) {
	if r := recover(); r != nil {
		*errp = fmt.Errorf("resultcodec: %s encode panic: %v", name, r)
	}
}

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

// ResultCodec returns the bound codec so the framework can discover it. It is an
// internal discovery hook, not a supported configuration entry point.
func (t *codecTool) ResultCodec() Codec {
	return t.codec
}

// TransparentUnwrap returns the wrapped tool. It declares that this wrapper is
// transparent: it keeps the wrapped tool's model-facing declaration and
// delegates capabilities, so framework capability/permission resolution may
// traverse it. This is a deliberate, explicit contract rather than a generic
// errors.Unwrap, so a renaming wrapper is not silently treated as transparent.
func (t *codecTool) TransparentUnwrap() tool.Tool {
	return t.base
}

// maxWrapDepth bounds wrapper-chain traversal so a self-referential or mutually
// cyclic Unwrap() implementation cannot cause an infinite loop.
const maxWrapDepth = 128

// walkStatus reports how a wrapper-chain traversal terminated.
type walkStatus int

const (
	// walkEnded means the chain was fully traversed without fn matching.
	walkEnded walkStatus = iota
	// walkFound means fn matched a tool in the chain.
	walkFound
	// walkExhausted means the depth bound was hit before the chain ended
	// (an overly deep or cyclic chain). Security decisions must fail closed.
	walkExhausted
)

// walkBase visits the wrapped tool and each tool reachable through the explicit
// transparency contract (TransparentUnwrap), starting at t.base, calling fn
// until it returns true or the chain ends. The traversal is depth-bounded for
// cycle safety and reports how it terminated so callers (permission in
// particular) can fail closed on walkExhausted. It resolves delegated
// capabilities through the full transparent-wrapper chain so an intermediate
// transparent wrapper cannot hide a deeper capability (for example a
// PermissionChecker).
func (t *codecTool) walkBase(fn func(tool.Tool) bool) walkStatus {
	cur := t.base
	for i := 0; i < maxWrapDepth; i++ {
		if cur == nil {
			return walkEnded
		}
		if fn(cur) {
			return walkFound
		}
		u, ok := cur.(interface{ TransparentUnwrap() tool.Tool })
		if !ok {
			return walkEnded
		}
		cur = u.TransparentUnwrap()
	}
	return walkExhausted
}

// ToolMetadata resolves metadata through the full wrapper chain so it is
// preserved for callers that inspect it directly (tool.MetadataOf does not
// unwrap). Only a full MetadataProvider terminates the traversal; the nearest
// ConcurrencyAware value is tracked separately and overlaid, so an outer wrapper
// that only publishes concurrency safety cannot discard a deeper provider's
// Destructive/OpenWorld/MaxResultSize metadata.
func (t *codecTool) ToolMetadata() tool.ToolMetadata {
	var (
		meta            tool.ToolMetadata
		concurrency     bool
		haveConcurrency bool
	)
	t.walkBase(func(cur tool.Tool) bool {
		if !haveConcurrency {
			if aware, ok := cur.(tool.ConcurrencyAware); ok {
				concurrency = aware.IsConcurrencySafe()
				haveConcurrency = true
			}
		}
		if provider, ok := cur.(tool.MetadataProvider); ok {
			meta = provider.ToolMetadata()
			return true
		}
		return false
	})
	if haveConcurrency {
		meta.ConcurrencySafe = concurrency
	}
	return meta
}

// IsConcurrencySafe reports the wrapped chain's concurrency safety.
func (t *codecTool) IsConcurrencySafe() bool {
	return t.ToolMetadata().ConcurrencySafe
}

// ShouldDefer resolves deferred-loading preference through the full wrapper
// chain (tool.ShouldDefer does not unwrap).
func (t *codecTool) ShouldDefer(ctx context.Context) bool {
	deferred := false
	t.walkBase(func(cur tool.Tool) bool {
		if d, ok := cur.(tool.DeferredTool); ok {
			deferred = d.ShouldDefer(ctx)
			return true
		}
		return false
	})
	return deferred
}

// CheckPermission resolves the permission decision through the full wrapper
// chain so an intermediate unwrap-only wrapper cannot bypass a deeper
// PermissionChecker. When no checker is found the decision defaults to allow.
// If the chain cannot be fully traversed (overly deep or cyclic), it fails
// closed by denying rather than allowing, since a deny may be hidden past the
// depth bound.
func (t *codecTool) CheckPermission(
	ctx context.Context,
	req *tool.PermissionRequest,
) (tool.PermissionDecision, error) {
	decision := tool.AllowPermission()
	var checkErr error
	status := t.walkBase(func(cur tool.Tool) bool {
		if c, ok := cur.(tool.PermissionChecker); ok {
			decision, checkErr = c.CheckPermission(ctx, req)
			return true
		}
		return false
	})
	if status == walkExhausted {
		return tool.DenyPermission(
			fmt.Sprintf(
				"resultcodec: permission checker traversal exhausted after %d wrappers "+
					"(overly deep or cyclic chain)", maxWrapDepth,
			),
		), nil
	}
	return decision, checkErr
}

// SkipSummarization resolves the preference through the full wrapper chain;
// otherwise it reports false.
func (t *codecTool) SkipSummarization() bool {
	skip := false
	t.walkBase(func(cur tool.Tool) bool {
		if s, ok := cur.(interface{ SkipSummarization() bool }); ok {
			skip = s.SkipSummarization()
			return true
		}
		return false
	})
	return skip
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
