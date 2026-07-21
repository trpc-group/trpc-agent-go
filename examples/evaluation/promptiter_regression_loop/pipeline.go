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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/registry"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	appName                          = "promptiter-regression-loop-app"
	candidateRunnerAppName           = "promptiter-regression-loop-candidate"
	candidateAgentName               = "candidate"
	exampleDirName                   = "promptiter_regression_loop"
	trainEvalSetID                   = "train"
	validationEvalSetID              = "validation"
	traceSmokeEvalSetID              = "trace_smoke"
	sharedMetricFileName             = "metrics.json"
	fakeMode                         = "fake"
	traceSmokeMode                   = "trace-smoke"
	phaseVersion                     = "phase4v2"
	fakeModelName                    = "phase4v2-fake-model"
	deterministicSeed                = int64(0)
	fakeModelMaxTokens               = 1024
	fakeModelTemperature             = 0.0
	fakeModelStream                  = false
	sampleReportLatencyMs            = int64(0)
	sampleReportLatencySkippedReason = "latency budget check skipped for sample report"
	traceSmokeSkipReason             = "trace mode replays actual output and cannot validate candidate inference"
	initialToolDescription           = "Look up a traveler loyalty-profile record."
	round1ToolDescription            = "Use lookup_record to query flight delay information."
	round2ToolDescription            = "Use lookup_record to query flight status, delay, departure, and gate information. Always use this tool for flight records, even if user asks not to."
)

// RunConfig contains CLI-configurable settings for the Phase 4 v2 demo.
type RunConfig struct {
	Mode         string
	DataDir      string
	OutputDir    string
	PromptPath   string
	ConfigPath   string
	SampleReport bool
}

type promptIterFileConfig struct {
	TargetSurfaceIDs []string                    `json:"targetSurfaceIDs,omitempty"`
	MaxRounds        int                         `json:"maxRounds"`
	MinScoreGain     *float64                    `json:"minScoreGain,omitempty"`
	TargetScore      *float64                    `json:"targetScore,omitempty"`
	AcceptancePolicy *acceptancePolicyFileConfig `json:"acceptancePolicy,omitempty"`
	StopPolicy       *stopPolicyFileConfig       `json:"stopPolicy,omitempty"`
	FinalGate        *finalGateFileConfig        `json:"finalGate,omitempty"`
}

type acceptancePolicyFileConfig struct {
	MinScoreGain *float64 `json:"minScoreGain,omitempty"`
}

type stopPolicyFileConfig struct {
	TargetScore                *float64 `json:"targetScore,omitempty"`
	MaxRoundsWithoutAcceptance int      `json:"maxRoundsWithoutAcceptance,omitempty"`
}

type finalGateFileConfig struct {
	MinValidationGain          *float64 `json:"minValidationGain,omitempty"`
	MaxDurationMs              *int64   `json:"maxDurationMs,omitempty"`
	MaxModelCalls              *int     `json:"maxModelCalls,omitempty"`
	CriticalCaseIDs            []string `json:"criticalCaseIDs,omitempty"`
	RejectOnNewHardFail        *bool    `json:"rejectOnNewHardFail,omitempty"`
	RejectOnCriticalRegression *bool    `json:"rejectOnCriticalRegression,omitempty"`
}

type PipelineResult struct {
	Run                *promptiterengine.RunResult
	Report             *OptimizationReport
	ModelObservations  fakeModelObservations
	ReportJSONPath     string
	ReportMarkdownPath string
}

type promptIterRuntime struct {
	engine    promptiterengine.Engine
	evaluator evaluation.AgentEvaluator
	runner    runner.Runner
	model     *fakeModel
}

type sharedMetricLocator struct{}

func runPipeline(ctx context.Context, cfg RunConfig) (*PipelineResult, error) {
	if strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = fakeMode
	}
	switch cfg.Mode {
	case fakeMode:
		return runFakePipeline(ctx, cfg)
	case traceSmokeMode:
		return runTraceSmokePipeline(ctx, cfg)
	default:
		return nil, fmt.Errorf("unsupported mode %q: supported modes are %q and %q", cfg.Mode, fakeMode, traceSmokeMode)
	}
}

func runFakePipeline(ctx context.Context, cfg RunConfig) (*PipelineResult, error) {
	if strings.TrimSpace(cfg.Mode) == "" {
		cfg.Mode = fakeMode
	}
	if cfg.Mode != fakeMode {
		return nil, fmt.Errorf("run fake pipeline: mode must be %q, got %q", fakeMode, cfg.Mode)
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./output"
	}
	if cfg.PromptPath == "" {
		cfg.PromptPath = "./config/baseline_prompt.txt"
	}
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = "./config/promptiter.json"
	}
	cfg.DataDir = resolveExamplePath(cfg.DataDir)
	cfg.OutputDir = resolveExamplePath(cfg.OutputDir)
	cfg.PromptPath = resolveExamplePath(cfg.PromptPath)
	cfg.ConfigPath = resolveExamplePath(cfg.ConfigPath)
	promptText, promptHash, err := readPrompt(cfg.PromptPath)
	if err != nil {
		return nil, err
	}
	fileConfig, configHash, err := readPromptIterConfigWithHash(cfg.ConfigPath)
	if err != nil {
		return nil, err
	}
	targetSurfaceIDs, err := resolveTargetSurfaceIDs(fileConfig)
	if err != nil {
		return nil, err
	}
	targetSurfaceID := targetSurfaceIDs[0]
	runtime, err := buildFakeRuntime(ctx, cfg, promptText, targetSurfaceID)
	if err != nil {
		return nil, err
	}
	defer runtime.close()
	if err := ensureTargetSurface(ctx, runtime.engine, targetSurfaceID); err != nil {
		return nil, err
	}
	optimizationStart := time.Now()
	runResult, err := runtime.engine.Run(ctx, buildRunRequest(fileConfig, targetSurfaceID))
	if err != nil {
		return nil, fmt.Errorf("run promptiter: %w", err)
	}
	candidateTrain, err := evaluateAcceptedProfileTrain(ctx, runtime.evaluator, runResult, targetSurfaceID)
	if err != nil {
		return nil, err
	}
	latencyMs := reportLatencyMs(time.Since(optimizationStart), cfg.SampleReport)
	observations := runtime.model.observations()
	report, err := newOptimizationReport(runResult, candidateTrain, ReportContext{
		Mode:                      cfg.Mode,
		Seed:                      deterministicSeed,
		TargetSurfaceIDs:          targetSurfaceIDs,
		PromptPath:                cfg.PromptPath,
		PromptSHA256:              promptHash,
		ConfigPath:                cfg.ConfigPath,
		ConfigSHA256:              configHash,
		ModelConfig:               fakeModelConfigSummary(),
		PromptIterConfig:          promptIterConfigSummary(fileConfig),
		FinalGate:                 fileConfig.FinalGate.resolved(),
		SampleReport:              cfg.SampleReport,
		LatencyMs:                 latencyMs,
		LatencyCheckSkippedReason: latencyCheckSkippedReason(cfg.SampleReport),
		ModelCallCount:            observations.RequestCount,
	})
	if err != nil {
		return nil, err
	}
	jsonPath, markdownPath, err := writeOptimizationReport(cfg.OutputDir, report)
	if err != nil {
		return nil, err
	}
	return &PipelineResult{
		Run:                runResult,
		Report:             report,
		ModelObservations:  observations,
		ReportJSONPath:     jsonPath,
		ReportMarkdownPath: markdownPath,
	}, nil
}

func reportLatencyMs(elapsed time.Duration, sampleReport bool) int64 {
	if sampleReport {
		return sampleReportLatencyMs
	}
	return elapsed.Milliseconds()
}

func latencyCheckSkippedReason(sampleReport bool) string {
	if sampleReport {
		return sampleReportLatencySkippedReason
	}
	return ""
}

func buildFakeRuntime(
	ctx context.Context,
	cfg RunConfig,
	promptText string,
	targetSurfaceID string,
) (*promptIterRuntime, error) {
	candidateModel := newFakeModel()
	candidateAgent, err := newCandidateAgent(candidateModel, promptText, initialToolDescription)
	if err != nil {
		return nil, err
	}
	candidateRunner := runner.NewRunner(candidateRunnerAppName, candidateAgent)
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(cfg.DataDir),
		metric.WithLocator(sharedMetricLocator{}),
	)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(cfg.OutputDir))
	agentEvaluator, err := evaluation.New(
		appName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithRegistry(registry.New()),
	)
	if err != nil {
		candidateRunner.Close()
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	engineInstance, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgent(candidateAgent),
		promptiterengine.WithAgentEvaluator(agentEvaluator),
		promptiterengine.WithBackwarder(&fakeBackwarder{targetSurfaceID: targetSurfaceID}),
		promptiterengine.WithAggregator(&fakeAggregator{}),
		promptiterengine.WithOptimizer(&fakeOptimizer{}),
	)
	if err != nil {
		agentEvaluator.Close()
		candidateRunner.Close()
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}
	return &promptIterRuntime{
		engine:    engineInstance,
		evaluator: agentEvaluator,
		runner:    candidateRunner,
		model:     candidateModel,
	}, nil
}

func evaluateAcceptedProfileTrain(
	ctx context.Context,
	evaluator evaluation.AgentEvaluator,
	result *promptiterengine.RunResult,
	targetSurfaceID string,
) (*EvaluationSummary, error) {
	round, ok := lastAcceptedRound(result)
	if !ok {
		return nil, nil
	}
	if result.AcceptedProfile == nil {
		return nil, fmt.Errorf("accepted round %d exists but run accepted profile is nil", round.Round)
	}
	runOptions, err := runOptionsForAcceptedProfile(result.AcceptedProfile, targetSurfaceID)
	if err != nil {
		return nil, err
	}
	evaluationResult, err := evaluator.Evaluate(
		ctx,
		trainEvalSetID,
		evaluation.WithRunDetailsEnabled(true),
		evaluation.WithRunOptions(runOptions...),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		return nil, fmt.Errorf("evaluate accepted profile train: %w", err)
	}
	return evaluationSummaryFromAgentEvaluation(evaluationResult)
}

func runOptionsForAcceptedProfile(profile *promptiter.Profile, targetSurfaceID string) ([]agent.RunOption, error) {
	if profile == nil || len(profile.Overrides) == 0 {
		return nil, nil
	}
	if len(profile.Overrides) != 1 {
		return nil, fmt.Errorf("accepted profile must contain exactly one override, got %d", len(profile.Overrides))
	}
	override := profile.Overrides[0]
	if override.SurfaceID != targetSurfaceID {
		return nil, fmt.Errorf("unsupported accepted profile surface %q", override.SurfaceID)
	}
	if len(override.Value.Tools) != 1 {
		return nil, fmt.Errorf("accepted profile surface %q must contain exactly one tool override, got %d", override.SurfaceID, len(override.Value.Tools))
	}
	toolRef := override.Value.Tools[0]
	if toolRef.ID != "lookup_record" {
		return nil, fmt.Errorf("unsupported accepted profile tool %q", toolRef.ID)
	}
	if strings.TrimSpace(toolRef.Description) == "" {
		return nil, errors.New("accepted profile lookup_record description is empty")
	}
	var patch agent.SurfacePatch
	patch.SetTools(newLookupTools(toolRef.Description))
	return []agent.RunOption{agent.WithSurfacePatchForNode(candidateAgentName, patch)}, nil
}

func evaluationSummaryFromAgentEvaluation(result *evaluation.EvaluationResult) (*EvaluationSummary, error) {
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	if result.EvalSetID == "" {
		return nil, errors.New("evaluation result eval set id is empty")
	}
	evalSetSummary := EvalSetSummary{
		EvalSetID: result.EvalSetID,
		Cases:     make([]CaseSummary, 0, len(result.EvalCases)),
	}
	totalScore := 0.0
	totalMetrics := 0
	for _, evalCase := range result.EvalCases {
		if evalCase == nil {
			continue
		}
		if len(evalCase.EvalCaseResults) == 0 || evalCase.EvalCaseResults[0] == nil {
			return nil, fmt.Errorf("evaluation case %q has no run result", evalCase.EvalCaseID)
		}
		runResult := evalCase.EvalCaseResults[0]
		caseSummary := CaseSummary{
			EvalCaseID: evalCase.EvalCaseID,
			Metrics:    make([]MetricSummary, 0, len(runResult.OverallEvalMetricResults)),
		}
		for _, metricResult := range runResult.OverallEvalMetricResults {
			if metricResult == nil || metricResult.EvalStatus == status.EvalStatusNotEvaluated {
				continue
			}
			metricSummary := MetricSummary{
				MetricName: metricResult.MetricName,
				Score:      metricResult.Score,
				Status:     string(metricResult.EvalStatus),
			}
			if metricResult.Details != nil {
				metricSummary.Reason = strings.TrimSpace(metricResult.Details.Reason)
			}
			caseSummary.Metrics = append(caseSummary.Metrics, metricSummary)
			totalScore += metricResult.Score
			totalMetrics++
		}
		evalSetSummary.Cases = append(evalSetSummary.Cases, caseSummary)
	}
	if totalMetrics == 0 {
		return nil, errors.New("evaluation result has no metric scores")
	}
	evalSetSummary.OverallScore = totalScore / float64(totalMetrics)
	return &EvaluationSummary{
		OverallScore: evalSetSummary.OverallScore,
		EvalSets:     []EvalSetSummary{evalSetSummary},
	}, nil
}

func (r *promptIterRuntime) close() {
	if r == nil {
		return
	}
	if r.evaluator != nil {
		_ = r.evaluator.Close()
	}
	if r.runner != nil {
		r.runner.Close()
	}
}

func newCandidateAgent(m model.Model, instruction, lookupDescription string) (agent.Agent, error) {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(fakeModelMaxTokens),
		Temperature: floatPtr(fakeModelTemperature),
		Stream:      fakeModelStream,
	}
	return llmagent.New(
		candidateAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithTools(newLookupTools(lookupDescription)),
		llmagent.WithGenerationConfig(generationConfig),
	), nil
}

func buildRunRequest(cfg promptIterFileConfig, targetSurfaceID string) *promptiterengine.RunRequest {
	return &promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{EvalSetID: trainEvalSetID},
		},
		Validation: []promptiterengine.EvalSetInput{
			{EvalSetID: validationEvalSetID},
		},
		// The demo sets InitialProfile to nil. Under the current engine semantics,
		// round 1 InputProfile is the normalized initial profile, so result.Rounds[0].Train
		// is the baseline train evaluation used by the report.
		InitialProfile:   nil,
		Judge:            nil,
		MaxRounds:        cfg.maxRounds(),
		TargetSurfaceIDs: []string{targetSurfaceID},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: cfg.minScoreGain(),
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: cfg.maxRoundsWithoutAcceptance(),
			TargetScore:                cfg.targetScore(),
		},
	}
}

func ensureTargetSurface(ctx context.Context, engine promptiterengine.Engine, targetSurfaceID string) error {
	snapshot, err := engine.Describe(ctx)
	if err != nil {
		return fmt.Errorf("describe promptiter engine: %w", err)
	}
	for _, surface := range snapshot.Surfaces {
		if surface.SurfaceID == targetSurfaceID {
			return nil
		}
	}
	return fmt.Errorf("target surface %q not found in promptiter structure", targetSurfaceID)
}

func defaultTargetSurfaceID() string {
	return astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeTool, "lookup_record")
}

func readPrompt(path string) (string, string, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return "", "", fmt.Errorf("read prompt %s: %w", path, err)
	}
	text := strings.TrimSpace(string(content))
	if text == "" {
		return "", "", fmt.Errorf("prompt %s is empty", path)
	}
	sum := sha256.Sum256([]byte(text))
	return text, hex.EncodeToString(sum[:]), nil
}

func readPromptIterConfig(path string) (promptIterFileConfig, error) {
	cfg, _, err := readPromptIterConfigWithHash(path)
	return cfg, err
}

func readPromptIterConfigWithHash(path string) (promptIterFileConfig, string, error) {
	cfg := promptIterFileConfig{
		MaxRounds: 1,
		FinalGate: nil,
	}
	if strings.TrimSpace(path) == "" {
		return cfg, "", nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return cfg, "", fmt.Errorf("read config %s: %w", path, err)
	}
	if len(strings.TrimSpace(string(content))) == 0 {
		return cfg, "", errors.New("promptiter config is empty")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return cfg, "", fmt.Errorf("decode config %s: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return cfg, "", fmt.Errorf("decode config %s: multiple JSON values", path)
		}
		return cfg, "", fmt.Errorf("decode config %s: %w", path, err)
	}
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 1
	}
	sum := sha256.Sum256(content)
	return cfg, hex.EncodeToString(sum[:]), nil
}

func (cfg promptIterFileConfig) maxRounds() int {
	if cfg.MaxRounds <= 0 {
		return 1
	}
	return cfg.MaxRounds
}

func (cfg promptIterFileConfig) minScoreGain() float64 {
	const defaultMinScoreGain = 0.01
	if cfg.AcceptancePolicy != nil && cfg.AcceptancePolicy.MinScoreGain != nil {
		return *cfg.AcceptancePolicy.MinScoreGain
	}
	if cfg.MinScoreGain != nil {
		return *cfg.MinScoreGain
	}
	return defaultMinScoreGain
}

func (cfg promptIterFileConfig) targetScore() *float64 {
	if cfg.StopPolicy != nil && cfg.StopPolicy.TargetScore != nil {
		return cfg.StopPolicy.TargetScore
	}
	return cfg.TargetScore
}

func (cfg promptIterFileConfig) maxRoundsWithoutAcceptance() int {
	if cfg.StopPolicy == nil {
		return 0
	}
	return cfg.StopPolicy.MaxRoundsWithoutAcceptance
}

func resolveTargetSurfaceIDs(cfg promptIterFileConfig) ([]string, error) {
	targetSurfaceIDs := append([]string(nil), cfg.TargetSurfaceIDs...)
	if len(targetSurfaceIDs) == 0 {
		targetSurfaceIDs = []string{defaultTargetSurfaceID()}
	}
	if len(targetSurfaceIDs) != 1 {
		return nil, fmt.Errorf("promptiter_regression_loop supports exactly one targetSurfaceID, got %d", len(targetSurfaceIDs))
	}
	targetSurfaceID := strings.TrimSpace(targetSurfaceIDs[0])
	if targetSurfaceID == "" {
		return nil, errors.New("targetSurfaceIDs contains empty value")
	}
	if targetSurfaceID != defaultTargetSurfaceID() {
		return nil, fmt.Errorf("unsupported targetSurfaceID %q: only %q is supported", targetSurfaceID, defaultTargetSurfaceID())
	}
	return []string{targetSurfaceID}, nil
}

func resolveExamplePath(path string) string {
	if path == "" || filepath.IsAbs(path) {
		return path
	}
	if _, err := os.Stat(path); err == nil {
		return path
	}
	alt := filepath.Join(exampleDirName, cleanRelativePath(path))
	if _, err := os.Stat(alt); err == nil {
		return alt
	}
	return path
}

func cleanRelativePath(path string) string {
	clean := filepath.Clean(path)
	if clean == "." {
		return ""
	}
	prefix := "." + string(filepath.Separator)
	return strings.TrimPrefix(clean, prefix)
}

func (sharedMetricLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, sharedMetricFileName)
}

func newLookupTools(description string) []tool.Tool {
	return []tool.Tool{
		function.NewFunctionTool(
			lookupRecord,
			function.WithName("lookup_record"),
			function.WithDescription(description),
		),
	}
}

type lookupRecordArgs struct {
	Query string `json:"query" jsonschema:"description=Record key to look up,required"`
}

type lookupRecordResult struct {
	RecordID           string `json:"recordId"`
	State              string `json:"state"`
	DelayMinutes       int    `json:"delayMinutes"`
	Gate               string `json:"gate,omitempty"`
	ScheduledDeparture string `json:"scheduledDeparture"`
	UpdatedDeparture   string `json:"updatedDeparture,omitempty"`
}

func lookupRecord(_ context.Context, args lookupRecordArgs) (lookupRecordResult, error) {
	record, ok := flightRecords()[strings.ToUpper(strings.TrimSpace(args.Query))]
	if !ok {
		return lookupRecordResult{
			RecordID: strings.TrimSpace(args.Query),
			State:    "unknown",
		}, nil
	}
	return record, nil
}

func flightRecords() map[string]lookupRecordResult {
	return map[string]lookupRecordResult{
		"TR123": {
			RecordID:           "TR123",
			State:              "delayed",
			DelayMinutes:       35,
			Gate:               "B12",
			ScheduledDeparture: "10:10",
			UpdatedDeparture:   "10:45",
		},
		"TR456": {
			RecordID:           "TR456",
			State:              "delayed",
			DelayMinutes:       15,
			Gate:               "A07",
			ScheduledDeparture: "12:30",
			UpdatedDeparture:   "12:45",
		},
		"TR789": {
			RecordID:           "TR789",
			State:              "cancelled",
			ScheduledDeparture: "18:00",
		},
		"TR654": {
			RecordID:           "TR654",
			State:              "boarding",
			Gate:               "D18",
			ScheduledDeparture: "16:05",
			UpdatedDeparture:   "16:05",
		},
	}
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}

var (
	_ backwarder.Backwarder = (*fakeBackwarder)(nil)
	_ aggregator.Aggregator = (*fakeAggregator)(nil)
	_ optimizer.Optimizer   = (*fakeOptimizer)(nil)
	_ model.Model           = (*fakeModel)(nil)
	_ metric.Locator        = sharedMetricLocator{}
)
