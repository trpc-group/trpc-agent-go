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
package safety
