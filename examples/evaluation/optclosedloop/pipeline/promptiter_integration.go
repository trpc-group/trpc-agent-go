//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// This file provides the integration bridge between the optclosedloop pipeline
// and the real evaluation/workflow/promptiter engine. The pipeline uses these
// functions when running in trace_mode or real mode to delegate optimization
// to the native PromptIter engine. In fake_deterministic mode the pipeline
// uses its own deterministic simulator but still produces the same promptiter
// types (PatchSet, Profile, TerminalLoss) via the conversion functions in
// attribution.go and optimizer.go.

package pipeline

import (
	"context"
	"fmt"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// PromptIterAdapter wraps the real promptiter engine.Engine and adapts it to
// the pipeline's internal type contracts. In fake_deterministic mode this
// adapter is not used; the pipeline's PromptOptimizer produces candidates
// directly. In trace_mode and real mode the adapter delegates to the engine.
type PromptIterAdapter struct {
	engine promptiterengine.Engine
}

// NewPromptIterAdapter creates an adapter around a real promptiter engine.
// The engine must be constructed with a real evaluation.AgentEvaluator,
// backwarder, aggregator, and optimizer as shown in the promptiter/syncrun
// example.
func NewPromptIterAdapter(engine promptiterengine.Engine) *PromptIterAdapter {
	return &PromptIterAdapter{engine: engine}
}

// RunnerOptions bundles the runners and parallelism settings needed by the
// real promptiter engine. It is populated by the caller (main.go) when
// running in trace_mode or real mode.
type RunnerOptions struct {
	Teacher            runner.Runner
	Judge              runner.Runner
	EvaluationOptions  promptiterengine.EvaluationOptions
	BackwardOptions    promptiterengine.BackwardOptions
	AggregationOptions promptiterengine.AggregationOptions
	OptimizerOptions   promptiterengine.OptimizerOptions
}

// BuildRunRequest constructs a real promptiterengine.RunRequest from the
// pipeline configuration. This is the primary integration point: the pipeline
// builds the request, delegates to the engine, then converts the result back.
//
// The caller must supply teacher and judge runners. In trace_mode the teacher
// runner is not invoked (evalsets use EvalModeTrace); in real mode both must
// be live runners backed by actual model clients.
func BuildRunRequest(
	cfg Config,
	targetSurfaceIDs []string,
	runnerOpts *RunnerOptions,
) *promptiterengine.RunRequest {
	req := &promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{{
			EvalSetID: cfg.TrainSetID,
		}},
		Validation: []promptiterengine.EvalSetInput{{
			EvalSetID: cfg.ValSetID,
		}},
		MaxRounds:        cfg.MaxRounds,
		TargetSurfaceIDs: targetSurfaceIDs,
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: cfg.GateConfig.MinValidationScoreGain,
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: cfg.MaxRounds,
		},
	}
	if runnerOpts != nil {
		req.Teacher = runnerOpts.Teacher
		req.Judge = runnerOpts.Judge
		req.EvaluationOptions = runnerOpts.EvaluationOptions
		req.BackwardOptions = runnerOpts.BackwardOptions
		req.AggregationOptions = runnerOpts.AggregationOptions
		req.OptimizerOptions = runnerOpts.OptimizerOptions
	}
	return req
}

// BuildBaselineProfile creates a real promptiter.Profile from the baseline
// prompt map. Every surface in the map becomes one SurfaceOverride with a
// Text SurfaceValue. This profile can be passed as InitialProfile to the
// promptiter engine.
func BuildBaselineProfile(prompts map[string]string) *promptiter.Profile {
	overrides := make([]promptiter.SurfaceOverride, 0, len(prompts))
	for surfaceID, text := range prompts {
		t := text
		overrides = append(overrides, promptiter.SurfaceOverride{
			SurfaceID: surfaceID,
			Value:     astructure.SurfaceValue{Text: &t},
		})
	}
	return &promptiter.Profile{
		StructureID: "optclosedloop-baseline",
		Overrides:   overrides,
	}
}

// ConvertRunResult converts a real promptiter engine RunResult into the
// pipeline's OptimizationReport format. This enables the pipeline to use the
// same audit, gate, and reporting infrastructure regardless of whether the
// optimization was run by the deterministic simulator or the real engine.
func ConvertRunResult(
	runResult *promptiterengine.RunResult,
	cfg Config,
) (*OptimizationReport, error) {
	if runResult == nil {
		return nil, fmt.Errorf("promptiter run result is nil")
	}
	now := time.Now()
	report := &OptimizationReport{
		AppName:         cfg.AppName,
		PipelineVersion: PipelineVersion,
		Mode:            string(cfg.Mode),
		StartedAt:       now,
		FinishedAt:      now,
		RandomSeed:      cfg.RandomSeed,
		GateConfig:      cfg.GateConfig,
		PromptTargets:   buildPromptTargets(cfg.PromptsBaseline, cfg.TargetSurfaceIDs),
		Rounds:          make([]RoundRecord, 0, len(runResult.Rounds)),
		PromptsFinal:    clonePrompts(cfg.PromptsBaseline),
	}
	if runResult.AcceptedProfile != nil {
		report.PromptsFinal = profileToPromptMap(runResult.AcceptedProfile)
	}
	report.FinalAccepted = runResult.AcceptedProfile != nil
	for _, rr := range runResult.Rounds {
		roundRec := RoundRecord{
			Round:      rr.Round,
			Timestamp:  now,
			RandomSeed: cfg.RandomSeed + int64(rr.Round*31),
		}
		if rr.Patches != nil {
			roundRec.Candidate = patchSetToCandidate(rr.Patches, rr.Round, cfg.Mode)
		}
		if rr.Acceptance != nil {
			roundRec.Acceptance = &AcceptanceDecision{
				Accepted:   rr.Acceptance.Accepted,
				ScoreDelta: rr.Acceptance.ScoreDelta,
				Reasons:    []string{rr.Acceptance.Reason},
			}
		}
		report.Rounds = append(report.Rounds, roundRec)
	}
	return report, nil
}

// RunEngineWithAdapter executes the real promptiter engine and converts the
// result back to the pipeline format. This is the entry point for trace_mode
// and real mode.
func (a *PromptIterAdapter) RunEngineWithAdapter(
	ctx context.Context,
	cfg Config,
	targetSurfaceIDs []string,
	runnerOpts *RunnerOptions,
) (*OptimizationReport, error) {
	req := BuildRunRequest(cfg, targetSurfaceIDs, runnerOpts)
	req.InitialProfile = BuildBaselineProfile(cfg.PromptsBaseline)
	runResult, err := a.engine.Run(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("promptiter engine run: %w", err)
	}
	return ConvertRunResult(runResult, cfg)
}

// patchSetToCandidate converts a real promptiter.PatchSet to the pipeline's
// PromptCandidate type.
func patchSetToCandidate(ps *promptiter.PatchSet, round int, mode Mode) *PromptCandidate {
	if ps == nil {
		return nil
	}
	patches := make(map[string]string, len(ps.Patches))
	for _, p := range ps.Patches {
		if p.Value.Text != nil {
			patches[p.SurfaceID] = *p.Value.Text
		}
	}
	return &PromptCandidate{
		CandidateID: fmt.Sprintf("cand_r%d_engine", round),
		Round:       round,
		GeneratedBy: "promptiter_engine_" + string(mode),
		Patches:     patches,
		PatchSet:    ps,
	}
}

// profileToPromptMap extracts a map[string]string from a promptiter.Profile.
func profileToPromptMap(profile *promptiter.Profile) map[string]string {
	if profile == nil {
		return nil
	}
	m := make(map[string]string, len(profile.Overrides))
	for _, o := range profile.Overrides {
		if o.Value.Text != nil {
			m[o.SurfaceID] = *o.Value.Text
		}
	}
	return m
}

func clonePrompts(src map[string]string) map[string]string {
	dst := make(map[string]string, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
