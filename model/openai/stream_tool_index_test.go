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
		{
			name:    "reserve valid index before negative index",
			choices: `[{"index":0,"delta":{"tool_calls":[{"index":-1,"id":"call_a","function":{"name":"first","arguments":"{}"}},{"index":0,"id":"call_b","function":{"name":"second","arguments":"{}"}}]}}]`,
			indices: [][]int64{{1, 0}},
			mapping: map[string]int{"call_a": 1, "call_b": 0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := `{"id":"test","object":"chat.completion.chunk","choices":` + tt.choices + `}`
			chunk := parseChunkWithExtraFields(t, raw)
			original := parseChunkWithExtraFields(t, raw)
			state := newToolCallIndexState()
			states := map[int64]*toolCallIndexState{0: state}
			fixed := fixToolCallIndices(chunk, states)
			assert.Equal(t, original, chunk, "normalization must not mutate the input")
			assert.Equal(t, tt.mapping, state.idToIndexMap)
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

func TestFixToolCallIndices_ChoiceScopedMappings(t *testing.T) {
	states := make(map[int64]*toolCallIndexState)
	chunks := []string{
		`{"id":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":-1,"id":"shared","function":{"name":"first","arguments":""}}]}},{"index":1,"delta":{"tool_calls":[{"index":0,"id":"valid","function":{"name":"second","arguments":""}},{"index":-1,"id":"shared","function":{"name":"third","arguments":""}}]}}]}`,
		`{"id":"test","choices":[{"index":1,"delta":{"tool_calls":[{"index":-1,"function":{"arguments":"{\"c\":3}"}},{"index":0,"function":{"arguments":"{\"b\":2}"}}]}},{"index":0,"delta":{"tool_calls":[{"index":-1,"function":{"arguments":"{\"a\":1}"}}]}}]}`,
		`{"id":"test","choices":[{"index":1,"delta":{"tool_calls":[{"index":-1,"id":"shared","function":{"arguments":""}}]}}]}`,
	}
	var acc openai.ChatCompletionAccumulator
	var m Model
	mapping := make(map[string]int)
	for _, raw := range chunks {
		chunk := parseChunkWithExtraFields(t, raw)
		original := parseChunkWithExtraFields(t, raw)
		fixed := fixToolCallIndices(chunk, states)
		assert.Equal(t, original, chunk)
		m.updateToolCallIndexMapping(fixed, mapping)
		require.True(t, acc.AddChunk(fixed))
	}
	require.Len(t, acc.Choices, 2)
	require.Len(t, acc.Choices[0].Message.ToolCalls, 1)
	require.Len(t, acc.Choices[1].Message.ToolCalls, 2)
	assert.Equal(t, "shared", acc.Choices[0].Message.ToolCalls[0].ID)
	assert.Equal(t, `{"a":1}`, acc.Choices[0].Message.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "valid", acc.Choices[1].Message.ToolCalls[0].ID)
	assert.Equal(t, `{"b":2}`, acc.Choices[1].Message.ToolCalls[0].Function.Arguments)
	assert.Equal(t, "shared", acc.Choices[1].Message.ToolCalls[1].ID)
	assert.Equal(t, `{"c":3}`, acc.Choices[1].Message.ToolCalls[1].Function.Arguments)
	final := m.processAccumulatedToolCalls(acc, mapping, nil)
	require.Len(t, final, 1)
	require.NotNil(t, final[0].Index)
	assert.Equal(t, 0, *final[0].Index)
}

func TestFixToolCallIndices_ExplicitIndexAlias(t *testing.T) {
	states := make(map[int64]*toolCallIndexState)
	chunks := []string{
		`{"id":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":1,"id":"call_a","function":{"name":"first","arguments":""}}]}}]}`,
		`{"id":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_a","function":{"arguments":"{\"a\":"}}]}}]}`,
		`{"id":"test","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"1}"}}]}}]}`,
	}
	var acc openai.ChatCompletionAccumulator
	for _, raw := range chunks {
		fixed := fixToolCallIndices(parseChunkWithExtraFields(t, raw), states)
		assert.Equal(t, int64(1), fixed.Choices[0].Delta.ToolCalls[0].Index)
		require.True(t, acc.AddChunk(fixed))
	}
	var m Model
	final := m.processAccumulatedToolCalls(acc, states[0].idToIndexMap, nil)
	require.Len(t, final, 1)
	assert.Equal(t, "call_a", final[0].ID)
	assert.Equal(t, `{"a":1}`, string(final[0].Function.Arguments))
	require.NotNil(t, final[0].Index)
	assert.Equal(t, 1, *final[0].Index)
}

func TestModel_StreamingNegativeToolIndices(t *testing.T) {
	tests := []struct {
		name           string
		deltas         []string
		ids            []string
		args           []string
		partialIndices [][]int // -1 represents an omitted index.
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
		{
			name: "mixed indices in one chunk with anonymous continuation",
			deltas: []string{
				`{"tool_calls":[{"index":-1,"id":"call_a","type":"function","function":{"name":"first","arguments":""}},{"index":0,"id":"call_b","type":"function","function":{"name":"second","arguments":""}}]}`,
				`{"tool_calls":[{"index":-1,"id":"call_a","function":{"arguments":"{\"a\":1}"}}]}`,
				`{"tool_calls":[{"index":0,"function":{"arguments":"{\"b\":2}"}}]}`,
			},
			ids: []string{"call_b", "call_a"}, args: []string{`{"b":2}`, `{"a":1}`},
		},
		{
			name: "valid index arrives after negative index",
			deltas: []string{
				`{"tool_calls":[{"index":-1,"id":"call_a","type":"function","function":{"name":"first","arguments":""}}]}`,
				`{"tool_calls":[{"index":0,"id":"call_b","type":"function","function":{"name":"second","arguments":""}}]}`,
				`{"tool_calls":[{"index":0,"function":{"arguments":"{\"b\":2}"}}]}`,
				`{"tool_calls":[{"index":-1,"function":{"arguments":"{\"a\":1}"}}]}`,
			},
			ids: []string{"call_a", "call_b"}, args: []string{`{"a":1}`, `{"b":2}`},
		},
		{
			name: "negative index arrives after valid index",
			deltas: []string{
				`{"tool_calls":[{"index":0,"id":"call_b","type":"function","function":{"name":"second","arguments":""}}]}`,
				`{"tool_calls":[{"index":-1,"id":"call_a","type":"function","function":{"name":"first","arguments":""}}]}`,
				`{"tool_calls":[{"index":-1,"function":{"arguments":"{\"a\":1}"}}]}`,
				`{"tool_calls":[{"index":0,"function":{"arguments":"{\"b\":2}"}}]}`,
			},
			ids: []string{"call_b", "call_a"}, args: []string{`{"b":2}`, `{"a":1}`},
		},
		{
			name: "shared negative index does not displace valid index",
			deltas: []string{
				`{"tool_calls":[{"index":-1,"id":"call_a","type":"function","function":{"name":"first","arguments":""}},{"index":-1,"id":"call_b","type":"function","function":{"name":"second","arguments":""}},{"index":1,"id":"call_c","type":"function","function":{"name":"third","arguments":""}}]}`,
				`{"tool_calls":[{"index":-1,"id":"call_b","function":{"arguments":"{\"b\":2}"}}]}`,
				`{"tool_calls":[{"index":1,"function":{"arguments":"{\"c\":3}"}}]}`,
				`{"tool_calls":[{"index":-1,"id":"call_a","function":{"arguments":"{\"a\":1}"}}]}`,
			},
			ids: []string{"call_a", "call_c", "call_b"}, args: []string{`{"a":1}`, `{"c":3}`, `{"b":2}`},
		},
		{
			name: "missing index on identified continuation",
			deltas: []string{
				`{"tool_calls":[{"index":1,"id":"call_a","type":"function","function":{"name":"first","arguments":""}}]}`,
				`{"tool_calls":[{"id":"call_a","function":{"arguments":"{\"a\":1}"}}]}`,
				`{"tool_calls":[{"index":0,"id":"call_b","type":"function","function":{"name":"second","arguments":""}}]}`,
				`{"tool_calls":[{"index":0,"function":{"arguments":"{\"b\":2}"}}]}`,
			},
			ids: []string{"call_b", "call_a"}, args: []string{`{"b":2}`, `{"a":1}`},
			partialIndices: [][]int{{1}, {1}, {0}, {0}},
		},
		{
			name: "null index on identified continuation",
			deltas: []string{
				`{"tool_calls":[{"index":1,"id":"call_a","type":"function","function":{"name":"first","arguments":""}}]}`,
				`{"tool_calls":[{"index":null,"id":"call_a","function":{"arguments":"{\"a\":1}"}}]}`,
				`{"tool_calls":[{"index":0,"id":"call_b","type":"function","function":{"name":"second","arguments":""}}]}`,
				`{"tool_calls":[{"index":0,"function":{"arguments":"{\"b\":2}"}}]}`,
			},
			ids: []string{"call_b", "call_a"}, args: []string{`{"b":2}`, `{"a":1}`},
			partialIndices: [][]int{{1}, {1}, {0}, {0}},
		},
		{
			name: "missing index on new tool does not claim provider zero",
			deltas: []string{
				`{"tool_calls":[{"id":"call_a","type":"function","function":{"name":"first","arguments":""}}]}`,
				`{"tool_calls":[{"index":0,"id":"call_b","type":"function","function":{"name":"second","arguments":""}}]}`,
				`{"tool_calls":[{"id":"call_a","function":{"arguments":"{\"a\":1}"}}]}`,
				`{"tool_calls":[{"index":0,"function":{"arguments":"{\"b\":2}"}}]}`,
			},
			ids: []string{"call_a", "call_b"}, args: []string{`{"a":1}`, `{"b":2}`},
			partialIndices: [][]int{{-1}, {1}, {-1}, {1}},
		},
		{
			name: "anonymous continuation at assigned index",
			deltas: []string{
				`{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"first","arguments":""}},{"index":0,"id":"call_b","type":"function","function":{"name":"second","arguments":""}}]}`,
				`{"tool_calls":[{"index":0,"function":{"arguments":"{\"a\":1}"}}]}`,
				`{"tool_calls":[{"index":1,"function":{"arguments":"{\"b\":2}"}}]}`,
			},
			ids: []string{"call_a", "call_b"}, args: []string{`{"a":1}`, `{"b":2}`},
			partialIndices: [][]int{{0, 1}, {0}, {1}},
		},
		{
			name: "anonymous declaration does not reuse occupied index",
			deltas: []string{
				`{"tool_calls":[{"index":-1,"id":"call_a","type":"function","function":{"name":"first","arguments":""}}]}`,
				`{"tool_calls":[{"index":0,"type":"function","function":{"name":"second","arguments":""}}]}`,
				`{"tool_calls":[{"index":-1,"function":{"arguments":"{\"a\":1}"}}]}`,
				`{"tool_calls":[{"index":0,"function":{"arguments":"{\"b\":2}"}}]}`,
			},
			ids: []string{"call_a", "auto_call_1"}, args: []string{`{"a":1}`, `{"b":2}`},
			partialIndices: [][]int{{0}, {1}, {0}, {1}},
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
				m := New("test-model", WithBaseURL(server.URL), WithAPIKey("test-key"),
					WithShowToolCallDelta(tt.partialIndices != nil))
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				req := &model.Request{
					Messages:         []model.Message{{Role: model.RoleUser, Content: "Call the tools."}},
					GenerationConfig: model.GenerationConfig{Stream: true},
				}
				var final *model.Response
				var partialIndices [][]int
				consume := func(resp *model.Response) bool {
					assert.Nil(t, resp.Error)
					if !resp.IsPartial {
						final = resp
					} else if len(resp.Choices) > 0 && len(resp.Choices[0].Delta.ToolCalls) > 0 {
						var indices []int
						for _, call := range resp.Choices[0].Delta.ToolCalls {
							index := -1
							if call.Index != nil {
								index = *call.Index
							}
							indices = append(indices, index)
						}
						partialIndices = append(partialIndices, indices)
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
				if tt.partialIndices != nil {
					assert.Equal(t, tt.partialIndices, partialIndices)
				}
			})
		}
	}
}
