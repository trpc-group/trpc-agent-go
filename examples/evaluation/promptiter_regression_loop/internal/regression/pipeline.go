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
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// EngineRunner is the PromptIter execution surface required by the regression pipeline.
type EngineRunner interface {
	Run(context.Context, *promptiterengine.RunRequest, ...promptiterengine.Option) (*promptiterengine.RunResult, error)
}

// Run executes PromptIter and applies regression analysis to the result.
func Run(ctx context.Context, engine EngineRunner, request *promptiterengine.RunRequest, cfg GateConfig) (*Report, error) {
	if engine == nil {
		return nil, fmt.Errorf("PromptIter engine is nil")
	}
	if request == nil {
		return nil, fmt.Errorf("PromptIter run request is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	result, err := engine.Run(ctx, request)
	if err != nil {
		return nil, fmt.Errorf("run PromptIter: %w", err)
	}
	return AnalyzeRun(result, cfg)
}

// Report is the auditable result of one PromptIter regression run.
type Report struct {
	SchemaVersion   string              `json:"schemaVersion"`
	GeneratedAt     time.Time           `json:"generatedAt"`
	RunStatus       string              `json:"runStatus"`
	BaselineScore   float64             `json:"baselineScore"`
	Rounds          []RoundReport       `json:"rounds"`
	AcceptedRound   int                 `json:"acceptedRound,omitempty"`
	Accepted        bool                `json:"accepted"`
	AcceptedProfile *promptiter.Profile `json:"acceptedProfile,omitempty"`
	FinalReasons    []string            `json:"finalReasons"`
}

// RoundReport records the two deltas and acceptance decision for one candidate.
type RoundReport struct {
	Round             int          `json:"round"`
	DeltaFromOriginal DeltaSummary `json:"deltaFromOriginal"`
	DeltaFromAccepted DeltaSummary `json:"deltaFromAccepted"`
	Usage             UsageSummary `json:"usage"`
	Decision          GateDecision `json:"decision"`
	EngineAccepted    bool         `json:"engineAccepted"`
}

// AnalyzeRun applies regression gates to a completed PromptIter result.
func AnalyzeRun(result *promptiterengine.RunResult, cfg GateConfig) (*Report, error) {
	if err := ValidateGateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid gate config: %w", err)
	}
	if result == nil {
		return nil, fmt.Errorf("PromptIter run result is nil")
	}
	report := &Report{SchemaVersion: "1.0", GeneratedAt: time.Now().UTC(), RunStatus: string(result.Status)}
	if result.Status != promptiterengine.RunStatusSucceeded {
		report.FinalReasons = []string{fmt.Sprintf("PromptIter run status is %s", result.Status)}
		return report, nil
	}
	if result.BaselineValidation == nil {
		return nil, fmt.Errorf("PromptIter run has no baseline validation")
	}
	report.BaselineScore = result.BaselineValidation.OverallScore
	original := result.BaselineValidation
	acceptedBaseline := original
	for _, round := range result.Rounds {
		if round.Validation == nil {
			decision := GateDecision{Accepted: false, Reasons: []string{"candidate validation is incomplete"}}
			report.Rounds = append(report.Rounds, RoundReport{Round: round.Round, Decision: decision})
			continue
		}
		fromOriginal, err := CompareEvaluations(original, round.Validation)
		if err != nil {
			return nil, fmt.Errorf("compare round %d with original baseline: %w", round.Round, err)
		}
		fromAccepted, err := CompareEvaluations(acceptedBaseline, round.Validation)
		if err != nil {
			return nil, fmt.Errorf("compare round %d with accepted baseline: %w", round.Round, err)
		}
		usage := SummarizeUsage(round.Validation)
		decision := DecideGate(cfg, fromAccepted, usage)
		engineAccepted := round.Acceptance != nil && round.Acceptance.Accepted
		if !engineAccepted {
			decision.Accepted = false
			decision.Reasons = append(decision.Reasons, "PromptIter engine rejected the candidate")
		}
		report.Rounds = append(report.Rounds, RoundReport{
			Round: round.Round, DeltaFromOriginal: fromOriginal, DeltaFromAccepted: fromAccepted,
			Usage: usage, Decision: decision, EngineAccepted: engineAccepted,
		})
		if decision.Accepted {
			acceptedBaseline = round.Validation
			report.Accepted = true
			report.AcceptedRound = round.Round
			report.AcceptedProfile = round.OutputProfile
			report.FinalReasons = decision.Reasons
		} else if !report.Accepted {
			report.FinalReasons = decision.Reasons
		}
	}
	if len(result.Rounds) == 0 {
		report.FinalReasons = []string{"PromptIter produced no candidate rounds"}
	}
	return report, nil
}

// SummarizeUsage counts model, tool, and token usage from case traces.
func SummarizeUsage(result *promptiterengine.EvaluationResult) UsageSummary {
	var usage UsageSummary
	if result == nil {
		return usage
	}
	for _, evalSet := range result.EvalSets {
		for _, evalCase := range evalSet.Cases {
			if evalCase.Trace == nil {
				continue
			}
			if len(evalCase.Trace.Steps) == 0 && evalCase.Trace.Usage != nil {
				usage.ModelCalls++
				usage.Tokens += evalCase.Trace.Usage.TotalTokens
				continue
			}
			for _, step := range evalCase.Trace.Steps {
				switch step.NodeType {
				case "llm":
					usage.ModelCalls++
				case "tool":
					usage.ToolCalls++
				}
				if step.Usage != nil {
					usage.Tokens += step.Usage.TotalTokens
				}
			}
		}
	}
	return usage
}
