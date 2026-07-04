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
const MinValidCompletionTokens = 1

// SanitizeMaxTokensPtr returns in if it points to a value >= MinValidCompletionTokens.
// Otherwise it returns nil so callers can omit the field and let the provider apply defaults.
func SanitizeMaxTokensPtr(in *int) *int {
	if in == nil || *in < MinValidCompletionTokens {
		return nil
	}
	return in
}

// ClampMaxTokensForModel sanitizes in and caps it to the model's documented max output
// tokens when ResolveMaxOutputTokens knows the limit. Returns nil when the value is
// invalid or unset.
func ClampMaxTokensForModel(modelName string, in *int) *int {
	mt := SanitizeMaxTokensPtr(in)
	if mt == nil {
		return nil
	}
	if modelCap := imodel.ResolveMaxOutputTokens(modelName); modelCap > 0 && *mt > modelCap {
		capped := modelCap
		return &capped
	}
	return mt
}

// MaxTokensToInt32 converts a max token count for provider APIs that use int32 fields.
// Values above math.MaxInt32 are clamped to avoid overflow when narrowing.
func MaxTokensToInt32(v int) int32 {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int32(v)
}
