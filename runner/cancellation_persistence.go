//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package runner

import (
	"context"
	"encoding/json"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/internal/tooljournal"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// The gate covers session restoration and framework appends on this Runner.
// It is not a distributed store transaction or a lock on user-owned objects.
type cancellationPersistence struct {
	gate        chan struct{}
	generations map[session.Key]string
}

func (r *runner) cancellationPersistence() *cancellationPersistence {
	r.persistenceOnce.Do(func() {
		r.persistence = &cancellationPersistence{gate: make(chan struct{}, 1), generations: make(map[session.Key]string)}
	})
	return r.persistence
}

func (p *cancellationPersistence) lock(ctx context.Context) error {
	select {
	case p.gate <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *cancellationPersistence) unlock() { <-p.gate }

func (r *runner) restoreWithGeneration(ctx context.Context, ro agent.RunOptions, key session.Key) (*session.Session, string, error) {
	if !r.toolExecutionTracking {
		s, e := r.getOrCreateSession(ctx, ro, key)
		return s, "", e
	}
	p := r.cancellationPersistence()
	if err := p.lock(ctx); err != nil {
		return nil, "", err
	}
	defer p.unlock()
	generation := uuid.NewString()
	p.generations[key] = generation
	s, err := r.getOrCreateSession(ctx, ro, key)
	return s, generation, err
}

func (r *runner) appendWithGeneration(ctx context.Context, sess *session.Session, e *event.Event, generation string) error {
	if generation == "" {
		return r.sessionService.AppendEvent(ctx, sess, e)
	}
	p := r.cancellationPersistence()
	if err := p.lock(ctx); err != nil {
		return err
	}
	defer p.unlock()
	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	current, known := p.generations[key]
	if !known {
		p.generations[key] = generation
		current = generation
	}
	if current != generation && len(e.StateDelta) > 0 {
		// Retain the real result and its original identity. Stale application state
		// is audit metadata; unique journal keys can still record the old outcome.
		clone := e.Clone()
		clone.ID = e.ID
		clone.StateDelta = make(map[string][]byte)
		original := make(map[string][]byte)
		for key, value := range e.StateDelta {
			if strings.HasPrefix(key, tooljournal.Prefix) {
				clone.StateDelta[key] = value
			} else {
				original[key] = value
			}
		}
		if len(original) > 0 {
			data, err := json.Marshal(original)
			if err != nil {
				return err
			}
			if clone.Extensions == nil {
				clone.Extensions = make(map[string]json.RawMessage)
			}
			clone.Extensions["trpc_agent.stale_state_delta.v1"] = data
		}
		e = clone
	}
	return r.sessionService.AppendEvent(ctx, sess, e)
}
