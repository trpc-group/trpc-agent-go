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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	specs    []codeexecutor.RunProgramSpec
}

func (r *stubRunner) RunProgram(
	_ context.Context,
	_ codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (codeexecutor.RunResult, error) {
	r.calls++
	r.lastSpec = spec
	r.specs = append(r.specs, spec)
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

	fs.collectFiles = []codeexecutor.File{{
		Name:    codeexecutor.MetaFileName,
		Content: "not-json}",
	}}
	md, err = st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Equal(t, 1, md.Version)
	require.NotNil(t, md.Skills)

	err = st.SaveWorkspaceMetadata(
		ctx, nil, ws, codeexecutor.WorkspaceMetadata{},
	)
	require.Error(t, err)

	err = st.SaveWorkspaceMetadata(
		ctx, eng, ws, codeexecutor.WorkspaceMetadata{},
	)
	require.Error(t, err)

	r := &stubRunner{}
	eng.r = r
	fs.putErr = fmt.Errorf("put fail")
	err = st.SaveWorkspaceMetadata(
		ctx, eng, ws, codeexecutor.WorkspaceMetadata{},
	)
	require.Error(t, err)
	require.Equal(t, 1, fs.putCalls)
	require.Equal(t, 1, r.calls)
	const (
		metadataTmpPrefix = ".metadata."
		metadataTmpSuffix = ".tmp"
	)
	require.True(t, strings.HasPrefix(
		fs.putFiles[0].Path,
		metadataTmpPrefix,
	))
	require.True(t, strings.HasSuffix(
		fs.putFiles[0].Path,
		metadataTmpSuffix,
	))
	require.Equal(t, workspaceMetadataFileMode, fs.putFiles[0].Mode)
	require.Contains(t, r.specs[0].Args[1], "rm -f")

	fs.putErr = nil
	r.err = fmt.Errorf("run fail")
	err = st.SaveWorkspaceMetadata(
		ctx, eng, ws, codeexecutor.WorkspaceMetadata{},
	)
	require.Error(t, err)
	require.Equal(t, 2, fs.putCalls)
	require.Equal(t, 3, r.calls)
	require.Equal(t, "bash", r.specs[1].Cmd)
	require.Len(t, r.specs[1].Args, 2)
	require.Contains(t, r.specs[1].Args[1], "mv -f")
	require.Contains(
		t,
		r.specs[1].Args[1],
		"metadata path is a directory",
	)
	require.Contains(t, r.specs[2].Args[1], "rm -f")

	r.err = nil
	r.res = codeexecutor.RunResult{ExitCode: 1, Stderr: "mv fail"}
	err = st.SaveWorkspaceMetadata(
		ctx, eng, ws, codeexecutor.WorkspaceMetadata{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "exit code 1")
}

func TestStager_SaveWorkspaceMetadata_MetadataDirectoryFails(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	ws, err := rt.CreateWorkspace(
		ctx, "stage-metadata-dir", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Cleanup(ctx, ws) })

	require.NoError(
		t,
		os.Remove(filepath.Join(ws.Path, codeexecutor.MetaFileName)),
	)
	require.NoError(
		t,
		os.Mkdir(filepath.Join(ws.Path, codeexecutor.MetaFileName), 0o755),
	)

	err = New().SaveWorkspaceMetadata(
		ctx,
		eng,
		ws,
		codeexecutor.WorkspaceMetadata{},
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "metadata path is a directory")

	matches, err := filepath.Glob(filepath.Join(ws.Path, ".metadata.*.tmp"))
	require.NoError(t, err)
	require.Empty(t, matches)
}

func TestCleanupMetadataTemp_NoRunnerOrPath(t *testing.T) {
	cleanupMetadataTemp(
		context.Background(),
		nil,
		codeexecutor.Workspace{},
		"tmp",
		nil,
	)
	cleanupMetadataTemp(
		context.Background(),
		&stubEngine{},
		codeexecutor.Workspace{},
		"",
		nil,
	)
}

func TestRunProgramExitError_ReturnsRunnerError(t *testing.T) {
	runnerErr := fmt.Errorf("runner failed")
	err := runProgramExitError(
		"op",
		codeexecutor.RunResult{},
		runnerErr,
	)

	require.ErrorIs(t, err, runnerErr)
}

func TestStager_StageSkillAndLinks(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	ws, err := rt.CreateWorkspace(
		ctx, "stage-skill", codeexecutor.WorkspacePolicy{},
	)
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

func TestStager_StageSkillConcurrentMetadataSafe(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	ws, err := rt.CreateWorkspace(
		ctx,
		"stage-skill-concurrent",
		codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = rt.Cleanup(ctx, ws)
	})

	const (
		skillCount    = 12
		skillFileName = "SKILL.md"
	)
	root := t.TempDir()
	names := make([]string, 0, skillCount)
	for i := 0; i < skillCount; i++ {
		name := fmt.Sprintf("skill_%02d", i)
		names = append(names, name)
		skillRoot := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(skillRoot, 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(skillRoot, skillFileName),
			[]byte(name),
			0o644,
		))
	}

	st := New()
	start := make(chan struct{})
	errs := make(chan error, skillCount)
	for _, name := range names {
		name := name
		go func() {
			<-start
			errs <- st.StageSkill(
				ctx, eng, ws, filepath.Join(root, name), name,
			)
		}()
	}
	close(start)
	for range names {
		require.NoError(t, <-errs)
	}

	raw, err := os.ReadFile(filepath.Join(ws.Path, codeexecutor.MetaFileName))
	require.NoError(t, err)
	require.True(t, json.Valid(raw))

	md, err := st.LoadWorkspaceMetadata(ctx, eng, ws)
	require.NoError(t, err)
	require.Len(t, md.Skills, skillCount)
	for _, name := range names {
		meta, ok := md.Skills[name]
		require.True(t, ok)
		require.Equal(t, name, meta.Name)
		require.True(t, meta.Mounted)
		ok, err := st.SkillLinksPresent(ctx, eng, ws, name)
		require.NoError(t, err)
		require.True(t, ok)
	}
}

// TestStager_StageSkillWithOptionsReadOnly exercises the legacy
// ReadOnly staging path used by the phased-out skill_run tool. The
// default path is already covered by TestStager_StageSkillAndLinks;
// this test additionally triggers readOnlyExceptSymlinks so the
// framework does not silently regress the old contract, and gives
// the chmod walk non-trivial coverage.
func TestStager_StageSkillWithOptionsReadOnly(t *testing.T) {
	ctx := context.Background()
	rt := localexec.NewRuntime("")
	eng := codeexecutor.NewEngine(rt, rt, rt)
	ws, err := rt.CreateWorkspace(
		ctx, "stage-skill-ro", codeexecutor.WorkspacePolicy{},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = rt.Cleanup(ctx, ws) })

	root := t.TempDir()
	skillRoot := filepath.Join(root, "echoer")
	require.NoError(t, os.MkdirAll(
		filepath.Join(skillRoot, "nested"), 0o755,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillRoot, "SKILL.md"),
		[]byte("body"),
		0o644,
	))
	require.NoError(t, os.WriteFile(
		filepath.Join(skillRoot, "nested", "helper.sh"),
		[]byte("echo hi"),
		0o755,
	))

	st := New()
	err = st.StageSkillWithOptions(
		ctx, eng, ws, skillRoot, "echoer",
		StageOptions{ReadOnly: true},
	)
	require.NoError(t, err)

	// Regular files under the staged tree should have the write bit
	// cleared after readOnlyExceptSymlinks runs.
	fi, err := os.Stat(filepath.Join(ws.Path, "skills", "echoer", "SKILL.md"))
	require.NoError(t, err)
	require.Zero(t, fi.Mode()&0o200,
		"read-only staging must clear the owner-write bit on regular files")

	// Symlinks must stay intact.
	fi, err = os.Lstat(filepath.Join(ws.Path, "skills", "echoer", "work"))
	require.NoError(t, err)
	require.NotZero(t, fi.Mode()&os.ModeSymlink)
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

func TestSkillStagingHelpers_ReturnExitCodeErrors(t *testing.T) {
	st := New()
	ctx := context.Background()
	ws := codeexecutor.Workspace{}
	r := &stubRunner{
		res: codeexecutor.RunResult{
			ExitCode: 1,
			Stderr:   "shell failed",
		},
	}
	eng := &stubEngine{r: r}

	err := st.linkWorkspaceDirs(ctx, eng, ws, "echoer")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exit code 1")

	err = st.RemoveWorkspacePath(ctx, eng, ws, "skills/echoer")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exit code 1")

	err = st.readOnlyExceptSymlinks(ctx, eng, ws, "skills/echoer")
	require.Error(t, err)
	require.Contains(t, err.Error(), "exit code 1")
}
