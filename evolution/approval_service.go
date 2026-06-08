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
	"time"
)

// ErrAlreadyDecided is returned when Decide is called on a revision
// that is no longer in pending_approval state.
var ErrAlreadyDecided = errors.New("revision already decided")

// ApprovalDecision is the external reviewer's decision on a pending revision.
type ApprovalDecision struct {
	RevisionID string
	SkillID    string
	Approved   bool
	Reviewer   string // reviewer identity (email, user ID, etc.)
	Comment    string // optional comment
	DecidedAt  time.Time
}

// ListPendingOpts configures the ListPending query.
type ListPendingOpts struct {
	Limit int // max results; 0 means no limit
}

// ApprovalService manages the lifecycle of pending_approval revisions.
// The worker writes revisions to this state; external systems (CLI, API,
// webhook) call Decide to promote or reject them.
type ApprovalService struct {
	store     CandidateStore
	pointer   ActivePointer
	publisher Publisher
}

// NewApprovalService creates an ApprovalService backed by the given stores.
func NewApprovalService(store CandidateStore, pointer ActivePointer, publisher Publisher) *ApprovalService {
	return &ApprovalService{
		store:     store,
		pointer:   pointer,
		publisher: publisher,
	}
}

// ListPending returns all revisions in pending_approval state.
func (s *ApprovalService) ListPending(ctx context.Context, opts ListPendingOpts) ([]*Revision, error) {
	if s.store == nil {
		return nil, nil
	}
	skills, err := s.store.ListSkills(ctx)
	if err != nil {
		return nil, fmt.Errorf("list skills: %w", err)
	}

	var pending []*Revision
	for _, skillID := range skills {
		revIDs, err := s.store.ListRevisions(ctx, skillID)
		if err != nil {
			continue
		}
		for _, revID := range revIDs {
			rev, err := s.store.ReadRevision(ctx, skillID, revID)
			if err != nil {
				continue
			}
			if rev.Status == RevisionPendingApproval {
				pending = append(pending, rev)
				if opts.Limit > 0 && len(pending) >= opts.Limit {
					return pending, nil
				}
			}
		}
	}
	return pending, nil
}

// Decide approves or rejects a pending_approval revision.
// Approved revisions are promoted to active via the publisher.
// Rejected revisions have their status set to rejected.
// Returns ErrAlreadyDecided if the revision is no longer pending.
func (s *ApprovalService) Decide(ctx context.Context, decision ApprovalDecision) error {
	if s.store == nil {
		return fmt.Errorf("no candidate store configured")
	}

	rev, err := s.store.ReadRevision(ctx, decision.SkillID, decision.RevisionID)
	if err != nil {
		return fmt.Errorf("read revision: %w", err)
	}
	if rev.Status != RevisionPendingApproval {
		return ErrAlreadyDecided
	}

	persisted := false
	if decision.Approved {
		// Promote: publish skill + set active pointer
		if s.publisher != nil && rev.Spec != nil {
			if err := s.publisher.UpsertSkill(ctx, rev.Spec); err != nil {
				return fmt.Errorf("publish skill: %w", err)
			}
		}
		rev.Status = RevisionActive
		now := time.Now().UTC()
		rev.PromotedAt = &now
		if err := s.store.WriteRevision(ctx, rev); err != nil {
			return fmt.Errorf("write revision: %w", err)
		}
		persisted = true
		if s.pointer != nil {
			if err := archiveCurrentActiveRevision(ctx, s.store, s.pointer, rev.SkillID, rev.RevisionID); err != nil {
				return err
			}
			if err := s.pointer.Set(ctx, rev.SkillID, rev.RevisionID); err != nil {
				return fmt.Errorf("set active pointer: %w", err)
			}
		}
	} else {
		rev.Status = RevisionRejected
	}

	// Persist updated status
	if !persisted {
		err = s.store.WriteRevision(ctx, rev)
	}
	if err != nil {
		return fmt.Errorf("write revision: %w", err)
	}

	// Audit trail
	action := "approve"
	if !decision.Approved {
		action = "reject"
	}
	reason := decision.Comment
	if reason == "" {
		reason = "human decision by " + decision.Reviewer
	}
	_ = s.store.AppendAudit(ctx, AuditEvent{
		Action:     action,
		SkillID:    decision.SkillID,
		RevisionID: decision.RevisionID,
		Status:     string(rev.Status),
		Reason:     reason,
	})

	return nil
}
