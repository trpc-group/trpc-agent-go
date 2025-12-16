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
	"time"

	"go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/internal/track"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Options holds the options for the runner.
type Options struct {
	TranslatorFactory  TranslatorFactory     // TranslatorFactory creates a translator for an AG-UI run.
	UserIDResolver     UserIDResolver        // UserIDResolver derives the user identifier for an AG-UI run.
	TranslateCallbacks *translator.Callbacks // TranslateCallbacks translates the run events to AG-UI events.
	RunAgentInputHook  RunAgentInputHook     // RunAgentInputHook allows modifying the run input before processing.
	AppName            string                // AppName is the name of the application.
	SessionService     session.Service       // SessionService is the session service.
	RunOptionResolver  RunOptionResolver     // RunOptionResolver resolves the runner options for an AG-UI run.
	AggregatorFactory  aggregator.Factory    // AggregatorFactory builds an aggregator for each run.
	AggregationOption  []aggregator.Option   // AggregationOption is the aggregation options for each run.
	FlushInterval      time.Duration         // FlushInterval controls how often buffered AG-UI events are flushed for a session.
	StartSpan          StartSpan             // StartSpan starts a span for an AG-UI run.
}

// NewOptions creates a new options instance.
func NewOptions(opt ...Option) *Options {
	opts := &Options{
		UserIDResolver:    defaultUserIDResolver,
		TranslatorFactory: defaultTranslatorFactory,
		RunAgentInputHook: defaultRunAgentInputHook,
		RunOptionResolver: defaultRunOptionResolver,
		AggregatorFactory: aggregator.New,
		FlushInterval:     track.DefaultFlushInterval,
		StartSpan:         defaultStartSpan,
	}
	for _, o := range opt {
		o(opts)
	}
	return opts
}

// Option is a function that configures the options.
type Option func(*Options)

// UserIDResolver is a function that derives the user identifier for an AG-UI run.
type UserIDResolver func(ctx context.Context, input *adapter.RunAgentInput) (string, error)

// WithUserIDResolver sets the user ID resolver.
func WithUserIDResolver(u UserIDResolver) Option {
	return func(o *Options) {
		o.UserIDResolver = u
	}
}

// TranslatorFactory is a function that creates a translator for an AG-UI run.
type TranslatorFactory func(ctx context.Context, input *adapter.RunAgentInput) translator.Translator

// WithTranslatorFactory sets the translator factory.
func WithTranslatorFactory(factory TranslatorFactory) Option {
	return func(o *Options) {
		o.TranslatorFactory = factory
	}
}

// WithTranslateCallbacks sets the translate callbacks.
func WithTranslateCallbacks(c *translator.Callbacks) Option {
	return func(o *Options) {
		o.TranslateCallbacks = c
	}
}

// RunAgentInputHook allows modifying the run input before processing.
type RunAgentInputHook func(ctx context.Context, input *adapter.RunAgentInput) (*adapter.RunAgentInput, error)

// WithRunAgentInputHook sets the run input hook.
func WithRunAgentInputHook(hook RunAgentInputHook) Option {
	return func(o *Options) {
		o.RunAgentInputHook = hook
	}
}

// WithAppName sets the app name.
func WithAppName(n string) Option {
	return func(o *Options) {
		o.AppName = n
	}
}

// WithSessionService sets the session service.
func WithSessionService(s session.Service) Option {
	return func(o *Options) {
		o.SessionService = s
	}
}

// WithAggregationOption forwards aggregator options to the runner-level factory.
func WithAggregationOption(option ...aggregator.Option) Option {
	return func(o *Options) {
		o.AggregationOption = append(o.AggregationOption, option...)
	}
}

// WithAggregatorFactory sets the aggregator factory used for tracking.
func WithAggregatorFactory(factory aggregator.Factory) Option {
	return func(o *Options) {
		o.AggregatorFactory = factory
	}
}

// WithFlushInterval sets how often buffered AG-UI events are flushed for a session.
func WithFlushInterval(d time.Duration) Option {
	return func(o *Options) {
		o.FlushInterval = d
	}
}

// RunOptionResolver is a function that resolves the run options for an AG-UI run.
type RunOptionResolver func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error)

// WithRunOptionResolver sets the run option resolver.
func WithRunOptionResolver(r RunOptionResolver) Option {
	return func(o *Options) {
		o.RunOptionResolver = r
	}
}

// StartSpan starts a span for an AG-UI run and returns the updated context.
type StartSpan func(ctx context.Context, input *adapter.RunAgentInput) (context.Context, trace.Span, error)

// WithStartSpan sets the span starter for AG-UI runs.
func WithStartSpan(start StartSpan) Option {
	return func(o *Options) {
		o.StartSpan = start
	}
}

// defaultUserIDResolver is the default user ID resolver.
func defaultUserIDResolver(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
	return "user", nil
}

// defaultTranslatorFactory is the default translator factory.
func defaultTranslatorFactory(ctx context.Context, input *adapter.RunAgentInput) translator.Translator {
	return translator.New(ctx, input.ThreadID, input.RunID)
}

// defaultRunAgentInputHook returns the input unchanged.
func defaultRunAgentInputHook(ctx context.Context, input *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
	return input, nil
}

// defaultRunOptionResolver is the default run option resolver.
func defaultRunOptionResolver(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
	return nil, nil
}

// defaultStartSpan returns the original context and a non-recording span.
func defaultStartSpan(ctx context.Context, _ *adapter.RunAgentInput) (context.Context, trace.Span, error) {
	return ctx, trace.SpanFromContext(ctx), nil
}
