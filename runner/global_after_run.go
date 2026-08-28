//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sync"

	oteltrace "go.opentelemetry.io/otel/trace"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/plugin"
)

var (
	errGlobalAfterRunHookNameEmpty = errors.New(
		"runner: global after-run hook name is empty",
	)
	errGlobalAfterRunHookNil = errors.New(
		"runner: global after-run hook is nil",
	)
)

type namedGlobalAfterRunHook struct {
	name string
	hook plugin.AfterRunHook
}

var globalAfterRunHooks = struct {
	sync.RWMutex
	hooks []namedGlobalAfterRunHook
	names map[string]struct{}
}{}

// RegisterGlobalAfterRunHook registers a process-wide hook that observes
// completed Runner runs.
//
// Hooks are identified by a non-empty, process-unique name. Registration is
// permanent for the process lifetime and affects runs that start after the
// registration completes, including runs on existing Runner instances. A hook
// must be safe for concurrent use and must treat the supplied Invocation as
// read-only. Each hook receives its own snapshot of the finalized completion
// event. Process-wide hooks run after Runner-scoped and per-run plugin AfterRun
// hooks. Runner invokes hooks synchronously, so they must return promptly and
// move blocking work to infrastructure they own. Hook errors and panics are
// logged and do not change Runner output.
//
// The hook context is detached from run cancellation and contains the root
// invoke_agent SpanContext when framework tracing captured a recording root
// span. When no root SpanContext was captured, the context contains an invalid
// SpanContext rather than inheriting an unrelated caller span.
//
// The registering component owns the hook and any resources it uses. Runner
// does not close process-wide hooks. RegisterGlobalAfterRunHook returns an
// error for an empty name, a nil hook, or a duplicate name.
func RegisterGlobalAfterRunHook(
	name string,
	hook plugin.AfterRunHook,
) error {
	if name == "" {
		return errGlobalAfterRunHookNameEmpty
	}
	if hook == nil {
		return errGlobalAfterRunHookNil
	}

	globalAfterRunHooks.Lock()
	defer globalAfterRunHooks.Unlock()
	if globalAfterRunHooks.names == nil {
		globalAfterRunHooks.names = make(map[string]struct{})
	}
	if _, ok := globalAfterRunHooks.names[name]; ok {
		return fmt.Errorf("runner: duplicate global after-run hook %q", name)
	}
	globalAfterRunHooks.names[name] = struct{}{}
	globalAfterRunHooks.hooks = append(
		globalAfterRunHooks.hooks,
		namedGlobalAfterRunHook{name: name, hook: hook},
	)
	return nil
}

func snapshotGlobalAfterRunHooks() []namedGlobalAfterRunHook {
	globalAfterRunHooks.RLock()
	defer globalAfterRunHooks.RUnlock()
	return append([]namedGlobalAfterRunHook(nil), globalAfterRunHooks.hooks...)
}

type globalAfterRunState struct {
	hooks       []namedGlobalAfterRunHook
	spanContext rootSpanContextCapture
}

func prepareGlobalAfterRunState() *globalAfterRunState {
	hooks := snapshotGlobalAfterRunHooks()
	if len(hooks) == 0 {
		return nil
	}
	return &globalAfterRunState{hooks: hooks}
}

func (s *globalAfterRunState) attach(invocation *agent.Invocation) {
	if s == nil || invocation == nil {
		return
	}
	invocation.RunOptions.TraceStartedCallbacks = append(
		invocation.RunOptions.TraceStartedCallbacks,
		s.spanContext.store,
	)
}

type rootSpanContextCapture struct {
	mu          sync.RWMutex
	spanContext oteltrace.SpanContext
}

func (c *rootSpanContextCapture) store(spanContext oteltrace.SpanContext) {
	if c == nil || !spanContext.IsValid() {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.spanContext.IsValid() {
		return
	}
	c.spanContext = spanContext
}

func (c *rootSpanContextCapture) load() oteltrace.SpanContext {
	if c == nil {
		return oteltrace.SpanContext{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.spanContext
}

func applyGlobalAfterRunHooks(
	ctx context.Context,
	state *globalAfterRunState,
	invocation *agent.Invocation,
	completionEvent *event.Event,
) {
	if state == nil || invocation == nil || completionEvent == nil {
		return
	}
	hookCtx := oteltrace.ContextWithSpanContext(
		context.WithoutCancel(ctx),
		state.spanContext.load(),
	)
	for _, registered := range state.hooks {
		completionSnapshot := completionEvent.Clone()
		if completionSnapshot != nil {
			completionSnapshot.ID = completionEvent.ID
		}
		invokeGlobalAfterRunHook(
			hookCtx,
			registered,
			&plugin.AfterRunArgs{
				Invocation:      invocation,
				CompletionEvent: completionSnapshot,
			},
		)
	}
}

func invokeGlobalAfterRunHook(
	ctx context.Context,
	registered namedGlobalAfterRunHook,
	args *plugin.AfterRunArgs,
) {
	defer func() {
		if recovered := recover(); recovered != nil {
			log.ErrorfContext(
				ctx,
				log.PanicPrefix+" global after-run hook %q panicked: %v\n%s",
				registered.name,
				recovered,
				string(debug.Stack()),
			)
		}
	}()
	if err := registered.hook(ctx, args); err != nil {
		log.ErrorfContext(
			ctx,
			"global after-run hook %q failed: %v",
			registered.name,
			err,
		)
	}
}
