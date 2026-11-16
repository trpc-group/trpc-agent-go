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

	resp := m.createFinalResponse(acc.acc, false, nil, "")
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 25194, resp.Usage.PromptTokens)
	assert.Equal(t, 443, resp.Usage.CompletionTokens)
	assert.Equal(t, 22653, resp.Usage.PromptTokensDetails.CacheReadTokens)
	assert.Equal(t, 586, resp.Usage.PromptTokensDetails.CacheCreationTokens)
}

// Cache counts are additive, matching the SDK accumulation for prompt,
// completion, and total tokens: gateways that send usage once on the final
// chunk and gateways that send independent increments per chunk both add up.
func TestStreamAccumulatorCacheTokensAddUp(t *testing.T) {
	acc := chatStreamAccumulator{}
	acc.addCacheTokens(completionUsageFromJSON(t,
		`{"prompt_tokens_details":{"cache_read_tokens":100,"cache_creation_tokens":10}}`))
	acc.addCacheTokens(completionUsageFromJSON(t,
		`{"prompt_tokens_details":{"cache_read_tokens":0,"cache_creation_tokens":0}}`))
	acc.addCacheTokens(completionUsageFromJSON(t,
		`{"prompt_tokens_details":{"cache_read_tokens":50}}`))

	cacheReadTokens, cacheCreationTokens := usageCacheTokens(acc.acc.Usage)
	assert.Equal(t, 150, cacheReadTokens)
	assert.Equal(t, 10, cacheCreationTokens)
}

// A custom WithAccumulateChunkTokenUsage result determines the final cache
// counts: the provider-reported values must not overwrite it, and each callback
// call receives the previous result as current.
func TestStreamAccumulatorCacheTokensPreserveCustomUsage(t *testing.T) {
	var callbackCurrent []model.Usage
	accumulate := func(current, delta model.Usage) model.Usage {
		callbackCurrent = append(callbackCurrent, current)
		current.PromptTokens += delta.PromptTokens
		current.CompletionTokens += delta.CompletionTokens
		current.TotalTokens += delta.TotalTokens
		current.PromptTokensDetails.CacheReadTokens = 7
		current.PromptTokensDetails.CacheCreationTokens = 3
		return current
	}
	m := New("claude-opus-5", WithAccumulateChunkTokenUsage(accumulate))

	acc := chatStreamAccumulator{}
	var buf bytes.Buffer
	for _, payload := range []string{
		`{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,` +
			`"prompt_tokens_details":{"cache_read_tokens":100,"cache_creation_tokens":10}}`,
		`{"prompt_tokens":50,"completion_tokens":5,"total_tokens":55,` +
			`"prompt_tokens_details":{"cache_read_tokens":50,"cache_creation_tokens":5}}`,
	} {
		var chunk openai.ChatCompletionChunk
		require.NoError(t, json.Unmarshal([]byte(`{"choices":[],"usage":`+payload+`}`), &chunk))
		m.accumulateChunk(chunk, &acc, &buf)
	}

	resp := m.createFinalResponse(acc.acc, false, nil, "")
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 150, resp.Usage.PromptTokens)
	// Provider-reported 150/15 must not replace the callback's 7/3.
	assert.Equal(t, 7, resp.Usage.PromptTokensDetails.CacheReadTokens)
	assert.Equal(t, 3, resp.Usage.PromptTokensDetails.CacheCreationTokens)

	// The second callback call sees the first call's result as current.
	require.Len(t, callbackCurrent, 2)
	assert.Equal(t, 100, callbackCurrent[1].PromptTokens)
	assert.Equal(t, 7, callbackCurrent[1].PromptTokensDetails.CacheReadTokens)
	assert.Equal(t, 3, callbackCurrent[1].PromptTokensDetails.CacheCreationTokens)
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

	var callbackUsage *model.Usage
	m := New("claude-opus-5", WithBaseURL(server.URL), WithAPIKey("test-key"),
		WithChatStreamCompleteCallback(func(_ context.Context, _ *openai.ChatCompletionNewParams,
			accumulator *openai.ChatCompletionAccumulator, streamErr error) {
			require.NoError(t, streamErr)
			require.NotNil(t, accumulator)
			usage := completionUsageToModelUsage(accumulator.Usage)
			callbackUsage = &usage
		}))
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

	// The stream complete callback observes the cache counts before the final
	// response is emitted.
	require.NotNil(t, callbackUsage)
	assert.Equal(t, 22653, callbackUsage.PromptTokensDetails.CacheReadTokens)
	assert.Equal(t, 586, callbackUsage.PromptTokensDetails.CacheCreationTokens)
}
