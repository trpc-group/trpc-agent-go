//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const agentWorkspaceToolName = "review_workspace_plan"

type agentAudit struct {
	SkillLoaded        bool
	WorkspaceCalled    bool
	BundledContentSeen bool
	PermissionCalls    []string
}

type agentWorkspaceInput struct {
	PlanDigest string `json:"plan_digest"`
}

type agentWorkspaceTool struct {
	expected string
	called   bool
}

func (t *agentWorkspaceTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        agentWorkspaceToolName,
		Description: "Validate the permission-bound deterministic workspace plan.",
		InputSchema: &tool.Schema{Type: "object", Required: []string{"plan_digest"}, Properties: map[string]*tool.Schema{
			"plan_digest": {Type: "string"},
		}},
	}
}

func (t *agentWorkspaceTool) Call(_ context.Context, args []byte) (any, error) {
	var in agentWorkspaceInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, err
	}
	if in.PlanDigest != t.expected {
		return nil, fmt.Errorf("agent workspace plan digest mismatch")
	}
	t.called = true
	return map[string]string{"status": "validated"}, nil
}

func (t *agentWorkspaceTool) CheckPermission(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	var in agentWorkspaceInput
	if req == nil || json.Unmarshal(req.Arguments, &in) != nil || in.PlanDigest != t.expected {
		return tool.DenyPermission("workspace plan digest mismatch"), nil
	}
	return tool.AllowPermission(), nil
}

type agentScriptModel struct {
	planDigest         string
	step               int
	bundledContentSeen bool
}

func (m *agentScriptModel) Info() model.Info { return model.Info{Name: "code-review-scripted-model"} }

func (m *agentScriptModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.step++
	for _, msg := range req.Messages {
		if strings.Contains(msg.Content, "deterministic Go review rules") {
			m.bundledContentSeen = true
		}
	}
	var rsp *model.Response
	switch m.step {
	case 1:
		rsp = agentToolCall("skill-load", "skill_load", []byte(`{"skill":"code-review"}`))
	case 2:
		args, _ := json.Marshal(agentWorkspaceInput{PlanDigest: m.planDigest})
		rsp = agentToolCall("workspace-plan", agentWorkspaceToolName, args)
	default:
		rsp = &model.Response{ID: "done", Object: model.ObjectTypeChatCompletion, Created: time.Now().Unix(), Done: true, Choices: []model.Choice{{Index: 0, Message: model.Message{Role: model.RoleAssistant, Content: "deterministic review plan validated"}}}}
	}
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		select {
		case ch <- rsp:
		case <-ctx.Done():
		}
	}()
	return ch, nil
}

func agentToolCall(id, name string, args []byte) *model.Response {
	return &model.Response{ID: id, Object: model.ObjectTypeChatCompletion, Created: time.Now().Unix(), Done: true, Choices: []model.Choice{{Index: 0, Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{Type: "function", ID: id, Function: model.FunctionDefinitionParam{Name: name, Arguments: args}}}}}}}
}

func runAgentIntegration(ctx context.Context, skillPath, planDigest string, action tool.PermissionAction, record func(string, string) error) (agentAudit, error) {
	repo, err := skill.NewFSRepository(filepath.Dir(skillPath))
	if err != nil {
		return agentAudit{}, fmt.Errorf("load bundled skill repository: %w", err)
	}
	workspace := &agentWorkspaceTool{expected: planDigest}
	modelImpl := &agentScriptModel{planDigest: planDigest}
	agt := llmagent.New(
		"code-review-agent",
		llmagent.WithModel(modelImpl),
		llmagent.WithSkills(repo),
		llmagent.WithTools([]tool.Tool{workspace}),
		llmagent.WithEnableCodeExecutionResponseProcessor(false),
	)
	r := runner.NewRunner("code-review-example", agt, runner.WithSessionService(inmemory.NewSessionService()))
	defer r.Close()
	audit := agentAudit{}
	policy := tool.PermissionPolicyFunc(func(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
		audit.PermissionCalls = append(audit.PermissionCalls, req.ToolName)
		if record != nil {
			if err := record(req.ToolName, string(action)); err != nil {
				return tool.PermissionDecision{}, err
			}
		}
		return tool.PermissionDecision{Action: action, Reason: "configured agent tool policy"}, nil
	})
	events, err := r.Run(ctx, "review-user", "review-session", model.NewUserMessage("load the bundled skill and validate the review workspace plan"), agent.WithToolPermissionPolicy(policy))
	if err != nil {
		return audit, err
	}
	if err := drainAgentEvents(events); err != nil {
		return audit, err
	}
	audit.SkillLoaded = modelImpl.bundledContentSeen
	audit.WorkspaceCalled = workspace.called
	audit.BundledContentSeen = modelImpl.bundledContentSeen
	return audit, nil
}

func drainAgentEvents(events <-chan *event.Event) error {
	var integrationErr error
	for ev := range events {
		if ev != nil && ev.Error != nil && integrationErr == nil {
			integrationErr = fmt.Errorf("agent integration: %s", ev.Error.Message)
		}
	}
	return integrationErr
}
