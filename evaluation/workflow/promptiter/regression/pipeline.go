//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation"
)

// EvaluationOutput is one Evaluation Service result and its runtime-observed cost.
type EvaluationOutput struct {
	Result *evaluation.EvaluationResult
	Cost   Cost
}

// EvaluateFunc evaluates one prompt against one configured eval set.
type EvaluateFunc func(context.Context, string, string, int64) (*EvaluationOutput, error)

// CandidateRequest contains the accepted prompt and its current training failures.
type CandidateRequest struct {
	Prompt string
	Hints  []FailureHint
	Round  int
	Seed   int64
}

// Candidate is a generated prompt and its runtime-observed optimization cost.
type Candidate struct {
	Prompt string
	Cost   Cost
}

// GenerateFunc generates one prompt candidate.
type GenerateFunc func(context.Context, CandidateRequest) (*Candidate, error)

// RunRequest configures one complete regression loop.
type RunRequest struct {
	InitialPrompt       string
	TrainEvalSetID      string
	ValidationEvalSetID string
	GatePolicy          GatePolicy
	MaxRounds           int
	Seed                int64
}

// Round records one candidate and its acceptance evidence.
type Round struct {
	Number           int           `json:"number"`
	InputPrompt      string        `json:"input_prompt"`
	CandidatePrompt  string        `json:"candidate_prompt"`
	Hints            []FailureHint `json:"hints"`
	Train            *EvalSummary  `json:"train"`
	Validation       *EvalSummary  `json:"validation"`
	TrainDelta       *DatasetDelta `json:"train_delta"`
	ValidationDelta  *DatasetDelta `json:"validation_delta"`
	Attribution      *Attribution  `json:"validation_attribution"`
	Gate             *GateDecision `json:"gate"`
	ServingCost      Cost          `json:"serving_cost"`
	OptimizationCost Cost          `json:"optimization_cost"`
}

// OptimizationRun is the complete in-memory result consumed by audit reports.
type OptimizationRun struct {
	InitialPrompt        string
	AcceptedPrompt       string
	BaselineTrain        *EvalSummary
	BaselineValidation   *EvalSummary
	AcceptedTrain        *EvalSummary
	AcceptedValidation   *EvalSummary
	Rounds               []Round
	TotalCost            Cost
	WriteBackRecommended bool
	StopReason           string
	Seed                 int64
}

// Run executes baseline evaluation, candidate generation, regression, and gating.
func Run(ctx context.Context, request RunRequest, evaluate EvaluateFunc, generate GenerateFunc) (*OptimizationRun, error) {
	if err := validateRunRequest(request, evaluate, generate); err != nil {
		return nil, err
	}
	baselineTrain, baselineTrainCost, err := evaluateSummary(ctx, evaluate, request.InitialPrompt, request.TrainEvalSetID, request.Seed)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline train: %w", err)
	}
	baselineValidation, baselineValidationCost, err := evaluateSummary(
		ctx, evaluate, request.InitialPrompt, request.ValidationEvalSetID, request.Seed,
	)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline validation: %w", err)
	}
	totalCost, err := addCosts(baselineTrainCost, baselineValidationCost)
	if err != nil {
		return nil, fmt.Errorf("baseline cost: %w", err)
	}
	run := &OptimizationRun{
		InitialPrompt: request.InitialPrompt, AcceptedPrompt: request.InitialPrompt,
		BaselineTrain: baselineTrain, BaselineValidation: baselineValidation,
		AcceptedTrain: baselineTrain, AcceptedValidation: baselineValidation,
		Rounds: make([]Round, 0, request.MaxRounds), TotalCost: totalCost, Seed: request.Seed,
	}

	for roundNumber := 1; roundNumber <= request.MaxRounds; roundNumber++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		attribution, err := Attribute(run.AcceptedTrain)
		if err != nil {
			return nil, fmt.Errorf("attribute train round %d: %w", roundNumber, err)
		}
		hints, err := Hints(attribution)
		if err != nil {
			return nil, fmt.Errorf("build hints round %d: %w", roundNumber, err)
		}
		if len(hints) == 0 {
			run.StopReason = "no optimizable training failures"
			break
		}
		candidate, err := generate(ctx, CandidateRequest{
			Prompt: run.AcceptedPrompt, Hints: append([]FailureHint(nil), hints...),
			Round: roundNumber, Seed: request.Seed,
		})
		if err != nil {
			return nil, fmt.Errorf("generate candidate round %d: %w", roundNumber, err)
		}
		if candidate == nil || strings.TrimSpace(candidate.Prompt) == "" {
			return nil, fmt.Errorf("generate candidate round %d returned no prompt", roundNumber)
		}
		if err := validateCost(candidate.Cost); err != nil {
			return nil, fmt.Errorf("candidate round %d cost: %w", roundNumber, err)
		}
		candidateTrain, trainCost, err := evaluateSummary(
			ctx, evaluate, candidate.Prompt, request.TrainEvalSetID, request.Seed,
		)
		if err != nil {
			return nil, fmt.Errorf("evaluate candidate train round %d: %w", roundNumber, err)
		}
		candidateValidation, validationCost, err := evaluateSummary(
			ctx, evaluate, candidate.Prompt, request.ValidationEvalSetID, request.Seed,
		)
		if err != nil {
			return nil, fmt.Errorf("evaluate candidate validation round %d: %w", roundNumber, err)
		}
		trainDelta, err := Compare(run.AcceptedTrain, candidateTrain)
		if err != nil {
			return nil, fmt.Errorf("compare train round %d: %w", roundNumber, err)
		}
		validationDelta, err := Compare(run.AcceptedValidation, candidateValidation)
		if err != nil {
			return nil, fmt.Errorf("compare validation round %d: %w", roundNumber, err)
		}
		if validationDelta.EvalSetID != request.ValidationEvalSetID {
			return nil, fmt.Errorf("gate round %d received eval set %q, want validation %q",
				roundNumber, validationDelta.EvalSetID, request.ValidationEvalSetID)
		}
		servingCost, err := addCosts(trainCost, validationCost)
		if err != nil {
			return nil, fmt.Errorf("serving cost round %d: %w", roundNumber, err)
		}
		gateCost, err := addCosts(servingCost, candidate.Cost)
		if err != nil {
			return nil, fmt.Errorf("gate cost round %d: %w", roundNumber, err)
		}
		decision, err := Gate(request.GatePolicy, validationDelta, gateCost)
		if err != nil {
			return nil, fmt.Errorf("gate round %d: %w", roundNumber, err)
		}
		validationAttribution, err := Attribute(candidateValidation)
		if err != nil {
			return nil, fmt.Errorf("attribute validation round %d: %w", roundNumber, err)
		}
		run.Rounds = append(run.Rounds, Round{
			Number: roundNumber, InputPrompt: run.AcceptedPrompt, CandidatePrompt: candidate.Prompt,
			Hints: hints, Train: candidateTrain, Validation: candidateValidation,
			TrainDelta: trainDelta, ValidationDelta: validationDelta, Attribution: validationAttribution,
			Gate: decision, ServingCost: servingCost, OptimizationCost: candidate.Cost,
		})
		run.TotalCost, err = addCosts(run.TotalCost, servingCost, candidate.Cost)
		if err != nil {
			return nil, fmt.Errorf("total cost round %d: %w", roundNumber, err)
		}
		if decision.Accepted {
			run.AcceptedPrompt = candidate.Prompt
			run.AcceptedTrain = candidateTrain
			run.AcceptedValidation = candidateValidation
			run.WriteBackRecommended = true
			run.StopReason = "candidate accepted by regression gate"
			break
		}
	}
	if run.StopReason == "" {
		run.StopReason = "maximum rounds reached without an accepted candidate"
	}
	return run, nil
}

func evaluateSummary(
	ctx context.Context, evaluate EvaluateFunc, prompt, evalSetID string, seed int64,
) (*EvalSummary, Cost, error) {
	if err := ctx.Err(); err != nil {
		return nil, Cost{}, err
	}
	output, err := evaluate(ctx, prompt, evalSetID, seed)
	if err != nil {
		return nil, Cost{}, err
	}
	if output == nil || output.Result == nil {
		return nil, Cost{}, errors.New("evaluator returned no result")
	}
	if err := validateCost(output.Cost); err != nil {
		return nil, Cost{}, err
	}
	summary, err := Summarize(output.Result)
	if err != nil {
		return nil, Cost{}, err
	}
	if summary.EvalSetID != evalSetID {
		return nil, Cost{}, fmt.Errorf("evaluator returned eval set %q, want %q", summary.EvalSetID, evalSetID)
	}
	// Runtime-observed model calls are authoritative; trace-derived tokens and
	// latency fill fields that an evaluator wrapper did not observe.
	if output.Cost.Tokens == 0 {
		output.Cost.Tokens = summary.Cost.Tokens
	}
	if output.Cost.LatencyMS == 0 {
		output.Cost.LatencyMS = summary.Cost.LatencyMS
	}
	summary.Cost = output.Cost
	return summary, output.Cost, nil
}

func validateRunRequest(request RunRequest, evaluate EvaluateFunc, generate GenerateFunc) error {
	switch {
	case evaluate == nil:
		return errors.New("evaluator is nil")
	case generate == nil:
		return errors.New("candidate generator is nil")
	case strings.TrimSpace(request.InitialPrompt) == "":
		return errors.New("initial prompt is empty")
	case strings.TrimSpace(request.TrainEvalSetID) == "":
		return errors.New("train eval set id is empty")
	case strings.TrimSpace(request.ValidationEvalSetID) == "":
		return errors.New("validation eval set id is empty")
	case request.TrainEvalSetID == request.ValidationEvalSetID:
		return errors.New("train and validation eval set ids must differ")
	case request.MaxRounds <= 0:
		return errors.New("max rounds must be greater than zero")
	default:
		return validatePolicy(request.GatePolicy)
	}
}

func validateCost(cost Cost) error {
	if cost.ModelCalls < 0 || cost.Tokens < 0 || cost.LatencyMS < 0 {
		return errors.New("cost must not be negative")
	}
	return nil
}

func addCosts(costs ...Cost) (Cost, error) {
	var result Cost
	for _, cost := range costs {
		if err := validateCost(cost); err != nil {
			return Cost{}, err
		}
		if result.ModelCalls > int(^uint(0)>>1)-cost.ModelCalls ||
			result.Tokens > int64(^uint64(0)>>1)-cost.Tokens ||
			result.LatencyMS > int64(^uint64(0)>>1)-cost.LatencyMS {
			return Cost{}, errors.New("cost overflow")
		}
		result.ModelCalls += cost.ModelCalls
		result.Tokens += cost.Tokens
		result.LatencyMS += cost.LatencyMS
	}
	return result, nil
}
