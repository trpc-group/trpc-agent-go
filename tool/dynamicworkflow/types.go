//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package dynamicworkflow exposes one-shot, code-defined orchestration of
// explicitly registered sub-agents and host tools.
package dynamicworkflow

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// CallKind identifies the host capability requested by workflow code.
type CallKind string

const (
	// CallKindTool invokes one explicitly allowlisted host tool.
	CallKindTool CallKind = "tool"
	// CallKindAgent invokes one explicitly registered sub-agent.
	CallKindAgent CallKind = "agent"
)

// Request describes one workflow-code execution.
type Request struct {
	Code string `json:"code"`
}

// Result is the JSON-safe final value and captured stdout emitted by workflow
// code.
type Result struct {
	Value  json.RawMessage `json:"value,omitempty"`
	Stdout string          `json:"stdout,omitempty"`
}

// AgentSpec describes one workflow-created agent instance. The workflow code
// sends this as JSON; Go resolves it against a registered template agent and
// applies only the supported, policy-checked fields.
//
// Template names the registered agent template. InstanceID optionally gives
// repeated calls within the same workflow a shared workflow-local history key.
// Instruction overrides the child instruction for this run. When Tools or
// Skills is omitted (or null in workflow JSON), the instance inherits the
// template's eligible user tools or configured skills. An explicit empty list
// disables that capability type; a non-empty list narrows it. Neither field
// can add capabilities. StructuredOutput overrides the template's structured
// output configuration for this workflow-local instance only.
type AgentSpec struct {
	Template         string                `json:"template"`
	InstanceID       string                `json:"instance_id,omitempty"`
	Instruction      string                `json:"instruction,omitempty"`
	Tools            []string              `json:"tools,omitempty"`
	Skills           []string              `json:"skills,omitempty"`
	StructuredOutput *StructuredOutputSpec `json:"structured_output,omitempty"`
}

// StructuredOutputSpec requests model-native JSON Schema output for one
// workflow-created AgentSpec instance. Schema must be a JSON object. If Strict
// is omitted, it defaults to true so workflow code can safely consume the
// returned result.Structured value.
//
// This is intentionally declarative data rather than a way for workflow code
// to construct an arbitrary Go agent. The registered template still owns the
// model, executor, callbacks, and all host capabilities.
type StructuredOutputSpec struct {
	Name        string          `json:"name,omitempty"`
	Schema      json.RawMessage `json:"schema"`
	Strict      *bool           `json:"strict,omitempty"`
	Description string          `json:"description,omitempty"`
}

// Call is one sandbox-to-host capability invocation.
type Call struct {
	ID   string
	Kind CallKind
	Name string
	Args json.RawMessage
}

// CallHandler is the host capability boundary. Runtimes must route every
// sandbox-originated call through this handler rather than accessing host
// services directly.
type CallHandler interface {
	HandleWorkflowCall(context.Context, Call) (json.RawMessage, error)
}

// Runtime executes workflow code and routes its host capability calls through
// the provided handler. Applications can implement this interface with local
// stdio, a remote sandbox, a microVM, or platform-native callbacks.
type Runtime interface {
	ExecuteWorkflow(context.Context, Request, CallHandler) (Result, error)
}

// Execute validates and runs one workflow program.
func Execute(ctx context.Context, runtime Runtime, handler CallHandler, code string) (Result, error) {
	if runtime == nil {
		return Result{}, required("runtime")
	}
	if handler == nil {
		return Result{}, required("call handler")
	}
	if strings.TrimSpace(code) == "" {
		return Result{}, required("code")
	}
	return runtime.ExecuteWorkflow(ctx, Request{Code: code}, handler)
}

func required(name string) error {
	return fmt.Errorf("dynamicworkflow: %s is required", name)
}
