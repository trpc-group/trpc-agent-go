//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package processor

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestWorkspaceExecRequestProcessor_ProcessRequest_NoSkillsRepo(
	t *testing.T,
) {
	p := NewWorkspaceExecRequestProcessor()
	req := &model.Request{}

	p.ProcessRequest(
		context.Background(),
		&agent.Invocation{AgentName: "tester"},
		req,
		nil,
	)

	require.NotEmpty(t, req.Messages)
	require.Equal(t, model.RoleSystem, req.Messages[0].Role)
	sys := req.Messages[0].Content
	require.Contains(t, sys, workspaceExecGuidanceHeader)
	require.Contains(t, sys, "default general shell runner")
	require.Contains(t, sys, "workspace is its scope, not its capability limit")
	require.Contains(t, sys, "Prefer work/, out/, and runs/")
	require.Contains(t, sys, "Network access depends on the current executor environment")
	require.Contains(t, sys, "verify first before claiming the limitation")
	require.Contains(t, sys, "command availability, file presence, or access to a known URL")
	require.Contains(t, sys, "workspace_publish_artifact")
	require.Contains(t, sys, "download, open, or preview")
	require.Contains(t, sys, "artifact service or session info is not configured")
	require.NotContains(t, sys, "skills/")
	require.NotContains(t, sys, "workspace_write_stdin")
}

func TestWorkspaceExecRequestProcessor_ProcessRequest_InteractiveWithSkillsRepo(
	t *testing.T,
) {
	p := NewWorkspaceExecRequestProcessor(
		WithWorkspaceExecSessionsEnabled(),
		WithWorkspaceExecSkillsRepo(),
	)
	req := &model.Request{Messages: []model.Message{
		model.NewSystemMessage("base"),
	}}

	p.ProcessRequest(
		context.Background(),
		&agent.Invocation{AgentName: "tester"},
		req,
		nil,
	)

	require.Len(t, req.Messages, 1)
	sys := req.Messages[0].Content
	require.Contains(t, sys, "base")
	require.Contains(t, sys, workspaceExecGuidanceHeader)
	require.Contains(t, sys, "default general shell runner")
	require.Contains(t, sys, "workspace is its scope, not its capability limit")
	require.Contains(t, sys, "Network access depends on the current executor environment")
	require.Contains(t, sys, "verify first before claiming the limitation")
	require.Contains(t, sys, "command availability, file presence, or access to a known URL")
	require.Contains(t, sys, "workspace_publish_artifact")
	require.Contains(t, sys, "download, open, or preview")
	require.Contains(t, sys, "artifact service or session info is not configured")
	require.Contains(t, sys, "Paths under skills/")
	require.Contains(t, sys, "workspace_write_stdin")
	require.Contains(t, sys, "workspace_kill_session")
	require.Contains(t, sys, "current invocation")
}

func TestWorkspaceExecRequestProcessor_NoDuplicateGuidance(t *testing.T) {
	p := NewWorkspaceExecRequestProcessor()
	req := &model.Request{}
	inv := &agent.Invocation{AgentName: "tester"}

	p.ProcessRequest(context.Background(), inv, req, nil)
	p.ProcessRequest(context.Background(), inv, req, nil)

	require.Len(t, req.Messages, 1)
	require.Equal(t, 1, strings.Count(req.Messages[0].Content, workspaceExecGuidanceHeader))
}
