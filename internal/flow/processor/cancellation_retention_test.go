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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
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
				watchdog, stop := context.WithTimeout(context.Background(), 5*time.Second)
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
					for i := range msgs {
						if msgs[i].ToolID == "call-A" {
							msgs[i].Content = "display-A"
						}
					}
					return &plugin.AfterToolMessagesResult{ToolResultMessages: msgs}, nil
				}})
				svc := inmemory.NewSessionService()
				t.Cleanup(func() { require.NoError(t, svc.Close()) })
				key := session.Key{AppName: "test", UserID: "user", SessionID: "session"}
				sess, err := svc.CreateSession(watchdog, key, nil)
				require.NoError(t, err)
				inv := agent.NewInvocation(agent.WithInvocationAgent(&mockAgentWithTools{name: "tester"}), agent.WithInvocationSession(sess), agent.WithInvocationPlugins(mgr))
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
					} else {
						require.ErrorContains(t, err, "redaction unavailable")
					}
					require.Len(t, reloaded.Events, 2)
					return
				}
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.Len(t, reloaded.Events, 3)
				saved := reloaded.Events[2]
				if timing == "attempted" {
					require.Equal(t, []string{"call-A", "call-B"}, saved.GetToolResultIDs())
				} else {
					require.Equal(t, []string{"call-A"}, saved.GetToolResultIDs())
				}
				require.Equal(t, "display-A", saved.Choices[0].Message.Content)
				require.Equal(t, []byte(`"A"`), saved.StateDelta["completed"])
				next := agent.NewInvocation(agent.WithInvocationAgent(&mockAgentWithTools{name: "tester"}), agent.WithInvocationSession(reloaded), agent.WithInvocationMessage(model.NewUserMessage("continue")))
				req := &model.Request{Tools: tools}
				NewContentRequestProcessor().ProcessRequest(watchdog, next, req, nil)
				req.Messages = toolcall.SanitizeMessagesWithTools(watchdog, req.Messages, tools)
				found := false
				for _, msg := range req.Messages {
					require.NotContains(t, msg.Content, "raw-A")
					if msg.ToolID == "call-A" {
						found = true
						require.Equal(t, "display-A", msg.Content)
					}
				}
				require.True(t, found, "completed result must remain in the next model request")
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
