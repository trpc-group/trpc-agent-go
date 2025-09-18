package service

import "context"

// Service captures the minimal lifecycle hooks an AG-UI transport must implement.
type Service interface {
	// Serve starts the transport and blocks until it stops or ctx is cancelled.
	Serve(ctx context.Context) error

	// Close should gracefully release transport resources; it must be idempotent.
	Close(ctx context.Context) error
}
