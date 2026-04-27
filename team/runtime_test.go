//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package team

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/eventcontrol"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	testTimeout = 5 * time.Second
)

func TestSwarmRuntime_OnTransfer_MaxHandoffs(t *testing.T) {
	cfg := SwarmConfig{
		MaxHandoffs: 2,
		NodeTimeout: testTimeout,
	}
	rt := &swarmRuntime{cfg: cfg}

	_, err := rt.OnTransfer(context.Background(), "a", "b")
	require.NoError(t, err)
	_, err = rt.OnTransfer(context.Background(), "b", "c")
	require.NoError(t, err)
	_, err = rt.OnTransfer(context.Background(), "c", "d")
	require.Error(t, err)
}

func TestSwarmRuntime_OnTransfer_RepetitiveDetection(t *testing.T) {
	cfg := SwarmConfig{
		RepetitiveHandoffWindow:    3,
		RepetitiveHandoffMinUnique: 2,
	}
	rt := &swarmRuntime{cfg: cfg}

	_, err := rt.OnTransfer(context.Background(), "a", "x")
	require.NoError(t, err)
	_, err = rt.OnTransfer(context.Background(), "b", "x")
	require.NoError(t, err)
	_, err = rt.OnTransfer(context.Background(), "c", "x")
	require.ErrorIs(t, err, errRepetitiveHandoff)
}

func TestSwarmRuntime_OnTransfer_ReturnsNodeTimeout(t *testing.T) {
	cfg := SwarmConfig{
		NodeTimeout: testTimeout,
	}
	rt := &swarmRuntime{cfg: cfg}

	got, err := rt.OnTransfer(context.Background(), "a", "b")
	require.NoError(t, err)
	require.Equal(t, testTimeout, got)
}

type composedTransferController struct {
	transfers   int
	customized  int
	completions int
}

func (c *composedTransferController) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	c.transfers++
	return 2 * testTimeout, nil
}

func (c *composedTransferController) CustomizeTransferInvocation(
	_ context.Context,
	_ *agent.Invocation,
	target *agent.Invocation,
) error {
	c.customized++
	target.Message = model.NewUserMessage("existing")
	return nil
}

func (c *composedTransferController) OnTransferComplete(
	_ context.Context,
	_ *agent.Invocation,
	_ *agent.Invocation,
	_ *event.Event,
) {
	c.completions++
}

func TestEnsureSwarmRuntime_PreservesExistingTransferControllerAndComposesCustomizer(t *testing.T) {
	existing := &composedTransferController{}
	inv := agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{
		RuntimeState: map[string]any{
			agent.RuntimeStateKeyTransferController: existing,
		},
	}))
	inputBuilder := func(ctx context.Context, args SwarmHandoffInputArgs) (model.Message, error) {
		_ = ctx
		return model.NewUserMessage(args.TransferMessage + "+swarm"), nil
	}
	ensureSwarmRuntime(
		inv,
		"team",
		"entry",
		SwarmConfig{NodeTimeout: testTimeout},
		swarmHandoffPolicy{},
		inputBuilder,
	)
	controller, ok := agent.GetRuntimeStateValue[agent.TransferController](
		&inv.RunOptions,
		agent.RuntimeStateKeyTransferController,
	)
	require.True(t, ok)
	timeout, err := controller.OnTransfer(context.Background(), "entry", "child")
	require.NoError(t, err)
	require.Equal(t, testTimeout, timeout)
	require.Equal(t, 1, existing.transfers)
	customizer, ok := controller.(transferInvocationCustomizer)
	require.True(t, ok)
	target := agent.NewInvocation(agent.WithInvocationAgent(testAgent{name: "child"}))
	require.NoError(t, customizer.CustomizeTransferInvocation(context.Background(), inv, target))
	require.Equal(t, 1, existing.customized)
	require.Equal(t, "existing+swarm", target.Message.Content)
	handler, ok := controller.(transferCompletionObserver)
	require.True(t, ok)
	handler.OnTransferComplete(context.Background(), inv, target, &event.Event{})
	require.Equal(t, 1, existing.completions)
}

func TestEnsureSwarmRuntime_IsolatesSharedRuntimeState(t *testing.T) {
	sharedState := map[string]any{"tenant": "demo"}
	invA := agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{
		RuntimeState: sharedState,
	}))
	invB := agent.NewInvocation(agent.WithInvocationRunOptions(agent.RunOptions{
		RuntimeState: sharedState,
	}))
	ensureSwarmRuntime(
		invA,
		"team",
		"entry",
		SwarmConfig{MaxHandoffs: 1},
		swarmHandoffPolicy{},
		nil,
	)
	ensureSwarmRuntime(
		invB,
		"team",
		"entry",
		SwarmConfig{MaxHandoffs: 1},
		swarmHandoffPolicy{},
		nil,
	)
	require.NotContains(t, sharedState, agent.RuntimeStateKeyTransferController)
	ctrlA, ok := agent.GetRuntimeStateValue[agent.TransferController](
		&invA.RunOptions,
		agent.RuntimeStateKeyTransferController,
	)
	require.True(t, ok)
	ctrlB, ok := agent.GetRuntimeStateValue[agent.TransferController](
		&invB.RunOptions,
		agent.RuntimeStateKeyTransferController,
	)
	require.True(t, ok)
	_, err := ctrlA.OnTransfer(context.Background(), "entry", "child")
	require.NoError(t, err)
	_, err = ctrlA.OnTransfer(context.Background(), "child", "entry")
	require.Error(t, err)
	_, err = ctrlB.OnTransfer(context.Background(), "entry", "child")
	require.NoError(t, err)
	invA.RunOptions.RuntimeState["branch"] = "a"
	require.NotContains(t, sharedState, "branch")
	require.NotContains(t, invB.RunOptions.RuntimeState, "branch")
}

func TestEnsureSwarmRuntime_DoesNotChainPriorSwarmRuntime(t *testing.T) {
	inv := agent.NewInvocation()
	ensureSwarmRuntime(
		inv,
		"team",
		"entry",
		SwarmConfig{MaxHandoffs: 1},
		swarmHandoffPolicy{},
		nil,
	)
	ctrl, ok := agent.GetRuntimeStateValue[agent.TransferController](
		&inv.RunOptions,
		agent.RuntimeStateKeyTransferController,
	)
	require.True(t, ok)
	_, err := ctrl.OnTransfer(context.Background(), "entry", "child")
	require.NoError(t, err)
	_, err = ctrl.OnTransfer(context.Background(), "child", "entry")
	require.Error(t, err)
	ensureSwarmRuntime(
		inv,
		"team",
		"entry",
		SwarmConfig{MaxHandoffs: 1},
		swarmHandoffPolicy{},
		nil,
	)
	ctrl, ok = agent.GetRuntimeStateValue[agent.TransferController](
		&inv.RunOptions,
		agent.RuntimeStateKeyTransferController,
	)
	require.True(t, ok)
	_, err = ctrl.OnTransfer(context.Background(), "entry", "child")
	require.NoError(t, err)
}

func TestSwarmRuntime_CustomizeTransferInvocation_IsolatesSessionAndBuildsInput(t *testing.T) {
	ctx := context.Background()
	service := sessioninmemory.NewSessionService()
	parentSess, err := service.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "parent",
	}, session.StateMap{})
	require.NoError(t, err)
	source := &agent.Invocation{
		AgentName: "parent",
		Session:   parentSess,
		Message:   model.NewUserMessage("raw user input"),
	}
	target := &agent.Invocation{
		AgentName:      "child",
		InvocationID:   "target-invocation",
		Session:        parentSess,
		SessionService: service,
		Message:        model.NewUserMessage("parent supplied transfer"),
	}
	rt := &swarmRuntime{
		teamName: "support",
		handoff:  swarmHandoffPolicy{sessionScope: swarmSessionScopePerAgent},
		inputBuilder: func(ctx context.Context, args SwarmHandoffInputArgs) (model.Message, error) {
			require.Equal(t, "parent", args.FromAgentName)
			require.Equal(t, "child", args.ToAgentName)
			require.Equal(t, "raw user input", args.RootInput.Content)
			require.Equal(t, "raw user input", args.ParentInput.Content)
			require.Equal(t, "parent supplied transfer", args.TransferMessage)
			require.Equal(t, "parent/support/child", target.Session.ID)
			return model.Message{Content: "rendered child input"}, nil
		},
	}
	require.NoError(t, rt.CustomizeTransferInvocation(ctx, source, target))
	require.Equal(t, "parent/support/child", target.Session.ID)
	require.Equal(t, model.RoleUser, target.Message.Role)
	require.Equal(t, "rendered child input", target.Message.Content)
	got, err := service.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "parent/support/child",
	})
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Empty(t, got.Events)
	parentGot, err := service.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "parent",
	})
	require.NoError(t, err)
	value, ok := parentGot.GetState(swarmMemberSessionKey("support", "child"))
	require.True(t, ok)
	require.Equal(t, []byte("parent/support/child"), value)
}

func TestSwarmRuntime_OnTransferCompleteKeepsRootStateOutOfRoutedChildEvent(t *testing.T) {
	ctx := context.Background()
	service := sessioninmemory.NewSessionService()
	rootSess, err := service.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "root",
	}, session.StateMap{SwarmTeamNameKey: []byte("team")})
	require.NoError(t, err)
	childSess, err := service.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "child",
	}, session.StateMap{})
	require.NoError(t, err)
	source := &agent.Invocation{
		Session:        rootSess,
		SessionService: service,
	}
	target := &agent.Invocation{
		AgentName: "child",
		Session:   childSess,
	}
	targetEvent := &event.Event{
		StateDelta: map[string][]byte{"child_state": []byte("ok")},
	}
	rt := &swarmRuntime{handoff: swarmHandoffPolicy{
		sessionScope: swarmSessionScopePerAgent,
		turnRouting:  swarmTurnRoutingTargetTakesOver,
	}}
	rt.OnTransferComplete(ctx, source, target, targetEvent)
	activeAgent, ok := rootSess.GetState(swarmActiveAgentKey("team"))
	require.True(t, ok)
	require.Equal(t, []byte("child"), activeAgent)
	require.Equal(t, map[string][]byte{"child_state": []byte("ok")}, targetEvent.StateDelta)
	rootGot, err := service.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "root",
	})
	require.NoError(t, err)
	require.Equal(t, []byte("child"), rootGot.State[swarmActiveAgentKey("team")])
}

func TestSwarmRuntime_OnTransferCompletePreservesSharedStateDelta(t *testing.T) {
	ctx := context.Background()
	service := sessioninmemory.NewSessionService()
	rootSess, err := service.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "root",
	}, session.StateMap{SwarmTeamNameKey: []byte("team")})
	require.NoError(t, err)
	source := &agent.Invocation{
		Session:        rootSess,
		SessionService: service,
	}
	target := &agent.Invocation{
		AgentName: "child",
		Session:   rootSess,
	}
	targetEvent := &event.Event{
		StateDelta: map[string][]byte{"child_state": []byte("ok")},
	}
	rt := &swarmRuntime{handoff: swarmHandoffPolicy{
		turnRouting: swarmTurnRoutingTargetTakesOver,
	}}
	rt.OnTransferComplete(ctx, source, target, targetEvent)
	require.Equal(t, []byte("child"), targetEvent.StateDelta[swarmActiveAgentKey("team")])
	require.Equal(t, []byte("ok"), targetEvent.StateDelta["child_state"])
	rootGot, err := service.GetSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "root",
	})
	require.NoError(t, err)
	require.Equal(t, []byte("child"), rootGot.State[swarmActiveAgentKey("team")])
}

func TestSwarmRuntime_PersistIsolatedEventInheritsParentRoute(t *testing.T) {
	ctx := context.Background()
	service := sessioninmemory.NewSessionService()
	root, err := service.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "root",
	}, session.StateMap{})
	require.NoError(t, err)
	child, err := service.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "child",
	}, session.StateMap{})
	require.NoError(t, err)
	rt := &swarmRuntime{handoff: swarmHandoffPolicy{sessionScope: swarmSessionScopePerAgent}}
	rt.registerInvocationSession("child-parent", "team/child", child)
	userEvent := event.NewResponseEvent(
		"child-parent",
		"child",
		&model.Response{Done: true, Choices: []model.Choice{{Message: model.NewUserMessage("child input")}}},
	)
	require.NoError(t, service.AppendEvent(ctx, child, userEvent))
	inv := agent.NewInvocation(
		agent.WithInvocationSession(root),
		agent.WithInvocationSessionService(service),
	)
	childEvent := event.NewResponseEvent(
		"child-descendant",
		"child",
		&model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("child answer")}}},
	)
	childEvent.ParentInvocationID = "child-parent"
	require.True(t, rt.HandleEventPersistence(ctx, inv, childEvent, childEvent))
	require.True(t, eventcontrol.SkipPersistence(inv, childEvent))
	grandchildEvent := event.NewResponseEvent(
		"child-grandchild",
		"child",
		&model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("grandchild answer")}}},
	)
	grandchildEvent.ParentInvocationID = "child-descendant"
	require.True(t, rt.HandleEventPersistence(ctx, inv, grandchildEvent, grandchildEvent))
	require.True(t, eventcontrol.SkipPersistence(inv, grandchildEvent))
	childAfter, err := service.GetSession(ctx, session.Key{AppName: "app", UserID: "user", SessionID: "child"})
	require.NoError(t, err)
	require.True(t, teamSessionContainsContent(childAfter, "child answer"))
	require.True(t, teamSessionContainsContent(childAfter, "grandchild answer"))
	rootAfter, err := service.GetSession(ctx, session.Key{AppName: "app", UserID: "user", SessionID: "root"})
	require.NoError(t, err)
	require.False(t, teamSessionContainsContent(rootAfter, "child answer"))
	require.False(t, teamSessionContainsContent(rootAfter, "grandchild answer"))
}

func TestSwarmRuntime_HandleEventPersistenceRoutesByBranch(t *testing.T) {
	ctx := context.Background()
	service := sessioninmemory.NewSessionService()
	root, err := service.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "root",
	}, session.StateMap{})
	require.NoError(t, err)
	child, err := service.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "child",
	}, session.StateMap{})
	require.NoError(t, err)
	rt := &swarmRuntime{handoff: swarmHandoffPolicy{sessionScope: swarmSessionScopePerAgent}}
	rt.registerInvocationSession("child-parent", "team/child", child)
	userEvent := event.NewResponseEvent(
		"child-parent",
		"child",
		&model.Response{Done: true, Choices: []model.Choice{{Message: model.NewUserMessage("child input")}}},
	)
	require.NoError(t, service.AppendEvent(ctx, child, userEvent))
	inv := agent.NewInvocation(
		agent.WithInvocationSession(root),
		agent.WithInvocationSessionService(service),
	)
	childEvent := event.NewResponseEvent(
		"child-internal-without-parent",
		"child",
		&model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("branch-routed answer")}}},
	)
	childEvent.Branch = "team/child/internal"
	require.True(t, rt.HandleEventPersistence(ctx, inv, childEvent, childEvent))
	require.True(t, eventcontrol.SkipPersistence(inv, childEvent))
	childAfter, err := service.GetSession(ctx, session.Key{AppName: "app", UserID: "user", SessionID: "child"})
	require.NoError(t, err)
	require.True(t, teamSessionContainsContent(childAfter, "branch-routed answer"))
	rootAfter, err := service.GetSession(ctx, session.Key{AppName: "app", UserID: "user", SessionID: "root"})
	require.NoError(t, err)
	require.False(t, teamSessionContainsContent(rootAfter, "branch-routed answer"))
}

func TestDefaultSwarmSessionID(t *testing.T) {
	got := defaultSwarmSessionID(swarmSessionIDArgs{
		ParentSessionID: "parent",
		TeamName:        "team",
		ToAgentName:     "child",
	})
	require.Equal(t, "parent/team/child", got)
	entry := defaultSwarmSessionID(swarmSessionIDArgs{
		ParentSessionID: "parent",
		EntryAgentName:  "main",
		ToAgentName:     "main",
	})
	require.Equal(t, "parent", entry)
}

func TestRootMessageUsesRootMostPayload(t *testing.T) {
	root := agent.NewInvocation(
		agent.WithInvocationID("root"),
		agent.WithInvocationMessage(model.NewUserMessage("root input")),
	)
	parent := root.Clone(
		agent.WithInvocationID("parent"),
		agent.WithInvocationMessage(model.NewUserMessage("parent input")),
	)
	child := parent.Clone(
		agent.WithInvocationID("child"),
		agent.WithInvocationMessage(model.NewUserMessage("child input")),
	)
	require.Equal(t, "root input", rootMessage(child).Content)
}
