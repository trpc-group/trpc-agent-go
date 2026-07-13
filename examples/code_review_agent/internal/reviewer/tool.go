//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package reviewer

import (
	"context"
	"fmt"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/store"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

type reviewToolSet struct {
	recorder *reviewRecorder
	tools    []tool.Tool
}

func newReviewToolSet(recorder *reviewRecorder) tool.ToolSet {
	set := &reviewToolSet{
		recorder: recorder,
	}
	set.tools = []tool.Tool{
		function.NewFunctionTool(
			set.submitReviewResults,
			function.WithName("submit_review_results"),
			function.WithDescription("Submit code review findings, warnings, human-review items, and conclusion for the current review task."),
		),
	}
	return set
}

func (s *reviewToolSet) Tools(context.Context) []tool.Tool {
	return s.tools
}

func (s *reviewToolSet) Close() error {
	return nil
}

func (s *reviewToolSet) Name() string {
	return "code_review_submit"
}

type submitReviewResultsInput struct {
	Findings         []reviewResultInput `json:"findings,omitempty" jsonschema:"description=High-confidence findings to include in the review report"`
	Warnings         []reviewResultInput `json:"warnings,omitempty" jsonschema:"description=Low-confidence warnings that should not be mixed into findings"`
	NeedsHumanReview []reviewResultInput `json:"needs_human_review,omitempty" jsonschema:"description=Items that require human review before being treated as findings"`
	Conclusion       string              `json:"conclusion,omitempty" jsonschema:"description=Short final conclusion for the current review task"`
}

type reviewResultInput struct {
	Severity       string  `json:"severity" jsonschema:"description=Finding severity, such as critical, high, medium, low, or info"`
	Category       string  `json:"category" jsonschema:"description=Review category, such as security, context_leak, resource_lifecycle, error_handling, test_gap, secret_leak, database_lifecycle"`
	File           string  `json:"file" jsonschema:"description=Repository-relative file path"`
	Line           int     `json:"line" jsonschema:"description=Line number for the issue, or 0 when no exact line is available"`
	Title          string  `json:"title" jsonschema:"description=Concise title for the review item"`
	Evidence       string  `json:"evidence" jsonschema:"description=Concrete evidence from the diff, tool output, or inspected code"`
	Recommendation string  `json:"recommendation,omitempty" jsonschema:"description=Actionable fix recommendation"`
	Confidence     float64 `json:"confidence" jsonschema:"description=Confidence score from 0 to 1"`
	Source         string  `json:"source" jsonschema:"description=Evidence source, such as agent, skill, static_rule, sandbox, go_test, go_vet"`
	RuleID         string  `json:"rule_id" jsonschema:"description=Rule id or check id that produced the item"`
}

type submitReviewResultsOutput struct {
	Status           string `json:"status"`
	FindingCount     int    `json:"finding_count"`
	WarningCount     int    `json:"warning_count"`
	HumanReviewCount int    `json:"human_review_count"`
}

func (s *reviewToolSet) submitReviewResults(
	ctx context.Context,
	in submitReviewResultsInput,
) (output submitReviewResultsOutput, err error) {
	taskID, err := reviewTaskIDFromContext(ctx)
	if err != nil {
		return submitReviewResultsOutput{}, err
	}
	if s == nil || s.recorder == nil {
		return submitReviewResultsOutput{}, fmt.Errorf("review recorder is not configured")
	}

	results := make([]store.ReviewResultRecord, 0, len(in.Findings)+len(in.Warnings)+len(in.NeedsHumanReview))
	results = appendReviewResults(results, "finding", in.Findings)
	results = appendReviewResults(results, "warning", in.Warnings)
	results = appendReviewResults(results, "needs_human_review", in.NeedsHumanReview)
	if err := s.recorder.SubmitReviewResults(ctx, taskID, results, in.Conclusion); err != nil {
		return submitReviewResultsOutput{}, err
	}

	return submitReviewResultsOutput{
		Status:           "accepted",
		FindingCount:     len(in.Findings),
		WarningCount:     len(in.Warnings),
		HumanReviewCount: len(in.NeedsHumanReview),
	}, nil
}

func appendReviewResults(records []store.ReviewResultRecord, kind string, inputs []reviewResultInput) []store.ReviewResultRecord {
	for _, in := range inputs {
		records = append(records, store.ReviewResultRecord{
			ResultKind:     kind,
			Severity:       in.Severity,
			Category:       in.Category,
			File:           in.File,
			Line:           in.Line,
			Title:          in.Title,
			Evidence:       in.Evidence,
			Recommendation: in.Recommendation,
			Confidence:     in.Confidence,
			Source:         in.Source,
			RuleID:         in.RuleID,
		})
	}
	return records
}

// newReviewPermissionPolicy returns a permission policy for the review agent
func newReviewPermissionPolicy(recorder *reviewRecorder) tool.PermissionPolicy {
	return tool.PermissionPolicyFunc(func(
		ctx context.Context,
		req *tool.PermissionRequest,
	) (decision tool.PermissionDecision, err error) {
		decision = tool.AllowPermission()
		taskID, err := reviewTaskIDFromContext(ctx)
		if err != nil || recorder == nil {
			return decision, nil
		}
		if err := recorder.RecordPermissionDecision(ctx, taskID, store.PermissionDecisionRecord{
			ToolCallID:     req.ToolCallID,
			DecisionKind:   "tool_permission",
			Operation:      req.ToolName,
			ToolName:       req.ToolName,
			CommandPreview: string(req.Arguments),
			Decision:       string(decision.Action),
			Reason:         decision.Reason,
			DecidedAt:      time.Now(),
		}); err != nil {
			return decision, err
		}
		return decision, nil
	})
}
