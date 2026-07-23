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
	"fmt"
	"os"
	"strings"
	"sync"
)

var revisionMutationLocks mutationLockRegistry

type mutationLockRegistry struct {
	mu    sync.Mutex
	locks map[string]*mutationLock
}

type mutationLock struct {
	mu   sync.Mutex
	refs int
}

type candidateStoreSkillLocker interface {
	lockSkill(ctx context.Context, skillID string) (func(), error)
}

func (r *mutationLockRegistry) lock(skillID string) func() {
	r.mu.Lock()
	if r.locks == nil {
		r.locks = make(map[string]*mutationLock)
	}
	lock := r.locks[skillID]
	if lock == nil {
		lock = &mutationLock{}
		r.locks[skillID] = lock
	}
	lock.refs++
	r.mu.Unlock()

	lock.mu.Lock()
	return func() {
		lock.mu.Unlock()
		r.mu.Lock()
		lock.refs--
		if lock.refs == 0 {
			delete(r.locks, skillID)
		}
		r.mu.Unlock()
	}
}

// lockRevisionMutation serializes all publisher and active-pointer mutations
// for one skill. File stores add a cross-process lock; other stores still get
// process-local serialization.
func lockRevisionMutation(
	ctx context.Context,
	store CandidateStore,
	skillID string,
) (func(), error) {
	localUnlock := revisionMutationLocks.lock(skillID)
	locker, ok := store.(candidateStoreSkillLocker)
	if !ok || locker == nil {
		return localUnlock, nil
	}
	storeUnlock, err := locker.lockSkill(ctx, skillID)
	if err != nil {
		localUnlock()
		return nil, err
	}
	return func() {
		storeUnlock()
		localUnlock()
	}, nil
}

func archiveCurrentActiveRevision(
	ctx context.Context,
	store CandidateStore,
	pointer ActivePointer,
	skillID string,
	replacingRevisionID string,
) error {
	if store == nil || pointer == nil || strings.TrimSpace(skillID) == "" {
		return nil
	}
	currentRevisionID, err := pointer.Get(ctx, skillID)
	if err != nil {
		return fmt.Errorf("get active pointer: %w", err)
	}
	if currentRevisionID == "" || currentRevisionID == replacingRevisionID {
		return nil
	}
	current, err := store.ReadRevision(ctx, skillID, currentRevisionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read active revision: %w", err)
	}
	if current.Status == RevisionArchived {
		return nil
	}
	current.Status = RevisionArchived
	if err := store.WriteRevision(ctx, current); err != nil {
		return fmt.Errorf("archive active revision: %w", err)
	}
	_ = store.AppendAudit(ctx, AuditEvent{
		Action:     AuditActionArchive,
		SkillID:    skillID,
		RevisionID: currentRevisionID,
		Status:     string(RevisionArchived),
		Reason:     "superseded by " + replacingRevisionID,
	})
	return nil
}
