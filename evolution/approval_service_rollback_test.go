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
//
// To keep ListRevisions ordering deterministic on coarse-modtime
// filesystems, the helper bumps each revision's mtime by one second
// after writing rather than relying on real-time sleeps.
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
		// Stamp deterministic mtime — ListRevisions orders by ModTime,
		// so we space each revision by one second instead of using
		// time.Sleep, which can flake on coarse-clock CI runners.
		setRevisionMtime(t, dir, skillID, rev.RevisionID,
			time.Date(2026, 1, 1, 0, 0, i, 0, time.UTC))
		revs = append(revs, rev)
	}
	require.NoError(t, pointer.Set(ctx, skillID, revs[count-1].RevisionID))
	return dir, skillID, store, pointer, publisher, revs
}

// setRevisionMtime adjusts the on-disk modification time of the
// revision directory so ListRevisions, which relies on ModTime, sees a
// stable order across platforms.
func setRevisionMtime(t *testing.T, root, skillID, revisionID string, when time.Time) {
	t.Helper()
	p := filepath.Join(root, skillID, "revisions", revisionID)
	require.NoError(t, os.Chtimes(p, when, when))
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

	// Publisher saw the restored skill. Read under the lock and
	// release before asserting so a failed assertion does not strand
	// the mutex held.
	pub.mu.Lock()
	publishedSkills := append([]*SkillSpec(nil), pub.skills...)
	pub.mu.Unlock()
	require.Len(t, publishedSkills, 1)
	assert.Equal(t, "Rollback Skill", publishedSkills[0].Name)
	assert.Contains(t, publishedSkills[0].Description, revs[1].RevisionID)

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
	// must pick an archived one. The error wraps ErrNoArchivedRevision
	// so callers can match with errors.Is.
	svc := NewApprovalService(store, pointer, &mockPublisher{})
	_, err := svc.Rollback(ctx, skillID, RollbackOpts{
		TargetRevisionID: revs[1].RevisionID,
	})
	require.ErrorIs(t, err, ErrNoArchivedRevision)
}

func TestApprovalService_Rollback_ExplicitTargetMissing(t *testing.T) {
	_, skillID, store, pointer, _, _ := rollbackHarness(t, 2)
	ctx := context.Background()

	svc := NewApprovalService(store, pointer, &mockPublisher{})
	_, err := svc.Rollback(ctx, skillID, RollbackOpts{
		TargetRevisionID: "nonexistent",
	})
	require.ErrorIs(t, err, ErrNoArchivedRevision)
}

func TestApprovalService_Rollback_PublisherFailureLeavesActiveIntact(t *testing.T) {
	_, skillID, store, pointer, pub, revs := rollbackHarness(t, 2)
	pub.err = fmt.Errorf("publish blew up")
	ctx := context.Background()

	svc := NewApprovalService(store, pointer, pub)
	_, err := svc.Rollback(ctx, skillID, RollbackOpts{Reviewer: "alice"})
	require.Error(t, err)

	// The previous active revision must remain active because the
	// publisher mutation failed before any state change. This
	// guarantees agents never see an archived revision wired up to
	// the live pointer.
	prev, err := store.ReadRevision(ctx, skillID, revs[1].RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, prev.Status)

	target, err := store.ReadRevision(ctx, skillID, revs[0].RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionArchived, target.Status)

	active, err := pointer.Get(ctx, skillID)
	require.NoError(t, err)
	assert.Equal(t, revs[1].RevisionID, active)
}

func TestApprovalService_Rollback_ToDeleteRevisionClearsPointer(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	pointer := NewFileActivePointer(dir)
	pub := &mockPublisher{}
	ctx := context.Background()
	skillID := "delete-rollback"

	// Archived delete revision (the rollback target) and a current
	// active create revision.
	deleteRev := &Revision{
		SkillID:    skillID,
		RevisionID: "rev-delete",
		Action:     RevisionActionDelete,
		Status:     RevisionArchived,
		TargetName: "Delete Skill",
		CreatedAt:  time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	createRev := &Revision{
		SkillID:    skillID,
		RevisionID: "rev-create",
		Action:     RevisionActionCreate,
		Status:     RevisionActive,
		Spec: &SkillSpec{
			Name: "Delete Skill", Description: "d", WhenToUse: "w", Steps: []string{"s"},
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
	}
	require.NoError(t, store.WriteRevision(ctx, deleteRev))
	require.NoError(t, store.WriteRevision(ctx, createRev))
	setRevisionMtime(t, dir, skillID, deleteRev.RevisionID, deleteRev.CreatedAt)
	setRevisionMtime(t, dir, skillID, createRev.RevisionID, createRev.CreatedAt)
	require.NoError(t, pointer.Set(ctx, skillID, createRev.RevisionID))

	svc := NewApprovalService(store, pointer, pub)
	res, err := svc.Rollback(ctx, skillID, RollbackOpts{
		TargetRevisionID: deleteRev.RevisionID,
		Reviewer:         "alice",
	})
	require.NoError(t, err)
	assert.Equal(t, deleteRev.RevisionID, res.RestoredID)

	// Publisher saw a delete, not an upsert.
	pub.mu.Lock()
	deletions := append([]string(nil), pub.deletions...)
	skills := append([]*SkillSpec(nil), pub.skills...)
	pub.mu.Unlock()
	assert.Equal(t, []string{"Delete Skill"}, deletions)
	assert.Empty(t, skills)

	// Active pointer cleared so the live skill view stays empty.
	active, err := pointer.Get(ctx, skillID)
	require.NoError(t, err)
	assert.Empty(t, active)
}

func TestApprovalService_Rollback_FromDeleteTombstoneArchivesDelete(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	pointer := NewFileActivePointer(dir)
	pub := &mockPublisher{}
	ctx := context.Background()
	skillID := "deleted-skill"

	archivedCreate := &Revision{
		SkillID:    skillID,
		RevisionID: "rev-create",
		Action:     RevisionActionCreate,
		Status:     RevisionArchived,
		Spec: &SkillSpec{
			Name: "Deleted Skill", Description: "old body", WhenToUse: "w", Steps: []string{"s"},
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	activeDelete := &Revision{
		SkillID:    skillID,
		RevisionID: "rev-delete",
		Action:     RevisionActionDelete,
		Status:     RevisionActive,
		TargetName: "Deleted Skill",
		CreatedAt:  time.Date(2026, 1, 1, 0, 0, 1, 0, time.UTC),
	}
	require.NoError(t, store.WriteRevision(ctx, archivedCreate))
	require.NoError(t, store.WriteRevision(ctx, activeDelete))
	setRevisionMtime(t, dir, skillID, archivedCreate.RevisionID, archivedCreate.CreatedAt)
	setRevisionMtime(t, dir, skillID, activeDelete.RevisionID, activeDelete.CreatedAt)
	require.NoError(t, pointer.Clear(ctx, skillID))

	svc := NewApprovalService(store, pointer, pub)
	res, err := svc.Rollback(ctx, skillID, RollbackOpts{Reviewer: "alice"})
	require.NoError(t, err)
	assert.Equal(t, activeDelete.RevisionID, res.PreviousActiveID)
	assert.Equal(t, archivedCreate.RevisionID, res.RestoredID)

	restored, err := store.ReadRevision(ctx, skillID, archivedCreate.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, restored.Status)

	tombstone, err := store.ReadRevision(ctx, skillID, activeDelete.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionArchived, tombstone.Status)

	active, err := pointer.Get(ctx, skillID)
	require.NoError(t, err)
	assert.Equal(t, archivedCreate.RevisionID, active)

	pub.mu.Lock()
	publishedSkills := append([]*SkillSpec(nil), pub.skills...)
	pub.mu.Unlock()
	require.Len(t, publishedSkills, 1)
	assert.Equal(t, "Deleted Skill", publishedSkills[0].Name)
	assert.Contains(t, publishedSkills[0].Description, "old body")
}

func TestApprovalService_Rollback_NoCurrentActivePromotesArchived(t *testing.T) {
	dir := t.TempDir()
	store := NewFileCandidateStore(dir)
	pointer := NewFileActivePointer(dir)
	pub := &mockPublisher{}
	ctx := context.Background()
	skillID := "archived-only"

	archived := &Revision{
		SkillID:    skillID,
		RevisionID: "rev-archived",
		Action:     RevisionActionCreate,
		Status:     RevisionArchived,
		Spec: &SkillSpec{
			Name: "Archived Only", Description: "restore me", WhenToUse: "w", Steps: []string{"s"},
		},
		CreatedAt: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	}
	require.NoError(t, store.WriteRevision(ctx, archived))
	require.NoError(t, pointer.Clear(ctx, skillID))

	svc := NewApprovalService(store, pointer, pub)
	res, err := svc.Rollback(ctx, skillID, RollbackOpts{})
	require.NoError(t, err)
	assert.Empty(t, res.PreviousActiveID)
	assert.Equal(t, archived.RevisionID, res.RestoredID)

	restored, err := store.ReadRevision(ctx, skillID, archived.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionActive, restored.Status)

	active, err := pointer.Get(ctx, skillID)
	require.NoError(t, err)
	assert.Equal(t, archived.RevisionID, active)

	raw, err := os.ReadFile(filepath.Join(dir, skillID, "audit.log"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"reason":"rollback"`)
}

func TestApprovalService_Rollback_TargetAlreadyActiveFromPointer(t *testing.T) {
	_, skillID, store, pointer, pub, revs := rollbackHarness(t, 2)
	ctx := context.Background()

	// The target is archived in the store, but the pointer still names it.
	// Rollback must detect that inconsistent state before publishing.
	require.NoError(t, pointer.Set(ctx, skillID, revs[0].RevisionID))

	svc := NewApprovalService(store, pointer, pub)
	_, err := svc.Rollback(ctx, skillID, RollbackOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "already active")

	pub.mu.Lock()
	publishedSkills := append([]*SkillSpec(nil), pub.skills...)
	pub.mu.Unlock()
	assert.Empty(t, publishedSkills)
}

func TestApprovalService_CurrentActiveRevisionID_ReturnsPointerError(t *testing.T) {
	svc := NewApprovalService(NewFileCandidateStore(t.TempDir()), errActivePointer{
		err: fmt.Errorf("pointer unavailable"),
	}, nil)

	_, err := svc.currentActiveRevisionID(context.Background(), "skill")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "get active pointer")
}

func TestApprovalService_FindLatestActiveRevisionID_PropagatesListError(t *testing.T) {
	svc := NewApprovalService(listErrorStore{
		err: fmt.Errorf("list unavailable"),
	}, &stubActivePointer{}, nil)

	_, err := svc.findLatestActiveRevisionID(context.Background(), "skill")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "list revisions")
}

func TestApprovalService_FindLatestActiveRevisionID_ReadBranches(t *testing.T) {
	ctx := context.Background()
	skillID := "scan-skill"
	archived := &Revision{
		SkillID:    skillID,
		RevisionID: "rev-archived",
		Status:     RevisionArchived,
	}

	svc := NewApprovalService(scanCandidateStore{
		ids: []string{"rev-archived", "rev-missing"},
		revs: map[string]*Revision{
			"rev-archived": archived,
		},
	}, &stubActivePointer{}, nil)

	active, err := svc.findLatestActiveRevisionID(ctx, skillID)
	require.NoError(t, err)
	assert.Empty(t, active)

	svc = NewApprovalService(scanCandidateStore{
		ids: []string{"rev-bad"},
		errs: map[string]error{
			"rev-bad": fmt.Errorf("read unavailable"),
		},
	}, &stubActivePointer{}, nil)

	_, err = svc.findLatestActiveRevisionID(ctx, skillID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read revision")
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

type errActivePointer struct {
	err error
}

func (p errActivePointer) Get(_ context.Context, _ string) (string, error) {
	return "", p.err
}

func (p errActivePointer) Set(_ context.Context, _, _ string) error {
	return nil
}

func (p errActivePointer) Clear(_ context.Context, _ string) error {
	return nil
}

type listErrorStore struct {
	CandidateStore
	err error
}

func (s listErrorStore) ListRevisions(_ context.Context, _ string) ([]string, error) {
	return nil, s.err
}

type scanCandidateStore struct {
	CandidateStore
	ids  []string
	revs map[string]*Revision
	errs map[string]error
}

func (s scanCandidateStore) ListRevisions(_ context.Context, _ string) ([]string, error) {
	return append([]string(nil), s.ids...), nil
}

func (s scanCandidateStore) ReadRevision(_ context.Context, _, revisionID string) (*Revision, error) {
	if err := s.errs[revisionID]; err != nil {
		return nil, err
	}
	rev := s.revs[revisionID]
	if rev == nil {
		return nil, os.ErrNotExist
	}
	return rev, nil
}
