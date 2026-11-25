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
			name: "openai cache_write_tokens only",
			payload: `{"prompt_tokens":309,"completion_tokens":82,"total_tokens":391,` +
				`"completion_tokens_details":{"reasoning_tokens":60},` +
				`"prompt_tokens_details":{"cache_write_tokens":256}}`,
			wantCacheCreation: 256,
		},
		{
			name: "openai cached_tokens and cache_write_tokens coexist",
			payload: `{"prompt_tokens":1566,"completion_tokens":100,"total_tokens":1666,` +
				`"prompt_tokens_details":{"cached_tokens":1024,"cache_write_tokens":256}}`,
			wantCached:        1024,
			wantCacheCreation: 256,
		},
		{
			name: "cache_write_tokens used when cache_creation_tokens is zero",
			payload: `{"prompt_tokens":500,"completion_tokens":10,"total_tokens":510,` +
				`"prompt_tokens_details":{"cache_creation_tokens":0,"cache_write_tokens":256}}`,
			wantCacheCreation: 256,
		},
		{
			name: "cache_write_tokens takes priority over cache_creation_tokens without double counting",
			payload: `{"prompt_tokens":500,"completion_tokens":10,"total_tokens":510,` +
				`"prompt_tokens_details":{"cache_creation_tokens":128,"cache_write_tokens":256}}`,
			wantCacheCreation: 256,
		},
		{
			name: "top-level usage uses the same precedence as prompt_tokens_details",
			payload: `{"prompt_tokens":500,"completion_tokens":10,"total_tokens":510,` +
				`"cache_creation_input_tokens":64,"cache_creation_tokens":128,"cache_write_tokens":256}`,
			wantCacheCreation: 256,
		},
		{
			name: "top-level zero placeholder does not shadow a later alias",
			payload: `{"prompt_tokens":500,"completion_tokens":10,"total_tokens":510,` +
				`"cache_write_tokens":0,"cache_creation_input_tokens":64}`,
			wantCacheCreation: 64,
		},
		{
			name: "prompt_tokens_details cache_creation_input_tokens alias",
			payload: `{"prompt_tokens":2048,"completion_tokens":50,"total_tokens":2098,` +
				`"prompt_tokens_details":{"cached_tokens":1024,"cache_creation_input_tokens":512}}`,
			wantCached:        1024,
			wantCacheCreation: 512,
		},
		{
			name: "top-level usage cache_creation_input_tokens fallback",
			payload: `{"prompt_tokens":2048,"completion_tokens":50,"total_tokens":2098,` +
				`"prompt_tokens_details":{"cached_tokens":1024},` +
				`"cache_creation_input_tokens":512}`,
			wantCached:        1024,
			wantCacheCreation: 512,
		},
		{
			name: "prompt_tokens_details takes priority over top-level usage fallback",
			payload: `{"prompt_tokens":2048,"completion_tokens":50,"total_tokens":2098,` +
				`"prompt_tokens_details":{"cache_write_tokens":256},` +
				`"cache_creation_input_tokens":512}`,
			wantCacheCreation: 256,
		},
		{
			name:    "no prompt tokens details",
			payload: `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010}`,
		},
		{
			name: "invalid negative and float values are ignored",
			payload: `{"prompt_tokens":1000,"completion_tokens":10,"total_tokens":1010,` +
				`"prompt_tokens_details":{"cache_read_tokens":"n/a","cache_creation_tokens":-5,"cache_write_tokens":12.5}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usage := completionUsageToModelUsage(completionUsageFromJSON(t, tt.payload))
			assert.Equal(t, tt.wantCached, usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, tt.wantCacheRead, usage.PromptTokensDetails.CacheReadTokens)
			assert.Equal(t, tt.wantCacheCreation, usage.PromptTokensDetails.CacheCreationTokens)
			encoded, err := json.Marshal(usage.PromptTokensDetails)
			require.NoError(t, err)
			expectedDetails := map[string]int{
				"cached_tokens": tt.wantCached,
			}
			if tt.wantCacheCreation != 0 {
				expectedDetails["cache_creation_tokens"] = tt.wantCacheCreation
			}
			if tt.wantCacheRead != 0 {
				expectedDetails["cache_read_tokens"] = tt.wantCacheRead
			}
			expectedJSON, err := json.Marshal(expectedDetails)
			require.NoError(t, err)
			require.JSONEq(t, string(expectedJSON), string(encoded))
		})
	}
}

func TestCompletionUsageToModelUsageZeroIsEmptyStruct(t *testing.T) {
	got := completionUsageToModelUsage(openai.CompletionUsage{})
	assert.Equal(t, model.Usage{}, got)

	nonZero := completionUsageToModelUsage(completionUsageFromJSON(t,
		`{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,"prompt_tokens_details":{"cache_write_tokens":80}}`))
	assert.Equal(t, model.Usage{}, inverseOpenAISDKAddChunkUsage(nonZero, nonZero))
}

func TestSetUsageCacheTokens(t *testing.T) {
	usage := openai.CompletionUsage{}
	usage.PromptTokensDetails.JSON.ExtraFields = map[string]respjson.Field{
		"cached_tokens_extra":              respjson.NewField("1"),
		extraFieldCacheWriteTokens:         respjson.NewField("256"),
		extraFieldCacheCreationInputTokens: respjson.NewField("128"),
	}

	setUsageCacheTokens(&usage, 100, 20)
	cacheReadTokens, cacheCreationTokens := usageCacheTokens(usage)
	assert.Equal(t, 100, cacheReadTokens)
	assert.Equal(t, 20, cacheCreationTokens)

	// Existing provider metadata is preserved, while stale write aliases are replaced.
	assert.Contains(t, usage.PromptTokensDetails.JSON.ExtraFields, "cached_tokens_extra")
	assert.NotContains(t, usage.PromptTokensDetails.JSON.ExtraFields, extraFieldCacheWriteTokens)
	assert.NotContains(t, usage.PromptTokensDetails.JSON.ExtraFields, extraFieldCacheCreationInputTokens)

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

func TestStreamAccumulatorCacheWriteTokensAddUpAndCustomAccumulator(t *testing.T) {
	acc := chatStreamAccumulator{}
	acc.addCacheTokens(completionUsageFromJSON(t,
		`{"prompt_tokens_details":{"cached_tokens":512,"cache_write_tokens":256}}`))
	acc.addCacheTokens(completionUsageFromJSON(t,
		`{"prompt_tokens_details":{"cache_write_tokens":128}}`))

	cacheReadTokens, cacheCreationTokens := usageCacheTokens(acc.acc.Usage)
	assert.Equal(t, 0, cacheReadTokens)
	assert.Equal(t, 384, cacheCreationTokens)

	// Snapshot-based custom accumulator should keep the latest snapshot without doubling.
	latest := func(current, delta model.Usage) model.Usage {
		if delta.PromptTokens == 0 &&
			delta.CompletionTokens == 0 &&
			delta.TotalTokens == 0 &&
			delta.PromptTokensDetails.CachedTokens == 0 &&
			delta.PromptTokensDetails.CacheReadTokens == 0 &&
			delta.PromptTokensDetails.CacheCreationTokens == 0 {
			return current
		}
		return delta
	}
	m := New("gpt-5", WithAccumulateChunkTokenUsage(latest))
	snapAcc := chatStreamAccumulator{}
	var buf bytes.Buffer
	for _, payload := range []string{
		`{"prompt_tokens":309,"completion_tokens":40,"total_tokens":349,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":256}}`,
		`{"prompt_tokens":309,"completion_tokens":82,"total_tokens":391,"prompt_tokens_details":{"cached_tokens":0,"cache_write_tokens":256}}`,
	} {
		var chunk openai.ChatCompletionChunk
		require.NoError(t, json.Unmarshal([]byte(`{"choices":[],"usage":`+payload+`}`), &chunk))
		m.accumulateChunk(chunk, &snapAcc, &buf)
	}
	resp := m.createFinalResponse(snapAcc.acc, false, nil, "")
	require.NotNil(t, resp.Usage)
	assert.Equal(t, 309, resp.Usage.PromptTokens)
	assert.Equal(t, 82, resp.Usage.CompletionTokens)
	assert.Equal(t, 0, resp.Usage.PromptTokensDetails.CachedTokens)
	assert.Equal(t, 0, resp.Usage.PromptTokensDetails.CacheReadTokens)
	assert.Equal(t, 256, resp.Usage.PromptTokensDetails.CacheCreationTokens)
}

func TestGenerateContentCacheWriteTokensEndToEnd(t *testing.T) {
	const writeUsageJSON = `{"prompt_tokens":309,"completion_tokens":82,"total_tokens":391,` +
		`"completion_tokens_details":{"reasoning_tokens":60},` +
		`"prompt_tokens_details":{"cached_tokens":32,"cache_write_tokens":256}}`

	for _, stream := range []bool{false, true} {
		name := "non-streaming"
		if stream {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
				if !stream {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{
						"id": "cache-write-non-stream",
						"object": "chat.completion",
						"created": 1699200000,
						"model": "gpt-5",
						"choices": [{
							"index": 0,
							"message": {"role": "assistant", "content": "ok"},
							"finish_reason": "stop"
						}],
						"usage": %s
					}`, writeUsageJSON)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				chunks := []string{
					`data: {"id":"cache-write-stream","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
					`data: {"id":"cache-write-stream","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
					`data: {"id":"cache-write-stream","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[],"usage":` + writeUsageJSON + `}`,
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

			m := New("gpt-5", WithBaseURL(server.URL), WithAPIKey("test-key"))
			responseChan, err := m.GenerateContent(context.Background(), &model.Request{
				Messages:         []model.Message{model.NewUserMessage("hi")},
				GenerationConfig: model.GenerationConfig{Stream: stream},
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
			assert.Equal(t, 309, final.Usage.PromptTokens)
			assert.Equal(t, 82, final.Usage.CompletionTokens)
			assert.Equal(t, 391, final.Usage.TotalTokens)
			assert.Equal(t, 60, final.Usage.CompletionTokensDetails.ReasoningTokens)
			assert.Equal(t, 32, final.Usage.PromptTokensDetails.CachedTokens)
			assert.Equal(t, 0, final.Usage.PromptTokensDetails.CacheReadTokens)
			assert.Equal(t, 256, final.Usage.PromptTokensDetails.CacheCreationTokens)
		})
	}
}

func TestGenerateContentStreamingCustomAccumulatorCacheWriteTokens(t *testing.T) {
	const writeOnlyUsageJSON = `{"prompt_tokens":100,"completion_tokens":10,"total_tokens":110,` +
		`"prompt_tokens_details":{"cache_write_tokens":80}}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunks := []string{
			`data: {"id":"acc-stream","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}],"usage":null}`,
			`data: {"id":"acc-stream","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[],"usage":` + writeOnlyUsageJSON + `}`,
			`data: {"id":"acc-stream","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":null}`,
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

	var seenCurrents []model.Usage
	var seenDeltas []model.Usage
	accumulate := func(current, delta model.Usage) model.Usage {
		seenCurrents = append(seenCurrents, current)
		seenDeltas = append(seenDeltas, delta)
		if delta == (model.Usage{}) {
			return current
		}
		return model.Usage{
			PromptTokens:     delta.PromptTokens,
			CompletionTokens: delta.CompletionTokens,
			TotalTokens:      delta.TotalTokens,
			PromptTokensDetails: model.PromptTokensDetails{
				CacheCreationTokens: delta.PromptTokensDetails.CacheCreationTokens,
			},
		}
	}

	m := New("gpt-5", WithBaseURL(server.URL), WithAPIKey("test-key"), WithAccumulateChunkTokenUsage(accumulate))
	ch, err := m.GenerateContent(context.Background(), &model.Request{
		Messages:         []model.Message{model.NewUserMessage("hi")},
		GenerationConfig: model.GenerationConfig{Stream: true},
	})
	require.NoError(t, err)

	var final *model.Response
	for rsp := range ch {
		if rsp.Done {
			final = rsp
		}
	}
	require.NotNil(t, final)
	require.NotNil(t, final.Usage)
	assert.Equal(t, 100, final.Usage.PromptTokens)
	assert.Equal(t, 10, final.Usage.CompletionTokens)
	assert.Equal(t, 110, final.Usage.TotalTokens)
	assert.Equal(t, 80, final.Usage.PromptTokensDetails.CacheCreationTokens)

	// Empty chunks arrive as zero model.Usage{}.
	require.Len(t, seenDeltas, 3)
	assert.Equal(t, model.Usage{}, seenCurrents[0])
	assert.Equal(t, model.Usage{}, seenDeltas[0])
	assert.Equal(t, model.Usage{}, seenCurrents[1])
	assert.Equal(t, 80, seenDeltas[1].PromptTokensDetails.CacheCreationTokens)
	assert.Equal(t, model.Usage{}, seenDeltas[2])
	assert.Equal(t, 100, seenCurrents[2].PromptTokens)
}

func TestShouldSkipEmptyChunkDetailOnlyUsage(t *testing.T) {
	m := New("gpt-5")
	tests := []struct {
		name     string
		raw      string
		wantSkip bool
	}{
		{
			name:     "empty delta with zero usage is skipped",
			raw:      `{"choices":[{"index":0,"delta":{},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
			wantSkip: true,
		},
		{
			name:     "empty tool_calls array with zero usage is skipped",
			raw:      `{"choices":[{"index":0,"delta":{"tool_calls":[]},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
			wantSkip: true,
		},
		{
			name:     "empty tool_calls array with cache_write_tokens only is kept",
			raw:      `{"choices":[{"index":0,"delta":{"tool_calls":[]},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_tokens_details":{"cache_write_tokens":256}}}`,
			wantSkip: false,
		},
		{
			name:     "empty delta with cache_write_tokens only is kept",
			raw:      `{"choices":[{"index":0,"delta":{},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_tokens_details":{"cache_write_tokens":256}}}`,
			wantSkip: false,
		},
		{
			name:     "empty delta with cache_read_tokens only is kept",
			raw:      `{"choices":[{"index":0,"delta":{},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_tokens_details":{"cache_read_tokens":128}}}`,
			wantSkip: false,
		},
		{
			name:     "empty delta with cached_tokens only is kept",
			raw:      `{"choices":[{"index":0,"delta":{},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_tokens_details":{"cached_tokens":64}}}`,
			wantSkip: false,
		},
		{
			name:     "empty delta with reasoning_tokens only is kept",
			raw:      `{"choices":[{"index":0,"delta":{},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"completion_tokens_details":{"reasoning_tokens":32}}}`,
			wantSkip: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var chunk openai.ChatCompletionChunk
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &chunk))
			assert.Equal(t, tt.wantSkip, m.shouldSkipEmptyChunk(chunk))
		})
	}
}

func TestHasAccumulatorPayloadBeyondReasoningDetailOnlyUsage(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{
			name: "pure reasoning chunk without usage returns false",
			raw:  `{"choices":[{"index":0,"delta":{"reasoning_content":"step 1"},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
			want: false,
		},
		{
			name: "reasoning chunk with detail-only cache_write_tokens returns true",
			raw:  `{"choices":[{"index":0,"delta":{"reasoning_content":"step 1"},"finish_reason":null}],"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,"prompt_tokens_details":{"cache_write_tokens":256}}}`,
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var chunk openai.ChatCompletionChunk
			require.NoError(t, json.Unmarshal([]byte(tt.raw), &chunk))
			stripped, ok := stripReasoningFromChunkForAccumulator(chunk)
			require.True(t, ok)
			assert.Equal(t, tt.want, hasAccumulatorPayloadBeyondReasoning(stripped))
		})
	}
}

func TestGenerateContentDetailOnlyCacheUsageEndToEnd(t *testing.T) {
	const detailOnlyUsageJSON = `{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0,` +
		`"prompt_tokens_details":{"cache_write_tokens":256}}`

	cases := []struct {
		name   string
		stream bool
		chunks []string
	}{
		{
			name:   "non-streaming",
			stream: false,
		},
		{
			name:   "streaming empty delta",
			stream: true,
			chunks: []string{
				`data: {"id":"detail-only-stream","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
				`data: {"id":"detail-only-stream","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":null}],"usage":` + detailOnlyUsageJSON + `}`,
				`data: {"id":"detail-only-stream","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
			},
		},
		{
			name:   "streaming empty tool_calls array",
			stream: true,
			chunks: []string{
				`data: {"id":"detail-only-tc","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","content":"ok"},"finish_reason":null}]}`,
				`data: {"id":"detail-only-tc","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{"tool_calls":[]},"finish_reason":null}],"usage":` + detailOnlyUsageJSON + `}`,
				`data: {"id":"detail-only-tc","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
			},
		},
		{
			name:   "streaming reasoning chunk with detail-only usage",
			stream: true,
			chunks: []string{
				`data: {"id":"detail-only-reasoning","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"think"},"finish_reason":null}],"usage":` + detailOnlyUsageJSON + `}`,
				`data: {"id":"detail-only-reasoning","object":"chat.completion.chunk","created":1699200000,"model":"gpt-5","choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}`,
				`data: [DONE]`,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasSuffix(r.URL.Path, "/chat/completions") {
					http.Error(w, "not found", http.StatusNotFound)
					return
				}
				if !tc.stream {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{
						"id": "detail-only-non-stream",
						"object": "chat.completion",
						"created": 1699200000,
						"model": "gpt-5",
						"choices": [{
							"index": 0,
							"message": {"role": "assistant", "content": "ok"},
							"finish_reason": "stop"
						}],
						"usage": %s
					}`, detailOnlyUsageJSON)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, chunk := range tc.chunks {
					fmt.Fprintf(w, "%s\n\n", chunk)
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
				}
			}))
			defer server.Close()

			m := New("gpt-5", WithBaseURL(server.URL), WithAPIKey("test-key"))
			ch, err := m.GenerateContent(context.Background(), &model.Request{
				Messages:         []model.Message{model.NewUserMessage("hi")},
				GenerationConfig: model.GenerationConfig{Stream: tc.stream},
			})
			require.NoError(t, err)

			var final *model.Response
			for rsp := range ch {
				if rsp.Done {
					final = rsp
				}
			}
			require.NotNil(t, final)
			require.NotNil(t, final.Usage)
			assert.Equal(t, 0, final.Usage.PromptTokens)
			assert.Equal(t, 0, final.Usage.CompletionTokens)
			assert.Equal(t, 0, final.Usage.TotalTokens)
			assert.Equal(t, 256, final.Usage.PromptTokensDetails.CacheCreationTokens)
		})
	}
}
