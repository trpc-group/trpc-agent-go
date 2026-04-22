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
)

// reconcile tuning constants.
//
// These are intentionally package-private: the goal is to keep the
// public surface of the memory package unchanged. If a concrete backend
// ever needs to override them, they can be promoted into
// AutoMemoryConfig without touching any exported API.
//
// Reconcile combines two independent signals so it works uniformly
// across vector-backed and keyword-backed stores:
//
//  1. The Score reported by SearchMemories.
//     - For vector backends (pgvector / sqlitevec / chromadb) this is
//     cosine similarity in [0, 1].
//     - For keyword backends (inmemory / sqlite / mysql / redis /
//     postgres) this is a BM25-based relevance in [0, 1]. Keyword
//     scores are systematically lower than vector scores for
//     semantically identical inputs, so the Score thresholds are
//     deliberately moderate rather than aggressive.
//
//  2. Token-level Jaccard similarity between the candidate memory
//     content and the best-matching existing entry. This uses the same
//     tokenizer that the keyword search scorer relies on (gse for CJK +
//     CJK trigrams + English words), so it catches "same core entities,
//     different filler words" cases that low BM25/vector scores miss
//     on their own.
//
// A candidate is treated as a near-duplicate when either signal
// crosses its threshold (logical OR). This keeps reconcile effective
// on both backend families without forcing callers to distinguish
// them or tune backend-specific parameters.
const (
	// reconcileTopK caps how many candidates SearchMemories is asked to
	// return per reconcile probe. Keeping this small bounds the extra
	// cost while still surfacing the closest match reliably.
	reconcileTopK = 3

	// reconcileSkipScore: at or above this search Score the candidate
	// is treated as an equivalent memory. The add is either dropped or
	// rewritten into a topic-only update.
	reconcileSkipScore = 0.90

	// reconcileUpdateScore: below skip but above update means the
	// stored memory is close enough to be refreshed with the new
	// wording / topics via an update.
	reconcileUpdateScore = 0.60

	// reconcileJaccardHigh: token overlap strong enough that the two
	// texts almost certainly describe the same fact. Treated the same
	// as crossing reconcileSkipScore.
	reconcileJaccardHigh = 0.70

	// reconcileJaccardMid: meaningful token overlap that warrants an
	// update even when the Score signal is weak. Primarily helps
	// keyword-backed stores where BM25 tends to land in the 0.4–0.6
	// band on paraphrases.
	reconcileJaccardMid = 0.40

	// reconcileMinProbeScore is passed as SimilarityThreshold to
	// SearchMemories so the backend can stop scanning once candidates
	// drop below a clearly irrelevant band.
	reconcileMinProbeScore = 0.30
)

// Reconcile decision tiers. A higher tier is always preferred when
// choosing among candidates, so a clearly duplicate entry is never
// shadowed by a weaker-signal candidate with slightly higher token
// overlap but no threshold crossing.
const (
	reconcileTierNone   = 0
	reconcileTierUpdate = 1
	reconcileTierSkip   = 2
)

// reconcileDecisionTier classifies a candidate against the reconcile
// thresholds. The same helper is shared by the candidate picker in
// decideAddOp and by the final switch, so both always agree on what
// "skip" / "update" / "keep" mean.
func reconcileDecisionTier(score, jaccard float64) int {
	switch {
	case score >= reconcileSkipScore || jaccard >= reconcileJaccardHigh:
		return reconcileTierSkip
	case score >= reconcileUpdateScore || jaccard >= reconcileJaccardMid:
		return reconcileTierUpdate
	default:
		return reconcileTierNone
	}
}

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
	userKey := memory.UserKey{AppName: sess.AppName, UserID: sess.UserID}
	if userKey.AppName == "" || userKey.UserID == "" {
		log.DebugfContext(ctx, "auto_memory: skipped due to empty userKey")
		return nil
	}

	since := readLastExtractAt(sess)
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
		Session:  sess,
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
	writeLastExtractAt(sess, latestTs)
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
	writeLastExtractAt(job.Session, job.LatestTs)
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

	// Reconcile Add operations against the store so that near-duplicate
	// memories get merged into updates instead of accumulating as
	// separate rows. Any failure inside reconcile is non-fatal: the
	// original ops slice is used and the worker keeps its pre-reconcile
	// behavior.
	ops = w.reconcileOps(ctx, userKey, ops)

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

// readLastExtractAt reads the last auto memory extraction timestamp from session state.
// Returns zero time if not found or parsing fails.
func readLastExtractAt(sess *session.Session) time.Time {
	raw, ok := sess.GetState(memory.SessionStateKeyAutoMemoryLastExtractAt)
	if !ok || len(raw) == 0 {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}
	}
	return ts
}

// writeLastExtractAt writes the last auto memory extraction timestamp to session state.
// The timestamp represents the last included event's timestamp for incremental extraction.
func writeLastExtractAt(sess *session.Session, ts time.Time) {
	sess.SetState(memory.SessionStateKeyAutoMemoryLastExtractAt,
		[]byte(ts.UTC().Format(time.RFC3339Nano)))
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

// reconcileOps rewrites extractor Add operations whose content is
// already covered by an existing memory, using the backend's own
// SearchMemories as the similarity oracle.
//
// The function is deliberately backend-agnostic: it relies only on the
// MemoryOperator contract that every memory service already satisfies,
// so vector-backed stores and keyword-backed stores receive the same
// treatment. Vector backends benefit the most because their Score is a
// true semantic similarity; keyword backends still benefit on cases
// with heavy lexical overlap, which covers the common "same sentence
// re-extracted with minor wording drift" bug.
//
// Failures are swallowed and the original operation is preserved so
// reconcile can never make behavior worse than the pre-reconcile
// baseline.
func (w *AutoMemoryWorker) reconcileOps(
	ctx context.Context,
	userKey memory.UserKey,
	ops []*extractor.Operation,
) []*extractor.Operation {
	if len(ops) == 0 || w.operator == nil {
		return ops
	}
	out := make([]*extractor.Operation, 0, len(ops))
	for _, op := range ops {
		if op == nil {
			continue
		}
		if op.Type != extractor.OperationAdd {
			out = append(out, op)
			continue
		}
		// Preserve the original Add tool gating. When the caller
		// disabled memory_add, reconcile must not sneak a mutation
		// through by rewriting the op into an Update. Leave the op
		// untouched and let executeOperation's EnabledTools check
		// skip it as it would have without reconcile.
		if !w.isToolEnabled(memory.AddToolName) {
			out = append(out, op)
			continue
		}
		decided := w.decideAddOp(ctx, userKey, op)
		// If reconcile rewrites an Add into an Update but memory_update
		// is disabled, the original Add would still have run under the
		// pre-reconcile behavior. Fall back to the original Add so the
		// Add tool gating keeps deciding the outcome, rather than
		// silently dropping the write.
		if decided != nil &&
			decided.Type == extractor.OperationUpdate &&
			!w.isToolEnabled(memory.UpdateToolName) {
			out = append(out, op)
			continue
		}
		if decided != nil {
			out = append(out, decided)
		}
	}
	return out
}

// decideAddOp inspects the store for memories similar to op.Memory and
// returns either the original op (keep as Add), a rewritten op (Update
// merging topics), or nil (drop the redundant Add).
func (w *AutoMemoryWorker) decideAddOp(
	ctx context.Context,
	userKey memory.UserKey,
	op *extractor.Operation,
) *extractor.Operation {
	query := strings.TrimSpace(op.Memory)
	if query == "" {
		return op
	}
	candidates, err := w.operator.SearchMemories(
		ctx, userKey, query,
		memory.WithSearchOptions(memory.SearchOptions{
			Query:               query,
			MaxResults:          reconcileTopK,
			SimilarityThreshold: reconcileMinProbeScore,
			// Kind / TimeAfter / TimeBefore intentionally left zero:
			// a new candidate should be compared against every stored
			// memory regardless of the classifier's current guess.
		}),
	)
	if err != nil || len(candidates) == 0 {
		return op
	}
	// Pick the candidate that produces the strongest reconcile
	// decision tier, not the highest Jaccard alone. Otherwise a
	// high-score duplicate could be shadowed by a candidate with
	// slightly higher token overlap that still sits below all
	// reconcile thresholds, causing the Add to be kept despite a
	// clearly duplicate entry existing.
	var best *memory.Entry
	bestJaccard := 0.0
	bestTier := -1
	for _, c := range candidates {
		if c == nil || c.Memory == nil {
			continue
		}
		j := tokenJaccard(op.Memory, c.Memory.Memory)
		tier := reconcileDecisionTier(c.Score, j)
		if best == nil ||
			tier > bestTier ||
			(tier == bestTier &&
				(c.Score > best.Score ||
					(c.Score == best.Score && j > bestJaccard))) {
			best = c
			bestJaccard = j
			bestTier = tier
		}
	}
	if best == nil || best.Memory == nil || best.ID == "" {
		return op
	}

	// Classify with two independent signals in logical OR so both
	// vector-backed and keyword-backed stores see reconcile kick in
	// on the same kinds of near-duplicates.
	switch bestTier {
	case reconcileTierSkip:
		if hasNewTopics(best.Memory.Topics, op.Topics) {
			log.DebugfContext(ctx,
				"auto_memory: reconcile merge topics for user %s/%s "+
					"(best=%s score=%.3f jaccard=%.3f)",
				userKey.AppName, userKey.UserID,
				best.ID, best.Score, bestJaccard)
			return toUpdateOp(op, best)
		}
		log.DebugfContext(ctx,
			"auto_memory: reconcile drop duplicate for user %s/%s "+
				"(best=%s score=%.3f jaccard=%.3f)",
			userKey.AppName, userKey.UserID,
			best.ID, best.Score, bestJaccard)
		return nil

	case reconcileTierUpdate:
		log.DebugfContext(ctx,
			"auto_memory: reconcile rewrite add as update for user "+
				"%s/%s (best=%s score=%.3f jaccard=%.3f)",
			userKey.AppName, userKey.UserID,
			best.ID, best.Score, bestJaccard)
		return toUpdateOp(op, best)

	default:
		return op
	}
}

// tokenJaccard returns the token-level Jaccard similarity between two
// memory texts using the same tokenizer stack that powers keyword
// search (gse segmentation for CJK plus CJK trigrams plus English
// tokens). This is what makes reconcile effective on "core entities
// match, filler words differ" phrasing drift regardless of whether
// the underlying store is vector-backed or keyword-backed.
func tokenJaccard(a, b string) float64 {
	as := textTokenSet(a)
	bs := textTokenSet(b)
	if len(as) == 0 && len(bs) == 0 {
		return 0
	}
	var inter int
	// Iterate over the smaller set for a cheap speedup.
	small, large := as, bs
	if len(bs) < len(as) {
		small, large = bs, as
	}
	for t := range small {
		if _, ok := large[t]; ok {
			inter++
		}
	}
	union := len(as) + len(bs) - inter
	if union == 0 {
		return 0
	}
	return float64(inter) / float64(union)
}

func textTokenSet(text string) map[string]struct{} {
	tokens := dedupStrings(append(
		BuildSearchTokens(text),
		buildFallbackCJKTrigrams(text)...,
	))
	set := make(map[string]struct{}, len(tokens))
	for _, t := range tokens {
		set[t] = struct{}{}
	}
	return set
}

// toUpdateOp converts an OperationAdd into an OperationUpdate that
// targets the given existing entry, merging topics so repeated
// extractions can accumulate category labels without inventing new
// synonymous topic names.
func toUpdateOp(op *extractor.Operation, best *memory.Entry) *extractor.Operation {
	merged := mergeTopics(best.Memory.Topics, op.Topics)
	updated := *op
	updated.Type = extractor.OperationUpdate
	updated.MemoryID = best.ID
	updated.Topics = merged
	// Preserve the existing memory kind when the extractor did not
	// classify this candidate itself. executeOperation -> opToMetadata
	// defaults an empty kind to KindFact and ApplyMetadataPatch always
	// writes Kind unconditionally, so a missing carry-over would
	// silently downgrade an episode (or any custom kind) on the
	// stored entry.
	if updated.MemoryKind == "" && best.Memory != nil {
		updated.MemoryKind = EffectiveKind(best.Memory)
	}
	// Keep other episodic metadata as-is: UpdateMemory flows through
	// ApplyMetadataPatch which only overwrites non-zero fields, so the
	// existing entry's metadata is preserved when the extractor did not
	// supply replacements.
	return &updated
}

// mergeTopics returns a case-insensitive de-duplicated union of the
// two topic slices, preserving the ordering of the existing slice
// first and appending new topics not yet present. Both inputs flow
// through the same trimming and empty-filtering pipeline so a
// fresh-only merge (when the existing entry had no topics) still
// emits a normalized slice.
func mergeTopics(existing, fresh []string) []string {
	seen := make(map[string]struct{}, len(existing)+len(fresh))
	out := make([]string, 0, len(existing)+len(fresh))
	for _, t := range existing {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	for _, t := range fresh {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		key := strings.ToLower(t)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, t)
	}
	return out
}

// hasNewTopics reports whether fresh contains any topic not already
// present in existing (case-insensitive).
func hasNewTopics(existing, fresh []string) bool {
	if len(fresh) == 0 {
		return false
	}
	known := make(map[string]struct{}, len(existing))
	for _, t := range existing {
		known[strings.ToLower(strings.TrimSpace(t))] = struct{}{}
	}
	for _, t := range fresh {
		k := strings.ToLower(strings.TrimSpace(t))
		if k == "" {
			continue
		}
		if _, ok := known[k]; !ok {
			return true
		}
	}
	return false
}
