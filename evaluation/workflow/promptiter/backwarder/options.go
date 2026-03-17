//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package backwarder computes backward propagation outputs from trace and gradient data.
package backwarder

// options stores optional backwarder behavior toggles.
type options struct {
}

// option mutates backwarder options during construction.
type option func(*options)

// newOptions applies all backwarder options and returns a configured options set.
func newOptions(opt ...option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}
