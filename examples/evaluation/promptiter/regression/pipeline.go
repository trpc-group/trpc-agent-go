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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const reportSchemaVersion = "1.0"

type pipelineOptions struct {
	ConfigPath  string
	OutputDir   string
	WritePrompt bool
}

func runPipeline(
	ctx context.Context,
	options pipelineOptions,
) (optimizationReport, error) {
	startedAt := time.Now().UTC()
	config, err := loadConfig(options.ConfigPath)
	if err != nil {
		return optimizationReport{}, err
	}
	trainSet, validationSet, metrics, baselinePrompt, err := loadEvaluationInputs(config)
	if err != nil {
		return optimizationReport{}, err
	}
	runtime, err := newEvaluationRuntime(ctx, config, trainSet, validationSet, metrics)
	if err != nil {
		return optimizationReport{}, err
	}
	defer runtime.close()

	baselineTrain, err := runtime.evaluate(ctx, trainSet.EvalSetID, baselinePrompt)
	if err != nil {
		return optimizationReport{}, fmt.Errorf("evaluate baseline train set: %w", err)
	}
	baselineValidation, err := runtime.evaluate(ctx, validationSet.EvalSetID, baselinePrompt)
	if err != nil {
		return optimizationReport{}, fmt.Errorf("evaluate baseline validation set: %w", err)
	}
	baseline := promptEvaluation{
		Prompt:     baselinePrompt,
		Train:      baselineTrain,
		Validation: baselineValidation,
	}

	current := baseline
	selected := baseline
	selectedDecision := gateDecision{
		Accepted: false,
		Reasons:  []string{"no candidate passed the acceptance gate"},
	}
	selected.CandidateID = "baseline"
	rounds := make([]roundAudit, 0, config.MaxRounds)
	var candidateCosts costSummary
	evaluatedRounds := 0
	for _, candidateConfig := range config.Candidates {
		if evaluatedRounds >= config.MaxRounds {
			break
		}
		if !candidateTargetsCurrentFailures(candidateConfig, current.Train) {
			continue
		}
		roundStartedAt := time.Now()
		proposal, err := buildPromptCandidate(
			current.Prompt,
			candidateConfig,
			config.TargetSurfaceID,
		)
		if err != nil {
			return optimizationReport{}, err
		}
		if len(proposal.Profile.Overrides) != 1 ||
			proposal.Profile.Overrides[0].Value.Text == nil {
			return optimizationReport{}, fmt.Errorf("candidate %q prompt patch is empty", proposal.ID)
		}
		evaluatedRounds++
		candidatePrompt := *proposal.Profile.Overrides[0].Value.Text
		train, err := runtime.evaluate(ctx, trainSet.EvalSetID, candidatePrompt)
		if err != nil {
			return optimizationReport{}, fmt.Errorf(
				"evaluate candidate %q train set: %w",
				proposal.ID,
				err,
			)
		}
		validation, err := runtime.evaluate(ctx, validationSet.EvalSetID, candidatePrompt)
		if err != nil {
			return optimizationReport{}, fmt.Errorf(
				"evaluate candidate %q validation set: %w",
				proposal.ID,
				err,
			)
		}
		delta, err := compareEvaluations(current.Validation, validation)
		if err != nil {
			return optimizationReport{}, fmt.Errorf(
				"compare candidate %q validation: %w",
				proposal.ID,
				err,
			)
		}
		decision := decideGate(config.Gate, current.Validation, validation, delta)
		candidate := promptEvaluation{
			Round:       evaluatedRounds,
			CandidateID: proposal.ID,
			Prompt:      candidatePrompt,
			Train:       train,
			Validation:  validation,
		}
		rounds = append(rounds, roundAudit{
			Round:          evaluatedRounds,
			CandidateID:    proposal.ID,
			Prompt:         candidatePrompt,
			PatchReason:    proposal.Reason,
			Train:          train,
			Validation:     validation,
			Delta:          delta,
			Decision:       decision,
			DurationMillis: time.Since(roundStartedAt).Milliseconds(),
		})
		addCost(&candidateCosts, train.Cost)
		addCost(&candidateCosts, validation.Cost)
		if decision.Accepted {
			current = candidate
			selected = candidate
			selectedDecision = decision
		}
	}
	if evaluatedRounds == 0 {
		return optimizationReport{}, errors.New("no candidate targeted the current training failures")
	}

	finalDelta, err := compareEvaluations(baseline.Validation, selected.Validation)
	if err != nil {
		return optimizationReport{}, fmt.Errorf("compare selected candidate to baseline: %w", err)
	}
	baselineCosts := costSummary{}
	addCost(&baselineCosts, baseline.Train.Cost)
	addCost(&baselineCosts, baseline.Validation.Cost)
	totalCosts := baselineCosts
	addCost(&totalCosts, candidateCosts)
	report := optimizationReport{
		SchemaVersion: reportSchemaVersion,
		RunID: deterministicID(
			config.Seed,
			trainSet.EvalSetID,
			validationSet.EvalSetID,
			config.TargetSurfaceID,
			baselinePrompt,
		),
		Inputs: inputAudit{
			TrainEvalSet:      portableInputPath(config.Inputs.TrainEvalSet),
			ValidationEvalSet: portableInputPath(config.Inputs.ValidationEvalSet),
			Metrics:           portableInputPath(config.Inputs.Metrics),
			PromptSource:      portableInputPath(config.Inputs.PromptSource),
			Config:            portableInputPath(options.ConfigPath),
		},
		Configuration: configurationAudit{
			TargetSurfaceID: config.TargetSurfaceID,
			MaxRounds:       config.MaxRounds,
			Gate:            config.Gate,
		},
		Runtime: runtimeAudit{
			Seed:           config.Seed,
			Engine:         "evaluation-service+promptiter-deterministic",
			Model:          config.FakeModel,
			StartedAt:      startedAt,
			DurationMillis: time.Since(startedAt).Milliseconds(),
		},
		Baseline:     baseline,
		Candidate:    selected,
		Delta:        finalDelta,
		GateDecision: selectedDecision,
		FailureAttribution: attributionSummary{
			Baseline:  summarizeFailures(baseline.Train, baseline.Validation),
			Candidate: summarizeFailures(selected.Train, selected.Validation),
		},
		CostLatency: costLatencySummary{
			Baseline:           baselineCosts,
			Candidates:         candidateCosts,
			Total:              totalCosts,
			TotalLatencyMillis: evaluationLatency(baseline.Train, baseline.Validation) + roundsLatency(rounds),
		},
		Rounds: rounds,
	}
	if err := writeReports(options.OutputDir, report); err != nil {
		return optimizationReport{}, err
	}
	if options.WritePrompt && report.GateDecision.Accepted {
		if err := os.WriteFile(config.Inputs.PromptSource, []byte(strings.TrimSpace(selected.Prompt)+"\n"), 0o644); err != nil {
			return optimizationReport{}, fmt.Errorf("write accepted prompt: %w", err)
		}
	}
	return report, nil
}

func summarizeFailures(summaries ...evaluationSummary) map[failureCategory]int {
	counts := make(map[failureCategory]int)
	for _, summary := range summaries {
		for _, evalCase := range summary.Cases {
			if len(evalCase.FailureAttributions) == 0 {
				continue
			}
			counts[evalCase.FailureAttributions[0].Category]++
		}
	}
	return counts
}

func portableInputPath(path string) string {
	return filepath.ToSlash(filepath.Join(filepath.Base(filepath.Dir(path)), filepath.Base(path)))
}

func evaluationLatency(summaries ...evaluationSummary) int64 {
	var total int64
	for _, summary := range summaries {
		total += summary.LatencyMillis
	}
	return total
}

func roundsLatency(rounds []roundAudit) int64 {
	var total int64
	for _, round := range rounds {
		total += round.Train.LatencyMillis + round.Validation.LatencyMillis
	}
	return total
}
