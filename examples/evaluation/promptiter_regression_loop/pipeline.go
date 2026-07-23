//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"
)

type Pipeline struct {
	Dir     string
	Metrics MetricsConfig
	Config  LoopConfig
	Train   EvalSet
	Valid   EvalSet
}

func LoadPipeline(dir string) (*Pipeline, error) {
	p := &Pipeline{Dir: dir}
	for _, item := range []struct {
		name   string
		target any
	}{
		{"train.evalset.json", &p.Train}, {"validation.evalset.json", &p.Valid},
		{"metrics.json", &p.Metrics}, {"promptiter.json", &p.Config},
	} {
		if err := decodeJSONFile(filepath.Join(dir, item.name), item.target); err != nil {
			return nil, fmt.Errorf("decode %s: %w", item.name, err)
		}
	}
	if err := p.validate(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *Pipeline) validate() error {
	if p.Config.Engine.Type != "fake_trace" {
		return fmt.Errorf("unsupported deterministic engine %q", p.Config.Engine.Type)
	}
	if len(p.Train.Cases) < 3 || len(p.Valid.Cases) < 3 {
		return errors.New("train and validation sets need at least three cases each")
	}
	if err := validateMetrics(p.Metrics); err != nil {
		return err
	}
	if err := validateGate(p.Config.Gate); err != nil {
		return err
	}

	promptIDs := []string{"baseline"}
	candidateIDs := map[string]bool{"baseline": true}
	for i, candidate := range p.Config.Candidates {
		if strings.TrimSpace(candidate.ID) == "" {
			return fmt.Errorf("candidate %d has an empty ID", i+1)
		}
		if candidateIDs[candidate.ID] {
			return fmt.Errorf("duplicate or reserved candidate ID %q", candidate.ID)
		}
		candidateIDs[candidate.ID] = true
		promptIDs = append(promptIDs, candidate.ID)
		if _, err := resolveDataFile(p.Dir, candidate.PromptFile); err != nil {
			return fmt.Errorf("candidate %q prompt file: %w", candidate.ID, err)
		}
	}
	if _, err := resolveDataFile(p.Dir, "baseline_prompt.txt"); err != nil {
		return fmt.Errorf("baseline prompt file: %w", err)
	}
	if _, err := validateEvalSet("train", p.Train, promptIDs); err != nil {
		return err
	}
	validIDs, err := validateEvalSet("validation", p.Valid, promptIDs)
	if err != nil {
		return err
	}
	protected := make(map[string]bool, len(p.Config.Gate.CriticalCaseIDs))
	for _, id := range p.Config.Gate.CriticalCaseIDs {
		if strings.TrimSpace(id) == "" {
			return errors.New("critical case ID cannot be empty")
		}
		if protected[id] {
			return fmt.Errorf("duplicate critical case ID %q", id)
		}
		protected[id] = true
		if !validIDs[id] {
			return fmt.Errorf("critical case ID %q is not in the validation set", id)
		}
	}
	return nil
}

func validateMetrics(cfg MetricsConfig) error {
	for _, field := range []struct {
		name  string
		value float64
	}{
		{"pass_threshold", cfg.PassThreshold},
		{"response_weight", cfg.ResponseWeight},
		{"tool_weight", cfg.ToolWeight},
		{"format_weight", cfg.FormatWeight},
	} {
		if !finite(field.value) {
			return fmt.Errorf("metrics %s must be finite", field.name)
		}
	}
	if cfg.PassThreshold < 0 || cfg.PassThreshold > 1 {
		return errors.New("metrics pass_threshold must be between 0 and 1")
	}
	if cfg.ResponseWeight < 0 || cfg.ToolWeight < 0 || cfg.FormatWeight < 0 {
		return errors.New("metric weights cannot be negative")
	}
	totalWeight := cfg.ResponseWeight + cfg.ToolWeight + cfg.FormatWeight
	if !finite(totalWeight) {
		return errors.New("total metric weight must be finite")
	}
	if totalWeight <= 0 {
		return errors.New("at least one metric weight must be positive")
	}
	return nil
}

func validateGate(cfg GateConfig) error {
	if !finite(cfg.MinValidationGain) || cfg.MinValidationGain < 0 || cfg.MinValidationGain > 1 {
		return errors.New("gate min_validation_gain must be finite and between 0 and 1")
	}
	if cfg.MaxCostIncrease != nil && (!finite(*cfg.MaxCostIncrease) || *cfg.MaxCostIncrease < 0) {
		return errors.New("gate max_cost_increase must be finite and non-negative")
	}
	if cfg.MaxToolCalls != nil && *cfg.MaxToolCalls < 0 {
		return errors.New("gate max_tool_calls must be non-negative")
	}
	return nil
}

func validateEvalSet(name string, set EvalSet, promptIDs []string) (map[string]bool, error) {
	if strings.TrimSpace(set.ID) == "" {
		return nil, fmt.Errorf("%s eval set has an empty ID", name)
	}
	caseIDs := make(map[string]bool, len(set.Cases))
	for i, evalCase := range set.Cases {
		if strings.TrimSpace(evalCase.ID) == "" {
			return nil, fmt.Errorf("%s case %d has an empty ID", name, i+1)
		}
		if caseIDs[evalCase.ID] {
			return nil, fmt.Errorf("%s eval set has duplicate case ID %q", name, evalCase.ID)
		}
		caseIDs[evalCase.ID] = true
		for _, promptID := range promptIDs {
			trace, ok := evalCase.Runs[promptID]
			if !ok {
				return nil, fmt.Errorf("%s case %q has no trace for prompt %q", name, evalCase.ID, promptID)
			}
			if !finite(trace.Cost) || trace.Cost < 0 {
				return nil, fmt.Errorf("%s case %q prompt %q has invalid cost", name, evalCase.ID, promptID)
			}
			if trace.LatencyMS < 0 {
				return nil, fmt.Errorf("%s case %q prompt %q has negative latency", name, evalCase.ID, promptID)
			}
		}
	}
	return caseIDs, nil
}

func finite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}

func resolveDataFile(root, name string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", errors.New("path is empty")
	}
	if len(name) >= 2 && ((name[0] >= 'a' && name[0] <= 'z') ||
		(name[0] >= 'A' && name[0] <= 'Z')) && name[1] == ':' {
		return "", fmt.Errorf("path %q must be relative to the data directory", name)
	}
	name = filepath.FromSlash(strings.ReplaceAll(name, `\`, "/"))
	if filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("path %q must be relative to the data directory", name)
	}
	resolvedRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve data directory: %w", err)
	}
	resolvedRoot, err = filepath.EvalSymlinks(resolvedRoot)
	if err != nil {
		return "", fmt.Errorf("resolve data directory links: %w", err)
	}
	candidate, err := filepath.Abs(filepath.Join(resolvedRoot, name))
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", name, err)
	}
	candidate, err = filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve path %q links: %w", name, err)
	}
	relative, err := filepath.Rel(resolvedRoot, candidate)
	if err != nil {
		return "", fmt.Errorf("validate path %q: %w", name, err)
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("path %q escapes the data directory", name)
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", fmt.Errorf("stat path %q: %w", name, err)
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("path %q is not a regular file", name)
	}
	return candidate, nil
}

func (p *Pipeline) Run(ctx context.Context) (*OptimizationReport, error) {
	if err := p.validate(); err != nil {
		return nil, fmt.Errorf("validate pipeline: %w", err)
	}
	started := time.Now()
	baselinePath, err := resolveDataFile(p.Dir, "baseline_prompt.txt")
	if err != nil {
		return nil, fmt.Errorf("resolve baseline prompt: %w", err)
	}
	baselinePrompt, err := readPrompt(baselinePath)
	if err != nil {
		return nil, err
	}
	report := &OptimizationReport{
		StartedAt:      started,
		Seed:           p.Config.Seed,
		Engine:         p.Config.Engine,
		Metrics:        p.Metrics,
		Gate:           cloneGateConfig(p.Config.Gate),
		BaselinePrompt: baselinePrompt,
	}
	report.BaselineTrain, err = p.Evaluate(ctx, p.Train, "baseline")
	if err != nil {
		return nil, err
	}
	report.BaselineValidation, err = p.Evaluate(ctx, p.Valid, "baseline")
	if err != nil {
		return nil, err
	}
	report.AttributionCounts = CountAttributions(report.BaselineTrain, report.BaselineValidation)

	bestScore := report.BaselineValidation.OverallScore
	for i, candidate := range p.Config.Candidates {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		roundStart := time.Now()
		promptPath, err := resolveDataFile(p.Dir, candidate.PromptFile)
		if err != nil {
			return nil, fmt.Errorf("resolve candidate %q prompt: %w", candidate.ID, err)
		}
		prompt, err := readPrompt(promptPath)
		if err != nil {
			return nil, err
		}
		train, err := p.Evaluate(ctx, p.Train, candidate.ID)
		if err != nil {
			return nil, err
		}
		validation, err := p.Evaluate(ctx, p.Valid, candidate.ID)
		if err != nil {
			return nil, err
		}
		delta := CompareEvaluations(report.BaselineValidation, validation)
		gate := ApplyGate(p.Config.Gate, report.BaselineValidation, validation, delta)
		round := RoundAudit{Round: i + 1, CandidateID: candidate.ID, Prompt: prompt, Train: train, Validation: validation, Delta: delta, Gate: gate, DurationMS: time.Since(roundStart).Milliseconds()}
		report.Rounds = append(report.Rounds, round)
		if gate.Accepted && validation.OverallScore > bestScore {
			bestScore, report.Accepted = validation.OverallScore, true
			report.SelectedCandidate, report.SelectedPrompt = candidate.ID, prompt
			report.DecisionReasons = append([]string(nil), gate.Reasons...)
		}
	}
	if !report.Accepted {
		report.DecisionReasons = []string{"no candidate passed the validation acceptance gate with a score improvement"}
	}
	report.DurationMS = time.Since(started).Milliseconds()
	return report, nil
}

func (p *Pipeline) Evaluate(ctx context.Context, set EvalSet, promptID string) (EvaluationResult, error) {
	result := EvaluationResult{EvalSetID: set.ID, PromptID: promptID}
	if err := validateMetrics(p.Metrics); err != nil {
		return result, err
	}
	for _, evalCase := range set.Cases {
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		default:
		}
		trace, ok := evalCase.Runs[promptID]
		if !ok {
			return result, fmt.Errorf("case %s has no trace for prompt %s", evalCase.ID, promptID)
		}
		if !finite(trace.Cost) || trace.Cost < 0 {
			return result, fmt.Errorf("case %s prompt %s has invalid cost", evalCase.ID, promptID)
		}
		if trace.LatencyMS < 0 {
			return result, fmt.Errorf("case %s prompt %s has negative latency", evalCase.ID, promptID)
		}
		caseResult := ScoreCase(evalCase, trace, p.Metrics)
		result.Cases = append(result.Cases, caseResult)
		result.OverallScore += caseResult.Score
		result.TotalCost += trace.Cost
		if !finite(result.TotalCost) {
			return result, fmt.Errorf("case %s prompt %s overflows total cost", evalCase.ID, promptID)
		}
		result.ToolCalls += len(trace.ToolCalls)
		if trace.LatencyMS > math.MaxInt64-result.LatencyMS {
			return result, fmt.Errorf("case %s prompt %s overflows total latency", evalCase.ID, promptID)
		}
		result.LatencyMS += trace.LatencyMS
		if caseResult.Passed {
			result.Passed++
		} else {
			result.Failed++
		}
	}
	result.OverallScore = round(result.OverallScore / float64(len(result.Cases)))
	result.TotalCost = round(result.TotalCost)
	return result, nil
}

func ScoreCase(c EvalCase, trace RunTrace, metrics MetricsConfig) CaseResult {
	response := similarity(c.ExpectedResponse, trace.FinalResponse)
	toolScore := 0.0
	if sameToolCalls(c.ExpectedToolCalls, trace.ToolCalls) {
		toolScore = 1
	} else if sameToolNames(c.ExpectedToolCalls, trace.ToolCalls) {
		toolScore = 0.5
	}
	formatScore := 0.0
	if trace.FormatValid {
		formatScore = 1
	}
	weight := metrics.ResponseWeight + metrics.ToolWeight + metrics.FormatWeight
	score := 0.0
	if weight > 0 {
		score = (response*metrics.ResponseWeight + toolScore*metrics.ToolWeight + formatScore*metrics.FormatWeight) / weight
	}
	contractsPassed := trace.Error == "" &&
		trace.FormatValid &&
		sameToolCalls(c.ExpectedToolCalls, trace.ToolCalls) &&
		(c.ExpectedRoute == "" || trace.Route == c.ExpectedRoute) &&
		(c.RetrievalRequired == nil || trace.RetrievalHit == *c.RetrievalRequired)
	result := CaseResult{
		CaseID:        c.ID,
		Critical:      c.Critical,
		Score:         round(score),
		Passed:        contractsPassed && score >= metrics.PassThreshold,
		FinalResponse: trace.FinalResponse,
		ToolCalls:     trace.ToolCalls,
		Trace:         trace,
	}
	if !result.Passed {
		result.Attribution, result.Reason = AttributeFailure(c, trace, response)
	}
	return result
}

func AttributeFailure(c EvalCase, trace RunTrace, responseSimilarity float64) (Attribution, string) {
	switch {
	case trace.Error != "":
		return AttributionRuntime, "runner returned: " + trace.Error
	case c.ExpectedRoute != "" && trace.Route != c.ExpectedRoute:
		return AttributionRoute, fmt.Sprintf("route %q, expected %q", trace.Route, c.ExpectedRoute)
	case !sameToolNames(c.ExpectedToolCalls, trace.ToolCalls):
		return AttributionToolCall, "tool call sequence does not match the expected trajectory"
	case !sameToolCalls(c.ExpectedToolCalls, trace.ToolCalls):
		return AttributionToolArgs, "tool arguments do not match the expected trajectory"
	case !trace.FormatValid:
		return AttributionFormat, "final response violates the required structured format"
	case c.RetrievalRequired != nil && trace.RetrievalHit != *c.RetrievalRequired:
		return AttributionKnowledge, fmt.Sprintf("retrieval hit %t, expected %t", trace.RetrievalHit, *c.RetrievalRequired)
	case responseSimilarity < 1:
		return AttributionFinalResponse, "final response does not contain the expected facts"
	default:
		return AttributionUnknown, "metric score is below threshold; inspect trace"
	}
}

func CompareEvaluations(baseline, candidate EvaluationResult) DeltaSummary {
	d := DeltaSummary{ScoreDelta: round(candidate.OverallScore - baseline.OverallScore)}
	base := make(map[string]CaseResult, len(baseline.Cases))
	candidateIDs := make(map[string]bool, len(candidate.Cases))
	for _, c := range baseline.Cases {
		if _, exists := base[c.CaseID]; exists {
			d.CaseSetErrors = append(d.CaseSetErrors, "duplicate baseline case: "+c.CaseID)
			continue
		}
		base[c.CaseID] = c
	}
	if len(base) != len(baseline.Cases) {
		d.CaseSetErrors = append(d.CaseSetErrors, "non-bijective baseline case set")
	}
	for _, c := range candidate.Cases {
		if candidateIDs[c.CaseID] {
			d.CaseSetErrors = append(d.CaseSetErrors, "duplicate candidate case: "+c.CaseID)
			continue
		}
		candidateIDs[c.CaseID] = true
		b, ok := base[c.CaseID]
		if !ok {
			d.CaseSetErrors = append(d.CaseSetErrors, "extra candidate case: "+c.CaseID)
			continue
		}
		cd := CaseDelta{CaseID: c.CaseID, BaselineScore: b.Score, CandidateScore: c.Score, ScoreDelta: round(c.Score - b.Score), NewlyPassed: !b.Passed && c.Passed, NewlyFailed: b.Passed && !c.Passed, Critical: b.Critical || c.Critical}
		if cd.NewlyPassed {
			d.NewlyPassed++
		}
		if cd.NewlyFailed {
			d.NewlyFailed++
		}
		if cd.ScoreDelta > 0 {
			d.Improved++
		}
		if cd.ScoreDelta < 0 {
			d.Regressed++
		}
		d.Cases = append(d.Cases, cd)
	}
	if len(candidate.Cases) != len(candidateIDs) {
		d.CaseSetErrors = append(d.CaseSetErrors, "non-bijective candidate case set")
	}
	for id := range base {
		if !candidateIDs[id] {
			d.CaseSetErrors = append(d.CaseSetErrors, "missing candidate case: "+id)
			baselineCase := base[id]
			d.Cases = append(d.Cases, CaseDelta{CaseID: id, BaselineScore: baselineCase.Score, CandidateScore: 0, ScoreDelta: round(-baselineCase.Score), NewlyFailed: baselineCase.Passed, Critical: baselineCase.Critical})
		}
	}
	sort.Slice(d.Cases, func(i, j int) bool { return d.Cases[i].CaseID < d.Cases[j].CaseID })
	sort.Strings(d.CaseSetErrors)
	return d
}

func ApplyGate(cfg GateConfig, baseline, candidate EvaluationResult, delta DeltaSummary) GateDecision {
	d := GateDecision{Accepted: true}
	for _, caseError := range delta.CaseSetErrors {
		d.Accepted = false
		d.Reasons = append(d.Reasons, "invalid evaluation case set: "+caseError)
	}
	if delta.ScoreDelta <= cfg.MinValidationGain {
		d.Accepted = false
		d.Reasons = append(d.Reasons, fmt.Sprintf("validation gain %.3f must be greater than %.3f", delta.ScoreDelta, cfg.MinValidationGain))
	}
	if cfg.NoNewHardFails {
		for _, c := range delta.Cases {
			if c.Critical && c.NewlyFailed {
				d.Accepted = false
				d.Reasons = append(d.Reasons, "new hard fail: "+c.CaseID)
			}
		}
	}
	critical := make(map[string]bool, len(cfg.CriticalCaseIDs))
	for _, id := range cfg.CriticalCaseIDs {
		critical[id] = true
	}
	candidatePassed := make(map[string]bool, len(candidate.Cases))
	for _, c := range candidate.Cases {
		candidatePassed[c.CaseID] = c.Passed
	}
	for _, c := range delta.Cases {
		if !critical[c.CaseID] {
			continue
		}
		if c.NewlyFailed {
			d.Accepted = false
			d.Reasons = append(d.Reasons, "critical case newly failed: "+c.CaseID)
		}
		if c.ScoreDelta < 0 {
			d.Accepted = false
			d.Reasons = append(d.Reasons, "critical case regressed: "+c.CaseID)
		}
		if passed, exists := candidatePassed[c.CaseID]; exists && !passed {
			d.Accepted = false
			d.Reasons = append(d.Reasons, "critical case did not pass: "+c.CaseID)
		}
	}
	if cfg.MaxCostIncrease != nil && candidate.TotalCost-baseline.TotalCost > *cfg.MaxCostIncrease {
		d.Accepted = false
		d.Reasons = append(d.Reasons, "cost increase exceeds budget")
	}
	if cfg.MaxToolCalls != nil && candidate.ToolCalls > *cfg.MaxToolCalls {
		d.Accepted = false
		d.Reasons = append(d.Reasons, "tool call budget exceeded")
	}
	if d.Accepted {
		d.Reasons = []string{fmt.Sprintf("accepted: validation score improved by %.3f with no protected regression", delta.ScoreDelta)}
	}
	return d
}

func CountAttributions(results ...EvaluationResult) map[Attribution]int {
	out := map[Attribution]int{}
	for _, result := range results {
		for _, c := range result.Cases {
			if !c.Passed {
				out[c.Attribution]++
			}
		}
	}
	return out
}
func readPrompt(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	return strings.TrimSpace(string(data)), nil
}

func decodeJSONFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func sameToolNames(expected, actual []ToolCall) bool {
	if len(expected) != len(actual) {
		return false
	}
	for i := range expected {
		if expected[i].Name != actual[i].Name {
			return false
		}
	}
	return true
}

func sameToolCalls(expected, actual []ToolCall) bool {
	if !sameToolNames(expected, actual) {
		return false
	}
	for i := range expected {
		if len(expected[i].Arguments) == 0 && len(actual[i].Arguments) == 0 {
			continue
		}
		if !reflect.DeepEqual(expected[i].Arguments, actual[i].Arguments) {
			return false
		}
	}
	return true
}

func cloneGateConfig(cfg GateConfig) GateConfig {
	cloned := cfg
	cloned.CriticalCaseIDs = append([]string(nil), cfg.CriticalCaseIDs...)
	if cfg.MaxCostIncrease != nil {
		value := *cfg.MaxCostIncrease
		cloned.MaxCostIncrease = &value
	}
	if cfg.MaxToolCalls != nil {
		value := *cfg.MaxToolCalls
		cloned.MaxToolCalls = &value
	}
	return cloned
}
func similarity(expected, actual string) float64 {
	e := words(expected)
	if len(e) == 0 {
		return 1
	}
	a := words(actual)
	hit := 0
	for w := range e {
		if a[w] {
			hit++
		}
	}
	return float64(hit) / float64(len(e))
}
func words(s string) map[string]bool {
	out := map[string]bool{}
	for _, w := range strings.Fields(strings.ToLower(s)) {
		w = strings.Trim(w, ".,!?;:\"'()[]{}")
		if w != "" {
			out[w] = true
		}
	}
	return out
}
func round(v float64) float64 {
	const scale = 1000.0
	// At this magnitude float64 has no fractional thousandths to discard, and
	// multiplying by scale would turn an otherwise finite audit value into Inf.
	if finite(v) && math.Abs(v) > math.MaxFloat64/scale {
		return v
	}
	return math.Round(v*scale) / scale
}
