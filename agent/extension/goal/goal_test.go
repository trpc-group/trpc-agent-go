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
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/extension"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func newTestInvocation(t *testing.T, agentName string) (context.Context, *agent.Invocation, *session.Session) {
	t.Helper()
	sess := session.NewSession("app", "user", "sid")
	inv := agent.NewInvocation(
		agent.WithInvocationSession(sess),
	)
	inv.AgentName = agentName
	ctx := agent.NewInvocationContext(context.Background(), inv)
	return ctx, inv, sess
}

func finalRsp(text string) *model.Response {
	return &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index:   0,
			Message: model.NewAssistantMessage(text),
		}},
	}
}

func partialRsp(text string) *model.Response {
	return &model.Response{
		Done:      false,
		IsPartial: true,
		Choices: []model.Choice{{
			Index: 0,
			Delta: model.Message{
				Role:    model.RoleAssistant,
				Content: text,
			},
		}},
	}
}

func toolCallRsp(name string, args string) *model.Response {
	return &model.Response{
		Done: true,
		Choices: []model.Choice{{
			Index: 0,
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					ID:   "call_goal",
					Type: "function",
					Function: model.FunctionDefinitionParam{
						Name:      name,
						Arguments: []byte(args),
					},
				}},
			},
		}},
	}
}

func TestNew_DefaultsApplied(t *testing.T) {
	e := New()
	assert.Equal(t, DefaultExtensionName, e.Name())
	assert.Equal(t, DefaultStateKey, e.opts.StateKey)
	assert.Equal(t, DefaultMaxRetries, e.opts.MaxRetries)
	assert.True(t, e.opts.InjectGuidance)
	assert.NotNil(t, e.opts.NudgeFormatter)

	bundle, err := extension.Collect([]extension.Extension{e})
	require.NoError(t, err)
	require.NotNil(t, bundle)

	tools := bundle.Tools()
	require.Len(t, tools, 3)
	assert.Equal(t, DefaultGetGoalToolName, tools[0].Declaration().Name)
	assert.Equal(t, DefaultCreateGoalToolName, tools[1].Declaration().Name)
	assert.Equal(t, DefaultUpdateGoalToolName, tools[2].Declaration().Name)

	modelCallbacks := bundle.ModelCallbacks()
	require.NotNil(t, modelCallbacks)
	assert.Len(t, modelCallbacks.BeforeModel, 1)
	assert.Len(t, modelCallbacks.AfterModel, 1)
	assert.Nil(t, bundle.AgentCallbacks())
	assert.Nil(t, bundle.ToolCallbacks())
}

func TestCreateGoalToolWritesStateAndDelta(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "planner")
	tl := newGoalTool(toolKindCreate, DefaultCreateGoalToolName, DefaultStateKey)

	result, err := tl.Call(ctx, []byte(`{"objective":"ship a migration plan"}`))
	require.NoError(t, err)
	out, ok := result.(goalToolOutput)
	require.True(t, ok)
	require.NotNil(t, out.Goal)
	assert.Equal(t, GoalStatusActive, out.Goal.Status)

	got, exists, err := GetGoal(sess)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, out.Goal.ID, got.ID)
	assert.Equal(t, "ship a migration plan", got.Objective)

	raw, err := json.Marshal(out)
	require.NoError(t, err)
	delta := tl.StateDeltaForInvocation(inv, "call", nil, raw)
	require.Contains(t, delta, DefaultStateKey)
	var persisted Goal
	require.NoError(t, json.Unmarshal(delta[DefaultStateKey], &persisted))
	assert.Equal(t, out.Goal.ID, persisted.ID)
}

func TestStart_RequiresSessionKey(t *testing.T) {
	ctx := context.Background()
	sessionService := inmemory.NewSessionService()
	_, err := Start(ctx, sessionService, session.Key{
		AppName: "goal-extension-test",
		UserID:  "user",
	}, "produce final plan")
	require.ErrorIs(t, err, session.ErrSessionIDRequired)
}

func TestCreateGoalToolRejectsActiveGoal(t *testing.T) {
	ctx, _, _ := newTestInvocation(t, "planner")
	tl := newGoalTool(toolKindCreate, DefaultCreateGoalToolName, DefaultStateKey)

	_, err := tl.Call(ctx, []byte(`{"objective":"first"}`))
	require.NoError(t, err)
	_, err = tl.Call(ctx, []byte(`{"objective":"second"}`))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "active goal already exists")
}

func TestUpdateGoalToolMarksComplete(t *testing.T) {
	ctx, _, sess := newTestInvocation(t, "planner")
	create := newGoalTool(toolKindCreate, DefaultCreateGoalToolName, DefaultStateKey)
	update := newGoalTool(toolKindUpdate, DefaultUpdateGoalToolName, DefaultStateKey)

	_, err := create.Call(ctx, []byte(`{"objective":"finish"}`))
	require.NoError(t, err)
	result, err := update.Call(ctx, []byte(`{"status":"complete"}`))
	require.NoError(t, err)
	out := result.(goalToolOutput)
	require.NotNil(t, out.Goal)
	assert.Equal(t, GoalStatusComplete, out.Goal.Status)
	require.NotNil(t, out.Goal.TerminalAtUnix)

	got, exists, err := GetGoal(sess)
	require.NoError(t, err)
	require.True(t, exists)
	assert.Equal(t, GoalStatusComplete, got.Status)
	require.NotNil(t, got.TerminalAtUnix)
}

func TestAfterModel_ActiveGoalBlocksAndBeforeModelInjectsNudge(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "planner")
	g, err := NewActiveGoal("finish a migration plan")
	require.NoError(t, err)
	require.NoError(t, writeGoalToSession(sess, DefaultStateKey, g))

	e := New()
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: finalRsp("done")})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.CustomResponse)
	assert.False(t, res.CustomResponse.Done)
	assert.Empty(t, res.CustomResponse.Choices)
	assert.True(t, reminderPending(inv))
	assert.Equal(t, 1, retryCount(inv))

	req := &model.Request{
		Messages:         []model.Message{model.NewUserMessage("continue")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}
	before, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Nil(t, before)
	assert.True(t, req.GenerationConfig.Stream)
	assert.False(t, reminderPending(inv))
	require.Len(t, req.Messages, 3)
	assert.Equal(t, model.RoleSystem, req.Messages[0].Role)
	assert.Equal(t, model.RoleUser, req.Messages[1].Role)
	assert.Equal(t, model.RoleUser, req.Messages[2].Role)
	assert.Contains(t, req.Messages[2].Content, "finish a migration plan")
}

func TestBeforeModel_InjectsConfiguredToolNames(t *testing.T) {
	ctx, _, _ := newTestInvocation(t, "planner")
	req := &model.Request{
		Messages: []model.Message{model.NewUserMessage("continue")},
	}
	e := New(WithToolNames("goal_read", "goal_create", "goal_update"))

	before, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: req})
	require.NoError(t, err)
	assert.Nil(t, before)
	require.Len(t, req.Messages, 2)
	guidance := req.Messages[0].Content
	assert.Contains(t, guidance, "Goal tools require serial semantics")
	assert.Contains(t, guidance, "call at most one goal tool")
	assert.Contains(t, guidance, "Do not call goal_create and goal_update in the same response")
	assert.Contains(t, guidance, "Use goal_create")
	assert.Contains(t, guidance, "Use goal_read")
	assert.Contains(t, guidance, "Use goal_update")
	assert.NotContains(t, guidance, "create_goal")
	assert.NotContains(t, guidance, "get_goal")
	assert.NotContains(t, guidance, "update_goal")
}

func TestBeforeModel_ActiveGoalLeavesStreamingUntouched(t *testing.T) {
	ctx, _, sess := newTestInvocation(t, "planner")
	g, err := NewActiveGoal("finish a migration plan")
	require.NoError(t, err)
	require.NoError(t, writeGoalToSession(sess, DefaultStateKey, g))

	streamReq := &model.Request{
		Messages:         []model.Message{model.NewUserMessage("continue")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	}
	e := New()
	res, err := e.beforeModel(ctx, &model.BeforeModelArgs{Request: streamReq})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.True(t, streamReq.GenerationConfig.Stream)

	nonStreamReq := &model.Request{
		Messages:         []model.Message{model.NewUserMessage("continue")},
		GenerationConfig: model.GenerationConfig{Stream: false},
	}
	res, err = e.beforeModel(ctx, &model.BeforeModelArgs{Request: nonStreamReq})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.False(t, nonStreamReq.GenerationConfig.Stream)
}

func TestAfterModel_TerminalGoalPassesThrough(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "planner")
	g, err := NewActiveGoal("finish")
	require.NoError(t, err)
	now := g.UpdatedAtUnix
	g.Status = GoalStatusComplete
	g.TerminalAtUnix = &now
	require.NoError(t, writeGoalToSession(sess, DefaultStateKey, g))

	e := New()
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: finalRsp("done")})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.False(t, reminderPending(inv))
	assert.Equal(t, 0, retryCount(inv))
}

func TestAfterModel_RetryBudgetExhaustionPassesThrough(t *testing.T) {
	ctx, inv, sess := newTestInvocation(t, "planner")
	g, err := NewActiveGoal("finish")
	require.NoError(t, err)
	require.NoError(t, writeGoalToSession(sess, DefaultStateKey, g))
	inv.SetState(stateKeyRetryCount, 1)

	var got EnforceEvent
	e := New(
		WithMaxRetries(1),
		WithOnEnforce(func(evt EnforceEvent) { got = evt }),
	)
	res, err := e.afterModel(ctx, &model.AfterModelArgs{Response: finalRsp("done")})
	require.NoError(t, err)
	assert.Nil(t, res)
	assert.Equal(t, ReasonExhausted, got.Reason)
	assert.Equal(t, 0, retryCount(inv))
}

type sequenceModel struct {
	name      string
	responses []*model.Response
	batches   [][]*model.Response

	mu       sync.Mutex
	requests []*capturedRequest
	nextIdx  int
}

type capturedRequest struct {
	messages []model.Message
	stream   bool
}

func (m *sequenceModel) GenerateContent(
	_ context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if req != nil {
		cp := make([]model.Message, len(req.Messages))
		copy(cp, req.Messages)
		m.requests = append(m.requests, &capturedRequest{
			messages: cp,
			stream:   req.GenerationConfig.Stream,
		})
	}
	if m.nextIdx >= len(m.responses) {
		if len(m.batches) == 0 || m.nextIdx >= len(m.batches) {
			return nil, fmt.Errorf("unexpected model call %d", m.nextIdx)
		}
		batch := m.batches[m.nextIdx]
		m.nextIdx++
		ch := make(chan *model.Response, len(batch))
		for _, rsp := range batch {
			ch <- rsp
		}
		close(ch)
		return ch, nil
	}
	rsp := m.responses[m.nextIdx]
	m.nextIdx++
	ch := make(chan *model.Response, 1)
	ch <- rsp
	close(ch)
	return ch, nil
}

func (m *sequenceModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *sequenceModel) Requests() []*capturedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*capturedRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

func TestRunnerRun_GoalExtensionContinuesWithinSingleCompletion(t *testing.T) {
	ctx := context.Background()
	sessionService := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "goal-extension-test",
		UserID:    "user",
		SessionID: "sid",
	}
	_, err := Start(ctx, sessionService, key, "produce final plan")
	require.NoError(t, err)

	modelStub := &sequenceModel{
		name: "sequence",
		responses: []*model.Response{
			finalRsp("premature done"),
			toolCallRsp(DefaultUpdateGoalToolName, `{"status":"complete"}`),
			finalRsp("final done"),
		},
	}
	ag := llmagent.New(
		"planner",
		llmagent.WithModel(modelStub),
		llmagent.WithExtensions(New(WithMaxRetries(2))),
	)
	r := runner.NewRunner(
		key.AppName,
		ag,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	ch, err := r.Run(ctx, key.UserID, key.SessionID, model.NewUserMessage("continue"), agent.WithStream(true))
	require.NoError(t, err)

	var events []*event.Event
	for ev := range ch {
		events = append(events, ev)
	}
	var completions int
	for _, ev := range events {
		if ev.IsRunnerCompletion() {
			completions++
		}
	}
	assert.Equal(t, 1, completions, "goal extension must not emit multiple runner completions")

	requests := modelStub.Requests()
	require.Len(t, requests, 3)
	assert.True(t, requests[0].stream)
	assert.True(t, requests[1].stream)
	assert.Contains(t, requests[1].messages[len(requests[1].messages)-1].Content, "produce final plan")

	stored, err := sessionService.GetSession(ctx, key)
	require.NoError(t, err)
	got, ok, err := GetGoal(stored)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, GoalStatusComplete, got.Status)
}

func TestRunnerRun_GoalExtensionAllowsStreamingProgress(t *testing.T) {
	ctx := context.Background()
	sessionService := inmemory.NewSessionService()
	key := session.Key{
		AppName:   "goal-extension-test",
		UserID:    "user",
		SessionID: "streaming",
	}
	_, err := Start(ctx, sessionService, key, "produce final plan")
	require.NoError(t, err)

	modelStub := &sequenceModel{
		name: "sequence",
		batches: [][]*model.Response{
			{partialRsp("drafting..."), finalRsp("premature done")},
			{toolCallRsp(DefaultUpdateGoalToolName, `{"status":"complete"}`)},
			{finalRsp("final done")},
		},
	}
	ag := llmagent.New(
		"planner",
		llmagent.WithModel(modelStub),
		llmagent.WithExtensions(New(WithMaxRetries(2))),
	)
	r := runner.NewRunner(
		key.AppName,
		ag,
		runner.WithSessionService(sessionService),
	)
	defer r.Close()

	ch, err := r.Run(ctx, key.UserID, key.SessionID, model.NewUserMessage("continue"), agent.WithStream(true))
	require.NoError(t, err)

	var (
		completions      int
		sawPartial       bool
		sawControl       bool
		sawPrematureText bool
	)
	for ev := range ch {
		if ev.IsRunnerCompletion() {
			completions++
			continue
		}
		if ev == nil || ev.Response == nil {
			continue
		}
		if ev.Response.IsPartial {
			sawPartial = true
		}
		if !ev.Response.Done && !ev.Response.IsPartial && len(ev.Response.Choices) == 0 {
			sawControl = true
		}
		for _, choice := range ev.Response.Choices {
			if choice.Message.Content == "premature done" ||
				choice.Delta.Content == "premature done" {
				sawPrematureText = true
			}
		}
	}
	assert.Equal(t, 1, completions, "goal extension must not emit multiple runner completions")
	assert.True(t, sawPartial, "streaming progress should remain visible")
	assert.True(t, sawControl, "premature final response should be converted to a non-final control event")
	assert.False(t, sawPrematureText, "premature final content should not be emitted as final text")

	requests := modelStub.Requests()
	require.Len(t, requests, 3)
	assert.True(t, requests[0].stream)
	assert.True(t, requests[1].stream)
	assert.Contains(t, requests[1].messages[len(requests[1].messages)-1].Content, "produce final plan")
}
