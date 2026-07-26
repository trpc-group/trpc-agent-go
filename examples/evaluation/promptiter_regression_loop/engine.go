//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	evalresultlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalsetlocal "trpc.group/trpc-go/trpc-agent-go/evaluation/evalset/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	metriclocal "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/local"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/aggregator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/backwarder"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/optimizer"
	"trpc.group/trpc-go/trpc-agent-go/examples/evaluation/promptiter_regression_loop/fakemodel"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const (
	appName             = "promptiter-regression-loop-app"
	candidateAppName    = "promptiter-regression-loop-candidate"
	judgeAppName        = "promptiter-regression-loop-judge"
	backwarderAppName   = "promptiter-regression-loop-backwarder"
	aggregatorAppName   = "promptiter-regression-loop-aggregator"
	optimizerAppName    = "promptiter-regression-loop-optimizer"
	trainEvalSetID      = "regression-loop-train"
	validationEvalSetID = "regression-loop-validation"
	sharedMetricFileID  = "regression-loop"
)

// modelSource selects how LLM roles obtain their model implementation.
type modelSource string

const (
	// modelSourceOpenAI uses an OpenAI-compatible endpoint and requires OPENAI_API_KEY.
	modelSourceOpenAI modelSource = "openai"
	// modelSourceFake uses deterministic scripted models + collaborators (no API key).
	modelSourceFake modelSource = "fake"
)

// runConfig captures every input needed to run the regression + optimization pipeline.
type runConfig struct {
	DataDir              string
	OutputDir            string
	FixturesDir          string
	BaselinePromptFile   string
	ModelSource          modelSource
	CandidateModelName   string
	CandidateInstruction string
	JudgeModelName       string
	WorkerModelName      string

	MaxRounds                  int
	MinScoreGain               float64
	MaxRoundsWithoutAcceptance int
	TargetScore                float64

	GateMinValidationGain  float64
	KeyCaseIDs             []string
	MaxCandidateModelCalls int

	EvalCaseParallelism        int
	BackwardCaseParallelism    int
	AggregationParallelism     int
	OptimizerParallelism       int
	ParallelInferenceEnabled   bool
	ParallelEvaluationEnabled  bool
	ParallelBackwardEnabled    bool
	ParallelAggregationEnabled bool
	ParallelOptimizerEnabled   bool

	DebugIO bool
	Logger  *log.Logger
	// modelCalls counts candidate model invocations across the whole run (shared pointer). Nil
	// disables counting.
	modelCalls *callCounter
}

// sharedMetricLocator resolves every eval set to a single shared metrics file.
type sharedMetricLocator struct {
	metricFileID string
}

// Build maps every eval set to the shared metric file used by the example.
func (l *sharedMetricLocator) Build(baseDir, appName, _ string) string {
	return filepath.Join(baseDir, appName, l.metricFileID+".metrics.json")
}

// promptIterRuntime bundles the assembled engine and its resource cleanup.
type promptIterRuntime struct {
	engine         promptiterengine.Engine
	agentEvaluator evaluation.AgentEvaluator
	close          func()
}

// collaborators bundles the three PromptIter stage implementations plus any cleanup they need.
type collaborators struct {
	backwarder backwarder.Backwarder
	aggregator aggregator.Aggregator
	optimizer  optimizer.Optimizer
	close      func()
}

// buildPromptIterRuntime wires the candidate agent, evaluator, and PromptIter collaborators into
// an engine. The candidate model and the three stage collaborators are chosen by model source:
// the fake source uses deterministic, no-network implementations; the openai source uses the real
// LLM-driven backwarder/aggregator/optimizer forked from examples/evaluation/promptiter/syncrun.
func buildPromptIterRuntime(ctx context.Context, cfg runConfig, fixture *fakemodel.Fixture) (*promptIterRuntime, error) {
	if (cfg.ParallelInferenceEnabled || cfg.ParallelEvaluationEnabled) && cfg.EvalCaseParallelism <= 0 {
		return nil, errors.New("eval case parallelism must be greater than 0 when parallel inference or evaluation is enabled")
	}
	surfaceID := targetSurfaceID()

	built, err := buildCandidateEvaluator(cfg, cfg.CandidateInstruction, fixture, "baseline")
	if err != nil {
		return nil, err
	}

	collab, err := buildCollaborators(ctx, cfg, surfaceID, fixture)
	if err != nil {
		built.close()
		return nil, fmt.Errorf("build collaborators: %w", err)
	}

	engineInstance, err := promptiterengine.New(
		ctx,
		promptiterengine.WithAgent(built.candidateAgent),
		promptiterengine.WithAgentEvaluator(built.evaluator),
		promptiterengine.WithBackwarder(collab.backwarder),
		promptiterengine.WithAggregator(collab.aggregator),
		promptiterengine.WithOptimizer(collab.optimizer),
	)
	if err != nil {
		collab.close()
		built.close()
		return nil, fmt.Errorf("create promptiter engine: %w", err)
	}
	return &promptIterRuntime{
		engine:         engineInstance,
		agentEvaluator: built.evaluator,
		close: func() {
			collab.close()
			built.close()
		},
	}, nil
}

// builtEvaluator bundles an AgentEvaluator with the candidate agent it wraps (needed by the engine)
// and a cleanup func that closes the evaluator and both runners.
type builtEvaluator struct {
	evaluator      evaluation.AgentEvaluator
	candidateAgent agent.Agent
	close          func()
}

// buildCandidateEvaluator constructs an AgentEvaluator whose candidate agent uses the supplied
// instruction. It is reused twice: once for the baseline/engine agent (baseline instruction) and
// once to evaluate the optimizer's accepted instruction on the held-out sets for the gate. The
// phase ("baseline" or "candidate") separates each run's raw eval-result artifacts into its own
// output subdirectory so the candidate run never overwrites the baseline's persisted results.
func buildCandidateEvaluator(cfg runConfig, instruction string, fixture *fakemodel.Fixture, phase string) (*builtEvaluator, error) {
	candidateModel, err := buildCandidateModel(cfg, fixture)
	if err != nil {
		return nil, fmt.Errorf("build candidate model: %w", err)
	}
	candidateAgent, err := newCandidateAgent(candidateModel, instruction)
	if err != nil {
		return nil, fmt.Errorf("create candidate agent: %w", err)
	}
	candidateRunner := runner.NewRunner(candidateAppName, candidateAgent)
	candidateLoggedRunner := newLoggingRunner("candidate", candidateRunner, cfg.Logger, cfg.DebugIO)

	// Judge is only exercised by llmJudge metrics; the sample metrics are deterministic so it is
	// never called, but the evaluator still expects a runner, so provide one.
	judgeModel, err := buildJudgeModel(cfg)
	if err != nil {
		candidateRunner.Close()
		return nil, fmt.Errorf("build judge model: %w", err)
	}
	judgeRunner := runner.NewRunner(judgeAppName, newJudgeAgent(judgeModel))
	judgeLoggedRunner := newLoggingRunner("judge", judgeRunner, cfg.Logger, cfg.DebugIO)

	evalSetManager := evalsetlocal.New(evalset.WithBaseDir(cfg.DataDir))
	metricManager := metriclocal.New(
		metric.WithBaseDir(cfg.DataDir),
		metric.WithLocator(&sharedMetricLocator{metricFileID: sharedMetricFileID}),
	)
	evalResultManager := evalresultlocal.New(evalresult.WithBaseDir(filepath.Join(cfg.OutputDir, phase)))
	agentEvaluator, err := evaluation.New(
		appName,
		candidateLoggedRunner,
		evaluation.WithEvalSetManager(evalSetManager),
		evaluation.WithMetricManager(metricManager),
		evaluation.WithEvalResultManager(evalResultManager),
		evaluation.WithJudgeRunner(judgeLoggedRunner),
		evaluation.WithNumRuns(1),
	)
	if err != nil {
		candidateRunner.Close()
		judgeRunner.Close()
		return nil, fmt.Errorf("create evaluator: %w", err)
	}
	return &builtEvaluator{
		evaluator:      agentEvaluator,
		candidateAgent: candidateAgent,
		close: func() {
			agentEvaluator.Close()
			candidateRunner.Close()
			judgeRunner.Close()
		},
	}, nil
}

// loadFixtureIfFake loads the deterministic fixture for the fake model source; it returns nil for
// the openai source (which uses live models and needs no fixture).
func loadFixtureIfFake(cfg runConfig) (*fakemodel.Fixture, error) {
	if cfg.ModelSource != modelSourceFake {
		return nil, nil
	}
	fixture, err := fakemodel.LoadFixture(fixturePath(cfg))
	if err != nil {
		return nil, fmt.Errorf("load fake fixture: %w", err)
	}
	return fixture, nil
}

// buildCandidateModel returns the candidate agent model for the configured source, wrapped in a
// call counter so the audit report can attribute candidate model invocations as a cost proxy.
func buildCandidateModel(cfg runConfig, fixture *fakemodel.Fixture) (model.Model, error) {
	base, err := buildCandidateBaseModel(cfg, fixture)
	if err != nil {
		return nil, err
	}
	return newCountingModel(base, cfg.modelCalls), nil
}

func buildCandidateBaseModel(cfg runConfig, fixture *fakemodel.Fixture) (model.Model, error) {
	switch cfg.ModelSource {
	case modelSourceFake:
		if fixture == nil {
			return nil, errors.New("fake fixture is nil")
		}
		return fakemodel.NewScriptedModel("candidate", fixture.Candidate), nil
	case modelSourceOpenAI, "":
		return loadOpenAIModel(cfg.CandidateModelName)
	default:
		return nil, fmt.Errorf("unknown model source %q", cfg.ModelSource)
	}
}

// buildJudgeModel returns the judge model. For the fake source it is a scripted no-op model
// (never invoked by deterministic metrics); for openai it is the configured judge model.
func buildJudgeModel(cfg runConfig) (model.Model, error) {
	switch cfg.ModelSource {
	case modelSourceFake:
		return fakemodel.NewScriptedModel("judge", fakemodel.CandidateScript{Default: ""}), nil
	case modelSourceOpenAI, "":
		return loadOpenAIModel(cfg.JudgeModelName)
	default:
		return nil, fmt.Errorf("unknown model source %q", cfg.ModelSource)
	}
}

// buildCollaborators returns the backwarder/aggregator/optimizer trio for the configured source.
func buildCollaborators(ctx context.Context, cfg runConfig, surfaceID string, fixture *fakemodel.Fixture) (*collaborators, error) {
	if cfg.ModelSource == modelSourceFake {
		if fixture == nil {
			return nil, errors.New("fake fixture is nil")
		}
		return &collaborators{
			backwarder: fakemodel.DeterministicBackwarder{TargetSurfaceID: surfaceID},
			aggregator: fakemodel.DeterministicAggregator{},
			optimizer:  fakemodel.DeterministicOptimizer{Transitions: fixture.Optimizer.Transitions},
			close:      func() {},
		}, nil
	}
	return buildOpenAICollaborators(ctx, cfg)
}

// buildOpenAICollaborators wires the real LLM-driven PromptIter stages using worker models.
func buildOpenAICollaborators(ctx context.Context, cfg runConfig) (*collaborators, error) {
	backwarderModel, err := loadOpenAIModel(cfg.WorkerModelName)
	if err != nil {
		return nil, fmt.Errorf("load backwarder model: %w", err)
	}
	aggregatorModel, err := loadOpenAIModel(cfg.WorkerModelName)
	if err != nil {
		return nil, fmt.Errorf("load aggregator model: %w", err)
	}
	optimizerModel, err := loadOpenAIModel(cfg.WorkerModelName)
	if err != nil {
		return nil, fmt.Errorf("load optimizer model: %w", err)
	}
	backwarderRunner := runner.NewRunner(backwarderAppName, newBackwarderAgent(backwarderModel))
	aggregatorRunner := runner.NewRunner(aggregatorAppName, newAggregatorAgent(aggregatorModel))
	optimizerRunner := runner.NewRunner(optimizerAppName, newOptimizerAgent(optimizerModel))
	backwarderLogged := newLoggingRunner("backwarder", backwarderRunner, cfg.Logger, cfg.DebugIO)
	aggregatorLogged := newLoggingRunner("aggregator", aggregatorRunner, cfg.Logger, cfg.DebugIO)
	optimizerLogged := newLoggingRunner("optimizer", optimizerRunner, cfg.Logger, cfg.DebugIO)
	closeAll := func() {
		backwarderRunner.Close()
		aggregatorRunner.Close()
		optimizerRunner.Close()
	}
	backwarderInstance, err := backwarder.New(ctx, backwarderLogged)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("create backwarder: %w", err)
	}
	aggregatorInstance, err := aggregator.New(ctx, aggregatorLogged)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("create aggregator: %w", err)
	}
	optimizerInstance, err := optimizer.New(ctx, optimizerLogged)
	if err != nil {
		closeAll()
		return nil, fmt.Errorf("create optimizer: %w", err)
	}
	return &collaborators{
		backwarder: backwarderInstance,
		aggregator: aggregatorInstance,
		optimizer:  optimizerInstance,
		close:      closeAll,
	}, nil
}

// buildRunRequest builds the PromptIter engine request that optimizes the candidate instruction
// surface over the train set with validation-based acceptance.
func buildRunRequest(cfg runConfig, surfaceID string) *promptiterengine.RunRequest {
	targetScore := cfg.TargetScore
	return &promptiterengine.RunRequest{
		Train:      []promptiterengine.EvalSetInput{{EvalSetID: trainEvalSetID}},
		Validation: []promptiterengine.EvalSetInput{{EvalSetID: validationEvalSetID}},
		EvaluationOptions: promptiterengine.EvaluationOptions{
			EvalCaseParallelism:               cfg.EvalCaseParallelism,
			EvalCaseParallelInferenceEnabled:  cfg.ParallelInferenceEnabled,
			EvalCaseParallelEvaluationEnabled: cfg.ParallelEvaluationEnabled,
		},
		BackwardOptions: promptiterengine.BackwardOptions{
			CaseParallelismEnabled: cfg.ParallelBackwardEnabled,
			CaseParallelism:        cfg.BackwardCaseParallelism,
		},
		AggregationOptions: promptiterengine.AggregationOptions{
			SurfaceParallelismEnabled: cfg.ParallelAggregationEnabled,
			SurfaceParallelism:        cfg.AggregationParallelism,
		},
		OptimizerOptions: promptiterengine.OptimizerOptions{
			SurfaceParallelismEnabled: cfg.ParallelOptimizerEnabled,
			SurfaceParallelism:        cfg.OptimizerParallelism,
		},
		AcceptancePolicy: promptiterengine.AcceptancePolicy{MinScoreGain: cfg.MinScoreGain},
		StopPolicy: promptiterengine.StopPolicy{
			MaxRoundsWithoutAcceptance: cfg.MaxRoundsWithoutAcceptance,
			TargetScore:                &targetScore,
		},
		MaxRounds:        cfg.MaxRounds,
		TargetSurfaceIDs: []string{surfaceID},
	}
}

func loadOpenAIModel(modelName string) (model.Model, error) {
	name := strings.TrimSpace(modelName)
	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	baseURL := strings.TrimSpace(os.Getenv("OPENAI_BASE_URL"))
	switch {
	case name == "":
		return nil, errors.New("model name is empty")
	case apiKey == "":
		return nil, errors.New("OPENAI_API_KEY is empty")
	}
	options := make([]openai.Option, 0, 2)
	options = append(options, openai.WithAPIKey(apiKey))
	if baseURL != "" {
		options = append(options, openai.WithBaseURL(baseURL))
	}
	return openai.New(name, options...), nil
}

// fixturePath returns the deterministic fixture file for the fake model source.
func fixturePath(cfg runConfig) string {
	return filepath.Join(cfg.FixturesDir, sharedMetricFileID+".fake.json")
}

// resolveBaselineInstruction resolves the baseline candidate instruction with precedence:
// explicit flag value > baseline prompt file > built-in default constant. It returns the resolved
// instruction and the source file actually read (empty when the flag or the default was used), so
// the audit report can record where the baseline prompt came from.
//
// A non-empty promptFile is treated as an operator commitment: if it cannot be read or is blank, the
// function returns an error rather than silently falling back to the built-in default — a path typo
// in a CI regression gate must fail loud, not optimize and approve a different prompt than intended.
// The built-in default is reserved for an explicitly absent prompt file (empty promptFile).
func resolveBaselineInstruction(flagValue, promptFile string) (instruction, sourceFile string, err error) {
	if trimmed := strings.TrimSpace(flagValue); trimmed != "" {
		return trimmed, "", nil
	}
	if promptFile == "" {
		return defaultCandidateInstruction, "", nil
	}
	data, err := os.ReadFile(promptFile)
	if err != nil {
		return "", "", fmt.Errorf("read baseline prompt file %q: %w", promptFile, err)
	}
	fromFile := strings.TrimSpace(string(data))
	if fromFile == "" {
		return "", "", fmt.Errorf("baseline prompt file %q is blank", promptFile)
	}
	return fromFile, promptFile, nil
}

// targetSurfaceID is the instruction surface of the candidate agent that PromptIter optimizes.
func targetSurfaceID() string {
	return astructure.SurfaceID(candidateAgentName, astructure.SurfaceTypeInstruction)
}
