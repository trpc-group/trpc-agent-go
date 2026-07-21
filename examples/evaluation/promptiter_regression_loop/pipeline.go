//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
)

const (
	offlineMode      = "fake-model+trace"
	oneRound         = 1
	caseParallelism  = 1
	metricConfigName = "metrics.json"
)

type pipelineState struct {
	report             *regression.Report
	originalValidation *regression.EvaluationResult
	acceptedValidation *regression.EvaluationResult
	acceptedProfile    *promptiter.Profile
	baselinePrompt     regression.PromptRecord
	acceptedPrompt     regression.PromptRecord
	catalog            regression.AttributionCatalog
}

type roundExecution struct {
	attempt        int
	result         *promptiterengine.RunResult
	duration       time.Duration
	candidateTrain *regression.EvaluationResult
}

type roundRequest struct {
	runtime         *promptIterRuntime
	cfg             *config
	acceptedProfile *promptiter.Profile
	attempt         int
	candidatePrompt string
}

type pipelineInitRequest struct {
	cfg          *config
	runtime      *promptIterRuntime
	baselineText string
	catalog      regression.AttributionCatalog
	startedAt    time.Time
}

type normalizedRound struct {
	engineRound        promptiterengine.RoundResult
	inputTrain         *regression.EvaluationResult
	train              *regression.EvaluationResult
	validation         *regression.EvaluationResult
	baselineValidation *regression.EvaluationResult
	delta              *regression.DeltaSummary
	decision           *regression.GateDecision
	candidate          regression.PromptRecord
}

func runPipeline(ctx context.Context, cfg *config) (report *regression.Report, resultErr error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	baselineText, err := loadBaselinePrompt(cfg.BaselinePromptSource)
	if err != nil {
		return nil, err
	}
	runtime, err := buildRuntime(cfg, baselineText)
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := runtime.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
			markReportFailed(report, closeErr)
		}
	}()
	catalog, err := loadAttributionCatalog(filepath.Join(cfg.DataDir, metricConfigName))
	if err != nil {
		return nil, err
	}
	startedAt := time.Now().UTC()
	state, err := initializePipelineState(ctx, pipelineInitRequest{
		cfg: cfg, runtime: runtime, baselineText: baselineText, catalog: catalog, startedAt: startedAt,
	})
	if err != nil {
		return nil, err
	}
	for index, candidatePrompt := range cfg.CandidatePrompts {
		execution, err := executeRound(ctx, roundRequest{
			runtime: runtime, cfg: cfg, acceptedProfile: state.acceptedProfile,
			attempt: index + 1, candidatePrompt: candidatePrompt,
		})
		if err != nil {
			return failPipeline(state, startedAt, err)
		}
		if err := applyRound(cfg, state, execution); err != nil {
			return failPipeline(state, startedAt, err)
		}
	}
	if err := finalizePipeline(state, startedAt, nil); err != nil {
		return state.report, err
	}
	return state.report, nil
}

func executeRound(
	ctx context.Context,
	request roundRequest,
) (*roundExecution, error) {
	startedAt := time.Now()
	execution := &roundExecution{attempt: request.attempt}
	engine, err := request.runtime.engineForAttempt(ctx, request.candidatePrompt, request.attempt)
	if err != nil {
		execution.duration = time.Since(startedAt)
		return execution, err
	}
	result, err := engine.Run(ctx, buildRunRequest(request.cfg, request.acceptedProfile))
	if err != nil {
		execution.duration = time.Since(startedAt)
		return execution, fmt.Errorf("run PromptIter attempt %d: %w", request.attempt, err)
	}
	if result == nil {
		execution.duration = time.Since(startedAt)
		return execution, fmt.Errorf("run PromptIter attempt %d returned nil result", request.attempt)
	}
	execution.result = result
	if len(result.Rounds) != oneRound {
		execution.duration = time.Since(startedAt)
		return execution, fmt.Errorf("attempt %d returned %d rounds, want one", request.attempt, len(result.Rounds))
	}
	candidateTrain, err := request.runtime.evaluateProfile(
		ctx, request.cfg.TrainEvalSetID, result.Rounds[0].OutputProfile,
	)
	if err != nil {
		execution.duration = time.Since(startedAt)
		return execution, fmt.Errorf("evaluate candidate train attempt %d: %w", request.attempt, err)
	}
	execution.duration = time.Since(startedAt)
	execution.candidateTrain = candidateTrain
	return execution, nil
}

func buildRunRequest(cfg *config, profile *promptiter.Profile) *promptiterengine.RunRequest {
	return &promptiterengine.RunRequest{
		Train:             []promptiterengine.EvalSetInput{{EvalSetID: cfg.TrainEvalSetID}},
		Validation:        []promptiterengine.EvalSetInput{{EvalSetID: cfg.ValidationEvalSetID}},
		InitialProfile:    profile,
		EvaluationOptions: promptiterengine.EvaluationOptions{EvalCaseParallelism: caseParallelism},
		AcceptancePolicy:  promptiterengine.AcceptancePolicy{MinScoreGain: cfg.Gate.MinValidationScoreGain},
		MaxRounds:         oneRound,
		TargetSurfaceIDs:  []string{cfg.TargetSurfaceID},
	}
}

func applyRound(
	cfg *config,
	state *pipelineState,
	execution *roundExecution,
) error {
	round := execution.result.Rounds[0]
	train, validation, baselineValidation, err := normalizeEvaluationRound(execution.result, round)
	if err != nil {
		return fmt.Errorf("normalize attempt %d: %w", execution.attempt, err)
	}
	delta, err := regression.Compare(state.originalValidation, validation)
	if err != nil {
		return fmt.Errorf("compare attempt %d with original baseline: %w", execution.attempt, err)
	}
	decision, err := regression.Decide(cfg.Gate, regression.GateInput{
		OriginalBaseline: state.originalValidation,
		AcceptedBaseline: state.acceptedValidation,
		Candidate:        validation,
	})
	if err != nil {
		return fmt.Errorf("gate attempt %d: %w", execution.attempt, err)
	}
	candidate, err := promptFromProfile(round.OutputProfile, cfg.TargetSurfaceID)
	if err != nil {
		return fmt.Errorf("read candidate prompt: %w", err)
	}
	artifacts := normalizedRound{
		engineRound: round, inputTrain: train, train: execution.candidateTrain, validation: validation,
		baselineValidation: baselineValidation,
		delta:              delta, decision: decision, candidate: candidate,
	}
	reportRound, err := buildRoundReport(state, execution, artifacts)
	if err != nil {
		return fmt.Errorf("build attempt %d report: %w", execution.attempt, err)
	}
	if err := regression.AppendRound(state.report, reportRound); err != nil {
		return fmt.Errorf("append attempt %d: %w", execution.attempt, err)
	}
	if decision.Accepted {
		state.acceptedProfile = round.OutputProfile
		state.acceptedValidation = validation
		state.acceptedPrompt = candidate
	}
	return nil
}

func normalizeEvaluationRound(
	result *promptiterengine.RunResult,
	round promptiterengine.RoundResult,
) (*regression.EvaluationResult, *regression.EvaluationResult, *regression.EvaluationResult, error) {
	train, err := regression.NormalizeEvaluation(round.Train)
	if err != nil {
		return nil, nil, nil, err
	}
	validation, err := regression.NormalizeEvaluation(round.Validation)
	if err != nil {
		return nil, nil, nil, err
	}
	baseline, err := regression.NormalizeEvaluation(result.BaselineValidation)
	if err != nil {
		return nil, nil, nil, err
	}
	return train, validation, baseline, nil
}

func initializePipelineState(
	ctx context.Context,
	request pipelineInitRequest,
) (*pipelineState, error) {
	train, err := request.runtime.evaluateProfile(ctx, request.cfg.TrainEvalSetID, nil)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline train: %w", err)
	}
	validation, err := request.runtime.evaluateProfile(ctx, request.cfg.ValidationEvalSetID, nil)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline validation: %w", err)
	}
	attribution := regression.MergeAttributions(
		regression.Attribute(train, request.catalog), regression.Attribute(validation, request.catalog),
	)
	report, err := regression.NewReport(regression.RunMetadata{
		Seed: request.cfg.Seed, Mode: offlineMode, Model: fakeModelName,
		StartedAt: request.startedAt, ConfigPath: request.cfg.ConfigPath,
		ConfigSHA256: request.cfg.ConfigSHA256, FakeEngine: fakeEngineVersion,
	}, train, validation, attribution)
	if err != nil {
		return nil, fmt.Errorf("create report: %w", err)
	}
	report.Usage = regression.AddUsage(train.Usage, validation.Usage)
	prompt := regression.PromptRecord{SurfaceID: request.cfg.TargetSurfaceID, Text: request.baselineText}
	return &pipelineState{
		report: report, originalValidation: validation,
		acceptedValidation: validation, baselinePrompt: prompt, acceptedPrompt: prompt, catalog: request.catalog,
	}, nil
}

func buildRoundReport(
	state *pipelineState,
	execution *roundExecution,
	artifacts normalizedRound,
) (regression.RoundReport, error) {
	if state == nil || execution == nil {
		return regression.RoundReport{}, errors.New("round report context is incomplete")
	}
	if artifacts.inputTrain == nil || artifacts.baselineValidation == nil || artifacts.train == nil ||
		artifacts.validation == nil || artifacts.delta == nil || artifacts.decision == nil {
		return regression.RoundReport{}, errors.New("round report evaluation artifacts are incomplete")
	}
	return regression.RoundReport{
		Attempt: execution.attempt, InputPrompt: state.acceptedPrompt, CandidatePrompt: artifacts.candidate,
		Patches: patchRecords(artifacts.engineRound.Patches), Train: artifacts.train,
		Validation: artifacts.validation, Delta: artifacts.delta,
		Attribution: regression.MergeAttributions(
			regression.Attribute(artifacts.train, state.catalog),
			regression.Attribute(artifacts.validation, state.catalog),
		),
		RegressionGateDecision: *artifacts.decision,
		Duration:               execution.duration, Usage: roundUsage(artifacts),
	}, nil
}

func failPipeline(
	state *pipelineState,
	startedAt time.Time,
	cause error,
) (*regression.Report, error) {
	if state == nil || state.report == nil || cause == nil {
		return nil, errors.New("failed pipeline state is incomplete")
	}
	finalizeErr := finalizePipeline(state, startedAt, cause)
	return state.report, errors.Join(cause, finalizeErr)
}

func finalizePipeline(state *pipelineState, startedAt time.Time, runErr error) error {
	if state == nil || state.report == nil {
		return errors.New("pipeline state is incomplete")
	}
	state.report.Run.Duration = time.Since(startedAt)
	state.report.Run.Status = regression.RunStatusCompleted
	state.report.Run.Error = ""
	if runErr != nil {
		state.report.Run.Status = regression.RunStatusFailed
		state.report.Run.Error = runErr.Error()
		return regression.DisableWriteback(state.report, "pipeline failed; writeback disabled")
	}
	return regression.SetWriteback(state.report, state.baselinePrompt, state.acceptedPrompt)
}

func markReportFailed(report *regression.Report, runErr error) {
	if report == nil || runErr == nil {
		return
	}
	report.Run.Status = regression.RunStatusFailed
	if disableErr := regression.DisableWriteback(report, "pipeline cleanup failed; writeback disabled"); disableErr != nil {
		runErr = errors.Join(runErr, disableErr)
	}
	if report.Run.Error == "" {
		report.Run.Error = runErr.Error()
		return
	}
	report.Run.Error = errors.Join(errors.New(report.Run.Error), runErr).Error()
}

func roundUsage(artifacts normalizedRound) regression.UsageSummary {
	usage := regression.AddUsage(artifacts.inputTrain.Usage, artifacts.baselineValidation.Usage)
	usage = regression.AddUsage(usage, artifacts.train.Usage)
	return regression.AddUsage(usage, artifacts.validation.Usage)
}

func promptFromProfile(profile *promptiter.Profile, surfaceID string) (regression.PromptRecord, error) {
	if profile == nil {
		return regression.PromptRecord{}, errors.New("profile is nil")
	}
	for _, override := range profile.Overrides {
		if override.SurfaceID != surfaceID {
			continue
		}
		if override.Value.Text == nil {
			return regression.PromptRecord{}, errors.New("instruction override text is nil")
		}
		return regression.PromptRecord{SurfaceID: surfaceID, Text: *override.Value.Text}, nil
	}
	return regression.PromptRecord{}, fmt.Errorf("profile has no override for %q", surfaceID)
}

func patchRecords(patches *promptiter.PatchSet) []regression.PatchRecord {
	if patches == nil {
		return []regression.PatchRecord{}
	}
	result := make([]regression.PatchRecord, 0, len(patches.Patches))
	for _, patch := range patches.Patches {
		text := ""
		if patch.Value.Text != nil {
			text = *patch.Value.Text
		}
		result = append(result, regression.PatchRecord{
			SurfaceID: patch.SurfaceID, Text: text, Reason: patch.Reason,
		})
	}
	return result
}

func loadAttributionCatalog(path string) (regression.AttributionCatalog, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return regression.AttributionCatalog{}, fmt.Errorf("read metrics: %w", err)
	}
	var metrics []*metric.EvalMetric
	if err := json.Unmarshal(data, &metrics); err != nil {
		return regression.AttributionCatalog{}, fmt.Errorf("decode metrics: %w", err)
	}
	catalog, err := regression.CatalogFromMetrics(metrics)
	if err != nil {
		return regression.AttributionCatalog{}, fmt.Errorf("validate metrics: %w", err)
	}
	return catalog, nil
}
