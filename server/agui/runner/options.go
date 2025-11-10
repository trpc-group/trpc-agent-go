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

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
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
}

// NewOptions creates a new options instance.
func NewOptions(opt ...Option) *Options {
	opts := &Options{
		UserIDResolver:    defaultUserIDResolver,
		TranslatorFactory: defaultTranslatorFactory,
		RunAgentInputHook: defaultRunAgentInputHook,
		RunOptionResolver: defaultRunOptionResolver,
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
type TranslatorFactory func(input *adapter.RunAgentInput) translator.Translator

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

// RunOptionResolver is a function that resolves the run options for an AG-UI run.
type RunOptionResolver func(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error)

// WithRunOptionResolver sets the run option resolver.
func WithRunOptionResolver(r RunOptionResolver) Option {
	return func(o *Options) {
		o.RunOptionResolver = r
	}
}

// defaultUserIDResolver is the default user ID resolver.
func defaultUserIDResolver(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
	return "user", nil
}

// defaultTranslatorFactory is the default translator factory.
func defaultTranslatorFactory(input *adapter.RunAgentInput) translator.Translator {
	return translator.New(input.ThreadID, input.RunID)
}

// defaultRunAgentInputHook returns the input unchanged.
func defaultRunAgentInputHook(ctx context.Context, input *adapter.RunAgentInput) (*adapter.RunAgentInput, error) {
	return input, nil
}

// defaultRunnerOptionResolver is the default runner option resolver.
func defaultRunOptionResolver(ctx context.Context, input *adapter.RunAgentInput) ([]agent.RunOption, error) {
	return nil, nil
}
