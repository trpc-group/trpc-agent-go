//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how to wire safety.Guard into the framework
// at two layers:
//
//  1. The Runner layer, via agent.WithToolPermissionPolicy.
//  2. A tool.ToolSet (hostexec-style) layer, via safety.WrapToolSet.
//
// The example exercises both paths against the same guard, against a
// set of safe and dangerous commands. Each call prints the
// permission decision and the reason for it.
//
// Run with:
//
//	go run ./examples/tool_safety_guard
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func main() {
	// 1. Build a guard with the default 10 rules.
	guard := safety.NewGuard()

	fmt.Println("=== Runner-level path (agent.WithToolPermissionPolicy) ===")
	demonstrateRunnerLevel(guard)

	fmt.Println()
	fmt.Println("=== ToolSet-level path (safety.WrapToolSet) ===")
	demonstrateToolSetLevel(guard)
}

// demonstrateRunnerLevel shows how a guard plugs into a Runner so the
// framework intercepts every tool call before the executor runs it.
// In production the `r.Run(...)` call below is what the agent drives;
// the example short-circuits it by calling the guard directly.
func demonstrateRunnerLevel(guard *safety.Guard) {
	cases := []struct {
		name    string
		command string
	}{
		{name: "safe: list directory", command: "ls -la"},
		{name: "safe: read README", command: "cat README.md"},
		{name: "deny: rm -rf /", command: "rm -rf /"},
		{name: "deny: curl http://evil.example", command: "curl http://evil.example"},
		{name: "deny: cat ~/.ssh/id_rsa", command: "cat ~/.ssh/id_rsa"},
		{name: "deny: bash -c evil", command: "bash -c evil"},
		{name: "ask: git push origin main", command: "git push origin main"},
	}
	for _, c := range cases {
		runCase(guard, c.name, c.command)
	}
	_ = agent.WithToolPermissionPolicy(guard) // keep import non-empty
}

// demonstrateToolSetLevel shows how to wrap a tool.ToolSet so each
// tool's Call is gated by the same guard. The example builds a
// minimal in-memory tool.ToolSet instead of pulling in hostexec, so
// the binary has no third-party dependencies and the demo is fully
// reproducible.
func demonstrateToolSetLevel(guard *safety.Guard) {
	inner := &exampleToolSet{
		name: "example_host",
		tools: []tool.Tool{
			&exampleTool{
				name: "exec_command",
				desc: "Run a host shell command.",
			},
		},
	}
	wrapped := safety.WrapToolSet(inner, guard)

	fmt.Printf("Wrapped tool set %q exposes %d tool(s):\n",
		wrapped.Name(), len(wrapped.Tools(context.Background())))

	runCaseOnToolSet(wrapped, "via WrapToolSet: ls -la", "ls -la")
	runCaseOnToolSet(wrapped, "via WrapToolSet: rm -rf /", "rm -rf /")
	runCaseOnToolSet(wrapped, "via WrapToolSet: git push", "git push origin main")
}

func runCase(guard *safety.Guard, label, command string) {
	args, _ := json.Marshal(map[string]string{"command": command})
	dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:   "exec_command",
		Arguments:  args,
		ToolCallID: "demo",
	})
	if err != nil {
		fmt.Printf("[%s] error: %v\n", label, err)
		return
	}
	fmt.Printf("[%s] action=%s reason=%q\n", label, dec.Action, dec.Reason)
}

func runCaseOnToolSet(ts tool.ToolSet, label, command string) {
	tools := ts.Tools(context.Background())
	if len(tools) == 0 {
		return
	}
	args, _ := json.Marshal(map[string]string{"command": command})
	callable, ok := tools[0].(tool.CallableTool)
	if !ok {
		fmt.Printf("[%s] tool is not callable: %T\n", label, tools[0])
		return
	}
	out, err := callable.Call(context.Background(), args)
	if err != nil {
		fmt.Printf("[%s] error: %v\n", label, err)
		return
	}
	if pr, ok := out.(tool.PermissionResult); ok {
		fmt.Printf("[%s] permission_result status=%s reason=%q\n",
			label, pr.Status, pr.Reason)
		return
	}
	fmt.Printf("[%s] executed: %v\n", label, out)
}

// exampleTool is a tiny tool.Tool used to demonstrate the wrap path
// without depending on the hostexec / workspaceexec packages.
type exampleTool struct {
	name string
	desc string
}

func (e *exampleTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: e.name, Description: e.desc}
}

func (e *exampleTool) Call(_ context.Context, _ []byte) (any, error) {
	return map[string]string{"status": "executed"}, nil
}

// exampleToolSet wraps a fixed list of tools and satisfies
// tool.ToolSet.
type exampleToolSet struct {
	name  string
	tools []tool.Tool
}

func (s *exampleToolSet) Tools(context.Context) []tool.Tool { return s.tools }
func (s *exampleToolSet) Close() error                      { return nil }
func (s *exampleToolSet) Name() string                      { return s.name }
