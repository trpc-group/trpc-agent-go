//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/chainagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/cycleagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/parallelagent"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestRunnerCompletion_ExecutionTraceDisabledByDefault(t *testing.T) {
	r := NewRunner("app", &noOpAgent{name: "assistant"}, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(context.Background(), "user-1", "session-1", model.NewUserMessage("hello"))
	require.NoError(t, err)
	var completion *event.Event
	for evt := range eventCh {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
	assert.Nil(t, completion.ExecutionTrace)
}

func TestRunnerCompletion_AttachesExecutionTraceWhenEnabled(t *testing.T) {
	r := NewRunner("app", &noOpAgent{name: "assistant"}, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	var completion *event.Event
	for evt := range eventCh {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.ExecutionTrace)
	assert.Equal(t, "assistant", completion.ExecutionTrace.RootAgentName)
	assert.Equal(t, completion.InvocationID, completion.ExecutionTrace.RootInvocationID)
	assert.Equal(t, "session-1", completion.ExecutionTrace.SessionID)
	assert.Equal(t, atrace.TraceStatusCompleted, completion.ExecutionTrace.Status)
}

func TestResolveExecutionTraceStatus_TreatsStopAgentAsCompleted(t *testing.T) {
	status := resolveExecutionTraceStatus(&eventLoopContext{
		finalError: &model.ResponseError{Type: agent.ErrorTypeStopAgentError},
	}, nil)
	assert.Equal(t, atrace.TraceStatusCompleted, status)
}

func TestRunnerCompletion_DoesNotPersistExecutionTraceIntoSessionEvents(t *testing.T) {
	sessionSvc := sessioninmemory.NewSessionService()
	r := NewRunner("app", &noOpAgent{name: "assistant"}, WithSessionService(sessionSvc))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.ExecutionTrace)
	sess, err := sessionSvc.GetSession(context.Background(), session.Key{AppName: "app", UserID: "user-1", SessionID: "session-1"})
	require.NoError(t, err)
	require.NotNil(t, sess)
	events := sess.GetEvents()
	require.NotEmpty(t, events)
	for _, evt := range events {
		assert.Nil(t, evt.ExecutionTrace)
	}
}

func TestRunnerCompletion_LLMRunProducesOneRealExecutionStep(t *testing.T) {
	ag := llmagent.New("assistant", llmagent.WithModel(&staticModel{name: "trace-model", content: "done"}))
	r := NewRunner("app", ag, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello trace"),
		agent.WithExecutionTraceEnabled(true),
		agent.WithUserMessageRewriter(func(
			context.Context,
			*agent.UserMessageRewriteArgs,
		) ([]model.Message, error) {
			return []model.Message{model.NewUserMessage("rewritten trace")}, nil
		}),
	)
	require.NoError(t, err)
	var completion *event.Event
	for evt := range eventCh {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.ExecutionTrace)
	assert.Equal(
		t,
		model.NewUserMessage("hello trace"),
		executionTraceSnapshotMessage(t, completion.ExecutionTrace.Input),
	)
	assert.Equal(
		t,
		model.NewAssistantMessage("done"),
		executionTraceSnapshotMessage(t, completion.ExecutionTrace.Output),
	)
	require.Len(t, completion.ExecutionTrace.Steps, 1)
	step := completion.ExecutionTrace.Steps[0]
	assert.Equal(t, completion.InvocationID, step.InvocationID)
	assert.Equal(t, "assistant", step.AgentName)
	assert.Equal(t, "assistant", step.NodeID)
	assert.Equal(t, "llm", step.NodeType)
	assert.Empty(t, step.PredecessorStepIDs)
	require.NotNil(t, step.Input)
	require.NotNil(t, step.Output)
	assert.Contains(t, step.Input.Text, "rewritten trace")
	assert.Contains(t, step.Output.Text, "done")
	assert.Empty(t, step.Error)
}

func TestExecutionTraceOutputSnapshot_UsesOnlyCompletedRootOutput(t *testing.T) {
	loop := &eventLoopContext{
		graphCompletionSeen: true,
		finalChoices: []model.Choice{{
			Message: model.NewAssistantMessage("graph final"),
		}},
		fallbackChoices: []model.Choice{{
			Message: model.NewAssistantMessage("intermediate"),
		}},
	}
	completed := executionTraceOutputSnapshot(
		loop,
		atrace.TraceStatusCompleted,
		nil,
		false,
	)
	assert.Equal(
		t,
		model.NewAssistantMessage("graph final"),
		executionTraceSnapshotMessage(t, completed),
	)
	assert.Nil(t, executionTraceOutputSnapshot(
		loop,
		atrace.TraceStatusFailed,
		nil,
		false,
	))
	assert.Nil(t, executionTraceOutputSnapshot(
		loop,
		atrace.TraceStatusIncomplete,
		nil,
		false,
	))
}

func TestExecutionTraceOutputSnapshot_GraphWithoutFinalAnswerOmitsIntermediate(t *testing.T) {
	loop := &eventLoopContext{
		graphCompletionSeen: true,
		fallbackChoices: []model.Choice{{
			Message: model.NewAssistantMessage("intermediate"),
		}},
	}
	assert.Nil(t, executionTraceOutputSnapshot(
		loop,
		atrace.TraceStatusCompleted,
		map[string][]byte{"other": []byte(`"state"`)},
		false,
	))
}

func TestCaptureCompletionFallback_CompleteResponseInvalidatesGraphResult(t *testing.T) {
	loop := &eventLoopContext{
		graphCompletionSeen: true,
		finalStateDelta: map[string][]byte{
			graph.StateKeyLastResponse: []byte(`"graph final"`),
		},
		finalChoices: []model.Choice{{
			Message: model.NewAssistantMessage("graph final"),
		}},
	}
	r := &runner{}
	r.captureCompletionFallback(loop, event.NewResponseEvent(
		"inv-1",
		"root",
		&model.Response{Done: true},
	))
	assert.True(t, loop.graphCompletionSeen)
	assert.Nil(t, loop.finalStateDelta)
	assert.Nil(t, loop.finalChoices)

	r.captureCompletionFallback(loop, event.NewResponseEvent(
		"inv-1",
		"root",
		&model.Response{
			Done:      true,
			IsPartial: true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("partial"),
			}},
		},
	))
	assert.True(t, loop.graphCompletionSeen)

	r.captureCompletionFallback(loop, event.NewResponseEvent(
		"inv-1",
		"root",
		&model.Response{
			Done: true,
			Choices: []model.Choice{{
				Message: model.NewAssistantMessage("root final"),
			}},
		},
	))
	assert.False(t, loop.graphCompletionSeen)
	assert.Nil(t, loop.finalStateDelta)
	assert.Nil(t, loop.finalChoices)
	snapshot := executionTraceOutputSnapshot(
		loop,
		atrace.TraceStatusCompleted,
		nil,
		false,
	)
	assert.Equal(
		t,
		model.NewAssistantMessage("root final"),
		executionTraceSnapshotMessage(t, snapshot),
	)
}

func TestExecutionTraceOutputSnapshot_ResumeSnapshotOnlyUsesAuthoritativeChoices(t *testing.T) {
	loop := &eventLoopContext{
		invocation: agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.NewRunOptions(
				agent.WithRuntimeState(graph.State{
					graph.StateKeyCommand: graph.NewResumeCommand().WithResume("approve"),
				}),
			)),
		),
		graphCompletionSeen:     true,
		baselineFinalResponseID: "resp-1",
		priorAssistantResponseIDs: map[string]struct{}{
			"resp-1": {},
		},
		finalChoices: []model.Choice{{
			Message: model.NewAssistantMessage("historical"),
		}},
	}
	finalStateDelta := map[string][]byte{
		graph.StateKeyLastResponseID: []byte(`"resp-1"`),
	}
	traceSnapshotOnly := graph.CompletionSnapshotOnlyFromStateDelta(finalStateDelta) ||
		shouldMarkCompletionSnapshotOnly(loop, loop.finalChoices, finalStateDelta)
	require.True(t, traceSnapshotOnly)
	assert.Nil(t, executionTraceOutputSnapshot(
		loop,
		atrace.TraceStatusCompleted,
		finalStateDelta,
		traceSnapshotOnly,
	))

	metadataStateDelta := map[string][]byte{}
	graph.SetCompletionSnapshotOnlyInStateDelta(metadataStateDelta, true)
	assert.Nil(t, executionTraceOutputSnapshot(
		loop,
		atrace.TraceStatusCompleted,
		metadataStateDelta,
		graph.CompletionSnapshotOnlyFromStateDelta(metadataStateDelta),
	))
}

func TestCaptureGraphCompletionClonesChoices(t *testing.T) {
	text := "original"
	contentRef := &model.ContentRef{ArtifactRef: "artifact://trace@1"}
	evt := event.NewResponseEvent("inv-1", "graph", &model.Response{
		Choices: []model.Choice{{Message: model.Message{
			Role: model.RoleAssistant,
			ContentParts: []model.ContentPart{{
				Type:       model.ContentTypeText,
				Text:       &text,
				ContentRef: contentRef,
			}},
		}}},
	})
	_, choices := (&runner{}).captureGraphCompletion(evt)
	require.Len(t, choices, 1)
	changed := "changed"
	evt.Response.Choices[0].Message.ContentParts[0].Text = &changed
	evt.Response.Choices[0].Message.ContentParts[0].ContentRef.ArtifactRef = "artifact://changed@2"
	assert.Equal(t, "original", *choices[0].Message.ContentParts[0].Text)
	assert.Equal(t, "artifact://trace@1", choices[0].Message.ContentParts[0].ContentRef.ArtifactRef)
}

func TestCaptureCompletionFallbackAcceptsContentParts(t *testing.T) {
	text := "rich output"
	evt := event.NewResponseEvent("inv-1", "assistant", &model.Response{
		Done: true,
		Choices: []model.Choice{{Message: model.Message{
			Role: model.RoleAssistant,
			ContentParts: []model.ContentPart{{
				Type: model.ContentTypeText,
				Text: &text,
			}},
		}}},
	})
	loop := &eventLoopContext{}
	(&runner{}).captureCompletionFallback(loop, evt)
	require.Len(t, loop.fallbackChoices, 1)
	snapshot := executionTraceOutputSnapshot(
		loop,
		atrace.TraceStatusCompleted,
		nil,
		false,
	)
	assert.Equal(t, evt.Response.Choices[0].Message, executionTraceSnapshotMessage(t, snapshot))
}

func executionTraceSnapshotMessage(
	t *testing.T,
	snapshot *atrace.Snapshot,
) model.Message {
	t.Helper()
	require.NotNil(t, snapshot)
	var message model.Message
	require.NoError(t, json.Unmarshal([]byte(snapshot.Text), &message))
	return message
}

func TestRunnerCompletion_ExecutionTraceCarriesUsage(t *testing.T) {
	usage := &model.Usage{
		PromptTokens:     11,
		CompletionTokens: 7,
		TotalTokens:      18,
		PromptTokensDetails: model.PromptTokensDetails{
			CachedTokens: 3,
		},
		CompletionTokensDetails: model.CompletionTokensDetails{
			ReasoningTokens: 2,
		},
	}
	ag := llmagent.New("assistant", llmagent.WithModel(&staticModel{
		name:    "trace-model",
		content: "done",
		usage:   usage,
	}))
	r := NewRunner("app", ag, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello trace"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.ExecutionTrace)
	require.NotNil(t, completion.ExecutionTrace.Usage)
	assert.Equal(t, 18, completion.ExecutionTrace.Usage.TotalTokens)
	assert.Equal(t, 3, completion.ExecutionTrace.Usage.PromptTokensDetails.CachedTokens)
	require.Len(t, completion.ExecutionTrace.Steps, 1)
	require.NotNil(t, completion.ExecutionTrace.Steps[0].Usage)
	assert.Equal(t, 18, completion.ExecutionTrace.Steps[0].Usage.TotalTokens)
	assert.Equal(t, 2, completion.ExecutionTrace.Steps[0].Usage.CompletionTokensDetails.ReasoningTokens)
}

func TestRunnerCompletion_ExecutionTraceOmitsTimingOnlyUsage(t *testing.T) {
	ag := llmagent.New("assistant", llmagent.WithModel(&staticModel{
		name:    "trace-model",
		content: "done",
		usage:   &model.Usage{TimingInfo: &model.TimingInfo{}},
	}))
	r := NewRunner("app", ag, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello trace"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.ExecutionTrace)
	assert.Nil(t, completion.ExecutionTrace.Usage)
	require.Len(t, completion.ExecutionTrace.Steps, 1)
	assert.Nil(t, completion.ExecutionTrace.Steps[0].Usage)
}

func TestRunnerCompletion_ChainAndParallelPropagatePredecessorsToRealChildSteps(t *testing.T) {
	fanout := parallelagent.New("fanout", parallelagent.WithSubAgents([]agent.Agent{
		llmagent.New("worker-a", llmagent.WithModel(&staticModel{name: "worker-a-model", content: "worker-a"})),
		llmagent.New("worker-b", llmagent.WithModel(&staticModel{name: "worker-b-model", content: "worker-b"})),
	}))
	workflow := chainagent.New("workflow", chainagent.WithSubAgents([]agent.Agent{
		llmagent.New("start", llmagent.WithModel(&staticModel{name: "start-model", content: "start"})),
		fanout,
	}))
	r := NewRunner("app", workflow, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello fanout"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	var completion *event.Event
	for evt := range eventCh {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.ExecutionTrace)
	require.Len(t, completion.ExecutionTrace.Steps, 3)
	stepsByNodeID := map[string]atrace.Step{}
	for _, step := range completion.ExecutionTrace.Steps {
		stepsByNodeID[step.NodeID] = step
	}
	startStep, ok := stepsByNodeID["workflow/start"]
	require.True(t, ok)
	workerAStep, ok := stepsByNodeID["workflow/fanout/worker-a"]
	require.True(t, ok)
	workerBStep, ok := stepsByNodeID["workflow/fanout/worker-b"]
	require.True(t, ok)
	assert.Empty(t, startStep.PredecessorStepIDs)
	assert.Equal(t, []string{startStep.StepID}, workerAStep.PredecessorStepIDs)
	assert.Equal(t, []string{startStep.StepID}, workerBStep.PredecessorStepIDs)
}

func TestRunnerCompletion_ChainAfterParallelUsesParallelTerminalsAsPredecessors(t *testing.T) {
	fanout := parallelagent.New("fanout", parallelagent.WithSubAgents([]agent.Agent{
		llmagent.New("worker-a", llmagent.WithModel(&staticModel{name: "worker-a-model", content: "worker-a"})),
		llmagent.New("worker-b", llmagent.WithModel(&staticModel{name: "worker-b-model", content: "worker-b"})),
	}))
	workflow := chainagent.New("workflow", chainagent.WithSubAgents([]agent.Agent{
		llmagent.New("start", llmagent.WithModel(&staticModel{name: "start-model", content: "start"})),
		fanout,
		llmagent.New("end", llmagent.WithModel(&staticModel{name: "end-model", content: "end"})),
	}))
	r := NewRunner("app", workflow, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello fanout"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	var completion *event.Event
	for evt := range eventCh {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.ExecutionTrace)
	require.Len(t, completion.ExecutionTrace.Steps, 4)
	stepsByNodeID := map[string]atrace.Step{}
	for _, step := range completion.ExecutionTrace.Steps {
		stepsByNodeID[step.NodeID] = step
	}
	startStep, ok := stepsByNodeID["workflow/start"]
	require.True(t, ok)
	workerAStep, ok := stepsByNodeID["workflow/fanout/worker-a"]
	require.True(t, ok)
	workerBStep, ok := stepsByNodeID["workflow/fanout/worker-b"]
	require.True(t, ok)
	endStep, ok := stepsByNodeID["workflow/end"]
	require.True(t, ok)
	assert.Empty(t, startStep.PredecessorStepIDs)
	assert.Equal(t, []string{startStep.StepID}, workerAStep.PredecessorStepIDs)
	assert.Equal(t, []string{startStep.StepID}, workerBStep.PredecessorStepIDs)
	assert.ElementsMatch(t, []string{workerAStep.StepID, workerBStep.StepID}, endStep.PredecessorStepIDs)
}

func TestRunnerCompletion_CycleCarriesPredecessorsAcrossIterations(t *testing.T) {
	iterations := 2
	workflow := cycleagent.New("workflow", cycleagent.WithMaxIterations(iterations), cycleagent.WithSubAgents([]agent.Agent{
		llmagent.New("worker", llmagent.WithModel(&staticModel{name: "worker-model", content: "worker"})),
	}))
	r := NewRunner("app", workflow, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello cycle"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	var completion *event.Event
	for evt := range eventCh {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
	require.NotNil(t, completion.ExecutionTrace)
	require.Len(t, completion.ExecutionTrace.Steps, iterations)
	first := completion.ExecutionTrace.Steps[0]
	second := completion.ExecutionTrace.Steps[1]
	assert.Equal(t, "workflow/worker", first.NodeID)
	assert.Equal(t, "workflow/worker", second.NodeID)
	assert.Empty(t, first.PredecessorStepIDs)
	assert.Equal(t, []string{first.StepID}, second.PredecessorStepIDs)
}

func TestRunnerCompletion_GraphRunCapturesComplexExecutionPredecessors(t *testing.T) {
	schema := graph.NewStateSchema().
		AddField("route_count", graph.StateField{
			Type:    reflect.TypeOf(0),
			Reducer: graph.DefaultReducer,
			Default: func() any { return 0 },
		}).
		AddField("visited", graph.StateField{
			Type:    reflect.TypeOf([]string{}),
			Reducer: graph.StringSliceReducer,
			Default: func() any { return []string{} },
		})
	builder := graph.NewStateGraph(schema)
	builder.AddNode("start", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"start"}}, nil
	})
	builder.AddNode("prepare", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"prepare"}}, nil
	})
	builder.AddNode("route", func(_ context.Context, state graph.State) (any, error) {
		count, _ := state["route_count"].(int)
		return graph.State{
			"route_count": count + 1,
			"visited":     []string{"route"},
		}, nil
	})
	builder.AddNode("tools", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"tools"}}, nil
	})
	builder.AddNode("branch_a", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"branch_a"}}, nil
	})
	builder.AddNode("branch_b", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"branch_b"}}, nil
	})
	builder.AddNode("join", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"join"}}, nil
	})
	builder.AddNode("done", func(context.Context, graph.State) (any, error) {
		return graph.State{"visited": []string{"done"}}, nil
	})
	builder.SetEntryPoint("start")
	builder.AddEdge("start", "route")
	builder.AddEdge("start", "prepare")
	builder.AddConditionalEdges("route", func(_ context.Context, state graph.State) (string, error) {
		count, _ := state["route_count"].(int)
		if count == 1 {
			return "tools", nil
		}
		return "branch_a", nil
	}, map[string]string{
		"tools":    "tools",
		"branch_a": "branch_a",
	})
	builder.AddEdge("tools", "route")
	builder.AddEdge("prepare", "branch_b")
	builder.AddJoinEdge([]string{"branch_a", "branch_b"}, "join")
	builder.AddConditionalEdges("join", func(context.Context, graph.State) (string, error) {
		return "done", nil
	}, map[string]string{"done": "done"})
	builder.SetFinishPoint("done")
	compiled := builder.MustCompile()
	ag, err := graphagent.New("assistant", compiled, graphagent.WithMaxConcurrency(1))
	require.NoError(t, err)
	r := NewRunner("app", ag, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello graph trace"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.ExecutionTrace)
	trace := completion.ExecutionTrace
	assert.Equal(t, "assistant", trace.RootAgentName)
	assert.Equal(t, atrace.TraceStatusCompleted, trace.Status)
	require.Len(t, trace.Steps, 9)
	stepIDToNodeID := make(map[string]string, len(trace.Steps))
	stepsByNodeID := make(map[string][]atrace.Step)
	for _, step := range trace.Steps {
		stepIDToNodeID[step.StepID] = step.NodeID
		stepsByNodeID[step.NodeID] = append(stepsByNodeID[step.NodeID], step)
		require.NotEmpty(t, step.StepID)
		require.NotEqual(t, step.StartedAt, step.EndedAt)
		require.NotNil(t, step.Input)
		assert.Empty(t, step.Error)
	}
	startSteps := stepsByNodeID["assistant/start"]
	prepareSteps := stepsByNodeID["assistant/prepare"]
	routeSteps := stepsByNodeID["assistant/route"]
	toolsSteps := stepsByNodeID["assistant/tools"]
	branchASteps := stepsByNodeID["assistant/branch_a"]
	branchBSteps := stepsByNodeID["assistant/branch_b"]
	joinSteps := stepsByNodeID["assistant/join"]
	doneSteps := stepsByNodeID["assistant/done"]
	require.Len(t, startSteps, 1)
	require.Len(t, prepareSteps, 1)
	require.Len(t, routeSteps, 2)
	require.Len(t, toolsSteps, 1)
	require.Len(t, branchASteps, 1)
	require.Len(t, branchBSteps, 1)
	require.Len(t, joinSteps, 1)
	require.Len(t, doneSteps, 1)
	startStep := startSteps[0]
	prepareStep := prepareSteps[0]
	toolsStep := toolsSteps[0]
	branchAStep := branchASteps[0]
	branchBStep := branchBSteps[0]
	joinStep := joinSteps[0]
	doneStep := doneSteps[0]
	assert.Empty(t, startStep.PredecessorStepIDs)
	assert.Contains(t, startStep.Input.Text, "hello graph trace")
	routeAfterStart := findTraceStepByPredecessor(t, routeSteps, startStep.StepID)
	routeAfterTools := findTraceStepByPredecessor(t, routeSteps, toolsStep.StepID)
	assert.Equal(t, []string{startStep.StepID}, prepareStep.PredecessorStepIDs)
	assert.Equal(t, []string{startStep.StepID}, routeAfterStart.PredecessorStepIDs)
	assert.Equal(t, []string{routeAfterStart.StepID}, toolsStep.PredecessorStepIDs)
	assert.Equal(t, []string{prepareStep.StepID}, branchBStep.PredecessorStepIDs)
	assert.Equal(t, []string{toolsStep.StepID}, routeAfterTools.PredecessorStepIDs)
	assert.Equal(t, []string{routeAfterTools.StepID}, branchAStep.PredecessorStepIDs)
	assert.ElementsMatch(
		t,
		[]string{branchAStep.StepID, branchBStep.StepID},
		joinStep.PredecessorStepIDs,
	)
	assert.Equal(t, []string{joinStep.StepID}, doneStep.PredecessorStepIDs)
	assert.ElementsMatch(
		t,
		[]string{"assistant/branch_a", "assistant/branch_b"},
		predecessorNodeIDs(stepIDToNodeID, joinStep.PredecessorStepIDs),
	)
}

func TestRunnerCompletion_GraphRunCapturesNodeFailure(t *testing.T) {
	builder := graph.NewStateGraph(graph.NewStateSchema())
	builder.AddNode("boom", func(context.Context, graph.State) (any, error) {
		return nil, errors.New("boom")
	})
	builder.SetEntryPoint("boom")
	compiled := builder.MustCompile()
	ag, err := graphagent.New("assistant", compiled, graphagent.WithMaxConcurrency(1))
	require.NoError(t, err)
	r := NewRunner("app", ag, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello graph failure"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	completion := collectRunnerCompletionEvent(t, eventCh)
	require.NotNil(t, completion.ExecutionTrace)
	trace := completion.ExecutionTrace
	assert.Equal(t, atrace.TraceStatusFailed, trace.Status)
	require.Len(t, trace.Steps, 1)
	step := trace.Steps[0]
	assert.Equal(t, "assistant/boom", step.NodeID)
	assert.Empty(t, step.PredecessorStepIDs)
	assert.Contains(t, step.Error, "boom")
	require.NotNil(t, step.Input)
	assert.Nil(t, step.Output)
}

func TestRunnerCompletion_GraphRunPropagatesGoToPredecessor(t *testing.T) {
	builder := graph.NewStateGraph(graph.NewStateSchema())
	builder.AddNode("start", func(context.Context, graph.State) (any, error) {
		return &graph.Command{GoTo: "done"}, nil
	})
	builder.AddNode("done", func(context.Context, graph.State) (any, error) {
		return graph.State{"ok": true}, nil
	})
	builder.SetEntryPoint("start")
	builder.SetFinishPoint("done")
	compiled := builder.MustCompile()
	ag, err := graphagent.New("assistant", compiled, graphagent.WithMaxConcurrency(1))
	require.NoError(t, err)
	r := NewRunner("app", ag, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello goto"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	trace := collectRunnerCompletionEvent(t, eventCh).ExecutionTrace
	require.NotNil(t, trace)
	require.Len(t, trace.Steps, 2)
	stepsByNodeID := make(map[string]atrace.Step, len(trace.Steps))
	for _, step := range trace.Steps {
		stepsByNodeID[step.NodeID] = step
	}
	startStep := stepsByNodeID["assistant/start"]
	doneStep := stepsByNodeID["assistant/done"]
	assert.Empty(t, startStep.PredecessorStepIDs)
	assert.Equal(t, []string{startStep.StepID}, doneStep.PredecessorStepIDs)
}

func TestRunnerCompletion_GraphRunPropagatesFanOutCommandPredecessors(t *testing.T) {
	builder := graph.NewStateGraph(graph.NewStateSchema())
	builder.AddNode("start", func(context.Context, graph.State) (any, error) {
		return []*graph.Command{
			{GoTo: "branch_a"},
			{GoTo: "branch_b"},
		}, nil
	})
	builder.AddNode("branch_a", func(context.Context, graph.State) (any, error) {
		return graph.State{"branch_a": true}, nil
	})
	builder.AddNode("branch_b", func(context.Context, graph.State) (any, error) {
		return graph.State{"branch_b": true}, nil
	})
	builder.SetEntryPoint("start")
	builder.SetFinishPoint("branch_a")
	builder.SetFinishPoint("branch_b")
	compiled := builder.MustCompile()
	ag, err := graphagent.New("assistant", compiled, graphagent.WithMaxConcurrency(1))
	require.NoError(t, err)
	r := NewRunner("app", ag, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello fanout commands"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	trace := collectRunnerCompletionEvent(t, eventCh).ExecutionTrace
	require.NotNil(t, trace)
	require.Len(t, trace.Steps, 3)
	stepsByNodeID := make(map[string]atrace.Step, len(trace.Steps))
	for _, step := range trace.Steps {
		stepsByNodeID[step.NodeID] = step
	}
	startStep := stepsByNodeID["assistant/start"]
	branchAStep := stepsByNodeID["assistant/branch_a"]
	branchBStep := stepsByNodeID["assistant/branch_b"]
	assert.Empty(t, startStep.PredecessorStepIDs)
	assert.Equal(t, []string{startStep.StepID}, branchAStep.PredecessorStepIDs)
	assert.Equal(t, []string{startStep.StepID}, branchBStep.PredecessorStepIDs)
}

func TestRunnerCompletion_GraphAgentNodePropagatesTraceMetadataToChildAgent(t *testing.T) {
	builder := graph.NewStateGraph(graph.NewStateSchema())
	builder.AddAgentNode("delegate")
	builder.SetEntryPoint("delegate")
	builder.SetFinishPoint("delegate")
	compiled := builder.MustCompile()
	childAgent := llmagent.New("delegate", llmagent.WithModel(&staticModel{name: "delegate-model", content: "worker"}))
	ag, err := graphagent.New(
		"assistant",
		compiled,
		graphagent.WithMaxConcurrency(1),
		graphagent.WithSubAgents([]agent.Agent{childAgent}),
	)
	require.NoError(t, err)
	r := NewRunner("app", ag, WithSessionService(sessioninmemory.NewSessionService()))
	eventCh, err := r.Run(
		context.Background(),
		"user-1",
		"session-1",
		model.NewUserMessage("hello delegated graph"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	trace := collectRunnerCompletionEvent(t, eventCh).ExecutionTrace
	require.NotNil(t, trace)
	require.Len(t, trace.Steps, 1)
	step := trace.Steps[0]
	assert.Equal(t, "assistant", step.AgentName)
	assert.Equal(t, "assistant/delegate", step.NodeID)
	assert.Empty(t, step.PredecessorStepIDs)
}

func collectRunnerCompletionEvent(t *testing.T, eventCh <-chan *event.Event) *event.Event {
	t.Helper()
	var completion *event.Event
	for evt := range eventCh {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	require.NotNil(t, completion)
	return completion
}

func findTraceStepByPredecessor(
	t *testing.T,
	steps []atrace.Step,
	predecessorStepID string,
) atrace.Step {
	t.Helper()
	for _, step := range steps {
		if len(step.PredecessorStepIDs) == 1 && step.PredecessorStepIDs[0] == predecessorStepID {
			return step
		}
	}
	require.Failf(t, "missing trace step", "no step found with predecessor %q", predecessorStepID)
	return atrace.Step{}
}

func predecessorNodeIDs(stepIDToNodeID map[string]string, stepIDs []string) []string {
	nodeIDs := make([]string, 0, len(stepIDs))
	for _, stepID := range stepIDs {
		nodeIDs = append(nodeIDs, stepIDToNodeID[stepID])
	}
	return nodeIDs
}
