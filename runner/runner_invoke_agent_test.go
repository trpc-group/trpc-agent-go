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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/graphagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/agent/parallelagent"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	invokeagenttelemetry "trpc.group/trpc-go/trpc-agent-go/internal/invokeagenttelemetry"
	"trpc.group/trpc-go/trpc-agent-go/model"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestRunner_Run_RecordsInvokeAgentSpanHierarchy_ForLLMAgent(t *testing.T) {
	recorder := useInvokeAgentSpanRecorder(t)

	root := llmagent.New(
		"assistant",
		llmagent.WithModel(&staticModel{name: "single-llm", content: "single response"}),
	)
	r := NewRunner(
		"app",
		root,
		WithSessionService(sessioninmemory.NewSessionService()),
	)

	eventCh, err := r.Run(
		context.Background(),
		"user-llm",
		"session-llm",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	require.NotNil(t, collectRunnerCompletionEvent(t, eventCh).Response)

	spans := collectInvokeAgentSpans(recorder.Ended())
	require.Len(t, spans, 1)

	rootSpan := requireInvokeAgentSpanByName(t, spans, invokeAgentSpanName("assistant"))
	require.False(t, rootSpan.Parent().IsValid())
}

func TestRunner_Run_RecordsInvokeAgentSpanHierarchy_ForParallelAgent(t *testing.T) {
	recorder := useInvokeAgentSpanRecorder(t)

	fanout := parallelagent.New(
		"fanout",
		parallelagent.WithSubAgents([]agent.Agent{
			llmagent.New(
				"left",
				llmagent.WithModel(&staticModel{name: "parallel-left", content: "left response"}),
			),
			llmagent.New(
				"right",
				llmagent.WithModel(&staticModel{name: "parallel-right", content: "right response"}),
			),
		}),
	)
	r := NewRunner(
		"app",
		fanout,
		WithSessionService(sessioninmemory.NewSessionService()),
	)

	eventCh, err := r.Run(
		context.Background(),
		"user-parallel",
		"session-parallel",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	require.NotNil(t, collectRunnerCompletionEvent(t, eventCh).Response)

	spans := collectInvokeAgentSpans(recorder.Ended())
	require.Len(t, spans, 3)

	rootSpan := requireInvokeAgentSpanByName(t, spans, invokeAgentSpanName("fanout"))
	leftSpan := requireInvokeAgentSpanByName(t, spans, invokeAgentSpanName("left"))
	rightSpan := requireInvokeAgentSpanByName(t, spans, invokeAgentSpanName("right"))

	require.False(t, rootSpan.Parent().IsValid())
	requireInvokeAgentChildOf(t, leftSpan, rootSpan)
	requireInvokeAgentChildOf(t, rightSpan, rootSpan)
}

func TestRunner_Run_RecordsInvokeAgentSpanHierarchy_ForGraphParallelSubAgent(t *testing.T) {
	recorder := useInvokeAgentSpanRecorder(t)

	fanout := parallelagent.New(
		"fanout",
		parallelagent.WithSubAgents([]agent.Agent{
			llmagent.New(
				"left",
				llmagent.WithModel(&staticModel{name: "graph-left", content: "left response"}),
			),
			llmagent.New(
				"right",
				llmagent.WithModel(&staticModel{name: "graph-right", content: "right response"}),
			),
		}),
	)
	builder := graph.NewStateGraph(graph.MessagesStateSchema())
	builder.AddAgentNode("fanout")
	builder.SetEntryPoint("fanout")
	builder.SetFinishPoint("fanout")

	parent, err := graphagent.New(
		"assistant",
		builder.MustCompile(),
		graphagent.WithSubAgents([]agent.Agent{fanout}),
	)
	require.NoError(t, err)

	r := NewRunner(
		"app",
		parent,
		WithSessionService(sessioninmemory.NewSessionService()),
	)

	eventCh, err := r.Run(
		context.Background(),
		"user-graph",
		"session-graph",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	require.NotNil(t, collectRunnerCompletionEvent(t, eventCh).Response)

	spans := collectInvokeAgentSpans(recorder.Ended())
	require.Len(t, spans, 4)

	graphSpan := requireInvokeAgentSpanByName(t, spans, invokeAgentSpanName("assistant"))
	parallelSpan := requireInvokeAgentSpanByName(t, spans, invokeAgentSpanName("fanout"))
	leftSpan := requireInvokeAgentSpanByName(t, spans, invokeAgentSpanName("left"))
	rightSpan := requireInvokeAgentSpanByName(t, spans, invokeAgentSpanName("right"))

	require.False(t, graphSpan.Parent().IsValid())
	requireInvokeAgentChildOf(t, parallelSpan, graphSpan)
	requireInvokeAgentChildOf(t, leftSpan, parallelSpan)
	requireInvokeAgentChildOf(t, rightSpan, parallelSpan)
}

func useInvokeAgentSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()

	recorder := tracetest.NewSpanRecorder()
	tp := tracesdk.NewTracerProvider(tracesdk.WithSpanProcessor(recorder))
	originalProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		otel.SetTracerProvider(originalProvider)
		_ = tp.Shutdown(context.Background())
	})

	return recorder
}

func collectInvokeAgentSpans(spans []tracesdk.ReadOnlySpan) []tracesdk.ReadOnlySpan {
	result := make([]tracesdk.ReadOnlySpan, 0, len(spans))
	prefix := invokeagenttelemetry.OperationInvokeAgent + " "
	for _, span := range spans {
		if span.Name() == invokeagenttelemetry.OperationInvokeAgent || strings.HasPrefix(span.Name(), prefix) {
			result = append(result, span)
		}
	}
	return result
}

func requireInvokeAgentSpanByName(
	t *testing.T,
	spans []tracesdk.ReadOnlySpan,
	name string,
) tracesdk.ReadOnlySpan {
	t.Helper()

	var matched tracesdk.ReadOnlySpan
	for _, span := range spans {
		if span.Name() == name {
			require.Nil(t, matched, "duplicate invoke_agent span %q", name)
			matched = span
		}
	}
	require.NotNil(t, matched, "missing invoke_agent span %q", name)
	return matched
}

func requireInvokeAgentChildOf(
	t *testing.T,
	child tracesdk.ReadOnlySpan,
	parent tracesdk.ReadOnlySpan,
) {
	t.Helper()

	require.True(t, child.Parent().IsValid(), "expected %q to have a parent span", child.Name())
	require.Equal(t, parent.SpanContext().TraceID(), child.SpanContext().TraceID())
	require.Equal(t, parent.SpanContext().SpanID(), child.Parent().SpanID())
}

func invokeAgentSpanName(agentName string) string {
	return invokeagenttelemetry.OperationInvokeAgent + " " + agentName
}
