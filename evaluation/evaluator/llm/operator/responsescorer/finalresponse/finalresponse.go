//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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

// labelMatchIsResponseValidRe extracts the validity label from judge output.
var labelMatchIsResponseValidRe = regexp.MustCompile(`"is_the_agent_response_valid"\s*:\s*\[?\s*"?([A-Za-z_]+)"?\s*\]?`)

type finalResponseResponseScorer struct {
}

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
	switch label {
	case LabelValid:
		score = 1.0
	case LabelInvalid:
		score = 0.0
	default:
		return nil, fmt.Errorf("unknown label: %v", label)
	}
	return &evalresult.ScoreResult{
		Score: score,
	}, nil
}

// Label captures the validity category returned by the judge.
type Label string

const (
	LabelValid   Label = "valid"   // LabelValid marks a valid agent response.
	LabelInvalid Label = "invalid" // LabelInvalid marks an invalid agent response.
)

// extractLabel extracts the validity label from the judge response.
func extractLabel(response string) Label {
	match := labelMatchIsResponseValidRe.FindStringSubmatch(response)
	if len(match) < 1 {
		return LabelInvalid
	}
	label := strings.TrimSpace(match[1])
	switch strings.ToLower(label) {
	case string(LabelValid):
		return LabelValid
	case string(LabelInvalid):
		return LabelInvalid
	}
	return Label(label)
}
