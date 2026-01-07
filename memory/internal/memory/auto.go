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
	DefaultAsyncMemoryNum   = 1
	DefaultMemoryQueueSize  = 10
	DefaultMemoryJobTimeout = 30 * time.Second
)

// MemoryJob represents a job for async memory extraction.
type MemoryJob struct {
	Ctx      context.Context
	UserKey  memory.UserKey
	Messages []model.Message
}

// AutoMemoryConfig contains configuration for auto memory extraction.
type AutoMemoryConfig struct {
	Extractor        extractor.MemoryExtractor
	AsyncMemoryNum   int
	MemoryQueueSize  int
	MemoryJobTimeout time.Duration
}

// MemoryOperator defines the interface for memory operations.
// This allows the auto memory worker to work with different storage backends.
type MemoryOperator interface {
	ReadMemories(ctx context.Context, userKey memory.UserKey, limit int) ([]*memory.Entry, error)
	AddMemory(ctx context.Context, userKey memory.UserKey, memory string, topics []string) error
	UpdateMemory(ctx context.Context, memoryKey memory.Key, memory string, topics []string) error
	DeleteMemory(ctx context.Context, memoryKey memory.Key) error
	ClearMemories(ctx context.Context, userKey memory.UserKey) error
}

// AutoMemoryWorker manages async memory extraction workers.
type AutoMemoryWorker struct {
	config   AutoMemoryConfig
	operator MemoryOperator
	jobChans []chan *MemoryJob
	wg       sync.WaitGroup
	mu       sync.RWMutex
	started  bool

	// stateMu protects extractionStates map.
	stateMu sync.RWMutex
	// extractionStates tracks extraction state per user.
	extractionStates map[string]*extractionState
}

// extractionState tracks extraction state for a user.
type extractionState struct {
	lastExtractAt *time.Time
	// pendingMessages accumulates messages from turns that were not extracted.
	pendingMessages []model.Message
}

// NewAutoMemoryWorker creates a new auto memory worker.
func NewAutoMemoryWorker(config AutoMemoryConfig, operator MemoryOperator) *AutoMemoryWorker {
	return &AutoMemoryWorker{
		config:           config,
		operator:         operator,
		extractionStates: make(map[string]*extractionState),
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

	// Get or create extraction state for this user.
	state := w.getOrCreateState(userKey)

	// Accumulate current turn messages.
	state.pendingMessages = append(state.pendingMessages, messages...)

	// Build extraction context.
	extractCtx := &extractor.ExtractionContext{
		UserKey:       userKey,
		Messages:      state.pendingMessages,
		LastExtractAt: state.lastExtractAt,
	}

	// Check if extraction should proceed.
	if !w.config.Extractor.ShouldExtract(extractCtx) {
		log.DebugfContext(ctx, "auto_memory: skipped by checker for user %s/%s",
			userKey.AppName, userKey.UserID)
		return nil
	}

	// Capture messages to extract and clear pending.
	messagesToExtract := state.pendingMessages
	state.pendingMessages = nil

	// Update lastExtractAt.
	now := time.Now()
	state.lastExtractAt = &now

	// Create job with detached context.
	job := &MemoryJob{
		Ctx:      context.WithoutCancel(ctx),
		UserKey:  userKey,
		Messages: messagesToExtract,
	}
	// Try to enqueue the job asynchronously.
	if w.tryEnqueueJob(ctx, userKey, job) {
		return nil
	}
	// Skip if context is already cancelled to avoid wasted work.
	if ctx.Err() != nil {
		log.DebugfContext(ctx, "auto_memory: skipped sync fallback due to cancelled context "+
			"for user %s/%s", userKey.AppName, userKey.UserID)
		return nil
	}
	// Fall back to synchronous processing with detached context and timeout.
	log.DebugfContext(ctx, "auto_memory: queue full, processing synchronously for user %s/%s",
		userKey.AppName, userKey.UserID)
	timeout := w.config.MemoryJobTimeout
	if timeout <= 0 {
		timeout = DefaultMemoryJobTimeout
	}
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	return w.createAutoMemory(syncCtx, userKey, messagesToExtract)
}

// tryEnqueueJob attempts to enqueue a memory job.
// Returns true if successful, false if should process synchronously.
// Uses RLock to prevent race with Stop() which closes channels under Lock().
func (w *AutoMemoryWorker) tryEnqueueJob(
	ctx context.Context,
	userKey memory.UserKey,
	job *MemoryJob,
) bool {
	if err := ctx.Err(); err != nil {
		return false
	}
	// Hold read lock during channel send to prevent race with Stop().
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.started || len(w.jobChans) == 0 {
		return false
	}
	// Use hash distribution for consistent routing.
	hash := hashUserKey(userKey)
	index := hash % len(w.jobChans)
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

	// Read all existing memories for the user.
	// The extractor needs complete memory context to properly deduplicate,
	// update, or delete existing memories.
	existing, err := w.operator.ReadMemories(ctx, userKey, 0)
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
	case extractor.OperationClear:
		if err := w.operator.ClearMemories(ctx, userKey); err != nil {
			log.WarnfContext(ctx, "auto_memory: clear memories failed for user %s/%s: %v",
				userKey.AppName, userKey.UserID, err)
		}
	default:
		log.WarnfContext(ctx, "auto_memory: unknown operation type '%s' for user %s/%s",
			op.Type, userKey.AppName, userKey.UserID)
	}
}

// hashUserKey computes a hash from userKey for channel distribution.
func hashUserKey(userKey memory.UserKey) int {
	h := fnv.New32a()
	h.Write([]byte(userKey.AppName))
	h.Write([]byte(userKey.UserID))
	return int(h.Sum32())
}

// getOrCreateState returns the extraction state for a user, creating if needed.
func (w *AutoMemoryWorker) getOrCreateState(userKey memory.UserKey) *extractionState {
	key := userKey.AppName + "/" + userKey.UserID
	w.stateMu.RLock()
	state, ok := w.extractionStates[key]
	w.stateMu.RUnlock()
	if ok {
		return state
	}
	w.stateMu.Lock()
	defer w.stateMu.Unlock()
	// Double-check after acquiring write lock.
	if state, ok = w.extractionStates[key]; ok {
		return state
	}
	state = &extractionState{}
	w.extractionStates[key] = state
	return state
}
