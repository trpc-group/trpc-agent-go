//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regressionloop

import (
	"fmt"
	"strings"
)

// EvaluateGate applies the production release policy to the candidate.
func EvaluateGate(
	cfg GateConfig,
	engineAccepted bool,
	delta DeltaReport,
	cost CostSummary,
	latency Duration,
) GateDecision {
	accepted := true
	reasons := make([]string, 0, 6)
	if cfg.RequireEngineAccepted {
		if engineAccepted {
			reasons = append(reasons, "PromptIter accepted a candidate profile")
		} else {
			accepted = false
			reasons = append(reasons, "PromptIter did not accept a candidate profile")
		}
	}
	if delta.OverallScoreDelta+scoreEpsilon >= cfg.MinValidationScoreGain {
		reasons = append(reasons, fmt.Sprintf(
			"validation score gain %.3f >= threshold %.3f",
			delta.OverallScoreDelta,
			cfg.MinValidationScoreGain,
		))
	} else {
		accepted = false
		reasons = append(reasons, fmt.Sprintf(
			"validation score gain %.3f < threshold %.3f",
			delta.OverallScoreDelta,
			cfg.MinValidationScoreGain,
		))
	}
	hardFailures := hardFailRegressions(delta, cfg.HardFailMetricNames)
	if len(hardFailures) == 0 && delta.Summary.NewlyFailed == 0 {
		reasons = append(reasons, "no newly failed validation metrics")
	} else if len(hardFailures) == 0 {
		reasons = append(reasons, fmt.Sprintf(
			"no newly failed hard validation metrics; %d non-hard validation metrics newly failed",
			delta.Summary.NewlyFailed,
		))
	} else if cfg.AllowNewHardFail {
		reasons = append(reasons, fmt.Sprintf(
			"%d newly failed hard validation metrics allowed by policy: %v",
			len(hardFailures),
			hardFailures,
		))
	} else {
		accepted = false
		reasons = append(reasons, fmt.Sprintf(
			"%d newly failed hard validation metrics: %v",
			len(hardFailures),
			hardFailures,
		))
	}
	if regressed := criticalRegressions(delta); len(regressed) > 0 {
		accepted = false
		reasons = append(reasons, fmt.Sprintf("critical cases regressed: %v", regressed))
	} else if len(cfg.CriticalCaseIDs) > 0 {
		reasons = append(reasons, "critical cases did not regress")
	}
	if cfg.RejectAnyScoreDown {
		if regressed := scoreDownRegressions(delta); len(regressed) > 0 {
			accepted = false
			reasons = append(reasons, fmt.Sprintf("score-down validation metrics: %v", regressed))
		} else {
			reasons = append(reasons, "no score-down validation metrics")
		}
	}
	if cfg.MaxModelCalls > 0 {
		if cost.ModelCalls <= cfg.MaxModelCalls {
			reasons = append(reasons, fmt.Sprintf(
				"model calls %d within budget %d",
				cost.ModelCalls,
				cfg.MaxModelCalls,
			))
		} else {
			accepted = false
			reasons = append(reasons, fmt.Sprintf(
				"model calls %d exceed budget %d",
				cost.ModelCalls,
				cfg.MaxModelCalls,
			))
		}
	}
	if cfg.MaxCost > 0 {
		if !hasMeasuredAmount(cost) {
			accepted = false
			reasons = append(reasons, "cost amount unavailable; configure CostProvider to enforce maxCost")
		} else if cost.Amount <= cfg.MaxCost+scoreEpsilon {
			reasons = append(reasons, fmt.Sprintf("cost %.4f within budget %.4f", cost.Amount, cfg.MaxCost))
		} else {
			accepted = false
			reasons = append(reasons, fmt.Sprintf("cost %.4f exceeds budget %.4f", cost.Amount, cfg.MaxCost))
		}
	}
	if cfg.MaxLatency.Duration > 0 {
		if latency.Duration <= cfg.MaxLatency.Duration {
			reasons = append(reasons, fmt.Sprintf("latency %s within budget %s", latency.Duration, cfg.MaxLatency.Duration))
		} else {
			accepted = false
			reasons = append(reasons, fmt.Sprintf("latency %s exceeds budget %s", latency.Duration, cfg.MaxLatency.Duration))
		}
	}
	return GateDecision{Accepted: accepted, Reasons: reasons}
}

func hasMeasuredAmount(cost CostSummary) bool {
	return cost.Source == CostSourceProvider || cost.Amount > 0
}

func hardFailRegressions(delta DeltaReport, hardFailMetricNames []string) []string {
	hardMetrics := normalizedStringSet(hardFailMetricNames)
	treatAllAsHard := len(hardMetrics) == 0
	failures := make([]string, 0, delta.Summary.NewlyFailed)
	for _, item := range delta.Cases {
		if item.Kind != DeltaNewlyFailed {
			continue
		}
		metricName := strings.TrimSpace(item.MetricName)
		if !treatAllAsHard && !hardMetrics[metricName] {
			continue
		}
		failures = append(failures, fmt.Sprintf("%s/%s", item.EvalCaseID, item.MetricName))
	}
	if treatAllAsHard && len(failures) == 0 && delta.Summary.NewlyFailed > 0 {
		for i := 0; i < delta.Summary.NewlyFailed; i++ {
			failures = append(failures, "unknown")
		}
	}
	return failures
}

func normalizedStringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			set[value] = true
		}
	}
	return set
}

func scoreDownRegressions(delta DeltaReport) []string {
	regressed := make([]string, 0, delta.Summary.ScoreDown)
	for _, item := range delta.Cases {
		if item.Kind != DeltaScoreDown {
			continue
		}
		regressed = append(regressed, fmt.Sprintf("%s/%s", item.EvalCaseID, item.MetricName))
	}
	return regressed
}

func criticalRegressions(delta DeltaReport) []string {
	seen := map[string]struct{}{}
	regressed := make([]string, 0)
	for _, item := range delta.Cases {
		if !item.Critical {
			continue
		}
		if item.Kind != DeltaNewlyFailed && item.Kind != DeltaScoreDown {
			continue
		}
		if _, ok := seen[item.EvalCaseID]; ok {
			continue
		}
		seen[item.EvalCaseID] = struct{}{}
		regressed = append(regressed, item.EvalCaseID)
	}
	return regressed
}
