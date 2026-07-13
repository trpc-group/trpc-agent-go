// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package safety provides the data structures and policy scaffolding
// for a tool-level Safety Guard that inspects tool calls before they
// reach the execution backend.
//
// # Design goals
//
// The Safety Guard sits between the agent framework and the tool
// executor. Every tool call that involves shell execution, file system
// mutation, or network access passes through the guard, which applies
// a declarative policy and returns a structured [Report] describing
// its decision.
//
// The design follows three principles:
//
//  1. Declarative policy — operators express allow/deny rules in a
//     YAML file ([tool_safety_policy.yaml]). Changing the policy must
//     never require a code change or redeploy; [Policy.Reload] performs
//     a hot swap at runtime.
//
//  2. Structured evidence — every non-allow decision carries one or
//     more [Evidence] entries that identify the triggering rule, the
//     matched snippet, and a human-readable reason. The caller (model
//     or human reviewer) can act on the report without parsing free-
//     form text.
//
//  3. Backend-agnostic — the guard does not assume a particular
//     command parser or execution backend. [ScanInput] carries the
//     raw command and an optional pre-parsed pipeline (compatible
//     with [internal/shellsafe.Pipeline]), so different backends can
//     plug in without changing the public types.
//
// # Decision model
//
// The guard produces one of four decisions, represented by the
// [Decision] type:
//
//   - [DecisionAllow] — the call is safe to execute.
//   - [DecisionDeny] — the call violates a hard rule and must not
//     execute.
//   - [DecisionAsk] — the call carries elevated risk; a human must
//     approve before execution.
//   - [DecisionNeedsHumanReview] — the guard could not classify the
//     call with sufficient confidence; a human must inspect it.
//
// The first three values ("allow", "deny", "ask") are deliberately
// aligned with [tool.PermissionAction] so the guard integrates with
// the existing permission pipeline without translation.
//
// # Backend boundary
//
// The hostexec lifecycle rule applies only when [ScanInput.Backend] is exactly
// "hostexec". hostexec executes through the host shell and maintains sessions
// for background, yielded, or PTY commands. workspaceexec instead runs through
// the codeexecutor/workspace abstraction, whose isolation and interactive
// capabilities depend on its configured runtime. Static findings do not
// configure either backend, terminate processes, or provide environment
// isolation.
//
// # Package layout
//
// This package defines only data structures and the policy loader.
// Rule logic, the scanner implementation, and the auditor backends
// are intentionally left to downstream tasks.
//
//   - [Policy] and [LoadPolicy] / [Policy.Reload] — YAML-driven policy.
//   - [ScanInput] — input to the scanner (T6).
//   - [Report], [Evidence], [Decision], [RiskLevel] — structured output.
//   - [Auditor] — audit sink interface (implementation in T8b).
package safety
