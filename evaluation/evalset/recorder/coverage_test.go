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
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/epochtime"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type marshalToObjectString string

func (m marshalToObjectString) MarshalJSON() ([]byte, error) {
	return []byte(`{"value":"x"}`), nil
}

type invalidStateJSON struct{}

func (invalidStateJSON) MarshalJSON() ([]byte, error) {
	return []byte("{"), nil
}

type stubManager struct {
	getFn        func(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error)
	createFn     func(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error)
	listFn       func(ctx context.Context, appName string) ([]string, error)
	deleteFn     func(ctx context.Context, appName, evalSetID string) error
	getCaseFn    func(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error)
	addCaseFn    func(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error
	updateCaseFn func(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error
	deleteCaseFn func(ctx context.Context, appName, evalSetID, evalCaseID string) error
	closeFn      func() error
}

func (m *stubManager) Get(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	if m.getFn != nil {
		return m.getFn(ctx, appName, evalSetID)
	}
	return nil, os.ErrNotExist
}

func (m *stubManager) Create(ctx context.Context, appName, evalSetID string) (*evalset.EvalSet, error) {
	if m.createFn != nil {
		return m.createFn(ctx, appName, evalSetID)
	}
	return &evalset.EvalSet{EvalSetID: evalSetID}, nil
}

func (m *stubManager) List(ctx context.Context, appName string) ([]string, error) {
	if m.listFn != nil {
		return m.listFn(ctx, appName)
	}
	return nil, nil
}

func (m *stubManager) Delete(ctx context.Context, appName, evalSetID string) error {
	if m.deleteFn != nil {
		return m.deleteFn(ctx, appName, evalSetID)
	}
	return nil
}

func (m *stubManager) GetCase(ctx context.Context, appName, evalSetID, evalCaseID string) (*evalset.EvalCase, error) {
	if m.getCaseFn != nil {
		return m.getCaseFn(ctx, appName, evalSetID, evalCaseID)
	}
	return nil, os.ErrNotExist
}

func (m *stubManager) AddCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if m.addCaseFn != nil {
		return m.addCaseFn(ctx, appName, evalSetID, evalCase)
	}
	return nil
}

func (m *stubManager) UpdateCase(ctx context.Context, appName, evalSetID string, evalCase *evalset.EvalCase) error {
	if m.updateCaseFn != nil {
		return m.updateCaseFn(ctx, appName, evalSetID, evalCase)
	}
	return nil
}

func (m *stubManager) DeleteCase(ctx context.Context, appName, evalSetID, evalCaseID string) error {
	if m.deleteCaseFn != nil {
		return m.deleteCaseFn(ctx, appName, evalSetID, evalCaseID)
	}
	return nil
}

func (m *stubManager) Close() error {
	if m.closeFn != nil {
		return m.closeFn()
	}
	return nil
}

func TestRecorder_OptionsAndHelperBranches(t *testing.T) {
	_, err := New(nil)
	require.ErrorContains(t, err, "evalset manager is nil")
	_, err = New(inmemory.New(), WithName(""))
	require.ErrorContains(t, err, "plugin name is empty")
	_, err = New(inmemory.New(), WithWriteTimeout(-time.Second))
	require.ErrorContains(t, err, "write timeout is negative")
	rec, err := New(inmemory.New(), WithName("named-recorder"))
	require.NoError(t, err)
	assert.Equal(t, "named-recorder", rec.Name())
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

func TestAccumulator_CoversGuardBranches(t *testing.T) {
	acc := newAccumulator()
	ctxMsgs := []model.Message{model.NewSystemMessage("ctx")}
	acc.captureRunInputs(map[string]any{
		"plain":        "value",
		"marshal_fail": make(chan int),
		"decode_fail":  invalidStateJSON{},
		"nil":          nil,
	}, ctxMsgs)
	acc.captureRunInputs(map[string]any{"ignored": true}, []model.Message{model.NewSystemMessage("later")})
	require.Equal(t, "value", acc.sessionInputState["plain"])
	require.IsType(t, "", acc.sessionInputState["marshal_fail"])
	require.Equal(t, "{}", acc.sessionInputState["decode_fail"])
	require.Nil(t, acc.sessionInputState["nil"])
	require.Len(t, acc.contextMessages, 1)
	assert.Equal(t, "ctx", acc.contextMessages[0].Content)
	acc.setUserContent(model.Message{Role: model.RoleUser})
	acc.setUserContent(model.NewUserMessage("user-1"))
	acc.setUserContent(model.NewUserMessage("user-2"))
	assert.Equal(t, "user-1", acc.userContent.Content)
	acc.setFinalResponse(model.Message{Role: model.RoleAssistant})
	acc.setFinalResponse(model.NewAssistantMessage("final-1"))
	acc.setFinalResponse(model.NewAssistantMessage("final-2"))
	assert.Equal(t, "final-2", acc.finalResponse.Content)
	acc.addIntermediateResponse(model.NewUserMessage("ignored"))
	acc.addIntermediateResponse(model.Message{Role: model.RoleAssistant})
	acc.addIntermediateResponse(model.NewAssistantMessage("mid"))
	require.Len(t, acc.intermediateResponses, 1)
	assert.Equal(t, "mid", acc.intermediateResponses[0].Content)
	acc.addToolCall(model.ToolCall{})
	acc.addToolCall(model.ToolCall{ID: "tool-1", Function: model.FunctionDefinitionParam{Arguments: []byte(" ")}})
	acc.addToolCall(model.ToolCall{ID: "tool-1", Function: model.FunctionDefinitionParam{Name: "ignored", Arguments: []byte(`{"x":2}`)}})
	acc.addToolCall(model.ToolCall{ID: "tool-2", Function: model.FunctionDefinitionParam{Name: "search", Arguments: []byte("not-json")}})
	acc.addToolResult("", "ignored", `{"skip":true}`)
	acc.addToolResult("tool-1", "calc", `{"done":true}`)
	acc.addToolResult("tool-3", "lookup", "not-json")
	require.Len(t, acc.tools, 3)
	assert.Equal(t, "calc", acc.tools[0].Name)
	assert.Equal(t, map[string]any{}, acc.tools[0].Arguments)
	assert.Equal(t, map[string]any{"done": true}, acc.tools[0].Result)
	assert.Equal(t, "not-json", acc.tools[1].Arguments)
	assert.Equal(t, "not-json", acc.tools[2].Result)
	snapshot := acc.finalizeAndSnapshot()
	require.True(t, snapshot.finalized)
	require.Len(t, snapshot.tools, 3)
	acc.setRunError(model.ResponseError{Type: "ignored", Message: "ignored"})
	acc.addIntermediateResponse(model.NewAssistantMessage("after-finalize"))
	acc.addToolCall(model.ToolCall{ID: "tool-4"})
	acc.addToolResult("tool-1", "ignored", `{"skip":false}`)
	assert.False(t, acc.isFinalized() == false)
	assert.Equal(t, map[string]any{}, cloneStateMap(nil))
	ch := make(chan int)
	assert.Equal(t, ch, cloneValue("channel", ch))
	assert.Equal(t, marshalToObjectString("value"), cloneValue("marshal-only", marshalToObjectString("value")))
	assert.Nil(t, normalizeStateValue(nil))
	assert.IsType(t, "", normalizeStateValue(make(chan int)))
	assert.Equal(t, "{}", normalizeStateValue(invalidStateJSON{}))
	assert.Equal(t, map[string]any{}, parseToolCallArguments([]byte(" ")))
	assert.Equal(t, "bad-json", parseToolCallArguments([]byte("bad-json")))
	assert.Equal(t, "", parseToolResultContent(""))
	assert.Equal(t, "bad-json", parseToolResultContent("bad-json"))
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

func TestPersist_FunctionBranches(t *testing.T) {
	t.Run("sort and conversation helpers", func(t *testing.T) {
		now := time.Unix(20, 0)
		later := time.Unix(30, 0)
		invocations := []*evalset.Invocation{
			{InvocationID: "b", CreationTimestamp: &epochtime.EpochTime{Time: now}},
			nil,
			{InvocationID: "a", CreationTimestamp: &epochtime.EpochTime{Time: now}},
			{InvocationID: "c", CreationTimestamp: &epochtime.EpochTime{Time: later}},
		}
		sortInvocations(invocations)
		require.Nil(t, conversationByMode(nil, evalset.EvalModeDefault))
		assert.Equal(t, time.Time{}, invocationTime(nil))
		assert.Equal(t, time.Time{}, invocationTime(&evalset.Invocation{}))
		assert.True(t, hasInvocation(invocations, "a"))
		assert.False(t, hasInvocation(invocations, "missing"))
		assert.False(t, hasInvocation(invocations, ""))
		assert.Nil(t, invocations[0])
		assert.Equal(t, "a", invocations[1].InvocationID)
		assert.Equal(t, "b", invocations[2].InvocationID)
		assert.Equal(t, "c", invocations[3].InvocationID)
		evalCase := &evalset.EvalCase{}
		appendConversationByMode(nil, evalset.EvalModeDefault, &evalset.Invocation{InvocationID: "ignored"})
		appendConversationByMode(evalCase, evalset.EvalModeDefault, &evalset.Invocation{InvocationID: "default"})
		appendConversationByMode(evalCase, evalset.EvalModeTrace, &evalset.Invocation{InvocationID: "trace"})
		require.Len(t, conversationByMode(evalCase, evalset.EvalModeDefault), 1)
		require.Len(t, conversationByMode(evalCase, evalset.EvalModeTrace), 1)
	})
	t.Run("ensure eval set paths", func(t *testing.T) {
		rec := &Recorder{manager: &stubManager{getFn: func(context.Context, string, string) (*evalset.EvalSet, error) {
			return &evalset.EvalSet{EvalSetID: "set"}, nil
		}}, locker: newKeyedLocker()}
		require.NoError(t, rec.ensureEvalSet(context.Background(), "app", "set"))
		rec = &Recorder{manager: &stubManager{getFn: func(context.Context, string, string) (*evalset.EvalSet, error) { return nil, errors.New("boom") }}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.ensureEvalSet(context.Background(), "app", "set"), "get eval set app.set")
		callCount := 0
		rec = &Recorder{manager: &stubManager{
			getFn: func(context.Context, string, string) (*evalset.EvalSet, error) {
				callCount++
				if callCount == 1 {
					return nil, os.ErrNotExist
				}
				return &evalset.EvalSet{EvalSetID: "set"}, nil
			},
			createFn: func(context.Context, string, string) (*evalset.EvalSet, error) {
				return nil, errors.New("already exists")
			},
		}, locker: newKeyedLocker()}
		require.NoError(t, rec.ensureEvalSet(context.Background(), "app", "set"))
		rec = &Recorder{manager: &stubManager{
			getFn:    func(context.Context, string, string) (*evalset.EvalSet, error) { return nil, os.ErrNotExist },
			createFn: func(context.Context, string, string) (*evalset.EvalSet, error) { return nil, errors.New("create boom") },
		}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.ensureEvalSet(context.Background(), "app", "set"), "create eval set app.set")
	})
	t.Run("append invocation paths", func(t *testing.T) {
		baseTurn := &turnToPersist{
			appName:    "app",
			evalSetID:  "set",
			evalCaseID: "case",
			sessionIn:  &evalset.SessionInput{AppName: "app"},
			invocation: &evalset.Invocation{InvocationID: "req-2", CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(20, 0)}},
		}
		var added *evalset.EvalCase
		rec := &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) { return nil, os.ErrNotExist },
			addCaseFn: func(_ context.Context, _, _ string, evalCase *evalset.EvalCase) error {
				added = evalCase
				return nil
			},
		}, locker: newKeyedLocker()}
		require.NoError(t, rec.appendInvocation(context.Background(), baseTurn))
		require.NotNil(t, added)
		require.Len(t, added.Conversation, 1)
		rec = &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) { return nil, os.ErrNotExist },
			addCaseFn: func(context.Context, string, string, *evalset.EvalCase) error { return errors.New("add boom") },
		}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.appendInvocation(context.Background(), baseTurn), "add eval case app.set.case")
		rec = &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) {
				return nil, errors.New("get boom")
			},
		}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.appendInvocation(context.Background(), baseTurn), "get eval case app.set.case")
		rec = &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) {
				return &evalset.EvalCase{EvalMode: evalset.EvalModeTrace}, nil
			},
		}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.appendInvocation(context.Background(), baseTurn), "mode mismatch")
		updateCalls := 0
		existing := &evalset.EvalCase{
			EvalID:       "case",
			SessionInput: nil,
			Conversation: []*evalset.Invocation{{InvocationID: "req-1", CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(10, 0)}}},
		}
		rec = &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) { return existing, nil },
			updateCaseFn: func(_ context.Context, _, _ string, evalCase *evalset.EvalCase) error {
				updateCalls++
				existing = evalCase
				return nil
			},
		}, locker: newKeyedLocker()}
		turn := &turnToPersist{
			appName:         "app",
			evalSetID:       "set",
			evalCaseID:      "case",
			sessionIn:       &evalset.SessionInput{AppName: "app", UserID: "u-1"},
			contextMessages: []*model.Message{{Role: model.RoleSystem, Content: "ctx"}},
			invocation:      &evalset.Invocation{InvocationID: "req-2", CreationTimestamp: &epochtime.EpochTime{Time: time.Unix(20, 0)}},
		}
		require.NoError(t, rec.appendInvocation(context.Background(), turn))
		require.Equal(t, 1, updateCalls)
		require.NotNil(t, existing.SessionInput)
		require.Len(t, existing.ContextMessages, 1)
		require.Len(t, existing.Conversation, 2)
		assert.Equal(t, "req-1", existing.Conversation[0].InvocationID)
		assert.Equal(t, "req-2", existing.Conversation[1].InvocationID)
		require.NoError(t, rec.appendInvocation(context.Background(), turn))
		require.Equal(t, 1, updateCalls)
		rec = &Recorder{manager: &stubManager{
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) {
				return &evalset.EvalCase{}, nil
			},
			updateCaseFn: func(context.Context, string, string, *evalset.EvalCase) error { return errors.New("update boom") },
		}, locker: newKeyedLocker()}
		require.ErrorContains(t, rec.appendInvocation(context.Background(), &turnToPersist{
			appName:    "app",
			evalSetID:  "set",
			evalCaseID: "case",
			invocation: &evalset.Invocation{InvocationID: "req-3"},
		}), "update eval case app.set.case")
	})
	t.Run("persist turn context paths", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		rec := &Recorder{manager: &stubManager{}, locker: newKeyedLocker()}
		err := rec.persistTurn(ctx, &turnToPersist{appName: "app", evalSetID: "set", evalCaseID: "case"})
		require.ErrorIs(t, err, context.Canceled)
		rec = &Recorder{manager: &stubManager{
			getFn: func(context.Context, string, string) (*evalset.EvalSet, error) {
				return &evalset.EvalSet{EvalSetID: "set"}, nil
			},
			getCaseFn: func(context.Context, string, string, string) (*evalset.EvalCase, error) { return nil, os.ErrNotExist },
		}, locker: newKeyedLocker()}
		require.NoError(t, rec.persistTurn(context.Background(), &turnToPersist{
			appName:    "app",
			evalSetID:  "set",
			evalCaseID: "case",
			invocation: &evalset.Invocation{InvocationID: "req"},
		}))
	})
}
