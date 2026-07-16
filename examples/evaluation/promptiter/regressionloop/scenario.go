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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/regloop"
)

const (
	scenarioSuccess     = "success"
	scenarioIneffective = "ineffective"
	scenarioOverfit     = "overfit"
	scenarioAttribution = "attribution"
)

// scenario encodes one deterministic demonstration: which cases the candidate
// passes before vs after optimization, the eval data to run, the engine
// acceptance threshold, and the harness release gate.
type scenario struct {
	name        string
	description string
	// metricFileID / trainEvalSetID / validationEvalSetID select the eval data.
	metricFileID        string
	trainEvalSetID      string
	validationEvalSetID string
	// baselineGolds are the score tokens the candidate answers correctly under
	// the baseline instruction (before optimization).
	baselineGolds map[string]string
	// optimizedGolds are the tokens answered correctly under the optimized
	// (marker-carrying) instruction.
	optimizedGolds map[string]string
	// engineMinScoreGain overrides cfg.MinScoreGain when non-nil. Only the
	// overfit scenario lowers it so the engine accepts an overall-improving
	// candidate that the harness gate then rejects on a regressed case.
	engineMinScoreGain *float64
	// gateOverride overrides the cfg-derived release gate when non-nil.
	gateOverride *regloop.ReleaseGate
}

func scenarioNames() []string {
	return []string{scenarioSuccess, scenarioIneffective, scenarioOverfit, scenarioAttribution}
}

// scenarioByName returns the named scenario configuration. Scenarios that leave
// engineMinScoreGain / gateOverride nil use the thresholds from promptiter.json.
func scenarioByName(name string) (scenario, error) {
	switch name {
	case scenarioSuccess:
		// Baseline fails everything; optimization fixes everything -> accepted &
		// released (uses the config thresholds).
		return scenario{
			name:                scenarioSuccess,
			description:         "optimization succeeds: baseline fails, candidate passes validation, accepted and released",
			metricFileID:        metricFileID,
			trainEvalSetID:      trainEvalSetID,
			validationEvalSetID: validationEvalSetID,
			baselineGolds:       map[string]string{},
			optimizedGolds:      goldSubset("100-90", "77-70", "3-2", "88-80", "5-4", "112-108"),
		}, nil
	case scenarioIneffective:
		// The optimized prompt still fails validation -> no gain -> rejected.
		return scenario{
			name:                scenarioIneffective,
			description:         "optimization ineffective: candidate never improves validation, rejected for no gain",
			metricFileID:        metricFileID,
			trainEvalSetID:      trainEvalSetID,
			validationEvalSetID: validationEvalSetID,
			baselineGolds:       map[string]string{},
			optimizedGolds:      map[string]string{},
		}, nil
	case scenarioOverfit:
		// Baseline passes val_01; optimization lifts training and val_02/val_03
		// but breaks val_01. Overall validation improves so the engine accepts
		// (lowered threshold), but the harness gate rejects on the regressed case.
		return scenario{
			name:                scenarioOverfit,
			description:         "overfitting: training and overall validation improve but val_01 regresses, gate rejects the candidate",
			metricFileID:        metricFileID,
			trainEvalSetID:      trainEvalSetID,
			validationEvalSetID: validationEvalSetID,
			baselineGolds:       goldSubset("88-80"),
			optimizedGolds:      goldSubset("100-90", "77-70", "3-2", "5-4", "112-108"),
			engineMinScoreGain:  floatPtr(0.2),
			gateOverride:        &regloop.ReleaseGate{MinTotalGain: 0.2, AllowNewHardFail: false, MaxRounds: 4, MaxModelCalls: 100},
		}, nil
	case scenarioAttribution:
		// A dataset whose validation case declares an expected tool call and a
		// gold final response. The text-only candidate makes no tool call and the
		// wrong text, failing both metrics, so the baseline attribution shows
		// responseMismatch AND toolError in one live run.
		return scenario{
			name:                scenarioAttribution,
			description:         "failure attribution demo: baseline fails a final-response metric and a tool-trajectory metric, classified as responseMismatch + toolError",
			metricFileID:        "attribution",
			trainEvalSetID:      "attribution-train",
			validationEvalSetID: "attribution-validation",
			baselineGolds:       map[string]string{},
			optimizedGolds:      map[string]string{},
		}, nil
	}
	return scenario{}, fmt.Errorf("unknown scenario %q (want one of %v)", name, scenarioNames())
}

// resolveMinScoreGain returns the engine acceptance threshold for this run.
func resolveMinScoreGain(cfg *loopConfig, sc scenario) float64 {
	if sc.engineMinScoreGain != nil {
		return *sc.engineMinScoreGain
	}
	return cfg.MinScoreGain
}

// resolveGate returns the harness release gate for this run.
func resolveGate(cfg *loopConfig, sc scenario) regloop.ReleaseGate {
	if sc.gateOverride != nil {
		return *sc.gateOverride
	}
	return cfg.releaseGate()
}

// goldSubset builds a token->gold map from the master golds for the given tokens.
func goldSubset(tokens ...string) map[string]string {
	subset := make(map[string]string, len(tokens))
	for _, token := range tokens {
		if gold, ok := golds[token]; ok {
			subset[token] = gold
		}
	}
	return subset
}
