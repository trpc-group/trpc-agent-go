//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

import (
	"math"

	imodel "trpc.group/trpc-go/trpc-agent-go/model/internal/model"
)

// MinValidCompletionTokens is the minimum max-completion value accepted by APIs
// that validate this field (e.g. Anthropic requires max_tokens >= 1).
//
// Deprecated: this helper is retained for source compatibility. It is not used
// by the public request API directly.
const MinValidCompletionTokens = imodel.MinValidCompletionTokens

// SanitizeMaxTokensPtr returns in if it points to a value >= MinValidCompletionTokens.
// Otherwise it returns nil so callers can omit the field and let the provider apply defaults.
//
// Deprecated: this helper is retained for source compatibility. Provider
// implementations use internal max-token handling.
func SanitizeMaxTokensPtr(in *int) *int {
	return imodel.ClampMaxTokensForModel("", in)
}

// ClampMaxTokensForModel sanitizes in and caps it to the model's documented max
// output tokens when the model limit is known. It returns nil when the value is
// invalid or unset.
//
// Deprecated: this helper is retained for source compatibility. Provider
// implementations use internal max-token handling.
func ClampMaxTokensForModel(modelName string, in *int) *int {
	return imodel.ClampMaxTokensForModel(modelName, in)
}

// MaxTokensToInt32 converts a max token count for provider APIs that use int32 fields.
// Values outside the int32 range are clamped to avoid overflow when narrowing.
//
// Deprecated: this helper is retained for source compatibility. Provider
// implementations keep provider-specific conversions local.
func MaxTokensToInt32(v int) int32 {
	n := int64(v)
	if n > math.MaxInt32 {
		return math.MaxInt32
	}
	if n < math.MinInt32 {
		return math.MinInt32
	}
	return int32(n)
}
