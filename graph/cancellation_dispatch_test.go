//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestCancellationDispatch_ToolCallbacks(t *testing.T) {
	cases := []struct {
		name  string
		calls int
		err   error
	}{
		{"pre_cancelled", 0, context.Canceled},
		{"before_cancelled", 0, context.Canceled},
		{"inherited", 0, context.Canceled},
		{"background", 1, nil},
		{"without_cancel", 1, nil},
		{"cancelled_replacement", 0, context.Canceled},
		{"expired_replacement", 0, context.DeadlineExceeded},
		{"permission_error", 0, context.Canceled},
		{"permission_allow_cancelled", 0, context.Canceled},
		{"tool_cancelled", 1, context.Canceled},
		{"retry_cancelled", 1, context.Canceled},
		{"success", 1, nil},
	}
	for _, mode := range []string{"callable", "retry"} {
		for _, pluginBefore := range []bool{false, true} {
			for _, tc := range cases {
				t.Run(fmt.Sprintf("%s/plugin_before=%t/%s", mode, pluginBefore, tc.name), func(t *testing.T) {
					if tc.name == "retry_cancelled" && mode != "retry" {
						t.Skip("requires a retry policy")
					}
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					beforeCalls, calls, pluginAfter, localAfter := 0, 0, 0, 0
					before := func(ctx context.Context, _ *tool.BeforeToolArgs) (*tool.BeforeToolResult, error) {
						beforeCalls++
						switch tc.name {
						case "before_cancelled":
							cancel()
						case "inherited":
							cancel()
							return &tool.BeforeToolResult{Context: context.WithValue(ctx, struct{}{}, "value")}, nil
						case "background":
							cancel()
							return &tool.BeforeToolResult{Context: context.Background()}, nil
						case "without_cancel":
							cancel()
							return &tool.BeforeToolResult{Context: context.WithoutCancel(ctx)}, nil
						case "cancelled_replacement":
							replacement, stop := context.WithCancel(context.Background())
							stop()
							return &tool.BeforeToolResult{Context: replacement}, nil
						case "expired_replacement":
							replacement, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
							defer stop()
							return &tool.BeforeToolResult{Context: replacement}, nil
						}
						return nil, nil
					}
					callbacks := tool.NewCallbacks()
					mgr := plugin.MustNewManager(&hookPlugin{name: "cancel", reg: func(r *plugin.Registry) {
						if pluginBefore {
							r.BeforeTool(before)
						}
						r.AfterTool(func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
							pluginAfter++
							require.ErrorIs(t, args.Error, tc.err)
							return nil, nil
						})
					}})
					if !pluginBefore {
						callbacks.RegisterBeforeTool(before)
					}
					callbacks.RegisterAfterTool(func(_ context.Context, args *tool.AfterToolArgs) (*tool.AfterToolResult, error) {
						localAfter++
						require.ErrorIs(t, args.Error, tc.err)
						return nil, nil
					})
					inv := agent.NewInvocation(agent.WithInvocationPlugins(mgr), agent.WithInvocationRunOptions(agent.NewRunOptions(
						agent.WithToolPermissionPolicyFunc(func(ctx context.Context, _ *tool.PermissionRequest) (tool.PermissionDecision, error) {
							if tc.name == "permission_error" {
								cancel()
								return tool.PermissionDecision{}, ctx.Err()
							}
							if tc.name == "permission_allow_cancelled" {
								cancel()
							}
							return tool.PermissionDecision{Action: tool.PermissionActionAllow}, nil
						}),
					)))
					ctx = agent.NewInvocationContext(ctx, inv)
					if tc.name == "pre_cancelled" {
						cancel()
					}
					call := func(ctx context.Context, _ []byte) (any, error) {
						calls++
						require.NoError(t, ctx.Err())
						if tc.name == "tool_cancelled" {
							cancel()
							return nil, ctx.Err()
						}
						if tc.name == "retry_cancelled" {
							return nil, errors.New("retryable")
						}
						return "done", nil
					}
					var retry *tool.RetryPolicy
					if mode == "retry" {
						retry = &tool.RetryPolicy{MaxAttempts: 2, RetryOn: func(context.Context, *tool.RetryInfo) (bool, error) { cancel(); return true, nil }}
					}
					tl := function.NewFunctionTool(func(ctx context.Context, _ struct{}) (string, error) { _, err := call(ctx, nil); return "done", err }, function.WithName("write"), function.WithDescription("Cancellation probe"))
					_, _, _, _, _, _, err := runToolWithEventContexts(ctx, model.ToolCall{ID: "call", Function: model.FunctionDefinitionParam{Name: "write", Arguments: []byte("{}")}}, callbacks, tl, State{}, retry, 0)
					require.ErrorIs(t, err, tc.err)
					require.Equal(t, tc.calls, calls)
					wantPluginAfter := tc.calls
					if tc.name == "background" {
						wantPluginAfter = 0
					} // The replacement drops the invocation and its plugins.
					require.Equal(t, wantPluginAfter, pluginAfter)
					require.Equal(t, tc.calls, localAfter)
					if tc.name == "pre_cancelled" {
						require.Zero(t, beforeCalls)
					} else {
						require.Equal(t, 1, beforeCalls)
					}
				})
			}
		}
	}
}

func TestCancellationDispatch_ModelCallbacks(t *testing.T) {
	for _, iter := range []bool{false, true} {
		for _, pluginBefore := range []bool{false, true} {
			for _, name := range []string{"pre_cancelled", "before_cancelled", "inherited", "background", "without_cancel", "cancelled_replacement", "expired_replacement", "success"} {
				t.Run(fmt.Sprintf("iter=%t/plugin_before=%t/%s", iter, pluginBefore, name), func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					beforeCalls := 0
					callbacks := model.NewCallbacks()
					before := func(ctx context.Context, _ *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
						beforeCalls++
						switch name {
						case "before_cancelled":
							cancel()
						case "inherited":
							cancel()
							return &model.BeforeModelResult{Context: context.WithValue(ctx, struct{}{}, "value")}, nil
						case "background":
							cancel()
							return &model.BeforeModelResult{Context: context.Background()}, nil
						case "without_cancel":
							cancel()
							return &model.BeforeModelResult{Context: context.WithoutCancel(ctx)}, nil
						case "cancelled_replacement":
							replacement, stop := context.WithCancel(context.Background())
							stop()
							return &model.BeforeModelResult{Context: replacement}, nil
						case "expired_replacement":
							replacement, stop := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
							defer stop()
							return &model.BeforeModelResult{Context: replacement}, nil
						}
						return nil, nil
					}
					mgr := plugin.MustNewManager(&hookPlugin{name: "cancel-model", reg: func(r *plugin.Registry) {
						if pluginBefore {
							r.BeforeModel(before)
						}
					}})
					if !pluginBefore {
						callbacks.RegisterBeforeModel(before)
					}
					inv := agent.NewInvocation(agent.WithInvocationPlugins(mgr))
					ctx = agent.NewInvocationContext(ctx, inv)
					if name == "pre_cancelled" {
						cancel()
					}
					channelModel := &namedGraphModel{name: "cancel-probe"}
					var m model.Model = channelModel
					called := channelModel.Called
					if iter {
						iterModel := &cancellationIterModel{}
						m = iterModel
						called = func() bool { return iterModel.iterCalled }
						t.Cleanup(func() { require.False(t, iterModel.Called(), "iterator models must not use the channel API") })
					}
					sg := NewStateGraph(MessagesStateSchema())
					sg.AddLLMNode("llm", m, "instruction", nil)
					_, err := sg.graph.nodes["llm"].Function(ctx, State{StateKeyUserInput: "hello", StateKeyModelCallbacks: callbacks})
					wantCall := name == "background" || name == "without_cancel" || name == "success"
					if wantCall {
						require.NoError(t, err)
					} else if name == "expired_replacement" {
						require.ErrorIs(t, err, context.DeadlineExceeded)
					} else {
						require.ErrorIs(t, err, context.Canceled)
					}
					require.Equal(t, wantCall, called())
					if name == "pre_cancelled" {
						require.Zero(t, beforeCalls)
					} else {
						require.Equal(t, 1, beforeCalls)
					}
				})
			}
		}
	}
}

type cancellationIterModel struct {
	namedGraphModel
	iterCalled bool
}

func (m *cancellationIterModel) GenerateContentIter(ctx context.Context, req *model.Request) (model.Seq[*model.Response], error) {
	m.iterCalled = true
	return func(yield func(*model.Response) bool) {
		yield(&model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("ok")}}})
	}, nil
}

type cancellationPreparationModel struct {
	cancel       context.CancelFunc
	infoCalls    int
	channelCalls int
}

func (m *cancellationPreparationModel) Info() model.Info {
	m.infoCalls++
	m.cancel()
	return model.Info{Name: "probe", ProviderDataProtocol: "test.native"}
}

func (m *cancellationPreparationModel) GenerateContent(context.Context, *model.Request) (<-chan *model.Response, error) {
	m.channelCalls++
	ch := make(chan *model.Response)
	close(ch)
	return ch, nil
}

type cancellationPreparationIterModel struct {
	*cancellationPreparationModel
	iterCalls int
}

func (m *cancellationPreparationIterModel) GenerateContentIter(context.Context, *model.Request) (model.Seq[*model.Response], error) {
	m.iterCalls++
	return func(func(*model.Response) bool) {}, nil
}

func TestCancellationDispatch_ModelPreparation(t *testing.T) {
	for _, iter := range []bool{false, true} {
		t.Run(fmt.Sprintf("iter=%t", iter), func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			channelModel := &cancellationPreparationModel{cancel: cancel}
			iterModel := &cancellationPreparationIterModel{cancellationPreparationModel: channelModel}
			var m model.Model = channelModel
			if iter {
				m = iterModel
			}
			request := &model.Request{Messages: []model.Message{{
				Role:         model.RoleAssistant,
				ProviderData: &model.ProviderData{Protocol: "test.native", Model: "probe", Payload: []byte("{}")},
			}}}
			_, err := generateModelStream(ctx, m, request, trace.SpanFromContext(ctx))
			require.ErrorIs(t, err, context.Canceled)
			require.Positive(t, channelModel.infoCalls)
			require.Zero(t, channelModel.channelCalls)
			require.Zero(t, iterModel.iterCalls)
		})
	}
}
