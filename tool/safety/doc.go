//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package safety provides a Tool Execution Safety Guard that scans commands
// and code before execution, produces structured reports and audit events,
// and integrates with tool.PermissionPolicy for pre-execution interception.
//
// The safety guard operates as a pipeline:
//   - A ScanInput describing the pending tool call is presented to the scanner.
//   - Registered rules evaluate the input and produce Findings with a risk
//     level and recommended decision (allow / deny / ask / needs_human_review).
//   - Findings are aggregated into a ScanResult carrying the highest-priority
//     decision and risk level.
//   - The result is mapped to a tool.PermissionDecision so it can be used by
//     any tool.PermissionPolicy implementation.
//   - A structured Report and AuditEvent are produced for logging and
//     observability.
//
// Policy is configurable via YAML or JSON files (see PolicyFile). The default
// policy is fail-closed: if no policy is loaded, the default action is deny.
package safety
