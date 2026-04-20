//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	toolcallidplugin "trpc.group/trpc-go/trpc-agent-go/plugin/toolcallid"
	baserunner "trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/multimodal"
	aguitool "trpc.group/trpc-go/trpc-agent-go/server/agui/internal/tool"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

func TestNew(t *testing.T) {
	r := New(nil)
	assert.NotNil(t, r)
	runner, ok := r.(*runner)
	assert.True(t, ok)

	assert.NotNil(t, runner.runAgentInputHook)
	assert.NotNil(t, runner.stateResolver)
	trans, err := runner.translatorFactory(context.Background(), &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.NoError(t, err)
	assert.NotNil(t, trans)
	expected, err := translator.New(context.Background(), "", "")
	assert.NoError(t, err)
	assert.IsType(t, expected, trans)
	assert.NotNil(t, runner.runOptionResolver)
	assert.False(t, runner.toolResultInputTranslationEnabled)
	assert.False(t, runner.streamingToolResultActivityEnabled)
	userID, err := runner.userIDResolver(context.Background(),
		&adapter.RunAgentInput{ThreadID: "thread", RunID: "run"})
	assert.NoError(t, err)
	assert.Equal(t, "user", userID)
}

func TestRunEmitsGraphNodeStartActivityWhenEnabled(t *testing.T) {
	meta := graph.NodeExecutionMetadata{
		NodeID:   "node-1",
		NodeType: graph.NodeTypeFunction,
		Phase:    graph.ExecutionPhaseStart,
		Attempt:  1,
	}
	raw, err := json.Marshal(meta)
	require.NoError(t, err)

	agentEvents := make(chan *agentevent.Event, 1)
	agentEvents <- &agentevent.Event{
		ID: "node-start-1",
		StateDelta: map[string][]byte{
			graph.MetadataKeyNode: raw,
		},
	}
	close(agentEvents)

	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			return agentEvents, nil
		},
	}

	r := New(underlying, WithGraphNodeLifecycleActivityEnabled(true))
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)

	evts := collectEvents(t, eventsCh)
	var found bool
	for _, evt := range evts {
		if delta, ok := evt.(*aguievents.ActivityDeltaEvent); ok {
			assert.Equal(t, "graph.node.lifecycle", delta.ActivityType)
			found = true
		}
	}
	assert.True(t, found)
}

func TestRunEmitsCanonicalAgentNodeLifecycleWhenEmitterMetadataPresent(t *testing.T) {
	startTime := time.Unix(1710000000, 0)
	endTime := startTime.Add(2 * time.Second)

	agentEvents := make(chan *agentevent.Event, 4)
	agentEvents <- graph.NewNodeStartEvent(
		graph.WithNodeEventInvocationID("inv-1"),
		graph.WithNodeEventNodeID("agent-node-1"),
		graph.WithNodeEventNodeType(graph.NodeTypeAgent),
		graph.WithNodeEventEmitter(graph.NodeEventEmitterAgentHelper),
		graph.WithNodeEventStartTime(startTime),
	)
	agentEvents <- graph.NewNodeStartEvent(
		graph.WithNodeEventInvocationID("inv-1"),
		graph.WithNodeEventNodeID("agent-node-1"),
		graph.WithNodeEventNodeType(graph.NodeTypeAgent),
		graph.WithNodeEventEmitter(graph.NodeEventEmitterExecutor),
		graph.WithNodeEventAttempt(1),
		graph.WithNodeEventStartTime(startTime),
	)
	agentEvents <- graph.NewNodeCompleteEvent(
		graph.WithNodeEventInvocationID("inv-1"),
		graph.WithNodeEventNodeID("agent-node-1"),
		graph.WithNodeEventNodeType(graph.NodeTypeAgent),
		graph.WithNodeEventEmitter(graph.NodeEventEmitterAgentHelper),
		graph.WithNodeEventStartTime(startTime),
		graph.WithNodeEventEndTime(endTime),
	)
	agentEvents <- graph.NewNodeCompleteEvent(
		graph.WithNodeEventInvocationID("inv-1"),
		graph.WithNodeEventNodeID("agent-node-1"),
		graph.WithNodeEventNodeType(graph.NodeTypeAgent),
		graph.WithNodeEventEmitter(graph.NodeEventEmitterExecutor),
		graph.WithNodeEventStepNumber(1),
		graph.WithNodeEventStartTime(startTime),
		graph.WithNodeEventEndTime(endTime),
	)
	close(agentEvents)

	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			return agentEvents, nil
		},
	}

	r := New(underlying, WithGraphNodeLifecycleActivityEnabled(true))
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)

	evts := collectEvents(t, eventsCh)
	var phases []string
	for _, evt := range evts {
		delta, ok := evt.(*aguievents.ActivityDeltaEvent)
		if !ok || delta.ActivityType != "graph.node.lifecycle" || len(delta.Patch) == 0 {
			continue
		}
		raw, err := json.Marshal(delta.Patch[0].Value)
		require.NoError(t, err)
		var patchValue map[string]any
		require.NoError(t, json.Unmarshal(raw, &patchValue))
		phase, _ := patchValue["phase"].(string)
		phases = append(phases, phase)
	}

	require.Equal(t, []string{"start", "complete"}, phases)
}

func TestRunEmitsGraphNodeInterruptActivityWhenEnabled(t *testing.T) {
	meta := graph.PregelStepMetadata{
		StepNumber:     3,
		NodeID:         "confirm",
		InterruptValue: "ask",
	}
	raw, err := json.Marshal(meta)
	require.NoError(t, err)

	agentEvents := make(chan *agentevent.Event, 1)
	agentEvents <- &agentevent.Event{
		ID: "pregel-interrupt-1",
		StateDelta: map[string][]byte{
			graph.MetadataKeyPregel: raw,
		},
	}
	close(agentEvents)

	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			return agentEvents, nil
		},
	}

	r := New(underlying, WithGraphNodeInterruptActivityEnabled(true))
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)

	evts := collectEvents(t, eventsCh)
	var found bool
	for _, evt := range evts {
		if delta, ok := evt.(*aguievents.ActivityDeltaEvent); ok {
			assert.Equal(t, "graph.node.interrupt", delta.ActivityType)
			found = true
		}
	}
	assert.True(t, found)
}

func TestRunEmitsGraphNodeInterruptResumeAckWhenResuming(t *testing.T) {
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			agentEvents := make(chan *agentevent.Event)
			close(agentEvents)
			return agentEvents, nil
		},
	}

	r := New(
		underlying,
		WithGraphNodeInterruptActivityEnabled(true),
		WithRunOptionResolver(func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
			runtimeState := map[string]any{
				graph.CfgKeyLineageID:    "demo-lineage",
				graph.CfgKeyCheckpointID: "ckpt-uuid-xxx",
				graph.StateKeyCommand: &graph.Command{
					ResumeMap: map[string]any{
						"confirm": true,
					},
				},
			}
			return []agent.RunOption{agent.WithRuntimeState(runtimeState)}, nil
		}),
	)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}

	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)

	evts := collectEvents(t, eventsCh)
	require.Len(t, evts, 2)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])

	delta, ok := evts[1].(*aguievents.ActivityDeltaEvent)
	require.True(t, ok)
	assert.Equal(t, "graph.node.interrupt", delta.ActivityType)
	require.Len(t, delta.Patch, 2)
	assert.Equal(t, "add", delta.Patch[0].Op)
	assert.Equal(t, "/interrupt", delta.Patch[0].Path)
	assert.Equal(t, json.RawMessage("null"), delta.Patch[0].Value)
	assert.Equal(t, "add", delta.Patch[1].Op)
	assert.Equal(t, "/resume", delta.Patch[1].Path)
	assert.Equal(t, map[string]any{
		"checkpointId": "ckpt-uuid-xxx",
		"lineageId":    "demo-lineage",
		"resumeMap": map[string]any{
			"confirm": true,
		},
	}, delta.Patch[1].Value)
}

func TestRunEmitsGraphNodeInterruptResumeAckWhenResumingViaStateKeyResumeMap(t *testing.T) {
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			agentEvents := make(chan *agentevent.Event)
			close(agentEvents)
			return agentEvents, nil
		},
	}
	r := New(
		underlying,
		WithGraphNodeInterruptActivityEnabled(true),
		WithRunOptionResolver(func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
			runtimeState := map[string]any{
				graph.CfgKeyLineageID:    "demo-lineage",
				graph.CfgKeyCheckpointID: "ckpt-uuid-xxx",
				graph.StateKeyResumeMap: map[string]any{
					"confirm": true,
				},
			}
			return []agent.RunOption{agent.WithRuntimeState(runtimeState)}, nil
		}),
	)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	require.Len(t, evts, 2)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	delta, ok := evts[1].(*aguievents.ActivityDeltaEvent)
	require.True(t, ok)
	assert.Equal(t, "graph.node.interrupt", delta.ActivityType)
	require.Len(t, delta.Patch, 2)
	assert.Equal(t, "add", delta.Patch[0].Op)
	assert.Equal(t, "/interrupt", delta.Patch[0].Path)
	assert.Equal(t, json.RawMessage("null"), delta.Patch[0].Value)
	assert.Equal(t, "add", delta.Patch[1].Op)
	assert.Equal(t, "/resume", delta.Patch[1].Path)
	assert.Equal(t, map[string]any{
		"checkpointId": "ckpt-uuid-xxx",
		"lineageId":    "demo-lineage",
		"resumeMap": map[string]any{
			"confirm": true,
		},
	}, delta.Patch[1].Value)
}

func TestRunEmitsGraphNodeInterruptResumeAckWhenResumingViaResumeChannel(t *testing.T) {
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			agentEvents := make(chan *agentevent.Event)
			close(agentEvents)
			return agentEvents, nil
		},
	}
	r := New(
		underlying,
		WithGraphNodeInterruptActivityEnabled(true),
		WithRunOptionResolver(func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
			runtimeState := map[string]any{
				graph.CfgKeyLineageID:    "demo-lineage",
				graph.CfgKeyCheckpointID: "ckpt-uuid-xxx",
				graph.ResumeChannel:      "approved",
			}
			return []agent.RunOption{agent.WithRuntimeState(runtimeState)}, nil
		}),
	)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	require.Len(t, evts, 2)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	delta, ok := evts[1].(*aguievents.ActivityDeltaEvent)
	require.True(t, ok)
	assert.Equal(t, "graph.node.interrupt", delta.ActivityType)
	require.Len(t, delta.Patch, 2)
	assert.Equal(t, "add", delta.Patch[0].Op)
	assert.Equal(t, "/interrupt", delta.Patch[0].Path)
	assert.Equal(t, json.RawMessage("null"), delta.Patch[0].Value)
	assert.Equal(t, "add", delta.Patch[1].Op)
	assert.Equal(t, "/resume", delta.Patch[1].Path)
	assert.Equal(t, map[string]any{
		"checkpointId": "ckpt-uuid-xxx",
		"lineageId":    "demo-lineage",
		"resume":       "approved",
	}, delta.Patch[1].Value)
}

func TestRunEmitsGraphNodeInterruptResumeAckWhenResumingViaResumeChannelNull(t *testing.T) {
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			agentEvents := make(chan *agentevent.Event)
			close(agentEvents)
			return agentEvents, nil
		},
	}
	r := New(
		underlying,
		WithGraphNodeInterruptActivityEnabled(true),
		WithRunOptionResolver(func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
			runtimeState := map[string]any{
				graph.CfgKeyLineageID:    "demo-lineage",
				graph.CfgKeyCheckpointID: "ckpt-uuid-xxx",
				graph.ResumeChannel:      nil,
			}
			return []agent.RunOption{agent.WithRuntimeState(runtimeState)}, nil
		}),
	)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	require.Len(t, evts, 2)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	delta, ok := evts[1].(*aguievents.ActivityDeltaEvent)
	require.True(t, ok)
	assert.Equal(t, "graph.node.interrupt", delta.ActivityType)
	require.Len(t, delta.Patch, 2)
	assert.Equal(t, "add", delta.Patch[0].Op)
	assert.Equal(t, "/interrupt", delta.Patch[0].Path)
	assert.Equal(t, json.RawMessage("null"), delta.Patch[0].Value)
	assert.Equal(t, "add", delta.Patch[1].Op)
	assert.Equal(t, "/resume", delta.Patch[1].Path)
	assert.Equal(t, map[string]any{
		"checkpointId": "ckpt-uuid-xxx",
		"lineageId":    "demo-lineage",
		"resume":       nil,
	}, delta.Patch[1].Value)
}

func TestRunDoesNotEmitGraphNodeInterruptResumeAckWhenCommandBindsEmptyResumeMap(t *testing.T) {
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			agentEvents := make(chan *agentevent.Event)
			close(agentEvents)
			return agentEvents, nil
		},
	}
	r := New(
		underlying,
		WithGraphNodeInterruptActivityEnabled(true),
		WithRunOptionResolver(func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
			runtimeState := map[string]any{
				graph.StateKeyCommand: &graph.Command{
					ResumeMap: map[string]any{},
				},
				graph.StateKeyResumeMap: map[string]any{
					"confirm": true,
				},
			}
			return []agent.RunOption{agent.WithRuntimeState(runtimeState)}, nil
		}),
	)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	require.Len(t, evts, 1)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
}

func TestRunValidatesInput(t *testing.T) {
	r := &runner{runOptionResolver: defaultRunOptionResolver}
	ch, err := r.Run(context.Background(), nil)
	assert.Nil(t, ch)
	assert.Error(t, err)

	r.runner = &fakeRunner{}
	ch, err = r.Run(context.Background(), nil)
	assert.Nil(t, ch)
	assert.Error(t, err)
}

func TestRunIgnoresRequestCancelButRespectsBackendTimeout(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ctxCh <- ctx
			ch := make(chan *agentevent.Event)
			go func() {
				<-ctx.Done()
				close(ch)
			}()
			return ch, nil
		},
	}
	r := &runner{
		runner:            underlying,
		translatorFactory: defaultTranslatorFactory,
		userIDResolver:    defaultUserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		startSpan:         defaultStartSpan,
		timeout:           200 * time.Millisecond,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventsCh, err := r.Run(reqCtx, input)
	require.NoError(t, err)

	select {
	case evt := <-eventsCh:
		assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evt)
	case <-time.After(time.Second):
		assert.FailNow(t, "timeout waiting for run started event")
	}

	var runCtx context.Context
	select {
	case runCtx = <-ctxCh:
	case <-time.After(time.Second):
		assert.FailNow(t, "timeout waiting for underlying runner context")
	}

	cancel()

	assert.Eventually(t, func() bool {
		return errors.Is(runCtx.Err(), context.DeadlineExceeded)
	}, time.Second, 5*time.Millisecond)

	evts := collectEvents(t, eventsCh)
	assert.False(t, hasRunFinishedEvent(evts))
	assert.True(t, hasRunErrorEvent(evts))
}

func TestRunCancelsOnRequestCancelWhenEnabled(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ctxCh <- ctx
			ch := make(chan *agentevent.Event)
			go func() {
				<-ctx.Done()
				close(ch)
			}()
			return ch, nil
		},
	}
	r := New(
		underlying,
		WithCancelOnContextDoneEnabled(true),
		WithTimeout(200*time.Millisecond),
	)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	reqCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	eventsCh, err := r.Run(reqCtx, input)
	require.NoError(t, err)

	select {
	case evt := <-eventsCh:
		assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evt)
	case <-time.After(time.Second):
		assert.FailNow(t, "timeout waiting for run started event")
	}

	var runCtx context.Context
	select {
	case runCtx = <-ctxCh:
	case <-time.After(time.Second):
		assert.FailNow(t, "timeout waiting for underlying runner context")
	}

	cancel()

	assert.Eventually(t, func() bool {
		return errors.Is(runCtx.Err(), context.Canceled)
	}, time.Second, 5*time.Millisecond)

	evts := collectEvents(t, eventsCh)
	assert.False(t, hasRunFinishedEvent(evts))
	assert.True(t, hasRunErrorEvent(evts))
}

func TestRunTimeoutUsesMinRequestDeadlineAndBackendTimeout(t *testing.T) {
	ctxCh := make(chan context.Context, 1)
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ctxCh <- ctx
			ch := make(chan *agentevent.Event)
			go func() {
				<-ctx.Done()
				close(ch)
			}()
			return ch, nil
		},
	}
	r := &runner{
		runner:            underlying,
		translatorFactory: defaultTranslatorFactory,
		userIDResolver:    defaultUserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		startSpan:         defaultStartSpan,
		timeout:           500 * time.Millisecond,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	reqCtx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	reqDeadline, ok := reqCtx.Deadline()
	require.True(t, ok)

	eventsCh, err := r.Run(reqCtx, input)
	require.NoError(t, err)

	select {
	case evt := <-eventsCh:
		assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evt)
	case <-time.After(time.Second):
		assert.FailNow(t, "timeout waiting for run started event")
	}

	var runCtx context.Context
	select {
	case runCtx = <-ctxCh:
	case <-time.After(time.Second):
		assert.FailNow(t, "timeout waiting for underlying runner context")
	}

	deadline, ok := runCtx.Deadline()
	require.True(t, ok)
	assert.WithinDuration(t, reqDeadline, deadline, 50*time.Millisecond)

	cancel()

	wait := time.Until(deadline) + 200*time.Millisecond
	if wait < 0 {
		wait = 200 * time.Millisecond
	}
	assert.Eventually(t, func() bool {
		return errors.Is(runCtx.Err(), context.DeadlineExceeded)
	}, wait, 5*time.Millisecond)

	_ = collectEvents(t, eventsCh)
}

func TestRunNoMessages(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner: underlying,
		translatorFactory: func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver:    NewOptions().UserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
	}

	input := &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"}
	eventsCh, err := r.Run(context.Background(), input)
	assert.Nil(t, eventsCh)
	assert.ErrorContains(t, err, "build input message")
	assert.ErrorContains(t, err, "no messages provided")
	assert.Equal(t, 0, underlying.calls)
}

func TestRunUserIDResolverError(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner: underlying,
		translatorFactory: func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "", errors.New("boom")
		},
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.Nil(t, eventsCh)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolve user ID")
	assert.Equal(t, 0, underlying.calls)
}

func TestRunLastMessageNotUser(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner: underlying,
		translatorFactory: func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver:    NewOptions().UserIDResolver,
		runOptionResolver: defaultRunOptionResolver,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleAssistant, Content: "bot"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.Nil(t, eventsCh)
	assert.ErrorContains(t, err, "build input message")
	assert.ErrorContains(t, err, "last message role must be user or tool")
	assert.Equal(t, 0, underlying.calls)
}

func TestRunToolMessageMissingToolCallID(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner: underlying,
		translatorFactory: func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver:    NewOptions().UserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		startSpan:         defaultStartSpan,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleTool, Content: "ok"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.Nil(t, eventsCh)
	assert.ErrorContains(t, err, "build input message")
	assert.ErrorContains(t, err, "tool message missing tool call id")
	assert.Equal(t, 0, underlying.calls)
}

func TestRunUnderlyingRunnerError(t *testing.T) {
	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context, userID, sessionID string, message model.Message,
		_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
		return nil, errors.New("fail")
	}
	fakeTrans := &fakeTranslator{}
	r := &runner{
		runner: underlying,
		translatorFactory: func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver:    NewOptions().UserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		startSpan:         defaultStartSpan,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	assert.Len(t, evts, 2)
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
	assert.Equal(t, 1, underlying.calls)
}

func TestRunToolMessageRecordedInTrackAndForwarded(t *testing.T) {
	var got model.Message
	tracker := &recordingTracker{}
	underlying := &fakeRunner{
		run: func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			got = message
			ch := make(chan *agentevent.Event)
			close(ch)
			return ch, nil
		},
	}
	r := &runner{
		appName:            "app",
		runner:             underlying,
		translatorFactory:  defaultTranslatorFactory,
		userIDResolver:     defaultUserIDResolver,
		stateResolver:      defaultStateResolver,
		runOptionResolver:  defaultRunOptionResolver,
		tracker:            tracker,
		startSpan:          defaultStartSpan,
		translateCallbacks: nil,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{
			{
				ID:         "tool-msg-1",
				Role:       types.RoleTool,
				Content:    "result",
				Name:       "calc",
				ToolCallID: "call-1",
			},
		},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	evts := collectEvents(t, eventsCh)

	var sseFound bool
	for _, evt := range evts {
		res, ok := evt.(*aguievents.ToolCallResultEvent)
		if !ok {
			continue
		}
		assert.Equal(t, "tool-msg-1", res.MessageID)
		assert.Equal(t, "call-1", res.ToolCallID)
		assert.Equal(t, "result", res.Content)
		sseFound = true
	}
	assert.True(t, sseFound)

	assert.Equal(t, model.RoleTool, got.Role)
	assert.Equal(t, "result", got.Content)
	assert.Equal(t, "calc", got.ToolName)
	assert.Equal(t, "call-1", got.ToolID)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	var found bool
	for _, evt := range tracker.events {
		res, ok := evt.(*aguievents.ToolCallResultEvent)
		if !ok {
			continue
		}
		assert.Equal(t, "tool-msg-1", res.MessageID)
		assert.Equal(t, "call-1", res.ToolCallID)
		assert.Equal(t, "result", res.Content)
		found = true
	}
	assert.True(t, found)
}

func TestRecordUserMessageTracksCustomEvent(t *testing.T) {
	tracker := &recordingTracker{}
	r := &runner{tracker: tracker}
	key := session.Key{AppName: "app", UserID: "demo-user", SessionID: "thread"}
	msg := &types.Message{Role: types.RoleUser, Content: "hi"}

	err := r.recordUserMessage(context.Background(), key, msg)
	require.NoError(t, err)
	assert.Empty(t, msg.ID)
	assert.Empty(t, msg.Name)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	require.Len(t, tracker.events, 1)
	custom, ok := tracker.events[0].(*aguievents.CustomEvent)
	require.True(t, ok)
	assert.Equal(t, multimodal.CustomEventNameUserMessage, custom.Name)
	userMessage, ok := custom.Value.(types.Message)
	require.True(t, ok)
	assert.NotEmpty(t, userMessage.ID)
	assert.Equal(t, types.RoleUser, userMessage.Role)
	assert.Equal(t, "demo-user", userMessage.Name)
	content, ok := userMessage.ContentString()
	require.True(t, ok)
	assert.Equal(t, "hi", content)
}

func TestRunUsesResolvedAppNameForTrackKey(t *testing.T) {
	tracker := &recordingTracker{}
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event)
			close(ch)
			return ch, nil
		},
	}
	r := &runner{
		appName:            "static-app",
		appNameResolver:    forwardedPropsAppNameResolver,
		runner:             underlying,
		translatorFactory:  defaultTranslatorFactory,
		userIDResolver:     func(context.Context, *adapter.RunAgentInput) (string, error) { return "demo-user", nil },
		stateResolver:      defaultStateResolver,
		runOptionResolver:  defaultRunOptionResolver,
		tracker:            tracker,
		startSpan:          defaultStartSpan,
		translateCallbacks: nil,
	}
	input := &adapter.RunAgentInput{
		ThreadID:       "thread",
		RunID:          "run",
		ForwardedProps: map[string]any{"appName": "dynamic-app"},
		Messages:       []types.Message{{ID: "user-msg-1", Role: types.RoleUser, Content: "hi"}},
	}
	ch, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	collectEvents(t, ch)
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	require.NotEmpty(t, tracker.keys)
	for _, key := range tracker.keys {
		assert.Equal(t, "dynamic-app", key.AppName)
		assert.Equal(t, "demo-user", key.UserID)
		assert.Equal(t, "thread", key.SessionID)
	}
}

func TestRunAppNameResolverError(t *testing.T) {
	underlying := &fakeRunner{}
	r := &runner{
		runner:            underlying,
		appNameResolver:   func(context.Context, *adapter.RunAgentInput) (string, error) { return "", errors.New("boom") },
		translatorFactory: defaultTranslatorFactory,
		userIDResolver:    func(context.Context, *adapter.RunAgentInput) (string, error) { return "demo-user", nil },
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		startSpan:         defaultStartSpan,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	ch, err := r.Run(context.Background(), input)
	assert.Nil(t, ch)
	assert.ErrorContains(t, err, "resolve app name")
	assert.Equal(t, 0, underlying.calls)
}

func TestResolveAppNameFallsBackToStaticAppName(t *testing.T) {
	r := &runner{appName: "static-app"}
	appName, err := r.resolveAppName(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	assert.Equal(t, "static-app", appName)
	r.appNameResolver = func(context.Context, *adapter.RunAgentInput) (string, error) {
		return "", nil
	}
	appName, err = r.resolveAppName(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	assert.Equal(t, "static-app", appName)
}

func TestResolveAppNameReturnsResolverError(t *testing.T) {
	r := &runner{
		appName: "static-app",
		appNameResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "", errors.New("boom")
		},
	}
	appName, err := r.resolveAppName(context.Background(), &adapter.RunAgentInput{})
	assert.Empty(t, appName)
	assert.EqualError(t, err, "boom")
}

func TestRecordUserMessageRejectsNilAndNonUserRole(t *testing.T) {
	r := &runner{}
	key := session.Key{AppName: "app", UserID: "demo-user", SessionID: "thread"}

	err := r.recordUserMessage(context.Background(), key, nil)
	assert.ErrorContains(t, err, "user message is nil")

	err = r.recordUserMessage(context.Background(), key, &types.Message{Role: types.RoleTool, Content: "hi"})
	assert.ErrorContains(t, err, "user message role must be user")
}

func TestRunUserMessageRecordedInTrackAsCustomEventWithStringContent(t *testing.T) {
	tracker := &recordingTracker{}
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event)
			close(ch)
			return ch, nil
		},
	}
	r := &runner{
		appName:            "app",
		runner:             underlying,
		translatorFactory:  defaultTranslatorFactory,
		userIDResolver:     func(context.Context, *adapter.RunAgentInput) (string, error) { return "demo-user", nil },
		stateResolver:      defaultStateResolver,
		runOptionResolver:  defaultRunOptionResolver,
		tracker:            tracker,
		startSpan:          defaultStartSpan,
		translateCallbacks: nil,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{ID: "user-msg-1", Role: types.RoleUser, Content: "hi"}},
	}

	ch, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	collectEvents(t, ch)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	var (
		found bool
		msg   types.Message
	)
	for _, evt := range tracker.events {
		custom, ok := evt.(*aguievents.CustomEvent)
		if !ok || custom.Name != multimodal.CustomEventNameUserMessage {
			continue
		}
		value, ok := custom.Value.(types.Message)
		require.True(t, ok)
		msg = value
		found = true
	}
	require.True(t, found)
	assert.Equal(t, "user-msg-1", msg.ID)
	assert.Equal(t, types.RoleUser, msg.Role)
	assert.Equal(t, "demo-user", msg.Name)
	content, ok := msg.ContentString()
	require.True(t, ok)
	assert.Equal(t, "hi", content)
}

func TestRunUserMessageRecordedInTrackAsCustomEventWithInputContents(t *testing.T) {
	tracker := &recordingTracker{}
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event)
			close(ch)
			return ch, nil
		},
	}
	r := &runner{
		appName:            "app",
		runner:             underlying,
		translatorFactory:  defaultTranslatorFactory,
		userIDResolver:     func(context.Context, *adapter.RunAgentInput) (string, error) { return "demo-user", nil },
		stateResolver:      defaultStateResolver,
		runOptionResolver:  defaultRunOptionResolver,
		tracker:            tracker,
		startSpan:          defaultStartSpan,
		translateCallbacks: nil,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{
			ID:   "user-msg-2",
			Role: types.RoleUser,
			Content: []any{
				map[string]any{"type": "binary", "mimeType": "image/jpeg", "url": "https://example.com/a.jpg"},
				map[string]any{"type": "text", "text": "hello"},
			},
		}},
	}

	ch, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	collectEvents(t, ch)

	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	var (
		found bool
		msg   types.Message
	)
	for _, evt := range tracker.events {
		custom, ok := evt.(*aguievents.CustomEvent)
		if !ok || custom.Name != multimodal.CustomEventNameUserMessage {
			continue
		}
		value, ok := custom.Value.(types.Message)
		require.True(t, ok)
		msg = value
		found = true
	}
	require.True(t, found)
	assert.Equal(t, "user-msg-2", msg.ID)
	assert.Equal(t, types.RoleUser, msg.Role)
	assert.Equal(t, "demo-user", msg.Name)
	contents, ok := msg.Content.([]types.InputContent)
	require.True(t, ok)
	require.Len(t, contents, 2)
	assert.Equal(t, types.InputContentTypeBinary, contents[0].Type)
	assert.Equal(t, "image/jpeg", contents[0].MimeType)
	assert.Equal(t, "https://example.com/a.jpg", contents[0].URL)
	assert.Equal(t, types.InputContentTypeText, contents[1].Type)
	assert.Equal(t, "hello", contents[1].Text)
}

func TestRunToolMessageSSEOrderAfterRunStarted(t *testing.T) {
	underlying := &fakeRunner{
		run: func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event, 1)
			ch <- agentevent.New("inv", "assistant")
			close(ch)
			return ch, nil
		},
	}
	fakeTrans := &fakeTranslator{
		events: [][]aguievents.Event{
			{
				aguievents.NewActivityDeltaEvent(
					"activity-1",
					"graph.node.start",
					[]aguievents.JSONPatchOperation{
						{
							Op:   "add",
							Path: "/node",
							Value: map[string]any{
								"nodeId": "external_tool",
							},
						},
					},
				),
				aguievents.NewTextMessageStartEvent("msg-1", aguievents.WithRole("assistant")),
			},
		},
	}
	r := &runner{
		runner: underlying,
		translatorFactory: func(ctx context.Context, input *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver:    NewOptions().UserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		startSpan:         defaultStartSpan,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{
			{
				ID:         "tool-msg-1",
				Role:       types.RoleTool,
				Content:    "tool result",
				ToolCallID: "call-1",
			},
		},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	require.Len(t, evts, 4)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	assert.IsType(t, (*aguievents.ToolCallResultEvent)(nil), evts[1])
	assert.IsType(t, (*aguievents.ActivityDeltaEvent)(nil), evts[2])
	assert.IsType(t, (*aguievents.TextMessageStartEvent)(nil), evts[3])
	resultEvent, ok := evts[1].(*aguievents.ToolCallResultEvent)
	require.True(t, ok)
	assert.Equal(t, "tool-msg-1", resultEvent.MessageID)
	assert.Equal(t, "call-1", resultEvent.ToolCallID)
	assert.Equal(t, "tool result", resultEvent.Content)
	require.Len(t, fakeTrans.seen, 1)
	assert.Equal(t, "assistant", fakeTrans.seen[0].Author)
	assert.False(t, fakeTrans.seen[0].Response.IsToolResultResponse())
}

func TestRunToolMessageTranslatedWhenEnabled(t *testing.T) {
	underlying := &fakeRunner{
		run: func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event)
			close(ch)
			return ch, nil
		},
	}
	fakeTrans := &fakeTranslator{
		events: [][]aguievents.Event{{
			aguievents.NewCustomEvent("translated-tool-result", aguievents.WithValue("ok")),
		}},
	}
	r := &runner{
		runner: underlying,
		translatorFactory: func(ctx context.Context, input *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver:                    NewOptions().UserIDResolver,
		stateResolver:                     defaultStateResolver,
		runOptionResolver:                 defaultRunOptionResolver,
		startSpan:                         defaultStartSpan,
		toolResultInputTranslationEnabled: true,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{
			{
				ID:         "tool-msg-1",
				Role:       types.RoleTool,
				Content:    "tool result",
				Name:       "calculator",
				ToolCallID: "call-1",
			},
		},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	require.Len(t, evts, 2)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	customEvent, ok := evts[1].(*aguievents.CustomEvent)
	require.True(t, ok)
	assert.Equal(t, "translated-tool-result", customEvent.Name)
	require.Len(t, fakeTrans.seen, 1)
	seen := fakeTrans.seen[0]
	require.NotNil(t, seen)
	assert.Equal(t, toolResultInputEventAuthor, seen.Author)
	assert.Empty(t, seen.Tag)
	assert.Equal(t, "tool-msg-1", seen.ID)
	require.NotNil(t, seen.Response)
	assert.Equal(t, model.ObjectTypeToolResponse, seen.Response.Object)
	assert.True(t, seen.Response.IsToolResultResponse())
	require.Len(t, seen.Response.Choices, 1)
	assert.Equal(t, model.RoleTool, seen.Response.Choices[0].Message.Role)
	assert.Equal(t, "tool result", seen.Response.Choices[0].Message.Content)
	assert.Equal(t, "call-1", seen.Response.Choices[0].Message.ToolID)
	assert.Equal(t, "calculator", seen.Response.Choices[0].Message.ToolName)
}

func TestRunRunOptionResolverError(t *testing.T) {
	underlying := &fakeRunner{}
	fakeTrans := &fakeTranslator{}
	wantErr := errors.New("resolver broke")
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	r := &runner{
		runner: underlying,
		translatorFactory: func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver: NewOptions().UserIDResolver,
		stateResolver:  defaultStateResolver,
		runOptionResolver: func(ctx context.Context, in *adapter.RunAgentInput) ([]agent.RunOption, error) {
			assert.Same(t, input, in)
			return nil, wantErr
		},
	}

	eventsCh, err := r.Run(context.Background(), input)
	assert.Nil(t, eventsCh)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "resolve run option")
	assert.Equal(t, 0, underlying.calls)
}

func TestRunStartSpanError(t *testing.T) {
	startErr := errors.New("start span fail")
	underlying := &fakeRunner{}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	r := &runner{
		runner: underlying,
		translatorFactory: func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return &fakeTranslator{}, nil
		},
		userIDResolver: defaultUserIDResolver,
		stateResolver:  defaultStateResolver,
		runOptionResolver: func(ctx context.Context, in *adapter.RunAgentInput) ([]agent.RunOption, error) {
			assert.Same(t, input, in)
			return nil, nil
		},
		startSpan: func(ctx context.Context, in *adapter.RunAgentInput) (context.Context, trace.Span, error) {
			assert.Same(t, input, in)
			return ctx, trace.SpanFromContext(ctx), startErr
		},
	}

	eventsCh, err := r.Run(context.Background(), input)
	assert.Nil(t, eventsCh)
	assert.ErrorIs(t, err, startErr)
	assert.Equal(t, 0, underlying.calls)
}

func TestRunLastMessageContentArray(t *testing.T) {
	messageCh := make(chan model.Message, 1)
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			messageCh <- message
			ch := make(chan *agentevent.Event)
			close(ch)
			return ch, nil
		},
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{
			Role: types.RoleUser,
			Content: []types.InputContent{
				{Type: types.InputContentTypeBinary, MimeType: "image/jpeg", URL: "https://example.com/resource/download?id=1"},
				{Type: types.InputContentTypeText, Text: "图中有哪些信息?"},
			},
		}},
	}
	r := &runner{
		runner:            underlying,
		translatorFactory: defaultTranslatorFactory,
		userIDResolver:    defaultUserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		startSpan:         defaultStartSpan,
	}

	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, eventsCh)
	collectEvents(t, eventsCh)

	gotMessage := <-messageCh
	assert.Equal(t, model.RoleUser, gotMessage.Role)
	assert.Empty(t, gotMessage.Content)
	require.Len(t, gotMessage.ContentParts, 2)
	assert.Equal(t, model.ContentTypeImage, gotMessage.ContentParts[0].Type)
	require.NotNil(t, gotMessage.ContentParts[0].Image)
	assert.Equal(t, "https://example.com/resource/download?id=1", gotMessage.ContentParts[0].Image.URL)
	assert.Empty(t, gotMessage.ContentParts[0].Image.Detail)
	assert.Equal(t, model.ContentTypeText, gotMessage.ContentParts[1].Type)
	require.NotNil(t, gotMessage.ContentParts[1].Text)
	assert.Equal(t, "图中有哪些信息?", *gotMessage.ContentParts[1].Text)
	assert.Equal(t, 1, underlying.calls)
}

func TestInputMessageFromRunAgentInputConvertsInputContentsFromAny(t *testing.T) {
	input := &adapter.RunAgentInput{
		Messages: []types.Message{{
			ID:   "msg-1",
			Role: types.RoleUser,
			Content: []any{
				map[string]any{"type": "binary", "mimeType": "image/jpeg", "url": "https://example.com/a.jpg"},
				map[string]any{"type": "text", "text": "hello"},
			},
		}},
	}

	gotMessage, gotID, gotUserMessage, err := inputMessageFromRunAgentInput(input)
	require.NoError(t, err)
	require.NotNil(t, gotMessage)
	assert.Equal(t, "msg-1", gotID)
	require.NotNil(t, gotUserMessage)

	assert.Equal(t, model.RoleUser, gotMessage.Role)
	assert.Empty(t, gotMessage.Content)
	require.Len(t, gotMessage.ContentParts, 2)
	assert.Equal(t, model.ContentTypeImage, gotMessage.ContentParts[0].Type)
	require.NotNil(t, gotMessage.ContentParts[0].Image)
	assert.Equal(t, "https://example.com/a.jpg", gotMessage.ContentParts[0].Image.URL)

	contents, ok := gotUserMessage.Content.([]types.InputContent)
	require.True(t, ok)
	require.Len(t, contents, 2)
	assert.Equal(t, types.InputContentTypeBinary, contents[0].Type)
	assert.Equal(t, "image/jpeg", contents[0].MimeType)
	assert.Equal(t, "https://example.com/a.jpg", contents[0].URL)
	assert.Equal(t, types.InputContentTypeText, contents[1].Type)
	assert.Equal(t, "hello", contents[1].Text)
}

func TestRunLastMessageContentNotString(t *testing.T) {
	underlying := &fakeRunner{}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{
			Role:    types.RoleUser,
			Content: map[string]any{"invalid": "payload"},
		}},
	}
	startSpanCalled := false
	r := &runner{
		runner:            underlying,
		translatorFactory: defaultTranslatorFactory,
		userIDResolver:    defaultUserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		startSpan: func(ctx context.Context, in *adapter.RunAgentInput) (context.Context, trace.Span, error) {
			assert.Same(t, input, in)
			startSpanCalled = true
			return ctx, trace.SpanFromContext(ctx), nil
		},
	}

	eventsCh, err := r.Run(context.Background(), input)
	assert.Nil(t, eventsCh)
	assert.ErrorContains(t, err, "build input message")
	assert.ErrorContains(t, err, "last message content is not a string")
	assert.False(t, startSpanCalled)
	assert.Equal(t, 0, underlying.calls)
}

func TestRunFlushesTracker(t *testing.T) {
	recorder := &flushRecorder{}
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event)
			close(ch)
			return ch, nil
		},
	}
	r := &runner{
		runner:            underlying,
		translatorFactory: defaultTranslatorFactory,
		userIDResolver:    defaultUserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		tracker:           recorder,
		startSpan:         defaultStartSpan,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	ch, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	collectEvents(t, ch)
	assert.GreaterOrEqual(t, recorder.appendCount, 1)
	assert.Equal(t, 1, recorder.flushCount)
}

func TestNewWithSessionServiceEnablesTracker(t *testing.T) {
	underlying := &fakeRunner{
		run: func(context.Context, string, string, model.Message, ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event)
			close(ch)
			return ch, nil
		},
	}
	r := New(underlying, WithSessionService(inmemory.NewSessionService()))
	run, ok := r.(*runner)
	assert.True(t, ok)
	assert.NotNil(t, run.tracker)
}

type nonTrackSessionService struct {
	session.Service
}

func TestNewWithSessionServiceWithoutTrackDisablesTracker(t *testing.T) {
	r := New(nil, WithSessionService(nonTrackSessionService{Service: inmemory.NewSessionService()}))
	run, ok := r.(*runner)
	assert.True(t, ok)
	assert.Nil(t, run.tracker)
}

func TestRunRejectsConcurrentSession(t *testing.T) {
	ch := make(chan *agentevent.Event)
	underlying := &fakeRunner{
		run: func(context.Context, string, string, model.Message, ...agent.RunOption) (<-chan *agentevent.Event, error) {
			return ch, nil
		},
	}
	r := New(underlying).(*runner)

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}

	events1, err := r.Run(context.Background(), input)
	assert.NoError(t, err)

	input2 := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run-2",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi again"}},
	}
	events2, err := r.Run(context.Background(), input2)
	assert.Nil(t, events2)
	assert.Error(t, err)
	assert.ErrorIs(t, err, ErrRunAlreadyExists)

	close(ch)
	collectEvents(t, events1)
}

func TestRunRunOptionResolverOptions(t *testing.T) {
	fakeTrans := &fakeTranslator{}
	resolverCalled := false
	optionsApplied := false
	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context,
		userID, sessionID string,
		message model.Message,
		opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
		assert.Equal(t, "user-123", userID)
		assert.Len(t, opts, 1)
		var runOpts agent.RunOptions
		for _, opt := range opts {
			opt(&runOpts)
		}
		assert.Equal(t, "resolver-request-id", runOpts.RequestID)
		optionsApplied = true
		ch := make(chan *agentevent.Event)
		go func() {
			close(ch)
		}()
		return ch, nil
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	r := &runner{
		runner: underlying,
		translatorFactory: func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "user-123", nil
		},
		stateResolver: defaultStateResolver,
		runOptionResolver: func(ctx context.Context, in *adapter.RunAgentInput) ([]agent.RunOption, error) {
			assert.Same(t, input, in)
			resolverCalled = true
			return []agent.RunOption{agent.WithRequestID("resolver-request-id")}, nil
		},
		startSpan: defaultStartSpan,
	}

	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	assert.NotEmpty(t, evts)
	assert.True(t, resolverCalled)
	assert.True(t, optionsApplied)
	assert.Equal(t, 1, underlying.calls)
}

func TestRunStateResolverOverridesRuntimeState(t *testing.T) {
	var runOpts agent.RunOptions
	underlying := &fakeRunner{
		run: func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
			for _, opt := range opts {
				opt(&runOpts)
			}
			ch := make(chan *agentevent.Event)
			close(ch)
			return ch, nil
		},
	}
	r := New(
		underlying,
		WithStateResolver(func(context.Context, *adapter.RunAgentInput) (map[string]any, error) {
			return map[string]any{
				"k1":                  "v1",
				graph.CfgKeyLineageID: "from-state",
			}, nil
		}),
		WithRunOptionResolver(func(context.Context, *adapter.RunAgentInput) ([]agent.RunOption, error) {
			return []agent.RunOption{
				agent.WithRuntimeState(map[string]any{
					graph.CfgKeyLineageID: "from-runopt",
					"k2":                  "v2",
				}),
			}, nil
		}),
	)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	_ = collectEvents(t, eventsCh)

	require.NotNil(t, runOpts.RuntimeState)
	assert.Equal(t, "v1", runOpts.RuntimeState["k1"])
	assert.Equal(t, "from-state", runOpts.RuntimeState[graph.CfgKeyLineageID])
	_, ok := runOpts.RuntimeState["k2"]
	assert.False(t, ok)
}

func TestRunStateResolverError(t *testing.T) {
	underlying := &fakeRunner{}
	wantErr := errors.New("state resolver failed")
	r := New(
		underlying,
		WithStateResolver(func(context.Context, *adapter.RunAgentInput) (map[string]any, error) {
			return nil, wantErr
		}),
	)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := r.Run(context.Background(), input)
	assert.Nil(t, eventsCh)
	assert.ErrorContains(t, err, "resolve state")
	assert.ErrorIs(t, err, wantErr)
	assert.Equal(t, 0, underlying.calls)
}

func TestRunTranslateError(t *testing.T) {
	fakeTrans := &fakeTranslator{err: errors.New("bad event")}
	eventsCh := make(chan *agentevent.Event, 1)
	eventsCh <- &agentevent.Event{}
	close(eventsCh)

	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context,
		userID, sessionID string,
		message model.Message,
		_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
		return eventsCh, nil
	}

	r := &runner{
		runner: underlying,
		translatorFactory: func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver:    NewOptions().UserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		startSpan:         defaultStartSpan,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	aguiCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	evts := collectEvents(t, aguiCh)
	assert.Len(t, evts, 2)
	_, ok := evts[1].(*aguievents.RunErrorEvent)
	assert.True(t, ok)
}

func TestRunNormal(t *testing.T) {
	fakeTrans := &fakeTranslator{events: [][]aguievents.Event{
		{aguievents.NewTextMessageStartEvent("msg-1")},
		{aguievents.NewTextMessageEndEvent("msg-1"), aguievents.NewRunFinishedEvent("thread", "run")},
	}}

	underlying := &fakeRunner{}
	underlying.run = func(ctx context.Context,
		userID, sessionID string,
		message model.Message,
		_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
		assert.Equal(t, "user-123", userID)
		assert.Equal(t, "thread", sessionID)
		ch := make(chan *agentevent.Event, 2)
		ch <- &agentevent.Event{}
		ch <- &agentevent.Event{}
		close(ch)
		return ch, nil
	}
	r := &runner{
		runner: underlying,
		translatorFactory: func(_ context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver: func(context.Context, *adapter.RunAgentInput) (string, error) {
			return "user-123", nil
		},
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		startSpan:         defaultStartSpan,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}

	aguiCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	evts := collectEvents(t, aguiCh)
	assert.Len(t, evts, 4)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	assert.IsType(t, (*aguievents.TextMessageStartEvent)(nil), evts[1])
	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), evts[2])
	assert.IsType(t, (*aguievents.RunFinishedEvent)(nil), evts[3])
	assert.Equal(t, 1, underlying.calls)
}

func TestRunAgentInputHook(t *testing.T) {
	t.Run("replace input", func(t *testing.T) {
		underlying := &fakeRunner{}
		underlying.run = func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			assert.Equal(t, "user-123", userID)
			assert.Equal(t, "new-thread", sessionID)
			assert.Equal(t, "new message", message.Content)
			ch := make(chan *agentevent.Event)
			go func() {
				close(ch)
			}()
			return ch, nil
		}
		baseInput := &adapter.RunAgentInput{
			ThreadID: "thread",
			RunID:    "run",
			Messages: []types.Message{{Role: types.RoleUser, Content: "old"}},
		}
		replaced := &adapter.RunAgentInput{
			ThreadID: "new-thread",
			RunID:    "run",
			Messages: []types.Message{{Role: types.RoleUser, Content: "new message"}},
		}
		r := &runner{
			runner: underlying,
			translatorFactory: func(ctx context.Context, _ *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
				return &fakeTranslator{}, nil
			},
			userIDResolver: func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
				assert.Equal(t, replaced, input)
				return "user-123", nil
			},
			stateResolver: defaultStateResolver,
			runAgentInputHook: func(ctx context.Context, input *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
				assert.Equal(t, baseInput, input)
				return replaced, nil
			},
			runOptionResolver: defaultRunOptionResolver,
			startSpan:         defaultStartSpan,
		}

		eventsCh, err := r.Run(context.Background(), baseInput)
		assert.NoError(t, err)
		collectEvents(t, eventsCh)
		assert.Equal(t, 1, underlying.calls)
	})

	t.Run("nil hook result keeps original", func(t *testing.T) {
		underlying := &fakeRunner{}
		underlying.run = func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			assert.Equal(t, "thread", sessionID)
			ch := make(chan *agentevent.Event)
			go func() {
				close(ch)
			}()
			return ch, nil
		}
		originalInput := &adapter.RunAgentInput{
			ThreadID: "thread",
			RunID:    "run",
			Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
		}
		r := &runner{
			runner: underlying,
			translatorFactory: func(ctx context.Context, in *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
				assert.Same(t, originalInput, in)
				return &fakeTranslator{}, nil
			},
			userIDResolver: func(ctx context.Context, in *adapter.RunAgentInput) (string, error) {
				assert.Same(t, originalInput, in)
				return "user", nil
			},
			stateResolver: defaultStateResolver,
			runAgentInputHook: func(ctx context.Context, in *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
				return nil, nil
			},
			runOptionResolver: defaultRunOptionResolver,
			startSpan:         defaultStartSpan,
		}

		ch, err := r.Run(context.Background(), originalInput)
		assert.NoError(t, err)
		collectEvents(t, ch)
		assert.Equal(t, 1, underlying.calls)
	})

	t.Run("hook error bubbles up", func(t *testing.T) {
		wantErr := errors.New("hook fail")
		r := &runner{
			runner: &fakeRunner{},
			runAgentInputHook: func(ctx context.Context, input *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
				return nil, wantErr
			},
			runOptionResolver: defaultRunOptionResolver,
			startSpan:         defaultStartSpan,
		}
		_, err := r.Run(context.Background(), &adapter.RunAgentInput{})
		assert.Error(t, err)
		assert.ErrorContains(t, err, "run input hook")
		assert.ErrorIs(t, err, wantErr)
	})
}

func TestRunnerHandleBeforeWithCallback(t *testing.T) {
	t.Run("without callback", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		r := &runner{}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Same(t, base, got)
	})
	t.Run("with callback", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		replacement := agentevent.New("inv-replacement", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return replacement, nil
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, replacement, got)
	})
	t.Run("return err", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return nil, errors.New("fail")
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
	t.Run("both nil", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return nil, nil
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Same(t, base, got)
	})
	t.Run("multiple callbacks", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		event1 := agentevent.New("inv-1", "assistant")
		event2 := agentevent.New("inv-2", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return event1, nil
				}).
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event1, got)
	})
	t.Run("multiple callbacks return nil", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		event2 := agentevent.New("inv-2", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return nil, nil
				}).
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event2, got)
	})
	t.Run("multiple callbacks return err", func(t *testing.T) {
		base := agentevent.New("inv", "assistant")
		event2 := agentevent.New("inv-2", "assistant")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return nil, errors.New("fail")
				}).
				RegisterBeforeTranslate(func(ctx context.Context, event *agentevent.Event) (*agentevent.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleBeforeTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
}

func TestRunnerHandleAfterWithCallback(t *testing.T) {
	t.Run("without callback", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Same(t, base, got)
	})
	t.Run("with callback", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		replacement := aguievents.NewRunErrorEvent("callback override")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return replacement, nil
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, replacement, got)
	})
	t.Run("return err", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return nil, errors.New("fail")
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
	t.Run("both nil", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return nil, nil
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Same(t, base, got)
	})
	t.Run("multiple callbacks", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		event1 := aguievents.NewRunFinishedEvent("thread", "run")
		event2 := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return event1, nil
				}).
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event1, got)
	})
	t.Run("multiple callbacks return nil", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		event2 := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return nil, nil
				}).
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.NoError(t, err)
		assert.Equal(t, event2, got)
	})
	t.Run("multiple callbacks return err", func(t *testing.T) {
		base := aguievents.NewRunFinishedEvent("thread", "run")
		event2 := aguievents.NewRunFinishedEvent("thread", "run")
		r := &runner{
			translateCallbacks: translator.NewCallbacks().
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return nil, errors.New("fail")
				}).
				RegisterAfterTranslate(func(ctx context.Context, event aguievents.Event) (aguievents.Event, error) {
					return event2, nil
				}),
		}
		got, err := r.handleAfterTranslate(context.Background(), base)
		assert.Error(t, err)
		assert.Nil(t, got)
	})
}

func TestRunnerBeforeTranslateCallbackOverridesInput(t *testing.T) {
	original := agentevent.NewResponseEvent("inv", "assistant",
		&model.Response{
			ID:      "id",
			Object:  model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "original"}}}})
	replacement := agentevent.NewResponseEvent("inv", "assistant",
		&model.Response{
			ID:      "id",
			Object:  model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{Message: model.Message{Role: model.RoleAssistant, Content: "replacement"}}}})

	callbacks := translator.NewCallbacks().
		RegisterBeforeTranslate(func(ctx context.Context, evt *agentevent.Event) (*agentevent.Event, error) {
			return replacement, nil
		})

	underlying := &fakeRunner{
		run: func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event, 1)
			ch <- original
			close(ch)
			return ch, nil
		}}

	r := New(underlying, WithTranslateCallbacks(callbacks))

	input := &adapter.RunAgentInput{ThreadID: "thread", RunID: "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hello"}}}
	ch, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	out := collectEvents(t, ch)

	assert.Len(t, out, 4)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), out[0])
	assert.IsType(t, (*aguievents.TextMessageStartEvent)(nil), out[1])
	assert.IsType(t, (*aguievents.TextMessageContentEvent)(nil), out[2])
	assert.IsType(t, (*aguievents.TextMessageEndEvent)(nil), out[3])

	contentEvent, ok := out[2].(*aguievents.TextMessageContentEvent)
	assert.True(t, ok)
	assert.Equal(t, "replacement", contentEvent.Delta)
}

func TestRunnerAfterTranslateCallbackOverridesEmission(t *testing.T) {
	replacement := aguievents.NewRunErrorEvent("override")
	fakeTrans := &fakeTranslator{events: [][]aguievents.Event{{aguievents.NewRunFinishedEvent("thread", "run")}}}
	callbacks := translator.NewCallbacks().
		RegisterAfterTranslate(func(ctx context.Context, evt aguievents.Event) (aguievents.Event, error) {
			if _, ok := evt.(*aguievents.RunFinishedEvent); ok {
				return replacement, nil
			}
			return nil, nil
		})

	underlying := &fakeRunner{
		run: func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event, 1)
			ch <- agentevent.New("inv", "assistant")
			close(ch)
			return ch, nil
		}}

	r := &runner{
		runner: underlying,
		translatorFactory: func(ctx context.Context, input *adapter.RunAgentInput, _ ...translator.Option) (translator.Translator, error) {
			return fakeTrans, nil
		},
		userIDResolver:     NewOptions().UserIDResolver,
		stateResolver:      defaultStateResolver,
		translateCallbacks: callbacks,
		runOptionResolver:  defaultRunOptionResolver,
		startSpan:          defaultStartSpan,
	}

	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hello"}},
	}
	ch, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	out := collectEvents(t, ch)
	require.Len(t, out, 2)
	assert.IsType(t, (*aguievents.RunErrorEvent)(nil), out[1])
}

type fakeTranslator struct {
	events [][]aguievents.Event
	err    error
	seen   []*agentevent.Event
}

func (f *fakeTranslator) Translate(ctx context.Context, evt *agentevent.Event) ([]aguievents.Event, error) {
	f.seen = append(f.seen, evt)
	if f.err != nil {
		return nil, f.err
	}
	if len(f.events) == 0 {
		return nil, nil
	}
	out := f.events[0]
	f.events = f.events[1:]
	return out, nil
}

type flushRecorder struct {
	appendCount int
	flushCount  int
}

func (f *flushRecorder) AppendEvent(ctx context.Context, key session.Key, event aguievents.Event) error {
	f.appendCount++
	return nil
}

func (f *flushRecorder) GetEvents(ctx context.Context, key session.Key, opts ...session.Option) (*session.TrackEvents, error) {
	return nil, nil
}

func (f *flushRecorder) Flush(ctx context.Context, key session.Key) error {
	f.flushCount++
	return nil
}

type recordingTracker struct {
	mu         sync.Mutex
	events     []aguievents.Event
	keys       []session.Key
	flushCount int
}

func (r *recordingTracker) AppendEvent(ctx context.Context, key session.Key, event aguievents.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.keys = append(r.keys, key)
	r.events = append(r.events, event)
	return nil
}

func (r *recordingTracker) GetEvents(ctx context.Context, key session.Key, opts ...session.Option) (*session.TrackEvents, error) {
	return nil, nil
}

func (r *recordingTracker) Flush(ctx context.Context, key session.Key) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.flushCount++
	return nil
}

type errorTracker struct {
	appendErr error
	flushErr  error
}

func (e *errorTracker) AppendEvent(ctx context.Context,
	_ session.Key, _ aguievents.Event) error {
	return e.appendErr
}

func (e *errorTracker) GetEvents(ctx context.Context,
	_ session.Key, _ ...session.Option) (*session.TrackEvents, error) {
	return nil, nil
}

func (e *errorTracker) Flush(ctx context.Context,
	_ session.Key) error {
	return e.flushErr
}

func forwardedPropsAppNameResolver(_ context.Context, input *adapter.RunAgentInput) (string, error) {
	props, ok := input.ForwardedProps.(map[string]any)
	if !ok || props == nil {
		return "", nil
	}
	appName, _ := props["appName"].(string)
	return appName, nil
}

type spySpan struct {
	trace.Span
	endCalls int
}

func (s *spySpan) End(options ...trace.SpanEndOption) {
	s.endCalls++
	s.Span.End(options...)
}

type fakeRunner struct {
	run func(ctx context.Context,
		userID, sessionID string,
		message model.Message,
		opts ...agent.RunOption) (<-chan *agentevent.Event, error)
	calls int
}

func (f *fakeRunner) Run(ctx context.Context,
	userID, sessionID string,
	message model.Message,
	opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
	f.calls++
	if f.run != nil {
		return f.run(ctx, userID, sessionID, message, opts...)
	}
	return nil, nil
}

func (f *fakeRunner) Close() error {
	return nil
}

func TestRunTrackingErrorsAreIgnored(t *testing.T) {
	appendErr := errors.New("append failed")
	flushErr := errors.New("flush failed")
	underlying := &fakeRunner{
		run: func(ctx context.Context,
			userID, sessionID string,
			message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			ch := make(chan *agentevent.Event)
			close(ch)
			return ch, nil
		},
	}
	r := &runner{
		runner:            underlying,
		translatorFactory: defaultTranslatorFactory,
		userIDResolver:    defaultUserIDResolver,
		stateResolver:     defaultStateResolver,
		runOptionResolver: defaultRunOptionResolver,
		tracker: &errorTracker{
			appendErr: appendErr,
			flushErr:  flushErr,
		},
		startSpan: defaultStartSpan,
	}
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{
			{
				Role:    types.RoleUser,
				Content: "hi",
			},
		},
	}

	eventsCh, err := r.Run(context.Background(), input)
	assert.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	assert.Len(t, evts, 1)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
}

func TestRunUsesCanonicalToolCallIDFromInstalledPlugin(t *testing.T) {
	modelStub := &toolCallIDRunnerIntegrationModel{
		responses: [][]*model.Response{
			{{
				ID:        "rsp-tool",
				Object:    model.ObjectTypeChatCompletion,
				Done:      true,
				IsPartial: false,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call-1",
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      "echo",
								Arguments: []byte(`{"value":"ok"}`),
							},
						}},
					},
				}},
			}},
			{{
				ID:        "rsp-final",
				Object:    model.ObjectTypeChatCompletion,
				Done:      true,
				IsPartial: false,
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage("done"),
				}},
			}},
		},
	}
	echoTool := function.NewFunctionTool(
		func(_ context.Context, input struct {
			Value string `json:"value"`
		}) (string, error) {
			return input.Value, nil
		},
		function.WithName("echo"),
		function.WithDescription("Echoes the input."),
	)
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{echoTool}),
	)
	underlying := baserunner.NewRunner(
		"toolcallid-agui-app",
		ag,
		baserunner.WithPlugins(toolcallidplugin.New()),
	)
	t.Cleanup(func() {
		require.NoError(t, underlying.Close())
	})
	r := New(underlying)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hello"}},
	}
	eventCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	events := collectEvents(t, eventCh)
	var (
		startID  string
		argsID   string
		endID    string
		resultID string
	)
	for _, evt := range events {
		switch v := evt.(type) {
		case *aguievents.ToolCallStartEvent:
			startID = v.ToolCallID
		case *aguievents.ToolCallArgsEvent:
			argsID = v.ToolCallID
		case *aguievents.ToolCallEndEvent:
			endID = v.ToolCallID
		case *aguievents.ToolCallResultEvent:
			resultID = v.ToolCallID
		}
	}
	require.NotEmpty(t, startID)
	require.Equal(t, startID, argsID)
	require.Equal(t, startID, endID)
	require.Equal(t, startID, resultID)
	require.NotEqual(t, "call-1", startID)
	require.Contains(t, startID, "trpc-agent-go-toolcall:")
}

func TestRunGraphToolMetadataUsesCanonicalToolCallID(t *testing.T) {
	const canonicalToolCallID = "trpc-agent-go-toolcall:inv-1:rsp-1:call-1:c0:t0"
	rawToolMeta, err := json.Marshal(graph.ToolExecutionMetadata{
		ResponseID: "assistant-1",
		ToolName:   "echo",
		ToolID:     canonicalToolCallID,
		Phase:      graph.ToolExecutionPhaseStart,
		Input:      `{"value":"ok"}`,
	})
	require.NoError(t, err)
	agentEvents := make(chan *agentevent.Event, 2)
	agentEvents <- &agentevent.Event{
		ID: "tool-start",
		StateDelta: map[string][]byte{
			graph.MetadataKeyTool: rawToolMeta,
		},
	}
	agentEvents <- &agentevent.Event{
		ID: "tool-result",
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{
					Role:     model.RoleTool,
					Content:  "ok",
					ToolID:   canonicalToolCallID,
					ToolName: "echo",
				},
			}},
		},
	}
	close(agentEvents)
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message,
			_ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			return agentEvents, nil
		},
	}
	r := New(underlying)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hello"}},
	}
	eventCh, err := r.Run(context.Background(), input)
	require.NoError(t, err)
	events := collectEvents(t, eventCh)
	var (
		startID  string
		argsID   string
		endID    string
		resultID string
	)
	for _, evt := range events {
		switch v := evt.(type) {
		case *aguievents.ToolCallStartEvent:
			startID = v.ToolCallID
		case *aguievents.ToolCallArgsEvent:
			argsID = v.ToolCallID
		case *aguievents.ToolCallEndEvent:
			endID = v.ToolCallID
		case *aguievents.ToolCallResultEvent:
			resultID = v.ToolCallID
		}
	}
	require.Equal(t, canonicalToolCallID, startID)
	require.Equal(t, canonicalToolCallID, argsID)
	require.Equal(t, canonicalToolCallID, endID)
	require.Equal(t, canonicalToolCallID, resultID)
}

func TestRunStreamingToolResultActivityEnabledTracksOnlyFinalToolResult(t *testing.T) {
	sessionService := inmemory.NewSessionService()
	agentEvents := make(chan *agentevent.Event, 5)
	agentEvents <- &agentevent.Event{
		Response: &model.Response{
			ID:     "msg-tool-call",
			Object: model.ObjectTypeChatCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					Role: model.RoleAssistant,
					ToolCalls: []model.ToolCall{{
						ID:   "call-1",
						Type: "function",
						Function: model.FunctionDefinitionParam{
							Name:      "streamer",
							Arguments: []byte(`{"count":2}`),
						},
					}},
				},
			}},
		},
	}
	agentEvents <- &agentevent.Event{
		ID: "tool-partial-1",
		Response: &model.Response{
			Object:    model.ObjectTypeToolResponse,
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{Role: model.RoleTool, ToolID: "call-1", Content: "Hello"},
			}},
		},
	}
	agentEvents <- &agentevent.Event{
		ID: "tool-partial-2",
		Response: &model.Response{
			Object:    model.ObjectTypeToolResponse,
			IsPartial: true,
			Choices: []model.Choice{{
				Delta: model.Message{Role: model.RoleTool, ToolID: "call-1", Content: " World"},
			}},
		},
	}
	agentEvents <- &agentevent.Event{
		ID: "tool-final",
		Response: &model.Response{
			Object: model.ObjectTypeToolResponse,
			Choices: []model.Choice{{
				Message: model.Message{Role: model.RoleTool, ToolID: "call-1", Content: `{"final":"done"}`},
			}},
		},
	}
	agentEvents <- &agentevent.Event{
		Response: &model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Done:   true,
		},
	}
	close(agentEvents)
	underlying := &fakeRunner{
		run: func(ctx context.Context, userID, sessionID string, message model.Message, _ ...agent.RunOption) (<-chan *agentevent.Event, error) {
			return agentEvents, nil
		},
	}
	rr, ok := New(
		underlying,
		WithAppName("demo"),
		WithSessionService(sessionService),
		WithStreamingToolResultActivityEnabled(true),
	).(*runner)
	require.True(t, ok)
	input := &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hi"}},
	}
	eventsCh, err := rr.Run(context.Background(), input)
	require.NoError(t, err)
	evts := collectEvents(t, eventsCh)
	require.Len(t, evts, 8)
	assert.IsType(t, (*aguievents.RunStartedEvent)(nil), evts[0])
	assert.IsType(t, (*aguievents.ToolCallStartEvent)(nil), evts[1])
	assert.IsType(t, (*aguievents.ToolCallArgsEvent)(nil), evts[2])
	assert.IsType(t, (*aguievents.ToolCallEndEvent)(nil), evts[3])
	assert.IsType(t, (*aguievents.ActivitySnapshotEvent)(nil), evts[4])
	assert.IsType(t, (*aguievents.ActivityDeltaEvent)(nil), evts[5])
	assert.IsType(t, (*aguievents.ToolCallResultEvent)(nil), evts[6])
	assert.IsType(t, (*aguievents.RunFinishedEvent)(nil), evts[7])
	result, ok := evts[6].(*aguievents.ToolCallResultEvent)
	require.True(t, ok)
	assert.Equal(t, "call-1", result.ToolCallID)
	assert.Equal(t, `{"final":"done"}`, result.Content)
	trackEvents, err := rr.tracker.GetEvents(context.Background(), session.Key{
		AppName:   "demo",
		UserID:    "user",
		SessionID: "thread",
	})
	require.NoError(t, err)
	require.NotNil(t, trackEvents)
	for _, trackEvent := range trackEvents.Events {
		evt, decodeErr := aguievents.EventFromJSON(trackEvent.Payload)
		require.NoError(t, decodeErr)
		assert.False(t, aguitool.IsStreamingToolResultActivityEvent(evt))
	}
	snapshotStream, err := rr.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "snapshot",
	})
	require.NoError(t, err)
	snapshotEvents := collectEvents(t, snapshotStream)
	require.Len(t, snapshotEvents, 3)
	snapshot, ok := snapshotEvents[1].(*aguievents.MessagesSnapshotEvent)
	require.True(t, ok)
	require.Len(t, snapshot.Messages, 3)
	assert.Equal(t, types.RoleUser, snapshot.Messages[0].Role)
	assert.Equal(t, types.RoleAssistant, snapshot.Messages[1].Role)
	assert.Equal(t, types.RoleTool, snapshot.Messages[2].Role)
	assert.Equal(t, "call-1", snapshot.Messages[2].ToolCallID)
	for _, msg := range snapshot.Messages {
		assert.NotEqual(t, types.RoleActivity, msg.Role)
	}
}

func TestRunStreamingToolResultActivityEnabledUsesMergedFinalToolResult(t *testing.T) {
	outcome := runStreamingToolResultIntegrationScenario(t, []any{"Hello", " World"})
	assertStreamingToolResultActivityOutcome(t, outcome, "Hello", "Hello World", `"Hello World"`)
}

func TestRunStreamingToolResultActivityEnabledUsesExplicitFinalResultChunk(t *testing.T) {
	outcome := runStreamingToolResultIntegrationScenario(t, []any{
		"line-1",
		" line-2",
		tool.FinalResultChunk{Result: map[string]any{"final": "done"}},
	})
	assertStreamingToolResultActivityOutcome(t, outcome, "line-1", "line-1 line-2", `{"final":"done"}`)
}

type toolCallIDRunnerIntegrationModel struct {
	mu        sync.Mutex
	responses [][]*model.Response
	callIndex int
}

func (m *toolCallIDRunnerIntegrationModel) Info() model.Info {
	return model.Info{Name: "toolcallid-agui-model"}
}

func (m *toolCallIDRunnerIntegrationModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.mu.Lock()
	callIndex := m.callIndex
	m.callIndex++
	var responses []*model.Response
	if callIndex < len(m.responses) {
		responses = m.responses[callIndex]
	}
	m.mu.Unlock()
	ch := make(chan *model.Response, len(responses))
	for _, rsp := range responses {
		ch <- cloneToolCallIDRunnerIntegrationResponse(rsp)
	}
	close(ch)
	return ch, nil
}

func cloneToolCallIDRunnerIntegrationResponse(rsp *model.Response) *model.Response {
	if rsp == nil {
		return nil
	}
	cloned := rsp.Clone()
	choices := make([]model.Choice, len(rsp.Choices))
	for i, choice := range rsp.Choices {
		choices[i] = choice
		choices[i].Message = cloneToolCallIDRunnerIntegrationMessage(choice.Message)
		choices[i].Delta = cloneToolCallIDRunnerIntegrationMessage(choice.Delta)
	}
	cloned.Choices = choices
	return cloned
}

func cloneToolCallIDRunnerIntegrationMessage(message model.Message) model.Message {
	cloned := message
	if len(message.ToolCalls) > 0 {
		cloned.ToolCalls = append([]model.ToolCall(nil), message.ToolCalls...)
	}
	if len(message.ContentParts) > 0 {
		cloned.ContentParts = append([]model.ContentPart(nil), message.ContentParts...)
	}
	return cloned
}

type streamingToolResultIntegrationOutcome struct {
	runner     *runner
	events     []aguievents.Event
	toolCallID string
}

func runStreamingToolResultIntegrationScenario(
	t *testing.T,
	chunks []any,
) streamingToolResultIntegrationOutcome {
	t.Helper()
	modelStub := &toolCallIDRunnerIntegrationModel{
		responses: [][]*model.Response{
			{{
				ID:        "rsp-tool",
				Object:    model.ObjectTypeChatCompletion,
				Done:      true,
				IsPartial: false,
				Choices: []model.Choice{{
					Index: 0,
					Message: model.Message{
						Role: model.RoleAssistant,
						ToolCalls: []model.ToolCall{{
							ID:   "call-1",
							Type: "function",
							Function: model.FunctionDefinitionParam{
								Name:      "streamer",
								Arguments: []byte(`{}`),
							},
						}},
					},
				}},
			}},
			{{
				ID:        "rsp-final",
				Object:    model.ObjectTypeChatCompletion,
				Done:      true,
				IsPartial: false,
				Choices: []model.Choice{{
					Index:   0,
					Message: model.NewAssistantMessage("done"),
				}},
			}},
		},
	}
	streamTool := function.NewStreamableFunctionTool[struct{}, string](
		func(_ context.Context, _ struct{}) (*tool.StreamReader, error) {
			stream := tool.NewStream(len(chunks))
			go func() {
				defer stream.Writer.Close()
				for _, chunk := range chunks {
					if stream.Writer.Send(tool.StreamChunk{Content: chunk}, nil) {
						return
					}
				}
			}()
			return stream.Reader, nil
		},
		function.WithName("streamer"),
		function.WithDescription("Streams tool output."),
	)
	ag := llmagent.New(
		"assistant",
		llmagent.WithModel(modelStub),
		llmagent.WithTools([]tool.Tool{streamTool}),
	)
	underlying := baserunner.NewRunner("streaming-tool-result-agui-app", ag)
	t.Cleanup(func() {
		require.NoError(t, underlying.Close())
	})
	rr, ok := New(
		underlying,
		WithAppName("demo"),
		WithSessionService(inmemory.NewSessionService()),
		WithStreamingToolResultActivityEnabled(true),
	).(*runner)
	require.True(t, ok)
	eventsCh, err := rr.Run(context.Background(), &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "run",
		Messages: []types.Message{{Role: types.RoleUser, Content: "hello"}},
	})
	require.NoError(t, err)
	events := collectEvents(t, eventsCh)
	var toolCallID string
	for _, evt := range events {
		start, ok := evt.(*aguievents.ToolCallStartEvent)
		if ok {
			toolCallID = start.ToolCallID
			break
		}
	}
	require.NotEmpty(t, toolCallID)
	return streamingToolResultIntegrationOutcome{
		runner:     rr,
		events:     events,
		toolCallID: toolCallID,
	}
}

func assertStreamingToolResultActivityOutcome(
	t *testing.T,
	outcome streamingToolResultIntegrationOutcome,
	wantSnapshotContent string,
	wantMergedActivityContent string,
	wantFinalToolResultContent string,
) {
	t.Helper()
	var (
		toolCallStartIndex int = -1
		toolCallArgsIndex  int = -1
		toolCallEndIndex   int = -1
		snapshotIndex      int = -1
		deltaIndex         int = -1
		resultIndex        int = -1
		snapshotEvent      *aguievents.ActivitySnapshotEvent
		deltaEvent         *aguievents.ActivityDeltaEvent
		resultEvents       []*aguievents.ToolCallResultEvent
	)
	for i, evt := range outcome.events {
		switch e := evt.(type) {
		case *aguievents.ToolCallStartEvent:
			toolCallStartIndex = i
			assert.Equal(t, outcome.toolCallID, e.ToolCallID)
		case *aguievents.ToolCallArgsEvent:
			toolCallArgsIndex = i
			assert.Equal(t, outcome.toolCallID, e.ToolCallID)
		case *aguievents.ToolCallEndEvent:
			toolCallEndIndex = i
			assert.Equal(t, outcome.toolCallID, e.ToolCallID)
		case *aguievents.ActivitySnapshotEvent:
			snapshotIndex = i
			snapshotEvent = e
		case *aguievents.ActivityDeltaEvent:
			deltaIndex = i
			deltaEvent = e
		case *aguievents.ToolCallResultEvent:
			resultIndex = i
			resultEvents = append(resultEvents, e)
		}
	}
	require.Len(t, resultEvents, 1)
	require.NotNil(t, snapshotEvent)
	require.NotNil(t, deltaEvent)
	assert.Greater(t, toolCallStartIndex, -1)
	assert.Greater(t, toolCallArgsIndex, toolCallStartIndex)
	assert.Greater(t, toolCallEndIndex, toolCallArgsIndex)
	assert.Greater(t, snapshotIndex, toolCallEndIndex)
	assert.Greater(t, deltaIndex, snapshotIndex)
	assert.Greater(t, resultIndex, deltaIndex)
	assert.Equal(t, aguitool.StreamingToolResultActivityMessageID(outcome.toolCallID), snapshotEvent.MessageID)
	assert.Equal(t, aguitool.StreamingToolResultActivityMessageID(outcome.toolCallID), deltaEvent.MessageID)
	assert.Equal(t, aguitool.StreamingToolResultActivityType, snapshotEvent.ActivityType)
	assert.Equal(t, aguitool.StreamingToolResultActivityType, deltaEvent.ActivityType)
	snapshotContent, ok := snapshotEvent.Content.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, outcome.toolCallID, snapshotContent["toolCallId"])
	assert.Equal(t, wantSnapshotContent, snapshotContent["content"])
	require.Len(t, deltaEvent.Patch, 1)
	assert.Equal(t, "add", deltaEvent.Patch[0].Op)
	assert.Equal(t, "/content", deltaEvent.Patch[0].Path)
	assert.Equal(t, wantMergedActivityContent, deltaEvent.Patch[0].Value)
	assert.Equal(t, outcome.toolCallID, resultEvents[0].ToolCallID)
	assert.Equal(t, wantFinalToolResultContent, resultEvents[0].Content)
	require.NotNil(t, outcome.runner.tracker)
	trackEvents, err := outcome.runner.tracker.GetEvents(context.Background(), session.Key{
		AppName:   "demo",
		UserID:    "user",
		SessionID: "thread",
	})
	require.NoError(t, err)
	require.NotNil(t, trackEvents)
	toolResultCount := 0
	for _, trackEvent := range trackEvents.Events {
		evt, decodeErr := aguievents.EventFromJSON(trackEvent.Payload)
		require.NoError(t, decodeErr)
		assert.False(t, aguitool.IsStreamingToolResultActivityEvent(evt))
		resultEvent, ok := evt.(*aguievents.ToolCallResultEvent)
		if !ok || resultEvent.ToolCallID != outcome.toolCallID {
			continue
		}
		toolResultCount++
		assert.Equal(t, wantFinalToolResultContent, resultEvent.Content)
	}
	assert.Equal(t, 1, toolResultCount)
	snapshotMessages := loadSnapshotMessages(t, outcome.runner)
	toolMessageCount := 0
	for _, msg := range snapshotMessages {
		assert.NotEqual(t, types.RoleActivity, msg.Role)
		if msg.Role != types.RoleTool || msg.ToolCallID != outcome.toolCallID {
			continue
		}
		toolMessageCount++
		assert.Equal(t, wantFinalToolResultContent, toolSnapshotContentString(t, msg))
	}
	assert.Equal(t, 1, toolMessageCount)
}

func loadSnapshotMessages(t *testing.T, rr *runner) []types.Message {
	t.Helper()
	snapshotStream, err := rr.MessagesSnapshot(context.Background(), &adapter.RunAgentInput{
		ThreadID: "thread",
		RunID:    "snapshot",
	})
	require.NoError(t, err)
	snapshotEvents := collectEvents(t, snapshotStream)
	require.Len(t, snapshotEvents, 3)
	snapshot, ok := snapshotEvents[1].(*aguievents.MessagesSnapshotEvent)
	require.True(t, ok)
	return snapshot.Messages
}

func toolSnapshotContentString(t *testing.T, msg types.Message) string {
	t.Helper()
	switch content := msg.Content.(type) {
	case string:
		return content
	case *string:
		require.NotNil(t, content)
		return *content
	default:
		require.FailNowf(t, "unexpected tool snapshot content type", "%T", msg.Content)
		return ""
	}
}

func collectEvents(t *testing.T, ch <-chan aguievents.Event) []aguievents.Event {
	t.Helper()
	var out []aguievents.Event
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, evt)
		case <-time.After(time.Second):
			assert.FailNow(t, "timeout collecting events")
			return out
		}
	}
}

func hasRunFinishedEvent(events []aguievents.Event) bool {
	for _, evt := range events {
		if _, ok := evt.(*aguievents.RunFinishedEvent); ok {
			return true
		}
	}
	return false
}

func hasRunErrorEvent(events []aguievents.Event) bool {
	for _, evt := range events {
		if _, ok := evt.(*aguievents.RunErrorEvent); ok {
			return true
		}
	}
	return false
}

func TestTranslateCallbackError(t *testing.T) {
	t.Run("before translate callback error", func(t *testing.T) {
		callbacks := translator.NewCallbacks().
			RegisterBeforeTranslate(func(ctx context.Context, evt *agentevent.Event) (*agentevent.Event, error) {
				return nil, errors.New("fail")
			})
		r := &runner{
			runner: &fakeRunner{
				run: func(ctx context.Context, userID, sessionID string, message model.Message,
					opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
					ch := make(chan *agentevent.Event, 1)
					ch <- agentevent.New("inv", "assistant")
					close(ch)
					return ch, nil
				},
			},
			translateCallbacks: callbacks,
			translatorFactory:  defaultTranslatorFactory,
			userIDResolver:     defaultUserIDResolver,
			stateResolver:      defaultStateResolver,
			runOptionResolver:  defaultRunOptionResolver,
			startSpan:          defaultStartSpan,
		}
		input := &adapter.RunAgentInput{
			ThreadID: "thread",
			RunID:    "run",
			Messages: []types.Message{{Role: types.RoleUser, Content: "hello"}},
		}
		ch, err := r.Run(context.Background(), input)
		assert.NoError(t, err)
		evts := collectEvents(t, ch)
		assert.Len(t, evts, 2)
		_, ok := evts[1].(*aguievents.RunErrorEvent)
		assert.True(t, ok)
	})
	t.Run("after translate callback error", func(t *testing.T) {
		callbacks := translator.NewCallbacks().
			RegisterAfterTranslate(func(ctx context.Context, evt aguievents.Event) (aguievents.Event, error) {
				return nil, errors.New("fail")
			})
		r := &runner{
			runner: &fakeRunner{
				run: func(ctx context.Context, userID, sessionID string, message model.Message,
					opts ...agent.RunOption) (<-chan *agentevent.Event, error) {
					ch := make(chan *agentevent.Event, 1)
					ch <- agentevent.New("inv", "assistant")
					close(ch)
					return ch, nil
				},
			},
			translateCallbacks: callbacks,
			translatorFactory:  defaultTranslatorFactory,
			userIDResolver:     defaultUserIDResolver,
			stateResolver:      defaultStateResolver,
			runOptionResolver:  defaultRunOptionResolver,
			startSpan:          defaultStartSpan,
		}
		input := &adapter.RunAgentInput{
			ThreadID: "thread",
			RunID:    "run",
			Messages: []types.Message{{Role: types.RoleUser, Content: "hello"}},
		}
		ch, err := r.Run(context.Background(), input)
		assert.NoError(t, err)
		evts := collectEvents(t, ch)
		assert.Len(t, evts, 1)
		_, ok := evts[0].(*aguievents.RunErrorEvent)
		assert.True(t, ok)
	})
}

func TestEmitEventStopsWhenContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &runner{}
	events := make(chan aguievents.Event)
	input := &runInput{threadID: "thread", runID: "run"}
	ok := r.emitEvent(ctx, events, aguievents.NewRunStartedEvent("thread", "run"), input)
	assert.False(t, ok)
}

func TestEmitEventStopsWhenAfterTranslateFailsAndContextDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &runner{
		translateCallbacks: translator.NewCallbacks().RegisterAfterTranslate(
			func(context.Context, aguievents.Event) (aguievents.Event, error) {
				return nil, errors.New("after translate fail")
			},
		),
	}
	events := make(chan aguievents.Event)
	input := &runInput{threadID: "thread", runID: "run"}
	ok := r.emitEvent(ctx, events, aguievents.NewRunStartedEvent("thread", "run"), input)
	assert.False(t, ok)
}

func TestEmitEventLogsAtTraceLevel(t *testing.T) {
	originalTracefContext := log.TracefContext
	originalDebugfContext := log.DebugfContext
	traceCalls := 0
	debugCalls := 0
	log.TracefContext = func(_ context.Context, format string, args ...any) {
		traceCalls++
		assert.Equal(t, "agui emit event: emitted event: %v, threadID: %s, runID: %s", format)
		require.Len(t, args, 3)
		assert.Equal(t, "thread", args[1])
		assert.Equal(t, "run", args[2])
	}
	log.DebugfContext = func(_ context.Context, _ string, _ ...any) {
		debugCalls++
	}
	t.Cleanup(func() {
		log.TracefContext = originalTracefContext
		log.DebugfContext = originalDebugfContext
	})
	r := &runner{}
	events := make(chan aguievents.Event, 1)
	input := &runInput{threadID: "thread", runID: "run"}
	ok := r.emitEvent(context.Background(), events, aguievents.NewRunStartedEvent("thread", "run"), input)
	assert.True(t, ok)
	assert.Equal(t, 1, traceCalls)
	assert.Zero(t, debugCalls)
}

func TestHandleAgentEventStopsWhenEmitCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := &runner{}
	events := make(chan aguievents.Event)
	input := &runInput{
		threadID:   "thread",
		runID:      "run",
		translator: &fakeTranslator{events: [][]aguievents.Event{{aguievents.NewRunFinishedEvent("thread", "run")}}},
	}
	ok := r.handleAgentEvent(ctx, events, input, agentevent.New("inv", "assistant"))
	assert.False(t, ok)
}
