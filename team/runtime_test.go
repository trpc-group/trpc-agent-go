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
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/state/sessionroute"
	itransfer "trpc.group/trpc-go/trpc-agent-go/internal/transfer"
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
	transfers  int
	customized int
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
	customizer, ok := controller.(itransfer.InvocationCustomizer)
	require.True(t, ok)
	target := agent.NewInvocation(agent.WithInvocationAgent(testAgent{name: "child"}))
	require.NoError(t, customizer.CustomizeTransferInvocation(context.Background(), inv, target))
	require.Equal(t, 1, existing.customized)
	require.Equal(t, "existing+swarm", target.Message.Content)
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

func TestSwarmRuntime_CustomizeTransferInvocation_BuildsInput(t *testing.T) {
	source := agent.NewInvocation(
		agent.WithInvocationAgent(testAgent{name: "parent"}),
		agent.WithInvocationMessage(model.NewUserMessage("raw user input")),
	)
	target := agent.NewInvocation(
		agent.WithInvocationAgent(testAgent{name: "child"}),
		agent.WithInvocationID("target-invocation"),
		agent.WithInvocationMessage(model.NewUserMessage("parent supplied transfer")),
	)
	rt := &swarmRuntime{
		inputBuilder: func(ctx context.Context, args SwarmHandoffInputArgs) (model.Message, error) {
			require.Equal(t, "parent", args.FromAgentName)
			require.Equal(t, "child", args.ToAgentName)
			require.Equal(t, "raw user input", args.RootInput.Content)
			require.Equal(t, "raw user input", args.ParentInput.Content)
			require.Equal(t, "parent supplied transfer", args.TransferMessage)
			_ = ctx
			return model.Message{Content: "rendered child input"}, nil
		},
	}
	require.NoError(t, rt.CustomizeTransferInvocation(context.Background(), source, target))
	require.Equal(t, model.RoleUser, target.Message.Role)
	require.Equal(t, "rendered child input", target.Message.Content)
}

func TestSwarmRuntime_CustomizeTransferInvocation_UsesRawTransferMessage(t *testing.T) {
	source := agent.NewInvocation(
		agent.WithInvocationAgent(testAgent{name: "parent"}),
		agent.WithInvocationMessage(model.NewUserMessage("original user input")),
	)
	target := agent.NewInvocation(
		agent.WithInvocationAgent(testAgent{name: "child"}),
		agent.WithInvocationMessage(model.NewUserMessage("original user input")),
	)
	rt := &swarmRuntime{
		inputBuilder: func(ctx context.Context, args SwarmHandoffInputArgs) (model.Message, error) {
			_ = ctx
			require.Empty(t, args.TransferMessage)
			return model.NewUserMessage("custom child input"), nil
		},
	}
	ctx := itransfer.ContextWithTransferMessage(context.Background(), "")
	require.NoError(t, rt.CustomizeTransferInvocation(ctx, source, target))
	require.Equal(t, "custom child input", target.Message.Content)
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
			_ = ctx
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
	owner := &testSwarmMember{
		name:      "team",
		subAgents: []agent.Agent{&testSwarmMember{name: "child"}},
	}
	turnSession, err := sessionroute.ResolveCurrentTurnSession(ctx, service, rootGot, owner)
	require.NoError(t, err)
	require.Equal(t, "child", turnSession.ID)
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
	_, ok := rootGot.State[swarmActiveAgentKey("team")]
	require.False(t, ok)
}

func TestSwarmRuntime_RouteIsolatedEventInheritsParentRoute(t *testing.T) {
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
	got, ok := rt.RouteEvent(inv, childEvent)
	require.True(t, ok)
	require.Same(t, child, got)
	grandchildEvent := event.NewResponseEvent(
		"child-grandchild",
		"child",
		&model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("grandchild answer")}}},
	)
	grandchildEvent.ParentInvocationID = "child-descendant"
	grandchildEvent.Branch = "team/child/internal"
	got, ok = rt.RouteEvent(inv, grandchildEvent)
	require.True(t, ok)
	require.Same(t, child, got)
}

func TestSwarmRuntime_RouteIsolatedEventUsesBranchFallback(t *testing.T) {
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
	got, ok := rt.RouteEvent(inv, childEvent)
	require.True(t, ok)
	require.Same(t, child, got)
	rt.mu.Lock()
	_, cached := rt.sessions["child-internal-without-parent"]
	rt.mu.Unlock()
	require.False(t, cached)
}

func TestDefaultSwarmSessionID(t *testing.T) {
	got := defaultSwarmSessionID(swarmSessionIDArgs{
		ParentSessionID: "parent",
		TeamName:        "team",
		ToAgentName:     "child",
	})
	require.Equal(t, "parent/team/child", got)
	escaped := defaultSwarmSessionID(swarmSessionIDArgs{
		ParentSessionID: "parent/id",
		TeamName:        "team/name",
		ToAgentName:     "child/name",
	})
	require.Equal(t, "parent%2Fid/team%2Fname/child%2Fname", escaped)
	left := defaultSwarmSessionID(swarmSessionIDArgs{
		ParentSessionID: "p/a",
		TeamName:        "b",
		ToAgentName:     "c",
	})
	right := defaultSwarmSessionID(swarmSessionIDArgs{
		ParentSessionID: "p",
		TeamName:        "a/b",
		ToAgentName:     "c",
	})
	require.NotEqual(t, left, right)
	entry := defaultSwarmSessionID(swarmSessionIDArgs{
		ParentSessionID: "parent",
		EntryAgentName:  "main",
		ToAgentName:     "main",
	})
	require.Equal(t, "parent", entry)
}

type createRaceSessionService struct {
	session.Service
}

func (s createRaceSessionService) CreateSession(
	ctx context.Context,
	key session.Key,
	state session.StateMap,
	options ...session.Option,
) (*session.Session, error) {
	_, err := s.Service.CreateSession(ctx, key, state, options...)
	if err != nil {
		return nil, err
	}
	return nil, errors.New("session already exists and has not expired")
}

func TestSwarmRuntime_GetOrCreateSessionFallsBackAfterConcurrentCreate(t *testing.T) {
	ctx := context.Background()
	base := sessioninmemory.NewSessionService()
	root, err := base.CreateSession(ctx, session.Key{
		AppName:   "app",
		UserID:    "user",
		SessionID: "root",
	}, session.StateMap{})
	require.NoError(t, err)
	got, err := (&swarmRuntime{}).getOrCreateSession(
		ctx,
		createRaceSessionService{Service: base},
		root,
		"root/team/child",
	)
	require.NoError(t, err)
	require.Equal(t, "root/team/child", got.ID)
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

func TestSwarmRuntime_CustomizeTransferInvocation_HandlesNilAndBuilderErrors(t *testing.T) {
	called := false
	rt := &swarmRuntime{
		inputBuilder: func(context.Context, SwarmHandoffInputArgs) (model.Message, error) {
			called = true
			return model.NewUserMessage("unused"), nil
		},
	}
	require.NoError(t, rt.CustomizeTransferInvocation(context.Background(), nil, nil))
	require.False(t, called)
	target := agent.NewInvocation(agent.WithInvocationMessage(model.NewUserMessage("original")))
	require.NoError(t, (&swarmRuntime{}).CustomizeTransferInvocation(context.Background(), nil, target))
	require.Equal(t, "original", target.Message.Content)
	buildErr := errors.New("build failed")
	rt = &swarmRuntime{
		inputBuilder: func(ctx context.Context, args SwarmHandoffInputArgs) (model.Message, error) {
			_ = ctx
			require.Empty(t, args.FromAgentName)
			require.Empty(t, args.RootInput.Content)
			require.Empty(t, args.ParentInput.Content)
			return model.Message{}, buildErr
		},
	}
	require.ErrorIs(t, rt.CustomizeTransferInvocation(context.Background(), nil, target), buildErr)
}

func TestRuntimeControllerHelpers_HandleNilAndStripSwarmControllers(t *testing.T) {
	require.NotPanics(t, func() {
		ensureSwarmRuntime(nil, "", "", SwarmConfig{}, swarmHandoffPolicy{}, nil)
	})
	installSwarmTransferController(nil, &swarmRuntime{})
	opts := &agent.RunOptions{}
	installSwarmTransferController(opts, nil)
	_, ok := agent.GetRuntimeStateValue[agent.TransferController](
		opts,
		agent.RuntimeStateKeyTransferController,
	)
	require.False(t, ok)
	existing := &runtimeTestController{timeout: testTimeout}
	require.Same(t, existing, composeTransferControllers(existing, nil))
	require.Nil(t, stripSwarmTransferControllers(nil))
	require.Nil(t, stripSwarmTransferControllers(&swarmRuntime{}))
	require.Same(t, existing, stripSwarmTransferControllers(existing))
	chained := chainedTransferController{
		first:  &swarmRuntime{},
		second: existing,
	}
	require.Same(t, existing, stripSwarmTransferControllers(chained))
}

func TestChainedTransferController_OnTransfer_PropagatesErrorsAndChoosesTimeout(t *testing.T) {
	firstErr := errors.New("first transfer failed")
	second := &runtimeTestController{timeout: testTimeout}
	_, err := (chainedTransferController{
		first:  &runtimeTestController{transferErr: firstErr},
		second: second,
	}).OnTransfer(context.Background(), "a", "b")
	require.ErrorIs(t, err, firstErr)
	require.Zero(t, second.transfers)
	secondErr := errors.New("second transfer failed")
	_, err = (chainedTransferController{
		first:  &runtimeTestController{timeout: 2 * testTimeout},
		second: &runtimeTestController{transferErr: secondErr},
	}).OnTransfer(context.Background(), "a", "b")
	require.ErrorIs(t, err, secondErr)
	timeout, err := (chainedTransferController{
		first:  &runtimeTestController{timeout: 2 * testTimeout},
		second: &runtimeTestController{timeout: testTimeout},
	}).OnTransfer(context.Background(), "a", "b")
	require.NoError(t, err)
	require.Equal(t, testTimeout, timeout)
}

func TestChainedTransferController_CustomizeTransferInvocation_PropagatesErrorsAndSkipsPlainControllers(t *testing.T) {
	firstErr := errors.New("first customize failed")
	second := &runtimeTestController{message: "second"}
	target := agent.NewInvocation()
	err := (chainedTransferController{
		first:  &runtimeTestController{customizeErr: firstErr},
		second: second,
	}).CustomizeTransferInvocation(context.Background(), nil, target)
	require.ErrorIs(t, err, firstErr)
	require.Zero(t, second.customized)
	secondErr := errors.New("second customize failed")
	target = agent.NewInvocation()
	err = (chainedTransferController{
		first:  &runtimeTestController{message: "first"},
		second: &runtimeTestController{customizeErr: secondErr},
	}).CustomizeTransferInvocation(context.Background(), nil, target)
	require.ErrorIs(t, err, secondErr)
	require.Equal(t, "first", target.Message.Content)
	target = agent.NewInvocation()
	require.NoError(t, (chainedTransferController{
		first:  plainTransferController{},
		second: &runtimeTestController{message: "only customizer"},
	}).CustomizeTransferInvocation(context.Background(), nil, target))
	require.Equal(t, "only customizer", target.Message.Content)
	require.NoError(t, (chainedTransferController{
		first:  plainTransferController{},
		second: plainTransferController{},
	}).CustomizeTransferInvocation(context.Background(), nil, target))
}

func TestTighterTimeout_SelectsNonZeroMinimum(t *testing.T) {
	require.Equal(t, 3*time.Second, tighterTimeout(0, 3*time.Second))
	require.Equal(t, 3*time.Second, tighterTimeout(3*time.Second, 0))
	require.Equal(t, 2*time.Second, tighterTimeout(2*time.Second, 3*time.Second))
	require.Equal(t, 2*time.Second, tighterTimeout(3*time.Second, 2*time.Second))
}

type runtimeTestController struct {
	timeout      time.Duration
	transferErr  error
	customizeErr error
	message      string
	transfers    int
	customized   int
}

func (c *runtimeTestController) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	c.transfers++
	return c.timeout, c.transferErr
}

func (c *runtimeTestController) CustomizeTransferInvocation(
	_ context.Context,
	_ *agent.Invocation,
	target *agent.Invocation,
) error {
	c.customized++
	if c.customizeErr != nil {
		return c.customizeErr
	}
	if c.message != "" {
		target.Message = model.NewUserMessage(c.message)
	}
	return nil
}

type plainTransferController struct{}

func (plainTransferController) OnTransfer(
	context.Context,
	string,
	string,
) (time.Duration, error) {
	return 0, nil
}
