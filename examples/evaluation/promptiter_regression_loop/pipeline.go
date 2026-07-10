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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName             = "promptiter-regression-loop-app"
	candidateAgentName  = "candidate"
	candidateRunnerName = "promptiter-regression-loop-candidate"
	trainEvalSetID      = "train"
	validationEvalSetID = "validation"
	traceSmokeMode      = "trace-smoke"
	traceSmokeEvalSetID = "trace_smoke"
	defaultMode         = "fake"
	defaultSeed         = int64(42)
)

const traceSmokeOptimizationSkippedReason = "trace mode replays actual output and cannot validate candidate inference"

type pipelineConfig struct {
	Mode       string
	DataDir    string
	OutputDir  string
	PromptPath string
	ConfigPath string
	Seed       int64
}

type promptIterConfig struct {
	TargetSurfaceIDs []string `json:"targetSurfaceIDs"`
	MaxRounds        int      `json:"maxRounds"`
	AcceptancePolicy struct {
		MinScoreGain float64 `json:"minScoreGain"`
	} `json:"acceptancePolicy"`
	StopPolicy struct {
		MaxRoundsWithoutAcceptance int `json:"maxRoundsWithoutAcceptance"`
	} `json:"stopPolicy"`
	FinalGate finalGateConfig `json:"finalGate"`
}

type finalGateConfig struct {
	MinValidationGain          float64  `json:"minValidationGain"`
	MaxDurationMs              int64    `json:"maxDurationMs"`
	CriticalCaseIDs            []string `json:"criticalCaseIDs"`
	RejectOnNewHardFail        bool     `json:"rejectOnNewHardFail"`
	RejectOnCriticalRegression bool     `json:"rejectOnCriticalRegression"`
}

type promptSource struct {
	Path    string
	Text    string
	Hash    string
	Summary string
}

type promptIterRuntime struct {
	engine         promptiterengine.Engine
	candidateRun   runner.Runner
	agentEvaluator evaluation.AgentEvaluator
	backwarder     *fakeBackwarder
	aggregator     *fakeAggregator
	optimizer      *fakeOptimizer
	close          func() error
}

type pipelineResult struct {
	Report             *OptimizationReport
	ReportJSONPath     string
	ReportMarkdownPath string
	Model              *deterministicFlightModel
	Backwarder         *fakeBackwarder
	Aggregator         *fakeAggregator
	Optimizer          *fakeOptimizer
	Prompt             promptSource
}

type metricsFileLocator struct{}

func runFakePipeline(ctx context.Context, cfg pipelineConfig) (*pipelineResult, error) {
	cfg = normalizePipelineConfig(cfg)
	switch cfg.Mode {
	case defaultMode:
		return runFakeOptimizationPipeline(ctx, cfg)
	case traceSmokeMode:
		return runTraceSmokePipeline(ctx, cfg)
	default:
		return nil, fmt.Errorf(
			"unsupported mode %q: supported modes are %q and %q",
			cfg.Mode,
			defaultMode,
			traceSmokeMode,
		)
	}
}

func runFakeOptimizationPipeline(ctx context.Context, cfg pipelineConfig) (*pipelineResult, error) {
	start := time.Now()
	prompt, err := readPromptSource(cfg.PromptPath)
	if err != nil {
		return nil, err
	}
	iterCfg, err := readPromptIterConfig(cfg.ConfigPath)
	if err != nil {
		return nil, err
	}
	fakeModel := &deterministicFlightModel{}
	runtime, err := buildPromptIterRuntime(ctx, cfg, prompt.Text, iterCfg, fakeModel)
	if err != nil {
		return nil, err
	}
	defer runtime.close()

	snapshot, err := runtime.engine.Describe(ctx)
	if err != nil {
		return nil, fmt.Errorf("describe promptiter engine: %w", err)
	}
	targetSurface, err := findTargetSurface(snapshot, iterCfg.TargetSurfaceIDs[0])
	if err != nil {
		return nil, err
	}
	baselineToolDescription := ""
	if len(targetSurface.Value.Tools) > 0 {
		baselineToolDescription = targetSurface.Value.Tools[0].Description
	}

	runRequest := buildRunRequest(iterCfg)
	runResult, err := runtime.engine.Run(ctx, runRequest)
	if err != nil {
		return nil, fmt.Errorf("run promptiter: %w", err)
	}
	candidateTrain, err := evaluateAcceptedProfileOnTrain(ctx, runtime.engine, runRequest.Train, runResult.AcceptedProfile)
	if err != nil {
		return nil, err
	}
	report, err := buildOptimizationReport(reportInput{
		mode:                    cfg.Mode,
		seed:                    cfg.Seed,
		prompt:                  prompt,
		targetSurfaceIDs:        iterCfg.TargetSurfaceIDs,
		baselineToolDescription: baselineToolDescription,
		runResult:               runResult,
		candidateTrain:          candidateTrain,
		finalGate:               iterCfg.FinalGate,
		latency:                 time.Since(start),
		modelCallCount:          fakeModel.CallCount(),
		workerCallCount: runtime.backwarder.callCount +
			runtime.aggregator.callCount +
			runtime.optimizer.callCount,
	})
	if err != nil {
		return nil, err
	}
	jsonPath, markdownPath, err := writeOptimizationReport(cfg.OutputDir, report)
	if err != nil {
		return nil, err
	}
	return &pipelineResult{
		Report:             report,
		ReportJSONPath:     jsonPath,
		ReportMarkdownPath: markdownPath,
		Model:              fakeModel,
		Backwarder:         runtime.backwarder,
		Aggregator:         runtime.aggregator,
		Optimizer:          runtime.optimizer,
		Prompt:             prompt,
	}, nil
}

func runTraceSmokePipeline(ctx context.Context, cfg pipelineConfig) (*pipelineResult, error) {
	start := time.Now()
	prompt, err := readPromptSource(cfg.PromptPath)
	if err != nil {
		return nil, err
	}
	iterCfg, err := readPromptIterConfig(cfg.ConfigPath)
	if err != nil {
		return nil, err
	}
	fakeModel := &deterministicFlightModel{}
	runtime, err := buildPromptIterRuntime(ctx, cfg, prompt.Text, iterCfg, fakeModel)
	if err != nil {
		return nil, err
	}
	defer runtime.close()

	snapshot, err := runtime.engine.Describe(ctx)
	if err != nil {
		return nil, fmt.Errorf("describe promptiter engine: %w", err)
	}
	targetSurface, err := findTargetSurface(snapshot, iterCfg.TargetSurfaceIDs[0])
	if err != nil {
		return nil, err
	}
	baselineToolDescription := ""
	if len(targetSurface.Value.Tools) > 0 {
		baselineToolDescription = targetSurface.Value.Tools[0].Description
	}
	profileEvaluator, ok := runtime.engine.(promptiterengine.ProfileEvaluator)
	if !ok {
		return nil, errors.New("promptiter engine does not support profile evaluation")
	}
	traceEvaluation, err := profileEvaluator.EvaluateWithProfile(ctx, promptiterengine.EvalSetInput{
		EvalSetID: traceSmokeEvalSetID,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("run trace smoke evaluation: %w", err)
	}
	report, err := buildTraceSmokeReport(traceSmokeReportInput{
		mode:                    cfg.Mode,
		seed:                    cfg.Seed,
		prompt:                  prompt,
		targetSurfaceIDs:        iterCfg.TargetSurfaceIDs,
		baselineToolDescription: baselineToolDescription,
		evaluation:              traceEvaluation,
		latency:                 time.Since(start),
		modelCallCount:          fakeModel.CallCount(),
		workerCallCount: runtime.backwarder.callCount +
			runtime.aggregator.callCount +
			runtime.optimizer.callCount,
	})
	if err != nil {
		return nil, err
	}
	jsonPath, markdownPath, err := writeOptimizationReport(cfg.OutputDir, report)
	if err != nil {
		return nil, err
	}
	return &pipelineResult{
		Report:             report,
		ReportJSONPath:     jsonPath,
		ReportMarkdownPath: markdownPath,
		Model:              fakeModel,
		Backwarder:         runtime.backwarder,
		Aggregator:         runtime.aggregator,
		Optimizer:          runtime.optimizer,
		Prompt:             prompt,
	}, nil
}

func normalizePipelineConfig(cfg pipelineConfig) pipelineConfig {
	if cfg.Mode == "" {
		cfg.Mode = defaultMode
	}
	if cfg.DataDir == "" {
		cfg.DataDir = "./data"
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "./output"
	}
	if cfg.PromptPath == "" {
		cfg.PromptPath = filepath.Join("config", "baseline_prompt.txt")
	}
	if cfg.ConfigPath == "" {
		cfg.ConfigPath = filepath.Join("config", "promptiter.json")
	}
	if cfg.Seed == 0 {
		cfg.Seed = defaultSeed
	}
	return cfg
}

func buildPromptIterRuntime(
	ctx context.Context,
	cfg pipelineConfig,
	instruction string,
	iterCfg promptIterConfig,
	fakeModel *deterministicFlightModel,
) (*promptIterRuntime, error) {
	candidateAgent, err := newCandidateAgent(fakeModel, instruction, initialLookupDescription)
	if err != nil {
		return nil, fmt.Errorf("create candidate agent: %w", err)
	}
	candidateRunner := runner.NewRunner(candidateRunnerName, candidateAgent)
	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(cfg.DataDir),
		metric.WithLocator(metricsFileLocator{}),
	)
	evalResultManager := inmemory.New()
	agentEvaluator, err := evaluation.New(
		appName,
		candidateRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
	)
	if err != nil {
		_ = candidateRunner.Close()
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	targetSurfaceID := iterCfg.TargetSurfaceIDs[0]
	backwarderInstance := &fakeBackwarder{targetSurfaceID: targetSurfaceID}
	aggregatorInstance := &fakeAggregator{}
	optimizerInstance := &fakeOptimizer{}
	engineInstance, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgent(candidateAgent),
		promptiterengine.WithAgentEvaluator(agentEvaluator),
		promptiterengine.WithBackwarder(backwarderInstance),
		promptiterengine.WithAggregator(aggregatorInstance),
		promptiterengine.WithOptimizer(optimizerInstance),
	)
	if err != nil {
		_ = agentEvaluator.Close()
		_ = candidateRunner.Close()
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}
	return &promptIterRuntime{
		engine:         engineInstance,
		candidateRun:   candidateRunner,
		agentEvaluator: agentEvaluator,
		backwarder:     backwarderInstance,
		aggregator:     aggregatorInstance,
		optimizer:      optimizerInstance,
		close: func() error {
			return errors.Join(agentEvaluator.Close(), candidateRunner.Close())
		},
	}, nil
}

func newCandidateAgent(
	m model.Model,
	instruction string,
	travelLookupToolDescription string,
) (agent.Agent, error) {
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(2048),
		Temperature: floatPtr(0.0),
		Stream:      false,
	}
	return llmagent.New(
		candidateAgentName,
		llmagent.WithModel(m),
		llmagent.WithInstruction(instruction),
		llmagent.WithTools(newTravelTools(travelLookupToolDescription)),
		llmagent.WithGenerationConfig(generationConfig),
	), nil
}

func buildRunRequest(cfg promptIterConfig) *promptiterengine.RunRequest {
	return &promptiterengine.RunRequest{
		Train: []promptiterengine.EvalSetInput{
			{EvalSetID: trainEvalSetID},
		},
		Validation: []promptiterengine.EvalSetInput{
			{EvalSetID: validationEvalSetID},
		},
		InitialProfile: nil,
		Judge:          nil,
		AcceptancePolicy: promptiterengine.AcceptancePolicy{
			MinScoreGain: cfg.AcceptancePolicy.MinScoreGain,
		},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: cfg.StopPolicy.MaxRoundsWithoutAcceptance,
		},
		MaxRounds:        cfg.MaxRounds,
		TargetSurfaceIDs: cfg.TargetSurfaceIDs,
	}
}

func evaluateAcceptedProfileOnTrain(
	ctx context.Context,
	engineInstance promptiterengine.Engine,
	trainInputs []promptiterengine.EvalSetInput,
	acceptedProfile *promptiter.Profile,
) (*promptiterengine.EvaluationResult, error) {
	profileEvaluator, ok := engineInstance.(promptiterengine.ProfileEvaluator)
	if !ok {
		return nil, errors.New("promptiter engine does not support profile evaluation")
	}
	if len(trainInputs) == 0 {
		return nil, errors.New("train eval sets are empty")
	}
	evalSets := make([]promptiterengine.EvalSetResult, 0, len(trainInputs))
	totalScore := 0.0
	for _, input := range trainInputs {
		result, err := profileEvaluator.EvaluateWithProfile(ctx, input, acceptedProfile)
		if err != nil {
			return nil, fmt.Errorf("evaluate accepted profile on train eval set %q: %w", input.EvalSetID, err)
		}
		for _, evalSet := range result.EvalSets {
			evalSets = append(evalSets, evalSet)
			totalScore += evalSet.OverallScore
		}
	}
	if len(evalSets) == 0 {
		return nil, errors.New("accepted profile train evaluation returned no eval sets")
	}
	return &promptiterengine.EvaluationResult{
		OverallScore: totalScore / float64(len(evalSets)),
		EvalSets:     evalSets,
	}, nil
}

func readPromptIterConfig(path string) (promptIterConfig, error) {
	cfg := promptIterConfig{
		TargetSurfaceIDs: []string{astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeTool, "lookup_record")},
		MaxRounds:        2,
	}
	cfg.AcceptancePolicy.MinScoreGain = 0.1
	cfg.StopPolicy.MaxRoundsWithoutAcceptance = 1
	cfg.FinalGate.MinValidationGain = 0.05
	cfg.FinalGate.MaxDurationMs = 180000
	cfg.FinalGate.CriticalCaseIDs = []string{"TR789"}
	cfg.FinalGate.RejectOnNewHardFail = true
	cfg.FinalGate.RejectOnCriticalRegression = true
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("read promptiter config %s: %w", path, err)
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("decode promptiter config %s: %w", path, err)
	}
	if len(cfg.TargetSurfaceIDs) == 0 {
		cfg.TargetSurfaceIDs = []string{astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeTool, "lookup_record")}
	}
	if cfg.MaxRounds <= 0 {
		cfg.MaxRounds = 2
	}
	if cfg.AcceptancePolicy.MinScoreGain == 0 {
		cfg.AcceptancePolicy.MinScoreGain = 0.1
	}
	if cfg.StopPolicy.MaxRoundsWithoutAcceptance <= 0 {
		cfg.StopPolicy.MaxRoundsWithoutAcceptance = 1
	}
	if cfg.FinalGate.MinValidationGain == 0 {
		cfg.FinalGate.MinValidationGain = 0.05
	}
	if cfg.FinalGate.MaxDurationMs <= 0 {
		cfg.FinalGate.MaxDurationMs = 180000
	}
	if len(cfg.FinalGate.CriticalCaseIDs) == 0 {
		cfg.FinalGate.CriticalCaseIDs = []string{"TR789"}
	}
	return cfg, nil
}

func readPromptSource(path string) (promptSource, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return promptSource{}, fmt.Errorf("read baseline prompt %s: %w", path, err)
	}
	sum := sha256.Sum256(data)
	text := string(data)
	return promptSource{
		Path:    filepath.ToSlash(filepath.Clean(path)),
		Text:    text,
		Hash:    hex.EncodeToString(sum[:]),
		Summary: summarizePrompt(text),
	}, nil
}

func summarizePrompt(text string) string {
	collapsed := strings.Join(strings.Fields(text), " ")
	if len(collapsed) <= 140 {
		return collapsed
	}
	return collapsed[:137] + "..."
}

func findTargetSurface(snapshot *astructure.Snapshot, surfaceID string) (astructure.Surface, error) {
	if snapshot == nil {
		return astructure.Surface{}, errors.New("engine describe returned nil snapshot")
	}
	for _, surface := range snapshot.Surfaces {
		if surface.SurfaceID == surfaceID {
			return surface, nil
		}
	}
	available := make([]string, 0, len(snapshot.Surfaces))
	for _, surface := range snapshot.Surfaces {
		available = append(available, surface.SurfaceID)
	}
	return astructure.Surface{}, fmt.Errorf(
		"target surface %q not found in engine.Describe snapshot; available surfaces: %s",
		surfaceID,
		strings.Join(available, ", "),
	)
}

func (metricsFileLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, "metrics.json")
}

func intPtr(value int) *int {
	return &value
}

func floatPtr(value float64) *float64 {
	return &value
}
