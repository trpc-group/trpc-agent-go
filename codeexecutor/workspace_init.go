//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeexecutor

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrWorkspaceInitNeedsEngineProvider is returned when hooks are requested but
// the executor does not implement [EngineProvider].
var ErrWorkspaceInitNeedsEngineProvider = errors.New(
	"codeexecutor: workspace init hooks require CodeExecutor to implement EngineProvider",
)

// ErrWorkspaceInitIncompleteEngine is returned when Engine() or Manager() is nil.
var ErrWorkspaceInitIncompleteEngine = errors.New(
	"codeexecutor: Engine() or Engine.Manager() is nil",
)

const (
	workspaceInitErrorOutputMax = 1024
	workspaceInitCleanupTimeout = 30 * time.Second
)

// WorkspaceInitHook is a callback run after [WorkspaceManager.CreateWorkspace]
// succeeds and before that workspace is returned to callers. Use it for
// deterministic setup (stage inputs, install dependencies). Hooks are scoped to
// workspace creation: they do not watch files on disk or re-run later solely
// because workspace contents changed.
//
// Multiple hooks run in declaration order; failure labels use hook index
// (0, 1, ...). Use [NewWorkspaceInitHook] for declarative staging and commands with
// per-command diagnostic names in errors.
type WorkspaceInitHook func(context.Context, WorkspaceInitEnv) error

// WorkspaceInitEnv is the capability bundle passed to each [WorkspaceInitHook].
// The workspace directory already exists when the hook runs.
type WorkspaceInitEnv struct {
	Workspace    Workspace
	ExecID       string
	Policy       WorkspacePolicy
	FS           WorkspaceFS
	Runner       ProgramRunner
	Capabilities Capabilities
}

// WorkspaceInitSpec is a declarative hook: stage [InputSpec] inputs, then run
// init commands in order. It does not express per-tool-call idempotency; callers
// that need convergence on every tool invocation handle that at a higher layer.
type WorkspaceInitSpec struct {
	// Inputs are staged via [WorkspaceFS.StageInputs] (artifact://, host://, etc.).
	Inputs []InputSpec
	// Commands run sequentially after inputs; non-zero exit code fails the hook.
	Commands []WorkspaceInitCommand
}

// WorkspaceInitCommand describes one init-time program run. Fields align with
// [RunProgramSpec] minus [ResourceLimits], which init hooks omit in v1.
// Name is optional; when set it appears in errors for that command.
type WorkspaceInitCommand struct {
	Name    string
	Cmd     string
	Args    []string
	Env     map[string]string
	Cwd     string
	Stdin   string
	Timeout time.Duration
}

// NewWorkspaceInitHook wraps [WorkspaceInitSpec] as a [WorkspaceInitHook] function.
func NewWorkspaceInitHook(spec WorkspaceInitSpec) WorkspaceInitHook {
	return func(ctx context.Context, env WorkspaceInitEnv) error {
		if len(spec.Inputs) > 0 {
			if env.FS == nil {
				return fmt.Errorf(
					"WorkspaceFS is nil but Inputs are non-empty",
				)
			}
			if err := env.FS.StageInputs(ctx, env.Workspace, spec.Inputs); err != nil {
				return err
			}
		}
		for i, c := range spec.Commands {
			if strings.TrimSpace(c.Cmd) == "" {
				return fmt.Errorf("command %d: Cmd is empty", i)
			}
			if env.Runner == nil {
				return fmt.Errorf(
					"ProgramRunner is nil but Commands are non-empty",
				)
			}
			label := c.Name
			if strings.TrimSpace(label) == "" {
				label = c.Cmd
			}
			rs := RunProgramSpec{
				Cmd:     c.Cmd,
				Args:    append([]string(nil), c.Args...),
				Env:     cloneStringStringMap(c.Env),
				Cwd:     c.Cwd,
				Stdin:   c.Stdin,
				Timeout: c.Timeout,
			}
			res, err := env.Runner.RunProgram(ctx, env.Workspace, rs)
			if err != nil {
				return fmt.Errorf("command %q: %w", label, err)
			}
			if res.ExitCode != 0 {
				return fmt.Errorf(
					"command %q exited %d: stderr=%s stdout=%s",
					label,
					res.ExitCode,
					truncateInitOut(res.Stderr),
					truncateInitOut(res.Stdout),
				)
			}
		}
		return nil
	}
}

// NewWorkspaceInitExecutor wraps exec so every [WorkspaceManager.CreateWorkspace]
// runs the given hooks before returning the workspace.
//
// When hooks is non-empty, exec must implement [EngineProvider] with a non-nil
// [Engine] and non-nil [WorkspaceManager]; otherwise this function returns an
// error satisfying [ErrWorkspaceInitNeedsEngineProvider] or
// [ErrWorkspaceInitIncompleteEngine].
//
// For [InputSpec] values that use artifact://, the context passed to
// CreateWorkspace must carry the artifact service and (when applicable) session
// information—the same requirements as [WorkspaceFS.StageInputs]. Standard agent
// workspace-session tooling injects that context before [WorkspaceRegistry.Acquire],
// so init hooks can load artifacts without extra setup.
func NewWorkspaceInitExecutor(
	exec CodeExecutor,
	hooks ...WorkspaceInitHook,
) (CodeExecutor, error) {
	if exec == nil {
		if len(hooks) > 0 {
			return nil, fmt.Errorf("codeexecutor.NewWorkspaceInitExecutor: exec is nil")
		}
		return nil, nil
	}
	if len(hooks) == 0 {
		return exec, nil
	}
	for i, h := range hooks {
		if h == nil {
			return nil, fmt.Errorf(
				"codeexecutor.NewWorkspaceInitExecutor: hook %d is nil",
				i,
			)
		}
	}
	ep, ok := exec.(EngineProvider)
	if !ok {
		return nil, fmt.Errorf(
			"codeexecutor.NewWorkspaceInitExecutor: %w",
			ErrWorkspaceInitNeedsEngineProvider,
		)
	}
	eng := ep.Engine()
	if eng == nil || eng.Manager() == nil {
		return nil, fmt.Errorf(
			"codeexecutor.NewWorkspaceInitExecutor: %w",
			ErrWorkspaceInitIncompleteEngine,
		)
	}
	safeHooks := append([]WorkspaceInitHook(nil), hooks...)
	return &workspaceInitExecutor{
		CodeExecutor: exec,
		ep:           ep,
		hooks:        safeHooks,
	}, nil
}

func cloneStringStringMap(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func truncateInitOut(s string) string {
	if len(s) <= workspaceInitErrorOutputMax {
		return s
	}
	return s[:workspaceInitErrorOutputMax] + "...(truncated)"
}

type workspaceInitExecutor struct {
	CodeExecutor
	ep    EngineProvider
	hooks []WorkspaceInitHook
}

func (e *workspaceInitExecutor) Engine() Engine {
	return newWorkspaceInitEngine(e.ep.Engine(), e.hooks)
}

type workspaceInitEngine struct {
	inner Engine
	hooks []WorkspaceInitHook
}

func newWorkspaceInitEngine(inner Engine, hooks []WorkspaceInitHook) Engine {
	if inner == nil {
		return nil
	}
	return &workspaceInitEngine{inner: inner, hooks: hooks}
}

func (e *workspaceInitEngine) Manager() WorkspaceManager {
	mgr := e.inner.Manager()
	if mgr == nil {
		return nil
	}
	return &workspaceInitManager{
		inner: mgr,
		eng:   e.inner,
		hooks: e.hooks,
	}
}

func (e *workspaceInitEngine) FS() WorkspaceFS { return e.inner.FS() }

func (e *workspaceInitEngine) Runner() ProgramRunner { return e.inner.Runner() }

func (e *workspaceInitEngine) Describe() Capabilities { return e.inner.Describe() }

type workspaceInitManager struct {
	inner WorkspaceManager
	eng   Engine
	hooks []WorkspaceInitHook
}

func (m *workspaceInitManager) CreateWorkspace(
	ctx context.Context,
	execID string,
	pol WorkspacePolicy,
) (Workspace, error) {
	ws, err := m.inner.CreateWorkspace(ctx, execID, pol)
	if err != nil {
		return Workspace{}, err
	}
	env := WorkspaceInitEnv{
		Workspace:    ws,
		ExecID:       execID,
		Policy:       pol,
		FS:           m.eng.FS(),
		Runner:       m.eng.Runner(),
		Capabilities: m.eng.Describe(),
	}
	for i, h := range m.hooks {
		if err := h(ctx, env); err != nil {
			hookErr := fmt.Errorf("workspace init hook %d: %w", i, err)
			// Best-effort cleanup without inheriting deadline/cancel from ctx,
			// which may already be expired when the hook failed for timeout.
			cleanCtx, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				workspaceInitCleanupTimeout,
			)
			cerr := m.inner.Cleanup(cleanCtx, ws)
			cancel()
			if cerr != nil {
				return Workspace{}, fmt.Errorf("%w (cleanup failed: %v)", hookErr, cerr)
			}
			return Workspace{}, hookErr
		}
	}
	return ws, nil
}

func (m *workspaceInitManager) Cleanup(
	ctx context.Context,
	ws Workspace,
) error {
	return m.inner.Cleanup(ctx, ws)
}
