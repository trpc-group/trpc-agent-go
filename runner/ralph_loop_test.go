//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type scriptedAgent struct {
	name    string
	outputs []string
	calls   int32
}

func (a *scriptedAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *scriptedAgent) SubAgents() []agent.Agent { return nil }

func (a *scriptedAgent) FindSubAgent(string) agent.Agent { return nil }

func (a *scriptedAgent) Tools() []tool.Tool { return nil }

func (a *scriptedAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		idx := int(atomic.AddInt32(&a.calls, 1) - 1)
		content := ""
		if idx >= 0 && idx < len(a.outputs) {
			content = a.outputs[idx]
		}
		evt := event.NewResponseEvent(
			inv.InvocationID,
			a.name,
			&model.Response{
				Done: true,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role:    model.RoleAssistant,
						Content: content,
					},
				}},
			},
		)
		agent.InjectIntoEvent(inv, evt)
		_ = event.EmitEvent(ctx, ch, evt)
	}()
	return ch, nil
}

type scriptedVerifyRunner struct {
	results []RalphLoopCommandResult
	calls   int32
}

func (r *scriptedVerifyRunner) Run(
	_ context.Context,
	_ RalphLoopCommandSpec,
) (RalphLoopCommandResult, error) {
	idx := int(atomic.AddInt32(&r.calls, 1) - 1)
	if idx >= 0 && idx < len(r.results) {
		return r.results[idx], nil
	}
	return RalphLoopCommandResult{}, nil
}

func TestRalphLoop_PromiseStopsLoop(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	base := &scriptedAgent{
		name: "worker",
		outputs: []string{
			"not done",
			"<promise>DONE</promise>",
		},
	}
	r := NewRunner(
		"app",
		base,
		WithSessionService(svc),
		WithRalphLoop(RalphLoopConfig{
			MaxIterations:     5,
			CompletionPromise: "DONE",
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.Message{Role: model.RoleUser, Content: "task"},
	)
	require.NoError(t, err)

	assistant := 0
	completion := 0
	for e := range ch {
		if e == nil || e.Response == nil {
			continue
		}
		if e.IsRunnerCompletion() {
			completion++
			continue
		}
		if len(e.Choices) > 0 &&
			e.Choices[0].Message.Role == model.RoleAssistant {
			assistant++
		}
	}

	require.Equal(t, 2, assistant)
	require.Equal(t, 1, completion)
	require.Equal(t, int32(2), atomic.LoadInt32(&base.calls))

	sess := mustGetSession(t, svc, "app", "u", "s")
	require.Equal(t, 2, countUserMessages(sess))
}

func TestRalphLoop_MaxIterationsEmitsStopError(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	base := &scriptedAgent{
		name:    "worker",
		outputs: []string{"no", "no"},
	}
	r := NewRunner(
		"app",
		base,
		WithSessionService(svc),
		WithRalphLoop(RalphLoopConfig{
			MaxIterations:     2,
			CompletionPromise: "DONE",
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.Message{Role: model.RoleUser, Content: "task"},
	)
	require.NoError(t, err)

	stopErr := 0
	for e := range ch {
		if e == nil || e.Error == nil {
			continue
		}
		if e.Error.Type == agent.ErrorTypeStopAgentError {
			stopErr++
		}
	}
	require.Equal(t, 1, stopErr)
	require.Equal(t, int32(2), atomic.LoadInt32(&base.calls))
}

func TestRalphLoop_VerifyCommandBlocksCompletion(t *testing.T) {
	svc := sessioninmemory.NewSessionService()
	verify := &scriptedVerifyRunner{
		results: []RalphLoopCommandResult{
			{ExitCode: 1, Stderr: "fail"},
			{ExitCode: 0},
		},
	}
	base := &scriptedAgent{
		name: "worker",
		outputs: []string{
			"<promise>DONE</promise>",
			"<promise>DONE</promise>",
		},
	}
	r := NewRunner(
		"app",
		base,
		WithSessionService(svc),
		WithRalphLoop(RalphLoopConfig{
			MaxIterations:     5,
			CompletionPromise: "DONE",
			VerifyCommand:     "go test ./...",
			VerifyRunner:      verify,
		}),
	)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	ch, err := r.Run(
		ctx,
		"u",
		"s",
		model.Message{Role: model.RoleUser, Content: "task"},
	)
	require.NoError(t, err)

	completion := 0
	for e := range ch {
		if e != nil && e.IsRunnerCompletion() {
			completion++
		}
	}

	require.Equal(t, 1, completion)
	require.Equal(t, int32(2), atomic.LoadInt32(&base.calls))
	require.Equal(t, int32(2), atomic.LoadInt32(&verify.calls))

	sess := mustGetSession(t, svc, "app", "u", "s")
	require.Equal(t, 2, countUserMessages(sess))
}

func TestMergeEnv_Overrides(t *testing.T) {
	env := mergeEnv(map[string]string{"TRPC_AGENT_FOO": "bar"})
	found := false
	for _, kv := range env {
		if kv == "TRPC_AGENT_FOO=bar" {
			found = true
			break
		}
	}
	require.True(t, found)
}

func mustGetSession(
	t *testing.T,
	svc session.Service,
	app string,
	user string,
	sid string,
) *session.Session {
	t.Helper()
	sess, err := svc.GetSession(context.Background(), session.Key{
		AppName:   app,
		UserID:    user,
		SessionID: sid,
	})
	require.NoError(t, err)
	require.NotNil(t, sess)
	return sess
}

func countUserMessages(sess *session.Session) int {
	if sess == nil {
		return 0
	}
	cnt := 0
	for _, evt := range sess.Events {
		e := evt
		if e.IsUserMessage() {
			cnt++
		}
	}
	return cnt
}
