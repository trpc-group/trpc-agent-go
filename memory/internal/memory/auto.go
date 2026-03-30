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
	"maps"
	"strings"
	"sync"
	"time"

	imemory "trpc.group/trpc-go/trpc-agent-go/internal/memory"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/memory/extractor"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

// Default values for auto memory configuration.
const (
	DefaultAsyncMemoryNum   = 1
	DefaultMemoryQueueSize  = 10
	DefaultMemoryJobTimeout = 30 * time.Second

	memoryNotFoundErrSubstr = "memory with id"
	memoryNotFoundErrMarker = "not found"

	autoMemoryLastExtractAtStateKeyPrefix = memory.SessionStateKeyAutoMemoryLastExtractAt + ":"
)

// MemoryJob represents a job for async memory extraction.
type MemoryJob struct {
	Ctx      context.Context
	UserKey  memory.UserKey
	Session  *session.Session
	LatestTs time.Time
	Messages []model.Message
}

// AutoMemoryConfig contains configuration for auto memory extraction.
type AutoMemoryConfig struct {
	Extractor        extractor.MemoryExtractor
	AsyncMemoryNum   int
	MemoryQueueSize  int
	MemoryJobTimeout time.Duration
	// EnabledTools controls which memory operations the worker
	// is allowed to execute. When nil, all operations are
	// allowed (default). When non-nil, only operations whose
	// corresponding tool name is present are executed; others
	// are silently skipped. A non-nil empty map disables all
	// operations.
	EnabledTools map[string]struct{}
}

// EnabledToolsConfigurer is an optional capability interface.
// Extractors that implement it can receive enabled tool flags
// from the memory service during initialization.
// This is intentionally not part of MemoryExtractor to avoid
// breaking users who implement their own extractors.
type EnabledToolsConfigurer interface {
	SetEnabledTools(enabled map[string]struct{})
}

// ConfigureExtractorEnabledTools passes enabled tool flags to the
// extractor if it implements EnabledToolsConfigurer.
func ConfigureExtractorEnabledTools(
	ext extractor.MemoryExtractor,
	enabledTools map[string]struct{},
) {
	if c, ok := ext.(EnabledToolsConfigurer); ok {
		c.SetEnabledTools(enabledTools)
	}
}

// MemoryOperator defines the interface for memory operations.
// This allows the auto memory worker to work with different
// storage backends.
type MemoryOperator interface {
	ReadMemories(ctx context.Context, userKey memory.UserKey,
		limit int) ([]*memory.Entry, error)
	SearchMemories(ctx context.Context, userKey memory.UserKey,
		query string,
		opts ...memory.SearchOption) ([]*memory.Entry, error)
	AddMemory(ctx context.Context, userKey memory.UserKey,
		mem string, topics []string,
		opts ...memory.AddOption) error
	UpdateMemory(ctx context.Context, memoryKey memory.Key,
		mem string, topics []string,
		opts ...memory.UpdateOption) error
	DeleteMemory(ctx context.Context,
		memoryKey memory.Key) error
	ClearMemories(ctx context.Context,
		userKey memory.UserKey) error
}

// AutoMemoryWorker manages async memory extraction workers.
type AutoMemoryWorker struct {
	config   AutoMemoryConfig
	operator MemoryOperator
	jobChans []chan *MemoryJob
	wg       sync.WaitGroup
	mu       sync.RWMutex
	started  bool
}

// NewAutoMemoryWorker creates a new auto memory worker.
// The EnabledTools map is defensively copied so that callers
// cannot mutate the worker's configuration after construction.
func NewAutoMemoryWorker(
	config AutoMemoryConfig,
	operator MemoryOperator,
) *AutoMemoryWorker {
	config.EnabledTools = maps.Clone(config.EnabledTools)
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
func (w *AutoMemoryWorker) EnqueueJob(ctx context.Context, sess *session.Session) error {
	if w.config.Extractor == nil {
		return nil
	}
	if sess == nil {
		log.DebugfContext(ctx, "auto_memory: skipped due to nil session")
		return nil
	}
	// runner.enqueueAutoMemoryJob already folds any run-scoped memory user
	// override into the cloned job session, so ResolveUserKey should read the
	// effective user directly from sess without a separate runtimeState map.
	appName, userID, ok := imemory.ResolveUserKey(sess, nil)
	if !ok {
		log.DebugfContext(ctx, "auto_memory: skipped due to empty userKey")
		return nil
	}
	userKey := memory.UserKey{AppName: appName, UserID: userID}

	// Scan new transcript events from the cloned job snapshot, but persist the
	// per-user extraction cursor on the original session carried via context.
	cursorSession := autoMemoryCursorSession(ctx, sess)
	since := readLastExtractAt(cursorSession, userKey)
	latestTs, messages := scanDeltaSince(sess, since)
	if len(messages) == 0 {
		log.DebugfContext(ctx, "auto_memory: skipped due to no new messages for user %s/%s",
			userKey.AppName, userKey.UserID)
		return nil
	}

	var lastExtractAtPtr *time.Time
	if !since.IsZero() {
		sinceUTC := since.UTC()
		lastExtractAtPtr = &sinceUTC
	}
	extractCtx := &extractor.ExtractionContext{
		UserKey:       userKey,
		Messages:      messages,
		LastExtractAt: lastExtractAtPtr,
	}

	if !w.config.Extractor.ShouldExtract(extractCtx) {
		log.DebugfContext(ctx, "auto_memory: skipped by checker for user %s/%s",
			userKey.AppName, userKey.UserID)
		return nil
	}

	job := &MemoryJob{
		Ctx:      context.WithoutCancel(ctx),
		UserKey:  userKey,
		Session:  cursorSession,
		LatestTs: latestTs,
		Messages: messages,
	}
	if w.tryEnqueueJob(ctx, userKey, job) {
		return nil
	}
	if ctx.Err() != nil {
		log.DebugfContext(ctx, "auto_memory: skipped sync fallback due to cancelled context "+
			"for user %s/%s", userKey.AppName, userKey.UserID)
		return nil
	}
	log.DebugfContext(ctx, "auto_memory: queue full, processing synchronously for user %s/%s",
		userKey.AppName, userKey.UserID)
	timeout := w.config.MemoryJobTimeout
	if timeout <= 0 {
		timeout = DefaultMemoryJobTimeout
	}
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	if err := w.createAutoMemory(syncCtx, userKey, messages); err != nil {
		return err
	}
	writeLastExtractAt(cursorSession, userKey, latestTs)
	return nil
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
		return
	}
	writeLastExtractAt(job.Session, job.UserKey, job.LatestTs)
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

	// Search for existing memories relevant to the current conversation
	// instead of loading all memories. This keeps the extractor prompt
	// within a reasonable token budget while surfacing the entries most
	// likely to need updating or deduplication.
	existing, err := w.searchRelevantMemories(ctx, userKey, messages)
	if err != nil {
		log.WarnfContext(ctx, "auto_memory: failed to prepare existing memories for user %s/%s: %v",
			userKey.AppName, userKey.UserID, err)
		return fmt.Errorf("auto_memory: prepare existing memories failed: %w", err)
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

// searchRelevantMemories builds a query from the conversation messages
// and searches for existing memories that are semantically related.
// This avoids injecting the full memory set into the extractor prompt,
// keeping token usage proportional to the conversation size rather than
// the total memory count. When the search path fails, it falls back to
// loading a small set of recent memories so extraction still has
// deduplication context instead of silently proceeding with none.
func (w *AutoMemoryWorker) searchRelevantMemories(
	ctx context.Context,
	userKey memory.UserKey,
	messages []model.Message,
) ([]*memory.Entry, error) {
	query := buildSearchQuery(messages)
	if query == "" {
		return nil, nil
	}
	entries, err := w.operator.SearchMemories(ctx, userKey, query)
	if err == nil {
		return entries, nil
	}
	fallback, readErr := w.operator.ReadMemories(
		ctx, userKey, DefaultMaxSearchResults,
	)
	if readErr != nil {
		return nil, fmt.Errorf(
			"search existing memories failed: %w; fallback read failed: %v",
			err, readErr,
		)
	}
	log.WarnfContext(ctx,
		"auto_memory: search existing memories failed, using recent fallback for user %s/%s: %v",
		userKey.AppName, userKey.UserID, err)
	return fallback, nil
}

// buildSearchQuery extracts user-side text from conversation messages
// and concatenates it into a single search query.
func buildSearchQuery(messages []model.Message) string {
	parts := make([]string, 0, len(messages))
	for _, msg := range messages {
		if msg.Role != model.RoleUser {
			continue
		}
		text := messageSearchText(msg)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, " ")
}

// messageSearchText extracts searchable text from a user message.
// It preserves both the legacy Content field and text ContentParts.
func messageSearchText(msg model.Message) string {
	parts := make([]string, 0, 1+len(msg.ContentParts))
	if text := strings.TrimSpace(msg.Content); text != "" {
		parts = append(parts, text)
	}
	for _, part := range msg.ContentParts {
		if part.Type != model.ContentTypeText || part.Text == nil {
			continue
		}
		text := strings.TrimSpace(*part.Text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func isMemoryNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, memoryNotFoundErrSubstr) &&
		strings.Contains(msg, memoryNotFoundErrMarker)
}

// operationToolName maps an operation type to the corresponding
// memory tool name for enabled-tools gating.
var operationToolName = map[extractor.OperationType]string{
	extractor.OperationAdd:    memory.AddToolName,
	extractor.OperationUpdate: memory.UpdateToolName,
	extractor.OperationDelete: memory.DeleteToolName,
	extractor.OperationClear:  memory.ClearToolName,
}

// isToolEnabled checks whether the given tool name is allowed
// by the EnabledTools configuration. Returns true when the
// allow-list is nil. A non-nil empty map disables all tools.
func (w *AutoMemoryWorker) isToolEnabled(toolName string) bool {
	et := w.config.EnabledTools
	if et == nil {
		return true
	}
	_, ok := et[toolName]
	return ok
}

// executeOperation executes a single memory operation.
// Operations whose tool is disabled in config.EnabledTools are
// silently skipped.
func (w *AutoMemoryWorker) executeOperation(
	ctx context.Context,
	userKey memory.UserKey,
	op *extractor.Operation,
) {
	if et := w.config.EnabledTools; et != nil {
		if name, ok := operationToolName[op.Type]; ok {
			if _, enabled := et[name]; !enabled {
				log.DebugfContext(ctx,
					"auto_memory: skipping disabled %s "+
						"operation for user %s/%s",
					op.Type, userKey.AppName, userKey.UserID)
				return
			}
		}
	}

	switch op.Type {
	case extractor.OperationAdd:
		ep := opToMetadata(op)
		if err := w.operator.AddMemory(ctx, userKey,
			op.Memory, op.Topics,
			memory.WithMetadata(ep)); err != nil {
			log.WarnfContext(ctx,
				"auto_memory: add memory failed "+
					"for user %s/%s: %v",
				userKey.AppName, userKey.UserID, err)
		}
	case extractor.OperationUpdate:
		memKey := memory.Key{
			AppName:  userKey.AppName,
			UserID:   userKey.UserID,
			MemoryID: op.MemoryID,
		}
		ep := opToMetadata(op)
		if err := w.operator.UpdateMemory(ctx, memKey,
			op.Memory, op.Topics,
			memory.WithUpdateMetadata(ep)); err != nil {
			if isMemoryNotFoundError(err) {
				if !w.isToolEnabled(memory.AddToolName) {
					log.DebugfContext(ctx,
						"auto_memory: update-not-found "+
							"fallback skipped (add disabled)"+
							" for user %s/%s, memory_id=%s",
						userKey.AppName, userKey.UserID,
						op.MemoryID)
					return
				}
				if addErr := w.operator.AddMemory(
					ctx, userKey, op.Memory, op.Topics,
					memory.WithMetadata(ep),
				); addErr != nil {
					log.WarnfContext(ctx,
						"auto_memory: update missing, "+
							"add memory failed for user "+
							"%s/%s, memory_id=%s: %v",
						userKey.AppName, userKey.UserID,
						op.MemoryID, addErr,
					)
				}
				return
			}
			log.WarnfContext(ctx,
				"auto_memory: update memory failed "+
					"for user %s/%s, memory_id=%s: %v",
				userKey.AppName, userKey.UserID,
				op.MemoryID, err)
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

// opToMetadata converts extractor.Operation episodic
// fields to memory.Metadata. Always returns a non-nil
// value; defaults to Kind=KindFact when no episodic data
// is present so that backends do not need nil-guard logic.
func opToMetadata(op *extractor.Operation) *memory.Metadata {
	kind := op.MemoryKind
	if kind == "" {
		kind = memory.KindFact
	}
	return &memory.Metadata{
		Kind:         kind,
		EventTime:    op.EventTime,
		Participants: op.Participants,
		Location:     op.Location,
	}
}

// hashUserKey computes a hash from userKey for channel distribution.
func hashUserKey(userKey memory.UserKey) int {
	h := fnv.New32a()
	h.Write([]byte(userKey.AppName))
	h.Write([]byte(userKey.UserID))
	return int(h.Sum32())
}

func autoMemoryCursorSession(
	ctx context.Context,
	sess *session.Session,
) *session.Session {
	if scopedSess, ok := imemory.AutoMemoryCursorSessionFromContext(ctx); ok {
		return scopedSess
	}
	return sess
}

func autoMemoryLastExtractAtStateKey(userKey memory.UserKey) string {
	userID := strings.TrimSpace(userKey.UserID)
	if userID == "" {
		return memory.SessionStateKeyAutoMemoryLastExtractAt
	}
	return autoMemoryLastExtractAtStateKeyPrefix + userID
}

func shouldUseLegacyLastExtractAtState(
	sess *session.Session,
	userKey memory.UserKey,
) bool {
	if sess == nil {
		return false
	}
	return strings.TrimSpace(sess.UserID) == strings.TrimSpace(userKey.UserID)
}

func readLastExtractAtState(sess *session.Session, key string) (time.Time, bool) {
	if sess == nil {
		return time.Time{}, false
	}
	raw, ok := sess.GetState(key)
	if !ok || len(raw) == 0 {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// readLastExtractAt reads the last auto memory extraction timestamp from session state.
// Returns zero time if not found or parsing fails.
func readLastExtractAt(
	sess *session.Session,
	userKey memory.UserKey,
) time.Time {
	if ts, ok := readLastExtractAtState(
		sess,
		autoMemoryLastExtractAtStateKey(userKey),
	); ok {
		return ts
	}
	if shouldUseLegacyLastExtractAtState(sess, userKey) {
		if ts, ok := readLastExtractAtState(
			sess,
			memory.SessionStateKeyAutoMemoryLastExtractAt,
		); ok {
			return ts
		}
	}
	return time.Time{}
}

func writeLastExtractAtState(sess *session.Session, key string, ts time.Time) {
	if sess == nil {
		return
	}
	sess.SetState(key, []byte(ts.UTC().Format(time.RFC3339Nano)))
}

// writeLastExtractAt writes the last auto memory extraction timestamp to session state.
// The timestamp represents the last included event's timestamp for incremental extraction.
func writeLastExtractAt(
	sess *session.Session,
	userKey memory.UserKey,
	ts time.Time,
) {
	writeLastExtractAtState(sess, autoMemoryLastExtractAtStateKey(userKey), ts)
	if shouldUseLegacyLastExtractAtState(sess, userKey) {
		writeLastExtractAtState(
			sess,
			memory.SessionStateKeyAutoMemoryLastExtractAt,
			ts,
		)
	}
}

// scanDeltaSince scans session events since the given timestamp and extracts messages.
// Returns the latest event timestamp and extracted messages.
// Only includes user/assistant messages with content, excluding tool calls.
func scanDeltaSince(
	sess *session.Session,
	since time.Time,
) (time.Time, []model.Message) {
	var latestTs time.Time
	var messages []model.Message
	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	for _, e := range sess.Events {
		// Skip events that are not newer than the since timestamp.
		if !since.IsZero() && !e.Timestamp.After(since) {
			continue
		}

		// Track the latest timestamp among all processed events.
		if e.Timestamp.After(latestTs) {
			latestTs = e.Timestamp
		}

		// Skip events without responses.
		if e.Response == nil {
			continue
		}

		// Extract messages from response choices, excluding tool-related messages.
		for _, choice := range e.Response.Choices {
			msg := choice.Message
			// Skip tool messages and messages with tool calls.
			if msg.Role == model.RoleTool || msg.ToolID != "" {
				continue
			}
			// Skip messages with no content (neither text nor content parts).
			if msg.Content == "" && len(msg.ContentParts) == 0 {
				continue
			}
			if len(msg.ToolCalls) > 0 {
				continue
			}
			messages = append(messages, msg)
		}
	}
	return latestTs, messages
}
