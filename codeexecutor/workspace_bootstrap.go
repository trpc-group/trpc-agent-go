//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

import "time"

// WorkspaceBootstrapSpec is the user-facing description of "what must
// exist in the workspace before any user command runs". It is
// intentionally narrow: the framework's reconciler abstractions
// (Requirement / Provider / Reconciler) are not part of the public
// API while their semantics are still being refined. Business code
// composes WorkspaceFile and WorkspaceCommand declaratively, and the
// agent translates them into reconcile work behind the scenes.
//
// The spec is independent of any particular agent type: it describes
// workspace state, not agent behavior. Agent packages (for example
// llmagent) provide Options that accept a WorkspaceBootstrapSpec and
// wire it into their workspace_exec tool.
//
// Files are staged first (in declaration order); Commands then run
// (also in declaration order). Both are idempotent: the reconciler
// fingerprints each entry and skips work whose fingerprint plus
// on-disk sentinel are still satisfied.
type WorkspaceBootstrapSpec struct {
	// Files are static inputs that must exist before commands run.
	// Each entry maps a workspace-relative Target to either inline
	// Content or a richer Input source (artifact://, host://,
	// workspace://, skill://).
	Files []WorkspaceFile
	// Commands are one-shot initialization commands such as
	// "python3 -m venv .venv" or "pip install -r requirements.txt".
	Commands []WorkspaceCommand
}

// WorkspaceFile describes a single file the framework must stage
// into the workspace before user commands run. Exactly one of
// Content or Input should be set.
type WorkspaceFile struct {
	// Key is an optional stable identifier. When empty the
	// reconciler derives one from Target.
	Key string
	// Target is the workspace-relative destination path.
	Target string
	// Content is inline file content. When set, Input is ignored.
	Content []byte
	// Mode is the POSIX mode for inline writes. When zero, inline
	// writes fall back to DefaultScriptFileMode (0o644).
	Mode uint32
	// Input is a richer source spec covering artifact://, host://,
	// workspace://, and skill:// URIs.
	Input *InputSpec
	// Optional marks this file as non-blocking: failures are
	// surfaced as warnings instead of aborting the workspace.
	Optional bool
}

// WorkspaceCommand describes a one-shot command the framework must
// execute during workspace preparation. The command runs through the
// engine's runner, with the same isolation guarantees as user
// commands.
//
// Self-healing notes:
//
//   - When MarkerPath is set, the reconciler treats the marker as
//     the sentinel: removing it forces the command to re-run on the
//     next reconcile.
//   - When ObservedPaths is set, the sentinel is satisfied only if
//     all listed paths still exist.
//   - When neither is set, re-runs are driven purely by Fingerprint
//     changes (Cmd/Args/Env/Cwd/FingerprintInputs/FingerprintSalt).
//
// FingerprintInputs lets callers fold the contents of arbitrary
// workspace-relative files into the fingerprint so that edits to,
// for example, requirements.txt naturally force a re-install.
type WorkspaceCommand struct {
	// Key is an optional stable identifier. When empty the
	// reconciler derives one from Cmd+Args.
	Key string
	// Cmd is the program to execute.
	Cmd string
	// Args are command-line arguments passed verbatim.
	Args []string
	// Env augments the run environment.
	Env map[string]string
	// Cwd is a workspace-relative working directory.
	Cwd string
	// Timeout bounds a single run. Zero means no extra timeout
	// beyond the engine's default.
	Timeout time.Duration
	// MarkerPath is a workspace-relative sentinel file. When set
	// and missing, Apply creates it after a successful run.
	MarkerPath string
	// ObservedPaths are additional workspace-relative paths used
	// as sentinels for self-healing.
	ObservedPaths []string
	// FingerprintInputs are workspace-relative files whose
	// contents are hashed into Fingerprint.
	FingerprintInputs []string
	// FingerprintSalt is a caller-supplied version string added
	// to the fingerprint, letting business config force a re-run
	// without changing Cmd/Args.
	FingerprintSalt string
	// Optional marks this command as non-blocking: failures are
	// surfaced as warnings instead of aborting the workspace.
	Optional bool
}
