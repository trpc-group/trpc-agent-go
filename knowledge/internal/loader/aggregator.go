//
// Tencent is pleased to support the open source community by making tRPC available.
//
// Copyright (C) 2025 Tencent.
// All rights reserved.
//
// If you have downloaded a copy of the tRPC source code from Tencent,
// please note that tRPC source code is licensed under the  Apache 2.0 License,
// A copy of the Apache 2.0 License is included in this file.
//
//

// Package loader contains internal helper utilities for concurrent knowledge
// base loading. Aggregator collects progress and statistics events from loader
// goroutines and logs them in a single place.

package loader

import (
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
)

const (
	// chanBufferSize is the default buffer size for aggregator channels.
	chanBufferSize = 1024
	// heartbeatInterval defines how often a heartbeat message is emitted
	// when the loader is still running.
	heartbeatInterval = 30 * time.Second
)

// StatEvent represents a single size statistic to be aggregated.
type StatEvent struct {
	Size int
}

// ProgEvent is emitted every time a document is processed.
type ProgEvent struct {
	SrcName      string
	SrcProcessed int
	SrcTotal     int
}

// Aggregator centralises statistics collection and progress logging. It owns
// all mutable state so callers do not need explicit locking – they simply send
// events over the provided channels.
type Aggregator struct {
	statCh chan StatEvent
	progCh chan ProgEvent
	done   chan struct{}
}

// NewAggregator starts a background goroutine that consumes events and logs
// according to the provided configuration. Call Close to flush and print the
// final statistics.
func NewAggregator(
	buckets []int,
	showStats bool,
	showProgress bool,
	step int,
) *Aggregator {
	ag := &Aggregator{
		statCh: make(chan StatEvent, chanBufferSize),
		progCh: make(chan ProgEvent, chanBufferSize),
		done:   make(chan struct{}),
	}

	go func() {
		defer close(ag.done)

		stats := NewStats(buckets)
		// Map to track per-source progress so we can decide whether to log.
		lastLogged := make(map[string]int)

		statCh := ag.statCh
		progCh := ag.progCh

		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()

		for statCh != nil || progCh != nil {
			select {
			case ev, ok := <-statCh:
				if !ok {
					statCh = nil
					continue
				}
				stats.Add(ev.Size, buckets)

			case ev, ok := <-progCh:
				if !ok {
					progCh = nil
					continue
				}
				if !showProgress {
					continue
				}

				// Emit progress logs only every `step` documents.
				if ev.SrcProcessed%step == 0 || ev.SrcProcessed == ev.SrcTotal {
					// Avoid duplicate logs if sender races.
					prev := lastLogged[ev.SrcName]
					if ev.SrcProcessed != prev {
						log.Infof(
							"Processed %d/%d doc(s) | source %s",
							ev.SrcProcessed, ev.SrcTotal, ev.SrcName,
						)
						lastLogged[ev.SrcName] = ev.SrcProcessed
					}
				}

			case <-ticker.C:
				// Heart-beat to reassure long-running loads.
				if showProgress {
					log.Infof("Loader is still running – waiting for sources")
				}
			}
		}

		if showStats && stats.TotalDocs > 0 {
			stats.Log(buckets)
		}
	}()

	return ag
}

// StatCh returns the write-only statistics channel.
func (a *Aggregator) StatCh() chan<- StatEvent { return a.statCh }

// ProgCh returns the write-only progress channel.
func (a *Aggregator) ProgCh() chan<- ProgEvent { return a.progCh }

// Close flushes the aggregator and blocks until the background goroutine
// finishes logging.
func (a *Aggregator) Close() {
	close(a.statCh)
	close(a.progCh)
	<-a.done
}
