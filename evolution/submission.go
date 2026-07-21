//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evolution

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

const revisionEvidenceDeltaTolerance = 1e-9

// RevisionRequest describes an evaluated skill candidate that should enter
// the revision governance pipeline. External submissions are persisted for
// approval and are never promoted directly to the live skill repository. Spec
// must contain a non-empty name, description, when-to-use condition, and at
// least one non-empty step.
type RevisionRequest struct {
	Scope    skill.SkillScope
	Source   string
	Action   RevisionAction
	ParentID string
	Spec     *SkillSpec
	Evidence *RevisionEvidence
}

// RevisionSubmitter accepts externally evaluated skill candidates and sends
// them through revision validation, quality gates, and approval. It is kept
// separate from Service because session learning and revision submission are
// independent capabilities.
type RevisionSubmitter interface {
	SubmitRevision(context.Context, RevisionRequest) (*Revision, error)
}

var _ RevisionSubmitter = (*service)(nil)

func (s *service) SubmitRevision(
	ctx context.Context,
	req RevisionRequest,
) (*Revision, error) {
	if s == nil || s.worker == nil {
		return nil, errors.New("evolution: submit revision: service is not initialized")
	}
	return s.worker.submitRevision(ctx, req)
}

func (w *worker) submitRevision(
	ctx context.Context,
	req RevisionRequest,
) (*Revision, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	action, err := validateRevisionRequest(req)
	if err != nil {
		return nil, err
	}
	scope, scoped, store, repo, err := w.submissionResources(ctx, req.Scope)
	if err != nil {
		return nil, err
	}

	rev := newSubmittedRevision(req, action)
	if err := w.validateSubmissionParent(ctx, rev, scope, scoped, store); err != nil {
		return nil, err
	}

	w.bumpGateMetric(func(m *approvalGateCounters) { m.CandidatesSeen++ })
	existing := loadExistingSkills(repo, w.existingSkillBodyMaxChars)
	if !w.runAutomaticGates(ctx, rev, existing, outcomeFromEvidence(rev.Evidence)) {
		return w.rejectSubmittedRevision(ctx, rev, store)
	}
	return w.holdSubmittedRevision(ctx, rev, store)
}

func validateRevisionRequest(req RevisionRequest) (RevisionAction, error) {
	if req.Spec == nil {
		return "", errors.New("evolution: submit revision: nil skill spec")
	}
	if err := validateSubmittedSkillSpec(req.Spec); err != nil {
		return "", fmt.Errorf("evolution: submit revision: invalid skill spec: %w", err)
	}
	action := req.Action
	if action == "" {
		action = RevisionActionUpdate
	}
	if action != RevisionActionCreate && action != RevisionActionUpdate {
		return "", fmt.Errorf("evolution: submit revision: unsupported action %q", action)
	}
	if action == RevisionActionCreate && strings.TrimSpace(req.ParentID) != "" {
		return "", errors.New("evolution: submit revision: create revision must not have a parent")
	}
	if err := validateRevisionEvidence(req.Evidence); err != nil {
		return "", fmt.Errorf("evolution: submit revision: invalid evidence: %w", err)
	}
	return action, nil
}

func validateRevisionEvidence(evidence *RevisionEvidence) error {
	if evidence == nil {
		return nil
	}
	for name, score := range map[string]float64{
		"baseline score":  evidence.BaselineScore,
		"candidate score": evidence.CandidateScore,
	} {
		if math.IsNaN(score) || math.IsInf(score, 0) || score < 0 || score > 1 {
			return fmt.Errorf("%s must be finite and within [0, 1]", name)
		}
	}
	if math.IsNaN(evidence.Delta) || math.IsInf(evidence.Delta, 0) ||
		evidence.Delta < -1 || evidence.Delta > 1 {
		return errors.New("delta must be finite and within [-1, 1]")
	}
	expectedDelta := evidence.CandidateScore - evidence.BaselineScore
	if math.Abs(evidence.Delta-expectedDelta) > revisionEvidenceDeltaTolerance {
		return fmt.Errorf(
			"delta must equal candidate score minus baseline score: got %.17g, want %.17g",
			evidence.Delta,
			expectedDelta,
		)
	}
	if evidence.CaseCount < 0 {
		return errors.New("case count must not be negative")
	}
	for name, value := range evidence.Objectives {
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return fmt.Errorf("objective %q must be finite", name)
		}
	}
	return nil
}

func validateSubmittedSkillSpec(spec *SkillSpec) error {
	switch {
	case strings.TrimSpace(spec.Name) == "":
		return errors.New("name is required")
	case strings.TrimSpace(spec.Description) == "":
		return errors.New("description is required")
	case strings.TrimSpace(spec.WhenToUse) == "":
		return errors.New("when_to_use is required")
	case len(spec.Steps) == 0:
		return errors.New("at least one step is required")
	}
	for index, step := range spec.Steps {
		if strings.TrimSpace(step) == "" {
			return fmt.Errorf("step %d is empty", index)
		}
	}
	return nil
}

func (w *worker) submissionResources(
	ctx context.Context,
	requestedScope skill.SkillScope,
) (skill.SkillScope, bool, CandidateStore, skill.Repository, error) {
	scope, scoped, err := w.resolveSubmissionScope(requestedScope)
	if err != nil {
		return skill.SkillScope{}, false, nil, nil, fmt.Errorf("evolution: submit revision: %w", err)
	}
	store, err := w.candidateStoreForScope(scope, scoped)
	if err != nil {
		return skill.SkillScope{}, false, nil, nil, fmt.Errorf(
			"evolution: submit revision: resolve candidate store: %w", err,
		)
	}
	if store == nil {
		return skill.SkillScope{}, false, nil, nil, errors.New(
			"evolution: submit revision: candidate store is required",
		)
	}
	repo, err := w.repositoryForScope(ctx, scope, scoped)
	if err != nil {
		return skill.SkillScope{}, false, nil, nil, fmt.Errorf(
			"evolution: submit revision: resolve skill repository: %w", err,
		)
	}
	return scope, scoped, store, repo, nil
}

func newSubmittedRevision(req RevisionRequest, action RevisionAction) *Revision {
	spec := cloneSkillSpec(req.Spec)
	source := strings.TrimSpace(req.Source)
	if source == "" {
		source = "external-evaluation"
	}
	return &Revision{
		SkillID:    skillIDFromName(spec.Name),
		RevisionID: newRevisionID(),
		ParentID:   strings.TrimSpace(req.ParentID),
		TargetName: spec.Name,
		Source:     source,
		Action:     action,
		Spec:       spec,
		Status:     RevisionPending,
		CreatedAt:  time.Now().UTC(),
		Evidence:   cloneRevisionEvidence(req.Evidence),
	}
}

func (w *worker) validateSubmissionParent(
	ctx context.Context,
	rev *Revision,
	scope skill.SkillScope,
	scoped bool,
	store CandidateStore,
) error {
	if rev.Action != RevisionActionUpdate {
		return nil
	}
	w.populateParentRevisionID(ctx, rev, scope, scoped)
	if rev.ParentID == "" {
		return nil
	}
	if _, err := store.ReadRevision(ctx, rev.SkillID, rev.ParentID); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf(
				"evolution: submit revision: parent revision %q does not exist for skill %q",
				rev.ParentID, rev.SkillID,
			)
		}
		return fmt.Errorf("evolution: submit revision: read parent revision: %w", err)
	}
	return nil
}

func (w *worker) rejectSubmittedRevision(
	ctx context.Context,
	rev *Revision,
	store CandidateStore,
) (*Revision, error) {
	if rev.Status == RevisionPending {
		rev.Status = RevisionRejected
	}
	if err := store.WriteRevision(ctx, rev); err != nil {
		return nil, fmt.Errorf("evolution: submit revision: write rejected revision: %w", err)
	}
	w.bumpGateMetric(func(m *approvalGateCounters) { m.RevisionsWritten++ })
	w.auditReject(ctx, rev, store)
	return rev, nil
}

func (w *worker) holdSubmittedRevision(
	ctx context.Context,
	rev *Revision,
	store CandidateStore,
) (*Revision, error) {

	rev.Status = RevisionPendingApproval
	rev.HumanReport = &HumanReport{
		Held:    true,
		Reasons: []string{"externally evaluated revisions require approval"},
	}
	if err := store.WriteRevision(ctx, rev); err != nil {
		return nil, fmt.Errorf("evolution: submit revision: write pending revision: %w", err)
	}
	w.bumpGateMetric(func(m *approvalGateCounters) {
		m.RevisionsWritten++
		m.HumanGateHeld++
	})
	if err := store.AppendAudit(ctx, AuditEvent{
		Action:     AuditActionSubmit,
		SkillID:    rev.SkillID,
		RevisionID: rev.RevisionID,
		Status:     string(rev.Status),
		Reason:     "external candidate awaiting approval",
	}); err != nil {
		log.WarnfContext(
			ctx,
			"evolution: submit revision: append audit for revision %s failed after persistence: %v",
			rev.RevisionID,
			err,
		)
	}
	return rev, nil
}

func (w *worker) resolveSubmissionScope(
	scope skill.SkillScope,
) (skill.SkillScope, bool, error) {
	if scope.IsZero() {
		if w.skillScopeMode == skill.SkillScopeNone {
			return skill.SkillScope{}, false, nil
		}
		return skill.SkillScope{}, false, errors.New("scope is required by the configured skill scope mode")
	}
	return w.resolveJobScope(LearningJob{Scope: scope})
}

func cloneSkillSpec(spec *SkillSpec) *SkillSpec {
	if spec == nil {
		return nil
	}
	cloned := *spec
	cloned.Steps = append([]string(nil), spec.Steps...)
	cloned.Pitfalls = append([]string(nil), spec.Pitfalls...)
	return &cloned
}

func cloneRevisionEvidence(evidence *RevisionEvidence) *RevisionEvidence {
	if evidence == nil {
		return nil
	}
	cloned := *evidence
	if evidence.Objectives != nil {
		cloned.Objectives = make(map[string]float64, len(evidence.Objectives))
		for name, value := range evidence.Objectives {
			cloned.Objectives[name] = value
		}
	}
	return &cloned
}

func outcomeFromEvidence(evidence *RevisionEvidence) *Outcome {
	if evidence == nil || evidence.CaseCount <= 0 {
		return nil
	}
	score := evidence.CandidateScore
	return &Outcome{
		Status:    OutcomeSuccess,
		Score:     &score,
		Notes:     fmt.Sprintf("paired evaluation delta %.4f across %d cases", evidence.Delta, evidence.CaseCount),
		Evaluator: "evolution-optimization",
	}
}
