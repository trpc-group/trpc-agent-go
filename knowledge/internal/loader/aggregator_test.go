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
	ag := NewAggregator(buckets /*showStats*/, true /*showProgress*/, true /*step*/, 2, nil /*onProgress*/)

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

// TestAggregator_ProgressCallback verifies that when a progress callback is
// provided, it is invoked for progress events at step boundaries.
func TestAggregator_ProgressCallback(t *testing.T) {
	buckets := []int{10, 100}
	var got []ProgEvent
	var elapsed, eta time.Duration
	onProgress := func(ev ProgEvent, el, e time.Duration) {
		got = append(got, ev)
		elapsed = el
		eta = e
	}

	ag := NewAggregator(buckets, false /*showStats*/, false /*showProgress*/, 2 /*step*/, onProgress)

	ag.StatCh() <- StatEvent{Size: 1}
	ag.ProgCh() <- ProgEvent{SrcName: "s1", SrcProcessed: 1, SrcTotal: 4, SrcIndex: 1, SrcTotalCount: 1}
	ag.ProgCh() <- ProgEvent{SrcName: "s1", SrcProcessed: 2, SrcTotal: 4, SrcIndex: 1, SrcTotalCount: 1}
	ag.ProgCh() <- ProgEvent{SrcName: "s1", SrcProcessed: 3, SrcTotal: 4, SrcIndex: 1, SrcTotalCount: 1}
	ag.ProgCh() <- ProgEvent{SrcName: "s1", SrcProcessed: 4, SrcTotal: 4, SrcIndex: 1, SrcTotalCount: 1}

	ag.Close()

	// Step=2: callback at 2 and 4 (and possibly at 4 as "last doc").
	if len(got) < 2 {
		t.Fatalf("expected at least 2 progress callbacks, got %d", len(got))
	}
	if got[0].SrcProcessed != 2 || got[0].SrcTotal != 4 {
		t.Errorf("first callback: want SrcProcessed=2 SrcTotal=4, got %d/%d", got[0].SrcProcessed, got[0].SrcTotal)
	}
	// Elapsed/ETA are best-effort; just ensure we got some callbacks.
	_ = elapsed
	_ = eta
}
