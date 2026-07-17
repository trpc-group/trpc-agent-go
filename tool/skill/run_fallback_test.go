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
	return codeexecutor.Capabilities{SupportsDeclarativeIO: codeexecutor.SupportsDeclarativeIOFalse}
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
