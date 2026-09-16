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
	"time"
)

// ErrAlreadyDecided is returned when Decide is called on a revision
// that is no longer in pending_approval state.
var ErrAlreadyDecided = errors.New("revision already decided")

// ErrNoArchivedRevision is returned when Rollback is called on a skill
// that has no archived revision to fall back to.
var ErrNoArchivedRevision = errors.New("no archived revision available for rollback")

// ErrStaleRevisionParent is returned when an update's ParentID no longer
// matches the configured active pointer. Submission does not persist the stale
// candidate; approval marks an already-persisted candidate as rejected.
var ErrStaleRevisionParent = errors.New("stale revision parent")

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
// When an active pointer is configured, an approved update is refused if its
// ParentID no longer matches the active revision. An empty ParentID means the
// update was evaluated with no active revision. Stale revisions are rejected,
// and the current active revision is unchanged.
// Returns ErrAlreadyDecided if the revision is no longer pending.
func (s *ApprovalService) Decide(ctx context.Context, decision ApprovalDecision) error {
	if s.store == nil {
		return fmt.Errorf("no candidate store configured")
	}

	unlock, err := lockRevisionMutation(ctx, s.store, decision.SkillID)
	if err != nil {
		return fmt.Errorf("lock skill %q: %w", decision.SkillID, err)
	}
	defer unlock()

	rev, err := s.store.ReadRevision(ctx, decision.SkillID, decision.RevisionID)
	if err != nil {
		return fmt.Errorf("read revision: %w", err)
	}
	if rev.Status != RevisionPendingApproval {
		return ErrAlreadyDecided
	}
	decidedAt := decision.DecidedAt
	if decidedAt.IsZero() {
		decidedAt = time.Now().UTC()
	}
	if decision.Approved {
		if err := validateCurrentParent(ctx, s.pointer, rev); err != nil {
			if errors.Is(err, ErrStaleRevisionParent) {
				return s.rejectStaleRevision(ctx, rev, decision, decidedAt, err)
			}
			return fmt.Errorf("approve revision: %w", err)
		}
	}
	rev.HumanReport = mergeHumanDecision(rev.HumanReport, decision, decidedAt)

	if decision.Approved {
		if err := s.approveRevision(ctx, rev); err != nil {
			return err
		}
		rev.Status = RevisionActive
		rev.PromotedAt = &decidedAt
	} else {
		rev.Status = RevisionRejected
	}

	// Persist updated status
	if err := s.store.WriteRevision(ctx, rev); err != nil {
		return fmt.Errorf("write revision: %w", err)
	}

	// Audit trail
	_ = s.store.AppendAudit(ctx, AuditEvent{
		At:         decidedAt,
		Action:     auditActionForDecision(decision.Approved),
		SkillID:    decision.SkillID,
		RevisionID: decision.RevisionID,
		Status:     string(rev.Status),
		Reason:     humanDecisionReason(decision),
		Actor:      decision.Reviewer,
		Comment:    decision.Comment,
	})

	return nil
}

func (s *ApprovalService) rejectStaleRevision(
	ctx context.Context,
	rev *Revision,
	decision ApprovalDecision,
	decidedAt time.Time,
	cause error,
) error {
	rejection := decision
	rejection.Approved = false
	rev.HumanReport = mergeHumanDecision(rev.HumanReport, rejection, decidedAt)
	rev.HumanReport.Reasons = append(rev.HumanReport.Reasons, cause.Error())
	rev.Status = RevisionRejected
	staleErr := fmt.Errorf("approve revision: %w", cause)
	if err := s.store.WriteRevision(ctx, rev); err != nil {
		return errors.Join(staleErr, fmt.Errorf("write stale revision rejection: %w", err))
	}
	_ = s.store.AppendAudit(ctx, AuditEvent{
		At:         decidedAt,
		Action:     AuditActionReject,
		SkillID:    decision.SkillID,
		RevisionID: decision.RevisionID,
		Status:     string(rev.Status),
		Reason:     cause.Error(),
		Actor:      decision.Reviewer,
		Comment:    decision.Comment,
	})
	return staleErr
}

func (s *ApprovalService) approveRevision(ctx context.Context, rev *Revision) error {
	switch rev.Action {
	case RevisionActionDelete:
		if s.publisher == nil {
			return fmt.Errorf("delete skill: no publisher configured")
		}
		name := revisionTargetName(rev)
		if err := s.publisher.DeleteSkill(ctx, name); err != nil {
			return fmt.Errorf("delete skill: %w", err)
		}
		if s.pointer != nil {
			if err := archiveCurrentActiveRevision(ctx, s.store, s.pointer, rev.SkillID, rev.RevisionID); err != nil {
				return err
			}
			if err := s.pointer.Clear(ctx, rev.SkillID); err != nil {
				return fmt.Errorf("clear active pointer: %w", err)
			}
		}
	default:
		if s.publisher != nil && rev.Spec != nil {
			if err := s.publisher.UpsertSkill(ctx, rev.Spec); err != nil {
				return fmt.Errorf("publish skill: %w", err)
			}
		}
		if s.pointer != nil {
			if err := archiveCurrentActiveRevision(ctx, s.store, s.pointer, rev.SkillID, rev.RevisionID); err != nil {
				return err
			}
			if err := s.pointer.Set(ctx, rev.SkillID, rev.RevisionID); err != nil {
				return fmt.Errorf("set active pointer: %w", err)
			}
		}
	}
	return nil
}

func mergeHumanDecision(report *HumanReport, decision ApprovalDecision, decidedAt time.Time) *HumanReport {
	if report == nil {
		report = &HumanReport{Held: true}
	}
	approved := decision.Approved
	report.Approved = &approved
	report.Reviewer = decision.Reviewer
	report.Comment = decision.Comment
	report.DecidedAt = &decidedAt
	return report
}

func auditActionForDecision(approved bool) AuditAction {
	if approved {
		return AuditActionApprove
	}
	return AuditActionReject
}

func humanDecisionReason(decision ApprovalDecision) string {
	if decision.Comment != "" {
		return decision.Comment
	}
	if decision.Reviewer != "" {
		return "human decision by " + decision.Reviewer
	}
	return "human decision"
}

// RollbackOpts configures Rollback. Reviewer / Comment / DecidedAt are
// recorded on the audit log entry so operators can reconstruct who
// reverted a skill and why.
type RollbackOpts struct {
	// TargetRevisionID, when non-empty, rolls back to the specific
	// archived revision id. When empty, the latest archived revision in
	// the store's revision order is selected automatically.
	TargetRevisionID string
	Reviewer         string
	Comment          string
	DecidedAt        time.Time
}

// RollbackResult describes the outcome of a Rollback operation.
type RollbackResult struct {
	// PreviousActiveID is the revision that was active before rollback
	// (now archived). Empty when the skill had no active revision.
	PreviousActiveID string
	// RestoredID is the revision that is now active.
	RestoredID string
}

// Rollback reverts the active revision of a skill to a previously
// archived one. The current active revision is demoted to archived and
// the chosen archived revision is promoted back to active. The
// publisher is updated to reflect the restored skill body so the
// rollback is immediately visible to running agents (no Refresh needed
// — agents pick up the new SKILL.md on the next read).
//
// Returns ErrNoArchivedRevision when no archived revision is available
// or when TargetRevisionID is set but does not exist / is not
// archived. Use errors.Is to distinguish this case.
func (s *ApprovalService) Rollback(ctx context.Context, skillID string, opts RollbackOpts) (*RollbackResult, error) {
	if s.store == nil {
		return nil, fmt.Errorf("no candidate store configured")
	}
	if s.pointer == nil {
		return nil, fmt.Errorf("no active pointer configured")
	}
	if strings.TrimSpace(skillID) == "" {
		return nil, fmt.Errorf("rollback: empty skill id")
	}

	unlock, err := lockRevisionMutation(ctx, s.store, skillID)
	if err != nil {
		return nil, fmt.Errorf("lock skill %q: %w", skillID, err)
	}
	defer unlock()

	target, err := s.selectRollbackTarget(ctx, skillID, opts.TargetRevisionID)
	if err != nil {
		return nil, err
	}
	if err := validateRollbackTarget(target); err != nil {
		return nil, err
	}
	currentRevID, err := s.currentActiveRevisionID(ctx, skillID)
	if err != nil {
		return nil, err
	}
	if currentRevID == target.RevisionID {
		return nil, fmt.Errorf("rollback: target %q is already active", target.RevisionID)
	}

	decidedAt := opts.DecidedAt
	if decidedAt.IsZero() {
		decidedAt = time.Now().UTC()
	}

	currentBefore, err := s.readRevisionForRollbackRestore(ctx, skillID, currentRevID)
	if err != nil {
		return nil, err
	}
	targetBefore := cloneRevision(target)

	// Apply the publisher mutation first so any failure happens before
	// we touch on-disk revision state. This keeps rollback "all or
	// nothing" from the agent's perspective: if publishing fails, the
	// previous active revision stays active and the pointer stays put.
	if err := s.applyRollbackPublish(ctx, target); err != nil {
		return nil, err
	}

	// Now that the publisher reflects the restored skill, demote the
	// previous active revision and update the candidate store / pointer
	// to match. These steps are local writes against the candidate
	// store. If the final pointer update fails, restore both revision
	// statuses so ActivePointer never keeps referencing an archived
	// previous-active revision.
	if currentRevID != "" {
		if err := s.archiveActive(ctx, skillID, currentRevID, target.RevisionID, decidedAt, opts); err != nil {
			return nil, err
		}
	}

	target.Status = RevisionActive
	target.PromotedAt = &decidedAt
	if err := s.store.WriteRevision(ctx, target); err != nil {
		return nil, fmt.Errorf("write restored revision: %w", err)
	}
	if err := s.updateRollbackPointer(ctx, skillID, target); err != nil {
		if restoreErr := s.restoreRollbackRevisions(ctx, currentBefore, targetBefore); restoreErr != nil {
			return nil, errors.Join(err, fmt.Errorf("restore rollback revisions: %w", restoreErr))
		}
		return nil, err
	}

	_ = s.store.AppendAudit(ctx, AuditEvent{
		At:         decidedAt,
		Action:     AuditActionPromote,
		SkillID:    skillID,
		RevisionID: target.RevisionID,
		Status:     string(RevisionActive),
		Reason:     rollbackReason(currentRevID, opts),
		Actor:      opts.Reviewer,
		Comment:    opts.Comment,
	})

	return &RollbackResult{
		PreviousActiveID: currentRevID,
		RestoredID:       target.RevisionID,
	}, nil
}

func (s *ApprovalService) updateRollbackPointer(ctx context.Context, skillID string, target *Revision) error {
	if target.Action == RevisionActionDelete {
		if err := s.pointer.Clear(ctx, skillID); err != nil {
			return fmt.Errorf("clear active pointer: %w", err)
		}
		return nil
	}
	if err := s.pointer.Set(ctx, skillID, target.RevisionID); err != nil {
		return fmt.Errorf("set active pointer: %w", err)
	}
	return nil
}

func (s *ApprovalService) readRevisionForRollbackRestore(
	ctx context.Context, skillID, revisionID string,
) (*Revision, error) {
	if strings.TrimSpace(revisionID) == "" {
		return nil, nil
	}
	rev, err := s.store.ReadRevision(ctx, skillID, revisionID)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read rollback restore revision %q: %w", revisionID, err)
	}
	return cloneRevision(rev), nil
}

func (s *ApprovalService) restoreRollbackRevisions(ctx context.Context, revs ...*Revision) error {
	var errs []error
	for _, rev := range revs {
		if rev == nil {
			continue
		}
		if err := s.store.WriteRevision(ctx, rev); err != nil {
			errs = append(errs, fmt.Errorf("restore revision %q: %w", rev.RevisionID, err))
		}
	}
	return errors.Join(errs...)
}

func cloneRevision(rev *Revision) *Revision {
	if rev == nil {
		return nil
	}
	cp := *rev
	if rev.Spec != nil {
		spec := *rev.Spec
		spec.Steps = append([]string(nil), rev.Spec.Steps...)
		spec.Pitfalls = append([]string(nil), rev.Spec.Pitfalls...)
		cp.Spec = &spec
	}
	if rev.PromotedAt != nil {
		promotedAt := *rev.PromotedAt
		cp.PromotedAt = &promotedAt
	}
	if rev.SpecReport != nil {
		report := *rev.SpecReport
		report.Reasons = append([]string(nil), rev.SpecReport.Reasons...)
		cp.SpecReport = &report
	}
	if rev.SafetyReport != nil {
		report := *rev.SafetyReport
		report.Reasons = append([]string(nil), rev.SafetyReport.Reasons...)
		cp.SafetyReport = &report
	}
	if rev.EffectivenessReport != nil {
		report := *rev.EffectivenessReport
		report.Reasons = append([]string(nil), rev.EffectivenessReport.Reasons...)
		cp.EffectivenessReport = &report
	}
	if rev.HumanReport != nil {
		report := *rev.HumanReport
		if rev.HumanReport.Approved != nil {
			approved := *rev.HumanReport.Approved
			report.Approved = &approved
		}
		if rev.HumanReport.DecidedAt != nil {
			decidedAt := *rev.HumanReport.DecidedAt
			report.DecidedAt = &decidedAt
		}
		report.Reasons = append([]string(nil), rev.HumanReport.Reasons...)
		cp.HumanReport = &report
	}
	cp.Evidence = cloneRevisionEvidence(rev.Evidence)
	return &cp
}

// currentActiveRevisionID resolves the revision that should be archived
// before a rollback target is promoted. Normally ActivePointer carries
// that identity; delete revisions are the exception because they are
// active tombstones while the pointer is cleared.
func (s *ApprovalService) currentActiveRevisionID(ctx context.Context, skillID string) (string, error) {
	currentRevID, err := s.pointer.Get(ctx, skillID)
	if err != nil {
		return "", fmt.Errorf("get active pointer: %w", err)
	}
	if strings.TrimSpace(currentRevID) != "" {
		return currentRevID, nil
	}
	activeRevID, err := s.findLatestActiveRevisionID(ctx, skillID)
	if err != nil {
		return "", err
	}
	return activeRevID, nil
}

func (s *ApprovalService) findLatestActiveRevisionID(ctx context.Context, skillID string) (string, error) {
	revIDs, err := s.store.ListRevisions(ctx, skillID)
	if err != nil {
		return "", fmt.Errorf("list revisions: %w", err)
	}
	for i := len(revIDs) - 1; i >= 0; i-- {
		rev, err := s.store.ReadRevision(ctx, skillID, revIDs[i])
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("read revision %q: %w", revIDs[i], err)
		}
		if rev.Status == RevisionActive {
			return rev.RevisionID, nil
		}
	}
	return "", nil
}

func validateRollbackTarget(target *Revision) error {
	if target == nil {
		return fmt.Errorf("rollback: nil target revision")
	}
	if target.Action != RevisionActionDelete && target.Spec == nil {
		return fmt.Errorf("rollback: target revision %q has no skill spec", target.RevisionID)
	}
	return nil
}

// applyRollbackPublish materializes the rollback target through the
// publisher: UpsertSkill for create/update revisions, DeleteSkill for
// delete revisions. A delete revision rollback removes the live skill
// body — the inverse of the original delete revision being archived.
func (s *ApprovalService) applyRollbackPublish(ctx context.Context, target *Revision) error {
	if s.publisher == nil {
		return nil
	}
	if target.Action == RevisionActionDelete {
		name := revisionTargetName(target)
		if err := s.publisher.DeleteSkill(ctx, name); err != nil {
			return fmt.Errorf("delete restored skill: %w", err)
		}
		return nil
	}
	if target.Spec == nil {
		return nil
	}
	if err := s.publisher.UpsertSkill(ctx, target.Spec); err != nil {
		return fmt.Errorf("publish restored skill: %w", err)
	}
	return nil
}

// selectRollbackTarget picks the revision that Rollback should promote
// back to active. When targetID is set it must point to an archived
// revision; otherwise the latest archived revision in store order wins.
//
// Missing or wrong-status explicit targets are wrapped in
// ErrNoArchivedRevision so callers can use errors.Is uniformly.
// Other store errors bubble up so corruption / permissions / context
// cancellation cannot be silently masked as "no rollback available".
func (s *ApprovalService) selectRollbackTarget(
	ctx context.Context, skillID, targetID string,
) (*Revision, error) {
	if strings.TrimSpace(targetID) != "" {
		rev, err := s.store.ReadRevision(ctx, skillID, targetID)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil, fmt.Errorf("%w: target revision %q", ErrNoArchivedRevision, targetID)
			}
			return nil, fmt.Errorf("read target revision: %w", err)
		}
		if rev.Status != RevisionArchived {
			return nil, fmt.Errorf("%w: revision %q has status %q",
				ErrNoArchivedRevision, targetID, rev.Status)
		}
		return rev, nil
	}
	revIDs, err := s.store.ListRevisions(ctx, skillID)
	if err != nil {
		return nil, fmt.Errorf("list revisions: %w", err)
	}
	// Walk in reverse so the latest archived revision in store order wins.
	for i := len(revIDs) - 1; i >= 0; i-- {
		rev, err := s.store.ReadRevision(ctx, skillID, revIDs[i])
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return nil, fmt.Errorf("read revision %q: %w", revIDs[i], err)
		}
		if rev.Status == RevisionArchived {
			return rev, nil
		}
	}
	return nil, ErrNoArchivedRevision
}

// archiveActive demotes the current active revision to archived and
// records one audit entry that mirrors the rollback metadata
// (timestamp, reviewer, comment) so operators can correlate the
// archive with the corresponding promote.
func (s *ApprovalService) archiveActive(
	ctx context.Context, skillID, activeID, replacingID string,
	at time.Time, opts RollbackOpts,
) error {
	current, err := s.store.ReadRevision(ctx, skillID, activeID)
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
	if err := s.store.WriteRevision(ctx, current); err != nil {
		return fmt.Errorf("archive active revision: %w", err)
	}
	_ = s.store.AppendAudit(ctx, AuditEvent{
		At:         at,
		Action:     AuditActionArchive,
		SkillID:    skillID,
		RevisionID: activeID,
		Status:     string(RevisionArchived),
		Reason:     "rolled back to " + replacingID,
		Actor:      opts.Reviewer,
		Comment:    opts.Comment,
	})
	return nil
}

func rollbackReason(previousActiveID string, opts RollbackOpts) string {
	if opts.Comment != "" {
		return opts.Comment
	}
	if previousActiveID != "" {
		return "rollback from " + previousActiveID
	}
	return "rollback"
}
