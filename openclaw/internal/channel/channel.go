package channel

import "context"

// Channel represents one external surface (Telegram, Slack, etc.) that
// can receive inbound messages and deliver replies.
type Channel interface {
	// ID returns a stable channel identifier.
	ID() string

	// Run blocks until ctx is done or an unrecoverable error happens.
	Run(ctx context.Context) error
}
