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
	"math"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const gepaAlgorithm = "gepa"

type gepaOptions struct {
	maxIterations       int
	reflectionBatchSize int
}

// NewGEPA creates a pure-Go GEPA optimizer backed by reflectionModel and
// evaluator. Each Optimize call owns its search state. Concurrent calls require
// the supplied model and evaluator to support concurrent use.
func NewGEPA(
	reflectionModel model.Model,
	evaluator Evaluator,
	opts ...Option,
) (Optimizer, error) {
	if reflectionModel == nil {
		return nil, errors.New("evolution optimization: nil reflection model")
	}
	if evaluator == nil {
		return nil, errors.New("evolution optimization: nil evaluator")
	}
	configured := defaultOptions()
	for _, opt := range opts {
		if opt != nil {
			opt(&configured)
		}
	}
	if configured.gepa.maxIterations < 0 {
		return nil, errors.New("evolution optimization: max iterations must not be negative")
	}
	if configured.gepa.reflectionBatchSize <= 0 {
		return nil, errors.New("evolution optimization: reflection batch size must be positive")
	}
	minimumImprovement := configured.engine.minimumHoldoutImprovement
	if math.IsNaN(minimumImprovement) ||
		minimumImprovement < 0 || minimumImprovement > 1 {
		return nil, errors.New("evolution optimization: holdout improvement must be between 0 and 1")
	}
	return &gepaOptimizer{
		engine: &engine{
			evaluator: evaluator,
			opts:      configured.engine,
		},
		search: &gepaSearch{
			reflector: newLLMReflector(reflectionModel),
			opts:      configured.gepa,
		},
	}, nil
}

type gepaOptimizer struct {
	engine *engine
	search *gepaSearch
}

var _ Optimizer = (*gepaOptimizer)(nil)

// Optimize runs GEPA reflective mutation, strict minibatch acceptance,
// instance-level Pareto parent selection, and a final holdout comparison.
func (o *gepaOptimizer) Optimize(
	ctx context.Context,
	req Request,
) (*Result, error) {
	if o == nil || o.engine == nil || o.search == nil {
		return nil, errors.New("evolution optimization: optimizer is not initialized")
	}
	return o.engine.optimize(ctx, req, o.search)
}

type gepaSearch struct {
	reflector reflector
	opts      gepaOptions
}

func (*gepaSearch) algorithm() string {
	return gepaAlgorithm
}

func (s *gepaSearch) settings() map[string]any {
	return map[string]any{
		"max_iterations":        s.opts.maxIterations,
		"reflection_batch_size": s.opts.reflectionBatchSize,
	}
}

func (s *gepaSearch) search(run *searchRun) (searchOutcome, error) {
	if s == nil || s.reflector == nil {
		return searchOutcome{}, errors.New("evolution optimization: GEPA search is not initialized")
	}
	state := &gepaRun{
		search: s,
		run:    run,
		pool:   make(map[string]*gepaCandidate),
		matrix: newScoreMatrix(run.req.Dataset.Validation),
	}
	seed := &gepaCandidate{candidate: run.seed}
	state.pool[run.seed.id] = seed
	if err := state.matrix.add(run.seed.id, run.seed.validation); err != nil {
		return searchOutcome{}, fmt.Errorf("evolution optimization: add seed scores: %w", err)
	}
	stopReason, err := state.searchCandidates()
	if err != nil {
		return searchOutcome{}, err
	}
	best := state.pool[state.matrix.bestCandidateID()]
	return searchOutcome{
		best:           best.candidate,
		candidateCount: len(state.pool),
		stopReason:     stopReason,
	}, nil
}

type gepaCandidate struct {
	candidate     *candidate
	nextComponent component
}

type gepaRun struct {
	search *gepaSearch
	run    *searchRun
	pool   map[string]*gepaCandidate
	matrix *scoreMatrix
}

type gepaIteration struct {
	iteration     int
	parent        *gepaCandidate
	feedbackCases []Case
	pairedSeed    int64
	parentScores  evaluationBatch
	component     component
	child         *gepaCandidate
}

func (r *gepaRun) searchCandidates() (string, error) {
	batchSize := min(r.search.opts.reflectionBatchSize, len(r.run.req.Dataset.Feedback))
	for iteration := 0; iteration < r.search.opts.maxIterations; iteration++ {
		if err := r.run.ctx.Err(); err != nil {
			return "", fmt.Errorf("evolution optimization: %w", err)
		}
		iterationCalls := 2*batchSize + len(r.run.req.Dataset.Validation)
		if !r.run.budget.canSpend(iterationCalls, r.run.holdoutReserve) {
			return "metric_budget", nil
		}
		if err := r.runIteration(iteration, batchSize); err != nil {
			return "", err
		}
	}
	return "max_iterations", nil
}

func (r *gepaRun) runIteration(iteration, batchSize int) error {
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

func (r *gepaRun) prepareParent(
	iteration int,
	batchSize int,
) (*gepaIteration, bool, error) {
	parentID, err := r.matrix.selectParent(r.run.rng)
	if err != nil {
		return nil, false, fmt.Errorf("evolution optimization: select parent: %w", err)
	}
	state := &gepaIteration{
		iteration:     iteration,
		parent:        r.pool[parentID],
		feedbackCases: sampleCases(r.run.req.Dataset.Feedback, batchSize, r.run.rng),
		pairedSeed:    r.run.rng.Int63(),
	}
	state.parentScores, err = r.run.evaluate(
		state.parent.candidate.spec,
		state.feedbackCases,
		state.pairedSeed,
	)
	r.run.budget.spend(len(state.feedbackCases))
	if err != nil {
		return nil, false, fmt.Errorf(
			"evolution optimization: evaluate parent feedback: %w",
			err,
		)
	}
	if err := r.run.recorder.recordEvaluation(
		"feedback-parent",
		state.parent.candidate,
		state.parentScores,
		state.pairedSeed,
	); err != nil {
		return nil, false, fmt.Errorf("evolution optimization: record parent feedback: %w", err)
	}
	state.component = state.parent.nextComponent
	state.parent.nextComponent = (state.parent.nextComponent + 1) % componentCount
	return state, true, nil
}

func (r *gepaRun) reflectChild(state *gepaIteration) (bool, error) {
	proposed, err := r.search.reflector.propose(r.run.ctx, reflectionInput{
		candidate:  state.parent.candidate.spec,
		component:  state.component,
		evaluation: state.parentScores,
	})
	if err != nil {
		if !errors.Is(err, errReflectionRejected) {
			return false, fmt.Errorf(
				"evolution optimization: reflect candidate: %w",
				err,
			)
		}
		return r.recordReflectionRejection(
			"reflection_rejected",
			map[string]any{
				"iteration": state.iteration,
				"parent_id": state.parent.candidate.id,
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
			"parent_id":    state.parent.candidate.id,
			"candidate_id": childID,
		})
	}
	state.child = &gepaCandidate{
		candidate: &candidate{
			id:       childID,
			parentID: state.parent.candidate.id,
			spec:     proposed.spec,
			metadata: map[string]string{
				"component": state.component.String(),
				"rationale": proposed.rationale,
			},
		},
		nextComponent: state.parent.nextComponent,
	}
	return true, nil
}

func (r *gepaRun) evaluateChild(state *gepaIteration) (bool, error) {
	scores, err := r.run.evaluate(
		state.child.candidate.spec,
		state.feedbackCases,
		state.pairedSeed,
	)
	r.run.budget.spend(len(state.feedbackCases))
	if err != nil {
		return false, fmt.Errorf(
			"evolution optimization: evaluate child feedback: %w",
			err,
		)
	}
	if err := r.run.recorder.recordEvaluation(
		"feedback-child",
		state.child.candidate,
		scores,
		state.pairedSeed,
	); err != nil {
		return false, fmt.Errorf("evolution optimization: record child feedback: %w", err)
	}
	if scores.sum() <= state.parentScores.sum() {
		return false, r.recordEvent("candidate_rejected", map[string]any{
			"iteration":    state.iteration,
			"candidate_id": state.child.candidate.id,
			"parent_score": state.parentScores.sum(),
			"child_score":  scores.sum(),
			"reason":       "strict_minibatch_improvement",
		})
	}
	return true, nil
}

func (r *gepaRun) validateAndAcceptChild(state *gepaIteration) (bool, error) {
	validation, err := r.run.evaluate(
		state.child.candidate.spec,
		r.run.req.Dataset.Validation,
		r.run.validationSeed,
	)
	r.run.budget.spend(len(r.run.req.Dataset.Validation))
	if err != nil {
		return false, fmt.Errorf(
			"evolution optimization: evaluate candidate validation: %w",
			err,
		)
	}
	state.child.candidate.validation = validation
	r.pool[state.child.candidate.id] = state.child
	if err := r.matrix.add(state.child.candidate.id, validation); err != nil {
		return false, fmt.Errorf("evolution optimization: add candidate scores: %w", err)
	}
	if err := r.run.recorder.recordCandidate(state.child.candidate); err != nil {
		return false, fmt.Errorf("evolution optimization: record candidate: %w", err)
	}
	if err := r.run.recorder.recordEvaluation(
		"validation",
		state.child.candidate,
		validation,
		r.run.validationSeed,
	); err != nil {
		return false, fmt.Errorf("evolution optimization: record validation: %w", err)
	}
	if err := r.recordEvent("candidate_accepted", map[string]any{
		"iteration":    state.iteration,
		"candidate_id": state.child.candidate.id,
		"parent_id":    state.parent.candidate.id,
		"component":    state.component.String(),
	}); err != nil {
		return false, err
	}
	return true, nil
}

func (r *gepaRun) recordReflectionRejection(
	kind string,
	data map[string]any,
	cause error,
) (bool, error) {
	if err := stopOnContext(r.run.ctx, cause); err != nil {
		return false, err
	}
	data["error"] = cause.Error()
	return false, r.recordEvent(kind, data)
}

func (r *gepaRun) recordEvent(kind string, data map[string]any) error {
	if err := r.run.recorder.recordEvent(kind, data); err != nil {
		return fmt.Errorf("evolution optimization: record event: %w", err)
	}
	return nil
}
