//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package toolsearch

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	telemetrytrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func useToolSearchSpanRecorder(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	originalProvider := telemetrytrace.TracerProvider
	originalTracer := telemetrytrace.Tracer
	telemetrytrace.TracerProvider = provider
	telemetrytrace.Tracer = provider.Tracer("tool-search-disable-tracing-test")
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		telemetrytrace.TracerProvider = originalProvider
		telemetrytrace.Tracer = originalTracer
	})
	return recorder
}

func TestSearchTools_DisableTracingSkipsSpanCreation(t *testing.T) {
	recorder := useToolSearchSpanRecorder(t)
	m := &fakeModel{
		info: model.Info{Name: "tool-search-model"},
		generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
			return respCh(&model.Response{
				Choices: []model.Choice{
					{Message: model.NewAssistantMessage(`{"tools":["weather"]}`)},
				},
			}), nil
		},
	}
	req := &model.Request{
		Messages: []model.Message{
			model.NewSystemMessage("sys"),
			model.NewUserMessage("weather"),
		},
	}
	ctx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.RunOptions{DisableTracing: true}),
		),
	)
	selectedCtx, selectedTools, err := searchTools(ctx, m, req, map[string]tool.Tool{
		"weather": fakeTool{decl: &tool.Declaration{Name: "weather"}},
	})
	if !assert.NoError(t, err) {
		return
	}
	assert.NotNil(t, selectedCtx)
	assert.Equal(t, []string{"weather"}, selectedTools)
	assert.Empty(t, recorder.Ended())
}

func TestKnowledgeSearcherRewriteQuery_DisableTracingSkipsSpanCreation(t *testing.T) {
	recorder := useToolSearchSpanRecorder(t)
	searcher := &knowledgeSearcher{
		model: &fakeModel{
			info: model.Info{Name: "tool-search-rewriter"},
			generate: func(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
				return respCh(&model.Response{
					Choices: []model.Choice{
						{Message: model.NewAssistantMessage("weather information")},
					},
				}), nil
			},
		},
		systemPrompt: defaultSystemPromptWithToolKnowledge,
	}
	ctx := agent.NewInvocationContext(
		context.Background(),
		agent.NewInvocation(
			agent.WithInvocationRunOptions(agent.RunOptions{DisableTracing: true}),
		),
	)
	rewrittenCtx, query, usage, err := searcher.rewriteQuery(ctx, "weather in beijing")
	if !assert.NoError(t, err) {
		return
	}
	assert.NotNil(t, rewrittenCtx)
	assert.Equal(t, "weather information", query)
	assert.NotNil(t, usage)
	assert.Empty(t, recorder.Ended())
}
