//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Demonstrates a minimal custom memory.Service backed by nested maps.
// No API keys required — run with: go run ./demo
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/memory/customservice"
	"trpc.group/trpc-go/trpc-agent-go/memory"
)

func main() {
	ctx := context.Background()
	svc := customservice.NewMapService()
	defer func() {
		if err := svc.Close(); err != nil {
			log.Printf("close: %v", err)
		}
	}()

	userKey := memory.UserKey{AppName: "customservice-demo", UserID: "alice"}

	if err := svc.AddMemory(ctx, userKey, "prefers tea over coffee", []string{"preference"}); err != nil {
		log.Fatalf("add preference: %v", err)
	}

	eventTime := time.Date(2026, 7, 3, 12, 0, 0, 0, time.UTC)
	if err := svc.AddMemory(
		ctx,
		userKey,
		"team lunch downtown",
		[]string{"social"},
		memory.WithMetadata(&memory.Metadata{
			Kind:      memory.KindEpisode,
			EventTime: &eventTime,
		}),
	); err != nil {
		log.Fatalf("add episode: %v", err)
	}

	// Idempotent re-add with the same content does not create a second row.
	if err := svc.AddMemory(ctx, userKey, "prefers tea over coffee", []string{"preference"}); err != nil {
		log.Fatalf("idempotent add: %v", err)
	}

	all, err := svc.ReadMemories(ctx, userKey, 0)
	if err != nil {
		log.Fatalf("read: %v", err)
	}
	fmt.Printf("stored memories: %d\n", len(all))

	matches, err := svc.SearchMemories(
		ctx,
		userKey,
		"lunch",
		memory.WithSearchOptions(memory.SearchOptions{
			Query: "lunch",
			Kind:  memory.KindEpisode,
		}),
	)
	if err != nil {
		log.Fatalf("search: %v", err)
	}
	for _, entry := range matches {
		fmt.Printf("episode match: id=%s content=%q\n", entry.ID, entry.Memory.Memory)
	}
}
