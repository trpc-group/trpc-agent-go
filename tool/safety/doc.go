//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety implements a pre-execution safety guard for tool,
// MCP tool, skill and code-executor command execution.
//
// The package provides three cooperating layers:
//
//   - Scan: a policy-driven scanner that inspects a command line,
//     code blocks, environment variables and execution parameters
//     before anything runs, and produces a structured Report with a
//     decision (allow / ask / needs_human_review / deny), a risk
//     level, per-rule findings, evidence and recommendations.
//
//   - Guard: a tool.PermissionPolicy implementation that converts
//     framework tool calls (workspace_exec, exec_command,
//     execute_code and user-registered names) into scan requests,
//     rejects or escalates risky calls before execution, and emits
//     JSONL audit events plus OpenTelemetry-ready span attributes.
//
//   - Policy: a declarative configuration (YAML or JSON) covering
//     allowed/denied commands, denied filesystem paths, a network
//     egress allowlist, resource limits and environment variable
//     rules. Operators can change the enforcement behaviour without
//     recompiling.
//
// Command structure is parsed with internal/shellsafe, which accepts
// only a conservative subset of shell syntax. Commands that cannot be
// parsed safely (command substitution, redirections, background
// operators, variable expansion, ...) are never allowed by default;
// they resolve to the configurable parse-error decision (deny unless
// overridden).
//
// The guard is a policy checkpoint, not a sandbox. It reduces the
// blast radius of obviously dangerous calls and produces an audit
// trail, but code that passes the scan still runs with whatever
// privileges the selected executor grants. Always pair it with real
// isolation (codeexecutor/container, E2B, workspace isolation) for
// untrusted workloads. See README.md in this directory for the full
// threat-model discussion.
package safety
