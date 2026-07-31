//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safetyguard provides a pre-execution safety Guard that scans
// Tool, MCP Tool, Skill and CodeExecutor invocations for execution
// security risks and returns an allow / deny / ask decision through the
// framework's tool.PermissionPolicy contract.
//
// The Guard is a cross-cutting policy: it inspects the JSON arguments of
// every tool call before the runner executes it, surfaces a structured
// ScanReport, emits a tool_safety_audit.jsonl audit event and records
// OpenTelemetry span attributes (tool.safety.decision,
// tool.safety.risk_level, ...).
//
// It builds on, and does not duplicate, the existing lower-level
// building blocks:
//
//   - internal/shellsafe performs the conservative command parse and the
//     executable-name allow/deny + built-in shell-wrapper deny. The Guard
//     delegates command structural validation to shellsafe and adds the
//     risk categories shellsafe does not cover (path, network, resource,
//     dependency, sensitive-info).
//   - internal/redact supplies the sensitive-information patterns reused
//     by the sensitive-info scanner.
//
// The Guard is opt-in: a zero Guard / zero SafetyPolicy allows every
// call, mirroring the shellsafe "zero policy = no-op" contract and
// preserving backward compatibility.
//
// # Security boundaries: workspace_exec vs hostexec
//
// The framework exposes two shell-execution surfaces with very different
// blast radius. The Guard applies the same static checks to both, but the
// documented boundary informs which rules an operator should enable:
//
//   - workspace_exec runs inside a managed executor workspace (local or
//     container). Its cwd is workspace-relative, conversation files are
//     staged under work/inputs, and (when a command policy is active) the
//     spawn is hardened: a scrubbed env (envscrub), a non-login shell
//     ("sh -c", no /etc/profile / $HOME/.profile) and CleanEnv=true. The
//     residual risks the Guard targets are: destructive workspace commands
//     (rm -rf work/), egress from the workspace shell (curl to a non-
//     allowlisted host), dependency mutation (go install / pip install)
//     and over-large output. workspace_exec does NOT reach the host by
//     design, so host-only paths (~/.ssh, /etc/passwd) are only reachable
//     when the workspace is bound to the host filesystem (local exec);
//     operators using a sandboxed runtime can relax forbidden_paths.
//
//   - hostexec runs directly on the host (PTY sessions, login shell,
//     inherited host environment). It is intended for trusted, host-scoped
//     automation. Its blast radius is the host: a "rm -rf ~" or a
//     "cat ~/.ssh/id_rsa" executes against the real user home. Operators
//     exposing hostexec MUST therefore enable forbidden_paths (host home,
//     /etc), network egress controls (curl/wget to non-allowlisted hosts),
//     the dependency-change deny set, the resource limits (max timeout,
//     max output) and the PTY/privilege-escalation detection. The Guard's
//     default policy treats hostexec as the higher-risk surface: privilege
//     escalation (sudo/su/doas) and long-lived PTY sessions are flagged at
//     high / critical and routed to ask.
//
// The Guard cannot, on its own, enforce runtime isolation: it only sees the
// arguments and the tool metadata. Runtime isolation (CleanEnv, workspace
// containment, seccomp, network namespaces) remains the responsibility of
// the configured codeexecutor and hostexec deployment. The Guard is the
// pre-execution policy gate that complements those runtime controls.
package safetyguard
