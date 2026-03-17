//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package engine implements PromptIter orchestration and runtime flow for a generation round.
package engine

// options stores future extension points for engine construction.
type options struct {
}

// option allows optional extension points when building engine instances.
type option func(*options)

// newOptions applies option functions and returns finalized engine options.
func newOptions(opt ...option) *options {
	opts := &options{}
	for _, o := range opt {
		o(opts)
	}
	return opts
}
