//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

// Mode selects how the pipeline sources models.
type Mode string

const (
	// ModeFake runs the whole pipeline on scripted deterministic components
	// without any API key or network access.
	ModeFake Mode = "fake"
	// ModeReal runs against OpenAI-compatible endpoints configured through
	// OPENAI_API_KEY and OPENAI_BASE_URL.
	ModeReal Mode = "real"
)

// Components carries the mode-specific collaborators injected by the caller.
// The pipeline itself stays agnostic of fake versus real sourcing.
type Components struct {
	// CandidateAgent is the agent under optimization.
	CandidateAgent agent.Agent
	// Backwarder computes gradients from terminal losses.
	Backwarder backwarder.Backwarder
	// Aggregator merges sample gradients per surface.
	Aggregator aggregator.Aggregator
	// Optimizer turns aggregated gradients into surface patches.
	Optimizer optimizer.Optimizer
	// Judge is the optional judge runner used by llmJudge metrics.
	Judge runner.Runner
	// ModelInfo describes the model or fake-engine configuration per role,
	// recorded into the audit trail.
	ModelInfo map[string]string
}

// Options carries the runtime inputs of one pipeline execution.
type Options struct {
	// Config is the validated pipeline configuration.
	Config *Config
	// Inputs are the resolved input files, from resolveInputs.
	Inputs *resolvedInputs
	// DataDir holds the evalset, metric, and prompt source files.
	DataDir string
	// OutputDir receives reports, audit files, and the candidate prompt.
	OutputDir string
	// Mode selects fake or real model sourcing.
	Mode Mode
	// WriteBack overwrites the baseline prompt source on acceptance instead of
	// only emitting output/candidate_prompt.txt.
	WriteBack bool
	// Components carries the mode-specific collaborators. Worker runners
	// (including Judge) must already be wrapped with Tracker by the builder.
	Components Components
	// Tracker accumulates runner costs across the run. Nil creates a fresh one.
	Tracker *CostTracker
	// Logger receives progress output. Defaults to the standard logger.
	Logger *log.Logger
}

// Pipeline lifecycle statuses.
const (
	// StatusAccepted means a candidate passed the safety gate.
	StatusAccepted = "accepted"
	// StatusRejected means no candidate passed the safety gate; the baseline
	// prompt stays in force. This is a normal business outcome.
	StatusRejected = "rejected"
)

// Names of the deployable candidate artifacts under the output dir and the
// write-back profile persisted next to the prompt source.
const (
	candidatePromptFileName  = "candidate_prompt.txt"
	candidateProfileFileName = "candidate_profile.json"
	baselineProfileFileName  = "baseline_profile.json"
)

// Result is the terminal outcome of one pipeline execution. A gate rejection
// is a normal business result, not an execution error.
type Result struct {
	// Status is the pipeline lifecycle outcome.
	Status string `json:"status"`
	// Message is a human-readable one-line summary.
	Message string `json:"message"`
	// RunID identifies this execution in the audit trail.
	RunID string `json:"runId"`
	// BaselineTrain stores per-case baseline results on the train set.
	BaselineTrain []CaseSnapshot `json:"baselineTrain"`
	// BaselineValidation stores per-case baseline results on validation.
	BaselineValidation []CaseSnapshot `json:"baselineValidation"`
	// BaselineTrainScore and BaselineValidationScore are the aggregates.
	BaselineTrainScore      float64 `json:"baselineTrainScore"`
	BaselineValidationScore float64 `json:"baselineValidationScore"`
	// BaselineAttributions explains every baseline failure (train and
	// validation) as a causal chain.
	BaselineAttributions []CaseAttribution `json:"baselineAttributions"`
	// Candidates are the gate-evaluated round outputs, in round order.
	Candidates []Candidate `json:"candidates"`
	// Gate is the final two-stage gate decision.
	Gate *GateDecision `json:"gate"`
	// CandidatePrompt is the accepted candidate's instruction text; empty on
	// rejection.
	CandidatePrompt string `json:"candidatePrompt,omitempty"`
	// CandidatePromptPath is where the accepted prompt was written.
	CandidatePromptPath string `json:"candidatePromptPath,omitempty"`
	// ReportJSONPath and ReportMarkdownPath locate the generated reports.
	ReportJSONPath     string `json:"reportJsonPath"`
	ReportMarkdownPath string `json:"reportMarkdownPath"`
	// Run is the raw engine run result.
	Run *promptiterengine.RunResult `json:"-"`
	// Cost summarizes runner costs across all stages.
	Cost CostSummary `json:"cost"`
	// StageDurations records wall clock time per pipeline stage.
	StageDurations map[string]time.Duration `json:"stageDurations"`
	// StartedAt and FinishedAt bound the execution.
	StartedAt  time.Time `json:"startedAt"`
	FinishedAt time.Time `json:"finishedAt"`
}

// runPipeline executes the regression loop pipeline: input resolution, baseline train
// evaluation (S1), failure attribution hooks (S2), the PromptIter engine run
// with audit observation (S3), and cost accounting. Delta, gating, and report
// generation build on the returned data in later stages.
func runPipeline(ctx context.Context, opts Options) (*Result, error) {
	if err := validateOptions(&opts); err != nil {
		return nil, err
	}
	logger := opts.Logger
	inputs := opts.Inputs
	if err := prepareOutputDir(opts.OutputDir); err != nil {
		return nil, err
	}

	tracker := opts.Tracker
	runID := newRunID()
	audit, err := newAuditWriter(opts.OutputDir, runID, tracker)
	if err != nil {
		return nil, err
	}
	result := &Result{
		RunID:          runID,
		StartedAt:      time.Now(),
		StageDurations: make(map[string]time.Duration),
	}
	if err := audit.WriteRunMeta(RunMeta{
		RunID:            result.RunID,
		StartedAt:        result.StartedAt,
		Mode:             string(opts.Mode),
		Seed:             opts.Config.Seed,
		AppName:          opts.Config.AppName,
		TargetSurfaceIDs: inputs.targetSurfaceIDs,
		Models:           opts.Components.ModelInfo,
		Config:           opts.Config,
	}); err != nil {
		return nil, err
	}

	// Shared evaluation stack: local evalset/metric managers over the data
	// dir, eval results persisted under the output dir, candidate runner
	// wrapped for cost accounting.
	candidateRunner := tracker.Wrap(
		"candidate",
		runner.NewRunner(opts.Config.AppName, opts.Components.CandidateAgent),
	)
	defer candidateRunner.Close()
	// The judge runner arrives pre-wrapped by the component builder.
	judgeRunner := opts.Components.Judge
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(opts.DataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(opts.DataDir),
		metric.WithLocator(&SharedMetricLocator{}),
	)
	evaluatorOptions := []evaluation.Option{
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalresultlocal.New(evalresult.WithBaseDir(opts.OutputDir))),
		evaluation.WithNumRuns(1),
	}
	if judgeRunner != nil {
		evaluatorOptions = append(evaluatorOptions, evaluation.WithJudgeRunner(judgeRunner))
	}
	agentEvaluator, err := evaluation.New(opts.Config.AppName, candidateRunner, evaluatorOptions...)
	if err != nil {
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	defer agentEvaluator.Close()

	// S1: baseline train evaluation. The candidate agent is constructed with
	// the baseline prompt, so no profile compilation is needed here. Run
	// details are enabled to capture actual tool calls for attribution.
	stageStart := time.Now()
	logger.Printf("S1: baseline train evaluation (%s)", opts.Config.EvalSets.Train)
	baselineTrainResult, err := agentEvaluator.Evaluate(
		ctx,
		opts.Config.EvalSets.Train,
		evaluation.WithRunDetailsEnabled(true),
	)
	if err != nil {
		return nil, fmt.Errorf("baseline train evaluation: %w", err)
	}
	result.BaselineTrain = SnapshotsFromEvaluationResult(baselineTrainResult)
	result.BaselineTrainScore = aggregateScore(result.BaselineTrain)
	result.StageDurations["s1_baseline_train"] = time.Since(stageStart)
	if err := audit.WriteFile("baseline_train.json", result.BaselineTrain); err != nil {
		return nil, err
	}

	// S2: failure attribution over the baseline train failures, reinjected
	// into the engine as root-cause loss hints so the optimizer works on
	// causes, not symptoms.
	stageStart = time.Now()
	attributor, expectedInvocations, err := buildAttributor(ctx, opts, evalSetManager, metricManager)
	if err != nil {
		return nil, err
	}
	trainAttributions := attributeFailures(attributor, result.BaselineTrain, expectedInvocations)
	result.BaselineAttributions = append(result.BaselineAttributions, trainAttributions...)
	lossHints := buildLossHints(trainAttributions)
	result.StageDurations["s2_attribution"] = time.Since(stageStart)
	if err := audit.WriteFile("baseline_train_attribution.json", trainAttributions); err != nil {
		return nil, err
	}

	// S3: PromptIter optimization run with every event audited.
	stageStart = time.Now()
	logger.Printf("S3: promptiter optimization (max %d rounds)", opts.Config.Engine.MaxRounds)
	engineInstance, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgent(opts.Components.CandidateAgent),
		promptiterengine.WithAgentEvaluator(agentEvaluator),
		promptiterengine.WithBackwarder(opts.Components.Backwarder),
		promptiterengine.WithAggregator(opts.Components.Aggregator),
		promptiterengine.WithOptimizer(opts.Components.Optimizer),
	)
	if err != nil {
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}
	runRequest, err := buildRunRequest(opts.Config, inputs, lossHints)
	if err != nil {
		return nil, err
	}
	runResult, err := engineInstance.Run(ctx, runRequest, promptiterengine.WithObserver(audit.Observer()))
	if err != nil {
		return nil, fmt.Errorf("run promptiter engine: %w", err)
	}
	result.Run = runResult
	result.BaselineValidation = SnapshotsFromEngineResult(runResult.BaselineValidation)
	result.BaselineValidationScore = runResult.BaselineValidation.OverallScore
	result.StageDurations["s3_optimization"] = time.Since(stageStart)

	// Baseline validation failures also get attributed for the report.
	validationAttributions := attributeFailures(attributor, result.BaselineValidation, expectedInvocations)
	result.BaselineAttributions = append(result.BaselineAttributions, validationAttributions...)
	if err := audit.WriteFile("baseline_validation_attribution.json", validationAttributions); err != nil {
		return nil, err
	}

	// S4: per-round candidates with per-case validation deltas versus
	// baseline; candidate-side failures re-attributed to expose "did the
	// failure move from one category to another".
	stageStart = time.Now()
	logger.Printf("S4: per-case delta against baseline")
	candidates, err := buildCandidates(result, runResult, attributor, expectedInvocations, audit, opts.Config.Gate.Epsilon())
	if err != nil {
		return nil, err
	}
	result.Candidates = candidates
	result.StageDurations["s4_delta"] = time.Since(stageStart)
	if err := audit.WriteFile("candidates.json", candidates); err != nil {
		return nil, err
	}

	// S5: safety gate — hard rules per candidate; the best-scoring
	// survivor is selected.
	stageStart = time.Now()
	logger.Printf("S5: acceptance gate (%d candidate(s))", len(candidates))
	totals := tracker.Snapshot()
	decision, err := EvaluateGate(GateInput{
		Gate:                    opts.Config.Gate,
		BaselineValidationScore: result.BaselineValidationScore,
		BaselineTrainScore:      result.BaselineTrainScore,
		Candidates:              candidates,
		TotalModelCalls:         totals.Total.ModelCalls,
		TotalWallClock:          time.Since(result.StartedAt),
	})
	if err != nil {
		return nil, err
	}
	result.Gate = decision
	result.StageDurations["s5_gate"] = time.Since(stageStart)
	if err := audit.WriteFile("gate_decision.json", decision); err != nil {
		return nil, err
	}

	// Persist the accepted candidate prompt; the baseline source file is only
	// overwritten when write-back is explicitly requested.
	if decision.Accepted {
		if err := writeCandidatePrompt(opts, inputs, result, decision); err != nil {
			return nil, err
		}
	}

	result.Cost = tracker.Snapshot()
	result.FinishedAt = time.Now()
	if decision.Accepted {
		result.Status = StatusAccepted
	} else {
		result.Status = StatusRejected
	}
	result.Message = decision.Summary

	// S6: machine- and human-readable reports plus the run summary.
	stageStart = time.Now()
	logger.Printf("S6: reports")
	jsonPath, markdownPath, err := WriteReports(opts, result)
	if err != nil {
		return nil, err
	}
	result.ReportJSONPath = jsonPath
	result.ReportMarkdownPath = markdownPath
	result.StageDurations["s6_report"] = time.Since(stageStart)
	logger.Printf("gate decision: %s", decision.Summary)
	return result, nil
}

// prepareOutputDir creates the output directory and removes the deployable
// candidate artifacts a previous run may have left behind. The paths are
// stable across runs, so without this cleanup a rejecting rerun would leave
// the previously accepted candidate_prompt.txt / candidate_profile.json next
// to its rejection report, and consumers of the stable paths could deploy a
// stale candidate despite the latest gate decision.
func prepareOutputDir(outputDir string) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %q: %w", outputDir, err)
	}
	for _, name := range []string{candidatePromptFileName, candidateProfileFileName} {
		if err := os.Remove(filepath.Join(outputDir, name)); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale candidate artifact %q: %w", name, err)
		}
	}
	return nil
}

// buildAttributor loads metric definitions and expected invocations, then
// constructs the attribution rule engine.
func buildAttributor(
	ctx context.Context,
	opts Options,
	evalSetManager evalset.Manager,
	metricManager metric.Manager,
) (*Attributor, map[string][]*evalset.Invocation, error) {
	metricNames, err := metricManager.List(ctx, opts.Config.AppName, opts.Config.EvalSets.Train)
	if err != nil {
		return nil, nil, fmt.Errorf("list metrics: %w", err)
	}
	metrics := make([]*metric.EvalMetric, 0, len(metricNames))
	for _, metricName := range metricNames {
		evalMetric, err := metricManager.Get(ctx, opts.Config.AppName, opts.Config.EvalSets.Train, metricName)
		if err != nil {
			return nil, nil, fmt.Errorf("get metric %q: %w", metricName, err)
		}
		metrics = append(metrics, evalMetric)
	}
	attributor := NewAttributor(metrics, opts.Config.Attribution.MetricCategoryHints)
	expected := make(map[string][]*evalset.Invocation)
	for _, evalSetID := range []string{opts.Config.EvalSets.Train, opts.Config.EvalSets.Validation} {
		set, err := evalSetManager.Get(ctx, opts.Config.AppName, evalSetID)
		if err != nil {
			return nil, nil, fmt.Errorf("get eval set %q: %w", evalSetID, err)
		}
		for _, evalCase := range set.EvalCases {
			if evalCase == nil {
				continue
			}
			expected[evalSetID+"/"+evalCase.EvalID] = evalCase.Conversation
		}
	}
	return attributor, expected, nil
}

// attributeFailures runs attribution over every failed snapshot.
func attributeFailures(
	attributor *Attributor,
	snapshots []CaseSnapshot,
	expected map[string][]*evalset.Invocation,
) []CaseAttribution {
	attributions := make([]CaseAttribution, 0)
	for _, snapshot := range snapshots {
		attribution := attributor.Attribute(snapshot, expected[snapshotKey(snapshot)])
		if attribution != nil {
			attributions = append(attributions, *attribution)
		}
	}
	return attributions
}

// buildCandidates converts engine rounds into gate-evaluable candidates.
func buildCandidates(
	result *Result,
	runResult *promptiterengine.RunResult,
	attributor *Attributor,
	expected map[string][]*evalset.Invocation,
	audit *auditWriter,
	epsilon float64,
) ([]Candidate, error) {
	candidates := make([]Candidate, 0, len(runResult.Rounds))
	for index, round := range runResult.Rounds {
		if round.Validation == nil || round.OutputProfile == nil {
			continue
		}
		candidateSnapshots := SnapshotsFromEngineResult(round.Validation)
		deltas, err := ComputeDeltas(result.BaselineValidation, candidateSnapshots, epsilon)
		if err != nil {
			return nil, fmt.Errorf("compute deltas for round %d: %w", round.Round, err)
		}
		snapshotByKey := make(map[string]CaseSnapshot, len(candidateSnapshots))
		for _, snapshot := range candidateSnapshots {
			snapshotByKey[snapshotKey(snapshot)] = snapshot
		}
		for i := range deltas {
			if deltas[i].CandidatePass {
				continue
			}
			key := deltas[i].EvalSetID + "/" + deltas[i].EvalCaseID
			if snapshot, ok := snapshotByKey[key]; ok {
				deltas[i].CandidateAttribution = attributor.Attribute(snapshot, expected[key])
			}
		}
		candidate := Candidate{
			Round:           round.Round,
			ValidationScore: round.Validation.OverallScore,
			ModelCalls:      audit.RoundCost(round.Round).Total.ModelCalls,
			WallClock:       audit.RoundDuration(round.Round),
			Deltas:          deltas,
			Profile:         round.OutputProfile,
		}
		// A candidate's train score is only measured when the engine accepted
		// it and re-evaluated the train set with it in the following round.
		if round.Acceptance != nil && round.Acceptance.Accepted && index+1 < len(runResult.Rounds) {
			nextTrain := runResult.Rounds[index+1].Train
			if nextTrain != nil {
				candidate.TrainScore = nextTrain.OverallScore
				candidate.TrainScoreKnown = true
				trainDeltas, err := ComputeDeltas(
					result.BaselineTrain,
					SnapshotsFromEngineResult(nextTrain),
					epsilon,
				)
				if err != nil {
					return nil, fmt.Errorf("compute train deltas for round %d: %w", round.Round, err)
				}
				candidate.TrainDeltas = trainDeltas
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

// writeCandidatePrompt persists the accepted candidate. The full profile
// (every accepted surface override, e.g. tool descriptions) always lands in
// candidate_profile.json; the instruction text additionally lands in
// candidate_prompt.txt. The engine normalizes away no-op overrides, so an
// accepted profile whose patches only touched non-instruction surfaces
// legitimately carries no instruction text: that keeps the baseline prompt in
// force. Write-back persists the complete effective baseline: the instruction
// text over the prompt source, and the accepted overrides merged onto the
// previously restored baseline profile into baseline_profile.json beside it.
// Merging keeps consecutive write-backs lossless — once a restored override is
// baked into the agent, a later accepted profile no longer carries it, so
// writing the accepted profile alone would silently drop it.
func writeCandidatePrompt(opts Options, inputs *resolvedInputs, result *Result, decision *GateDecision) error {
	var profile *promptiter.Profile
	for _, candidate := range result.Candidates {
		if candidate.Round == decision.SelectedRound {
			profile = candidate.Profile
			break
		}
	}
	if profile == nil {
		return fmt.Errorf("selected round %d has no profile", decision.SelectedRound)
	}
	profilePath := filepath.Join(opts.OutputDir, candidateProfileFileName)
	profileContent, err := json.MarshalIndent(profile, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal candidate profile: %w", err)
	}
	if err := os.WriteFile(profilePath, append(profileContent, '\n'), 0o644); err != nil {
		return fmt.Errorf("write candidate profile: %w", err)
	}
	instructionSurfaceID, err := instructionTargetSurfaceID(opts.Config)
	if err != nil {
		return err
	}
	promptText := ""
	for _, override := range profile.Overrides {
		if override.SurfaceID == instructionSurfaceID && override.Value.Text != nil {
			promptText = *override.Value.Text
			break
		}
	}
	if promptText != "" {
		result.CandidatePrompt = promptText
		result.CandidatePromptPath = filepath.Join(opts.OutputDir, candidatePromptFileName)
		if err := os.WriteFile(result.CandidatePromptPath, []byte(promptText+"\n"), 0o644); err != nil {
			return fmt.Errorf("write candidate prompt: %w", err)
		}
	}
	if opts.WriteBack {
		if promptText != "" {
			if err := os.WriteFile(inputs.promptSourcePath, []byte(promptText+"\n"), 0o644); err != nil {
				return fmt.Errorf("write back baseline prompt: %w", err)
			}
		}
		merged := mergedBaselineProfile(inputs, profile, instructionSurfaceID, promptText)
		mergedContent, err := json.MarshalIndent(merged, "", "  ")
		if err != nil {
			return fmt.Errorf("marshal merged baseline profile: %w", err)
		}
		if err := os.WriteFile(inputs.baselineProfilePath, append(mergedContent, '\n'), 0o644); err != nil {
			return fmt.Errorf("write back baseline profile: %w", err)
		}
	}
	return nil
}

// mergedBaselineProfile overlays the accepted overrides onto the previously
// restored baseline profile, producing the complete effective baseline for
// the next run. Without the merge an instruction-only acceptance would
// overwrite baseline_profile.json without the inherited tool overrides (they
// are baked into the agent, so the engine no longer emits them as patches).
// The instruction override is refreshed from the effective instruction text
// so the stored profile always stays consistent with the prompt source.
func mergedBaselineProfile(
	inputs *resolvedInputs,
	accepted *promptiter.Profile,
	instructionSurfaceID string,
	promptText string,
) *promptiter.Profile {
	merged := &promptiter.Profile{StructureID: accepted.StructureID}
	if inputs.baselineProfile != nil {
		merged.Overrides = append(merged.Overrides, inputs.baselineProfile.Overrides...)
	}
	for _, override := range accepted.Overrides {
		merged.Overrides = upsertOverride(merged.Overrides, override)
	}
	instructionText := promptText
	if instructionText == "" {
		instructionText = inputs.baselinePrompt
	}
	merged.Overrides = upsertOverride(merged.Overrides, promptiter.SurfaceOverride{
		SurfaceID: instructionSurfaceID,
		Value:     astructureTextValue(instructionText),
	})
	return merged
}

// upsertOverride replaces the override with the same surface ID or appends it.
func upsertOverride(
	overrides []promptiter.SurfaceOverride,
	override promptiter.SurfaceOverride,
) []promptiter.SurfaceOverride {
	for i := range overrides {
		if overrides[i].SurfaceID == override.SurfaceID {
			overrides[i] = override
			return overrides
		}
	}
	return append(overrides, override)
}

// aggregateScore mirrors the engine's aggregation: the mean over every
// evaluated metric score across all cases.
func aggregateScore(snapshots []CaseSnapshot) float64 {
	total := 0.0
	count := 0
	for _, snapshot := range snapshots {
		for _, metricResult := range snapshot.Metrics {
			if metricResult.Status == status.EvalStatusNotEvaluated {
				continue
			}
			total += metricResult.Score
			count++
		}
	}
	if count == 0 {
		return 0
	}
	return total / float64(count)
}

func buildRunRequest(
	config *Config,
	inputs *resolvedInputs,
	lossHints []promptiterengine.LossHint,
) (*promptiterengine.RunRequest, error) {
	instructionSurfaceID, err := instructionTargetSurfaceID(config)
	if err != nil {
		return nil, err
	}
	request := &promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{EvalSetID: config.EvalSets.Train, LossHints: lossHints},
		},
		Validation: []promptiterengine.EvalSetInput{
			{EvalSetID: config.EvalSets.Validation},
		},
		// The baseline prompt file is the optimization starting point: it is
		// injected as an instruction surface override so the source of truth
		// stays on disk, not in code.
		InitialProfile: &promptiter.Profile{
			Overrides: []promptiter.SurfaceOverride{
				{
					SurfaceID: instructionSurfaceID,
					Value:     astructureTextValue(inputs.baselinePrompt),
				},
			},
		},
		EvaluationOptions: promptiterengine.EvaluationOptions{
			EvalCaseParallelism: config.Engine.EvalCaseParallelism,
		},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: *config.Engine.MinScoreGain,
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: *config.Engine.MaxRoundsWithoutAcceptance,
		},
		MaxRounds:        config.Engine.MaxRounds,
		TargetSurfaceIDs: inputs.targetSurfaceIDs,
	}
	return request, nil
}

// instructionTargetSurfaceID returns the surface that receives the baseline
// prompt override: the first configured instruction-type target surface.
func instructionTargetSurfaceID(config *Config) (string, error) {
	for _, surface := range config.TargetSurfaces {
		if surface.Type == "instruction" || surface.Type == "global_instruction" {
			return surface.ID()
		}
	}
	return "", errors.New("targetSurfaces must include an instruction surface to receive the baseline prompt")
}

// resolvedInputs stores verified input file paths and preloaded content.
type resolvedInputs struct {
	trainEvalSetPath      string
	validationEvalSetPath string
	metricsPath           string
	promptSourcePath      string
	baselinePrompt        string
	targetSurfaceIDs      []string
	// baselineProfilePath is where write-back persists the full accepted
	// profile so the next run starts from the candidate that passed the gate.
	baselineProfilePath string
	// baselineProfile is the previously written-back profile, reloaded so a
	// later write-back can merge onto it instead of dropping inherited
	// overrides. Nil when no previous run wrote back a profile.
	baselineProfile *promptiter.Profile
	// baselineToolDescriptions carries accepted tool-description overrides
	// loaded from the baseline profile, keyed by tool name. Nil when no
	// previous run wrote back a profile.
	baselineToolDescriptions map[string]string
}

func validateOptions(opts *Options) error {
	switch {
	case opts.Config == nil:
		return errors.New("pipeline config is nil")
	case opts.Inputs == nil:
		return errors.New("pipeline inputs are nil, call resolveInputs first")
	case opts.DataDir == "":
		return errors.New("data dir is empty")
	case opts.OutputDir == "":
		return errors.New("output dir is empty")
	case opts.Components.CandidateAgent == nil:
		return errors.New("candidate agent is nil")
	case opts.Components.Backwarder == nil:
		return errors.New("backwarder is nil")
	case opts.Components.Aggregator == nil:
		return errors.New("aggregator is nil")
	case opts.Components.Optimizer == nil:
		return errors.New("optimizer is nil")
	}
	switch opts.Mode {
	case ModeFake, ModeReal:
	default:
		return fmt.Errorf("mode %q is not supported, expected %q or %q", opts.Mode, ModeFake, ModeReal)
	}
	if opts.Logger == nil {
		opts.Logger = log.Default()
	}
	if opts.Tracker == nil {
		opts.Tracker = NewCostTracker()
	}
	return nil
}

// resolveInputs verifies every input file exists and preloads the baseline
// prompt. It is called once by the entry point; the pipeline reuses the result.
func resolveInputs(dataDir string, config *Config) (*resolvedInputs, error) {
	appDir := filepath.Join(dataDir, config.AppName)
	if info, err := os.Stat(appDir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("app data dir %q does not exist", appDir)
	}
	inputs := &resolvedInputs{
		trainEvalSetPath:      filepath.Join(appDir, config.EvalSets.Train+".evalset.json"),
		validationEvalSetPath: filepath.Join(appDir, config.EvalSets.Validation+".evalset.json"),
		metricsPath:           filepath.Join(appDir, "metrics.json"),
		promptSourcePath:      config.PromptSourcePath(),
	}
	for _, path := range []string{
		inputs.trainEvalSetPath,
		inputs.validationEvalSetPath,
		inputs.metricsPath,
	} {
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("required input file %q does not exist", path)
		}
	}
	prompt, err := os.ReadFile(inputs.promptSourcePath)
	if err != nil {
		return nil, fmt.Errorf("read baseline prompt %q: %w", inputs.promptSourcePath, err)
	}
	if strings.TrimSpace(string(prompt)) == "" {
		return nil, fmt.Errorf("baseline prompt %q is empty", inputs.promptSourcePath)
	}
	inputs.baselinePrompt = strings.TrimSpace(string(prompt))
	inputs.baselineProfilePath = filepath.Join(filepath.Dir(inputs.promptSourcePath), baselineProfileFileName)
	inputs.baselineProfile, inputs.baselineToolDescriptions, err = loadBaselineProfile(inputs.baselineProfilePath, config)
	if err != nil {
		return nil, err
	}
	for _, surface := range config.TargetSurfaces {
		surfaceID, err := surface.ID()
		if err != nil {
			return nil, err
		}
		inputs.targetSurfaceIDs = append(inputs.targetSurfaceIDs, surfaceID)
	}
	return inputs, nil
}

// loadBaselineProfile reads the write-back profile persisted by a previously
// accepted run, so a rerun starts from the exact profile that passed the gate
// instead of the in-code constants. The instruction override inside the
// profile is skipped: the prompt source file stays the instruction source of
// truth (write-back updates both from the same accepted profile). It returns
// the full profile (kept for merged write-backs) plus the tool-description
// overrides keyed by tool name; a missing file means no previous write-back
// and yields nils.
func loadBaselineProfile(path string, config *Config) (*promptiter.Profile, map[string]string, error) {
	content, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read baseline profile %q: %w", path, err)
	}
	profile := &promptiter.Profile{}
	if err := json.Unmarshal(content, profile); err != nil {
		return nil, nil, fmt.Errorf("decode baseline profile %q: %w", path, err)
	}
	instructionSurfaceID, err := instructionTargetSurfaceID(config)
	if err != nil {
		return nil, nil, err
	}
	descriptions := make(map[string]string)
	for _, override := range profile.Overrides {
		switch {
		case override.SurfaceID == instructionSurfaceID:
			// The prompt source file carries the instruction text.
		case len(override.Value.Tools) > 0:
			for _, toolRef := range override.Value.Tools {
				descriptions[toolRef.ID] = toolRef.Description
			}
		default:
			return nil, nil, fmt.Errorf(
				"baseline profile %q override %q is neither the instruction surface nor a tool surface; "+
					"this example's agent builder cannot restore it",
				path, override.SurfaceID,
			)
		}
	}
	return profile, descriptions, nil
}

// newRunID builds a time-based unique run identifier for the audit trail.
func newRunID() string {
	return "run-" + time.Now().Format("20060102-150405.000000000")
}
