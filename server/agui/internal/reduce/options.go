//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reduce

// Option configures how AG-UI track events are reduced into snapshot messages.
type Option func(*options)

type options struct {
	includeRunLifecycleEvents bool
}

// WithRunLifecycleEvents controls whether RUN_* lifecycle events are included in the
// message snapshot as activity messages.
func WithRunLifecycleEvents(include bool) Option {
	return func(o *options) {
		o.includeRunLifecycleEvents = include
	}
}
