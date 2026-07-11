package memoryharness

import (
	"context"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/scenario"
)

// Result contains the observable output of one memory replay.
type Result struct {
	Read   []*memory.Entry
	Search []*memory.Entry
}

// Run replays a memory case against one backend.
func Run(
	ctx context.Context,
	svc memory.Service,
	c *scenario.MemoryCase,
) (*Result, error) {
	if svc == nil {
		return nil, fmt.Errorf("memory service is nil")
	}
	if c == nil {
		return nil, fmt.Errorf("memory case is nil")
	}

	userKey := memory.UserKey{
		AppName: "replaytest",
		UserID:  "user01",
	}
	for i, write := range c.Writes {
		metadata := &memory.Metadata{
			Kind:         write.Kind,
			EventTime:    write.EventTime,
			Participants: write.Participants,
			Location:     write.Location,
		}
		if err := svc.AddMemory(
			ctx,
			userKey,
			write.Content,
			write.Topics,
			memory.WithMetadata(metadata),
		); err != nil {
			return nil, fmt.Errorf("write[%d] memory: %w", i, err)
		}
	}

	read, err := svc.ReadMemories(ctx, userKey, 100)
	if err != nil {
		return nil, fmt.Errorf("read memories: %w", err)
	}
	search, err := svc.SearchMemories(ctx, userKey, c.SearchQuery)
	if err != nil {
		return nil, fmt.Errorf("search memories: %w", err)
	}
	return &Result{Read: read, Search: search}, nil
}
