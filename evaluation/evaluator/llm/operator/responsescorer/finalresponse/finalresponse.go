//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package finalresponse converts judge feedback into validity scores for final responses.
package finalresponse

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const labelValid string = "valid" // labelValid marks a valid agent response.

// finalResponseBlockRegex extracts the reasoning and validity label from the judge response.
var finalResponseBlockRegex = regexp.MustCompile(
	`(?ms)reasoning:\s*(.*?)\s*` + // 1: reasoning text
		`is_the_agent_response_valid:\s*(.*?)\s*$`, // 2: validity label
)

type finalResponseResponseScorer struct {
}

// New returns a response scorer for final responses.
func New() responsescorer.ResponseScorer {
	return &finalResponseResponseScorer{}
}

// ScoreBasedOnResponse converts judge feedback to a numeric score.
func (e *finalResponseResponseScorer) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	_ *metric.EvalMetric) (*evaluator.ScoreResult, error) {
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}
	content := response.Choices[0].Message.Content
	if content == "" {
		return nil, fmt.Errorf("empty response text")
	}
	reasoning, label, err := extractReasoningAndLabel(content)
	if err != nil {
		return nil, fmt.Errorf("extract reasoning and label: %w", err)
	}
	score := 0.0
	if label == labelValid {
		score = 1.0
	}
	return &evaluator.ScoreResult{Score: score, Reason: reasoning}, nil
}

// extractReasoningAndLabel parses judge output in text form.
func extractReasoningAndLabel(content string) (string, string, error) {
	matches := finalResponseBlockRegex.FindAllStringSubmatch(content, -1)
	if len(matches) < 1 {
		return "", "", fmt.Errorf("no final response blocks found in response")
	}
	reasoning := strings.TrimSpace(matches[0][1])
	label := strings.TrimSpace(matches[0][2])
	label = strings.ToLower(label)
	return reasoning, label, nil
}
