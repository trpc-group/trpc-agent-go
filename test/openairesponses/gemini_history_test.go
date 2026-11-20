//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openairesponses_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/gemini"
)

func assertGeminiReplaysReorderedResults(t *testing.T, f *switchFixture, sid string) {
	t.Helper()
	bodies := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1beta/models/gemini-test:generateContent" {
			http.NotFound(w, r)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		select {
		case bodies <- body:
		default:
			http.Error(w, "unexpected extra Gemini request", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"candidates":[{"content":{"role":"model","parts":[{"text":"gemini continuation"}]},"finishReason":"STOP"}]}`)
	}))
	defer server.Close()
	target, err := gemini.New(t.Context(), "gemini-test", gemini.WithGeminiClientConfig(&genai.ClientConfig{
		APIKey: "test", Backend: genai.BackendGeminiAPI, HTTPOptions: genai.HTTPOptions{BaseURL: server.URL, APIVersion: "v1beta"},
	}))
	require.NoError(t, err)
	original := string(f.savedNative(t, sid).ProviderData.Payload)
	require.Empty(t, f.run(t, sid, "", model.NewUserMessage("continue in Gemini"), agent.WithModel(target)))
	require.JSONEq(t, original, string(f.savedNative(t, sid).ProviderData.Payload))
	require.EqualValues(t, 2, f.toolRuns.Load())
	require.Len(t, bodies, 1)
	body := <-bodies
	require.NotContains(t, string(body), "opaque-switch-reasoning")
	require.Contains(t, string(body), "source answer")
	var req struct {
		Contents []struct {
			Role  string
			Parts []struct {
				Call   *struct{ ID, Name string } `json:"functionCall"`
				Result *struct {
					ID, Name string
					Response json.RawMessage
				} `json:"functionResponse"`
			}
		}
	}
	require.NoError(t, json.Unmarshal(body, &req))
	var calls, results []string
	for _, content := range req.Contents {
		for _, part := range content.Parts {
			if call := part.Call; call != nil {
				require.Equal(t, "model", content.Role)
				require.Equal(t, "lookup", call.Name)
				calls = append(calls, call.ID)
			}
			if result := part.Result; result != nil {
				require.Equal(t, "user", content.Role)
				require.Len(t, content.Parts, 2)
				require.Equal(t, "lookup", result.Name)
				results = append(results, result.ID)
				switch result.ID {
				case "call_first":
					require.Contains(t, string(result.Response), "result-for-first")
				case "call_second":
					require.Contains(t, string(result.Response), "result-for-second")
				}
			}
		}
	}
	require.Equal(t, []string{"call_first", "call_second"}, calls)
	require.Equal(t, calls, results)
}
