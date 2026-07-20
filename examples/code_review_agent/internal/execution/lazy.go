//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package execution

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

// ExecutorFactory constructs a runtime-specific CodeExecutor.
type ExecutorFactory func(Config) (codeexecutor.CodeExecutor, error)

var defaultCodeBlockDelimiter = codeexecutor.CodeBlockDelimiter{
	Start: "```",
	End:   "```",
}

// LazyExecutor defers runtime construction until execution is actually needed.
type LazyExecutor struct {
	cfg     Config
	factory ExecutorFactory

	mu           sync.Mutex
	initialized  bool
	initializing bool
	initDone     chan struct{}
	exec         codeexecutor.CodeExecutor
	initErr      error
	closeErr     error
	closed       bool
}

// NewLazyExecutor returns an executor wrapper that only constructs the
// underlying runtime on first use.
func NewLazyExecutor(
	cfg Config,
	factory ExecutorFactory,
) *LazyExecutor {
	if factory == nil {
		factory = NewExecutor
	}
	return &LazyExecutor{
		cfg:     cfg,
		factory: factory,
	}
}

var _ codeexecutor.CodeExecutor = (*LazyExecutor)(nil)
var _ codeexecutor.EngineProvider = (*LazyExecutor)(nil)

func (e *LazyExecutor) ExecuteCode(
	ctx context.Context,
	input codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	exec, err := e.ensure()
	if err != nil {
		return codeexecutor.CodeExecutionResult{}, err
	}
	return exec.ExecuteCode(ctx, input)
}

func (e *LazyExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	e.mu.Lock()
	exec := e.exec
	e.mu.Unlock()
	if exec != nil {
		return exec.CodeBlockDelimiter()
	}
	return defaultCodeBlockDelimiter
}

func (e *LazyExecutor) Engine() codeexecutor.Engine {
	exec, err := e.ensure()
	if err != nil {
		return lazyErrorEngine{err: err}
	}
	ep, ok := exec.(codeexecutor.EngineProvider)
	if !ok || ep == nil {
		return nil
	}
	return ep.Engine()
}

func (e *LazyExecutor) Close() error {
	e.mu.Lock()
	if e.closed {
		err := e.closeErr
		e.mu.Unlock()
		return err
	}
	e.closed = true
	done := e.initDone
	initializing := e.initializing
	e.mu.Unlock()
	if initializing {
		<-done
	}

	e.mu.Lock()
	exec := e.exec
	e.exec = nil
	e.mu.Unlock()

	var err error
	if exec != nil {
		err = CleanupExecutor(exec)
	}

	e.mu.Lock()
	e.closeErr = err
	e.mu.Unlock()
	return err
}

func (e *LazyExecutor) ensure() (codeexecutor.CodeExecutor, error) {
	e.mu.Lock()
	if e.closed {
		err := e.closeErr
		e.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return nil, errors.New("executor is closed")
	}
	if e.initialized {
		done := e.initDone
		initializing := e.initializing
		e.mu.Unlock()
		if initializing {
			<-done
			e.mu.Lock()
			exec, err := e.exec, e.initErr
			e.mu.Unlock()
			return exec, err
		}
		e.mu.Lock()
		exec, err := e.exec, e.initErr
		e.mu.Unlock()
		return exec, err
	}
	e.initialized = true
	e.initializing = true
	e.initDone = make(chan struct{})
	factory := e.factory
	cfg := e.cfg
	e.mu.Unlock()

	exec, err := factory(cfg)
	if err != nil {
		err = fmt.Errorf("create %s executor: %w", cfg.Runtime, err)
	}

	e.mu.Lock()
	if e.closed {
		e.initializing = false
		e.initErr = errors.New("executor is closed")
		close(e.initDone)
		e.mu.Unlock()
		if exec != nil {
			_ = CleanupExecutor(exec)
		}
		return nil, e.initErr
	}
	e.exec = exec
	e.initErr = err
	e.initializing = false
	close(e.initDone)
	e.mu.Unlock()
	return exec, err
}

type lazyErrorEngine struct {
	err error
}

func (e lazyErrorEngine) Manager() codeexecutor.WorkspaceManager {
	return lazyErrorManager{err: e.err}
}

func (e lazyErrorEngine) FS() codeexecutor.WorkspaceFS {
	return lazyErrorFS{err: e.err}
}

func (e lazyErrorEngine) Runner() codeexecutor.ProgramRunner {
	return lazyErrorRunner{err: e.err}
}

func (e lazyErrorEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{}
}

type lazyErrorManager struct {
	err error
}

func (m lazyErrorManager) CreateWorkspace(
	context.Context,
	string,
	codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	return codeexecutor.Workspace{}, m.err
}

func (m lazyErrorManager) Cleanup(
	context.Context,
	codeexecutor.Workspace,
) error {
	return m.err
}

type lazyErrorFS struct {
	err error
}

func (f lazyErrorFS) PutFiles(
	context.Context,
	codeexecutor.Workspace,
	[]codeexecutor.PutFile,
) error {
	return f.err
}

func (f lazyErrorFS) StageDirectory(
	context.Context,
	codeexecutor.Workspace,
	string,
	string,
	codeexecutor.StageOptions,
) error {
	return f.err
}

func (f lazyErrorFS) Collect(
	context.Context,
	codeexecutor.Workspace,
	[]string,
) ([]codeexecutor.File, error) {
	return nil, f.err
}

func (f lazyErrorFS) StageInputs(
	context.Context,
	codeexecutor.Workspace,
	[]codeexecutor.InputSpec,
) error {
	return f.err
}

func (f lazyErrorFS) CollectOutputs(
	context.Context,
	codeexecutor.Workspace,
	codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, f.err
}

type lazyErrorRunner struct {
	err error
}

func (r lazyErrorRunner) RunProgram(
	context.Context,
	codeexecutor.Workspace,
	codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	return codeexecutor.RunResult{}, r.err
}
