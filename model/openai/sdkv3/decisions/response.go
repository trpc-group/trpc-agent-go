//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package decisions

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/respjson"
	"trpc.group/trpc-go/trpc-agent-go/decision"
)

const jsonNumberPrefixes = "-0123456789"

func projectResponse(req decision.Request, resp *openai.Decision, requestID string) (*decision.Response, error) {
	if resp == nil || !hasJSONType(resp.JSON.Model, `"`) || resp.Model == "" ||
		!hasJSONType(resp.JSON.Answers, "[") || resp.Answers == nil {
		return nil, fmt.Errorf("openai decisions: response is missing a model or answers array")
	}
	if reported(resp.JSON.Usage) && !hasJSONType(resp.JSON.Usage, "{") {
		return nil, fmt.Errorf("openai decisions: invalid usage")
	}
	usage, err := projectUsage(resp.Usage)
	if err != nil {
		return nil, err
	}
	result := &decision.Response{
		Model: resp.Model, RequestID: requestID, Usage: usage,
		Answers: make(map[string]decision.Answer, len(req.Questions)),
	}
	questions := make(map[string]decision.Question, len(req.Questions))
	for _, q := range req.Questions {
		questions[q.ID] = q
		result.Answers[q.ID] = decision.Answer{Status: decision.Unavailable}
	}
	for _, a := range resp.Answers {
		q, ok := questions[a.Name]
		if !ok || !hasJSONType(a.JSON.Name, `"`) {
			return nil, fmt.Errorf("openai decisions: unexpected or duplicate answer name %q", a.Name)
		}
		answer, err := projectAnswer(q, a)
		if err != nil {
			return nil, fmt.Errorf("openai decisions: answer %q: %w", a.Name, err)
		}
		result.Answers[a.Name] = answer
		delete(questions, a.Name)
	}
	return result, nil
}

func projectAnswer(q decision.Question, a openai.DecisionAnswerUnion) (decision.Answer, error) {
	if a.Type == "refusal" {
		return decision.Answer{Status: decision.Refused}, nil
	}
	if a.Type != string(q.Kind) {
		return decision.Answer{}, fmt.Errorf("expected %s, received %q", q.Kind, a.Type)
	}
	result := decision.Answer{Status: decision.Answered}
	if q.Kind == decision.Predicate {
		if !hasJSONType(a.JSON.Probability, jsonNumberPrefixes) || !isProbability(a.Probability) {
			return decision.Answer{}, fmt.Errorf("missing or invalid predicate probability")
		}
		result.Probability = &a.Probability
		return result, nil
	}
	if reported(a.JSON.Confidence) {
		if !hasJSONType(a.JSON.Confidence, jsonNumberPrefixes) || !isProbability(a.Confidence) {
			return decision.Answer{}, fmt.Errorf("invalid confidence")
		}
		result.Confidence = &a.Confidence
	}
	if reported(a.JSON.Probabilities) && !hasJSONType(a.JSON.Probabilities, "[") {
		return decision.Answer{}, fmt.Errorf("invalid probabilities")
	}
	switch q.Kind {
	case decision.Choice:
		return projectChoice(q, a.AsChoice(), result)
	case decision.Score:
		return projectScore(q, a.AsScore(), result)
	default:
		return decision.Answer{}, fmt.Errorf("unsupported answer kind %q", q.Kind)
	}
}

func projectChoice(q decision.Question, a openai.DecisionAnswerChoice, result decision.Answer) (decision.Answer, error) {
	if !hasJSONType(a.JSON.Choice, `"`) || !a.Choice.JSON.OfString.Valid() || !hasOption(q, a.Choice.OfString) {
		return decision.Answer{}, fmt.Errorf("choice is not a requested option id")
	}
	result.Choice = a.Choice.OfString
	if len(a.Probabilities) != 0 {
		result.Probabilities = make(map[string]float64, len(a.Probabilities))
	}
	for _, p := range a.Probabilities {
		id := p.Value.OfString
		if !hasJSONType(p.JSON.Value, `"`) || !p.Value.JSON.OfString.Valid() || !hasOption(q, id) ||
			!hasJSONType(p.JSON.Probability, jsonNumberPrefixes) || !isProbability(p.Probability) {
			return decision.Answer{}, fmt.Errorf("invalid choice probability")
		}
		if _, exists := result.Probabilities[id]; exists {
			return decision.Answer{}, fmt.Errorf("duplicate probability for option %q", id)
		}
		result.Probabilities[id] = p.Probability
	}
	return result, nil
}

func projectScore(q decision.Question, a openai.DecisionAnswerScore, result decision.Answer) (decision.Answer, error) {
	if !hasJSONType(a.JSON.Score, jsonNumberPrefixes) || !(a.Score >= 0 && a.Score <= float64(len(q.Options)-1)) {
		return decision.Answer{}, fmt.Errorf("missing or out-of-range score")
	}
	result.Score = &a.Score
	if len(a.Probabilities) != 0 {
		result.Probabilities = make(map[string]float64, len(a.Probabilities))
	}
	for _, p := range a.Probabilities {
		index, ok := jsonInteger(p.JSON.Value)
		if !ok || index < 0 || index >= int64(len(q.Options)) ||
			!hasJSONType(p.JSON.Label, `"`) || p.Label != q.Options[index].ID ||
			!hasJSONType(p.JSON.Probability, jsonNumberPrefixes) || !isProbability(p.Probability) {
			return decision.Answer{}, fmt.Errorf("invalid score level probability")
		}
		if _, exists := result.Probabilities[p.Label]; exists {
			return decision.Answer{}, fmt.Errorf("duplicate probability for level %q", p.Label)
		}
		result.Probabilities[p.Label] = p.Probability
	}
	return result, nil
}

func hasOption(q decision.Question, id string) bool {
	for _, opt := range q.Options {
		if opt.ID == id {
			return true
		}
	}
	return false
}

func isProbability(p float64) bool {
	return p >= 0 && p <= 1
}

func reported(field respjson.Field) bool {
	return field.Raw() != respjson.Omitted && field.Raw() != respjson.Null
}

// Check the original JSON token because the SDK can coerce scalar values.
func hasJSONType(field respjson.Field, prefixes string) bool {
	raw := strings.TrimSpace(field.Raw())
	return field.Valid() && len(raw) != 0 && strings.IndexByte(prefixes, raw[0]) >= 0
}

func jsonInteger(field respjson.Field) (int64, bool) {
	if !hasJSONType(field, jsonNumberPrefixes) {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSpace(field.Raw()), 10, 64)
	return value, err == nil
}

func projectUsage(usage openai.DecisionUsage) (decision.Usage, error) {
	var result decision.Usage
	for _, count := range []struct {
		field respjson.Field
		dst   **int64
	}{
		{usage.JSON.InputTokens, &result.InputTokens},
		{usage.JSON.OutputTokens, &result.OutputTokens},
		{usage.JSON.TotalTokens, &result.TotalTokens},
	} {
		if !reported(count.field) {
			continue
		}
		value, ok := jsonInteger(count.field)
		if !ok || value < 0 {
			return decision.Usage{}, fmt.Errorf("openai decisions: invalid token count")
		}
		*count.dst = &value
	}
	return result, nil
}
