//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package finalresponse assembles judge prompts for evaluating final agent outputs.
package finalresponse

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/internal/content"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	// finalResponsePrompt is the template fed to the judge model.
	finalResponsePrompt = `
You are an expert evaluator for an AI agent (Agent: a model that executes tasks). Your job is to **only** judge whether the agent’s **final answer** matches the reference answer, and to output a fixed-format plain-text report.

### Core scoring rules

1. **The reference answer is the only Ground Truth (Ground Truth: the official “correct” answer used for evaluation).**
   No matter whether you personally think the reference answer might be wrong, outdated, or unreasonable, you **must** treat it as absolutely correct.

* Your job is not to fact-check or correct the reference answer, but to judge whether the agent’s answer is aligned with it.
* If the agent’s answer does not match the reference answer, then even if you think the agent is “more correct,” you must mark it **invalid**.

2. **Clarification questions are never allowed.**
   If the agent asks the user for more information, requests clarification, asks follow-up questions, or tells the user to provide missing conditions, it is considered **not completing the task**, and must be marked **invalid**.
   (Examples: “Please provide more details / what exactly do you want / can you share the date and location?”)

3. **No independent verification or calculation.**

* If the user prompt includes CSV (Comma-Separated Values, a table-like text format where values are separated by commas) or other tabular data: do **not** parse or calculate it yourself. Always follow the reference answer.
* If math, date arithmetic, or unit conversion is needed: do **not** compute it yourself. Always follow the reference answer.

### Input

You will receive three items:

* User prompt: the user’s question
* Agent response: the agent’s answer
* Reference response: the reference answer (the only Ground Truth)

Format:
User prompt: {{.UserPrompt}}
Agent response: {{.ActualResponse}}
Reference response: {{.ExpectedResponse}}

### Matching rules

As long as the meaning does not change, the following differences are allowed and can still be considered a match (**valid**):

* **Formatting differences**: list vs. paragraph; line breaks, punctuation, or slightly different ordering (as long as the key information is unchanged).
* **Equivalent writing**: different number formatting (e.g., 1000000 vs 1,000,000), different capitalization.
* **Paraphrases**: as long as the key entities (Key Entities: the critical items required by the answer) and main components clearly align with the reference answer.

Must mark **invalid** in typical cases:

* **Missing key information**: the agent does not include all key entities / core fields required by the reference answer.
* **Key information mismatch**: numbers, conclusions, objects, units, etc. differ from the reference answer.

  * Pay special attention to units: for example, if the reference answer is 100 miles but the agent writes 100 km, it must be **invalid**.
* **Clarification / deflection / refusal**: any response that asks for more input, turns into a question, or fails to directly provide the required result must be **invalid**.

### Output requirements

Your output must be plain text with fixed field types:

* reasoning: string. Briefly explain why you judged valid/invalid, pointing to the key aligned or misaligned points.
* is_the_agent_response_valid: string, must be either "valid" or "invalid".

Output structure (exactly two lines):
reasoning: [your reasoning]
is_the_agent_response_valid: [valid|invalid]

Requirement: be assertive and unambiguous; do not hedge.
`
	// finalResponsePromptTemplate renders the judge prompt with data.
	finalResponsePromptTemplate = template.Must(template.New("finalResponsePrompt").Parse(finalResponsePrompt))
)

type finalResponseMessagesConstructor struct {
}

// New returns a messages constructor for final responses.
func New() messagesconstructor.MessagesConstructor {
	return &finalResponseMessagesConstructor{}
}

// ConstructMessages builds judge prompts from actual and expected responses.
func (e *finalResponseMessagesConstructor) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	_ *metric.EvalMetric) ([]model.Message, error) {
	if len(actuals) == 0 {
		return nil, fmt.Errorf("actuals is empty")
	}
	if len(expecteds) == 0 {
		return nil, fmt.Errorf("expecteds is empty")
	}
	actual := actuals[len(actuals)-1]
	expected := expecteds[len(expecteds)-1]
	data := finalResponsePromptData{
		UserPrompt:       content.ExtractTextFromContent(actual.UserContent),
		ActualResponse:   content.ExtractTextFromContent(actual.FinalResponse),
		ExpectedResponse: content.ExtractTextFromContent(expected.FinalResponse),
	}
	var buf bytes.Buffer
	if err := finalResponsePromptTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute final response prompt template: %w", err)
	}
	return []model.Message{
		{
			Role:    model.RoleUser,
			Content: buf.String(),
		},
	}, nil
}

// finalResponsePromptData feeds values into the judge prompt template.
type finalResponsePromptData struct {
	UserPrompt       string // UserPrompt is the original user prompt text.
	ActualResponse   string // ActualResponse is the agent response to be judged.
	ExpectedResponse string // ExpectedResponse is the reference response for comparison.
}
