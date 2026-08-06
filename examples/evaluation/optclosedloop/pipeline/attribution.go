//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package pipeline

import (
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiter"
)

// AttributeFailures categorizes every failing metric in an EvalSummary using
// simple but robust heuristics over final response, tool trajectory, and
// trace signals.
//
// The classification order is intentionally deterministic:
//  1. FormatError       -> output malformed JSON / required tags absent
//  2. ToolCallError     -> any tool invocation returned an error field
//  3. ToolArgumentError -> tool args failed schema validation / parse errors
//  4. RouteError        -> route tool selected an unexpected target
//  5. KnowledgeRecall   -> response explicitly "not sure" / "don't know"
//  6. FinalResponseMismatch -> otherwise failing metric
//  7. UnknownFailure    -> fallthrough
func AttributeFailures(summary *EvalSummary) []FailureAttribution {
	if summary == nil {
		return nil
	}
	attrs := make([]FailureAttribution, 0, 4)
	for _, c := range summary.PerCase {
		if c.OverallPassed {
			continue
		}
		for _, m := range c.Metrics {
			if m.Passed {
				continue
			}
			attr := classify(summary.EvalSetID, c, m)
			attrs = append(attrs, attr)
		}
	}
	return attrs
}

func classify(setID string, c CaseEval, m CaseMetric) FailureAttribution {
	_ = setID // reserved for future trace-based attribution
	attr := FailureAttribution{
		EvalCaseID: c.EvalCaseID,
		MetricName: m.MetricName,
		Reason:     m.Reason,
		Evidence:   []string{},
	}
	// Prefer pre-stamped classification (fake deterministic runner).
	oc, ok := caseOutcomes[c.EvalCaseID]
	if ok && !oc.PassBaseline {
		attr.Category = oc.FailReason
		attr.Severity = promptiter.LossSeverityP1
		if attr.Reason == "" {
			attr.Reason = oc.FailReasonStr
		}
		if len(oc.Tools) > 0 {
			for _, t := range oc.Tools {
				if t.Error != "" {
					attr.Evidence = append(attr.Evidence, "tool error: "+t.ToolName+" -> "+t.Error)
				}
			}
		}
		return attr
	}
	// Fallback heuristic classification.
	resp := strings.ToLower(c.FinalResponse)
	reason := strings.ToLower(m.Reason)
	// Format check.
	if strings.Contains(reason, "json") || strings.Contains(resp, "{") && !strings.Contains(resp, "}") {
		attr.Category = FormatError
		attr.Severity = promptiter.LossSeverityP2
		if attr.Reason == "" {
			attr.Reason = "response format failed to parse"
		}
		return attr
	}
	// Tool call error check.
	for _, t := range c.ToolTrajectory {
		if t.Error != "" {
			attr.Category = ToolCallError
			attr.Severity = promptiter.LossSeverityP1
			attr.Evidence = append(attr.Evidence, t.ToolName+": "+t.Error)
			if attr.Reason == "" {
				attr.Reason = "tool " + t.ToolName + " returned error"
			}
			return attr
		}
	}
	// Tool arg error check.
	if strings.Contains(reason, "argument") || strings.Contains(reason, "parsefloat") || strings.Contains(reason, "invalid syntax") {
		attr.Category = ToolArgumentError
		attr.Severity = promptiter.LossSeverityP1
		return attr
	}
	// Route error.
	for _, t := range c.ToolTrajectory {
		if t.ToolName == "route" {
			attr.Category = RouteError
			attr.Severity = promptiter.LossSeverityP1
			if attr.Reason == "" {
				attr.Reason = "routing decision looks incorrect"
			}
			return attr
		}
	}
	// Knowledge recall / hedging.
	if strings.Contains(resp, "not sure") || strings.Contains(resp, "don't know") ||
		strings.Contains(resp, "do not have enough info") || strings.Contains(resp, "hedge") {
		attr.Category = KnowledgeRecallInsufficient
		attr.Severity = promptiter.LossSeverityP2
		if attr.Reason == "" {
			attr.Reason = "agent hedges instead of recalling knowledge"
		}
		return attr
	}
	// Default final response mismatch.
	attr.Category = FinalResponseMismatch
	attr.Severity = promptiter.LossSeverityP1
	if attr.Reason == "" {
		attr.Reason = "final answer does not match expected output"
	}
	return attr
}

// ToTerminalLosses converts failure attributions into the real
// promptiter.CaseLoss slice that the backwarder stage consumes.
// Each attribution becomes one TerminalLoss with its Severity and Reason
// preserved, enabling direct integration with the promptiter engine.
// The returned slice is sorted by EvalCaseID for deterministic output.
func ToTerminalLosses(attrs []FailureAttribution, evalSetID string) []promptiter.CaseLoss {
	if len(attrs) == 0 {
		return nil
	}
	byCase := make(map[string][]promptiter.TerminalLoss)
	for _, a := range attrs {
		sev := a.Severity
		if sev == "" {
			sev = promptiter.LossSeverityP1
		}
		byCase[a.EvalCaseID] = append(byCase[a.EvalCaseID], promptiter.TerminalLoss{
			EvalSetID:  evalSetID,
			EvalCaseID: a.EvalCaseID,
			MetricName: a.MetricName,
			Severity:   sev,
			StepID:     "", // populated by trace-based mode
			Loss:       a.Reason,
		})
	}
	// Sort case IDs for deterministic output order.
	caseIDs := make([]string, 0, len(byCase))
	for caseID := range byCase {
		caseIDs = append(caseIDs, caseID)
	}
	sort.Strings(caseIDs)
	out := make([]promptiter.CaseLoss, 0, len(caseIDs))
	for _, caseID := range caseIDs {
		out = append(out, promptiter.CaseLoss{
			EvalSetID:      evalSetID,
			EvalCaseID:     caseID,
			TerminalLosses: byCase[caseID],
		})
	}
	return out
}
