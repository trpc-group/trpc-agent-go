//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package workspaceprep provides a generic, declarative "desired-state"
// layer for executor workspaces. It lets different subsystems (business
// bootstrap, skill_load session state, conversation file uploads, ...)
// express what must exist in a workspace before a command runs, and it
// converges the workspace to that state in a single idempotent pass
// right before workspace_exec executes a user command.
//
// The package exposes three collaborating concepts:
//
//   - Requirement: an atomic, addressable piece of desired state such
//     as "file work/config.json must have content X", "run
//     pip install -r work/requirements.txt once", or "stage skill foo".
//     Each Requirement knows how to fingerprint itself and how to
//     apply itself.
//   - Provider: a source of Requirements. Providers are lightweight;
//     they inspect the invocation, session state, or static business
//     configuration and return a list of Requirements without
//     performing workspace I/O.
//   - Reconciler: the top-level orchestrator that collects Requirements
//     from all Providers, orders them by Phase, serializes reconcile
//     per workspace via a process-local keyed mutex, checks whether
//     each Requirement is already satisfied (fingerprint match plus
//     sentinel presence), applies the rest, and updates the
//     PreparedRecord entries in WorkspaceMetadata.
//
// Reconciler is deliberately minimal: no dependency graph, no
// distributed locking, no open-ended hook system. Requirement order is
// fixed by Phase (files before skills before commands) and, within a
// phase, the order Providers returned them in.
//
// This package is the intended replacement for the tool-local
// skill-staging logic inside skill_run. workspace_exec should call
// Reconcile before executing a user command, and skill_load should
// only write session state, never perform workspace I/O.
package workspaceprep
