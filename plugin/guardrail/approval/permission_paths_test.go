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

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	agenttool "trpc.group/trpc-go/trpc-agent-go/tool/agent"
)

func TestGraphPermissionReviewsExecutedArguments(t *testing.T) {
	const finalArgs = `{ "value": "final", "id": 9007199254740993 }`
	for _, tc := range []struct {
		name          string
		checker, base tool.PermissionAction
		approve       bool
		wantOrder     []string
		wantStatus    string
	}{
		{"allow", tool.PermissionActionAllow, tool.PermissionActionAllow, true, []string{"callback", "checker", "base", "reviewer", "execute"}, ""},
		{"reviewer deny", tool.PermissionActionAllow, tool.PermissionActionAllow, false, []string{"callback", "checker", "base", "reviewer"}, "denied"},
		{"checker deny", tool.PermissionActionDeny, tool.PermissionActionAllow, true, []string{"callback", "checker"}, "denied"},
		{"checker ask", tool.PermissionActionAsk, tool.PermissionActionAllow, true, []string{"callback", "checker"}, "approval_required"},
		{"base deny", tool.PermissionActionAllow, tool.PermissionActionDeny, true, []string{"callback", "checker", "base"}, "denied"},
		{"base ask", tool.PermissionActionAllow, tool.PermissionActionAsk, true, []string{"callback", "checker", "base"}, "approval_required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var order []string
			var executed string
			leaf := &checkedTool{
				check: func(req *tool.PermissionRequest) (tool.PermissionDecision, error) {
					order = append(order, "checker")
					require.Equal(t, finalArgs, string(req.Arguments))
					// Neither in-place edits nor replacing fields may rewrite the
					// next check's input or the actual invocation.
					req.Arguments[0] = '!'
					req.Arguments = []byte(`{"value":"checker"}`)
					req.ToolName = "other"
					return tool.PermissionDecision{Action: tc.checker}, nil
				},
				call: func(args []byte) {
					order = append(order, "execute")
					executed = string(args)
				},
			}
			callbacks := tool.NewCallbacks()
			callbacks.RegisterBeforeTool(func(context.Context, *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
				order = append(order, "callback")
				return &tool.BeforeToolResult{ModifiedArguments: []byte(finalArgs)}, nil
			})
			gate, err := approval.New(approval.WithReviewer(reviewFunc(func(_ context.Context, req *review.Request) (*review.Decision, error) {
				order = append(order, "reviewer")
				require.Equal(t, "record", req.Action.ToolName)
				require.Equal(t, finalArgs, string(req.Action.Arguments))
				req.Action.Arguments[0] = '!'
				return &review.Decision{Approved: tc.approve}, nil
			})))
			require.NoError(t, err)
			base := tool.PermissionPolicyFunc(func(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
				order = append(order, "base")
				require.Equal(t, "record", req.ToolName)
				require.Equal(t, finalArgs, string(req.Arguments))
				req.Arguments[0] = '!'
				req.Arguments = []byte(`{"value":"base"}`)
				return tool.PermissionDecision{Action: tc.base}, nil
			})
			inv := agent.NewInvocation()
			inv.RunOptions = agent.NewRunOptions(agent.WithToolPermissionPolicy(gate.PermissionPolicy(base)))
			node := graph.NewToolsNodeFunc(map[string]tool.Tool{"record": leaf}, graph.WithToolCallbacks(callbacks))
			result, err := node(agent.NewInvocationContext(context.Background(), inv), graph.State{
				graph.StateKeyMessages: []model.Message{{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{
					ID: "graph-call", Type: "function", Function: model.FunctionDefinitionParam{Name: "record", Arguments: []byte(`{"value":"draft"}`)},
				}}}},
			})
			require.NoError(t, err)
			require.Equal(t, tc.wantOrder, order)
			if tc.wantStatus == "" {
				require.Equal(t, finalArgs, executed)
			} else {
				require.Empty(t, executed)
				data, err := json.Marshal(result)
				require.NoError(t, err)
				require.Contains(t, string(data), tc.wantStatus)
			}
		})
	}
}

func TestDynamicTemplateInheritsFinalPermission(t *testing.T) {
	const finalArgs = `{ "value": "final child", "id": 9007199254740993 }`
	for _, stream := range []bool{false, true} {
		for _, tc := range []struct {
			name    string
			base    tool.PermissionAction
			approve bool
		}{
			{"allow", tool.PermissionActionAllow, true},
			{"reviewer deny", tool.PermissionActionAllow, false},
			{"base deny", tool.PermissionActionDeny, true},
			{"base ask", tool.PermissionActionAsk, true},
		} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, tc.name), func(t *testing.T) {
				var mu sync.Mutex
				var reviewed, executed []string
				baseCalls := 0
				leaf := &checkedTool{
					check: func(req *tool.PermissionRequest) (tool.PermissionDecision, error) {
						require.Equal(t, finalArgs, string(req.Arguments))
						req.Arguments[0] = '!'
						req.Arguments = []byte(`{"value":"checker"}`)
						return tool.AllowPermission(), nil
					},
					call: func(args []byte) {
						mu.Lock()
						defer mu.Unlock()
						executed = append(executed, string(args))
					},
				}
				callbacks := tool.NewCallbacks()
				callbacks.RegisterBeforeTool(func(context.Context, *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
					return &tool.BeforeToolResult{ModifiedArguments: []byte(finalArgs)}, nil
				})
				child := llmagent.New("worker", llmagent.WithModel(callModel{"record", `{"value":"draft"}`}),
					llmagent.WithTools([]tool.Tool{leaf}), llmagent.WithToolCallbacks(callbacks), llmagent.WithMaxLLMCalls(3))
				delegated := agenttool.NewDynamicTool(agenttool.WithTemplateAgent(child),
					agenttool.WithCapabilityTools([]tool.Tool{leaf}), agenttool.WithStreamInner(stream))
				parent := llmagent.New("parent", llmagent.WithModel(callModel{delegated.Declaration().Name, `{"request":"Record a child value."}`}),
					llmagent.WithTools([]tool.Tool{delegated}), llmagent.WithMaxLLMCalls(3))
				gate, err := approval.New(approval.WithReviewer(reviewFunc(func(_ context.Context, req *review.Request) (*review.Decision, error) {
					if req.Action.ToolName != "record" {
						return &review.Decision{Approved: true}, nil
					}
					mu.Lock()
					defer mu.Unlock()
					reviewed = append(reviewed, string(req.Action.Arguments))
					require.Equal(t, finalArgs, string(req.Action.Arguments))
					host := false
					for i, entry := range req.Transcript {
						if entry.Content == "Ask the worker to record a value." && req.Context.TranscriptInfo[i].Origin == review.OriginUserInput {
							host = true
						}
					}
					require.True(t, host, "child reviews need the original host authorization")
					req.Action.Arguments[0] = '!'
					return &review.Decision{Approved: tc.approve}, nil
				})))
				require.NoError(t, err)
				base := tool.PermissionPolicyFunc(func(_ context.Context, req *tool.PermissionRequest) (tool.PermissionDecision, error) {
					if req.ToolName != "record" {
						return tool.AllowPermission(), nil
					}
					mu.Lock()
					defer mu.Unlock()
					baseCalls++
					require.Equal(t, finalArgs, string(req.Arguments))
					req.Arguments[0] = '!'
					return tool.PermissionDecision{Action: tc.base}, nil
				})
				r := runner.NewRunner("dynamic-permission-test", parent)
				defer r.Close()
				consumeRun(t, r, gate.PermissionPolicy(base), "Ask the worker to record a value.")
				mu.Lock()
				defer mu.Unlock()
				require.Equal(t, 1, baseCalls)
				if tc.base == tool.PermissionActionAllow {
					require.Equal(t, []string{finalArgs}, reviewed)
				} else {
					require.Empty(t, reviewed)
				}
				if tc.base == tool.PermissionActionAllow && tc.approve {
					require.Equal(t, reviewed, executed)
				} else {
					require.Empty(t, executed)
				}
			})
		}
	}
}
