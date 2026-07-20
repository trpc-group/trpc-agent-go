//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety implements a Tool Execution Safety Guard for trpc-agent-go.
//
// The guard inspects commands and code blocks before they are executed by
// workspaceexec, hostexec, codeexec, or MCP tools, and returns one of three
// decisions:
//
//   - DecisionAllow: the input passed every enabled rule.
//   - DecisionDeny:  a critical or high-risk rule matched and the policy
//     threshold rejects it.
//   - DecisionAsk:   the input requires human review. The framework adapter
//     converts this to tool.PermissionActionAsk so hosts with an approval UI
//     can intervene before execution.
//
// The guard also redacts detected secrets from tool results, writes JSONL
// audit events, and exposes OpenTelemetry span attributes for the existing
// execute-tool span.
//
// # Fail-closed behavior
//
// Unknown execution shapes fail closed. A known tool (workspace_exec,
// exec_command, execute_code, write_stdin, kill_session) with malformed
// arguments is denied. An unknown tool with command-shaped arguments is
// mapped to DecisionAsk unless a ToolProfile is registered for it. A
// shellsafe parse failure is a finding, never an implicit allow; no fallback
// to strings.Split may authorize a command.
//
// # Sandbox relationship
//
// This guard is a static preflight check. It does not replace a sandbox or
// kernel boundary. It cannot see runtime behavior (IPC, memory/CPU
// exhaustion, child processes, side channels), encoded or generated
// commands, or DNS rebinding. Production deployments must combine the guard
// with clean environment isolation, timeout/output limits, process cleanup,
// and a real sandbox (container, E2B, OS-level virtualization). See
// examples/tool_safety_guard/README.md for the full boundary explanation.
//
// # No raw secrets
//
// Evidence, reports, audit events, OTel attributes, and example artifacts
// contain only redacted snippets, field names, hashes, lengths, or rule
// identifiers. The original command is summarized; CommandHash allows
// correlation without storing the raw payload.
package safety
