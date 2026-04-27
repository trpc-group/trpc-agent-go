//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates the identity plugin end-to-end without
// depending on any real model backend.
//
// The demo runs the same pair of "tools" twice — once without the identity
// plugin, once with it — and prints what each tool sees so callers can
// compare the effect of plugin.BeforeAgent + plugin.BeforeTool on the
// per-call context.
//
// The two tools represent the two canonical consumer patterns:
//
//   - An HTTP tool that signs outgoing requests with identity.HeadersFromContext,
//     simulating how MCP HTTP toolsets pick up per-user Authorization via
//     mcp.WithHTTPBeforeRequest.
//   - A command tool that merges identity.EnvVarsFromContext into its child
//     process environment, simulating how skill_run / workspace_exec pick up
//     per-user env vars via codeexecutor.NewEnvInjectingCodeExecutor.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/plugin/identity"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	agentName = "identity-agent"
	userID    = "alice"
	sessionID = "session-001"
)

func main() {
	fmt.Println("Identity Plugin Demo")
	fmt.Println(strings.Repeat("=", 72))

	if err := runWithoutPlugin(); err != nil {
		fmt.Printf("baseline run failed: %v\n", err)
		os.Exit(1)
	}
	fmt.Println(strings.Repeat("-", 72))
	if err := runWithPlugin(); err != nil {
		fmt.Printf("plugin run failed: %v\n", err)
		os.Exit(1)
	}
}

// runWithoutPlugin invokes the two tools directly, without ever resolving
// identity. Each tool finds an empty identity in its context.
func runWithoutPlugin() error {
	fmt.Println("[1] Tools invoked without the identity plugin.")
	ctx := context.Background()

	httpResult, err := newHTTPTool().Call(ctx, []byte(`{"path":"/api/v1/me"}`))
	if err != nil {
		return err
	}
	fmt.Printf("  http_tool    -> %v\n", httpResult)

	cmdResult, err := newCommandTool().Call(ctx, []byte(`{"command":"printenv"}`))
	if err != nil {
		return err
	}
	fmt.Printf("  command_tool -> %v\n", cmdResult)
	return nil
}

// runWithPlugin wires identity.NewPlugin into a plugin.Manager, manually
// drives the BeforeAgent / BeforeTool lifecycle, and then invokes the tools
// against the enriched context. This mirrors what Runner does internally
// for every tool call.
func runWithPlugin() error {
	fmt.Println("[2] Tools invoked with the identity plugin enabled.")

	mgr, err := plugin.NewManager(identity.NewPlugin(newDemoProvider()))
	if err != nil {
		return fmt.Errorf("create plugin manager: %w", err)
	}

	ctx := context.Background()
	inv := &agent.Invocation{
		AgentName: agentName,
		Session: &session.Session{
			UserID: userID,
			ID:     sessionID,
		},
	}

	// 1. BeforeAgent resolves identity once and stores it on the invocation.
	if cb := mgr.AgentCallbacks(); cb != nil {
		if _, err := cb.RunBeforeAgent(ctx, &agent.BeforeAgentArgs{
			Invocation: inv,
		}); err != nil {
			return fmt.Errorf("before agent: %w", err)
		}
	}
	// The invocation must be present on the context so BeforeTool can find
	// the resolved Identity in state. Runner does this automatically.
	// *InvocationContext embeds context.Context and therefore satisfies the
	// context.Context interface directly; no field access is required.
	var toolCtx context.Context = agent.NewInvocationContext(ctx, inv)

	// 2. BeforeTool attaches the Identity to each per-tool context.
	toolCtx = applyBeforeTool(toolCtx, mgr, "http_tool",
		[]byte(`{"path":"/api/v1/me"}`))
	httpResult, err := newHTTPTool().Call(toolCtx, []byte(`{"path":"/api/v1/me"}`))
	if err != nil {
		return err
	}
	fmt.Printf("  http_tool    -> %v\n", httpResult)

	toolCtx = applyBeforeTool(toolCtx, mgr, "command_tool",
		[]byte(`{"command":"printenv"}`))
	cmdResult, err := newCommandTool().Call(toolCtx, []byte(`{"command":"printenv"}`))
	if err != nil {
		return err
	}
	fmt.Printf("  command_tool -> %v\n", cmdResult)
	return nil
}

// applyBeforeTool simulates the Runner-internal dispatch for one tool call:
// it invokes the plugin manager's BeforeTool callbacks and threads any
// returned context back to the caller.
func applyBeforeTool(
	ctx context.Context,
	mgr *plugin.Manager,
	toolName string,
	jsonArgs []byte,
) context.Context {
	cb := mgr.ToolCallbacks()
	if cb == nil {
		return ctx
	}
	res, err := cb.RunBeforeTool(ctx, &tool.BeforeToolArgs{
		ToolName:  toolName,
		Arguments: jsonArgs,
	})
	if err != nil {
		fmt.Printf("  [warn] before_tool %s: %v\n", toolName, err)
		return ctx
	}
	if res != nil && res.Context != nil {
		return res.Context
	}
	return ctx
}
