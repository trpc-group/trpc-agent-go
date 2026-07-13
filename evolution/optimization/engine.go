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

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
)

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

type optimizerRun struct {
	optimizer          *Optimizer
	ctx                context.Context
	req                Request
	recorder           experimentRecorder
	budget             *metricBudget
	rng                *rand.Rand
	holdoutReserve     int
	seed               *candidate
	seedValidationSeed int64
	pool               map[string]*candidate
	matrix             *scoreMatrix
}

type iterationState struct {
	iteration     int
	parent        *candidate
	feedbackCases []Case
	pairedSeed    int64
	parentScores  evaluationBatch
	component     component
	child         *candidate
}

// Optimize runs reflective mutation, strict minibatch acceptance,
// instance-level Pareto parent selection, and a final holdout comparison.
func (o *Optimizer) Optimize(
	ctx context.Context,
	req Request,
) (*Result, error) {
	if err := o.validateRun(req); err != nil {
		return nil, err
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req.Seed = cloneSpec(req.Seed)
	req.Dataset = cloneDataset(req.Dataset)
	runCtx, cancel := o.withTimeLimit(ctx)
	defer cancel()

	experimentID := uuid.NewString()
	recorder, err := newExperimentRecorder(o.opts.storeDir, experimentID)
	if err != nil {
		return nil, fmt.Errorf("evolution optimization: %w", err)
	}
	if err := recorder.start(req, o.opts); err != nil {
		return nil, fmt.Errorf("evolution optimization: start experiment record: %w", err)
	}

	run := o.newRun(runCtx, req, recorder)
	if !run.budget.canSpend(len(req.Dataset.Validation), run.holdoutReserve) {
		return nil, errors.New("evolution optimization: metric budget cannot cover validation and holdout")
	}
	if err := run.initializeSeed(); err != nil {
		return nil, err
	}
	stopReason, err := run.search()
	if err != nil {
		return nil, err
	}
	best := run.pool[run.matrix.bestCandidateID()]
	result := run.buildResult(experimentID, best, stopReason)
	baselineHoldout, candidateHoldout, err := run.evaluateHoldout(best, result)
	if err != nil {
		return nil, err
	}
	result.MetricCalls = run.budget.used
	if req.Submit {
		if err := o.submitCandidate(
			runCtx, req, run.seed, best, baselineHoldout, candidateHoldout, result,
		); err != nil {
			return nil, err
		}
	}
	if err := recorder.finish(result); err != nil {
		return nil, fmt.Errorf("evolution optimization: finish experiment record: %w", err)
	}
	return result, nil
}

func (o *Optimizer) validateRun(req Request) error {
	if o == nil || o.reflector == nil || o.evaluator == nil {
		return errors.New("evolution optimization: optimizer is not initialized")
	}
	if err := validateRequest(req); err != nil {
		return fmt.Errorf("evolution optimization: %w", err)
	}
	if req.Submit && o.opts.evolutionService == nil {
		return errors.New("evolution optimization: submission requested without an evolution service")
	}
	return nil
}

func (o *Optimizer) withTimeLimit(ctx context.Context) (context.Context, context.CancelFunc) {
	if o.opts.timeLimit > 0 {
		return context.WithTimeout(ctx, o.opts.timeLimit)
	}
	return ctx, func() {}
}

func (o *Optimizer) newRun(
	ctx context.Context,
	req Request,
	recorder experimentRecorder,
) *optimizerRun {
	// #nosec G404 -- deterministic experiment sampling is not security-sensitive.
	rng := rand.New(rand.NewSource(o.opts.randomSeed))
	return &optimizerRun{
		optimizer:      o,
		ctx:            ctx,
		req:            req,
		recorder:       recorder,
		budget:         &metricBudget{max: o.opts.maxMetricCalls},
		rng:            rng,
		holdoutReserve: 2 * len(req.Dataset.Holdout),
		pool:           make(map[string]*candidate),
		matrix:         newScoreMatrix(req.Dataset.Validation),
	}
}

func (r *optimizerRun) initializeSeed() error {
	seedID, err := specHash(r.req.Seed)
	if err != nil {
		return fmt.Errorf("evolution optimization: hash seed: %w", err)
	}
	r.seed = &candidate{id: seedID, spec: cloneSpec(r.req.Seed)}
	r.seedValidationSeed = r.rng.Int63()
	validation, err := r.optimizer.evaluate(
		r.ctx,
		r.seed.spec,
		r.req.Dataset.Validation,
		r.seedValidationSeed,
	)
	r.budget.spend(len(r.req.Dataset.Validation))
	if err != nil {
		return fmt.Errorf("evolution optimization: evaluate seed validation: %w", err)
	}
	r.seed.validation = validation
	r.pool[r.seed.id] = r.seed
	if err := r.matrix.add(r.seed.id, validation); err != nil {
		return fmt.Errorf("evolution optimization: add seed scores: %w", err)
	}
	if err := r.recorder.recordCandidate(r.seed); err != nil {
		return fmt.Errorf("evolution optimization: record seed: %w", err)
	}
	if err := r.recorder.recordEvaluation(
		"validation",
		r.seed,
		validation,
		r.seedValidationSeed,
	); err != nil {
		return fmt.Errorf("evolution optimization: record seed validation: %w", err)
	}
	return nil
}

func (r *optimizerRun) search() (string, error) {
	batchSize := min(r.optimizer.opts.reflectionBatchSize, len(r.req.Dataset.Feedback))
	for iteration := 0; iteration < r.optimizer.opts.maxIterations; iteration++ {
		if err := r.ctx.Err(); err != nil {
			return "", fmt.Errorf("evolution optimization: %w", err)
		}
		iterationCalls := 2*batchSize + len(r.req.Dataset.Validation)
		if !r.budget.canSpend(iterationCalls, r.holdoutReserve) {
			return "metric_budget", nil
		}
		if err := r.runIteration(iteration, batchSize); err != nil {
			return "", err
		}
	}
	return "max_iterations", nil
}

func (r *optimizerRun) runIteration(iteration, batchSize int) error {
	state, proceed, err := r.prepareParent(iteration, batchSize)
	if err != nil || !proceed {
		return err
	}
	proceed, err = r.reflectChild(state)
	if err != nil || !proceed {
		return err
	}
	proceed, err = r.evaluateChild(state)
	if err != nil || !proceed {
		return err
	}
	_, err = r.validateAndAcceptChild(state)
	return err
}

func (r *optimizerRun) prepareParent(
	iteration int,
	batchSize int,
) (*iterationState, bool, error) {
	parentID, err := r.matrix.selectParent(r.rng)
	if err != nil {
		return nil, false, fmt.Errorf("evolution optimization: select parent: %w", err)
	}
	state := &iterationState{
		iteration:     iteration,
		parent:        r.pool[parentID],
		feedbackCases: sampleCases(r.req.Dataset.Feedback, batchSize, r.rng),
		pairedSeed:    r.rng.Int63(),
	}
	state.parentScores, err = r.optimizer.evaluate(
		r.ctx,
		state.parent.spec,
		state.feedbackCases,
		state.pairedSeed,
	)
	r.budget.spend(len(state.feedbackCases))
	if err != nil {
		proceed, failureErr := r.recordRecoverableFailure(
			"parent_evaluation_failed",
			map[string]any{"iteration": iteration, "parent_id": parentID},
			err,
		)
		return nil, proceed, failureErr
	}
	if err := r.recorder.recordEvaluation(
		"feedback-parent",
		state.parent,
		state.parentScores,
		state.pairedSeed,
	); err != nil {
		return nil, false, fmt.Errorf("evolution optimization: record parent feedback: %w", err)
	}
	state.component = state.parent.nextComponent
	state.parent.nextComponent = (state.parent.nextComponent + 1) % componentCount
	return state, true, nil
}

func (r *optimizerRun) reflectChild(state *iterationState) (bool, error) {
	proposed, err := r.optimizer.reflector.propose(r.ctx, reflectionInput{
		candidate:  state.parent.spec,
		component:  state.component,
		evaluation: state.parentScores,
	})
	if err != nil {
		return r.recordRecoverableFailure(
			"reflection_rejected",
			map[string]any{
				"iteration": state.iteration,
				"parent_id": state.parent.id,
				"component": state.component.String(),
			},
			err,
		)
	}
	childID, err := specHash(proposed.spec)
	if err != nil {
		return false, fmt.Errorf("evolution optimization: hash reflected candidate: %w", err)
	}
	if _, duplicate := r.pool[childID]; duplicate {
		return false, r.recordEvent("candidate_duplicate", map[string]any{
			"iteration":    state.iteration,
			"parent_id":    state.parent.id,
			"candidate_id": childID,
		})
	}
	state.child = &candidate{
		id:            childID,
		parentID:      state.parent.id,
		spec:          proposed.spec,
		component:     state.component,
		rationale:     proposed.rationale,
		nextComponent: state.parent.nextComponent,
	}
	return true, nil
}

func (r *optimizerRun) evaluateChild(state *iterationState) (bool, error) {
	scores, err := r.optimizer.evaluate(
		r.ctx,
		state.child.spec,
		state.feedbackCases,
		state.pairedSeed,
	)
	r.budget.spend(len(state.feedbackCases))
	if err != nil {
		return r.recordRecoverableFailure(
			"child_evaluation_failed",
			map[string]any{
				"iteration":    state.iteration,
				"candidate_id": state.child.id,
			},
			err,
		)
	}
	if err := r.recorder.recordEvaluation(
		"feedback-child",
		state.child,
		scores,
		state.pairedSeed,
	); err != nil {
		return false, fmt.Errorf("evolution optimization: record child feedback: %w", err)
	}
	if scores.sum() <= state.parentScores.sum() {
		return false, r.recordEvent("candidate_rejected", map[string]any{
			"iteration":    state.iteration,
			"candidate_id": state.child.id,
			"parent_score": state.parentScores.sum(),
			"child_score":  scores.sum(),
			"reason":       "strict_minibatch_improvement",
		})
	}
	return true, nil
}

func (r *optimizerRun) validateAndAcceptChild(state *iterationState) (bool, error) {
	validation, err := r.optimizer.evaluate(
		r.ctx,
		state.child.spec,
		r.req.Dataset.Validation,
		r.seedValidationSeed,
	)
	r.budget.spend(len(r.req.Dataset.Validation))
	if err != nil {
		return r.recordRecoverableFailure(
			"validation_failed",
			map[string]any{
				"iteration":    state.iteration,
				"candidate_id": state.child.id,
			},
			err,
		)
	}
	state.child.validation = validation
	r.pool[state.child.id] = state.child
	if err := r.matrix.add(state.child.id, validation); err != nil {
		return false, fmt.Errorf("evolution optimization: add candidate scores: %w", err)
	}
	if err := r.recorder.recordCandidate(state.child); err != nil {
		return false, fmt.Errorf("evolution optimization: record candidate: %w", err)
	}
	if err := r.recorder.recordEvaluation(
		"validation",
		state.child,
		validation,
		r.seedValidationSeed,
	); err != nil {
		return false, fmt.Errorf("evolution optimization: record validation: %w", err)
	}
	if err := r.recordEvent("candidate_accepted", map[string]any{
		"iteration":    state.iteration,
		"candidate_id": state.child.id,
		"parent_id":    state.parent.id,
		"component":    state.component.String(),
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (r *optimizerRun) recordRecoverableFailure(
	kind string,
	data map[string]any,
	cause error,
) (bool, error) {
	if err := stopOnContext(r.ctx, cause); err != nil {
		return false, err
	}
	data["error"] = cause.Error()
	return false, r.recordEvent(kind, data)
}

func (r *optimizerRun) recordEvent(kind string, data map[string]any) error {
	if err := r.recorder.recordEvent(kind, data); err != nil {
		return fmt.Errorf("evolution optimization: record event: %w", err)
	}
	return nil
}

func (r *optimizerRun) buildResult(
	experimentID string,
	best *candidate,
	stopReason string,
) *Result {
	return &Result{
		ExperimentID:        experimentID,
		Spec:                cloneSpec(best.spec),
		BaselineValidation:  r.seed.validation.summary(),
		CandidateValidation: best.validation.summary(),
		CandidateCount:      len(r.pool),
		StopReason:          stopReason,
	}
}

func (r *optimizerRun) evaluateHoldout(
	best *candidate,
	result *Result,
) (evaluationBatch, evaluationBatch, error) {
	var baseline, candidateScores evaluationBatch
	if len(r.req.Dataset.Holdout) == 0 {
		return baseline, candidateScores, nil
	}
	pairedSeed := r.rng.Int63()
	var err error
	baseline, err = r.optimizer.evaluate(
		r.ctx,
		r.seed.spec,
		r.req.Dataset.Holdout,
		pairedSeed,
	)
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
	candidateScores, err = r.evaluateBestHoldout(best, baseline, pairedSeed)
	if err != nil {
		return baseline, candidateScores, err
	}
	result.BaselineHoldout = baseline.summary()
	result.CandidateHoldout = candidateScores.summary()
	return baseline, candidateScores, nil
}

func (r *optimizerRun) evaluateBestHoldout(
	best *candidate,
	baseline evaluationBatch,
	pairedSeed int64,
) (evaluationBatch, error) {
	candidateScores := baseline
	if best.id != r.seed.id {
		var err error
		candidateScores, err = r.optimizer.evaluate(
			r.ctx,
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

func (o *Optimizer) evaluate(
	ctx context.Context,
	spec *evolution.SkillSpec,
	cases []Case,
	seed int64,
) (evaluationBatch, error) {
	inputs := cloneCases(cases)
	evaluations, err := o.evaluator.Evaluate(ctx, cloneSpec(spec), inputs, seed)
	if err != nil {
		return evaluationBatch{}, err
	}
	return newEvaluationBatch(cases, evaluations)
}

func (o *Optimizer) submitCandidate(
	ctx context.Context,
	req Request,
	seed *candidate,
	best *candidate,
	baselineHoldout evaluationBatch,
	candidateHoldout evaluationBatch,
	result *Result,
) error {
	if best.id == seed.id {
		result.SubmissionReason = "no accepted candidate improved validation"
		return nil
	}
	delta := result.CandidateHoldout.Score - result.BaselineHoldout.Score
	if delta < o.opts.minimumHoldoutImprovement {
		result.SubmissionReason = fmt.Sprintf(
			"holdout delta %.4f is below required %.4f",
			delta,
			o.opts.minimumHoldoutImprovement,
		)
		return nil
	}
	if caseID, regressed := criticalRegression(
		req.Dataset.Holdout,
		baselineHoldout,
		candidateHoldout,
	); regressed {
		result.SubmissionReason = "critical holdout case regressed: " + caseID
		return nil
	}
	revision, err := evolution.SubmitRevision(ctx, o.opts.evolutionService, evolution.RevisionRequest{
		Scope:    req.Scope,
		Source:   "genetic-pareto:" + result.ExperimentID,
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
		return fmt.Errorf("evolution optimization: submit revision: %w", err)
	}
	result.Revision = revision
	result.SubmissionReason = "revision submitted with status " + string(revision.Status)
	return nil
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
