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
	"trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

type workspaceExecStubRepo struct{}

func (workspaceExecStubRepo) Summaries() []skill.Summary {
	return nil
}

func (workspaceExecStubRepo) Get(string) (*skill.Skill, error) {
	return nil, nil
}

func (workspaceExecStubRepo) Path(string) (string, error) {
	return "", nil
}

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
	require.NotContains(t, sys, "workspace_save_artifact")
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
		&agent.Invocation{
			AgentName: "tester",
			Session: &session.Session{
				ID:      "sess",
				AppName: "app",
				UserID:  "user",
			},
			ArtifactService: inmemory.NewService(),
		},
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
	require.Contains(t, sys, "workspace_save_artifact")
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

func TestWorkspaceExecRequestProcessor_ProcessRequest_UsesSkillsRepoResolver(
	t *testing.T,
) {
	p := NewWorkspaceExecRequestProcessor(
		WithWorkspaceExecSkillsRepositoryResolver(
			func(*agent.Invocation) skill.Repository {
				return workspaceExecStubRepo{}
			},
		),
	)
	req := &model.Request{}

	p.ProcessRequest(
		context.Background(),
		&agent.Invocation{AgentName: "tester"},
		req,
		nil,
	)

	require.NotEmpty(t, req.Messages)
	require.Contains(t, req.Messages[0].Content, "Paths under skills/")
}

func TestWorkspaceExecRequestProcessor_ProcessRequest_ResolverCanDisableSkillsGuidance(
	t *testing.T,
) {
	p := NewWorkspaceExecRequestProcessor(
		WithWorkspaceExecSkillsRepo(),
		WithWorkspaceExecSkillsRepositoryResolver(
			func(*agent.Invocation) skill.Repository {
				return nil
			},
		),
	)
	req := &model.Request{}

	p.ProcessRequest(
		context.Background(),
		&agent.Invocation{AgentName: "tester"},
		req,
		nil,
	)

	require.NotEmpty(t, req.Messages)
	require.NotContains(t, req.Messages[0].Content, "Paths under skills/")
}

func TestWorkspaceExecRequestProcessor_ProcessRequest_DisabledByResolver(
	t *testing.T,
) {
	p := NewWorkspaceExecRequestProcessor(
		WithWorkspaceExecEnabledResolver(
			func(*agent.Invocation) bool {
				return false
			},
		),
	)
	req := &model.Request{}

	p.ProcessRequest(
		context.Background(),
		&agent.Invocation{AgentName: "tester"},
		req,
		nil,
	)

	require.Empty(t, req.Messages)
}

func TestWorkspaceExecRequestProcessor_ProcessRequest_SessionToolsEnabledByResolver(
	t *testing.T,
) {
	p := NewWorkspaceExecRequestProcessor(
		WithWorkspaceExecSessionsResolver(
			func(*agent.Invocation) bool {
				return true
			},
		),
	)
	req := &model.Request{}

	p.ProcessRequest(
		context.Background(),
		&agent.Invocation{AgentName: "tester"},
		req,
		nil,
	)

	require.NotEmpty(t, req.Messages)
	require.Contains(t, req.Messages[0].Content, "workspace_write_stdin")
	require.Contains(t, req.Messages[0].Content, "workspace_kill_session")
}
