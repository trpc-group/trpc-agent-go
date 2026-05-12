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
	itransfer "trpc.group/trpc-go/trpc-agent-go/internal/transfer"
	"trpc.group/trpc-go/trpc-agent-go/model"
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
		SwarmConfig{NodeTimeout: testTimeout},
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
		SwarmConfig{MaxHandoffs: 1},
		nil,
	)
	ensureSwarmRuntime(
		invB,
		SwarmConfig{MaxHandoffs: 1},
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
		ensureSwarmRuntime(nil, SwarmConfig{}, nil)
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
