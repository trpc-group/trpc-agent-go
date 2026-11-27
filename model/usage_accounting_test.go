//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestUsagePromptCacheAccounting_TravelsWithValue(t *testing.T) {
	newInclusive := func() *model.Usage {
		u := &model.Usage{
			PromptTokens:        100,
			CompletionTokens:    10,
			TotalTokens:         110,
			PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80},
			TimingInfo:          &model.TimingInfo{},
		}
		u.PromptCacheAccounting = model.PromptCacheAccountingInclusive
		return u
	}

	t.Run("Response.Clone", func(t *testing.T) {
		rsp := &model.Response{Usage: newInclusive()}
		cloned := rsp.Clone()
		require.NotSame(t, rsp.Usage, cloned.Usage)
		require.NotSame(t, rsp.Usage.TimingInfo, cloned.Usage.TimingInfo)
		assert.Equal(t, model.PromptCacheAccountingInclusive, cloned.Usage.PromptCacheAccounting)
	})

	t.Run("in-place value overwrite takes the source accounting", func(t *testing.T) {
		dst := newInclusive()
		src := &model.Usage{PromptTokens: 100, PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80}}
		src.PromptCacheAccounting = model.PromptCacheAccountingExclusive
		*dst = *src
		assert.Equal(t, model.PromptCacheAccountingExclusive, dst.PromptCacheAccounting)
	})

	t.Run("user-built replacement is unspecified", func(t *testing.T) {
		dst := newInclusive()
		*dst = model.Usage{PromptTokens: 100, PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80}}
		assert.Equal(t, model.PromptCacheAccountingUnspecified, dst.PromptCacheAccounting)
	})

	t.Run("output-only edits keep accounting", func(t *testing.T) {
		u := newInclusive()
		u.CompletionTokens = 11
		u.TotalTokens = 111
		assert.Equal(t, model.PromptCacheAccountingInclusive, u.PromptCacheAccounting)
	})
}

func TestUsagePromptCacheAccounting_NotSerialized(t *testing.T) {
	u := &model.Usage{PromptTokens: 100, PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80}}
	plain, err := json.Marshal(u)
	require.NoError(t, err)
	u.PromptCacheAccounting = model.PromptCacheAccountingInclusive
	marked, err := json.Marshal(u)
	require.NoError(t, err)
	assert.JSONEq(t, string(plain), string(marked))

	var restored model.Usage
	require.NoError(t, json.Unmarshal(marked, &restored))
	u.PromptCacheAccounting = model.PromptCacheAccountingUnspecified
	assert.Equal(t, *u, restored)
}

func TestUsagePromptCacheAccounting_UnmarshalReusedUsage(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{name: "replacement", data: `{"prompt_tokens":100,"completion_tokens":11,"total_tokens":111,"prompt_tokens_details":{"cache_creation_tokens":80}}`},
		{name: "output-only update", data: `{"completion_tokens":11}`},
		{name: "empty object", data: `{}`},
		{name: "cache-only replacement", data: `{"prompt_tokens_details":{"cache_creation_tokens":80}}`},
		{name: "case-insensitive prompt replacement", data: `{"PROMPT_TOKENS":100}`},
		{name: "duplicate prompt field ending with null", data: `{"prompt_tokens":90,"prompt_tokens":null}`},
		{name: "JSON cannot set the declaration", data: `{"prompt_cache_accounting":"exclusive","PromptCacheAccounting":"exclusive"}`},
		{name: "null is a no-op", data: " \nnull\t"},
		{name: "syntax error preserves value", data: `{"prompt_tokens":`},
		{name: "type error with partial update", data: `{"prompt_tokens":90,"completion_tokens":"invalid"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			u := model.Usage{
				PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110,
				PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80},
				TimingInfo:          &model.TimingInfo{FirstTokenDuration: 123},
			}
			u.PromptCacheAccounting = model.PromptCacheAccountingInclusive
			// Decode the same public fields to check that omitted fields,
			// null, and decode errors retain the standard JSON semantics.
			type plainUsage model.Usage
			want := plainUsage(u)
			wantErr := json.Unmarshal([]byte(tc.data), &want)
			err := json.Unmarshal([]byte(tc.data), &u)
			if wantErr != nil {
				require.Error(t, err)
				assert.IsType(t, wantErr, err)
			} else {
				require.NoError(t, err)
			}
			want.PromptCacheAccounting = model.PromptCacheAccountingInclusive
			assert.Equal(t, model.Usage(want), u)
		})
	}
}

// Anonymous embedding is a public JSON contract: Usage must not intercept the
// enclosing object's decoding and silently discard its other fields.
func TestUsageJSON_AnonymousEmbedding(t *testing.T) {
	data := []byte(`{"prompt_tokens":100,"completion_tokens":11,"request_id":"req-1"}`)
	t.Run("value", func(t *testing.T) {
		type envelope struct {
			model.Usage
			RequestID string `json:"request_id"`
		}
		got := envelope{Usage: model.Usage{PromptCacheAccounting: model.PromptCacheAccountingInclusive}}
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, "req-1", got.RequestID)
		assert.Equal(t, 100, got.PromptTokens)
		assert.Equal(t, 11, got.CompletionTokens)
		assert.Equal(t, model.PromptCacheAccountingInclusive, got.PromptCacheAccounting)
		encoded, err := json.Marshal(got)
		require.NoError(t, err)
		var fields map[string]json.RawMessage
		require.NoError(t, json.Unmarshal(encoded, &fields))
		assert.JSONEq(t, `100`, string(fields["prompt_tokens"]))
		assert.JSONEq(t, `"req-1"`, string(fields["request_id"]))
		assert.NotContains(t, fields, "usage")
	})
	t.Run("pointer", func(t *testing.T) {
		type envelope struct {
			*model.Usage
			RequestID string `json:"request_id"`
		}
		var got envelope
		require.NoError(t, json.Unmarshal(data, &got))
		require.NotNil(t, got.Usage)
		assert.Equal(t, "req-1", got.RequestID)
		assert.Equal(t, 100, got.PromptTokens)
		assert.Equal(t, 11, got.CompletionTokens)
	})
}

func TestUsagePromptCacheAccounting_JSONReplacement(t *testing.T) {
	data := []byte(`{"prompt_tokens":100,"completion_tokens":11,"prompt_tokens_details":{"cache_creation_tokens":80}}`)
	initial := model.Usage{
		PromptTokens:          100,
		PromptTokensDetails:   model.PromptTokensDetails{CacheCreationTokens: 80},
		PromptCacheAccounting: model.PromptCacheAccountingInclusive,
	}
	t.Run("fresh value replacement", func(t *testing.T) {
		got := initial
		var next model.Usage
		require.NoError(t, json.Unmarshal(data, &next))
		got = next
		assert.Equal(t, 100, got.PromptTokens)
		assert.Equal(t, 11, got.CompletionTokens)
		assert.Equal(t, 80, got.PromptTokensDetails.CacheCreationTokens)
		assert.Equal(t, model.PromptCacheAccountingUnspecified, got.PromptCacheAccounting)
	})
	t.Run("explicit reset before in-place decoding", func(t *testing.T) {
		got := initial
		got.PromptCacheAccounting = model.PromptCacheAccountingUnspecified
		require.NoError(t, json.Unmarshal(data, &got))
		assert.Equal(t, 11, got.CompletionTokens)
		assert.Equal(t, model.PromptCacheAccountingUnspecified, got.PromptCacheAccounting)
	})
}
