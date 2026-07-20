//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package evolution

import (
	"context"
	"hash/fnv"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// Default values for worker configuration.
const (
	defaultWorkerNum  = 1
	defaultQueueSize  = 10
	defaultJobTimeout = 60 * time.Second

	// defaultExistingSkillBodyMaxChars caps the body excerpt the worker
	// loads per existing skill before handing the list to the reviewer.
	// Picked so a library of ~50 skills still fits comfortably in a
	// 32K-token reviewer prompt; bump it for libraries with longer
	// SKILL.md files or shrink it (or set 0) to disable bodies.
	defaultExistingSkillBodyMaxChars = 600
)

// worker manages async evolution workers.
//
// worker only manages reusable skills (create/update/delete via
// Publisher and skill.Repository). Durable fact memory is intentionally
// out of scope: it is owned by `memory.Service` + the auto-memory
// extractor (memory/<backend>.WithExtractor), so users get a single,
// dedup-aware fact pipeline instead of two competing writers against
// the same backend.
type worker struct {
	reviewer     Reviewer
	publisher    Publisher
	reviewPolicy ReviewPolicy
	skillRepo    skill.Repository
	repoProv     skill.RepositoryProvider

	workerNum                 int
	queueSize                 int
	jobTimeout                time.Duration
	existingSkillBodyMaxChars int
	skillScopeMode            skill.SkillScopeMode
	publisherBaseDir          string

	// Quality-gate plumbing. All nil-able; when any is set,
	// applyDecision routes writes through the revision pipeline
	// (candidate store → gates → publish) instead of direct publishing.
	candidateStore     CandidateStore
	activePointer      ActivePointer
	specGate           SpecGate
	safetyGate         SafetyGate
	effectivenessGate  EffectivenessGate
	humanGate          HumanGate
	managedSkillsDir   string
	approvalGateShadow bool
	candidateStoreRoot string
	activePointerRoot  string

	// approvalTimeout, approvalSweepInterval drive the optional
	// pending_approval auto-expiration sweeper. When approvalTimeout
	// > 0, Start launches a background goroutine that scans the
	// candidate store and auto-promotes revisions whose
	// pending_approval age exceeds approvalTimeout. sweepCancel is the
	// cancel func for the sweeper's root context; closing it both
	// stops the ticker loop and cancels any in-flight sweep so Stop
	// does not block on slow stores.
	approvalTimeout       time.Duration
	approvalSweepInterval time.Duration
	sweepCtx              context.Context
	sweepCancel           context.CancelFunc
	sweepDone             chan struct{}

	scopedMu       sync.Mutex
	scopedPubs     map[string]Publisher
	scopedStores   map[string]CandidateStore
	scopedPointers map[string]ActivePointer

	// approvalGateCounters records the last observed gate activity so
	// callers (benchmark, adopter metrics) can read counters after a
	// Close without racing against the worker goroutine.
	approvalGateCounters approvalGateCounters
	approvalGateMu       sync.Mutex

	jobChans []chan *pendingJob
	wg       sync.WaitGroup
	mu       sync.RWMutex
	started  bool
}

// approvalGateCounters counts what the gates have seen and done.
// Counters are cumulative across all jobs the worker has processed.
type approvalGateCounters struct {
	CandidatesSeen            int
	RevisionsWritten          int
	SpecGateRejected          int
	SafetyGateRejected        int
	EffectivenessGateRejected int
	HumanGateHeld             int
	RevisionsPromoted         int
	Rollbacks                 int
	DeletionsApplied          int
	UpdatesApplied            int
	CreatesApplied            int
	ShadowModeBypassed        int // revisions that failed gate but were still published due to shadow mode.
}

// pendingJob is the internal queue item: a public LearningJob plus the
// per-job context snapshot. We snapshot the context with WithoutCancel so
// the reviewer is not cancelled when the request that triggered the
// enqueue completes (online services typically tear down the request
// context immediately after returning to the user).
type pendingJob struct {
	ctx context.Context
	job LearningJob
}

// workerConfig holds configuration for the worker.
type workerConfig struct {
	Reviewer  Reviewer
	Publisher Publisher
	// PublisherBaseDir enables file-backed scope routing for Publisher.
	PublisherBaseDir string
	ReviewPolicy     ReviewPolicy
	SkillRepo        skill.Repository
	// SkillRepoProvider resolves the repository visible for each skill scope.
	SkillRepoProvider skill.RepositoryProvider
	// SkillScopeMode controls app-level sharing vs app+user isolation.
	SkillScopeMode skill.SkillScopeMode
	WorkerNum      int
	QueueSize      int
	JobTimeout     time.Duration

	// ExistingSkillBodyMaxChars caps the body excerpt the worker loads
	// per existing skill before sending the library snapshot to the
	// reviewer. Zero means "use the package default"; a
	// negative value means "do not include bodies at all" (description
	// only — the pre-P1 behavior).
	ExistingSkillBodyMaxChars int

	// Quality-gate plumbing. Any non-nil field opts into the revision
	// pipeline. Leaving all nil preserves direct-publish behavior
	// (reviewer output → Publisher → managed_skills/ immediately).
	CandidateStore     CandidateStore
	ActivePointer      ActivePointer
	SpecGate           SpecGate
	SafetyGate         SafetyGate
	EffectivenessGate  EffectivenessGate
	HumanGate          HumanGate
	ApprovalGateShadow bool

	// ManagedSkillsDir is the directory where evolution publishes SKILL.md
	// files. When set, the worker enforces write isolation: updates
	// targeting skills whose on-disk path is outside this directory are
	// skipped to protect bundled and user-authored skills.
	ManagedSkillsDir string

	// ApprovalTimeout enables the pending_approval auto-expiration
	// sweeper. Revisions that have been in pending_approval state for
	// longer than ApprovalTimeout are auto-promoted to active. Zero
	// disables the sweeper (default).
	ApprovalTimeout time.Duration
	// ApprovalSweepInterval overrides the sweep period; zero falls
	// back to min(ApprovalTimeout/4, 1h).
	ApprovalSweepInterval time.Duration
}

// newWorker creates a new worker.
func newWorker(cfg workerConfig) *worker {
	if cfg.WorkerNum <= 0 {
		cfg.WorkerNum = defaultWorkerNum
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = defaultQueueSize
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = defaultJobTimeout
	}
	if cfg.ReviewPolicy == nil {
		cfg.ReviewPolicy = DefaultReviewPolicy{}
	}
	bodyMax := cfg.ExistingSkillBodyMaxChars
	if bodyMax == 0 {
		bodyMax = defaultExistingSkillBodyMaxChars
	}
	w := &worker{
		reviewer:                  cfg.Reviewer,
		publisher:                 cfg.Publisher,
		reviewPolicy:              cfg.ReviewPolicy,
		skillRepo:                 cfg.SkillRepo,
		repoProv:                  cfg.SkillRepoProvider,
		workerNum:                 cfg.WorkerNum,
		queueSize:                 cfg.QueueSize,
		jobTimeout:                cfg.JobTimeout,
		existingSkillBodyMaxChars: bodyMax,
		skillScopeMode:            cfg.SkillScopeMode,
		publisherBaseDir:          cfg.PublisherBaseDir,
		candidateStore:            cfg.CandidateStore,
		activePointer:             cfg.ActivePointer,
		specGate:                  cfg.SpecGate,
		safetyGate:                cfg.SafetyGate,
		effectivenessGate:         cfg.EffectivenessGate,
		humanGate:                 cfg.HumanGate,
		approvalGateShadow:        cfg.ApprovalGateShadow,
		managedSkillsDir:          cfg.ManagedSkillsDir,
		approvalTimeout:           cfg.ApprovalTimeout,
		approvalSweepInterval:     cfg.ApprovalSweepInterval,
	}
	if store, ok := cfg.CandidateStore.(*fileCandidateStore); ok && store != nil {
		w.candidateStoreRoot = store.root
	}
	if ptr, ok := cfg.ActivePointer.(*fileActivePointer); ok && ptr != nil {
		w.activePointerRoot = ptr.root
	}
	return w
}

// Start launches the background processing goroutines.
func (w *worker) Start() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.started || w.reviewer == nil {
		return
	}
	w.jobChans = make([]chan *pendingJob, w.workerNum)
	for i := range w.jobChans {
		w.jobChans[i] = make(chan *pendingJob, w.queueSize)
	}
	w.wg.Add(w.workerNum)
	for _, ch := range w.jobChans {
		go func(ch chan *pendingJob) {
			defer w.wg.Done()
			for item := range ch {
				w.processJob(item)
			}
		}(ch)
	}
	w.startApprovalSweeperLocked()
	w.started = true
}

// Stop shuts down all workers and waits for them to finish.
func (w *worker) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.started || len(w.jobChans) == 0 {
		return
	}
	w.stopApprovalSweeperLocked()
	for _, ch := range w.jobChans {
		close(ch)
	}
	w.wg.Wait()
	w.jobChans = nil
	w.started = false
}

// Enqueue adds a learning job to the async queue. It falls back to synchronous
// processing when the queue is full or the worker has not been started.
//
// The caller's context is snapshotted with WithoutCancel before the job is
// queued so the reviewer continues to run even after the originating
// request context is torn down. Outcome (when set) is forwarded verbatim
// to the reviewer prompt.
func (w *worker) Enqueue(ctx context.Context, job LearningJob) error {
	if w.reviewer == nil || job.Session == nil {
		return nil
	}

	item := &pendingJob{
		ctx: context.WithoutCancel(ctx),
		job: job,
	}

	if w.tryEnqueue(ctx, job.Session, item) {
		return nil
	}
	if ctx.Err() != nil {
		return nil
	}
	log.DebugfContext(ctx, "evolution: queue full, processing synchronously for session %s",
		job.Session.ID)
	syncCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), w.jobTimeout)
	defer cancel()
	item.ctx = syncCtx
	w.processJob(item)
	return nil
}

func (w *worker) tryEnqueue(ctx context.Context, sess *session.Session, item *pendingJob) bool {
	if ctx.Err() != nil {
		return false
	}
	w.mu.RLock()
	defer w.mu.RUnlock()
	if !w.started || len(w.jobChans) == 0 {
		return false
	}
	idx := hashSession(sess) % len(w.jobChans)
	select {
	case w.jobChans[idx] <- item:
		return true
	default:
		return false
	}
}

func (w *worker) processJob(item *pendingJob) {
	defer func() {
		if r := recover(); r != nil {
			log.ErrorfContext(context.Background(), "evolution: panic in worker: %v", r)
		}
	}()

	ctx := item.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(ctx, w.jobTimeout)
	defer cancel()

	sess := item.job.Session
	scope, scoped, err := w.resolveJobScope(item.job)
	if err != nil {
		log.WarnfContext(ctx, "evolution: resolve skill scope for session %s failed: %v", sess.ID, err)
		return
	}
	repo, err := w.repositoryForScope(ctx, scope, scoped)
	if err != nil {
		log.WarnfContext(ctx, "evolution: resolve skill repo for session %s failed: %v", sess.ID, err)
		return
	}

	since := readLastReviewAt(sess)
	latestTs, reviewCtx := scanDelta(sess, since)
	if len(reviewCtx.Messages) == 0 {
		log.DebugfContext(ctx, "evolution: no messages in delta for session %s, skipping", sess.ID)
		return
	}

	policyInput := &ReviewPolicyInput{
		AppName:       sess.AppName,
		UserID:        sess.UserID,
		SessionID:     sess.ID,
		Scope:         scope,
		Scoped:        scoped,
		Outcome:       item.job.Outcome,
		ReviewContext: reviewCtx,
	}
	shouldReview, err := w.reviewPolicy.ShouldReview(ctx, policyInput)
	if err != nil {
		log.WarnfContext(ctx, "evolution: review policy failed for session %s: %v", sess.ID, err)
		return
	}
	if !shouldReview {
		log.DebugfContext(ctx, "evolution: review policy declined review for session %s (tool_calls=%d)",
			sess.ID, reviewCtx.ToolCallCount)
		writeLastReviewAt(sess, latestTs)
		return
	}

	log.InfofContext(ctx, "evolution: starting review for session %s (tool_calls=%d, messages=%d)",
		sess.ID, reviewCtx.ToolCallCount, len(reviewCtx.Messages))

	if hasSkillWritesInDelta(reviewCtx.Transcript) {
		writeLastReviewAt(sess, latestTs)
		return
	}

	existing := loadExistingSkills(repo, w.existingSkillBodyMaxChars)
	decision, err := w.reviewer.Review(ctx, sanitizeReviewInput(&ReviewInput{
		AppName:        sess.AppName,
		UserID:         sess.UserID,
		SessionID:      sess.ID,
		Messages:       reviewCtx.Messages,
		Transcript:     reviewCtx.Transcript,
		ExistingSkills: existing,
		Outcome:        item.job.Outcome,
	}))
	if err != nil {
		log.WarnfContext(ctx, "evolution: review failed for session %s: %v", sess.ID, err)
		return
	}
	if decision == nil {
		log.InfofContext(ctx, "evolution: review skipped for session %s: empty decision", sess.ID)
		writeLastReviewAt(sess, latestTs)
		return
	}
	if decision.SkipReason != "" {
		log.InfofContext(ctx, "evolution: review skipped for session %s: %s", sess.ID, decision.SkipReason)
		writeLastReviewAt(sess, latestTs)
		return
	}

	// Deterministic library-aware fixes after the LLM produced its raw
	// decision: strict-name-superset rewrites and intra-batch dedup.
	// These are pure string-shape rules and stay safe to enable
	// unconditionally (see reconcile.go for the rule set).
	decision, events := reconcileWithLibrary(decision, existing)
	for _, e := range events {
		log.InfofContext(ctx,
			"evolution: reconciler %s candidate=%q target=%q reason=%s",
			e.Kind, e.Original, e.Target, e.Reason)
	}

	w.applyDecision(ctx, decision, item.job.Outcome, scope, scoped, repo)
	writeLastReviewAt(sess, latestTs)
}

func (w *worker) applyDecision(ctx context.Context, decision *ReviewDecision, outcome *Outcome, scope skill.SkillScope, scoped bool, repo skill.Repository) {
	mutated := false
	if w.approvalGateEnabled() {
		existing := loadExistingSkills(repo, w.existingSkillBodyMaxChars)
		if w.applySkillsWithGate(ctx, decision.Skills, existing, outcome, scope, scoped) {
			mutated = true
		}
		if w.applyUpdatesWithGate(ctx, decision.Updates, existing, outcome, scope, scoped, repo) {
			mutated = true
		}
		if w.applyDeletionsWithGate(ctx, decision.Deletions, existing, outcome, scope, scoped, repo) {
			mutated = true
		}
	} else {
		if w.applySkills(ctx, decision.Skills, scope, scoped) {
			mutated = true
		}
		if w.applyUpdates(ctx, decision.Updates, scope, scoped, repo) {
			mutated = true
		}
		if w.applyDeletions(ctx, decision.Deletions, scope, scoped, repo) {
			mutated = true
		}
	}

	if !mutated {
		return
	}
	refreshable, ok := repo.(skill.RefreshableRepository)
	if !ok {
		return
	}
	if err := refreshable.Refresh(); err != nil {
		log.WarnfContext(ctx, "evolution: skill repo refresh failed: %v", err)
	}
}

// approvalGateEnabled reports whether any quality-gate component is
// configured. When true, applyDecision routes through the revision
// pipeline; when false, the original direct-publish path is used.
func (w *worker) approvalGateEnabled() bool {
	return w.candidateStore != nil || w.activePointer != nil ||
		w.specGate != nil || w.safetyGate != nil || w.effectivenessGate != nil ||
		w.humanGate != nil
}

func (w *worker) resolveJobScope(job LearningJob) (skill.SkillScope, bool, error) {
	if w.skillScopeMode == skill.SkillScopeNone && job.Scope.IsZero() {
		return skill.SkillScope{}, false, nil
	}
	mode := skill.NormalizeSkillScopeMode(w.skillScopeMode)
	if w.skillScopeMode == skill.SkillScopeNone && !job.Scope.IsZero() {
		// An explicit job scope should remain usable even when the service is
		// otherwise unscoped. UserID selects app+user isolation; otherwise
		// the explicit AppName selects app-level isolation for this single job.
		if strings.TrimSpace(job.Scope.UserID) != "" {
			mode = skill.SkillScopeUser
		} else {
			mode = skill.SkillScopeApp
		}
	}
	if job.Scope.IsZero() {
		if job.Session == nil {
			return skill.SkillScope{}, false, nil
		}
		scope, err := skill.NewSkillScope(mode, job.Session.AppName, job.Session.UserID)
		return scope, true, err
	}
	scope, err := skill.NewSkillScope(mode, job.Scope.AppName, job.Scope.UserID)
	return scope, true, err
}

func (w *worker) repositoryForScope(ctx context.Context, scope skill.SkillScope, scoped bool) (skill.Repository, error) {
	if !scoped || w.repoProv == nil {
		return w.skillRepo, nil
	}
	return w.repoProv.Repository(ctx, scope)
}

func (w *worker) scopedRoot(root string, scope skill.SkillScope, scoped bool) (string, error) {
	if !scoped || root == "" {
		return root, nil
	}
	mode := skill.NormalizeSkillScopeMode(w.skillScopeMode)
	if w.skillScopeMode == skill.SkillScopeNone {
		if strings.TrimSpace(scope.UserID) != "" {
			mode = skill.SkillScopeUser
		} else {
			mode = skill.SkillScopeApp
		}
	}
	parts, err := skill.ScopePathParts(mode, scope)
	if err != nil {
		return "", err
	}
	all := append([]string{root}, parts...)
	return filepath.Join(all...), nil
}

func (w *worker) publisherForScope(scope skill.SkillScope, scoped bool) (Publisher, error) {
	if !scoped {
		return w.publisher, nil
	}
	root, err := w.scopedRoot(w.publisherBaseDir, scope, scoped)
	if err != nil {
		return nil, err
	}
	if root == "" {
		return w.publisher, nil
	}
	key := "publisher:" + root
	w.scopedMu.Lock()
	defer w.scopedMu.Unlock()
	if w.scopedPubs == nil {
		w.scopedPubs = make(map[string]Publisher)
	}
	if p := w.scopedPubs[key]; p != nil {
		return p, nil
	}
	p := newFilePublisher(root)
	w.scopedPubs[key] = p
	return p, nil
}

func (w *worker) candidateStoreForScope(scope skill.SkillScope, scoped bool) (CandidateStore, error) {
	if !scoped {
		return w.candidateStore, nil
	}
	root, err := w.scopedRoot(w.candidateStoreRoot, scope, scoped)
	if err != nil {
		return nil, err
	}
	if root == "" {
		return w.candidateStore, nil
	}
	key := "store:" + root
	w.scopedMu.Lock()
	defer w.scopedMu.Unlock()
	if w.scopedStores == nil {
		w.scopedStores = make(map[string]CandidateStore)
	}
	if s := w.scopedStores[key]; s != nil {
		return s, nil
	}
	s := newFileCandidateStore(root)
	w.scopedStores[key] = s
	return s, nil
}

func (w *worker) activePointerForScope(scope skill.SkillScope, scoped bool) (ActivePointer, error) {
	if !scoped {
		return w.activePointer, nil
	}
	root, err := w.scopedRoot(w.activePointerRoot, scope, scoped)
	if err != nil {
		return nil, err
	}
	if root == "" {
		return w.activePointer, nil
	}
	key := "pointer:" + root
	w.scopedMu.Lock()
	defer w.scopedMu.Unlock()
	if w.scopedPointers == nil {
		w.scopedPointers = make(map[string]ActivePointer)
	}
	if p := w.scopedPointers[key]; p != nil {
		return p, nil
	}
	p := newFileActivePointer(root)
	w.scopedPointers[key] = p
	return p, nil
}

// approvalGateCountersCopy returns a copy of the worker's current
// gate-activity counters. Safe to call at any time; updates to the
// underlying counters are serialized by a mutex.
func (w *worker) approvalGateCountersCopy() approvalGateCounters {
	w.approvalGateMu.Lock()
	defer w.approvalGateMu.Unlock()
	return w.approvalGateCounters
}

// ApprovalGateMetrics is a public, exported view of the
// internal approvalGateCounters. Kept separate from the internal type
// so the internal one stays free to evolve.
type ApprovalGateMetrics struct {
	CandidatesSeen            int `json:"candidates_seen"`
	RevisionsWritten          int `json:"revisions_written"`
	SpecGateRejected          int `json:"spec_gate_rejected"`
	SafetyGateRejected        int `json:"safety_gate_rejected"`
	EffectivenessGateRejected int `json:"effectiveness_gate_rejected"`
	HumanGateHeld             int `json:"human_gate_held"`
	RevisionsPromoted         int `json:"revisions_promoted"`
	Rollbacks                 int `json:"rollbacks"`
	DeletionsApplied          int `json:"deletions_applied"`
	UpdatesApplied            int `json:"updates_applied"`
	CreatesApplied            int `json:"creates_applied"`
	ShadowModeBypassed        int `json:"shadow_mode_bypassed"`
}

// approvalGateMetrics returns an externally-visible snapshot of the
// approval-gate counters.
func (w *worker) approvalGateMetrics() ApprovalGateMetrics {
	m := w.approvalGateCountersCopy()
	return ApprovalGateMetrics{
		CandidatesSeen:            m.CandidatesSeen,
		RevisionsWritten:          m.RevisionsWritten,
		SpecGateRejected:          m.SpecGateRejected,
		SafetyGateRejected:        m.SafetyGateRejected,
		EffectivenessGateRejected: m.EffectivenessGateRejected,
		HumanGateHeld:             m.HumanGateHeld,
		RevisionsPromoted:         m.RevisionsPromoted,
		Rollbacks:                 m.Rollbacks,
		DeletionsApplied:          m.DeletionsApplied,
		UpdatesApplied:            m.UpdatesApplied,
		CreatesApplied:            m.CreatesApplied,
		ShadowModeBypassed:        m.ShadowModeBypassed,
	}
}

// bumpGateMetric applies a callback to the locked metrics struct.
// Keeps all metric bumps in one place so the mutex boundary is easy
// to audit.
func (w *worker) bumpGateMetric(fn func(*approvalGateCounters)) {
	w.approvalGateMu.Lock()
	defer w.approvalGateMu.Unlock()
	fn(&w.approvalGateCounters)
}

// -----------------------------------------------------------------------------
// Gated apply path. One function per action type; all share the same
// "write candidate -> run gates -> (shadow or enforced) publish ->
// update ActivePointer -> audit" shape.
// -----------------------------------------------------------------------------

func (w *worker) applySkillsWithGate(ctx context.Context, skills []*SkillSpec, existing []ExistingSkill, outcome *Outcome, scope skill.SkillScope, scoped bool) bool {
	mutated := false
	for _, spec := range skills {
		if spec == nil {
			continue
		}
		rev := w.buildRevision(spec, RevisionActionCreate, "")
		if w.processRevision(ctx, rev, existing, RevisionActionCreate, outcome, scope, scoped) {
			mutated = true
		}
	}
	return mutated
}

func (w *worker) applyUpdatesWithGate(ctx context.Context, updates []*SkillUpdate, existing []ExistingSkill, outcome *Outcome, scope skill.SkillScope, scoped bool, repo skill.Repository) bool {
	mutated := false
	for _, upd := range updates {
		if upd == nil || upd.NewSpec == nil {
			continue
		}
		if !skillExists(repo, upd.Name) {
			log.WarnfContext(ctx, "evolution: update skill %q skipped: not found in repo", upd.Name)
			continue
		}
		if !w.isEvolutionManagedSkill(upd.Name, repo, scope, scoped) {
			log.WarnfContext(ctx, "evolution: update skill %q skipped: not evolution-managed (protected)", upd.Name)
			continue
		}
		spec := *upd.NewSpec
		spec.Name = upd.Name // force stable on-disk name
		rev := w.buildRevision(&spec, RevisionActionUpdate, upd.Name)
		if w.processRevision(ctx, rev, existing, RevisionActionUpdate, outcome, scope, scoped) {
			mutated = true
		}
	}
	return mutated
}

func (w *worker) applyDeletionsWithGate(ctx context.Context, names []string, existing []ExistingSkill, outcome *Outcome, scope skill.SkillScope, scoped bool, repo skill.Repository) bool {
	mutated := false
	for _, name := range names {
		if strings.TrimSpace(name) == "" || !skillExists(repo, name) {
			continue
		}
		if !w.isEvolutionManagedSkill(name, repo, scope, scoped) {
			log.WarnfContext(ctx, "evolution: delete skill %q skipped: not evolution-managed (protected)", name)
			continue
		}
		rev := w.buildDeleteRevision(name)
		if w.processRevision(ctx, rev, existing, RevisionActionDelete, outcome, scope, scoped) {
			mutated = true
		}
	}
	return mutated
}

// buildRevision constructs a fresh Revision for a create or update.
func (w *worker) buildRevision(spec *SkillSpec, action RevisionAction, parentName string) *Revision {
	rev := &Revision{
		SkillID:    skillIDFromName(spec.Name),
		TargetName: spec.Name,
		RevisionID: newRevisionID(),
		Source:     "reviewer",
		Action:     action,
		Spec:       spec,
		Status:     RevisionPending,
		CreatedAt:  time.Now().UTC(),
	}
	if parentName != "" {
		rev.ParentID = skillIDFromName(parentName)
	}
	return rev
}

func (w *worker) buildDeleteRevision(name string) *Revision {
	return &Revision{
		SkillID:    skillIDFromName(name),
		TargetName: name,
		RevisionID: newRevisionID(),
		Source:     "reviewer",
		Action:     RevisionActionDelete,
		Status:     RevisionPending,
		CreatedAt:  time.Now().UTC(),
	}
}

// processRevision runs the full gate + publish + audit pipeline for
// one revision. Returns true when the live publisher was actually
// updated (so the worker knows to refresh the repository).
func (w *worker) processRevision(ctx context.Context, rev *Revision, existing []ExistingSkill, actionLabel RevisionAction, outcome *Outcome, scope skill.SkillScope, scoped bool) bool {
	w.bumpGateMetric(func(m *approvalGateCounters) { m.CandidatesSeen++ })
	store, err := w.candidateStoreForScope(scope, scoped)
	if err != nil {
		log.WarnfContext(ctx, "evolution: resolve candidate store failed: %v", err)
		return false
	}

	gatePassed := w.runGates(ctx, rev, existing, outcome)
	if !gatePassed && rev.Status == RevisionPending {
		rev.Status = RevisionRejected
	}

	// Always write the revision so the audit trail stays complete,
	// even for rejected revisions.
	if store != nil {
		if err := store.WriteRevision(ctx, rev); err != nil {
			log.WarnfContext(ctx, "evolution: write revision %s failed: %v", rev.RevisionID, err)
		} else {
			w.bumpGateMetric(func(m *approvalGateCounters) { m.RevisionsWritten++ })
		}
	}

	// Decide whether to publish.
	shouldPublish := gatePassed || w.approvalGateShadow
	if !shouldPublish {
		w.auditReject(ctx, rev, store)
		return false
	}
	if !gatePassed && w.approvalGateShadow {
		w.bumpGateMetric(func(m *approvalGateCounters) { m.ShadowModeBypassed++ })
		log.InfofContext(ctx,
			"evolution: shadow mode publishing failed revision %s (reasons=%v)",
			rev.RevisionID, gateRejectReason(rev))
	}

	return w.publishRevision(ctx, rev, actionLabel, gatePassed, scope, scoped, store)
}

// runGates evaluates spec, safety, and effectiveness gates in order.
// Returns true if all gates pass (or if no gates are configured).
func (w *worker) runGates(ctx context.Context, rev *Revision, existing []ExistingSkill, outcome *Outcome) bool {
	passed := true
	if rev.Action != RevisionActionDelete && w.specGate != nil {
		if !w.runSpecGate(ctx, rev, existing) {
			passed = false
		}
	}
	if rev.Action != RevisionActionDelete && w.safetyGate != nil {
		if !w.runSafetyGate(ctx, rev) {
			passed = false
		}
	}
	// Effectiveness gate. Only runs when spec+safety passed.
	if passed && w.effectivenessGate != nil {
		if !w.runEffectivenessGate(ctx, rev, outcome) {
			passed = false
		}
	}
	// Human gate. Only runs when all automatic gates passed.
	if passed && w.humanGate != nil {
		if !w.runHumanGate(ctx, rev, outcome) {
			passed = false
		}
	}
	return passed
}

func (w *worker) runSpecGate(ctx context.Context, rev *Revision, existing []ExistingSkill) bool {
	report, err := w.specGate.Validate(ctx, rev, existing)
	if err != nil {
		log.WarnfContext(ctx, "evolution: spec gate error on %q: %v", revisionTargetName(rev), err)
		return false // fail closed on error
	}
	rev.SpecReport = report
	if report != nil && !report.Passed {
		w.bumpGateMetric(func(m *approvalGateCounters) { m.SpecGateRejected++ })
		log.InfofContext(ctx,
			"evolution: spec gate rejected %q revision=%s reasons=%v",
			rev.Spec.Name, rev.RevisionID, report.Reasons)
		return false
	}
	return true
}

func (w *worker) runSafetyGate(ctx context.Context, rev *Revision) bool {
	report, err := w.safetyGate.Scan(ctx, rev)
	if err != nil {
		log.WarnfContext(ctx, "evolution: safety gate error on %q: %v", revisionTargetName(rev), err)
		return false // fail closed on error
	}
	rev.SafetyReport = report
	if report != nil && !report.Passed {
		w.bumpGateMetric(func(m *approvalGateCounters) { m.SafetyGateRejected++ })
		log.InfofContext(ctx,
			"evolution: safety gate rejected %q revision=%s reasons=%v",
			rev.Spec.Name, rev.RevisionID, report.Reasons)
		return false
	}
	return true
}

func (w *worker) runEffectivenessGate(ctx context.Context, rev *Revision, outcome *Outcome) bool {
	report, err := w.effectivenessGate.Evaluate(ctx, rev, outcome)
	if err != nil {
		log.WarnfContext(ctx, "evolution: effectiveness gate error on %q: %v", revisionTargetName(rev), err)
		return false // fail closed on error
	}
	rev.EffectivenessReport = report
	if report != nil && !report.Passed {
		rev.Status = RevisionPendingEval
		w.bumpGateMetric(func(m *approvalGateCounters) { m.EffectivenessGateRejected++ })
		log.InfofContext(ctx,
			"evolution: effectiveness gate held %q revision=%s reasons=%v",
			revisionTargetName(rev), rev.RevisionID, report.Reasons)
		return false
	}
	return true
}

func (w *worker) runHumanGate(ctx context.Context, rev *Revision, outcome *Outcome) bool {
	hold, err := w.humanGate.ShouldHold(ctx, rev, outcome)
	if err != nil {
		log.WarnfContext(ctx, "evolution: human gate error on %q: %v", revisionTargetName(rev), err)
		hold = true // fail-closed
	}
	rev.HumanReport = &HumanReport{Held: hold}
	if hold {
		rev.Status = RevisionPendingApproval
		w.bumpGateMetric(func(m *approvalGateCounters) { m.HumanGateHeld++ })
		log.InfofContext(ctx, "evolution: human gate held %q revision=%s for approval",
			revisionTargetName(rev), rev.RevisionID)
		return false
	}
	return true
}

// auditReject appends a rejection audit event.
func (w *worker) auditReject(ctx context.Context, rev *Revision, store CandidateStore) {
	if store != nil {
		_ = store.AppendAudit(ctx, AuditEvent{
			Action:     AuditActionReject,
			SkillID:    rev.SkillID,
			RevisionID: rev.RevisionID,
			Status:     string(rev.Status),
			Reason:     gateRejectReason(rev),
		})
	}
}

// publishRevision writes the skill, updates the active pointer,
// and appends a promotion audit event.
func (w *worker) publishRevision(ctx context.Context, rev *Revision, actionLabel RevisionAction, gatePassed bool, scope skill.SkillScope, scoped bool, store CandidateStore) bool {
	publisher, err := w.publisherForScope(scope, scoped)
	if err != nil {
		log.WarnfContext(ctx, "evolution: resolve publisher failed: %v", err)
		return false
	}
	pointer, err := w.activePointerForScope(scope, scoped)
	if err != nil {
		log.WarnfContext(ctx, "evolution: resolve active pointer failed: %v", err)
		return false
	}
	if publisher == nil {
		return false
	}
	switch rev.Action {
	case RevisionActionDelete:
		if err := publisher.DeleteSkill(ctx, revisionTargetName(rev)); err != nil {
			log.WarnfContext(ctx, "evolution: delete revision %s failed: %v", rev.RevisionID, err)
			return false
		}
	default:
		if rev.Spec == nil {
			return false
		}
		if err := publisher.UpsertSkill(ctx, rev.Spec); err != nil {
			log.WarnfContext(ctx, "evolution: publish revision %s failed: %v", rev.RevisionID, err)
			return false
		}
	}
	if gatePassed {
		rev.Status = RevisionActive
	}
	now := time.Now().UTC()
	rev.PromotedAt = &now
	// Rewrite meta.json to reflect the new status/promoted_at.
	if store != nil {
		_ = store.WriteRevision(ctx, rev)
	}
	if pointer != nil {
		if err := archiveCurrentActiveRevision(ctx, store, pointer, rev.SkillID, rev.RevisionID); err != nil {
			log.WarnfContext(ctx, "evolution: archive active revision %q failed: %v", rev.SkillID, err)
			return false
		}
		var err error
		if rev.Action == RevisionActionDelete {
			err = pointer.Clear(ctx, rev.SkillID)
		} else {
			err = pointer.Set(ctx, rev.SkillID, rev.RevisionID)
		}
		if err != nil {
			log.WarnfContext(ctx, "evolution: active pointer set %s failed: %v", rev.SkillID, err)
		}
	}
	if store != nil {
		_ = store.AppendAudit(ctx, AuditEvent{
			Action:     auditActionForRevisionPromotion(rev),
			SkillID:    rev.SkillID,
			RevisionID: rev.RevisionID,
			Status:     string(rev.Status),
			Reason:     string(actionLabel),
		})
	}
	w.bumpGateMetric(func(m *approvalGateCounters) {
		m.RevisionsPromoted++
		switch actionLabel {
		case RevisionActionCreate:
			m.CreatesApplied++
		case RevisionActionUpdate:
			m.UpdatesApplied++
		case RevisionActionDelete:
			m.DeletionsApplied++
		}
	})
	return true
}

func auditActionForRevisionPromotion(rev *Revision) AuditAction {
	if rev != nil && rev.Action == RevisionActionDelete {
		return AuditActionDelete
	}
	return AuditActionPromote
}

// gateRejectReason returns a short human-readable reason from the
// gate reports on a revision. Used when appending audit events for
// rejections.
func gateRejectReason(rev *Revision) string {
	var parts []string
	if rev.SpecReport != nil && !rev.SpecReport.Passed {
		parts = append(parts, "spec:"+strings.Join(rev.SpecReport.Reasons, ";"))
	}
	if rev.SafetyReport != nil && !rev.SafetyReport.Passed {
		parts = append(parts, "safety:"+strings.Join(rev.SafetyReport.Reasons, ";"))
	}
	if rev.EffectivenessReport != nil && !rev.EffectivenessReport.Passed {
		parts = append(parts, "effectiveness:"+strings.Join(rev.EffectivenessReport.Reasons, ";"))
	}
	if rev.HumanReport != nil && rev.HumanReport.Held {
		parts = append(parts, "human:held_for_approval")
	}
	if len(parts) == 0 {
		return "rejected"
	}
	return strings.Join(parts, " | ")
}

func (w *worker) applySkills(ctx context.Context, skills []*SkillSpec, scope skill.SkillScope, scoped bool) bool {
	publisher, err := w.publisherForScope(scope, scoped)
	if err != nil {
		log.WarnfContext(ctx, "evolution: resolve publisher failed: %v", err)
		return false
	}
	if publisher == nil {
		return false
	}
	mutated := false
	for _, spec := range skills {
		if spec == nil {
			continue
		}
		if err := publisher.UpsertSkill(ctx, spec); err != nil {
			log.WarnfContext(ctx, "evolution: upsert skill %q failed: %v", spec.Name, err)
			continue
		}
		mutated = true
	}
	return mutated
}

// isEvolutionManagedSkill checks whether the named skill resides within
// the evolution-managed directory. Returns true (allow write) if:
//   - managedSkillsDir is not configured (no isolation enforced)
//   - skill's on-disk path is under managedSkillsDir
func (w *worker) isEvolutionManagedSkill(name string, repo skill.Repository, scope skill.SkillScope, scoped bool) bool {
	managedDir, err := w.scopedRoot(w.managedSkillsDir, scope, scoped)
	if err != nil {
		return false
	}
	if managedDir == "" || repo == nil {
		return true
	}
	p, err := repo.Path(name)
	if err != nil {
		return !skillExists(repo, name)
	}
	rel, err := filepath.Rel(managedDir, p)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) && !filepath.IsAbs(rel)
}

func (w *worker) applyUpdates(ctx context.Context, updates []*SkillUpdate, scope skill.SkillScope, scoped bool, repo skill.Repository) bool {
	publisher, err := w.publisherForScope(scope, scoped)
	if err != nil {
		log.WarnfContext(ctx, "evolution: resolve publisher failed: %v", err)
		return false
	}
	if publisher == nil {
		return false
	}
	mutated := false
	for _, upd := range updates {
		if upd == nil || upd.NewSpec == nil {
			continue
		}
		if !skillExists(repo, upd.Name) {
			log.WarnfContext(ctx, "evolution: update skill %q skipped: not found in repo", upd.Name)
			continue
		}
		// Write isolation: only update skills that live within the
		// evolution-managed directory. Bundled and user-authored skills
		// are protected from accidental overwrites.
		if !w.isEvolutionManagedSkill(upd.Name, repo, scope, scoped) {
			log.WarnfContext(ctx, "evolution: update skill %q skipped: not evolution-managed (protected)", upd.Name)
			continue
		}
		// Force the on-disk directory name to remain stable.
		upd.NewSpec.Name = upd.Name
		if err := publisher.UpsertSkill(ctx, upd.NewSpec); err != nil {
			log.WarnfContext(ctx, "evolution: update skill %q failed: %v", upd.Name, err)
			continue
		}
		mutated = true
	}
	return mutated
}

func (w *worker) applyDeletions(ctx context.Context, names []string, scope skill.SkillScope, scoped bool, repo skill.Repository) bool {
	publisher, err := w.publisherForScope(scope, scoped)
	if err != nil {
		log.WarnfContext(ctx, "evolution: resolve publisher failed: %v", err)
		return false
	}
	if publisher == nil {
		return false
	}
	mutated := false
	for _, name := range names {
		if strings.TrimSpace(name) == "" || !skillExists(repo, name) {
			// Idempotent: nothing to delete (or never existed).
			continue
		}
		// Write isolation: only delete skills that live within the
		// evolution-managed directory. Bundled and user-authored skills
		// are protected from accidental deletion.
		if !w.isEvolutionManagedSkill(name, repo, scope, scoped) {
			log.WarnfContext(ctx, "evolution: delete skill %q skipped: not evolution-managed (protected)", name)
			continue
		}
		if err := publisher.DeleteSkill(ctx, name); err != nil {
			log.WarnfContext(ctx, "evolution: delete skill %q failed: %v", name, err)
			continue
		}
		mutated = true
	}
	return mutated
}

// skillExists reports whether the repo currently contains a skill with the
// given exact name. A nil repo or empty name returns false so callers
// reject unknown targets safely.
func skillExists(repo skill.Repository, name string) bool {
	if repo == nil || strings.TrimSpace(name) == "" {
		return false
	}
	got, err := repo.Get(name)
	return err == nil && got != nil
}

// scanDelta extracts the session delta since the given timestamp and builds a
// ReviewContext with heuristic signals.
func scanDelta(sess *session.Session, since time.Time) (time.Time, *ReviewContext) {
	var (
		latestTs      time.Time
		messages      []model.Message
		transcript    []ReviewMessage
		toolCallCount int
		hasCorrection bool
		hasRecovered  bool
		lastRole      model.Role
		sawError      bool
	)

	sess.EventMu.RLock()
	defer sess.EventMu.RUnlock()

	for _, e := range sess.Events {
		if !since.IsZero() && !e.Timestamp.After(since) {
			continue
		}
		if e.Timestamp.After(latestTs) {
			latestTs = e.Timestamp
		}
		if e.Response == nil {
			continue
		}
		for _, choice := range e.Response.Choices {
			msg := choice.Message

			// Count tool calls.
			toolCallCount += len(msg.ToolCalls)

			if reviewMsg, ok := buildReviewMessage(msg); ok {
				transcript = append(transcript, reviewMsg)
			}

			// Track error signals from tool responses.
			if msg.Role == model.RoleTool {
				if looksLikeError(msg.Content) {
					sawError = true
				}
				continue
			}

			// Detect user correction: user message right after an assistant turn.
			if msg.Role == model.RoleUser && lastRole == model.RoleAssistant {
				if looksLikeCorrection(msg.Content) {
					hasCorrection = true
				}
			}

			// Detect recovered error: assistant continues after a tool error.
			if msg.Role == model.RoleAssistant && sawError {
				hasRecovered = true
				sawError = false
			}

			if msg.Role == model.RoleUser || msg.Role == model.RoleAssistant {
				if msg.Content != "" || len(msg.ContentParts) > 0 {
					messages = append(messages, msg)
					lastRole = msg.Role
				}
			}
		}
	}

	return latestTs, &ReviewContext{
		LatestTs:          latestTs,
		Messages:          messages,
		Transcript:        transcript,
		ToolCallCount:     toolCallCount,
		HasUserCorrection: hasCorrection,
		HasRecoveredError: hasRecovered,
	}
}

func buildReviewMessage(msg model.Message) (ReviewMessage, bool) {
	reviewMsg := reviewMessageFromModel(msg)
	if reviewMsg.Content == "" && reviewMsg.ToolName == "" && len(reviewMsg.ToolCalls) == 0 {
		return ReviewMessage{}, false
	}
	return reviewMsg, true
}

// hasSkillWritesInDelta checks whether the assistant already wrote skill
// files in this delta via a generic filesystem / shell tool, which would
// mean the main flow is managing skills outside the evolution Publisher
// and the background reviewer should not compete.
//
// evolution itself no longer ships an agent-facing skill_manage tool
// (that path was found, in benchmark v1, to add prompt overhead without
// ever being exercised by the model and was removed in favor of the
// reviewer-driven path). The SKILL.md filename heuristic still matters
// because users can wire up a filesystem MCP server that lets the agent
// write SKILL.md files directly.
func hasSkillWritesInDelta(messages []ReviewMessage) bool {
	for _, msg := range messages {
		if containsSkillWriteText(msg.Content) {
			return true
		}
		for _, call := range msg.ToolCalls {
			if isSkillWriteToolCall(call) {
				return true
			}
		}
		if isSkillWriteToolResult(msg) {
			return true
		}
	}
	return false
}

func containsSkillWriteText(content string) bool {
	return strings.Contains(strings.ToLower(content), "skill.md")
}

func isSkillWriteToolResult(msg ReviewMessage) bool {
	if msg.Role != model.RoleTool {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(msg.ToolName))
	if name == "" {
		return false
	}
	if !strings.Contains(strings.ToLower(msg.Content), "skill.md") {
		return false
	}
	return strings.Contains(name, "write") ||
		strings.Contains(name, "edit") ||
		strings.Contains(name, "patch") ||
		strings.Contains(name, "workspace") ||
		strings.Contains(name, "file")
}

func isSkillWriteToolCall(call ReviewToolCall) bool {
	name := strings.ToLower(strings.TrimSpace(call.Name))
	args := strings.ToLower(strings.TrimSpace(call.Arguments))
	if name == "" || !strings.Contains(args, "skill.md") {
		return false
	}
	if strings.Contains(name, "write") ||
		strings.Contains(name, "edit") ||
		strings.Contains(name, "patch") ||
		strings.Contains(name, "apply") {
		return true
	}
	if strings.Contains(name, "exec") || strings.Contains(name, "shell") || strings.Contains(name, "workspace") {
		return containsMutationCommand(args)
	}
	return false
}

func containsMutationCommand(args string) bool {
	markers := []string{
		"apply_patch",
		"cat <<",
		"cat >",
		"tee ",
		">>",
		"printf ",
		"echo ",
		"mkdir ",
		"cp ",
		"mv ",
		"sed -i",
		"python ",
	}
	for _, marker := range markers {
		if strings.Contains(args, marker) {
			return true
		}
	}
	return false
}

// looksLikeCorrection uses simple heuristics to detect a user correction.
func looksLikeCorrection(content string) bool {
	lower := strings.ToLower(content)
	markers := []string{
		"no,", "wrong", "actually", "instead", "not what i",
		"that's incorrect", "please fix", "try again",
		"不对", "错了", "不是", "而是", "请修正",
		"请修改", "改成", "重新来", "重来",
	}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return looksLikeFutureWorkflowFeedback(lower)
}

func looksLikeFutureWorkflowFeedback(lower string) bool {
	futureMarkers := []string{
		"next time", "going forward", "in the future", "future",
		"default to", "by default", "reuse", "keep using",
		"以后", "今后", "后续", "下次", "未来", "默认",
	}
	workflowMarkers := []string{
		"workflow", "procedure", "process", "steps", "checklist",
		"template", "format", "structure", "schema", "fields",
		"category", "classification", "output", "rule",
		"工作流", "流程", "步骤", "清单", "模板", "格式",
		"结构", "字段", "分类", "输出", "规则", "按这套",
		"照这个", "保持这个", "固定",
	}
	hasFuture := false
	for _, m := range futureMarkers {
		if strings.Contains(lower, m) {
			hasFuture = true
			break
		}
	}
	if !hasFuture {
		return false
	}
	for _, m := range workflowMarkers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

// looksLikeError uses simple heuristics to detect an error in tool output.
func looksLikeError(content string) bool {
	lower := strings.ToLower(content)
	markers := []string{"error:", "failed:", "exception:", "traceback"}
	for _, m := range markers {
		if strings.Contains(lower, m) {
			return true
		}
	}
	return false
}

func readLastReviewAt(sess *session.Session) time.Time {
	raw, ok := sess.GetState(sessionStateKeyLastReviewAt)
	if !ok || len(raw) == 0 {
		return time.Time{}
	}
	ts, err := time.Parse(time.RFC3339Nano, string(raw))
	if err != nil {
		return time.Time{}
	}
	return ts
}

func writeLastReviewAt(sess *session.Session, ts time.Time) {
	sess.SetState(sessionStateKeyLastReviewAt,
		[]byte(ts.UTC().Format(time.RFC3339Nano)))
}

func hashSession(sess *session.Session) int {
	h := fnv.New32a()
	h.Write([]byte(sess.AppName))
	h.Write([]byte(sess.UserID))
	return int(h.Sum32())
}
