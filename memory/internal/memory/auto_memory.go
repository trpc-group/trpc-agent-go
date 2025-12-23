//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package memory

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Default values for auto memory configuration.
const (
	DefaultAsyncMemoryNum      = 1
	DefaultMemoryQueueSize     = 10
	DefaultMemoryJobTimeout    = 30 * time.Second
	DefaultMaxExistingMemories = 50
)

// MemoryJob represents a job for async memory extraction.
type MemoryJob struct {
	Ctx      context.Context
	UserKey  memory.UserKey
	Messages []model.Message
}

// AutoMemoryConfig contains configuration for auto memory extraction.
type AutoMemoryConfig struct {
	Extractor           extractor.MemoryExtractor
	AsyncMemoryNum      int
	MemoryQueueSize     int
	MemoryJobTimeout    time.Duration
	MaxExistingMemories int
}

// MemoryOperator defines the interface for memory operations.
// This allows the auto memory worker to work with different storage backends.
type MemoryOperator interface {
	ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error)
	AddMemory(ctx context.Context, userKey memory.UserKey, memory string, topics []string) error
	UpdateMemory(ctx context.Context, memoryKey memory.Key, memory string, topics []string) error
	DeleteMemory(ctx context.Context, memoryKey memory.Key) error
}

// AutoMemoryWorker manages async memory extraction workers.
type AutoMemoryWorker struct {
	config   AutoMemoryConfig
	operator MemoryOperator
	jobChans []chan *MemoryJob
	wg       sync.WaitGroup
	mu       sync.Mutex
	started  bool
}

// NewAutoMemoryWorker creates a new auto memory worker.
func NewAutoMemoryWorker(config AutoMemoryConfig, operator MemoryOperator) *AutoMemoryWorker {
	return &AutoMemoryWorker{
		config:   config,
		operator: operator,
	}
}

// Start starts the async memory workers.
func (w *AutoMemoryWorker) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started {
		return
	}
	if w.config.Extractor == nil {
		return
	}
	num := w.config.AsyncMemoryNum
	if num <= 0 {
		num = DefaultAsyncMemoryNum
	}
	queueSize := w.config.MemoryQueueSize
	if queueSize <= 0 {
		queueSize = DefaultMemoryQueueSize
	}
	w.jobChans = make([]chan *MemoryJob, num)
	for i := 0; i < num; i++ {
		w.jobChans[i] = make(chan *MemoryJob, queueSize)
	}
	w.wg.Add(num)
	for _, ch := range w.jobChans {
		go func(ch chan *MemoryJob) {
			defer w.wg.Done()
			for job := range ch {
				w.processJob(job)
			}
		}(ch)
	}
	w.started = true
}

// Stop stops all async memory workers.
func (w *AutoMemoryWorker) Stop() {
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

// EnqueueJob enqueues an auto memory job for async processing.
// Returns nil if successfully enqueued or processed synchronously.
func (w *AutoMemoryWorker) EnqueueJob(
	ctx context.Context,
	userKey memory.UserKey,
	messages []model.Message,
) error {
	if w.config.Extractor == nil {
		return nil
	}
	// Validate userKey.
	if userKey.AppName == "" || userKey.UserID == "" {
		log.DebugfContext(ctx, "auto_memory: skipped due to empty userKey")
		return nil
	}
	// Validate messages.
	if len(messages) == 0 {
		log.DebugfContext(ctx, "auto_memory: skipped due to empty messages")
		return nil
	}

	// If async workers are not started, fall back to synchronous.
	w.mu.Lock()
	started := w.started
	w.mu.Unlock()
	if !started || len(w.jobChans) == 0 {
		return w.createAutoMemory(ctx, userKey, messages)
	}
	// Create job with detached context.
	job := &MemoryJob{
		Ctx:      context.WithoutCancel(ctx),
		UserKey:  userKey,
		Messages: messages,
	}
	// Try to enqueue the job asynchronously.
	if w.tryEnqueueJob(ctx, userKey, job) {
		return nil
	}
	// Fall back to synchronous processing.
	log.DebugfContext(ctx, "auto_memory: queue full, processing synchronously for user %s/%s",
		userKey.AppName, userKey.UserID)
	return w.createAutoMemory(ctx, userKey, messages)
}

// tryEnqueueJob attempts to enqueue a memory job.
// Returns true if successful, false if should process synchronously.
func (w *AutoMemoryWorker) tryEnqueueJob(
	ctx context.Context,
	userKey memory.UserKey,
	job *MemoryJob,
) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	// Use hash distribution for consistent routing.
	hash := HashUserKey(userKey)
	index := hash % len(w.jobChans)
	// Use a defer-recover pattern to handle potential panic from sending to
	// closed channel.
	defer func() {
		if r := recover(); r != nil {
			log.WarnfContext(
				ctx,
				"memory job channel may be closed, falling back to synchronous processing: %v",
				r,
			)
		}
	}()
	select {
	case w.jobChans[index] <- job:
		return true
	default:
		log.WarnfContext(ctx, "memory job queue full, fallback to sync")
		return false
	}
}

// processJob processes a single memory job.
func (w *AutoMemoryWorker) processJob(job *MemoryJob) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(context.Background(), "panic in memory worker: %v", r)
		}
	}()
	ctx := job.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := w.config.MemoryJobTimeout
	if timeout <= 0 {
		timeout = DefaultMemoryJobTimeout
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, timeout)
	defer cancel()

	if err := w.createAutoMemory(ctx, job.UserKey, job.Messages); err != nil {
		log.WarnfContext(ctx, "auto_memory: job failed for user %s/%s: %v",
			job.UserKey.AppName, job.UserKey.UserID, err)
	}
}

// createAutoMemory performs memory extraction and persists operations.
func (w *AutoMemoryWorker) createAutoMemory(
	ctx context.Context,
	userKey memory.UserKey,
	messages []model.Message,
) error {
	if w.config.Extractor == nil {
		return nil
	}

	// Read existing memories.
	maxExisting := w.config.MaxExistingMemories
	if maxExisting <= 0 {
		maxExisting = DefaultMaxExistingMemories
	}
	existing, err := w.operator.ReadMemories(ctx, userKey, maxExisting)
	if err != nil {
		log.WarnfContext(ctx, "auto_memory: failed to read existing memories for user %s/%s: %v",
			userKey.AppName, userKey.UserID, err)
		existing = nil
	}

	// Extract memory operations.
	ops, err := w.config.Extractor.Extract(ctx, messages, existing)
	if err != nil {
		log.WarnfContext(ctx, "auto_memory: extraction failed for user %s/%s: %v",
			userKey.AppName, userKey.UserID, err)
		return fmt.Errorf("auto_memory: extract failed: %w", err)
	}

	// Execute operations.
	for _, op := range ops {
		w.executeOperation(ctx, userKey, op)
	}

	return nil
}

// executeOperation executes a single memory operation.
func (w *AutoMemoryWorker) executeOperation(
	ctx context.Context,
	userKey memory.UserKey,
	op *extractor.Operation,
) {
	switch op.Type {
	case extractor.OperationAdd:
		if err := w.operator.AddMemory(ctx, userKey, op.Memory, op.Topics); err != nil {
			log.WarnfContext(ctx, "auto_memory: add memory failed for user %s/%s: %v",
				userKey.AppName, userKey.UserID, err)
		}
	case extractor.OperationUpdate:
		memKey := memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: op.MemoryID,
		}
		if err := w.operator.UpdateMemory(ctx, memKey, op.Memory, op.Topics); err != nil {
			log.WarnfContext(ctx, "auto_memory: update memory failed for user %s/%s, memory_id=%s: %v",
				userKey.AppName, userKey.UserID, op.MemoryID, err)
		}
	case extractor.OperationDelete:
		memKey := memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: op.MemoryID,
		}
		if err := w.operator.DeleteMemory(ctx, memKey); err != nil {
			log.WarnfContext(ctx, "auto_memory: delete memory failed for user %s/%s, memory_id=%s: %v",
				userKey.AppName, userKey.UserID, op.MemoryID, err)
		}
	default:
		log.WarnfContext(ctx, "auto_memory: unknown operation type '%s' for user %s/%s",
			op.Type, userKey.AppName, userKey.UserID)
	}
}

// HashUserKey computes a hash from userKey for channel distribution.
func HashUserKey(userKey memory.UserKey) int {
	h := fnv.New32a()
	h.Write([]byte(userKey.AppName))
	h.Write([]byte(userKey.UserID))
	return int(h.Sum32())
}
