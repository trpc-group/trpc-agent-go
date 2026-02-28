//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type ctxKeySessionID struct{}
type ctxKeyTurnIndex struct{}

type Tokens struct {
	Prompt     int `json:"prompt"`
	Completion int `json:"completion"`
	Total      int `json:"total"`
}

func (t *Tokens) AddUsage(u *model.Usage) {
	if u == nil {
		return
	}
	t.Prompt += u.PromptTokens
	t.Completion += u.CompletionTokens
	t.Total += u.TotalTokens
}

type TurnStats struct {
	Chat       Tokens
	ToolSearch Tokens
	Duration   time.Duration
}

type Collector struct {
	mu       sync.Mutex
	sessions map[string]*SessionStats
}

type SessionStats struct {
	NextTurn int
	Turns    map[int]*TurnStats
}

func NewCollector() *Collector {
	return &Collector{sessions: make(map[string]*SessionStats)}
}

func (c *Collector) ensureSession(sessionID string) *SessionStats {
	s, ok := c.sessions[sessionID]
	if !ok {
		s = &SessionStats{Turns: make(map[int]*TurnStats)}
		c.sessions[sessionID] = s
	}
	return s
}

func (c *Collector) nextTurn(sessionID string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.ensureSession(sessionID)
	s.NextTurn++
	idx := s.NextTurn
	if _, ok := s.Turns[idx]; !ok {
		s.Turns[idx] = &TurnStats{}
	}
	return idx
}

func (c *Collector) AddChatUsage(sessionID string, turnIndex int, usage *model.Usage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.ensureSession(sessionID)
	if _, ok := s.Turns[turnIndex]; !ok {
		s.Turns[turnIndex] = &TurnStats{}
	}
	s.Turns[turnIndex].Chat.AddUsage(usage)
}

func (c *Collector) AddToolSearchUsage(sessionID string, turnIndex int, usage *model.Usage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.ensureSession(sessionID)
	if _, ok := s.Turns[turnIndex]; !ok {
		s.Turns[turnIndex] = &TurnStats{}
	}
	s.Turns[turnIndex].ToolSearch.AddUsage(usage)
}

func (c *Collector) SetTurnDuration(sessionID string, turnIndex int, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.ensureSession(sessionID)
	if _, ok := s.Turns[turnIndex]; !ok {
		s.Turns[turnIndex] = &TurnStats{}
	}
	s.Turns[turnIndex].Duration = d
}

func (c *Collector) Snapshot(sessionID string) map[int]TurnStats {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.sessions[sessionID]
	out := make(map[int]TurnStats)
	if s == nil {
		return out
	}
	for k, v := range s.Turns {
		if v == nil {
			continue
		}
		out[k] = *v
	}
	return out
}

// CountingRunner wraps a runner and collects per-turn token usage and duration.
// Token counting rule: only count events with Usage and !IsPartial to avoid streaming double-count.
type CountingRunner struct {
	base      runner.Runner
	collector *Collector
}

func NewCountingRunner(base runner.Runner, collector *Collector) *CountingRunner {
	return &CountingRunner{base: base, collector: collector}
}

func (r *CountingRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	turnIndex := r.collector.nextTurn(sessionID)
	ctx = context.WithValue(ctx, ctxKeySessionID{}, sessionID)
	ctx = context.WithValue(ctx, ctxKeyTurnIndex{}, turnIndex)

	start := time.Now()
	ch, err := r.base.Run(ctx, userID, sessionID, message, runOpts...)
	if err != nil {
		return nil, err
	}

	out := make(chan *event.Event)
	go func() {
		defer close(out)
		defer func() {
			r.collector.SetTurnDuration(sessionID, turnIndex, time.Since(start))
		}()
		for evt := range ch {
			if evt != nil && evt.Response != nil && evt.Response.Usage != nil && !evt.Response.IsPartial {
				r.collector.AddChatUsage(sessionID, turnIndex, evt.Response.Usage)
			}
			out <- evt
		}
	}()

	return out, nil
}

func (r *CountingRunner) Close() error { return r.base.Close() }

func sessionTurnFromContext(ctx context.Context) (sessionID string, turnIndex int, ok bool) {
	s, okS := ctx.Value(ctxKeySessionID{}).(string)
	ti, okT := ctx.Value(ctxKeyTurnIndex{}).(int)
	if !okS || !okT {
		return "", 0, false
	}
	return s, ti, true
}
