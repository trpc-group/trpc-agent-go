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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// newSweeperWorker constructs a worker wired for auto-expire tests:
// file-backed store/pointer/publisher under tmp, with a tiny sweep
// interval so the test does not hang waiting for the default tick.
func newSweeperWorker(t *testing.T, timeout, sweep time.Duration) (*worker, string) {
	t.Helper()
	dir := t.TempDir()
	w := newWorker(workerConfig{
		Reviewer:              &mockReviewer{},
		Publisher:             newFilePublisher(filepath.Join(dir, "skills")),
		PublisherBaseDir:      filepath.Join(dir, "skills"),
		CandidateStore:        newFileCandidateStore(filepath.Join(dir, "revisions")),
		ActivePointer:         newFileActivePointer(filepath.Join(dir, "revisions")),
		ApprovalTimeout:       timeout,
		ApprovalSweepInterval: sweep,
	})
	return w, dir
}

// writePendingApprovalRev writes a pending_approval revision whose
// HumanReport.DecidedAt is `age` in the past, simulating a revision
// that has been waiting for human review.
func writePendingApprovalRev(t *testing.T, store CandidateStore, skillID, revID string, age time.Duration) *Revision {
	t.Helper()
	heldAt := time.Now().UTC().Add(-age)
	rev := &Revision{
		SkillID:    skillID,
		RevisionID: revID,
		Action:     RevisionActionCreate,
		Status:     RevisionPendingApproval,
		Source:     "reviewer",
		CreatedAt:  heldAt,
		HumanReport: &HumanReport{
			Held:      true,
			DecidedAt: &heldAt,
		},
		Spec: &SkillSpec{
			Name:        "Stale Skill " + revID,
			Description: "d",
			WhenToUse:   "w",
			Steps:       []string{"s"},
		},
	}
	require.NoError(t, store.WriteRevision(context.Background(), rev))
	return rev
}

func TestApprovalSweep_PromotesStaleRevisions(t *testing.T) {
	w, dir := newSweeperWorker(t, 100*time.Millisecond, 0)
	defer w.Stop()

	store := w.candidateStore
	pointer := w.activePointer

	// One stale (older than 100ms) and one fresh (younger).
	stale := writePendingApprovalRev(t, store, "stale-skill", "rev-stale", 1*time.Hour)
	fresh := writePendingApprovalRev(t, store, "fresh-skill", "rev-fresh", 0)

	w.runOneSweep(context.Background())

	// Stale revision is now active.
	got, err := store.ReadRevision(context.Background(), stale.SkillID, stale.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, got.Status)
	require.NotNil(t, got.HumanReport)
	require.NotNil(t, got.HumanReport.Approved)
	assert.True(t, *got.HumanReport.Approved)
	assert.Equal(t, autoExpireReviewer, got.HumanReport.Reviewer)

	active, err := pointer.Get(context.Background(), stale.SkillID)
	require.NoError(t, err)
	assert.Equal(t, "rev-stale", active)

	// Fresh revision still pending.
	got, err = store.ReadRevision(context.Background(), fresh.SkillID, fresh.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionPendingApproval, got.Status)

	// Audit log captured the auto-promote.
	auditPath := filepath.Join(dir, "revisions", stale.SkillID, "audit.log")
	raw, err := os.ReadFile(auditPath)
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"actor":"`+autoExpireReviewer+`"`)
	assert.Contains(t, string(raw), `"action":"approve"`)

	// Counter bumped.
	m := w.approvalGateMetrics()
	assert.Equal(t, 1, m.RevisionsPromoted)
}

func TestApprovalSweep_DisabledWhenTimeoutZero(t *testing.T) {
	w, _ := newSweeperWorker(t, 0, 0)
	defer w.Stop()

	store := w.candidateStore
	stale := writePendingApprovalRev(t, store, "skill", "rev", 1*time.Hour)

	// Even after a manual sweep, nothing should change because the
	// sweeper is disabled — runOneSweep uses approvalTimeout of zero,
	// so cutoff is "now" and every pending revision is "newer than
	// cutoff" → skipped.
	w.runOneSweep(context.Background())

	got, err := store.ReadRevision(context.Background(), stale.SkillID, stale.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionPendingApproval, got.Status)
}

func TestApprovalSweep_FallsBackToCreatedAt(t *testing.T) {
	w, _ := newSweeperWorker(t, 100*time.Millisecond, 0)
	defer w.Stop()

	store := w.candidateStore
	heldAt := time.Now().UTC().Add(-1 * time.Hour)
	// Revision without a HumanReport — sweeper should fall back to CreatedAt.
	rev := &Revision{
		SkillID:    "no-report",
		RevisionID: "rev-no-report",
		Action:     RevisionActionCreate,
		Status:     RevisionPendingApproval,
		CreatedAt:  heldAt,
		Spec:       &SkillSpec{Name: "No Report", Description: "d", WhenToUse: "w", Steps: []string{"s"}},
	}
	require.NoError(t, store.WriteRevision(context.Background(), rev))

	w.runOneSweep(context.Background())

	got, err := store.ReadRevision(context.Background(), rev.SkillID, rev.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, got.Status)
}

func TestApprovalSweep_StartStopCycle(t *testing.T) {
	w, _ := newSweeperWorker(t, 50*time.Millisecond, 20*time.Millisecond)
	store := w.candidateStore
	stale := writePendingApprovalRev(t, store, "skill", "rev", 1*time.Hour)

	w.Start()
	// Wait long enough for the sweeper to run at least one tick.
	require.Eventually(t, func() bool {
		got, err := store.ReadRevision(context.Background(), stale.SkillID, stale.RevisionID)
		if err != nil {
			return false
		}
		return got.Status == RevisionActive
	}, 2*time.Second, 20*time.Millisecond)

	// Stop must be clean and idempotent.
	w.Stop()
	w.Stop()
}

// TestApprovalSweep_DisabledWhenPublisherMissing exercises the
// readiness check: WithApprovalTimeout is documented as requiring the
// full revision pipeline, so a Service with no Publisher must not
// start the sweeper goroutine. Otherwise stale revisions would be
// retried forever because the auto-promote path cannot republish the
// skill body.
func TestApprovalSweep_DisabledWhenPublisherMissing(t *testing.T) {
	dir := t.TempDir()
	w := newWorker(workerConfig{
		Reviewer:        &mockReviewer{},
		CandidateStore:  newFileCandidateStore(filepath.Join(dir, "revisions")),
		ActivePointer:   newFileActivePointer(filepath.Join(dir, "revisions")),
		ApprovalTimeout: 100 * time.Millisecond,
	})
	w.startApprovalSweeperLocked()
	defer w.stopApprovalSweeperLocked()
	assert.Nil(t, w.sweepCancel, "sweeper must not start without a publisher")
}

func TestApprovalSweep_UsesConfiguredRouteWhenScopedRootsIncomplete(t *testing.T) {
	dir := t.TempDir()
	store := newFileCandidateStore(filepath.Join(dir, "revisions"))
	pointer := &stubActivePointer{}
	publisher := &mockPublisher{}
	w := newWorker(workerConfig{
		Reviewer:       &mockReviewer{},
		Publisher:      publisher,
		CandidateStore: store,
		ActivePointer:  pointer,
		SkillScopeMode: skill.SkillScopeUser,
	})

	routes := w.collectStoresForSweep()
	require.Len(t, routes, 1)
	assert.Same(t, store, routes[0].store)
	assert.Same(t, pointer, routes[0].pointer)
	assert.Same(t, publisher, routes[0].publisher)
}

func TestApprovalSweep_UsesScopedRoutesWhenRootsComplete(t *testing.T) {
	cases := []struct {
		name      string
		mode      skill.SkillScopeMode
		scopeDirs []string
	}{
		{
			name:      "app",
			mode:      skill.SkillScopeApp,
			scopeDirs: []string{"apps/app1", "apps/app2"},
		},
		{
			name:      "user",
			mode:      skill.SkillScopeUser,
			scopeDirs: []string{"users/app1/alice", "users/app1/bob"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w, dir := newSweeperWorker(t, 100*time.Millisecond, 0)
			defer w.Stop()
			w.skillScopeMode = tc.mode

			revRoot := filepath.Join(dir, "revisions")
			pubRoot := filepath.Join(dir, "skills")
			for _, rel := range tc.scopeDirs {
				require.NoError(t, os.MkdirAll(filepath.Join(revRoot, rel), 0o755))
			}

			routes := w.collectStoresForSweep()
			require.Len(t, routes, len(tc.scopeDirs))

			gotRoots := make([]string, 0, len(routes))
			for _, route := range routes {
				gotRoots = append(gotRoots, route.root)

				store, ok := route.store.(*fileCandidateStore)
				require.True(t, ok)
				assert.Equal(t, route.root, store.root)

				rel, err := filepath.Rel(revRoot, route.root)
				require.NoError(t, err)

				pointer, ok := route.pointer.(*fileActivePointer)
				require.True(t, ok)
				assert.Equal(t, filepath.Join(revRoot, rel), pointer.root)

				publisher, ok := route.publisher.(*filePublisher)
				require.True(t, ok)
				assert.Equal(t, filepath.Join(pubRoot, rel), publisher.root)
			}

			wantRoots := make([]string, 0, len(tc.scopeDirs))
			for _, rel := range tc.scopeDirs {
				wantRoots = append(wantRoots, filepath.Join(revRoot, rel))
			}
			assert.ElementsMatch(t, wantRoots, gotRoots)
		})
	}
}

func TestApprovalSweep_PicksSingleRevisionPerSkill(t *testing.T) {
	w, dir := newSweeperWorker(t, 100*time.Millisecond, 0)
	defer w.Stop()
	store := w.candidateStore

	older := writePendingApprovalRev(t, store, "skill", "rev-older", 2*time.Hour)
	newer := writePendingApprovalRev(t, store, "skill", "rev-newer", 1*time.Hour)
	// Pin filesystem mtimes so ListRevisions iterates oldest-first
	// regardless of how quickly the test wrote each revision.
	revRoot := filepath.Join(dir, "revisions")
	setRevisionMtime(t, revRoot, "skill", older.RevisionID,
		time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	setRevisionMtime(t, revRoot, "skill", newer.RevisionID,
		time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC))

	w.runOneSweep(context.Background())

	// pickSweepTarget walks ListRevisions oldest-first, so the older
	// revision wins. The newer one stays pending until the next sweep.
	gotOlder, err := store.ReadRevision(context.Background(), older.SkillID, older.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, gotOlder.Status)

	gotNewer, err := store.ReadRevision(context.Background(), newer.SkillID, newer.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionPendingApproval, gotNewer.Status)
}

func TestEffectiveSweepInterval(t *testing.T) {
	cases := []struct {
		name    string
		timeout time.Duration
		sweep   time.Duration
		want    time.Duration
	}{
		{
			name:    "explicit override wins",
			timeout: 24 * time.Hour,
			sweep:   30 * time.Second,
			want:    30 * time.Second,
		},
		{
			name:    "default is timeout/4",
			timeout: 4 * time.Minute,
			want:    1 * time.Minute,
		},
		{
			name:    "capped at one hour",
			timeout: 24 * time.Hour,
			want:    1 * time.Hour,
		},
		{
			name:    "floor at one second",
			timeout: 100 * time.Millisecond,
			want:    1 * time.Second,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := &worker{
				approvalTimeout:       tc.timeout,
				approvalSweepInterval: tc.sweep,
			}
			assert.Equal(t, tc.want, w.effectiveSweepInterval())
		})
	}
}

func TestPendingApprovalReferenceTime_PrefersHumanReport(t *testing.T) {
	created := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	decided := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	rev := &Revision{
		CreatedAt: created,
		HumanReport: &HumanReport{
			Held:      true,
			DecidedAt: &decided,
		},
	}
	assert.Equal(t, decided, pendingApprovalReferenceTime(rev))

	rev.HumanReport = nil
	assert.Equal(t, created, pendingApprovalReferenceTime(rev))

	assert.True(t, pendingApprovalReferenceTime(nil).IsZero())
}

func TestApprovalSweep_DiscoverScopeDirs_User(t *testing.T) {
	w, dir := newSweeperWorker(t, 100*time.Millisecond, 0)
	defer w.Stop()
	w.skillScopeMode = skill.SkillScopeUser

	// Lay out files mimicking the worker's user-scoped layout.
	revRoot := filepath.Join(dir, "revisions")
	w.candidateStoreRoot = revRoot
	w.activePointerRoot = revRoot
	w.publisherBaseDir = filepath.Join(dir, "skills")

	for _, p := range []string{"users/app1/alice", "users/app1/bob", "users/app2/alice"} {
		full := filepath.Join(revRoot, p)
		require.NoError(t, os.MkdirAll(full, 0o755))
	}
	scopes := w.discoverScopeDirs()
	require.Len(t, scopes, 3)
	for _, s := range scopes {
		assert.Contains(t, s.candidateRoot, "users")
		assert.NotEmpty(t, s.pointerRoot)
		assert.NotEmpty(t, s.publisherRoot)
	}
}

type stubActivePointer struct {
	active map[string]string
}

func (p *stubActivePointer) Get(_ context.Context, skillID string) (string, error) {
	if p.active == nil {
		return "", nil
	}
	return p.active[skillID], nil
}

func (p *stubActivePointer) Set(_ context.Context, skillID, revisionID string) error {
	if p.active == nil {
		p.active = make(map[string]string)
	}
	p.active[skillID] = revisionID
	return nil
}

func (p *stubActivePointer) Clear(_ context.Context, skillID string) error {
	if p.active != nil {
		delete(p.active, skillID)
	}
	return nil
}
