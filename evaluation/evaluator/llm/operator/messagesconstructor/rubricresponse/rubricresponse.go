//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package rubricresponse builds judge prompts for rubric-based evaluations.
package rubricresponse

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
	rubricResponsePrompt = `
# Mission

Your mission is to evaluate the quality of an AI agent’s final answer. You will be shown a user prompt (<user_prompt>), the agent’s response (<response>, which contains <final_answer>), and a rubric (<rubric>). You must use the rubric to objectively assess whether the agent’s final answer satisfies each rubric item.
Only respond to the rubric items provided. Do not invent new rubric items.

# Rubric

"yes": The final answer fulfills the rubric item, OR the rubric item’s condition was not applicable to the response.
"no": The rubric item is applicable but the final answer fails to fulfill it, OR the rubric item requires a fact/conclusion that cannot be unambiguously verified from <user_prompt> and <final_answer> (i.e., it is ambiguous or lacks checkable information).

# Key Evaluation Principles

1. **Evaluate final answer content only**
   You must evaluate only whether <final_answer> satisfies each rubric item in <rubric>. Do not evaluate tool usage, intermediate steps, chain-of-thought, or any process artifacts.

2. **Restricted evidence sources**
   Your judgment may only be based on:

* the original text of <user_prompt> (the user’s requirements and any given information), and
* the text of <final_answer> (the agent’s final output).
  Do not use external knowledge, common-sense guessing, or additional background to “fill in” missing information.

3. **Allow semantic equivalence**
   As long as the rubric item is still satisfied, accept different wording, formatting, and paraphrases.
   For numbers, accept numerically equivalent expressions (different representations), and allow minor rounding/precision differences as long as they do not change the final conclusion.

4. **Conditional rubric items (not applicable => yes)**
   If a rubric item is conditional (e.g., “If … then …”), you may mark it as not applicable and return "yes" only if you can clearly determine from <user_prompt> and <final_answer> that the condition is not met.
   If you cannot determine whether the condition is met, you may not mark it as “probably not applicable.” Treat it as not fulfilled (typically "no").

# Output Format (repeat this format for every rubric item, starting on a new line)

ID: [The ID of the rubric item, unique within the rubric. If the rubric itself is numbered 1..N, the ID must match that numbering.]
Rubric: [Repeat the rubric item word-for-word without any changes. Keep punctuation and capitalization exactly as-is. Do not translate or paraphrase.]
Evidence: [List the evidence text snippets relevant to this rubric item from <user_prompt> and/or <final_answer>. If no evidence is required to decide, explain why. If it cannot be unambiguously verified, explain why it cannot be verified.]
Reason: [Explain your reasoning: how the evidence supports/contradicts the final answer, or why the rubric item is not applicable.]
Verdict: [yes|no]

REMEMBER: Your answer will help improve the AI agent. It is important to determine whether rubric items are fulfilled correctly. Even answering "no" can improve the agent! Respond in pure text, not json.

# Your Turn

## Input

<user_prompt>
<main_prompt>
{{.UserInput}}
</main_prompt>
</user_prompt>

<response>
  <final_answer>
  {{.FinalResponse}}
  </final_answer>
</response>

<rubric>
{{.Rubrics}}
</rubric>

## Output
`
	// rubricResponsePromptTemplate renders the judge prompt with data.
	rubricResponsePromptTemplate = template.Must(template.New("rubricResponsePrompt").Parse(rubricResponsePrompt))
)

type rubricResponseMessagesConstructor struct {
}

// New returns a messages constructor for rubric responses.
func New() messagesconstructor.MessagesConstructor {
	return &rubricResponseMessagesConstructor{}
}

// ConstructMessages builds judge prompts for rubric responses.
func (e *rubricResponseMessagesConstructor) ConstructMessages(ctx context.Context, actuals, _ []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	if len(actuals) == 0 {
		return nil, fmt.Errorf("actuals is empty")
	}
	actual := actuals[len(actuals)-1]
	data := rubricResponsePromptData{
		UserInput:     content.ExtractTextFromContent(actual.UserContent),
		FinalResponse: content.ExtractTextFromContent(actual.FinalResponse),
		Rubrics:       content.ExtractRubrics(evalMetric.Criterion.LLMJudge.Rubrics),
	}
	var buf bytes.Buffer
	if err := rubricResponsePromptTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute rubric response prompt template: %w", err)
	}
	return []model.Message{
		{
			Role:    model.RoleUser,
			Content: buf.String(),
		},
	}, nil
}

// rubricResponsePromptData feeds values into the judge prompt template.
type rubricResponsePromptData struct {
	UserInput     string
	FinalResponse string
	Rubrics       string
}
