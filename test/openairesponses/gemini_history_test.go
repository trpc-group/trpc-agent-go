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
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/gemini"
)

func TestModelSwitchGeminiToolCallWithoutArguments(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			f := newSwitchFixture(t, switchTextReply("msg_source", "source answer"), switchTextReply("msg_back", "back answer"))
			const sid = "gemini-no-args"
			require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("start")))
			original := f.savedNative(t, sid).ProviderData.Payload

			const callID = "gemini_lookup"
			replies := []string{
				`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"id":"gemini_lookup","name":"lookup"}}]},"finishReason":"STOP"}]}`,
				`{"candidates":[{"content":{"role":"model","parts":[{"text":"first Gemini answer"}]},"finishReason":"STOP"}]}`,
				`{"candidates":[{"content":{"role":"model","parts":[{"text":"second Gemini answer"}]},"finishReason":"STOP"}]}`,
			}
			method := "generateContent"
			if stream {
				method = "streamGenerateContent"
			}
			var requestCount atomic.Int32
			bodies := make(chan []byte, len(replies))
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/v1beta/models/gemini-test:"+method || (stream && r.URL.Query().Get("alt") != "sse") {
					http.NotFound(w, r)
					return
				}
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				index := int(requestCount.Add(1) - 1)
				if index >= len(replies) {
					http.Error(w, "unexpected Gemini request", http.StatusBadRequest)
					return
				}
				bodies <- body
				if stream {
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: %s\n\n", replies[index])
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, replies[index])
			}))
			t.Cleanup(server.Close)
			target, err := gemini.New(t.Context(), "gemini-test", gemini.WithGeminiClientConfig(&genai.ClientConfig{
				APIKey: "test", Backend: genai.BackendGeminiAPI, HTTPOptions: genai.HTTPOptions{BaseURL: server.URL, APIVersion: "v1beta"},
			}))
			require.NoError(t, err)
			selected := geminiModelWithStream{Model: target, stream: stream}

			require.Empty(t, f.run(t, sid, "", model.NewUserMessage("run lookup"), agent.WithModel(selected)))
			require.EqualValues(t, 1, f.toolRuns.Load())
			require.Empty(t, f.run(t, sid, "", model.NewUserMessage("continue in Gemini"), agent.WithModel(selected)))
			require.Len(t, bodies, 3)
			<-bodies
			assertGeminiLookupRound(t, <-bodies, callID)
			assertGeminiLookupRound(t, <-bodies, callID)
			require.Empty(t, f.run(t, sid, switchSource, model.NewUserMessage("switch back")))
			require.EqualValues(t, 1, f.toolRuns.Load(), "completed tool must not execute again")
			require.JSONEq(t, string(original), string(f.savedNative(t, sid).ProviderData.Payload))

			seen := f.snapshot()
			require.Len(t, seen, 2)
			var input []struct {
				Type      string `json:"type"`
				CallID    string `json:"call_id"`
				Arguments string `json:"arguments"`
				Output    string `json:"output"`
			}
			require.NoError(t, json.Unmarshal(seen[1].Input, &input))
			var calls, results int
			for _, item := range input {
				if item.CallID != callID {
					continue
				}
				switch item.Type {
				case "function_call":
					calls++
					require.JSONEq(t, "{}", item.Arguments)
				case "function_call_output":
					results++
					require.Contains(t, item.Output, "case-42")
				}
			}
			require.Equal(t, 1, calls)
			require.Equal(t, 1, results)
			require.Contains(t, string(seen[1].Input), "second Gemini answer")
		})
	}
}

type geminiModelWithStream struct {
	*gemini.Model
	stream bool
}

func (m geminiModelWithStream) GenerateContent(ctx context.Context, request *model.Request) (<-chan *model.Response, error) {
	copy := *request
	copy.Stream = m.stream
	return m.Model.GenerateContent(ctx, &copy)
}

func assertGeminiLookupRound(t *testing.T, body []byte, callID string) {
	t.Helper()
	var request struct {
		Contents []struct {
			Parts []struct {
				FunctionCall     *genai.FunctionCall     `json:"functionCall"`
				FunctionResponse *genai.FunctionResponse `json:"functionResponse"`
			} `json:"parts"`
		} `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(body, &request))
	var calls, results int
	for _, content := range request.Contents {
		for _, part := range content.Parts {
			if call := part.FunctionCall; call != nil {
				calls++
				require.Equal(t, callID, call.ID)
				require.Empty(t, call.Args)
			}
			if result := part.FunctionResponse; result != nil {
				results++
				require.Equal(t, callID, result.ID)
				encoded, err := json.Marshal(result.Response)
				require.NoError(t, err)
				require.Contains(t, string(encoded), "case-42")
			}
		}
	}
	require.Equal(t, 1, calls)
	require.Equal(t, 1, results)
}

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
