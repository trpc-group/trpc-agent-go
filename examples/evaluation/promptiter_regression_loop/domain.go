//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"unicode"

	atrace "trpc.group/trpc-go/trpc-agent-go/agent/trace"
	"trpc.group/trpc-go/trpc-agent-go/evaluation"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	promptiterengine "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter/engine"
)

const reportSchemaVersion = "trpc-agent-go.promptiter-regression.report/v1alpha1"

type snapshotIdentity struct {
	EvaluationRunID string `json:"evaluationRunId"`
	Split           string `json:"split"`
	EvalSetID       string `json:"evalSetId"`
	DatasetHash     string `json:"datasetHash"`
	MetricsHash     string `json:"metricsHash"`
	ProfileHash     string `json:"profileHash"`
	Seed            int64  `json:"seed"`
}

type evaluationSnapshot struct {
	Identity     snapshotIdentity `json:"identity"`
	OverallScore float64          `json:"overallScore"`
	Cases        []caseEvidence   `json:"cases"`
}

type caseEvidence struct {
	CaseID        string               `json:"caseId"`
	RunID         int                  `json:"runId"`
	Status        string               `json:"status"`
	ErrorMessage  string               `json:"errorMessage,omitempty"`
	FinalResponse string               `json:"finalResponse,omitempty"`
	Metrics       []metricEvidence     `json:"metrics"`
	Invocations   []invocationEvidence `json:"invocations,omitempty"`
	Trace         traceEvidence        `json:"trace"`
}

type metricEvidence struct {
	MetricName string  `json:"metricName"`
	Score      float64 `json:"score"`
	Threshold  float64 `json:"threshold"`
	Status     string  `json:"status"`
	Reason     string  `json:"reason,omitempty"`
}

type invocationEvidence struct {
	Index                 int            `json:"index"`
	FinalResponse         string         `json:"finalResponse,omitempty"`
	ExpectedFinalResponse string         `json:"expectedFinalResponse,omitempty"`
	ActualTools           []toolEvidence `json:"actualTools,omitempty"`
	ExpectedTools         []toolEvidence `json:"expectedTools,omitempty"`
}

type toolEvidence struct {
	Name          string `json:"name"`
	ArgumentsHash string `json:"argumentsHash,omitempty"`
	ResultHash    string `json:"resultHash,omitempty"`
}

type traceEvidence struct {
	Status    string              `json:"status,omitempty"`
	StepCount int                 `json:"stepCount"`
	Steps     []traceStepEvidence `json:"steps,omitempty"`
}

type traceStepEvidence struct {
	StepID     string `json:"stepId"`
	NodeID     string `json:"nodeId"`
	NodeType   string `json:"nodeType"`
	Branch     string `json:"branch,omitempty"`
	Error      string `json:"error,omitempty"`
	InputHash  string `json:"inputHash,omitempty"`
	OutputHash string `json:"outputHash,omitempty"`
}

type attribution struct {
	CaseID          string           `json:"caseId"`
	MetricName      string           `json:"metricName"`
	PrimaryCategory string           `json:"primaryCategory"`
	Reason          string           `json:"reason"`
	RuleID          string           `json:"ruleId"`
	Confidence      float64          `json:"confidence"`
	EvidenceRefs    []string         `json:"evidenceRefs"`
	Snapshot        snapshotIdentity `json:"snapshotIdentity"`
}

type metricDelta struct {
	CaseID          string   `json:"caseId"`
	MetricName      string   `json:"metricName"`
	BaselineScore   *float64 `json:"baselineScore"`
	CandidateScore  *float64 `json:"candidateScore"`
	BaselineStatus  string   `json:"baselineStatus,omitempty"`
	CandidateStatus string   `json:"candidateStatus,omitempty"`
	ScoreDelta      *float64 `json:"scoreDelta"`
	Transition      string   `json:"transition"`
}

type comparison struct {
	BaselineScore  float64       `json:"baselineScore"`
	CandidateScore float64       `json:"candidateScore"`
	OverallDelta   float64       `json:"overallDelta"`
	Metrics        []metricDelta `json:"metrics"`
}

type gateCheck struct {
	Name      string `json:"name"`
	Passed    bool   `json:"passed"`
	Observed  any    `json:"observed,omitempty"`
	Threshold any    `json:"threshold,omitempty"`
}

type gateDecision struct {
	Accepted    bool        `json:"accepted"`
	Deployable  bool        `json:"deployable"`
	ReasonCodes []string    `json:"reasonCodes"`
	Summary     string      `json:"summary"`
	Checks      []gateCheck `json:"checks"`
}

func buildSnapshot(
	result *evaluation.EvaluationResult,
	split string,
	datasetHash string,
	metricsHash string,
	profileHash string,
	seed int64,
) (*evaluationSnapshot, error) {
	if result == nil {
		return nil, errors.New("evaluation result is nil")
	}
	if split != "train" && split != "validation" {
		return nil, fmt.Errorf("invalid split %q", split)
	}
	snapshot := &evaluationSnapshot{
		Identity: snapshotIdentity{
			Split:       split,
			EvalSetID:   result.EvalSetID,
			DatasetHash: datasetHash,
			MetricsHash: metricsHash,
			ProfileHash: profileHash,
			Seed:        seed,
		},
		Cases: make([]caseEvidence, 0, len(result.EvalCases)),
	}
	if result.EvalResult != nil {
		snapshot.Identity.EvaluationRunID = result.EvalResult.EvalSetResultID
	}
	totalScore := 0.0
	metricCount := 0
	seen := make(map[string]struct{})
	for _, evalCase := range result.EvalCases {
		if evalCase == nil {
			continue
		}
		if strings.TrimSpace(evalCase.EvalCaseID) == "" {
			return nil, errors.New("evaluation case id is empty")
		}
		if len(evalCase.EvalCaseResults) == 0 || evalCase.EvalCaseResults[0] == nil {
			return nil, fmt.Errorf("case %q has no run result", evalCase.EvalCaseID)
		}
		runResult := evalCase.EvalCaseResults[0]
		runMetricReasons := make(map[string]string, len(runResult.OverallEvalMetricResults))
		for _, runMetric := range runResult.OverallEvalMetricResults {
			if runMetric != nil && runMetric.Details != nil {
				runMetricReasons[runMetric.MetricName] = runMetric.Details.Reason
			}
		}
		current := caseEvidence{
			CaseID:       evalCase.EvalCaseID,
			RunID:        runResult.RunID,
			Status:       string(evalCase.OverallStatus),
			ErrorMessage: runResult.ErrorMessage,
			Metrics:      make([]metricEvidence, 0, len(evalCase.MetricResults)),
		}
		for _, metricResult := range evalCase.MetricResults {
			if metricResult == nil {
				continue
			}
			metricName := strings.TrimSpace(metricResult.MetricName)
			if metricName == "" {
				return nil, fmt.Errorf("case %q has an empty metric name", evalCase.EvalCaseID)
			}
			if math.IsNaN(metricResult.Score) || math.IsInf(metricResult.Score, 0) {
				return nil, fmt.Errorf("case %q metric %q has a non-finite score", evalCase.EvalCaseID, metricName)
			}
			key := evalCase.EvalCaseID + "\x00" + metricName
			if _, ok := seen[key]; ok {
				return nil, fmt.Errorf("duplicate case/metric %q/%q", evalCase.EvalCaseID, metricName)
			}
			seen[key] = struct{}{}
			reason := ""
			if metricResult.Details != nil {
				reason = metricResult.Details.Reason
			}
			if reason == "" {
				reason = runMetricReasons[metricName]
			}
			current.Metrics = append(current.Metrics, metricEvidence{
				MetricName: metricName,
				Score:      metricResult.Score,
				Threshold:  metricResult.Threshold,
				Status:     string(metricResult.EvalStatus),
				Reason:     reason,
			})
			if metricResult.EvalStatus != status.EvalStatusNotEvaluated {
				totalScore += metricResult.Score
				metricCount++
			}
		}
		populateInvocationAndTraceEvidence(&current, evalCase)
		sort.Slice(current.Metrics, func(i, j int) bool {
			return current.Metrics[i].MetricName < current.Metrics[j].MetricName
		})
		snapshot.Cases = append(snapshot.Cases, current)
	}
	if metricCount == 0 {
		return nil, errors.New("evaluation result has no metric scores")
	}
	snapshot.OverallScore = totalScore / float64(metricCount)
	sort.Slice(snapshot.Cases, func(i, j int) bool {
		return snapshot.Cases[i].CaseID < snapshot.Cases[j].CaseID
	})
	return snapshot, nil
}

func populateInvocationAndTraceEvidence(
	current *caseEvidence,
	evalCase *evaluation.EvaluationCaseResult,
) {
	if current == nil || evalCase == nil || len(evalCase.RunDetails) == 0 || evalCase.RunDetails[0] == nil {
		return
	}
	inference := evalCase.RunDetails[0].Inference
	if inference == nil {
		return
	}
	current.Invocations = make([]invocationEvidence, 0, len(inference.Inferences))
	for index, invocation := range inference.Inferences {
		if invocation == nil {
			continue
		}
		item := invocationEvidence{Index: index}
		if invocation.FinalResponse != nil {
			item.FinalResponse = invocation.FinalResponse.Content
			current.FinalResponse = invocation.FinalResponse.Content
		}
		item.ActualTools = toolEvidenceFromInvocation(invocation)
		current.Invocations = append(current.Invocations, item)
	}
	for _, trace := range inference.ExecutionTraces {
		if trace == nil {
			continue
		}
		current.Trace.Status = string(trace.Status)
		current.Trace.StepCount += len(trace.Steps)
		for _, step := range trace.Steps {
			current.Trace.Steps = append(current.Trace.Steps, traceStepEvidence{
				StepID:     step.StepID,
				NodeID:     step.NodeID,
				NodeType:   step.NodeType,
				Branch:     step.Branch,
				Error:      step.Error,
				InputHash:  traceSnapshotHash(step.Input),
				OutputHash: traceSnapshotHash(step.Output),
			})
		}
	}
	if len(evalCase.EvalCaseResults) == 0 || evalCase.EvalCaseResults[0] == nil {
		return
	}
	perInvocation := evalCase.EvalCaseResults[0].EvalMetricResultPerInvocation
	for _, metricInvocation := range perInvocation {
		if metricInvocation == nil || metricInvocation.ExpectedInvocation == nil {
			continue
		}
		index := invocationIndex(metricInvocation.ActualInvocation, current.Invocations)
		if index >= 0 {
			if metricInvocation.ExpectedInvocation.FinalResponse != nil {
				current.Invocations[index].ExpectedFinalResponse = metricInvocation.ExpectedInvocation.FinalResponse.Content
			}
			if len(current.Invocations[index].ExpectedTools) == 0 {
				current.Invocations[index].ExpectedTools = toolEvidenceFromInvocation(metricInvocation.ExpectedInvocation)
			}
		}
	}
}

func invocationIndex(actual *evalset.Invocation, invocations []invocationEvidence) int {
	if actual == nil {
		return -1
	}
	for index, invocation := range invocations {
		if actual.FinalResponse != nil && invocation.FinalResponse == actual.FinalResponse.Content {
			return index
		}
	}
	if len(invocations) == 1 {
		return 0
	}
	return -1
}

func toolEvidenceFromInvocation(invocation *evalset.Invocation) []toolEvidence {
	if invocation == nil {
		return nil
	}
	tools := make([]toolEvidence, 0, len(invocation.Tools))
	for _, current := range invocation.Tools {
		if current == nil {
			continue
		}
		argumentJSON, _ := json.Marshal(current.Arguments)
		resultJSON, _ := json.Marshal(current.Result)
		tools = append(tools, toolEvidence{
			Name:          current.Name,
			ArgumentsHash: hashBytes(argumentJSON),
			ResultHash:    hashBytes(resultJSON),
		})
	}
	return tools
}

func attributeFailures(snapshot *evaluationSnapshot) ([]attribution, error) {
	if snapshot == nil {
		return nil, errors.New("snapshot is nil")
	}
	result := make([]attribution, 0)
	for _, evalCase := range snapshot.Cases {
		for _, metric := range evalCase.Metrics {
			if metric.Status != string(status.EvalStatusFailed) {
				continue
			}
			category, reason, ruleID, confidence := classifyFailure(evalCase, metric)
			result = append(result, attribution{
				CaseID:          evalCase.CaseID,
				MetricName:      metric.MetricName,
				PrimaryCategory: category,
				Reason:          reason,
				RuleID:          ruleID,
				Confidence:      confidence,
				EvidenceRefs: []string{
					fmt.Sprintf("evidence://%s/%s/run-%d/%s", snapshot.Identity.Split, evalCase.CaseID, evalCase.RunID, metric.MetricName),
				},
				Snapshot: snapshot.Identity,
			})
		}
	}
	return result, nil
}

func classifyFailure(evalCase caseEvidence, metric metricEvidence) (string, string, string, float64) {
	if evalCase.ErrorMessage != "" || evalCase.Status == string(status.EvalStatusNotEvaluated) {
		return "execution_error", "评测执行失败：" + firstNonEmpty(evalCase.ErrorMessage, metric.Reason), "ATTR-EXEC-001", 1
	}
	lowerReason := strings.ToLower(metric.Reason)
	if traceShowsExecutionFailure(evalCase.Trace) || reasonShowsExecutionFailure(lowerReason) {
		return "execution_error", "Trace 或 evaluator reason 表明执行失败：" + firstNonEmpty(metric.Reason, firstTraceError(evalCase.Trace)), "ATTR-EXEC-001", 0.95
	}
	if traceShowsRouteFailure(evalCase.Trace) || containsAny(lowerReason, "route", "router", "wrong branch", "路由", "分支错误") {
		return "route_error", "Trace 或 evaluator reason 表明请求进入了错误路由", "ATTR-ROUTE-001", 0.9
	}
	if hasToolSelectionMismatch(evalCase.Invocations) {
		return "tool_selection_error", "实际工具序列与期望工具序列不一致", "ATTR-TOOL-SELECT-001", 1
	}
	if hasToolArgumentMismatch(evalCase.Invocations) {
		return "tool_argument_error", "工具名称一致，但参数证据不一致", "ATTR-TOOL-ARG-001", 1
	}
	if containsAny(lowerReason, "json", "xml", "format", "schema", "parse", "格式", "结构化", "解析失败") {
		return "output_format_error", "结构化输出不满足格式要求：" + metric.Reason, "ATTR-FORMAT-001", 0.95
	}
	if containsAny(lowerReason, "retrieval", "knowledge", "recall", "grounding", "召回", "检索", "知识", "证据不足") {
		return "knowledge_retrieval_insufficient", "Evaluator reason 提供了知识召回不足证据：" + metric.Reason, "ATTR-RETRIEVAL-001", 0.85
	}
	if hasFinalResponseMismatch(evalCase.Invocations) {
		return "final_response_mismatch", "实际最终回复与期望最终回复不一致", "ATTR-RESPONSE-001", 1
	}
	if containsAny(lowerReason, "final response", "text mismatch", "wrong answer", "reference answer", "最终回复", "答案错误", "回复不匹配") {
		return "final_response_mismatch", "最终回复与期望结果不匹配：" + metric.Reason, "ATTR-RESPONSE-001", 0.95
	}
	return "unclassified_failure", "评测确认失败，但现有证据不足以安全细分：" + firstNonEmpty(metric.Reason, "无 evaluator reason"), "ATTR-UNKNOWN-001", 0.5
}

func hasToolSelectionMismatch(invocations []invocationEvidence) bool {
	for _, invocation := range invocations {
		if len(invocation.ActualTools) != len(invocation.ExpectedTools) {
			return len(invocation.ActualTools) > 0 || len(invocation.ExpectedTools) > 0
		}
		for index := range invocation.ActualTools {
			if invocation.ActualTools[index].Name != invocation.ExpectedTools[index].Name {
				return true
			}
		}
	}
	return false
}

func hasToolArgumentMismatch(invocations []invocationEvidence) bool {
	for _, invocation := range invocations {
		if len(invocation.ActualTools) != len(invocation.ExpectedTools) {
			continue
		}
		for index := range invocation.ActualTools {
			if invocation.ActualTools[index].Name == invocation.ExpectedTools[index].Name &&
				invocation.ActualTools[index].ArgumentsHash != invocation.ExpectedTools[index].ArgumentsHash {
				return true
			}
		}
	}
	return false
}

func hasFinalResponseMismatch(invocations []invocationEvidence) bool {
	for _, invocation := range invocations {
		if invocation.ExpectedFinalResponse != "" && invocation.FinalResponse != invocation.ExpectedFinalResponse {
			return true
		}
	}
	return false
}

func traceShowsRouteFailure(trace traceEvidence) bool {
	for _, step := range trace.Steps {
		lower := strings.ToLower(step.Error)
		if containsAny(lower, "route", "router", "wrong branch", "路由", "分支错误") {
			return true
		}
	}
	return false
}

func traceShowsExecutionFailure(trace traceEvidence) bool {
	for _, step := range trace.Steps {
		lower := strings.ToLower(step.Error)
		if lower == "" || containsAny(lower, "route", "router", "wrong branch", "路由", "分支错误") {
			continue
		}
		if containsAny(lower, "timeout", "timed out", "failed", "failure", "panic", "canceled", "cancelled", "unavailable", "执行失败", "超时", "取消") {
			return true
		}
	}
	return false
}

func reasonShowsExecutionFailure(lowerReason string) bool {
	if containsAny(lowerReason, "route", "router", "wrong branch", "路由", "分支错误") {
		return false
	}
	return containsAny(lowerReason, "execution failed", "tool failed", "model timeout", "runner canceled", "evaluation crashed", "服务不可用", "执行失败", "工具失败", "模型超时", "运行取消")
}

func firstTraceError(trace traceEvidence) string {
	for _, step := range trace.Steps {
		if strings.TrimSpace(step.Error) != "" {
			return step.Error
		}
	}
	return ""
}

func traceSnapshotHash(snapshot *atrace.Snapshot) string {
	if snapshot == nil {
		return ""
	}
	return hashText(snapshot.Text)
}

func lossHintsFromAttributions(items []attribution) ([]promptiterengine.LossHint, error) {
	hints := make([]promptiterengine.LossHint, 0, len(items))
	for _, item := range items {
		if strings.TrimSpace(item.CaseID) == "" || strings.TrimSpace(item.MetricName) == "" || strings.TrimSpace(item.Reason) == "" {
			return nil, errors.New("attribution cannot be converted to a loss hint")
		}
		hints = append(hints, promptiterengine.LossHint{
			EvalCaseID: item.CaseID,
			MetricName: item.MetricName,
			Reason:     item.Reason,
		})
	}
	return hints, nil
}

func compareSnapshots(baseline, candidate *evaluationSnapshot) (*comparison, error) {
	if baseline == nil || candidate == nil {
		return nil, errors.New("baseline and candidate snapshots are required")
	}
	result := &comparison{
		BaselineScore:  baseline.OverallScore,
		CandidateScore: candidate.OverallScore,
		OverallDelta:   candidate.OverallScore - baseline.OverallScore,
	}
	baselineIndex := metricIndex(baseline)
	candidateIndex := metricIndex(candidate)
	keys := make([]string, 0, len(baselineIndex)+len(candidateIndex))
	seen := make(map[string]struct{})
	for key := range baselineIndex {
		keys = append(keys, key)
		seen[key] = struct{}{}
	}
	for key := range candidateIndex {
		if _, ok := seen[key]; !ok {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		baselineMetric, baselineOK := baselineIndex[key]
		candidateMetric, candidateOK := candidateIndex[key]
		caseID, metricName, _ := strings.Cut(key, "\x00")
		delta := metricDelta{CaseID: caseID, MetricName: metricName}
		switch {
		case !candidateOK:
			delta.BaselineScore = float64Ptr(baselineMetric.Score)
			delta.BaselineStatus = baselineMetric.Status
			delta.Transition = "missing_in_candidate"
		case !baselineOK:
			delta.CandidateScore = float64Ptr(candidateMetric.Score)
			delta.CandidateStatus = candidateMetric.Status
			delta.Transition = "unexpected_in_candidate"
		default:
			difference := candidateMetric.Score - baselineMetric.Score
			delta.BaselineScore = float64Ptr(baselineMetric.Score)
			delta.CandidateScore = float64Ptr(candidateMetric.Score)
			delta.ScoreDelta = float64Ptr(difference)
			delta.BaselineStatus = baselineMetric.Status
			delta.CandidateStatus = candidateMetric.Status
			delta.Transition = deltaTransition(baselineMetric, candidateMetric, difference)
		}
		result.Metrics = append(result.Metrics, delta)
	}
	return result, nil
}

func metricIndex(snapshot *evaluationSnapshot) map[string]metricEvidence {
	index := make(map[string]metricEvidence)
	if snapshot == nil {
		return index
	}
	for _, evalCase := range snapshot.Cases {
		for _, metric := range evalCase.Metrics {
			index[evalCase.CaseID+"\x00"+metric.MetricName] = metric
		}
	}
	return index
}

func deltaTransition(baseline, candidate metricEvidence, difference float64) string {
	if baseline.Status == string(status.EvalStatusFailed) && candidate.Status == string(status.EvalStatusPassed) {
		return "newly_passed"
	}
	if baseline.Status == string(status.EvalStatusPassed) && candidate.Status == string(status.EvalStatusFailed) {
		return "newly_failed"
	}
	if difference > 1e-9 {
		return "improved"
	}
	if difference < -1e-9 {
		return "regressed"
	}
	return "unchanged"
}

func evaluateGate(
	cfg gateConfig,
	budget budgetConfig,
	baselineTrain, candidateTrain *evaluationSnapshot,
	baselineValidation, candidateValidation *evaluationSnapshot,
	trainDelta, validationDelta *comparison,
	accounting accountingSummary,
) gateDecision {
	decision := gateDecision{Checks: make([]gateCheck, 0, 9)}
	reasons := make(map[string]struct{})
	addCheck := func(name string, passed bool, observed, threshold any, reason string) {
		decision.Checks = append(decision.Checks, gateCheck{Name: name, Passed: passed, Observed: observed, Threshold: threshold})
		if !passed && reason != "" {
			reasons[reason] = struct{}{}
		}
	}

	provenanceOK := validGateProvenance(baselineTrain, candidateTrain, baselineValidation, candidateValidation)
	addCheck("provenance", provenanceOK, gateIdentitySummary(baselineTrain, candidateTrain, baselineValidation, candidateValidation), "fresh, matching run/dataset/metrics/profile identities", "PROVENANCE_MISMATCH")

	matrixValid := validSnapshotMatrix(baselineTrain) && validSnapshotMatrix(candidateTrain) &&
		validSnapshotMatrix(baselineValidation) && validSnapshotMatrix(candidateValidation)
	addCheck("case_metric_identity", matrixValid, matrixValid, true, "DUPLICATE_CASE_METRIC")
	finiteScores := finiteSnapshotScores(baselineTrain) && finiteSnapshotScores(candidateTrain) &&
		finiteSnapshotScores(baselineValidation) && finiteSnapshotScores(candidateValidation) &&
		finiteComparisonScores(trainDelta) && finiteComparisonScores(validationDelta)
	addCheck("finite_scores", finiteScores, finiteScores, true, "NON_FINITE_SCORE")
	scoreConsistency := snapshotScoreConsistent(baselineTrain) && snapshotScoreConsistent(candidateTrain) &&
		snapshotScoreConsistent(baselineValidation) && snapshotScoreConsistent(candidateValidation)
	addCheck("snapshot_score_consistency", scoreConsistency, scoreConsistency, true, "INCONSISTENT_SNAPSHOT_SCORE")
	evaluationStatuses := validEvaluationStatuses(baselineTrain) && validEvaluationStatuses(candidateTrain) &&
		validEvaluationStatuses(baselineValidation) && validEvaluationStatuses(candidateValidation)
	addCheck("evaluated_case_metrics", evaluationStatuses, evaluationStatuses, true, "UNEVALUATED_CASE_METRIC")
	deltaConsistency := comparisonsMatchSnapshots(baselineTrain, candidateTrain, trainDelta) &&
		comparisonsMatchSnapshots(baselineValidation, candidateValidation, validationDelta)
	addCheck("delta_consistency", deltaConsistency, deltaConsistency, true, "DELTA_SNAPSHOT_MISMATCH")

	complete := true
	unexpected := false
	if validationDelta == nil || trainDelta == nil {
		complete = false
	} else {
		for _, delta := range append(append([]metricDelta(nil), validationDelta.Metrics...), trainDelta.Metrics...) {
			if delta.Transition == "missing_in_candidate" {
				complete = false
			}
			if delta.Transition == "unexpected_in_candidate" {
				unexpected = true
			}
		}
	}
	// Missing or unexpected Case/Metric keys are always fail-closed. The config
	// fields remain in v1alpha1 for compatibility, but cannot weaken this safety invariant.
	addCheck("complete_case_metric_matrix", complete, complete, true, "CANDIDATE_RESULT_INCOMPLETE")
	addCheck("unexpected_case_metric", !unexpected, unexpected, false, "UNEXPECTED_CASE_METRIC")

	validationGain := 0.0
	trainGain := 0.0
	if validationDelta != nil {
		validationGain = validationDelta.OverallDelta
	}
	if trainDelta != nil {
		trainGain = trainDelta.OverallDelta
	}
	addCheck("validation_gain", validationGain+1e-9 >= cfg.MinValidationGain, validationGain, cfg.MinValidationGain, "VALIDATION_GAIN_BELOW_THRESHOLD")

	hardFailure := false
	hardMetrics := stringSet(cfg.HardMetrics)
	criticalRegression := false
	criticalCases := stringSet(cfg.CriticalCases)
	metricRegression := false
	if validationDelta != nil {
		for _, delta := range validationDelta.Metrics {
			if _, ok := hardMetrics[delta.MetricName]; ok && delta.Transition == "newly_failed" {
				hardFailure = true
			}
			if _, ok := criticalCases[delta.CaseID]; ok && (delta.Transition == "newly_failed" || delta.Transition == "regressed" || delta.Transition == "missing_in_candidate") {
				criticalRegression = true
			}
			if delta.ScoreDelta != nil && *delta.ScoreDelta < -cfg.MaxMetricRegression-1e-9 {
				metricRegression = true
			}
		}
	}
	addCheck("hard_fail", !hardFailure, hardFailure, false, "NEW_HARD_FAIL")
	addCheck("critical_cases", !criticalRegression, criticalRegression, false, "CRITICAL_CASE_REGRESSION")
	addCheck("metric_regression", !metricRegression, metricRegression, cfg.MaxMetricRegression, "METRIC_REGRESSION_EXCEEDED")

	overfit := trainGain > 1e-9 && validationGain < -1e-9
	generalizationGap := trainGain - validationGain
	addCheck("overfit_validation_regression", !overfit, map[string]float64{"trainGain": trainGain, "validationGain": validationGain}, "validation gain >= 0 when train improves", "OVERFIT_VALIDATION_REGRESSION")
	addCheck("generalization_gap", generalizationGap <= cfg.MaxGeneralizationGap+1e-9, generalizationGap, cfg.MaxGeneralizationGap, "GENERALIZATION_GAP_EXCEEDED")

	accountingValid := validAccounting(accounting)
	addCheck("accounting_integrity", accountingValid, accountingValid, true, "INVALID_ACCOUNTING")

	addCheck("model_call_budget", accounting.ModelCalls <= budget.MaxModelCalls, accounting.ModelCalls, budget.MaxModelCalls, "MODEL_CALL_BUDGET_EXCEEDED")
	addCheck("token_budget", accounting.TotalTokens <= budget.MaxTotalTokens, accounting.TotalTokens, budget.MaxTotalTokens, "TOKEN_BUDGET_EXCEEDED")
	addCheck("latency_budget", accounting.WallLatencyMS <= budget.MaxLatencyMS, accounting.WallLatencyMS, budget.MaxLatencyMS, "LATENCY_BUDGET_EXCEEDED")
	if budget.MaxCost != nil {
		if accounting.Cost == nil {
			addCheck("cost_budget", false, nil, *budget.MaxCost, "COST_UNKNOWN")
		} else {
			addCheck("cost_budget", *accounting.Cost <= *budget.MaxCost, *accounting.Cost, *budget.MaxCost, "COST_BUDGET_EXCEEDED")
		}
	}

	for reason := range reasons {
		decision.ReasonCodes = append(decision.ReasonCodes, reason)
	}
	sort.Strings(decision.ReasonCodes)
	decision.Accepted = len(decision.ReasonCodes) == 0
	decision.Deployable = decision.Accepted
	if decision.Accepted {
		decision.ReasonCodes = []string{"ALL_CHECKS_PASSED"}
		decision.Summary = "候选通过全部独立 Release Gate 检查。"
	} else {
		decision.Summary = "候选被拒绝：" + strings.Join(decision.ReasonCodes, ", ")
	}
	return decision
}

func validGateProvenance(
	baselineTrain, candidateTrain, baselineValidation, candidateValidation *evaluationSnapshot,
) bool {
	if baselineTrain == nil || candidateTrain == nil || baselineValidation == nil || candidateValidation == nil {
		return false
	}
	identities := []snapshotIdentity{
		baselineTrain.Identity,
		candidateTrain.Identity,
		baselineValidation.Identity,
		candidateValidation.Identity,
	}
	for _, identity := range identities {
		if identity.EvaluationRunID == "" || identity.EvalSetID == "" || identity.DatasetHash == "" ||
			identity.MetricsHash == "" || identity.ProfileHash == "" {
			return false
		}
	}
	if baselineTrain.Identity.Split != "train" || candidateTrain.Identity.Split != "train" ||
		baselineValidation.Identity.Split != "validation" || candidateValidation.Identity.Split != "validation" {
		return false
	}
	if baselineTrain.Identity.EvalSetID != candidateTrain.Identity.EvalSetID ||
		baselineValidation.Identity.EvalSetID != candidateValidation.Identity.EvalSetID ||
		baselineTrain.Identity.EvalSetID == baselineValidation.Identity.EvalSetID {
		return false
	}
	if baselineTrain.Identity.DatasetHash != candidateTrain.Identity.DatasetHash ||
		baselineValidation.Identity.DatasetHash != candidateValidation.Identity.DatasetHash {
		return false
	}
	if baselineTrain.Identity.MetricsHash != candidateTrain.Identity.MetricsHash ||
		baselineValidation.Identity.MetricsHash != candidateValidation.Identity.MetricsHash ||
		baselineTrain.Identity.MetricsHash != baselineValidation.Identity.MetricsHash {
		return false
	}
	if baselineTrain.Identity.ProfileHash != baselineValidation.Identity.ProfileHash ||
		candidateTrain.Identity.ProfileHash != candidateValidation.Identity.ProfileHash {
		return false
	}
	if baselineTrain.Identity.ProfileHash == candidateTrain.Identity.ProfileHash {
		return false
	}
	if baselineTrain.Identity.Seed != candidateTrain.Identity.Seed ||
		baselineTrain.Identity.Seed != baselineValidation.Identity.Seed ||
		baselineTrain.Identity.Seed != candidateValidation.Identity.Seed {
		return false
	}
	runIDs := make(map[string]struct{}, len(identities))
	for _, identity := range identities {
		if _, exists := runIDs[identity.EvaluationRunID]; exists {
			return false
		}
		runIDs[identity.EvaluationRunID] = struct{}{}
	}
	if len(runIDs) != len(identities) {
		return false
	}
	return true
}

func gateIdentitySummary(snapshots ...*evaluationSnapshot) []snapshotIdentity {
	identities := make([]snapshotIdentity, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if snapshot != nil {
			identities = append(identities, snapshot.Identity)
		}
	}
	return identities
}

func validSnapshotMatrix(snapshot *evaluationSnapshot) bool {
	if snapshot == nil || len(snapshot.Cases) == 0 {
		return false
	}
	seenCases := make(map[string]struct{}, len(snapshot.Cases))
	seenMetrics := make(map[string]struct{})
	for _, evalCase := range snapshot.Cases {
		if evalCase.CaseID == "" {
			return false
		}
		if _, exists := seenCases[evalCase.CaseID]; exists {
			return false
		}
		seenCases[evalCase.CaseID] = struct{}{}
		if len(evalCase.Metrics) == 0 {
			return false
		}
		for _, metric := range evalCase.Metrics {
			if metric.MetricName == "" {
				return false
			}
			key := evalCase.CaseID + "\x00" + metric.MetricName
			if _, exists := seenMetrics[key]; exists {
				return false
			}
			seenMetrics[key] = struct{}{}
		}
	}
	return true
}

func validEvaluationStatuses(snapshot *evaluationSnapshot) bool {
	if snapshot == nil || len(snapshot.Cases) == 0 {
		return false
	}
	for _, evalCase := range snapshot.Cases {
		for _, metric := range evalCase.Metrics {
			if metric.Status != string(status.EvalStatusPassed) && metric.Status != string(status.EvalStatusFailed) {
				return false
			}
		}
	}
	return true
}

func snapshotScoreConsistent(snapshot *evaluationSnapshot) bool {
	if snapshot == nil {
		return false
	}
	total := 0.0
	count := 0
	for _, evalCase := range snapshot.Cases {
		for _, metric := range evalCase.Metrics {
			if metric.Status == string(status.EvalStatusNotEvaluated) {
				continue
			}
			total += metric.Score
			count++
		}
	}
	return count > 0 && nearlyEqual(snapshot.OverallScore, total/float64(count))
}

func comparisonsMatchSnapshots(baseline, candidate *evaluationSnapshot, actual *comparison) bool {
	expected, err := compareSnapshots(baseline, candidate)
	if err != nil || actual == nil || len(expected.Metrics) != len(actual.Metrics) {
		return false
	}
	if !nearlyEqual(expected.BaselineScore, actual.BaselineScore) ||
		!nearlyEqual(expected.CandidateScore, actual.CandidateScore) ||
		!nearlyEqual(expected.OverallDelta, actual.OverallDelta) {
		return false
	}
	for index := range expected.Metrics {
		left, right := expected.Metrics[index], actual.Metrics[index]
		if left.CaseID != right.CaseID || left.MetricName != right.MetricName ||
			left.BaselineStatus != right.BaselineStatus || left.CandidateStatus != right.CandidateStatus ||
			left.Transition != right.Transition || !optionalFloatEqual(left.BaselineScore, right.BaselineScore) ||
			!optionalFloatEqual(left.CandidateScore, right.CandidateScore) || !optionalFloatEqual(left.ScoreDelta, right.ScoreDelta) {
			return false
		}
	}
	return true
}

func validAccounting(accounting accountingSummary) bool {
	if accounting.ModelCalls < 0 || accounting.PromptTokens < 0 || accounting.CompletionTokens < 0 ||
		accounting.TotalTokens < 0 || accounting.WallLatencyMS < 0 {
		return false
	}
	if accounting.PromptTokens+accounting.CompletionTokens != accounting.TotalTokens {
		return false
	}
	if accounting.Cost != nil && (math.IsNaN(*accounting.Cost) || math.IsInf(*accounting.Cost, 0) || *accounting.Cost < 0) {
		return false
	}
	for _, call := range accounting.ByStage {
		if call.PromptTokens < 0 || call.CompletionTokens < 0 || call.TotalTokens < 0 || call.LatencyMS < 0 ||
			call.PromptTokens+call.CompletionTokens != call.TotalTokens {
			return false
		}
	}
	if len(accounting.ByStage) > 0 {
		if len(accounting.ByStage) != accounting.ModelCalls {
			return false
		}
		promptTokens, completionTokens, totalTokens := 0, 0, 0
		for _, call := range accounting.ByStage {
			promptTokens += call.PromptTokens
			completionTokens += call.CompletionTokens
			totalTokens += call.TotalTokens
		}
		if promptTokens != accounting.PromptTokens || completionTokens != accounting.CompletionTokens || totalTokens != accounting.TotalTokens {
			return false
		}
	}
	return true
}

func finiteSnapshotScores(snapshot *evaluationSnapshot) bool {
	if snapshot == nil || math.IsNaN(snapshot.OverallScore) || math.IsInf(snapshot.OverallScore, 0) {
		return false
	}
	for _, evalCase := range snapshot.Cases {
		for _, metric := range evalCase.Metrics {
			if math.IsNaN(metric.Score) || math.IsInf(metric.Score, 0) ||
				math.IsNaN(metric.Threshold) || math.IsInf(metric.Threshold, 0) {
				return false
			}
		}
	}
	return true
}

func finiteComparisonScores(comparison *comparison) bool {
	if comparison == nil || math.IsNaN(comparison.BaselineScore) || math.IsInf(comparison.BaselineScore, 0) ||
		math.IsNaN(comparison.CandidateScore) || math.IsInf(comparison.CandidateScore, 0) ||
		math.IsNaN(comparison.OverallDelta) || math.IsInf(comparison.OverallDelta, 0) {
		return false
	}
	for _, delta := range comparison.Metrics {
		for _, value := range []*float64{delta.BaselineScore, delta.CandidateScore, delta.ScoreDelta} {
			if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
				return false
			}
		}
	}
	return true
}

func hashFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return hashBytes(data), nil
}

func hashText(value string) string {
	return hashBytes([]byte(value))
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func validateDatasetIsolation(trainPath, validationPath string, guard datasetGuardConfig) ([]string, error) {
	train, err := loadEvalSet(trainPath)
	if err != nil {
		return nil, fmt.Errorf("load train evalset: %w", err)
	}
	validation, err := loadEvalSet(validationPath)
	if err != nil {
		return nil, fmt.Errorf("load validation evalset: %w", err)
	}
	trainContent := make(map[string]string)
	trainTexts := make(map[string]string)
	for _, evalCase := range train.EvalCases {
		trainContent[evalCaseFingerprint(evalCase)] = evalCase.EvalID
		trainTexts[evalCase.EvalID] = evalCaseInputText(evalCase)
	}
	overlaps := make([]string, 0)
	for _, evalCase := range validation.EvalCases {
		fingerprint := evalCaseFingerprint(evalCase)
		if trainCaseID, ok := trainContent[fingerprint]; ok {
			overlap := fmt.Sprintf("exact:%s~%s", trainCaseID, evalCase.EvalID)
			overlaps = append(overlaps, overlap)
			if guard.FailOnExactOverlap {
				return overlaps, fmt.Errorf("train case %q exactly overlaps validation case %q", trainCaseID, evalCase.EvalID)
			}
			continue
		}
		validationText := evalCaseInputText(evalCase)
		for trainCaseID, trainText := range trainTexts {
			score := textNearDuplicateScore(trainText, validationText)
			if score < guard.NearThreshold {
				continue
			}
			overlap := fmt.Sprintf("near:%s~%s:%.3f", trainCaseID, evalCase.EvalID, score)
			overlaps = append(overlaps, overlap)
			if guard.FailOnNearOverlap {
				return overlaps, fmt.Errorf("train case %q is near-duplicate of validation case %q (score %.3f)", trainCaseID, evalCase.EvalID, score)
			}
		}
	}
	sort.Strings(overlaps)
	return overlaps, nil
}

func loadEvalSet(path string) (*evalset.EvalSet, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var result evalset.EvalSet
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func evalCaseFingerprint(evalCase *evalset.EvalCase) string {
	if evalCase == nil {
		return hashText("nil")
	}
	var builder strings.Builder
	builder.WriteString(evalCaseInputText(evalCase))
	return hashText(builder.String())
}

func evalCaseInputText(evalCase *evalset.EvalCase) string {
	if evalCase == nil {
		return ""
	}
	var builder strings.Builder
	for _, invocation := range evalCase.Conversation {
		if invocation == nil {
			continue
		}
		for _, message := range invocation.ContextMessages {
			if message == nil {
				continue
			}
			builder.WriteString(string(message.Role))
			builder.WriteByte(':')
			builder.WriteString(strings.TrimSpace(message.Content))
			builder.WriteByte('\x00')
		}
		if invocation.UserContent != nil {
			builder.WriteString(strings.TrimSpace(invocation.UserContent.Content))
		}
		builder.WriteByte('\x00')
	}
	return builder.String()
}

func textNearDuplicateScore(left, right string) float64 {
	leftTrigrams := runeTrigrams(normalizeNearDuplicateText(left))
	rightTrigrams := runeTrigrams(normalizeNearDuplicateText(right))
	if len(leftTrigrams) == 0 || len(rightTrigrams) == 0 {
		return 0
	}
	intersection := 0
	for item := range leftTrigrams {
		if _, ok := rightTrigrams[item]; ok {
			intersection++
		}
	}
	union := len(leftTrigrams) + len(rightTrigrams) - intersection
	if union == 0 {
		return 1
	}
	return float64(intersection) / float64(union)
}

func normalizeNearDuplicateText(value string) string {
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "case:train-", "case:")
	value = strings.ReplaceAll(value, "case:validation-", "case:")
	var builder strings.Builder
	for _, current := range value {
		if unicode.IsLetter(current) || unicode.IsDigit(current) {
			builder.WriteRune(current)
		}
	}
	return builder.String()
}

func runeTrigrams(value string) map[string]struct{} {
	runes := []rune(value)
	result := make(map[string]struct{})
	if len(runes) < 3 {
		result[value] = struct{}{}
		return result
	}
	for index := 0; index+3 <= len(runes); index++ {
		result[string(runes[index:index+3])] = struct{}{}
	}
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return "unknown"
}

func float64Ptr(value float64) *float64 {
	return &value
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func containsAny(value string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(value, needle) {
			return true
		}
	}
	return false
}

func nearlyEqual(left, right float64) bool {
	return math.Abs(left-right) <= 1e-9
}

func optionalFloatEqual(left, right *float64) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return nearlyEqual(*left, *right)
}
