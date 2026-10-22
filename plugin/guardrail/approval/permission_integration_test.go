//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package approval_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

type reviewFunc func(context.Context, *review.Request) (*review.Decision, error)

func (f reviewFunc) Review(ctx context.Context, req *review.Request) (*review.Decision, error) {
	return f(ctx, req)
}

type callModel struct {
	name string
	args string
}

func (m callModel) Info() model.Info { return model.Info{Name: "scripted-agent"} }
func (m callModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	msg := model.NewAssistantMessage("Finished.")
	if len(req.Messages) == 0 || req.Messages[len(req.Messages)-1].Role != model.RoleTool {
		msg = model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: "call-" + m.name, Type: "function", Function: model.FunctionDefinitionParam{Name: m.name, Arguments: []byte(m.args)}}}}
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Done: true, Choices: []model.Choice{{Message: msg}}}
	close(ch)
	return ch, nil
}

type checkedTool struct {
	check func(*tool.PermissionRequest) (tool.PermissionDecision, error)
	call  func([]byte)
}

func (*checkedTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "record", InputSchema: &tool.Schema{Type: "object", Properties: map[string]*tool.Schema{"value": {Type: "string"}}}}
}
func (t *checkedTool) CheckPermission(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
	return t.check(req)
}
func (t *checkedTool) Call(_ context.Context, args []byte) (any, error) {
	t.call(args)
	return "recorded", nil
}

func consumeRun(t *testing.T, r runner.Runner, policy tool.PermissionPolicy, prompt string) []*event.Event {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	events, err := r.Run(ctx, "user", "session", model.NewUserMessage(prompt), agent.WithToolPermissionPolicy(policy))
	require.NoError(t, err)
	var out []*event.Event
	for evt := range events {
		out = append(out, evt)
	}
	require.NoError(t, ctx.Err())
	return out
}

func TestFinalPermissionReviewsExecutedArguments(t *testing.T) {
	for _, decision := range []tool.PermissionAction{tool.PermissionActionAllow, tool.PermissionActionDeny, tool.PermissionActionAsk} {
		t.Run(string(decision), func(t *testing.T) {
			var mu sync.Mutex
			var order []string
			add := func(s string) { mu.Lock(); defer mu.Unlock(); order = append(order, s) }
			checked := &checkedTool{
				check: func(req *tool.PermissionRequest) (tool.PermissionDecision, error) {
					add("checker")
					require.JSONEq(t, `{"value":"final"}`, string(req.Arguments))
					// Even in-place mutation must not change the policy input or execution.
					copy(req.Arguments, []byte(`{"value":"other"}`))
					req.Declaration = &tool.Declaration{InputSchema: &tool.Schema{Type: "mutated"}}
					return tool.PermissionDecision{Action: decision}, nil
				},
				call: func(args []byte) { add("execute"); require.JSONEq(t, `{"value":"final"}`, string(args)) },
			}
			callbacks := tool.NewCallbacks()
			callbacks.RegisterBeforeTool(func(context.Context, *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
				add("callback")
				return &tool.BeforeToolResult{ModifiedArguments: []byte(`{"value":"final"}`)}, nil
			})
			p, err := approval.New(approval.WithReviewer(reviewFunc(func(_ context.Context, req *review.Request) (*review.Decision, error) {
				add("reviewer")
				require.JSONEq(t, `{"value":"final"}`, string(req.Action.Arguments))
				require.Equal(t, "object", req.Context.InputSchema.Type)
				require.Equal(t, "call-record", req.Context.ToolCallID)
				require.Equal(t, len(req.Transcript), len(req.Context.TranscriptInfo))
				found := false
				for i, line := range req.Transcript {
					if line.Content == "Record the final value." {
						found = true
						require.Equal(t, review.OriginUserInput, req.Context.TranscriptInfo[i].Origin)
					}
				}
				require.True(t, found)
				req.Action.Arguments[0] = '!'
				return &review.Decision{Approved: true}, nil
			})))
			require.NoError(t, err)
			base := tool.PermissionPolicyFunc(func(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
				add("base")
				require.JSONEq(t, `{"value":"final"}`, string(req.Arguments))
				req.Arguments[0] = '!'
				return tool.AllowPermission(), nil
			})
			ag := llmagent.New("main", llmagent.WithModel(callModel{"record", `{"value":"draft"}`}), llmagent.WithTools([]tool.Tool{checked}), llmagent.WithToolCallbacks(callbacks), llmagent.WithMaxLLMCalls(3))
			r := runner.NewRunner("permission-test", ag)
			defer r.Close()
			events := consumeRun(t, r, p.PermissionPolicy(base), "Record the final value.")
			mu.Lock()
			defer mu.Unlock()
			if decision == tool.PermissionActionAllow {
				require.Equal(t, []string{"callback", "checker", "base", "reviewer", "execute"}, order)
			} else {
				require.Equal(t, []string{"callback", "checker"}, order)
				data, err := json.Marshal(events)
				require.NoError(t, err)
				status := "denied"
				if decision == tool.PermissionActionAsk {
					status = "approval_required"
				}
				require.Contains(t, string(data), status)
			}
		})
	}
}

func TestDelegatedAgentInheritsPolicyWithoutBecomingUser(t *testing.T) {
	t.Run("original", func(t *testing.T) { testDelegatedAgentInheritsPolicy(t) })
	t.Run("replacement-plugin", func(t *testing.T) {
		testDelegatedAgentInheritsPolicy(t, runner.WithPlugins(replaceUserEventPlugin{}))
	})
}

func testDelegatedAgentInheritsPolicy(t *testing.T, opts ...runner.Option) {
	t.Helper()
	var mu sync.Mutex
	var reviews []*review.Request
	var executions int
	leaf := &checkedTool{check: func(*tool.PermissionRequest) (tool.PermissionDecision, error) { return tool.AllowPermission(), nil }, call: func([]byte) { mu.Lock(); defer mu.Unlock(); executions++ }}
	child := llmagent.New("worker", llmagent.WithModel(callModel{"record", `{"value":"child"}`}), llmagent.WithTools([]tool.Tool{leaf}), llmagent.WithMaxLLMCalls(3))
	delegated := agenttool.NewTool(child)
	parent := llmagent.New("parent", llmagent.WithModel(callModel{delegated.Declaration().Name, `{"request":"Record a child value."}`}), llmagent.WithTools([]tool.Tool{delegated}), llmagent.WithMaxLLMCalls(3))
	p, err := approval.New(approval.WithReviewer(reviewFunc(func(_ context.Context, req *review.Request) (*review.Decision, error) {
		mu.Lock()
		defer mu.Unlock()
		reviews = append(reviews, req)
		return &review.Decision{Approved: true}, nil
	})))
	require.NoError(t, err)
	r := runner.NewRunner("delegation-test", parent, opts...)
	defer r.Close()
	consumeRun(t, r, p.PermissionPolicy(nil), "Ask the worker to record a value.")
	mu.Lock()
	defer mu.Unlock()
	require.Equal(t, 1, executions)
	require.Len(t, reviews, 2)
	req := reviews[1]
	require.Equal(t, "record", req.Action.ToolName)
	var host, delegation bool
	for i, entry := range req.Transcript {
		info := req.Context.TranscriptInfo[i]
		if entry.Role != model.RoleUser {
			continue
		}
		switch info.Origin {
		case review.OriginUserInput:
			host = true
			require.Equal(t, "Ask the worker to record a value.", entry.Content)
		case review.OriginAgent:
			delegation = true
			require.NotEmpty(t, info.ParentInvocationID)
		default:
			t.Errorf("unexpected source for %s: %s", entry.Content, info.Origin)
		}
	}
	require.True(t, host, "host authorization must remain visible")
	require.True(t, delegation, fmt.Sprintf("missing delegated input in %#v", req.Transcript))
}
