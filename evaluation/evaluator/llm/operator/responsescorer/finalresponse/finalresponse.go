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

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/responsescorer"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

const labelValid string = "valid" // labelValid marks a valid agent response.

// labelMatchIsResponseValidRe extracts the validity label from judge output.
var labelMatchIsResponseValidRe = regexp.MustCompile(`"is_the_agent_response_valid"\s*:\s*\[?\s*"?([A-Za-z_]+)"?\s*\]?`)

type finalResponseResponseScorer struct {
}

// New returns a response scorer for final responses.
func New() responsescorer.ResponseScorer {
	return &finalResponseResponseScorer{}
}

// ScoreBasedOnResponse converts judge feedback to a numeric score.
func (e *finalResponseResponseScorer) ScoreBasedOnResponse(ctx context.Context, response *model.Response,
	_ *metric.EvalMetric) (*evalresult.ScoreResult, error) {
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}
	responseText := response.Choices[0].Message.Content
	if responseText == "" {
		return nil, fmt.Errorf("empty response text")
	}
	label := extractLabel(responseText)
	score := 0.0
	if label == labelValid {
		score = 1.0
	}
	return &evalresult.ScoreResult{Score: score}, nil
}

// extractLabel extracts the validity label from the judge response.
func extractLabel(response string) string {
	match := labelMatchIsResponseValidRe.FindStringSubmatch(response)
	if len(match) < 1 {
		return ""
	}
	label := strings.TrimSpace(match[1])
	switch strings.ToLower(label) {
	case labelValid:
		return labelValid
	}
	return ""
}
