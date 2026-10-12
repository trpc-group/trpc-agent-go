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
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/taskrun"
	"trpc.group/trpc-go/trpc-agent-go/agent/taskrun/inprocess"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	tasktool "trpc.group/trpc-go/trpc-agent-go/tool/taskrun"
)

type taskrunModel struct {
	callModel
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (m *taskrunModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.once.Do(func() { close(m.started) })
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-m.release:
		return m.callModel.GenerateContent(ctx, req)
	}
}

func TestTaskrunInheritsPermissionAndUserEvidence(t *testing.T) {
	const hostInput = "Ask the worker to record a value locally; never upload it."
	const delegatedInput = "Record a child value."
	const finalArgs = `{ "value": "final child", "id": 9007199254740993 }`
	for _, mode := range []string{"sync", "async"} {
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
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				var mu sync.Mutex
				var reviewed []*review.Request
				var executed []string
				baseCalls := 0
				leaf := &checkedTool{
					check: func(req *tool.PermissionRequest) (tool.PermissionDecision, error) {
						require.Equal(t, finalArgs, string(req.Arguments))
						req.Arguments[0] = '!'
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
				gate, err := approval.New(approval.WithReviewer(reviewFunc(func(_ context.Context, req *review.Request) (*review.Decision, error) {
					if req.Action.ToolName != "record" {
						return &review.Decision{Approved: true}, nil
					}
					mu.Lock()
					defer mu.Unlock()
					reviewed = append(reviewed, req)
					require.Equal(t, finalArgs, string(req.Action.Arguments))
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
				workerModel := &taskrunModel{callModel: callModel{"record", `{"value":"draft"}`},
					started: make(chan struct{}), release: make(chan struct{})}
				if mode == "sync" {
					close(workerModel.release)
				}
				child := llmagent.New("worker", llmagent.WithModel(workerModel), llmagent.WithTools([]tool.Tool{leaf}),
					llmagent.WithToolCallbacks(callbacks), llmagent.WithMaxLLMCalls(3))
				worker := runner.NewRunner("taskrun-worker", child)
				defer worker.Close()
				service, err := inprocess.NewService(worker)
				require.NoError(t, err)
				service.Start(ctx)
				defer service.Close()
				tools := tasktool.NewTools(service)
				parent := llmagent.New("parent", llmagent.WithModel(callModel{"start_task_run",
					fmt.Sprintf(`{"task":%q,"mode":%q}`, delegatedInput, mode)}),
					llmagent.WithTools(tools.All()), llmagent.WithMaxLLMCalls(3))
				r := runner.NewRunner("taskrun-parent", parent)
				defer r.Close()
				parentCtx, cancelParent := context.WithCancel(ctx)
				defer cancelParent()
				events, err := r.Run(parentCtx, "user", "session", model.NewUserMessage(hostInput),
					agent.WithToolPermissionPolicy(gate.PermissionPolicy(base)))
				require.NoError(t, err)
				for range events {
				}
				if mode == "async" {
					select {
					case <-workerModel.started:
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					}
					// Background work must retain its permission evidence after
					// the parent invocation and its cancellation lifetime end.
					cancelParent()
					close(workerModel.release)
				}
				runs, err := service.List(ctx, taskrun.ListFilter{})
				require.NoError(t, err)
				require.Len(t, runs, 1)
				final, err := service.Wait(ctx, runs[0].ID)
				require.NoError(t, err)
				require.Equal(t, taskrun.StatusCompleted, final.Status)
				mu.Lock()
				defer mu.Unlock()
				require.Equal(t, 1, baseCalls)
				if tc.base != tool.PermissionActionAllow {
					require.Empty(t, reviewed)
					require.Empty(t, executed)
					return
				}
				require.Len(t, reviewed, 1)
				req := reviewed[0]
				host, delegation := false, false
				for i, entry := range req.Transcript {
					switch entry.Content {
					case hostInput:
						host = true
						require.Equal(t, review.OriginUserInput, req.Context.TranscriptInfo[i].Origin)
					case delegatedInput:
						delegation = true
						require.Equal(t, review.OriginAgent, req.Context.TranscriptInfo[i].Origin)
					}
				}
				require.True(t, host, "worker review must retain the real user's authorization and restrictions")
				require.True(t, delegation, "model-generated task text must remain agent-authored")
				if tc.approve {
					require.Equal(t, []string{finalArgs}, executed)
				} else {
					require.Empty(t, executed)
				}
			})
		}
	}
}
