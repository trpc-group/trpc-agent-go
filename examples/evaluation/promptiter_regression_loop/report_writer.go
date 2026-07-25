//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"

	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/pipeline"
)

const (
	reportJSONName = "optimization_report.json"
	reportMDName   = "optimization_report.md"
)

// writeReport assembles the audit report from the run's snapshots + gate decision and writes both
// optimization_report.json and optimization_report.md into the output directory.
func writeReport(
	cfg runConfig,
	result *engine.RunResult,
	baseline, candidate *evalSnapshot,
	decision pipeline.GateDecision,
	candidateInstruction string,
	elapsed time.Duration,
) error {
	surfaceID := targetSurfaceID()
	report := pipeline.BuildReport(pipeline.ReportInput{
		GeneratedAt:          time.Now().UTC().Format(time.RFC3339),
		App:                  appName,
		ModelSource:          string(cfg.ModelSource),
		TargetSurfaceID:      surfaceID,
		BaselineInstruction:  cfg.CandidateInstruction,
		CandidateInstruction: candidateInstruction,
		EngineAccepted:       engineAccepted(result),
		Config:               configSnapshot(cfg),
		Determinism:          determinism(cfg),
		CostLatency:          costLatency(cfg, baseline, candidate, elapsed),
		BaselineTrain:        baseline.train,
		BaselineValidation:   baseline.validation,
		CandidateTrain:       candidate.train,
		CandidateValidation:  candidate.validation,
		Gate:                 decision,
		Rounds:               roundSummaries(result, surfaceID, cfg.CandidateInstruction),
	})
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	jsonBytes, err := report.JSON()
	if err != nil {
		return fmt.Errorf("marshal report json: %w", err)
	}
	jsonPath := filepath.Join(cfg.OutputDir, reportJSONName)
	if err := os.WriteFile(jsonPath, append(jsonBytes, '\n'), 0o644); err != nil {
		return fmt.Errorf("write report json: %w", err)
	}
	mdPath := filepath.Join(cfg.OutputDir, reportMDName)
	if err := os.WriteFile(mdPath, []byte(report.Markdown()), 0o644); err != nil {
		return fmt.Errorf("write report markdown: %w", err)
	}
	fmt.Printf("Audit report written to %s and %s\n", jsonPath, mdPath)
	return nil
}

// configSnapshot captures the run configuration for the audit report.
func configSnapshot(cfg runConfig) pipeline.ConfigSnapshot {
	snapshot := pipeline.ConfigSnapshot{
		ModelSource:            string(cfg.ModelSource),
		MaxRounds:              cfg.MaxRounds,
		MinScoreGain:           cfg.MinScoreGain,
		GateMinValidationGain:  cfg.GateMinValidationGain,
		MaxCandidateModelCalls: cfg.MaxCandidateModelCalls,
		TargetScore:            cfg.TargetScore,
		KeyCaseIDs:             cfg.KeyCaseIDs,
		BaselinePromptFile:     cfg.BaselinePromptFile,
		DataDir:                cfg.DataDir,
	}
	if cfg.ModelSource == modelSourceOpenAI {
		snapshot.CandidateModel = cfg.CandidateModelName
		snapshot.JudgeModel = cfg.JudgeModelName
		snapshot.WorkerModel = cfg.WorkerModelName
	}
	if cfg.ModelSource == modelSourceFake {
		snapshot.FixtureFile = fixturePath(cfg)
	}
	return snapshot
}

// determinism records the reproducibility basis. The fake source is fully deterministic and uses
// no RNG, so there is no seed; the openai source depends on the endpoint's sampling.
func determinism(cfg runConfig) pipeline.Determinism {
	if cfg.ModelSource == modelSourceFake {
		return pipeline.Determinism{
			Deterministic: true,
			Seed:          nil,
			Note:          "scripted fake model + deterministic collaborators; no RNG",
		}
	}
	return pipeline.Determinism{
		Deterministic: false,
		Seed:          nil,
		Note:          "openai source: reproducibility depends on the endpoint's sampling",
	}
}

// costLatency summarizes the run cost. In fake mode there is no monetary token cost, so the
// candidate model-call count and evaluation latency stand in for it.
func costLatency(cfg runConfig, baseline, candidate *evalSnapshot, elapsed time.Duration) pipeline.CostLatency {
	note := ""
	if cfg.ModelSource == modelSourceFake {
		note = "fake mode: no monetary token cost; candidate model calls and latency are the cost proxy"
	}
	return pipeline.CostLatency{
		TotalWallClockMs:    elapsed.Milliseconds(),
		BaselineEvalMs:      snapshotLatency(baseline).Milliseconds(),
		CandidateEvalMs:     snapshotLatency(candidate).Milliseconds(),
		CandidateModelCalls: cfg.modelCalls.count(),
		Note:                note,
	}
}

func snapshotLatency(s *evalSnapshot) time.Duration {
	var total time.Duration
	if s.train != nil {
		total += s.train.ExecutionTime
	}
	if s.validation != nil {
		total += s.validation.ExecutionTime
	}
	return total
}

// engineAccepted reports whether the engine accepted at least one optimization patch.
func engineAccepted(result *engine.RunResult) bool {
	for _, round := range result.Rounds {
		if round.Acceptance != nil && round.Acceptance.Accepted {
			return true
		}
	}
	return false
}

// roundSummaries flattens the engine's per-round results into report-friendly summaries, including
// each round's candidate instruction extracted from its output profile.
func roundSummaries(result *engine.RunResult, surfaceID, fallbackInstruction string) []pipeline.RoundSummary {
	rounds := make([]pipeline.RoundSummary, 0, len(result.Rounds))
	for _, round := range result.Rounds {
		summary := pipeline.RoundSummary{
			Round:           round.Round,
			Instruction:     profileInstruction(round.OutputProfile, surfaceID, fallbackInstruction),
			TrainScore:      evaluationResultScore(round.Train),
			ValidationScore: evaluationResultScore(round.Validation),
		}
		if round.Acceptance != nil {
			summary.Accepted = round.Acceptance.Accepted
			summary.ScoreDelta = round.Acceptance.ScoreDelta
		}
		if round.Stop != nil {
			summary.Stopped = round.Stop.ShouldStop
			summary.StopReason = round.Stop.Reason
		}
		rounds = append(rounds, summary)
	}
	return rounds
}

// profileInstruction extracts the instruction text for surfaceID from a profile, falling back to
// the provided instruction when the profile has no override for that surface.
func profileInstruction(profile *promptiter.Profile, surfaceID, fallback string) string {
	if profile == nil {
		return fallback
	}
	for _, override := range profile.Overrides {
		if override.SurfaceID == surfaceID && override.Value.Text != nil {
			return *override.Value.Text
		}
	}
	return fallback
}
