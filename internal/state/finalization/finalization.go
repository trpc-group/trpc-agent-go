//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package finalization coordinates acknowledged event appends for one run.
package finalization

import (
	"context"
	"errors"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
)

const stateKey = "__run_finalization__"

// Coordinator serializes event appends and retains their acknowledged outcome.
// Failed/uncertain writes are not automatically retried, even after cancellation.
type Coordinator struct {
	gate       chan struct{}
	mu         sync.Mutex
	results    map[string]error
	append     func(context.Context, *event.Event) error
	firstError error
}

// Attach installs a coordinator inherited by invocation clones.
func Attach(inv *agent.Invocation, appendEvent func(context.Context, *event.Event) error) *Coordinator {
	c := &Coordinator{gate: make(chan struct{}, 1), results: make(map[string]error), append: appendEvent}
	inv.SetState(stateKey, c)
	return c
}

// FromInvocation returns the optional run coordinator.
func FromInvocation(inv *agent.Invocation) *Coordinator {
	c, _ := agent.GetStateValue[*Coordinator](inv, stateKey)
	return c
}

// Append serializes competing normal/fallback paths. The first append's error
// remains authoritative: an error may mean the store committed before timing out.
func (c *Coordinator) Append(ctx context.Context, e *event.Event) error {
	select {
	case c.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-c.gate }()
	c.mu.Lock()
	err, ok := c.results[e.ID]
	c.mu.Unlock()
	if ok {
		return err
	}
	err = c.append(ctx, e)
	c.mu.Lock()
	c.results[e.ID] = err
	if err != nil && c.firstError == nil {
		c.firstError = err
	}
	c.mu.Unlock()
	return err
}

// Result reports actual append completion, not event-loop consumption.
func (c *Coordinator) Result(id string) (error, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	err, ok := c.results[id]
	return err, ok
}

// Err reports whether any append had a failed or uncertain acknowledgement.
func (c *Coordinator) Err() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.firstError
}

// Append uses the optional coordinator without widening SessionService.
func Append(ctx context.Context, inv *agent.Invocation, e *event.Event) (bool, error) {
	c := FromInvocation(inv)
	if c == nil {
		return false, nil
	}
	return true, c.Append(ctx, e)
}

// Acknowledged checks the storage outcome after a completion notice.
func Acknowledged(inv *agent.Invocation, id string) error {
	c := FromInvocation(inv)
	if c == nil {
		return nil
	}
	err, ok := c.Result(id)
	if !ok {
		return errors.New("event persistence was not acknowledged")
	}
	return err
}
