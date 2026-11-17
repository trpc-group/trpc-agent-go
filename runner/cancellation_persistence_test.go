//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/finalization"
	"trpc.group/trpc-go/trpc-agent-go/internal/tooljournal"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestCancellationPersistence_StaleStateIsAuditOnly(t *testing.T) {
	svc := inmemory.NewSessionService()
	defer svc.Close()
	r := &runner{sessionService: svc, toolExecutionTracking: true}
	key := session.Key{AppName: "app", UserID: "user", SessionID: "session"}
	oldSess, oldGeneration, err := r.restoreWithGeneration(context.Background(), agent.RunOptions{}, key)
	require.NoError(t, err)
	require.NoError(t, svc.AppendEvent(context.Background(), oldSess, event.NewResponseEvent("old", "user", &model.Response{Choices: []model.Choice{{Message: model.NewUserMessage("run")}}})))
	inv := agent.NewInvocation(agent.WithInvocationSession(oldSess))
	r.attachSessionAppender(inv, oldSess, oldGeneration)
	newSess, newGeneration, err := r.restoreWithGeneration(context.Background(), agent.RunOptions{}, key)
	require.NoError(t, err)
	newer := event.New("new", "agent")
	newer.StateDelta = map[string][]byte{"value": []byte(`"new"`)}
	require.NoError(t, r.appendWithGeneration(context.Background(), newSess, newer, newGeneration))
	late := event.NewResponseEvent("old", "agent", &model.Response{Choices: []model.Choice{{Message: model.Message{Role: model.RoleTool, ToolID: "call", Content: "real late result"}}}})
	late.StateDelta = map[string][]byte{"value": []byte(`"old"`), tooljournal.Prefix + "round": []byte(`{"version":1}`)}
	ok, err := finalization.Append(context.Background(), inv, late)
	require.True(t, ok)
	require.NoError(t, err)
	// A competing normal/fallback submission does not duplicate the tool event.
	_, err = finalization.Append(context.Background(), inv, late)
	require.NoError(t, err)
	restored, err := svc.GetSession(context.Background(), key)
	require.NoError(t, err)
	value, _ := restored.GetState("value")
	require.JSONEq(t, `"new"`, string(value))
	require.True(t, restored.HasStateKeyWithPrefix(tooljournal.Prefix))
	require.Len(t, restored.Events, 2)
	require.Equal(t, late.ID, restored.Events[1].ID)
	var audit map[string][]byte
	require.NoError(t, json.Unmarshal(restored.Events[1].Extensions["trpc_agent.stale_state_delta.v1"], &audit))
	require.Equal(t, []byte(`"old"`), audit["value"])
	require.Equal(t, []byte(`"old"`), late.StateDelta["value"], "caller-owned event is not mutated")
}

type cancellationResultFailure struct{ session.Service }

func (s *cancellationResultFailure) AppendEvent(ctx context.Context, sess *session.Session, e *event.Event, opts ...session.Option) error {
	if e.IsToolResultResponse() {
		return errors.New("tool result append uncertain")
	}
	return s.Service.AppendEvent(ctx, sess, e, opts...)
}

func TestCancellationPersistence_FailedAckStopsModelContinuation(t *testing.T) {
	svc := &cancellationResultFailure{Service: inmemory.NewSessionService()}
	defer svc.Close()
	m := executionTestModel("a")
	tl := function.NewFunctionTool(func(context.Context, struct{}) (string, error) { return "written", nil }, function.WithName("a"))
	r := NewRunner("execution-test", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithTools([]tool.Tool{tl})), WithSessionService(svc), WithToolExecutionTracking(true))
	defer r.Close()
	ch, err := r.Run(context.Background(), "u", "s", model.NewUserMessage("run"))
	require.NoError(t, err)
	for range ch {
	}
	require.Len(t, m.Requests(), 1, "consuming the event is not a successful append acknowledgement")
	records := executionRecords(t, svc)
	require.Equal(t, "completed", records[0].Calls[0].Status, "real outcome is still retained independently")
}
