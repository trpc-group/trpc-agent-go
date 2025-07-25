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
	produce func() (*event.Event, bool)
	consume func([]*event.Event) (bool, error)

	ctx    context.Context
	cancel context.CancelFunc
}

// newEventTunnel creates a new event tunnel.
func newEventTunnel(
	batchSize int,
	flushInterval time.Duration,
	produce func() (*event.Event, bool),
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

	for {
		select {
		case <-ctx.Done():
			if len(t.batch) > 0 {
				t.flushBatch()
			}
			return ctx.Err()

		default:
			event, ok := t.produce()
			if !ok {
				if len(t.batch) > 0 {
					_, err := t.flushBatch()
					if err != nil {
						return fmt.Errorf("tunnel error during final flush: %v", err)
					}
				}
				return nil
			}

			if event != nil {
				t.batch = append(t.batch, event)
				if len(t.batch) >= t.batchSize {
					fmt.Printf("flushing batch batch size: %d\n", len(t.batch))
					ok, err := t.flushBatch()
					if err != nil {
						return fmt.Errorf("tunnel error during batch flush: %v", err)
					}
					if !ok {
						return nil
					}
				}
			}

			select {
			case <-ticker.C:
				if len(t.batch) > 0 {
					fmt.Printf("flushing batch now %s, batch size: %d\n", time.Now().Format("2006-01-02 15:04:05.000"), len(t.batch))
					ok, err := t.flushBatch()
					if err != nil {
						return fmt.Errorf("tunnel error during timer flush: %v", err)
					}
					if !ok {
						return nil
					}
				}
			default:
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
