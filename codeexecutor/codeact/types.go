//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeact

import (
	"context"
	"encoding/json"
)

// Request describes one CodeAct execution request. CodeAct runtimes may use
// any transport internally (stdio, container RPC, gRPC, WebSocket, sandbox FFI),
// but they must preserve the ToolCall/ToolCallHandler semantics.
type Request struct {
	Code     string
	Language string
}

// Result is the JSON-safe final value and captured stdout emitted by guest code.
type Result struct {
	Value  json.RawMessage `json:"value,omitempty"`
	Stdout string          `json:"stdout,omitempty"`
}

// ToolCall is a single sandbox-to-host tool invocation.
type ToolCall struct {
	ID   string
	Name string
	Args json.RawMessage
}

// ToolCallHandler handles sandbox-originated tool calls. Implementations are
// the host capability boundary; runtimes should not bypass them.
type ToolCallHandler interface {
	HandleToolCall(context.Context, ToolCall) (json.RawMessage, error)
}

// Runtime executes generated code and routes sandbox-originated tool calls to
// the provided handler. Local stdio, Docker, remote, and sandbox-native
// backends should all implement this interface.
type Runtime interface {
	ExecuteCodeAct(context.Context, Request, ToolCallHandler) (Result, error)
}

// Execute runs CodeAct code with the provided runtime and tool-call handler.
func Execute(ctx context.Context, runtime Runtime, handler ToolCallHandler, code string) (Result, error) {
	if runtime == nil {
		return Result{}, errRequired("runtime")
	}
	if handler == nil {
		return Result{}, errRequired("tool call handler")
	}
	if code == "" {
		return Result{}, errRequired("code")
	}
	return runtime.ExecuteCodeAct(ctx, Request{Code: code, Language: "python"}, handler)
}

func errRequired(name string) error { return requiredError{name: name} }

type requiredError struct{ name string }

func (e requiredError) Error() string { return "codeact: " + e.name + " is required" }
