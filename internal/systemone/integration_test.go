//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

//go:build integration

package systemone_test

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/internal/systemone"
)

const integrationRequestTimeout = 60 * time.Second

// TestIntegrationJev calls the Jev deployment configured by JEV_BASE_URL,
// JEV_API_KEY, and JEV_MODEL. Missing URL configuration skips only this suite;
// once configured, both the key and model are required. Calls can incur charges.
// It covers core inputs; TestIntegrationJevExtended checks additional shapes.
func TestIntegrationJev(t *testing.T) {
	config := integrationConfigFromEnvironment(t, "JEV")
	require.NotEmpty(t, config.apiKey, "JEV_API_KEY is required when JEV_BASE_URL is set")
	require.NotEmpty(t, config.model, "JEV_MODEL is required when JEV_BASE_URL is set")
	runIntegrationSuite(t, config, integrationCases(config.model))
}

// TestIntegrationLaya calls the Laya deployment configured by LAYA_BASE_URL.
// LAYA_API_KEY and LAYA_MODEL are optional for unauthenticated, auto-routing
// deployments. Missing URL configuration skips only this suite.
// It covers core inputs; TestIntegrationLayaExtended checks additional shapes.
func TestIntegrationLaya(t *testing.T) {
	config := integrationConfigFromEnvironment(t, "LAYA")
	runIntegrationSuite(t, config, integrationCases(config.model))
}

// runIntegrationSuite checks the shared protocol against each provider's own
// endpoint. Subtests run sequentially without retries or accuracy thresholds.
// Configured authentication, connection, and protocol errors fail the suite.
func runIntegrationSuite(t *testing.T, config integrationConfig, cases []integrationCase) {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	t.Cleanup(transport.CloseIdleConnections)
	client, err := systemone.NewClient(config.baseURL,
		systemone.WithAPIKey(config.apiKey),
		systemone.WithDefaultModel(config.model),
		systemone.WithHTTPClient(&http.Client{Transport: transport}),
		systemone.WithTimeout(config.timeout),
	)
	require.NoError(t, err)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), config.timeout)
			defer cancel()
			started := time.Now()
			response, err := client.SystemOne(ctx, &tc.request)
			if err != nil {
				var httpErr *systemone.HTTPError
				if errors.As(err, &httpErr) {
					if config.logErrorBody {
						t.Logf("response_body=%q server_body_truncated=%t",
							integrationErrorBody(httpErr.Body, config.apiKey), httpErr.Truncated)
					}
					t.Fatalf("systemone HTTP failure: status=%d request_id=%q elapsed=%s",
						httpErr.StatusCode, httpErr.RequestID, time.Since(started))
				}
				t.Fatalf("systemone call failed: %v", err)
			}
			checkIntegrationResponse(t, &tc.request, response)
			t.Logf("model=%q request_id=%q questions=%d elapsed=%s",
				response.Model, response.RequestID, len(response.Answers), time.Since(started))
			if response.Usage.InputTokens != nil {
				t.Logf("input_tokens=%d", *response.Usage.InputTokens)
			}
			if response.Usage.OutputTokens != nil {
				t.Logf("output_tokens=%d", *response.Usage.OutputTokens)
			}
			if response.Usage.Truncated != nil {
				t.Logf("truncated=%t", *response.Usage.Truncated)
			}
		})
	}
}

type integrationConfig struct {
	baseURL      string
	apiKey       string
	model        string
	timeout      time.Duration
	logErrorBody bool
}

func integrationConfigFromEnvironment(t *testing.T, prefix string) integrationConfig {
	t.Helper()
	config := integrationConfig{
		baseURL: strings.TrimSpace(os.Getenv(prefix + "_BASE_URL")),
		apiKey:  strings.TrimSpace(os.Getenv(prefix + "_API_KEY")),
		model:   strings.TrimSpace(os.Getenv(prefix + "_MODEL")),
		timeout: integrationRequestTimeout,
	}
	if config.baseURL == "" {
		t.Skipf("set %s_BASE_URL to run this provider's integration test", prefix)
	}
	if raw := strings.TrimSpace(os.Getenv(prefix + "_TIMEOUT")); raw != "" {
		var err error
		config.timeout, err = time.ParseDuration(raw)
		require.NoError(t, err, "%s_TIMEOUT must be a Go duration", prefix)
		require.Positive(t, config.timeout, "%s_TIMEOUT must be positive", prefix)
	}
	if raw := strings.TrimSpace(os.Getenv(prefix + "_LOG_ERROR_BODY")); raw != "" {
		var err error
		config.logErrorBody, err = strconv.ParseBool(raw)
		require.NoError(t, err, "%s_LOG_ERROR_BODY must be a boolean", prefix)
	}
	return config
}

// Error bodies can echo request input, so only explicit diagnostic runs log them.
// Redact the configured credential before truncation, including its JSON form.
func integrationErrorBody(body []byte, apiKey string) string {
	text := string(body)
	if apiKey != "" {
		encoded, _ := json.Marshal(apiKey)
		text = strings.ReplaceAll(text, string(encoded[1:len(encoded)-1]), "[REDACTED]")
		text = strings.ReplaceAll(text, apiKey, "[REDACTED]")
	}
	const maxLogBytes = 4096
	if len(text) > maxLogBytes {
		return text[:maxLogBytes] + "... (truncated)"
	}
	return text
}

type integrationCase struct {
	name    string
	request systemone.Request
}

func integrationCases(model string) []integrationCase {
	binary := systemone.BinaryQuestion{Instructions: "Does the customer explicitly request a refund?"}
	choice := systemone.ChoiceQuestion{
		Instructions: "Choose the team that should handle this request.",
		Options: []systemone.ChoiceOption{
			{Name: "z_billing", Description: "Charges, invoices, and refunds"},
			{Name: "a_support", Description: "Technical troubleshooting"},
			{Name: "m_other", Description: "Other requests"},
		},
	}
	score := systemone.ScoreQuestion{
		Instructions: "Rate the urgency of this customer request.",
		Criteria:     []string{"Routine question", "Problem requiring attention", "Critical outage"},
	}
	cases := []integrationCase{
		{
			name: "BinaryStringState",
			request: systemone.Request{
				State:     "I was charged twice. Please refund the duplicate payment.",
				Questions: map[string]systemone.Question{"refund": binary},
			},
		},
		{
			name: "BinaryCriteriaObjectState",
			request: systemone.Request{
				State: map[string]any{"message": "Please refund the duplicate charge.", "duplicate": true},
				Questions: map[string]systemone.Question{"refund": systemone.BinaryQuestion{
					Instructions: binary.Instructions,
					Criteria: &systemone.BinaryCriteria{
						True:  "The customer explicitly requests money back.",
						False: "The customer does not ask for money back.",
					},
				}},
			},
		},
		{
			name: "ChoiceStringArrayState",
			request: systemone.Request{
				State: []string{
					"I was charged twice.",
					"Please refund the duplicate payment.",
				},
				Questions: map[string]systemone.Question{"route": choice},
			},
		},
		{
			name: "ChoiceLabelsOnly",
			request: systemone.Request{
				State: "Please refund the duplicate payment.",
				Questions: map[string]systemone.Question{"route": systemone.ChoiceQuestion{
					Instructions: "Choose the relevant topic.",
					Options:      []systemone.ChoiceOption{{Name: "billing"}, {Name: "technical support"}},
				}},
			},
		},
		{
			name: "ScoreOrdinalLevels",
			request: systemone.Request{
				State:     "I have a question about the invoice date; there is no outage.",
				Questions: map[string]systemone.Question{"urgency": score},
			},
		},
		{
			name: "ScoreTwoLevels",
			request: systemone.Request{
				State: "A routine billing question.",
				Questions: map[string]systemone.Question{"urgency": systemone.ScoreQuestion{
					Instructions: "Rate the request using the two available levels.",
					Criteria:     []string{"Routine question", "Urgent problem"},
				}},
			},
		},
		{
			name: "MixedQuestionsStructState",
			request: systemone.Request{
				State: struct {
					Message string `json:"message"`
					Outage  bool   `json:"outage"`
				}{Message: "我被重复扣款了，请退还重复支付的款项。"},
				Questions: map[string]systemone.Question{
					"退款/判定": &binary,
					"route": &choice,
					"urgency": &systemone.ScoreQuestion{
						Instructions: "Rate urgency, considering impact and any outage.",
						Criteria:     []string{"Routine question", "Needs attention", "Critical service outage"},
					},
				},
			},
		},
	}
	if model != "" {
		cases = append(cases, integrationCase{
			name: "ExplicitRequestModel",
			request: systemone.Request{
				Model: model, State: "Please refund the duplicate payment.",
				Questions: map[string]systemone.Question{"refund": binary},
			},
		})
	}
	return cases
}

func checkIntegrationResponse(t *testing.T, request *systemone.Request, response *systemone.Response) {
	t.Helper()
	require.NotNil(t, response)
	// Servers may return a resolved model name different from the request alias.
	require.NotEmpty(t, response.Model)
	require.Len(t, response.Answers, len(request.Questions))
	for id, question := range request.Questions {
		switch q := question.(type) {
		case systemone.BinaryQuestion, *systemone.BinaryQuestion:
			a, err := response.Binary(id)
			require.NoError(t, err)
			checkIntegrationProbability(t, a.Probability)
		case systemone.ChoiceQuestion:
			checkIntegrationChoice(t, q, response, id)
		case *systemone.ChoiceQuestion:
			checkIntegrationChoice(t, *q, response, id)
		case systemone.ScoreQuestion:
			checkIntegrationScore(t, q, response, id)
		case *systemone.ScoreQuestion:
			checkIntegrationScore(t, *q, response, id)
		default:
			t.Fatalf("unsupported integration question %T", question)
		}
	}
	// Independently inspect Raw to check that common metadata survives decoding.
	// Provider-specific fields remain in Raw; they are not required on all servers.
	var raw struct {
		Model   string                     `json:"model"`
		Answers map[string]json.RawMessage `json:"answers"`
		Usage   systemone.Usage            `json:"usage"`
	}
	require.NoError(t, json.Unmarshal(response.Raw, &raw))
	require.Equal(t, response.Model, raw.Model)
	require.Len(t, raw.Answers, len(request.Questions))
	require.Equal(t, response.Usage, raw.Usage)
	for id := range request.Questions {
		require.Contains(t, raw.Answers, id)
	}
	for _, count := range []*int{response.Usage.InputTokens, response.Usage.OutputTokens} {
		if count != nil {
			require.GreaterOrEqual(t, *count, 0)
		}
	}
}

func checkIntegrationProbability(t *testing.T, value float64) {
	t.Helper()
	require.False(t, math.IsNaN(value) || math.IsInf(value, 0))
	require.GreaterOrEqual(t, value, 0.0)
	require.LessOrEqual(t, value, 1.0)
}

func checkIntegrationChoice(t *testing.T, question systemone.ChoiceQuestion, response *systemone.Response, id string) {
	t.Helper()
	a, err := response.Choice(id)
	require.NoError(t, err)
	checkIntegrationProbability(t, a.Confidence)
	require.Len(t, a.Probabilities, len(question.Options))
	var sum float64
	for _, option := range question.Options {
		p, ok := a.Probabilities[option.Name]
		require.True(t, ok, "missing choice probability %q", option.Name)
		checkIntegrationProbability(t, p)
		sum += p
	}
	require.Contains(t, a.Probabilities, a.Choice)
	// Compatible servers can round each entry independently to four decimals.
	require.InDelta(t, 1, sum, float64(len(question.Options))*0.0001+1e-6)
}

func checkIntegrationScore(t *testing.T, question systemone.ScoreQuestion, response *systemone.Response, id string) {
	t.Helper()
	a, err := response.Score(id)
	require.NoError(t, err)
	checkIntegrationProbability(t, a.Confidence)
	require.False(t, math.IsNaN(a.Score) || math.IsInf(a.Score, 0))
	require.GreaterOrEqual(t, a.Score, 0.0)
	require.LessOrEqual(t, a.Score, float64(len(question.Criteria)-1))
	require.Len(t, a.Levels, len(question.Criteria))
	var sum float64
	for _, level := range a.Levels {
		checkIntegrationProbability(t, level.Probability)
		sum += level.Probability
	}
	require.InDelta(t, 1, sum, float64(len(question.Criteria))*0.0001+1e-6)
}
