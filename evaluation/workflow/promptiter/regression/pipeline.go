//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package regression

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

// CandidatePassThroughGain is the finite normalized lower bound used to let
// PromptIter expose every candidate while the independent release Gate audits it.
const CandidatePassThroughGain = -1.0

// Engine is the PromptIter execution surface consumed by Pipeline.
type Engine interface {
	Run(context.Context, *engine.RunRequest, ...engine.Option) (*engine.RunResult, error)
}

// ProfileEvaluator supplies candidate-train measurements missing from RoundResult.
type ProfileEvaluator interface {
	EvaluateProfile(context.Context, string, *promptiter.Profile) (*engine.EvaluationResult, error)
}

// ResourceMeter returns profile-scoped resource, latency, and cost measurements.
// Implementations may return zero values when a measurement is unavailable.
type ResourceMeter interface {
	Measure(evalSetID string, profile *promptiter.Profile) ResourceMeasurement
	TotalModelCalls() int
}

// ResourceMeasurement captures one profile and eval-set execution budget sample.
type ResourceMeasurement struct {
	Usage          Usage
	LatencySeconds float64
	Cost           float64
}

// Config controls the reusable regression workflow.
type Config struct {
	Seed                    int64
	TrainEvalSetID          string
	ValidationEvalSetID     string
	TargetSurfaceIDs        []string
	MaxRounds               int
	MaxRoundsWithoutRelease int
	PromptIterMinScoreGain  float64
	ReleaseGate             GatePolicy
	ModelConfig             ModelConfig
	EstimatedCost           EstimatedCost
	SaveArtifacts           bool
	BaselineProfileRef      string
	PerformedWriteBack      bool
	ExpectedAgentNames      map[string]string
}

// Options contains pipeline dependencies and runtime controls.
type Options struct {
	Config         Config
	Engine         Engine
	Evaluator      ProfileEvaluator
	Meter          ResourceMeter
	InitialProfile *promptiter.Profile
	Artifacts      ArtifactWriter
	Now            func() time.Time
}

// Run executes baseline evaluation, PromptIter search, candidate regression,
// release gating, and complete audit persistence.
func Run(ctx context.Context, options Options) (*Report, error) {
	if err := validateOptions(options); err != nil {
		return nil, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	initialProfile := options.InitialProfile
	searchProfile := initialProfile
	releasedProfile := initialProfile
	baselineRef := options.Config.BaselineProfileRef
	if baselineRef == "" {
		baselineRef = "baseline/input_profile.json"
	}
	releasedRef := baselineRef
	var initialTrain, initialValidation *engine.EvaluationResult
	var releasedTrain, releasedValidation *engine.EvaluationResult
	var initialTrainMeasurement, initialValidationMeasurement ResourceMeasurement
	var releasedTrainMeasurement, releasedValidationMeasurement ResourceMeasurement
	measurementsInitialized := false
	noRelease := 0
	rounds := make([]RoundReport, 0, options.Config.MaxRounds)
	totalUsage := Usage{}
	for roundNumber := 1; roundNumber <= options.Config.MaxRounds; roundNumber++ {
		runResult, err := options.Engine.Run(ctx, &engine.RunRequest{
			Train:            []engine.EvalSetInput{{EvalSetID: options.Config.TrainEvalSetID}},
			Validation:       []engine.EvalSetInput{{EvalSetID: options.Config.ValidationEvalSetID}},
			InitialProfile:   searchProfile,
			AcceptancePolicy: engine.AcceptancePolicy{MinScoreGain: options.Config.PromptIterMinScoreGain},
			MaxRounds:        1,
			TargetSurfaceIDs: options.Config.TargetSurfaceIDs,
		})
		if err != nil {
			return nil, fmt.Errorf("run PromptIter round %d: %w", roundNumber, err)
		}
		if len(runResult.Rounds) != 1 {
			return nil, fmt.Errorf("PromptIter round %d returned %d rounds", roundNumber, len(runResult.Rounds))
		}
		round := runResult.Rounds[0]
		if round.Acceptance == nil {
			return nil, fmt.Errorf("PromptIter round %d has no acceptance decision", roundNumber)
		}
		if initialValidation == nil {
			initialValidation = runResult.BaselineValidation
			initialTrain = round.Train
			releasedTrain = initialTrain
			releasedValidation = initialValidation
			if err := persistBaseline(options, initialTrain, initialValidation); err != nil {
				return nil, err
			}
		}
		candidateTrain, err := options.Evaluator.EvaluateProfile(ctx, options.Config.TrainEvalSetID, round.OutputProfile)
		if err != nil {
			return nil, fmt.Errorf("evaluate candidate train round %d: %w", roundNumber, err)
		}
		searchValidationMeasurement := options.Meter.Measure(options.Config.ValidationEvalSetID, searchProfile)
		candidateValidationMeasurement := options.Meter.Measure(options.Config.ValidationEvalSetID, round.OutputProfile)
		searchTrainMeasurement := options.Meter.Measure(options.Config.TrainEvalSetID, searchProfile)
		candidateTrainMeasurement := options.Meter.Measure(options.Config.TrainEvalSetID, round.OutputProfile)
		if !measurementsInitialized {
			initialTrainMeasurement = searchTrainMeasurement
			initialValidationMeasurement = searchValidationMeasurement
			releasedTrainMeasurement = initialTrainMeasurement
			releasedValidationMeasurement = initialValidationMeasurement
			measurementsInitialized = true
		}
		againstInitial := Compare(initialValidation, round.Validation)
		againstInput := Compare(runResult.BaselineValidation, round.Validation)
		againstReleased := Compare(releasedValidation, round.Validation)
		decision := Decide(options.Config.ReleaseGate, GateInput{
			InputTrainScore: releasedTrain.OverallScore, CandidateTrainScore: candidateTrain.OverallScore,
			InputValidationScore: releasedValidation.OverallScore, CandidateValidationScore: round.Validation.OverallScore,
			ValidationDelta:    againstReleased,
			ExpectedTrainCases: countCases(releasedTrain), ActualTrainCases: countCases(candidateTrain),
			ExpectedValidationCases: countCases(releasedValidation), ActualValidationCases: countCases(round.Validation),
			TrainEvaluationComplete: evaluationComplete(releasedTrain, candidateTrain), ValidationEvaluationComplete: evaluationComplete(releasedValidation, round.Validation),
			InputUsage: releasedValidationMeasurement.Usage, CandidateUsage: candidateValidationMeasurement.Usage,
			InputLatencySeconds: releasedValidationMeasurement.LatencySeconds, CandidateLatencySeconds: candidateValidationMeasurement.LatencySeconds,
			InputCost: releasedValidationMeasurement.Cost, CandidateCost: candidateValidationMeasurement.Cost,
		})
		references := roundArtifactReferences(roundNumber)
		roundReport := RoundReport{
			Round: roundNumber, PromptIterAccepted: round.Acceptance.Accepted,
			PromptIterReasons: []string{round.Acceptance.Reason},
			Train:             summarizeEvaluationWithResources(candidateTrain, candidateTrainMeasurement, options.Config.EstimatedCost, options.Config.ExpectedAgentNames),
			Validation:        summarizeEvaluationWithResources(round.Validation, candidateValidationMeasurement, options.Config.EstimatedCost, options.Config.ExpectedAgentNames),
			Delta:             DeltaBundle{AgainstInitial: againstInitial, AgainstRoundInput: againstInput, AgainstLastReleased: againstReleased},
			Resources: EvaluationResourceComparison{
				Train:      resourceComparison(releasedTrainMeasurement, candidateTrainMeasurement, options.Config.EstimatedCost),
				Validation: resourceComparison(releasedValidationMeasurement, candidateValidationMeasurement, options.Config.EstimatedCost),
			},
			ReleaseGate:    decision,
			EstimatedCost:  costSnapshot(candidateValidationMeasurement.Cost, options.Config.EstimatedCost),
			LatencySeconds: candidateValidationMeasurement.LatencySeconds, Artifacts: references,
		}
		if err := persistRound(options, roundNumber, searchProfile, round.OutputProfile, candidateTrain, round.Validation, roundReport.Delta, decision); err != nil {
			return nil, err
		}
		rounds = append(rounds, roundReport)
		roundUsage := Usage{EvaluationCaseRuns: countCases(runResult.BaselineValidation) + countCases(round.Train) + countCases(round.Validation) + countCases(candidateTrain)}
		for _, measurement := range []ResourceMeasurement{searchValidationMeasurement, candidateValidationMeasurement, searchTrainMeasurement, candidateTrainMeasurement} {
			roundUsage.ModelCalls += measurement.Usage.ModelCalls
			roundUsage.ToolCalls += measurement.Usage.ToolCalls
			roundUsage.InputTokens += measurement.Usage.InputTokens
			roundUsage.OutputTokens += measurement.Usage.OutputTokens
			roundUsage.Retries += measurement.Usage.Retries
			roundUsage.TokenUsageAvailable = roundUsage.TokenUsageAvailable || measurement.Usage.TokenUsageAvailable
		}
		rounds[len(rounds)-1].Usage = roundUsage
		addUsage(&totalUsage, roundUsage)
		if decision.Accepted {
			releasedProfile = round.OutputProfile
			releasedTrain = candidateTrain
			releasedValidation = round.Validation
			releasedTrainMeasurement = candidateTrainMeasurement
			releasedValidationMeasurement = candidateValidationMeasurement
			releasedRef = references.CandidateProfile
			noRelease = 0
		} else {
			noRelease++
		}
		if round.Acceptance.Accepted {
			searchProfile = round.OutputProfile
		}
		if noRelease >= options.Config.MaxRoundsWithoutRelease {
			break
		}
	}
	finished := now()
	totalUsage.ModelCalls = options.Meter.TotalModelCalls()
	baseline := BaselineSnapshot{
		Train:      summarizeEvaluationWithResources(initialTrain, initialTrainMeasurement, options.Config.EstimatedCost, options.Config.ExpectedAgentNames),
		Validation: summarizeEvaluationWithResources(initialValidation, initialValidationMeasurement, options.Config.EstimatedCost, options.Config.ExpectedAgentNames),
		Artifacts:  ArtifactReferences{InputProfile: "baseline/input_profile.json", TrainEvaluation: "baseline/train_evaluation.json", ValidationEvaluation: "baseline/validation_evaluation.json"},
	}
	report := &Report{
		Version: 1, Seed: options.Config.Seed, ModelConfig: options.Config.ModelConfig,
		TargetSurfaceIDs: append([]string(nil), options.Config.TargetSurfaceIDs...),
		Timing:           Timing{StartedAt: started, FinishedAt: finished, DurationSeconds: finished.Sub(started).Seconds()},
		Usage:            totalUsage, EstimatedCost: costSnapshot(totalMeasuredCost(options.Meter), options.Config.EstimatedCost),
		Baseline:  baseline,
		Rounds:    rounds,
		WriteBack: WriteBackDecision{RecommendedForWriteBack: releasedProfile != initialProfile && releasedRef != baselineRef, Performed: options.Config.PerformedWriteBack, AcceptedProfileRef: filepath.ToSlash(releasedRef)},
	}
	report.FailureAttributionStats = buildFailureAttributionStats(report.Baseline, report.Rounds)
	if err := persistReport(options.Artifacts, report); err != nil {
		return nil, err
	}
	return report, nil
}

func summarizeEvaluationWithResources(result *engine.EvaluationResult, measurement ResourceMeasurement, cost EstimatedCost, expectedAgentNames map[string]string) EvaluationSnapshot {
	summary := SummarizeEvaluation(result, AttributeFailures(result, AttributionOptions{ExpectedAgentNames: expectedAgentNames}))
	summary.Resources = ResourceSnapshot{
		Usage: measurement.Usage, LatencySeconds: measurement.LatencySeconds,
		EstimatedCost: costSnapshot(measurement.Cost, cost),
	}
	return summary
}

func costSnapshot(amount float64, template EstimatedCost) EstimatedCost {
	return EstimatedCost{Currency: template.Currency, Amount: amount, Source: template.Source}
}

func resourceDelta(baseline, candidate ResourceMeasurement) ResourceDelta {
	return ResourceDelta{
		EvaluationCaseRuns:  candidate.Usage.EvaluationCaseRuns - baseline.Usage.EvaluationCaseRuns,
		ModelCalls:          candidate.Usage.ModelCalls - baseline.Usage.ModelCalls,
		ToolCalls:           candidate.Usage.ToolCalls - baseline.Usage.ToolCalls,
		LatencySeconds:      candidate.LatencySeconds - baseline.LatencySeconds,
		EstimatedCostAmount: candidate.Cost - baseline.Cost,
	}
}

func resourceComparison(baseline, candidate ResourceMeasurement, cost EstimatedCost) ResourceComparison {
	return ResourceComparison{
		LastReleased: resourceSnapshot(baseline, cost),
		Candidate:    resourceSnapshot(candidate, cost),
		Delta:        resourceDelta(baseline, candidate),
	}
}

func resourceSnapshot(measurement ResourceMeasurement, cost EstimatedCost) ResourceSnapshot {
	return ResourceSnapshot{Usage: measurement.Usage, LatencySeconds: measurement.LatencySeconds, EstimatedCost: costSnapshot(measurement.Cost, cost)}
}

func totalMeasuredCost(meter ResourceMeter) float64 {
	if provider, ok := meter.(interface{ TotalCost() float64 }); ok {
		return provider.TotalCost()
	}
	return 0
}

func validateOptions(options Options) error {
	switch {
	case options.Engine == nil, options.Evaluator == nil, options.Meter == nil, options.InitialProfile == nil, options.Artifacts == nil:
		return errors.New("engine, evaluator, meter, initial profile, and artifact writer are required")
	case options.Config.TrainEvalSetID == "", options.Config.ValidationEvalSetID == "":
		return errors.New("train and validation eval set ids are required")
	case options.Config.MaxRounds <= 0:
		return errors.New("max rounds must be positive")
	case options.Config.MaxRoundsWithoutRelease <= 0:
		return errors.New("max rounds without release must be positive")
	case len(options.Config.TargetSurfaceIDs) == 0:
		return errors.New("target surface ids are required")
	}
	return nil
}

func persistBaseline(options Options, train, validation *engine.EvaluationResult) error {
	if !options.Config.SaveArtifacts {
		return nil
	}
	if err := writeJSON(options.Artifacts, "baseline/input_profile.json", options.InitialProfile); err != nil {
		return err
	}
	if err := writeJSON(options.Artifacts, "baseline/train_evaluation.json", train); err != nil {
		return err
	}
	return writeJSON(options.Artifacts, "baseline/validation_evaluation.json", validation)
}

func persistRound(options Options, round int, input, candidate *promptiter.Profile, train, validation *engine.EvaluationResult, delta DeltaBundle, decision GateDecision) error {
	if !options.Config.SaveArtifacts {
		return nil
	}
	references := roundArtifactReferences(round)
	values := []struct {
		path  string
		value any
	}{{references.InputProfile, input}, {references.CandidateProfile, candidate}, {references.TrainEvaluation, train}, {references.ValidationEvaluation, validation}, {references.Delta, delta}, {references.Gate, decision}}
	for _, value := range values {
		if err := writeJSON(options.Artifacts, value.path, value.value); err != nil {
			return err
		}
	}
	return nil
}

func persistReport(writer ArtifactWriter, report *Report) error {
	payload, err := JSON(report)
	if err != nil {
		return err
	}
	if err := writer.Write("optimization_report.json", payload); err != nil {
		return err
	}
	return writer.Write("optimization_report.md", Markdown(report))
}

func roundArtifactReferences(round int) ArtifactReferences {
	prefix := fmt.Sprintf("round_%d", round)
	return ArtifactReferences{
		InputProfile: prefix + "/input_profile.json", CandidateProfile: prefix + "/candidate_profile.json",
		TrainEvaluation: prefix + "/train_evaluation.json", ValidationEvaluation: prefix + "/validation_evaluation.json",
		Delta: prefix + "/delta.json", Gate: prefix + "/gate.json",
	}
}

func countCases(result *engine.EvaluationResult) int {
	count := 0
	if result != nil {
		for _, set := range result.EvalSets {
			count += len(set.Cases)
		}
	}
	return count
}

func evaluationComplete(expected, actual *engine.EvaluationResult) bool {
	expectedStates := caseStates(expected)
	actualStates := caseStates(actual)
	if len(expectedStates) == 0 || len(expectedStates) != len(actualStates) {
		return false
	}
	for id := range expectedStates {
		state, ok := actualStates[id]
		if !ok || !state.present {
			return false
		}
	}
	for _, set := range actual.EvalSets {
		for _, evalCase := range set.Cases {
			if len(evalCase.Metrics) == 0 {
				return false
			}
			for _, metric := range evalCase.Metrics {
				if metric.Status == status.EvalStatusNotEvaluated {
					return false
				}
			}
		}
	}
	return true
}

func addUsage(total *Usage, value Usage) {
	total.EvaluationCaseRuns += value.EvaluationCaseRuns
	total.ModelCalls += value.ModelCalls
	total.ToolCalls += value.ToolCalls
	total.InputTokens += value.InputTokens
	total.OutputTokens += value.OutputTokens
	total.Retries += value.Retries
	total.TokenUsageAvailable = total.TokenUsageAvailable || value.TokenUsageAvailable
}
