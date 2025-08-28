//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package redis

import (
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// ServiceOpts is the options for the redis session service.
type ServiceOpts struct {
	sessionEventLimit int
	url               string
	instanceName      string
	extraOptions      []interface{}
	// summarizerManager holds an optional in-memory summarizer manager.
	// Summary is not persisted to Redis.
	summarizerManager summary.SummarizerManager
}

// ServiceOpt is the option for the redis session service.
type ServiceOpt func(*ServiceOpts)

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.sessionEventLimit = limit
	}
}

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

// WithExtraOptions sets the extra options for the redis session service.
// this option mainly used for the customized redis client builder, it will be passed to the builder.
func WithExtraOptions(extraOptions ...interface{}) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.extraOptions = append(opts.extraOptions, extraOptions...)
	}
}

// WithSummarizerManager attaches a summarizer manager for in-memory summaries.
// The summary will not be persisted to Redis.
func WithSummarizerManager(m summary.SummarizerManager) ServiceOpt {
	return func(opts *ServiceOpts) {
		opts.summarizerManager = m
	}
}
