//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package workspaceexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

func TestPublishArtifactTool_PublishesExistingFile(t *testing.T) {
	exec := localexec.New()
	reg := codeexecutor.NewWorkspaceRegistry()
	execTool := NewExecTool(exec, WithWorkspaceRegistry(reg))
	tl := NewPublishArtifactTool(execTool)
	svc := inmemory.NewService()
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-publish",
			AppName: "app",
			UserID:  "user",
		}),
		agent.WithInvocationArtifactService(svc),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	eng := execTool.resolver.EnsureEngine()
	ws, err := execTool.resolver.CreateWorkspace(ctx, eng, "workspace")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(
		filepath.Join(ws.Path, codeexecutor.DirOut),
		0o755,
	))
	path := filepath.Join(ws.Path, codeexecutor.DirOut, "site.zip")
	data := []byte("zip-payload")
	require.NoError(t, os.WriteFile(path, data, 0o644))

	enc, err := json.Marshal(publishArtifactInput{Path: "out/site.zip"})
	require.NoError(t, err)

	res, err := tl.Call(ctx, enc)
	require.NoError(t, err)
	out := res.(publishArtifactOutput)
	require.Equal(t, "out/site.zip", out.Path)
	require.Equal(t, "out/site.zip", out.SavedAs)
	require.Equal(t, 0, out.Version)
	require.Equal(t, "artifact://out/site.zip@0", out.Ref)
	require.EqualValues(t, len(data), out.SizeBytes)

	art, err := svc.LoadArtifact(ctx, artifact.SessionInfo{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-publish",
	}, "out/site.zip", nil)
	require.NoError(t, err)
	require.NotNil(t, art)
	require.Equal(t, data, art.Data)
}

func TestPublishArtifactTool_RequiresArtifactService(t *testing.T) {
	exec := localexec.New()
	tl := NewPublishArtifactTool(NewExecTool(exec))
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-publish",
			AppName: "app",
			UserID:  "user",
		}),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	enc, err := json.Marshal(publishArtifactInput{Path: "out/site.zip"})
	require.NoError(t, err)

	_, err = tl.Call(ctx, enc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "artifact service is not configured")
}

func TestPublishArtifactTool_RejectsGlobPath(t *testing.T) {
	tl := NewPublishArtifactTool(NewExecTool(localexec.New()))
	enc, err := json.Marshal(publishArtifactInput{Path: "out/*.zip"})
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), enc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "must not contain glob patterns")
}

func TestPublishArtifactTool_RejectsSkillsPath(t *testing.T) {
	tl := NewPublishArtifactTool(NewExecTool(localexec.New()))
	enc, err := json.Marshal(
		publishArtifactInput{Path: "skills/demo/out/site.zip"},
	)
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), enc)
	require.Error(t, err)
	require.Contains(
		t,
		err.Error(),
		"path must stay under supported publish roots such as work/, out/, or runs/",
	)
}

func TestPublishArtifactTool_StateDelta(t *testing.T) {
	tl := NewPublishArtifactTool(NewExecTool(localexec.New()))
	resultJSON := []byte(`{
		"path":"out/site.zip",
		"saved_as":"out/site.zip",
		"version":2,
		"ref":"artifact://out/site.zip@2",
		"mime_type":"application/zip",
		"size_bytes":17139
	}`)

	delta := tl.StateDelta("call-1", nil, resultJSON)
	require.Len(t, delta, 1)

	payload, ok := delta[skill.StateKeyArtifacts]
	require.True(t, ok)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(payload, &parsed))
	require.Equal(t, "call-1", parsed["tool_call_id"])

	artifacts, ok := parsed["artifacts"].([]any)
	require.True(t, ok)
	require.Len(t, artifacts, 1)

	artifactMap, ok := artifacts[0].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "out/site.zip", artifactMap["name"])
	require.Equal(t, float64(2), artifactMap["version"])
	require.Equal(t, "artifact://out/site.zip@2", artifactMap["ref"])
}

func TestPublishArtifactTool_Declaration(t *testing.T) {
	tl := NewPublishArtifactTool(NewExecTool(localexec.New()))

	decl := tl.Declaration()
	require.NotNil(t, decl)
	require.Equal(t, "workspace_publish_artifact", decl.Name)
	require.Contains(t, decl.Description, "final deliverables")
	require.Contains(t, decl.Description, "work/, out/, or runs/")
}

func TestPublishArtifactTool_RequiresInvocationContext(t *testing.T) {
	tl := NewPublishArtifactTool(NewExecTool(localexec.New()))
	enc, err := json.Marshal(publishArtifactInput{Path: "out/site.zip"})
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), enc)
	require.Error(t, err)
	require.Contains(t, err.Error(), publishReasonNoInvocation)
}

func TestPublishArtifactTool_RequiresCompleteSessionIDs(t *testing.T) {
	exec := localexec.New()
	tl := NewPublishArtifactTool(NewExecTool(exec))
	svc := inmemory.NewService()
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{ID: "sess-only"}),
		agent.WithInvocationArtifactService(svc),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)
	enc, err := json.Marshal(publishArtifactInput{Path: "out/site.zip"})
	require.NoError(t, err)

	_, err = tl.Call(ctx, enc)
	require.Error(t, err)
	require.Contains(t, err.Error(), publishReasonNoSessionIDs)
}

func TestPublishArtifactTool_NormalizesPathVariants(t *testing.T) {
	rel, err := normalizeArtifactPath("/out/site.zip")
	require.NoError(t, err)
	require.Equal(t, "out/site.zip", rel)

	rel, err = normalizeArtifactPath("${OUTPUT_DIR}/site.zip")
	require.NoError(t, err)
	require.Equal(t, "out/site.zip", rel)
}

func TestPublishArtifactTool_NormalizePathValidation(t *testing.T) {
	_, err := normalizeArtifactPath("")
	require.Error(t, err)
	require.Contains(t, err.Error(), "path is required")

	_, err = normalizeArtifactPath("/")
	require.Error(t, err)
	require.Contains(t, err.Error(), "inside the workspace")

	_, err = normalizeArtifactPath("../site.zip")
	require.Error(t, err)
	require.Contains(t, err.Error(), "stay within the workspace")

	_, err = normalizeArtifactPath("tmp/site.zip")
	require.Error(t, err)
	require.Contains(t, err.Error(), "supported publish roots")
}

func TestPublishArtifactTool_RejectsMissingFile(t *testing.T) {
	exec := localexec.New()
	reg := codeexecutor.NewWorkspaceRegistry()
	execTool := NewExecTool(exec, WithWorkspaceRegistry(reg))
	tl := NewPublishArtifactTool(execTool)
	svc := inmemory.NewService()
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-publish-missing",
			AppName: "app",
			UserID:  "user",
		}),
		agent.WithInvocationArtifactService(svc),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	eng := execTool.resolver.EnsureEngine()
	_, err := execTool.resolver.CreateWorkspace(ctx, eng, "workspace")
	require.NoError(t, err)

	enc, err := json.Marshal(publishArtifactInput{Path: "out/missing.zip"})
	require.NoError(t, err)

	_, err = tl.Call(ctx, enc)
	require.Error(t, err)
	require.Contains(t, err.Error(), "workspace artifact file not found")
}

func TestPublishArtifactTool_ManifestFailures(t *testing.T) {
	t.Run("multiple matches", func(t *testing.T) {
		tl := NewPublishArtifactTool(newStubExecTool(
			stubOutputFS{manifest: codeexecutor.OutputManifest{
				Files: []codeexecutor.FileRef{
					{Name: "out/a.zip"},
					{Name: "out/b.zip"},
				},
			}},
		))
		ctx := publishArtifactContext()
		enc, err := json.Marshal(publishArtifactInput{Path: "out/site.zip"})
		require.NoError(t, err)

		_, err = tl.Call(ctx, enc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "matched 2 files")
	})

	t.Run("save omitted", func(t *testing.T) {
		tl := NewPublishArtifactTool(newStubExecTool(
			stubOutputFS{manifest: codeexecutor.OutputManifest{
				Files: []codeexecutor.FileRef{{Name: "out/site.zip"}},
			}},
		))
		ctx := publishArtifactContext()
		enc, err := json.Marshal(publishArtifactInput{Path: "out/site.zip"})
		require.NoError(t, err)

		_, err = tl.Call(ctx, enc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "was not persisted")
	})

	t.Run("collect outputs error", func(t *testing.T) {
		tl := NewPublishArtifactTool(newStubExecTool(
			stubOutputFS{err: errors.New("boom")},
		))
		ctx := publishArtifactContext()
		enc, err := json.Marshal(publishArtifactInput{Path: "out/site.zip"})
		require.NoError(t, err)

		_, err = tl.Call(ctx, enc)
		require.Error(t, err)
		require.Contains(t, err.Error(), "boom")
	})
}

func TestPublishArtifactTool_StateDeltaFallbacks(t *testing.T) {
	tl := NewPublishArtifactTool(NewExecTool(localexec.New()))

	require.Nil(t, tl.StateDelta("", nil, []byte(`{}`)))
	require.Nil(t, tl.StateDelta("call-1", nil, []byte(`not-json`)))

	resultJSON := []byte(`{
		"path":"out/site.zip",
		"saved_as":"out/site.zip",
		"version":3,
		"size_bytes":17139
	}`)
	delta := tl.StateDelta("call-2", nil, resultJSON)
	payload, ok := delta[skill.StateKeyArtifacts]
	require.True(t, ok)

	var parsed map[string]any
	require.NoError(t, json.Unmarshal(payload, &parsed))
	artifacts := parsed["artifacts"].([]any)
	artifactMap := artifacts[0].(map[string]any)
	require.Equal(t, "artifact://out/site.zip@3", artifactMap["ref"])

	require.Nil(t, tl.StateDelta("call-3", nil, []byte(`{"saved_as":"","version":1}`)))
	require.Nil(t, tl.StateDelta("call-4", nil, []byte(`{"saved_as":"out/site.zip","version":-1}`)))
}

func TestPublishArtifactTool_ArtifactContextHelpers(t *testing.T) {
	require.Equal(t, publishReasonNoInvocation, artifactPublishSkipReason(context.Background()))

	noSvc := agent.NewInvocationContext(context.Background(), agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess",
			AppName: "app",
			UserID:  "user",
		}),
	))
	require.Equal(t, publishReasonNoService, artifactPublishSkipReason(noSvc))

	noSession := agent.NewInvocationContext(context.Background(), agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationArtifactService(inmemory.NewService()),
	))
	require.Equal(t, publishReasonNoSession, artifactPublishSkipReason(noSession))

	incompleteSession := agent.NewInvocationContext(context.Background(), agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationArtifactService(inmemory.NewService()),
		agent.WithInvocationSession(&session.Session{ID: "sess"}),
	))
	require.Equal(t, publishReasonNoSessionIDs, artifactPublishSkipReason(incompleteSession))

	ctx := publishArtifactContext()
	require.Equal(t, "", artifactPublishSkipReason(ctx))

	ctx = withArtifactContext(ctx)
	_, ok := codeexecutor.ArtifactServiceFromContext(ctx)
	require.True(t, ok)
	_, err := codeexecutor.SaveArtifactHelper(ctx, "out/site.zip", []byte("payload"), "text/plain")
	require.NoError(t, err)
}

func publishArtifactContext() context.Context {
	svc := inmemory.NewService()
	inv := agent.NewInvocation(
		agent.WithInvocationMessage(model.NewUserMessage("hi")),
		agent.WithInvocationSession(&session.Session{
			ID:      "sess-publish-stub",
			AppName: "app",
			UserID:  "user",
		}),
		agent.WithInvocationArtifactService(svc),
	)
	return agent.NewInvocationContext(context.Background(), inv)
}

func newStubExecTool(fs codeexecutor.WorkspaceFS) *ExecTool {
	exec := &stubEngineExec{
		eng: codeexecutor.NewEngine(&nonInteractiveMgr{}, fs, &nonInteractiveRunner{}),
	}
	return NewExecTool(exec)
}

type stubEngineExec struct {
	eng codeexecutor.Engine
}

func (s *stubEngineExec) ExecuteCode(context.Context, codeexecutor.CodeExecutionInput) (codeexecutor.CodeExecutionResult, error) {
	return codeexecutor.CodeExecutionResult{}, nil
}

func (s *stubEngineExec) CodeBlockDelimiter() codeexecutor.CodeBlockDelimiter {
	return codeexecutor.CodeBlockDelimiter{Start: "```", End: "```"}
}

func (s *stubEngineExec) Engine() codeexecutor.Engine { return s.eng }

type stubOutputFS struct {
	manifest codeexecutor.OutputManifest
	err      error
}

func (f stubOutputFS) PutFiles(context.Context, codeexecutor.Workspace, []codeexecutor.PutFile) error {
	return nil
}

func (f stubOutputFS) StageDirectory(context.Context, codeexecutor.Workspace, string, string, codeexecutor.StageOptions) error {
	return nil
}

func (f stubOutputFS) Collect(context.Context, codeexecutor.Workspace, []string) ([]codeexecutor.File, error) {
	return nil, nil
}

func (f stubOutputFS) StageInputs(context.Context, codeexecutor.Workspace, []codeexecutor.InputSpec) error {
	return nil
}

func (f stubOutputFS) CollectOutputs(context.Context, codeexecutor.Workspace, codeexecutor.OutputSpec) (codeexecutor.OutputManifest, error) {
	return f.manifest, f.err
}
