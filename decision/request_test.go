//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package decision_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/decision"
)

func TestRequestValidate(t *testing.T) {
	predicate := decision.Question{ID: "late", Kind: decision.Predicate, Instructions: "Is the delivery late?"}
	tests := []struct {
		name  string
		input decision.Input
		want  string
	}{
		{name: "text", input: decision.Input{Text: "The delivery is late."}},
		{name: "json object", input: decision.Input{JSON: json.RawMessage(` {"days": 9} `)}},
		{name: "json objects", input: decision.Input{JSON: json.RawMessage(`[{"days": 9}]`)}},
		{name: "both inputs", input: decision.Input{Text: "late", JSON: json.RawMessage(`{}`)}, want: "exactly one"},
		{name: "missing input", want: "exactly one"},
		{name: "malformed json", input: decision.Input{JSON: json.RawMessage(`{`)}, want: "not valid"},
		{name: "scalar json", input: decision.Input{JSON: json.RawMessage(`"late"`)}, want: "object or array"},
		{name: "non-object array entry", input: decision.Input{JSON: json.RawMessage(`[{}, null]`)}, want: "not an object"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := (decision.Request{Input: tc.input, Questions: []decision.Question{predicate}}).Validate()
			if tc.want == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.want)
			}
		})
	}

	t.Run("duplicate ids", func(t *testing.T) {
		req := decision.Request{Input: decision.Input{Text: "late"}, Questions: []decision.Question{predicate, predicate}}
		require.ErrorContains(t, req.Validate(), "duplicate question id")
	})
	t.Run("duplicate choice ids", func(t *testing.T) {
		req := decision.Request{Input: decision.Input{Text: "late"}, Questions: []decision.Question{{
			ID: "route", Kind: decision.Choice, Instructions: "Pick a route.",
			Options: []decision.Option{{ID: "logistics"}, {ID: "logistics", Description: "a different meaning"}},
		}}}
		require.ErrorContains(t, req.Validate(), "duplicate option id")
	})
}

func TestRequestValidateInvalidQuestion(t *testing.T) {
	tests := []struct {
		name    string
		kind    decision.Kind
		options []decision.Option
		want    string
	}{
		{"predicate with options", decision.Predicate, []decision.Option{{ID: "low"}}, "predicate does not take options"},
		{"score with one level", decision.Score, []decision.Option{{ID: "low"}}, "requires at least two options"},
		{"score with duplicate ids", decision.Score, []decision.Option{{ID: "low"}, {ID: "low"}}, "duplicate option id"},
		{"unsupported kind", "rank", nil, "unsupported kind"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := decision.Request{Input: decision.Input{Text: "A delivery is late."}, Questions: []decision.Question{{
				ID: "severity", Instructions: "Assess severity.", Kind: tc.kind, Options: tc.options,
			}}}
			require.ErrorContains(t, req.Validate(), tc.want)
		})
	}
}
