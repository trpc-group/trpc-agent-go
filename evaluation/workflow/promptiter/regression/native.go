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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	astructure "trpc.group/trpc-go/trpc-agent-go/agent/structure"
	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
	"trpc.group/trpc-go/trpc-agent-go/internal/profilecompiler"
)

// SnapshotEvaluator evaluates one profile against an authoritative dataset and
// returns a provenance-bound outer-loop snapshot.
type SnapshotEvaluator interface {
	Evaluate(ctx context.Context, request SnapshotRequest) (*EvaluationSnapshot, error)
}

// SnapshotRequest contains every input that participates in evaluation
// provenance. Dataset is always a complete outer-loop split.
type SnapshotRequest struct {
	EvaluationRunID     string
	Profile             *promptiter.Profile
	ExpectedProfileHash string
	Dataset             DatasetSpec
	Split               string
	Seed                int64
	EvaluatorConfigHash string
	MetricPolicyHash    string
	PrimaryMetric       string
	MetricDirections    map[string]ScoreDirection
	CriticalCaseIDs     []string
	HardFailureCaseIDs  []string
	EvidenceLimit       int
}

// ProfileEvaluatorConfig wires the native Evaluation implementation and the
// official source managers used to verify inventories and recover case evidence.
type ProfileEvaluatorConfig struct {
	AppName        string
	AgentEvaluator evaluation.AgentEvaluator
	EvalSetManager evalset.Manager
	MetricManager  metric.Manager
	Structure      *astructure.Snapshot
	ResourceMeter  ResourceMeter
}

// ProfileEvaluator adapts native Evaluation results into auditable snapshots.
type ProfileEvaluator struct {
	appName        string
	agentEvaluator evaluation.AgentEvaluator
	evalSetManager evalset.Manager
	metricManager  metric.Manager
	structure      *profilecompiler.Structure
	resourceMeter  ResourceMeter
}

// NewProfileEvaluator creates a native outer-loop evaluator.
func NewProfileEvaluator(config ProfileEvaluatorConfig) (*ProfileEvaluator, error) {
	switch {
	case strings.TrimSpace(config.AppName) == "":
		return nil, errors.New("app name is empty")
	case config.AgentEvaluator == nil:
		return nil, errors.New("agent evaluator is nil")
	case config.EvalSetManager == nil:
		return nil, errors.New("eval set manager is nil")
	case config.MetricManager == nil:
		return nil, errors.New("metric manager is nil")
	}
	structure, err := profilecompiler.NewStructure(config.Structure)
	if err != nil {
		return nil, fmt.Errorf("compile structure: %w", err)
	}
	return &ProfileEvaluator{
		appName:        config.AppName,
		agentEvaluator: config.AgentEvaluator,
		evalSetManager: config.EvalSetManager,
		metricManager:  config.MetricManager,
		structure:      structure,
		resourceMeter:  config.ResourceMeter,
	}, nil
}

// Evaluate executes native Evaluation with trace capture and profile run
// options, then verifies and adapts the result.
func (e *ProfileEvaluator) Evaluate(
	ctx context.Context,
	request SnapshotRequest,
) (*EvaluationSnapshot, error) {
	profileHash, err := ProfileFingerprint(request.Profile)
	if err != nil {
		return nil, fmt.Errorf("fingerprint profile: %w", err)
	}
	snapshot := newEvaluationSnapshot(request, profileHash)
	if err := validateSnapshotRequest(request, profileHash); err != nil {
		snapshot.Error = err.Error()
		return snapshot, err
	}
	sourceSet, metricIndex, err := e.loadAndVerifyInputs(ctx, request.Dataset)
	if err != nil {
		snapshot.Error = err.Error()
		return snapshot, err
	}
	runOptions, err := e.compileRunOptions(request.Profile)
	if err != nil {
		snapshot.Error = err.Error()
		return snapshot, fmt.Errorf("compile profile run options: %w", err)
	}
	before := snapshotMeter(e.resourceMeter)
	started := time.Now()
	result, evalErr := e.agentEvaluator.Evaluate(
		ctx,
		request.Dataset.EvalSetID,
		evaluation.WithRunDetailsEnabled(true),
		evaluation.WithNumRuns(1),
		evaluation.WithEvalCaseIDs(request.Dataset.CaseIDs...),
		evaluation.WithRunOptions(runOptions...),
	)
	elapsed := time.Since(started).Milliseconds()
	snapshot.LatencyMS = elapsed
	snapshot.Resources = measuredUsage(e.resourceMeter, before, elapsed)
	if evalErr != nil {
		snapshot.Status = EvaluationRunFailed
		snapshot.Error = evalErr.Error()
		return snapshot, fmt.Errorf("evaluate %s: %w", request.Split, evalErr)
	}
	if err := e.adaptResult(snapshot, request, sourceSet, metricIndex, result); err != nil {
		snapshot.Status = EvaluationNotEvaluable
		if errors.Is(err, errNativeEvaluationRunFailed) {
			snapshot.Status = EvaluationRunFailed
		}
		snapshot.Error = err.Error()
		return snapshot, err
	}
	snapshot.Status = EvaluationCompleted
	for _, evalCase := range snapshot.Cases {
		for _, metricResult := range evalCase.Metrics {
			if metricResult.Passed {
				continue
			}
			attribution := AttributeFailure(AttributionInput{
				Snapshot: snapshot,
				Case:     evalCase,
				Metric:   metricResult,
			})
			if len(attribution.Evidence) > request.EvidenceLimit {
				attribution.Evidence = attribution.Evidence[:request.EvidenceLimit]
			}
			snapshot.Attributions = append(snapshot.Attributions, attribution)
		}
	}
	return snapshot, nil
}

// ProfileFingerprint returns a stable fingerprint independent of override
// ordering.
func ProfileFingerprint(profile *promptiter.Profile) (string, error) {
	type canonicalProfile struct {
		StructureID string                       `json:"structureId"`
		Overrides   []promptiter.SurfaceOverride `json:"overrides"`
	}
	canonical := canonicalProfile{}
	if profile != nil {
		canonical.StructureID = profile.StructureID
		canonical.Overrides = append([]promptiter.SurfaceOverride(nil), profile.Overrides...)
		sort.SliceStable(canonical.Overrides, func(i, j int) bool {
			return canonical.Overrides[i].SurfaceID < canonical.Overrides[j].SurfaceID
		})
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:]), nil
}

func newEvaluationSnapshot(request SnapshotRequest, profileHash string) *EvaluationSnapshot {
	return &EvaluationSnapshot{
		Status: EvaluationNotEvaluable,
		Provenance: EvaluationProvenance{
			RunID:               request.EvaluationRunID,
			ProfileHash:         profileHash,
			EvalSetID:           request.Dataset.EvalSetID,
			EvalSetHash:         request.Dataset.EvalSetHash,
			MetricsHash:         request.Dataset.MetricsHash,
			Split:               request.Split,
			Seed:                request.Seed,
			EvaluatorConfigHash: request.EvaluatorConfigHash,
			MetricPolicyHash:    request.MetricPolicyHash,
		},
		Inventory: ExpectedInventory{
			CaseIDs:     append([]string(nil), request.Dataset.CaseIDs...),
			MetricNames: append([]string(nil), request.Dataset.MetricNames...),
		},
		Cases: []CaseResult{},
	}
}

//nolint:gocyclo // Request completeness checks intentionally report the exact invalid field.
func validateSnapshotRequest(request SnapshotRequest, profileHash string) error {
	switch {
	case strings.TrimSpace(request.EvaluationRunID) == "":
		return errors.New("evaluation run id is empty")
	case request.Profile == nil:
		return errors.New("profile is nil")
	case strings.TrimSpace(request.ExpectedProfileHash) == "":
		return errors.New("expected profile hash is empty")
	case strings.TrimSpace(request.Dataset.EvalSetID) == "":
		return errors.New("eval set id is empty")
	case strings.TrimSpace(request.Dataset.EvalSetHash) == "":
		return errors.New("eval set hash is empty")
	case strings.TrimSpace(request.Dataset.MetricsHash) == "":
		return errors.New("metrics hash is empty")
	case len(request.Dataset.CaseIDs) == 0:
		return errors.New("expected case inventory is empty")
	case len(request.Dataset.MetricNames) == 0:
		return errors.New("expected metric inventory is empty")
	case strings.TrimSpace(request.Split) == "":
		return errors.New("split is empty")
	case strings.TrimSpace(request.EvaluatorConfigHash) == "":
		return errors.New("evaluator config hash is empty")
	case strings.TrimSpace(request.MetricPolicyHash) == "":
		return errors.New("metric policy hash is empty")
	case request.EvidenceLimit <= 0:
		return errors.New("evidence limit must be greater than zero")
	case request.EvidenceLimit > 100:
		return errors.New("evidence limit must not exceed 100")
	case request.ExpectedProfileHash != profileHash:
		return fmt.Errorf(
			"expected profile hash %q does not match computed hash %q",
			request.ExpectedProfileHash,
			profileHash,
		)
	}
	if err := validateUniqueNonempty("case", request.Dataset.CaseIDs); err != nil {
		return err
	}
	if err := validateUniqueNonempty("metric", request.Dataset.MetricNames); err != nil {
		return err
	}
	if strings.TrimSpace(request.PrimaryMetric) == "" {
		return errors.New("primary metric is empty")
	}
	metricSet := stringSet(request.Dataset.MetricNames)
	if _, ok := metricSet[request.PrimaryMetric]; !ok {
		return fmt.Errorf("primary metric %q is not in metric inventory", request.PrimaryMetric)
	}
	for _, metricName := range request.Dataset.MetricNames {
		switch request.MetricDirections[metricName] {
		case ScoreHigherIsBetter, ScoreLowerIsBetter:
		default:
			return fmt.Errorf("metric %q has no valid score direction", metricName)
		}
	}
	if len(request.Dataset.NormalizedInputHashes) > 0 {
		for _, caseID := range request.Dataset.CaseIDs {
			if strings.TrimSpace(request.Dataset.NormalizedInputHashes[caseID]) == "" {
				return fmt.Errorf("normalized input hash for case %q is missing", caseID)
			}
		}
	}
	return nil
}

func validateUniqueNonempty(kind string, values []string) error {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s inventory contains an empty id", kind)
		}
		if _, ok := seen[value]; ok {
			return fmt.Errorf("%s inventory contains duplicate %q", kind, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func (e *ProfileEvaluator) loadAndVerifyInputs(
	ctx context.Context,
	dataset DatasetSpec,
) (*evalset.EvalSet, map[string]*metric.EvalMetric, error) {
	sourceSet, err := e.evalSetManager.Get(ctx, e.appName, dataset.EvalSetID)
	if err != nil {
		return nil, nil, fmt.Errorf("get eval set %q: %w", dataset.EvalSetID, err)
	}
	if sourceSet == nil {
		return nil, nil, fmt.Errorf("eval set %q is nil", dataset.EvalSetID)
	}
	if sourceSet.EvalSetID != "" && sourceSet.EvalSetID != dataset.EvalSetID {
		return nil, nil, fmt.Errorf(
			"source eval set id %q does not match %q",
			sourceSet.EvalSetID,
			dataset.EvalSetID,
		)
	}
	sourceCaseIDs := make([]string, 0, len(sourceSet.EvalCases))
	for _, evalCase := range sourceSet.EvalCases {
		if evalCase == nil {
			return nil, nil, fmt.Errorf("eval set %q contains a nil case", dataset.EvalSetID)
		}
		if evalCase.EvalMode == evalset.EvalModeTrace {
			return nil, nil, fmt.Errorf(
				"eval set %q case %q uses trace mode; prompt regression requires candidate execution",
				dataset.EvalSetID,
				evalCase.EvalID,
			)
		}
		if evalCase.ExpectedRunnerEnabled {
			return nil, nil, fmt.Errorf(
				"eval set %q case %q enables the expected runner; prompt regression requires fixed expected outputs",
				dataset.EvalSetID,
				evalCase.EvalID,
			)
		}
		sourceCaseIDs = append(sourceCaseIDs, evalCase.EvalID)
	}
	if err := verifyInventory("case", dataset.CaseIDs, sourceCaseIDs); err != nil {
		return nil, nil, err
	}
	metricNames, err := e.metricManager.List(ctx, e.appName, dataset.EvalSetID)
	if err != nil {
		return nil, nil, fmt.Errorf("list metrics for %q: %w", dataset.EvalSetID, err)
	}
	if err := verifyInventory("metric", dataset.MetricNames, metricNames); err != nil {
		return nil, nil, err
	}
	metricIndex := make(map[string]*metric.EvalMetric, len(dataset.MetricNames))
	for _, metricName := range dataset.MetricNames {
		metricConfig, err := e.metricManager.Get(ctx, e.appName, dataset.EvalSetID, metricName)
		if err != nil {
			return nil, nil, fmt.Errorf("get metric %q: %w", metricName, err)
		}
		if metricConfig == nil {
			return nil, nil, fmt.Errorf("metric %q is nil", metricName)
		}
		if metricConfig.MetricName != "" && metricConfig.MetricName != metricName {
			return nil, nil, fmt.Errorf(
				"metric manager returned %q for %q",
				metricConfig.MetricName,
				metricName,
			)
		}
		metricIndex[metricName] = metricConfig
	}
	return sourceSet, metricIndex, nil
}

func verifyInventory(kind string, expected, actual []string) error {
	if err := validateUniqueNonempty(kind, expected); err != nil {
		return err
	}
	if err := validateUniqueNonempty(kind, actual); err != nil {
		return fmt.Errorf("source %w", err)
	}
	expectedSet := make(map[string]struct{}, len(expected))
	for _, value := range expected {
		expectedSet[value] = struct{}{}
	}
	actualSet := make(map[string]struct{}, len(actual))
	for _, value := range actual {
		actualSet[value] = struct{}{}
	}
	for _, value := range expected {
		if _, ok := actualSet[value]; !ok {
			return fmt.Errorf("expected %s %q is missing from source inventory", kind, value)
		}
	}
	for _, value := range actual {
		if _, ok := expectedSet[value]; !ok {
			return fmt.Errorf("unexpected %s %q in source inventory", kind, value)
		}
	}
	return nil
}

func (e *ProfileEvaluator) compileRunOptions(
	profile *promptiter.Profile,
) ([]agent.RunOption, error) {
	compilerProfile := toCompilerProfile(profile)
	normalized, err := e.structure.NormalizeProfile(compilerProfile)
	if err != nil {
		return nil, err
	}
	runOptions, err := profilecompiler.CompileRunOptions(normalized, true)
	if err != nil {
		return nil, err
	}
	if len(normalized.Overrides) > 0 {
		runOptions = append(runOptions, profilecompiler.WithProfile(normalized))
	}
	return runOptions, nil
}

func toCompilerProfile(profile *promptiter.Profile) *profilecompiler.Profile {
	if profile == nil {
		return nil
	}
	converted := &profilecompiler.Profile{
		StructureID: profile.StructureID,
		Overrides:   make([]profilecompiler.SurfaceOverride, 0, len(profile.Overrides)),
	}
	for _, override := range profile.Overrides {
		converted.Overrides = append(converted.Overrides, profilecompiler.SurfaceOverride{
			SurfaceID: override.SurfaceID,
			Value:     override.Value,
		})
	}
	return converted
}

func (e *ProfileEvaluator) adaptResult(
	snapshot *EvaluationSnapshot,
	request SnapshotRequest,
	sourceSet *evalset.EvalSet,
	metricIndex map[string]*metric.EvalMetric,
	result *evaluation.EvaluationResult,
) error {
	if result == nil {
		return errors.New("native evaluation result is nil")
	}
	if result.AppName != "" && result.AppName != e.appName {
		return fmt.Errorf("evaluation app name %q does not match %q", result.AppName, e.appName)
	}
	if result.EvalSetID != request.Dataset.EvalSetID {
		return fmt.Errorf(
			"evaluation result eval set id %q does not match %q",
			result.EvalSetID,
			request.Dataset.EvalSetID,
		)
	}
	caseIndex := make(map[string]*evaluation.EvaluationCaseResult, len(result.EvalCases))
	resultCaseIDs := make([]string, 0, len(result.EvalCases))
	for _, evalCase := range result.EvalCases {
		if evalCase == nil {
			return errors.New("evaluation result contains a nil case")
		}
		if _, ok := caseIndex[evalCase.EvalCaseID]; ok {
			return fmt.Errorf("evaluation result contains duplicate case %q", evalCase.EvalCaseID)
		}
		caseIndex[evalCase.EvalCaseID] = evalCase
		resultCaseIDs = append(resultCaseIDs, evalCase.EvalCaseID)
	}
	if err := verifyInventory("result case", request.Dataset.CaseIDs, resultCaseIDs); err != nil {
		return err
	}
	sourceIndex := make(map[string]*evalset.EvalCase, len(sourceSet.EvalCases))
	for _, sourceCase := range sourceSet.EvalCases {
		sourceIndex[sourceCase.EvalID] = sourceCase
	}
	critical := stringSet(request.CriticalCaseIDs)
	hardFailure := stringSet(request.HardFailureCaseIDs)
	totalPrimaryScore := 0.0
	primaryResults := 0
	for _, caseID := range request.Dataset.CaseIDs {
		converted, err := adaptNativeCase(
			request,
			caseIndex[caseID],
			sourceIndex[caseID],
			metricIndex,
			critical,
			hardFailure,
		)
		if err != nil {
			return fmt.Errorf("adapt eval case %q: %w", caseID, err)
		}
		snapshot.Cases = append(snapshot.Cases, *converted)
		if converted.Passed {
			snapshot.Passed++
		} else {
			snapshot.Failed++
		}
		for _, resultMetric := range converted.Metrics {
			if resultMetric.MetricName != request.PrimaryMetric {
				continue
			}
			totalPrimaryScore += resultMetric.Score
			primaryResults++
		}
	}
	if primaryResults != len(request.Dataset.CaseIDs) {
		return fmt.Errorf(
			"primary metric %q has %d results for %d cases",
			request.PrimaryMetric,
			primaryResults,
			len(request.Dataset.CaseIDs),
		)
	}
	snapshot.OverallScore = totalPrimaryScore / float64(primaryResults)
	if math.IsNaN(snapshot.OverallScore) || math.IsInf(snapshot.OverallScore, 0) {
		return errors.New("evaluation overall score is not finite")
	}
	return nil
}

func adaptNativeCase(
	request SnapshotRequest,
	nativeCase *evaluation.EvaluationCaseResult,
	sourceCase *evalset.EvalCase,
	metricIndex map[string]*metric.EvalMetric,
	critical map[string]struct{},
	hardFailure map[string]struct{},
) (*CaseResult, error) {
	if nativeCase == nil {
		return nil, errors.New("native case is nil")
	}
	if sourceCase == nil {
		return nil, errors.New("source case is nil")
	}
	if message := nativeOperationalError(nativeCase); message != "" {
		return nil, fmt.Errorf(
			"%w: case %q: %s",
			errNativeEvaluationRunFailed,
			nativeCase.EvalCaseID,
			message,
		)
	}
	switch nativeCase.OverallStatus {
	case status.EvalStatusPassed, status.EvalStatusFailed:
	default:
		return nil, fmt.Errorf("case status %q is not evaluable", nativeCase.OverallStatus)
	}
	metricResults, err := adaptNativeMetrics(request, nativeCase, metricIndex)
	if err != nil {
		return nil, err
	}
	actual, expected := invocationEvidence(nativeCase, sourceCase)
	expectStructured, expectedRoute, expectedFacts := sourceExpectations(sourceCase)
	traceSteps, route := adaptTraceEvidence(nativeCase)
	caseResult := &CaseResult{
		EvalSetID:        request.Dataset.EvalSetID,
		CaseID:           nativeCase.EvalCaseID,
		Status:           string(nativeCase.OverallStatus),
		Passed:           nativeCase.OverallStatus == status.EvalStatusPassed,
		PrimaryMetric:    request.PrimaryMetric,
		Metrics:          metricResults,
		FinalResponse:    invocationResponse(lastInvocation(actual)),
		ExpectedResponse: invocationResponse(lastInvocation(expected)),
		ExpectStructured: expectStructured,
		ToolTrajectory:   invocationTools(actual),
		ExpectedTools:    invocationTools(expected),
		ExpectNoTools:    sourceExpectsNoTools(sourceCase),
		Route:            route,
		ExpectedRoute:    expectedRoute,
		ExpectedFacts:    expectedFacts,
		Trace:            traceSteps,
	}
	if caseResult.PrimaryMetric == "" && len(request.Dataset.MetricNames) > 0 {
		caseResult.PrimaryMetric = request.Dataset.MetricNames[0]
	}
	if expectStructured {
		caseResult.StructuredOutput = caseResult.FinalResponse
	}
	_, caseResult.Critical = critical[nativeCase.EvalCaseID]
	_, caseResult.HardFailure = hardFailure[nativeCase.EvalCaseID]
	if len(caseResult.ToolTrajectory) > request.EvidenceLimit {
		caseResult.ToolTrajectory = caseResult.ToolTrajectory[:request.EvidenceLimit]
	}
	if len(caseResult.ExpectedTools) > request.EvidenceLimit {
		caseResult.ExpectedTools = caseResult.ExpectedTools[:request.EvidenceLimit]
	}
	if len(caseResult.Trace) > request.EvidenceLimit {
		caseResult.Trace = caseResult.Trace[:request.EvidenceLimit]
	}
	return caseResult, nil
}

var errNativeEvaluationRunFailed = errors.New("native evaluation run failed")

func nativeOperationalError(nativeCase *evaluation.EvaluationCaseResult) string {
	for _, result := range nativeCase.EvalCaseResults {
		if result == nil {
			continue
		}
		if message := strings.TrimSpace(result.ErrorMessage); message != "" {
			return message
		}
	}
	for _, details := range nativeCase.RunDetails {
		if details == nil || details.Inference == nil {
			continue
		}
		if message := strings.TrimSpace(details.Inference.ErrorMessage); message != "" {
			return message
		}
	}
	return ""
}

func adaptNativeMetrics(
	request SnapshotRequest,
	nativeCase *evaluation.EvaluationCaseResult,
	metricIndex map[string]*metric.EvalMetric,
) ([]MetricResult, error) {
	nativeIndex := make(map[string]*evalresult.EvalMetricResult, len(nativeCase.MetricResults))
	for _, resultMetric := range nativeCase.MetricResults {
		if resultMetric == nil {
			return nil, errors.New("metric result is nil")
		}
		if _, ok := nativeIndex[resultMetric.MetricName]; ok {
			return nil, fmt.Errorf("duplicate metric result %q", resultMetric.MetricName)
		}
		nativeIndex[resultMetric.MetricName] = resultMetric
	}
	if len(nativeIndex) == 0 && len(nativeCase.EvalCaseResults) > 0 &&
		nativeCase.EvalCaseResults[0] != nil {
		for _, resultMetric := range nativeCase.EvalCaseResults[0].OverallEvalMetricResults {
			if resultMetric != nil {
				nativeIndex[resultMetric.MetricName] = resultMetric
			}
		}
	}
	actualNames := make([]string, 0, len(nativeIndex))
	for metricName := range nativeIndex {
		actualNames = append(actualNames, metricName)
	}
	if err := verifyInventory("result metric", request.Dataset.MetricNames, actualNames); err != nil {
		return nil, err
	}
	converted := make([]MetricResult, 0, len(request.Dataset.MetricNames))
	for _, metricName := range request.Dataset.MetricNames {
		nativeMetric := nativeIndex[metricName]
		switch nativeMetric.EvalStatus {
		case status.EvalStatusPassed, status.EvalStatusFailed:
		default:
			return nil, fmt.Errorf(
				"metric %q status %q is not evaluable",
				metricName,
				nativeMetric.EvalStatus,
			)
		}
		if math.IsNaN(nativeMetric.Score) || math.IsInf(nativeMetric.Score, 0) {
			return nil, fmt.Errorf("metric %q score is not finite", metricName)
		}
		if configured := metricIndex[metricName]; configured != nil &&
			math.Abs(configured.Threshold-nativeMetric.Threshold) > DefaultEpsilon {
			return nil, fmt.Errorf(
				"metric %q threshold %.6f does not match configured threshold %.6f",
				metricName,
				nativeMetric.Threshold,
				configured.Threshold,
			)
		}
		direction := request.MetricDirections[metricName]
		item := MetricResult{
			MetricName: metricName,
			Score:      nativeMetric.Score,
			Status:     string(nativeMetric.EvalStatus),
			Passed:     nativeMetric.EvalStatus == status.EvalStatusPassed,
			Threshold:  nativeMetric.Threshold,
			Direction:  direction,
		}
		if nativeMetric.Details != nil {
			item.Reason = nativeMetric.Details.Reason
			for _, rubric := range nativeMetric.Details.RubricScores {
				if rubric == nil {
					continue
				}
				item.RubricScores = append(item.RubricScores, RubricScore{
					ID:     rubric.ID,
					Reason: rubric.Reason,
					Score:  rubric.Score,
				})
			}
		}
		converted = append(converted, item)
	}
	return converted, nil
}

func invocationEvidence(
	nativeCase *evaluation.EvaluationCaseResult,
	sourceCase *evalset.EvalCase,
) ([]*evalset.Invocation, []*evalset.Invocation) {
	actual := make([]*evalset.Invocation, 0)
	expected := make([]*evalset.Invocation, 0)
	perInvocationActual := make([]*evalset.Invocation, 0)
	if len(nativeCase.EvalCaseResults) > 0 && nativeCase.EvalCaseResults[0] != nil {
		perInvocation := nativeCase.EvalCaseResults[0].EvalMetricResultPerInvocation
		for _, invocationResult := range perInvocation {
			if invocationResult == nil {
				continue
			}
			if invocationResult.ActualInvocation != nil {
				perInvocationActual = append(perInvocationActual, invocationResult.ActualInvocation)
			}
			if invocationResult.ExpectedInvocation != nil {
				expected = append(expected, invocationResult.ExpectedInvocation)
			}
		}
	}
	for _, detail := range nativeCase.RunDetails {
		if detail == nil || detail.Inference == nil {
			continue
		}
		for _, invocation := range detail.Inference.Inferences {
			if invocation != nil {
				actual = append(actual, invocation)
			}
		}
	}
	if len(actual) == 0 {
		actual = perInvocationActual
	}
	if len(expected) == 0 {
		for _, invocation := range sourceCase.Conversation {
			if invocation != nil {
				expected = append(expected, invocation)
			}
		}
	}
	if len(actual) == 0 {
		for _, invocation := range sourceCase.ActualConversation {
			if invocation != nil {
				actual = append(actual, invocation)
			}
		}
	}
	return actual, expected
}

func invocationResponse(invocation *evalset.Invocation) string {
	if invocation == nil || invocation.FinalResponse == nil {
		return ""
	}
	return invocation.FinalResponse.Content
}

func lastInvocation(invocations []*evalset.Invocation) *evalset.Invocation {
	for index := len(invocations) - 1; index >= 0; index-- {
		if invocations[index] != nil {
			return invocations[index]
		}
	}
	return nil
}

func invocationTools(invocations []*evalset.Invocation) []ToolCall {
	tools := make([]ToolCall, 0)
	for _, invocation := range invocations {
		if invocation == nil {
			continue
		}
		for _, item := range invocation.Tools {
			if item == nil {
				continue
			}
			tools = append(tools, ToolCall{
				Sequence:  len(tools) + 1,
				Name:      item.Name,
				Arguments: item.Arguments,
				Result:    item.Result,
			})
		}
	}
	return tools
}

func sourceExpectsNoTools(sourceCase *evalset.EvalCase) bool {
	if sourceCase == nil {
		return false
	}
	hasInvocation := false
	for _, invocation := range sourceCase.Conversation {
		if invocation == nil {
			continue
		}
		hasInvocation = true
		if invocation.Tools == nil || len(invocation.Tools) > 0 {
			return false
		}
	}
	return hasInvocation
}

func sourceExpectations(sourceCase *evalset.EvalCase) (bool, string, []string) {
	expectStructured := false
	expectedRoute := ""
	expectedFacts := make([]string, 0)
	for _, rubric := range sourceCase.Rubrics {
		if rubric == nil {
			continue
		}
		value := rubric.Description
		if rubric.Content != nil && strings.TrimSpace(rubric.Content.Text) != "" {
			value = rubric.Content.Text
		}
		value = strings.TrimSpace(value)
		switch strings.ToLower(strings.TrimSpace(rubric.Type)) {
		case "expected_route":
			expectedRoute = value
		case "expected_fact":
			if value != "" {
				expectedFacts = append(expectedFacts, value)
			}
		case "structured_output":
			expectStructured = true
		}
	}
	return expectStructured, expectedRoute, expectedFacts
}

func adaptTraceEvidence(nativeCase *evaluation.EvaluationCaseResult) ([]TraceStep, string) {
	steps := make([]TraceStep, 0)
	route := ""
	for _, detail := range nativeCase.RunDetails {
		if detail == nil || detail.Inference == nil {
			continue
		}
		for _, executionTrace := range detail.Inference.ExecutionTraces {
			if executionTrace == nil {
				continue
			}
			for _, step := range executionTrace.Steps {
				converted := convertTraceStep(step)
				steps = append(steps, converted)
				if strings.TrimSpace(step.Branch) != "" {
					route = routeLeaf(step.Branch)
				}
			}
		}
	}
	return steps, route
}

func convertTraceStep(step atrace.Step) TraceStep {
	converted := TraceStep{
		StepID:             step.StepID,
		NodeID:             step.NodeID,
		AgentName:          step.AgentName,
		Branch:             step.Branch,
		PredecessorStepIDs: append([]string(nil), step.PredecessorStepIDs...),
		AppliedSurfaceIDs:  append([]string(nil), step.AppliedSurfaceIDs...),
		Error:              step.Error,
	}
	if step.Input != nil {
		converted.Input = step.Input.Text
	}
	if step.Output != nil {
		converted.Output = step.Output.Text
	}
	return converted
}

func routeLeaf(branch string) string {
	replacer := strings.NewReplacer("->", "/", ":", "/", ".", "/")
	parts := strings.Split(replacer.Replace(branch), "/")
	for index := len(parts) - 1; index >= 0; index-- {
		if value := strings.TrimSpace(parts[index]); value != "" {
			return value
		}
	}
	return strings.TrimSpace(branch)
}

func stringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, value := range values {
		set[value] = struct{}{}
	}
	return set
}

func snapshotMeter(meter ResourceMeter) ResourceUsage {
	if meter == nil {
		return ResourceUsage{}
	}
	return meter.Snapshot()
}

func measuredUsage(meter ResourceMeter, before ResourceUsage, latencyMS int64) ResourceUsage {
	if meter == nil {
		return ResourceUsage{LatencyMS: Count{Available: true, Value: latencyMS}}
	}
	usage := resourceUsageDelta(before, meter.Snapshot())
	if !usage.LatencyMS.Available {
		usage.LatencyMS = Count{Available: true, Value: latencyMS}
	}
	return usage
}
