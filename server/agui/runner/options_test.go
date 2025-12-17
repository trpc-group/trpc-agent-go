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
	"errors"
	"testing"
	"time"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/stretchr/testify/assert"
	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

func TestNewOptionsDefaults(t *testing.T) {
	opts := NewOptions()

	assert.NotNil(t, opts.UserIDResolver)
	userID, err := opts.UserIDResolver(context.Background(), &adapter.RunAgentInput{})
	assert.NoError(t, err)
	assert.Equal(t, "user", userID)
	assert.Nil(t, opts.TranslateCallbacks)

	assert.NotNil(t, opts.TranslatorFactory)
	input := &adapter.RunAgentInput{ThreadID: "thread-1", RunID: "run-1"}
	tr := opts.TranslatorFactory(context.Background(), input)
	assert.NotNil(t, tr)
	assert.IsType(t, translator.New(context.Background(), "", ""), tr)

	assert.NotNil(t, opts.RunAgentInputHook)
	modified, err := opts.RunAgentInputHook(context.Background(), input)
	assert.NoError(t, err)
	assert.Same(t, input, modified)

	assert.NotNil(t, opts.RunOptionResolver)
	resolvedOpts, err := opts.RunOptionResolver(context.Background(), input)
	assert.NoError(t, err)
	assert.Nil(t, resolvedOpts)

	assert.NotNil(t, opts.StartSpan)
	rootCtx := context.Background()
	ctx, span, err := opts.StartSpan(rootCtx, input)
	assert.NoError(t, err)
	assert.Equal(t, rootCtx, ctx)
	assert.NotNil(t, span)
}

func TestWithUserIDResolver(t *testing.T) {
	wantErr := errors.New("resolver error")
	called := false
	customResolver := func(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
		called = true
		return "custom", wantErr
	}

	opts := NewOptions(WithUserIDResolver(customResolver))

	userID, err := opts.UserIDResolver(context.Background(), &adapter.RunAgentInput{})
	assert.Equal(t, wantErr, err)
	assert.Equal(t, "custom", userID)
	assert.True(t, called)
}

func TestWithTranslatorFactory(t *testing.T) {
	customTranslator := translator.New(context.Background(), "custom-thread", "custom-run")
	factoryCalled := false
	opts := NewOptions(
		WithTranslatorFactory(
			func(ctx context.Context, input *adapter.RunAgentInput) translator.Translator {
				factoryCalled = true
				return customTranslator
			},
		),
	)

	input := &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"}
	tr := opts.TranslatorFactory(context.Background(), input)

	assert.True(t, factoryCalled)
	assert.Equal(t, customTranslator, tr)
}

func TestWithTranslateCallbacks(t *testing.T) {
	cb := translator.NewCallbacks()
	opts := NewOptions(WithTranslateCallbacks(cb))
	assert.Same(t, cb, opts.TranslateCallbacks)
}

func TestWithRunAgentInputHook(t *testing.T) {
	called := false
	input := &adapter.RunAgentInput{ThreadID: "thread", RunID: "run"}
	custom := &adapter.RunAgentInput{ThreadID: "other-thread", RunID: "other-run"}
	hook := func(ctx context.Context, in *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
		called = true
		assert.Same(t, input, in)
		return custom, nil
	}

	opts := NewOptions(WithRunAgentInputHook(hook))

	got, err := opts.RunAgentInputHook(context.Background(), input)
	assert.NoError(t, err)
	assert.True(t, called)
	assert.Equal(t, custom, got)
}

func TestWithAppName(t *testing.T) {
	opts := NewOptions(WithAppName("custom-app"))
	assert.Equal(t, "custom-app", opts.AppName)
}

func TestWithSessionService(t *testing.T) {
	opts := NewOptions(WithSessionService(inmemory.NewSessionService()))
	assert.NotNil(t, opts.SessionService)
}

func TestWithAggregationOptionsAndFactory(t *testing.T) {
	customCalled := false
	customFactory := func(ctx context.Context, opt ...aggregator.Option) aggregator.Aggregator {
		customCalled = true
		return aggregator.New(ctx, opt...)
	}
	opts := NewOptions(
		WithAggregationOption(aggregator.WithEnabled(false)),
		WithAggregatorFactory(customFactory),
		WithFlushInterval(time.Second),
	)

	assert.Equal(t, time.Second, opts.FlushInterval)
	agg := opts.AggregatorFactory(context.Background(), opts.AggregationOption...)
	assert.True(t, customCalled)

	events, err := agg.Append(context.Background(), aguievents.NewTextMessageContentEvent("msg", "hi"))
	assert.NoError(t, err)
	assert.Len(t, events, 1) // disabled aggregation should pass through.
}

func TestWithRunOptionResolver(t *testing.T) {
	called := false
	resolver := func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
		called = true
		return nil, nil
	}
	opts := NewOptions(WithRunOptionResolver(resolver))
	assert.NotNil(t, opts.RunOptionResolver)
	opts.RunOptionResolver(context.Background(), nil)
	assert.True(t, called)
}

func TestWithStartSpan(t *testing.T) {
	called := false
	start := func(ctx context.Context, input *adapter.RunAgentInput) (context.Context, trace.Span, error) {
		called = true
		return ctx, trace.SpanFromContext(ctx), errors.New("start failed")
	}

	opts := NewOptions(WithStartSpan(start))
	_, _, err := opts.StartSpan(context.Background(), &adapter.RunAgentInput{})
	assert.True(t, called)
	assert.EqualError(t, err, "start failed")
}
