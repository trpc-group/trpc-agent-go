//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package todoenforcer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool/todo"
)

// newTestInvocation builds a fresh Invocation+Session pair wired
// into the returned context. Tests use it to exercise BeforeModel
// / AfterModel directly without spinning up a full LLMAgent.
//
// We go through agent.NewInvocation rather than a struct literal
// so the internal noticeMu / noticeChannels are initialised — the
// state-setting helpers in agent/invocation.go warn loudly when
// those are nil.
func newTestInvocation(t *testing.T, agentName string) (context.Context, *agent.Invocation, *session.Session) {
	t.Helper()
	sess := session.NewSession("app", "user", "sid")
	inv := agent.NewInvocation(agent.WithInvocationSession(sess))
	inv.AgentName = agentName
	ctx := agent.NewInvocationContext(context.Background(), inv)
	return ctx, inv, sess
}

// writeTodos persists a list directly into session state, so the
// tests can simulate a state-after-todo_write without going
// through the tool's Call. Bypassing Call keeps these tests
// narrow: we exercise the enforcer's reaction to state, not the
// parser inside todo_write.
func writeTodos(t *testing.T, sess *session.Session, branch string, items []todo.Item) {
	t.Helper()
	raw, err := json.Marshal(items)
	require.NoError(t, err)
	key := todo.DefaultStateKeyPrefix
	if branch != "" {
		key = todo.DefaultStateKeyPrefix + ":" + branch
	}
	sess.SetState(key, raw)
}

// finalRsp returns a Response that satisfies IsFinalResponse.
// Tests use it as the "model just declared we are done" input.
func finalRsp(text string) *model.Response {
	return &model.Response{
		Done:    true,
		Choices: []model.Choice{{Message: model.NewAssistantMessage(text)}},
	}
}

// TestNew_DefaultsApplied is the option-pipeline regression net:
// a default-constructed Enforcer must expose the documented
// defaults across every config dimension, and registering it on a
// fresh extension.Registry must contribute exactly the two tools
// in the canonical order (todo_write first, todo_declare_blocker
// second).
func TestNew_DefaultsApplied(t *testing.T) {
	e := New()
	assert.Equal(t, DefaultExtensionName, e.Name())
	assert.Equal(t, DefaultMaxRetries, e.opts.MaxRetries)
	assert.Equal(t, DefaultDeclareBlockerToolName, e.declareBlockerTool.name)
	assert.NotNil(t, e.opts.NudgeFormatter)

	bundle, err := extension.Collect([]extension.Extension{e})
	require.NoError(t, err)
	require.NotNil(t, bundle)
	tools := bundle.Tools()
	require.Len(t, tools, 2,
		"a default Enforcer must contribute exactly todo_write + todo_declare_blocker")
	assert.Equal(t, todo.DefaultToolName, tools[0].Declaration().Name,
		"todo_write must come first so the natural reading order in the agent declaration matches the docs")
	assert.Equal(t, DefaultDeclareBlockerToolName, tools[1].Declaration().Name,
		"todo_declare_blocker (the escape hatch) must come second")
}

// TestNew_OptionsOverridden ensures every With* option lands in
// the constructed Options, including the ones whose fast paths
// might silently drop bad inputs (WithMaxRetries clamps to
// default on <=0, WithName ignores empty strings, …).
func TestNew_OptionsOverridden(t *testing.T) {
	td := todo.New(todo.WithStateKeyPrefix("temp:plan"))
	e := New(
		WithName("planner-enforcer"),
		WithMaxRetries(7),
		WithTodoTool(td),
		WithDeclareBlockerToolName("need_human"),
		WithDeclareBlockerToolDescription("custom desc"),
	)
	assert.Equal(t, "planner-enforcer", e.Name())
	assert.Equal(t, 7, e.opts.MaxRetries)
	assert.Same(t, td, e.todoTool)
	assert.Equal(t, "need_human", e.declareBlockerTool.name)
	assert.Equal(t, "custom desc", e.declareBlockerTool.description)
}

// TestRegister_WiresExpectedHooks installs the enforcer through
// the same extension.Collect path that LLMAgent.New uses in
// production and confirms the resulting Contributions exposes the
// expected hook counts. This guards against accidental removal of
// one of the two model hooks or accidental registration of agent /
// tool hooks.
func TestRegister_WiresExpectedHooks(t *testing.T) {
	bundle, err := extension.Collect([]extension.Extension{New()})
	require.NoError(t, err)
	require.NotNil(t, bundle)

	modelCallbacks := bundle.ModelCallbacks()
	require.NotNil(t, modelCallbacks)
	assert.Len(t, modelCallbacks.BeforeModel, 1,
		"BeforeModel injects the nudge — exactly one registration expected")
	assert.Len(t, modelCallbacks.AfterModel, 1,
		"AfterModel decides whether the response is allowed to be final — exactly one registration expected")

	// Accessors return nil when a callback surface is empty. The
	// enforcer should leave agent/tool hooks empty: it only operates
	// on model callbacks.
	agentCallbacks := bundle.AgentCallbacks()
	assert.Nil(t, agentCallbacks,
		"enforcer must not register agent hooks")
	toolCallbacks := bundle.ToolCallbacks()
	assert.Nil(t, toolCallbacks,
		"enforcer must not register tool hooks")
}

// TestAfterModel_NoTodos_PassesThrough is the empty-state baseline:
// when no list has ever been written, IsFinalResponse must remain
// true and no per-invocation state should be left behind.
func TestAfterModel_NoTodos_PassesThrough(t *testing.T) {
	ctx, inv, _ := newTestInvocation(t, "a")
	e := New()

	rsp := finalRsp("done")
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	assert.Nil(t, res, "no enforcement → nil result")
	assert.True(t, rsp.Done, "Done must be untouched")
	assert.False(t, reminderPending(inv))
	assert.Equal(t, 0, retryCount(inv))
}

// TestAfterModel_AllCompleted_PassesThrough is the happy path:
// every item completed → model is allowed to declare itself done.
func TestAfterModel_AllCompleted_PassesThrough(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusCompleted},
	})
	e := New()

	rsp := finalRsp("done")
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.True(t, rsp.Done)
	assert.False(t, reminderPending(inv))
}

// TestAfterModel_OpenItems_BlocksAndQueuesReminder is the core
// enforcement assertion: open items present → AfterModel must
//   - return a non-content CustomResponse with Done=false (so
//     llmflow keeps looping without leaking the premature answer),
//   - set reminder_pending so BeforeModel knows to inject,
//   - bump the retry counter.
func TestAfterModel_OpenItems_BlocksAndQueuesReminder(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "Run tests", ActiveForm: "Running tests", Status: todo.StatusInProgress},
		{Content: "Deploy", ActiveForm: "Deploying", Status: todo.StatusPending},
	})
	e := New()

	rsp := finalRsp("done")
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	require.NotNil(t, res, "blocked response must be returned via CustomResponse")
	require.NotNil(t, res.CustomResponse)
	assert.NotSame(t, rsp, res.CustomResponse,
		"blocked responses must be scrubbed, not the original model text")
	assert.True(t, rsp.Done, "original model response must not be mutated")
	assert.Equal(t, "done", rsp.Choices[0].Message.Content)
	assert.False(t, res.CustomResponse.Done,
		"control response must keep the loop running")
	assert.Empty(t, res.CustomResponse.Choices,
		"premature assistant content must not leak to clients or session history")
	assert.False(t, res.CustomResponse.IsValidContent(),
		"control response must not be persisted as assistant content")
	assert.True(t, reminderPending(inv))
	assert.Equal(t, 1, retryCount(inv))
}

func TestAfterModel_ErrorResponse_PassesThrough(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	e := New()

	rsp := &model.Response{
		Done: true,
		Error: &model.ResponseError{
			Type:    model.ErrorTypeAPIError,
			Message: "provider failed",
		},
	}
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.True(t, rsp.Done, "error response must surface unchanged")
	assert.False(t, reminderPending(inv))
	assert.Equal(t, 0, retryCount(inv))

	rsp = finalRsp("done")
	res, err = e.afterModel(ctx, &model.AfterModelArgs{
		Response: rsp,
		Error:    errors.New("stream failed"),
	})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.True(t, rsp.Done, "callback error must bypass enforcement")
	assert.False(t, reminderPending(inv))
	assert.Equal(t, 0, retryCount(inv))
}

// TestAfterModel_ToolCallResponse_PassesThrough confirms that a
// response carrying tool calls is treated as non-final and passes
// through, regardless of pending todos. The natural completion
// pattern (model → todo_write → model → final) relies on this.
func TestAfterModel_ToolCallResponse_PassesThrough(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	e := New()

	rsp := &model.Response{
		Done: true,
		Choices: []model.Choice{{Message: model.Message{
			Role: model.RoleAssistant,
			ToolCalls: []model.ToolCall{
				{ID: "1", Function: model.FunctionDefinitionParam{Name: "todo_write"}},
			},
		}}},
	}
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.False(t, reminderPending(inv))
	assert.Equal(t, 0, retryCount(inv))
}

// TestAfterModel_PartialResponse_PassesThrough ensures we never
// interfere with streaming chunks. Partial responses are by
// definition not final.
func TestAfterModel_PartialResponse_PassesThrough(t *testing.T) {
	ctx, _, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	e := New()

	rsp := finalRsp("partial")
	rsp.IsPartial = true
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	assert.Nil(t, res)
}

// TestAfterModel_BlockerDeclared_ShortCircuits proves that once
// todo_declare_blocker has been called on this invocation,
// AfterModel stops blocking — even with open items. This is the
// model's escape route working as documented in v2: declare a
// blocker, then immediately produce the user-facing final
// message.
func TestAfterModel_BlockerDeclared_ShortCircuits(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	markBlockerDeclared(inv, "user has not granted prod-write permission")
	e := New()

	rsp := finalRsp("Need prod-write to continue.")
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.True(t, rsp.Done)
}

// TestAfterModel_BlockerDeclared_RemainsLatchedForRestOfInvocation
// is the v2-specific guarantee: the latch is permanent for the
// scope of one invocation. A second AfterModel call on the same
// invocation must also pass through, even if new open items
// appear in the meantime.
func TestAfterModel_BlockerDeclared_RemainsLatchedForRestOfInvocation(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	markBlockerDeclared(inv, "missing creds")
	e := New()

	for i := 0; i < 3; i++ {
		if i == 1 {
			writeTodos(t, sess, "", []todo.Item{
				{
					Content:    "new item",
					ActiveForm: "processing new item",
					Status:     todo.StatusInProgress,
				},
			})
		}
		rsp := finalRsp("waiting on user")
		res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
		require.NoError(t, err, "pass %d", i)
		assert.Nil(t, res, "pass %d: latched declaration must keep passing through", i)
		assert.True(t, rsp.Done)
	}
}

// TestAfterModel_RetryBudgetExhausted_FailsOpen verifies the
// fail-open semantics: once the configured budget is gone, the
// response passes through unchanged so an undisciplined model
// cannot trap the runner. An OnEnforce callback observes both
// the blocked events and the final exhaustion event.
func TestAfterModel_RetryBudgetExhausted_FailsOpen(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	var observed []EnforceEvent
	e := New(
		WithMaxRetries(2),
		WithOnEnforce(func(evt EnforceEvent) { observed = append(observed, evt) }),
	)

	for i := 0; i < 2; i++ {
		rsp := finalRsp("done")
		res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
		require.NoError(t, err)
		require.NotNil(t, res, "block %d", i)
		require.NotNil(t, res.CustomResponse, "block %d", i)
		assert.False(t, res.CustomResponse.Done, "block %d", i)
	}
	rsp := finalRsp("done")
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	assert.Nil(t, res, "after exhaustion the response must pass through")
	assert.True(t, rsp.Done)
	assert.Equal(t, 0, retryCount(inv),
		"counter resets on exhaustion for observability cleanliness")

	require.GreaterOrEqual(t, len(observed), 3)
	assert.Equal(t, ReasonBlocked, observed[0].Reason)
	assert.Equal(t, ReasonBlocked, observed[1].Reason)
	assert.Equal(t, ReasonExhausted, observed[len(observed)-1].Reason)
}

// TestBeforeModel_InjectsNudge_WhenPending verifies the second
// leg of the contract: a pending reminder is consumed and the
// formatter output appears as a user message in args.Request.
func TestBeforeModel_InjectsNudge_WhenPending(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "Run tests", ActiveForm: "Running tests", Status: todo.StatusInProgress},
	})
	setReminderPending(inv, true)
	incRetryCount(inv)
	e := New()

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	_, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Len(t, req.Messages, 2)
	assert.Equal(t, model.RoleUser, req.Messages[1].Role)
	assert.Contains(t, req.Messages[1].Content, "Running tests")
	assert.Contains(t, req.Messages[1].Content, todo.DefaultToolName)
	assert.Contains(t, req.Messages[1].Content, DefaultDeclareBlockerToolName)
	assert.False(t, reminderPending(inv),
		"BeforeModel must consume the pending flag")
}

func TestBeforeModel_InjectsConfiguredTodoToolName(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "Run tests", ActiveForm: "Running tests", Status: todo.StatusInProgress},
	})
	setReminderPending(inv, true)
	incRetryCount(inv)
	e := New(WithTodoTool(todo.New(todo.WithToolName("plan_update"))))

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	_, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Len(t, req.Messages, 2)
	assert.Contains(t, req.Messages[1].Content, "plan_update")
	assert.NotContains(t, req.Messages[1].Content, "call "+todo.DefaultToolName,
		"nudge must not tell the model to call a tool name that was overridden")
}

// TestBeforeModel_NoPending_NoOp confirms the BeforeModel branch
// is a true no-op on the common path.
func TestBeforeModel_NoPending_NoOp(t *testing.T) {
	ctx, _, _ := newTestInvocation(t, "a")
	e := New()

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	_, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Len(t, req.Messages, 1)
}

// TestBeforeModel_OpenItems_DisablesStreamingWithoutNudge covers
// the streaming side of hard compliance. Even when no reminder is
// pending yet, the enforcer must turn streaming off while open
// todo items exist; otherwise partial deltas could reach clients
// before AfterModel can reject the final answer.
func TestBeforeModel_OpenItems_DisablesStreamingWithoutNudge(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "Run tests", ActiveForm: "Running tests", Status: todo.StatusInProgress},
	})
	e := New()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}
	_, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.False(t, req.GenerationConfig.Stream,
		"hard enforcement must disable streaming while open todos exist")
	assert.Len(t, req.Messages, 1,
		"no pending reminder means no nudge message is injected yet")
	assert.False(t, reminderPending(inv))
}

func TestBeforeModel_NoOpenItems_LeavesStreamingUntouched(t *testing.T) {
	ctx, _, _ := newTestInvocation(t, "a")
	e := New()

	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{
			Stream: true,
		},
	}
	_, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.True(t, req.GenerationConfig.Stream,
		"streaming remains available when there is nothing to enforce")
	assert.Len(t, req.Messages, 1)
}

// TestBeforeModel_EmptyFormatterOutput_SkipsInjection covers the
// silent-block mode: when the formatter returns "", the pending
// flag is still consumed but no message is appended.
func TestBeforeModel_EmptyFormatterOutput_SkipsInjection(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	setReminderPending(inv, true)
	e := New(WithNudgeFormatter(func(NudgeContext) string { return "" }))

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	_, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Len(t, req.Messages, 1, "silent block must not append a message")
	assert.False(t, reminderPending(inv), "pending flag is still consumed")
}

// TestBeforeModel_NilArgs_NoOp covers the defensive early returns
// at the very top of beforeModel. These cannot happen via the
// llmflow path (which always supplies a non-nil args.Request), but
// they exist so unit tests that drive the hook directly do not
// crash.
func TestBeforeModel_NilArgs_NoOp(t *testing.T) {
	e := New()

	res, err := e.beforeModel(context.Background(), nil)
	require.NoError(t, err)
	assert.Nil(t, res, "nil args must produce a true no-op (nil, nil)")

	res, err = e.beforeModel(context.Background(), &model.BeforeModelArgs{Request: nil})
	require.NoError(t, err)
	assert.Nil(t, res, "nil Request must produce a true no-op (nil, nil)")
}

// TestBeforeModel_OutOfScope_NoOp pins the scoping pass-through.
// Without this branch, the enforcer would silently invoke its
// state-reading code for every BeforeModel call across every
// agent the extension happens to be installed on — wasteful on
// its own and dangerous when those agents share a session-state
// namespace with code that does not expect todoenforcer's keys.
func TestBeforeModel_OutOfScope_NoOp(t *testing.T) {
	e := New(WithScopedAgents("planner"))
	ctx, inv, _ := newTestInvocation(t, "other")
	setReminderPending(inv, true)

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	_, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Len(t, req.Messages, 1, "out-of-scope must not inject a nudge")
	// The pending flag is *deliberately* not consumed by out-of-
	// scope calls — if the user later re-runs the same invocation
	// against the scoped agent, the reminder must still fire.
	assert.True(t, reminderPending(inv),
		"out-of-scope must leave the pending flag intact for the in-scope agent")
}

// TestBeforeModel_PendingButTodosEmpty_SkipsInjection covers the
// "reminder pending, but the todo list has been completed since
// the previous AfterModel turn" race. AfterModel can flag a
// reminder and then the model immediately writes todo_write to
// close the last item; BeforeModel must observe the now-empty
// list and bail rather than injecting a stale-looking nudge.
func TestBeforeModel_PendingButTodosEmpty_SkipsInjection(t *testing.T) {
	ctx, inv, _ := newTestInvocation(t, "a")
	setReminderPending(inv, true) // no writeTodos call → list is empty
	e := New()

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	_, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Len(t, req.Messages, 1,
		"empty todo list must skip injection — no items to nudge about")
	// The pending flag IS consumed (setReminderPending(inv, false)
	// runs before the empty-items check), because the contract
	// is "one pending → at most one inspection attempt".
	assert.False(t, reminderPending(inv),
		"pending flag must be consumed even when the list turns out empty")
}

// TestScopedAgents_OnlyTargetEnforced exercises ScopedAgents and
// the negative branch: out-of-scope invocations get a true pass-
// through and never even read state.
func TestScopedAgents_OnlyTargetEnforced(t *testing.T) {
	e := New(WithScopedAgents("planner"))

	ctxOther, _, sessOther := newTestInvocation(t, "other")
	writeTodos(t, sessOther, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	rspOther := finalRsp("done")
	resOther, err := e.afterModel(ctxOther, &model.AfterModelArgs{Response: rspOther})
	require.NoError(t, err)
	assert.Nil(t, resOther)
	assert.True(t, rspOther.Done)

	ctxPlan, _, sessPlan := newTestInvocation(t, "planner")
	writeTodos(t, sessPlan, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	rspPlan := finalRsp("done")
	resPlan, err := e.afterModel(ctxPlan, &model.AfterModelArgs{Response: rspPlan})
	require.NoError(t, err)
	require.NotNil(t, resPlan)
	require.NotNil(t, resPlan.CustomResponse)
	assert.False(t, resPlan.CustomResponse.Done)
}

// TestBypassAgents_SkipsEnforcement is the inverse of the scoped
// case: an explicit exemption wins over the default everyone-in-
// scope behaviour.
func TestBypassAgents_SkipsEnforcement(t *testing.T) {
	e := New(WithBypassAgents("planner"))

	ctx, _, sess := newTestInvocation(t, "planner")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	rsp := finalRsp("done")
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.True(t, rsp.Done)
}

// TestAfterModel_OnEnforce_PanicSwallowed confirms the recover
// in notify: a misbehaving observer cannot crash the model-
// callback hot path, and enforcement still happens.
func TestAfterModel_OnEnforce_PanicSwallowed(t *testing.T) {
	ctx, _, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "x", ActiveForm: "x", Status: todo.StatusInProgress},
	})
	e := New(WithOnEnforce(func(EnforceEvent) { panic("boom") }))

	rsp := finalRsp("done")
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	require.NotNil(t, res, "enforcement must still happen even if observer panics")
}

// TestDeclareBlockerTool_StoresStateAndReturnsSuccess is the
// v2-defining test of the escape hatch: the tool MUST return a
// normal success result (no StopError, no termination signal),
// MUST latch the blocker_declared flag, MUST persist the reason,
// and MUST surface a ReasonBlockerDeclared event so observers
// can count it.
func TestDeclareBlockerTool_StoresStateAndReturnsSuccess(t *testing.T) {
	ctx, inv, _ := newTestInvocation(t, "a")
	var observed *EnforceEvent
	e := New(WithOnEnforce(func(evt EnforceEvent) {
		observed = &evt
	}))

	args, err := json.Marshal(declareBlockerInput{Reason: "missing prod-write permission"})
	require.NoError(t, err)
	out, err := e.declareBlockerTool.Call(ctx, args)
	require.NoError(t, err,
		"declare-blocker must NOT return an error: the model still needs to send its final message")

	require.NotNil(t, out)
	payload, ok := out.(declareBlockerOutput)
	require.True(t, ok)
	assert.True(t, payload.OK)
	assert.Equal(t, "missing prod-write permission", payload.Reason)

	assert.True(t, blockerDeclared(inv))
	assert.Equal(t, "missing prod-write permission", blockerReason(inv))

	require.NotNil(t, observed)
	assert.Equal(t, ReasonBlockerDeclared, observed.Reason)
	assert.Equal(t, "missing prod-write permission", observed.BlockerReason)
}

// TestDeclareBlockerTool_DoesNotReturnStopError pins down the v2
// behavioural promise: even if some upstream code looks for an
// agent.StopError to terminate the run, the declare-blocker tool
// must NEVER produce one. Termination is the runner's
// responsibility, not the extension's.
func TestDeclareBlockerTool_DoesNotReturnStopError(t *testing.T) {
	ctx, _, _ := newTestInvocation(t, "a")
	e := New()

	args, err := json.Marshal(declareBlockerInput{Reason: "missing creds"})
	require.NoError(t, err)
	_, err = e.declareBlockerTool.Call(ctx, args)
	require.NoError(t, err)

	_, isStop := agent.AsStopError(err)
	assert.False(t, isStop)
}

// TestDeclareBlockerTool_RejectsEmptyReason ensures the friction
// described in tools.go is real: missing or blank reason is a
// regular error so the model retries instead of latching the
// declared flag with a useless empty reason.
func TestDeclareBlockerTool_RejectsEmptyReason(t *testing.T) {
	ctx, inv, _ := newTestInvocation(t, "a")
	e := New()

	args, _ := json.Marshal(declareBlockerInput{Reason: "   "})
	_, err := e.declareBlockerTool.Call(ctx, args)
	require.Error(t, err)
	assert.Contains(t, strings.ToLower(err.Error()), "reason")
	assert.False(t, blockerDeclared(inv),
		"empty-reason rejection must NOT latch the declared flag")
}

// TestDeclareBlockerTool_RejectsMalformedJSON treats parse
// failures as regular errors so the framework can show them back
// to the model — declare-blocker side-effects must NOT happen.
func TestDeclareBlockerTool_RejectsMalformedJSON(t *testing.T) {
	ctx, inv, _ := newTestInvocation(t, "a")
	e := New()

	_, err := e.declareBlockerTool.Call(ctx, []byte("not json"))
	require.Error(t, err)
	assert.False(t, blockerDeclared(inv),
		"parse failure must not latch the declared flag")
}

// TestEndToEnd_DeclareBlockerThenFinalPasses simulates the
// happy path of the v2 escape hatch:
//
//  1. Open items present, model emits a premature final → blocked.
//  2. Model receives the nudge, calls todo_declare_blocker with a
//     real reason → success result, no termination.
//  3. Model emits a follow-up final ("Need X from you to
//     continue") → must pass through unmodified.
//
// This is the contract the extension advertises in doc.go and the
// most important behavioural guarantee for users adopting the
// new escape-hatch semantics.
func TestEndToEnd_DeclareBlockerThenFinalPasses(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "Open prod terminal", ActiveForm: "Opening prod terminal", Status: todo.StatusInProgress},
	})
	e := New()

	rsp1 := finalRsp("done")
	res1, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp1})
	require.NoError(t, err)
	require.NotNil(t, res1, "first final response must be blocked")
	require.NotNil(t, res1.CustomResponse)
	assert.False(t, res1.CustomResponse.Done)
	assert.True(t, reminderPending(inv))

	args, err := json.Marshal(declareBlockerInput{Reason: "prod-write not yet granted to this session"})
	require.NoError(t, err)
	out, err := e.declareBlockerTool.Call(ctx, args)
	require.NoError(t, err, "v2: declare-blocker must succeed without terminating")
	require.NotNil(t, out)
	assert.True(t, blockerDeclared(inv))

	rsp2 := finalRsp("I cannot continue until you grant prod-write.")
	res2, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp2})
	require.NoError(t, err)
	assert.Nil(t, res2,
		"after declare-blocker, the model's final message must pass through")
	assert.True(t, rsp2.Done)
}

// TestEndToEnd_BlockThenContinueThenComplete walks the
// non-blocker recovery cycle: block once, model writes the
// missing item as completed, next final response passes. This
// exercises the original (pre-escape-hatch) loop and proves the
// v2 changes did not regress it.
func TestEndToEnd_BlockThenContinueThenComplete(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "a")
	writeTodos(t, sess, "", []todo.Item{
		{Content: "Run tests", ActiveForm: "Running tests", Status: todo.StatusInProgress},
	})
	e := New()

	rsp1 := finalRsp("done")
	res1, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp1})
	require.NoError(t, err)
	require.NotNil(t, res1)
	require.NotNil(t, res1.CustomResponse)
	assert.False(t, res1.CustomResponse.Done,
		"first final response must be blocked")
	assert.True(t, reminderPending(inv))

	req := &model.Request{Messages: []model.Message{model.NewUserMessage("hi")}}
	_, err = e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	require.Len(t, req.Messages, 2, "BeforeModel must inject the nudge")

	writeTodos(t, sess, "", []todo.Item{
		{Content: "Run tests", ActiveForm: "Running tests", Status: todo.StatusCompleted},
	})

	rsp2 := finalRsp("done for real")
	res2, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp2})
	require.NoError(t, err)
	assert.Nil(t, res2, "with all items completed, response must pass through")
	assert.True(t, rsp2.Done)
}

// TestAfterModel_HonoursCustomTodoToolPrefix is the regression
// guard for a review-time finding: the enforcer originally read
// session state through todo.GetTodos, which hard-codes
// DefaultStateKeyPrefix. A user who wired the enforcer with
// WithTodoTool(todo.New(todo.WithStateKeyPrefix("custom"))) would
// then see the enforcer silently miss every write the tool made
// under "custom:<branch>", and conclude that the list was empty —
// effectively disabling enforcement while the configuration looks
// correct.
//
// The fix reads through e.todoTool.StateKeyPrefix() instead. This
// test pins the contract: when the configured todo tool uses a
// custom prefix, the enforcer must observe open items written
// under that prefix (and trip into the BLOCKED branch), and must
// NOT see items left over under the default prefix from a stale
// installation (cross-prefix isolation).
func TestAfterModel_HonoursCustomTodoToolPrefix(t *testing.T) {
	const customPrefix = "temp:my_custom_todo"
	customTool := todo.New(todo.WithStateKeyPrefix(customPrefix))
	require.Equal(t, customPrefix, customTool.StateKeyPrefix(),
		"sanity: getter must surface the configured prefix")

	e := New(
		WithTodoTool(customTool),
		WithMaxRetries(3),
	)

	ctx, _, sess := newTestInvocation(t, "agent-A")

	openItem := todo.Item{
		Content: "Inspect the staging logs", ActiveForm: "Inspecting the staging logs",
		Status: todo.StatusInProgress,
	}
	raw, err := json.Marshal([]todo.Item{openItem})
	require.NoError(t, err)
	// Write under the CUSTOM prefix the configured tool uses.
	sess.SetState(customPrefix, raw)
	// Also leave a CLEAN list under the DEFAULT prefix to prove the
	// enforcer reads from the configured prefix, not the default.
	cleanRaw, err := json.Marshal([]todo.Item{})
	require.NoError(t, err)
	sess.SetState(todo.DefaultStateKeyPrefix, cleanRaw)

	rsp := finalRsp("all set, see you")
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	require.NotNil(t, res,
		"enforcer must see the in_progress item written under the custom prefix and block")
	require.NotNil(t, res.CustomResponse)
	assert.False(t, res.CustomResponse.Done,
		"enforcer must return Done=false when open items remain at the custom prefix")
}

// The block below covers the small nil-safety / default-fallback
// helpers in enforcer.go that live outside the AfterModel hot
// path. They are trivial individually but they form the contract
// that "creating an Enforcer should never panic on degenerate
// inputs" — a contract the package doc relies on when it tells
// users to share one Enforcer across multiple agents.

func TestEnforcer_Name_DefaultsWhenOptionEmpty(t *testing.T) {
	// New() always seeds opts.Name to DefaultExtensionName via
	// defaultOptions, so this assertion goes through the
	// "configured value" return path.
	assert.Equal(t, DefaultExtensionName, New().Name(),
		"the default constructor must surface DefaultExtensionName")
	assert.Equal(t, "custom-name", New(WithName("custom-name")).Name(),
		"Name() must surface the configured value when WithName was used")

	// To exercise the "opts.Name == \"\"" defensive branch the
	// Enforcer must be constructed without going through New —
	// a zero-value struct has opts.Name == "". This branch
	// shouldn't be reachable from a happy-path caller, but it
	// exists so a future refactor that wires options differently
	// (e.g. lazy options or builder pattern) cannot accidentally
	// surface the empty string as an extension name to the
	// registry.
	bare := &Enforcer{}
	assert.Equal(t, DefaultExtensionName, bare.Name(),
		"empty opts.Name must fall back to DefaultExtensionName")
}

// TestNew_NilOptionAndZeroOptionsRecoverToDefaults documents the
// three defensive fallbacks in New(): nil functional options are
// skipped, a non-positive MaxRetries snaps back to the package
// default, and a nil NudgeFormatter snaps back to
// DefaultNudgeFormatter. None of these can happen via the
// supplied With* helpers, but third-party Option implementations
// could.
func TestNew_NilOptionAndZeroOptionsRecoverToDefaults(t *testing.T) {
	// nil option is skipped (guards against external callers that
	// build []Option slices with conditional entries).
	e := New(nil)
	require.NotNil(t, e)
	assert.Equal(t, DefaultMaxRetries, e.opts.MaxRetries)
	assert.NotNil(t, e.opts.NudgeFormatter)

	// Explicit zero/nil-equivalent options must trip the
	// defaults-restoration branch. We intentionally use raw
	// closures rather than helper With* — the helpers themselves
	// already guard against bad inputs, which would never let the
	// raw zero values reach the defaults-restoration code.
	e = New(
		func(o *Options) { o.MaxRetries = 0 },
		func(o *Options) { o.NudgeFormatter = nil },
	)
	assert.Equal(t, DefaultMaxRetries, e.opts.MaxRetries,
		"non-positive MaxRetries must snap back to DefaultMaxRetries")
	require.NotNil(t, e.opts.NudgeFormatter,
		"nil NudgeFormatter must snap back to DefaultNudgeFormatter")
}

func TestEnforcer_Register_NilRegistryIsNoOp(t *testing.T) {
	// Register(nil) must short-circuit before dereferencing the
	// registry. The contract extension.Collect maintains is that
	// every Extension's Register sees a non-nil Registry, but
	// nothing in the framework stops a future caller from invoking
	// Register manually with nil; surfacing a panic in that case
	// would propagate up to agent.New as a hard crash rather than
	// a clean misuse error.
	assert.NotPanics(t, func() { New().Register(nil) })
}

func TestEnforcer_NotifyBlockerDeclared_NilEnforcerIsNoOp(t *testing.T) {
	// Tools constructed via newDeclareBlockerTool capture *Enforcer
	// in a closure. Defending against e==nil makes the tool safe
	// to call in pure unit tests that bypass enforcer construction
	// (no observable behaviour change in normal operation).
	var e *Enforcer
	var inv *agent.Invocation
	assert.NotPanics(t, func() {
		e.notifyBlockerDeclared(inv, "any")
	})
}

func TestEnforcer_Notify_NoOnEnforce_IsNoOp(t *testing.T) {
	// Default Options.OnEnforce is nil. The hot path must not
	// allocate or panic when there is no observer; this test
	// keeps that invariant honest.
	e := New() // no WithOnEnforce
	assert.NotPanics(t, func() {
		e.notify(EnforceEvent{Reason: ReasonBlocked})
	})
}

func TestEnforcer_Notify_RecoversFromObserverPanic(t *testing.T) {
	// OnEnforce sits on the model-callback hot path; a buggy
	// observer must not be able to crash the agent. The fix is a
	// deferred recover inside notify. The branch is small but the
	// downside of regressing it is "one bad observer brings down
	// the whole agent", so we pin it.
	var calls int
	e := New(WithOnEnforce(func(EnforceEvent) {
		calls++
		panic("observer is buggy")
	}))
	assert.NotPanics(t, func() {
		e.notify(EnforceEvent{Reason: ReasonBlocked})
	})
	assert.Equal(t, 1, calls,
		"observer must have been invoked exactly once before panicking")
}

func TestInvocationHelpers_NilSafe(t *testing.T) {
	// invocationSession/Branch/AgentName are nil-safe shims; pure
	// unit tests that bypass agent.NewInvocation rely on the
	// shims to return zero values rather than panic.
	assert.Nil(t, invocationSession(nil))
	assert.Equal(t, "", invocationBranch(nil))
	assert.Equal(t, "", invocationAgentName(nil))
}

func TestInvocationHelpers_HappyPath(t *testing.T) {
	_, inv, sess := newTestInvocation(t, "agent-A")
	assert.Same(t, sess, invocationSession(inv))
	assert.Equal(t, "agent-A", invocationAgentName(inv))
	// Branch is empty by default in the test helper; the helper
	// chooses not to set Branch so the agent name acts as the
	// implicit branch, mirroring single-agent production setups.
	assert.Equal(t, "", invocationBranch(inv))
}

// TestShouldConsiderResponse_AllBranches covers the response
// triage predicate in one shot. The hot path calls it on every
// AfterModel invocation, so all branches (nil, partial, error,
// tool-call, regular final) need pinning.
func TestShouldConsiderResponse_AllBranches(t *testing.T) {
	e := New()

	assert.False(t, e.shouldConsiderResponse(nil),
		"nil response must short-circuit (defensive)")
	assert.False(t, e.shouldConsiderResponse(&model.Response{IsPartial: true}),
		"streaming partials must never trigger enforcement")
	assert.False(t, e.shouldConsiderResponse(&model.Response{
		Done:  true,
		Error: &model.ResponseError{Type: "x", Message: "boom"},
	}), "error responses must bypass enforcement")

	// Tool-call responses are continuation signals. They must
	// pass through so the model can complete open todos via tools.
	toolCallRsp := &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role:      model.RoleAssistant,
				ToolCalls: []model.ToolCall{{ID: "x", Type: "function"}},
			},
		}},
	}
	assert.False(t, e.shouldConsiderResponse(toolCallRsp),
		"tool-call responses must stay out of enforcement")

	// A regular final response is exactly what enforcement
	// targets.
	finalR := finalRsp("done")
	assert.True(t, e.shouldConsiderResponse(finalR),
		"regular final response must be considered for enforcement")
}

// TestAfterModel_DefaultPrefixUnchangedWhenNoCustomTool guards the
// other direction of the same fix: without WithTodoTool, behaviour
// must be byte-for-byte identical to before — the enforcer
// constructs todo.New() (DefaultStateKeyPrefix) and reads through
// that same default. Catches accidental regressions where a future
// refactor of the prefix plumbing might hard-code "custom" or drop
// the default entirely.
func TestAfterModel_DefaultPrefixUnchangedWhenNoCustomTool(t *testing.T) {
	e := New(WithMaxRetries(3))

	ctx, _, sess := newTestInvocation(t, "agent-A")
	writeTodos(t, sess, "", []todo.Item{{
		Content: "Run tests", ActiveForm: "Running tests",
		Status: todo.StatusPending,
	}})

	rsp := finalRsp("see you")
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: rsp})
	require.NoError(t, err)
	require.NotNil(t, res, "default-prefix path must remain functional")
	require.NotNil(t, res.CustomResponse)
	assert.False(t, res.CustomResponse.Done)
}
