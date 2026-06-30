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

// rollbackHarness builds a CandidateStore + ActivePointer pair pre-populated
// with `count` create-revisions for the same skillID, all but the last in
// archived state. The last revision is left active and pointed to. The
// returned slice is ordered oldest-first.
func rollbackHarness(t *testing.T, count int) (
	dir, skillID string,
	store CandidateStore,
	pointer ActivePointer,
	publisher *mockPublisher,
	revs []*Revision,
) {
	t.Helper()
	dir = t.TempDir()
	store = NewFileCandidateStore(dir)
	pointer = NewFileActivePointer(dir)
	publisher = &mockPublisher{}
	skillID = "rollback-skill"
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := 0; i < count; i++ {
		status := RevisionArchived
		if i == count-1 {
			status = RevisionActive
		}
		rev := &Revision{
			SkillID:    skillID,
			RevisionID: revisionIDForIndex(i),
			Action:     RevisionActionCreate,
			Status:     status,
			CreatedAt:  base.Add(time.Duration(i) * time.Hour),
			Spec: &SkillSpec{
				Name:        "Rollback Skill",
				Description: "version " + revisionIDForIndex(i),
				WhenToUse:   "w",
				Steps:       []string{"s"},
			},
		}
		require.NoError(t, store.WriteRevision(ctx, rev))
		revs = append(revs, rev)
		// ListRevisions sorts by ModTime so we space out writes.
		time.Sleep(2 * time.Millisecond)
	}
	require.NoError(t, pointer.Set(ctx, skillID, revs[count-1].RevisionID))
	return dir, skillID, store, pointer, publisher, revs
}

func revisionIDForIndex(i int) string {
	return fmt.Sprintf("20260101T000000.000-rev%03d", i)
}

func TestApprovalService_Rollback_AutoSelectsMostRecentArchived(t *testing.T) {
	dir, skillID, store, pointer, pub, revs := rollbackHarness(t, 3)
	ctx := context.Background()

	svc := NewApprovalService(store, pointer, pub)
	res, err := svc.Rollback(ctx, skillID, RollbackOpts{
		Reviewer: "alice",
		Comment:  "regressed quality",
	})
	require.NoError(t, err)
	assert.Equal(t, revs[2].RevisionID, res.PreviousActiveID)
	assert.Equal(t, revs[1].RevisionID, res.RestoredID)

	active, err := pointer.Get(ctx, skillID)
	require.NoError(t, err)
	assert.Equal(t, revs[1].RevisionID, active)

	// Restored revision is now active.
	stored, err := store.ReadRevision(ctx, skillID, revs[1].RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, stored.Status)
	require.NotNil(t, stored.PromotedAt)

	// Previously-active revision is now archived.
	prev, err := store.ReadRevision(ctx, skillID, revs[2].RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionArchived, prev.Status)

	// Publisher saw the restored skill.
	pub.mu.Lock()
	require.Len(t, pub.skills, 1)
	assert.Equal(t, "Rollback Skill", pub.skills[0].Name)
	assert.Contains(t, pub.skills[0].Description, revs[1].RevisionID)
	pub.mu.Unlock()

	// Audit log captured both the archive and the promote.
	raw, err := os.ReadFile(filepath.Join(dir, skillID, "audit.log"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"action":"archive"`)
	assert.Contains(t, string(raw), `"action":"promote"`)
	assert.Contains(t, string(raw), `"actor":"alice"`)
	assert.Contains(t, string(raw), `"comment":"regressed quality"`)
}

func TestApprovalService_Rollback_ExplicitTargetRevision(t *testing.T) {
	_, skillID, store, pointer, pub, revs := rollbackHarness(t, 4)
	ctx := context.Background()

	// Roll back to the oldest archived revision, skipping the more recent ones.
	svc := NewApprovalService(store, pointer, pub)
	res, err := svc.Rollback(ctx, skillID, RollbackOpts{
		TargetRevisionID: revs[0].RevisionID,
	})
	require.NoError(t, err)
	assert.Equal(t, revs[3].RevisionID, res.PreviousActiveID)
	assert.Equal(t, revs[0].RevisionID, res.RestoredID)
}

func TestApprovalService_Rollback_NoArchivedRevision(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	pointer := NewFileActivePointer(dir)
	ctx := context.Background()

	rev := &Revision{
		SkillID:    "lonely-skill",
		RevisionID: "rev-001",
		Action:     RevisionActionCreate,
		Status:     RevisionActive,
		CreatedAt:  time.Now().UTC(),
		Spec:       &SkillSpec{Name: "Lonely", Description: "d", WhenToUse: "w", Steps: []string{"s"}},
	}
	require.NoError(t, store.WriteRevision(ctx, rev))
	require.NoError(t, pointer.Set(ctx, "lonely-skill", "rev-001"))

	svc := NewApprovalService(store, pointer, &mockPublisher{})
	_, err := svc.Rollback(ctx, "lonely-skill", RollbackOpts{})
	require.ErrorIs(t, err, ErrNoArchivedRevision)
}

func TestApprovalService_Rollback_ExplicitTargetMustBeArchived(t *testing.T) {
	_, skillID, store, pointer, _, revs := rollbackHarness(t, 2)
	ctx := context.Background()

	// Targeting the currently-active revision is an error — the user
	// must pick an archived one.
	svc := NewApprovalService(store, pointer, &mockPublisher{})
	_, err := svc.Rollback(ctx, skillID, RollbackOpts{
		TargetRevisionID: revs[1].RevisionID,
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "expected archived")
}

func TestApprovalService_Rollback_RequiresStoreAndPointer(t *testing.T) {
	ctx := context.Background()
	// No store configured.
	svc := NewApprovalService(nil, nil, nil)
	_, err := svc.Rollback(ctx, "any", RollbackOpts{})
	require.Error(t, err)
	// Store but no pointer.
	dir := t.TempDir()
	svc = NewApprovalService(NewFileCandidateStore(dir), nil, nil)
	_, err = svc.Rollback(ctx, "any", RollbackOpts{})
	require.Error(t, err)
	// Empty skill ID.
	svc = NewApprovalService(NewFileCandidateStore(dir), NewFileActivePointer(dir), nil)
	_, err = svc.Rollback(ctx, "  ", RollbackOpts{})
	require.Error(t, err)
}
