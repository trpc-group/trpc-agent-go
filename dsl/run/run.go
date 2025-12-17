// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package run provides a thin, opinionated "DSL‑run" façade over the core
// engine‑level DSL. It is intended to be the default entry point for running
// engine DSL graphs in-process, without going through the HTTP DSL server or
// any code generation pipeline.
//
// This package deliberately stays small: it wraps Parser + Validator +
// Compiler + graph.Executor into a single Runner type with sensible defaults.
// Higher‑level platforms can build their own orchestration on top of these
// primitives while still depending only on the engine‑level DSL.
package run

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	engine "trpc.group/trpc-go/trpc-agent-go/dsl"
	"trpc.group/trpc-go/trpc-agent-go/dsl/compiler"
	"trpc.group/trpc-go/trpc-agent-go/dsl/registry"
	dslvalidator "trpc.group/trpc-go/trpc-agent-go/dsl/validator"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Options configures a Runner. All registries are optional; when nil,
// the Runner falls back to the default shared registries used by the
// DSL system.
type Options struct {
	// ComponentRegistry provides component implementations for node_type
	// references. Defaults to registry.DefaultRegistry when nil.
	ComponentRegistry *registry.Registry

	// ToolRegistry resolves tool names used by LLM / tools / MCP nodes.
	// Defaults to a fresh registry when nil.
	ToolRegistry *registry.ToolRegistry

	// ToolSetRegistry resolves named ToolSets, when used.
	ToolSetRegistry *registry.ToolSetRegistry

	// ReducerRegistry provides named reducers for State fields. Defaults
	// to a fresh registry when nil.
	ReducerRegistry *registry.ReducerRegistry

	// AgentRegistry resolves pre‑registered agents for builtin.agent nodes.
	// Defaults to a fresh registry when nil.
	AgentRegistry *registry.AgentRegistry
}

// Runner is a convenience façade that wires together Parser, Validator,
// SchemaInference, Compiler and graph.Executor into a single helper for
// running engine‑level DSL graphs.
type Runner struct {
	parser    *engine.Parser
	validator *dslvalidator.Validator
	compiler  *compiler.Compiler
}

// NewRunner constructs a Runner using the provided Options. When specific
// registries are nil, sensible defaults are used.
func NewRunner(opts Options) *Runner {
	reg := opts.ComponentRegistry
	if reg == nil {
		reg = registry.DefaultRegistry
	}

	validator := dslvalidator.New(dslvalidator.WithComponentRegistry(reg))
	var compilerOpts []compiler.Option
	if opts.ToolRegistry != nil {
		compilerOpts = append(compilerOpts, compiler.WithToolProvider(opts.ToolRegistry))
	}
	if opts.ToolSetRegistry != nil {
		compilerOpts = append(compilerOpts, compiler.WithToolSetProvider(opts.ToolSetRegistry))
	}
	if opts.ReducerRegistry != nil {
		compilerOpts = append(compilerOpts, compiler.WithReducerRegistry(opts.ReducerRegistry))
	}
	if opts.AgentRegistry != nil {
		compilerOpts = append(compilerOpts, compiler.WithAgentRegistry(opts.AgentRegistry))
	}
	// Always pass the component registry explicitly so Validator and Compiler
	// stay in sync, while still defaulting to DefaultRegistry for callers
	// that don't provide one.
	compilerOpts = append(compilerOpts, compiler.WithComponentRegistry(reg))
	comp := compiler.New(compilerOpts...)

	return &Runner{
		parser:    engine.NewParser(),
		validator: validator,
		compiler:  comp,
	}
}

// ParseGraph parses raw JSON bytes into an engine‑level Graph.
func (r *Runner) ParseGraph(data []byte) (*engine.Graph, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}
	return r.parser.Parse(data)
}

// ParseGraphFile parses a JSON DSL file into an engine‑level Graph.
func (r *Runner) ParseGraphFile(filename string) (*engine.Graph, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}
	return r.parser.ParseFile(filename)
}

// CompileGraph validates and compiles an engine‑level Graph into a compiled
// graph ready to be executed with graph.NewExecutor.
func (r *Runner) CompileGraph(graphDef *engine.Graph) (*graph.Graph, error) {
	if r == nil {
		return nil, fmt.Errorf("runner is nil")
	}
	if err := r.validator.Validate(graphDef); err != nil {
		return nil, err
	}
	return r.compiler.Compile(graphDef)
}

// ExecuteCompiled runs a compiled Graph with the given initial State and
// Invocation. It is a thin wrapper around graph.NewExecutor + Executor.Execute.
func (r *Runner) ExecuteCompiled(
	ctx context.Context,
	compiled *graph.Graph,
	initialState graph.State,
	invocation *agent.Invocation,
	execOpts ...graph.ExecutorOption,
) (<-chan *event.Event, error) {
	if compiled == nil {
		return nil, fmt.Errorf("compiled graph is nil")
	}
	exec, err := graph.NewExecutor(compiled, execOpts...)
	if err != nil {
		return nil, err
	}
	return exec.Execute(ctx, initialState, invocation)
}
