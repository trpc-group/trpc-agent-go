//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeexecutor

import "context"

// RunEnvProvider returns per-run environment variables derived from the
// execution context. Implementations typically read caller-supplied
// state (e.g. user tokens) from the context and return them as
// key-value pairs to be merged into every RunProgramSpec executed by
// the wrapped engine.
//
// Returned entries never override values already present in the spec's
// Env map, preserving explicit per-call overrides from tools.
type RunEnvProvider func(ctx context.Context) map[string]string

// NewEnvInjectingEngine wraps eng so that every RunProgram and
// StartProgram call merges environment variables from provider into
// the spec before delegating to the underlying runner.
//
// If eng or provider is nil the original engine is returned unchanged.
func NewEnvInjectingEngine(eng Engine, provider RunEnvProvider) Engine {
	if eng == nil || provider == nil {
		return eng
	}
	return &envEngine{
		inner:    eng,
		provider: provider,
	}
}

type envEngine struct {
	inner    Engine
	provider RunEnvProvider
}

func (e *envEngine) Manager() WorkspaceManager { return e.inner.Manager() }
func (e *envEngine) FS() WorkspaceFS           { return e.inner.FS() }
func (e *envEngine) Describe() Capabilities    { return e.inner.Describe() }

func (e *envEngine) Runner() ProgramRunner {
	inner := e.inner.Runner()
	if inner == nil {
		return nil
	}
	base := &envRunner{inner: inner, provider: e.provider}
	if ir, ok := inner.(InteractiveProgramRunner); ok {
		return &envInteractiveRunner{
			envRunner:   *base,
			interactive: ir,
		}
	}
	return base
}

// envRunner wraps a ProgramRunner to inject per-run env vars.
type envRunner struct {
	inner    ProgramRunner
	provider RunEnvProvider
}

func (r *envRunner) RunProgram(
	ctx context.Context,
	ws Workspace,
	spec RunProgramSpec,
) (RunResult, error) {
	mergeProviderEnv(ctx, r.provider, &spec)
	return r.inner.RunProgram(ctx, ws, spec)
}

// envInteractiveRunner extends envRunner with InteractiveProgramRunner
// support, so that type assertions from workspace_exec etc. continue
// to work through the wrapper.
type envInteractiveRunner struct {
	envRunner
	interactive InteractiveProgramRunner
}

func (r *envInteractiveRunner) StartProgram(
	ctx context.Context,
	ws Workspace,
	spec InteractiveProgramSpec,
) (ProgramSession, error) {
	mergeProviderEnv(ctx, r.provider, &spec.RunProgramSpec)
	return r.interactive.StartProgram(ctx, ws, spec)
}

// NewEnvInjectingCodeExecutor wraps exec so that Engine() returns an
// env-injecting engine. exec must implement EngineProvider; if it does
// not (or is nil), the original executor is returned unchanged.
//
// This is the recommended top-level entry point: pass the wrapped
// executor to llmagent.WithCodeExecutor and all tool paths (skill_run,
// workspace_exec, interactive sessions) will automatically receive the
// injected environment variables.
func NewEnvInjectingCodeExecutor(
	exec CodeExecutor,
	provider RunEnvProvider,
) CodeExecutor {
	if exec == nil || provider == nil {
		return exec
	}
	ep, ok := exec.(EngineProvider)
	if !ok {
		return exec
	}
	return &envCodeExecutor{
		CodeExecutor: exec,
		ep:           ep,
		provider:     provider,
	}
}

type envCodeExecutor struct {
	CodeExecutor
	ep       EngineProvider
	provider RunEnvProvider
}

func (e *envCodeExecutor) Engine() Engine {
	return NewEnvInjectingEngine(e.ep.Engine(), e.provider)
}

// mergeProviderEnv builds a fresh Env map that contains all entries
// from spec.Env plus any provider-supplied entries whose keys are not
// already present. The original spec.Env map is never mutated.
func mergeProviderEnv(
	ctx context.Context,
	provider RunEnvProvider,
	spec *RunProgramSpec,
) {
	if provider == nil {
		return
	}
	extra := provider(ctx)
	if len(extra) == 0 {
		return
	}
	merged := make(map[string]string, len(spec.Env)+len(extra))
	for k, v := range spec.Env {
		merged[k] = v
	}
	for k, v := range extra {
		if _, exists := merged[k]; !exists {
			merged[k] = v
		}
	}
	spec.Env = merged
}
