//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package a2ui

import (
	"context"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
)

// Option configures A2UI translator behavior.
type Option func(*options)

// PassThroughEventHook decides whether a default event should be passed through.
type PassThroughEventHook func(ctx context.Context, event aguievents.Event) bool

type options struct {
	passThroughEventHook PassThroughEventHook
}

func newOptions(opt ...Option) options {
	opts := options{}
	for _, o := range opt {
		o(&opts)
	}
	return opts
}

// WithPassThroughEventHook sets a hook that decides whether default events should be passed through.
func WithPassThroughEventHook(hook PassThroughEventHook) Option {
	return func(opts *options) {
		opts.passThroughEventHook = hook
	}
}
