//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mragent

import (
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultMaxToolRounds = 4
	defaultMaxCues       = 6
	defaultMaxPaths      = 12
)

// Option configures the MRAgent GraphAgent.
type Option func(*Options)

// Options contains configuration for the MRAgent GraphAgent.
type Options struct {
	// Name is the graph agent name.
	Name string
	// Description describes the graph agent capability.
	Description string
	// RewriteInstruction controls the query rewrite node.
	RewriteInstruction string
	// ReconstructionInstruction controls the active reconstruction tool loop.
	ReconstructionInstruction string
	// PruneInstruction controls evidence pruning after reconstruction.
	PruneInstruction string
	// AnswerInstruction controls final answer generation.
	AnswerInstruction string
	// GenerationConfig applies to every LLM node.
	GenerationConfig model.GenerationConfig
	// ExtraTools are additional tools available during reconstruction.
	ExtraTools []tool.Tool
	// IncludeSessionLoadTool exposes session_load for raw event windows.
	IncludeSessionLoadTool bool
	// EnableParallelTools enables parallel execution in the graph tool node.
	EnableParallelTools bool
	// MaxToolRounds caps graph steps for the reconstruction loop.
	MaxToolRounds int
	// MaxCues is prompt guidance for cue search breadth.
	MaxCues int
	// MaxPaths is prompt guidance for path/content breadth.
	MaxPaths int
	// GraphAgentOptions are appended after the built-in GraphAgent options.
	GraphAgentOptions []graphagent.Option
	// ExecutorOptions are appended after the built-in max-step guard.
	ExecutorOptions []graph.ExecutorOption
}

func defaultOptions() Options {
	return Options{
		Name:                   DefaultAgentName,
		Description:            "MRAgent active memory reconstruction over cue-tag-content memory.",
		RewriteInstruction:     defaultRewriteInstruction,
		PruneInstruction:       defaultPruneInstruction,
		AnswerInstruction:      defaultAnswerInstruction,
		IncludeSessionLoadTool: true,
		MaxToolRounds:          defaultMaxToolRounds,
		MaxCues:                defaultMaxCues,
		MaxPaths:               defaultMaxPaths,
	}
}

func newOptions(opts ...Option) Options {
	cfg := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	if cfg.Name == "" {
		cfg.Name = DefaultAgentName
	}
	if cfg.MaxToolRounds <= 0 {
		cfg.MaxToolRounds = defaultMaxToolRounds
	}
	if cfg.MaxCues <= 0 {
		cfg.MaxCues = defaultMaxCues
	}
	if cfg.MaxPaths <= 0 {
		cfg.MaxPaths = defaultMaxPaths
	}
	if cfg.ReconstructionInstruction == "" {
		cfg.ReconstructionInstruction = defaultReconstructionInstruction(
			cfg.MaxToolRounds,
			cfg.MaxCues,
			cfg.MaxPaths,
			cfg.IncludeSessionLoadTool,
		)
	}
	if cfg.RewriteInstruction == "" {
		cfg.RewriteInstruction = defaultRewriteInstruction
	}
	if cfg.PruneInstruction == "" {
		cfg.PruneInstruction = defaultPruneInstruction
	}
	if cfg.AnswerInstruction == "" {
		cfg.AnswerInstruction = defaultAnswerInstruction
	}
	return cfg
}

func (o Options) executorOptions() []graph.ExecutorOption {
	// One tool round executes prepare -> reconstruct -> decide -> tools -> absorb.
	// The fixed stages are init, rewrite, absorb_rewrite, prune, absorb_prune,
	// prepare_answer, and answer. Keep a small cushion for early-stop paths.
	maxSteps := 5*o.MaxToolRounds + 10
	out := []graph.ExecutorOption{graph.WithMaxSteps(maxSteps)}
	out = append(out, o.ExecutorOptions...)
	return out
}

func (o Options) toolSetOptions() []ToolSetOption {
	opts := []ToolSetOption{
		WithToolSetSessionLoadTool(o.IncludeSessionLoadTool),
		WithToolSetExtraTools(o.ExtraTools...),
	}
	return opts
}

// WithName sets the GraphAgent name.
func WithName(name string) Option {
	return func(opts *Options) {
		opts.Name = name
	}
}

// WithDescription sets the GraphAgent description.
func WithDescription(description string) Option {
	return func(opts *Options) {
		opts.Description = description
	}
}

// WithGenerationConfig sets the generation config used by all LLM nodes.
func WithGenerationConfig(cfg model.GenerationConfig) Option {
	return func(opts *Options) {
		opts.GenerationConfig = cfg
	}
}

// WithSessionLoadTool controls whether session_load is available.
func WithSessionLoadTool(enable bool) Option {
	return func(opts *Options) {
		opts.IncludeSessionLoadTool = enable
	}
}

// WithExtraTools appends extra reconstruction tools.
func WithExtraTools(tools ...tool.Tool) Option {
	return func(opts *Options) {
		opts.ExtraTools = append(opts.ExtraTools, tools...)
	}
}

// WithMaxToolRounds caps reconstruction loop rounds through graph max steps.
func WithMaxToolRounds(rounds int) Option {
	return func(opts *Options) {
		opts.MaxToolRounds = rounds
	}
}

// WithBudgets sets prompt guidance for reconstruction breadth.
func WithBudgets(maxCues, maxPaths int) Option {
	return func(opts *Options) {
		opts.MaxCues = maxCues
		opts.MaxPaths = maxPaths
	}
}

// WithInstructions overrides all graph node instructions.
func WithInstructions(rewrite, reconstruct, prune, answer string) Option {
	return func(opts *Options) {
		opts.RewriteInstruction = rewrite
		opts.ReconstructionInstruction = reconstruct
		opts.PruneInstruction = prune
		opts.AnswerInstruction = answer
	}
}

// WithParallelTools controls parallel tool execution in the tool node.
func WithParallelTools(enable bool) Option {
	return func(opts *Options) {
		opts.EnableParallelTools = enable
	}
}

// WithGraphAgentOptions appends options to graphagent.New.
func WithGraphAgentOptions(opts ...graphagent.Option) Option {
	return func(options *Options) {
		options.GraphAgentOptions = append(options.GraphAgentOptions, opts...)
	}
}

// WithExecutorOptions appends executor options after the built-in max-step guard.
func WithExecutorOptions(opts ...graph.ExecutorOption) Option {
	return func(options *Options) {
		options.ExecutorOptions = append(options.ExecutorOptions, opts...)
	}
}
