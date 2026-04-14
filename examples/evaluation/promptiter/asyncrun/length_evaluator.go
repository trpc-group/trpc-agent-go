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
	"fmt"
	"strings"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
)

const commentaryLengthMetricName = "final_response_length_compliance"

const (
	commentaryPreferredMinLength  = 32
	commentaryPreferredMaxLength  = 58
	commentaryAcceptableMinLength = 18
	commentaryAcceptableMaxLength = 72
)

type commentaryLengthEvaluator struct{}

func newCommentaryLengthEvaluator() evaluator.Evaluator {
	return &commentaryLengthEvaluator{}
}

func (e *commentaryLengthEvaluator) Name() string {
	return commentaryLengthMetricName
}

func (e *commentaryLengthEvaluator) Description() string {
	return "Checks whether the final response stays close to a concise live-call length of about 50 Chinese characters."
}

func (e *commentaryLengthEvaluator) Evaluate(
	_ context.Context,
	actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric,
) (*evaluator.EvaluateResult, error) {
	if len(actuals) != len(expecteds) {
		return nil, fmt.Errorf("length evaluator: actual invocations (%d) and expected invocations (%d) count mismatch", len(actuals), len(expecteds))
	}
	perInvocation := make([]*evaluator.PerInvocationResult, 0, len(actuals))
	var totalScore float64
	for i := range len(actuals) {
		actual := actuals[i]
		actualText := commentaryFinalResponseText(actual)
		actualLength := utf8.RuneCountInString(actualText)
		score, reason := commentaryLengthScore(actualLength)
		invocationStatus := commentaryStatusForScore(score, evalMetric)
		perInvocation = append(perInvocation, &evaluator.PerInvocationResult{
			ActualInvocation:   actual,
			ExpectedInvocation: expecteds[i],
			Score:              score,
			Status:             invocationStatus,
			Details: &evaluator.PerInvocationDetails{
				Reason: reason,
				Score:  score,
			},
		})
		totalScore += score
	}
	if len(perInvocation) == 0 {
		return &evaluator.EvaluateResult{OverallStatus: status.EvalStatusNotEvaluated}, nil
	}
	overallScore := totalScore / float64(len(perInvocation))
	return &evaluator.EvaluateResult{
		OverallScore:         overallScore,
		OverallStatus:        commentaryStatusForScore(overallScore, evalMetric),
		PerInvocationResults: perInvocation,
	}, nil
}

func commentaryFinalResponseText(invocation *evalset.Invocation) string {
	if invocation == nil || invocation.FinalResponse == nil {
		return ""
	}
	return strings.TrimSpace(invocation.FinalResponse.Content)
}

func commentaryLengthScore(actualLength int) (float64, string) {
	direction, deltaToPreferred := commentaryLengthDirection(actualLength)
	switch {
	case actualLength >= commentaryPreferredMinLength && actualLength <= commentaryPreferredMaxLength:
		return 1.0, commentaryLengthReason(actualLength, "preferred", direction, deltaToPreferred)
	case actualLength >= commentaryAcceptableMinLength && actualLength <= commentaryAcceptableMaxLength:
		return 0.5, commentaryLengthReason(actualLength, "acceptable", direction, deltaToPreferred)
	default:
		return 0.0, commentaryLengthReason(actualLength, "outside_acceptable", direction, deltaToPreferred)
	}
}

func commentaryLengthReason(actualLength int, band string, direction string, deltaToPreferred int) string {
	action := "Keep this response length stable."
	switch direction {
	case "too_short":
		action = "Expand the commentary with one more concrete live detail and no filler."
	case "too_long":
		action = "Compress the commentary by cutting filler and keeping only the decisive live detail."
	}
	return fmt.Sprintf(
		"length_signal: actual_length=%d preferred_range=[%d,%d] acceptable_range=[%d,%d] band=%s direction=%s delta_to_preferred=%d. %s",
		actualLength,
		commentaryPreferredMinLength,
		commentaryPreferredMaxLength,
		commentaryAcceptableMinLength,
		commentaryAcceptableMaxLength,
		band,
		direction,
		deltaToPreferred,
		action,
	)
}

func commentaryLengthDirection(actualLength int) (string, int) {
	switch {
	case actualLength < commentaryPreferredMinLength:
		return "too_short", commentaryPreferredMinLength - actualLength
	case actualLength > commentaryPreferredMaxLength:
		return "too_long", actualLength - commentaryPreferredMaxLength
	default:
		return "on_target", 0
	}
}

func commentaryStatusForScore(score float64, evalMetric *metric.EvalMetric) status.EvalStatus {
	if evalMetric != nil && score >= evalMetric.Threshold {
		return status.EvalStatusPassed
	}
	return status.EvalStatusFailed
}
