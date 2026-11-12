//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package decisions_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/decision"
	"trpc.group/trpc-go/trpc-agent-go/decision/systemone"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/decisions"
)

func TestDecideAcrossBackends(t *testing.T) {
	req := decision.Request{
		Input: decision.Input{JSON: json.RawMessage(`{"order_id":9007199254740993,"message":"Where is my package?"}`)},
		Questions: []decision.Question{
			{ID: "refund", Kind: decision.Predicate, Instructions: "Did the user request a refund?"},
			{ID: "route", Kind: decision.Choice, Instructions: "What is the request about?",
				Options: []decision.Option{{ID: "shipping", Description: "Package status"}, {ID: "billing"}}},
			{ID: "severity", Kind: decision.Score, Instructions: "How severe is the issue?",
				Options: []decision.Option{{ID: "low", Description: "Status inquiry"}, {ID: "high"}}},
		},
	}
	for _, tc := range []struct {
		name, path, model, auth, wantBody, response string
	}{
		{
			name: "OpenAI", path: "/gateway/v1/decisions", model: "gpt-6-luna", auth: "Bearer test-openai",
			wantBody: `{
				"model":"gpt-6-luna",
				"input":[{"type":"message","role":"user","content":[
					{"type":"input_json","data":{"order_id":9007199254740993,"message":"Where is my package?"}}
				]}],
				"questions":[
					{"type":"predicate","name":"refund","instructions":"Did the user request a refund?"},
					{"type":"choice","name":"route","instructions":"What is the request about?","choices":[
						{"value":"shipping","description":"Package status"},{"value":"billing"}
					]},
					{"type":"score","name":"severity","instructions":"How severe is the issue?","levels":[
						{"label":"low","description":"Status inquiry"},{"label":"high"}
					]}
				]
			}`,
			response: `{"model":"gpt-6-luna","answers":[
				{"type":"predicate","name":"refund","probability":0},
				{"type":"choice","name":"route","choice":"shipping","confidence":0.65,"probabilities":[
					{"value":"shipping","probability":0.9},{"value":"billing","probability":0.1}
				]},
				{"type":"score","name":"severity","score":0.25,"confidence":0,"probabilities":[
					{"value":0,"label":"low","probability":0.75},{"value":1,"label":"high","probability":0.25}
				]}
			],"usage":{"input_tokens":18,"output_tokens":0,"total_tokens":18}}`,
		},
		{
			name: "Jev", path: "/gateway/v1/systemone", model: "jev", auth: "Bearer test-jev",
			wantBody: `{"model":"jev","state":{"order_id":9007199254740993,"message":"Where is my package?"},
				"questions":` + systemOneQuestions + `}`,
			response: `{"model":"jev","answers":` + systemOneAnswers + `,"usage":{"output_tokens":0}}`,
		},
		{
			name: "Laya", path: "/gateway/v1/systemone", model: "laya-multilingual",
			wantBody: `{"state":{"order_id":9007199254740993,"message":"Where is my package?"},
				"questions":` + systemOneQuestions + `}`,
			response: `{"model":"laya-multilingual","answers":` + systemOneAnswers + `,
				"usage":{"output_tokens":0,"truncated":true},"routing":{"model":"multilingual"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, tc.path, r.URL.Path)
				assert.Equal(t, tc.auth, r.Header.Get("Authorization"))
				body, err := io.ReadAll(r.Body)
				if assert.NoError(t, err) {
					assert.JSONEq(t, tc.wantBody, string(body))
					// JSONEq decodes numbers as float64. Check the raw digits too.
					assert.Contains(t, string(body), "9007199254740993")
				}
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("X-Request-ID", "test-request")
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()

			var decider decision.Decider
			switch tc.name {
			case "OpenAI":
				decider = openAIClient(t, server, "/gateway/v1", nil)
			case "Jev", "Laya":
				config := systemone.Config{BaseURL: server.URL + "/gateway", HTTPClient: server.Client()}
				if tc.name == "Jev" {
					config.Model = tc.model
					config.APIKey = "test-jev"
				}
				c, err := systemone.New(config)
				require.NoError(t, err)
				decider = c
			}

			result, err := decider.Decide(context.Background(), req)
			require.NoError(t, err)
			require.Equal(t, tc.model, result.Model)
			require.Equal(t, "test-request", result.RequestID)
			require.Len(t, result.Answers, 3)
			refund, route, severity := result.Answers["refund"], result.Answers["route"], result.Answers["severity"]
			require.Equal(t, decision.Answered, refund.Status)
			require.NotNil(t, refund.Probability)
			require.Zero(t, *refund.Probability)
			require.Equal(t, decision.Answered, route.Status)
			require.Equal(t, "shipping", route.Choice)
			require.Equal(t, map[string]float64{"shipping": 0.9, "billing": 0.1}, route.Probabilities)
			require.NotNil(t, route.Confidence)
			require.Equal(t, 0.65, *route.Confidence)
			require.Equal(t, decision.Answered, severity.Status)
			require.NotNil(t, severity.Score)
			require.Equal(t, 0.25, *severity.Score)
			require.Equal(t, map[string]float64{"low": 0.75, "high": 0.25}, severity.Probabilities)
			require.NotNil(t, severity.Confidence)
			require.Zero(t, *severity.Confidence)
			require.NotNil(t, result.Usage.OutputTokens)
			require.Zero(t, *result.Usage.OutputTokens)
			if tc.name == "Laya" {
				require.NotNil(t, result.Usage.Truncated)
				require.True(t, *result.Usage.Truncated)
			} else {
				require.Nil(t, result.Usage.Truncated)
			}
			if tc.name == "OpenAI" {
				require.NotNil(t, result.Usage.TotalTokens)
				require.EqualValues(t, 18, *result.Usage.TotalTokens)
			} else {
				require.Nil(t, result.Usage.InputTokens)
				require.Nil(t, result.Usage.TotalTokens)
			}
		})
	}
}

func TestDecideRefusalAndMissingAnswer(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if assert.NoError(t, err) {
			assert.Contains(t, string(body), `"input":"A support ticket"`)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-6-luna","answers":[
			{"name":"refused","type":"refusal"},
			{"name":"ordinary","type":"predicate","probability":0}
		]}`)
	}))
	defer server.Close()

	req := predicateRequest("ordinary", "missing", "refused")
	resp, err := openAIClient(t, server, "/v1").Decide(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, decision.Answer{Status: decision.Refused}, resp.Answers["refused"])
	require.Equal(t, decision.Answer{Status: decision.Unavailable}, resp.Answers["missing"])
	require.Equal(t, decision.Answered, resp.Answers["ordinary"].Status)
	require.NotNil(t, resp.Answers["ordinary"].Probability)
	require.Zero(t, *resp.Answers["ordinary"].Probability)
	require.Nil(t, resp.Usage.OutputTokens)
}

func TestDecideRejectsMismatchedAnswers(t *testing.T) {
	for _, tc := range []struct {
		name, answers, wantError string
	}{
		{"unknown name", `[{"name":"other","type":"predicate","probability":1}]`, "unexpected or duplicate"},
		{"duplicate name", `[{"name":"q","type":"predicate","probability":1},{"name":"q","type":"refusal"}]`, "unexpected or duplicate"},
		{"wrong kind", `[{"name":"q","type":"score","score":0}]`, "expected predicate"},
		{"missing probability", `[{"name":"q","type":"predicate"}]`, "invalid predicate probability"},
		{"boolean probability", `[{"name":"q","type":"predicate","probability":true}]`, "invalid predicate probability"},
		{"string probability", `[{"name":"q","type":"predicate","probability":"0.5"}]`, "invalid predicate probability"},
		{"null answers", `null`, "missing a model or answers array"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"model":"gpt-6-luna","answers":`+tc.answers+`}`)
			}))
			defer server.Close()
			resp, err := openAIClient(t, server, "/v1").Decide(context.Background(), predicateRequest("q"))
			require.ErrorContains(t, err, tc.wantError)
			require.Nil(t, resp)
		})
	}
}

func TestDecideRejectsFractionalScoreLevelIndex(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-6-luna","answers":[{"name":"q","type":"score","score":0,
			"probabilities":[{"value":0.7,"label":"low","probability":1}]}]}`)
	}))
	defer server.Close()
	req := predicateRequest("q")
	req.Questions[0] = decision.Question{
		ID: "q", Kind: decision.Score, Instructions: "How severe is the issue?",
		Options: []decision.Option{{ID: "low"}, {ID: "high"}},
	}
	resp, err := openAIClient(t, server, "/v1").Decide(context.Background(), req)
	require.ErrorContains(t, err, "invalid score level probability")
	require.Nil(t, resp)
}

func TestDecideRejectsBooleanForStringChoice(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"model":"gpt-6-luna","answers":[{"name":"q","type":"choice","choice":false}]}`)
	}))
	defer server.Close()
	req := predicateRequest("q")
	req.Questions[0].Kind = decision.Choice
	req.Questions[0].Options = []decision.Option{{ID: "true"}, {ID: "false"}}
	resp, err := openAIClient(t, server, "/v1").Decide(context.Background(), req)
	require.ErrorContains(t, err, "choice is not a requested option id")
	require.Nil(t, resp)
}

func TestDecideHTTPErrorIsNotRetriedByDefault(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(w, `{"error":{"message":"rate limited","type":"rate_limit_error"}}`)
	}))
	defer server.Close()
	resp, err := openAIClient(t, server, "/v1").Decide(context.Background(), predicateRequest("q"))
	var apiErr *openai.Error
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusTooManyRequests, apiErr.StatusCode)
	require.Nil(t, resp)
	require.EqualValues(t, 1, calls.Load())
}

func TestDecideCancelsInFlightRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var reachedServer atomic.Bool
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		reachedServer.Store(true)
		cancel()
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
			t.Error("canceling Decide did not cancel the HTTP request")
		}
	}))
	defer server.Close()
	resp, err := openAIClient(t, server, "/v1").Decide(ctx, predicateRequest("q"))
	require.True(t, reachedServer.Load())
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, resp)
}

func predicateRequest(ids ...string) decision.Request {
	req := decision.Request{Input: decision.Input{Text: "A support ticket"}}
	for _, id := range ids {
		req.Questions = append(req.Questions, decision.Question{
			ID: id, Kind: decision.Predicate, Instructions: "Does the ticket mention shipping?",
		})
	}
	return req
}

func openAIClient(t *testing.T, server *httptest.Server, basePath string, opts ...option.RequestOption) *decisions.Client {
	t.Helper()
	for _, name := range []string{"OPENAI_API_KEY", "OPENAI_ADMIN_KEY", "OPENAI_ORG_ID", "OPENAI_PROJECT_ID", "OPENAI_CUSTOM_HEADERS"} {
		t.Setenv(name, "")
	}
	t.Setenv("OPENAI_BASE_URL", server.URL+basePath)
	client, err := decisions.New("gpt-6-luna", append([]option.RequestOption{
		option.WithBaseURL(server.URL + basePath),
		option.WithAPIKey("test-openai"),
		option.WithHTTPClient(server.Client()),
	}, opts...)...)
	require.NoError(t, err)
	return client
}

const systemOneQuestions = `{
	"refund":{"type":"noul","instructions":"Did the user request a refund?"},
	"route":{"type":"choice","instructions":"What is the request about?",
		"criteria":{"shipping":"Package status","billing":null}},
	"severity":{"type":"score","instructions":"How severe is the issue?",
		"criteria":["low: Status inquiry","high"]}
}`

const systemOneAnswers = `{
	"refund":{"type":"noul","noul":0},
	"route":{"type":"choice","choice":"shipping","confidence":0.65,"probabilities":{"shipping":0.9,"billing":0.1}},
	"severity":{"type":"score","score":0.25,"confidence":0,
		"probabilities":{"0":0.75,"1":0.25},"legend":{"0":"low: Status inquiry","1":"high"}}
}`
