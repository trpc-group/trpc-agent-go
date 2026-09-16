//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolerror

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// Source identifies which part of a tool call produced a failure.
type Source string

const (
	// SourceModel identifies failures caused by a model-produced tool call,
	// such as invalid arguments or an unknown tool name.
	SourceModel Source = "model"
	// SourceTool identifies failures returned by tool execution.
	SourceTool Source = "tool"
	// SourceFramework identifies framework or configuration failures around a
	// tool call, such as an invalid tool schema or an expired context.
	SourceFramework Source = "framework"
)

// Kind is the stable, high-level classification of a tool call failure.
type Kind string

const (
	// KindInvalidArguments indicates that tool call arguments are not valid.
	KindInvalidArguments Kind = "invalid_arguments"
	// KindToolNotFound indicates that the requested tool is unavailable.
	KindToolNotFound Kind = "tool_not_found"
	// KindExecution indicates that execution started but did not complete
	// successfully.
	KindExecution Kind = "execution"
	// KindConfiguration indicates an invalid framework or tool configuration.
	KindConfiguration Kind = "configuration"
)

// Details describes one structured tool call failure.
//
// Source and Kind are stable classifications intended for programmatic use.
// Code provides a more specific reason within Kind. Param, when present, is a
// JSON Pointer into the tool arguments. Message is model-facing diagnostic
// text and should not be used as a stable classification key.
type Details struct {
	Source    Source `json:"source"`
	Kind      Kind   `json:"kind"`
	Code      string `json:"code"`
	Message   string `json:"message"`
	Param     string `json:"param,omitempty"`
	Retryable bool   `json:"retryable"`
}

// Failure is the JSON envelope emitted as a tool result when the plugin
// handles a failure. Successful tool results are not wrapped or modified.
type Failure struct {
	OK    bool    `json:"ok"`
	Error Details `json:"error"`
}

// Resolver classifies application-specific tool failures.
//
// Resolver is called after a tool returns, including when the tool returned a
// nil error. Returning ok=true replaces the tool result with a Failure built
// from details. Returning ok=false delegates to the plugin's default handling,
// which only classifies non-nil execution errors.
//
// Resolver may inspect args.Result to support tools that encode failure in a
// result value while returning a nil error. It may also override the default
// classification of args.Error.
type Resolver func(
	ctx context.Context,
	args *tool.AfterToolArgs,
) (details Details, ok bool)
