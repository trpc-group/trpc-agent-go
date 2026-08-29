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
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
	telemetrytrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

func TestRegisterGlobalAfterRunHookValidatesRegistration(t *testing.T) {
	isolateGlobalAfterRunHooks(t)
	hook := func(context.Context, *plugin.AfterRunArgs) error { return nil }

	require.ErrorIs(
		t,
		RegisterGlobalAfterRunHook("", hook),
		errGlobalAfterRunHookNameEmpty,
	)
	require.ErrorIs(
		t,
		RegisterGlobalAfterRunHook("nil", nil),
		errGlobalAfterRunHookNil,
	)
	require.NoError(t, RegisterGlobalAfterRunHook("observer", hook))
	require.EqualError(
		t,
		RegisterGlobalAfterRunHook("observer", hook),
		`runner: duplicate global after-run hook "observer"`,
	)

	hooks := snapshotGlobalAfterRunHooks()
	require.Len(t, hooks, 1)
	assert.Equal(t, "observer", hooks[0].name)
}

func TestRegisterGlobalAfterRunHookIsConcurrentSafe(t *testing.T) {
	isolateGlobalAfterRunHooks(t)
	hook := func(context.Context, *plugin.AfterRunArgs) error { return nil }

	const uniqueHooks = 32
	results := make(chan error, uniqueHooks)
	var wg sync.WaitGroup
	for i := 0; i < uniqueHooks; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			results <- RegisterGlobalAfterRunHook(
				fmt.Sprintf("observer-%d", index),
				hook,
			)
		}(i)
	}
	wg.Wait()
	close(results)
	for err := range results {
		require.NoError(t, err)
	}

	const duplicateRegistrations = 16
	results = make(chan error, duplicateRegistrations)
	for i := 0; i < duplicateRegistrations; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- RegisterGlobalAfterRunHook("shared", hook)
		}()
	}
	wg.Wait()
	close(results)
	var successfulDuplicates int
	for err := range results {
		if err == nil {
			successfulDuplicates++
		}
	}
	assert.Equal(t, 1, successfulDuplicates)
	assert.Len(t, snapshotGlobalAfterRunHooks(), uniqueHooks+1)
}

func TestGlobalAfterRunHooksObserveCompletedRun(t *testing.T) {
	isolateGlobalAfterRunHooks(t)
	exporter := installGlobalAfterRunTestTracer(t)
	agt := llmagent.New(
		"root-agent",
		llmagent.WithModel(&staticModel{name: "test-model", content: "done"}),
	)
	r := NewRunner("test-app", agt)

	var (
		calls             []string
		firstSpanContext  oteltrace.SpanContext
		secondSpanContext oteltrace.SpanContext
		firstHadTrace     bool
		secondHadTrace    bool
		secondTag         string
		secondInput       string
	)
	require.NoError(t, RegisterGlobalAfterRunHook(
		"first",
		func(ctx context.Context, args *plugin.AfterRunArgs) error {
			calls = append(calls, "first")
			firstSpanContext = oteltrace.SpanContextFromContext(ctx)
			if args == nil || args.CompletionEvent == nil ||
				args.CompletionEvent.ExecutionTrace == nil {
				return nil
			}
			firstHadTrace = true
			args.CompletionEvent.Tag = "mutated"
			if args.CompletionEvent.ExecutionTrace.Input != nil {
				args.CompletionEvent.ExecutionTrace.Input.Text = "mutated"
			}
			return nil
		},
	))
	require.NoError(t, RegisterGlobalAfterRunHook(
		"second",
		func(ctx context.Context, args *plugin.AfterRunArgs) error {
			calls = append(calls, "second")
			secondSpanContext = oteltrace.SpanContextFromContext(ctx)
			if args == nil || args.CompletionEvent == nil ||
				args.CompletionEvent.ExecutionTrace == nil {
				return nil
			}
			secondHadTrace = true
			secondTag = args.CompletionEvent.Tag
			if args.CompletionEvent.ExecutionTrace.Input != nil {
				secondInput = args.CompletionEvent.ExecutionTrace.Input.Text
			}
			return nil
		},
	))

	events, err := r.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("hello"),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	var completion *event.Event
	for evt := range events {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}

	require.NotNil(t, completion)
	require.NotNil(t, completion.ExecutionTrace)
	assert.Equal(t, []string{"first", "second"}, calls)
	assert.True(t, firstHadTrace)
	assert.True(t, secondHadTrace)
	assert.Empty(t, secondTag)
	assert.NotEmpty(t, secondInput)
	assert.NotEqual(t, "mutated", secondInput)
	require.True(t, firstSpanContext.IsValid())
	assert.Equal(t, firstSpanContext, secondSpanContext)

	var matchedRootSpan bool
	for _, span := range exporter.GetSpans() {
		if span.SpanContext.SpanID() == firstSpanContext.SpanID() {
			matchedRootSpan = true
			break
		}
	}
	assert.True(t, matchedRootSpan)
}

func TestGlobalAfterRunHooksIsolateNestedCompletionData(t *testing.T) {
	isolateGlobalAfterRunHooks(t)

	text := "original"
	arguments := []byte(`{"value":"original"}`)
	completion := event.NewResponseEvent(
		"invocation",
		"runner",
		&model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Choices: []model.Choice{{
				Message: model.Message{
					ContentParts: []model.ContentPart{
						{Type: model.ContentTypeText, Text: &text},
						{Type: model.ContentTypeImage, Image: &model.Image{Data: []byte("image")}},
					},
					ToolCalls: []model.ToolCall{{
						Function: model.FunctionDefinitionParam{Arguments: arguments},
						ExtraFields: map[string]any{
							"nested": map[string]any{"key": "original"},
						},
					}},
				},
			}},
		},
	)
	var (
		secondText      string
		secondArguments string
		secondExtra     string
	)
	require.NoError(t, RegisterGlobalAfterRunHook(
		"mutator",
		func(_ context.Context, args *plugin.AfterRunArgs) error {
			choice := args.CompletionEvent.Choices[0]
			*choice.Message.ContentParts[0].Text = "mutated"
			choice.Message.ContentParts[1].Image.Data[0] = 'X'
			choice.Message.ToolCalls[0].Function.Arguments[0] = '['
			choice.Message.ToolCalls[0].ExtraFields["nested"].(map[string]any)["key"] = "mutated"
			return nil
		},
	))
	require.NoError(t, RegisterGlobalAfterRunHook(
		"observer",
		func(_ context.Context, args *plugin.AfterRunArgs) error {
			choice := args.CompletionEvent.Choices[0]
			secondText = *choice.Message.ContentParts[0].Text
			secondArguments = string(choice.Message.ToolCalls[0].Function.Arguments)
			secondExtra = choice.Message.ToolCalls[0].ExtraFields["nested"].(map[string]any)["key"].(string)
			return nil
		},
	))

	applyGlobalAfterRunHooks(
		context.Background(),
		prepareGlobalAfterRunState(),
		agent.NewInvocation(),
		completion,
	)

	assert.Equal(t, "original", secondText)
	assert.Equal(t, `{"value":"original"}`, secondArguments)
	assert.Equal(t, "original", secondExtra)
	assert.Equal(t, "original", *completion.Choices[0].Message.ContentParts[0].Text)
	assert.Equal(t, []byte("image"), completion.Choices[0].Message.ContentParts[1].Image.Data)
	assert.Equal(t, `{"value":"original"}`, string(completion.Choices[0].Message.ToolCalls[0].Function.Arguments))
	assert.Equal(
		t,
		"original",
		completion.Choices[0].Message.ToolCalls[0].ExtraFields["nested"].(map[string]any)["key"],
	)
}

func TestGlobalAfterRunHooksIsolateResponseErrorPointers(t *testing.T) {
	isolateGlobalAfterRunHooks(t)

	param := "original-param"
	code := "original-code"
	completion := event.NewResponseEvent(
		"invocation",
		"runner",
		&model.Response{
			Object: model.ObjectTypeRunnerCompletion,
			Error: &model.ResponseError{
				Type:    model.ErrorTypeRunError,
				Message: "original-message",
				Param:   &param,
				Code:    &code,
			},
		},
	)
	var (
		secondParam string
		secondCode  string
	)
	require.NoError(t, RegisterGlobalAfterRunHook(
		"mutator",
		func(_ context.Context, args *plugin.AfterRunArgs) error {
			*args.CompletionEvent.Response.Error.Param = "mutated-param"
			*args.CompletionEvent.Response.Error.Code = "mutated-code"
			return nil
		},
	))
	require.NoError(t, RegisterGlobalAfterRunHook(
		"observer",
		func(_ context.Context, args *plugin.AfterRunArgs) error {
			err := args.CompletionEvent.Response.Error
			secondParam = *err.Param
			secondCode = *err.Code
			return nil
		},
	))

	applyGlobalAfterRunHooks(
		context.Background(),
		prepareGlobalAfterRunState(),
		agent.NewInvocation(),
		completion,
	)

	assert.Equal(t, "original-param", secondParam)
	assert.Equal(t, "original-code", secondCode)
	require.NotNil(t, completion.Response)
	require.NotNil(t, completion.Response.Error)
	assert.Equal(t, "original-param", *completion.Response.Error.Param)
	assert.Equal(t, "original-code", *completion.Response.Error.Code)
}

func TestGlobalAfterRunHookFailuresDoNotAffectCompletion(t *testing.T) {
	isolateGlobalAfterRunHooks(t)
	installGlobalAfterRunTestTracer(t)

	require.NoError(t, RegisterGlobalAfterRunHook(
		"error",
		func(context.Context, *plugin.AfterRunArgs) error {
			return errors.New("failed")
		},
	))
	require.NoError(t, RegisterGlobalAfterRunHook(
		"panic",
		func(context.Context, *plugin.AfterRunArgs) error {
			panic("failed")
		},
	))
	var (
		lastCalled   bool
		lastHadTrace bool
		spanContext  oteltrace.SpanContext
	)
	require.NoError(t, RegisterGlobalAfterRunHook(
		"last",
		func(ctx context.Context, args *plugin.AfterRunArgs) error {
			lastCalled = true
			spanContext = oteltrace.SpanContextFromContext(ctx)
			lastHadTrace = args != nil && args.CompletionEvent != nil &&
				args.CompletionEvent.ExecutionTrace != nil
			return nil
		},
	))

	outerCtx, outerSpan := telemetrytrace.Tracer.Start(
		context.Background(),
		"outer",
	)
	r := NewRunner("test-app", &mockAgent{name: "test-agent"})
	events, err := r.Run(
		outerCtx,
		"user",
		"session",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	var completion *event.Event
	for evt := range events {
		if evt != nil && evt.IsRunnerCompletion() {
			completion = evt
		}
	}
	outerSpan.End()

	require.NotNil(t, completion)
	assert.True(t, lastCalled)
	assert.False(t, lastHadTrace)
	assert.False(t, spanContext.IsValid())
}

func TestGlobalAfterRunHookDisabledTracingHasInvalidSpanContext(t *testing.T) {
	isolateGlobalAfterRunHooks(t)
	installGlobalAfterRunTestTracer(t)

	var (
		called      bool
		hadTrace    bool
		spanContext oteltrace.SpanContext
	)
	require.NoError(t, RegisterGlobalAfterRunHook(
		"disabled-tracing",
		func(ctx context.Context, args *plugin.AfterRunArgs) error {
			called = true
			spanContext = oteltrace.SpanContextFromContext(ctx)
			hadTrace = args != nil && args.CompletionEvent != nil &&
				args.CompletionEvent.ExecutionTrace != nil
			return nil
		},
	))
	agt := llmagent.New(
		"root-agent",
		llmagent.WithModel(&staticModel{name: "test-model", content: "done"}),
	)
	r := NewRunner("test-app", agt)
	events, err := r.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("hello"),
		agent.WithDisableTracing(true),
		agent.WithExecutionTraceEnabled(true),
	)
	require.NoError(t, err)
	for range events {
	}

	assert.True(t, called)
	assert.True(t, hadTrace)
	assert.False(t, spanContext.IsValid())
}

func TestGlobalAfterRunHooksRunAfterRunnerPlugins(t *testing.T) {
	isolateGlobalAfterRunHooks(t)
	var calls []string
	require.NoError(t, RegisterGlobalAfterRunHook(
		"global",
		func(context.Context, *plugin.AfterRunArgs) error {
			calls = append(calls, "global")
			return nil
		},
	))
	runnerPlugin := &testPlugin{
		name: "runner",
		reg: func(registry *plugin.Registry) {
			registry.AfterRun(func(
				context.Context,
				*plugin.AfterRunArgs,
			) error {
				calls = append(calls, "runner")
				return nil
			})
		},
	}
	r := NewRunner(
		"test-app",
		&mockAgent{name: "test-agent"},
		WithPlugins(runnerPlugin),
	)

	events, err := r.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	for range events {
	}

	assert.Equal(t, []string{"runner", "global"}, calls)
}

func TestGlobalAfterRunHooksAreSnapshottedAtRunStart(t *testing.T) {
	isolateGlobalAfterRunHooks(t)
	var firstCalled, lateCalled bool
	require.NoError(t, RegisterGlobalAfterRunHook(
		"first",
		func(context.Context, *plugin.AfterRunArgs) error {
			firstCalled = true
			return nil
		},
	))

	release := make(chan struct{})
	r := NewRunner("test-app", &blockingGlobalAfterRunAgent{
		mockAgent: &mockAgent{name: "test-agent"},
		release:   release,
	})
	events, err := r.Run(
		context.Background(),
		"user",
		"session",
		model.NewUserMessage("hello"),
	)
	require.NoError(t, err)
	require.NoError(t, RegisterGlobalAfterRunHook(
		"late",
		func(context.Context, *plugin.AfterRunArgs) error {
			lateCalled = true
			return nil
		},
	))
	close(release)
	for range events {
	}

	assert.True(t, firstCalled)
	assert.False(t, lateCalled)
}

type blockingGlobalAfterRunAgent struct {
	*mockAgent
	release <-chan struct{}
}

func (a *blockingGlobalAfterRunAgent) Run(
	_ context.Context,
	_ *agent.Invocation,
) (<-chan *event.Event, error) {
	events := make(chan *event.Event)
	go func() {
		defer close(events)
		<-a.release
	}()
	return events, nil
}

func isolateGlobalAfterRunHooks(t *testing.T) {
	t.Helper()
	globalAfterRunHooks.Lock()
	previousHooks := append(
		[]namedGlobalAfterRunHook(nil),
		globalAfterRunHooks.hooks...,
	)
	previousNames := make(map[string]struct{}, len(globalAfterRunHooks.names))
	for name := range globalAfterRunHooks.names {
		previousNames[name] = struct{}{}
	}
	globalAfterRunHooks.hooks = nil
	globalAfterRunHooks.names = nil
	globalAfterRunHooks.Unlock()
	t.Cleanup(func() {
		globalAfterRunHooks.Lock()
		globalAfterRunHooks.hooks = previousHooks
		globalAfterRunHooks.names = previousNames
		globalAfterRunHooks.Unlock()
	})
}

func installGlobalAfterRunTestTracer(t *testing.T) *tracetest.InMemoryExporter {
	t.Helper()
	originalProvider := telemetrytrace.TracerProvider
	originalTracer := telemetrytrace.Tracer
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	telemetrytrace.TracerProvider = provider
	telemetrytrace.Tracer = provider.Tracer("global-after-run-test")
	t.Cleanup(func() {
		require.NoError(t, provider.Shutdown(context.Background()))
		telemetrytrace.TracerProvider = originalProvider
		telemetrytrace.Tracer = originalTracer
	})
	return exporter
}
