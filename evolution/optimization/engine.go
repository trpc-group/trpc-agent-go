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
	"fmt"
	"math/rand"
	"time"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
)

type engineOptions struct {
	maxMetricCalls            int
	randomSeed                int64
	timeLimit                 time.Duration
	storeDir                  string
	revisionSubmitter         evolution.RevisionSubmitter
	minimumHoldoutImprovement float64
}

type engine struct {
	evaluator Evaluator
	opts      engineOptions
}

type searchStrategy interface {
	algorithm() string
	settings() map[string]any
	search(*searchRun) (searchOutcome, error)
}

type searchOutcome struct {
	best           *candidate
	candidateCount int
	stopReason     string
}

type candidate struct {
	id         string
	parentID   string
	spec       *evolution.SkillSpec
	metadata   map[string]string
	validation evaluationBatch
}

type metricBudget struct {
	max  int
	used int
}

func (b *metricBudget) canSpend(calls, reserve int) bool {
	return b.max <= 0 || b.used+calls+reserve <= b.max
}

func (b *metricBudget) spend(calls int) {
	b.used += calls
}

type searchRun struct {
	engine         *engine
	ctx            context.Context
	req            Request
	recorder       experimentRecorder
	budget         *metricBudget
	rng            *rand.Rand
	holdoutReserve int
	seed           *candidate
	validationSeed int64
}

func (e *engine) optimize(
	ctx context.Context,
	req Request,
	strategy searchStrategy,
) (*Result, error) {
	if err := e.validateRun(req, strategy); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req.Seed = cloneSpec(req.Seed)
	req.Dataset = cloneDataset(req.Dataset)
	runCtx, cancel := e.withTimeLimit(ctx)
	defer cancel()

	experimentID := uuid.NewString()
	recorder, err := newExperimentRecorder(e.opts.storeDir, experimentID)
	if err != nil {
		return nil, fmt.Errorf("evolution optimization: %w", err)
	}
	if err := recorder.start(req, experimentConfig{
		algorithm:     strategy.algorithm(),
		engine:        e.opts,
		searchOptions: strategy.settings(),
	}); err != nil {
		return nil, fmt.Errorf("evolution optimization: start experiment record: %w", err)
	}

	run := e.newSearchRun(runCtx, req, recorder)
	if err := run.initializeSeed(); err != nil {
		return nil, err
	}
	outcome, err := strategy.search(run)
	if err != nil {
		return nil, err
	}
	if err := validateSearchOutcome(outcome, len(req.Dataset.Validation)); err != nil {
		return nil, fmt.Errorf("evolution optimization: invalid search outcome: %w", err)
	}
	result := run.buildResult(experimentID, strategy.algorithm(), outcome)
	baselineHoldout, candidateHoldout, err := run.evaluateHoldout(outcome.best, result)
	if err != nil {
		result.MetricCalls = run.budget.used
		return result, errors.Join(err, finishExperiment(recorder, result))
	}
	e.assessPromotion(
		req, run.seed, outcome.best, baselineHoldout, candidateHoldout, result,
	)
	result.MetricCalls = run.budget.used
	var submissionErr error
	if req.Submit {
		submissionErr = e.submitCandidate(runCtx, req, outcome.best, result)
	}
	finishErr := finishExperiment(recorder, result)
	if submissionErr != nil || finishErr != nil {
		return result, errors.Join(submissionErr, finishErr)
	}
	return result, nil
}

func finishExperiment(recorder experimentRecorder, result *Result) error {
	if err := recorder.finish(result); err != nil {
		return fmt.Errorf("evolution optimization: finish experiment record: %w", err)
	}
	return nil
}

func (e *engine) validateRun(req Request, strategy searchStrategy) error {
	if e == nil || e.evaluator == nil || strategy == nil {
		return errors.New("evolution optimization: optimizer is not initialized")
	}
	if err := validateRequest(req); err != nil {
		return fmt.Errorf("evolution optimization: %w", err)
	}
	if req.Submit && e.opts.revisionSubmitter == nil {
		return errors.New("evolution optimization: submission requested without a revision submitter")
	}
	return nil
}

func (e *engine) withTimeLimit(ctx context.Context) (context.Context, context.CancelFunc) {
	if e.opts.timeLimit > 0 {
		return context.WithTimeout(ctx, e.opts.timeLimit)
	}
	return ctx, func() {}
}

func (e *engine) newSearchRun(
	ctx context.Context,
	req Request,
	recorder experimentRecorder,
) *searchRun {
	// #nosec G404 -- deterministic experiment sampling is not security-sensitive.
	rng := rand.New(rand.NewSource(e.opts.randomSeed))
	return &searchRun{
		engine:         e,
		ctx:            ctx,
		req:            req,
		recorder:       recorder,
		budget:         &metricBudget{max: e.opts.maxMetricCalls},
		rng:            rng,
		holdoutReserve: 2 * len(req.Dataset.Holdout),
	}
}

func (r *searchRun) initializeSeed() error {
	if !r.budget.canSpend(len(r.req.Dataset.Validation), r.holdoutReserve) {
		return errors.New("evolution optimization: metric budget cannot cover validation and holdout")
	}
	seedID, err := specHash(r.req.Seed)
	if err != nil {
		return fmt.Errorf("evolution optimization: hash seed: %w", err)
	}
	r.seed = &candidate{id: seedID, spec: cloneSpec(r.req.Seed)}
	r.validationSeed = r.rng.Int63()
	validation, err := r.evaluate(
		r.seed.spec,
		r.req.Dataset.Validation,
		r.validationSeed,
	)
	r.budget.spend(len(r.req.Dataset.Validation))
	if err != nil {
		return fmt.Errorf("evolution optimization: evaluate seed validation: %w", err)
	}
	r.seed.validation = validation
	if err := r.recorder.recordCandidate(r.seed); err != nil {
		return fmt.Errorf("evolution optimization: record seed: %w", err)
	}
	if err := r.recorder.recordEvaluation(
		"validation",
		r.seed,
		validation,
		r.validationSeed,
	); err != nil {
		return fmt.Errorf("evolution optimization: record seed validation: %w", err)
	}
	return nil
}

func (r *searchRun) evaluate(
	spec *evolution.SkillSpec,
	cases []Case,
	seed int64,
) (evaluationBatch, error) {
	inputs := cloneCases(cases)
	evaluations, err := r.engine.evaluator.Evaluate(
		r.ctx, cloneSpec(spec), inputs, seed,
	)
	if err != nil {
		return evaluationBatch{}, err
	}
	return newEvaluationBatch(cases, evaluations)
}

func validateSearchOutcome(outcome searchOutcome, validationCases int) error {
	if outcome.best == nil || outcome.best.spec == nil {
		return errors.New("missing selected candidate")
	}
	if len(outcome.best.validation.ordered) != validationCases {
		return errors.New("selected candidate has incomplete validation results")
	}
	if outcome.candidateCount <= 0 {
		return errors.New("candidate count must be positive")
	}
	if outcome.stopReason == "" {
		return errors.New("stop reason is required")
	}
	return nil
}

func (r *searchRun) buildResult(
	experimentID string,
	algorithm string,
	outcome searchOutcome,
) *Result {
	return &Result{
		Algorithm:           algorithm,
		ExperimentID:        experimentID,
		Spec:                cloneSpec(outcome.best.spec),
		BaselineValidation:  r.seed.validation.summary(),
		CandidateValidation: outcome.best.validation.summary(),
		CandidateCount:      outcome.candidateCount,
		StopReason:          outcome.stopReason,
	}
}

func (r *searchRun) evaluateHoldout(
	best *candidate,
	result *Result,
) (evaluationBatch, evaluationBatch, error) {
	var baseline, candidateScores evaluationBatch
	if len(r.req.Dataset.Holdout) == 0 {
		return baseline, candidateScores, nil
	}
	pairedSeed := r.rng.Int63()
	var err error
	baseline, err = r.evaluate(r.seed.spec, r.req.Dataset.Holdout, pairedSeed)
	r.budget.spend(len(r.req.Dataset.Holdout))
	if err != nil {
		return baseline, candidateScores, fmt.Errorf(
			"evolution optimization: evaluate baseline holdout: %w",
			err,
		)
	}
	if err := r.recorder.recordEvaluation(
		"holdout-baseline",
		r.seed,
		baseline,
		pairedSeed,
	); err != nil {
		return baseline, candidateScores, fmt.Errorf(
			"evolution optimization: record baseline holdout: %w",
			err,
		)
	}
	result.BaselineHoldout = baseline.summary()
	candidateScores, err = r.evaluateBestHoldout(best, baseline, pairedSeed)
	if err != nil {
		return baseline, candidateScores, err
	}
	result.CandidateHoldout = candidateScores.summary()
	return baseline, candidateScores, nil
}

func (r *searchRun) evaluateBestHoldout(
	best *candidate,
	baseline evaluationBatch,
	pairedSeed int64,
) (evaluationBatch, error) {
	candidateScores := baseline
	if best.id != r.seed.id {
		var err error
		candidateScores, err = r.evaluate(
			best.spec,
			r.req.Dataset.Holdout,
			pairedSeed,
		)
		r.budget.spend(len(r.req.Dataset.Holdout))
		if err != nil {
			return candidateScores, fmt.Errorf(
				"evolution optimization: evaluate candidate holdout: %w",
				err,
			)
		}
	}
	if err := r.recorder.recordEvaluation(
		"holdout-candidate",
		best,
		candidateScores,
		pairedSeed,
	); err != nil {
		return candidateScores, fmt.Errorf(
			"evolution optimization: record candidate holdout: %w",
			err,
		)
	}
	return candidateScores, nil
}

func (e *engine) submitCandidate(
	ctx context.Context,
	req Request,
	best *candidate,
	result *Result,
) error {
	if !result.PromotionEligible {
		result.SubmissionReason = result.PromotionReason
		return nil
	}
	delta := result.CandidateHoldout.Score - result.BaselineHoldout.Score
	revision, err := e.opts.revisionSubmitter.SubmitRevision(ctx, evolution.RevisionRequest{
		Scope:    req.Scope,
		Source:   "optimization/" + result.Algorithm + ":" + result.ExperimentID,
		Action:   evolution.RevisionActionUpdate,
		ParentID: req.ParentRevisionID,
		Spec:     cloneSpec(best.spec),
		Evidence: &evolution.RevisionEvidence{
			ExperimentID:   result.ExperimentID,
			DatasetID:      req.Dataset.ID,
			DatasetVersion: req.Dataset.Version,
			BaselineScore:  result.BaselineHoldout.Score,
			CandidateScore: result.CandidateHoldout.Score,
			Delta:          delta,
			CaseCount:      result.CandidateHoldout.Cases,
			Objectives:     cloneFloatMap(result.CandidateHoldout.Objectives),
		},
	})
	if err != nil {
		result.SubmissionReason = "revision submission failed: " + err.Error()
		return fmt.Errorf("evolution optimization: submit revision: %w", err)
	}
	if revision == nil {
		err := errors.New("revision submitter returned a nil revision")
		result.SubmissionReason = "revision submission failed: " + err.Error()
		return fmt.Errorf("evolution optimization: submit revision: %w", err)
	}
	result.Revision = revision
	result.SubmissionReason = "revision submitted with status " + string(revision.Status)
	return nil
}

func (e *engine) assessPromotion(
	req Request,
	seed *candidate,
	best *candidate,
	baselineHoldout evaluationBatch,
	candidateHoldout evaluationBatch,
	result *Result,
) {
	result.PromotionEligible = false
	if best.id == seed.id {
		result.PromotionReason = "no accepted candidate improved validation"
		return
	}
	if len(req.Dataset.Holdout) == 0 {
		result.PromotionReason = "no holdout split configured"
		return
	}
	delta := result.CandidateHoldout.Score - result.BaselineHoldout.Score
	if delta < e.opts.minimumHoldoutImprovement {
		result.PromotionReason = fmt.Sprintf(
			"holdout delta %.4f is below required %.4f",
			delta,
			e.opts.minimumHoldoutImprovement,
		)
		return
	}
	if caseID, regressed := criticalRegression(
		req.Dataset.Holdout,
		baselineHoldout,
		candidateHoldout,
	); regressed {
		result.PromotionReason = "critical holdout case regressed: " + caseID
		return
	}
	result.PromotionEligible = true
	result.PromotionReason = "holdout requirements satisfied"
}

func criticalRegression(
	cases []Case,
	baseline evaluationBatch,
	candidate evaluationBatch,
) (string, bool) {
	for _, item := range cases {
		if !item.Critical {
			continue
		}
		if candidate.byID[item.ID].Score < baseline.byID[item.ID].Score {
			return item.ID, true
		}
	}
	return "", false
}

func sampleCases(cases []Case, count int, rng *rand.Rand) []Case {
	permutation := rng.Perm(len(cases))
	selected := make([]Case, 0, count)
	for _, index := range permutation[:count] {
		selected = append(selected, cases[index])
	}
	return cloneCases(selected)
}

func stopOnContext(ctx context.Context, _ error) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("evolution optimization: %w", err)
	}
	return nil
}
