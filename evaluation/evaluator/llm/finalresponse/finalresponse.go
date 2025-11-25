//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package finalresponse implements an LLM judge for final responses.
package finalresponse

import (
	"bytes"
	"context"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalresult"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evalset"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/evaluator/llm"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/metric"
	"trpc.group/trpc-go/trpc-agent-go/evaluation/status"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

var (
	// finalResponsePrompt is the template fed to the judge model.
	finalResponsePrompt = `You are an expert rater for an AI agent. The AI agent is going to call an API to answer the user query and generate API tool use code based for the choice of the API and API arguments. The ideal model response should be a function call that fulfills user query, or a natural language response hedges or asks users for further clarification if a function call does not apply.
	The primary focus of this rating task is to check correctness of the model responses.
	
	The data consists of:
	- A user query.
	- A model generated response for the prompt. The responses can consist of:
	  - Natural language, when the model is asking for clarification, or tells the user it does not possess the requested functionality / option.
	  - Code, in the form of one or multiple python function calls, and additional code as needed, for when the model is fulfilling the user request.
	You can use the help from a reference response annotated by a human rater. This reference response is of high quality. You can compare the agent's response with the reference response and decide if the agent's response is valid.
	Note sometimes the reference response only contains the key entities of the correct answer and you need to be flexible to allow the agent response to contain more information than the reference response, or to present the key entities in a different format or structure or in shorter or longer format.
	When the agent response is provided in the form of tables/dataframes or should be best provided in the form of tables/dataframes: focus on the key entities and main components requested in the user query and check whether you can retrieve those from the agent response. Likewise, if you have the reference response, then find out the key entities and main components in them and check whether you can retrieve those from the agent response. If the prompt does not specify any format instructions and the main items/components are included in the response then tolerate the differences in the formatting of those tables/dataframes.
	
	You should follow the constitutions below very carefully to rate the model response:
	- Allow flexibility of format even when reference code only uses one of the possible format, unless API spec or user prompt has explicit format requirement
	  - e.g. For state name, allow both abbreviation and full name unless API spec has explicit requirement. e.g. both 'tx' and 'Texas' should be allowed in the agent response even when reference code only uses one of them.
	  - e.g. If a reference response list outputs in a list format, the agent response is allowed to use sentence format and vice versa unless user prompt explicitly asks for a specific format.
	  - e.g. For numbers, allow flexibility of formatting, e.g. 1000000 vs 1,000,000.
	- The model shouldn't assume that it doesn't have access to according data or incapable of answering the question if reference response is able to find a legit answer.
	- If the model response contains the correct final answer, rate it as valid even when the model response contains more information than the reference response.
	- If the user prompt has csv or other table format data, don't read it yourself. Trust the reference response final answer instead.
	- When the validation needs maths, date calculations, do not use your own calculator. Trust the reference response final answer instead.
	- Be mindful about unit of numbers. For example, if the reference response says 100 miles, but the model response says 100 km, it is invalid.
	- When the agent response or the reference response is provided in the form of tables/dataframes: focus on the key entities and main components requested in the user query and check whether you can retrieve those from the agent response and whether those match the reference response. If the user query does not specify any format instructions and the main items/components are included in the response then tolerate the differences in the formatting of those tables/dataframes.
	- When the answer is in numeric format, check whether there are any format requirements in the numeric format, rounding, precision, number of decimals, etc. specified in the user query and the prompt. If there are no such instructions, then tolerate different numerical formats.
	- When the answer is in numeric format and there are rounding or precision differences between the agent response and the reference response, if no further instructions are provided evaluate if the rounding strategy or precision in the agent response follows the standards for that entity. For instance, model accuracy scores must be reported with at least two decimal places (e.g., 0.798 → 0.80 is acceptable,  but 0.7 is not).
	
	Below are the inputs:
	{
	  "User prompt": {{.UserPrompt}},
	  "Agent response": {{.ActualResponse}},
	  "Reference response": {{.ExpectedResponse}},
	}
	
	The answer should be a json alone which follows the json structure below:
	{
	  "reasoning": [reasoning],
	  "is_the_agent_response_valid": [valid or invalid],
	}
	Answer with assertiveness:
	`
	// finalResponsePromptTemplate renders the judge prompt with data.
	finalResponsePromptTemplate = template.Must(template.New("finalResponsePrompt").Parse(finalResponsePrompt))
	// labelMatchIsResponseValidRe extracts the validity label from judge output.
	labelMatchIsResponseValidRe = regexp.MustCompile(`"is_the_agent_response_valid"\s*:\s*\[?\s*"?([A-Za-z_]+)"?\s*\]?`)
)

// finalResponseEvaluator evaluates final responses via an LLM judge.
type finalResponseEvaluator struct {
	llmBaseEvaluator llm.LLMEvaluator
}

// New builds the final response evaluator.
func New() evaluator.Evaluator {
	e := &finalResponseEvaluator{}
	e.llmBaseEvaluator = llm.New(e)
	return e
}

// Name returns the evaluator identifier.
func (e *finalResponseEvaluator) Name() string {
	return "llm_final_response"
}

// Description describes the evaluator purpose.
func (e *finalResponseEvaluator) Description() string {
	return "LLM judge for final responses"
}

// Evaluate runs LLM-based evaluation on final responses.
func (e *finalResponseEvaluator) Evaluate(ctx context.Context, actuals, expecteds []*evalset.Invocation,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	return e.llmBaseEvaluator.Evaluate(ctx, actuals, expecteds, evalMetric)
}

// ConstructMessages builds judge prompts from actual and expected responses.
func (e *finalResponseEvaluator) ConstructMessages(actual, expected *evalset.Invocation,
	_ *metric.EvalMetric) ([]model.Message, error) {
	data := finalResponsePromptData{
		UserPrompt:       getTextFromContent(actual.UserContent),
		ActualResponse:   getTextFromContent(actual.FinalResponse),
		ExpectedResponse: getTextFromContent(expected.FinalResponse),
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

// ScoreBasedOnResponse converts judge feedback to a numeric score.
func (e *finalResponseEvaluator) ScoreBasedOnResponse(response *model.Response,
	_ *metric.EvalMetric) (*evalresult.ScoreResult, error) {
	if len(response.Choices) == 0 {
		return nil, fmt.Errorf("no choices in response")
	}
	responseText := response.Choices[0].Message.Content
	if responseText == "" {
		return nil, fmt.Errorf("empty response text")
	}
	label := extractLabel(responseText)
	score := 0.0
	switch label {
	case LabelValid:
		score = 1.0
	case LabelInvalid:
		score = 0.0
	default:
		return nil, fmt.Errorf("unknown label: %v", label)
	}
	return &evalresult.ScoreResult{
		Score: score,
	}, nil
}

// AggregateSamples resolves multiple judge samples to one invocation result.
func (e *finalResponseEvaluator) AggregateSamples(samples []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.PerInvocationResult, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("no samples")
	}
	positiveResults := make([]*evaluator.PerInvocationResult, 0)
	negativeResults := make([]*evaluator.PerInvocationResult, 0)
	for _, sample := range samples {
		if sample.Status == status.EvalStatusNotEvaluated {
			continue
		}
		if sample.Score >= evalMetric.Threshold {
			positiveResults = append(positiveResults, sample)
		} else {
			negativeResults = append(negativeResults, sample)
		}
	}
	if len(positiveResults) == 0 && len(negativeResults) == 0 {
		return samples[0], nil
	}
	if len(positiveResults) > len(negativeResults) {
		return positiveResults[0], nil
	} else {
		return negativeResults[0], nil
	}
}

// AggregateInvocations summarizes per-invocation results into an overall score.
func (e *finalResponseEvaluator) AggregateInvocations(results []*evaluator.PerInvocationResult,
	evalMetric *metric.EvalMetric) (*evaluator.EvaluateResult, error) {
	sumScore := 0.0
	numEvaluated := 0.0
	for _, result := range results {
		if result.Status == status.EvalStatusNotEvaluated {
			continue
		}
		numEvaluated++
		sumScore += result.Score
	}
	if numEvaluated == 0 {
		return &evaluator.EvaluateResult{
			OverallStatus: status.EvalStatusNotEvaluated,
		}, nil
	}
	overallScore := sumScore / numEvaluated
	overallStatus := status.EvalStatusPassed
	if overallScore < evalMetric.Threshold {
		overallStatus = status.EvalStatusFailed
	}
	return &evaluator.EvaluateResult{
		OverallScore:         overallScore,
		OverallStatus:        overallStatus,
		PerInvocationResults: results,
	}, nil
}

// finalResponsePromptData feeds values into the judge prompt template.
type finalResponsePromptData struct {
	UserPrompt       string // UserPrompt is the original user prompt text.
	ActualResponse   string // ActualResponse is the agent response to be judged.
	ExpectedResponse string // ExpectedResponse is the reference response for comparison.
}

// getTextFromContent extracts plain text from genai content.
func getTextFromContent(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var text strings.Builder
	for _, part := range content.Parts {
		text.WriteString(part.Text)
	}
	return text.String()
}

// Label captures the validity category returned by the judge.
type Label string

const (
	LabelValid   Label = "valid"   // LabelValid marks a valid agent response.
	LabelInvalid Label = "invalid" // LabelInvalid marks an invalid agent response.
)

// extractLabel extracts the validity label from the judge response.
func extractLabel(response string) Label {
	match := labelMatchIsResponseValidRe.FindStringSubmatch(response)
	if len(match) < 1 {
		return LabelInvalid
	}
	label := strings.TrimSpace(match[1])
	switch strings.ToLower(label) {
	case string(LabelValid):
		return LabelValid
	case string(LabelInvalid):
		return LabelInvalid
	}
	return Label(label)
}
