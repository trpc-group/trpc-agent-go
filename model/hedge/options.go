//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package hedge

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const defaultDelay = 100 * time.Millisecond

type options struct {
	candidates []model.Model
	name       string
	delay      time.Duration
	delays     []time.Duration
}

func newOptions(opt ...Option) options {
	opts := options{
		delay: defaultDelay,
	}
	for _, o := range opt {
		o(&opts)
	}
	return opts
}

// Option configures a hedge model.
type Option func(*options)

// WithCandidates appends hedge candidates in launch order.
// Multiple calls accumulate candidates instead of replacing them.
func WithCandidates(candidates ...model.Model) Option {
	return func(o *options) {
		o.candidates = append(o.candidates, candidates...)
	}
}

// WithName sets a stable logical model name for the hedge wrapper.
func WithName(name string) Option {
	return func(o *options) {
		o.name = name
	}
}

// WithDelay sets a fixed interval between successive hedge launches.
func WithDelay(delay time.Duration) Option {
	return func(o *options) {
		o.delay = delay
	}
}

// WithDelays sets absolute launch offsets for candidates[1:].
func WithDelays(delays ...time.Duration) Option {
	return func(o *options) {
		o.delays = make([]time.Duration, len(delays))
		copy(o.delays, delays)
	}
}
