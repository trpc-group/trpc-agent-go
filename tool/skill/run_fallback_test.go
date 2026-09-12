//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package skill

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestOutputSpecAllowsGlobsOnlyFallback(t *testing.T) {
	require.True(t, outputSpecAllowsGlobsOnlyFallback(codeexecutor.OutputSpec{
		Globs: []string{"**/*.txt"},
	}))
	require.False(t, outputSpecAllowsGlobsOnlyFallback(codeexecutor.OutputSpec{
		Globs: []string{"**/*.txt"},
		Save:  true,
	}))
	require.False(t, outputSpecAllowsGlobsOnlyFallback(codeexecutor.OutputSpec{
		Globs:        []string{"**/*.txt"},
		NameTemplate: "pref/",
	}))
	require.False(t, outputSpecAllowsGlobsOnlyFallback(codeexecutor.OutputSpec{
		Globs:    []string{"**/*.txt"},
		MaxFiles: 10,
	}))
	require.False(t, outputSpecAllowsGlobsOnlyFallback(codeexecutor.OutputSpec{
		Globs:  []string{"**/*.txt"},
		Inline: true,
	}))
}

// unsupportedIOFS returns ErrDeclarativeIONotSupported from CollectOutputs.
type unsupportedIOFS struct {
	codeexecutor.WorkspaceFS
}

func (u unsupportedIOFS) CollectOutputs(
	ctx context.Context, ws codeexecutor.Workspace, spec codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, codeexecutor.ErrDeclarativeIONotSupported
}

func (u unsupportedIOFS) Collect(
	ctx context.Context, ws codeexecutor.Workspace, patterns []string,
) ([]codeexecutor.File, error) {
	return []codeexecutor.File{{Name: "out/a.txt", Content: "hi"}}, nil
}

type unsupportedEngine struct {
	fs codeexecutor.WorkspaceFS
}

func (e unsupportedEngine) Manager() codeexecutor.WorkspaceManager { return nil }
func (e unsupportedEngine) FS() codeexecutor.WorkspaceFS           { return e.fs }
func (e unsupportedEngine) Runner() codeexecutor.ProgramRunner     { return nil }
func (e unsupportedEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{SupportsDeclarativeIO: codeexecutor.SupportsDeclarativeIOFalse()}
}

func TestPrepareOutputs_DeclarativeIO_GlobsOnlyFallback(t *testing.T) {
	rt := &RunTool{}
	eng := unsupportedEngine{fs: unsupportedIOFS{}}
	ws := codeexecutor.Workspace{Path: "/tmp/ws"}
	files, mf, warns, _, err := rt.prepareOutputs(context.Background(), eng, ws, runInput{
		Outputs: &codeexecutor.OutputSpec{Globs: []string{"**/*.txt"}},
	})
	require.NoError(t, err)
	require.Nil(t, mf)
	require.Empty(t, warns)
	require.Len(t, files, 1)
	require.Equal(t, "out/a.txt", files[0].Name)
}

func TestPrepareOutputs_DeclarativeIO_SaveRejected(t *testing.T) {
	rt := &RunTool{}
	eng := unsupportedEngine{fs: unsupportedIOFS{}}
	ws := codeexecutor.Workspace{Path: "/tmp/ws"}
	_, _, _, _, err := rt.prepareOutputs(context.Background(), eng, ws, runInput{
		Outputs: &codeexecutor.OutputSpec{
			Globs: []string{"**/*.txt"},
			Save:  true,
		},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, codeexecutor.ErrDeclarativeIONotSupported))
}

// countingRunner records RunProgram calls for preflight falsifiers.
type countingRunner struct {
	calls int
}

func (r *countingRunner) RunProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	r.calls++
	return codeexecutor.RunResult{ExitCode: 0}, nil
}

type preflightEngine struct {
	fs     codeexecutor.WorkspaceFS
	runner codeexecutor.ProgramRunner
	clean  bool
	declIO *bool
}

func (e preflightEngine) Manager() codeexecutor.WorkspaceManager { return nil }
func (e preflightEngine) FS() codeexecutor.WorkspaceFS           { return e.fs }
func (e preflightEngine) Runner() codeexecutor.ProgramRunner     { return e.runner }
func (e preflightEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{
		SupportsCleanEnv:      e.clean,
		SupportsDeclarativeIO: e.declIO,
	}
}

func TestInvariant_Preflight_UnsupportedOutputBeforeRun(t *testing.T) {
	runner := &countingRunner{}
	eng := preflightEngine{
		fs:     unsupportedIOFS{},
		runner: runner,
		declIO: codeexecutor.SupportsDeclarativeIOFalse(),
	}
	err := preflightDeclarativeOutputs(eng, runInput{
		Outputs: &codeexecutor.OutputSpec{
			Globs: []string{"**/*.txt"},
			Save:  true,
		},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, codeexecutor.ErrDeclarativeIONotSupported))
	require.Equal(t, 0, runner.calls, "preflight must not invoke runner")

	err = preflightDeclarativeOutputs(eng, runInput{
		Outputs: &codeexecutor.OutputSpec{Globs: []string{"**/*.txt"}},
	})
	require.NoError(t, err)
}

func TestInvariant_CleanEnv_PolicyRequiresSupportsCleanEnv(t *testing.T) {
	engFalse := preflightEngine{declIO: codeexecutor.SupportsDeclarativeIOFalse(), clean: false}
	err := checkSkillRunnerSupportsPolicy(engFalse)
	require.Error(t, err)
	require.Contains(t, err.Error(), "CleanEnv")

	engTrue := preflightEngine{clean: true}
	require.NoError(t, checkSkillRunnerSupportsPolicy(engTrue))
}

func TestBuildRunProgramSpec_PolicyFailsClosedWithoutCleanEnv(t *testing.T) {
	rt := &RunTool{
		allowedCmds: map[string]struct{}{"echo": {}},
	}
	runner := &countingRunner{}
	eng := preflightEngine{
		runner: runner,
		clean:  false,
	}
	_, err := rt.buildRunProgramSpec(
		context.Background(),
		eng,
		codeexecutor.Workspace{Path: "/tmp/ws"},
		".",
		".",
		runInput{Command: "echo hi", Skill: "s"},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "CleanEnv")
	require.Equal(t, 0, runner.calls)
}

func TestInvariant_Preflight_InputsBeforePrepare(t *testing.T) {
	eng := preflightEngine{
		fs:     unsupportedIOFS{},
		runner: &countingRunner{},
		declIO: codeexecutor.SupportsDeclarativeIOFalse(),
	}
	err := preflightDeclarativeOutputs(eng, runInput{
		Inputs: []codeexecutor.InputSpec{{
			// minimal non-empty inputs list
		}},
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, codeexecutor.ErrDeclarativeIONotSupported))
}

func TestInvariant_CleanEnv_LocalFallbackHonorsPolicy(t *testing.T) {
	rt := &RunTool{exec: nil, wsr: nil}
	eng := rt.ensureEngine()
	require.NotNil(t, eng)
	require.True(t, eng.Describe().SupportsCleanEnv,
		"local fallback engine must support CleanEnv for policy mode")
	require.NoError(t, checkSkillRunnerSupportsPolicy(eng))
}

func TestPreauthorizeSkillCommand_BeforeMutation(t *testing.T) {
	rt := &RunTool{allowedCmds: map[string]struct{}{"echo": {}}}
	require.NoError(t, preauthorizeSkillCommand(rt, "echo hi"))
	require.Error(t, preauthorizeSkillCommand(rt, "curl http://x"))
	rt2 := &RunTool{deniedCmds: map[string]struct{}{"rm": {}}}
	require.Error(t, preauthorizeSkillCommand(rt2, "rm -rf /"))
}

// TestRunTool_Call_DeniedCommandRejectedBeforeMutation verifies that a
// command denied by denied_commands is rejected in preflight (before
// workspace acquisition/staging), so no StageInputs or PutFiles
// mutations occur on a persistent workspace.
func TestRunTool_Call_DeniedCommandRejectedBeforeMutation(t *testing.T) {
	root := t.TempDir()
	writeSkill(t, root, testSkillName)
	repo, err := skill.NewFSRepository(root)
	require.NoError(t, err)

	tracker := &mutationTrackerEngine{
		Engine: localexec.New().Engine(),
	}
	rt := NewRunTool(
		repo,
		&engineProviderExecutor{engine: tracker},
	)
	rt.deniedCmds = map[string]struct{}{"rm": {}}

	args := runInput{
		Skill:   testSkillName,
		Command: "rm -rf /tmp",
		Timeout: timeoutSecSmall,
	}
	enc, err := jsonMarshal(args)
	require.NoError(t, err)
	_, err = rt.Call(context.Background(), enc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "denied",
		"denied command must be rejected before workspace staging")

	// Assert no workspace mutation methods were called after denial.
	tracker.assertNoMutations(t)
}

// mutationTrackerEngine wraps an Engine and records whether any
// mutating operations (CreateWorkspace, PutFiles, StageInputs,
// StageDirectory) were invoked.
type mutationTrackerEngine struct {
	codeexecutor.Engine
	mgr       *mutationTrackerManager
	fsTracker *mutationTrackerFS
	mu        sync.Mutex
}

func (e *mutationTrackerEngine) Manager() codeexecutor.WorkspaceManager {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.mgr == nil {
		e.mgr = &mutationTrackerManager{
			WorkspaceManager: e.Engine.Manager(),
		}
	}
	return e.mgr
}

func (e *mutationTrackerEngine) FS() codeexecutor.WorkspaceFS {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fsTracker == nil {
		e.fsTracker = &mutationTrackerFS{WorkspaceFS: e.Engine.FS()}
	}
	return e.fsTracker
}

func (e *mutationTrackerEngine) assertNoMutations(t *testing.T) {
	t.Helper()
	e.mu.Lock()
	mgr := e.mgr
	fs := e.fsTracker
	e.mu.Unlock()
	if mgr != nil {
		mgr.assertNoCreate(t)
	}
	if fs != nil {
		fs.assertNoFileMutations(t)
	}
}

// mutationTrackerManager records CreateWorkspace calls.
type mutationTrackerManager struct {
	codeexecutor.WorkspaceManager
	createCount int
	mu          sync.Mutex
}

func (m *mutationTrackerManager) CreateWorkspace(
	ctx context.Context, execID string, pol codeexecutor.WorkspacePolicy,
) (codeexecutor.Workspace, error) {
	m.mu.Lock()
	m.createCount++
	m.mu.Unlock()
	return m.WorkspaceManager.CreateWorkspace(ctx, execID, pol)
}

func (m *mutationTrackerManager) assertNoCreate(t *testing.T) {
	t.Helper()
	m.mu.Lock()
	defer m.mu.Unlock()
	require.Zero(t, m.createCount,
		"CreateWorkspace must not be called for a denied command")
}

// mutationTrackerFS records PutFiles, StageInputs, StageDirectory calls.
type mutationTrackerFS struct {
	codeexecutor.WorkspaceFS
	putFilesCount    int
	stageInputsCount int
	stageDirCount    int
	mu               sync.Mutex
}

func (f *mutationTrackerFS) PutFiles(
	ctx context.Context, ws codeexecutor.Workspace, files []codeexecutor.PutFile,
) error {
	f.mu.Lock()
	f.putFilesCount++
	f.mu.Unlock()
	return f.WorkspaceFS.PutFiles(ctx, ws, files)
}

func (f *mutationTrackerFS) StageInputs(
	ctx context.Context, ws codeexecutor.Workspace, specs []codeexecutor.InputSpec,
) error {
	f.mu.Lock()
	f.stageInputsCount++
	f.mu.Unlock()
	return f.WorkspaceFS.StageInputs(ctx, ws, specs)
}

func (f *mutationTrackerFS) StageDirectory(
	ctx context.Context, ws codeexecutor.Workspace, src, to string, opt codeexecutor.StageOptions,
) error {
	f.mu.Lock()
	f.stageDirCount++
	f.mu.Unlock()
	return f.WorkspaceFS.StageDirectory(ctx, ws, src, to, opt)
}

func (f *mutationTrackerFS) assertNoFileMutations(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	require.Zero(t, f.putFilesCount, "PutFiles must not be called for a denied command")
	require.Zero(t, f.stageInputsCount, "StageInputs must not be called for a denied command")
	require.Zero(t, f.stageDirCount, "StageDirectory must not be called for a denied command")
}

// engineProviderExecutor wraps an Engine so it satisfies
// codeexecutor.CodeExecutor + EngineProvider for RunTool construction.
// ExecuteCode is not used in preflight tests; it returns ErrNotSupported.
type engineProviderExecutor struct {
	engine codeexecutor.Engine
}

func (e *engineProviderExecutor) Engine() codeexecutor.Engine { return e.engine }

func (e *engineProviderExecutor) ExecuteCode(
	_ context.Context, _ codeexecutor.CodeExecutionInput,
) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, errors.New(
		"engineProviderExecutor: ExecuteCode not implemented")
}

func (e *engineProviderExecutor) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{}
}

func TestPreauthorizeSkillCommand_NilAndEdgeCases(t *testing.T) {
	// nil RunTool is a safe no-op.
	require.NoError(t, preauthorizeSkillCommand(nil, "echo hi"))

	rt := &RunTool{allowedCmds: map[string]struct{}{"echo": {}}}

	// Shell metacharacters are rejected by splitCommandLine.
	require.Error(t, preauthorizeSkillCommand(rt, "echo; ls"))

	// A bare backslash produces no argv tokens.
	require.Error(t, preauthorizeSkillCommand(rt, `\`))
}

func TestPreauthorizeSkillCommand_DenyOnlyAllowsNonDenied(t *testing.T) {
	// deniedCmds set, allowedCmds empty: a non-denied command passes.
	rt := &RunTool{deniedCmds: map[string]struct{}{"rm": {}}}
	require.NoError(t, preauthorizeSkillCommand(rt, "echo hi"))
}

func TestPreauthorizeSkillCommand_BaseNameMatch(t *testing.T) {
	// An allowed entry stored as a full path (e.g. /usr/bin/echo) must
	// still permit the bare command name "echo" via base-name matching.
	rt := &RunTool{allowedCmds: map[string]struct{}{"/usr/bin/echo": {}}}
	require.NoError(t, preauthorizeSkillCommand(rt, "echo hi"))
	require.Error(t, preauthorizeSkillCommand(rt, "curl http://x"))
}
