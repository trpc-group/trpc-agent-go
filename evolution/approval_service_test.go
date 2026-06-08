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
		Action:     "create",
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
		Action:     "create",
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
		Action:     "create",
		Status:     RevisionPendingApproval,
		Spec:       &SkillSpec{Name: "My Skill", Description: "d", WhenToUse: "w", Steps: []string{"s1", "s2"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, rev))

	svc := NewApprovalService(store, ptr, pub)
	err := svc.Decide(ctx, ApprovalDecision{
		RevisionID: "rev-approve",
		SkillID:    "my-skill",
		Approved:   true,
		Reviewer:   "alice@example.com",
		Comment:    "looks good",
		DecidedAt:  time.Now().UTC(),
	})
	require.NoError(t, err)

	// Verify revision is now active
	stored, err := store.ReadRevision(ctx, "my-skill", "rev-approve")
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, stored.Status)
	assert.NotNil(t, stored.PromotedAt)

	// Verify publisher was called
	pub.mu.Lock()
	assert.Len(t, pub.skills, 1)
	pub.mu.Unlock()

	// Verify active pointer
	activeRev, err := ptr.Get(ctx, "my-skill")
	require.NoError(t, err)
	assert.Equal(t, "rev-approve", activeRev)
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
		Action:     "create",
		Status:     RevisionActive,
		Spec:       &SkillSpec{Name: "My Skill", Description: "old", WhenToUse: "w", Steps: []string{"s1", "s2"}},
		CreatedAt:  time.Now().UTC(),
	}
	pendingRev := &Revision{
		SkillID:    "my-skill",
		RevisionID: "rev-new",
		Source:     "reviewer",
		Action:     "update",
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

func TestApprovalService_Decide_Reject(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ctx := context.Background()

	rev := &Revision{
		SkillID:    "bad-skill",
		RevisionID: "rev-reject",
		Source:     "reviewer",
		Action:     "create",
		Status:     RevisionPendingApproval,
		Spec:       &SkillSpec{Name: "Bad Skill", Description: "d", WhenToUse: "w", Steps: []string{"s1", "s2"}},
		CreatedAt:  time.Now().UTC(),
	}
	require.NoError(t, store.WriteRevision(ctx, rev))

	svc := NewApprovalService(store, nil, nil)
	err := svc.Decide(ctx, ApprovalDecision{
		RevisionID: "rev-reject",
		SkillID:    "bad-skill",
		Approved:   false,
		Reviewer:   "bob@example.com",
		Comment:    "steps too vague",
		DecidedAt:  time.Now().UTC(),
	})
	require.NoError(t, err)

	stored, err := store.ReadRevision(ctx, "bad-skill", "rev-reject")
	require.NoError(t, err)
	assert.Equal(t, RevisionRejected, stored.Status)
}

func TestApprovalService_Decide_AlreadyDecided(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	ctx := context.Background()

	rev := &Revision{
		SkillID:    "decided-skill",
		RevisionID: "rev-decided",
		Source:     "reviewer",
		Action:     "create",
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
			Action:     "create",
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
		Action:     "create",
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
