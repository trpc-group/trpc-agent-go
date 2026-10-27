//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package openai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/packages/respjson"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func completionUsageFromJSON(t *testing.T, payload string) openai.CompletionUsage {
	t.Helper()
	var usage openai.CompletionUsage
	require.NoError(t, json.Unmarshal([]byte(payload), &usage))
	return usage
}

func TestCompletionUsageToModelUsageCacheTokens(t *testing.T) {
	tests := []struct {
		name              string
		payload           string
		wantCached        int
		wantCacheRead     int
		wantCacheCreation int
	}{
		{
			name: "gateway cache tokens in prompt tokens details",
			payload: `{"prompt_tokens":25194,"completion_tokens":443,"total_tokens":25637,` +
				`"prompt_tokens_details":{"cache_read_tokens":22653,"cache_creation_tokens":586}}`,
			wantCacheRead:     22653,
			wantCacheCreation: 586,
		},
		{
			name: "openai standard cached tokens only",
			payload: `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,` +
				`"prompt_tokens_details":{"cached_tokens":800}}`,
			wantCached: 800,
		},
		{
			name: "standard cached tokens and cache read coexist",
			payload: `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,` +
				`"prompt_tokens_details":{"cached_tokens":800,"cache_read_tokens":900}}`,
			wantCached:    800,
			wantCacheRead: 900,
		},
		{
			name:    "no prompt tokens details",
			payload: `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010}`,
		},
		{
			name: "invalid and negative values are ignored",
			payload: `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,` +
				`"prompt_tokens_details":{"cache_read_tokens":"n/a","cache_creation_tokens":-5}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := completionUsageToModelUsage(completionUsageFromJSON(t, tt.payload))
			assert.Equal(t, tt.wantCached, usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, tt.wantCacheRead, usage.PromptTokensDetails.CacheReadTokens)
			assert.Equal(t, tt.wantCacheCreation, usage.PromptTokensDetails.CacheCreationTokens)
		})
	}
}

func TestSetUsageCacheTokens(t *testing.T) {
	usage := openai.CompletionUsage{}
	usage.PromptTokensDetails.JSON.ExtraFields = map[string]respjson.Field{
		"cached_tokens_extra": respjson.NewField("1"),
	}

	setUsageCacheTokens(&usage, 100, 20)
	cacheReadTokens, cacheCreationTokens := usageCacheTokens(usage)
	assert.Equal(t, 100, cacheReadTokens)
	assert.Equal(t, 20, cacheCreationTokens)

	// Existing provider metadata is preserved.
	assert.Contains(t, usage.PromptTokensDetails.JSON.ExtraFields, "cached_tokens_extra")

	// Zero counts leave the payload untouched.
	before := usage.PromptTokensDetails.JSON.ExtraFields
	setUsageCacheTokens(&usage, 0, 0)
	assert.Equal(t, before, usage.PromptTokensDetails.JSON.ExtraFields)
}

func TestStreamAccumulatorCacheTokens(t *testing.T) {
	m := New("claude-opus-5")
	var buf bytes.Buffer

	contentChunk := openai.ChatCompletionChunk{
		Choices: []openai.ChatCompletionChunkChoice{{
			Index: 0,
			Delta: openai.ChatCompletionChunkChoiceDelta{Content: "hi"},
		}},
	}
	var usageChunk openai.ChatCompletionChunk
	require.NoError(t, json.Unmarshal([]byte(`{
		"choices": [],
		"usage": {
			"prompt_tokens": 25194,
			"completion_tokens": 443,
			"total_tokens": 25637,
			"prompt_tokens_details": {
				"cache_read_tokens": 22653,
				"cache_creation_tokens": 586
			}
		}
	}`), &usageChunk))

	acc := chatStreamAccumulator{}
	m.accumulateChunk(contentChunk, &acc, &buf)
	m.accumulateChunk(usageChunk, &acc, &buf)
	acc.applyCacheTokens()

	resp := m.createFinalResponse(acc.acc, false, nil, "")
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 25194, resp.Usage.PromptTokens)
	assert.Equal(t, 443, resp.Usage.CompletionTokens)
	assert.Equal(t, 22653, resp.Usage.PromptTokensDetails.CacheReadTokens)
	assert.Equal(t, 586, resp.Usage.PromptTokensDetails.CacheCreationTokens)
}

func TestStreamAccumulatorCacheTokensKeepLatestNonZero(t *testing.T) {
	acc := chatStreamAccumulator{}
	acc.recordCacheTokens(completionUsageFromJSON(t,
		`{"prompt_tokens_details":{"cache_read_tokens":100,"cache_creation_tokens":10}}`))
	acc.recordCacheTokens(completionUsageFromJSON(t,
		`{"prompt_tokens_details":{"cache_read_tokens":0,"cache_creation_tokens":0}}`))
	acc.recordCacheTokens(completionUsageFromJSON(t,
		`{"prompt_tokens_details":{"cache_read_tokens":200}}`))

	assert.Equal(t, 200, acc.cacheReadTokens)
	assert.Equal(t, 10, acc.cacheCreationTokens)
}

// cacheUsageJSON is the usage payload returned by a gateway that exposes
// prompt cache counts. It stays on a single line because streaming tests embed
// it directly in an SSE data field.
const cacheUsageJSON = `{"prompt_tokens":25194,"completion_tokens":443,"total_tokens":25637,` +
	`"prompt_tokens_details":{"cache_read_tokens":22653,"cache_creation_tokens":586}}`

func TestGenerateContentNonStreamingCacheTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{
			"id": "cache-non-stream",
			"object": "chat.completion",
			"created": 1699200000,
			"model": "claude-opus-5",
			"choices": [{
				"index": 0,
				"message": {"role": "assistant", "content": "ok"},
				"finish_reason": "stop"
			}],
			"usage": %s
		}`, cacheUsageJSON)
	}))
	defer server.Close()

	m := New("claude-opus-5", WithBaseURL(server.URL), WithAPIKey("test-key"))
	responseChan, err := m.GenerateContent(context.Background(), &model.Request{
		Messages: []model.Message{model.NewUserMessage("hi")},
	})
	require.NoError(t, err)

	var final *model.Response
	for response := range responseChan {
		if response.Done {
			final = response
		}
	}
	require.NotNil(t, final)
	require.NotNil(t, final.Usage)
	assert.Equal(t, 22653, final.Usage.PromptTokensDetails.CacheReadTokens)
	assert.Equal(t, 586, final.Usage.PromptTokensDetails.CacheCreationTokens)
}

func TestGenerateContentStreamingCacheTokens(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"id":"cache-stream","object":"chat.completion.chunk","created":1699200000,"model":"claude-opus-5","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
			`data: {"id":"cache-stream","object":"chat.completion.chunk","created":1699200000,"model":"claude-opus-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
			`data: {"id":"cache-stream","object":"chat.completion.chunk","created":1699200000,"model":"claude-opus-5","choices":[],"usage":` + cacheUsageJSON + `}`,
			`data: [DONE]`,
		}
		for _, chunk := range chunks {
			fmt.Fprintf(w, "%s\n\n", chunk)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
	}))
	defer server.Close()

	m := New("claude-opus-5", WithBaseURL(server.URL), WithAPIKey("test-key"))
	responseChan, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)

	var final *model.Response
	for response := range responseChan {
		if response.Done {
			final = response
		}
	}
	require.NotNil(t, final)
	require.NotNil(t, final.Usage)
	assert.Equal(t, 25194, final.Usage.PromptTokens)
	assert.Equal(t, 443, final.Usage.CompletionTokens)
	assert.Equal(t, 22653, final.Usage.PromptTokensDetails.CacheReadTokens)
	assert.Equal(t, 586, final.Usage.PromptTokensDetails.CacheCreationTokens)
}
