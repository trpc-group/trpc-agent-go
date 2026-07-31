//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

const promptSurfaceID = "candidate:instruction"

func runPipeline(ctx context.Context, dataDir, outputDir string) (*optimizationReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	loaded, err := loadInputs(dataDir)
	if err != nil {
		return nil, err
	}
	startedAt := time.Now().UTC()
	baselineTrain := evaluate(loaded.Train, loaded.Prompt, loaded.Metrics)
	baselineValidation := evaluate(loaded.Validation, loaded.Prompt, loaded.Metrics)
	report := &optimizationReport{
		Metadata: runMetadata{
			Seed: loaded.Config.Seed, Model: loaded.Config.Model, StartedAt: startedAt,
			Mode: "deterministic", Promptiter: "evaluation/workflow/promptiter.PatchSet",
		},
		Baseline:       baselineReport{Prompt: loaded.Prompt, Train: baselineTrain, Validation: baselineValidation},
		Rounds:         make([]roundReport, 0, len(loaded.Config.Candidates)),
		AcceptedPrompt: loaded.Prompt,
		Decision:       "keep_baseline",
	}
	for index, candidate := range loaded.Config.Candidates {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		patchSet := buildPatchSet(candidate)
		prompt, err := applyInstructionPatch(patchSet)
		if err != nil {
			return nil, fmt.Errorf("apply candidate %q: %w", candidate.ID, err)
		}
		train := evaluate(loaded.Train, prompt, loaded.Metrics)
		validation := evaluate(loaded.Validation, prompt, loaded.Metrics)
		deltas := computeDelta(baselineValidation, validation)
		cost := mergeCost(train.Cost, validation.Cost)
		gate := decideGate(loaded.Config.Gate, baselineValidation, validation, deltas, cost)
		round := roundReport{
			Round: index + 1, CandidateID: candidate.ID, Prompt: prompt, PatchReason: candidate.Reason,
			Train: train, Validation: validation, ValidationDelta: deltas,
			AttributionSummary: summarizeAttributions(train, validation), Cost: cost, Gate: gate,
		}
		report.Rounds = append(report.Rounds, round)
		if gate.Accepted {
			report.AcceptedCandidateID = candidate.ID
			report.AcceptedPrompt = prompt
			report.Decision = "accept_candidate"
			break
		}
	}
	report.Metadata.DurationMS = time.Since(startedAt).Milliseconds()
	if err := writeReports(outputDir, report); err != nil {
		return nil, err
	}
	return report, nil
}

func buildPatchSet(candidate candidateConfig) promptiter.PatchSet {
	prompt := candidate.Prompt
	return promptiter.PatchSet{Patches: []promptiter.SurfacePatch{{
		SurfaceID: promptSurfaceID,
		Value:     astructure.SurfaceValue{Text: &prompt},
		Reason:    candidate.Reason,
	}}}
}

func applyInstructionPatch(patches promptiter.PatchSet) (string, error) {
	if len(patches.Patches) != 1 {
		return "", errors.New("candidate must contain exactly one instruction patch")
	}
	patch := patches.Patches[0]
	if patch.SurfaceID != promptSurfaceID {
		return "", fmt.Errorf("unexpected surface %q", patch.SurfaceID)
	}
	if patch.Value.Text == nil {
		return "", errors.New("instruction patch text is nil")
	}
	return *patch.Value.Text, nil
}
