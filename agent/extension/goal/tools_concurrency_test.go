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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/flow/processor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// readStubTool is a sibling that raises no objection, so that a turn pairing it
// with a goal tool is a mixed batch the parallel path would otherwise admit.
type readStubTool struct{}

func (readStubTool) Declaration() *tool.Declaration {
	return &tool.Declaration{Name: "read", Description: "read a file"}
}

func (readStubTool) Call(context.Context, []byte) (any, error) {
	return map[string]any{"content": "ok"}, nil
}

// create_goal and update_goal are read-modify-write transitions over the
// session, and a parallel worker reads a snapshot cloned before any sibling
// started: two create_goal calls in one batch would both see "no active goal"
// and both succeed, where the second must fail. The mutating tools object; the
// read-only one does not.
func TestGoalToolsObjectToTheParallelPath(t *testing.T) {
	assert.False(t, tool.IsConcurrencySafe(newGoalTool(toolKindCreate, "create_goal", "goal")),
		"create_goal must not be admitted to the parallel path")
	assert.False(t, tool.IsConcurrencySafe(newGoalTool(toolKindUpdate, "update_goal", "goal")),
		"update_goal must not be admitted to the parallel path")
	assert.True(t, tool.IsConcurrencySafe(newGoalTool(toolKindGet, "get_goal", "goal")),
		"get_goal only reads and raises no objection")
}

// A mixed batch — a safe sibling plus two create_goal calls — with parallel
// tools enabled. The objection keeps the turn sequential, so the second create
// sees the first and fails, preserving the single-active-goal contract.
func TestCreateGoalStaysOffTheParallelPath(t *testing.T) {
	create := newGoalTool(toolKindCreate, "create_goal", "goal")

	// On a parallel worker's view the second call cannot see the first: both
	// read the same pre-batch snapshot, and both succeed.
	_, base, _ := newTestInvocation(t, "agent-A")
	for _, view := range []*agent.Invocation{base.View(), base.View()} {
		view.Session = view.Session.Clone()
		_, err := create.Call(agent.NewInvocationContext(context.Background(), view),
			[]byte(`{"objective":"ship the release"}`))
		require.NoError(t, err,
			"precondition: a worker reading a cloned snapshot sees no active goal")
	}

	ctx, inv, sess := newTestInvocation(t, "agent-A")
	p := processor.NewFunctionCallResponseProcessor(true, nil)
	req := &model.Request{Tools: map[string]tool.Tool{
		"read":        readStubTool{},
		"create_goal": create,
	}}
	rsp := &model.Response{Choices: []model.Choice{{Message: model.Message{
		Role: model.RoleAssistant,
		ToolCalls: []model.ToolCall{
			{ID: "call-read", Function: model.FunctionDefinitionParam{
				Name: "read", Arguments: []byte(`{}`)}},
			{ID: "call-create-1", Function: model.FunctionDefinitionParam{
				Name: "create_goal", Arguments: []byte(`{"objective":"ship the release"}`)}},
			{ID: "call-create-2", Function: model.FunctionDefinitionParam{
				Name: "create_goal", Arguments: []byte(`{"objective":"also ship the docs"}`)}},
		},
	}}}}
	ch := make(chan *event.Event, 16)
	p.ProcessResponse(ctx, inv, req, rsp, ch)
	close(ch)
	results := map[string]string{}
	for evt := range ch {
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			if choice.Message.Role == model.RoleTool {
				results[choice.Message.ToolID] = choice.Message.Content
			}
		}
	}

	g, ok, err := GetGoalWithStateKey(sess, "goal")
	require.NoError(t, err)
	require.True(t, ok, "the first create must reach the base session")
	assert.Equal(t, "ship the release", g.Objective,
		"the second create must not have replaced the first")
	require.Len(t, results, 3, "every call still produces a result")
	assert.NotContains(t, results["call-create-1"], "already exists",
		"the first create succeeds")
	assert.Contains(t, results["call-create-2"], "active goal already exists",
		"the second create must see the first and fail")
}
