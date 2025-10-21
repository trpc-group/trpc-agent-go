//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package loader

import (
	"testing"
	"time"
)

// TestAggregator_Smoke exercises the aggregator happy path without asserting on log output.
// It ensures the goroutine consumes events and Close terminates promptly.
func TestAggregator_Smoke(t *testing.T) {
	buckets := []int{10, 100}
	ag := NewAggregator(buckets /*showStats*/, true /*showProgress*/, true /*step*/, 2)

	// Send a few stat events to populate stats and trigger Stats.Log on shutdown.
	ag.StatCh() <- StatEvent{Size: 1}
	ag.StatCh() <- StatEvent{Size: 5}
	ag.StatCh() <- StatEvent{Size: 42}

	// Progress events: only some will pass the modulo gate, but we don't assert logs.
	ag.ProgCh() <- ProgEvent{SrcName: "srcA", SrcProcessed: 1, SrcTotal: 3}
	ag.ProgCh() <- ProgEvent{SrcName: "srcA", SrcProcessed: 2, SrcTotal: 3}
	ag.ProgCh() <- ProgEvent{SrcName: "srcA", SrcProcessed: 3, SrcTotal: 3}

	// Close should not block for long – the goroutine exits after channels close.
	done := make(chan struct{})
	go func() {
		ag.Close()
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(3 * time.Second):
		t.Fatal("aggregator Close timed out")
	}
}
