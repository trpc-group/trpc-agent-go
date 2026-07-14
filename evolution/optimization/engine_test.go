//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package optimization

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
)

type improvingReflector struct{}

func (improvingReflector) propose(_ context.Context, input reflectionInput) (mutation, error) {
	spec := cloneSpec(input.candidate)
	switch input.component {
	case componentDescription:
		spec.Description = "improved reusable workflow"
	case componentWhenToUse:
		spec.WhenToUse = "improved trigger"
	case componentSteps:
		spec.Steps = append(spec.Steps, "Check the improved result.")
	case componentPitfalls:
		spec.Pitfalls = append(spec.Pitfalls, "Avoid the observed failure.")
	}
	return mutation{spec: spec, rationale: "fix evaluator feedback"}, nil
}

type errorReflector struct{}

func (errorReflector) propose(context.Context, reflectionInput) (mutation, error) {
	return mutation{}, errors.New("reflection unavailable")
}

type duplicateReflector struct{}

func (duplicateReflector) propose(_ context.Context, input reflectionInput) (mutation, error) {
	return mutation{spec: cloneSpec(input.candidate)}, nil
}

type evaluatorCall struct {
	description string
	caseIDs     []string
	seed        int64
}

type scoringEvaluator struct {
	equalScore bool
	calls      []evaluatorCall
}

type stagedEvaluator struct {
	calls  int
	failAt int
	base   scoringEvaluator
}

type recordingRevisionSubmitter struct {
	delegate evolution.RevisionSubmitter
	requests []evolution.RevisionRequest
}

func (s *recordingRevisionSubmitter) SubmitRevision(
	ctx context.Context,
	req evolution.RevisionRequest,
) (*evolution.Revision, error) {
	s.requests = append(s.requests, req)
	return s.delegate.SubmitRevision(ctx, req)
}

func (e *stagedEvaluator) Evaluate(
	ctx context.Context,
	spec *evolution.SkillSpec,
	cases []Case,
	seed int64,
) ([]Evaluation, error) {
	e.calls++
	if e.calls == e.failAt {
		return nil, errors.New("scripted evaluator failure")
	}
	return e.base.Evaluate(ctx, spec, cases, seed)
}

func (e *scoringEvaluator) Evaluate(
	_ context.Context,
	spec *evolution.SkillSpec,
	cases []Case,
	seed int64,
) ([]Evaluation, error) {
	call := evaluatorCall{description: spec.Description, seed: seed}
	results := make([]Evaluation, 0, len(cases))
	for _, item := range cases {
		call.caseIDs = append(call.caseIDs, item.ID)
		score := 0.2
		if !e.equalScore && strings.Contains(spec.Description, "improved") {
			score = 0.8
		}
		results = append(results, Evaluation{
			CaseID:   item.ID,
			Score:    score,
			Output:   "candidate output",
			Feedback: "make the workflow more precise",
			Trace:    "tool execution trace",
			Objectives: map[string]float64{
				"correctness": score,
			},
		})
	}
	e.calls = append(e.calls, call)
	return results, nil
}

func TestOptimizerRunsReflectiveParetoLoopAndRecordsExperiment(t *testing.T) {
	evaluator := &scoringEvaluator{}
	storeDir := t.TempDir()
	opts := defaultOptions()
	opts.maxIterations = 1
	opts.reflectionBatchSize = 2
	opts.storeDir = storeDir
	optimizer := &Optimizer{
		reflector: improvingReflector{},
		evaluator: evaluator,
		opts:      opts,
	}

	result, err := optimizer.Optimize(context.Background(), Request{
		Seed:    testSeedSpec(),
		Dataset: testDataset(2),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Equal(t, "improved reusable workflow", result.Spec.Description)
	assert.Equal(t, 0.2, result.BaselineValidation.Score)
	assert.Equal(t, 0.8, result.CandidateValidation.Score)
	assert.Equal(t, 0.2, result.BaselineHoldout.Score)
	assert.Equal(t, 0.8, result.CandidateHoldout.Score)
	assert.Equal(t, 2, result.CandidateCount)
	assert.Equal(t, 12, result.MetricCalls)
	assert.Equal(t, "max_iterations", result.StopReason)
	assert.True(t, result.PromotionEligible)
	assert.Equal(t, "holdout requirements satisfied", result.PromotionReason)

	require.Len(t, evaluator.calls, 6)
	assert.Equal(t, evaluator.calls[0].seed, evaluator.calls[3].seed,
		"all validation candidates must use the same seed")
	assert.Equal(t, evaluator.calls[1].seed, evaluator.calls[2].seed,
		"parent and child minibatches must be paired")
	assert.Equal(t, evaluator.calls[4].seed, evaluator.calls[5].seed,
		"baseline and candidate holdout must be paired")

	_, err = os.Stat(filepath.Join(storeDir, result.ExperimentID, "experiment.json"))
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(storeDir, result.ExperimentID, "result.json"))
	require.NoError(t, err)
}

func TestOptimizerRejectsCandidateWithoutStrictMinibatchImprovement(t *testing.T) {
	evaluator := &scoringEvaluator{equalScore: true}
	opts := defaultOptions()
	opts.maxIterations = 1
	opts.reflectionBatchSize = 2
	optimizer := &Optimizer{
		reflector: improvingReflector{},
		evaluator: evaluator,
		opts:      opts,
	}

	result, err := optimizer.Optimize(context.Background(), Request{
		Seed: testSeedSpec(),
		Dataset: Dataset{
			ID:         "strict-acceptance",
			Version:    "v1",
			Feedback:   makeCases("feedback", 2),
			Validation: makeCases("validation", 2),
		},
	})
	require.NoError(t, err)
	assert.Equal(t, testSeedSpec(), result.Spec)
	assert.Equal(t, 1, result.CandidateCount)
	assert.Equal(t, 6, result.MetricCalls)
	assert.False(t, result.PromotionEligible)
	assert.Contains(t, result.PromotionReason, "no accepted candidate")
}

func TestOptimizerReservesMetricBudgetForHoldout(t *testing.T) {
	evaluator := &scoringEvaluator{}
	opts := defaultOptions()
	opts.maxIterations = 2
	opts.maxMetricCalls = 11
	opts.reflectionBatchSize = 2
	optimizer := &Optimizer{
		reflector: improvingReflector{},
		evaluator: evaluator,
		opts:      opts,
	}

	result, err := optimizer.Optimize(context.Background(), Request{
		Seed:    testSeedSpec(),
		Dataset: testDataset(2),
	})
	require.NoError(t, err)
	assert.Equal(t, "metric_budget", result.StopReason)
	assert.Equal(t, 1, result.CandidateCount)
	assert.Equal(t, 4, result.MetricCalls)
}

func TestOptimizerSubmitsImprovedCandidateForApproval(t *testing.T) {
	store := evolution.NewFileCandidateStore(filepath.Join(t.TempDir(), "candidates"))
	require.NoError(t, store.WriteRevision(context.Background(), &evolution.Revision{
		SkillID:    "benchmark-skill",
		RevisionID: "rev-parent",
		Source:     "test",
		Action:     evolution.RevisionActionUpdate,
		Status:     evolution.RevisionActive,
		Spec:       testSeedSpec(),
	}))
	svc := evolution.NewService(nil,
		evolution.WithCandidateStore(store),
		evolution.WithSpecGate(evolution.NewDefaultSpecGate()),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	evaluator := &scoringEvaluator{}
	opts := defaultOptions()
	opts.maxIterations = 1
	opts.reflectionBatchSize = 2
	submitter, ok := svc.(evolution.RevisionSubmitter)
	require.True(t, ok)
	recordingSubmitter := &recordingRevisionSubmitter{delegate: submitter}
	WithRevisionSubmitter(recordingSubmitter)(&opts)
	optimizer := &Optimizer{
		reflector: improvingReflector{},
		evaluator: evaluator,
		opts:      opts,
	}
	dataset := testDataset(10)
	dataset.Validation = makeCases("validation", 10)
	dataset.Feedback = makeCases("feedback", 10)

	result, err := optimizer.Optimize(context.Background(), Request{
		Seed:             testSeedSpec(),
		Dataset:          dataset,
		ParentRevisionID: "rev-parent",
		Submit:           true,
	})
	require.NoError(t, err)
	require.NotNil(t, result.Revision)
	assert.True(t, result.PromotionEligible)
	assert.Equal(t, evolution.RevisionPendingApproval, result.Revision.Status)
	assert.Equal(t, "rev-parent", result.Revision.ParentID)
	assert.Equal(t, result.ExperimentID, result.Revision.Evidence.ExperimentID)
	assert.Contains(t, result.SubmissionReason, "pending_approval")
	require.Len(t, recordingSubmitter.requests, 1)
	assert.Equal(t, "rev-parent", recordingSubmitter.requests[0].ParentID)

	stored, err := store.ReadRevision(
		context.Background(), result.Revision.SkillID, result.Revision.RevisionID,
	)
	require.NoError(t, err)
	assert.Equal(t, evolution.RevisionPendingApproval, stored.Status)
}

func TestOptimizerSkipsRecoverableProposalFailures(t *testing.T) {
	tests := []struct {
		name      string
		reflector reflector
		failAt    int
	}{
		{name: "parent evaluation", reflector: improvingReflector{}, failAt: 2},
		{name: "child evaluation", reflector: improvingReflector{}, failAt: 3},
		{name: "validation", reflector: improvingReflector{}, failAt: 4},
		{name: "reflection", reflector: errorReflector{}},
		{name: "duplicate", reflector: duplicateReflector{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluator := &stagedEvaluator{failAt: test.failAt}
			opts := defaultOptions()
			opts.maxIterations = 1
			opts.reflectionBatchSize = 2
			optimizer := &Optimizer{
				reflector: test.reflector,
				evaluator: evaluator,
				opts:      opts,
			}
			result, err := optimizer.Optimize(context.Background(), Request{
				Seed: testSeedSpec(),
				Dataset: Dataset{
					ID:         "failure-dataset",
					Version:    "v1",
					Feedback:   makeCases("feedback", 2),
					Validation: makeCases("validation", 2),
				},
			})
			require.NoError(t, err)
			assert.Equal(t, 1, result.CandidateCount)
		})
	}
}

func TestOptimizerReturnsFatalInitializationAndEvaluationErrors(t *testing.T) {
	_, err := (*Optimizer)(nil).Optimize(context.Background(), Request{})
	require.ErrorContains(t, err, "not initialized")

	opts := defaultOptions()
	optimizer := &Optimizer{reflector: improvingReflector{}, evaluator: &scoringEvaluator{}, opts: opts}
	_, err = optimizer.Optimize(context.Background(), Request{})
	require.ErrorContains(t, err, "seed")

	opts.maxMetricCalls = 1
	optimizer.opts = opts
	_, err = optimizer.Optimize(context.Background(), Request{
		Seed: testSeedSpec(),
		Dataset: Dataset{
			ID:         "budget",
			Version:    "v1",
			Feedback:   makeCases("feedback", 2),
			Validation: makeCases("validation", 2),
		},
	})
	require.ErrorContains(t, err, "cannot cover validation")

	opts = defaultOptions()
	optimizer.opts = opts
	_, err = optimizer.Optimize(context.Background(), Request{
		Seed:   testSeedSpec(),
		Submit: true,
		Dataset: Dataset{
			ID:         "submit",
			Version:    "v1",
			Feedback:   makeCases("feedback", 10),
			Validation: makeCases("validation", 10),
			Holdout:    makeCases("holdout", 10),
		},
	})
	require.ErrorContains(t, err, "without a revision submitter")

	seedFailure := &stagedEvaluator{failAt: 1}
	optimizer = &Optimizer{reflector: improvingReflector{}, evaluator: seedFailure, opts: defaultOptions()}
	_, err = optimizer.Optimize(context.Background(), Request{
		Seed: testSeedSpec(),
		Dataset: Dataset{
			ID:         "seed-failure",
			Version:    "v1",
			Feedback:   makeCases("feedback", 2),
			Validation: makeCases("validation", 2),
		},
	})
	require.ErrorContains(t, err, "evaluate seed validation")
}

func TestOptimizerReturnsHoldoutEvaluationErrors(t *testing.T) {
	t.Run("baseline", func(t *testing.T) {
		evaluator := &stagedEvaluator{failAt: 2}
		opts := defaultOptions()
		opts.maxIterations = 0
		optimizer := &Optimizer{reflector: improvingReflector{}, evaluator: evaluator, opts: opts}
		_, err := optimizer.Optimize(context.Background(), Request{
			Seed:    testSeedSpec(),
			Dataset: testDataset(2),
		})
		require.ErrorContains(t, err, "baseline holdout")
	})
	t.Run("candidate", func(t *testing.T) {
		evaluator := &stagedEvaluator{failAt: 6}
		opts := defaultOptions()
		opts.maxIterations = 1
		opts.reflectionBatchSize = 2
		optimizer := &Optimizer{reflector: improvingReflector{}, evaluator: evaluator, opts: opts}
		_, err := optimizer.Optimize(context.Background(), Request{
			Seed:    testSeedSpec(),
			Dataset: testDataset(2),
		})
		require.ErrorContains(t, err, "candidate holdout")
	})
}

func TestSubmissionPolicyRejectsRegressionWithoutCallingService(t *testing.T) {
	cases := makeCases("holdout", 10)
	cases[0].Critical = true
	baseline := mustBatch(t, cases, 0.5)
	candidateBatch := mustBatch(t, cases, 0.7)
	seed := &candidate{id: "seed", spec: testSeedSpec()}
	best := &candidate{id: "best", spec: testSeedSpec()}
	best.spec.Description = "improved reusable workflow"
	req := Request{Dataset: Dataset{ID: "dataset", Version: "v1", Holdout: cases}}

	optimizer := &Optimizer{opts: defaultOptions()}
	result := &Result{BaselineHoldout: baseline.summary(), CandidateHoldout: candidateBatch.summary()}
	require.NoError(t, optimizer.submitCandidate(
		context.Background(), req, seed, seed, baseline, baseline, result,
	))
	assert.False(t, result.PromotionEligible)
	assert.Contains(t, result.SubmissionReason, "no accepted candidate")

	noHoldout := req
	noHoldout.Dataset.Holdout = nil
	result = &Result{}
	require.NoError(t, optimizer.submitCandidate(
		context.Background(), noHoldout, seed, best,
		evaluationBatch{}, evaluationBatch{}, result,
	))
	assert.False(t, result.PromotionEligible)
	assert.Contains(t, result.SubmissionReason, "no holdout")

	optimizer.opts.minimumHoldoutImprovement = 0.3
	result = &Result{BaselineHoldout: baseline.summary(), CandidateHoldout: candidateBatch.summary()}
	require.NoError(t, optimizer.submitCandidate(
		context.Background(), req, seed, best, baseline, candidateBatch, result,
	))
	assert.False(t, result.PromotionEligible)
	assert.Contains(t, result.SubmissionReason, "below required")

	regressedResults := append([]Evaluation(nil), candidateBatch.ordered...)
	regressedResults[0].Score = 0.4
	regressed, err := newEvaluationBatch(cases, regressedResults)
	require.NoError(t, err)
	optimizer.opts.minimumHoldoutImprovement = 0
	result = &Result{BaselineHoldout: baseline.summary(), CandidateHoldout: regressed.summary()}
	require.NoError(t, optimizer.submitCandidate(
		context.Background(), req, seed, best, baseline, regressed, result,
	))
	assert.False(t, result.PromotionEligible)
	assert.Contains(t, result.SubmissionReason, "critical holdout case regressed")

	caseID, regressedCase := criticalRegression(cases, baseline, candidateBatch)
	assert.Empty(t, caseID)
	assert.False(t, regressedCase)
}

func TestStopOnContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorContains(t, stopOnContext(ctx, errors.New("failure")), "context canceled")
	require.NoError(t, stopOnContext(context.Background(), errors.New("failure")))
}

func mustBatch(t *testing.T, cases []Case, score float64) evaluationBatch {
	t.Helper()
	results := make([]Evaluation, 0, len(cases))
	for _, item := range cases {
		results = append(results, Evaluation{CaseID: item.ID, Score: score})
	}
	batch, err := newEvaluationBatch(cases, results)
	require.NoError(t, err)
	return batch
}

func testSeedSpec() *evolution.SkillSpec {
	return &evolution.SkillSpec{
		Name:        "Benchmark Skill",
		Description: "baseline reusable workflow",
		WhenToUse:   "Use for benchmark tasks.",
		Steps:       []string{"Prepare the input.", "Validate the output."},
	}
}

func testDataset(holdout int) Dataset {
	return Dataset{
		ID:         "benchmark-dataset",
		Version:    "v1",
		Feedback:   makeCases("feedback", 2),
		Validation: makeCases("validation", 2),
		Holdout:    makeCases("holdout", holdout),
	}
}

func makeCases(prefix string, count int) []Case {
	cases := make([]Case, 0, count)
	for index := 0; index < count; index++ {
		cases = append(cases, Case{
			ID:       prefix + "-" + string(rune('a'+index)),
			Input:    "input " + prefix,
			Expected: "expected output",
		})
	}
	return cases
}
