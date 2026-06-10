//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package verifierpairwise builds pairwise LLM verifier judge prompts.
package verifierpairwise

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"text/template"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/internal/content"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var verifierPromptTemplate = template.Must(template.New("verifierPrompt").Parse(`
# Mission

You are an expert evaluator of agent responses. You will see one user request and two candidate responses. Score Candidate A and Candidate B independently.

# Score Scale

Use exactly one of 20 score tokens from A to T for each candidate.

The scale is strictly ordered: A is best, T is worst, and every earlier letter is better than every later letter.

- A = clearly and completely satisfies the request under the evaluation guideline.
- B-D = satisfies the request with only minor issues.
- E-G = above average, mostly correct with some issues.
- H-J = uncertain, leans toward success.
- K-M = uncertain, leans toward failure.
- N-P = below average, significant issues remain.
- Q-S = failed with some partial progress.
- T = clearly and completely fails.

# Evaluation Criteria

{{.Criteria}}

# Evaluation Rules

Score each candidate only on the listed evaluation criteria. Ignore qualities that are not relevant to those criteria.

Use the user request as the task requirements. Do not trust a candidate's own claims of correctness unless the final answer itself satisfies the request.

Do not make the scores comparative by default. Candidate A and Candidate B can receive the same score if they have the same quality.

# Output Format

First write a concise analysis. Then output the final score tags exactly once:

<score_A>LETTER_A_TO_T</score_A>
<score_B>LETTER_A_TO_T</score_B>

# User Request

{{.UserInput}}

# Candidate A

{{.ActualResponse}}

# Candidate B

{{.ExpectedResponse}}
`))

type verifierMessagesConstructor struct {
}

// New returns a messages constructor for pairwise verifier evaluation.
func New() messagesconstructor.MessagesConstructor {
	return &verifierMessagesConstructor{}
}

// ConstructMessages builds pairwise verifier prompts.
func (c *verifierMessagesConstructor) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	if len(actuals) == 0 {
		return nil, fmt.Errorf("actuals is empty")
	}
	if len(expecteds) == 0 {
		return nil, fmt.Errorf("expecteds is empty")
	}
	if evalMetric == nil {
		return nil, fmt.Errorf("eval metric is nil")
	}
	if evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, fmt.Errorf("llm judge criterion is required")
	}
	actual := actuals[len(actuals)-1]
	expected := expecteds[len(expecteds)-1]
	if actual == nil {
		return nil, fmt.Errorf("actual invocation is nil")
	}
	if expected == nil {
		return nil, fmt.Errorf("expected invocation is nil")
	}
	if expected.FinalResponse == nil {
		return nil, fmt.Errorf("expected final response is required for llm_verifier_pairwise")
	}
	if actual.FinalResponse == nil {
		return nil, fmt.Errorf("actual final response is required for llm_verifier_pairwise")
	}
	criteria := criteriaText(evalMetric)
	if criteria == "" {
		return nil, fmt.Errorf("llm judge rubrics are required")
	}
	data := verifierPromptData{
		UserInput:        content.ExtractTextFromContent(actual.UserContent),
		ActualResponse:   content.ExtractTextFromContent(actual.FinalResponse),
		ExpectedResponse: content.ExtractTextFromContent(expected.FinalResponse),
		Criteria:         criteria,
	}
	var buf bytes.Buffer
	if err := verifierPromptTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute verifier prompt template: %w", err)
	}
	return []model.Message{{
		Role:    model.RoleUser,
		Content: buf.String(),
	}}, nil
}

type verifierPromptData struct {
	UserInput        string
	ActualResponse   string
	ExpectedResponse string
	Criteria         string
}

func criteriaText(evalMetric *metric.EvalMetric) string {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return ""
	}
	return strings.TrimSpace(content.ExtractRubrics(evalMetric.Criterion.LLMJudge.Rubrics))
}
