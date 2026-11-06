//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package systemone_test

import (
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

type embeddedAnswer struct{ systemone.Answer }

func TestResponseLookups(t *testing.T) {
	binary := &systemone.BinaryAnswer{Probability: 0}
	choice := &systemone.ChoiceAnswer{Choice: "a", Probabilities: map[string]float64{"a": 1}}
	score := &systemone.ScoreAnswer{Score: 0, Levels: []systemone.ScoreLevel{{Description: "low", Probability: 1}}}
	answers := map[string]systemone.Answer{"binary": binary, "choice": choice, "score": score}
	// Lookups use Answers directly, without consulting or re-decoding Raw.
	response := &systemone.Response{Answers: answers, Raw: json.RawMessage(`not json`)}
	for _, tc := range []struct {
		name string
		get  func(*systemone.Response, string) (systemone.Answer, bool, error)
	}{
		{"binary", func(r *systemone.Response, id string) (systemone.Answer, bool, error) {
			a, err := r.Binary(id)
			return a, a == nil, err
		}},
		{"choice", func(r *systemone.Response, id string) (systemone.Answer, bool, error) {
			a, err := r.Choice(id)
			return a, a == nil, err
		}},
		{"score", func(r *systemone.Response, id string) (systemone.Answer, bool, error) {
			a, err := r.Score(id)
			return a, a == nil, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, isNil, err := tc.get(response, tc.name)
			if err != nil || isNil || got != answers[tc.name] {
				t.Fatalf("lookup must return original answer: %v %v", got, err)
			}
			for id := range answers {
				if id == tc.name {
					continue
				}
				_, isNil, err := tc.get(response, id)
				if err == nil || !isNil || !strings.Contains(err.Error(), id) || !strings.Contains(err.Error(), "expected") || !strings.Contains(err.Error(), "got") {
					t.Fatalf("wrong type must return nil and descriptive error: %v", err)
				}
			}
			for name, r := range map[string]*systemone.Response{
				"nil response":       nil,
				"zero response":      {},
				"missing id":         {Answers: answers},
				"nil answer":         {Answers: map[string]systemone.Answer{"q": nil}},
				"nil binary":         {Answers: map[string]systemone.Answer{"q": (*systemone.BinaryAnswer)(nil)}},
				"nil choice":         {Answers: map[string]systemone.Answer{"q": (*systemone.ChoiceAnswer)(nil)}},
				"nil score":          {Answers: map[string]systemone.Answer{"q": (*systemone.ScoreAnswer)(nil)}},
				"unsupported answer": {Answers: map[string]systemone.Answer{"q": embeddedAnswer{binary}}},
			} {
				t.Run(name, func(t *testing.T) {
					_, isNil, err := tc.get(r, "q")
					if err == nil || !isNil || !strings.Contains(err.Error(), `"q"`) {
						t.Fatalf("invalid lookup must return nil and identify the ID: %v", err)
					}
				})
			}
		})
	}
}
