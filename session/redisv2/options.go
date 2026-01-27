//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redisv2

import (
	"time"
)

// serviceOpts holds configuration for Service.
type serviceOpts struct {
	url                 string
	instanceName        string
	extraOptions        []any
	sessionTTL          time.Duration
	appStateTTL         time.Duration
	userStateTTL        time.Duration
	maxEventsPerSession int // 0 means no limit
	evictionBatchSize   int // default 10
}

// Option configures the Service.
type Option func(*serviceOpts)

const defaultEvictionBatchSize = 10

var defaultOptions = serviceOpts{
	sessionTTL:          0,
	appStateTTL:         0,
	userStateTTL:        0,
	maxEventsPerSession: 0,
	evictionBatchSize:   defaultEvictionBatchSize,
}

// WithRedisClientURL sets the Redis URL.
func WithRedisClientURL(url string) Option {
	return func(opts *serviceOpts) {
		opts.url = url
	}
}

// WithRedisInstance sets the Redis instance name.
func WithRedisInstance(instanceName string) Option {
	return func(opts *serviceOpts) {
		opts.instanceName = instanceName
	}
}

// WithExtraOptions sets extra options for redis client builder.
func WithExtraOptions(extraOptions ...any) Option {
	return func(opts *serviceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSessionTTL sets the TTL for session data.
func WithSessionTTL(ttl time.Duration) Option {
	return func(opts *serviceOpts) {
		opts.sessionTTL = ttl
	}
}

// WithAppStateTTL sets the TTL for app state.
func WithAppStateTTL(ttl time.Duration) Option {
	return func(opts *serviceOpts) {
		opts.appStateTTL = ttl
	}
}

// WithUserStateTTL sets the TTL for user state.
func WithUserStateTTL(ttl time.Duration) Option {
	return func(opts *serviceOpts) {
		opts.userStateTTL = ttl
	}
}

// WithMaxEventsPerSession sets the maximum events per session.
// When exceeded, oldest events are automatically evicted.
// 0 means no limit.
func WithMaxEventsPerSession(max int) Option {
	return func(opts *serviceOpts) {
		opts.maxEventsPerSession = max
	}
}

// WithEvictionBatchSize sets how many events to evict at once.
func WithEvictionBatchSize(size int) Option {
	return func(opts *serviceOpts) {
		if size > 0 {
			opts.evictionBatchSize = size
		}
	}
}

