//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

type awaitReplyUpdateCall struct {
	key   session.Key
	state session.StateMap
}

type awaitReplySessionService struct {
	*mockSessionService
	updateCalls []awaitReplyUpdateCall
	updateErr   error
}

func (s *awaitReplySessionService) UpdateSessionState(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
) error {
	s.updateCalls = append(
		s.updateCalls,
		awaitReplyUpdateCall{key: key, state: state},
	)
	return s.updateErr
}

type awaitReplyTrackingAgent struct {
	name      string
	subAgents []agent.Agent
	markAwait bool
	calls     int
}

func (a *awaitReplyTrackingAgent) Info() agent.Info {
	return agent.Info{Name: a.name}
}

func (a *awaitReplyTrackingAgent) SubAgents() []agent.Agent {
	return a.subAgents
}

func (a *awaitReplyTrackingAgent) FindSubAgent(name string) agent.Agent {
	for _, sub := range a.subAgents {
		if sub != nil && sub.Info().Name == name {
			return sub
		}
	}
	return nil
}

func (a *awaitReplyTrackingAgent) Tools() []tool.Tool {
	return nil
}

func (a *awaitReplyTrackingAgent) Run(
	ctx context.Context,
	inv *agent.Invocation,
) (<-chan *event.Event, error) {
	a.calls++
	ch := make(chan *event.Event, 1)
	go func() {
		defer close(ch)
		if a.markAwait {
			_ = agent.MarkAwaitingUserReply(inv)
		}
		_ = agent.EmitEvent(
			ctx,
			inv,
			ch,
			event.NewResponseEvent(
				inv.InvocationID,
				a.name,
				&model.Response{
					Done: true,
					Choices: []model.Choice{{
						Index: 0,
						Message: model.Message{
							Role:    model.RoleAssistant,
							Content: a.name,
						},
					}},
				},
			),
		)
	}()
	return ch, nil
}

func TestRunner_Run_AwaitUserReplyRoutingConsumesRoute(t *testing.T) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	state, err := (agent.AwaitUserReplyRoute{
		AgentName: "child",
	}).State()
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, state)
	require.NoError(t, err)

	parent := &awaitReplyTrackingAgent{name: "parent"}
	child := &awaitReplyTrackingAgent{name: "child"}
	r := NewRunner(
		"app",
		parent,
		WithSessionService(svc),
		WithAgent("child", child),
		WithAwaitUserReplyRouting(true),
	)

	eventCh, err := r.Run(
		ctx,
		key.UserID,
		key.SessionID,
		model.NewUserMessage("follow up"),
		agent.WithRequestID("req-await-consume"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.Equal(t, 0, parent.calls)
	require.Equal(t, 1, child.calls)

	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	_, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRunner_Run_AwaitUserReplyRoutingDisabledByDefault(t *testing.T) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-disabled",
	}
	state, err := (agent.AwaitUserReplyRoute{
		AgentName: "child",
	}).State()
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, state)
	require.NoError(t, err)

	parent := &awaitReplyTrackingAgent{name: "parent"}
	child := &awaitReplyTrackingAgent{name: "child"}
	r := NewRunner(
		"app",
		parent,
		WithSessionService(svc),
		WithAgent("child", child),
	)

	eventCh, err := r.Run(
		ctx,
		key.UserID,
		key.SessionID,
		model.NewUserMessage("follow up"),
		agent.WithRequestID("req-await-disabled"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.Equal(t, 1, parent.calls)
	require.Equal(t, 0, child.calls)

	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	route, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "child", route.AgentName)
}

func TestRunner_Run_AwaitUserReplyRoutingExplicitAgentWins(t *testing.T) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-explicit",
	}
	state, err := (agent.AwaitUserReplyRoute{
		AgentName: "child",
	}).State()
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, state)
	require.NoError(t, err)

	parent := &awaitReplyTrackingAgent{name: "parent"}
	child := &awaitReplyTrackingAgent{name: "child"}
	r := NewRunner(
		"app",
		parent,
		WithSessionService(svc),
		WithAgent("child", child),
		WithAwaitUserReplyRouting(true),
	)

	eventCh, err := r.Run(
		ctx,
		key.UserID,
		key.SessionID,
		model.NewUserMessage("follow up"),
		agent.WithRequestID("req-await-explicit"),
		agent.WithAgentByName("parent"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.Equal(t, 1, parent.calls)
	require.Equal(t, 0, child.calls)

	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	_, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRunner_Run_AwaitUserReplyRoutingFallsBackWhenMissing(t *testing.T) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-missing",
	}
	state, err := (agent.AwaitUserReplyRoute{
		AgentName: "missing",
	}).State()
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, state)
	require.NoError(t, err)

	parent := &awaitReplyTrackingAgent{name: "parent"}
	r := NewRunner(
		"app",
		parent,
		WithSessionService(svc),
		WithAwaitUserReplyRouting(true),
	)

	eventCh, err := r.Run(
		ctx,
		key.UserID,
		key.SessionID,
		model.NewUserMessage("follow up"),
		agent.WithRequestID("req-await-missing"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.Equal(t, 1, parent.calls)

	sess, err := svc.GetSession(ctx, key)
	require.NoError(t, err)
	_, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestRunner_Run_AwaitUserReplyRoutingResolvesNestedPath(t *testing.T) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-nested",
	}
	state, err := (agent.AwaitUserReplyRoute{
		AgentName:  "child",
		LookupPath: "parent/child",
	}).State()
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, state)
	require.NoError(t, err)

	nestedChild := &awaitReplyTrackingAgent{name: "child"}
	parent := &awaitReplyTrackingAgent{
		name:      "parent",
		subAgents: []agent.Agent{nestedChild},
	}
	topLevelChild := &awaitReplyTrackingAgent{name: "child"}
	r := NewRunner(
		"app",
		parent,
		WithSessionService(svc),
		WithAgent("child", topLevelChild),
		WithAwaitUserReplyRouting(true),
	)

	eventCh, err := r.Run(
		ctx,
		key.UserID,
		key.SessionID,
		model.NewUserMessage("follow up"),
		agent.WithRequestID("req-await-nested"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.Equal(t, 0, parent.calls)
	require.Equal(t, 1, nestedChild.calls)
	require.Equal(t, 0, topLevelChild.calls)
}

func TestRunner_Run_AwaitUserReplyRoutingPersistsFactoryLookupPath(
	t *testing.T,
) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	r := NewRunnerWithAgentFactory(
		"app",
		"coordinator",
		func(
			_ context.Context,
			_ agent.RunOptions,
		) (agent.Agent, error) {
			return &awaitReplyTrackingAgent{
				name:      "runtime-root",
				markAwait: true,
			}, nil
		},
		WithSessionService(svc),
		WithAwaitUserReplyRouting(true),
	)

	eventCh, err := r.Run(
		ctx,
		"user",
		"sess-factory-root",
		model.NewUserMessage("first turn"),
		agent.WithRequestID("req-await-factory-root"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	sess, err := svc.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-factory-root",
	})
	require.NoError(t, err)
	route, ok, err := agent.PendingAwaitUserReplyRoute(sess)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "runtime-root", route.AgentName)
	require.Equal(t, "coordinator", route.LookupPath)
}

func TestRunner_Run_AwaitUserReplyRoutingResolvesFactorySubAgentPath(
	t *testing.T,
) {
	ctx := context.Background()
	svc := sessioninmemory.NewSessionService()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess-factory-nested",
	}
	state, err := (agent.AwaitUserReplyRoute{
		AgentName:  "child",
		LookupPath: "coordinator/child",
	}).State()
	require.NoError(t, err)
	_, err = svc.CreateSession(ctx, key, state)
	require.NoError(t, err)

	child := &awaitReplyTrackingAgent{name: "child"}
	factoryCalls := 0
	r := NewRunnerWithAgentFactory(
		"app",
		"coordinator",
		func(
			_ context.Context,
			_ agent.RunOptions,
		) (agent.Agent, error) {
			factoryCalls++
			return &awaitReplyTrackingAgent{
				name:      "runtime-root",
				subAgents: []agent.Agent{child},
			}, nil
		},
		WithSessionService(svc),
		WithAwaitUserReplyRouting(true),
	)

	eventCh, err := r.Run(
		ctx,
		key.UserID,
		key.SessionID,
		model.NewUserMessage("follow up"),
		agent.WithRequestID("req-await-factory-nested"),
	)
	require.NoError(t, err)
	for range eventCh {
	}

	require.Equal(t, 1, factoryCalls)
	require.Equal(t, 1, child.calls)
}

func TestRunner_ResolveAwaitUserReplyRoute_ReturnsRawAgent(t *testing.T) {
	child := &awaitReplyTrackingAgent{name: "child"}
	parent := &awaitReplyTrackingAgent{
		name:      "parent",
		subAgents: []agent.Agent{child},
	}
	r := &runner{
		agents:         map[string]agent.Agent{"parent": parent},
		agentFactories: map[string]AgentFactory{},
	}

	got, rootName, ok, err := r.resolveAwaitUserReplyRoute(
		context.Background(),
		agent.AwaitUserReplyRoute{
			AgentName:  "child",
			LookupPath: "parent/child",
		},
		agent.RunOptions{},
	)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "parent", rootName)
	require.Same(t, child, got)
}

func TestRunner_ApplyAwaitUserReplyRoute_EdgeCases(t *testing.T) {
	ctx := context.Background()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	userMessage := model.NewUserMessage("follow up")

	t.Run("disabled", func(t *testing.T) {
		r := &runner{}

		got, rootName, err := r.applyAwaitUserReplyRoute(
			ctx,
			key,
			nil,
			userMessage,
			agent.RunOptions{},
		)
		require.NoError(t, err)
		require.Empty(t, rootName)
		require.Nil(t, got.Agent)
		require.Empty(t, got.AgentByName)
	})

	t.Run("non user message", func(t *testing.T) {
		r := &runner{awaitUserReplyRouting: true}

		got, rootName, err := r.applyAwaitUserReplyRoute(
			ctx,
			key,
			nil,
			model.Message{Role: model.RoleAssistant},
			agent.RunOptions{},
		)
		require.NoError(t, err)
		require.Empty(t, rootName)
		require.Nil(t, got.Agent)
	})

	t.Run("no pending route", func(t *testing.T) {
		r := &runner{awaitUserReplyRouting: true}
		sess := session.NewSession("app", "user", "sess")

		got, rootName, err := r.applyAwaitUserReplyRoute(
			ctx,
			key,
			sess,
			userMessage,
			agent.RunOptions{},
		)
		require.NoError(t, err)
		require.Empty(t, rootName)
		require.Nil(t, got.Agent)
	})

	t.Run("invalid route clear error", func(t *testing.T) {
		svc := &awaitReplySessionService{
			mockSessionService: &mockSessionService{},
			updateErr:          errors.New("clear failed"),
		}
		r := &runner{
			awaitUserReplyRouting: true,
			sessionService:        svc,
		}
		sess := session.NewSession(
			"app",
			"user",
			"sess",
			session.WithSessionState(session.StateMap{
				"__trpc_agent_await_user_reply_route__": []byte("{"),
			}),
		)

		_, _, err := r.applyAwaitUserReplyRoute(
			ctx,
			key,
			sess,
			userMessage,
			agent.RunOptions{},
		)
		require.ErrorContains(
			t,
			err,
			"clear invalid await_user_reply route",
		)
	})

	t.Run("invalid route clears state", func(t *testing.T) {
		svc := &awaitReplySessionService{
			mockSessionService: &mockSessionService{},
		}
		r := &runner{
			awaitUserReplyRouting: true,
			sessionService:        svc,
		}
		sess := session.NewSession(
			"app",
			"user",
			"sess",
			session.WithSessionState(session.StateMap{
				"__trpc_agent_await_user_reply_route__": []byte("{"),
			}),
		)

		got, rootName, err := r.applyAwaitUserReplyRoute(
			ctx,
			key,
			sess,
			userMessage,
			agent.RunOptions{},
		)
		require.NoError(t, err)
		require.Empty(t, rootName)
		require.Nil(t, got.Agent)
		require.Len(t, svc.updateCalls, 1)
	})

	t.Run("stale route clear error", func(t *testing.T) {
		state, err := (agent.AwaitUserReplyRoute{
			AgentName: "missing",
		}).State()
		require.NoError(t, err)

		svc := &awaitReplySessionService{
			mockSessionService: &mockSessionService{},
			updateErr:          errors.New("clear failed"),
		}
		r := &runner{
			awaitUserReplyRouting: true,
			sessionService:        svc,
		}
		sess := session.NewSession(
			"app",
			"user",
			"sess",
			session.WithSessionState(state),
		)

		_, _, err = r.applyAwaitUserReplyRoute(
			ctx,
			key,
			sess,
			userMessage,
			agent.RunOptions{},
		)
		require.ErrorContains(
			t,
			err,
			"clear stale await_user_reply route",
		)
	})

	t.Run("resolve error", func(t *testing.T) {
		state, err := (agent.AwaitUserReplyRoute{
			AgentName: "child",
		}).State()
		require.NoError(t, err)

		r := &runner{
			awaitUserReplyRouting: true,
			agentFactories: map[string]AgentFactory{
				"child": func(
					context.Context,
					agent.RunOptions,
				) (agent.Agent, error) {
					return nil, errors.New("factory failed")
				},
			},
		}
		sess := session.NewSession(
			"app",
			"user",
			"sess",
			session.WithSessionState(state),
		)

		_, _, err = r.applyAwaitUserReplyRoute(
			ctx,
			key,
			sess,
			userMessage,
			agent.RunOptions{},
		)
		require.ErrorContains(t, err, "factory failed")
	})
}

func TestRunner_ClearOverriddenAwaitUserReplyRoute_EdgeCases(t *testing.T) {
	ctx := context.Background()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}
	ro := agent.RunOptions{AgentByName: "parent"}

	t.Run("no pending route", func(t *testing.T) {
		svc := &awaitReplySessionService{
			mockSessionService: &mockSessionService{},
		}
		r := &runner{sessionService: svc}
		sess := session.NewSession("app", "user", "sess")

		got, rootName, err := r.clearOverriddenAwaitUserReplyRoute(
			ctx,
			key,
			sess,
			ro,
		)
		require.NoError(t, err)
		require.Empty(t, rootName)
		require.Equal(t, ro.AgentByName, got.AgentByName)
		require.Empty(t, svc.updateCalls)
	})

	t.Run("invalid route clears state", func(t *testing.T) {
		svc := &awaitReplySessionService{
			mockSessionService: &mockSessionService{},
		}
		r := &runner{sessionService: svc}
		sess := session.NewSession(
			"app",
			"user",
			"sess",
			session.WithSessionState(session.StateMap{
				"__trpc_agent_await_user_reply_route__": []byte("{"),
			}),
		)

		_, _, err := r.clearOverriddenAwaitUserReplyRoute(
			ctx,
			key,
			sess,
			ro,
		)
		require.NoError(t, err)
		require.Len(t, svc.updateCalls, 1)

		_, ok, routeErr := agent.PendingAwaitUserReplyRoute(sess)
		require.NoError(t, routeErr)
		require.False(t, ok)
	})

	t.Run("clear error", func(t *testing.T) {
		state, err := (agent.AwaitUserReplyRoute{
			AgentName: "child",
		}).State()
		require.NoError(t, err)

		svc := &awaitReplySessionService{
			mockSessionService: &mockSessionService{},
			updateErr:          errors.New("clear failed"),
		}
		r := &runner{sessionService: svc}
		sess := session.NewSession(
			"app",
			"user",
			"sess",
			session.WithSessionState(state),
		)

		_, _, err = r.clearOverriddenAwaitUserReplyRoute(
			ctx,
			key,
			sess,
			ro,
		)
		require.ErrorContains(
			t,
			err,
			"clear overridden await_user_reply route",
		)
	})
}

func TestRunner_ClearAwaitUserReplyRoute_EdgeCases(t *testing.T) {
	ctx := context.Background()
	key := session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "sess",
	}

	t.Run("nil runner", func(t *testing.T) {
		var r *runner
		require.NoError(t, r.clearAwaitUserReplyRoute(ctx, key, nil))
	})

	t.Run("nil session service", func(t *testing.T) {
		r := &runner{}
		require.NoError(t, r.clearAwaitUserReplyRoute(ctx, key, nil))
	})

	t.Run("nil session", func(t *testing.T) {
		svc := &awaitReplySessionService{
			mockSessionService: &mockSessionService{},
		}
		r := &runner{sessionService: svc}

		err := r.clearAwaitUserReplyRoute(ctx, key, nil)
		require.NoError(t, err)
		require.Len(t, svc.updateCalls, 1)
		require.Equal(t, key, svc.updateCalls[0].key)
	})

	t.Run("updates in memory session", func(t *testing.T) {
		state, err := (agent.AwaitUserReplyRoute{
			AgentName: "child",
		}).State()
		require.NoError(t, err)

		svc := &awaitReplySessionService{
			mockSessionService: &mockSessionService{},
		}
		r := &runner{sessionService: svc}
		sess := session.NewSession(
			"app",
			"user",
			"sess",
			session.WithSessionState(state),
		)

		err = r.clearAwaitUserReplyRoute(ctx, key, sess)
		require.NoError(t, err)
		require.Len(t, svc.updateCalls, 1)

		_, ok, routeErr := agent.PendingAwaitUserReplyRoute(sess)
		require.NoError(t, routeErr)
		require.False(t, ok)
	})
}

func TestRunner_ResolveAwaitUserReplyRoute_EdgeCases(t *testing.T) {
	ctx := context.Background()
	ro := agent.RunOptions{}

	t.Run("nil runner", func(t *testing.T) {
		var r *runner

		got, rootName, ok, err := r.resolveAwaitUserReplyRoute(
			ctx,
			agent.AwaitUserReplyRoute{LookupPath: "root"},
			ro,
		)
		require.NoError(t, err)
		require.False(t, ok)
		require.Empty(t, rootName)
		require.Nil(t, got)
	})

	t.Run("empty path", func(t *testing.T) {
		r := &runner{}

		got, rootName, ok, err := r.resolveAwaitUserReplyRoute(
			ctx,
			agent.AwaitUserReplyRoute{},
			ro,
		)
		require.NoError(t, err)
		require.False(t, ok)
		require.Empty(t, rootName)
		require.Nil(t, got)
	})

	t.Run("missing root", func(t *testing.T) {
		r := &runner{
			agents:         map[string]agent.Agent{},
			agentFactories: map[string]AgentFactory{},
		}

		got, rootName, ok, err := r.resolveAwaitUserReplyRoute(
			ctx,
			agent.AwaitUserReplyRoute{LookupPath: "missing"},
			ro,
		)
		require.NoError(t, err)
		require.False(t, ok)
		require.Empty(t, rootName)
		require.Nil(t, got)
	})

	t.Run("missing nested sub agent", func(t *testing.T) {
		parent := &awaitReplyTrackingAgent{name: "parent"}
		r := &runner{
			agents: map[string]agent.Agent{"parent": parent},
		}

		got, rootName, ok, err := r.resolveAwaitUserReplyRoute(
			ctx,
			agent.AwaitUserReplyRoute{
				LookupPath: "parent/child",
			},
			ro,
		)
		require.NoError(t, err)
		require.False(t, ok)
		require.Empty(t, rootName)
		require.Nil(t, got)
	})

	t.Run("factory error", func(t *testing.T) {
		r := &runner{
			agentFactories: map[string]AgentFactory{
				"factory": func(
					context.Context,
					agent.RunOptions,
				) (agent.Agent, error) {
					return nil, errors.New("factory failed")
				},
			},
		}

		got, rootName, ok, err := r.resolveAwaitUserReplyRoute(
			ctx,
			agent.AwaitUserReplyRoute{LookupPath: "factory"},
			ro,
		)
		require.ErrorContains(t, err, "factory failed")
		require.False(t, ok)
		require.Empty(t, rootName)
		require.Nil(t, got)
	})

	t.Run("factory returned nil", func(t *testing.T) {
		r := &runner{
			agentFactories: map[string]AgentFactory{
				"factory": func(
					context.Context,
					agent.RunOptions,
				) (agent.Agent, error) {
					return nil, nil
				},
			},
		}

		got, rootName, ok, err := r.resolveAwaitUserReplyRoute(
			ctx,
			agent.AwaitUserReplyRoute{LookupPath: "factory"},
			ro,
		)
		require.ErrorContains(t, err, "returned nil")
		require.False(t, ok)
		require.Empty(t, rootName)
		require.Nil(t, got)
	})
}

func TestSplitAgentPath_TrimsEmptySegments(t *testing.T) {
	got := splitAgentPath(" / root // child / ")
	require.Equal(t, []string{"root", "child"}, got)
}
