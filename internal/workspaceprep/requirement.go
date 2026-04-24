//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceprep

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// Kind is a short, stable label describing what a Requirement
// produces in the workspace. It is persisted in WorkspaceMetadata and
// can be used for telemetry, logging and skip heuristics.
type Kind string

const (
	// KindFile denotes a file (or directory) that must exist with a
	// specific content fingerprint.
	KindFile Kind = "file"
	// KindCommand denotes a one-shot command that must have been
	// executed successfully for the given fingerprint.
	KindCommand Kind = "command"
	// KindSkill denotes a skill working copy materialized into
	// skills/<name>.
	KindSkill Kind = "skill"
	// KindConversationFile denotes a single conversation attachment
	// staged into work/inputs/.
	KindConversationFile Kind = "conversation_file"
)

// Phase determines the fixed ordering across Requirement kinds. All
// requirements in an earlier phase are applied to completion before
// any requirement from a later phase. Within a single phase the
// declaration order is preserved.
type Phase int

const (
	// PhaseFile runs first. Files, artifacts, hostfs mounts and
	// conversation file uploads happen in this phase.
	PhaseFile Phase = 10
	// PhaseSkill runs after files. Skill working copies are
	// materialized here so subsequent commands can see the latest
	// version.
	PhaseSkill Phase = 20
	// PhaseCommand runs last. Bootstrap commands like pip install
	// execute once all inputs and skills are in place.
	PhaseCommand Phase = 30
)

// ApplyContext is the limited surface a Requirement implementation
// needs while applying itself. It is intentionally narrow so that
// Requirements cannot reach into the reconciler or surrounding tools.
type ApplyContext struct {
	// Engine is the active workspace engine.
	Engine codeexecutor.Engine
	// Workspace is the invocation-scoped workspace that the
	// reconciler resolved via the shared WorkspaceRegistry.
	Workspace codeexecutor.Workspace
	// Invocation is the current agent invocation. It may be nil when
	// the reconciler is called outside an invocation (for example in
	// unit tests), so Requirement implementations should guard access.
	Invocation *agent.Invocation
	// Metadata is the current, mutable WorkspaceMetadata view held by
	// the reconciler. Requirement implementations may inspect it and
	// mutate non-Prepared fields when strictly necessary (for example
	// to record conversation file InputRecord entries). The Prepared
	// map is managed by the reconciler itself and must not be written
	// directly by requirement code.
	Metadata *codeexecutor.WorkspaceMetadata
}

// Requirement is a single, idempotent, addressable piece of workspace
// desired state. Implementations should be cheap to construct and
// cheap to fingerprint; the Apply method performs the actual I/O.
type Requirement interface {
	// Key is a stable identifier used both as the map key in
	// WorkspaceMetadata.Prepared and as the de-duplication key when
	// multiple Providers return the same logical requirement. Keys
	// must be unique across providers for a given workspace.
	Key() string
	// Kind returns the Requirement kind for reporting and ordering.
	Kind() Kind
	// Phase returns the execution phase. Requirements in an earlier
	// phase always run before later phases.
	Phase() Phase
	// Required reports whether a failure in Apply should abort the
	// whole reconcile. Optional requirements are logged as warnings
	// and skipped on failure.
	Required() bool
	// Fingerprint returns a content-addressed hash that captures
	// "what this requirement currently resolves to". When Fingerprint
	// matches the previously-persisted PreparedRecord and the
	// requirement's sentinel still exists, the reconciler skips Apply.
	// Implementations may perform lightweight I/O (for example
	// hashing a skill source directory) but should avoid expensive
	// work; anything truly expensive should be deferred to Apply.
	Fingerprint(ctx context.Context, rctx ApplyContext) (string, error)
	// SentinelExists lets the reconciler detect the "metadata says
	// prepared but the filesystem no longer has it" case, for
	// example when a user ran rm -rf work/skills/foo between calls.
	// Returning false forces Apply even if Fingerprint matches.
	// Requirements without a stable sentinel (one-shot commands
	// without MarkerPath) can return (true, nil).
	SentinelExists(ctx context.Context, rctx ApplyContext) (bool, error)
	// Apply brings the workspace into the desired state for this
	// requirement. Implementations must be idempotent: Apply may be
	// called more than once across reconcile attempts.
	Apply(ctx context.Context, rctx ApplyContext) error
	// Target is an optional human-readable description of where this
	// requirement writes to (for logging and PreparedRecord).
	Target() string
}

// Provider returns the Requirements contributed by one source.
// Providers should be deterministic and side-effect free; they may
// read invocation state, session state and static business config,
// but they must not write to the workspace.
type Provider interface {
	Name() string
	Requirements(
		ctx context.Context,
		inv *agent.Invocation,
	) ([]Requirement, error)
}

// Reconciler brings a workspace into the desired state described by a
// slice of Requirements. Implementations must be safe for concurrent
// use across workspaces and must serialize reconcile for the same
// workspace (by ws.Path) using a keyed mutex.
type Reconciler interface {
	// Reconcile collects requirements from the provided list, decides
	// which ones already satisfy the workspace, applies the rest in
	// phase order, and returns warnings collected from optional
	// requirement failures. When a required requirement fails Apply,
	// Reconcile returns the corresponding error and the remaining
	// requirements are skipped.
	Reconcile(
		ctx context.Context,
		eng codeexecutor.Engine,
		ws codeexecutor.Workspace,
		reqs []Requirement,
	) ([]string, error)
}
