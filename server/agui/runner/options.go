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

	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/event"
)

// Options holds the options for the runner.
type Options struct {
	BridgeFactory  BridgeFactory
	UserIDResolver UserIDResolver
}

// NewOptions creates a new options instance.
func NewOptions(opt ...Option) *Options {
	opts := &Options{
		UserIDResolver: defaultUserIDResolver,
		BridgeFactory:  defaultBridgeFactory,
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

// BridgeFactory is a function that creates a bridge for an AG-UI run.
type BridgeFactory func(input *adapter.RunAgentInput) event.Bridge

// WithBridgeFactory sets the bridge factory.
func WithBridgeFactory(factory BridgeFactory) Option {
	return func(o *Options) {
		o.BridgeFactory = factory
	}
}

// defaultUserIDResolver is the default user ID resolver.
func defaultUserIDResolver(ctx context.Context, input *adapter.RunAgentInput) (string, error) {
	return "user", nil
}

// defaultBridgeFactory is the default bridge factory.
func defaultBridgeFactory(input *adapter.RunAgentInput) event.Bridge {
	return event.NewBridge(input.ThreadID, input.RunID)
}
