//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package issue defines the domain models used by the prompt iteration workflow.
package issue

import (
	"encoding/json"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

// Severity indicates the priority level for a prompt issue.
type Severity string

const (
	// SeverityP0 indicates a must-fix issue.
	SeverityP0 Severity = "P0"
	// SeverityP1 indicates an important but non-blocking issue.
	SeverityP1 Severity = "P1"
)

// Issue is a normalized prompt issue extracted from evaluations.
type Issue struct {
	// Severity is the priority level of the issue.
	Severity Severity `json:"severity,omitempty"`
	// Key is a stable identifier used for deduplication.
	Key string `json:"key,omitempty"`
	// Summary describes the observed problem in a concise form.
	Summary string `json:"summary,omitempty"`
	// Action describes how to update the prompt to address the issue.
	Action string `json:"action,omitempty"`
}

// IssueExtractor extracts prompt issues from an evaluation case result.
type IssueExtractor func(evalSetID string, caseResult *evalresult.EvalCaseResult) []IssueRecord

// DefaultExtractor extracts issues from an evaluation case result using a minimal heuristic.
func DefaultExtractor(evalSetID string, caseResult *evalresult.EvalCaseResult) []IssueRecord {
	if caseResult == nil {
		return nil
	}
	out := make([]IssueRecord, 0)
	if strings.TrimSpace(caseResult.ErrorMessage) != "" {
		out = append(out, IssueRecord{
			Issue: Issue{
				Severity: SeverityP0,
				Key:      "case_error",
				Summary:  strings.TrimSpace(caseResult.ErrorMessage),
				Action:   "Fix evaluation execution errors before optimizing the prompt.",
			},
			EvalSetID:  evalSetID,
			EvalCaseID: caseResult.EvalID,
		})
	}
	metricResults := caseResult.OverallEvalMetricResults
	if len(metricResults) == 0 {
		for _, perInvocation := range caseResult.EvalMetricResultPerInvocation {
			if perInvocation == nil {
				continue
			}
			metricResults = append(metricResults, perInvocation.EvalMetricResults...)
		}
	}
	for _, metricResult := range metricResults {
		if metricResult == nil {
			continue
		}
		if metricResult.EvalStatus == status.EvalStatusPassed {
			continue
		}
		metricName := strings.TrimSpace(metricResult.MetricName)
		if metricName == "" {
			metricName = "metric_failed"
		}
		summary := ""
		if metricResult.Details != nil {
			summary = strings.TrimSpace(metricResult.Details.Reason)
		}
		if summary == "" {
			summary = fmt.Sprintf("Metric %s did not pass.", metricName)
		}
		out = append(out, IssueRecord{
			Issue: Issue{
				Severity: SeverityP0,
				Key:      metricName,
				Summary:  summary,
				Action:   fmt.Sprintf("Update the prompt to improve metric %s.", metricName),
			},
			EvalSetID:  evalSetID,
			EvalCaseID: caseResult.EvalID,
			MetricName: metricName,
		})
	}
	return out
}

// IssueRecord attaches an issue to a specific eval case and metric.
type IssueRecord struct {
	// Issue contains the normalized issue details.
	Issue Issue `json:"issue,omitempty"`
	// EvalSetID is the eval set identifier where the issue is observed.
	EvalSetID string `json:"eval_set_id,omitempty"`
	// EvalCaseID is the eval case identifier where the issue is observed.
	EvalCaseID string `json:"eval_case_id,omitempty"`
	// MetricName is the metric name that produced the issue, when applicable.
	MetricName string `json:"metric_name,omitempty"`
}

// AggregatedGradient is the output of the gradient aggregator.
type AggregatedGradient struct {
	// Issues is a deduplicated list of aggregated issues.
	Issues []AggregatedIssue `json:"issues,omitempty"`
	// Notes contains optional global guidance for the optimizer.
	Notes string `json:"notes,omitempty"`
	// Extra stores user-defined gradient fields preserved during JSON marshalling.
	Extra map[string]any `json:"-"`
}

// IsEmpty reports whether the aggregated gradient contains no actionable fields.
func (g *AggregatedGradient) IsEmpty() bool {
	if g == nil {
		return true
	}
	if len(g.Issues) != 0 {
		return false
	}
	if strings.TrimSpace(g.Notes) != "" {
		return false
	}
	return len(g.Extra) == 0
}

// UnmarshalJSON preserves unknown top-level fields into Extra.
func (g *AggregatedGradient) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	g.Issues = nil
	g.Notes = ""
	g.Extra = nil
	if v, ok := raw["issues"]; ok {
		if err := json.Unmarshal(v, &g.Issues); err != nil {
			return err
		}
		delete(raw, "issues")
	}
	if v, ok := raw["notes"]; ok {
		if err := json.Unmarshal(v, &g.Notes); err != nil {
			return err
		}
		delete(raw, "notes")
	}
	if len(raw) == 0 {
		return nil
	}
	g.Extra = make(map[string]any, len(raw))
	for k, v := range raw {
		var decoded any
		if err := json.Unmarshal(v, &decoded); err != nil {
			return err
		}
		g.Extra[k] = decoded
	}
	return nil
}

// MarshalJSON flattens Extra fields into the top-level object.
func (g AggregatedGradient) MarshalJSON() ([]byte, error) {
	out := make(map[string]any, 2+len(g.Extra))
	if len(g.Issues) != 0 {
		out["issues"] = g.Issues
	}
	if g.Notes != "" {
		out["notes"] = g.Notes
	}
	for k, v := range g.Extra {
		if k == "issues" || k == "notes" {
			continue
		}
		out[k] = v
	}
	return json.Marshal(out)
}

// AggregatedIssue is a deduplicated issue with representative cases.
type AggregatedIssue struct {
	// Severity is the highest severity observed for the issue.
	Severity Severity `json:"severity,omitempty"`
	// Key is a stable identifier used for deduplication.
	Key string `json:"key,omitempty"`
	// Summary describes the issue in a concise form.
	Summary string `json:"summary,omitempty"`
	// Action describes the intended prompt change.
	Action string `json:"action,omitempty"`
	// Cases lists representative eval case references for debugging.
	Cases []string `json:"cases,omitempty"`
}
