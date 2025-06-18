// Package agent provides the core agent functionality.
package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/core/event"
)

// Agent is the interface that all agents must implement.
type Agent interface {
	Run(ctx context.Context, invocation *Invocation) (<-chan *event.Event, error)
}
