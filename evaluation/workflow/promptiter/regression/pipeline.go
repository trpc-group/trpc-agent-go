//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"context"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// Dependencies supplies deterministic audit policy modules.
type Dependencies struct {
	Attributor  Attributor
	DeltaEngine DeltaEngine
	Gate        Gate
	Now         func() time.Time
}

// Analyzer converts one completed PromptIter run into a release audit report.
type Analyzer interface {
	Analyze(context.Context, *RunSpec, *engine.RunResult, UsageSummary) (*RunResult, error)
}

type analyzer struct {
	deps Dependencies
}

// New creates a PromptIter audit analyzer.
func New(deps Dependencies) (Analyzer, error) {
	switch {
	case deps.Attributor == nil:
		return nil, errors.New("attributor is nil")
	case deps.DeltaEngine == nil:
		return nil, errors.New("delta engine is nil")
	case deps.Gate == nil:
		return nil, errors.New("gate is nil")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &analyzer{deps: deps}, nil
}

func (a *analyzer) Analyze(
	ctx context.Context,
	spec *RunSpec,
	source *engine.RunResult,
	usage UsageSummary,
) (result *RunResult, err error) {
	started := a.deps.Now().UTC()
	result = &RunResult{
		SchemaVersion: CurrentSchemaVersion,
		Status:        RunStatusRunning,
		StartedAt:     started,
		Decision:      DecisionRejected,
		Usage:         usage,
	}
	defer func() {
		result.EndedAt = a.deps.Now().UTC()
		if err == nil {
			result.Status = RunStatusSucceeded
			return
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			result.Status = RunStatusCanceled
		} else {
			result.Status = RunStatusFailed
		}
		policy := AuditPolicy{}
		if spec != nil {
			policy = spec.Audit
		}
		result.ErrorMessage = sanitizeContent(policy, err.Error())
	}()
	if err = ctx.Err(); err != nil {
		return result, err
	}
	if err = spec.Validate(); err != nil {
		return result, fmt.Errorf("preflight: %w", err)
	}
	result.Usage, err = normalizeUsageSummary(result.Usage)
	if err != nil {
		return result, fmt.Errorf("preflight usage: %w", err)
	}
	result.Usage.Source = sanitizeContent(spec.Audit, result.Usage.Source)
	if err = validatePromptIterResult(source, spec); err != nil {
		return result, fmt.Errorf("PromptIter result: %w", err)
	}
	result.RunID = spec.RunID
	result.Spec = cloneRunSpec(spec)
	result.PromptIter = promptIterConfiguration(source.Configuration)
	baselineProfile := initialProfile(source)
	if baselineProfile == nil {
		return result, errors.New("PromptIter result has no initial profile")
	}
	result.BaselineProfile = sanitizeProfile(baselineProfile, spec.Audit)
	if err = a.buildAudit(ctx, source, result, baselineProfile); err != nil {
		return result, err
	}
	return result, nil
}

func validatePromptIterResult(source *engine.RunResult, spec *RunSpec) error {
	if source == nil {
		return errors.New("result is nil")
	}
	if source.Status != engine.RunStatusSucceeded {
		return fmt.Errorf("run status is %q", source.Status)
	}
	if err := validatePromptIterConfiguration(source.Configuration, spec); err != nil {
		return fmt.Errorf("configuration: %w", err)
	}
	if source.ErrorMessage != "" {
		return errors.New("succeeded run contains an error message")
	}
	if source.InitialProfile == nil {
		return errors.New("effective initial profile is nil")
	}
	if source.BaselineValidation == nil {
		return errors.New("baseline validation is nil")
	}
	if source.AcceptedProfile == nil {
		return errors.New("accepted profile is nil")
	}
	if len(source.Rounds) == 0 {
		return errors.New("optimization rounds are missing")
	}
	expectedProfile := source.InitialProfile
	for index := range source.Rounds {
		round := &source.Rounds[index]
		expectedRound := index + 1
		if round.Round != expectedRound {
			return fmt.Errorf("round %d has sequence number %d", expectedRound, round.Round)
		}
		if round.InputProfile == nil || round.Train == nil || round.OutputProfile == nil ||
			round.Validation == nil || round.Acceptance == nil || round.Stop == nil {
			return fmt.Errorf("round %d has incomplete execution evidence", round.Round)
		}
		if !finite(round.Acceptance.ScoreDelta) {
			return fmt.Errorf("round %d acceptance score delta is not finite", round.Round)
		}
		lastRound := index == len(source.Rounds)-1
		if round.Stop.ShouldStop != lastRound {
			return fmt.Errorf(
				"round %d stop state does not match its position in completed history",
				round.Round,
			)
		}
		if round.Stop.ShouldStop && strings.TrimSpace(round.Stop.Reason) == "" {
			return fmt.Errorf("round %d stopped without a reason", round.Round)
		}
		matches, err := sameProfile(round.InputProfile, expectedProfile)
		if err != nil {
			return fmt.Errorf("compare round %d input profile: %w", round.Round, err)
		}
		if !matches {
			return fmt.Errorf("round %d input profile does not match the accepted state", round.Round)
		}
		if round.Acceptance.Accepted {
			expectedProfile = round.OutputProfile
		}
	}
	if source.CurrentRound != len(source.Rounds) {
		return fmt.Errorf(
			"current round %d does not match completed round count %d",
			source.CurrentRound, len(source.Rounds),
		)
	}
	if source.CurrentRound > source.Configuration.MaxRounds {
		return fmt.Errorf(
			"completed round count %d exceeds configured max rounds %d",
			source.CurrentRound,
			source.Configuration.MaxRounds,
		)
	}
	matches, err := sameProfile(source.AcceptedProfile, expectedProfile)
	if err != nil {
		return fmt.Errorf("compare final accepted profile: %w", err)
	}
	if !matches {
		return errors.New("accepted profile does not match round history")
	}
	return nil
}

func sameProfile(left, right *promptiter.Profile) (bool, error) {
	leftHash, err := ProfileHash(left)
	if err != nil {
		return false, err
	}
	rightHash, err := ProfileHash(right)
	if err != nil {
		return false, err
	}
	return leftHash == rightHash, nil
}

func (a *analyzer) buildAudit(
	ctx context.Context,
	source *engine.RunResult,
	result *RunResult,
	baselineProfile *promptiter.Profile,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	critical := stringSet(result.Spec.CriticalCaseIDs)
	var err error
	result.BaselineValidation, err = adaptEvaluation(
		source.BaselineValidation, baselineProfile, critical, result.Spec.Audit,
	)
	if err != nil {
		return fmt.Errorf("adapt baseline validation: %w", err)
	}
	if err := validateCriticalCases(result.BaselineValidation, result.Spec.CriticalCaseIDs); err != nil {
		return fmt.Errorf("validate critical cases: %w", err)
	}
	markConfiguredMetricCoverage(result.BaselineValidation, result.Spec.MetricPolicies)
	markExpectedRunCoverage(result.BaselineValidation, result.Spec.Runtime.NumRuns)
	result.BaselineTrain, err = adaptEvaluation(
		source.Rounds[0].Train, baselineProfile, critical, result.Spec.Audit,
	)
	if err != nil {
		return fmt.Errorf("adapt baseline train: %w", err)
	}
	markConfiguredMetricCoverage(result.BaselineTrain, result.Spec.MetricPolicies)
	markExpectedRunCoverage(result.BaselineTrain, result.Spec.Runtime.NumRuns)
	if err := a.attributeFailures(ctx, result); err != nil {
		return err
	}
	trainByProfile, err := buildTrainIndex(
		ctx, source, critical, result.Spec.Audit, result.Spec.MetricPolicies,
		result.Spec.Runtime.NumRuns,
	)
	if err != nil {
		return err
	}
	for _, round := range source.Rounds {
		if err := ctx.Err(); err != nil {
			return err
		}
		candidateResult, err := a.auditRound(
			result, baselineProfile, round, trainByProfile, critical,
		)
		if err != nil {
			return err
		}
		result.Candidates = append(result.Candidates, *candidateResult)
	}
	selectCandidate(result)
	return nil
}

func validateCriticalCases(snapshot *EvaluationSnapshot, configured []string) error {
	available := make(map[string]int, len(snapshot.Cases))
	for _, caseResult := range snapshot.Cases {
		available[caseResult.CaseID]++
	}
	for _, caseID := range configured {
		switch available[caseID] {
		case 0:
			return fmt.Errorf("configured critical case %q is absent from baseline validation", caseID)
		case 1:
			continue
		default:
			return fmt.Errorf(
				"configured critical case %q is ambiguous across evaluation sets", caseID,
			)
		}
	}
	return nil
}

func (a *analyzer) attributeFailures(ctx context.Context, result *RunResult) error {
	for index := range result.BaselineTrain.Cases {
		if err := ctx.Err(); err != nil {
			return err
		}
		caseResult := &result.BaselineTrain.Cases[index]
		if caseResult.Passed && !hasExecutionError(caseResult) {
			continue
		}
		attribution, err := a.deps.Attributor.Attribute(ctx, caseResult)
		if err != nil {
			return fmt.Errorf("attribute case %q: %w", caseResult.CaseID, err)
		}
		if attribution == nil {
			return fmt.Errorf("attribute case %q returned nil result", caseResult.CaseID)
		}
		if attribution.CaseID != caseResult.CaseID {
			return fmt.Errorf(
				"attribute case %q returned result for case %q",
				caseResult.CaseID, attribution.CaseID,
			)
		}
		if attribution.EvalSetID == "" {
			attribution.EvalSetID = caseResult.EvalSetID
		} else if attribution.EvalSetID != caseResult.EvalSetID {
			return fmt.Errorf(
				"attribute case %q returned eval set %q instead of %q",
				caseResult.CaseID, attribution.EvalSetID, caseResult.EvalSetID,
			)
		}
		if attribution.Category == "" || strings.TrimSpace(attribution.Reason) == "" ||
			len(attribution.Evidence) == 0 {
			return fmt.Errorf("attribute case %q returned incomplete evidence", caseResult.CaseID)
		}
		for _, evidence := range attribution.Evidence {
			if strings.TrimSpace(evidence.Reason) == "" {
				return fmt.Errorf("attribute case %q returned empty evidence reason", caseResult.CaseID)
			}
		}
		attribution = sanitizeAttribution(attribution, result.Spec.Audit)
		result.Attributions = append(result.Attributions, *attribution)
	}
	sort.Slice(result.Attributions, func(i, j int) bool {
		if result.Attributions[i].EvalSetID != result.Attributions[j].EvalSetID {
			return result.Attributions[i].EvalSetID < result.Attributions[j].EvalSetID
		}
		return result.Attributions[i].CaseID < result.Attributions[j].CaseID
	})
	result.AttributionCounts = make(map[FailureCategory]int, len(result.Attributions))
	for _, attribution := range result.Attributions {
		result.AttributionCounts[attribution.Category]++
	}
	return nil
}

func (a *analyzer) auditRound(
	result *RunResult,
	baselineProfile *promptiter.Profile,
	round engine.RoundResult,
	trainByProfile map[string][]trainEvidence,
	critical map[string]struct{},
) (*CandidateResult, error) {
	hash, err := ProfileHash(round.OutputProfile)
	if err != nil {
		return nil, fmt.Errorf("hash round %d output profile: %w", round.Round, err)
	}
	validation, err := adaptEvaluation(
		round.Validation, round.OutputProfile, critical, result.Spec.Audit,
	)
	if err != nil {
		return nil, fmt.Errorf("adapt round %d validation: %w", round.Round, err)
	}
	markConfiguredMetricCoverage(validation, result.Spec.MetricPolicies)
	markExpectedRunCoverage(validation, result.Spec.Runtime.NumRuns)
	validationDelta, err := a.deps.DeltaEngine.Compare(
		result.BaselineValidation,
		validation,
		result.Spec.MetricPolicies,
	)
	if err != nil {
		return nil, fmt.Errorf("compare round %d validation: %w", round.Round, err)
	}
	candidate := &CandidateResult{
		Candidate: Candidate{
			ID:          fmt.Sprintf("round-%d-%s", round.Round, hash[:12]),
			Round:       round.Round,
			Profile:     sanitizeProfile(round.OutputProfile, result.Spec.Audit),
			ProfileHash: hash,
		},
		Validation:      validation,
		ValidationDelta: validationDelta,
	}
	profileChanged, err := profileChanged(round.InputProfile, round.OutputProfile)
	if err != nil {
		return nil, fmt.Errorf("compare round %d input and output profiles: %w", round.Round, err)
	}
	candidate.ProfileChanged = profileChanged
	candidate.PromptIterAccepted = round.Acceptance.Accepted
	candidate.PromptIterReason = sanitizeContent(result.Spec.Audit, round.Acceptance.Reason)
	if round.Stop.ShouldStop {
		candidate.PromptIterShouldStop = true
		candidate.PromptIterStopReason = sanitizeContent(result.Spec.Audit, round.Stop.Reason)
	}
	train, err := roundCandidateTrain(
		round,
		trainByProfile[hash],
		critical,
		result.Spec.Audit,
		result.Spec.MetricPolicies,
		result.Spec.Runtime.NumRuns,
	)
	if err != nil {
		return nil, fmt.Errorf("adapt round %d candidate train: %w", round.Round, err)
	}
	if train != nil {
		candidate.Train = train
		candidate.TrainDelta, err = a.deps.DeltaEngine.Compare(
			result.BaselineTrain,
			train,
			result.Spec.MetricPolicies,
		)
		if err != nil {
			return nil, fmt.Errorf("compare round %d train: %w", round.Round, err)
		}
	}
	profileValid, profileReason := profileOnlyChangesTarget(
		baselineProfile,
		round.OutputProfile,
		result.Spec.TargetSurfaceID,
	)
	gateDecision, err := a.deps.Gate.Decide(&GateInput{
		Spec:                   result.Spec,
		PromptIterAccepted:     candidate.PromptIterAccepted,
		PromptIterReason:       candidate.PromptIterReason,
		CandidateProfileValid:  profileValid,
		CandidateProfileReason: profileReason,
		CandidateValidation:    validation,
		TrainDelta:             candidate.TrainDelta,
		ValidationDelta:        validationDelta,
		TotalUsage:             result.Usage,
	})
	if err != nil {
		return nil, fmt.Errorf("gate round %d: %w", round.Round, err)
	}
	if gateDecision == nil {
		return nil, fmt.Errorf("gate round %d returned nil decision", round.Round)
	}
	if err := validateGateDecision(gateDecision); err != nil {
		return nil, fmt.Errorf("gate round %d returned invalid decision: %w", round.Round, err)
	}
	candidate.Gate = sanitizeGateDecision(gateDecision, result.Spec.Audit)
	return candidate, nil
}

func profileChanged(input, output *promptiter.Profile) (bool, error) {
	matches, err := sameProfile(input, output)
	if err != nil {
		return false, err
	}
	return !matches, nil
}

func validateGateDecision(decision *GateDecision) error {
	if decision == nil {
		return errors.New("decision is nil")
	}
	switch decision.Decision {
	case DecisionAccepted, DecisionRejected, DecisionInconclusive:
	default:
		return fmt.Errorf("unknown decision %q", decision.Decision)
	}
	if len(decision.Rules) == 0 {
		return errors.New("decision has no rule evidence")
	}
	failedRules := 0
	for _, rule := range decision.Rules {
		if strings.TrimSpace(rule.Rule) == "" {
			return errors.New("decision contains an unnamed rule")
		}
		if rule.Passed {
			continue
		}
		failedRules++
		if strings.TrimSpace(rule.Reason) == "" {
			return fmt.Errorf("failed rule %q has no reason", rule.Rule)
		}
	}
	if decision.Decision == DecisionAccepted && failedRules > 0 {
		return errors.New("accepted decision contains failed rules")
	}
	if decision.Decision != DecisionAccepted {
		if failedRules == 0 {
			return errors.New("non-accepted decision has no failed rule")
		}
		if len(decision.Reasons) == 0 {
			return errors.New("non-accepted decision has no reasons")
		}
	}
	return nil
}

func selectCandidate(result *RunResult) {
	result.Decision = DecisionRejected
	bestGain := math.Inf(-1)
	bestRound := 0
	bestID := ""
	for index := range result.Candidates {
		candidate := &result.Candidates[index]
		if candidate.Gate == nil {
			continue
		}
		if candidate.Gate.Decision == DecisionInconclusive && result.Decision == DecisionRejected {
			result.Decision = DecisionInconclusive
		}
		if candidate.Gate.Decision != DecisionAccepted || candidate.ValidationDelta == nil {
			continue
		}
		gain := candidate.ValidationDelta.WeightedScoreDelta
		if gain > bestGain || (gain == bestGain && candidatePrecedes(
			candidate.Candidate, bestRound, bestID,
		)) {
			bestGain = gain
			bestRound = candidate.Candidate.Round
			bestID = candidate.Candidate.ID
			result.SelectedCandidateID = candidate.Candidate.ID
			result.Decision = DecisionAccepted
		}
	}
}

func candidatePrecedes(candidate Candidate, selectedRound int, selectedID string) bool {
	if selectedID == "" {
		return true
	}
	if candidate.Round != selectedRound {
		return candidate.Round < selectedRound
	}
	return candidate.ID < selectedID
}

func hasExecutionError(result *CaseResult) bool {
	for _, run := range result.Runs {
		if observationHasExecutionError(run) {
			return true
		}
	}
	return false
}

func observationHasExecutionError(run Observation) bool {
	if run.Error != "" {
		return true
	}
	for _, tool := range run.Tools {
		if tool.Error != "" {
			return true
		}
	}
	for _, step := range run.Trace {
		if step.Error != "" {
			return true
		}
	}
	return false
}

func cloneRunSpec(source *RunSpec) *RunSpec {
	if source == nil {
		return nil
	}
	result := *source
	result.CriticalCaseIDs = append([]string(nil), source.CriticalCaseIDs...)
	if len(source.MetricPolicies) > 0 {
		result.MetricPolicies = make(map[string]MetricPolicy, len(source.MetricPolicies))
		for name, policy := range source.MetricPolicies {
			result.MetricPolicies[name] = policy
		}
	}
	result.Metadata = sanitizeMetadata(source.Metadata, source.Audit)
	return &result
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
