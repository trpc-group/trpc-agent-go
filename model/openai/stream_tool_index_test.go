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
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	openai "github.com/openai/openai-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestFixToolCallIndices_NegativeIndices(t *testing.T) {
	tests := []struct {
		name    string
		choices string
		indices [][]int64
		mapping map[string]int
	}{
		{
			name:    "first tool call",
			choices: `[{"index":0,"delta":{"tool_calls":[{"index":-1,"id":"call_a","type":"function","function":{"name":"file_write","arguments":"{}"}}]}}]`,
			indices: [][]int64{{0}},
			mapping: map[string]int{"call_a": 0},
		},
		{
			name:    "without ID",
			choices: `[{"index":0,"delta":{"tool_calls":[{"index":-1,"function":{"arguments":"{}"}}]}}]`,
			indices: [][]int64{{0}},
			mapping: map[string]int{},
		},
		{
			name:    "multiple IDs sharing a negative index",
			choices: `[{"index":0,"delta":{"tool_calls":[{"index":-1,"id":"call_a","function":{"name":"first","arguments":"{}"}},{"index":-1,"id":"call_b","function":{"name":"second","arguments":"{}"}}]}}]`,
			indices: [][]int64{{0, 1}},
			mapping: map[string]int{"call_a": 0, "call_b": 1},
		},
		{
			name:    "negative index outside first choice",
			choices: `[{"index":0,"delta":{"content":"text"}},{"index":1,"delta":{"tool_calls":[{"index":-2,"id":"call_b","function":{"name":"second","arguments":"{}"}}]}}]`,
			indices: [][]int64{nil, {0}},
			mapping: map[string]int{},
		},
		{
			name:    "valid sparse index is preserved",
			choices: `[{"index":0,"delta":{"tool_calls":[{"index":3,"id":"call_a","function":{"name":"first","arguments":"{}"}}]}}]`,
			indices: [][]int64{{3}},
			mapping: map[string]int{"call_a": 3},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := `{"id":"test","object":"chat.completion.chunk","choices":` + tt.choices + `}`
			chunk := parseChunkWithExtraFields(t, raw)
			original := parseChunkWithExtraFields(t, raw)
			mapping := make(map[string]int)
			nextIndex := 0
			fixed := fixToolCallIndices(chunk, mapping, &nextIndex)
			assert.Equal(t, original, chunk, "normalization must not mutate the input")
			assert.Equal(t, tt.mapping, mapping)
			for i, indices := range tt.indices {
				for j, index := range indices {
					assert.Equal(t, index, fixed.Choices[i].Delta.ToolCalls[j].Index)
				}
			}
			var acc openai.ChatCompletionAccumulator
			require.NotPanics(t, func() { require.True(t, acc.AddChunk(fixed)) })
			// Finishing the delta also exercises the SDK's saved tool index.
			finish := parseChunkWithExtraFields(t, `{"id":"test","choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`)
			require.NotPanics(t, func() {
				require.True(t, acc.AddChunk(finish))
				acc.JustFinishedToolCall()
			})
		})
	}
}

func TestModel_StreamingNegativeToolIndices(t *testing.T) {
	tests := []struct {
		name   string
		deltas []string
		ids    []string
		args   []string
	}{
		{
			name: "single tool with anonymous continuation",
			deltas: []string{
				`{"tool_calls":[{"index":-1,"id":"call_a","type":"function","function":{"name":"first","arguments":""}}]}`,
				`{"tool_calls":[{"index":-1,"function":{"arguments":"{\"a\":1}"}}]}`,
			},
			ids: []string{"call_a"}, args: []string{`{"a":1}`},
		},
		{
			name: "multiple tools with identified continuations",
			deltas: []string{
				`{"tool_calls":[{"index":-1,"id":"call_a","type":"function","function":{"name":"first","arguments":""}},{"index":-1,"id":"call_b","type":"function","function":{"name":"second","arguments":""}}]}`,
				`{"tool_calls":[{"index":-1,"id":"call_b","function":{"arguments":"{\"b\":2}"}}]}`,
				`{"tool_calls":[{"index":-1,"id":"call_a","function":{"arguments":"{\"a\":1}"}}]}`,
			},
			ids: []string{"call_a", "call_b"}, args: []string{`{"a":1}`, `{"b":2}`},
		},
	}
	for _, tt := range tests {
		for _, api := range []string{"iterator", "channel"} {
			t.Run(tt.name+"/"+api, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "text/event-stream")
					for _, delta := range tt.deltas {
						fmt.Fprintf(w, "data: {\"id\":\"test\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":%s}]}\n\n", delta)
					}
					fmt.Fprint(w, "data: {\"id\":\"test\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
				}))
				defer server.Close()
				m := New("test-model", WithBaseURL(server.URL), WithAPIKey("test-key"))
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				req := &model.Request{
					Messages:         []model.Message{{Role: model.RoleUser, Content: "Call the tools."}},
					GenerationConfig: model.GenerationConfig{Stream: true},
				}
				var final *model.Response
				consume := func(resp *model.Response) bool {
					assert.Nil(t, resp.Error)
					if !resp.IsPartial {
						final = resp
					}
					return true
				}
				if api == "iterator" {
					seq, err := m.GenerateContentIter(ctx, req)
					require.NoError(t, err)
					require.NotPanics(t, func() { seq(consume) })
				} else {
					responses, err := m.GenerateContent(ctx, req)
					require.NoError(t, err)
					for resp := range responses {
						consume(resp)
					}
				}
				require.NotNil(t, final)
				require.Len(t, final.Choices, 1)
				calls := final.Choices[0].Message.ToolCalls
				require.Len(t, calls, len(tt.ids))
				for i, call := range calls {
					assert.Equal(t, tt.ids[i], call.ID)
					require.NotNil(t, call.Index)
					assert.Equal(t, i, *call.Index)
					assert.Equal(t, tt.args[i], string(call.Function.Arguments))
				}
			})
		}
	}
}
