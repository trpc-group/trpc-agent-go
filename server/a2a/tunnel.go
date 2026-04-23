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

	batch   []*event.Event
	produce func(context.Context) (*event.Event, bool)
	consume func([]*event.Event) (bool, error)

	ctx    context.Context
	cancel context.CancelFunc
}

// newEventTunnel creates a new event tunnel.
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

type producedEvent struct {
	event *event.Event
	ok    bool
}

// Run runs the event tunnel.
func (t *eventTunnel) Run(ctx context.Context) error {
	t.ctx, t.cancel = context.WithCancel(ctx)
	defer t.cancel()

	ticker := time.NewTicker(t.flushInterval)
	defer ticker.Stop()

	producedEventCh := make(chan producedEvent)
	go func() {
		defer close(producedEventCh)
		for {
			event, ok := t.produce(t.ctx)
			select {
			case <-t.ctx.Done():
				return
			case producedEventCh <- producedEvent{event: event, ok: ok}:
			}
			if !ok {
				return
			}
		}
	}()

	for {
		select {
		case <-t.ctx.Done():
			if len(t.batch) > 0 {
				t.flushBatch()
			}
			return t.ctx.Err()

		case produced, ok := <-producedEventCh:
			if !ok {
				if len(t.batch) > 0 {
					_, err := t.flushBatch()
					if err != nil {
						return fmt.Errorf("tunnel error during final flush: %v", err)
					}
				}
				if err := t.ctx.Err(); err != nil {
					return err
				}
				return nil
			}

			if !produced.ok {
				if len(t.batch) > 0 {
					_, err := t.flushBatch()
					if err != nil {
						return fmt.Errorf("tunnel error during final flush: %v", err)
					}
				}
				return nil
			}

			if produced.event != nil {
				t.batch = append(t.batch, produced.event)
				if len(t.batch) >= t.batchSize {
					ok, err := t.flushBatch()
					if err != nil {
						return fmt.Errorf("tunnel error during batch flush: %v", err)
					}
					if !ok {
						return nil
					}
				}
			}

		case <-ticker.C:
			if len(t.batch) > 0 {
				ok, err := t.flushBatch()
				if err != nil {
					return fmt.Errorf("tunnel error during timer flush: %v", err)
				}
				if !ok {
					return nil
				}
			}
		}
	}
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
