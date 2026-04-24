//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package a2a

import (
	"context"
	"errors"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const defaultBatchSize = 5
const defaultFlushInterval = 200 * time.Millisecond

// eventTunnel is the event tunnel.
// It provides a way to tunnel events from agent to a2a server.
// And aggregate events into batches to improve performance.
type eventTunnel struct {
	batchSize     int
	flushInterval time.Duration

	batch []*event.Event
	// produce runs in a dedicated goroutine and must observe ctx cancellation
	// around blocking work so the producer can stop when Run returns.
	produce func(context.Context) (*event.Event, bool)
	consume func([]*event.Event) (bool, error)

	ctx    context.Context
	cancel context.CancelFunc
}

// newEventTunnel creates a new event tunnel.
// The produce callback is invoked from a dedicated goroutine. It must return
// promptly when the provided context is done to avoid leaking that goroutine.
func newEventTunnel(
	batchSize int,
	flushInterval time.Duration,
	produce func(context.Context) (*event.Event, bool),
	consume func([]*event.Event) (bool, error),
) *eventTunnel {
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if flushInterval <= 0 {
		flushInterval = defaultFlushInterval
	}
	return &eventTunnel{
		batchSize:     batchSize,
		flushInterval: flushInterval,
		batch:         make([]*event.Event, 0, batchSize),
		produce:       produce,
		consume:       consume,
	}
}

// Run runs the event tunnel.
func (t *eventTunnel) Run(ctx context.Context) error {
	t.ctx, t.cancel = context.WithCancel(ctx)
	defer t.cancel()

	ticker := time.NewTicker(t.flushInterval)
	defer ticker.Stop()

	producedEventCh := t.startProducing()

	for {
		select {
		case <-t.ctx.Done():
			if len(t.batch) > 0 {
				if _, err := t.flushBatch(); err != nil {
					return errors.Join(
						t.ctx.Err(),
						fmt.Errorf("tunnel error during cancel flush: %w", err),
					)
				}
			}
			return t.ctx.Err()

		case produced, ok := <-producedEventCh:
			if !ok {
				return t.finalizeRun()
			}

			done, err := t.handleProducedEvent(produced)
			if err != nil {
				return err
			}
			if done {
				return nil
			}

		case <-ticker.C:
			done, err := t.flushPendingBatch("timer")
			if err != nil {
				return err
			}
			if done {
				return nil
			}
		}
	}
}

func (t *eventTunnel) startProducing() <-chan *event.Event {
	producedEventCh := make(chan *event.Event)
	go func() {
		defer close(producedEventCh)
		for {
			event, ok := t.produce(t.ctx)
			if !ok {
				return
			}

			select {
			case <-t.ctx.Done():
				return
			case producedEventCh <- event:
			}
		}
	}()
	return producedEventCh
}

func (t *eventTunnel) handleProducedEvent(produced *event.Event) (bool, error) {
	if produced == nil {
		return false, nil
	}

	t.batch = append(t.batch, produced)
	if len(t.batch) < t.batchSize {
		return false, nil
	}

	return t.flushPendingBatch("batch")
}

func (t *eventTunnel) flushPendingBatch(reason string) (bool, error) {
	if len(t.batch) == 0 {
		return false, nil
	}

	ok, err := t.flushBatch()
	if err != nil {
		return false, fmt.Errorf("tunnel error during %s flush: %w", reason, err)
	}
	return !ok, nil
}

func (t *eventTunnel) finalizeRun() error {
	ctxErr := t.ctx.Err()
	reason := "final"
	if ctxErr != nil {
		reason = "cancel"
	}

	_, err := t.flushPendingBatch(reason)
	if err != nil {
		return errors.Join(ctxErr, err)
	}
	return ctxErr
}

func (t *eventTunnel) flushBatch() (bool, error) {
	if len(t.batch) == 0 {
		return true, nil
	}

	batch := make([]*event.Event, len(t.batch))
	copy(batch, t.batch)

	t.batch = t.batch[:0]

	ok, err := t.consume(batch)
	if err != nil {
		log.Errorf("Failed to consume batch: %v", err)
		return false, err
	}

	return ok, nil
}
