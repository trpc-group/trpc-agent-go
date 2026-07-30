package main

import (
	"context"
	"time"
)

// runRegressionLoop is the closed loop required by issue #2003:
//
//  1. Run PromptIter (baseline validation eval + train-driven optimization +
//     candidate validation eval). This reuses the existing engine.
//  2. Attribute baseline failures to coarse reason buckets (batched LLM when
//     configured, deterministic rule otherwise / on fallback).
//  3. Aggregate the per-case attributions into dominant patterns (always
//     deterministic, so counts are exact) and, in LLM mode, generate the
//     summary + suggested fix + narrative in a SINGLE merged model call.
//  4. Compare the accepted candidate validation result against the baseline and
//     decide accept/reject with a deterministic gate (regression guard, key
//     cases, new hard fails).
//  5. Write an audit report (optimization_report.json / .md) capturing the
//     baseline score, candidate score, per-case delta, attribution summary +
//     clusters, gate decision, and the optimized prompt.
func runRegressionLoop(ctx context.Context, cfg regressionConfig) error {
	start := time.Now()
	result, _, err := runEngine(ctx, cfg)
	if err != nil {
		return err
	}

	baseline := result.BaselineValidation
	candidate := baseline
	if n := len(result.Rounds); n > 0 {
		// Compare the gate against the candidate the optimizer actually produced
		// in the final round. The engine already refuses to adopt a regressing
		// candidate internally, but the regression loop must still surface that
		// regression for audit: taking the last round's validation (instead of
		// only an engine-accepted round) lets a degraded candidate be seen and
		// rejected by the deterministic gate.
		candidate = result.Rounds[n-1].Validation
	}

	// Observability: count how many optional LLM enhancement calls were attempted
	// and how many failed. The pipeline always degrades to the deterministic rule,
	// so these counters only inform the operator; they never gate acceptance.
	stats := &llmCallStats{}

	// Build the failure-attribution strategy. The LLM enhancement is optional: any
	// error (no real LLM available, model init failure) falls back to the
	// deterministic rule so the gate remains reproducible and free. Attribution is
	// batched into one LLM call when available (the main cost/ latency win).
	attr, attrErr := buildAttributor(cfg, stats)
	if attrErr != nil {
		cfg.Logger.Printf("attribution: falling back to rule: %v", attrErr)
		attr = ruleAttributor{}
	}
	attrib := classifyFailures(ctx, baseline, attr)

	// Cross-case insight aggregation: always deterministic pattern counts + a
	// template summary. The LLM enhancement (summary/fix/narrative) is produced
	// separately by the EnhancedReporter in a single merged call, keeping the
	// counts exact and the number of LLM calls minimal.
	agg, aggErr := buildInsightAggregator(cfg)
	if aggErr != nil {
		cfg.Logger.Printf("insight aggregation: falling back to rule: %v", aggErr)
		agg = ruleInsightAggregator{}
	}
	attrib.Insights = agg.Aggregate(ctx, attrib.Failures)

	cost := estimateCost(cfg, result)
	gateCfg := GateConfig{
		MinValidationGain: cfg.MinScoreGain,
		AllowRegression:   false,
		KeyCaseIDs:        cfg.KeyCaseIDs,
		MaxNewHardFails:   0,
		CostBudget:        cfg.CostBudget,
		CostUsed:          cost.Total,
	}
	gateDecision := decideAcceptance(baseline, candidate, gateCfg)

	bs := overallScore(baseline)
	cs := overallScore(candidate)
	ni := NarrativeInput{
		BaselineScore:  bs,
		CandidateScore: cs,
		ScoreDelta:     cs - bs,
		Accepted:       gateDecision.Accepted,
		GateReason:     gateDecision.Reason,
		GateRejectedBy: gateDecision.RejectedBy,
		Insights:       attrib.Insights,
		Failures:       attrib.Failures,
	}

	// Natural-language narrative: in LLM mode the EnhancedReporter collapses the
	// summary + suggested fix + narrative into one model call (and also enriches
	// attrib.Insights.Summary / SuggestedFix). Any failure degrades to the
	// deterministic rule narrative so the report is always populated.
	var narrative string
	if selectAttributionMode(cfg) == "llm" && realLLMAvailable(cfg) {
		if reporter, rerr := buildEnhancedReporter(cfg, stats); rerr == nil {
			if out, oerr := reporter.Report(ctx, EnhancedInput{
				BaselineScore:  ni.BaselineScore,
				CandidateScore: ni.CandidateScore,
				ScoreDelta:     ni.ScoreDelta,
				Accepted:       ni.Accepted,
				GateReason:     ni.GateReason,
				GateRejectedBy: ni.GateRejectedBy,
				Insights:       ni.Insights,
				Failures:       ni.Failures,
			}); oerr == nil && out != nil {
				if out.Summary != "" {
					attrib.Insights.Summary = out.Summary
				}
				if out.SuggestedFix != "" {
					attrib.Insights.SuggestedFix = out.SuggestedFix
				}
				narrative = out.Narrative
			} else {
				cfg.Logger.Printf("enhanced reporter: LLM failed, using rule narrative: %v", oerr)
				narrative, _ = ruleNarrator{}.Narrate(ctx, ni)
			}
		} else {
			cfg.Logger.Printf("enhanced reporter: unavailable, using rule narrative: %v", rerr)
			narrative, _ = ruleNarrator{}.Narrate(ctx, ni)
		}
	} else {
		narrative, _ = ruleNarrator{}.Narrate(ctx, ni)
	}

	report := buildReport(cfg, result, attrib, gateDecision, cost, time.Since(start), narrative, stats.Calls, stats.Errors)
	if err := writeReport(cfg.OutputDir, report, result); err != nil {
		return err
	}
	printSummary(report)
	return nil
}
