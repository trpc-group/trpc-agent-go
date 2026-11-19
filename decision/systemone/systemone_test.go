//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2026 Tencent.  All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package systemone_test

import (
	"context"
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
