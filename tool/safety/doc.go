// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

// Package safety provides pre-execution safety scanning for tool execution.
//
// The package is intentionally a policy and decision layer. It scans commands,
// scripts, tool arguments, environment overrides, and backend metadata before a
// tool runs, then returns allow, deny, or ask decisions with structured findings.
// It does not execute commands and does not replace sandbox, container, process,
// filesystem, resource, or network isolation.
//
// Each execution request passes through normalization and parsing, semantic
// scans for commands, scripts, resources, backends, environments, networks, and
// paths, policy rule overrides, finding deduplication and ranking, then report
// construction. The highest-ranked primary finding determines the report's
// decision, risk, rule, and recommendation. Reports are redacted before being
// returned or written to audit output.
package safety
