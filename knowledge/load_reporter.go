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

func (lr *loadReporter) Progress(ctx context.Context, ev LoadProgressEvent) {
	if ev.SourceProcessed%lr.cfg.progressStepSize != 0 && ev.SourceProcessed != ev.SourceTotal {
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
