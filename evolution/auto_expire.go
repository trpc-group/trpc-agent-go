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
	"errors"
	"os"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const (
	// autoExpireReviewer is the actor identifier recorded in the audit
	// log for revisions that the sweeper auto-promotes. Operators
	// filtering audit lines by reviewer can use this constant to spot
	// auto-expirations.
	autoExpireReviewer = "auto-expire"

	// maxApprovalSweepInterval caps the sweep period so a multi-day
	// timeout still wakes up at least hourly. This keeps the
	// auto-expire latency bounded without burning CPU on tight loops.
	maxApprovalSweepInterval = 1 * time.Hour
)

// startApprovalSweeperLocked launches the auto-expiration sweeper if a
// timeout is configured. Caller holds w.mu.
func (w *worker) startApprovalSweeperLocked() {
	if w.approvalTimeout <= 0 {
		return
	}
	if w.candidateStore == nil || w.activePointer == nil {
		log.WarnfContext(context.Background(),
			"evolution: ApprovalTimeout set but CandidateStore/ActivePointer missing — sweeper disabled")
		return
	}
	w.sweepStop = make(chan struct{})
	w.sweepDone = make(chan struct{})
	interval := w.effectiveSweepInterval()
	go w.runApprovalSweeper(interval)
}

// stopApprovalSweeperLocked signals the sweeper goroutine to exit and
// waits for it. Caller holds w.mu.
func (w *worker) stopApprovalSweeperLocked() {
	if w.sweepStop == nil {
		return
	}
	close(w.sweepStop)
	<-w.sweepDone
	w.sweepStop = nil
	w.sweepDone = nil
}

// effectiveSweepInterval picks the sweep period: either the explicit
// override or min(approvalTimeout/4, 1h). Defaults guarantee the
// sweeper wakes up well before the timeout expires for any setting,
// while bounding CPU on multi-day timeouts.
func (w *worker) effectiveSweepInterval() time.Duration {
	if w.approvalSweepInterval > 0 {
		return w.approvalSweepInterval
	}
	d := max(min(w.approvalTimeout/4, maxApprovalSweepInterval), time.Second)
	return d
}

// runApprovalSweeper is the sweeper goroutine. It wakes up on a ticker
// and walks every scope (or the unscoped store when scope mode is
// SkillScopeNone) auto-promoting pending_approval revisions older than
// approvalTimeout.
func (w *worker) runApprovalSweeper(interval time.Duration) {
	defer close(w.sweepDone)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	// Run one sweep right after Start so ad-hoc tests don't have to
	// wait a full interval to observe auto-expiration.
	w.runOneSweep()
	for {
		select {
		case <-w.sweepStop:
			return
		case <-ticker.C:
			w.runOneSweep()
		}
	}
}

// runOneSweep scans the candidate store(s) once and auto-promotes any
// pending_approval revision whose age exceeds approvalTimeout. No-op
// when approvalTimeout is non-positive so the sweeper stays passive
// even if it is invoked manually (e.g. in tests).
func (w *worker) runOneSweep() {
	if w.approvalTimeout <= 0 {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cutoff := time.Now().UTC().Add(-w.approvalTimeout)

	stores := w.collectStoresForSweep()
	for _, st := range stores {
		w.sweepStore(ctx, st, cutoff)
	}
}

// sweepRoute pairs a candidate store with the publisher and pointer
// that share its scope. The sweeper needs all three to promote a
// revision: write the new active body, archive the prior active, and
// update the pointer.
type sweepRoute struct {
	store     CandidateStore
	pointer   ActivePointer
	publisher Publisher
	root      string // for logging only
}

// collectStoresForSweep returns every (store, pointer, publisher)
// triple the worker needs to scan. In unscoped mode this is a single
// triple; in scoped mode it is one triple per discovered scope
// directory under candidateStoreRoot.
func (w *worker) collectStoresForSweep() []sweepRoute {
	if w.skillScopeMode == skill.SkillScopeNone {
		return []sweepRoute{{
			store:     w.candidateStore,
			pointer:   w.activePointer,
			publisher: w.publisher,
			root:      w.candidateStoreRoot,
		}}
	}
	if w.candidateStoreRoot == "" {
		// File-store without a known root: fall back to the unscoped
		// triple — at least the in-memory case still works for tests.
		return []sweepRoute{{
			store:     w.candidateStore,
			pointer:   w.activePointer,
			publisher: w.publisher,
			root:      "",
		}}
	}
	// Walk the on-disk scope directory to find candidate stores per
	// scope. Each store gets its own publisher/pointer rooted at the
	// matching scoped directory so promotion stays scope-local.
	var routes []sweepRoute
	scopes := w.discoverScopeDirs()
	for _, sd := range scopes {
		store := newFileCandidateStore(sd.candidateRoot)
		pointer := newFileActivePointer(sd.pointerRoot)
		var publisher Publisher
		if sd.publisherRoot != "" {
			publisher = newFilePublisher(sd.publisherRoot)
		}
		routes = append(routes, sweepRoute{
			store:     store,
			pointer:   pointer,
			publisher: publisher,
			root:      sd.candidateRoot,
		})
	}
	return routes
}

// scopeDir holds the per-scope filesystem roots used to construct a
// store/pointer/publisher triple for one tenant.
type scopeDir struct {
	candidateRoot string
	pointerRoot   string
	publisherRoot string
}

// discoverScopeDirs lists every leaf scope directory beneath the
// candidate store root. The layout matches what the worker writes
// during normal job processing:
//
//	SkillScopeApp:  <root>/apps/<app>/...
//	SkillScopeUser: <root>/users/<app>/<user>/...
func (w *worker) discoverScopeDirs() []scopeDir {
	mode := skill.NormalizeSkillScopeMode(w.skillScopeMode)
	var prefix string
	depth := 1
	switch mode {
	case skill.SkillScopeApp:
		prefix = "apps"
	case skill.SkillScopeUser:
		prefix = "users"
		depth = 2
	default:
		return nil
	}
	base := filepath.Join(w.candidateStoreRoot, prefix)
	leaves := walkScopeLeaves(base, depth)
	out := make([]scopeDir, 0, len(leaves))
	for _, leaf := range leaves {
		rel, err := filepath.Rel(w.candidateStoreRoot, leaf)
		if err != nil {
			continue
		}
		// Mirror the same relative path under the pointer/publisher roots
		// so each scope's triple stays self-consistent.
		entry := scopeDir{candidateRoot: leaf}
		if w.activePointerRoot != "" {
			entry.pointerRoot = filepath.Join(w.activePointerRoot, rel)
		} else {
			entry.pointerRoot = leaf
		}
		if w.publisherBaseDir != "" {
			entry.publisherRoot = filepath.Join(w.publisherBaseDir, rel)
		}
		out = append(out, entry)
	}
	return out
}

// walkScopeLeaves returns every directory at the requested depth under
// base. Used to enumerate scope directories without recursing further
// than necessary.
func walkScopeLeaves(base string, depth int) []string {
	if depth <= 0 {
		return []string{base}
	}
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		child := filepath.Join(base, e.Name())
		if depth == 1 {
			out = append(out, child)
			continue
		}
		out = append(out, walkScopeLeaves(child, depth-1)...)
	}
	return out
}

// sweepStore scans one CandidateStore and auto-promotes every
// pending_approval revision older than cutoff.
func (w *worker) sweepStore(ctx context.Context, route sweepRoute, cutoff time.Time) {
	if route.store == nil {
		return
	}
	skills, err := route.store.ListSkills(ctx)
	if err != nil {
		log.WarnfContext(ctx, "evolution: sweep list skills failed at %q: %v", route.root, err)
		return
	}
	svc := NewApprovalService(route.store, route.pointer, route.publisher)
	for _, skillID := range skills {
		if ctx.Err() != nil {
			return
		}
		w.sweepSkill(ctx, svc, route.store, skillID, cutoff)
	}
}

// sweepSkill auto-promotes every stale pending_approval revision for a
// single skill. Other revisions are left untouched. Errors are logged
// but never bubble up — the next tick will retry, and one bad revision
// must not block the whole sweep.
func (w *worker) sweepSkill(
	ctx context.Context, svc *ApprovalService, store CandidateStore,
	skillID string, cutoff time.Time,
) {
	revIDs, err := store.ListRevisions(ctx, skillID)
	if err != nil {
		log.WarnfContext(ctx, "evolution: sweep list revisions for %q failed: %v", skillID, err)
		return
	}
	for _, revID := range revIDs {
		rev, err := store.ReadRevision(ctx, skillID, revID)
		if err != nil {
			if !errors.Is(err, os.ErrNotExist) {
				log.WarnfContext(ctx,
					"evolution: sweep read revision %q/%q failed: %v", skillID, revID, err)
			}
			continue
		}
		if rev.Status != RevisionPendingApproval {
			continue
		}
		ageRef := pendingApprovalReferenceTime(rev)
		if ageRef.IsZero() || !ageRef.Before(cutoff) {
			continue
		}
		w.autoPromote(ctx, svc, rev)
	}
}

// pendingApprovalReferenceTime returns the timestamp the sweeper should
// compare against the cutoff. Prefer the human-gate decision time
// (when the gate held the revision); fall back to creation time.
func pendingApprovalReferenceTime(rev *Revision) time.Time {
	if rev == nil {
		return time.Time{}
	}
	if rev.HumanReport != nil && rev.HumanReport.DecidedAt != nil {
		return *rev.HumanReport.DecidedAt
	}
	return rev.CreatedAt
}

// autoPromote drives one revision through the standard approve path.
// Errors are logged but never abort the sweep. Counter bookkeeping
// stays consistent with the regular approve flow because we go
// through ApprovalService.Decide, not a custom shortcut.
func (w *worker) autoPromote(ctx context.Context, svc *ApprovalService, rev *Revision) {
	reason := "pending_approval timeout: " + w.approvalTimeout.String()
	err := svc.Decide(ctx, ApprovalDecision{
		RevisionID: rev.RevisionID,
		SkillID:    rev.SkillID,
		Approved:   true,
		Reviewer:   autoExpireReviewer,
		Comment:    reason,
		DecidedAt:  time.Now().UTC(),
	})
	if err != nil {
		if !errors.Is(err, ErrAlreadyDecided) {
			log.WarnfContext(ctx,
				"evolution: auto-expire revision %q/%q failed: %v",
				rev.SkillID, rev.RevisionID, err)
		}
		return
	}
	log.InfofContext(ctx,
		"evolution: auto-expired %s/%s after %s in pending_approval",
		rev.SkillID, rev.RevisionID, w.approvalTimeout)
	w.recordAutoPromotion()
}

// recordAutoPromotion bumps the rollback/promotion counters under the
// gate metrics mutex so external readers see consistent values.
func (w *worker) recordAutoPromotion() {
	w.approvalGateMu.Lock()
	defer w.approvalGateMu.Unlock()
	w.approvalGateCounters.RevisionsPromoted++
}
