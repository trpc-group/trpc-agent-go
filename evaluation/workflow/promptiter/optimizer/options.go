//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package optimizer transforms aggregated gradients into patch suggestions for the target prompt.
package optimizer

// options stores optional optimizer behavior flags.
type options struct {
}

// option mutates optimizer options during construction.
type option func(*options)

// newOptions applies all optimizer options and returns a finalized option set.
func newOptions(opt ...option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}
