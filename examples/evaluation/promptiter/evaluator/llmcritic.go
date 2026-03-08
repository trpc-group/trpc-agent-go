//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package evaluator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"text/template"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	evalevaluator "trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/invocationsaggregator/average"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm/operator/samplesaggregator/majorityvote"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	criterionllm "trpc.group/trpc-go/trpc-agent-go/evaluation/metric/criterion/llm"
	promptissue "trpc.group/trpc-go/trpc-agent-go/evaluation/workflow/promptiterator/issue"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type llmCriticEvaluator struct {
	llmBaseEvaluator llm.LLMEvaluator
	judgeTmpl        *template.Template
	outputSchema     *jsonschema.Schema
}

type judgeOutput struct {
	// Rubrics contains per-rubric verdicts produced by the judge.
	Rubrics []judgeRubric `json:"rubrics,omitempty"`
	// Issues are normalized prompt issues suggested by the judge.
	Issues []promptissue.Issue `json:"issues,omitempty"`
}

type judgeRubric struct {
	// ID is the rubric identifier.
	ID string `json:"id,omitempty"`
	// Verdict is the rubric verdict value.
	Verdict string `json:"verdict,omitempty"`
	// Reason explains the rubric verdict.
	Reason string `json:"reason,omitempty"`
}

// NewLLMCritic creates an LLM critic evaluator using a judge prompt template and an output schema.
func NewLLMCritic(judgePromptPath string, outputSchemaPath string) (evalevaluator.Evaluator, error) {
	if strings.TrimSpace(judgePromptPath) == "" {
		return nil, errors.New("judge prompt path is empty")
	}
	if strings.TrimSpace(outputSchemaPath) == "" {
		return nil, errors.New("judge output schema path is empty")
	}
	judgePromptBytes, err := os.ReadFile(judgePromptPath)
	if err != nil {
		return nil, fmt.Errorf("read judge prompt: %w", err)
	}
	judgeTmpl, err := template.New("judge_critic").Parse(string(judgePromptBytes))
	if err != nil {
		return nil, fmt.Errorf("parse judge prompt template: %w", err)
	}
	schemaBytes, err := os.ReadFile(outputSchemaPath)
	if err != nil {
		return nil, fmt.Errorf("read schema: %w", err)
	}
	s, err := compileJSONSchemaBytes(schemaBytes)
	if err != nil {
		return nil, err
	}
	e := &llmCriticEvaluator{
		judgeTmpl:    judgeTmpl,
		outputSchema: s,
	}
	e.llmBaseEvaluator = llm.New(e)
	return e, nil
}

func (e *llmCriticEvaluator) Name() string {
	return "llm_critic"
}

func (e *llmCriticEvaluator) Description() string {
	return "Evaluates candidate output with an LLM judge, using expected outputs as references"
}

func (e *llmCriticEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) (*evalevaluator.EvaluateResult, error) {
	return e.llmBaseEvaluator.Evaluate(ctx, actuals, expecteds, evalMetric)
}

func (e *llmCriticEvaluator) ConstructMessages(ctx context.Context, actuals, expecteds []*evalset.Invocation, evalMetric *metric.EvalMetric) ([]model.Message, error) {
	if e.judgeTmpl == nil {
		return nil, errors.New("judge template is nil")
	}
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, errors.New("llm judge criterion not configured")
	}
	if len(actuals) == 0 {
		return nil, errors.New("actuals is empty")
	}
	if len(expecteds) == 0 {
		return nil, errors.New("expecteds is empty")
	}
	actual := actuals[len(actuals)-1]
	if actual == nil || actual.UserContent == nil {
		return nil, errors.New("actual user content is nil")
	}
	if actual.FinalResponse == nil {
		return nil, errors.New("actual final response is nil")
	}
	expected := expecteds[len(expecteds)-1]
	if expected == nil || expected.FinalResponse == nil {
		return nil, errors.New("expected final response is nil")
	}
	rubricsText := formatRubrics(evalMetric.Criterion.LLMJudge.Rubrics)
	prompt, err := renderJudgePrompt(e.judgeTmpl, judgePromptData{
		UserInput:       actual.UserContent.Content,
		CandidateOutput: actual.FinalResponse.Content,
		TeacherOutput:   expected.FinalResponse.Content,
		Rubrics:         rubricsText,
	})
	if err != nil {
		return nil, fmt.Errorf("render judge prompt: %w", err)
	}
	return []model.Message{model.NewUserMessage(prompt)}, nil
}

func (e *llmCriticEvaluator) ScoreBasedOnResponse(ctx context.Context, resp *model.Response, evalMetric *metric.EvalMetric) (*evalevaluator.ScoreResult, error) {
	if evalMetric == nil || evalMetric.Criterion == nil || evalMetric.Criterion.LLMJudge == nil {
		return nil, errors.New("llm judge criterion not configured")
	}
	if resp == nil {
		return nil, errors.New("judge response is nil")
	}
	raw, err := extractJudgeOutput(resp)
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, fmt.Errorf("judge output is not valid JSON: %w", err)
	}
	if e.outputSchema == nil {
		return nil, errors.New("judge output schema is nil")
	}
	if err := e.outputSchema.Validate(v); err != nil {
		return nil, fmt.Errorf("judge output schema validation failed: %w", err)
	}
	var parsed judgeOutput
	if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
		return nil, fmt.Errorf("unmarshal judge output: %w", err)
	}
	score, rubricScores := scoreFromJudgeOutput(evalMetric, parsed)
	return &evalevaluator.ScoreResult{
		Reason:       raw,
		Score:        score,
		RubricScores: rubricScores,
	}, nil
}

func (e *llmCriticEvaluator) AggregateSamples(ctx context.Context, samples []*evalevaluator.PerInvocationResult, evalMetric *metric.EvalMetric) (*evalevaluator.PerInvocationResult, error) {
	return majorityvote.New().AggregateSamples(ctx, samples, evalMetric)
}

func (e *llmCriticEvaluator) AggregateInvocations(ctx context.Context, results []*evalevaluator.PerInvocationResult, evalMetric *metric.EvalMetric) (*evalevaluator.EvaluateResult, error) {
	return average.New().AggregateInvocations(ctx, results, evalMetric)
}

func compileJSONSchemaBytes(schemaBytes []byte) (*jsonschema.Schema, error) {
	resourceName := "schema.json"
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(resourceName, strings.NewReader(string(schemaBytes))); err != nil {
		return nil, fmt.Errorf("add schema resource: %w", err)
	}
	s, err := compiler.Compile(resourceName)
	if err != nil {
		return nil, fmt.Errorf("compile schema: %w", err)
	}
	return s, nil
}

type judgePromptData struct {
	// UserInput is the raw user message content.
	UserInput string
	// CandidateOutput is the candidate model output content.
	CandidateOutput string
	// TeacherOutput is the teacher reference output.
	TeacherOutput string
	// Rubrics is the formatted rubric list for the judge.
	Rubrics string
}

func formatRubrics(rubrics []*criterionllm.Rubric) string {
	if len(rubrics) == 0 {
		return ""
	}
	var b strings.Builder
	for _, r := range rubrics {
		if r == nil || r.Content == nil {
			continue
		}
		b.WriteString("- ")
		b.WriteString(r.ID)
		b.WriteString(": ")
		b.WriteString(strings.TrimSpace(r.Content.Text))
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func renderJudgePrompt(tmpl *template.Template, data judgePromptData) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func extractJudgeOutput(resp *model.Response) (string, error) {
	if resp == nil {
		return "", errors.New("judge response is nil")
	}
	if len(resp.Choices) == 0 {
		return "", errors.New("judge response has no choices")
	}
	msg := resp.Choices[0].Message
	raw := strings.TrimSpace(msg.Content)
	if raw != "" {
		return raw, nil
	}
	finishReason := ""
	if resp.Choices[0].FinishReason != nil {
		finishReason = *resp.Choices[0].FinishReason
	}
	usage := ""
	if resp.Usage != nil {
		usage = fmt.Sprintf(", completion_tokens=%d, total_tokens=%d", resp.Usage.CompletionTokens, resp.Usage.TotalTokens)
	}
	return "", fmt.Errorf("judge returned empty content (finish_reason=%s, tool_calls=%d%s)", finishReason, len(msg.ToolCalls), usage)
}

func scoreFromJudgeOutput(evalMetric *metric.EvalMetric, out judgeOutput) (float64, []*evalresult.RubricScore) {
	wanted := evalMetric.Criterion.LLMJudge.Rubrics
	byID := make(map[string]judgeRubric, len(out.Rubrics))
	for _, r := range out.Rubrics {
		byID[r.ID] = r
	}
	total := 0.0
	rubricScores := make([]*evalresult.RubricScore, 0, len(wanted))
	for _, w := range wanted {
		id := w.ID
		r, ok := byID[id]
		verdict := "no"
		reason := "Missing rubric verdict."
		if ok {
			verdict = strings.ToLower(strings.TrimSpace(r.Verdict))
			reason = strings.TrimSpace(r.Reason)
		}
		score := 0.0
		if verdict == "yes" {
			score = 1.0
		}
		total += score
		rubricScores = append(rubricScores, &evalresult.RubricScore{
			ID:     id,
			Reason: reason,
			Score:  score,
		})
	}
	if len(wanted) == 0 {
		return 0.0, rubricScores
	}
	return total / float64(len(wanted)), rubricScores
}
