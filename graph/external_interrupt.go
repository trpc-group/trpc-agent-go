//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package graph

import (
	"context"
	"sync"
	"time"
)

type graphInterruptKey struct{}

type graphInterruptState struct {
	mu      sync.RWMutex
	timeout *time.Duration

	done chan struct{}
	once sync.Once
}

type graphInterruptOptions struct {
	timeout *time.Duration
}

// GraphInterruptOption configures behavior for WithGraphInterrupt().
type GraphInterruptOption func(*graphInterruptOptions)

// WithGraphInterruptTimeout specifies the max waiting time before forcing an
// interrupt. After the timeout the executor will cancel in-flight work and
// interrupt as soon as it can.
func WithGraphInterruptTimeout(
	timeout time.Duration,
) GraphInterruptOption {
	return func(o *graphInterruptOptions) {
		if o == nil {
			return
		}
		o.timeout = &timeout
	}
}

// WithGraphInterrupt creates a context that can be interrupted externally.
//
// When the returned context is used to execute a graph, calling the returned
// interrupt function requests the run to pause and save an interrupt
// checkpoint.
//
// By default the executor waits for the current step's tasks to finish and
// interrupts before starting the next step. When WithGraphInterruptTimeout is
// provided, the executor will cancel in-flight tasks after the timeout.
func WithGraphInterrupt(
	parent context.Context,
) (
	ctx context.Context,
	interrupt func(opts ...GraphInterruptOption),
) {
	st := &graphInterruptState{
		done: make(chan struct{}),
	}
	ctx = context.WithValue(parent, graphInterruptKey{}, st)

	interrupt = func(opts ...GraphInterruptOption) {
		o := &graphInterruptOptions{}
		for _, opt := range opts {
			if opt == nil {
				continue
			}
			opt(o)
		}
		st.once.Do(func() {
			st.mu.Lock()
			st.timeout = o.timeout
			st.mu.Unlock()
			close(st.done)
		})
	}
	return ctx, interrupt
}

func graphInterruptFromContext(
	ctx context.Context,
) *graphInterruptState {
	if ctx == nil {
		return nil
	}
	v, ok := ctx.Value(graphInterruptKey{}).(*graphInterruptState)
	if !ok {
		return nil
	}
	return v
}

func (s *graphInterruptState) requested() bool {
	if s == nil {
		return false
	}
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

func (s *graphInterruptState) timeoutOrNil() *time.Duration {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.timeout
}

func (s *graphInterruptState) doneCh() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.done
}
