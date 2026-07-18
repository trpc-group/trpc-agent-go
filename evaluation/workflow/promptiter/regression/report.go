//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package regression

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// ReportSchemaVersion identifies the audit report contract.
const ReportSchemaVersion = "v1"

// AuditInput identifies one reproducibility input without exposing absolute paths.
type AuditInput struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

// RuntimeAudit identifies the deterministic or model-backed runtime.
type RuntimeAudit struct {
	Mode         string            `json:"mode"`
	Provider     string            `json:"provider,omitempty"`
	Model        string            `json:"model,omitempty"`
	ConfigSHA256 string            `json:"config_sha256,omitempty"`
	Config       map[string]string `json:"config,omitempty"`
}

// AuditMetadata records caller-supplied reproducibility facts.
type AuditMetadata struct {
	RunID      string       `json:"run_id"`
	StartedAt  time.Time    `json:"started_at"`
	FinishedAt time.Time    `json:"finished_at"`
	DurationMS int64        `json:"duration_ms"`
	Seed       int64        `json:"seed"`
	Inputs     []AuditInput `json:"inputs"`
	Runtime    RuntimeAudit `json:"runtime"`
}

// PromptAudit stores prompt text and a stable content hash.
type PromptAudit struct {
	Text   string `json:"text"`
	SHA256 string `json:"sha256"`
}

// FinalDecision summarizes whether the candidate should be written back.
type FinalDecision struct {
	Accepted             bool   `json:"accepted"`
	WriteBackRecommended bool   `json:"write_back_recommended"`
	StopReason           string `json:"reason"`
}

// ReportEvaluation keeps score details while including execution evidence only
// when it is needed to explain a baseline failure.
type ReportEvaluation struct {
	EvalSetID string       `json:"eval_set_id"`
	Score     float64      `json:"score"`
	Passed    bool         `json:"passed"`
	Cases     []ReportCase `json:"cases"`
}

// ReportCase is the compact audit view of one evaluated case.
type ReportCase struct {
	ID                  string              `json:"id"`
	Score               float64             `json:"score"`
	Passed              bool                `json:"passed"`
	Error               string              `json:"error,omitempty"`
	Metrics             []ReportMetric      `json:"metrics"`
	ActualInvocations   []InvocationSummary `json:"actual_invocations,omitempty"`
	ExpectedInvocations []InvocationSummary `json:"expected_invocations,omitempty"`
}

// ReportMetric omits normalization-only details from the audit summary.
type ReportMetric struct {
	Name      string  `json:"name"`
	Score     float64 `json:"score"`
	Threshold float64 `json:"threshold"`
	Passed    bool    `json:"passed"`
	Evaluated bool    `json:"evaluated"`
	Reason    string  `json:"reason,omitempty"`
}

// ReportDatasetDelta contains only changed cases and metrics. Dataset-level
// scores remain available on the adjacent evaluation summary.
type ReportDatasetDelta struct {
	EvalSetID  string            `json:"eval_set_id"`
	Kind       DeltaKind         `json:"kind"`
	ScoreDelta float64           `json:"score_delta"`
	Cases      []ReportCaseDelta `json:"changed_cases,omitempty"`
}

// ReportCaseDelta is a changed case in a compact dataset delta.
type ReportCaseDelta struct {
	ID             string              `json:"id"`
	Kind           DeltaKind           `json:"kind"`
	BaselineScore  float64             `json:"baseline_score"`
	CandidateScore float64             `json:"candidate_score"`
	ScoreDelta     float64             `json:"score_delta"`
	Metrics        []ReportMetricDelta `json:"changed_metrics,omitempty"`
}

// ReportMetricDelta is a changed metric or evaluation-state transition.
type ReportMetricDelta struct {
	Name               string    `json:"name"`
	Kind               DeltaKind `json:"kind"`
	BaselineScore      float64   `json:"baseline_score"`
	CandidateScore     float64   `json:"candidate_score"`
	ScoreDelta         float64   `json:"score_delta"`
	BaselineEvaluated  bool      `json:"baseline_evaluated"`
	CandidateEvaluated bool      `json:"candidate_evaluated"`
}

// ReportRound removes repeated candidate execution evidence already explained
// by deltas and attribution.
type ReportRound struct {
	Number           int                 `json:"number"`
	InputPrompt      string              `json:"input_prompt"`
	CandidatePrompt  string              `json:"candidate_prompt"`
	Hints            []FailureHint       `json:"hints"`
	Train            *ReportEvaluation   `json:"train"`
	Validation       *ReportEvaluation   `json:"validation"`
	TrainDelta       *ReportDatasetDelta `json:"train_delta"`
	ValidationDelta  *ReportDatasetDelta `json:"validation_delta"`
	Attribution      *Attribution        `json:"validation_attribution"`
	Gate             *GateDecision       `json:"gate"`
	ServingCost      Cost                `json:"serving_cost"`
	OptimizationCost Cost                `json:"optimization_cost"`
}

// OptimizationReport is the single source used by JSON and Markdown writers.
type OptimizationReport struct {
	SchemaVersion                 string            `json:"schema_version"`
	Status                        string            `json:"status"`
	Audit                         AuditMetadata     `json:"audit"`
	InitialPrompt                 PromptAudit       `json:"initial_prompt"`
	AcceptedPrompt                PromptAudit       `json:"accepted_prompt"`
	BaselineTrain                 *ReportEvaluation `json:"baseline_train"`
	BaselineValidation            *ReportEvaluation `json:"baseline_validation"`
	BaselineTrainAttribution      *Attribution      `json:"baseline_train_attribution"`
	BaselineValidationAttribution *Attribution      `json:"baseline_validation_attribution"`
	Rounds                        []ReportRound     `json:"rounds"`
	Decision                      FinalDecision     `json:"decision"`
	Cost                          Cost              `json:"cost"`
}

// BuildReport validates a completed run and builds its auditable representation.
func BuildReport(run *OptimizationRun, metadata AuditMetadata) (*OptimizationReport, error) {
	if run == nil || run.BaselineTrain == nil || run.BaselineValidation == nil {
		return nil, errors.New("optimization run or baseline results are missing")
	}
	if strings.TrimSpace(run.InitialPrompt) == "" || strings.TrimSpace(run.AcceptedPrompt) == "" {
		return nil, errors.New("optimization run prompts are missing")
	}
	if strings.TrimSpace(run.StopReason) == "" {
		return nil, errors.New("optimization run stop reason is missing")
	}
	for index, round := range run.Rounds {
		if round.Number <= 0 || round.Train == nil || round.Validation == nil ||
			round.TrainDelta == nil || round.ValidationDelta == nil || round.Gate == nil {
			return nil, fmt.Errorf("round at index %d is incomplete", index)
		}
		if _, err := addCosts(round.ServingCost, round.OptimizationCost); err != nil {
			return nil, fmt.Errorf("round %d cost: %w", round.Number, err)
		}
	}
	normalized, err := normalizeAudit(metadata, run.Seed)
	if err != nil {
		return nil, err
	}
	status := "rejected"
	if run.WriteBackRecommended {
		status = "accepted"
	} else if len(run.Rounds) == 0 {
		status = "not_optimized"
	}
	baselineTrainAttribution, err := Attribute(run.BaselineTrain)
	if err != nil {
		return nil, fmt.Errorf("attribute baseline train: %w", err)
	}
	baselineValidationAttribution, err := Attribute(run.BaselineValidation)
	if err != nil {
		return nil, fmt.Errorf("attribute baseline validation: %w", err)
	}
	return &OptimizationReport{
		SchemaVersion: ReportSchemaVersion, Status: status, Audit: normalized,
		InitialPrompt: promptAudit(run.InitialPrompt), AcceptedPrompt: promptAudit(run.AcceptedPrompt),
		BaselineTrain:                 reportEvaluation(run.BaselineTrain, true),
		BaselineValidation:            reportEvaluation(run.BaselineValidation, true),
		BaselineTrainAttribution:      baselineTrainAttribution,
		BaselineValidationAttribution: baselineValidationAttribution,
		Rounds:                        reportRounds(run.Rounds),
		Decision: FinalDecision{
			Accepted: run.WriteBackRecommended, WriteBackRecommended: run.WriteBackRecommended,
			StopReason: run.StopReason,
		},
		Cost: run.TotalCost,
	}, nil
}

func reportEvaluation(summary *EvalSummary, includeFailureEvidence bool) *ReportEvaluation {
	result := &ReportEvaluation{EvalSetID: summary.EvalSetID, Score: summary.Score, Passed: summary.Passed,
		Cases: make([]ReportCase, 0, len(summary.Cases))}
	for _, evalCase := range summary.Cases {
		item := ReportCase{ID: evalCase.ID, Score: evalCase.Score, Passed: evalCase.Passed, Error: evalCase.Error}
		for _, metric := range evalCase.Metrics {
			item.Metrics = append(item.Metrics, ReportMetric{
				Name: metric.Name, Score: metric.Score, Threshold: metric.Threshold,
				Passed: metric.Passed, Evaluated: metric.Evaluated, Reason: metric.Reason,
			})
		}
		if includeFailureEvidence && !evalCase.Passed {
			item.ActualInvocations = evalCase.ActualInvocations
			item.ExpectedInvocations = evalCase.ExpectedInvocations
		}
		result.Cases = append(result.Cases, item)
	}
	return result
}

func reportRounds(rounds []Round) []ReportRound {
	result := make([]ReportRound, 0, len(rounds))
	for _, round := range rounds {
		result = append(result, ReportRound{
			Number: round.Number, InputPrompt: round.InputPrompt, CandidatePrompt: round.CandidatePrompt,
			Hints: round.Hints, Train: reportEvaluation(round.Train, false),
			Validation: reportEvaluation(round.Validation, false), TrainDelta: reportDelta(round.TrainDelta),
			ValidationDelta: reportDelta(round.ValidationDelta), Attribution: round.Attribution, Gate: round.Gate,
			ServingCost: round.ServingCost, OptimizationCost: round.OptimizationCost,
		})
	}
	return result
}

func reportDelta(delta *DatasetDelta) *ReportDatasetDelta {
	result := &ReportDatasetDelta{EvalSetID: delta.EvalSetID, Kind: delta.Kind, ScoreDelta: delta.ScoreDelta}
	for _, evalCase := range delta.Cases {
		item := ReportCaseDelta{ID: evalCase.ID, Kind: evalCase.Kind, BaselineScore: evalCase.BaselineScore,
			CandidateScore: evalCase.CandidateScore, ScoreDelta: evalCase.ScoreDelta}
		for _, metric := range evalCase.Metrics {
			if metric.Kind == DeltaUnchanged && metric.BaselineEvaluated == metric.CandidateEvaluated {
				continue
			}
			item.Metrics = append(item.Metrics, ReportMetricDelta{Name: metric.Name, Kind: metric.Kind,
				BaselineScore: metric.BaselineScore, CandidateScore: metric.CandidateScore, ScoreDelta: metric.ScoreDelta,
				BaselineEvaluated: metric.BaselineEvaluated, CandidateEvaluated: metric.CandidateEvaluated})
		}
		if evalCase.Kind != DeltaUnchanged || len(item.Metrics) > 0 {
			result.Cases = append(result.Cases, item)
		}
	}
	return result
}

// WriteJSON writes deterministic, indented JSON.
func WriteJSON(writer io.Writer, report *OptimizationReport) error {
	if writer == nil || report == nil {
		return errors.New("JSON writer and report are required")
	}
	encoder := json.NewEncoder(writer)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	return encoder.Encode(report)
}

// WriteMarkdown writes a concise human-readable audit report.
func WriteMarkdown(writer io.Writer, report *OptimizationReport) error {
	if writer == nil || report == nil {
		return errors.New("Markdown writer and report are required")
	}
	var output strings.Builder
	fmt.Fprintf(&output, "# Prompt Optimization Report\n\n")
	fmt.Fprintf(&output, "- Status: **%s**\n", report.Status)
	fmt.Fprintf(&output, "- Decision: %s\n", report.Decision.StopReason)
	fmt.Fprintf(&output, "- Seed: %d\n", report.Audit.Seed)
	fmt.Fprintf(&output, "- Runtime: %s", report.Audit.Runtime.Mode)
	if report.Audit.Runtime.Model != "" {
		fmt.Fprintf(&output, " (%s)", report.Audit.Runtime.Model)
	}
	fmt.Fprintln(&output)
	fmt.Fprintln(&output)

	fmt.Fprintln(&output, "## Baseline")
	fmt.Fprintln(&output)
	fmt.Fprintln(&output, "| Dataset | Score | Passed |")
	fmt.Fprintln(&output, "| --- | ---: | :---: |")
	markdownScoreRow(&output, "Train", report.BaselineTrain)
	markdownScoreRow(&output, "Validation", report.BaselineValidation)
	fmt.Fprintln(&output, "\n### Train failures")
	writeAttribution(&output, report.BaselineTrainAttribution)
	fmt.Fprintln(&output, "\n### Validation failures")
	writeAttribution(&output, report.BaselineValidationAttribution)

	fmt.Fprintln(&output, "\n## Optimization Rounds")
	if len(report.Rounds) == 0 {
		fmt.Fprintln(&output, "\nNo candidate was generated.")
	}
	for _, round := range report.Rounds {
		fmt.Fprintf(&output, "\n### Round %d\n\n", round.Number)
		fmt.Fprintf(&output, "- Gate: **%s**\n", acceptedText(round.Gate != nil && round.Gate.Accepted))
		fmt.Fprintln(&output, "- Candidate prompt:")
		fmt.Fprintf(&output, "\n```text\n%s\n```\n", markdownCodeBlock(round.CandidatePrompt))
		if round.Gate != nil {
			for _, reason := range round.Gate.Reasons {
				fmt.Fprintf(&output, "  - %s\n", reason)
			}
		}
		fmt.Fprintf(&output, "- Train score: %.6f (%+.6f)\n", round.Train.Score, round.TrainDelta.ScoreDelta)
		fmt.Fprintf(&output, "- Validation score: %.6f (%+.6f)\n", round.Validation.Score, round.ValidationDelta.ScoreDelta)
		fmt.Fprintf(&output, "- Evaluation cost: %s\n", costText(round.ServingCost))
		fmt.Fprintf(&output, "- Optimization cost: %s\n", costText(round.OptimizationCost))
		roundCost, _ := addCosts(round.ServingCost, round.OptimizationCost)
		fmt.Fprintf(&output, "- Round total: %s\n", costText(roundCost))
		writeCaseDeltas(&output, round.ValidationDelta)
		writeMetricDeltas(&output, round.ValidationDelta)
		writeAttribution(&output, round.Attribution)
	}

	fmt.Fprintln(&output, "\n## Cost")
	fmt.Fprintln(&output)
	fmt.Fprintf(&output, "- Model calls: %d\n- Tokens: %d\n- Latency: %d ms\n",
		report.Cost.ModelCalls, report.Cost.Tokens, report.Cost.LatencyMS)
	_, err := io.WriteString(writer, output.String())
	return err
}

func normalizeAudit(metadata AuditMetadata, seed int64) (AuditMetadata, error) {
	if metadata.RunID == "" || metadata.StartedAt.IsZero() || metadata.FinishedAt.IsZero() {
		return AuditMetadata{}, errors.New("audit run id and timestamps are required")
	}
	if metadata.FinishedAt.Before(metadata.StartedAt) {
		return AuditMetadata{}, errors.New("audit finish time precedes start time")
	}
	metadata.Seed = seed
	metadata.DurationMS = metadata.FinishedAt.Sub(metadata.StartedAt).Milliseconds()
	metadata.Inputs = append([]AuditInput(nil), metadata.Inputs...)
	sort.Slice(metadata.Inputs, func(i, j int) bool { return metadata.Inputs[i].Name < metadata.Inputs[j].Name })
	for index := range metadata.Inputs {
		input := &metadata.Inputs[index]
		if input.Name == "" || input.SHA256 == "" {
			return AuditMetadata{}, errors.New("audit input name and sha256 are required")
		}
		input.Path = filepath.Base(input.Path)
	}
	metadata.Runtime.Config = cloneAndRedactConfig(metadata.Runtime.Config)
	if metadata.Runtime.Mode == "" {
		return AuditMetadata{}, errors.New("audit runtime mode is required")
	}
	return metadata, nil
}

func cloneAndRedactConfig(config map[string]string) map[string]string {
	if len(config) == 0 {
		return nil
	}
	result := make(map[string]string, len(config))
	for key, value := range config {
		if sensitiveKey(key) {
			value = "<redacted>"
		}
		result[key] = value
	}
	return result
}

func promptAudit(text string) PromptAudit {
	hash := sha256.Sum256([]byte(text))
	return PromptAudit{Text: text, SHA256: fmt.Sprintf("%x", hash)}
}

func markdownScoreRow(output *strings.Builder, name string, summary *ReportEvaluation) {
	fmt.Fprintf(output, "| %s | %.6f | %t |\n", name, summary.Score, summary.Passed)
}

func acceptedText(accepted bool) string {
	if accepted {
		return "accepted"
	}
	return "rejected"
}

func writeCaseDeltas(output *strings.Builder, delta *ReportDatasetDelta) {
	if delta == nil {
		return
	}
	fmt.Fprintln(output, "\n| Case | Change | Baseline | Candidate | Delta |")
	fmt.Fprintln(output, "| --- | --- | ---: | ---: | ---: |")
	for _, evalCase := range delta.Cases {
		fmt.Fprintf(output, "| %s | %s | %.6f | %.6f | %+.6f |\n",
			markdownCell(evalCase.ID), evalCase.Kind, evalCase.BaselineScore, evalCase.CandidateScore, evalCase.ScoreDelta)
	}
}

func writeMetricDeltas(output *strings.Builder, delta *ReportDatasetDelta) {
	if delta == nil {
		return
	}
	wroteHeader := false
	for _, evalCase := range delta.Cases {
		for _, metric := range evalCase.Metrics {
			if metric.Kind == DeltaUnchanged && metric.BaselineEvaluated == metric.CandidateEvaluated {
				continue
			}
			if !wroteHeader {
				fmt.Fprintln(output, "\nChanged metrics:")
				fmt.Fprintln(output, "\n| Case | Metric | Change | Baseline | Candidate | Delta |")
				fmt.Fprintln(output, "| --- | --- | --- | ---: | ---: | ---: |")
				wroteHeader = true
			}
			fmt.Fprintf(output, "| %s | %s | %s | %.6f | %.6f | %+.6f |\n",
				markdownCell(evalCase.ID), markdownCell(metric.Name), metricChange(metric),
				metric.BaselineScore, metric.CandidateScore, metric.ScoreDelta)
		}
	}
}

func metricChange(metric ReportMetricDelta) string {
	if metric.BaselineEvaluated != metric.CandidateEvaluated {
		if metric.CandidateEvaluated {
			return "newly_evaluated"
		}
		return "no_longer_evaluated"
	}
	return string(metric.Kind)
}

func costText(cost Cost) string {
	return fmt.Sprintf("%d calls, %d tokens, %d ms", cost.ModelCalls, cost.Tokens, cost.LatencyMS)
}

func writeAttribution(output *strings.Builder, attribution *Attribution) {
	if attribution == nil || len(attribution.Counts) == 0 {
		return
	}
	categories := make([]string, 0, len(attribution.Counts))
	for category := range attribution.Counts {
		categories = append(categories, string(category))
	}
	sort.Strings(categories)
	fmt.Fprintln(output, "\nFailure attribution:")
	for _, category := range categories {
		fmt.Fprintf(output, "- %s: %d\n", category, attribution.Counts[FailureCategory(category)])
	}
	for _, failure := range attribution.Failures {
		fmt.Fprintf(output, "  - %s: %s — %s\n", markdownCell(failure.CaseID), failure.Category, markdownCell(failure.Reason))
	}
}

func markdownCell(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "|", "\\|"), "\n", " ")
}

func markdownCodeBlock(value string) string {
	return strings.ReplaceAll(value, "```", "` ` `")
}
