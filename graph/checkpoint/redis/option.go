//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redis provides Redis-based checkpoint storage implementation
// for graph execution state persistence and recovery.
package redis

import "time"

const (
	defaultTTL = time.Hour * 24 * 7 // 7 days
)

var (
	defaultOptions = Options{
		ttl: defaultTTL,
	}
)

// Options is the options for the redis checkpoint service.
type Options struct {
	url          string
	instanceName string
	extraOptions []any
	ttl          time.Duration
}

// ServiceOpt is the option for the redis checkpoint service.
type Option func(*Options)

// WithRedisClientURL creates a redis client from URL and sets it to the service.
func WithRedisClientURL(url string) Option {
	return func(opts *Options) {
		opts.url = url
	}
}

// WithRedisInstance uses a redis instance from storage.
// Note: WithRedisClientURL has higher priority than WithRedisInstance.
// If both are specified, WithRedisClientURL will be used.
func WithRedisInstance(instanceName string) Option {
	return func(opts *Options) {
		opts.instanceName = instanceName
	}
}

// WithExtraOptions sets the extra options for the redis checkpoint service.
// this option mainly used for the customized redis client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) Option {
	return func(opts *Options) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithTTL sets the TTL for the checkpoint data in redis.
func WithTTL(ttl time.Duration) Option {
	return func(opts *Options) {
		if ttl <= 0 {
			ttl = defaultTTL
		}
		opts.ttl = ttl
	}
}
