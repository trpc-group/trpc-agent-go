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
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestApprovalService_ListPending_Empty(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	svc := NewApprovalService(store, nil, nil)

	pending, err := svc.ListPending(context.Background(), ListPendingOpts{})
	require.NoError(t, err)
	assert.Empty(t, pending)
}

func TestApprovalService_ListPending_FindsPendingApproval(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ctx := context.Background()

	// Write a pending_approval revision
	rev := &Revision{
		SkillID:    "test-skill",
		RevisionID: "rev-001",
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionPendingApproval,
		Spec:       &SkillSpec{Name: "Test Skill", Description: "d", WhenToUse: "w", Steps: []string{"a"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, rev))

	// Write an active revision (should not be returned)
	rev2 := &Revision{
		SkillID:    "other-skill",
		RevisionID: "rev-002",
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionActive,
		Spec:       &SkillSpec{Name: "Other Skill", Description: "d", WhenToUse: "w", Steps: []string{"a"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, rev2))

	svc := NewApprovalService(store, nil, nil)
	pending, err := svc.ListPending(ctx, ListPendingOpts{})
	require.NoError(t, err)
	require.Len(t, pending, 1)
	assert.Equal(t, "rev-001", pending[0].RevisionID)
}

func TestApprovalService_Decide_Approve(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)
	pub := &mockPublisher{}
	ctx := context.Background()

	// Write a pending_approval revision
	rev := &Revision{
		SkillID:    "my-skill",
		RevisionID: "rev-approve",
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionPendingApproval,
		Spec:       &SkillSpec{Name: "My Skill", Description: "d", WhenToUse: "w", Steps: []string{"s1", "s2"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, rev))

	decidedAt := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	svc := NewApprovalService(store, ptr, pub)
	err := svc.Decide(ctx, ApprovalDecision{
		RevisionID: "rev-approve",
		SkillID:    "my-skill",
		Approved:   true,
		Reviewer:   "alice@example.com",
		Comment:    "looks good",
		DecidedAt:  decidedAt,
	})
	require.NoError(t, err)

	// Verify revision is now active
	stored, err := store.ReadRevision(ctx, "my-skill", "rev-approve")
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, stored.Status)
	assert.NotNil(t, stored.PromotedAt)
	require.NotNil(t, stored.HumanReport)
	require.NotNil(t, stored.HumanReport.Approved)
	assert.True(t, *stored.HumanReport.Approved)
	assert.Equal(t, "alice@example.com", stored.HumanReport.Reviewer)
	assert.Equal(t, "looks good", stored.HumanReport.Comment)
	require.NotNil(t, stored.HumanReport.DecidedAt)
	assert.True(t, decidedAt.Equal(*stored.HumanReport.DecidedAt))

	// Verify publisher was called
	pub.mu.Lock()
	assert.Len(t, pub.skills, 1)
	pub.mu.Unlock()

	// Verify active pointer
	activeRev, err := ptr.Get(ctx, "my-skill")
	require.NoError(t, err)
	assert.Equal(t, "rev-approve", activeRev)
	raw, err := os.ReadFile(filepath.Join(dir, "my-skill", "audit.log"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"action":"approve"`)
	assert.Contains(t, string(raw), `"actor":"alice@example.com"`)
	assert.Contains(t, string(raw), `"comment":"looks good"`)
}

func TestApprovalService_Decide_ApproveArchivesPreviousActive(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)
	pub := &mockPublisher{}
	ctx := context.Background()

	oldRev := &Revision{
		SkillID:    "my-skill",
		RevisionID: "rev-old",
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionActive,
		Spec:       &SkillSpec{Name: "My Skill", Description: "old", WhenToUse: "w", Steps: []string{"s1", "s2"}},
		CreatedAt:  time.Now().UTC(),
	}
	pendingRev := &Revision{
		SkillID:    "my-skill",
		RevisionID: "rev-new",
		Source:     "reviewer",
		Action:     RevisionActionUpdate,
		Status:     RevisionPendingApproval,
		Spec:       &SkillSpec{Name: "My Skill", Description: "new", WhenToUse: "w", Steps: []string{"s1", "s2"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, oldRev))
	require.NoError(t, store.WriteRevision(ctx, pendingRev))
	require.NoError(t, ptr.Set(ctx, "my-skill", "rev-old"))

	svc := NewApprovalService(store, ptr, pub)
	err := svc.Decide(ctx, ApprovalDecision{
		RevisionID: "rev-new",
		SkillID:    "my-skill",
		Approved:   true,
		Reviewer:   "alice@example.com",
		Comment:    "replace old revision",
	})
	require.NoError(t, err)

	storedOld, err := store.ReadRevision(ctx, "my-skill", "rev-old")
	require.NoError(t, err)
	assert.Equal(t, RevisionArchived, storedOld.Status)
	activeRev, err := ptr.Get(ctx, "my-skill")
	require.NoError(t, err)
	assert.Equal(t, "rev-new", activeRev)
	raw, err := os.ReadFile(filepath.Join(dir, "my-skill", "audit.log"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"action":"archive"`)
	assert.Contains(t, string(raw), `"revision_id":"rev-old"`)
}

func TestApprovalService_Decide_ApproveDeleteRevision(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)
	pub := &mockPublisher{}
	ctx := context.Background()
	skillID := skillIDFromName("Stale Skill")

	oldRev := &Revision{
		SkillID:    skillID,
		RevisionID: "rev-old",
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionActive,
		Spec:       &SkillSpec{Name: "Stale Skill", Description: "old", WhenToUse: "old", Steps: []string{"s1", "s2"}},
		CreatedAt:  time.Now().UTC(),
	}
	pendingRev := &Revision{
		SkillID:    skillID,
		RevisionID: "rev-delete",
		Source:     "reviewer",
		Action:     RevisionActionDelete,
		TargetName: "Stale Skill",
		Status:     RevisionPendingApproval,
		CreatedAt:  time.Now().UTC(),
		HumanReport: &HumanReport{
			Held:    true,
			Reasons: []string{"human approval required"},
		},
	}
	require.NoError(t, store.WriteRevision(ctx, oldRev))
	require.NoError(t, store.WriteRevision(ctx, pendingRev))
	require.NoError(t, ptr.Set(ctx, skillID, "rev-old"))

	svc := NewApprovalService(store, ptr, pub)
	err := svc.Decide(ctx, ApprovalDecision{
		RevisionID: "rev-delete",
		SkillID:    skillID,
		Approved:   true,
		Reviewer:   "alice@example.com",
		Comment:    "remove stale skill",
	})
	require.NoError(t, err)

	pub.mu.Lock()
	require.Equal(t, []string{"Stale Skill"}, pub.deletions)
	pub.mu.Unlock()

	storedDelete, err := store.ReadRevision(ctx, skillID, "rev-delete")
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, storedDelete.Status)
	assert.NotNil(t, storedDelete.PromotedAt)
	require.NotNil(t, storedDelete.HumanReport)
	require.NotNil(t, storedDelete.HumanReport.Approved)
	assert.True(t, *storedDelete.HumanReport.Approved)
	assert.Equal(t, "alice@example.com", storedDelete.HumanReport.Reviewer)
	assert.Equal(t, "remove stale skill", storedDelete.HumanReport.Comment)
	assert.Equal(t, []string{"human approval required"}, storedDelete.HumanReport.Reasons)

	storedOld, err := store.ReadRevision(ctx, skillID, "rev-old")
	require.NoError(t, err)
	assert.Equal(t, RevisionArchived, storedOld.Status)
	activeRev, err := ptr.Get(ctx, skillID)
	require.NoError(t, err)
	assert.Empty(t, activeRev)

	raw, err := os.ReadFile(filepath.Join(dir, skillID, "audit.log"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"action":"archive"`)
	assert.Contains(t, string(raw), `"action":"approve"`)
	assert.Contains(t, string(raw), `"comment":"remove stale skill"`)
}

func TestApprovalService_Decide_Reject(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ctx := context.Background()

	rev := &Revision{
		SkillID:    "bad-skill",
		RevisionID: "rev-reject",
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionPendingApproval,
		Spec:       &SkillSpec{Name: "Bad Skill", Description: "d", WhenToUse: "w", Steps: []string{"s1", "s2"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, rev))

	decidedAt := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	svc := NewApprovalService(store, nil, nil)
	err := svc.Decide(ctx, ApprovalDecision{
		RevisionID: "rev-reject",
		SkillID:    "bad-skill",
		Approved:   false,
		Reviewer:   "bob@example.com",
		Comment:    "steps too vague",
		DecidedAt:  decidedAt,
	})
	require.NoError(t, err)

	stored, err := store.ReadRevision(ctx, "bad-skill", "rev-reject")
	require.NoError(t, err)
	assert.Equal(t, RevisionRejected, stored.Status)
	require.NotNil(t, stored.HumanReport)
	require.NotNil(t, stored.HumanReport.Approved)
	assert.False(t, *stored.HumanReport.Approved)
	assert.Equal(t, "bob@example.com", stored.HumanReport.Reviewer)
	assert.Equal(t, "steps too vague", stored.HumanReport.Comment)
	require.NotNil(t, stored.HumanReport.DecidedAt)
	assert.True(t, decidedAt.Equal(*stored.HumanReport.DecidedAt))
	raw, err := os.ReadFile(filepath.Join(dir, "bad-skill", "audit.log"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"action":"reject"`)
	assert.Contains(t, string(raw), `"actor":"bob@example.com"`)
	assert.Contains(t, string(raw), `"comment":"steps too vague"`)
}

func TestApprovalService_Decide_AlreadyDecided(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ctx := context.Background()

	rev := &Revision{
		SkillID:    "decided-skill",
		RevisionID: "rev-decided",
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionActive, // already promoted
		Spec:       &SkillSpec{Name: "Decided Skill", Description: "d", WhenToUse: "w", Steps: []string{"s1", "s2"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, rev))

	svc := NewApprovalService(store, nil, nil)
	err := svc.Decide(ctx, ApprovalDecision{
		RevisionID: "rev-decided",
		SkillID:    "decided-skill",
		Approved:   true,
		Reviewer:   "charlie",
	})
	assert.ErrorIs(t, err, ErrAlreadyDecided)
}

func TestApprovalService_Decide_SerializesConcurrentDecisions(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)
	pub := newBlockingPublisher()
	ctx := context.Background()

	rev := &Revision{
		SkillID:    "race-skill",
		RevisionID: "rev-race",
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionPendingApproval,
		Spec:       &SkillSpec{Name: "Race Skill", Description: "d", WhenToUse: "w", Steps: []string{"s"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, rev))

	svc := NewApprovalService(store, ptr, pub)
	firstErr := make(chan error, 1)
	go func() {
		firstErr <- svc.Decide(ctx, ApprovalDecision{
			RevisionID: rev.RevisionID,
			SkillID:    rev.SkillID,
			Approved:   true,
			Reviewer:   autoExpireReviewer,
			Comment:    "timeout",
		})
	}()
	<-pub.entered

	secondErr := make(chan error, 1)
	go func() {
		secondErr <- svc.Decide(ctx, ApprovalDecision{
			RevisionID: rev.RevisionID,
			SkillID:    rev.SkillID,
			Approved:   false,
			Reviewer:   "human",
			Comment:    "reject after timeout started",
		})
	}()

	close(pub.release)
	require.NoError(t, <-firstErr)
	require.ErrorIs(t, <-secondErr, ErrAlreadyDecided)
	assert.Equal(t, 1, pub.upsertCount())

	stored, err := store.ReadRevision(ctx, rev.SkillID, rev.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, stored.Status)
	require.NotNil(t, stored.HumanReport)
	assert.Equal(t, autoExpireReviewer, stored.HumanReport.Reviewer)
}

func TestApprovalService_Decide_SerializesConcurrentDecisionsForSkill(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)
	pub := newBlockingPublisher()
	ctx := context.Background()
	skillID := "race-skill"

	for _, revID := range []string{"rev-a", "rev-b"} {
		require.NoError(t, store.WriteRevision(ctx, &Revision{
			SkillID:    skillID,
			RevisionID: revID,
			Source:     "reviewer",
			Action:     RevisionActionCreate,
			Status:     RevisionPendingApproval,
			Spec:       &SkillSpec{Name: "Race Skill", Description: revID, WhenToUse: "w", Steps: []string{"s"}},
			CreatedAt:  time.Now().UTC(),
		}))
	}

	svc := NewApprovalService(store, ptr, pub)
	firstErr := make(chan error, 1)
	go func() {
		firstErr <- svc.Decide(ctx, ApprovalDecision{
			RevisionID: "rev-a",
			SkillID:    skillID,
			Approved:   true,
			Reviewer:   "first",
		})
	}()
	<-pub.entered

	secondErr := make(chan error, 1)
	go func() {
		secondErr <- svc.Decide(ctx, ApprovalDecision{
			RevisionID: "rev-b",
			SkillID:    skillID,
			Approved:   true,
			Reviewer:   "second",
		})
	}()

	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, pub.upsertCount(), "second revision must wait for the skill-level decision lock")

	close(pub.release)
	require.NoError(t, <-firstErr)
	require.NoError(t, <-secondErr)

	active, err := ptr.Get(ctx, skillID)
	require.NoError(t, err)
	assert.Equal(t, "rev-b", active)
	first, err := store.ReadRevision(ctx, skillID, "rev-a")
	require.NoError(t, err)
	assert.Equal(t, RevisionArchived, first.Status)
	second, err := store.ReadRevision(ctx, skillID, "rev-b")
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, second.Status)
}

func TestApprovalService_Decide_WaitsForFileStoreSkillLock(t *testing.T) {
	dir := t.TempDir()
	store := newFileCandidateStore(dir)
	ptr := NewFileActivePointer(dir)
	pub := &mockPublisher{}
	ctx := context.Background()
	skillID := "locked-skill"
	revID := "rev-lock"

	require.NoError(t, store.WriteRevision(ctx, &Revision{
		SkillID:    skillID,
		RevisionID: revID,
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionPendingApproval,
		Spec:       &SkillSpec{Name: "Locked Skill", Description: "d", WhenToUse: "w", Steps: []string{"s"}},
		CreatedAt:  time.Now().UTC(),
	}))
	unlock, err := store.lockSkill(ctx, skillID)
	require.NoError(t, err)
	defer func() {
		if unlock != nil {
			unlock()
		}
	}()

	svc := NewApprovalService(store, ptr, pub)
	done := make(chan error, 1)
	go func() {
		done <- svc.Decide(ctx, ApprovalDecision{
			RevisionID: revID,
			SkillID:    skillID,
			Approved:   true,
			Reviewer:   "alice",
		})
	}()

	select {
	case err := <-done:
		require.NoError(t, err)
		require.Fail(t, "decision completed while file-store skill lock was held")
	case <-time.After(50 * time.Millisecond):
	}
	assert.Equal(t, 0, pub.upsertCount())

	unlock()
	unlock = nil
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(time.Second):
		require.Fail(t, "decision did not complete after file-store skill lock was released")
	}
	assert.Equal(t, 1, pub.upsertCount())
}

func TestApprovalService_LockSkill_FallsBackToProcessLock(t *testing.T) {
	svc := NewApprovalService(scanCandidateStore{}, nil, nil)
	unlock, err := svc.lockSkill(context.Background(), "fallback-skill")
	require.NoError(t, err)
	require.NotNil(t, unlock)
	unlock()
}

func TestApprovalService_Decide_ReturnsSkillLockError(t *testing.T) {
	svc := NewApprovalService(lockErrorStore{
		err: fmt.Errorf("lock unavailable"),
	}, nil, nil)
	err := svc.Decide(context.Background(), ApprovalDecision{
		RevisionID: "rev-lock",
		SkillID:    "locked-skill",
		Approved:   true,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), `lock skill "locked-skill"`)
	assert.Contains(t, err.Error(), "lock unavailable")
}

func TestApprovalService_ListPending_WithLimit(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ctx := context.Background()

	// Write 3 pending revisions
	for i, name := range []string{"skill-a", "skill-b", "skill-c"} {
		rev := &Revision{
			SkillID:    name,
			RevisionID: fmt.Sprintf("rev-%d", i),
			Source:     "reviewer",
			Action:     RevisionActionCreate,
			Status:     RevisionPendingApproval,
			Spec:       &SkillSpec{Name: name, Description: "d", WhenToUse: "w", Steps: []string{"s"}},
			CreatedAt:  time.Now().UTC(),
		}
		require.NoError(t, store.WriteRevision(ctx, rev))
	}

	svc := NewApprovalService(store, nil, nil)
	pending, err := svc.ListPending(ctx, ListPendingOpts{Limit: 2})
	require.NoError(t, err)
	assert.Len(t, pending, 2)
}

type blockingPublisher struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	mu      sync.Mutex
	upserts int
}

func newBlockingPublisher() *blockingPublisher {
	return &blockingPublisher{
		entered: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (p *blockingPublisher) UpsertSkill(_ context.Context, _ *SkillSpec) error {
	p.mu.Lock()
	p.upserts++
	p.mu.Unlock()
	p.once.Do(func() { close(p.entered) })
	<-p.release
	return nil
}

func (p *blockingPublisher) DeleteSkill(context.Context, string) error {
	return nil
}

func (p *blockingPublisher) upsertCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.upserts
}

func (m *mockPublisher) upsertCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.skills)
}

type lockErrorStore struct {
	CandidateStore
	err error
}

func (s lockErrorStore) lockSkill(context.Context, string) (func(), error) {
	return nil, s.err
}

func TestApprovalService_ListPending_NilStore(t *testing.T) {
	svc := NewApprovalService(nil, nil, nil)
	pending, err := svc.ListPending(context.Background(), ListPendingOpts{})
	require.NoError(t, err)
	assert.Nil(t, pending)
}

func TestApprovalService_Decide_NilStore(t *testing.T) {
	svc := NewApprovalService(nil, nil, nil)
	err := svc.Decide(context.Background(), ApprovalDecision{
		RevisionID: "r", SkillID: "s", Approved: true,
	})
	require.Error(t, err)
}

func TestApprovalService_Decide_ReadRevisionError(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	svc := NewApprovalService(store, nil, nil)
	// Revision does not exist → read error.
	err := svc.Decide(context.Background(), ApprovalDecision{
		RevisionID: "missing", SkillID: "missing", Approved: true,
	})
	require.Error(t, err)
}

func TestApprovalService_Decide_PublisherError(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	pub := &mockPublisher{err: fmt.Errorf("publish failed")}
	ctx := context.Background()

	rev := &Revision{
		SkillID:    "skill",
		RevisionID: "rev",
		Source:     "reviewer",
		Action:     RevisionActionCreate,
		Status:     RevisionPendingApproval,
		Spec:       &SkillSpec{Name: "Skill", Description: "d", WhenToUse: "w", Steps: []string{"s1", "s2"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, rev))

	svc := NewApprovalService(store, nil, pub)
	err := svc.Decide(ctx, ApprovalDecision{
		RevisionID: "rev", SkillID: "skill", Approved: true, Reviewer: "x",
	})
	require.Error(t, err)
}

func TestFileCandidateStore_ListSkills(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ctx := context.Background()

	// Initially empty
	skills, err := store.ListSkills(ctx)
	require.NoError(t, err)
	assert.Empty(t, skills)

	// Write revisions for two skills
	for _, sk := range []string{"alpha", "beta"} {
		rev := &Revision{
			SkillID:    sk,
			RevisionID: "r1",
			Status:     RevisionActive,
			CreatedAt:  time.Now(),
		}
		require.NoError(t, store.WriteRevision(ctx, rev))
	}

	skills, err = store.ListSkills(ctx)
	require.NoError(t, err)
	assert.Len(t, skills, 2)
	assert.Contains(t, skills, "alpha")
	assert.Contains(t, skills, "beta")
}
