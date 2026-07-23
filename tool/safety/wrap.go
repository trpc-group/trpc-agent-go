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
	"errors"
	"io"
	"reflect"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// WrapCallableTool places Guard directly in front of a CallableTool.
//
// The normal agent runner should install Guard as a tool.PermissionPolicy.
// This wrapper is for hosts that invoke CallableTool.Call directly and would
// otherwise bypass the runner permission phase. Deny and ask decisions return
// the same structured permission result used by the runner and do not call the
// wrapped tool. Allowed calls have their successful result and error text
// redacted before return. JSON-compatible concrete result types are preserved
// when possible; values that cannot be inspected or serialized safely are
// replaced with RedactedValue. A redacted error preserves errors.Is matching but deliberately
// does not expose its original cause through errors.Unwrap.
func WrapCallableTool(callable tool.CallableTool, guard *Guard) (tool.CallableTool, error) {
	if isNilCallableTool(callable) {
		return nil, errors.New("tool safety: callable tool is nil")
	}
	if guard == nil {
		return nil, errors.New("tool safety: guard is nil")
	}
	if _, err := callableDeclarationSafely(callable); err != nil {
		return nil, err
	}
	return &guardedCallableTool{callable: callable, guard: guard}, nil
}

func isNilCallableTool(callable tool.CallableTool) bool {
	if callable == nil {
		return true
	}
	value := reflect.ValueOf(callable)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map,
		reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

type guardedCallableTool struct {
	callable tool.CallableTool
	guard    *Guard
}

func (g *guardedCallableTool) Declaration() *tool.Declaration {
	declaration, _ := callableDeclarationSafely(g.callable)
	return declaration
}

func (g *guardedCallableTool) ToolMetadata() tool.ToolMetadata {
	metadata, err := callableMetadataSafely(g.callable)
	if err != nil {
		return tool.ToolMetadata{Destructive: true, OpenWorld: true}
	}
	return metadata
}

func (g *guardedCallableTool) Call(ctx context.Context, arguments []byte) (any, error) {
	declaration, err := callableDeclarationSafely(g.callable)
	if err != nil {
		return nil, err
	}
	metadata, err := callableMetadataSafely(g.callable)
	if err != nil {
		return nil, err
	}
	request := &tool.PermissionRequest{
		Tool:        g.callable,
		ToolName:    declaration.Name,
		Declaration: declaration,
		Arguments:   arguments,
		Metadata:    metadata,
	}
	decision, err := g.guard.CheckToolPermission(ctx, request)
	if err != nil {
		return nil, redactToolError(g.guard.redactor, err)
	}
	decision, err = tool.NormalizePermissionDecision(decision)
	if err != nil {
		return nil, redactToolError(g.guard.redactor, err)
	}
	if decision.Action != tool.PermissionActionAllow {
		return tool.PermissionResultFor(declaration.Name, decision), nil
	}
	result, err := callCallableSafely(g.callable, ctx, arguments)
	if err != nil {
		return nil, redactToolError(g.guard.redactor, err)
	}
	redacted, _ := redactValueSafely(g.guard.redactor, result)
	return redacted, nil
}

func callCallableSafely(
	callable tool.CallableTool,
	ctx context.Context,
	arguments []byte,
) (result any, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = errors.New("tool safety: callable tool call panicked")
		}
	}()
	return callable.Call(ctx, arguments)
}

func callableDeclarationSafely(
	callable tool.CallableTool,
) (declaration *tool.Declaration, err error) {
	defer func() {
		if recover() != nil {
			declaration = nil
			err = errors.New("tool safety: callable tool declaration panicked")
		}
	}()
	declaration = callable.Declaration()
	if declaration == nil {
		return nil, errors.New("tool safety: callable tool declaration is nil")
	}
	return declaration, nil
}

func callableMetadataSafely(
	callable tool.CallableTool,
) (metadata tool.ToolMetadata, err error) {
	defer func() {
		if recover() != nil {
			metadata = tool.ToolMetadata{}
			err = errors.New("tool safety: callable tool metadata panicked")
		}
	}()
	return tool.MetadataOf(callable), nil
}

// WrapStreamableTool places Guard directly in front of a StreamableTool.
// Deny and ask decisions return one FinalResultChunk without invoking the
// wrapped tool. Allowed calls retain pull-based backpressure and Close
// propagation while independently redacting each complete chunk and non-EOF
// receive error. Producers must not split one secret across multiple chunks.
func WrapStreamableTool(
	streamable tool.StreamableTool,
	guard *Guard,
) (tool.StreamableTool, error) {
	if isNilValue(streamable) {
		return nil, errors.New("tool safety: streamable tool is nil")
	}
	if guard == nil {
		return nil, errors.New("tool safety: guard is nil")
	}
	if _, err := streamableDeclarationSafely(streamable); err != nil {
		return nil, err
	}
	return &guardedStreamableTool{streamable: streamable, guard: guard}, nil
}

type guardedStreamableTool struct {
	streamable tool.StreamableTool
	guard      *Guard
}

func (g *guardedStreamableTool) Declaration() *tool.Declaration {
	declaration, _ := streamableDeclarationSafely(g.streamable)
	return declaration
}

func (g *guardedStreamableTool) ToolMetadata() tool.ToolMetadata {
	metadata, err := streamableMetadataSafely(g.streamable)
	if err != nil {
		return tool.ToolMetadata{Destructive: true, OpenWorld: true}
	}
	return metadata
}

func (g *guardedStreamableTool) StreamableCall(
	ctx context.Context,
	arguments []byte,
) (*tool.StreamReader, error) {
	declaration, err := streamableDeclarationSafely(g.streamable)
	if err != nil {
		return nil, err
	}
	metadata, err := streamableMetadataSafely(g.streamable)
	if err != nil {
		return nil, err
	}
	decision, err := g.guard.CheckToolPermission(ctx, &tool.PermissionRequest{
		Tool: g.streamable, ToolName: declaration.Name,
		Declaration: declaration, Arguments: arguments, Metadata: metadata,
	})
	if err != nil {
		return nil, redactToolError(g.guard.redactor, err)
	}
	decision, err = tool.NormalizePermissionDecision(decision)
	if err != nil {
		return nil, redactToolError(g.guard.redactor, err)
	}
	if decision.Action != tool.PermissionActionAllow {
		return permissionResultStream(declaration.Name, decision), nil
	}
	source, err := streamableCallSafely(g.streamable, ctx, arguments)
	if err != nil {
		return nil, redactToolError(g.guard.redactor, err)
	}
	if source == nil {
		return nil, errors.New("tool safety: streamable tool returned a nil reader")
	}
	return tool.TransformStreamReader(
		source,
		func(chunk tool.StreamChunk, err error) (tool.StreamChunk, error) {
			if err != nil {
				if err == io.EOF {
					return chunk, err
				}
				return tool.StreamChunk{}, redactToolError(g.guard.redactor, err)
			}
			chunk.Content, _ = redactValueSafely(g.guard.redactor, chunk.Content)
			return chunk, nil
		},
	)
}

func permissionResultStream(
	toolName string,
	decision tool.PermissionDecision,
) *tool.StreamReader {
	stream := tool.NewStream(1)
	stream.Writer.Send(tool.StreamChunk{Content: tool.FinalResultChunk{
		Result: tool.PermissionResultFor(toolName, decision),
	}}, nil)
	stream.Writer.Close()
	return stream.Reader
}

func streamableCallSafely(
	streamable tool.StreamableTool,
	ctx context.Context,
	arguments []byte,
) (reader *tool.StreamReader, err error) {
	defer func() {
		if recover() != nil {
			reader = nil
			err = errors.New("tool safety: streamable tool call panicked")
		}
	}()
	return streamable.StreamableCall(ctx, arguments)
}

func streamableDeclarationSafely(
	streamable tool.StreamableTool,
) (declaration *tool.Declaration, err error) {
	defer func() {
		if recover() != nil {
			declaration = nil
			err = errors.New("tool safety: streamable tool declaration panicked")
		}
	}()
	declaration = streamable.Declaration()
	if declaration == nil {
		return nil, errors.New("tool safety: streamable tool declaration is nil")
	}
	return declaration, nil
}

func streamableMetadataSafely(
	streamable tool.StreamableTool,
) (metadata tool.ToolMetadata, err error) {
	defer func() {
		if recover() != nil {
			metadata = tool.ToolMetadata{}
			err = errors.New("tool safety: streamable tool metadata panicked")
		}
	}()
	return tool.MetadataOf(streamable), nil
}

type redactedToolError struct {
	message string
	target  error
}

func (e *redactedToolError) Error() string {
	return e.message
}

func (e *redactedToolError) Is(target error) bool {
	return errors.Is(e.target, target)
}

func redactToolError(redactor Redactor, err error) error {
	if err == nil {
		return nil
	}
	if isNilRedactor(redactor) {
		redactor = NewRedactor()
	}
	redactedValue, valueCount := redactor.RedactValue(err)
	message := err.Error()
	switch typed := redactedValue.(type) {
	case error:
		message = typed.Error()
	case string:
		message = typed
	}
	message, textCount := redactor.RedactString(message)
	if valueCount == 0 && textCount == 0 && message == err.Error() {
		return err
	}
	if message == err.Error() {
		message = "tool safety: error details " + RedactedValue
	}
	return &redactedToolError{message: message, target: err}
}

var _ tool.CallableTool = (*guardedCallableTool)(nil)
var _ tool.MetadataProvider = (*guardedCallableTool)(nil)
var _ tool.StreamableTool = (*guardedStreamableTool)(nil)
var _ tool.MetadataProvider = (*guardedStreamableTool)(nil)
