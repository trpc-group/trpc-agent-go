//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package goal

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestOptionsAndNewFallbacks(t *testing.T) {
	opts := Options{
		Name:               "original",
		StateKey:           "original:key",
		GetGoalToolName:    "get_original",
		CreateGoalToolName: "create_original",
		UpdateGoalToolName: "update_original",
		InjectGuidance:     true,
		MaxRetries:         2,
	}
	customFormatter := func(ctx NudgeContext) string { return ctx.AgentName }
	var enforced EnforceEvent

	WithName("")(&opts)
	WithStateKey("")(&opts)
	WithToolNames("", "", "")(&opts)
	WithMaxRetries(0)(&opts)
	WithNudgeFormatter(nil)(&opts)
	assert.Equal(t, "original", opts.Name)
	assert.Equal(t, "original:key", opts.StateKey)
	assert.Equal(t, "get_original", opts.GetGoalToolName)
	assert.Equal(t, "create_original", opts.CreateGoalToolName)
	assert.Equal(t, "update_original", opts.UpdateGoalToolName)
	assert.Equal(t, 2, opts.MaxRetries)
	assert.Nil(t, opts.NudgeFormatter)

	WithName("custom")(&opts)
	WithStateKey("custom:key")(&opts)
	WithToolNames("get_custom", "create_custom", "update_custom")(&opts)
	WithGuidance(false)(&opts)
	WithMaxRetries(5)(&opts)
	WithNudgeFormatter(customFormatter)(&opts)
	WithOnEnforce(func(evt EnforceEvent) { enforced = evt })(&opts)
	assert.Equal(t, "custom", opts.Name)
	assert.Equal(t, "custom:key", opts.StateKey)
	assert.Equal(t, "get_custom", opts.GetGoalToolName)
	assert.Equal(t, "create_custom", opts.CreateGoalToolName)
	assert.Equal(t, "update_custom", opts.UpdateGoalToolName)
	assert.False(t, opts.InjectGuidance)
	assert.Equal(t, 5, opts.MaxRetries)
	assert.NotNil(t, opts.NudgeFormatter)
	opts.OnEnforce(EnforceEvent{Reason: ReasonBlocked})
	assert.Equal(t, ReasonBlocked, enforced.Reason)

	e := New(
		func(o *Options) {
			o.Name = ""
			o.StateKey = ""
			o.GetGoalToolName = ""
			o.CreateGoalToolName = ""
			o.UpdateGoalToolName = ""
			o.MaxRetries = 0
			o.NudgeFormatter = nil
		},
	)
	assert.Equal(t, DefaultExtensionName, e.Name())
	assert.Equal(t, DefaultStateKey, e.opts.StateKey)
	assert.Equal(t, DefaultGetGoalToolName, e.getGoalTool.name)
	assert.Equal(t, DefaultCreateGoalToolName, e.createGoalTool.name)
	assert.Equal(t, DefaultUpdateGoalToolName, e.updateGoalTool.name)
	assert.Equal(t, DefaultMaxRetries, e.opts.MaxRetries)
	assert.NotNil(t, e.opts.NudgeFormatter)

	var nilExt *Extension
	assert.Equal(t, DefaultExtensionName, nilExt.Name())
}

func TestStateHelpersAndStartPaths(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "goal-extension-test", UserID: "user", SessionID: "sid-state"}

	_, ok, err := GetGoalWithStateKey(nil, "")
	require.NoError(t, err)
	assert.False(t, ok)

	sess := session.NewSession(key.AppName, key.UserID, key.SessionID)
	sess.SetState(DefaultStateKey, []byte("null"))
	_, ok, err = GetGoalWithStateKey(sess, "")
	require.NoError(t, err)
	assert.False(t, ok)

	sess.SetState(DefaultStateKey, []byte("  "))
	_, ok, err = GetGoalWithStateKey(sess, "")
	require.NoError(t, err)
	assert.False(t, ok)

	sess.SetState(DefaultStateKey, []byte("{"))
	_, _, err = GetGoalWithStateKey(sess, "")
	require.Error(t, err)

	sess.SetState(DefaultStateKey, []byte(`{"id":"goal-id"}`))
	_, ok, err = GetGoalWithStateKey(sess, "")
	require.NoError(t, err)
	assert.False(t, ok)

	_, err = NewActiveGoal("   ")
	require.Error(t, err)

	raw, err := encodeGoal(nil)
	require.NoError(t, err)
	assert.Equal(t, "null", string(raw))

	err = writeGoalToSession(nil, DefaultStateKey, &Goal{ID: "x"})
	require.Error(t, err)

	assert.Equal(t, 0, retryCount(nil))
	assert.Equal(t, 0, incRetryCount(nil))
	resetRetryCount(nil)
	assert.False(t, reminderPending(nil))
	setReminderPending(nil, true)

	inv := agent.NewInvocation(agent.WithInvocationSession(sess))
	assert.Equal(t, 0, retryCount(inv))
	assert.Equal(t, 1, incRetryCount(inv))
	assert.Equal(t, 1, retryCount(inv))
	setReminderPending(inv, true)
	assert.True(t, reminderPending(inv))
	setReminderPending(inv, false)
	assert.False(t, reminderPending(inv))
	resetRetryCount(inv)
	assert.Equal(t, 0, retryCount(inv))

	startCfg := startOptions{stateKey: "existing:key"}
	WithStartStateKey("")(&startCfg)
	assert.Equal(t, "existing:key", startCfg.stateKey)
	WithStartStateKey("custom:key")(&startCfg)
	assert.Equal(t, "custom:key", startCfg.stateKey)

	sessionService := inmemory.NewSessionService()
	created, err := Start(ctx, sessionService, key, "first objective", WithStartStateKey("custom:key"))
	require.NoError(t, err)
	require.NotNil(t, created)

	stored, err := sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	got, ok, err := GetGoalWithStateKey(stored, "custom:key")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "first objective", got.Objective)

	updated, err := Start(ctx, sessionService, key, "second objective", WithStartStateKey("custom:key"))
	require.NoError(t, err)
	require.NotNil(t, updated)
	stored, err = sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	got, ok, err = GetGoalWithStateKey(stored, "custom:key")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "second objective", got.Objective)

	_, err = Start(ctx, nil, key, "third objective")
	require.Error(t, err)
	_, err = Start(ctx, sessionService, key, "   ")
	require.Error(t, err)
}

func TestStart_ReturnsGetSessionError(t *testing.T) {
	ctx := context.Background()
	key := session.Key{AppName: "goal-extension-test", UserID: "user", SessionID: "sid-error"}
	sessionService := &getSessionErrorService{err: assert.AnError}

	_, err := Start(ctx, sessionService, key, "produce final plan")
	require.ErrorIs(t, err, assert.AnError)
	assert.False(t, sessionService.createCalled)
}

type getSessionErrorService struct {
	session.Service
	err          error
	createCalled bool
}

func (s *getSessionErrorService) GetSession(
	context.Context,
	session.Key,
	...session.Option,
) (*session.Session, error) {
	return nil, s.err
}

func (s *getSessionErrorService) CreateSession(
	context.Context,
	session.Key,
	session.StateMap,
	...session.Option,
) (*session.Session, error) {
	s.createCalled = true
	return nil, nil
}

func TestFormattingAndInvocationHelpers(t *testing.T) {
	guidance := renderGuidance("", "", "")
	assert.Contains(t, guidance, DefaultCreateGoalToolName)
	assert.Contains(t, guidance, DefaultGetGoalToolName)
	assert.Contains(t, guidance, DefaultUpdateGoalToolName)

	msg := DefaultNudgeFormatter(NudgeContext{AttemptNumber: 2, MaxRetries: 4})
	assert.Contains(t, msg, "(unknown objective)")
	assert.Contains(t, msg, DefaultUpdateGoalToolName)

	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("existing system"),
			model.NewUserMessage("hello"),
		},
	}
	insertGuidance(req, "  extra guidance  ")
	require.Len(t, req.Messages, 3)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Equal(t, model.RoleSystem, req.Messages[1].Role)
	assert.Equal(t, "extra guidance", req.Messages[1].Content)
	assert.Equal(t, model.RoleUser, req.Messages[2].Role)

	insertGuidance(nil, "ignored")
	req = &model.Request{Messages: []model.Message{model.NewUserMessage("hello")}}
	insertGuidance(req, "   ")
	require.Len(t, req.Messages, 1)

	assert.Nil(t, invocationSession(nil))
	assert.Equal(t, "", invocationAgentName(nil))
	assert.Contains(t, (*Extension)(nil).guidance(), DefaultCreateGoalToolName)
	assert.Equal(t, DefaultGetGoalToolName, (*Extension)(nil).getGoalToolName())
	assert.Equal(t, DefaultCreateGoalToolName, (*Extension)(nil).createGoalToolName())
	assert.Equal(t, DefaultUpdateGoalToolName, (*Extension)(nil).updateGoalToolName())
	assert.Equal(t, "configured-get", (&Extension{opts: Options{GetGoalToolName: "configured-get"}}).getGoalToolName())
	assert.Equal(t, "configured-create", (&Extension{opts: Options{CreateGoalToolName: "configured-create"}}).createGoalToolName())
	assert.Equal(t, "configured", (&Extension{opts: Options{UpdateGoalToolName: "configured"}}).updateGoalToolName())
	assert.Equal(t, "get-tool", (&Extension{getGoalTool: &goalTool{name: "get-tool"}}).getGoalToolName())
	assert.Equal(t, "create-tool", (&Extension{createGoalTool: &goalTool{name: "create-tool"}}).createGoalToolName())
	assert.Equal(t, "tool-name", (&Extension{updateGoalTool: &goalTool{name: "tool-name"}}).updateGoalToolName())

	rsp := blockedControlResponse(nil)
	require.NotNil(t, rsp)
	assert.False(t, rsp.Done)
}

func TestGoalToolEdgeCases(t *testing.T) {
	getCtx, _, getSess := newTestInvocation(t, "planner")
	getTool := newGoalTool(toolKindGet, "", "")
	assert.Equal(t, DefaultGetGoalToolName, getTool.Declaration().Name)

	result, err := getTool.Call(getCtx, nil)
	require.NoError(t, err)
	out := result.(goalToolOutput)
	assert.Equal(t, "No session goal is set.", out.Message)
	assert.Nil(t, out.Goal)

	activeGoal, err := NewActiveGoal("inspect status")
	require.NoError(t, err)
	require.NoError(t, writeGoalToSession(getSess, DefaultStateKey, activeGoal))
	result, err = getTool.Call(getCtx, nil)
	require.NoError(t, err)
	out = result.(goalToolOutput)
	assert.Equal(t, "Current session goal loaded.", out.Message)
	require.NotNil(t, out.Goal)

	createTool := newGoalTool(toolKindCreate, "", "")
	_, err = createTool.Call(context.Background(), []byte(`{"objective":"x"}`))
	require.Error(t, err)
	_, err = createTool.Call(getCtx, nil)
	require.Error(t, err)
	_, err = createTool.Call(getCtx, []byte(`{"objective":"   "}`))
	require.Error(t, err)

	updateTool := newGoalTool(toolKindUpdate, "", "")
	_, err = updateTool.Call(context.Background(), []byte(`{"status":"complete"}`))
	require.Error(t, err)
	_, err = updateTool.Call(getCtx, nil)
	require.Error(t, err)
	_, err = updateTool.Call(getCtx, []byte(`{"status":"active"}`))
	require.Error(t, err)
	_, err = updateTool.Call(getCtx, []byte(`{"status":"bogus"}`))
	require.Error(t, err)

	updateCtx, _, _ := newTestInvocation(t, "planner")
	_, err = updateTool.Call(updateCtx, []byte(`{"status":"complete"}`))
	require.Error(t, err)

	terminalCtx, _, terminalSess := newTestInvocation(t, "planner")
	terminalGoal, err := NewActiveGoal("already done")
	require.NoError(t, err)
	now := terminalGoal.UpdatedAtUnix
	terminalGoal.Status = GoalStatusBlocked
	terminalGoal.TerminalAtUnix = &now
	require.NoError(t, writeGoalToSession(terminalSess, DefaultStateKey, terminalGoal))
	_, err = updateTool.Call(terminalCtx, []byte(`{"status":"complete"}`))
	require.Error(t, err)

	assert.Nil(t, getTool.StateDeltaForInvocation(nil, "call", nil, []byte(`{"message":"x"}`)))
	assert.Nil(t, createTool.StateDeltaForInvocation(nil, "call", nil, nil))
	assert.Nil(t, createTool.StateDeltaForInvocation(nil, "call", nil, []byte("{")))
	assert.Nil(t, createTool.StateDeltaForInvocation(nil, "call", nil, []byte(`{"message":"x"}`)))

	deltaCtx, deltaInv, _ := newTestInvocation(t, "planner")
	_, err = createTool.Call(deltaCtx, []byte(`{"objective":"persist through rewritten result"}`))
	require.NoError(t, err)
	delta := createTool.StateDeltaForInvocation(deltaInv, "call", nil, []byte("rewritten result"))
	require.Contains(t, delta, DefaultStateKey)
	var persisted Goal
	require.NoError(t, json.Unmarshal(delta[DefaultStateKey], &persisted))
	assert.Equal(t, "persist through rewritten result", persisted.Objective)

	unknownTool := newGoalTool(99, "mystery", "")
	assert.Equal(t, "mystery", unknownTool.Declaration().Name)
	_, err = unknownTool.Call(getCtx, nil)
	require.Error(t, err)
}

func TestExtensionEdgeCases(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "planner")
	goalRecord, err := NewActiveGoal("edge coverage goal")
	require.NoError(t, err)
	require.NoError(t, writeGoalToSession(sess, "custom:key", goalRecord))

	e := New(
		WithStateKey("custom:key"),
		WithGuidance(false),
		WithNudgeFormatter(func(NudgeContext) string { return "" }),
	)
	e.Register(nil)

	beforeRes, err := e.beforeModel(ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, beforeRes)
	beforeRes, err = e.beforeModel(ctx, &model.BeforeModelArgs{})
	require.NoError(t, err)
	assert.Nil(t, beforeRes)

	setReminderPending(inv, true)
	req := &model.Request{Messages: []model.Message{model.NewUserMessage("continue")}}
	beforeRes, err = e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Nil(t, beforeRes)
	require.Len(t, req.Messages, 1)
	assert.False(t, reminderPending(inv))

	sess.SetState("custom:key", []byte("{"))
	beforeRes, err = e.beforeModel(ctx, &model.BeforeModelArgs{Request: &model.Request{Messages: []model.Message{model.NewUserMessage("continue")}}})
	require.NoError(t, err)
	assert.Nil(t, beforeRes)

	afterRes, err := e.afterModel(ctx, &model.AfterModelArgs{Response: finalRsp("done")})
	require.NoError(t, err)
	assert.Nil(t, afterRes)

	afterRes, err = e.afterModel(ctx, nil)
	require.NoError(t, err)
	assert.Nil(t, afterRes)
	afterRes, err = e.afterModel(ctx, &model.AfterModelArgs{})
	require.NoError(t, err)
	assert.Nil(t, afterRes)
	afterRes, err = e.afterModel(ctx, &model.AfterModelArgs{Error: assert.AnError, Response: finalRsp("done")})
	require.NoError(t, err)
	assert.Nil(t, afterRes)
	afterRes, err = e.afterModel(ctx, &model.AfterModelArgs{Response: &model.Response{Error: &model.ResponseError{Message: "upstream"}}})
	require.NoError(t, err)
	assert.Nil(t, afterRes)

	assert.False(t, e.shouldConsiderResponse(nil))
	assert.False(t, e.shouldConsiderResponse(partialRsp("partial")))
	assert.False(t, e.shouldConsiderResponse(&model.Response{Error: &model.ResponseError{Message: "upstream"}}))
	assert.False(t, e.shouldConsiderResponse(toolCallRsp(DefaultUpdateGoalToolName, `{"status":"complete"}`)))
	assert.True(t, e.shouldConsiderResponse(finalRsp("done")))

	panicObserver := New(WithOnEnforce(func(EnforceEvent) { panic("boom") }))
	panicObserver.notify(EnforceEvent{Reason: ReasonBlocked})
}
