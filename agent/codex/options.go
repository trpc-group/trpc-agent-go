//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codex

import (
	"context"
	"errors"
	"slices"
)

// StateKeyThreadID is the session state key used to persist the Codex thread id.
const StateKeyThreadID = "agent.codex.thread_id"

// options stores CodexAgent configuration.
type options struct {
	name          string
	description   string
	bin           string
	globalArgs    []string
	args          []string
	env           []string
	workDir       string
	commandRunner commandRunner
	rawOutputHook RawOutputHook
}

// Option configures a CodexAgent.
type Option func(*options)

// RawOutputHookArgs provides raw CLI output for a single Codex invocation.
type RawOutputHookArgs struct {
	// InvocationID is the invocation identifier associated with this CLI execution.
	InvocationID string
	// SessionID is the framework session identifier from the invocation.
	SessionID string
	// ResumeThreadID is the Codex thread identifier used for resume, if any.
	ResumeThreadID string
	// ThreadID is the Codex thread identifier observed in this CLI output, if any.
	ThreadID string
	// Prompt is the user prompt string passed to the CLI.
	Prompt string
	// Stdout is the raw stdout bytes captured from the CLI process.
	Stdout []byte
	// Stderr is the raw stderr bytes captured from the CLI process.
	Stderr []byte
	// Error is the execution error returned by the command runner, if any.
	Error error
}

// RawOutputHook is invoked after the CLI command completes and before transcript parsing.
//
// If the hook returns an error, the agent emits a final error event and stops processing the invocation.
type RawOutputHook func(ctx context.Context, args *RawOutputHookArgs) error

// WithName sets the agent name.
func WithName(name string) Option {
	return func(o *options) {
		o.name = name
	}
}

// WithBin sets the Codex CLI executable path.
func WithBin(bin string) Option {
	return func(o *options) {
		o.bin = bin
	}
}

// WithGlobalArgs appends Codex CLI root arguments before the exec subcommand.
func WithGlobalArgs(args ...string) Option {
	return func(o *options) {
		o.globalArgs = append(o.globalArgs, args...)
	}
}

// WithExtraArgs appends arguments after exec or exec resume before the session id and prompt.
// Use it only for flags accepted by both Codex exec forms.
func WithExtraArgs(args ...string) Option {
	return func(o *options) {
		o.args = append(o.args, args...)
	}
}

// WithEnv appends additional environment variables for the CLI process.
func WithEnv(env ...string) Option {
	return func(o *options) {
		o.env = append(o.env, env...)
	}
}

// WithWorkDir sets the CLI working directory.
func WithWorkDir(dir string) Option {
	return func(o *options) {
		o.workDir = dir
	}
}

// WithRawOutputHook sets a callback to observe raw CLI stdout/stderr output.
func WithRawOutputHook(hook RawOutputHook) Option {
	return func(o *options) {
		o.rawOutputHook = hook
	}
}

// withCommandRunner overrides how the agent executes external commands, only for test.
func withCommandRunner(runner commandRunner) Option {
	return func(o *options) {
		o.commandRunner = runner
	}
}

// newOptions applies options and validates the resulting configuration.
func newOptions(opt ...Option) (*options, error) {
	opts := &options{
		name:          "codex-cli",
		description:   "Invokes a locally installed Codex CLI and emits tool events from its JSONL output.",
		bin:           "codex",
		commandRunner: execCommandRunner{},
	}
	for _, o := range opt {
		o(opts)
	}
	if opts.bin == "" {
		return nil, errors.New("codex bin is empty")
	}
	if opts.commandRunner == nil {
		return nil, errors.New("command runner is nil")
	}
	if !slices.Contains(opts.args, "--json") {
		opts.args = append(opts.args, "--json")
	}
	return opts, nil
}
