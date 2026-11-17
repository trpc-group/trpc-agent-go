//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmflow

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/calllimit"
	imodelrequest "trpc.group/trpc-go/trpc-agent-go/internal/modelrequest"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type preservingFinalizationModel struct{ *mockModel }

func (*preservingFinalizationModel) PreservesToolsWhenDisabled() bool { return true }

type finalizationDeclarationTool struct{ decl *tool.Declaration }

func (t *finalizationDeclarationTool) Declaration() *tool.Declaration { return t.decl }

func TestCallLimitFinalization_CallbackToolChanges(t *testing.T) {
	for _, change := range []string{"filter", "schema", "replace", "rename", "clear", "readd", "shared"} {
		t.Run(change, func(t *testing.T) {
			run := func(finalizing bool) *model.Request {
				m := &preservingFinalizationModel{&mockModel{}}
				inv := agent.NewInvocation(agent.WithInvocationModel(m))
				if finalizing {
					instruction := "finish"
					calllimit.Configure(inv, &instruction, nil)
					_, active := calllimit.ActivateForLLM(inv, true)
					require.True(t, active)
				}
				lookup := &finalizationDeclarationTool{&tool.Declaration{Name: "lookup"}}
				injected := &finalizationDeclarationTool{&tool.Declaration{Name: "injected"}}
				sharedTools := map[string]tool.Tool{"lookup": lookup, "injected": injected}
				sharedExtra := map[string]any{"tool_choice": "required", "tools": "callback-value"}
				req := &model.Request{Tools: map[string]tool.Tool{
					"lookup": lookup, "restricted": &finalizationDeclarationTool{&tool.Declaration{Name: "restricted"}},
				}}
				callbacks := model.NewCallbacks().RegisterBeforeModel(func(_ context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
					delete(args.Request.Tools, "restricted")
					args.Request.Tools["injected"] = injected
					args.Request.ExtraFields = sharedExtra
					switch change {
					case "schema":
						lookup.decl.Description = "normalized"
					case "replace":
						args.Request.Tools["lookup"] = &finalizationDeclarationTool{&tool.Declaration{Name: "lookup", Description: "normalized"}}
					case "rename":
						lookup.decl.Name = "renamed"
					case "clear":
						args.Request.Tools = nil
					case "readd":
						delete(args.Request.Tools, "lookup")
					case "shared":
						args.Request.Tools = sharedTools
					}
					return &model.BeforeModelResult{Context: context.Background()}, nil
				}).RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
					require.Equal(t, finalizing, imodelrequest.ToolsDisabled(ctx))
					require.NotContains(t, args.Request.Tools, "restricted")
					if change == "clear" {
						require.Nil(t, args.Request.Tools)
					} else {
						require.Contains(t, args.Request.Tools, "injected")
						if change == "readd" {
							require.NotContains(t, args.Request.Tools, "lookup")
							args.Request.Tools["lookup"] = lookup
						}
					}
					return nil, nil
				})
				f := New(nil, nil, Options{ModelCallbacks: callbacks})
				_, _, err := f.runBeforeModelCallbacks(context.Background(), inv, req, m)
				require.NoError(t, err)
				require.Len(t, sharedTools, 2)
				require.Equal(t, map[string]any{"tool_choice": "required", "tools": "callback-value"}, sharedExtra)
				return req
			}
			// Finalization changes tool choice in the adapter, not callback semantics.
			require.Equal(t, run(false), run(true))
		})
	}
}

func TestCallLimitFinalization_UsesResolvedModelCapability(t *testing.T) {
	for _, preserve := range []bool{false, true} {
		t.Run(map[bool]string{false: "omit", true: "preserve"}[preserve], func(t *testing.T) {
			plain := &mockModel{responses: []*model.Response{{Done: true}}}
			preserving := &preservingFinalizationModel{plain}
			var resolved, base model.Model = plain, preserving
			if preserve {
				resolved, base = preserving, plain
			}
			inv := agent.NewInvocation(agent.WithInvocationModel(base))
			inv.MaxLLMCalls = 1
			instruction := "finish"
			calllimit.Configure(inv, &instruction, nil)
			req := &model.Request{
				Messages: []model.Message{model.NewUserMessage("question")},
				Tools:    map[string]tool.Tool{"lookup": &mockLongRunnerTool{name: "lookup"}},
			}
			f := New(nil, nil, Options{})
			_, seq, called, err := f.callLLM(context.Background(), inv, req, resolved)
			require.NoError(t, err)
			require.True(t, called)
			require.NotNil(t, seq)
			require.Len(t, req.Tools, map[bool]int{false: 0, true: 1}[preserve])
		})
	}
}

type preservingRetryModel struct{ modelRetryTestBinder }

func (preservingRetryModel) PreservesToolsWhenDisabled() bool { return true }

func TestCallLimitFinalization_RetryCallbackToolChanges(t *testing.T) {
	inv := agent.NewInvocation()
	instruction := "finish"
	calllimit.Configure(inv, &instruction, nil)
	_, active := calllimit.ActivateForLLM(inv, true)
	require.True(t, active)
	lookup := &finalizationDeclarationTool{&tool.Declaration{Name: "lookup"}}
	added := &finalizationDeclarationTool{&tool.Declaration{Name: "added"}}
	req := &model.Request{Tools: map[string]tool.Tool{"lookup": lookup}}
	callbacks := model.NewCallbacks().RegisterBeforeModel(func(ctx context.Context, args *model.BeforeModelArgs) (*model.BeforeModelResult, error) {
		require.True(t, imodelrequest.ToolsDisabled(ctx))
		require.Same(t, lookup, args.Request.Tools["lookup"])
		args.Request.Tools["added"] = added
		return &model.BeforeModelResult{Context: context.Background()}, nil
	})
	ctx := contextWithModelRetryCallbacks(context.Background(), New(nil, nil, Options{ModelCallbacks: callbacks}), inv, preservingRetryModel{})
	bound := ctx.Value(modelRetryTestCallbacksKey{}).(modelRetryTestCallbacks)
	ctx, response, err := bound.before(ctx, req)
	require.NoError(t, err)
	require.Nil(t, response)
	require.True(t, imodelrequest.ToolsDisabled(ctx))
	require.Equal(t, map[string]tool.Tool{"lookup": lookup, "added": added}, req.Tools)
}

func TestCallLimitFinalization_DoesNotMarkLongRunningToolPending(t *testing.T) {
	for _, source := range []string{"shared", "flow", "context"} {
		t.Run(source, func(t *testing.T) {
			inv := agent.NewInvocation()
			instruction := "finish"
			calllimit.Configure(inv, &instruction, nil)
			_, active := calllimit.ActivateForLLM(inv, true)
			require.True(t, active)
			eventInvocation, optionsInvocation := inv, inv
			switch source {
			case "flow":
				optionsInvocation = inv.Clone()
			case "context":
				eventInvocation = inv.Clone()
			}
			req := &model.Request{Tools: map[string]tool.Tool{
				"lookup": &mockLongRunnerTool{name: "lookup", long: true},
			}}
			response := &model.Response{Choices: []model.Choice{{Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID: "lookup-1", Type: "function", Function: model.FunctionDefinitionParam{Name: "lookup", Arguments: []byte("{}")},
				}},
			}}}}
			f := New(nil, nil, Options{})
			evt := f.createLLMResponseEvent(eventInvocation, optionsInvocation, response, req)
			require.Empty(t, evt.LongRunningToolIDs)
			require.Equal(t, response, evt.Response)
			require.Contains(t, req.Tools, "lookup")

			calllimit.Finish(inv)
			evt = f.createLLMResponseEvent(eventInvocation, optionsInvocation, response, req)
			require.Contains(t, evt.LongRunningToolIDs, "lookup-1")
		})
	}
}

func TestCallLimitFinalization_DoesNotRepairOrBufferToolText(t *testing.T) {
	for _, source := range []string{"shared", "flow", "context"} {
		t.Run(source, func(t *testing.T) {
			inv := agent.NewInvocation(agent.WithInvocationRunOptions(
				agent.NewRunOptions(agent.WithToolCallTextRepairEnabled(true)),
			))
			instruction := "finish"
			calllimit.Configure(inv, &instruction, nil)
			_, active := calllimit.ActivateForLLM(inv, true)
			require.True(t, active)
			p := &streamingResponseProcessor{
				ctx:               context.Background(),
				invocation:        inv,
				currentInvocation: inv,
				llmRequest:        &model.Request{Tools: map[string]tool.Tool{"lookup": &mockLongRunnerTool{name: "lookup"}}},
			}
			switch source {
			case "flow":
				p.currentInvocation = inv.Clone()
			case "context":
				p.invocation = inv.Clone()
			}
			const content = "<tool_call>lookup</tool_call>"
			response := &model.Response{Choices: []model.Choice{{
				Message: model.NewAssistantMessage(content),
			}}}
			require.False(t, p.repairToolCallText(response))
			require.Empty(t, response.Choices[0].Message.ToolCalls)
			require.Equal(t, content, response.Choices[0].Message.Content)
			response.IsPartial = true
			require.False(t, p.shouldBufferToolCallTextPartial(response))

			// The same valid tool text is buffered and repaired outside finalization.
			calllimit.Finish(inv)
			require.True(t, p.shouldBufferToolCallTextPartial(response))
			response.IsPartial = false
			require.True(t, p.repairToolCallText(response))
			require.Len(t, response.Choices[0].Message.ToolCalls, 1)
			require.Equal(t, "lookup", response.Choices[0].Message.ToolCalls[0].Function.Name)
		})
	}
}
