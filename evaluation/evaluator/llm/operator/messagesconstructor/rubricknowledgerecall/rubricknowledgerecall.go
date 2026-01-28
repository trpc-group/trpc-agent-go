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

"yes": The retrieved knowledge **directly supports** the key information required by the rubric item, OR the rubric item’s condition is clearly not applicable to this user question.
"no": The rubric item is applicable, but the retrieved knowledge is missing, insufficient, only broadly on-topic, or clearly irrelevant, and therefore cannot support the rubric item.

# Key Evaluation Principles

1. **Trusted evidence comes from retrieved documents only**
   You may only use content from <retrieved_knowledge> as trusted evidence. Do not use any final answer text, model reasoning, external knowledge, or common-sense guessing to fill in missing information.

2. **Relevance first, and it must be answerable**
   Even if the retrieved knowledge contains correct facts, it must be relevant to the user’s intent and usable for satisfying the corresponding rubric item.
   If it is merely “same topic / loosely related / generic background” but does not support the required information for the rubric item, answer "no".

3. **Sufficiency must be judged with an operational test**
   For each rubric item, use the following test to determine whether the retrieval is “sufficient”:

* Imagine an answerer who can only see <retrieved_knowledge> (no external knowledge, no guessing).
* If they could complete the rubric item using only that retrieved knowledge (i.e., produce the key conclusion/elements required), then the item may be "yes".
* If they could not (missing key entities, steps, numbers, conditions, definitions, etc.), then it must be "no".

4. **Evidence must be close to the original text; no abstract fabrication**
   Evidence must come from <retrieved_knowledge>, with these requirements:

* Prefer **verbatim excerpts** (you may truncate, but must not change meaning).
* If you must paraphrase, it must be a **near-verbatim** paraphrase that can be directly located in the documents.
* If <retrieved_knowledge> contains document IDs/titles/sectioning, you must cite the source location in Evidence (e.g., “Doc 2 / Paragraph 3”). If there is no numbering, include enough raw text to make the source identifiable.

5. **Conditional rubric items (not applicable => yes)**
   If a rubric item is conditional (e.g., “If … then …”):

* You may return "yes" as not applicable only if you can **clearly determine from <user_prompt>** that the condition is not met.
* If you cannot determine whether the condition is met, treat the item as applicable; if retrieval is insufficient, return "no".
* When you return "yes" due to not-applicable, your Reason must explicitly state what part of <user_prompt> makes it not applicable.

6. **No extra output**
   You must output only the per-item evaluations in the format below. Do not add any overall summary or additional commentary.

# Internal steps for each rubric item (for internal analysis only)

1. Understand the rubric item and the key evaluation principles.
2. Collect relevant excerpts from <retrieved_knowledge> as evidence.
3. Judge whether the evidence is relevant and sufficient (using the sufficiency test).
4. Output the verdict in the required format.
   Note: These steps are for your internal analysis only and must not be output.

# Output Format (repeat this format for every rubric item, starting with a new line)

ID: [The ID of the rubric item, unique within the rubric. If the rubric is numbered 1..N, the ID must match that numbering.]
Rubric: [Repeat the rubric item word-for-word without any changes. Keep punctuation and capitalization exactly as-is. Do not translate or paraphrase.]
Evidence: [Relevant verbatim excerpts (or near-verbatim paraphrases) from <retrieved_knowledge>, with source/location where possible. If there is no relevant evidence, write “none”.]
Reason: [Explain why the evidence directly supports the rubric item, or why evidence is missing/insufficient/irrelevant; or explain why the item is not applicable, and cite which part of <user_prompt> establishes that.]
Verdict: [yes|no]

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

type rubricKnowledgeRecallPromptData struct {
	UserInput          string
	RetrievedKnowledge string
	Rubrics            string
}
