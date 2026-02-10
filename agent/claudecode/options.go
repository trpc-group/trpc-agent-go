//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package claudecode

import (
	"context"
	"errors"
	"fmt"
)

// OutputFormat is the Claude Code CLI transcript output format.
type OutputFormat string

const (
	// OutputFormatJSON is the array-based JSON transcript output format.
	OutputFormatJSON OutputFormat = "json"
	// OutputFormatStreamJSON is the JSONL transcript output format.
	OutputFormatStreamJSON OutputFormat = "stream-json"
)

// options stores ClaudeCodeAgent configuration.
type options struct {
	name          string
	description   string
	bin           string
	args          []string
	outputFormat  OutputFormat
	env           []string
	workDir       string
	commandRunner commandRunner
	rawOutputHook RawOutputHook
}

// Option configures a ClaudeCodeAgent.
type Option func(*options)

// RawOutputHookArgs provides raw CLI output for a single Claude Code invocation.
type RawOutputHookArgs struct {
	// InvocationID is the invocation identifier associated with this CLI execution.
	InvocationID string
	// SessionID is the framework session identifier from the invocation.
	SessionID string
	// CLISessionID is the Claude Code session identifier used for this CLI execution.
	CLISessionID string
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
// If the hook returns an error, the agent emits a final error event and stops processing the invocation.
type RawOutputHook func(ctx context.Context, args *RawOutputHookArgs) error

// WithName sets the agent name.
func WithName(name string) Option {
	return func(o *options) {
		o.name = name
	}
}

// WithBin sets the Claude Code CLI executable path.
func WithBin(bin string) Option {
	return func(o *options) {
		o.bin = bin
	}
}

// WithExtraArgs appends additional CLI arguments before the session flags and prompt.
func WithExtraArgs(args ...string) Option {
	return func(o *options) {
		o.args = append(o.args, args...)
	}
}

// WithOutputFormat sets the Claude Code CLI output format used by this agent.
// Only JSON-based formats are supported because the agent needs transcripts to emit tool events.
func WithOutputFormat(format OutputFormat) Option {
	return func(o *options) {
		o.outputFormat = format
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
// If the hook returns an error, the invocation fails with a flow error event.
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
		name:        "claude-code-cli",
		description: "Invokes a locally installed Claude Code CLI and emits tool events from its transcript.",
		bin:         "claude",
		args: []string{
			"-p",
			"--verbose",
		},
		outputFormat:  OutputFormatJSON,
		commandRunner: execCommandRunner{},
	}
	for _, o := range opt {
		o(opts)
	}
	if opts.bin == "" {
		return nil, errors.New("claude bin is empty")
	}
	if opts.commandRunner == nil {
		return nil, errors.New("command runner is nil")
	}
	if !isSupportedOutputFormat(opts.outputFormat) {
		return nil, fmt.Errorf("unsupported output format: %s", opts.outputFormat)
	}
	opts.args = append(opts.args, "--output-format", string(opts.outputFormat))
	return opts, nil
}

// isSupportedOutputFormat reports whether the agent can parse transcripts in the given output format.
func isSupportedOutputFormat(format OutputFormat) bool {
	switch format {
	case OutputFormatJSON, OutputFormatStreamJSON:
		return true
	default:
		return false
	}
}
