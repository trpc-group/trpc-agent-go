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
	DefaultWorkerNum  = 1
	DefaultQueueSize  = 10
	DefaultJobTimeout = 60 * time.Second

	// DefaultExistingSkillBodyMaxChars caps the body excerpt the worker
	// loads per existing skill before handing the list to the reviewer.
	// Picked so a library of ~50 skills still fits comfortably in a
	// 32K-token reviewer prompt; bump it for libraries with longer
	// SKILL.md files or shrink it (or set 0) to disable bodies.
	DefaultExistingSkillBodyMaxChars = 600
)

// Worker manages async evolution workers.
//
// Worker only manages reusable skills (create/update/delete via
// Publisher and skill.Repository). Durable fact memory is intentionally
// out of scope: it is owned by `memory.Service` + the auto-memory
// extractor (memory/<backend>.WithExtractor), so users get a single,
// dedup-aware fact pipeline instead of two competing writers against
// the same backend.
type Worker struct {
	reviewer  Reviewer
	publisher Publisher
	policy    Policy
	skillRepo skill.Repository

	workerNum                 int
	queueSize                 int
	jobTimeout                time.Duration
	existingSkillBodyMaxChars int

	// Approval-gate plumbing (Phase A + B). All nil-able; when any is
	// set, applyDecision routes writes through the revision pipeline.
	candidateStore     CandidateStore
	activePointer      ActivePointer
	specGate           SpecGate
	safetyGate         SafetyGate
	effectivenessGate  EffectivenessGate
	approvalGateShadow bool

	// approvalGateMetrics records the last observed gate activity so
	// callers (benchmark, adopter metrics) can read counters after a
	// Close without racing against the worker goroutine.
	approvalGateMetrics approvalGateMetrics
	approvalGateMu      sync.Mutex

	jobChans []chan *pendingJob
	wg       sync.WaitGroup
	mu       sync.RWMutex
	started  bool
}

// approvalGateMetrics counts what the gates have seen and done.
// Counters are cumulative across all jobs the worker has processed.
type approvalGateMetrics struct {
	CandidatesSeen            int
	RevisionsWritten          int
	SpecGateRejected          int
	SafetyGateRejected        int
	EffectivenessGateRejected int
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

// WorkerConfig holds configuration for the Worker.
type WorkerConfig struct {
	Reviewer   Reviewer
	Publisher  Publisher
	Policy     Policy
	SkillRepo  skill.Repository
	WorkerNum  int
	QueueSize  int
	JobTimeout time.Duration

	// ExistingSkillBodyMaxChars caps the body excerpt the worker loads
	// per existing skill before sending the library snapshot to the
	// reviewer. Zero means "use DefaultExistingSkillBodyMaxChars"; a
	// negative value means "do not include bodies at all" (description
	// only — the pre-P1 behavior).
	ExistingSkillBodyMaxChars int

	// Approval-gate plumbing. Any non-nil field opts into the Phase A
	// revision pipeline. Leaving all nil preserves exact pre-Phase-A
	// behavior (direct Publisher writes, no candidate store).
	CandidateStore     CandidateStore
	ActivePointer      ActivePointer
	SpecGate           SpecGate
	SafetyGate         SafetyGate
	EffectivenessGate  EffectivenessGate
	ApprovalGateShadow bool
}

// NewWorker creates a new Worker.
func NewWorker(cfg WorkerConfig) *Worker {
	if cfg.WorkerNum <= 0 {
		cfg.WorkerNum = DefaultWorkerNum
	}
	if cfg.QueueSize <= 0 {
		cfg.QueueSize = DefaultQueueSize
	}
	if cfg.JobTimeout <= 0 {
		cfg.JobTimeout = DefaultJobTimeout
	}
	if cfg.Policy == nil {
		cfg.Policy = DefaultPolicy{}
	}
	bodyMax := cfg.ExistingSkillBodyMaxChars
	if bodyMax == 0 {
		bodyMax = DefaultExistingSkillBodyMaxChars
	}
	return &Worker{
		reviewer:                  cfg.Reviewer,
		publisher:                 cfg.Publisher,
		policy:                    cfg.Policy,
		skillRepo:                 cfg.SkillRepo,
		workerNum:                 cfg.WorkerNum,
		queueSize:                 cfg.QueueSize,
		jobTimeout:                cfg.JobTimeout,
		existingSkillBodyMaxChars: bodyMax,
		candidateStore:            cfg.CandidateStore,
		activePointer:             cfg.ActivePointer,
		specGate:                  cfg.SpecGate,
		safetyGate:                cfg.SafetyGate,
		effectivenessGate:         cfg.EffectivenessGate,
		approvalGateShadow:        cfg.ApprovalGateShadow,
	}
}

// Start launches the background processing goroutines.
func (w *Worker) Start() {
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
	w.started = true
}

// Stop shuts down all workers and waits for them to finish.
func (w *Worker) Stop() {
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

// Enqueue adds a learning job to the async queue. It falls back to synchronous
// processing when the queue is full or the worker has not been started.
//
// The caller's context is snapshotted with WithoutCancel before the job is
// queued so the reviewer continues to run even after the originating
// request context is torn down. Outcome (when set) is forwarded verbatim
// to the reviewer prompt.
func (w *Worker) Enqueue(ctx context.Context, job LearningJob) error {
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

func (w *Worker) tryEnqueue(ctx context.Context, sess *session.Session, item *pendingJob) bool {
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

func (w *Worker) processJob(item *pendingJob) {
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

	since := readLastReviewAt(sess)
	latestTs, reviewCtx := scanDelta(sess, since)
	if len(reviewCtx.Messages) == 0 {
		return
	}

	if !w.policy.ShouldReview(reviewCtx) {
		writeLastReviewAt(sess, latestTs)
		return
	}

	if hasSkillWritesInDelta(reviewCtx.Transcript) {
		writeLastReviewAt(sess, latestTs)
		return
	}

	existing := loadExistingSkills(w.skillRepo, w.existingSkillBodyMaxChars)
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
	if decision == nil || decision.SkipReason != "" {
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

	w.applyDecision(ctx, decision, item.job.Outcome)
	writeLastReviewAt(sess, latestTs)
}

func (w *Worker) applyDecision(ctx context.Context, decision *ReviewDecision, outcome *Outcome) {
	mutated := false
	if w.approvalGateEnabled() {
		existing := loadExistingSkills(w.skillRepo, w.existingSkillBodyMaxChars)
		if w.applySkillsWithGate(ctx, decision.Skills, existing, outcome) {
			mutated = true
		}
		if w.applyUpdatesWithGate(ctx, decision.Updates, existing, outcome) {
			mutated = true
		}
		if w.applyDeletionsWithGate(ctx, decision.Deletions) {
			mutated = true
		}
	} else {
		if w.applySkills(ctx, decision.Skills) {
			mutated = true
		}
		if w.applyUpdates(ctx, decision.Updates) {
			mutated = true
		}
		if w.applyDeletions(ctx, decision.Deletions) {
			mutated = true
		}
	}

	if !mutated {
		return
	}
	refreshable, ok := w.skillRepo.(skill.RefreshableRepository)
	if !ok {
		return
	}
	if err := refreshable.Refresh(); err != nil {
		log.WarnfContext(ctx, "evolution: skill repo refresh failed: %v", err)
	}
}

// approvalGateEnabled reports whether any Phase A/B component is
// configured. When true, applyDecision routes through the revision
// pipeline; when false, the original direct-publish path is used.
func (w *Worker) approvalGateEnabled() bool {
	return w.candidateStore != nil || w.activePointer != nil ||
		w.specGate != nil || w.safetyGate != nil || w.effectivenessGate != nil
}

// ApprovalGateMetrics returns a copy of the worker's current
// gate-activity counters. Safe to call at any time; updates to the
// underlying counters are serialized by a mutex.
func (w *Worker) ApprovalGateMetrics() approvalGateMetrics {
	w.approvalGateMu.Lock()
	defer w.approvalGateMu.Unlock()
	return w.approvalGateMetrics
}

// ApprovalGateMetricsSnapshot is a public, exported view of the
// internal approvalGateMetrics. Kept separate from the internal type
// so the internal one stays free to evolve.
type ApprovalGateMetricsSnapshot struct {
	CandidatesSeen            int `json:"candidates_seen"`
	RevisionsWritten          int `json:"revisions_written"`
	SpecGateRejected          int `json:"spec_gate_rejected"`
	SafetyGateRejected        int `json:"safety_gate_rejected"`
	EffectivenessGateRejected int `json:"effectiveness_gate_rejected"`
	RevisionsPromoted         int `json:"revisions_promoted"`
	Rollbacks                 int `json:"rollbacks"`
	DeletionsApplied          int `json:"deletions_applied"`
	UpdatesApplied            int `json:"updates_applied"`
	CreatesApplied            int `json:"creates_applied"`
	ShadowModeBypassed        int `json:"shadow_mode_bypassed"`
}

// ApprovalGateMetricsJSON returns an externally-visible snapshot of the
// approval-gate counters. Callers that want JSON-friendly output use
// this; callers that already work with the internal type use
// ApprovalGateMetrics. Both return the same numbers.
func (w *Worker) ApprovalGateMetricsJSON() ApprovalGateMetricsSnapshot {
	m := w.ApprovalGateMetrics()
	return ApprovalGateMetricsSnapshot{
		CandidatesSeen:            m.CandidatesSeen,
		RevisionsWritten:          m.RevisionsWritten,
		SpecGateRejected:          m.SpecGateRejected,
		SafetyGateRejected:        m.SafetyGateRejected,
		EffectivenessGateRejected: m.EffectivenessGateRejected,
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
func (w *Worker) bumpGateMetric(fn func(*approvalGateMetrics)) {
	w.approvalGateMu.Lock()
	defer w.approvalGateMu.Unlock()
	fn(&w.approvalGateMetrics)
}

// -----------------------------------------------------------------------------
// Gated apply path. One function per action type; all share the same
// "write candidate -> run gates -> (shadow or enforced) publish ->
// update ActivePointer -> audit" shape.
// -----------------------------------------------------------------------------

func (w *Worker) applySkillsWithGate(ctx context.Context, skills []*SkillSpec, existing []ExistingSkill, outcome *Outcome) bool {
	mutated := false
	for _, spec := range skills {
		if spec == nil {
			continue
		}
		rev := w.buildRevision(spec, "create", "")
		if w.processRevision(ctx, rev, existing, "create", outcome) {
			mutated = true
		}
	}
	return mutated
}

func (w *Worker) applyUpdatesWithGate(ctx context.Context, updates []*SkillUpdate, existing []ExistingSkill, outcome *Outcome) bool {
	mutated := false
	for _, upd := range updates {
		if upd == nil || upd.NewSpec == nil {
			continue
		}
		if !skillExists(w.skillRepo, upd.Name) {
			log.WarnfContext(ctx, "evolution: update skill %q skipped: not found in repo", upd.Name)
			continue
		}
		spec := *upd.NewSpec
		spec.Name = upd.Name // force stable on-disk name
		rev := w.buildRevision(&spec, "update", upd.Name)
		if w.processRevision(ctx, rev, existing, "update", outcome) {
			mutated = true
		}
	}
	return mutated
}

func (w *Worker) applyDeletionsWithGate(ctx context.Context, names []string) bool {
	mutated := false
	for _, name := range names {
		if name == "" || !skillExists(w.skillRepo, name) {
			continue
		}
		rev := &Revision{
			SkillID:    SkillIDFromName(name),
			RevisionID: NewRevisionID(),
			Source:     "reviewer",
			Action:     "delete",
			Status:     RevisionPending,
			CreatedAt:  time.Now().UTC(),
		}
		w.bumpGateMetric(func(m *approvalGateMetrics) { m.CandidatesSeen++ })
		// No spec gate / safety gate for deletes — the Spec is nil by
		// design. We still log the revision for audit and rollback.
		if w.candidateStore != nil {
			if err := w.candidateStore.WriteRevision(ctx, rev); err != nil {
				log.WarnfContext(ctx, "evolution: write delete revision failed: %v", err)
			} else {
				w.bumpGateMetric(func(m *approvalGateMetrics) { m.RevisionsWritten++ })
			}
		}
		if w.publisher != nil {
			if err := w.publisher.DeleteSkill(ctx, name); err != nil {
				log.WarnfContext(ctx, "evolution: delete skill %q failed: %v", name, err)
				continue
			}
			rev.Status = RevisionActive
			now := time.Now().UTC()
			rev.PromotedAt = &now
		} else {
			// No publisher means we cannot actually delete the skill
			// from the live repository. Skip pointer/audit/metrics to
			// avoid diverging state.
			continue
		}
		if w.activePointer != nil {
			if err := w.activePointer.Clear(ctx, rev.SkillID); err != nil {
				log.WarnfContext(ctx, "evolution: clear active pointer %q failed: %v", rev.SkillID, err)
			}
		}
		if w.candidateStore != nil {
			_ = w.candidateStore.AppendAudit(ctx, AuditEvent{
				Action:     "delete",
				SkillID:    rev.SkillID,
				RevisionID: rev.RevisionID,
				Status:     string(rev.Status),
				Reason:     "reviewer-driven deletion",
			})
		}
		w.bumpGateMetric(func(m *approvalGateMetrics) { m.DeletionsApplied++ })
		mutated = true
	}
	return mutated
}

// buildRevision constructs a fresh Revision for a create or update.
func (w *Worker) buildRevision(spec *SkillSpec, action, parentName string) *Revision {
	rev := &Revision{
		SkillID:    SkillIDFromName(spec.Name),
		RevisionID: NewRevisionID(),
		Source:     "reviewer",
		Action:     action,
		Spec:       spec,
		Status:     RevisionPending,
		CreatedAt:  time.Now().UTC(),
	}
	if parentName != "" {
		rev.ParentID = SkillIDFromName(parentName)
	}
	return rev
}

// processRevision runs the full gate + publish + audit pipeline for
// one revision. Returns true when the live publisher was actually
// updated (so the worker knows to refresh the repository).
func (w *Worker) processRevision(ctx context.Context, rev *Revision, existing []ExistingSkill, actionLabel string, outcome *Outcome) bool {
	w.bumpGateMetric(func(m *approvalGateMetrics) { m.CandidatesSeen++ })

	gatePassed := w.runGates(ctx, rev, existing, outcome)
	if !gatePassed && rev.Status == RevisionPending {
		rev.Status = RevisionRejected
	}

	// Always write the revision so the audit trail stays complete,
	// even for rejected revisions.
	if w.candidateStore != nil {
		if err := w.candidateStore.WriteRevision(ctx, rev); err != nil {
			log.WarnfContext(ctx, "evolution: write revision %s failed: %v", rev.RevisionID, err)
		} else {
			w.bumpGateMetric(func(m *approvalGateMetrics) { m.RevisionsWritten++ })
		}
	}

	// Decide whether to publish.
	shouldPublish := gatePassed || w.approvalGateShadow
	if !shouldPublish {
		w.auditReject(ctx, rev)
		return false
	}
	if !gatePassed && w.approvalGateShadow {
		w.bumpGateMetric(func(m *approvalGateMetrics) { m.ShadowModeBypassed++ })
		log.InfofContext(ctx,
			"evolution: shadow mode publishing failed revision %s (reasons=%v)",
			rev.RevisionID, gateRejectReason(rev))
	}

	return w.publishRevision(ctx, rev, actionLabel, gatePassed)
}

// runGates evaluates spec, safety, and effectiveness gates in order.
// Returns true if all gates pass (or if no gates are configured).
func (w *Worker) runGates(ctx context.Context, rev *Revision, existing []ExistingSkill, outcome *Outcome) bool {
	passed := true
	if w.specGate != nil {
		if !w.runSpecGate(ctx, rev, existing) {
			passed = false
		}
	}
	if w.safetyGate != nil {
		if !w.runSafetyGate(ctx, rev) {
			passed = false
		}
	}
	// Phase C: effectiveness gate. Only runs when spec+safety passed.
	if passed && w.effectivenessGate != nil {
		if !w.runEffectivenessGate(ctx, rev, outcome) {
			passed = false
		}
	}
	return passed
}

func (w *Worker) runSpecGate(ctx context.Context, rev *Revision, existing []ExistingSkill) bool {
	report, err := w.specGate.Validate(ctx, rev, existing)
	if err != nil {
		log.WarnfContext(ctx, "evolution: spec gate error on %q: %v", rev.Spec.Name, err)
		return false // fail closed on error
	}
	rev.SpecReport = report
	if report != nil && !report.Passed {
		w.bumpGateMetric(func(m *approvalGateMetrics) { m.SpecGateRejected++ })
		log.InfofContext(ctx,
			"evolution: spec gate rejected %q revision=%s reasons=%v",
			rev.Spec.Name, rev.RevisionID, report.Reasons)
		return false
	}
	return true
}

func (w *Worker) runSafetyGate(ctx context.Context, rev *Revision) bool {
	report, err := w.safetyGate.Scan(ctx, rev)
	if err != nil {
		log.WarnfContext(ctx, "evolution: safety gate error on %q: %v", rev.Spec.Name, err)
		return false // fail closed on error
	}
	rev.SafetyReport = report
	if report != nil && !report.Passed {
		w.bumpGateMetric(func(m *approvalGateMetrics) { m.SafetyGateRejected++ })
		log.InfofContext(ctx,
			"evolution: safety gate rejected %q revision=%s reasons=%v",
			rev.Spec.Name, rev.RevisionID, report.Reasons)
		return false
	}
	return true
}

func (w *Worker) runEffectivenessGate(ctx context.Context, rev *Revision, outcome *Outcome) bool {
	report, err := w.effectivenessGate.Evaluate(ctx, rev, outcome)
	if err != nil {
		log.WarnfContext(ctx, "evolution: effectiveness gate error on %q: %v", rev.Spec.Name, err)
		return false // fail closed on error
	}
	rev.EffectivenessReport = report
	if report != nil && !report.Passed {
		rev.Status = RevisionPendingEval
		w.bumpGateMetric(func(m *approvalGateMetrics) { m.EffectivenessGateRejected++ })
		log.InfofContext(ctx,
			"evolution: effectiveness gate held %q revision=%s reasons=%v",
			rev.Spec.Name, rev.RevisionID, report.Reasons)
		return false
	}
	return true
}

// auditReject appends a rejection audit event.
func (w *Worker) auditReject(ctx context.Context, rev *Revision) {
	if w.candidateStore != nil {
		_ = w.candidateStore.AppendAudit(ctx, AuditEvent{
			Action:     "reject",
			SkillID:    rev.SkillID,
			RevisionID: rev.RevisionID,
			Status:     string(rev.Status),
			Reason:     gateRejectReason(rev),
		})
	}
}

// publishRevision writes the skill, updates the active pointer,
// and appends a promotion audit event.
func (w *Worker) publishRevision(ctx context.Context, rev *Revision, actionLabel string, gatePassed bool) bool {
	if w.publisher == nil {
		return false
	}
	if err := w.publisher.UpsertSkill(ctx, rev.Spec); err != nil {
		log.WarnfContext(ctx, "evolution: publish revision %s failed: %v", rev.RevisionID, err)
		return false
	}
	if gatePassed {
		rev.Status = RevisionActive
	}
	now := time.Now().UTC()
	rev.PromotedAt = &now
	// Rewrite meta.json to reflect the new status/promoted_at.
	if w.candidateStore != nil {
		_ = w.candidateStore.WriteRevision(ctx, rev)
	}
	if w.activePointer != nil {
		if err := w.activePointer.Set(ctx, rev.SkillID, rev.RevisionID); err != nil {
			log.WarnfContext(ctx, "evolution: active pointer set %s failed: %v", rev.SkillID, err)
		}
	}
	if w.candidateStore != nil {
		_ = w.candidateStore.AppendAudit(ctx, AuditEvent{
			Action:     "promote",
			SkillID:    rev.SkillID,
			RevisionID: rev.RevisionID,
			Status:     string(rev.Status),
			Reason:     actionLabel,
		})
	}
	w.bumpGateMetric(func(m *approvalGateMetrics) {
		m.RevisionsPromoted++
		switch actionLabel {
		case "create":
			m.CreatesApplied++
		case "update":
			m.UpdatesApplied++
		}
	})
	return true
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
	if len(parts) == 0 {
		return "rejected"
	}
	return strings.Join(parts, " | ")
}

func (w *Worker) applySkills(ctx context.Context, skills []*SkillSpec) bool {
	if w.publisher == nil {
		return false
	}
	mutated := false
	for _, spec := range skills {
		if spec == nil {
			continue
		}
		if err := w.publisher.UpsertSkill(ctx, spec); err != nil {
			log.WarnfContext(ctx, "evolution: upsert skill %q failed: %v", spec.Name, err)
			continue
		}
		mutated = true
	}
	return mutated
}

func (w *Worker) applyUpdates(ctx context.Context, updates []*SkillUpdate) bool {
	if w.publisher == nil {
		return false
	}
	mutated := false
	for _, upd := range updates {
		if upd == nil || upd.NewSpec == nil {
			continue
		}
		if !skillExists(w.skillRepo, upd.Name) {
			log.WarnfContext(ctx, "evolution: update skill %q skipped: not found in repo", upd.Name)
			continue
		}
		// Force the on-disk directory name to remain stable.
		upd.NewSpec.Name = upd.Name
		if err := w.publisher.UpsertSkill(ctx, upd.NewSpec); err != nil {
			log.WarnfContext(ctx, "evolution: update skill %q failed: %v", upd.Name, err)
			continue
		}
		mutated = true
	}
	return mutated
}

func (w *Worker) applyDeletions(ctx context.Context, names []string) bool {
	if w.publisher == nil {
		return false
	}
	mutated := false
	for _, name := range names {
		if name == "" || !skillExists(w.skillRepo, name) {
			// Idempotent: nothing to delete (or never existed).
			continue
		}
		if err := w.publisher.DeleteSkill(ctx, name); err != nil {
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
	}
	for _, m := range markers {
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
	raw, ok := sess.GetState(SessionStateKeyLastReviewAt)
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
	sess.SetState(SessionStateKeyLastReviewAt,
		[]byte(ts.UTC().Format(time.RFC3339Nano)))
}

func hashSession(sess *session.Session) int {
	h := fnv.New32a()
	h.Write([]byte(sess.AppName))
	h.Write([]byte(sess.UserID))
	return int(h.Sum32())
}
