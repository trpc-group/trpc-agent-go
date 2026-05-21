//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rubricknowledgerecall builds judge prompts for knowledge recall evaluation.
package rubricknowledgerecall

import (
	"bytes"
	"context"
	"fmt"
	"text/template"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/internal/rubrics"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/internal/content"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	rubricKnowledgeRecallPrompt = `
# Mission

Your mission is to evaluate whether the retrieved knowledge (<retrieved_knowledge>) is relevant to the user question (<user_prompt>), and whether it is sufficient to support each rubric item in the rubric (<rubric>).
You will be given: the user question (<user_prompt>), the retrieved documents (<retrieved_knowledge>), and the rubric (<rubric>).
Only respond to the rubric items provided. Do not invent new rubric items.

# Rubric

Score 1: The retrieved knowledge **directly supports** the key information required by the rubric item, OR the rubric item’s condition is clearly not applicable to this user question.
Score 0: The rubric item is applicable, but the retrieved knowledge is missing, insufficient, only broadly on-topic, or clearly irrelevant, and therefore cannot support the rubric item.

# Key Evaluation Principles

1. **Trusted evidence comes from retrieved documents only**
   You may only use content from <retrieved_knowledge> as trusted evidence. Do not use any final answer text, model reasoning, external knowledge, or common-sense guessing to fill in missing information.

2. **Relevance first, and it must be answerable**
   Even if the retrieved knowledge contains correct facts, it must be relevant to the user’s intent and usable for satisfying the corresponding rubric item.
   If it is merely “same topic / loosely related / generic background” but does not support the required information for the rubric item, use score 0.

3. **Sufficiency must be judged with an operational test**
   For each rubric item, use the following test to determine whether the retrieval is “sufficient”:

* Imagine an answerer who can only see <retrieved_knowledge> (no external knowledge, no guessing).
* If they could complete the rubric item using only that retrieved knowledge (i.e., produce the key conclusion/elements required), then use score 1.
* If they could not (missing key entities, steps, numbers, conditions, definitions, etc.), then use score 0.

4. **Evidence must be close to the original text; no abstract fabrication**
   Evidence must come from <retrieved_knowledge>, with these requirements:

* Prefer **verbatim excerpts** (you may truncate, but must not change meaning).
* If you must paraphrase, it must be a **near-verbatim** paraphrase that can be directly located in the documents.
* If <retrieved_knowledge> contains document IDs/titles/sectioning, cite the source location inside the reason string (e.g., “Doc 2 / Paragraph 3”). If there is no numbering, include enough raw text in the reason to make the source identifiable.

5. **Conditional rubric items (not applicable => score 1)**
   If a rubric item is conditional (e.g., “If … then …”):

* You may use score 1 as not applicable only if you can **clearly determine from <user_prompt>** that the condition is not met.
* If you cannot determine whether the condition is met, treat the item as applicable; if retrieval is insufficient, use score 0.
* When you use score 1 due to not-applicable, your reason must explicitly state what part of <user_prompt> makes it not applicable.

# Internal steps for each rubric item (for internal analysis only)

1. Understand the rubric item and the key evaluation principles.
2. Collect relevant excerpts from <retrieved_knowledge> as evidence.
3. Judge whether the evidence is relevant and sufficient (using the sufficiency test).
4. Choose score 1 or score 0 and state the decisive reason.
   Note: Do not output these steps as separate sections. The final JSON reason should still include the decisive evidence and judgment.

# Output Format

Return a single valid JSON object and nothing else:

{
  "rubricScores": [
    {
      "id": "[The ID of the rubric item, unique within the rubric. If the rubric is numbered 1..N, the ID must match that numbering.]",
      "score": 0,
      "reason": "[Evidence: cite source-labeled verbatim excerpts, or near-verbatim paraphrases, from <retrieved_knowledge>. Judgment: state whether the evidence is relevant and sufficient for this rubric item; if insufficient, state the missing key information; if not applicable, cite the part of <user_prompt> that makes it not applicable.]"
    }
  ]
}

# Output Rules

Produce exactly one rubricScores item for each input rubric item, in the same order. Use the exact input rubric ID; do not add, omit, merge, split, translate, or rename IDs.

Set score to 1 only when <retrieved_knowledge> directly supports the rubric item, or when the item is clearly not applicable to <user_prompt>. Set score to 0 when the retrieved knowledge is missing, insufficient, loosely related, irrelevant, or cannot support the required answer. The numeric score in the example is not a default.

Write reason as one concise evaluator note containing both source-labeled evidence and judgment. Do not add separate Rubric, Evidence, Reason, or Verdict fields.

Return JSON only: double-quote keys and strings, escape quotes/newlines inside strings, and do not include markdown, comments, trailing commas, summaries, or extra fields.

# Your Turn

## Input

<user_prompt>
<main_prompt>
{{.UserInput}}
</main_prompt>
</user_prompt>

<retrieved_knowledge>
{{.RetrievedKnowledge}}
</retrieved_knowledge>

<rubric>
{{.Rubrics}}
</rubric>

## Output
`
	rubricKnowledgeRecallPromptTemplate = template.Must(template.New("rubricKnowledgeRecallPrompt").Parse(rubricKnowledgeRecallPrompt))
)

type rubricKnowledgeRecallMessagesConstructor struct {
}

// New returns a messages constructor for knowledge recall.
func New() messagesconstructor.MessagesConstructor {
	return &rubricKnowledgeRecallMessagesConstructor{}
}

// ConstructMessages builds judge prompts for knowledge recall evaluation.
func (e *rubricKnowledgeRecallMessagesConstructor) ConstructMessages(ctx context.Context, actuals, _ []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	if len(actuals) == 0 {
		return nil, fmt.Errorf("actuals is empty")
	}
	if evalMetric == nil {
		return nil, fmt.Errorf("eval metric is nil")
	}
	if evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, fmt.Errorf("llm judge criterion is required")
	}
	if rubrics.Count(evalMetric) == 0 {
		return nil, fmt.Errorf("llm judge rubrics are required")
	}
	actual := actuals[len(actuals)-1]
	retrieved, err := content.ExtractKnowledgeRecall(actual.Tools)
	if err != nil {
		return nil, fmt.Errorf("extract knowledge recall: %w", err)
	}
	if retrieved == "" {
		retrieved = "No knowledge search results were found."
	}
	data := rubricKnowledgeRecallPromptData{
		UserInput:          content.ExtractTextFromContent(actual.UserContent),
		RetrievedKnowledge: retrieved,
		Rubrics:            content.ExtractRubrics(evalMetric.Criterion.LLMJudge.Rubrics),
	}
	var buf bytes.Buffer
	if err := rubricKnowledgeRecallPromptTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute rubric knowledge recall prompt template: %w", err)
	}
	return []model.Message{
		{
			Role:    model.RoleUser,
			Content: buf.String(),
		},
	}, nil
}

// StructuredOutput returns the structured output schema for knowledge recall evaluation.
func (e *rubricKnowledgeRecallMessagesConstructor) StructuredOutput(ctx context.Context,
	actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*model.StructuredOutput, error) {
	visibleRubrics, err := rubrics.ValidateStructured(evalMetric)
	if err != nil {
		return nil, err
	}
	return rubrics.ScoresOutput(
		"rubric_knowledge_recall_scores",
		"Per-rubric binary scores and reasons for knowledge recall evaluation.",
		visibleRubrics,
	), nil
}

type rubricKnowledgeRecallPromptData struct {
	UserInput          string
	RetrievedKnowledge string
	Rubrics            string
}
