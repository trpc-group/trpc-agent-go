//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package processor

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcall"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestCancellationRetention_RewritesBeforePersistence(t *testing.T) {
	for _, timing := range []string{"before_entry", "stream_selection", "attempted", "explicit_cancel"} {
		for _, rewriteFails := range []bool{false, true} {
			name := timing + "/success"
			if rewriteFails {
				name = timing + "/rewrite_error"
			}
			t.Run(name, func(t *testing.T) {
				inv, traceCtx := newExecutionTraceProcessorInvocation(t)
				watchdog, stop := context.WithTimeout(traceCtx, 5*time.Second)
				defer stop()
				parent, expire := context.WithCancel(watchdog)
				defer expire()
				var ctx context.Context = controlledDeadlineContext{parent}
				if timing == "explicit_cancel" {
					ctx = parent
				}
				hooks := 0
				mgr := plugin.MustNewManager(afterToolMessagesTestPlugin{hook: func(hookCtx context.Context, args *plugin.AfterToolMessagesArgs) (*plugin.AfterToolMessagesResult, error) {
					hooks++
					if timing != "attempted" {
						require.NoError(t, hookCtx.Err())
						_, bounded := hookCtx.Deadline()
						require.True(t, bounded)
					}
					if rewriteFails {
						return nil, errors.New("redaction unavailable")
					}
					msgs := append([]model.Message(nil), args.ToolResultMessages...)
					require.Len(t, msgs, 2)
					for i := range msgs {
						if msgs[i].ToolID == "call-A" {
							msgs[i].Content = "display-A"
						}
						if msgs[i].ToolID == "call-B" && timing != "attempted" {
							require.JSONEq(t, `{"status":"not_executed","reason":"deadline_exceeded"}`, msgs[i].Content)
						}
					}
					return &plugin.AfterToolMessagesResult{ToolResultMessages: msgs}, nil
				}})
				svc := inmemory.NewSessionService()
				t.Cleanup(func() { require.NoError(t, svc.Close()) })
				key := session.Key{AppName: "test", UserID: "user", SessionID: "session"}
				sess, err := svc.CreateSession(watchdog, key, nil)
				require.NoError(t, err)
				inv.Session = sess
				inv.Plugins = mgr
				inv.SessionService = svc
				second := &cancellationStreamTool{selectStream: func() bool {
					if timing == "stream_selection" {
						expire()
					}
					return true
				}, call: func(context.Context) error { expire(); return context.DeadlineExceeded }}
				tools := map[string]tool.Tool{
					"A": &mockInvocationStateDeltaTool{declaration: &tool.Declaration{Name: "A"}, callFn: func(context.Context, []byte) (any, error) {
						if timing == "before_entry" || timing == "explicit_cancel" {
							expire()
						}
						return "raw-A", nil
					}, delta: map[string][]byte{"completed": []byte(`"A"`)}},
					"B": second,
				}
				calls := []model.ToolCall{
					{ID: "call-A", Function: model.FunctionDefinitionParam{Name: "A", Arguments: []byte(`{}`)}},
					{ID: "call-B", Function: model.FunctionDefinitionParam{Name: "B", Arguments: []byte(`{}`)}},
				}
				userEvent := event.NewResponseEvent(inv.InvocationID, "user", &model.Response{
					Choices: []model.Choice{{Message: model.NewUserMessage("run A and B")}},
				})
				userEvent.FilterKey = inv.GetEventFilterKey()
				require.NoError(t, svc.AppendEvent(watchdog, sess, userEvent))
				rsp := newToolCallResponseWithCalls(calls)
				callEvent := event.NewResponseEvent(inv.InvocationID, inv.AgentName, rsp)
				callEvent.FilterKey = inv.GetEventFilterKey()
				require.NoError(t, svc.AppendEvent(watchdog, sess, callEvent))
				after := []string{}
				callbacks := tool.NewCallbacks().RegisterAfterTool(func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
					after = append(after, args.ToolName)
					return nil, nil
				})
				got, err := NewFunctionCallResponseProcessor(false, callbacks).handleFunctionCallsAndSendEvent(ctx, inv, rsp, tools, make(chan *event.Event, 4))
				require.Nil(t, got)
				if timing == "explicit_cancel" {
					require.Zero(t, hooks)
				} else {
					require.Equal(t, 1, hooks)
				}
				if timing == "attempted" {
					require.Equal(t, 1, second.calls)
					require.Len(t, after, 2)
				} else {
					require.Zero(t, second.calls)
					require.Equal(t, []string{"A"}, after)
				}
				reloaded, loadErr := svc.GetSession(watchdog, key)
				require.NoError(t, loadErr)
				if timing == "explicit_cancel" {
					require.ErrorIs(t, err, context.Canceled)
					require.Len(t, reloaded.Events, 2)
					return
				}
				if rewriteFails {
					if timing != "attempted" {
						require.ErrorIs(t, err, context.DeadlineExceeded)
						trace := agent.BuildExecutionTrace(inv, atrace.TraceStatusFailed)
						require.Len(t, trace.Steps, 1)
						require.Len(t, trace.Steps[0].Tools, 2)
						require.Equal(t, "call-A", trace.Steps[0].Tools[0].ID)
						require.Equal(t, "A", trace.Steps[0].Tools[0].Name)
						require.Nil(t, trace.Steps[0].Tools[0].Result)
						require.Nil(t, trace.Steps[0].Tools[0].Arguments)
						require.Empty(t, trace.Steps[0].Tools[0].Error)
						encoded, encodeErr := json.Marshal(trace)
						require.NoError(t, encodeErr)
						require.NotContains(t, string(encoded), "raw-A")
					} else {
						require.ErrorContains(t, err, "redaction unavailable")
					}
					require.Len(t, reloaded.Events, 2)
					return
				}
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Len(t, reloaded.Events, 3)
				saved := reloaded.Events[2]
				require.Equal(t, []string{"call-A", "call-B"}, saved.GetToolResultIDs())
				if timing == "attempted" {
					require.Contains(t, saved.Choices[1].Message.Content, context.DeadlineExceeded.Error())
					require.NotContains(t, saved.Choices[1].Message.Content, "not_executed")
				} else {
					require.JSONEq(t, `{"status":"not_executed","reason":"deadline_exceeded"}`, saved.Choices[1].Message.Content)
				}
				require.Equal(t, "display-A", saved.Choices[0].Message.Content)
				require.Equal(t, []byte(`"A"`), saved.StateDelta["completed"])
				trace := agent.BuildExecutionTrace(inv, atrace.TraceStatusFailed)
				require.Equal(t, "display-A", trace.Steps[0].Tools[0].Result)
				next := agent.NewInvocation(agent.WithInvocationAgent(&mockAgentWithTools{name: inv.AgentName}), agent.WithInvocationSession(reloaded), agent.WithInvocationMessage(model.NewUserMessage("continue")))
				req := &model.Request{Tools: tools}
				NewContentRequestProcessor().ProcessRequest(watchdog, next, req, nil)
				req.Messages = toolcall.SanitizeMessagesWithTools(watchdog, req.Messages, tools)
				var callIDs, resultIDs []string
				for _, msg := range req.Messages {
					require.NotContains(t, msg.Content, "raw-A")
					for _, call := range msg.ToolCalls {
						callIDs = append(callIDs, call.ID)
					}
					if msg.Role == model.RoleTool {
						resultIDs = append(resultIDs, msg.ToolID)
					}
					if msg.Role == model.RoleUser {
						require.Contains(t, []string{"run A and B", "continue"}, msg.Content, "no unmatched call should be downgraded to an orphan user message")
					}
					if msg.ToolID == "call-A" {
						require.Equal(t, "display-A", msg.Content)
					}
					if msg.ToolID == "call-B" {
						require.Equal(t, saved.Choices[1].Message.Content, msg.Content)
					}
				}
				require.Equal(t, []string{"call-A", "call-B"}, callIDs)
				require.Equal(t, callIDs, resultIDs, "completed results and unstarted statuses must remain paired in the next model request")
			})
		}
	}
}

func TestCancellationRetention_AdmissionClassification(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "rejected", true: "attempted"}[started], func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			tl := &cancellationStreamTool{selectStream: func() bool {
				if !started {
					cancel()
				}
				return true
			}, call: func(context.Context) error { cancel(); return context.Canceled }}
			p := NewFunctionCallResponseProcessor(false, nil)
			execution, err := p.executeToolCall(ctx, agent.NewInvocation(), model.ToolCall{ID: "call", Function: model.FunctionDefinitionParam{Name: "stream"}}, map[string]tool.Tool{"stream": tl}, 0, nil)
			require.ErrorIs(t, err, context.Canceled)
			require.Equal(t, started, execution.shouldIgnoreError)
			var rejected *toolAdmissionError
			require.Equal(t, !started, errors.As(err, &rejected))
		})
	}
}

func TestCancellationRetention_RewriteFailureOmitsErrorOutputFromTrace(t *testing.T) {
	inv, traceCtx := newExecutionTraceProcessorInvocation(t)
	parent, expire := context.WithCancel(traceCtx)
	defer expire()
	ctx := controlledDeadlineContext{parent}
	inv.Plugins = plugin.MustNewManager(afterToolMessagesTestPlugin{hook: func(context.Context, *plugin.AfterToolMessagesArgs) (*plugin.AfterToolMessagesResult, error) {
		return nil, errors.New("redaction unavailable")
	}})
	first := &mockCallableTool{declaration: &tool.Declaration{Name: "A"},
		callFn: func(context.Context, []byte) (any, error) {
			expire()
			return "private output", errors.New("private failure details")
		}}
	calls := []model.ToolCall{
		{ID: "call-A", Function: model.FunctionDefinitionParam{Name: "A", Arguments: []byte(`{"value":"private input"}`)}},
		{ID: "call-B", Function: model.FunctionDefinitionParam{Name: "B"}},
	}
	got, err := NewFunctionCallResponseProcessor(false, nil).handleFunctionCallsAndSendEvent(
		ctx, inv, newToolCallResponseWithCalls(calls), map[string]tool.Tool{"A": first}, make(chan *event.Event, 4),
	)
	require.Nil(t, got)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	trace := agent.BuildExecutionTrace(inv, atrace.TraceStatusFailed)
	require.Len(t, trace.Steps, 1)
	require.Len(t, trace.Steps[0].Tools, 2)
	completed := trace.Steps[0].Tools[0]
	require.Equal(t, "call-A", completed.ID)
	require.Equal(t, "A", completed.Name)
	require.NotEmpty(t, completed.Error, "omitting output must preserve the failure status")
	require.Nil(t, completed.Result)
	require.Nil(t, completed.Arguments)
	encoded, err := json.Marshal(trace)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "private")
}
