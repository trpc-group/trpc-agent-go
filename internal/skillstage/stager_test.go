//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skillstage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

type stubFS struct {
	collectFiles []codeexecutor.File
	collectErr   error
	putErr       error
	putCalls     int
	putFiles     []codeexecutor.PutFile
}

func (s *stubFS) PutFiles(
	_ context.Context,
	_ codeexecutor.Workspace,
	files []codeexecutor.PutFile,
) error {
	s.putCalls++
	s.putFiles = append(s.putFiles, files...)
	return s.putErr
}

func (*stubFS) StageDirectory(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ string,
	_ string,
	_ codeexecutor.StageOptions,
) error {
	return nil
}

func (s *stubFS) Collect(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ []string,
) ([]codeexecutor.File, error) {
	if s.collectErr != nil {
		return nil, s.collectErr
	}
	return s.collectFiles, nil
}

func (*stubFS) StageInputs(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ []codeexecutor.InputSpec,
) error {
	return nil
}

func (*stubFS) CollectOutputs(
	_ context.Context,
	_ codeexecutor.Workspace,
	_ codeexecutor.OutputSpec,
) (codeexecutor.OutputManifest, error) {
	return codeexecutor.OutputManifest{}, nil
}

type stubRunner struct {
	res      codeexecutor.RunResult
	err      error
	calls    int
	lastSpec codeexecutor.RunProgramSpec
}

func (r *stubRunner) RunProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	r.calls++
	r.lastSpec = spec
	return r.res, r.err
}

type stubEngine struct {
	f codeexecutor.WorkspaceFS
	r codeexecutor.ProgramRunner
}

func (*stubEngine) Manager() codeexecutor.WorkspaceManager { return nil }
func (e *stubEngine) FS() codeexecutor.WorkspaceFS         { return e.f }
func (e *stubEngine) Runner() codeexecutor.ProgramRunner   { return e.r }
func (*stubEngine) Describe() codeexecutor.Capabilities {
	return codeexecutor.Capabilities{}
}

func TestStager_LoadSaveMetadata_CoversBranches(t *testing.T) {
	st := New()
	ctx := context.Background()
	ws := codeexecutor.Workspace{ID: "x", Path: t.TempDir()}

	_, err := st.LoadWorkspaceMetadata(ctx, nil, ws)
	require.Error(t, err)

	fs := &stubFS{collectErr: fmt.Errorf("collect fail")}
	eng := &stubEngine{f: fs}
	_, err = st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.Error(t, err)

	fs.collectErr = nil
	md, err := st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)

	fs.collectFiles = []codeexecutor.File{{
		Name:    codeexecutor.MetaFileName,
		Content: " \n\t ",
	}}
	md, err = st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)

	fs.collectFiles = []codeexecutor.File{{
		Name: codeexecutor.MetaFileName,
		Content: `{"version":0,"created_at":"0001-01-01T00:00:00Z",` +
			`"updated_at":"0001-01-01T00:00:00Z","last_access":"0001-01-01T00:00:00Z","skills":null}`,
	}}
	start := time.Now()
	md, err = st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)
	require.False(t, md.CreatedAt.IsZero())
	require.False(t, md.CreatedAt.Before(start))

	err = st.SaveWorkspaceMetadata(ctx, nil, ws, codeexecutor.WorkspaceMetadata{})
	require.Error(t, err)

	err = st.SaveWorkspaceMetadata(ctx, eng, ws, codeexecutor.WorkspaceMetadata{})
	require.Error(t, err)

	r := &stubRunner{}
	eng.r = r
	fs.putErr = fmt.Errorf("put fail")
	err = st.SaveWorkspaceMetadata(ctx, eng, ws, codeexecutor.WorkspaceMetadata{})
	require.Error(t, err)
	require.Equal(t, 1, fs.putCalls)
	require.Equal(t, 0, r.calls)
	require.Equal(t, workspaceMetadataTmpFile, fs.putFiles[0].Path)
	require.Equal(t, workspaceMetadataFileMode, fs.putFiles[0].Mode)

	fs.putErr = nil
	r.err = fmt.Errorf("run fail")
	err = st.SaveWorkspaceMetadata(ctx, eng, ws, codeexecutor.WorkspaceMetadata{})
	require.Error(t, err)
	require.Equal(t, 2, fs.putCalls)
	require.Equal(t, 1, r.calls)
	require.Equal(t, "bash", r.lastSpec.Cmd)
	require.Len(t, r.lastSpec.Args, 2)
	require.Contains(t, r.lastSpec.Args[1], "mv -f")
}

func TestStager_StageSkillAndLinks(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	ws, err := rt.CreateWorkspace(ctx, "stage-skill", codeexecutor.WorkspacePolicy{})
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Cleanup(ctx, ws)
	})

	root := t.TempDir()
	skillRoot := filepath.Join(root, "echoer")
	require.NoError(t, os.MkdirAll(skillRoot, 0o755))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillRoot, "SKILL.md"),
		[]byte("body"),
		0o644,
	))

	st := New()
	err = st.StageSkill(ctx, eng, ws, skillRoot, "echoer")
	require.NoError(t, err)

	files, err := rt.Collect(ctx, ws, []string{"skills/echoer/SKILL.md"})
	require.NoError(t, err)
	require.Len(t, files, 1)
	require.Equal(t, "body", files[0].Content)

	md, err := st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	meta, ok := md.Skills["echoer"]
	require.True(t, ok)
	require.Equal(t, "echoer", meta.Name)
	require.Equal(t, filepath.ToSlash("skills/echoer"), meta.RelPath)
	require.True(t, meta.Mounted)
	require.NotEmpty(t, meta.Digest)

	ok, err = st.SkillLinksPresent(ctx, eng, ws, "echoer")
	require.NoError(t, err)
	require.True(t, ok)

	checkSymlink := func(rel string) {
		t.Helper()
		fi, err := os.Lstat(filepath.Join(ws.Path, filepath.FromSlash(rel)))
		require.NoError(t, err)
		require.NotZero(t, fi.Mode()&os.ModeSymlink)
	}
	checkSymlink("skills/echoer/out")
	checkSymlink("skills/echoer/work")
	checkSymlink("skills/echoer/inputs")

	fi, err := os.Stat(filepath.Join(ws.Path, "skills", "echoer", ".venv"))
	require.NoError(t, err)
	require.True(t, fi.IsDir())

	// Staging the same skill again should be a no-op when links already exist.
	err = st.StageSkill(ctx, eng, ws, skillRoot, "echoer")
	require.NoError(t, err)
}

func TestSkillStagingHelpers_EarlyReturns(t *testing.T) {
	st := New()
	ctx := context.Background()
	ws := codeexecutor.Workspace{}

	ok, err := st.SkillLinksPresent(ctx, nil, ws, "")
	require.NoError(t, err)
	require.False(t, ok)

	ok, err = st.SkillLinksPresent(ctx, nil, ws, "echoer")
	require.Error(t, err)
	require.False(t, ok)

	require.NoError(t, st.RemoveWorkspacePath(ctx, nil, ws, ""))
	require.Error(t, st.RemoveWorkspacePath(ctx, nil, ws, "skills/echoer"))
}
