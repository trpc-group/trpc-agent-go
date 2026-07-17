//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package app

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestModelCallBudgetModel_EnforcesPerContextLimit(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudget(context.Background(), 2)

	_, err := wrapped.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (2) exceeded")
	require.EqualValues(t, 2, underlying.callCount())
}

func TestModelCallBudgetModel_NoBudgetPassesThrough(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)

	_, err := wrapped.GenerateContent(context.Background(), &model.Request{})
	require.NoError(t, err)
	require.EqualValues(t, 1, underlying.callCount())
}

func TestModelCallBudgetIterModel_NoBudgetPassesThrough(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetIterModel{}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)

	seq, err := iter.GenerateContentIter(context.Background(), &model.Request{})
	require.NoError(t, err)

	var responses int
	seq(func(*model.Response) bool {
		responses++
		return true
	})
	require.Equal(t, 1, responses)
	require.EqualValues(t, 1, underlying.iterCallCount())
}

func TestModelCallBudgetModel_UsesInvocationRuntimeStateFactory(
	t *testing.T,
) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	factory := newModelCallBudgetFactory(1, false, 0)
	inv := agent.NewInvocation(agent.WithInvocationRunOptions(
		agent.NewRunOptions(agent.MergeRuntimeState(map[string]any{
			modelCallBudgetRuntimeStateKey: factory,
		})),
	))
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, err := wrapped.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 1, underlying.callCount())
}

func TestModelCallBudgetIterModel_EnforcesPerContextLimit(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetIterModel{}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)

	ctx := withModelCallBudget(context.Background(), 1)
	_, err := iter.GenerateContentIter(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = iter.GenerateContentIter(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 1, underlying.iterCallCount())
}

func TestModelCallBudgetModel_ConcurrentCallsShareLimit(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudget(context.Background(), 3)

	var wg sync.WaitGroup
	var successes atomic.Int64
	var failures atomic.Int64
	errs := make(chan string, 16)
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := wrapped.GenerateContent(ctx, &model.Request{})
			if err != nil {
				stopErr, ok := agent.AsStopError(err)
				if !ok || stopErr.Message !=
					"max LLM calls (3) exceeded" {
					errs <- err.Error()
				}
				failures.Add(1)
				return
			}
			successes.Add(1)
		}()
	}
	wg.Wait()
	close(errs)

	for msg := range errs {
		require.Empty(t, msg)
	}
	require.EqualValues(t, 3, successes.Load())
	require.EqualValues(t, 13, failures.Load())
	require.EqualValues(t, 3, underlying.callCount())
}

func TestModelCallBudgetModel_InfoPassesThrough(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)

	require.Equal(t, underlying.Info(), wrapped.Info())
}

func TestNewModelCallBudgetModel_Nil(t *testing.T) {
	t.Parallel()

	require.Nil(t, newModelCallBudgetModel(nil))
}

func TestModelCallBudget_Guards(t *testing.T) {
	t.Parallel()

	require.Nil(t, newModelCallBudget(0, false, 0))
	require.Nil(t, newModelCallBudget(-1, false, 0))
	require.NotNil(t, newModelCallBudget(0, false, time.Second))
	_, err := (*modelCallBudget)(nil).use(context.Background())
	require.NoError(t, err)

	ctx := withModelCallBudget(nil, 1)
	require.NotNil(t, ctx)
	require.NotNil(t, modelCallBudgetFromContext(ctx))

	inv := agent.NewInvocation(agent.WithInvocationRunOptions(
		agent.NewRunOptions(agent.MergeRuntimeState(map[string]any{
			modelCallBudgetRuntimeStateKey: "not-a-budget",
		})),
	))
	ctx = agent.NewInvocationContext(context.Background(), inv)
	require.Nil(t, modelCallBudgetFromContext(ctx))
}

func TestModelCallBudget_FinalRequestEvidenceGuards(t *testing.T) {
	t.Parallel()

	var nilBudget *modelCallBudget
	require.Equal(t, modelCallBudgetFinalRequestConfig{}, nilBudget.finalConfig())
	require.False(t, modelCallBudgetHasToolEvidence(nil))
	require.True(t, modelCallBudgetHasToolEvidence(&model.Request{
		Messages: []model.Message{{Role: model.RoleAssistant, ToolID: "call_1"}},
	}))
	require.True(t, modelCallBudgetHasToolEvidence(&model.Request{
		Messages: []model.Message{{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   "call_1",
				Type: "function",
			}},
		}},
	}))

	direct := nilBudget.applyFinalRequest(&model.Request{
		Messages: []model.Message{
			model.NewToolMessage("call_1", "search", "evidence"),
		},
	}, false)
	require.NotNil(t, direct)
	require.Contains(t, budgetTestMessageText(direct.Messages), "final allowed")

	budget := newModelCallBudget(2, true, 0)
	budget.rememberRequest(&model.Request{
		Messages: []model.Message{
			model.NewUserMessage("question"),
			model.NewToolMessage("call_1", "search", "original evidence"),
		},
	})
	budget.rememberRequest(&model.Request{
		Messages: []model.Message{
			model.NewToolMessage("call_2", "search", "shorter evidence"),
		},
	})
	finalReq := budget.applyFinalRequest(&model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
	}, false)
	content := budgetTestMessageText(finalReq.Messages)
	require.Contains(t, content, "original evidence")
	require.NotContains(t, content, "shorter evidence")
}

func TestModelCallBudgetDeadlineSoon_Guards(t *testing.T) {
	t.Parallel()

	require.False(t, modelCallBudgetDeadlineSoon(nil, time.Second))
	require.False(
		t,
		modelCallBudgetDeadlineSoon(context.Background(), time.Second),
	)

	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second),
	)
	defer cancel()
	require.False(t, modelCallBudgetDeadlineSoon(ctx, 0))
	require.True(t, modelCallBudgetDeadlineSoon(ctx, time.Minute))
}

func TestModelCallBudgetPrefinalHelpers_Guards(t *testing.T) {
	t.Parallel()

	ctx, cancel, ok := modelCallBudgetPrefinalContext(
		context.Background(),
		time.Second,
	)
	require.False(t, ok)
	require.Nil(t, cancel)
	require.NotNil(t, ctx)

	nearDeadline, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(10*time.Millisecond),
	)
	defer cancel()
	ctx, prefinalCancel, ok := modelCallBudgetPrefinalContext(
		nearDeadline,
		time.Minute,
	)
	require.False(t, ok)
	require.Nil(t, prefinalCancel)
	require.Same(t, nearDeadline, ctx)

	require.False(t, modelCallBudgetPrefinalTimedOut(nil, context.Background()))
	require.False(t, modelCallBudgetTimeoutResponse(
		timeoutResponse(time.Second, context.DeadlineExceeded),
		context.Background(),
		context.Background(),
		&modelCallBudget{deadlineWindow: time.Second},
	))

	canceled, stop := context.WithCancel(context.Background())
	stop()
	require.False(t, modelCallBudgetSendResponse(
		canceled,
		make(chan *model.Response),
		&model.Response{},
	))
}

func TestModelCallBudgetCallbacks_RunBeforeModel(t *testing.T) {
	t.Parallel()

	callbacks := modelCallBudgetCallbacks()
	require.NotNil(t, callbacks)
	require.Len(t, callbacks.BeforeModel, 1)

	result, err := callbacks.RunBeforeModel(
		context.Background(),
		&model.BeforeModelArgs{Request: &model.Request{}},
	)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestBaseLLMAgentOptions_AddsModelCallBudgetCallbacks(t *testing.T) {
	t.Parallel()

	withoutBudget := baseLLMAgentOptions(
		&countingBudgetModel{},
		agentConfig{},
		"",
		"",
		model.GenerationConfig{},
		nil,
	)
	withBudget := baseLLMAgentOptions(
		&countingBudgetModel{},
		agentConfig{MaxLLMCalls: 1},
		"",
		"",
		model.GenerationConfig{},
		nil,
	)

	require.Len(t, withBudget, len(withoutBudget)+1)
}

func TestAppendModelCallBudgetGatewayOption_Disabled(t *testing.T) {
	t.Parallel()

	opts := appendModelCallBudgetGatewayOption(nil, 0, false, 0)
	require.Empty(t, opts)
}

func TestAppendModelCallBudgetGatewayOption_AddsRunBudget(t *testing.T) {
	t.Parallel()

	opts := appendModelCallBudgetGatewayOption(nil, 1, false, 0)
	require.Len(t, opts, 1)
}

func TestAppendModelCallBudgetGatewayOption_AddsDeadlineBudget(
	t *testing.T,
) {
	t.Parallel()

	opts := appendModelCallBudgetGatewayOption(nil, 0, false, time.Minute)
	require.Len(t, opts, 1)
}

func TestModelCallBudgetRunOptions(t *testing.T) {
	t.Parallel()

	require.Nil(t, modelCallBudgetRunOptions(0, false, 0))

	runOpts := modelCallBudgetRunOptions(0, false, time.Minute)
	require.Len(t, runOpts, 1)
	opts := agent.NewRunOptions(runOpts...)
	factory, ok := opts.RuntimeState[modelCallBudgetRuntimeStateKey].(*modelCallBudgetFactory)
	require.True(t, ok)
	require.Equal(t, time.Minute, factory.deadlineWindow)
}

func TestModelCallBudgetModel_FinalizesOnLastAllowedCall(t *testing.T) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudgetValue(
		context.Background(),
		newModelCallBudget(1, true, 0),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
		ExtraFields: map[string]any{
			"parallel_tool_calls": true,
			"response_format":     "json",
			"tool_choice":         "required",
			"tools":               []string{"search"},
		},
	}

	_, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)

	got := underlying.lastRequest()
	require.NotNil(t, got)
	require.Nil(t, got.Tools)
	require.Len(t, got.Messages, 2)
	require.Contains(
		t,
		got.Messages[1].Content,
		"final allowed model call",
	)
	require.Contains(
		t,
		got.Messages[1].Content,
		"Do not emit tool calls",
	)
	require.Contains(
		t,
		got.Messages[1].Content,
		"<tool_call>",
	)
	require.Nil(t, req.Tools)
	require.Len(t, req.Messages, 2)
	require.Equal(t, map[string]any{
		"response_format": "json",
	}, req.ExtraFields)
	require.Equal(t, map[string]any{
		"response_format": "json",
	}, got.ExtraFields)
	require.True(t, got.Stream)
	require.True(t, req.Stream)
}

func TestModelCallBudgetModel_FinalizesWithStoredEvidenceMessages(
	t *testing.T,
) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudgetValue(
		context.Background(),
		newModelCallBudget(2, true, 0),
	)
	richReq := &model.Request{
		Messages: []model.Message{
			model.NewUserMessage("question"),
			model.NewAssistantMessage("I should verify the candidate."),
			model.NewToolMessage(
				"call_1",
				"web_fetch",
				"Michele Fitzgerald was born May 5, 1990.",
			),
		},
		Tools: map[string]tool.Tool{"web_fetch": nil},
	}
	_, err := wrapped.GenerateContent(ctx, richReq)
	require.NoError(t, err)

	minimalReq := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("final system"),
			model.NewUserMessage("question"),
		},
		Tools: map[string]tool.Tool{"web_fetch": nil},
	}
	_, err = wrapped.GenerateContent(ctx, minimalReq)
	require.NoError(t, err)

	got := underlying.lastRequest()
	require.NotNil(t, got)
	require.Nil(t, got.Tools)
	require.Len(t, got.Messages, 4)
	require.Equal(t, model.RoleTool, got.Messages[2].Role)
	require.Contains(t, got.Messages[2].Content, "Michele Fitzgerald")
	require.Contains(
		t,
		got.Messages[3].Content,
		"final allowed model call",
	)
	require.Len(t, minimalReq.Messages, 2)
	require.Len(t, minimalReq.Tools, 1)
}

func TestModelCallBudgetModel_FinalizationDisablesThinking(
	t *testing.T,
) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudgetValue(
		context.Background(),
		newModelCallBudget(
			1,
			true,
			0,
			modelCallBudgetFinalRequestConfig{DisableThinking: true},
		),
	)
	req := &model.Request{
		GenerationConfig: model.GenerationConfig{
			ThinkingEnabled: model.BoolPtr(true),
		},
		Messages: []model.Message{model.NewUserMessage("question")},
	}

	_, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)

	got := underlying.lastRequest()
	require.NotNil(t, got)
	require.NotNil(t, got.ThinkingEnabled)
	require.False(t, *got.ThinkingEnabled)
	require.NotNil(t, req.ThinkingEnabled)
	require.False(t, *req.ThinkingEnabled)
}

func TestFinalModelCallRequest_DropsReasoningWhenConfigured(t *testing.T) {
	t.Parallel()

	req := &model.Request{
		Messages: []model.Message{
			{
				Role:               model.RoleAssistant,
				Content:            "visible answer",
				ReasoningContent:   "private reasoning",
				ReasoningSignature: "signature",
			},
		},
	}

	got := finalModelCallRequest(
		req,
		modelCallBudgetFinalRequestConfig{DropReasoningContent: true},
	)

	require.Len(t, got.Messages, 2)
	require.Equal(t, "visible answer", got.Messages[0].Content)
	require.Empty(t, got.Messages[0].ReasoningContent)
	require.Empty(t, got.Messages[0].ReasoningSignature)
	require.Equal(t, "private reasoning", req.Messages[0].ReasoningContent)
	require.Equal(t, "signature", req.Messages[0].ReasoningSignature)
}

func TestFinalModelCallRequest_DropsSkillOverviewSystemPrompt(
	t *testing.T,
) {
	t.Parallel()

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(
				"\n" + finalModelCallSkillOverviewPrefix +
					"\n\n- skill-a: " + strings.Repeat("x", 200),
			),
			model.NewSystemMessage("memory and stable instructions"),
			model.NewUserMessage("latest question"),
		},
	}

	got := finalModelCallRequest(
		req,
		modelCallBudgetFinalRequestConfig{MaxInputTokens: 1000},
	)

	content := budgetTestMessageText(got.Messages)
	require.NotContains(t, content, "skill-a")
	require.Contains(t, content, "memory and stable instructions")
	require.Contains(t, content, "latest question")
	require.Contains(t, content, "final allowed model call")
}

func TestFinalModelCallRequest_TrimsContextWhenConfigured(t *testing.T) {
	t.Parallel()

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system instructions"),
			model.NewUserMessage("old question " + strings.Repeat("x", 800)),
			model.NewAssistantMessage(
				"old answer " + strings.Repeat("y", 800),
			),
			model.NewUserMessage("latest question"),
			model.NewToolMessage("call_1", "search", "latest evidence"),
		},
		Tools: map[string]tool.Tool{"search": nil},
	}

	got := finalModelCallRequest(
		req,
		modelCallBudgetFinalRequestConfig{MaxInputTokens: 20},
	)

	require.Nil(t, got.Tools)
	require.Len(t, got.Messages, 1)
	require.Equal(t, model.RoleUser, got.Messages[0].Role)
	require.Contains(t, got.Messages[0].Content, "OpenClaw Budget Notice")
	counter := finalModelCallTokenCounter(modelCallBudgetFinalRequestConfig{})
	tokens, err := counter.CountTokensRange(
		context.Background(),
		got.Messages,
		0,
		len(got.Messages),
	)
	require.NoError(t, err)
	require.LessOrEqual(t, tokens, 20)
}

func TestFinalModelCallRequest_UsesConfiguredTokenEstimate(t *testing.T) {
	t.Parallel()

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system instructions"),
			model.NewUserMessage(
				"old question " + strings.Repeat("x", 100),
			),
			model.NewAssistantMessage(
				"old answer " + strings.Repeat("y", 100),
			),
			model.NewUserMessage("latest question"),
		},
	}

	relaxed := finalModelCallRequest(
		req,
		modelCallBudgetFinalRequestConfig{MaxInputTokens: 600},
	)
	strict := finalModelCallRequest(
		req,
		modelCallBudgetFinalRequestConfig{
			MaxInputTokens:      600,
			ApproxRunesPerToken: 1,
		},
	)

	relaxedContent := budgetTestMessageText(relaxed.Messages)
	require.Contains(t, relaxedContent, "old question")
	require.Contains(t, relaxedContent, "old answer")

	strictContent := budgetTestMessageText(strict.Messages)
	require.Contains(t, strictContent, "latest question")
	require.NotContains(t, strictContent, "old question")
	require.NotContains(t, strictContent, "old answer")
}

func TestFinalModelCallRequest_TrimsSingleUserToolChain(t *testing.T) {
	t.Parallel()

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage(
				"system " + strings.Repeat("s", 600),
			),
			model.NewUserMessage(
				"solve the task " + strings.Repeat("q", 120),
			),
		},
	}
	for i := 0; i < 16; i++ {
		req.Messages = append(req.Messages, model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{{
				ID:   fmt.Sprintf("call_%02d", i),
				Type: "function",
				Function: model.FunctionDefinitionParam{
					Name:      "search",
					Arguments: []byte(`{"query":"large query payload"}`),
				},
			}},
		})
		req.Messages = append(req.Messages, model.NewToolMessage(
			fmt.Sprintf("call_%02d", i),
			"search",
			fmt.Sprintf(
				"tool-result-%02d %s",
				i,
				strings.Repeat("r", 120),
			),
		))
	}

	got := finalModelCallRequest(
		req,
		modelCallBudgetFinalRequestConfig{
			MaxInputTokens:      700,
			ApproxRunesPerToken: 1,
		},
	)

	require.Less(t, len(got.Messages), len(req.Messages))
	content := budgetTestMessageText(got.Messages)
	require.Contains(t, content, "solve the task")
	require.Contains(t, content, "tool-result-15")
	require.NotContains(t, content, "tool-result-00")
	for _, msg := range got.Messages {
		require.NotEqual(t, model.RoleTool, msg.Role)
		require.Empty(t, msg.ToolCalls)
	}
}

func TestFinalModelCallRequest_PreservesAnswerFormatInstruction(
	t *testing.T,
) {
	t.Parallel()

	userPrompt := "Solve the visible task.\n\n" +
		strings.Repeat("background evidence ", 120) +
		"\n\nFINAL ANSWER: put only the numeric value on the final line." +
		"\n\n" + strings.Repeat("attachment paths and metadata ", 120)
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("system instructions"),
			model.NewUserMessage(userPrompt),
			model.NewAssistantMessage("I will inspect the evidence."),
			model.NewToolMessage("call_1", "image_inspect", "evidence"),
		},
	}

	got := finalModelCallRequest(
		req,
		modelCallBudgetFinalRequestConfig{
			MaxInputTokens:      1000,
			ApproxRunesPerToken: 1,
		},
	)

	content := budgetTestMessageText(got.Messages)
	require.Contains(t, content, "FINAL ANSWER:")
	require.Contains(t, content, "numeric value")
	require.Contains(t, content, "evidence")
	require.Contains(t, content, "final allowed model call")
}

func TestFinalModelCallHelpers_EdgeCases(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	counter := model.NewSimpleTokenCounter(model.WithApproxRunesPerToken(1))
	require.Empty(t, finalModelCallRecentTailEvidenceSnippet([]model.Message{
		model.NewSystemMessage("system only"),
	}))
	require.Len(
		t,
		finalModelCallEvidenceSnippet(strings.Repeat("x", 80)),
		finalModelCallEvidenceSnippetLen,
	)
	require.Equal(t, 0, finalModelCallTrimBudget(ctx, counter, 0))
	require.Equal(
		t,
		20,
		finalModelCallTrimBudget(ctx, failingBudgetTokenCounter{}, 20),
	)
	require.False(t, finalModelCallFits(ctx, counter, nil, 20))
	require.False(t, finalModelCallFits(ctx, counter, []model.Message{
		model.NewUserMessage("question"),
	}, 0))
	require.Nil(t, finalModelCallTailEvidenceMessages(
		ctx,
		counter,
		[]model.Message{model.NewSystemMessage("system only")},
		20,
	))

	oversizedPrefix := []model.Message{
		model.NewUserMessage(strings.Repeat("x", 200)),
	}
	require.Equal(t, oversizedPrefix, finalModelCallTailEvidenceWithPrefix(
		ctx,
		counter,
		[]model.Message{
			model.NewUserMessage("question"),
			model.NewAssistantMessage("answer"),
		},
		oversizedPrefix,
		0,
		1,
	))

	compact := finalModelCallCompactTailEvidenceWithPrefix(
		ctx,
		counter,
		[]model.Message{
			model.NewUserMessage("question"),
			model.NewAssistantMessage(strings.Repeat("evidence ", 80)),
		},
		[]model.Message{model.NewUserMessage("question")},
		0,
		80,
	)
	require.NotEmpty(t, compact)
	require.Contains(t, budgetTestMessageText(compact), "evidence")

	compacted := finalModelCallCompactMessages(
		[]model.Message{model.NewUserMessage(strings.Repeat("abc", 20))},
		12,
	)
	require.Len(t, compacted, 1)
	require.LessOrEqual(t, len([]rune(compacted[0].Content)), 12)

	require.Equal(
		t,
		"[Tool result: tool]",
		finalModelCallToolResultText(model.Message{}),
	)
	require.Equal(t, 1, finalModelCallAnchorUserIndex(
		[]model.Message{
			model.NewSystemMessage("system"),
			model.NewAssistantMessage("assistant"),
		},
		-1,
	))
	require.Equal(t, -1, finalModelCallAnchorUserIndex(
		[]model.Message{model.NewSystemMessage("system")},
		0,
	))
	require.Equal(t, 0, finalModelCallPartRuneLimit(0, 2))
	require.Equal(t, 10, finalModelCallPartRuneLimit(10, 0))
	require.Equal(t, 1, finalModelCallPartRuneLimit(1, 10))
	require.Equal(t, "\n\nFIN", finalModelCallTrimContent(
		"FINAL ANSWER: value",
		5,
	))
	require.Empty(t, finalModelCallTrimContentPlain("abc", 0))
	require.Equal(t, 0, finalModelCallParagraphStart("answer", 0))
	require.Equal(t, 2, finalModelCallParagraphStart("a\nanswer", 4))
	require.Equal(t, 6, finalModelCallParagraphEnd("answer", len("answer")))
	require.Equal(t, 6, finalModelCallParagraphEnd("answer\nnext", 0))
	require.Equal(
		t,
		"xxxxx",
		finalModelCallLimitSnippet(strings.Repeat("x", 20), -1, 5),
	)
	require.Equal(
		t,
		"xxxxx",
		finalModelCallLimitSnippet(strings.Repeat("x", 20), 100, 5),
	)
	require.Equal(t, []model.Message{model.NewUserMessage("tail")},
		finalModelCallNormalizeTail([]model.Message{
			model.NewToolMessage("call_1", "search", "skip"),
			model.NewUserMessage("tail"),
		}),
	)
	require.NotNil(t, applyFinalModelCallRequest(nil, false))
}

func budgetTestMessageText(messages []model.Message) string {
	var b strings.Builder
	for _, msg := range messages {
		b.WriteString(msg.Content)
		b.WriteByte('\n')
	}
	return b.String()
}

func TestModelCallBudgetModel_FinalizesNearDeadline(t *testing.T) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, time.Minute),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	_, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)

	got := underlying.lastRequest()
	require.NotNil(t, got)
	require.Nil(t, got.Tools)
	require.Len(t, got.Messages, 2)
	require.Contains(
		t,
		got.Messages[1].Content,
		"final allowed model call",
	)
	require.Nil(t, req.Tools)
	require.Len(t, req.Messages, 2)
	require.False(t, got.Stream)
	require.False(t, req.Stream)
}

func TestModelCallBudgetIterModel_FinalizesNearDeadline(t *testing.T) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, time.Minute),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}

	_, err := iter.GenerateContentIter(ctx, req)
	require.NoError(t, err)

	got := underlying.lastIterRequest()
	require.NotNil(t, got)
	require.Nil(t, got.Tools)
	require.Len(t, got.Messages, 2)
	require.Contains(
		t,
		got.Messages[1].Content,
		"final allowed model call",
	)
	require.Nil(t, req.Tools)
	require.Len(t, req.Messages, 2)
	require.False(t, got.Stream)
	require.False(t, req.Stream)
}

func TestModelCallBudgetModel_FinalizesWhenPrefinalWindowExpires(
	t *testing.T,
) {
	t.Parallel()

	const (
		prefinalTestDeadline = 500 * time.Millisecond
		prefinalTestWindow   = 300 * time.Millisecond
	)

	underlying := &prefinalTimeoutBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(prefinalTestDeadline),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, prefinalTestWindow),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	}

	ch, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)
	var got []*model.Response
	for resp := range ch {
		got = append(got, resp)
	}

	require.Len(t, got, 2)
	require.True(t, got[0].IsPartial)
	require.Equal(t, "partial draft", got[0].Choices[0].Message.Content)
	require.Equal(t, "final answer", got[1].Choices[0].Message.Content)
	requests := underlying.requestsSnapshot()
	require.Len(t, requests, 2)
	require.NotNil(t, requests[0].Tools)
	require.Nil(t, requests[1].Tools)
	require.Len(t, requests[1].Messages, 2)
	require.Contains(
		t,
		requests[1].Messages[1].Content,
		"final allowed model call",
	)
	require.Nil(t, req.Tools)
}

func TestModelCallBudgetIterModel_FinalizesWhenPrefinalWindowExpires(
	t *testing.T,
) {
	t.Parallel()

	const (
		prefinalTestDeadline = 500 * time.Millisecond
		prefinalTestWindow   = 300 * time.Millisecond
	)

	underlying := &prefinalTimeoutBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(prefinalTestDeadline),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, prefinalTestWindow),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	}

	seq, err := iter.GenerateContentIter(ctx, req)
	require.NoError(t, err)
	var got []*model.Response
	seq(func(resp *model.Response) bool {
		got = append(got, resp)
		return true
	})

	require.Len(t, got, 2)
	require.True(t, got[0].IsPartial)
	require.Equal(t, "partial draft", got[0].Choices[0].Message.Content)
	require.Equal(t, "final answer", got[1].Choices[0].Message.Content)
	requests := underlying.iterRequestsSnapshot()
	require.Len(t, requests, 2)
	require.NotNil(t, requests[0].Tools)
	require.Nil(t, requests[1].Tools)
	require.Len(t, requests[1].Messages, 2)
	require.Contains(
		t,
		requests[1].Messages[1].Content,
		"final allowed model call",
	)
	require.Nil(t, req.Tools)
}

func TestModelCallBudgetModel_DeadlineFallbackRespectsCallLimits(
	t *testing.T,
) {
	const (
		deadline = 300 * time.Millisecond
		window   = 200 * time.Millisecond
	)

	tests := []struct {
		name        string
		budgetLimit int
		invLimit    int
		primeInv    bool
	}{
		{
			name:        "model call budget",
			budgetLimit: 1,
		},
		{
			name:        "invocation budget",
			budgetLimit: 2,
			invLimit:    1,
			primeInv:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			underlying := &prefinalTimeoutBudgetModel{}
			wrapped := newModelCallBudgetModel(underlying)
			ctx, cancel := context.WithDeadline(
				context.Background(),
				time.Now().Add(deadline),
			)
			defer cancel()
			if tt.invLimit > 0 {
				inv := agent.NewInvocation()
				inv.MaxLLMCalls = tt.invLimit
				if tt.primeInv {
					require.NoError(t, inv.IncLLMCallCount())
				}
				ctx = agent.NewInvocationContext(ctx, inv)
			}
			ctx = withModelCallBudgetValue(
				ctx,
				newModelCallBudget(
					tt.budgetLimit,
					false,
					window,
				),
			)

			ch, err := wrapped.GenerateContent(ctx, &model.Request{
				Messages: []model.Message{
					model.NewUserMessage("question"),
				},
				Tools: map[string]tool.Tool{"search": nil},
			})
			require.NoError(t, err)
			var got []*model.Response
			for resp := range ch {
				got = append(got, resp)
			}

			require.Len(t, got, 2)
			require.True(t, got[0].IsPartial)
			require.NotNil(t, got[1].Error)
			require.Contains(
				t,
				got[1].Error.Message,
				"max LLM calls",
			)
			require.Len(t, underlying.requestsSnapshot(), 1)
		})
	}
}

func TestModelCallBudgetIterModel_StopsWithoutDeadlineRetry(
	t *testing.T,
) {
	t.Parallel()

	underlying := &prefinalTimeoutBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(500*time.Millisecond),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, 300*time.Millisecond),
	)

	seq, err := iter.GenerateContentIter(ctx, &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	})
	require.NoError(t, err)
	var got []*model.Response
	seq(func(resp *model.Response) bool {
		got = append(got, resp)
		return false
	})

	require.Len(t, got, 1)
	require.True(t, got[0].IsPartial)
	require.Len(t, underlying.iterRequestsSnapshot(), 1)
}

type modelCallBudgetRetryCallbackBinder interface {
	WithModelRetryCallbacks(
		context.Context,
		func(context.Context, *model.Request) (
			context.Context,
			*model.Response,
			error,
		),
		func(context.Context, *model.Request, *model.Response) (
			context.Context,
			error,
		),
	) context.Context
}

func TestModelCallBudgetModel_DeadlineRetryRunsCallbacks(t *testing.T) {
	t.Parallel()

	underlying := &prefinalTimeoutBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	binder, ok := wrapped.(modelCallBudgetRetryCallbackBinder)
	require.True(t, ok)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(500*time.Millisecond),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, 300*time.Millisecond),
	)
	var beforeCalls int
	var afterCalls int
	ctx = binder.WithModelRetryCallbacks(
		ctx,
		func(
			callbackCtx context.Context,
			req *model.Request,
		) (context.Context, *model.Response, error) {
			beforeCalls++
			require.Nil(t, req.Tools)
			return callbackCtx, nil, nil
		},
		func(
			callbackCtx context.Context,
			req *model.Request,
			resp *model.Response,
		) (context.Context, error) {
			afterCalls++
			require.NotNil(t, req.Tools)
			require.NotNil(t, resp.Error)
			return callbackCtx, nil
		},
	)

	ch, err := wrapped.GenerateContent(ctx, &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	})
	require.NoError(t, err)
	for range ch {
	}

	require.Equal(t, 1, beforeCalls)
	require.Equal(t, 1, afterCalls)
	require.Len(t, underlying.requestsSnapshot(), 2)
}

func TestModelCallBudgetModel_StreamsBeforeDeadlineWindow(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	underlying := &controlledStreamingBudgetModel{gate: gate}
	wrapped := newModelCallBudgetModel(underlying)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(2*time.Second),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, 100*time.Millisecond),
	)

	ch, err := wrapped.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	select {
	case resp := <-ch:
		require.True(t, resp.IsPartial)
	case <-time.After(500 * time.Millisecond):
		close(gate)
		t.Fatal("partial response was buffered until the stream completed")
	}
	close(gate)
	var remaining []*model.Response
	for resp := range ch {
		remaining = append(remaining, resp)
	}
	require.Len(t, remaining, 1)
	require.True(t, remaining[0].Done)
	require.Equal(t, 1, underlying.requestCount())
}

func TestModelCallBudgetIterModel_StreamsBeforeDeadlineWindow(t *testing.T) {
	t.Parallel()

	gate := make(chan struct{})
	underlying := &controlledStreamingBudgetModel{gate: gate}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(2*time.Second),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, 100*time.Millisecond),
	)

	seq, err := iter.GenerateContentIter(ctx, &model.Request{})
	require.NoError(t, err)
	responses := make(chan *model.Response, 2)
	done := make(chan struct{})
	go func() {
		defer close(done)
		seq(func(resp *model.Response) bool {
			responses <- resp
			return true
		})
	}()
	select {
	case resp := <-responses:
		require.True(t, resp.IsPartial)
	case <-time.After(500 * time.Millisecond):
		close(gate)
		t.Fatal("partial response was buffered until the iterator completed")
	}
	close(gate)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("iterator did not complete")
	}
	select {
	case resp := <-responses:
		require.True(t, resp.Done)
	default:
		t.Fatal("final response was not yielded")
	}
	require.Equal(t, 1, underlying.iterRequestCount())
}

func TestModelCallBudgetModel_DoesNotFinalizeInnerTimeoutBeforeWindow(
	t *testing.T,
) {
	t.Parallel()

	underlying := &innerTimeoutBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, 150*time.Millisecond),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	}

	ch, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)
	var got []*model.Response
	for resp := range ch {
		got = append(got, resp)
	}

	require.Len(t, got, 1)
	require.NotNil(t, got[0].Error)
	require.Contains(t, got[0].Error.Message, "model request timeout")
	requests := underlying.requestsSnapshot()
	require.Len(t, requests, 1)
	require.NotNil(t, requests[0].Tools)
	require.NotNil(t, req.Tools)
}

func TestModelCallBudgetIterModel_DoesNotFinalizeInnerTimeoutBeforeWindow(
	t *testing.T,
) {
	t.Parallel()

	underlying := &innerTimeoutBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	iter, ok := wrapped.(model.IterModel)
	require.True(t, ok)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, 150*time.Millisecond),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	}

	seq, err := iter.GenerateContentIter(ctx, req)
	require.NoError(t, err)
	var got []*model.Response
	seq(func(resp *model.Response) bool {
		got = append(got, resp)
		return true
	})

	require.Len(t, got, 1)
	require.NotNil(t, got[0].Error)
	require.Contains(t, got[0].Error.Message, "model request timeout")
	requests := underlying.iterRequestsSnapshot()
	require.Len(t, requests, 1)
	require.NotNil(t, requests[0].Tools)
	require.NotNil(t, req.Tools)
}

func TestModelCallBudgetModel_DoesNotFinalizeOutsideDeadlineWindow(
	t *testing.T,
) {
	t.Parallel()

	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Hour),
	)
	defer cancel()
	ctx = withModelCallBudgetValue(
		ctx,
		newModelCallBudget(0, false, time.Minute),
	)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	}

	_, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)

	got := underlying.lastRequest()
	require.NotNil(t, got)
	require.NotNil(t, got.Tools)
	require.Len(t, got.Messages, 1)
	require.NotNil(t, req.Tools)
}

func TestModelCallBudgetModel_UserPromptPrefixConsumesBudget(t *testing.T) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	ctx := withModelCallBudget(context.Background(), 1)
	req := &model.Request{Messages: []model.Message{model.NewUserMessage(
		"Analyze the following conversation between a user and an assistant, " +
			"and provide a concise summary.",
	)}}

	_, err := wrapped.GenerateContent(ctx, req)
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 1, underlying.callCount())
}

func TestModelCallBudgetBypassModel_DoesNotConsumeContextBudget(
	t *testing.T,
) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	budgeted := newModelCallBudgetModel(underlying)
	bypassed := newModelCallBudgetBypassModel(budgeted)
	ctx := withModelCallBudget(context.Background(), 1)

	_, err := bypassed.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = bypassed.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)

	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 3, underlying.callCount())
}

func TestModelCallBudgetBypassModel_DoesNotConsumeInvocationBudget(
	t *testing.T,
) {
	t.Parallel()

	underlying := &countingBudgetModel{}
	budgeted := newModelCallBudgetModel(underlying)
	bypassed := newModelCallBudgetBypassModel(budgeted)
	factory := newModelCallBudgetFactory(1, false, 0)
	inv := agent.NewInvocation(agent.WithInvocationRunOptions(
		agent.NewRunOptions(agent.MergeRuntimeState(map[string]any{
			modelCallBudgetRuntimeStateKey: factory,
		})),
	))
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, err := bypassed.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = bypassed.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)

	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 3, underlying.callCount())
}

func TestModelCallBudgetBypassIterModel_DoesNotConsumeContextBudget(
	t *testing.T,
) {
	t.Parallel()

	underlying := &countingBudgetIterModel{}
	budgeted := newModelCallBudgetModel(underlying)
	bypassed := newModelCallBudgetBypassModel(budgeted)
	bypassIter, ok := bypassed.(model.IterModel)
	require.True(t, ok)
	budgetedIter, ok := budgeted.(model.IterModel)
	require.True(t, ok)
	ctx := withModelCallBudget(context.Background(), 1)

	_, err := bypassIter.GenerateContentIter(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = bypassIter.GenerateContentIter(ctx, &model.Request{})
	require.NoError(t, err)

	_, err = budgetedIter.GenerateContentIter(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = budgetedIter.GenerateContentIter(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 3, underlying.iterCallCount())
}

type countingBudgetModel struct {
	calls atomic.Int64
}

func (m *countingBudgetModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.calls.Add(1)
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Choices: []model.Choice{{
		Message: model.NewAssistantMessage("ok"),
	}}}
	close(ch)
	return ch, nil
}

func (m *countingBudgetModel) Info() model.Info {
	return model.Info{Name: "counting"}
}

func (m *countingBudgetModel) callCount() int64 {
	return m.calls.Load()
}

type capturingBudgetModel struct {
	mu       sync.Mutex
	last     *model.Request
	iterLast *model.Request
}

func (m *capturingBudgetModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.last = req
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{}
	close(ch)
	return ch, nil
}

func (m *capturingBudgetModel) Info() model.Info {
	return model.Info{Name: "capturing"}
}

func (m *capturingBudgetModel) GenerateContentIter(
	_ context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	m.mu.Lock()
	m.iterLast = req
	m.mu.Unlock()
	return func(yield func(*model.Response) bool) {
		yield(&model.Response{})
	}, nil
}

func (m *capturingBudgetModel) lastRequest() *model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.last
}

func (m *capturingBudgetModel) lastIterRequest() *model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.iterLast
}

type prefinalTimeoutBudgetModel struct {
	mu           sync.Mutex
	requests     []*model.Request
	iterRequests []*model.Request
}

type controlledStreamingBudgetModel struct {
	mu           sync.Mutex
	gate         <-chan struct{}
	requests     int
	iterRequests int
}

func (m *controlledStreamingBudgetModel) GenerateContent(
	ctx context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests++
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		ch <- &model.Response{IsPartial: true}
		select {
		case <-m.gate:
			ch <- &model.Response{Done: true}
		case <-ctx.Done():
			ch <- modelCallBudgetErrorResponse(ctx.Err())
		}
	}()
	return ch, nil
}

func (m *controlledStreamingBudgetModel) Info() model.Info {
	return model.Info{Name: "controlled-streaming"}
}

func (m *controlledStreamingBudgetModel) GenerateContentIter(
	ctx context.Context,
	_ *model.Request,
) (model.Seq[*model.Response], error) {
	m.mu.Lock()
	m.iterRequests++
	m.mu.Unlock()
	return func(yield func(*model.Response) bool) {
		if !yield(&model.Response{IsPartial: true}) {
			return
		}
		select {
		case <-m.gate:
			yield(&model.Response{Done: true})
		case <-ctx.Done():
			yield(modelCallBudgetErrorResponse(ctx.Err()))
		}
	}, nil
}

func (m *controlledStreamingBudgetModel) requestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.requests
}

func (m *controlledStreamingBudgetModel) iterRequestCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.iterRequests
}

func (m *prefinalTimeoutBudgetModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, cloneBudgetTestRequest(req))
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	go func() {
		defer close(ch)
		if req == nil || req.Tools == nil {
			ch <- modelCallBudgetTestFinalResponse()
			return
		}
		ch <- &model.Response{
			IsPartial: true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("partial draft"),
			}},
		}
		<-ctx.Done()
		ch <- &model.Response{
			Error: model.ResponseErrorFromError(
				ctx.Err(),
				model.ErrorTypeStreamError,
			),
			Done: true,
		}
	}()
	return ch, nil
}

func (m *prefinalTimeoutBudgetModel) Info() model.Info {
	return model.Info{Name: "prefinal-timeout"}
}

func (m *prefinalTimeoutBudgetModel) GenerateContentIter(
	ctx context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	m.mu.Lock()
	m.iterRequests = append(m.iterRequests, cloneBudgetTestRequest(req))
	m.mu.Unlock()
	return func(yield func(*model.Response) bool) {
		if req == nil || req.Tools == nil {
			yield(modelCallBudgetTestFinalResponse())
			return
		}
		if !yield(&model.Response{
			IsPartial: true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("partial draft"),
			}},
		}) {
			return
		}
		<-ctx.Done()
		yield(&model.Response{
			Error: model.ResponseErrorFromError(
				ctx.Err(),
				model.ErrorTypeStreamError,
			),
			Done: true,
		})
	}, nil
}

func (m *prefinalTimeoutBudgetModel) requestsSnapshot() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.Request(nil), m.requests...)
}

func (m *prefinalTimeoutBudgetModel) iterRequestsSnapshot() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.Request(nil), m.iterRequests...)
}

type innerTimeoutBudgetModel struct {
	mu           sync.Mutex
	requests     []*model.Request
	iterRequests []*model.Request
}

func (m *innerTimeoutBudgetModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	m.requests = append(m.requests, cloneBudgetTestRequest(req))
	m.mu.Unlock()
	ch := make(chan *model.Response, 1)
	if req == nil || req.Tools == nil {
		ch <- modelCallBudgetTestFinalResponse()
	} else {
		ch <- timeoutResponse(5*time.Minute, context.DeadlineExceeded)
	}
	close(ch)
	return ch, nil
}

func (m *innerTimeoutBudgetModel) Info() model.Info {
	return model.Info{Name: "inner-timeout"}
}

func (m *innerTimeoutBudgetModel) GenerateContentIter(
	_ context.Context,
	req *model.Request,
) (model.Seq[*model.Response], error) {
	m.mu.Lock()
	m.iterRequests = append(m.iterRequests, cloneBudgetTestRequest(req))
	m.mu.Unlock()
	return func(yield func(*model.Response) bool) {
		if req == nil || req.Tools == nil {
			yield(modelCallBudgetTestFinalResponse())
			return
		}
		yield(timeoutResponse(5*time.Minute, context.DeadlineExceeded))
	}, nil
}

func (m *innerTimeoutBudgetModel) requestsSnapshot() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.Request(nil), m.requests...)
}

func (m *innerTimeoutBudgetModel) iterRequestsSnapshot() []*model.Request {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]*model.Request(nil), m.iterRequests...)
}

func modelCallBudgetTestFinalResponse() *model.Response {
	return &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage("final answer"),
		}},
	}
}

func cloneBudgetTestRequest(req *model.Request) *model.Request {
	if req == nil {
		return nil
	}
	clone := *req
	if req.Tools != nil {
		clone.Tools = make(map[string]tool.Tool, len(req.Tools))
		for name, t := range req.Tools {
			clone.Tools[name] = t
		}
	}
	clone.Messages = append([]model.Message(nil), req.Messages...)
	return &clone
}

type failingBudgetTokenCounter struct{}

func (failingBudgetTokenCounter) CountTokens(
	context.Context,
	model.Message,
) (int, error) {
	return 0, fmt.Errorf("count tokens")
}

func (failingBudgetTokenCounter) CountTokensRange(
	context.Context,
	[]model.Message,
	int,
	int,
) (int, error) {
	return 0, fmt.Errorf("count tokens range")
}

type countingBudgetIterModel struct {
	countingBudgetModel
	iterCalls atomic.Int64
}

func (m *countingBudgetIterModel) GenerateContentIter(
	_ context.Context,
	_ *model.Request,
) (model.Seq[*model.Response], error) {
	m.iterCalls.Add(1)
	return func(yield func(*model.Response) bool) {
		yield(&model.Response{})
	}, nil
}

func (m *countingBudgetIterModel) iterCallCount() int64 {
	return m.iterCalls.Load()
}

func TestBuildModelCallBudgetRunOptionResolverInjectsBudget(
	t *testing.T,
) {
	t.Parallel()

	resolver := buildModelCallBudgetRunOptionResolver(1, false, 0)
	ctx, runOpts, err := resolver(context.Background(), gateway.RunOptionInput{})
	require.NoError(t, err)
	require.Len(t, runOpts, 1)

	require.Nil(t, modelCallBudgetFromContext(ctx))

	opts := agent.NewRunOptions(runOpts...)
	rawFactory := opts.RuntimeState[modelCallBudgetRuntimeStateKey]
	factory, ok := rawFactory.(*modelCallBudgetFactory)
	require.True(t, ok)
	require.NotNil(t, factory)

	underlying := &countingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	parent := agent.NewInvocation(
		agent.WithInvocationID("parent"),
		agent.WithInvocationRunOptions(opts),
	)
	parentCtx := agent.NewInvocationContext(context.Background(), parent)
	child := parent.Clone(agent.WithInvocationID("child"))
	childCtx := agent.NewInvocationContext(context.Background(), child)

	_, err = wrapped.GenerateContent(parentCtx, &model.Request{})
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(parentCtx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")

	_, err = wrapped.GenerateContent(childCtx, &model.Request{})
	require.NoError(t, err)
	_, err = wrapped.GenerateContent(childCtx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 2, underlying.callCount())
}

func TestBuildModelCallBudgetRunOptionResolverBypassesAuxiliaryCalls(
	t *testing.T,
) {
	t.Parallel()

	resolver := buildModelCallBudgetRunOptionResolver(1, false, 0)
	_, runOpts, err := resolver(context.Background(), gateway.RunOptionInput{})
	require.NoError(t, err)

	opts := agent.NewRunOptions(runOpts...)
	underlying := &countingBudgetModel{}
	budgeted := newModelCallBudgetModel(underlying)
	auxiliary := newModelCallBudgetBypassModel(budgeted)
	inv := agent.NewInvocation(
		agent.WithInvocationID("run"),
		agent.WithInvocationRunOptions(opts),
	)
	ctx := agent.NewInvocationContext(context.Background(), inv)

	_, err = auxiliary.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = auxiliary.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)

	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.NoError(t, err)
	_, err = budgeted.GenerateContent(ctx, &model.Request{})
	require.ErrorContains(t, err, "max LLM calls (1) exceeded")
	require.EqualValues(t, 3, underlying.callCount())
}

func TestBuildModelCallBudgetRunOptionResolverInjectsDeadlineBudget(
	t *testing.T,
) {
	t.Parallel()

	resolver := buildModelCallBudgetRunOptionResolver(
		0,
		false,
		time.Minute,
	)
	ctx, runOpts, err := resolver(context.Background(), gateway.RunOptionInput{})
	require.NoError(t, err)
	require.Len(t, runOpts, 1)
	require.Nil(t, modelCallBudgetFromContext(ctx))

	opts := agent.NewRunOptions(runOpts...)
	underlying := &capturingBudgetModel{}
	wrapped := newModelCallBudgetModel(underlying)
	inv := agent.NewInvocation(
		agent.WithInvocationID("deadline-run"),
		agent.WithInvocationRunOptions(opts),
	)
	deadlineCtx, cancel := context.WithDeadline(
		context.Background(),
		time.Now().Add(time.Second),
	)
	defer cancel()
	deadlineCtx = agent.NewInvocationContext(deadlineCtx, inv)
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("question")},
		Tools:    map[string]tool.Tool{"search": nil},
	}

	_, err = wrapped.GenerateContent(deadlineCtx, req)
	require.NoError(t, err)

	got := underlying.lastRequest()
	require.NotNil(t, got)
	require.Nil(t, got.Tools)
	require.Len(t, got.Messages, 2)
	require.Contains(
		t,
		got.Messages[1].Content,
		"final allowed model call",
	)
}

func TestModelCallBudgetFactoryReusesDeadlineBudgetForInvocation(
	t *testing.T,
) {
	t.Parallel()

	factory := newModelCallBudgetFactory(0, false, time.Minute)
	inv := agent.NewInvocation(agent.WithInvocationID("deadline-run"))

	first := factory.budgetFor(inv)
	second := factory.budgetFor(inv)

	require.NotNil(t, first)
	require.Same(t, first, second)
	require.Equal(t, time.Minute, first.deadlineWindow)
}
