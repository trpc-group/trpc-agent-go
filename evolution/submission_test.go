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

func (p *submissionPublisher) UpsertSkill(context.Context, *SkillSpec) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.upserts++
	return nil
}

func (*submissionPublisher) DeleteSkill(context.Context, string) error { return nil }

type serviceWithoutSubmission struct{}

func (serviceWithoutSubmission) EnqueueLearningJob(context.Context, LearningJob) error {
	return nil
}

func (serviceWithoutSubmission) Close() error { return nil }

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
	svc := NewService(nil,
		WithCandidateStore(store),
		WithPublisher(publisher),
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
	rev, err := SubmitRevision(ctx, svc, RevisionRequest{
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

	rev, err := SubmitRevision(ctx, svc, RevisionRequest{
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

func TestSubmitRevisionRequiresOptionalCapabilityAndStore(t *testing.T) {
	_, err := SubmitRevision(context.Background(), nil, RevisionRequest{})
	require.ErrorContains(t, err, "nil service")

	_, err = SubmitRevision(context.Background(), serviceWithoutSubmission{}, RevisionRequest{})
	require.ErrorContains(t, err, "does not support submissions")

	svc := NewService(nil)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	_, err = SubmitRevision(context.Background(), svc, RevisionRequest{
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
	_, err := SubmitRevision(context.Background(), &service{}, RevisionRequest{})
	require.ErrorContains(t, err, "service is not initialized")

	svc := NewService(nil)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	_, err = SubmitRevision(context.Background(), svc, RevisionRequest{})
	require.ErrorContains(t, err, "nil skill spec")
	_, err = SubmitRevision(context.Background(), svc, RevisionRequest{
		Action: RevisionActionDelete,
		Spec: &SkillSpec{
			Name:        "Unsupported Action",
			Description: "Request validation.",
			WhenToUse:   "Testing.",
			Steps:       []string{"First.", "Second."},
		},
	})
	require.ErrorContains(t, err, "unsupported action")
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

func TestSubmitRevisionRequiresScopeForScopedService(t *testing.T) {
	store := NewFileCandidateStore(filepath.Join(t.TempDir(), "candidates"))
	svc := NewService(nil,
		WithCandidateStore(store),
		WithSkillScopeMode(skill.SkillScopeApp),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	_, err := SubmitRevision(context.Background(), svc, RevisionRequest{
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
	svc := NewService(nil, WithCandidateStore(store))
	t.Cleanup(func() { require.NoError(t, svc.Close()) })
	spec := &SkillSpec{
		Name:        "Lineage Skill",
		Description: "Valid candidate body.",
		WhenToUse:   "Testing lineage.",
		Steps:       []string{"First.", "Second."},
	}

	_, err := SubmitRevision(context.Background(), svc, RevisionRequest{
		Action:   RevisionActionUpdate,
		ParentID: "missing-parent",
		Spec:     spec,
	})
	require.ErrorContains(t, err, "does not exist")

	_, err = SubmitRevision(context.Background(), svc, RevisionRequest{
		Action:   RevisionActionCreate,
		ParentID: "unexpected-parent",
		Spec:     spec,
	})
	require.ErrorContains(t, err, "must not have a parent")
}
