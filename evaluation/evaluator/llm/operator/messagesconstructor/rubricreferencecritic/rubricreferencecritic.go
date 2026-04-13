//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rubricreferencecritic builds judge prompts for rubric-based evaluations against reference answers.
package rubricreferencecritic

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
	rubricReferenceCriticPrompt = `
# Mission

Your mission is to evaluate the quality of an AI agent’s final answer. You will be shown the user prompt (<user_prompt>), a reference answer (<reference_answer>), the agent’s response (<response>, which contains <final_answer>), and a rubric (<rubric>). You must use the rubric to judge whether the final answer reaches the quality and fidelity demonstrated by the reference answer while staying grounded in the user prompt.
Only respond to the rubric items provided. Do not invent new rubric items.

# Rubric

"yes": The final answer fulfills the rubric item. Accept paraphrases and different sentence structure when the answer keeps the same meaning, fidelity, and live-call quality as the reference answer.
"no": The final answer fails the rubric item, drifts away from the current play or decisive cue shown by the reference answer, becomes materially less specific or less natural, or cannot be verified from the user prompt plus the reference answer.

# Key Evaluation Principles

1. **Evaluate only the final answer**
   Judge only the quality of <final_answer>. Do not evaluate tool usage, chain-of-thought, or intermediate steps.

2. **Use the reference answer as a quality anchor, not an exact-match target**
   The reference answer shows the intended level of grounding, live-call sharpness, and detail. The final answer does not need to copy wording or sentence structure exactly, but it should preserve the same decisive play focus and comparable level of useful detail when supported by the user prompt.

3. **Restricted evidence sources**
   Base your judgment only on:
   * the original text of <user_prompt>,
   * the text of <reference_answer>, and
   * the text of <final_answer>.
   Do not use external knowledge, hidden assumptions, or guessed basketball context.

4. **Prefer grounded equivalence**
   Accept different wording when the final answer stays faithful to the same current play, actor, action, result, and decisive live cue that the reference answer highlights. Fail when the final answer becomes generic, misses an important grounded cue, or introduces unsupported specificity.

# Output Format (repeat this format for every rubric item, starting on a new line)

ID: [The ID of the rubric item, unique within the rubric. If the rubric itself is numbered 1..N, the ID must match that numbering.]
Rubric: [Repeat the rubric item word-for-word without any changes. Keep punctuation and capitalization exactly as-is. Do not translate or paraphrase.]
Evidence: [List the evidence text snippets relevant to this rubric item from <user_prompt>, <reference_answer>, and/or <final_answer>. If it cannot be unambiguously verified, explain why.]
Reason: [Explain your reasoning: how the evidence shows the final answer does or does not match the reference-quality expectation for this rubric item.]
Verdict: [yes|no]

REMEMBER: Your answer will help improve the AI agent. Even answering "no" can improve the agent. Respond in pure text, not json.

# Your Turn

## Input

<user_prompt>
<main_prompt>
{{.UserInput}}
</main_prompt>
</user_prompt>

<reference_answer>
{{.ExpectedFinalResponse}}
</reference_answer>

<response>
  <final_answer>
  {{.ActualFinalResponse}}
  </final_answer>
</response>

<rubric>
{{.Rubrics}}
</rubric>

## Output
`
	rubricReferenceCriticPromptTemplate = template.Must(
		template.New("rubricReferenceCriticPrompt").Parse(rubricReferenceCriticPrompt),
	)
)

type rubricReferenceCriticMessagesConstructor struct{}

// New returns a messages constructor for rubric criticism with reference answers.
func New() messagesconstructor.MessagesConstructor {
	return &rubricReferenceCriticMessagesConstructor{}
}

// ConstructMessages builds judge prompts for rubric criticism against reference answers.
func (e *rubricReferenceCriticMessagesConstructor) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	if len(actuals) == 0 {
		return nil, fmt.Errorf("actuals is empty")
	}
	if len(expecteds) == 0 {
		return nil, fmt.Errorf("expecteds is empty")
	}
	actual := actuals[len(actuals)-1]
	expected := expecteds[len(expecteds)-1]
	if expected.FinalResponse == nil {
		return nil, fmt.Errorf("expected final response is nil")
	}
	data := rubricReferenceCriticPromptData{
		UserInput:             content.ExtractTextFromContent(actual.UserContent),
		ExpectedFinalResponse: content.ExtractTextFromContent(expected.FinalResponse),
		ActualFinalResponse:   content.ExtractTextFromContent(actual.FinalResponse),
		Rubrics:               content.ExtractRubrics(evalMetric.Criterion.LLMJudge.Rubrics),
	}
	var buf bytes.Buffer
	if err := rubricReferenceCriticPromptTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute rubric reference critic prompt template: %w", err)
	}
	return []model.Message{
		{
			Role:    model.RoleUser,
			Content: buf.String(),
		},
	}, nil
}

type rubricReferenceCriticPromptData struct {
	UserInput             string
	ExpectedFinalResponse string
	ActualFinalResponse   string
	Rubrics               string
}
