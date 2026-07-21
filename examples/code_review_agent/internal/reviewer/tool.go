//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
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
	"strings"

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
	Findings         []reviewResultInput `json:"findings" jsonschema:"description=High-confidence findings after a mandatory root-cause merge pass; if one edit fixes two observations submit one finding in the most-specific category and merge the evidence,required"`
	Warnings         []reviewResultInput `json:"warnings" jsonschema:"description=Low-confidence warnings that should not be mixed into findings,required"`
	NeedsHumanReview []reviewResultInput `json:"needs_human_review" jsonschema:"description=Items that require human review before being treated as findings,required"`
	Conclusion       string              `json:"conclusion" jsonschema:"description=Short final conclusion for the current review task,required"`
}

type reviewResultInput struct {
	Severity       string  `json:"severity" jsonschema:"description=Review severity: critical, high, medium, low, or warning"`
	Category       string  `json:"category" jsonschema:"description=Most-specific review category: correctness, security, sensitive_info, concurrency, resource_lifecycle, error_handling, tests, or database_lifecycle. Use database_lifecycle for database/sql resources. Correctness is only a fallback; a missing Close is one lifecycle item and a removed cancellation path plus its unused context is one concurrency item"`
	File           string  `json:"file" jsonschema:"description=Repository-relative file path"`
	Line           int     `json:"line" jsonschema:"description=Line number for the issue, or 0 when no exact line is available"`
	Title          string  `json:"title" jsonschema:"description=Concise title for the review item"`
	Evidence       string  `json:"evidence" jsonschema:"description=Concrete evidence from the diff, tool output, or inspected code"`
	Recommendation string  `json:"recommendation" jsonschema:"description=Actionable fix recommendation,required"`
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

	if strings.TrimSpace(in.Conclusion) == "" {
		return submitReviewResultsOutput{}, fmt.Errorf("review conclusion is required")
	}
	submission, err := canonicalizeReviewToolSubmission(in)
	if err != nil {
		return submitReviewResultsOutput{}, err
	}
	counts, err := s.recorder.SubmitReviewResults(
		ctx,
		taskID,
		submission.Results,
		in.Conclusion,
	)
	if err != nil {
		return submitReviewResultsOutput{}, err
	}

	return submitReviewResultsOutput{
		Status:           "accepted",
		FindingCount:     counts.FindingCount,
		WarningCount:     counts.WarningCount,
		HumanReviewCount: counts.HumanReviewCount,
	}, nil
}
