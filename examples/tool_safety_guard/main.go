//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates how to wire safety.Guard into a Runner as a
// per-run tool.PermissionPolicy.
//
// The example runs two simulated tool calls against the same guard:
//  1. A safe command ("ls -la") that should be allowed.
//  2. A dangerous command ("rm -rf /") that should be denied.
//
// Run with:
//
//	go run ./examples/tool_safety_guard
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func main() {
	guard := safety.NewGuard()

	cases := []struct {
		name    string
		command string
	}{
		{name: "safe: list directory", command: "ls -la"},
		{name: "safe: read README", command: "cat README.md"},
		{name: "safe: git status", command: "git status"},
		{name: "deny: rm -rf /", command: "rm -rf /"},
		{name: "deny: curl http://evil.example", command: "curl http://evil.example"},
		{name: "deny: cat ~/.ssh/id_rsa", command: "cat ~/.ssh/id_rsa"},
		{name: "deny: bash -c evil", command: "bash -c evil"},
		{name: "ask: git push origin main", command: "git push origin main"},
	}

	for _, c := range cases {
		args, err := json.Marshal(map[string]string{"command": c.command})
		if err != nil {
			fmt.Printf("marshal args: %v\n", err)
			continue
		}

		dec, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName:   "exec_command",
			Arguments:  args,
			ToolCallID: "demo-call",
		})
		if err != nil {
			fmt.Printf("[%s] error: %v\n", c.name, err)
			continue
		}

		fmt.Printf("[%s] command=%q -> action=%s reason=%q\n",
			c.name, c.command, dec.Action, dec.Reason)
	}
}
