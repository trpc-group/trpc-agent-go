//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.

// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package inmemory

import "time"

// serviceOpts is the options for session service.
type serviceOpts struct {
	// sessionEventLimit is the limit of events in a session.
	sessionEventLimit int
	// sessionTTL is the TTL for session state and event list.
	sessionTTL time.Duration
	// appStateTTL is the TTL for app state.
	appStateTTL time.Duration
	// userStateTTL is the TTL for user state.
	userStateTTL time.Duration
	// cleanupInterval is the interval for automatic cleanup of expired data.
	// If set to 0, automatic cleanup is disabled.
	cleanupInterval time.Duration
}

// ServiceOpt is the option for the in-memory session service.
type ServiceOpt func(*serviceOpts)

// WithSessionEventLimit sets the limit of events in a session.
func WithSessionEventLimit(limit int) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.sessionEventLimit = limit
	}
}

// WithSessionTTL sets the TTL for session state and event list.
// if not set, session will expire in 30 min, set 0 will not expire.
func WithSessionTTL(ttl time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.sessionTTL = ttl
	}
}

// WithAppStateTTL sets the TTL for app state.
// If not set, app state will not expire automatically.
func WithAppStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.appStateTTL = ttl
	}
}

// WithUserStateTTL sets the TTL for user state.
// If not set, user state will not expire automatically.
func WithUserStateTTL(ttl time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.userStateTTL = ttl
	}
}

// WithCleanupInterval sets the interval for automatic cleanup of expired data.
// If set to 0, automatic cleanup will be determined based on TTL configuration.
// Default cleanup interval is 5 minutes if any TTL is configured.
func WithCleanupInterval(interval time.Duration) ServiceOpt {
	return func(opts *serviceOpts) {
		opts.cleanupInterval = interval
	}
}
