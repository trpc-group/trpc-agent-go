//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package summary

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

// summaryJob represents a job for async summary extraction.
type summaryJob struct {
	ctx       context.Context
	filterKey string
	force     bool
	session   *session.Session
}

// AsyncSummaryWorker manages async summary workers.
type AsyncSummaryWorker struct {
	config   AsyncSummaryConfig
	jobChans []chan *summaryJob
	wg       sync.WaitGroup
	mu       sync.RWMutex
	started  bool
}

// AsyncSummaryConfig contains configuration for async summary worker.
type AsyncSummaryConfig struct {
	Summarizer        summary.SessionSummarizer
	AsyncSummaryNum   int
	SummaryQueueSize  int
	SummaryJobTimeout time.Duration
	CreateSummaryFunc func(context.Context, *session.Session, string, bool) error
}

// NewAsyncSummaryWorker creates a new async summary worker.
func NewAsyncSummaryWorker(config AsyncSummaryConfig) *AsyncSummaryWorker {
	return &AsyncSummaryWorker{
		config: config,
	}
}

// Start starts the async summary workers.
func (w *AsyncSummaryWorker) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return
	}
	if w.config.Summarizer == nil {
		return
	}
	num := w.config.AsyncSummaryNum
	if num <= 0 {
		num = 1
	}
	queueSize := w.config.SummaryQueueSize
	if queueSize <= 0 {
		queueSize = 10
	}
	w.jobChans = make([]chan *summaryJob, num)
	for i := 0; i < num; i++ {
		w.jobChans[i] = make(chan *summaryJob, queueSize)
	}
	w.wg.Add(num)
	for _, ch := range w.jobChans {
		go func(ch chan *summaryJob) {
			defer w.wg.Done()
			for job := range ch {
				w.processJob(job)
			}
		}(ch)
	}
	w.started = true
}

// Stop stops all async summary workers.
func (w *AsyncSummaryWorker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started || len(w.jobChans) == 0 {
		return
	}
	for _, ch := range w.jobChans {
		close(ch)
	}
	w.wg.Wait()
	w.jobChans = nil
	w.started = false
}

// EnqueueJob enqueues a summary job for async processing.
func (w *AsyncSummaryWorker) EnqueueJob(
	ctx context.Context,
	sess *session.Session,
	filterKey string,
	force bool,
) error {
	if w.config.Summarizer == nil {
		return nil
	}

	if sess == nil {
		return errors.New("nil session")
	}

	key := session.Key{AppName: sess.AppName, UserID: sess.UserID, SessionID: sess.ID}
	if err := key.CheckSessionKey(); err != nil {
		return fmt.Errorf("check session key failed: %w", err)
	}

	// Create job with detached context.
	job := &summaryJob{
		ctx:       context.WithoutCancel(ctx),
		filterKey: filterKey,
		force:     force,
		session:   sess,
	}

	// Try to enqueue the job asynchronously.
	if w.tryEnqueueJob(ctx, job) {
		return nil
	}

	// Fall back to synchronous processing.
	log.DebugfContext(ctx, "summary job queue full, processing synchronously")
	return CreateSessionSummaryWithCascade(ctx, sess, filterKey, force, w.config.CreateSummaryFunc)
}

// tryEnqueueJob attempts to enqueue a summary job.
// Uses RLock to prevent race with Stop() which closes channels under Lock().
func (w *AsyncSummaryWorker) tryEnqueueJob(ctx context.Context, job *summaryJob) bool {
	if ctx.Err() != nil {
		return false
	}

	// Hold read lock during channel send to prevent race with Stop().
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.started || len(w.jobChans) == 0 {
		return false
	}

	// Select a channel using hash distribution.
	index := job.session.Hash % len(w.jobChans)

	select {
	case w.jobChans[index] <- job:
		return true
	default:
		return false
	}
}

// processJob processes a single summary job.
func (w *AsyncSummaryWorker) processJob(job *summaryJob) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(context.Background(), "panic in summary worker: %v", r)
		}
	}()

	ctx := job.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := w.config.SummaryJobTimeout
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if err := CreateSessionSummaryWithCascade(ctx, job.session, job.filterKey,
		job.force, w.config.CreateSummaryFunc); err != nil {
		log.WarnfContext(ctx, "summary worker failed to create session summary: %v", err)
	}
}
