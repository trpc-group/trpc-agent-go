//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package hallucination builds judge prompts for hallucination evaluation.
package hallucination

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/internal/judger"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/messagesconstructor/internal/content"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	noValidationContext = "No validation context was captured."
	segmentationPrompt  = `
# Mission

Segment the final answer into sentence-level or bullet-level claims.
You must preserve the original wording and punctuation exactly.

# Rules

1. Output one claim per line using the exact format <sentence>...</sentence>.
2. Cover the whole final answer in order.
3. Split bullet items and numbered items into separate claims when they carry separate meaning.
4. Keep short stylistic or process-only statements as their own claims if they appear in the final answer.
5. Do not add explanations, numbering, or any text outside <sentence> tags.
6. If the final answer is empty, output exactly:
<sentence>[EMPTY_RESPONSE]</sentence>

# Input

<final_answer>
{{.FinalResponse}}
</final_answer>
`
	segmentationPromptTemplate = template.Must(template.New("segmentationPrompt").Parse(segmentationPrompt))
	validatorPrompt            = `
# Mission

Your mission is to detect hallucinations in the agent's segmented final answer.
You will be given:
- a textual context captured during execution, and
- segmented sentences from the final answer.

Evaluate every input sentence against the textual context.
You must cover the whole segmented answer.

# Labels

supported: The sentence is fully supported by the textual context.
unsupported: The sentence is not supported by the textual context.
contradictory: The textual context directly contradicts the sentence.
disputed: The textual context contains both supporting and contradicting evidence.
not_applicable: The sentence does not require factual grounding, such as greetings, stylistic fillers, or process-only statements.

# Key Evaluation Principles

1. Only use the provided textual context as trusted evidence.
   Do not use external knowledge, common-sense guessing, or the final answer itself as evidence.
2. Be strict.
   If the textual context does not fully support a sentence, label it as unsupported.
3. Use the sentence IDs from the segmented input exactly.
   Do not renumber, merge, or skip them.
4. Keep the reason concise.
   Mention the decisive grounding signal briefly when useful.
5. Map labels to verdicts exactly as follows.
   supported => yes
   not_applicable => yes
   unsupported => no
   contradictory => no
   disputed => no
6. If the final answer is empty, output exactly one block with:
   ID: 1
   Reason: No final answer was produced.
   Label: unsupported
   Verdict: no

# Output Format

Repeat the following block for every sentence, starting with a new line:

ID: [1..N]
Reason: [A concise explanation of the label.]
Label: [supported|unsupported|contradictory|disputed|not_applicable]
Verdict: [yes|no]

Output only these blocks.

# Input

<context>
{{.ValidationContext}}
</context>

<segmented_sentences>
{{.SegmentedSentences}}
</segmented_sentences>
`
	validatorPromptTemplate = template.Must(template.New("validatorPrompt").Parse(validatorPrompt))
	sentenceRegex           = regexp.MustCompile(`(?s)<sentence>(.*?)</sentence>`)
)

type hallucinationMessagesConstructor struct {
}

// New returns a messages constructor for hallucination evaluation.
func New() messagesconstructor.MessagesConstructor {
	return &hallucinationMessagesConstructor{}
}

// ConstructMessages builds judge prompts for hallucination evaluation.
func (e *hallucinationMessagesConstructor) ConstructMessages(ctx context.Context, actuals, _ []*evalset.Invocation,
	evalMetric *metric.EvalMetric) ([]model.Message, error) {
	if len(actuals) == 0 {
		return nil, fmt.Errorf("actuals is empty")
	}
	actual := actuals[len(actuals)-1]
	groundingContext, err := buildValidationContext(actuals)
	if err != nil {
		return nil, fmt.Errorf("extract grounding context: %w", err)
	}
	data := hallucinationPromptData{
		ValidationContext: groundingContext,
		FinalResponse:     content.ExtractTextFromContent(actual.FinalResponse),
	}
	sentences, err := buildSegmentedSentences(ctx, data.FinalResponse, evalMetric)
	if err != nil {
		return nil, fmt.Errorf("build segmented sentences: %w", err)
	}
	var buf bytes.Buffer
	if err := validatorPromptTemplate.Execute(&buf, hallucinationPromptData{
		ValidationContext:  data.ValidationContext,
		SegmentedSentences: sentences,
	}); err != nil {
		return nil, fmt.Errorf("execute validator prompt template: %w", err)
	}
	return []model.Message{
		{
			Role:    model.RoleUser,
			Content: buf.String(),
		},
	}, nil
}

func buildValidationContext(actuals []*evalset.Invocation) (string, error) {
	contexts := make([]string, 0, len(actuals))
	for i, actual := range actuals {
		groundingContext, err := content.ExtractGroundingContext(actual)
		if err != nil {
			return "", fmt.Errorf("actual %d: %w", i, err)
		}
		if groundingContext == noValidationContext {
			continue
		}
		contexts = append(contexts, groundingContext)
	}
	if len(contexts) == 0 {
		return noValidationContext, nil
	}
	return strings.Join(contexts, "\n\n"), nil
}

func buildSegmentedSentences(ctx context.Context, finalResponse string, evalMetric *metric.EvalMetric) (string, error) {
	var buf bytes.Buffer
	if err := segmentationPromptTemplate.Execute(&buf, hallucinationPromptData{FinalResponse: finalResponse}); err != nil {
		return "", fmt.Errorf("execute segmentation prompt template: %w", err)
	}
	response, err := judger.Judge(ctx, []model.Message{{
		Role:    model.RoleUser,
		Content: buf.String(),
	}}, evalMetric)
	if err != nil {
		return "", fmt.Errorf("execute segmentation judge: %w", err)
	}
	if response == nil || len(response.Choices) == 0 {
		return "", fmt.Errorf("segmentation response is empty")
	}
	matches := sentenceRegex.FindAllStringSubmatch(response.Choices[0].Message.Content, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("no segmented sentences found in response")
	}
	sentences := make([]string, 0, len(matches))
	for i, match := range matches {
		sentence := strings.TrimSpace(match[1])
		if sentence == "" {
			continue
		}
		sentences = append(sentences, fmt.Sprintf("<sentence id=\"%d\">\n%s\n</sentence>", i+1, sentence))
	}
	if len(sentences) == 0 {
		return "", fmt.Errorf("no non-empty segmented sentences found in response")
	}
	return strings.Join(sentences, "\n"), nil
}

type hallucinationPromptData struct {
	ValidationContext  string
	FinalResponse      string
	SegmentedSentences string
}
