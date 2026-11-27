//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openairesponses_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model/openai/sdkv3/responses"
	openaiserver "trpc.group/trpc-go/trpc-agent-go/server/openai"
)

func TestClientToolSearchStreamFinishesAfterAnswer(t *testing.T) {
	const answer = "Invoice found."
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := conversationItems(t, mustReadJSONBody(t, r)["input"])
		switch {
		case len(items) == 1 && items[0].Role == "user":
			writeSSE(w, `{"type":"response.completed","response":{"id":"r1","status":"completed","output":[`+clientSearchItem+`]}}`)
		case len(items) == 3 && items[2].Type == "tool_search_output" && items[2].CallID == "search_1":
			writeSSE(w, `{"type":"response.output_text.delta","delta":"`+answer+`"}`,
				`{"type":"response.completed","response":{"id":"r2","status":"completed","output":`+messageOutput("msg_done", answer)+`}}`)
		default:
			http.Error(w, "unexpected search history", http.StatusBadRequest)
		}
	}))
	defer upstream.Close()
	handler := func(_ context.Context, req *responses.ToolSearchRequest) ([]responses.ToolSearchEntry, error) {
		return selectSearchCandidates(req, "billing_lookup"), nil
	}
	m := newSearchModel(upstream, responses.WithClientToolSearch(handler))
	server, err := openaiserver.New(openaiserver.WithAgent(newSearchAgent(m, &searchExecutions{}, true)))
	require.NoError(t, err)
	defer server.Close()
	endpoint := httptest.NewServer(server.Handler())
	defer endpoint.Close()

	client := openai.NewClient(option.WithBaseURL(endpoint.URL+"/v1"), option.WithAPIKey("test"), option.WithMaxRetries(0))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream := client.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
		Model:    "gpt-5",
		Messages: []openai.ChatCompletionMessageParamUnion{openai.UserMessage("Find the invoice.")},
	})
	defer stream.Close()
	var content strings.Builder
	var stopped bool
	for stream.Next() {
		for _, choice := range stream.Current().Choices {
			content.WriteString(choice.Delta.Content)
			if choice.FinishReason != "" {
				require.Equal(t, "stop", choice.FinishReason)
				require.Equal(t, answer, content.String(), "received a finish reason before the answer")
				stopped = true
			}
		}
	}
	require.NoError(t, stream.Err())
	require.True(t, stopped, "the final answer must finish the stream")
	require.Equal(t, answer, content.String())
}
