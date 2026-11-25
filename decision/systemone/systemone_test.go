//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package systemone_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/decision"
	"trpc.group/trpc-go/trpc-agent-go/decision/systemone"
)

func TestDecideRejectsDuplicateScoreCriteriaBeforeSending(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP request for duplicate score criteria")
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	client, err := systemone.New(systemone.Config{BaseURL: server.URL})
	require.NoError(t, err)
	req := decision.Request{Input: decision.Input{Text: "A delivery is late."}, Questions: []decision.Question{{
		ID: "severity", Kind: decision.Score, Instructions: "Assess severity.",
		Options: []decision.Option{{ID: "low", Description: "medium"}, {ID: "low: medium"}},
	}}}
	require.NoError(t, req.Validate())
	_, err = client.Decide(context.Background(), req)
	require.ErrorContains(t, err, `question "severity": duplicate score criterion "low: medium"`)
}

func TestDecideProjectsBatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Request-ID", "batch-1")
		fmt.Fprint(w, `{"model":"test-model","answers":{
			"late":{"type":"noul","noul":0},
			"route":{"type":"choice","choice":"shipping","confidence":1,"probabilities":{"shipping":1,"billing":0}},
			"severity":{"type":"score","score":0.25,"confidence":0.8,"probabilities":{"0":0.75,"1":0.25},"legend":{"0":"low: Minor delay","1":"high"}}
		},"usage":{"input_tokens":12,"truncated":false}}`)
	}))
	defer server.Close()
	client, err := systemone.New(systemone.Config{BaseURL: server.URL})
	require.NoError(t, err)
	resp, err := client.Decide(context.Background(), decision.Request{
		Input: decision.Input{JSON: json.RawMessage(`{"delay_days":1}`)},
		Questions: []decision.Question{
			{ID: "late", Kind: decision.Predicate, Instructions: "Is the delivery late?"},
			{ID: "route", Kind: decision.Choice, Instructions: "Pick a team.", Options: []decision.Option{{ID: "shipping"}, {ID: "billing"}}},
			{ID: "severity", Kind: decision.Score, Instructions: "Assess severity.", Options: []decision.Option{{ID: "low", Description: "Minor delay"}, {ID: "high"}}},
		},
	})
	require.NoError(t, err)
	require.Equal(t, "test-model", resp.Model)
	require.Equal(t, "batch-1", resp.RequestID)
	require.Equal(t, int64(12), *resp.Usage.InputTokens)
	require.Nil(t, resp.Usage.OutputTokens)
	require.False(t, *resp.Usage.Truncated)
	for _, id := range []string{"late", "route", "severity"} {
		require.Equal(t, decision.Answered, resp.Answers[id].Status)
	}
	require.Zero(t, *resp.Answers["late"].Probability)
	require.Equal(t, "shipping", resp.Answers["route"].Choice)
	require.Equal(t, 0.25, *resp.Answers["severity"].Score)
	require.Equal(t, map[string]float64{"low": 0.75, "high": 0.25}, resp.Answers["severity"].Probabilities)
}

func TestDecideRejectsInvalidCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("unexpected HTTP request")
	}))
	defer server.Close()
	client, err := systemone.New(systemone.Config{BaseURL: server.URL})
	require.NoError(t, err)
	var empty decision.Request
	_, err = client.Decide(context.Background(), empty)
	require.ErrorContains(t, err, "exactly one")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = client.Decide(ctx, empty)
	require.ErrorIs(t, err, context.Canceled)
	var uninitialized systemone.Client
	_, err = uninitialized.Decide(context.Background(), empty)
	require.ErrorContains(t, err, "not initialized")
}
