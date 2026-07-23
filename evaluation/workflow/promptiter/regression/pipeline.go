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
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent/structure"
)

// Clock supplies timestamps and can be fixed by tests.
type Clock func() time.Time

// Pipeline runs baseline evaluation, failure attribution, PromptIter proposal,
// validation regression, acceptance gating, and report assembly.
type Pipeline struct {
	config    Config
	evaluator Evaluator
	optimizer Optimizer
	clock     Clock
}

// NewPipeline constructs a regression pipeline.
func NewPipeline(config Config, evaluator Evaluator, optimizer Optimizer, clock Clock) (*Pipeline, error) {
	if err := validateConfig(&config); err != nil {
		return nil, err
	}
	if evaluator == nil {
		return nil, errors.New("evaluator is nil")
	}
	if optimizer == nil {
		return nil, errors.New("optimizer is nil")
	}
	modeReporter, reportsMode := evaluator.(interface{ RuntimeMode() string })
	if !reportsMode {
		return nil, fmt.Errorf("%s mode requires an evaluator with explicit RuntimeMode capability", config.Mode)
	}
	if reportsMode && modeReporter.RuntimeMode() != config.Mode {
		return nil, fmt.Errorf("evaluator runtime mode %q does not match config mode %q", modeReporter.RuntimeMode(), config.Mode)
	}
	if clock == nil {
		clock = time.Now
	}
	return &Pipeline{
		config:    cloneConfig(config),
		evaluator: evaluator,
		optimizer: optimizer,
		clock:     clock,
	}, nil
}

// Run executes the full loop. Every candidate is compared with the immutable
// original validation baseline, so a rejected overfit candidate never becomes
// the implicit incumbent of a later round.
func (p *Pipeline) Run(
	ctx context.Context,
	baselinePrompt string,
	train *EvalSet,
	validation *EvalSet,
) (*Report, error) {
	if stringsTrimmedEmpty(baselinePrompt) {
		return nil, errors.New("baseline prompt is empty")
	}
	if !utf8.ValidString(baselinePrompt) {
		return nil, errors.New("baseline prompt is not valid UTF-8")
	}
	if train == nil || validation == nil {
		return nil, errors.New("train and validation eval sets are required")
	}
	var err error
	train, err = cloneEvalSet(train)
	if err != nil {
		return nil, fmt.Errorf("clone train eval set: %w", err)
	}
	validation, err = cloneEvalSet(validation)
	if err != nil {
		return nil, fmt.Errorf("clone validation eval set: %w", err)
	}
	if err := validateEvalSet(train); err != nil {
		return nil, fmt.Errorf("validate train eval set: %w", err)
	}
	if err := validateEvalSet(validation); err != nil {
		return nil, fmt.Errorf("validate validation eval set: %w", err)
	}
	if train.EvalSetID == validation.EvalSetID {
		return nil, errors.New("train and validation eval set ids must differ")
	}
	trainCaseIDs := make(map[string]struct{}, len(train.EvalCases))
	for _, evalCase := range train.EvalCases {
		trainCaseIDs[evalCase.EvalID] = struct{}{}
	}
	for _, evalCase := range validation.EvalCases {
		if _, ok := trainCaseIDs[evalCase.EvalID]; ok {
			return nil, fmt.Errorf("case %q appears in both train and validation", evalCase.EvalID)
		}
	}
	if err := p.validateEvalSetVariants(train); err != nil {
		return nil, fmt.Errorf("validate train variants: %w", err)
	}
	if err := p.validateEvalSetVariants(validation); err != nil {
		return nil, fmt.Errorf("validate validation variants: %w", err)
	}
	if err := p.validateCriticalCases(validation); err != nil {
		return nil, err
	}
	if strings.Contains(baselinePrompt, promptVariantMarkerPrefix) {
		return nil, errors.New("baseline prompt contains a reserved candidate marker")
	}
	startedAt := p.clock().UTC()
	baselineTrain, err := p.evaluate(ctx, train, "baseline", baselinePrompt)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline train: %w", err)
	}
	baselineValidation, err := p.evaluate(ctx, validation, "baseline", baselinePrompt)
	if err != nil {
		return nil, fmt.Errorf("evaluate baseline validation: %w", err)
	}
	baselinePair := EvaluationPair{Train: *baselineTrain, Validation: *baselineValidation}
	baselineUsage, err := baselineTrain.Usage.AddChecked(baselineValidation.Usage)
	if err != nil {
		return nil, fmt.Errorf("aggregate baseline usage: %w", err)
	}
	totalRunUsage := baselineUsage
	baselineHash := HashText(baselinePrompt)
	baselineSemanticHash := HashText(semanticPromptContent(baselinePrompt))
	surfaceID := surfaceIDFromConfig(p.config.Surface)
	report := &Report{
		SchemaVersion: p.config.SchemaVersion,
		RunID:         p.config.RunID,
		Mode:          p.config.Mode,
		Seed:          p.config.Seed,
		StartedAt:     startedAt,
		ModelConfig:   p.config.FakeEngine,
		BaselinePrompt: PromptSnapshot{
			ID:             "baseline",
			Content:        baselinePrompt,
			SHA256:         baselineHash,
			SemanticSHA256: baselineSemanticHash,
			SurfaceID:      surfaceID,
		},
		Baseline: baselinePair,
		FailureAttributionStats: mergeAttributionStats(
			baselineTrain.AttributionStats,
			baselineValidation.AttributionStats,
		),
		Rounds: make([]RoundReport, 0, p.config.MaxRounds),
	}
	if report.RunID == "" {
		report.RunID = fmt.Sprintf("promptiter-regression-%d-%s", p.config.Seed, baselineHash[:12])
	}

	var selected *roundSelection
	seenCandidateIDs := make(map[string]struct{}, p.config.MaxRounds)
	for round := 1; round <= p.config.MaxRounds; round++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		optimizerTrain, err := cloneEvaluationSummary(baselineTrain)
		if err != nil {
			return nil, fmt.Errorf("clone baseline train for round %d optimizer: %w", round, err)
		}
		candidate, err := p.optimizer.Propose(ctx, OptimizeRequest{
			Round:          round,
			BaselinePrompt: baselinePrompt,
			Train:          optimizerTrain,
		})
		if err != nil {
			return nil, fmt.Errorf("propose round %d: %w", round, err)
		}
		if err := p.validateCandidate(candidate, round, seenCandidateIDs); err != nil {
			return nil, fmt.Errorf("validate round %d candidate: %w", round, err)
		}
		candidate.PromptHash = HashText(candidate.Prompt)
		seenCandidateIDs[candidate.ID] = struct{}{}
		candidateTrain, err := p.evaluate(ctx, train, candidate.ID, candidate.Prompt)
		if err != nil {
			return nil, fmt.Errorf("evaluate round %d candidate train: %w", round, err)
		}
		candidateValidation, err := p.evaluate(ctx, validation, candidate.ID, candidate.Prompt)
		if err != nil {
			return nil, fmt.Errorf("evaluate round %d candidate validation: %w", round, err)
		}
		if err := validateValidationRerun(candidateValidation); err != nil {
			return nil, fmt.Errorf("validate round %d candidate validation rerun: %w", round, err)
		}
		if err := validateFallbackBindings(baselineTrain, candidateTrain, p.config.FakeEngine.FallbackVariant); err != nil {
			return nil, fmt.Errorf("validate round %d train fallback bindings: %w", round, err)
		}
		if err := validateFallbackBindings(
			baselineValidation,
			candidateValidation,
			p.config.FakeEngine.FallbackVariant,
		); err != nil {
			return nil, fmt.Errorf("validate round %d validation fallback bindings: %w", round, err)
		}
		trainDelta, err := ComputeDelta(baselineTrain, candidateTrain)
		if err != nil {
			return nil, fmt.Errorf("compute round %d train delta: %w", round, err)
		}
		validationDelta, err := ComputeDelta(baselineValidation, candidateValidation)
		if err != nil {
			return nil, fmt.Errorf("compute round %d validation delta: %w", round, err)
		}
		candidateUsage, err := candidateTrain.Usage.AddChecked(candidateValidation.Usage)
		if err != nil {
			return nil, fmt.Errorf("aggregate round %d candidate usage: %w", round, err)
		}
		totalRunUsage, err = totalRunUsage.AddChecked(candidateUsage)
		if err != nil {
			return nil, fmt.Errorf("aggregate total run usage at round %d: %w", round, err)
		}
		decision, err := EvaluateGate(p.config.Gate, GateInput{
			Delta:               validationDelta,
			BaselineValidation:  baselineValidation,
			CandidateValidation: candidateValidation,
			BaselineUsage:       baselineUsage,
			CandidateUsage:      candidateUsage,
			BaselinePromptHash:  baselineSemanticHash,
			CandidatePromptHash: HashText(semanticPromptContent(candidate.Prompt)),
		})
		if err != nil {
			return nil, fmt.Errorf("evaluate round %d gate: %w", round, err)
		}
		candidateSnapshot := PromptSnapshot{
			ID:             candidate.ID,
			Content:        candidate.Prompt,
			SHA256:         candidate.PromptHash,
			SemanticSHA256: HashText(semanticPromptContent(candidate.Prompt)),
			SurfaceID:      candidate.SurfaceID,
			PatchReason:    candidate.Reason,
		}
		evaluationPair := EvaluationPair{Train: *candidateTrain, Validation: *candidateValidation}
		deltaPair := DeltaPair{Train: *trainDelta, Validation: *validationDelta}
		roundReport := RoundReport{
			Round:        round,
			Candidate:    candidateSnapshot,
			Evaluation:   evaluationPair,
			Delta:        deltaPair,
			GateDecision: decision,
			Usage:        candidateUsage,
		}
		report.Rounds = append(report.Rounds, roundReport)
		selection := &roundSelection{
			candidate:  candidateSnapshot,
			evaluation: evaluationPair,
			delta:      deltaPair,
			decision:   decision,
			usage:      candidateUsage,
			round:      round,
		}
		if betterSelection(selection, selected) {
			selected = selection
		}
	}
	if selected == nil {
		return nil, errors.New("optimizer produced no candidate")
	}
	report.CandidatePrompt = selected.candidate
	report.Candidate = selected.evaluation
	report.Delta = selected.delta
	report.GateDecision = selected.decision
	report.CostLatencySummary = CostLatencySummary{
		Baseline:  baselineUsage,
		Candidate: selected.usage,
		Delta:     subtractUsage(selected.usage, baselineUsage),
		TotalRun:  totalRunUsage,
	}
	report.CompletedAt = p.clock().UTC()
	if report.CompletedAt.Before(report.StartedAt) {
		report.CompletedAt = report.StartedAt
	}
	report.WallTimeMS = report.CompletedAt.Sub(report.StartedAt).Milliseconds()
	return report, nil
}

func (p *Pipeline) validateEvalSetVariants(set *EvalSet) error {
	allowed := map[string]struct{}{"baseline": {}}
	for _, candidate := range p.config.Candidates {
		allowed[candidate.ID] = struct{}{}
	}
	for _, evalCase := range set.EvalCases {
		if _, ok := evalCase.FakeResponses["baseline"]; !ok {
			return fmt.Errorf("case %q has no baseline fake response", evalCase.EvalID)
		}
		for variantID := range evalCase.FakeResponses {
			if _, ok := allowed[variantID]; !ok {
				return fmt.Errorf("case %q has unconfigured fake response variant %q", evalCase.EvalID, variantID)
			}
		}
	}
	return nil
}

func (p *Pipeline) validateCriticalCases(validation *EvalSet) error {
	available := make(map[string]struct{}, len(validation.EvalCases))
	for _, evalCase := range validation.EvalCases {
		available[evalCase.EvalID] = struct{}{}
	}
	for _, id := range p.config.Gate.CriticalCaseIDs {
		if _, ok := available[id]; !ok {
			return fmt.Errorf("configured critical case %q is absent from validation", id)
		}
	}
	return nil
}

func (p *Pipeline) evaluate(
	ctx context.Context,
	set *EvalSet,
	variantID string,
	prompt string,
) (*EvaluationSummary, error) {
	coverage := snapshotEvalSetCoverage(set)
	isolatedSet, err := cloneEvalSet(set)
	if err != nil {
		return nil, fmt.Errorf("clone eval set: %w", err)
	}
	summary, err := p.evaluator.Evaluate(ctx, isolatedSet, variantID, prompt)
	if err != nil {
		return nil, err
	}
	if summary == nil {
		return nil, errors.New("evaluator returned a nil summary")
	}
	if summary.EvalSetID != coverage.evalSetID {
		return nil, fmt.Errorf("evaluator returned eval set %q for %q", summary.EvalSetID, coverage.evalSetID)
	}
	if summary.VariantID != variantID {
		return nil, fmt.Errorf("evaluator returned variant %q for %q", summary.VariantID, variantID)
	}
	if err := validateEvaluationSummary(summary); err != nil {
		return nil, err
	}
	expectedSemanticHash := HashText(semanticPromptContent(prompt))
	for _, evalCase := range summary.Cases {
		if evalCase.UsedFallback {
			if evalCase.ResponseVariantID != p.config.FakeEngine.FallbackVariant {
				return nil, fmt.Errorf(
					"case %q fallback source %q does not match configured fallback %q",
					evalCase.CaseID,
					evalCase.ResponseVariantID,
					p.config.FakeEngine.FallbackVariant,
				)
			}
			if evalCase.ResponsePromptSHA256 == "" {
				return nil, fmt.Errorf("case %q fallback output has no prompt semantic hash", evalCase.CaseID)
			}
			continue
		}
		if evalCase.ResponsePromptSHA256 != expectedSemanticHash {
			return nil, fmt.Errorf(
				"case %q response prompt semantic hash %q does not match evaluated prompt %q",
				evalCase.CaseID,
				evalCase.ResponsePromptSHA256,
				expectedSemanticHash,
			)
		}
	}
	if err := validateSummaryCoverage(coverage, summary); err != nil {
		return nil, err
	}
	isolatedSummary, err := cloneEvaluationSummary(summary)
	if err != nil {
		return nil, fmt.Errorf("clone evaluation summary: %w", err)
	}
	return isolatedSummary, nil
}

func validateFallbackBindings(
	baseline *EvaluationSummary,
	candidate *EvaluationSummary,
	fallbackVariant string,
) error {
	if baseline == nil || candidate == nil {
		return errors.New("baseline and candidate summaries are required")
	}
	baselineByID := make(map[string]CaseResult, len(baseline.Cases))
	for _, evalCase := range baseline.Cases {
		baselineByID[evalCase.CaseID] = evalCase
	}
	for _, evalCase := range candidate.Cases {
		if !evalCase.UsedFallback {
			continue
		}
		baselineCase, ok := baselineByID[evalCase.CaseID]
		if !ok {
			return fmt.Errorf("fallback case %q is absent from baseline", evalCase.CaseID)
		}
		if baselineCase.ResponseVariantID != fallbackVariant || baselineCase.UsedFallback {
			return fmt.Errorf("baseline case %q is not a direct %q output", evalCase.CaseID, fallbackVariant)
		}
		if evalCase.ResponseVariantID != baselineCase.ResponseVariantID ||
			evalCase.ResponsePromptSHA256 != baselineCase.ResponsePromptSHA256 {
			return fmt.Errorf("fallback case %q does not match its verified baseline source", evalCase.CaseID)
		}
		candidateAsBaseline := evalCase
		candidateAsBaseline.UsedFallback = false
		if !reflect.DeepEqual(candidateAsBaseline, baselineCase) {
			return fmt.Errorf("fallback case %q does not reproduce its verified baseline result", evalCase.CaseID)
		}
	}
	return nil
}

func validateValidationRerun(candidate *EvaluationSummary) error {
	if candidate == nil {
		return errors.New("candidate validation summary is required")
	}
	fallbackCases := make([]string, 0)
	for _, evalCase := range candidate.Cases {
		if evalCase.UsedFallback {
			fallbackCases = append(fallbackCases, evalCase.CaseID)
		}
	}
	if len(fallbackCases) == 0 {
		return nil
	}
	sort.Strings(fallbackCases)
	return fmt.Errorf(
		"candidate validation must rerun every case; baseline fallback used for %v",
		fallbackCases,
	)
}

type evalSetCoverageSnapshot struct {
	evalSetID     string
	passThreshold float64
	cases         map[string]bool
}

func snapshotEvalSetCoverage(set *EvalSet) evalSetCoverageSnapshot {
	cases := make(map[string]bool, len(set.EvalCases))
	for _, evalCase := range set.EvalCases {
		cases[evalCase.EvalID] = evalCase.Critical
	}
	return evalSetCoverageSnapshot{
		evalSetID:     set.EvalSetID,
		passThreshold: *set.PassThreshold,
		cases:         cases,
	}
}

func validateSummaryCoverage(coverage evalSetCoverageSnapshot, summary *EvaluationSummary) error {
	if math.Abs(summary.PassThreshold-coverage.passThreshold) > scoreEpsilon {
		return fmt.Errorf(
			"evaluator returned pass threshold %.9f, expected %.9f",
			summary.PassThreshold,
			coverage.passThreshold,
		)
	}
	if len(summary.Cases) != len(coverage.cases) {
		return fmt.Errorf("evaluator returned %d cases, expected %d", len(summary.Cases), len(coverage.cases))
	}
	expected := make(map[string]bool, len(coverage.cases))
	for id, critical := range coverage.cases {
		expected[id] = critical
	}
	for _, result := range summary.Cases {
		critical, ok := expected[result.CaseID]
		if !ok {
			return fmt.Errorf("evaluator returned unknown case %q", result.CaseID)
		}
		if result.Critical != critical {
			return fmt.Errorf("evaluator changed critical flag for case %q", result.CaseID)
		}
		expectedPassed := scorePasses(result.Score, coverage.passThreshold) && !result.HardFail
		if result.Passed != expectedPassed {
			return fmt.Errorf("evaluator returned inconsistent pass status for case %q", result.CaseID)
		}
		delete(expected, result.CaseID)
	}
	if len(expected) != 0 {
		return fmt.Errorf("evaluator omitted cases %v", expected)
	}
	return nil
}

func cloneEvalSet(set *EvalSet) (*EvalSet, error) {
	data, err := json.Marshal(set)
	if err != nil {
		return nil, err
	}
	var cloned EvalSet
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}

func cloneEvaluationSummary(summary *EvaluationSummary) (*EvaluationSummary, error) {
	data, err := json.Marshal(summary)
	if err != nil {
		return nil, err
	}
	var cloned EvaluationSummary
	if err := json.Unmarshal(data, &cloned); err != nil {
		return nil, err
	}
	return &cloned, nil
}

func validateEvaluationSummary(summary *EvaluationSummary) error {
	if err := validateSummaryForGate(summary); err != nil {
		return err
	}
	usage := Usage{}
	passedCases := 0
	failedCases := 0
	hardFailedCases := 0
	attributionStats := make(map[FailureCategory]int)
	for _, evalCase := range summary.Cases {
		if !validCandidateID(evalCase.ResponseVariantID) {
			return fmt.Errorf(
				"case %q has invalid response variant id %q",
				evalCase.CaseID,
				evalCase.ResponseVariantID,
			)
		}
		if evalCase.UsedFallback {
			if summary.VariantID == "baseline" {
				return fmt.Errorf("baseline case %q unexpectedly used fallback output", evalCase.CaseID)
			}
			if evalCase.ResponseVariantID == summary.VariantID {
				return fmt.Errorf(
					"case %q marks requested variant %q as fallback output",
					evalCase.CaseID,
					summary.VariantID,
				)
			}
		} else if evalCase.ResponseVariantID != summary.VariantID {
			return fmt.Errorf(
				"case %q response variant %q does not match summary variant %q",
				evalCase.CaseID,
				evalCase.ResponseVariantID,
				summary.VariantID,
			)
		}
		if evalCase.ResponsePromptSHA256 != "" && !validSHA256(evalCase.ResponsePromptSHA256) {
			return fmt.Errorf("case %q has invalid response prompt semantic hash", evalCase.CaseID)
		}
		if summary.VariantID != "baseline" && !evalCase.UsedFallback && evalCase.ResponsePromptSHA256 == "" {
			return fmt.Errorf("case %q explicit candidate output has no prompt semantic hash", evalCase.CaseID)
		}
		if err := validateUsage(evalCase.Usage); err != nil {
			return fmt.Errorf("case %q usage: %w", evalCase.CaseID, err)
		}
		var err error
		usage, err = usage.AddChecked(evalCase.Usage)
		if err != nil {
			return fmt.Errorf("aggregate case %q usage: %w", evalCase.CaseID, err)
		}
		if evalCase.Passed {
			passedCases++
		} else {
			failedCases++
		}
		if evalCase.HardFail {
			hardFailedCases++
		}
		if evalCase.Passed {
			if evalCase.PrimaryFailure != nil || len(evalCase.FailureAttributions) != 0 {
				return fmt.Errorf("passed case %q unexpectedly has failure attribution", evalCase.CaseID)
			}
		} else {
			if len(evalCase.FailureAttributions) == 0 || evalCase.PrimaryFailure == nil {
				return fmt.Errorf("failed case %q has no primary failure attribution", evalCase.CaseID)
			}
			if !reflect.DeepEqual(*evalCase.PrimaryFailure, evalCase.FailureAttributions[0]) {
				return fmt.Errorf("failed case %q primary attribution is not the first attribution", evalCase.CaseID)
			}
			attributionStats[evalCase.PrimaryFailure.Category]++
		}
		attributionCategories := make(map[FailureCategory]struct{}, len(evalCase.FailureAttributions))
		for _, attribution := range evalCase.FailureAttributions {
			if !knownFailureCategory(attribution.Category) ||
				!finiteScore(attribution.Confidence) || attribution.Confidence < 0 || attribution.Confidence > 1 ||
				strings.TrimSpace(attribution.Evidence) == "" {
				return fmt.Errorf("case %q has invalid failure attribution %+v", evalCase.CaseID, attribution)
			}
			if _, ok := attributionCategories[attribution.Category]; ok {
				return fmt.Errorf("case %q has duplicate failure attribution category %q", evalCase.CaseID, attribution.Category)
			}
			attributionCategories[attribution.Category] = struct{}{}
		}
	}
	if !reflect.DeepEqual(usage, summary.Usage) {
		return fmt.Errorf("summary usage %+v does not match case usage %+v", summary.Usage, usage)
	}
	if summary.PassedCases != passedCases || summary.FailedCases != failedCases || summary.HardFailedCases != hardFailedCases {
		return errors.New("summary pass/fail counters do not match case results")
	}
	if !equalAttributionStats(summary.AttributionStats, attributionStats) {
		return fmt.Errorf("summary attribution stats %v do not match case primaries %v", summary.AttributionStats, attributionStats)
	}
	return nil
}

func equalAttributionStats(left, right map[FailureCategory]int) bool {
	if len(left) != len(right) {
		return false
	}
	for category, count := range left {
		if count < 0 || right[category] != count {
			return false
		}
	}
	return true
}

func (p *Pipeline) validateCandidate(
	candidate *Candidate,
	round int,
	seen map[string]struct{},
) error {
	if candidate == nil {
		return errors.New("optimizer returned a nil candidate")
	}
	if candidate.ID == "" || !validCandidateID(candidate.ID) {
		return fmt.Errorf("candidate id %q is invalid", candidate.ID)
	}
	if candidate.ID == "baseline" {
		return errors.New("candidate id \"baseline\" is reserved")
	}
	if round > len(p.config.Candidates) || candidate.ID != p.config.Candidates[round-1].ID {
		return fmt.Errorf("candidate id %q does not match configured round %d candidate", candidate.ID, round)
	}
	if _, ok := seen[candidate.ID]; ok {
		return fmt.Errorf("candidate id %q was already evaluated", candidate.ID)
	}
	if candidate.Round != round {
		return fmt.Errorf("candidate round %d does not match requested round %d", candidate.Round, round)
	}
	if stringsTrimmedEmpty(candidate.Prompt) {
		return errors.New("candidate prompt is empty")
	}
	if !utf8.ValidString(candidate.Prompt) || !utf8.ValidString(candidate.Reason) {
		return errors.New("candidate prompt and reason must be valid UTF-8")
	}
	if strings.Count(candidate.Prompt, promptVariantMarkerPrefix) != 1 {
		return errors.New("candidate prompt must contain exactly one deterministic variant marker")
	}
	markerID, markerSeed, marked := promptVariantMetadata(candidate.Prompt)
	if !marked || markerID != candidate.ID {
		return fmt.Errorf("candidate prompt marker %q does not match id %q", markerID, candidate.ID)
	}
	if markerSeed != p.config.Seed {
		return fmt.Errorf("candidate prompt marker seed %d does not match config seed %d", markerSeed, p.config.Seed)
	}
	computedHash := HashText(candidate.Prompt)
	if candidate.PromptHash != "" && candidate.PromptHash != computedHash {
		return fmt.Errorf("candidate prompt hash %q does not match computed hash %q", candidate.PromptHash, computedHash)
	}
	expectedSurfaceID := surfaceIDFromConfig(p.config.Surface)
	if candidate.SurfaceID != expectedSurfaceID {
		return fmt.Errorf("candidate surface %q does not match target %q", candidate.SurfaceID, expectedSurfaceID)
	}
	if strings.TrimSpace(candidate.Reason) == "" {
		return errors.New("candidate patch reason is empty")
	}
	if candidate.Profile == nil || candidate.PatchSet == nil {
		return errors.New("candidate PromptIter profile and patch set are required")
	}
	if candidate.Profile.StructureID != p.config.Surface.StructureID || len(candidate.Profile.Overrides) != 1 {
		return errors.New("candidate profile does not contain exactly one target structure override")
	}
	override := candidate.Profile.Overrides[0]
	if override.SurfaceID != expectedSurfaceID || !textSurfaceValueMatches(override.Value, candidate.Prompt) {
		return errors.New("candidate profile override does not match candidate prompt")
	}
	if len(candidate.PatchSet.Patches) != 1 {
		return errors.New("candidate patch set must contain exactly one patch")
	}
	patch := candidate.PatchSet.Patches[0]
	if patch.SurfaceID != expectedSurfaceID || !textSurfaceValueMatches(patch.Value, candidate.Prompt) ||
		!reflect.DeepEqual(override.Value, patch.Value) || patch.Reason != candidate.Reason {
		return errors.New("candidate patch does not match target prompt")
	}
	return nil
}

func textSurfaceValueMatches(value structure.SurfaceValue, prompt string) bool {
	return value.Text != nil && *value.Text == prompt && value.PromptSyntax == nil &&
		len(value.FewShot) == 0 && value.Model == nil && len(value.Tools) == 0 && len(value.Skills) == 0
}

type roundSelection struct {
	candidate  PromptSnapshot
	evaluation EvaluationPair
	delta      DeltaPair
	decision   GateDecision
	usage      Usage
	round      int
}

func betterSelection(candidate, incumbent *roundSelection) bool {
	if incumbent == nil {
		return true
	}
	if candidate.decision.Accepted != incumbent.decision.Accepted {
		return candidate.decision.Accepted
	}
	leftScore := candidate.evaluation.Validation.OverallScore
	rightScore := incumbent.evaluation.Validation.OverallScore
	if math.Abs(leftScore-rightScore) > scoreEpsilon {
		return leftScore > rightScore
	}
	if candidate.delta.Validation.NewHardFails != incumbent.delta.Validation.NewHardFails {
		return candidate.delta.Validation.NewHardFails < incumbent.delta.Validation.NewHardFails
	}
	if candidate.delta.Validation.NewFailures != incumbent.delta.Validation.NewFailures {
		return candidate.delta.Validation.NewFailures < incumbent.delta.Validation.NewFailures
	}
	if math.Abs(candidate.usage.CostUSD-incumbent.usage.CostUSD) > scoreEpsilon {
		return candidate.usage.CostUSD < incumbent.usage.CostUSD
	}
	return candidate.round < incumbent.round
}

func mergeAttributionStats(values ...map[FailureCategory]int) map[FailureCategory]int {
	merged := make(map[FailureCategory]int)
	for _, value := range values {
		for category, count := range value {
			merged[category] += count
		}
	}
	return merged
}

func subtractUsage(left, right Usage) UsageDelta {
	return UsageDelta{
		ModelCalls:   left.ModelCalls - right.ModelCalls,
		ToolCalls:    left.ToolCalls - right.ToolCalls,
		InputTokens:  left.InputTokens - right.InputTokens,
		OutputTokens: left.OutputTokens - right.OutputTokens,
		CostUSD:      left.CostUSD - right.CostUSD,
		LatencyMS:    left.LatencyMS - right.LatencyMS,
	}
}

func stringsTrimmedEmpty(value string) bool {
	return strings.TrimSpace(value) == ""
}
