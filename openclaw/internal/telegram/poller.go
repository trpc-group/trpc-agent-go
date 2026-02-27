//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"context"
	"errors"
	"time"
)

const (
	defaultPollTimeout  = 25 * time.Second
	defaultErrorBackoff = 1 * time.Second
)

// UpdatesClient fetches updates from Telegram.
type UpdatesClient interface {
	GetUpdates(
		ctx context.Context,
		offset int,
		timeout time.Duration,
	) ([]Update, error)
}

// MessageHandler handles one inbound Telegram message.
type MessageHandler func(ctx context.Context, msg Message) error

// Poller consumes updates via getUpdates and calls the handler for
// each text message.
type Poller struct {
	client          UpdatesClient
	timeout         time.Duration
	backoff         time.Duration
	startFromLatest bool
	offsetStore     OffsetStore
	onError         func(error)
	handler         MessageHandler
}

// PollerOption configures a Poller.
type PollerOption func(*Poller)

// WithPollTimeout sets the long-poll timeout.
func WithPollTimeout(timeout time.Duration) PollerOption {
	return func(p *Poller) { p.timeout = timeout }
}

// WithErrorBackoff sets the backoff delay after polling/handler errors.
func WithErrorBackoff(backoff time.Duration) PollerOption {
	return func(p *Poller) { p.backoff = backoff }
}

// WithStartFromLatest controls whether the poller drains pending
// updates on startup.
func WithStartFromLatest(enabled bool) PollerOption {
	return func(p *Poller) { p.startFromLatest = enabled }
}

// WithOffsetStore enables persisting polling offsets.
func WithOffsetStore(store OffsetStore) PollerOption {
	return func(p *Poller) { p.offsetStore = store }
}

// WithOnError registers a callback for non-fatal errors.
func WithOnError(onError func(error)) PollerOption {
	return func(p *Poller) { p.onError = onError }
}

// WithMessageHandler sets the message handler.
func WithMessageHandler(h MessageHandler) PollerOption {
	return func(p *Poller) { p.handler = h }
}

// NewPoller creates a poller.
func NewPoller(client UpdatesClient, opts ...PollerOption) (*Poller, error) {
	if client == nil {
		return nil, errors.New("telegram: nil updates client")
	}
	p := &Poller{
		client:          client,
		timeout:         defaultPollTimeout,
		backoff:         defaultErrorBackoff,
		startFromLatest: true,
		onError:         func(error) {},
	}
	for _, opt := range opts {
		opt(p)
	}
	if p.handler == nil {
		return nil, errors.New("telegram: nil message handler")
	}
	if p.timeout < 0 {
		return nil, errors.New("telegram: negative poll timeout")
	}
	if p.backoff < 0 {
		return nil, errors.New("telegram: negative error backoff")
	}
	if p.onError == nil {
		return nil, errors.New("telegram: nil onError callback")
	}
	return p, nil
}

// Run starts the polling loop and blocks until ctx is done.
func (p *Poller) Run(ctx context.Context) error {
	offset := 0
	hasStoredOffset := false
	if p.offsetStore != nil {
		stored, ok, err := p.offsetStore.Read(ctx)
		if err != nil {
			return err
		}
		if ok {
			offset = stored
			hasStoredOffset = true
		}
	}

	if !hasStoredOffset && p.startFromLatest {
		next, err := p.bootstrapOffset(ctx, offset)
		if err != nil {
			return err
		}
		offset = next
		p.persistOffset(ctx, offset)
	}

	for {
		if err := ctx.Err(); err != nil {
			return nil
		}

		updates, err := p.client.GetUpdates(ctx, offset, p.timeout)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			p.onError(err)
			if !sleepWithContext(ctx, p.backoff) {
				return nil
			}
			continue
		}
		if len(updates) == 0 {
			continue
		}

		for _, upd := range updates {
			nextOffset := offset
			if upd.UpdateID >= offset {
				nextOffset = upd.UpdateID + 1
			}
			msg := upd.Message
			if msg == nil {
				offset = nextOffset
				p.persistOffset(ctx, offset)
				continue
			}
			if msg.Text == "" {
				offset = nextOffset
				p.persistOffset(ctx, offset)
				continue
			}
			if msg.From != nil && msg.From.IsBot {
				offset = nextOffset
				p.persistOffset(ctx, offset)
				continue
			}
			if err := p.handler(ctx, *msg); err != nil {
				p.onError(err)
				if !sleepWithContext(ctx, p.backoff) {
					return nil
				}
				break
			}
			offset = nextOffset
			p.persistOffset(ctx, offset)
		}
	}
}

func (p *Poller) bootstrapOffset(
	ctx context.Context,
	offset int,
) (int, error) {
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		updates, err := p.client.GetUpdates(ctx, offset, 0)
		if err != nil {
			return 0, err
		}
		if len(updates) == 0 {
			return offset, nil
		}
		for _, upd := range updates {
			if upd.UpdateID >= offset {
				offset = upd.UpdateID + 1
			}
		}
	}
}

func (p *Poller) persistOffset(ctx context.Context, offset int) {
	if p.offsetStore == nil {
		return
	}
	if err := p.offsetStore.Write(ctx, offset); err != nil {
		p.onError(err)
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
