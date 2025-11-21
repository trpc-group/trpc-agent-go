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

var (
	defaultOptions = ServiceOpts{}
)

// ServiceOpts is the options for the redis checkpoint service.
type ServiceOpts struct {
	url          string
	instanceName string
	extraOptions []any
}

// ServiceOpt is the option for the redis checkpoint service.
type ServiceOpt func(*ServiceOpts)

// WithRedisClientURL creates a redis client from URL and sets it to the service.
func WithRedisClientURL(url string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.url = url
	}
}

// WithRedisInstance uses a redis instance from storage.
// Note: WithRedisClientURL has higher priority than WithRedisInstance.
// If both are specified, WithRedisClientURL will be used.
func WithRedisInstance(instanceName string) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.instanceName = instanceName
	}
}

// WithExtraOptions sets the extra options for the redis checkpoint service.
// this option mainly used for the customized redis client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...any) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}
