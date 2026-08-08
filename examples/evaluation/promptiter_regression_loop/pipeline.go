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
	"sort"
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
	// ConfigPath is the configuration file this run was loaded from. It is
	// only used to render the promotion command in the report; empty falls
	// back to the binary's own default resolution.
	ConfigPath string
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
	// rejection and when the accepted profile touched no instruction surface.
	CandidatePrompt string `json:"candidatePrompt,omitempty"`
	// CandidatePromptPath is where the accepted prompt was written.
	CandidatePromptPath string `json:"candidatePromptPath,omitempty"`
	// CandidateProfilePath is where the accepted effective profile was
	// written; empty on rejection.
	CandidateProfilePath string `json:"candidateProfilePath,omitempty"`
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

	// The accepted candidate artifacts (and the optional write-back) are only
	// staged here. On rejection the stable candidate artifact paths are staged
	// for removal instead: a previously accepted run may have left its
	// candidate files there, and leaving them next to a fresh rejection report
	// would let consumers of the stable paths deploy a stale candidate despite
	// the latest gate decision.
	var staged []stagedFile
	if decision.Accepted {
		staged, err = stageCandidateArtifacts(opts, inputs, result, decision)
		if err != nil {
			return nil, err
		}
	} else {
		staged = stageCandidateRemovals(opts.OutputDir)
	}

	result.Cost = tracker.Snapshot()
	result.FinishedAt = time.Now()
	if decision.Accepted {
		result.Status = StatusAccepted
	} else {
		result.Status = StatusRejected
	}
	result.Message = decision.Summary

	// S6: machine- and human-readable reports plus the run summary. The
	// reports are rendered in memory and published together with the staged
	// candidate state as one rollback unit, so a failure anywhere in the
	// publication leaves both the previous reports and the previous deployable
	// state intact — the stable paths never expose a decision whose candidate
	// artifacts were not successfully published.
	stageStart = time.Now()
	logger.Printf("S6: reports")
	reportFiles, jsonPath, markdownPath, err := renderReports(opts, result)
	if err != nil {
		return nil, err
	}
	result.ReportJSONPath = jsonPath
	result.ReportMarkdownPath = markdownPath
	result.StageDurations["s6_report"] = time.Since(stageStart)
	if err := publishFiles(append(reportFiles, staged...)); err != nil {
		return nil, err
	}
	logger.Printf("gate decision: %s", decision.Summary)
	return result, nil
}

// prepareOutputDir creates the output directory. Stale deployable candidate
// artifacts from a previous run are not removed here: their removal (on
// rejection) or replacement (on acceptance) is staged and published together
// with the reports, so the stable paths always change as one consistent unit
// and a failed run keeps the previous run's reports and candidate state.
func prepareOutputDir(outputDir string) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("create output dir %q: %w", outputDir, err)
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
	knownMetrics := make(map[string]struct{}, len(metricNames))
	for _, metricName := range metricNames {
		evalMetric, err := metricManager.Get(ctx, opts.Config.AppName, opts.Config.EvalSets.Train, metricName)
		if err != nil {
			return nil, nil, fmt.Errorf("get metric %q: %w", metricName, err)
		}
		metrics = append(metrics, evalMetric)
		knownMetrics[evalMetric.MetricName] = struct{}{}
	}
	// A hint keyed by a metric name that does not exist is silently unused,
	// and the metric it was meant to classify falls back to
	// final_response_mismatch — a mistyped route_error hint could then slip a
	// hard failure past a nonzero regression allowance. Fail closed instead.
	unknownHints := make([]string, 0)
	for hintMetric := range opts.Config.Attribution.MetricCategoryHints {
		if _, ok := knownMetrics[hintMetric]; !ok {
			unknownHints = append(unknownHints, hintMetric)
		}
	}
	if len(unknownHints) > 0 {
		sort.Strings(unknownHints)
		return nil, nil, fmt.Errorf(
			"attribution.metricCategoryHints references metric(s) not defined for the app: %s",
			strings.Join(unknownHints, ", "),
		)
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
	// Baseline attributions ride along on each delta so the hard-fail gate can
	// detect a failure-category transition on a case that fails on both sides.
	baselineAttributions := make(map[string]*CaseAttribution, len(result.BaselineAttributions))
	for i := range result.BaselineAttributions {
		attribution := result.BaselineAttributions[i]
		baselineAttributions[attribution.EvalSetID+"/"+attribution.EvalCaseID] = &attribution
	}
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
			key := deltas[i].EvalSetID + "/" + deltas[i].EvalCaseID
			if !deltas[i].BaselinePass {
				deltas[i].BaselineAttribution = baselineAttributions[key]
			}
			if deltas[i].CandidatePass {
				continue
			}
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

// stagedFile is one deferred artifact operation: a content write, or a
// removal when remove is set. Reports, candidate outputs, and the write-back
// baseline are staged after the gate decision and published in one
// publishFiles call, so a failure never leaves reports that disagree with
// the deployable candidate state.
type stagedFile struct {
	path    string
	content []byte
	// remove deletes the path instead of writing content, used to clear the
	// stable candidate artifacts of a previously accepted run on rejection.
	remove bool
}

// stageCandidateRemovals stages the deletion of the stable deployable
// candidate artifacts, so a rejecting run clears any previously accepted
// candidate in the same rollback unit that publishes its rejection reports.
func stageCandidateRemovals(outputDir string) []stagedFile {
	return []stagedFile{
		{path: filepath.Join(outputDir, candidatePromptFileName), remove: true},
		{path: filepath.Join(outputDir, candidateProfileFileName), remove: true},
	}
}

// Inter-process publication lock. Snapshot, apply, and rollback must not
// interleave with another process publishing to the same paths: two runs (or a
// run and a -promote) that each succeed can otherwise leave a mixed state, such
// as a candidate prompt without the candidate profile it was published with.
const (
	// publishLockFileName is created inside every directory a publication
	// touches. Exclusive creation is the atomic primitive; it works on every
	// platform this example builds for.
	publishLockFileName = ".promptiter-publish.lock"
	// publishLockPollInterval is how often a waiter retries.
	publishLockPollInterval = 20 * time.Millisecond
)

// publishLockTimeout bounds the wait for a concurrent publication. The
// critical section is a handful of small file writes, so a wait this long
// means the holder is stuck or died. It is a variable so tests can shorten it.
var publishLockTimeout = 30 * time.Second

// publishFiles applies every staged operation. When any step fails, files
// already touched are restored to their prior content (or removed when they
// did not exist) so an error never leaves a partially published state. Every
// directory it touches is locked against other processes for the whole
// snapshot-apply-rollback sequence.
func publishFiles(files []stagedFile) error {
	release, err := lockPublishDirs(files)
	if err != nil {
		return err
	}
	defer release()
	return publishLockedFiles(files)
}

// lockPublishDirs takes the publication lock of every directory the staged
// files live in and returns the release function. Locks are acquired in a
// stable order (sorted by absolute path) so two processes publishing to the
// same pair of directories can never deadlock against each other.
func lockPublishDirs(files []stagedFile) (func(), error) {
	dirs := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		dir := filepath.Dir(file.path)
		if absolute, err := filepath.Abs(dir); err == nil {
			dir = absolute
		}
		if _, ok := seen[dir]; ok {
			continue
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	held := make([]string, 0, len(dirs))
	release := func() {
		for i := len(held) - 1; i >= 0; i-- {
			_ = os.Remove(held[i])
		}
	}
	for _, dir := range dirs {
		lockPath := filepath.Join(dir, publishLockFileName)
		if err := acquirePublishLock(lockPath); err != nil {
			release()
			return nil, err
		}
		held = append(held, lockPath)
	}
	return release, nil
}

// acquirePublishLock creates the lock file exclusively, retrying while another
// process holds it. The holder's pid and start time are recorded so a lock left
// behind by a killed process can be identified and removed by hand.
func acquirePublishLock(lockPath string) error {
	deadline := time.Now().Add(publishLockTimeout)
	for {
		file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			_, writeErr := fmt.Fprintf(file, "pid=%d since=%s\n", os.Getpid(), time.Now().Format(time.RFC3339Nano))
			closeErr := file.Close()
			if writeErr != nil || closeErr != nil {
				// The lock is held; the diagnostic content is best effort.
				return nil
			}
			return nil
		}
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("acquire publication lock %q: %w", lockPath, err)
		}
		if time.Now().After(deadline) {
			holder, _ := os.ReadFile(lockPath)
			return fmt.Errorf(
				"publication lock %q is held by another run (%s) after %s; "+
					"wait for it to finish, or delete the lock file if that process died",
				lockPath, strings.TrimSpace(string(holder)), publishLockTimeout,
			)
		}
		time.Sleep(publishLockPollInterval)
	}
}

// publishLockedFiles performs the staged operations. It assumes the caller
// holds the publication lock of every directory involved.
func publishLockedFiles(files []stagedFile) error {
	type priorState struct {
		path    string
		content []byte
		existed bool
	}
	priors := make([]priorState, 0, len(files))
	rollback := func() {
		for i := len(priors) - 1; i >= 0; i-- {
			prior := priors[i]
			if prior.existed {
				_ = os.WriteFile(prior.path, prior.content, 0o644)
			} else {
				_ = os.Remove(prior.path)
			}
		}
	}
	for _, file := range files {
		prior := priorState{path: file.path}
		content, err := os.ReadFile(file.path)
		switch {
		case err == nil:
			prior.existed = true
			prior.content = content
		case !errors.Is(err, os.ErrNotExist):
			rollback()
			return fmt.Errorf("snapshot %q before publish: %w", file.path, err)
		}
		priors = append(priors, prior)
		if file.remove {
			if err := os.Remove(file.path); err != nil && !errors.Is(err, os.ErrNotExist) {
				rollback()
				return fmt.Errorf("remove %q: %w", file.path, err)
			}
			continue
		}
		if err := os.WriteFile(file.path, file.content, 0o644); err != nil {
			rollback()
			return fmt.Errorf("publish %q: %w", file.path, err)
		}
	}
	return nil
}

// stageCandidateArtifacts prepares the accepted candidate's writes. The deployable
// candidate_profile.json always carries the *effective* profile: the accepted
// overrides merged onto the previously restored baseline profile. The restored
// baseline overrides are baked into the agent for this run, so the engine no
// longer emits them as patches; serializing the round-relative overrides alone
// would publish an artifact missing inherited overrides (e.g. an earlier
// accepted tool description) that were active when the candidate passed the
// gate. The instruction text additionally lands in candidate_prompt.txt. The
// engine normalizes away no-op overrides, so an accepted profile whose patches
// only touched non-instruction surfaces legitimately carries no instruction
// text: that keeps the baseline prompt in force, and the stable prompt artifact
// is staged for removal so an earlier acceptance's prompt cannot outlive the
// run that stopped changing the instruction. Write-back stages the same
// effective profile as the next run's baseline: the instruction text over the
// prompt source, and the merged profile into baseline_profile.json beside it,
// keeping consecutive write-backs lossless.
func stageCandidateArtifacts(opts Options, inputs *resolvedInputs, result *Result, decision *GateDecision) ([]stagedFile, error) {
	var profile *promptiter.Profile
	for _, candidate := range result.Candidates {
		if candidate.Round == decision.SelectedRound {
			profile = candidate.Profile
			break
		}
	}
	if profile == nil {
		return nil, fmt.Errorf("selected round %d has no profile", decision.SelectedRound)
	}
	instructionSurfaceID, err := instructionTargetSurfaceID(opts.Config)
	if err != nil {
		return nil, err
	}
	rawPromptText := ""
	for _, override := range profile.Overrides {
		if override.SurfaceID == instructionSurfaceID && override.Value.Text != nil {
			rawPromptText = *override.Value.Text
			break
		}
	}
	// Every artifact carries the canonical (trimmed) instruction text, matching
	// how resolveInputs loads the baseline prompt, so the prompt file, the
	// effective profile, and a later promotion cannot disagree over surrounding
	// whitespace. An override that is *only* whitespace passes the optimizer
	// sanitizer but is not a publishable baseline: writing it would leave the
	// next run rejecting its own baseline prompt as empty (and -promote
	// rejecting the profile), so it fails closed here instead.
	promptText := strings.TrimSpace(rawPromptText)
	if rawPromptText != "" && promptText == "" {
		return nil, fmt.Errorf(
			"selected round %d overrides instruction surface %q with whitespace only; "+
				"refusing to publish a baseline the next run would reject as empty",
			decision.SelectedRound, instructionSurfaceID,
		)
	}
	effective := effectiveProfile(inputs, profile, instructionSurfaceID, promptText)
	profileContent, err := json.MarshalIndent(effective, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal candidate profile: %w", err)
	}
	profileFileContent := append(profileContent, '\n')
	result.CandidateProfilePath = filepath.Join(opts.OutputDir, candidateProfileFileName)
	staged := []stagedFile{{
		path:    result.CandidateProfilePath,
		content: profileFileContent,
	}}
	promptPath := filepath.Join(opts.OutputDir, candidatePromptFileName)
	if promptText != "" {
		result.CandidatePrompt = promptText
		result.CandidatePromptPath = promptPath
		staged = append(staged, stagedFile{
			path:    promptPath,
			content: []byte(promptText + "\n"),
		})
	} else {
		// Tool-only acceptance: the effective profile keeps the baseline
		// instruction, so this run publishes no prompt artifact. A
		// candidate_prompt.txt left in the same output dir by an earlier
		// instruction-changing acceptance would otherwise survive and let
		// consumers of the stable prompt path deploy text the latest accepted
		// run never evaluated. Clear it inside the same rollback unit.
		staged = append(staged, stagedFile{path: promptPath, remove: true})
	}
	if opts.WriteBack {
		if promptText != "" {
			staged = append(staged, stagedFile{
				path:    inputs.promptSourcePath,
				content: []byte(promptText + "\n"),
			})
		}
		staged = append(staged, stagedFile{
			path:    inputs.baselineProfilePath,
			content: profileFileContent,
		})
	}
	return staged, nil
}

// effectiveProfile overlays the accepted overrides onto the previously
// restored baseline profile, producing the complete profile that was actually
// in force when the candidate passed the gate. Restored baseline overrides
// are baked into the agent, so the engine no longer emits them as patches;
// without the merge, both candidate_profile.json and a written-back
// baseline_profile.json would silently drop inherited overrides such as an
// earlier accepted tool description. The instruction override is refreshed
// from the effective instruction text so the profile always stays consistent
// with the prompt artifacts.
func effectiveProfile(
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
	if err := validateProtectedCases(inputs.validationEvalSetPath, config); err != nil {
		return nil, err
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

// validateProtectedCases resolves gate.protectedCases against the validation
// eval set. A mistyped protected case ID would never match any delta, silently
// disabling the protection while the general regression allowance could still
// accept the candidate; unknown IDs therefore fail closed at input-resolution
// time.
func validateProtectedCases(validationEvalSetPath string, config *Config) error {
	if len(config.Gate.ProtectedCases) == 0 {
		return nil
	}
	content, err := os.ReadFile(validationEvalSetPath)
	if err != nil {
		return fmt.Errorf("read validation eval set %q: %w", validationEvalSetPath, err)
	}
	set := &evalset.EvalSet{}
	if err := json.Unmarshal(content, set); err != nil {
		return fmt.Errorf("decode validation eval set %q: %w", validationEvalSetPath, err)
	}
	known := make(map[string]struct{}, len(set.EvalCases))
	for _, evalCase := range set.EvalCases {
		if evalCase != nil {
			known[evalCase.EvalID] = struct{}{}
		}
	}
	unknown := make([]string, 0)
	for _, caseID := range config.Gate.ProtectedCases {
		if _, ok := known[caseID]; !ok {
			unknown = append(unknown, caseID)
		}
	}
	if len(unknown) > 0 {
		return fmt.Errorf(
			"gate.protectedCases contains case ID(s) not present in validation eval set %q: %s",
			config.EvalSets.Validation, strings.Join(unknown, ", "),
		)
	}
	return nil
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
