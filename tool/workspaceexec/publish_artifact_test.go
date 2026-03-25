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
