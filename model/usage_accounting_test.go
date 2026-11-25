//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/internal/usagemeta"
)

func TestUsagePromptCacheAccounting_SetAndGet(t *testing.T) {
	u := &Usage{PromptTokens: 100, PromptTokensDetails: PromptTokensDetails{CacheCreationTokens: 80}}
	assert.Equal(t, usagemeta.PromptCacheAccountingUnspecified, usagemeta.GetPromptCacheAccounting(u))

	usagemeta.SetPromptCacheAccounting(u, usagemeta.PromptCacheAccountingInclusive)
	assert.Equal(t, usagemeta.PromptCacheAccountingInclusive, usagemeta.GetPromptCacheAccounting(u))

	usagemeta.SetPromptCacheAccounting(u, usagemeta.PromptCacheAccountingExclusive)
	assert.Equal(t, usagemeta.PromptCacheAccountingExclusive, usagemeta.GetPromptCacheAccounting(u))

	usagemeta.SetPromptCacheAccounting(u, usagemeta.PromptCacheAccounting("bogus"))
	assert.Equal(t, usagemeta.PromptCacheAccountingUnspecified, usagemeta.GetPromptCacheAccounting(u))
}

func TestUsagePromptCacheAccounting_NoCacheCountersKeepsZeroComparable(t *testing.T) {
	u := &Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}
	usagemeta.SetPromptCacheAccounting(u, usagemeta.PromptCacheAccountingInclusive)
	assert.Equal(t, usagemeta.PromptCacheAccountingUnspecified, usagemeta.GetPromptCacheAccounting(u))
	assert.Equal(t, Usage{PromptTokens: 10, CompletionTokens: 2, TotalTokens: 12}, *u)

	empty := &Usage{}
	usagemeta.SetPromptCacheAccounting(empty, usagemeta.PromptCacheAccountingInclusive)
	assert.True(t, *empty == (Usage{}))
}

func TestUsagePromptCacheAccounting_InvalidInputs(t *testing.T) {
	var nilUsage *Usage
	assert.NotPanics(t, func() {
		usagemeta.SetPromptCacheAccounting(nilUsage, usagemeta.PromptCacheAccountingInclusive)
		usagemeta.SetPromptCacheAccounting(nil, usagemeta.PromptCacheAccountingInclusive)
		usagemeta.SetPromptCacheAccounting(Usage{}, usagemeta.PromptCacheAccountingInclusive)
	})
	assert.Equal(t, usagemeta.PromptCacheAccountingUnspecified, usagemeta.GetPromptCacheAccounting(nilUsage))
	assert.Equal(t, usagemeta.PromptCacheAccountingUnspecified, usagemeta.GetPromptCacheAccounting(nil))
	assert.Equal(t, usagemeta.PromptCacheAccountingUnspecified, usagemeta.GetPromptCacheAccounting("usage"))
}

func TestUsagePromptCacheAccounting_TravelsWithValue(t *testing.T) {
	newInclusive := func() *Usage {
		u := &Usage{
			PromptTokens:        100,
			CompletionTokens:    10,
			TotalTokens:         110,
			PromptTokensDetails: PromptTokensDetails{CacheCreationTokens: 80},
			TimingInfo:          &TimingInfo{},
		}
		usagemeta.SetPromptCacheAccounting(u, usagemeta.PromptCacheAccountingInclusive)
		return u
	}

	t.Run("Response.Clone", func(t *testing.T) {
		rsp := &Response{Usage: newInclusive()}
		cloned := rsp.Clone()
		require.NotSame(t, rsp.Usage, cloned.Usage)
		require.NotSame(t, rsp.Usage.TimingInfo, cloned.Usage.TimingInfo)
		assert.Equal(t, usagemeta.PromptCacheAccountingInclusive, usagemeta.GetPromptCacheAccounting(cloned.Usage))
	})

	t.Run("in-place value overwrite takes the source accounting", func(t *testing.T) {
		dst := newInclusive()
		src := &Usage{PromptTokens: 100, PromptTokensDetails: PromptTokensDetails{CacheCreationTokens: 80}}
		usagemeta.SetPromptCacheAccounting(src, usagemeta.PromptCacheAccountingExclusive)
		*dst = *src
		assert.Equal(t, usagemeta.PromptCacheAccountingExclusive, usagemeta.GetPromptCacheAccounting(dst))
	})

	t.Run("user-built replacement is unspecified", func(t *testing.T) {
		dst := newInclusive()
		*dst = Usage{PromptTokens: 100, PromptTokensDetails: PromptTokensDetails{CacheCreationTokens: 80}}
		assert.Equal(t, usagemeta.PromptCacheAccountingUnspecified, usagemeta.GetPromptCacheAccounting(dst))
	})

	t.Run("output-only edits keep accounting", func(t *testing.T) {
		u := newInclusive()
		u.CompletionTokens = 11
		u.TotalTokens = 111
		assert.Equal(t, usagemeta.PromptCacheAccountingInclusive, usagemeta.GetPromptCacheAccounting(u))
	})
}

func TestUsagePromptCacheAccounting_NotSerialized(t *testing.T) {
	u := &Usage{PromptTokens: 100, PromptTokensDetails: PromptTokensDetails{CacheCreationTokens: 80}}
	plain, err := json.Marshal(u)
	require.NoError(t, err)
	usagemeta.SetPromptCacheAccounting(u, usagemeta.PromptCacheAccountingInclusive)
	marked, err := json.Marshal(u)
	require.NoError(t, err)
	assert.JSONEq(t, string(plain), string(marked))
}
