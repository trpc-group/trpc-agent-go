//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package gemini_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/decision"
	"trpc.group/trpc-go/trpc-agent-go/decision/gemini"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func testClient(t *testing.T, transport roundTripFunc) *gemini.Client {
	t.Helper()
	client, err := gemini.New("gemini-decision-1", "test-key", &http.Client{Transport: transport})
	require.NoError(t, err)
	return client
}

func response(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func batchRequest() decision.Request {
	return decision.Request{
		Input: decision.Input{JSON: json.RawMessage(`{"order_id":9007199254740993,"message":"My package is late."}`)},
		Questions: []decision.Question{
			{ID: "late", Kind: decision.Predicate, Instructions: "Is the delivery late?"},
			{ID: "route", Kind: decision.Choice, Instructions: "Pick a team.",
				Options: []decision.Option{{ID: "shipping", Description: "Delivery status"}, {ID: "billing"}}},
			{ID: "severity", Kind: decision.Score, Instructions: "Assess severity.",
				Options: []decision.Option{{ID: "low", Description: "Minor delay"}, {ID: "high"}}},
		},
	}
}

func TestDecideBatch(t *testing.T) {
	calls := 0
	client := testClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "https://generativelanguage.googleapis.com/v1beta/decisions", r.URL.String())
		require.Equal(t, "test-key", r.Header.Get("x-goog-api-key"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		// JSON evidence is transported as text without rounding large integers.
		require.JSONEq(t, `{
			"model":"gemini-decision-1",
			"input":"{\"order_id\":9007199254740993,\"message\":\"My package is late.\"}",
			"questions":{
				"late":{"type":"noul","instructions":"Is the delivery late?"},
				"route":{"type":"choice","instructions":"Pick a team.",
					"criteria":{"shipping":"Delivery status","billing":""}},
				"severity":{"type":"score","instructions":"Assess severity.",
					"criteria":["low: Minor delay","high"]}
			}
		}`, string(body))
		resp := response(http.StatusOK, `{"answers":{
			"late":{"type":"noul","noul":0.99,"confidence":0.99},
			"route":{"type":"choice","choice":"shipping","confidence":0.8,
				"probabilities":{"shipping":0.8,"billing":0.2}},
			"severity":{"type":"score","score":0.25,"confidence":0.75,
				"legend":{"0":"low: Minor delay","1":"high"},
				"probabilities":{"0":0.75,"1":0.25}}
			},"usage":{"total_input_tokens":15,"total_output_tokens":0,"total_tokens":15}}`)
		return resp, nil
	})

	answer, err := client.Decide(context.Background(), batchRequest())
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.NotNil(t, answer)
	require.Len(t, answer.Answers, 3)
	// The documented response does not report a model or request ID.
	require.Empty(t, answer.Model)
	require.Empty(t, answer.RequestID)
	require.EqualValues(t, 15, *answer.Usage.InputTokens)
	require.Zero(t, *answer.Usage.OutputTokens)
	require.EqualValues(t, 15, *answer.Usage.TotalTokens)
	require.Nil(t, answer.Usage.Truncated)
	for _, id := range []string{"late", "route", "severity"} {
		require.Equal(t, decision.Answered, answer.Answers[id].Status)
	}
	require.InDelta(t, 0.99, *answer.Answers["late"].Probability, 1e-12)
	require.InDelta(t, 0.99, *answer.Answers["late"].Confidence, 1e-12)
	require.Equal(t, "shipping", answer.Answers["route"].Choice)
	require.InDelta(t, 0.8, *answer.Answers["route"].Confidence, 1e-12)
	require.Equal(t, map[string]float64{"shipping": 0.8, "billing": 0.2}, answer.Answers["route"].Probabilities)
	require.InDelta(t, 0.25, *answer.Answers["severity"].Score, 1e-12)
	require.InDelta(t, 0.75, *answer.Answers["severity"].Confidence, 1e-12)
	require.Equal(t, map[string]float64{"low": 0.75, "high": 0.25}, answer.Answers["severity"].Probabilities)
}

func TestDecideOptionalUsage(t *testing.T) {
	client := testClient(t, func(*http.Request) (*http.Response, error) {
		return response(http.StatusOK, `{"answers":{"q":{"type":"noul","noul":0.01,"confidence":0.99}}}`), nil
	})
	req := decision.Request{Input: decision.Input{Text: "An order."},
		Questions: []decision.Question{{ID: "q", Kind: decision.Predicate, Instructions: "Is it late?"}}}
	answer, err := client.Decide(context.Background(), req)
	require.NoError(t, err)
	require.InDelta(t, 0.01, *answer.Answers["q"].Probability, 1e-12)
	require.Nil(t, answer.Usage.InputTokens)
	require.Nil(t, answer.Usage.OutputTokens)
	require.Nil(t, answer.Usage.TotalTokens)
	require.Nil(t, answer.Usage.Truncated)
}

func TestDecideRejectsMalformedResponses(t *testing.T) {
	req := batchRequest()
	req.Questions = req.Questions[:1]
	tests := []struct {
		name string
		body string
	}{
		{"missing answer", `{"answers":{}}`},
		{"unexpected answer", `{"answers":{"unknown":{"type":"noul","noul":0.99,"confidence":0.99}}}`},
		{"null answers", `{"answers":null}`},
		{"wrong type", `{"answers":{"late":{"type":"choice","choice":"shipping","confidence":0.8,"probabilities":{"shipping":0.8,"billing":0.2}}}}`},
		{"missing noul", `{"answers":{"late":{"type":"noul","confidence":0.99}}}`},
		{"null noul", `{"answers":{"late":{"type":"noul","noul":null,"confidence":0.99}}}`},
		{"invalid probability type", `{"answers":{"late":{"type":"noul","noul":"0.5","confidence":0.5}}}`},
		{"out of range confidence", `{"answers":{"late":{"type":"noul","noul":0.5,"confidence":1.1}}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, tc.body), nil
			})
			answer, err := client.Decide(context.Background(), req)
			require.Error(t, err)
			require.Nil(t, answer)
		})
	}
}

func TestDecideRejectsInvalidChoiceAndScore(t *testing.T) {
	tests := []struct {
		name     string
		question decision.Question
		body     string
	}{
		{"unrequested choice", decision.Question{ID: "q", Kind: decision.Choice, Instructions: "Select.",
			Options: []decision.Option{{ID: "a"}, {ID: "b"}}},
			`{"type":"choice","choice":"c","confidence":0.7,"probabilities":{"a":0.7,"b":0.3}}`},
		{"missing choice probability", decision.Question{ID: "q", Kind: decision.Choice, Instructions: "Select.",
			Options: []decision.Option{{ID: "a"}, {ID: "b"}}},
			`{"type":"choice","choice":"a","confidence":0.7,"probabilities":{"a":0.7}}`},
		{"out of range choice probability", decision.Question{ID: "q", Kind: decision.Choice, Instructions: "Select.",
			Options: []decision.Option{{ID: "a"}, {ID: "b"}}},
			`{"type":"choice","choice":"a","confidence":0.7,"probabilities":{"a":1.1,"b":-0.1}}`},
		{"out of range score", decision.Question{ID: "q", Kind: decision.Score, Instructions: "Rate.",
			Options: []decision.Option{{ID: "low"}, {ID: "high"}}},
			`{"type":"score","score":2,"confidence":0.7,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.7,"1":0.3}}`},
		{"wrong score legend", decision.Question{ID: "q", Kind: decision.Score, Instructions: "Rate.",
			Options: []decision.Option{{ID: "low"}, {ID: "high"}}},
			`{"type":"score","score":0.3,"confidence":0.7,"legend":{"0":"high","1":"low"},"probabilities":{"0":0.7,"1":0.3}}`},
		{"missing score level", decision.Question{ID: "q", Kind: decision.Score, Instructions: "Rate.",
			Options: []decision.Option{{ID: "low"}, {ID: "high"}}},
			`{"type":"score","score":0.3,"confidence":0.7,"legend":{"0":"low","1":"high"},"probabilities":{"0":0.7}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := testClient(t, func(*http.Request) (*http.Response, error) {
				return response(http.StatusOK, `{"answers":{"q":`+tc.body+`}}`), nil
			})
			req := decision.Request{Input: decision.Input{Text: "An order."}, Questions: []decision.Question{tc.question}}
			answer, err := client.Decide(context.Background(), req)
			require.Error(t, err)
			require.Nil(t, answer)
		})
	}
}

func TestDecideHTTPErrorAndCancellation(t *testing.T) {
	t.Run("safety block", func(t *testing.T) {
		calls := 0
		client := testClient(t, func(*http.Request) (*http.Response, error) {
			calls++
			return response(http.StatusBadRequest, `{"error":{"code":"safety","message":"input blocked"}}`), nil
		})
		req := decision.Request{Input: decision.Input{Text: "An order."},
			Questions: []decision.Question{{ID: "q", Kind: decision.Predicate, Instructions: "Is it late?"}}}
		answer, err := client.Decide(context.Background(), req)
		require.ErrorContains(t, err, "safety")
		require.Nil(t, answer)
		require.Equal(t, 1, calls)
	})
	t.Run("caller canceled", func(t *testing.T) {
		client := testClient(t, func(*http.Request) (*http.Response, error) {
			t.Error("unexpected request after cancellation")
			return nil, errors.New("unexpected request")
		})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, err := client.Decide(ctx, batchRequest())
		require.ErrorIs(t, err, context.Canceled)
	})
	t.Run("redirect does not forward API key", func(t *testing.T) {
		calls := 0
		client := testClient(t, func(*http.Request) (*http.Response, error) {
			calls++
			result := response(http.StatusTemporaryRedirect, "")
			result.Header.Set("Location", "https://unrelated.example/decisions")
			return result, nil
		})
		_, err := client.Decide(context.Background(), batchRequest())
		require.Error(t, err)
		require.Equal(t, 1, calls)
	})
}

func TestDecideRejectsUnsupportedRequestBeforeSending(t *testing.T) {
	tests := []struct {
		name string
		edit func(*decision.Request)
	}{
		{"question ID spaces", func(r *decision.Request) { r.Questions[0].ID = "not valid" }},
		{"question ID too long", func(r *decision.Request) { r.Questions[0].ID = strings.Repeat("q", 65) }},
		{"choice ID leading space", func(r *decision.Request) { r.Questions[1].Options[0].ID = " shipping" }},
		{"choice ID control character", func(r *decision.Request) { r.Questions[1].Options[0].ID = "ship\nping" }},
		{"choice ID non-ASCII space", func(r *decision.Request) { r.Questions[1].Options[0].ID = "ship\u00a0ping" }},
		{"choice ID too long", func(r *decision.Request) { r.Questions[1].Options[0].ID = strings.Repeat("a", 129) }},
		{"ambiguous score criteria", func(r *decision.Request) {
			r.Questions[2].Options = []decision.Option{{ID: "low", Description: "medium"}, {ID: "low: medium"}}
		}},
		{"too many options", func(r *decision.Request) {
			options := make([]decision.Option, 256)
			for i := range options {
				options[i].ID = fmt.Sprintf("option-%d", i)
			}
			r.Questions[1].Options = options
		}},
		{"too many questions", func(r *decision.Request) {
			for i := 0; i < 1000; i++ {
				r.Questions = append(r.Questions, decision.Question{ID: fmt.Sprintf("extra-%d", i), Kind: decision.Predicate, Instructions: "Is it late?"})
			}
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := testClient(t, func(*http.Request) (*http.Response, error) {
				calls++
				return response(http.StatusInternalServerError, ""), nil
			})
			req := batchRequest()
			tc.edit(&req)
			require.NoError(t, req.Validate())
			answer, err := client.Decide(context.Background(), req)
			require.Error(t, err)
			require.Nil(t, answer)
			require.Zero(t, calls)
		})
	}
}
