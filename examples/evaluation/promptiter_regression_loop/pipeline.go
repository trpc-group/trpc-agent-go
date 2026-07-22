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
	"strings"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/internal/regression"
)

const (
	offlineMode = "deterministic-fake-model"
	oneRound    = 1
)

func runPipeline(
	ctx context.Context,
	cfg *config,
	now func() time.Time,
) (report *regression.Report, resultErr error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if cfg == nil {
		return nil, errors.New("config is nil")
	}
	if now == nil {
		return nil, errors.New("clock is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	baselinePrompt, err := loadBaselinePrompt(cfg.BaselinePromptSource)
	if err != nil {
		return nil, err
	}
	runtime, err := buildRuntime(cfg, baselinePrompt)
	if err != nil {
		return nil, err
	}
	startedAt := now().UTC()
	defer func() {
		if closeErr := runtime.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, closeErr)
			if report != nil {
				report.Run.Duration = nonNegativeDuration(now().UTC().Sub(startedAt))
				_ = regression.FinalizeReport(report, resultErr)
			}
		}
	}()
	catalog, err := loadAttributionCatalog(filepath.Join(cfg.DataDir, "metrics.json"))
	if err != nil {
		return nil, err
	}
	baselineTrain, err := runtime.evaluateProfile(ctx, cfg.TrainEvalSetID, nil)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline train: %w", err)
	}
	baselineValidation, err := runtime.evaluateProfile(ctx, cfg.ValidationEvalSetID, nil)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline validation: %w", err)
	}
	metadata := regression.RunMetadata{
		ID:           runID(startedAt, cfg.ConfigSHA256),
		Status:       "running",
		Mode:         offlineMode,
		Seed:         cfg.Seed,
		Model:        fakeModelName,
		ConfigPath:   cfg.ConfigPath,
		ConfigSHA256: cfg.ConfigSHA256,
		StartedAt:    startedAt,
	}
	report, err = regression.NewReport(
		metadata,
		baselineTrain,
		baselineValidation,
		regression.MergeAttributions(
			regression.AttributeFailures(baselineTrain, catalog),
			regression.AttributeFailures(baselineValidation, catalog),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create optimization report: %w", err)
	}
	acceptedProfile := (*promptiter.Profile)(nil)
	acceptedValidation := baselineValidation
	acceptedPrompt := regression.PromptRecord{SurfaceID: cfg.TargetSurfaceID, Text: baselinePrompt}
	for index, candidateText := range cfg.CandidatePrompts {
		if err := ctx.Err(); err != nil {
			return failPipeline(report, startedAt, now, err)
		}
		attempt := index + 1
		roundStarted := now().UTC()
		engine, err := runtime.engineForAttempt(ctx, candidateText, attempt)
		if err != nil {
			return failPipeline(report, startedAt, now, err)
		}
		engineResult, err := engine.Run(ctx, buildRunRequest(cfg, acceptedProfile))
		if err != nil {
			return failPipeline(report, startedAt, now,
				fmt.Errorf("run PromptIter attempt %d: %w", attempt, err))
		}
		engineRound, err := requireSingleRound(engineResult, attempt)
		if err != nil {
			return failPipeline(report, startedAt, now, err)
		}
		candidatePrompt, err := promptFromProfile(engineRound.OutputProfile, cfg.TargetSurfaceID)
		if err != nil {
			return failPipeline(report, startedAt, now,
				fmt.Errorf("read candidate attempt %d: %w", attempt, err))
		}
		candidateTrain, err := runtime.evaluateProfile(ctx, cfg.TrainEvalSetID, engineRound.OutputProfile)
		if err != nil {
			return failPipeline(report, startedAt, now,
				fmt.Errorf("evaluate candidate train attempt %d: %w", attempt, err))
		}
		candidateValidation, err := runtime.evaluateProfile(ctx, cfg.ValidationEvalSetID, engineRound.OutputProfile)
		if err != nil {
			return failPipeline(report, startedAt, now,
				fmt.Errorf("evaluate candidate validation attempt %d: %w", attempt, err))
		}
		delta, err := regression.Compare(acceptedValidation, candidateValidation)
		if err != nil {
			return failPipeline(report, startedAt, now,
				fmt.Errorf("compare candidate attempt %d: %w", attempt, err))
		}
		baselineDelta, err := regression.Compare(baselineValidation, candidateValidation)
		if err != nil {
			return failPipeline(report, startedAt, now,
				fmt.Errorf("compare candidate attempt %d with original baseline: %w", attempt, err))
		}
		decision, err := regression.Decide(cfg.Gate, regression.GateInput{
			OriginalBaseline: baselineValidation,
			AcceptedBaseline: acceptedValidation,
			Candidate:        candidateValidation,
		})
		if err != nil {
			return failPipeline(report, startedAt, now,
				fmt.Errorf("gate candidate attempt %d: %w", attempt, err))
		}
		patches, err := patchRecords(engineRound.Patches)
		if err != nil {
			return failPipeline(report, startedAt, now,
				fmt.Errorf("audit PromptIter patches attempt %d: %w", attempt, err))
		}
		usage, err := engineRunUsage(engineResult)
		if err != nil {
			return failPipeline(report, startedAt, now,
				fmt.Errorf("audit PromptIter attempt %d: %w", attempt, err))
		}
		usage = regression.AddUsage(usage, candidateTrain.Usage)
		usage = regression.AddUsage(usage, candidateValidation.Usage)
		round := regression.RoundReport{
			Attempt:            attempt,
			InputPrompt:        acceptedPrompt,
			CandidatePrompt:    candidatePrompt,
			PromptIterAccepted: engineRound.Acceptance != nil && engineRound.Acceptance.Accepted,
			Train:              candidateTrain,
			Validation:         candidateValidation,
			Delta:              delta,
			BaselineDelta:      baselineDelta,
			Attribution: regression.MergeAttributions(
				regression.AttributeFailures(candidateTrain, catalog),
				regression.AttributeFailures(candidateValidation, catalog),
			),
			Gate:     *decision,
			Patches:  patches,
			Usage:    usage,
			Duration: nonNegativeDuration(now().UTC().Sub(roundStarted)),
		}
		if err := regression.AppendRound(report, round); err != nil {
			return failPipeline(report, startedAt, now,
				fmt.Errorf("append attempt %d: %w", attempt, err))
		}
		if decision.Accepted {
			acceptedProfile = engineRound.OutputProfile
			acceptedValidation = candidateValidation
			acceptedPrompt = candidatePrompt
		}
	}
	report.Run.Duration = nonNegativeDuration(now().UTC().Sub(startedAt))
	if err := regression.FinalizeReport(report, nil); err != nil {
		return report, err
	}
	return report, nil
}

func buildRunRequest(cfg *config, profile *promptiter.Profile) *promptiterengine.RunRequest {
	return &promptiterengine.RunRequest{
		Train:             []promptiterengine.EvalSetInput{{EvalSetID: cfg.TrainEvalSetID}},
		Validation:        []promptiterengine.EvalSetInput{{EvalSetID: cfg.ValidationEvalSetID}},
		InitialProfile:    profile,
		EvaluationOptions: promptiterengine.EvaluationOptions{EvalCaseParallelism: caseParallelism},
		AcceptancePolicy:  promptiterengine.AcceptancePolicy{MinScoreGain: 0},
		MaxRounds:         oneRound,
		TargetSurfaceIDs:  []string{cfg.TargetSurfaceID},
	}
}

func requireSingleRound(
	result *promptiterengine.RunResult,
	attempt int,
) (*promptiterengine.RoundResult, error) {
	if result == nil {
		return nil, fmt.Errorf("PromptIter attempt %d returned nil result", attempt)
	}
	if result.Status != promptiterengine.RunStatusSucceeded {
		return nil, fmt.Errorf("PromptIter attempt %d status is %q", attempt, result.Status)
	}
	if len(result.Rounds) != oneRound {
		return nil, fmt.Errorf("PromptIter attempt %d returned %d rounds, want one", attempt, len(result.Rounds))
	}
	round := &result.Rounds[0]
	if round.OutputProfile == nil || round.Validation == nil || round.Train == nil || round.Acceptance == nil {
		return nil, fmt.Errorf("PromptIter attempt %d result is incomplete", attempt)
	}
	return round, nil
}

func promptFromProfile(profile *promptiter.Profile, surfaceID string) (regression.PromptRecord, error) {
	if profile == nil {
		return regression.PromptRecord{}, errors.New("candidate profile is nil")
	}
	for _, override := range profile.Overrides {
		if override.SurfaceID != surfaceID {
			continue
		}
		text, err := surfaceValueText(override.Value)
		if err != nil {
			return regression.PromptRecord{}, err
		}
		return regression.PromptRecord{SurfaceID: surfaceID, Text: text}, nil
	}
	return regression.PromptRecord{}, fmt.Errorf("candidate profile has no surface %q", surfaceID)
}

func surfaceValueText(value astructure.SurfaceValue) (string, error) {
	switch {
	case value.Text != nil:
		return *value.Text, nil
	case len(value.Tools) == 1:
		return value.Tools[0].Description, nil
	default:
		return "", errors.New("candidate surface has no single reportable text value")
	}
}

func patchRecords(patches *promptiter.PatchSet) ([]regression.PatchRecord, error) {
	if patches == nil {
		return []regression.PatchRecord{}, nil
	}
	result := make([]regression.PatchRecord, 0, len(patches.Patches))
	for _, patch := range patches.Patches {
		text, err := surfaceValueText(patch.Value)
		if err != nil {
			return nil, fmt.Errorf("surface %q: %w", patch.SurfaceID, err)
		}
		result = append(result, regression.PatchRecord{
			SurfaceID: patch.SurfaceID,
			Text:      text,
			Reason:    patch.Reason,
		})
	}
	return result, nil
}

func engineRunUsage(result *promptiterengine.RunResult) (regression.Usage, error) {
	if result == nil || len(result.Rounds) != oneRound {
		return regression.Usage{}, errors.New("PromptIter run result is incomplete")
	}
	usage := regression.Usage{Measured: true}
	parts := []*promptiterengine.EvaluationResult{
		result.BaselineValidation,
		result.Rounds[0].Train,
		result.Rounds[0].Validation,
	}
	for _, part := range parts {
		normalized, err := regression.NormalizeEngineEvaluation(part)
		if err != nil {
			return regression.Usage{}, err
		}
		usage = regression.AddUsage(usage, normalized.Usage)
	}
	return usage, nil
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
		return regression.AttributionCatalog{}, fmt.Errorf("build attribution catalog: %w", err)
	}
	return catalog, nil
}

func failPipeline(
	report *regression.Report,
	startedAt time.Time,
	now func() time.Time,
	cause error,
) (*regression.Report, error) {
	if report == nil {
		return nil, cause
	}
	report.Run.Duration = nonNegativeDuration(now().UTC().Sub(startedAt))
	finalizeErr := regression.FinalizeReport(report, cause)
	return report, errors.Join(cause, finalizeErr)
}

func runID(startedAt time.Time, configHash string) string {
	hash := strings.TrimSpace(configHash)
	if len(hash) > 12 {
		hash = hash[:12]
	}
	return startedAt.UTC().Format("20060102T150405.000000000Z") + "-" + hash
}

func nonNegativeDuration(value time.Duration) time.Duration {
	if value < 0 {
		return 0
	}
	return value
}
