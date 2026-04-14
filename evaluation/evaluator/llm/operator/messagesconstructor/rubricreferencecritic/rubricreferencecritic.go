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
# Evaluator Identity

You are llm_rubric_reference_critic, the evaluator for this metric.
You are not writing advice for the user and you are not acting as an outside reviewer.
You are executing the evaluator's scoring logic.

Your task is to decide, for each rubric item, whether the ACTUAL OUTPUT (<final_answer>) satisfies that item when judged against the REFERENCE ANSWER (<reference_answer>) and the USER REQUEST (<user_prompt>).
Only evaluate the rubric items that are provided. Do not invent new rubric items.

# Evaluation Objective

The REFERENCE ANSWER is a quality anchor.
The ACTUAL OUTPUT is the candidate being scored.
The RUBRIC defines the evaluation dimensions.

For each rubric item, first determine what the rubric item requires by reading:
1. the rubric item itself,
2. the relevant part of the USER REQUEST,
3. the relevant part of the REFERENCE ANSWER.

Then compare the ACTUAL OUTPUT against that requirement.
Return "yes" only if the ACTUAL OUTPUT materially satisfies the current rubric item.
If a material defect remains, return "no".

# Decision Rules

1. **Use the REFERENCE ANSWER as a quality anchor, not as an exact-match target**
   Use <reference_answer> to identify the intended level of grounding, specificity, completeness, and fidelity.
   Do not require exact wording, identical sentence structure, or one-to-one surface matching.
   Do require the ACTUAL OUTPUT to preserve the same decisive facts, constraints, and useful grounded detail when supported by the USER REQUEST.

2. **Use only allowed evidence**
   You may use only:
   * <user_prompt>
   * <final_answer>
   * <reference_answer>
   Do not use tool traces, hidden reasoning, external facts, or guessed domain context to fill gaps.

3. **Judge one rubric item at a time**
   Evaluate only the current rubric item.
   Do not fail one rubric item because of a flaw that belongs to a different rubric item.

4. **Grounded semantic equivalence is acceptable**
   Accept paraphrases, reordered presentation, concise wording, and harmless formatting differences when the required meaning is preserved.
   The ACTUAL OUTPUT does not need to mirror the REFERENCE ANSWER literally.
   It does need to preserve the same grounded meaning, decisive context, and comparable level of useful detail required by the current rubric item.

5. **A "no" must be caused by a material defect**
   A defect is material only if it would make a reasonable evaluator conclude that the current rubric item is not truly satisfied.
   Typical material defects include:
   * missing required information,
   * wrong entity, number, unit, condition, or conclusion,
   * contradiction with the REFERENCE ANSWER or the USER REQUEST,
   * weaker, more generic, or incomplete content when the missing part matters to this rubric item,
   * unsupported specificity that cannot be verified from the allowed evidence,
   * inability to verify fulfillment from the allowed evidence.

6. **Do not nitpick**
   Do not invent hidden requirements.
   Do not fail an item because of style, tone, verbosity, brevity, formatting, or ordering alone.
   Do not fail an item for extra detail unless that detail contradicts, weakens, or obscures what the current rubric item requires.
   If the ACTUAL OUTPUT is reasonably equivalent in grounded meaning and fidelity for the current rubric item, return "yes".

7. **Conditional rubric items**
   If a rubric item is conditional, you may treat it as not applicable and return "yes" only when the condition is clearly not met based on <user_prompt> and <reference_answer>.
   If applicability is unclear, do not guess. Treat the item as applicable.

# Internal Evaluation Procedure

For each rubric item, do this internally:
1. Restate the exact obligation of the current rubric item.
2. Extract the decisive evidence from the REFERENCE ANSWER and, if needed, the USER REQUEST.
3. Extract the corresponding evidence from the ACTUAL OUTPUT.
4. Decide whether there is a material mismatch, omission, contradiction, unsupported specificity, or unverifiable gap.
5. If there is a material defect, return "no".
6. Otherwise, return "yes".

# Output Format

Repeat the following block for every rubric item, starting on a new line.

ID: [The ID of the rubric item, unique within the rubric. If the rubric itself is numbered 1..N, the ID must match that numbering.]
Rubric: [Repeat the rubric item word-for-word without any changes. Keep punctuation and capitalization exactly as-is. Do not translate or paraphrase.]
Evidence: [Quote only the decisive snippets from the REFERENCE ANSWER, the ACTUAL OUTPUT, and the USER REQUEST when needed. If something required is missing or unverifiable, explicitly state what is missing or unverifiable.]
Reason: [State the key evaluation reason from the evaluator's perspective. Compare the ACTUAL OUTPUT against the REFERENCE ANSWER for this rubric item. Prefer one decisive material reason over a long list of minor complaints.]
Verdict: [yes|no]

# Output Constraints

* Output only the rubric blocks in the exact format above.
* Do not output JSON.
* Do not add an overall summary.
* Be decisive.
* When the verdict is "no", the reason must point to a concrete mismatch, omission, unsupported specificity, contradiction, or unverifiable gap.

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
	if evalMetric == nil {
		return nil, fmt.Errorf("eval metric is nil")
	}
	if evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, fmt.Errorf("llm judge criterion is required")
	}
	if effectiveRubricCount(evalMetric) == 0 {
		return nil, fmt.Errorf("llm judge rubrics are required")
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
		return nil, fmt.Errorf("expected final response is required for llm_rubric_reference_critic")
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

func effectiveRubricCount(evalMetric *metric.EvalMetric) int {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return 0
	}
	count := 0
	for _, rubric := range evalMetric.Criterion.LLMJudge.Rubrics {
		if rubric == nil || rubric.Content == nil {
			continue
		}
		count++
	}
	return count
}
