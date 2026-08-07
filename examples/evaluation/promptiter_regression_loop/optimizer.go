//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"fmt"
	"strings"

	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	promptiter "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

func buildLosses(run EvaluationRun) []promptiter.CaseLoss {
	losses := make([]promptiter.CaseLoss, 0)
	for _, evalCase := range run.Cases {
		if len(evalCase.FailureReasons) == 0 {
			continue
		}
		caseLoss := promptiter.CaseLoss{
			EvalSetID:  run.EvalSetID,
			EvalCaseID: evalCase.CaseID,
		}
		for _, failure := range evalCase.FailureReasons {
			caseLoss.TerminalLosses = append(caseLoss.TerminalLosses, promptiter.TerminalLoss{
				EvalSetID:  run.EvalSetID,
				EvalCaseID: evalCase.CaseID,
				MetricName: failure.MetricName,
				Severity:   severityForFailure(failure.Category),
				StepID:     "final",
				Loss:       fmt.Sprintf("%s: %s", failure.Category, failure.Evidence),
			})
		}
		losses = append(losses, caseLoss)
	}
	return losses
}

func severityForFailure(category string) promptiter.LossSeverity {
	switch category {
	case FailureRouteError, FailureToolCallError:
		return promptiter.LossSeverityP1
	case FailureToolArgumentError, FailureFormatError:
		return promptiter.LossSeverityP2
	case FailureKnowledgeRecallGap:
		return promptiter.LossSeverityP3
	default:
		return promptiter.LossSeverityP2
	}
}

func buildPromptIterArtifacts(
	cfg Config,
	candidate CandidateConfig,
	candidatePrompt string,
) (*promptiter.PatchSet, *promptiter.Profile) {
	value := astructure.SurfaceValue{Text: &candidatePrompt}
	patches := &promptiter.PatchSet{
		Patches: []promptiter.SurfacePatch{{
			SurfaceID: cfg.TargetSurfaceID,
			Value:     value,
			Reason:    candidate.Description,
		}},
	}
	profile := &promptiter.Profile{
		StructureID: cfg.AppName + "-deterministic-structure",
		Overrides: []promptiter.SurfaceOverride{{
			SurfaceID: cfg.TargetSurfaceID,
			Value:     value,
		}},
	}
	return patches, profile
}

func candidatePrompt(baseline string, candidate CandidateConfig) string {
	appendix := strings.TrimSpace(candidate.AppendPrompt)
	if appendix == "" {
		return baseline
	}
	return strings.TrimRight(baseline, "\n") + "\n\n" + appendix + "\n"
}
