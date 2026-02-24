package telegram

import (
	"context"
	"errors"
	"time"
)

const (
	defaultPollTimeout = 25 * time.Second
	errorBackoff       = 1 * time.Second
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
	startFromLatest bool
	handler         MessageHandler
}

// PollerOption configures a Poller.
type PollerOption func(*Poller)

// WithPollTimeout sets the long-poll timeout.
func WithPollTimeout(timeout time.Duration) PollerOption {
	return func(p *Poller) { p.timeout = timeout }
}

// WithStartFromLatest controls whether the poller drains pending
// updates on startup.
func WithStartFromLatest(enabled bool) PollerOption {
	return func(p *Poller) { p.startFromLatest = enabled }
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
		startFromLatest: true,
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
	return p, nil
}

// Run starts the polling loop and blocks until ctx is done.
func (p *Poller) Run(ctx context.Context) error {
	offset := 0
	if p.startFromLatest {
		next, err := p.bootstrapOffset(ctx, offset)
		if err != nil {
			return err
		}
		offset = next
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
			time.Sleep(errorBackoff)
			continue
		}
		if len(updates) == 0 {
			continue
		}

		for _, upd := range updates {
			if upd.UpdateID >= offset {
				offset = upd.UpdateID + 1
			}
			msg := upd.Message
			if msg == nil {
				continue
			}
			if msg.Text == "" {
				continue
			}
			if msg.From != nil && msg.From.IsBot {
				continue
			}
			if err := p.handler(ctx, *msg); err != nil {
				time.Sleep(errorBackoff)
				continue
			}
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
