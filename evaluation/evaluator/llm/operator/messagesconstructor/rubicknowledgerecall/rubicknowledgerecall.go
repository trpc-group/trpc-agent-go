//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package rubicknowledgerecall builds judge prompts for knowledge recall evaluation.
package rubicknowledgerecall

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"text/template"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/internal/content"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	rubicKnowledgeRecallPrompt = `
SPECIAL INSTRUCTION: think silently. Silent thinking token budget: 10240 tokens.

# Mission
You are grading whether the retrieved knowledge is relevant to the user prompt. You will be given the user question (<user_prompt>), the retrieved documents (<retrieved_knowledge>), and a set of properties (<property>) to judge.
Only respond to the properties provided. Do not make up new properties.

# Rubric
"yes": The retrieved knowledge supports the property or the property's condition is not applicable.
"no": The property applies but the retrieved knowledge is missing or irrelevant.

# Key Evaluation Principles
1. Trusted evidence comes from the retrieved documents only. Ignore any final answer text.
2. Judge if the retrieved knowledge contains enough relevant information to satisfy each property. If evidence is missing, answer "no".
3. Be concise and faithful to the provided documents. Do not invent facts.

For each property follow these internal steps:
1. Understand the property and the key evaluation principles.
2. Collect relevant snippets from the retrieved knowledge as evidence.
3. Judge whether the evidence is sufficient and relevant.
4. Output the verdict in the required format.

# Output Format (repeat this format for every property, starting with a new line):
ID: [The ID of the property, unique within the rubric.]
Property: [Repeat the property exactly.]
Evidence: [List trusted evidence from retrieved documents relevant to the property. If none, say so.]
Reason: [Explain why the evidence supports or contradicts the property, or why the property is not applicable.]
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
	rubicKnowledgeRecallPromptTemplate = template.Must(template.New("rubicKnowledgeRecallPrompt").Parse(rubicKnowledgeRecallPrompt))
)

type rubicKnowledgeRecallMessagesConstructor struct {
}

func New() messagesconstructor.MessagesConstructor {
	return &rubicKnowledgeRecallMessagesConstructor{}
}

func (e *rubicKnowledgeRecallMessagesConstructor) ConstructMessages(ctx context.Context, actual, _ *evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	if actual == nil {
		return nil, fmt.Errorf("actual invocation is nil")
	}
	retrieved, err := content.ExtractKnowledgeRecall(actual.IntermediateData)
	if err != nil {
		return nil, fmt.Errorf("extract knowledge recall: %w", err)
	}
	if retrieved == "" {
		retrieved = "No knowledge search results were found."
	}
	data := rubicKnowledgeRecallPromptData{
		UserInput:          content.ExtractTextFromContent(actual.UserContent),
		RetrievedKnowledge: retrieved,
		Rubrics:            content.ExtractRubrics(evalMetric.Criterion.LLMJudge.Rubrics),
	}
	var buf bytes.Buffer
	if err := rubicKnowledgeRecallPromptTemplate.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute rubic knowledge recall prompt template: %w", err)
	}
	log.Println(buf.String())
	return []model.Message{
		{
			Role:    model.RoleUser,
			Content: buf.String(),
		},
	}, nil
}

type rubicKnowledgeRecallPromptData struct {
	UserInput          string
	RetrievedKnowledge string
	Rubrics            string
}
