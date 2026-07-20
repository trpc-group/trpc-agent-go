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
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
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
	files, mf, warns, err := rt.prepareOutputs(context.Background(), eng, ws, runInput{
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
	_, _, _, err := rt.prepareOutputs(context.Background(), eng, ws, runInput{
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
