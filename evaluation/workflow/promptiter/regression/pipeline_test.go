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
	"testing"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

func TestRunPromotesAcceptedPromptAndKeepsRejectedRound(t *testing.T) {
	evaluations := map[string]*evaluation.EvaluationResult{
		"base/train":             pipelineResult("train", 0.4, false),
		"base/validation":        pipelineResult("validation", 0.4, false),
		"candidate-1/train":      pipelineResult("train", 0.8, true),
		"candidate-1/validation": pipelineResult("validation", 0.3, false),
		"candidate-2/train":      pipelineResult("train", 0.7, true),
		"candidate-2/validation": pipelineResult("validation", 0.7, true),
	}
	var evaluationRequests []string
	evaluate := func(_ context.Context, prompt, evalSetID string, seed int64) (*EvaluationOutput, error) {
		if seed != 2003 {
			t.Fatalf("seed = %d", seed)
		}
		key := prompt + "/" + evalSetID
		evaluationRequests = append(evaluationRequests, key)
		return &EvaluationOutput{Result: evaluations[key], Cost: Cost{ModelCalls: 1, Tokens: 10}}, nil
	}
	var generated []CandidateRequest
	generate := func(_ context.Context, request CandidateRequest) (*Candidate, error) {
		generated = append(generated, request)
		return &Candidate{Prompt: fmt.Sprintf("candidate-%d", request.Round), Cost: Cost{ModelCalls: 1, Tokens: 5}}, nil
	}
	run, err := Run(context.Background(), RunRequest{
		InitialPrompt: "base", TrainEvalSetID: "train", ValidationEvalSetID: "validation",
		GatePolicy: GatePolicy{MinValidationGain: 0.1, MaxMetricDrop: 1}, MaxRounds: 2, Seed: 2003,
	}, evaluate, generate)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if run.AcceptedPrompt != "candidate-2" || !run.WriteBackRecommended || len(run.Rounds) != 2 {
		t.Fatalf("run = %+v", run)
	}
	if run.Rounds[0].Gate.Accepted || !run.Rounds[1].Gate.Accepted {
		t.Fatalf("round gates = %+v, %+v", run.Rounds[0].Gate, run.Rounds[1].Gate)
	}
	if generated[0].Prompt != "base" || generated[1].Prompt != "base" {
		t.Fatalf("generated requests = %+v", generated)
	}
	if len(generated[0].Hints) != 1 || generated[0].Hints[0].MetricName != "quality" {
		t.Fatalf("round one hints = %+v", generated[0].Hints)
	}
	if run.TotalCost.ModelCalls != 8 || run.TotalCost.Tokens != 70 {
		t.Fatalf("total cost = %+v", run.TotalCost)
	}
	wantRequests := []string{
		"base/train", "base/validation", "candidate-1/train", "candidate-1/validation",
		"candidate-2/train", "candidate-2/validation",
	}
	if strings.Join(evaluationRequests, ",") != strings.Join(wantRequests, ",") {
		t.Fatalf("evaluation requests = %v", evaluationRequests)
	}
}

func TestRunRejectsWrongEvaluationSetBeforeGate(t *testing.T) {
	evaluate := func(_ context.Context, _, evalSetID string, _ int64) (*EvaluationOutput, error) {
		returnedID := evalSetID
		if evalSetID == "validation" {
			returnedID = "train"
		}
		return &EvaluationOutput{Result: pipelineResult(returnedID, 0.5, false)}, nil
	}
	_, err := Run(context.Background(), RunRequest{
		InitialPrompt: "base", TrainEvalSetID: "train", ValidationEvalSetID: "validation", MaxRounds: 1,
	}, evaluate, func(context.Context, CandidateRequest) (*Candidate, error) {
		return &Candidate{Prompt: "candidate"}, nil
	})
	if err == nil || !strings.Contains(err.Error(), `want "validation"`) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunStopsWithoutOptimizableTrainingFailures(t *testing.T) {
	executionFailure := pipelineResult("train", 1, true)
	executionFailure.EvalCases[0].RunDetails = []*evaluation.EvaluationCaseRunDetails{{
		Inference: &evaluation.EvaluationInferenceDetails{
			Status: status.EvalStatusFailed, ErrorMessage: "runner stopped",
		},
	}}
	tests := []struct {
		name  string
		train *evaluation.EvaluationResult
	}{
		{name: "all training metrics pass", train: pipelineResult("train", 1, true)},
		{name: "execution failure without failed metric", train: executionFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			generatorCalls := 0
			evaluate := func(_ context.Context, _, evalSetID string, _ int64) (*EvaluationOutput, error) {
				if evalSetID == "train" {
					return &EvaluationOutput{Result: test.train}, nil
				}
				return &EvaluationOutput{Result: pipelineResult("validation", 1, true)}, nil
			}
			run, err := Run(context.Background(), RunRequest{
				InitialPrompt: "base", TrainEvalSetID: "train", ValidationEvalSetID: "validation", MaxRounds: 2,
			}, evaluate, func(context.Context, CandidateRequest) (*Candidate, error) {
				generatorCalls++
				return &Candidate{Prompt: "unexpected"}, nil
			})
			if err != nil {
				t.Fatalf("Run() error = %v", err)
			}
			if generatorCalls != 0 || len(run.Rounds) != 0 || run.WriteBackRecommended {
				t.Fatalf("run = %+v, generator calls = %d", run, generatorCalls)
			}
			if run.StopReason != "no optimizable training failures" || run.AcceptedPrompt != "base" {
				t.Fatalf("run = %+v", run)
			}
		})
	}
}

func TestRunValidatesInputsAndCollaboratorErrors(t *testing.T) {
	valid := RunRequest{InitialPrompt: "base", TrainEvalSetID: "train", ValidationEvalSetID: "validation", MaxRounds: 1}
	evaluate := func(context.Context, string, string, int64) (*EvaluationOutput, error) {
		return nil, errors.New("evaluation unavailable")
	}
	generate := func(context.Context, CandidateRequest) (*Candidate, error) { return &Candidate{Prompt: "next"}, nil }
	tests := []struct {
		request  RunRequest
		evaluate EvaluateFunc
		generate GenerateFunc
	}{
		{request: valid, generate: generate},
		{request: valid, evaluate: evaluate},
		{request: RunRequest{}, evaluate: evaluate, generate: generate},
		{request: RunRequest{InitialPrompt: "x", ValidationEvalSetID: "validation", MaxRounds: 1}, evaluate: evaluate, generate: generate},
		{request: RunRequest{InitialPrompt: "x", TrainEvalSetID: "train", MaxRounds: 1}, evaluate: evaluate, generate: generate},
		{request: RunRequest{InitialPrompt: "x", TrainEvalSetID: "train", ValidationEvalSetID: "validation"}, evaluate: evaluate, generate: generate},
		{request: RunRequest{InitialPrompt: "x", TrainEvalSetID: "same", ValidationEvalSetID: "same", MaxRounds: 1}, evaluate: evaluate, generate: generate},
	}
	for _, test := range tests {
		if _, err := Run(context.Background(), test.request, test.evaluate, test.generate); err == nil {
			t.Fatalf("Run(%+v) error = nil", test.request)
		}
	}
}

func TestRunRejectsGeneratorAndRuntimeCosts(t *testing.T) {
	valid := RunRequest{InitialPrompt: "base", TrainEvalSetID: "train", ValidationEvalSetID: "validation", MaxRounds: 1}
	evaluate := func(_ context.Context, _, evalSetID string, _ int64) (*EvaluationOutput, error) {
		return &EvaluationOutput{Result: pipelineResult(evalSetID, 0, false)}, nil
	}
	if _, err := Run(context.Background(), valid, evaluate, func(context.Context, CandidateRequest) (*Candidate, error) {
		return nil, errors.New("optimizer unavailable")
	}); err == nil || !strings.Contains(err.Error(), "optimizer unavailable") {
		t.Fatalf("generator error = %v", err)
	}
	if _, err := Run(context.Background(), valid, evaluate, func(context.Context, CandidateRequest) (*Candidate, error) {
		return &Candidate{Prompt: "candidate", Cost: Cost{Tokens: -1}}, nil
	}); err == nil || !strings.Contains(err.Error(), "cost") {
		t.Fatalf("candidate cost error = %v", err)
	}
	badCost := func(_ context.Context, _, evalSetID string, _ int64) (*EvaluationOutput, error) {
		return &EvaluationOutput{Result: pipelineResult(evalSetID, 0, false), Cost: Cost{ModelCalls: -1}}, nil
	}
	if _, err := Run(context.Background(), valid, badCost, func(context.Context, CandidateRequest) (*Candidate, error) {
		return &Candidate{Prompt: "candidate"}, nil
	}); err == nil || !strings.Contains(err.Error(), "cost") {
		t.Fatalf("evaluation cost error = %v", err)
	}
}

func TestRunPropagatesEvaluationErrorsByStage(t *testing.T) {
	for _, failAt := range []int{2, 3, 4} {
		t.Run(fmt.Sprintf("evaluation %d", failAt), func(t *testing.T) {
			calls := 0
			evaluate := func(_ context.Context, _, evalSetID string, _ int64) (*EvaluationOutput, error) {
				calls++
				if calls == failAt {
					return nil, errors.New("evaluation unavailable")
				}
				return &EvaluationOutput{Result: pipelineResult(evalSetID, 0, false)}, nil
			}
			_, err := Run(context.Background(), RunRequest{
				InitialPrompt: "base", TrainEvalSetID: "train", ValidationEvalSetID: "validation", MaxRounds: 1,
			}, evaluate, func(context.Context, CandidateRequest) (*Candidate, error) {
				return &Candidate{Prompt: "candidate"}, nil
			})
			if err == nil || !strings.Contains(err.Error(), "evaluation unavailable") {
				t.Fatalf("Run() error = %v", err)
			}
		})
	}
}

func TestGeneratePromptIterUsesTargetAndHints(t *testing.T) {
	engine := &promptIterStub{
		structure: &astructure.Snapshot{
			StructureID: "structure", Surfaces: []astructure.Surface{{SurfaceID: "system"}},
		},
		candidate: "improved prompt",
	}
	base := promptiterengine.RunRequest{
		Train:      []promptiterengine.EvalSetInput{{EvalSetID: "train"}},
		Validation: []promptiterengine.EvalSetInput{{EvalSetID: "validation"}},
		MaxRounds:  9,
	}
	prompt, err := GeneratePromptIter(context.Background(), engine, base, "system", CandidateRequest{
		Prompt: "baseline", Hints: []FailureHint{{CaseID: "case", MetricName: "quality", Reason: "wrong answer"}},
	})
	if err != nil {
		t.Fatalf("GeneratePromptIter() error = %v", err)
	}
	if prompt != "improved prompt" || engine.request.MaxRounds != 1 || len(engine.request.Train[0].LossHints) != 1 {
		t.Fatalf("prompt = %q, request = %+v", prompt, engine.request)
	}
	if base.MaxRounds != 9 || len(base.Train[0].LossHints) != 0 {
		t.Fatalf("base request was modified: %+v", base)
	}
	if engine.request.InitialProfile.Overrides[0].Value.Text == nil ||
		*engine.request.InitialProfile.Overrides[0].Value.Text != "baseline" {
		t.Fatalf("initial profile = %+v", engine.request.InitialProfile)
	}
}

func TestGeneratePromptIterRejectsMissingCandidate(t *testing.T) {
	engine := &promptIterStub{structure: &astructure.Snapshot{
		StructureID: "structure", Surfaces: []astructure.Surface{{SurfaceID: "system"}},
	}}
	_, err := GeneratePromptIter(context.Background(), engine, promptiterengine.RunRequest{
		Train:      []promptiterengine.EvalSetInput{{EvalSetID: "train"}},
		Validation: []promptiterengine.EvalSetInput{{EvalSetID: "validation"}},
	}, "system", CandidateRequest{Prompt: "baseline"})
	if err == nil {
		t.Fatal("GeneratePromptIter() error = nil")
	}
}

func TestGeneratePromptIterRejectsInvalidContracts(t *testing.T) {
	validBase := promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{{EvalSetID: "train"}}, Validation: []promptiterengine.EvalSetInput{{EvalSetID: "validation"}},
	}
	validRequest := CandidateRequest{Prompt: "baseline"}
	validStructure := &astructure.Snapshot{StructureID: "structure", Surfaces: []astructure.Surface{{SurfaceID: "system"}}}
	tests := []struct {
		name    string
		engine  promptiterengine.Engine
		base    promptiterengine.RunRequest
		target  string
		request CandidateRequest
	}{
		{name: "nil engine", base: validBase, target: "system", request: validRequest},
		{name: "missing target", engine: &promptIterStub{structure: validStructure}, base: validBase, request: validRequest},
		{name: "wrong dataset count", engine: &promptIterStub{structure: validStructure}, target: "system", request: validRequest},
		{name: "describe error", engine: &promptIterStub{describeErr: errors.New("describe")}, base: validBase, target: "system", request: validRequest},
		{name: "missing surface", engine: &promptIterStub{structure: &astructure.Snapshot{StructureID: "structure"}}, base: validBase, target: "system", request: validRequest},
		{name: "incomplete hint", engine: &promptIterStub{structure: validStructure}, base: validBase, target: "system",
			request: CandidateRequest{Prompt: "baseline", Hints: []FailureHint{{CaseID: "case"}}}},
		{name: "run error", engine: &promptIterStub{structure: validStructure, runErr: errors.New("run")}, base: validBase, target: "system", request: validRequest},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := GeneratePromptIter(context.Background(), test.engine, test.base, test.target, test.request); err == nil {
				t.Fatal("GeneratePromptIter() error = nil")
			}
		})
	}
}

func TestPromptFromProfileRejectsDuplicateAndEmptyTarget(t *testing.T) {
	empty := " "
	value := "candidate"
	for _, profile := range []*promptiter.Profile{
		{},
		{Overrides: []promptiter.SurfaceOverride{{SurfaceID: "system", Value: astructure.SurfaceValue{Text: &empty}}}},
		{Overrides: []promptiter.SurfaceOverride{
			{SurfaceID: "system", Value: astructure.SurfaceValue{Text: &value}},
			{SurfaceID: "system", Value: astructure.SurfaceValue{Text: &value}},
		}},
	} {
		if _, err := promptFromProfile(profile, "system"); err == nil {
			t.Fatalf("promptFromProfile(%+v) error = nil", profile)
		}
	}
}

func TestAddCostsRejectsOverflow(t *testing.T) {
	maxInt := int(^uint(0) >> 1)
	maxInt64 := int64(^uint64(0) >> 1)
	for _, costs := range [][]Cost{
		{{ModelCalls: maxInt}, {ModelCalls: 1}},
		{{Tokens: maxInt64}, {Tokens: 1}},
		{{LatencyMS: maxInt64}, {LatencyMS: 1}},
	} {
		if _, err := addCosts(costs...); err == nil {
			t.Fatalf("addCosts(%+v) error = nil", costs)
		}
	}
}

func TestRunCancellationNilOutputsAndEmptyCandidate(t *testing.T) {
	valid := RunRequest{InitialPrompt: "base", TrainEvalSetID: "train", ValidationEvalSetID: "validation", MaxRounds: 1}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	evaluate := func(ctx context.Context, _ string, evalSetID string, _ int64) (*EvaluationOutput, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return &EvaluationOutput{Result: pipelineResult(evalSetID, 0, false)}, nil
	}
	if _, err := Run(cancelled, valid, evaluate, func(context.Context, CandidateRequest) (*Candidate, error) {
		return &Candidate{Prompt: "candidate"}, nil
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled Run() error = %v", err)
	}
	if _, err := Run(context.Background(), valid, func(context.Context, string, string, int64) (*EvaluationOutput, error) {
		return nil, nil
	}, func(context.Context, CandidateRequest) (*Candidate, error) { return nil, nil }); err == nil {
		t.Fatal("Run() accepted nil evaluation output")
	}
	if _, err := Run(context.Background(), valid, evaluate, func(context.Context, CandidateRequest) (*Candidate, error) {
		return &Candidate{}, nil
	}); err == nil || !strings.Contains(err.Error(), "no prompt") {
		t.Fatalf("empty candidate error = %v", err)
	}
}

func TestRunChecksCancellationBeforeCandidateGeneration(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	evaluations := 0
	evaluate := func(_ context.Context, _, evalSetID string, _ int64) (*EvaluationOutput, error) {
		evaluations++
		if evaluations == 2 {
			cancel()
		}
		return &EvaluationOutput{Result: pipelineResult(evalSetID, 0, false)}, nil
	}
	generated := false
	_, err := Run(ctx, RunRequest{
		InitialPrompt: "base", TrainEvalSetID: "train", ValidationEvalSetID: "validation", MaxRounds: 1,
	}, evaluate, func(context.Context, CandidateRequest) (*Candidate, error) {
		generated = true
		return &Candidate{Prompt: "candidate"}, nil
	})
	if !errors.Is(err, context.Canceled) || generated {
		t.Fatalf("Run() error = %v, generated = %t", err, generated)
	}
}

func TestRunStopsAtMaximumRounds(t *testing.T) {
	evaluate := func(_ context.Context, _, evalSetID string, _ int64) (*EvaluationOutput, error) {
		return &EvaluationOutput{Result: pipelineResult(evalSetID, 0, false)}, nil
	}
	run, err := Run(context.Background(), RunRequest{
		InitialPrompt: "base", TrainEvalSetID: "train", ValidationEvalSetID: "validation",
		MaxRounds: 1, GatePolicy: GatePolicy{MinValidationGain: 1},
	}, evaluate, func(context.Context, CandidateRequest) (*Candidate, error) {
		return &Candidate{Prompt: "candidate"}, nil
	})
	if err != nil || run.StopReason != "maximum rounds reached without an accepted candidate" || len(run.Rounds) != 1 {
		t.Fatalf("Run() = %+v, %v", run, err)
	}
}

type promptIterStub struct {
	structure   *astructure.Snapshot
	candidate   string
	request     promptiterengine.RunRequest
	describeErr error
	runErr      error
}

func (s *promptIterStub) Describe(context.Context) (*astructure.Snapshot, error) {
	return s.structure, s.describeErr
}

func (s *promptIterStub) Run(
	_ context.Context, request *promptiterengine.RunRequest, _ ...promptiterengine.Option,
) (*promptiterengine.RunResult, error) {
	s.request = *request
	if s.runErr != nil {
		return nil, s.runErr
	}
	if s.candidate == "" {
		return &promptiterengine.RunResult{}, nil
	}
	text := s.candidate
	return &promptiterengine.RunResult{Rounds: []promptiterengine.RoundResult{{
		OutputProfile: &promptiter.Profile{
			StructureID: "structure",
			Overrides:   []promptiter.SurfaceOverride{{SurfaceID: "system", Value: astructure.SurfaceValue{Text: &text}}},
		},
	}}}, nil
}

func pipelineResult(evalSetID string, score float64, passed bool) *evaluation.EvaluationResult {
	evalStatus := status.EvalStatusFailed
	if passed {
		evalStatus = status.EvalStatusPassed
	}
	return &evaluation.EvaluationResult{
		EvalSetID: evalSetID,
		EvalCases: []*evaluation.EvaluationCaseResult{{
			EvalCaseID: "case",
			MetricResults: []*evalresult.EvalMetricResult{{
				MetricName: "quality", Score: score, Threshold: 0.5, EvalStatus: evalStatus,
				Details: &evalresult.EvalMetricResultDetails{Reason: "wrong answer"},
			}},
		}},
	}
}
