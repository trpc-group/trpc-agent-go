//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package hook provides internal hook execution utilities for session services.
package hook

import (
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// RunAppendEventHooks executes AppendEvent hooks chain.
// The final hook performs the actual storage operation.
func RunAppendEventHooks(
	hooks []session.AppendEventHook,
	ctx *session.AppendEventContext,
	final session.AppendEventHook,
) error {
	// Wrap final as a hook that ignores next (it's the terminal)
	allHooks := make([]session.AppendEventHook, 0, len(hooks)+1)
	allHooks = append(allHooks, hooks...)
	if final != nil {
		allHooks = append(allHooks, final)
	}

	if len(allHooks) == 0 {
		return nil
	}

	var run func(idx int) error
	run = func(idx int) error {
		if idx >= len(allHooks) {
			return nil
		}
		return allHooks[idx](ctx, func() error { return run(idx + 1) })
	}
	return run(0)
}

// RunGetSessionHooks executes GetSession hooks chain.
// The final hook performs the actual storage retrieval.
func RunGetSessionHooks(
	hooks []session.GetSessionHook,
	ctx *session.GetSessionContext,
	final session.GetSessionHook,
) (*session.Session, error) {
	// Wrap final as a hook that ignores next (it's the terminal)
	allHooks := make([]session.GetSessionHook, 0, len(hooks)+1)
	allHooks = append(allHooks, hooks...)
	if final != nil {
		allHooks = append(allHooks, final)
	}

	if len(allHooks) == 0 {
		return nil, nil
	}

	var run func(idx int) (*session.Session, error)
	run = func(idx int) (*session.Session, error) {
		if idx >= len(allHooks) {
			return nil, nil
		}
		return allHooks[idx](ctx, func() (*session.Session, error) { return run(idx + 1) })
	}
	return run(0)
}
