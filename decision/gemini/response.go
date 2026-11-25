//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini

import (
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"trpc.group/trpc-go/trpc-agent-go/decision"
)

func decodeResponse(body io.Reader, req decision.Request, questions map[string]question) (*decision.Response, error) {
	var reply struct {
		Answers map[string]answer
		Usage   struct {
			Input  *int64 `json:"total_input_tokens"`
			Output *int64 `json:"total_output_tokens"`
			Total  *int64 `json:"total_tokens"`
		}
	}
	dec := json.NewDecoder(body)
	if err := dec.Decode(&reply); err != nil {
		return nil, fmt.Errorf("gemini decisions: decode response: %w", err)
	}
	if dec.Decode(new(json.RawMessage)) != io.EOF {
		return nil, fmt.Errorf("gemini decisions: trailing response data")
	}
	if len(reply.Answers) != len(questions) {
		return nil, fmt.Errorf("gemini decisions: unexpected answer count")
	}
	answers := make(map[string]decision.Answer, len(questions))
	for _, q := range req.Questions {
		a, err := reply.Answers[q.ID].project(q, questions[q.ID].Type)
		if err != nil {
			return nil, fmt.Errorf("gemini decisions: answer %q: %w", q.ID, err)
		}
		answers[q.ID] = a
	}
	u := reply.Usage
	for _, n := range []*int64{u.Input, u.Output, u.Total} {
		if n != nil && *n < 0 {
			return nil, fmt.Errorf("gemini decisions: negative token count")
		}
	}
	return &decision.Response{Answers: answers, Usage: decision.Usage{InputTokens: u.Input, OutputTokens: u.Output, TotalTokens: u.Total}}, nil
}

type answer struct {
	Type, Choice            string
	Noul, Score, Confidence *float64
	Probabilities           map[string]*float64
	Legend                  map[string]string
}

func within(v *float64, min, max float64) bool { return v != nil && *v >= min && *v <= max }
func (a answer) project(q decision.Question, kind string) (decision.Answer, error) {
	var empty decision.Answer
	if a.Type != kind || !within(a.Confidence, 0, 1) {
		return empty, fmt.Errorf("unexpected type or invalid confidence")
	}
	v := decision.Answer{Status: decision.Answered, Confidence: a.Confidence}
	if q.Kind == decision.Predicate {
		if !within(a.Noul, 0.01, 0.99) {
			return empty, fmt.Errorf("invalid noul probability")
		}
		v.Probability = a.Noul
		return v, nil
	}
	if len(a.Probabilities) != len(q.Options) {
		return empty, fmt.Errorf("incomplete probabilities")
	}
	if q.Kind == decision.Score {
		if !within(a.Score, 0, float64(len(q.Options)-1)) || len(a.Legend) != len(q.Options) {
			return empty, fmt.Errorf("invalid score or legend")
		}
		v.Score = a.Score
	} else {
		if _, ok := a.Probabilities[a.Choice]; !ok {
			return empty, fmt.Errorf("invalid selected choice")
		}
		v.Choice = a.Choice
	}
	v.Probabilities = make(map[string]float64, len(q.Options))
	for i, o := range q.Options {
		key := o.ID
		if q.Kind == decision.Score {
			key = strconv.Itoa(i)
			if a.Legend[key] != criterion(o) {
				return empty, fmt.Errorf("unexpected score legend at %s", key)
			}
		}
		p := a.Probabilities[key]
		if !within(p, 0, 1) {
			return empty, fmt.Errorf("invalid probability for %q", o.ID)
		}
		v.Probabilities[o.ID] = *p
	}
	return v, nil
}
