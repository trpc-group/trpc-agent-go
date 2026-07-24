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
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

type optimizerSkillRepository struct {
	name string
}

func (r optimizerSkillRepository) Summaries() []skill.Summary {
	return []skill.Summary{{Name: r.name}}
}

func (r optimizerSkillRepository) Get(name string) (*skill.Skill, error) {
	if name != r.name {
		return nil, os.ErrNotExist
	}
	return &skill.Skill{Summary: skill.Summary{Name: name}, Body: "baseline"}, nil
}

func (r optimizerSkillRepository) Path(name string) (string, error) {
	if name != r.name {
		return "", os.ErrNotExist
	}
	return filepath.Join("skills", name), nil
}

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

type rejectedReflector struct{}

func (rejectedReflector) propose(context.Context, reflectionInput) (mutation, error) {
	return mutation{}, reflectionRejection(errors.New("reflection unavailable"))
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

type failingRevisionSubmitter struct{}

type nilRevisionSubmitter struct{}

func (failingRevisionSubmitter) SubmitRevision(
	context.Context,
	evolution.RevisionRequest,
) (*evolution.Revision, error) {
	return nil, errors.New("submission unavailable")
}

func (nilRevisionSubmitter) SubmitRevision(
	context.Context,
	evolution.RevisionRequest,
) (*evolution.Revision, error) {
	return nil, nil
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
		if !e.equalScore && improvedSpec(spec) {
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

func improvedSpec(spec *evolution.SkillSpec) bool {
	if spec == nil {
		return false
	}
	return strings.Contains(spec.Description, "improved") ||
		strings.Contains(spec.WhenToUse, "improved") ||
		slices.Contains(spec.Steps, "Check the improved result.") ||
		slices.Contains(spec.Pitfalls, "Avoid the observed failure.")
}

func newTestOptimizer(
	reflection reflector,
	evaluator Evaluator,
	opts options,
) *gepaOptimizer {
	return &gepaOptimizer{
		engine: &engine{evaluator: evaluator, opts: opts.engine},
		search: &gepaSearch{reflector: reflection, opts: opts.gepa},
	}
}

type seedSearch struct{}

func (seedSearch) algorithm() string { return "seed-only" }

func (seedSearch) settings() map[string]any {
	return map[string]any{"mode": "baseline"}
}

func (seedSearch) search(run *searchRun) (searchOutcome, error) {
	return searchOutcome{
		best:           run.seed,
		candidateCount: 1,
		stopReason:     "completed",
	}, nil
}

func TestEngineOwnsLifecycleIndependentOfSearchStrategy(t *testing.T) {
	evaluator := &scoringEvaluator{}
	storeDir := t.TempDir()
	engine := &engine{
		evaluator: evaluator,
		opts: engineOptions{
			storeDir:   storeDir,
			randomSeed: 7,
		},
	}

	result, err := engine.optimize(
		context.Background(),
		Request{Seed: testSeedSpec(), Dataset: testDataset(2)},
		seedSearch{},
	)
	require.NoError(t, err)
	assert.Equal(t, "seed-only", result.Algorithm)
	assert.Equal(t, "completed", result.StopReason)
	assert.Equal(t, 1, result.CandidateCount)
	assert.Equal(t, 4, result.MetricCalls)
	require.Len(t, evaluator.calls, 2)

	payload, err := os.ReadFile(filepath.Join(
		storeDir, result.ExperimentID, "experiment.json",
	))
	require.NoError(t, err)
	var record experimentRecord
	require.NoError(t, json.Unmarshal(payload, &record))
	assert.Equal(t, "seed-only", record.Algorithm)
	assert.Equal(t, "baseline", record.SearchOptions["mode"])
}

func TestOptimizerRunsReflectiveParetoLoopAndRecordsExperiment(t *testing.T) {
	evaluator := &scoringEvaluator{}
	storeDir := t.TempDir()
	opts := defaultOptions()
	opts.gepa.maxIterations = 1
	opts.gepa.reflectionBatchSize = 2
	opts.engine.storeDir = storeDir
	opts.engine.minimumHoldoutImprovement = 0.25
	optimizer := newTestOptimizer(improvingReflector{}, evaluator, opts)

	result, err := optimizer.Optimize(context.Background(), Request{
		Seed:    testSeedSpec(),
		Dataset: testDataset(2),
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Contains(t, result.Spec.Steps, "Check the improved result.")
	assert.Equal(t, "baseline reusable workflow", result.Spec.Description)
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

	experimentDir := filepath.Join(storeDir, result.ExperimentID)
	experimentPath := filepath.Join(experimentDir, "experiment.json")
	_, err = os.Stat(experimentPath)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(experimentDir, "result.json"))
	require.NoError(t, err)

	payload, err := os.ReadFile(experimentPath)
	require.NoError(t, err)
	var record experimentRecord
	require.NoError(t, json.Unmarshal(payload, &record))
	assert.Equal(t, gepaAlgorithm, record.Algorithm)
	assert.Equal(t, 0.25, record.MinimumHoldoutImprovement)
	assert.Equal(t, float64(1), record.SearchOptions["max_iterations"])
	assert.Equal(t, gepaAlgorithm, result.Algorithm)

	if runtime.GOOS != "windows" {
		for _, path := range []string{
			experimentDir,
			filepath.Join(experimentDir, "candidates"),
			filepath.Join(experimentDir, "evaluations"),
		} {
			info, statErr := os.Stat(path)
			require.NoError(t, statErr)
			assert.Equal(t, os.FileMode(0o700), info.Mode().Perm(), path)
		}
		for _, path := range []string{
			experimentPath,
			filepath.Join(experimentDir, "result.json"),
			filepath.Join(experimentDir, "events.jsonl"),
		} {
			info, statErr := os.Stat(path)
			require.NoError(t, statErr)
			assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), path)
		}
	}
}

func TestExperimentRecorderBoundsEvaluatorText(t *testing.T) {
	root := t.TempDir()
	recorder, err := newExperimentRecorder(root, "bounded")
	require.NoError(t, err)
	cases := []Case{{ID: "case-a"}}
	oversized := strings.Repeat("界", storedEvaluationTextMaxBytes)
	batch, err := newEvaluationBatch(cases, []Evaluation{{
		CaseID:   "case-a",
		Score:    1,
		Output:   oversized,
		Feedback: oversized,
		Trace:    oversized,
	}})
	require.NoError(t, err)
	require.NoError(t, recorder.recordEvaluation(
		"validation",
		&candidate{id: "candidate-a", spec: testSeedSpec()},
		batch,
		1,
	))

	paths, err := filepath.Glob(filepath.Join(root, "bounded", "evaluations", "*.json"))
	require.NoError(t, err)
	require.Len(t, paths, 1)
	payload, err := os.ReadFile(paths[0])
	require.NoError(t, err)
	var record evaluationRecord
	require.NoError(t, json.Unmarshal(payload, &record))
	require.Len(t, record.Results, 1)
	var rawRecord map[string]any
	require.NoError(t, json.Unmarshal(payload, &rawRecord))
	_, storesFullCases := rawRecord["cases"]
	assert.False(t, storesFullCases)
	for _, value := range []string{
		record.Results[0].Output,
		record.Results[0].Feedback,
		record.Results[0].Trace,
	} {
		assert.LessOrEqual(t, len(value), storedEvaluationTextMaxBytes)
		assert.True(t, utf8.ValidString(value))
		assert.Contains(t, value, storedTextTruncationMarker)
	}
}

func TestOptimizerReturnsAndRecordsResultWhenSubmissionFails(t *testing.T) {
	storeDir := t.TempDir()
	opts := defaultOptions()
	opts.gepa.maxIterations = 1
	opts.gepa.reflectionBatchSize = 2
	opts.engine.storeDir = storeDir
	opts.engine.revisionSubmitter = failingRevisionSubmitter{}
	optimizer := newTestOptimizer(improvingReflector{}, &scoringEvaluator{}, opts)
	dataset := testDataset(10)
	dataset.Feedback = makeCases("feedback", 10)
	dataset.Validation = makeCases("validation", 10)

	result, err := optimizer.Optimize(context.Background(), Request{
		Seed:    testSeedSpec(),
		Dataset: dataset,
		Submit:  true,
	})
	require.ErrorContains(t, err, "submission unavailable")
	require.NotNil(t, result)
	assert.Contains(t, result.SubmissionReason, "submission unavailable")

	payload, readErr := os.ReadFile(filepath.Join(
		storeDir, result.ExperimentID, "result.json",
	))
	require.NoError(t, readErr)
	var stored Result
	require.NoError(t, json.Unmarshal(payload, &stored))
	assert.Equal(t, result.SubmissionReason, stored.SubmissionReason)
}

func TestOptimizerRejectsNilSuccessfulSubmission(t *testing.T) {
	storeDir := t.TempDir()
	opts := defaultOptions()
	opts.gepa.maxIterations = 1
	opts.gepa.reflectionBatchSize = 2
	opts.engine.storeDir = storeDir
	opts.engine.revisionSubmitter = nilRevisionSubmitter{}
	optimizer := newTestOptimizer(improvingReflector{}, &scoringEvaluator{}, opts)
	dataset := testDataset(10)
	dataset.Feedback = makeCases("feedback", 10)
	dataset.Validation = makeCases("validation", 10)

	result, err := optimizer.Optimize(context.Background(), Request{
		Seed:    testSeedSpec(),
		Dataset: dataset,
		Submit:  true,
	})
	require.ErrorContains(t, err, "nil revision")
	require.NotNil(t, result)
	assert.Contains(t, result.SubmissionReason, "nil revision")
	assert.Nil(t, result.Revision)

	payload, readErr := os.ReadFile(filepath.Join(
		storeDir, result.ExperimentID, "result.json",
	))
	require.NoError(t, readErr)
	var stored Result
	require.NoError(t, json.Unmarshal(payload, &stored))
	assert.Equal(t, result.SubmissionReason, stored.SubmissionReason)
}

func TestOptimizerRejectsCandidateWithoutStrictMinibatchImprovement(t *testing.T) {
	evaluator := &scoringEvaluator{equalScore: true}
	opts := defaultOptions()
	opts.gepa.maxIterations = 1
	opts.gepa.reflectionBatchSize = 2
	optimizer := newTestOptimizer(improvingReflector{}, evaluator, opts)

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
	opts.gepa.maxIterations = 2
	opts.engine.maxMetricCalls = 11
	opts.gepa.reflectionBatchSize = 2
	optimizer := newTestOptimizer(improvingReflector{}, evaluator, opts)

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
		evolution.WithSkillRepository(optimizerSkillRepository{name: testSeedSpec().Name}),
		evolution.WithSpecGate(evolution.NewDefaultSpecGate()),
	)
	t.Cleanup(func() { require.NoError(t, svc.Close()) })

	evaluator := &scoringEvaluator{}
	opts := defaultOptions()
	opts.gepa.maxIterations = 1
	opts.gepa.reflectionBatchSize = 2
	submitter, ok := svc.(evolution.RevisionSubmitter)
	require.True(t, ok)
	recordingSubmitter := &recordingRevisionSubmitter{delegate: submitter}
	WithRevisionSubmitter(recordingSubmitter)(&opts)
	optimizer := newTestOptimizer(improvingReflector{}, evaluator, opts)
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
	assert.Equal(t,
		"optimization/gepa:"+result.ExperimentID,
		recordingSubmitter.requests[0].Source,
	)

	stored, err := store.ReadRevision(
		context.Background(), result.Revision.SkillID, result.Revision.RevisionID,
	)
	require.NoError(t, err)
	assert.Equal(t, evolution.RevisionPendingApproval, stored.Status)
}

func TestOptimizerSkipsRejectedAndDuplicateReflections(t *testing.T) {
	tests := []struct {
		name      string
		reflector reflector
	}{
		{name: "reflection", reflector: rejectedReflector{}},
		{name: "duplicate", reflector: duplicateReflector{}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluator := &scoringEvaluator{}
			opts := defaultOptions()
			opts.gepa.maxIterations = 1
			opts.gepa.reflectionBatchSize = 2
			optimizer := newTestOptimizer(test.reflector, evaluator, opts)
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

func TestOptimizerReturnsSearchEvaluatorErrors(t *testing.T) {
	tests := []struct {
		name    string
		failAt  int
		message string
	}{
		{name: "parent feedback", failAt: 2, message: "evaluate parent feedback"},
		{name: "child feedback", failAt: 3, message: "evaluate child feedback"},
		{name: "validation", failAt: 4, message: "evaluate candidate validation"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			evaluator := &stagedEvaluator{failAt: test.failAt}
			opts := defaultOptions()
			opts.gepa.maxIterations = 1
			opts.gepa.reflectionBatchSize = 2
			optimizer := newTestOptimizer(improvingReflector{}, evaluator, opts)

			result, err := optimizer.Optimize(context.Background(), Request{
				Seed: testSeedSpec(),
				Dataset: Dataset{
					ID:         "failure-dataset",
					Version:    "v1",
					Feedback:   makeCases("feedback", 2),
					Validation: makeCases("validation", 2),
				},
			})
			require.ErrorContains(t, err, test.message)
			assert.Nil(t, result)
		})
	}
}

func TestOptimizerReturnsFatalInitializationAndEvaluationErrors(t *testing.T) {
	_, err := (*gepaOptimizer)(nil).Optimize(context.Background(), Request{})
	require.ErrorContains(t, err, "not initialized")

	opts := defaultOptions()
	optimizer := newTestOptimizer(improvingReflector{}, &scoringEvaluator{}, opts)
	_, err = optimizer.Optimize(context.Background(), Request{})
	require.ErrorContains(t, err, "seed")

	opts.engine.maxMetricCalls = 1
	optimizer = newTestOptimizer(improvingReflector{}, &scoringEvaluator{}, opts)
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
	optimizer = newTestOptimizer(improvingReflector{}, &scoringEvaluator{}, opts)
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
	optimizer = newTestOptimizer(improvingReflector{}, seedFailure, defaultOptions())
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
		opts.gepa.maxIterations = 0
		opts.engine.storeDir = t.TempDir()
		optimizer := newTestOptimizer(improvingReflector{}, evaluator, opts)
		result, err := optimizer.Optimize(context.Background(), Request{
			Seed:    testSeedSpec(),
			Dataset: testDataset(2),
		})
		require.ErrorContains(t, err, "baseline holdout")
		require.NotNil(t, result)
		assert.Equal(t, 4, result.MetricCalls)
		assert.Equal(t, 0.2, result.BaselineValidation.Score)
		assert.Zero(t, result.BaselineHoldout.Cases)
		_, statErr := os.Stat(filepath.Join(
			opts.engine.storeDir, result.ExperimentID, "result.json",
		))
		require.NoError(t, statErr)
	})
	t.Run("candidate", func(t *testing.T) {
		evaluator := &stagedEvaluator{failAt: 6}
		opts := defaultOptions()
		opts.gepa.maxIterations = 1
		opts.gepa.reflectionBatchSize = 2
		optimizer := newTestOptimizer(improvingReflector{}, evaluator, opts)
		result, err := optimizer.Optimize(context.Background(), Request{
			Seed:    testSeedSpec(),
			Dataset: testDataset(2),
		})
		require.ErrorContains(t, err, "candidate holdout")
		require.NotNil(t, result)
		assert.Equal(t, 12, result.MetricCalls)
		assert.Equal(t, 0.2, result.BaselineHoldout.Score)
		assert.Zero(t, result.CandidateHoldout.Cases)
	})
}

func TestPromotionPolicyRejectsRegressionWithoutCallingService(t *testing.T) {
	cases := makeCases("holdout", 10)
	cases[0].Critical = true
	baseline := mustBatch(t, cases, 0.5)
	candidateBatch := mustBatch(t, cases, 0.7)
	seed := &candidate{id: "seed", spec: testSeedSpec()}
	best := &candidate{id: "best", spec: testSeedSpec()}
	best.spec.Description = "improved reusable workflow"
	req := Request{Dataset: Dataset{ID: "dataset", Version: "v1", Holdout: cases}}

	optimizer := &engine{opts: defaultOptions().engine}
	assessAndSubmit := func(
		req Request,
		seed, best *candidate,
		baseline, candidateBatch evaluationBatch,
		result *Result,
	) {
		optimizer.assessPromotion(
			req, seed, best, baseline, candidateBatch, result,
		)
		require.NoError(t, optimizer.submitCandidate(
			context.Background(), req, best, result,
		))
	}
	result := &Result{BaselineHoldout: baseline.summary(), CandidateHoldout: candidateBatch.summary()}
	assessAndSubmit(req, seed, seed, baseline, baseline, result)
	assert.False(t, result.PromotionEligible)
	assert.Contains(t, result.SubmissionReason, "no accepted candidate")

	noHoldout := req
	noHoldout.Dataset.Holdout = nil
	result = &Result{}
	assessAndSubmit(
		noHoldout, seed, best, evaluationBatch{}, evaluationBatch{}, result,
	)
	assert.False(t, result.PromotionEligible)
	assert.Contains(t, result.SubmissionReason, "no holdout")

	optimizer.opts.minimumHoldoutImprovement = 0.3
	result = &Result{BaselineHoldout: baseline.summary(), CandidateHoldout: candidateBatch.summary()}
	assessAndSubmit(req, seed, best, baseline, candidateBatch, result)
	assert.False(t, result.PromotionEligible)
	assert.Contains(t, result.SubmissionReason, "below required")

	regressedResults := append([]Evaluation(nil), candidateBatch.ordered...)
	regressedResults[0].Score = 0.4
	regressed, err := newEvaluationBatch(cases, regressedResults)
	require.NoError(t, err)
	optimizer.opts.minimumHoldoutImprovement = 0
	result = &Result{BaselineHoldout: baseline.summary(), CandidateHoldout: regressed.summary()}
	assessAndSubmit(req, seed, best, baseline, regressed, result)
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
