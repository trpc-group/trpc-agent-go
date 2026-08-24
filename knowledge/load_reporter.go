//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package knowledge

import (
	"context"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/knowledge/internal/loader"
	"trpc.group/trpc-go/trpc-agent-go/log"
)

const loadHeartbeatInterval = 30 * time.Second

type loadReporter struct {
	cfg         *loadConfig
	sourceNames []string
	startTime   time.Time
	buckets     []int
	totalFunc   func() int

	mu            sync.Mutex
	closeOnce     sync.Once
	stats         *loader.Stats
	lastLogged    map[string]int
	heartbeatStop chan struct{}
	heartbeatDone chan struct{}
}

func newLoadReporter(cfg *loadConfig, sourceNames []string, start time.Time, buckets []int, totalFn func() int) *loadReporter {
	lr := &loadReporter{
		cfg:         cfg,
		sourceNames: sourceNames,
		startTime:   start,
		buckets:     buckets,
		totalFunc:   totalFn,
		lastLogged:  make(map[string]int),
	}
	if cfg.showStats {
		lr.stats = loader.NewStats(buckets)
	}
	if cfg.showProgress {
		lr.heartbeatStop = make(chan struct{})
		lr.heartbeatDone = make(chan struct{})
		go lr.runHeartbeat()
	}
	return lr
}

func (lr *loadReporter) enabled() bool {
	return lr.cfg.progressCallback != nil
}

func (lr *loadReporter) RecordStat(size int) {
	if lr.stats == nil {
		return
	}
	lr.mu.Lock()
	lr.stats.Add(size, lr.buckets)
	lr.mu.Unlock()
}

// Progress reports one progress update for a source. prevProcessed is that
// source's processed count before the update, which is what lets the reporter
// honour progressStepSize when a single update advances the count by a whole
// embedding batch instead of by one document.
func (lr *loadReporter) Progress(ctx context.Context, prevProcessed int, ev LoadProgressEvent) {
	if !lr.shouldReport(prevProcessed, ev) {
		return
	}
	if lr.cfg.showProgress {
		lr.mu.Lock()
		prev := lr.lastLogged[ev.SourceName]
		if ev.SourceProcessed != prev {
			log.InfofContext(ctx, "Processed %d/%d doc(s) | source %s | elapsed %s | ETA %s",
				ev.SourceProcessed, ev.SourceTotal, ev.SourceName,
				ev.SourceElapsed.Truncate(time.Second), ev.SourceETA.Truncate(time.Second))
			lr.lastLogged[ev.SourceName] = ev.SourceProcessed
		}
		lr.mu.Unlock()
	}
	if !lr.enabled() {
		return
	}
	ev.SourceNames = lr.sourceNames
	ev.Total = lr.totalFunc()
	ev.TotalElapsed = time.Since(lr.startTime)
	lr.cfg.progressCallback(ctx, ev)
}

// shouldReport reports whether an update crossed a progressStepSize boundary
// or completed the source.
//
// An update that advances the count by one document crosses a boundary
// exactly when the new count is a multiple of the step size, so the
// per-document path keeps reporting the same updates as before. An update that
// advances the count by a whole batch is reported when it moves the count into
// a later step, so one batch yields at most one update however many boundaries
// it crossed, and that update carries the real document count.
func (lr *loadReporter) shouldReport(prevProcessed int, ev LoadProgressEvent) bool {
	if ev.SourceProcessed == ev.SourceTotal {
		return true
	}
	step := lr.cfg.progressStepSize
	if step <= 0 {
		return true
	}
	return prevProcessed/step != ev.SourceProcessed/step
}

func (lr *loadReporter) Error(ctx context.Context, ev LoadProgressEvent, err error) {
	if !lr.enabled() {
		return
	}
	ev.SourceNames = lr.sourceNames
	ev.Total = lr.totalFunc()
	ev.TotalElapsed = time.Since(lr.startTime)
	ev.Err = err
	lr.cfg.progressCallback(ctx, ev)
}

func (lr *loadReporter) Done(ctx context.Context) {
	if !lr.enabled() {
		return
	}
	lr.cfg.progressCallback(ctx, LoadProgressEvent{
		SourceNames:  lr.sourceNames,
		Total:        lr.totalFunc(),
		TotalElapsed: time.Since(lr.startTime),
		Done:         true,
	})
}

func (lr *loadReporter) Close() {
	lr.closeOnce.Do(func() {
		if lr.heartbeatStop != nil {
			close(lr.heartbeatStop)
			<-lr.heartbeatDone
		}
		if lr.stats != nil && lr.stats.TotalDocs > 0 {
			lr.stats.Log(lr.buckets)
		}
	})
}

func (lr *loadReporter) runHeartbeat() {
	ticker := time.NewTicker(loadHeartbeatInterval)
	defer func() {
		ticker.Stop()
		close(lr.heartbeatDone)
	}()
	for {
		select {
		case <-ticker.C:
			log.Infof("Loader is still running – waiting for sources")
		case <-lr.heartbeatStop:
			return
		}
	}
}
