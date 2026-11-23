//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package tooljournal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/providerdata"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const resumeKey = "__tool_execution_resume__"

// Claims prevents two runs on the same Runner from restoring one round from
// stale snapshots. It is intentionally not a distributed lock.
type Claims struct {
	mu     sync.Mutex
	active map[string]bool
}

func (c *Claims) acquire(id string) (func(), error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active == nil {
		c.active = make(map[string]bool)
	}
	if c.active[id] {
		return nil, errors.New("tool round is still executing in this runner")
	}
	c.active[id] = true
	var once sync.Once
	return func() { once.Do(func() { c.mu.Lock(); delete(c.active, id); c.mu.Unlock() }) }, nil
}

// ConfigureRecovery installs Runner-local claims and a fresh storage read.
func ConfigureRecovery(inv *agent.Invocation, claims *Claims, load func(context.Context) (*session.Session, error)) {
	r, _ := agent.GetStateValue[*Recorder](inv, recorderKey)
	if r == nil {
		return
	}
	r.claims = claims
	r.load = load
}

// PrepareResume acquires the round before refreshing its evidence. Completed
// calls are reused; only proven not-started calls may execute. Unknown work is
// never automatically retried, even if the previous worker is no longer alive.
func PrepareResume(ctx context.Context, inv *agent.Invocation, source event.Event, record Record) (*model.Response, error) {
	recorder, _ := agent.GetStateValue[*Recorder](inv, recorderKey)
	if recorder == nil || recorder.claims == nil || recorder.load == nil {
		return nil, errors.New("recorded tool resume requires runner execution tracking")
	}
	release, err := recorder.claims.acquire(record.ID)
	if err != nil {
		return nil, err
	}
	claimed := true
	defer func() {
		if claimed {
			release()
		}
	}()
	latest, err := recorder.load(ctx)
	if err != nil {
		return nil, fmt.Errorf("refresh tool execution evidence: %w", err)
	}
	records, err := Index(latest)
	if err != nil {
		return nil, err
	}
	fresh, ok := records[source.ID]
	if !ok || fresh.ID != record.ID {
		return nil, errors.New("tool execution evidence changed or disappeared")
	}
	var pending []model.ToolCall
	for _, call := range fresh.Calls {
		switch call.Status {
		case "completed", "failed":
			if _, err := Outcome(call); err != nil {
				return nil, err
			}
		case "not_started":
			if call.Attempts != 0 {
				return nil, errors.New("not-started tool has recorded execution attempts")
			}
			pending = append(pending, call.Tool)
		default:
			return nil, fmt.Errorf("cannot resume tool %q: outcome is %s; verify external effects before retrying", call.Tool.ID, call.Status)
		}
	}
	// Refresh the invocation's immutable state snapshot so history projects the
	// same facts used for admission, even when another run just finished.
	b, err := json.Marshal(fresh)
	if err != nil {
		return nil, err
	}
	inv.Session.SetState(Prefix+fresh.ID, b)
	if len(pending) == 0 {
		return nil, nil
	}
	if providerdata.ResponseHasData(fresh.Response) {
		return nil, errors.New("recorded native tool resume requires provider-specific recovery")
	}
	if fresh.Response == nil || len(fresh.Response.Choices) == 0 {
		return nil, errors.New("tool resume source response is missing")
	}
	response := fresh.Response.Clone()
	response.Choices[0].Message.ToolCalls = pending
	round := &Round{recorder: recorder, record: fresh, author: source.Author, filterKey: source.FilterKey, release: release,
		executionRequestID: inv.RunOptions.RequestID, executionInvocationID: inv.InvocationID}
	inv.SetState(resumeKey, round)
	claimed = false
	return response, nil
}

// End releases ownership only after the round's managed execution has exited.
func End(ctx context.Context) {
	if r := fromContext(ctx); r != nil && r.release != nil {
		r.release()
	}
}

// ReleaseResume releases a prepared round if cancellation prevented dispatch.
func ReleaseResume(inv *agent.Invocation) {
	if r, ok := agent.GetStateValue[*Round](inv, resumeKey); ok && r != nil && r.release != nil {
		r.release()
	}
	inv.DeleteState(resumeKey)
}
