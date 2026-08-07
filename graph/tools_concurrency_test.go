//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package graph

import (
	"context"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// objectingTool declines to share a turn, as the transfer and await_user_reply
// tools do.
type objectingTool struct{ *blockingTool }

func (objectingTool) IsConcurrencySafe() bool { return false }

// WithEnableParallelTools must honor tool.ConcurrencyAware.
//
// tool.IsConcurrencySafe is a framework-wide contract, and a Tools node is the
// second scheduler that can run a tool concurrently. Without this check the
// exported guarantee would hold only on the LLMAgent path, and a tool that
// declared it cannot share a turn would still be launched in its own goroutine
// here.
func TestProcessToolCallsHonorsConcurrencyObjection(t *testing.T) {
	started := make(chan string, 2)
	allowA := make(chan struct{})
	allowB := make(chan struct{})

	tools := map[string]tool.Tool{
		"A": &blockingTool{
			name: "A", startedCh: started, proceedCh: allowA,
			result: map[string]string{"v": "A"},
		},
		"B": objectingTool{&blockingTool{
			name: "B", startedCh: started, proceedCh: allowB,
			result: map[string]string{"v": "B"},
		}},
	}

	done := make(chan []model.Message, 1)
	go func() {
		msgs, err := processToolCalls(context.Background(), toolCallsConfig{
			ToolCalls:      makeToolCalls("A", "B"),
			Tools:          tools,
			InvocationID:   "inv",
			State:          State{},
			EnableParallel: true,
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		done <- msgs
	}()

	// A runs first and B must not have started, which is only true of the serial
	// path — the parallel path launches every call up front.
	select {
	case got := <-started:
		if got != "A" {
			t.Fatalf("first started tool = %s, want A", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the first tool to start")
	}
	select {
	case got := <-started:
		t.Fatalf("%s started beside A despite objecting to a shared turn", got)
	case <-time.After(100 * time.Millisecond):
	}

	close(allowA)
	select {
	case got := <-started:
		if got != "B" {
			t.Fatalf("second started tool = %s, want B", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for the second tool to start")
	}
	close(allowB)

	msgs := <-done
	if len(msgs) != 2 {
		t.Fatalf("messages length = %d, want 2", len(msgs))
	}
	if msgs[0].ToolName != "A" || msgs[1].ToolName != "B" {
		t.Fatalf("order not preserved: %s then %s", msgs[0].ToolName, msgs[1].ToolName)
	}
}

// An objection from one tool must not disable parallelism for batches that do
// not contain it.
func TestProcessToolCallsAdmitsUnobjectingBatch(t *testing.T) {
	tools := map[string]tool.Tool{
		"A": &blockingTool{name: "A"},
		"B": &blockingTool{name: "B"},
	}
	if !admitsConcurrentToolCalls(makeToolCalls("A", "B"), tools) {
		t.Error("a batch of tools that publish nothing must stay admissible")
	}
	// A name the node cannot resolve produces a terminal error result rather than
	// executing, so it cannot constrain its siblings.
	if !admitsConcurrentToolCalls(makeToolCalls("A", "missing"), tools) {
		t.Error("an unresolvable name must not disqualify the batch")
	}
}
