//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package recorder

import (
	"context"
	"errors"
	"os"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

func TestRecorder_SingleTurn_Success(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	rec, err := New(manager)
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	inv.RunOptions.RuntimeState = map[string]any{
		"tenant":  "blue",
		"feature": true,
		"profile": map[string]any{"tier": "gold"},
	}
	events := []*event.Event{
		newToolCallEvent(inv, "tool-1", "calc", `{"x":1}`),
		newToolResultEvent(inv, "tool-1", "calc", `{"y":2}`),
		newFinalResponseEvent(inv, "ok"),
		newRunnerCompletionEvent(inv),
	}
	for _, e := range events {
		_, hookErr := rec.onEvent(ctx, inv, e)
		require.NoError(t, hookErr)
	}
	require.NoError(t, rec.Close(ctx))
	c, err := manager.GetCase(ctx, "app-1", "s-1", "s-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.NotNil(t, c.SessionInput)
	assert.Equal(t, "app-1", c.SessionInput.AppName)
	assert.Equal(t, "u-1", c.SessionInput.UserID)
	assert.Equal(t, inv.RunOptions.RuntimeState, c.SessionInput.State)
	require.Len(t, c.Conversation, 1)
	got := c.Conversation[0]
	require.NotNil(t, got)
	assert.Equal(t, "r-1", got.InvocationID)
	require.NotNil(t, got.UserContent)
	assert.Equal(t, model.RoleUser, got.UserContent.Role)
	assert.Equal(t, "hi", got.UserContent.Content)
	require.NotNil(t, got.FinalResponse)
	assert.Equal(t, model.RoleAssistant, got.FinalResponse.Role)
	assert.Equal(t, "ok", got.FinalResponse.Content)
	require.Len(t, got.Tools, 1)
	assert.Equal(t, "tool-1", got.Tools[0].ID)
	assert.Equal(t, "calc", got.Tools[0].Name)
	assert.Equal(t, map[string]any{"x": float64(1)}, got.Tools[0].Arguments)
	assert.Equal(t, map[string]any{"y": float64(2)}, got.Tools[0].Result)
}

func TestRecorder_MultiTurn_AppendsConversation(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	rec, err := New(manager)
	require.NoError(t, err)
	inv1 := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	inv2 := newTestInvocation("app-1", "u-1", "s-1", "r-2", model.NewUserMessage("next"))
	_, err = rec.onEvent(ctx, inv1, newFinalResponseEvent(inv1, "a1"))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv1, newRunnerCompletionEvent(inv1))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv2, newFinalResponseEvent(inv2, "a2"))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv2, newRunnerCompletionEvent(inv2))
	require.NoError(t, err)
	require.NoError(t, rec.Close(ctx))
	c, err := manager.GetCase(ctx, "app-1", "s-1", "s-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.Len(t, c.Conversation, 2)
	assert.Equal(t, "r-1", c.Conversation[0].InvocationID)
	assert.Equal(t, "r-2", c.Conversation[1].InvocationID)
}

func TestRecorder_ErrorTurn_PersistsOnce(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	rec, err := New(manager)
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	errEvent := event.NewErrorEvent("inv-1", "agent-1", model.ErrorTypeRunError, "boom")
	agent.InjectIntoEvent(inv, errEvent)
	_, err = rec.onEvent(ctx, inv, errEvent)
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(ctx))
	c, err := manager.GetCase(ctx, "app-1", "s-1", "s-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.Len(t, c.Conversation, 1)
	require.NotNil(t, c.Conversation[0])
	require.NotNil(t, c.Conversation[0].FinalResponse)
	assert.Contains(t, c.Conversation[0].FinalResponse.Content, "[RUN_ERROR]")
}

func TestRecorder_ResponseErrorEvent_PersistsRunError(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	rec, err := New(manager)
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	errEvent := newResponseErrorEvent(inv, model.ErrorTypeRunError, "boom")
	_, err = rec.onEvent(ctx, inv, errEvent)
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(ctx))
	c, err := manager.GetCase(ctx, "app-1", "s-1", "s-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.Len(t, c.Conversation, 1)
	require.NotNil(t, c.Conversation[0])
	require.NotNil(t, c.Conversation[0].FinalResponse)
	assert.Contains(t, c.Conversation[0].FinalResponse.Content, "[RUN_ERROR]")
}

func TestRecorder_ErrorAfterFinalResponse_PersistsRunError(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	rec, err := New(manager)
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	_, err = rec.onEvent(ctx, inv, newFinalResponseEvent(inv, "ok"))
	require.NoError(t, err)
	errEvent := event.NewErrorEvent("inv-1", "agent-1", model.ErrorTypeRunError, "boom")
	agent.InjectIntoEvent(inv, errEvent)
	_, err = rec.onEvent(ctx, inv, errEvent)
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(ctx))
	c, err := manager.GetCase(ctx, "app-1", "s-1", "s-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.Len(t, c.Conversation, 1)
	require.NotNil(t, c.Conversation[0])
	require.NotNil(t, c.Conversation[0].FinalResponse)
	assert.Contains(t, c.Conversation[0].FinalResponse.Content, "[RUN_ERROR]")
	assert.NotEqual(t, "ok", c.Conversation[0].FinalResponse.Content)
}

func TestRecorder_CustomIDResolvers(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	rec, err := New(
		manager,
		WithEvalSetIDResolver(func(_ context.Context, _ *agent.Invocation) (string, error) {
			return "set-1", nil
		}),
		WithEvalCaseIDResolver(func(_ context.Context, _ *agent.Invocation) (string, error) {
			return "case-1", nil
		}),
	)
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	_, err = rec.onEvent(ctx, inv, newFinalResponseEvent(inv, "ok"))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(ctx))
	c, err := manager.GetCase(ctx, "app-1", "set-1", "case-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.Len(t, c.Conversation, 1)
	assert.Equal(t, "r-1", c.Conversation[0].InvocationID)
}

func TestRecorder_PersistsContextMessagesByDefault(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	rec, err := New(manager)
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	inv.RunOptions.InjectedContextMessages = []model.Message{
		model.NewSystemMessage("You are a careful assistant."),
	}
	_, err = rec.onEvent(ctx, inv, newFinalResponseEvent(inv, "ok"))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(ctx))
	c, err := manager.GetCase(ctx, "app-1", "s-1", "s-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.Len(t, c.ContextMessages, 1)
	assert.Equal(t, model.RoleSystem, c.ContextMessages[0].Role)
	assert.Equal(t, "You are a careful assistant.", c.ContextMessages[0].Content)
	require.Len(t, c.Conversation, 1)
	require.Len(t, c.Conversation[0].ContextMessages, 1)
	assert.Equal(t, "You are a careful assistant.", c.Conversation[0].ContextMessages[0].Content)
}

func TestRecorder_ClonesCapturedMessages(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	rec, err := New(manager)
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	inv.RunOptions.InjectedContextMessages = []model.Message{
		model.NewSystemMessage("before-context"),
	}
	finalEvent := newFinalResponseEvent(inv, "before-final")
	_, err = rec.onEvent(ctx, inv, finalEvent)
	require.NoError(t, err)
	inv.Message.Content = "after-user"
	inv.RunOptions.InjectedContextMessages[0].Content = "after-context"
	finalEvent.Response.Choices[0].Message.Content = "after-final"
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(ctx))
	c, err := manager.GetCase(ctx, "app-1", "s-1", "s-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.Len(t, c.ContextMessages, 1)
	assert.Equal(t, "before-context", c.ContextMessages[0].Content)
	require.Len(t, c.Conversation, 1)
	require.NotNil(t, c.Conversation[0])
	require.NotNil(t, c.Conversation[0].UserContent)
	assert.Equal(t, "hi", c.Conversation[0].UserContent.Content)
	require.NotNil(t, c.Conversation[0].FinalResponse)
	assert.Equal(t, "before-final", c.Conversation[0].FinalResponse.Content)
}

func TestRecorder_TraceModeEnabled_PersistsActualConversation(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	rec, err := New(manager, WithTraceModeEnabled(true))
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	_, err = rec.onEvent(ctx, inv, newFinalResponseEvent(inv, "ok"))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(ctx))
	c, err := manager.GetCase(ctx, "app-1", "s-1", "s-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, evalset.EvalModeTrace, c.EvalMode)
	assert.Empty(t, c.Conversation)
	require.Len(t, c.ActualConversation, 1)
	assert.Equal(t, "r-1", c.ActualConversation[0].InvocationID)
}

func TestRecorder_TraceModeMismatch_DoesNotCorruptExistingCase(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	_, err := manager.Create(ctx, "app-1", "s-1")
	require.NoError(t, err)
	err = manager.AddCase(ctx, "app-1", "s-1", &evalset.EvalCase{
		EvalID:       "s-1",
		EvalMode:     evalset.EvalModeDefault,
		SessionInput: &evalset.SessionInput{AppName: "app-1", UserID: "u-1", State: map[string]any{}},
		Conversation: []*evalset.Invocation{{InvocationID: "existing"}},
	})
	require.NoError(t, err)
	rec, err := New(manager, WithTraceModeEnabled(true))
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	_, err = rec.onEvent(ctx, inv, newFinalResponseEvent(inv, "ok"))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(ctx))
	c, err := manager.GetCase(ctx, "app-1", "s-1", "s-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	assert.Equal(t, evalset.EvalModeDefault, c.EvalMode)
	require.Len(t, c.Conversation, 1)
	assert.Equal(t, "existing", c.Conversation[0].InvocationID)
	assert.Empty(t, c.ActualConversation)
}

func TestRecorder_ConcurrentDifferentCases_SameEvalSet_PersistsBothTurns(t *testing.T) {
	ctx := context.Background()
	inner := inmemory.New()
	manager := newBarrierManager(inner, "app-1", "set-1", 2)
	rec, err := New(
		manager,
		WithEvalSetIDResolver(func(_ context.Context, _ *agent.Invocation) (string, error) {
			return "set-1", nil
		}),
		WithEvalCaseIDResolver(func(_ context.Context, inv *agent.Invocation) (string, error) {
			return inv.RunOptions.RequestID, nil
		}),
	)
	require.NoError(t, err)
	inv1 := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	inv2 := newTestInvocation("app-1", "u-1", "s-2", "r-2", model.NewUserMessage("next"))
	_, err = rec.onEvent(ctx, inv1, newFinalResponseEvent(inv1, "a1"))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv2, newFinalResponseEvent(inv2, "a2"))
	require.NoError(t, err)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, hookErr := rec.onEvent(ctx, inv1, newRunnerCompletionEvent(inv1))
		assert.NoError(t, hookErr)
	}()
	go func() {
		defer wg.Done()
		_, hookErr := rec.onEvent(ctx, inv2, newRunnerCompletionEvent(inv2))
		assert.NoError(t, hookErr)
	}()
	wg.Wait()
	require.NoError(t, rec.Close(ctx))
	_, err = inner.GetCase(ctx, "app-1", "set-1", "r-1")
	require.NoError(t, err)
	_, err = inner.GetCase(ctx, "app-1", "set-1", "r-2")
	require.NoError(t, err)
}

func TestRecorder_CanceledContext_SkipsPersistence(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	manager := inmemory.New()
	rec, err := New(manager)
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	_, err = rec.onEvent(ctx, inv, newFinalResponseEvent(inv, "ok"))
	require.NoError(t, err)
	cancel()
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(context.Background()))
	_, err = manager.GetCase(context.Background(), "app-1", "s-1", "s-1")
	assert.ErrorIs(t, err, os.ErrNotExist)
}

func TestRecorder_AsyncWrite_DetachesContextCancellation(t *testing.T) {
	prev := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(prev)
	ctx, cancel := context.WithCancel(context.Background())
	manager := inmemory.New()
	rec, err := New(manager, WithAsyncWriteEnabled(true))
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	_, err = rec.onEvent(ctx, inv, newFinalResponseEvent(inv, "ok"))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	cancel()
	runtime.Gosched()
	require.NoError(t, rec.Close(context.Background()))
	c, err := manager.GetCase(context.Background(), "app-1", "s-1", "s-1")
	require.NoError(t, err)
	require.NotNil(t, c)
	require.Len(t, c.Conversation, 1)
	require.NotNil(t, c.Conversation[0])
	require.NotNil(t, c.Conversation[0].FinalResponse)
	assert.Equal(t, "ok", c.Conversation[0].FinalResponse.Content)
}

func TestRecorder_WriteTimeout_SetsDeadline(t *testing.T) {
	ctx := context.Background()
	inner := inmemory.New()
	manager := newDeadlineSpyManager(inner)
	rec, err := New(manager, WithWriteTimeout(5*time.Second))
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	_, err = rec.onEvent(ctx, inv, newFinalResponseEvent(inv, "ok"))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(context.Background()))
	assert.True(t, manager.sawDeadline())
}

func TestRecorder_WriteTimeout_Unset_UsesOriginalContext(t *testing.T) {
	ctx := context.Background()
	inner := inmemory.New()
	manager := newDeadlineSpyManager(inner)
	rec, err := New(manager)
	require.NoError(t, err)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	_, err = rec.onEvent(ctx, inv, newFinalResponseEvent(inv, "ok"))
	require.NoError(t, err)
	_, err = rec.onEvent(ctx, inv, newRunnerCompletionEvent(inv))
	require.NoError(t, err)
	require.NoError(t, rec.Close(context.Background()))
	assert.False(t, manager.sawDeadline())
}

func TestRecorder_OnEvent_NilArgs_NoPanic(t *testing.T) {
	ctx := context.Background()
	manager := inmemory.New()
	rec, err := New(manager)
	require.NoError(t, err)
	_, hookErr := rec.onEvent(ctx, nil, nil)
	assert.NoError(t, hookErr)
	inv := newTestInvocation("app-1", "u-1", "s-1", "r-1", model.NewUserMessage("hi"))
	_, hookErr = rec.onEvent(ctx, inv, nil)
	assert.NoError(t, hookErr)
}

func TestRecorder_HelperBranches(t *testing.T) {
	_, err := New(nil)
	require.ErrorContains(t, err, "evalset manager is nil")
	rec, err := New(inmemory.New())
	require.NoError(t, err)
	assert.Equal(t, defaultPluginName, rec.Name())
	rec.Register(nil)
	msg, err := buildFinalResponse(turnSnapshot{hasRunError: true, runError: model.ResponseError{}}, true)
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "[RUN_ERROR] unknown: unknown", msg.Content)
	_, err = buildFinalResponse(turnSnapshot{}, true)
	require.ErrorContains(t, err, "run error is missing")
	final := model.NewAssistantMessage("ok")
	msg, err = buildFinalResponse(turnSnapshot{hasFinalResponse: true, finalResponse: final}, false)
	require.NoError(t, err)
	require.NotNil(t, msg)
	assert.Equal(t, "ok", msg.Content)
	_, err = buildFinalResponse(turnSnapshot{}, false)
	require.ErrorContains(t, err, "final response is missing")
	assert.Equal(t, "[RUN_ERROR] unknown: unknown", formatRunError(model.ResponseError{}))
	_, ok := extractAssistantContentMessage(nil)
	assert.False(t, ok)
	_, ok = extractAssistantContentMessage(&model.Response{})
	assert.False(t, ok)
	_, ok = extractAssistantContentMessage(&model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("user")}}})
	assert.False(t, ok)
	_, ok = extractAssistantContentMessage(&model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant}}}})
	assert.False(t, ok)
	got, ok := extractAssistantContentMessage(&model.Response{Choices: []model.Choice{{Message: model.NewAssistantMessage("assistant")}}})
	require.True(t, ok)
	assert.Equal(t, "assistant", got.Content)
}

func TestRecorder_BuildTurnAndStartWriteBranches(t *testing.T) {
	ctx := context.Background()
	rec := &Recorder{locker: newKeyedLocker(), manager: &stubManager{}}
	snapshot := turnSnapshot{
		hasUserContent:        true,
		userContent:           model.NewUserMessage("hi"),
		hasFinalResponse:      true,
		finalResponse:         model.NewAssistantMessage("ok"),
		sessionInputState:     map[string]any{"tenant": "blue"},
		contextMessages:       []model.Message{model.NewSystemMessage("ctx")},
		intermediateResponses: []model.Message{model.NewAssistantMessage("mid")},
	}
	_, err := rec.buildTurn(ctx, nil, "req", snapshot, false, time.Unix(10, 0))
	require.ErrorContains(t, err, "invocation is nil")
	_, err = rec.buildTurn(ctx, &agent.Invocation{}, "req", snapshot, false, time.Unix(10, 0))
	require.ErrorContains(t, err, "session is nil")
	inv := newTestInvocation("app-1", "u-1", "s-1", "req", model.NewUserMessage("hi"))
	_, err = rec.buildTurn(ctx, inv, "", snapshot, false, time.Unix(10, 0))
	require.ErrorContains(t, err, "request id is empty")
	_, err = rec.buildTurn(ctx, inv, "req", turnSnapshot{}, false, time.Unix(10, 0))
	require.ErrorContains(t, err, "user content is missing")
	emptyAppInv := newTestInvocation("", "u-1", "s-1", "req", model.NewUserMessage("hi"))
	_, err = rec.buildTurn(ctx, emptyAppInv, "req", snapshot, false, time.Unix(10, 0))
	require.ErrorContains(t, err, "app name is empty")
	setErrRecorder := &Recorder{
		manager:           &stubManager{},
		locker:            newKeyedLocker(),
		evalSetIDResolver: func(context.Context, *agent.Invocation) (string, error) { return "", errors.New("set boom") },
	}
	_, err = setErrRecorder.buildTurn(ctx, inv, "req", snapshot, false, time.Unix(10, 0))
	require.ErrorContains(t, err, "resolve eval set id: set boom")
	caseErrRecorder := &Recorder{
		manager:            &stubManager{},
		locker:             newKeyedLocker(),
		evalCaseIDResolver: func(context.Context, *agent.Invocation) (string, error) { return "", errors.New("case boom") },
	}
	_, err = caseErrRecorder.buildTurn(ctx, inv, "req", snapshot, false, time.Unix(10, 0))
	require.ErrorContains(t, err, "resolve eval case id: case boom")
	emptyIDRecorder := &Recorder{
		manager:            &stubManager{},
		locker:             newKeyedLocker(),
		evalSetIDResolver:  func(context.Context, *agent.Invocation) (string, error) { return "", nil },
		evalCaseIDResolver: func(context.Context, *agent.Invocation) (string, error) { return "", nil },
	}
	_, err = emptyIDRecorder.buildTurn(ctx, inv, "req", snapshot, false, time.Unix(10, 0))
	require.ErrorContains(t, err, "eval set id or eval case id is empty")
	traceRecorder := &Recorder{manager: &stubManager{}, locker: newKeyedLocker(), traceModeEnabled: true}
	turn, err := traceRecorder.buildTurn(ctx, inv, "req", snapshot, false, time.Unix(10, 0))
	require.NoError(t, err)
	require.NotNil(t, turn)
	assert.Equal(t, evalset.EvalModeTrace, turn.evalMode)
	require.NotNil(t, turn.invocation.CreationTimestamp)
	assert.Equal(t, time.Unix(10, 0), turn.invocation.CreationTimestamp.Time)
	require.Len(t, turn.contextMessages, 1)
	assert.Equal(t, "ctx", turn.contextMessages[0].Content)
	require.Len(t, turn.invocation.IntermediateResponses, 1)
	assert.Equal(t, "mid", turn.invocation.IntermediateResponses[0].Content)
	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	rec.startWrite(canceledCtx, turn)
	closedRecorder := &Recorder{manager: &stubManager{addCaseFn: func(context.Context, string, string, *evalset.EvalCase) error {
		t.Fatal("unexpected write")
		return nil
	}}, locker: newKeyedLocker(), asyncWriteEnabled: true, closed: true}
	closedRecorder.startWrite(context.Background(), turn)
}

func newTestInvocation(appName, userID, sessionID, requestID string, msg model.Message) *agent.Invocation {
	return &agent.Invocation{
		Session: &session.Session{
			ID:      sessionID,
			AppName: appName,
			UserID:  userID,
		},
		Message: msg,
		RunOptions: agent.RunOptions{
			RequestID: requestID,
		},
	}
}

func newToolCallEvent(inv *agent.Invocation, toolID, toolName, args string) *event.Event {
	tc := model.ToolCall{
		Type: "function",
		ID:   toolID,
		Function: model.FunctionDefinitionParam{
			Name:      toolName,
			Arguments: []byte(args),
		},
	}
	rsp := &model.Response{
		Done: false,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{tc}},
		}},
	}
	evt := event.NewResponseEvent("inv-1", "agent-1", rsp)
	agent.InjectIntoEvent(inv, evt)
	return evt
}

func newToolResultEvent(inv *agent.Invocation, toolID, toolName, content string) *event.Event {
	rsp := &model.Response{
		Object: model.ObjectTypeToolResponse,
		Done:   false,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role:     model.RoleTool,
				ToolID:   toolID,
				ToolName: toolName,
				Content:  content,
			},
		}},
	}
	evt := event.NewResponseEvent("inv-1", "agent-1", rsp)
	agent.InjectIntoEvent(inv, evt)
	return evt
}

func newFinalResponseEvent(inv *agent.Invocation, content string) *event.Event {
	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(content),
		}},
	}
	evt := event.NewResponseEvent("inv-1", "agent-1", rsp)
	agent.InjectIntoEvent(inv, evt)
	return evt
}

func newResponseErrorEvent(inv *agent.Invocation, errType, errMsg string) *event.Event {
	rsp := &model.Response{
		Done: true,
		Error: &model.ResponseError{
			Type:    errType,
			Message: errMsg,
		},
	}
	evt := event.NewResponseEvent("inv-1", "agent-1", rsp)
	agent.InjectIntoEvent(inv, evt)
	return evt
}

func newRunnerCompletionEvent(inv *agent.Invocation) *event.Event {
	rsp := &model.Response{
		Object: model.ObjectTypeRunnerCompletion,
		Done:   true,
	}
	evt := event.NewResponseEvent("inv-1", "runner", rsp)
	agent.InjectIntoEvent(inv, evt)
	return evt
}

type barrierManager struct {
	inner     evalset.Manager
	targetApp string
	targetSet string
	waitN     int
	mu        sync.Mutex
	hits      int
	release   chan struct{}
}

func newBarrierManager(inner evalset.Manager, targetApp, targetSet string, waitN int) *barrierManager {
	return &barrierManager{inner: inner, targetApp: targetApp, targetSet: targetSet, waitN: waitN, release: make(chan struct{})}
}

func (m *barrierManager) waitForMissingEvalSet(ctx context.Context, appName, evalSetID string, err error) {
	if appName != m.targetApp || evalSetID != m.targetSet {
		return
	}
	if !errors.Is(err, os.ErrNotExist) {
		return
	}
	m.mu.Lock()
	m.hits++
	if m.hits == m.waitN {
		close(m.release)
	}
	m.mu.Unlock()
	select {
	case <-m.release:
	case <-ctx.Done():
	}
}

func (m *barrierManager) Get(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	evalSet, err := m.inner.Get(ctx, appName, evalSetID)
	m.waitForMissingEvalSet(ctx, appName, evalSetID, err)
	return evalSet, err
}

func (m *barrierManager) Create(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	return m.inner.Create(ctx, appName, evalSetID)
}

func (m *barrierManager) List(ctx context.Context, appName string) ([]string, error) {
	return m.inner.List(ctx, appName)
}

func (m *barrierManager) Delete(ctx context.Context, appName, evalSetID string) error {
	return m.inner.Delete(ctx, appName, evalSetID)
}

func (m *barrierManager) GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	return m.inner.GetCase(ctx, appName, evalSetID, evalCaseID)
}

func (m *barrierManager) AddCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	return m.inner.AddCase(ctx, appName, evalSetID, evalCase)
}

func (m *barrierManager) UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	return m.inner.UpdateCase(ctx, appName, evalSetID, evalCase)
}

func (m *barrierManager) DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error {
	return m.inner.DeleteCase(ctx, appName, evalSetID, evalCaseID)
}

func (m *barrierManager) Close() error {
	return m.inner.Close()
}

type deadlineSpyManager struct {
	inner       evalset.Manager
	mu          sync.Mutex
	hasDeadline bool
}

func newDeadlineSpyManager(inner evalset.Manager) *deadlineSpyManager {
	return &deadlineSpyManager{inner: inner}
}

func (m *deadlineSpyManager) recordDeadline(ctx context.Context) {
	_, ok := ctx.Deadline()
	if !ok {
		return
	}
	m.mu.Lock()
	m.hasDeadline = true
	m.mu.Unlock()
}

func (m *deadlineSpyManager) sawDeadline() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.hasDeadline
}

func (m *deadlineSpyManager) Get(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	m.recordDeadline(ctx)
	return m.inner.Get(ctx, appName, evalSetID)
}

func (m *deadlineSpyManager) Create(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	m.recordDeadline(ctx)
	return m.inner.Create(ctx, appName, evalSetID)
}

func (m *deadlineSpyManager) List(ctx context.Context, appName string) ([]string, error) {
	m.recordDeadline(ctx)
	return m.inner.List(ctx, appName)
}

func (m *deadlineSpyManager) Delete(ctx context.Context, appName, evalSetID string) error {
	m.recordDeadline(ctx)
	return m.inner.Delete(ctx, appName, evalSetID)
}

func (m *deadlineSpyManager) GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	m.recordDeadline(ctx)
	return m.inner.GetCase(ctx, appName, evalSetID, evalCaseID)
}

func (m *deadlineSpyManager) AddCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	m.recordDeadline(ctx)
	return m.inner.AddCase(ctx, appName, evalSetID, evalCase)
}

func (m *deadlineSpyManager) UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	m.recordDeadline(ctx)
	return m.inner.UpdateCase(ctx, appName, evalSetID, evalCase)
}

func (m *deadlineSpyManager) DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error {
	m.recordDeadline(ctx)
	return m.inner.DeleteCase(ctx, appName, evalSetID, evalCaseID)
}

func (m *deadlineSpyManager) Close() error {
	return m.inner.Close()
}
