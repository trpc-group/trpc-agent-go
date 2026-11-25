//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telemetry

import (
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/internal/usagemeta"
	"trpc.group/trpc-go/trpc-agent-go/model"
	semconvtrace "trpc.group/trpc-go/trpc-agent-go/telemetry/semconv/trace"
)

func TestBuildResponseAttributes_PromptCacheAccounting(t *testing.T) {
	withCache := func(accounting usagemeta.PromptCacheAccounting) *model.Usage {
		u := &model.Usage{
			PromptTokens:        100,
			CompletionTokens:    10,
			TotalTokens:         110,
			PromptTokensDetails: model.PromptTokensDetails{CacheCreationTokens: 80},
		}
		usagemeta.SetPromptCacheAccounting(u, accounting)
		return u
	}
	tests := []struct {
		name           string
		usage          *model.Usage
		wantPresent    bool
		wantAccounting string
	}{
		{
			name:           "inclusive",
			usage:          withCache(usagemeta.PromptCacheAccountingInclusive),
			wantPresent:    true,
			wantAccounting: semconvtrace.ValueGenAIUsageInputTokensCacheAccountingInclusive,
		},
		{
			name:           "exclusive",
			usage:          withCache(usagemeta.PromptCacheAccountingExclusive),
			wantPresent:    true,
			wantAccounting: semconvtrace.ValueGenAIUsageInputTokensCacheAccountingExclusive,
		},
		{
			name:           "undeclared accounting with cache counters is written as empty",
			usage:          withCache(usagemeta.PromptCacheAccountingUnspecified),
			wantPresent:    true,
			wantAccounting: "",
		},
		{
			name:        "no cache counters omits the attribute",
			usage:       &model.Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110},
			wantPresent: false,
		},
		{
			name:        "nil usage omits the attribute",
			wantPresent: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attrs := buildResponseAttributes(&model.Response{Usage: tt.usage}, semconvtrace.ValueDefaultErrorType)
			got, ok := attrStringValue(attrs, semconvtrace.KeyGenAIUsageInputTokensCacheAccounting)
			require.Equal(t, tt.wantPresent, ok)
			if tt.wantPresent {
				require.Equal(t, tt.wantAccounting, got)
			}
		})
	}
}
