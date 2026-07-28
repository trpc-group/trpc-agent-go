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
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

type submissionPublisher struct {
	mu      sync.Mutex
	upserts int
}

type auditFailingCandidateStore struct {
	CandidateStore
}

func (auditFailingCandidateStore) AppendAudit(context.Context, AuditEvent) error {
	return errors.New("audit unavailable")
}

func (p *submissionPublisher) UpsertSkill(context.Context, *SkillSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.upserts++
	return nil
}

func (*submissionPublisher) DeleteSkill(context.Context, string) error { return nil }

func revisionSubmitterForTest(t *testing.T, svc Service) RevisionSubmitter {
	t.Helper()
	submitter, ok := svc.(RevisionSubmitter)
	require.True(t, ok)
	return submitter
}

func TestSubmitRevisionHoldsEvaluatedCandidateForApproval(t *testing.T) {
	ctx := context.Background()
	store := NewFileCandidateStore(filepath.Join(t.TempDir(), "candidates"))
	require.NoError(t, store.WriteRevision(ctx, &Revision{
		SkillID:    "evaluated-skill",
		RevisionID: "rev-parent",
		Source:     "test",
		Action:     RevisionActionUpdate,
		Status:     RevisionActive,
		Spec: &SkillSpec{
			Name:        "Evaluated Skill",
			Description: "Baseline workflow.",
			WhenToUse:   "Testing.",
			Steps:       []string{"First.", "Second."},
		},
	}))
	publisher := &submissionPublisher{}
	repo := &mockSkillRepo{bodies: map[string]string{"Evaluated Skill": "body"}}
	svc := NewService(nil,
		WithCandidateStore(store),
		WithPublisher(publisher),
		WithSkillRepository(repo),
		WithSpecGate(NewDefaultSpecGate()),
		WithSafetyGate(NewDefaultSafetyGate()),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	evidence := &RevisionEvidence{
		ExperimentID:   "experiment-1",
		DatasetID:      "dataset-1",
		DatasetVersion: "v1",
		BaselineScore:  0.6,
		CandidateScore: 0.8,
		Delta:          0.2,
		CaseCount:      10,
		Objectives:     map[string]float64{"correctness": 0.9},
	}
	rev, err := revisionSubmitterForTest(t, svc).SubmitRevision(ctx, RevisionRequest{
		Source:   "genetic-pareto:experiment-1",
		Action:   RevisionActionUpdate,
		ParentID: "rev-parent",
		Spec: &SkillSpec{
			Name:        "Evaluated Skill",
			Description: "An evaluated reusable workflow.",
			WhenToUse:   "Use for deterministic benchmark tasks.",
			Steps:       []string{"Prepare the input.", "Validate the output."},
		},
		Evidence: evidence,
	})
	require.NoError(t, err)
	require.NotNil(t, rev)
	assert.Equal(t, RevisionPendingApproval, rev.Status)
	assert.Equal(t, "rev-parent", rev.ParentID)
	assert.NotNil(t, rev.HumanReport)
	assert.True(t, rev.HumanReport.Held)
	assert.Equal(t, evidence, rev.Evidence)

	evidence.Objectives["correctness"] = 0
	assert.Equal(t, 0.9, rev.Evidence.Objectives["correctness"])
	cloned := cloneRevision(rev)
	cloned.Evidence.Objectives["correctness"] = 0.1
	assert.Equal(t, 0.9, rev.Evidence.Objectives["correctness"])
	publisher.mu.Lock()
	assert.Zero(t, publisher.upserts, "external submission must not update the live publisher")
	publisher.mu.Unlock()

	stored, err := store.ReadRevision(ctx, rev.SkillID, rev.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionPendingApproval, stored.Status)
}

func TestSubmitRevisionPersistsAutomaticGateRejection(t *testing.T) {
	ctx := context.Background()
	store := NewFileCandidateStore(filepath.Join(t.TempDir(), "candidates"))
	svc := NewService(nil,
		WithCandidateStore(store),
		WithSpecGate(NewDefaultSpecGate()),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	rev, err := revisionSubmitterForTest(t, svc).SubmitRevision(ctx, RevisionRequest{
		Action: RevisionActionCreate,
		Spec: &SkillSpec{
			Name:        "Invalid Candidate",
			Description: "Only one step.",
			WhenToUse:   "Never.",
			Steps:       []string{"One step."},
		},
	})
	require.NoError(t, err)
	assert.Equal(t, RevisionRejected, rev.Status)
	require.NotNil(t, rev.SpecReport)
	assert.False(t, rev.SpecReport.Passed)

	stored, err := store.ReadRevision(ctx, rev.SkillID, rev.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionRejected, stored.Status)
}

func TestSubmitRevisionRequiresStore(t *testing.T) {
	svc := NewService(nil)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	_, err := revisionSubmitterForTest(t, svc).SubmitRevision(context.Background(), RevisionRequest{
		Spec: &SkillSpec{
			Name:        "No Store",
			Description: "No candidate store configured.",
			WhenToUse:   "Testing.",
			Steps:       []string{"First.", "Second."},
		},
	})
	require.ErrorContains(t, err, "candidate store is required")
}

func TestSubmitRevisionValidatesRequestAndServiceState(t *testing.T) {
	_, err := (&service{}).SubmitRevision(context.Background(), RevisionRequest{})
	require.ErrorContains(t, err, "service is not initialized")

	svc := NewService(nil)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	submitter := revisionSubmitterForTest(t, svc)
	_, err = submitter.SubmitRevision(context.Background(), RevisionRequest{})
	require.ErrorContains(t, err, "nil skill spec")
	_, err = submitter.SubmitRevision(context.Background(), RevisionRequest{
		Action: RevisionActionDelete,
		Spec: &SkillSpec{
			Name:        "Unsupported Action",
			Description: "Request validation.",
			WhenToUse:   "Testing.",
			Steps:       []string{"First.", "Second."},
		},
	})
	require.ErrorContains(t, err, "unsupported action")

	valid := &SkillSpec{
		Name:        "Valid",
		Description: "Valid description.",
		WhenToUse:   "Testing.",
		Steps:       []string{"Do the work."},
	}
	tests := []struct {
		name    string
		mutate  func(*SkillSpec)
		message string
	}{
		{"name", func(spec *SkillSpec) { spec.Name = " " }, "name is required"},
		{"description", func(spec *SkillSpec) { spec.Description = "" }, "description is required"},
		{"when to use", func(spec *SkillSpec) { spec.WhenToUse = "" }, "when_to_use is required"},
		{"steps", func(spec *SkillSpec) { spec.Steps = nil }, "at least one step"},
		{"empty step", func(spec *SkillSpec) { spec.Steps = []string{" "} }, "step 0 is empty"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := cloneSkillSpec(valid)
			test.mutate(spec)
			_, submitErr := submitter.SubmitRevision(context.Background(), RevisionRequest{Spec: spec})
			require.ErrorContains(t, submitErr, test.message)
		})
	}
}

func TestSubmissionCloneAndEvidenceNilPaths(t *testing.T) {
	assert.Nil(t, cloneSkillSpec(nil))
	assert.Nil(t, cloneRevisionEvidence(nil))
	assert.Nil(t, outcomeFromEvidence(nil))
	assert.Nil(t, outcomeFromEvidence(&RevisionEvidence{}))

	spec := &SkillSpec{
		Name:        "Clone",
		Description: "Clone slices.",
		WhenToUse:   "Testing.",
		Steps:       []string{"First.", "Second."},
		Pitfalls:    []string{"Avoid aliases."},
	}
	cloned := cloneSkillSpec(spec)
	cloned.Steps[0] = "Changed."
	cloned.Pitfalls[0] = "Changed."
	assert.Equal(t, "First.", spec.Steps[0])
	assert.Equal(t, "Avoid aliases.", spec.Pitfalls[0])
}

func TestRevisionEvidencePreservesZeroScoresInJSON(t *testing.T) {
	payload, err := json.Marshal(&RevisionEvidence{})
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"baseline_score": 0,
		"candidate_score": 0,
		"delta": 0
	}`, string(payload))
}

func TestRevisionEvidenceValidation(t *testing.T) {
	require.NoError(t, validateRevisionEvidence(nil))
	require.NoError(t, validateRevisionEvidence(&RevisionEvidence{
		BaselineScore:  0.2,
		CandidateScore: 0.8,
		Delta:          0.6,
		CaseCount:      10,
		Objectives:     map[string]float64{"duration": 2.5},
	}))
	tests := []struct {
		name     string
		evidence *RevisionEvidence
		message  string
	}{
		{"baseline", &RevisionEvidence{BaselineScore: math.NaN()}, "baseline score"},
		{"candidate", &RevisionEvidence{CandidateScore: 2}, "candidate score"},
		{"delta", &RevisionEvidence{Delta: math.Inf(1)}, "delta"},
		{"inconsistent delta", &RevisionEvidence{
			BaselineScore: 0.9, CandidateScore: 0.1, Delta: 0.8,
		}, "candidate score minus baseline score"},
		{"cases", &RevisionEvidence{CaseCount: -1}, "case count"},
		{"objective", &RevisionEvidence{Objectives: map[string]float64{"cost": math.NaN()}}, "objective"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.ErrorContains(t, validateRevisionEvidence(test.evidence), test.message)
		})
	}
}

func TestSubmitRevisionTreatsAuditFailureAsCommitted(t *testing.T) {
	ctx := context.Background()
	baseStore := NewFileCandidateStore(filepath.Join(t.TempDir(), "candidates"))
	store := auditFailingCandidateStore{CandidateStore: baseStore}
	svc := NewService(nil, WithCandidateStore(store))
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	rev, err := revisionSubmitterForTest(t, svc).SubmitRevision(ctx, RevisionRequest{
		Action: RevisionActionCreate,
		Spec: &SkillSpec{
			Name:        "Audit Failure",
			Description: "A structurally valid candidate.",
			WhenToUse:   "Testing audit persistence.",
			Steps:       []string{"Submit the candidate."},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, rev)
	assert.Equal(t, RevisionPendingApproval, rev.Status)

	stored, err := baseStore.ReadRevision(ctx, rev.SkillID, rev.RevisionID)
	require.NoError(t, err)
	assert.Equal(t, RevisionPendingApproval, stored.Status)
}

func TestSubmitRevisionRequiresScopeForScopedService(t *testing.T) {
	store := NewFileCandidateStore(filepath.Join(t.TempDir(), "candidates"))
	svc := NewService(nil,
		WithCandidateStore(store),
		WithSkillScopeMode(skill.SkillScopeApp),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	_, err := revisionSubmitterForTest(t, svc).SubmitRevision(context.Background(), RevisionRequest{
		Spec: &SkillSpec{
			Name:        "Scoped Skill",
			Description: "Requires an app scope.",
			WhenToUse:   "Testing.",
			Steps:       []string{"First.", "Second."},
		},
	})
	require.ErrorContains(t, err, "scope is required")
}

func TestWorkerUpdateUsesActiveRevisionAsParent(t *testing.T) {
	ctx := context.Background()
	pointer := NewFileActivePointer(t.TempDir())
	worker := newWorker(workerConfig{ActivePointer: pointer})
	spec := &SkillSpec{
		Name:        "Lineage Skill",
		Description: "Tracks the active parent revision.",
		WhenToUse:   "Testing lineage.",
		Steps:       []string{"First.", "Second."},
	}
	require.NoError(t, pointer.Set(ctx, skillIDFromName(spec.Name), "rev-active"))

	rev := worker.buildRevision(spec, RevisionActionUpdate)
	worker.populateParentRevisionID(ctx, rev, skill.SkillScope{}, false)
	assert.Equal(t, "rev-active", rev.ParentID)
}

func TestValidateCurrentParentTreatsEmptyParentAsNoActiveRevision(t *testing.T) {
	ctx := context.Background()
	pointer := NewFileActivePointer(t.TempDir())
	rev := &Revision{
		SkillID: "lineage-skill",
		Action:  RevisionActionUpdate,
	}
	require.NoError(t, validateCurrentParent(ctx, pointer, rev))

	require.NoError(t, pointer.Set(ctx, rev.SkillID, "rev-active"))
	err := validateCurrentParent(ctx, pointer, rev)
	require.ErrorIs(t, err, ErrStaleRevisionParent)
}

func TestPopulateParentRevisionIDSkipsInapplicableRevisions(t *testing.T) {
	worker := newWorker(workerConfig{})
	worker.populateParentRevisionID(context.Background(), nil, skill.SkillScope{}, false)
	create := worker.buildRevision(&SkillSpec{Name: "Create"}, RevisionActionCreate)
	worker.populateParentRevisionID(context.Background(), create, skill.SkillScope{}, false)
	assert.Empty(t, create.ParentID)
	update := worker.buildRevision(&SkillSpec{Name: "Update"}, RevisionActionUpdate)
	update.ParentID = "already-set"
	worker.populateParentRevisionID(context.Background(), update, skill.SkillScope{}, false)
	assert.Equal(t, "already-set", update.ParentID)
}

func TestSubmitRevisionRejectsInvalidLineage(t *testing.T) {
	store := NewFileCandidateStore(filepath.Join(t.TempDir(), "candidates"))
	spec := &SkillSpec{
		Name:        "Lineage Skill",
		Description: "Valid candidate body.",
		WhenToUse:   "Testing lineage.",
		Steps:       []string{"First.", "Second."},
	}
	svc := NewService(nil,
		WithCandidateStore(store),
		WithSkillRepository(&mockSkillRepo{
			bodies: map[string]string{spec.Name: "body"},
		}),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	submitter := revisionSubmitterForTest(t, svc)
	_, err := submitter.SubmitRevision(context.Background(), RevisionRequest{
		Action:   RevisionActionUpdate,
		ParentID: "missing-parent",
		Spec:     spec,
	})
	require.ErrorContains(t, err, "does not exist")

	_, err = submitter.SubmitRevision(context.Background(), RevisionRequest{
		Action:   RevisionActionCreate,
		ParentID: "unexpected-parent",
		Spec:     spec,
	})
	require.ErrorContains(t, err, "must not have a parent")
}

func TestSubmitRevisionRejectsStaleParent(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store := NewFileCandidateStore(filepath.Join(root, "candidates"))
	pointer := NewFileActivePointer(filepath.Join(root, "pointers"))
	spec := &SkillSpec{
		Name:        "Lineage Skill",
		Description: "Valid candidate body.",
		WhenToUse:   "Testing lineage.",
		Steps:       []string{"First.", "Second."},
	}
	skillID := skillIDFromName(spec.Name)
	for _, revision := range []*Revision{
		{RevisionID: "rev-a", Status: RevisionArchived},
		{RevisionID: "rev-b", Status: RevisionActive},
	} {
		require.NoError(t, store.WriteRevision(ctx, &Revision{
			SkillID:    skillID,
			RevisionID: revision.RevisionID,
			Action:     RevisionActionUpdate,
			Status:     revision.Status,
			Spec:       cloneSkillSpec(spec),
		}))
	}
	require.NoError(t, pointer.Set(ctx, skillID, "rev-b"))
	svc := NewService(nil,
		WithCandidateStore(store),
		WithActivePointer(pointer),
		WithSkillRepository(&mockSkillRepo{
			bodies: map[string]string{spec.Name: "body"},
		}),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	rev, err := revisionSubmitterForTest(t, svc).SubmitRevision(ctx, RevisionRequest{
		Action:   RevisionActionUpdate,
		ParentID: "rev-a",
		Spec:     spec,
	})
	require.ErrorContains(t, err, "parent revision \"rev-a\" is stale")
	require.ErrorIs(t, err, ErrStaleRevisionParent)
	assert.Nil(t, rev)
	revisionIDs, listErr := store.ListRevisions(ctx, skillID)
	require.NoError(t, listErr)
	assert.ElementsMatch(t, []string{"rev-a", "rev-b"}, revisionIDs)
}

func TestSubmitRevisionReturnsActivePointerReadError(t *testing.T) {
	store := NewFileCandidateStore(filepath.Join(t.TempDir(), "candidates"))
	name := "Pointer Failure"
	svc := NewService(nil,
		WithCandidateStore(store),
		WithActivePointer(errActivePointer{err: errors.New("pointer unavailable")}),
		WithSkillRepository(&mockSkillRepo{
			bodies: map[string]string{name: "body"},
		}),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	rev, err := revisionSubmitterForTest(t, svc).SubmitRevision(
		context.Background(),
		RevisionRequest{Spec: &SkillSpec{
			Name:        name,
			Description: "Valid candidate body.",
			WhenToUse:   "Testing pointer errors.",
			Steps:       []string{"First.", "Second."},
		}},
	)
	require.ErrorContains(t, err, "pointer unavailable")
	assert.Nil(t, rev)
}

func TestSubmitRevisionEnforcesManagedSkillBoundary(t *testing.T) {
	root := t.TempDir()
	managedDir := filepath.Join(root, "managed")
	store := NewFileCandidateStore(filepath.Join(root, "candidates"))
	repo := &mockSkillRepo{
		bodies: map[string]string{
			"Protected Skill": "protected",
			"Managed Skill":   "managed",
		},
		paths: map[string]string{
			"Protected Skill": filepath.Join(root, "bundled", "protected-skill"),
			"Managed Skill":   filepath.Join(managedDir, "managed-skill"),
		},
	}
	svc := NewService(nil,
		WithManagedSkillsDir(managedDir),
		WithSkillRepository(repo),
		WithCandidateStore(store),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	submitter := revisionSubmitterForTest(t, svc)
	request := func(name string, action RevisionAction) RevisionRequest {
		return RevisionRequest{
			Action: action,
			Spec: &SkillSpec{
				Name:        name,
				Description: "A valid candidate.",
				WhenToUse:   "Testing submission isolation.",
				Steps:       []string{"Run the workflow."},
			},
		}
	}

	_, err := submitter.SubmitRevision(
		context.Background(), request("Protected Skill", RevisionActionUpdate),
	)
	require.ErrorContains(t, err, "not evolution-managed")
	_, err = submitter.SubmitRevision(
		context.Background(), request("Missing Skill", RevisionActionUpdate),
	)
	require.ErrorContains(t, err, "does not exist in skill repository")
	_, err = submitter.SubmitRevision(
		context.Background(), request("Protected Skill", RevisionActionCreate),
	)
	require.ErrorContains(t, err, "already exists")

	rev, err := submitter.SubmitRevision(
		context.Background(), request("Managed Skill", RevisionActionUpdate),
	)
	require.NoError(t, err)
	assert.Equal(t, RevisionPendingApproval, rev.Status)
}
