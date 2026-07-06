//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package model

// MinValidCompletionTokens is the minimum max-completion value accepted by APIs
// that validate this field (e.g. Anthropic requires max_tokens >= 1).
const MinValidCompletionTokens = 1

func sanitizeMaxTokensPtr(in *int) *int {
	if in == nil || *in < MinValidCompletionTokens {
		return nil
	}
	return in
}

// ClampMaxTokensForModel sanitizes in and caps it to the model's documented max output
// tokens when ResolveMaxOutputTokens knows the limit. Returns nil when the value is
// invalid or unset.
func ClampMaxTokensForModel(modelName string, in *int) *int {
	mt := sanitizeMaxTokensPtr(in)
	if mt == nil {
		return nil
	}
	if modelCap := ResolveMaxOutputTokens(modelName); modelCap > 0 && *mt > modelCap {
		capped := modelCap
		return &capped
	}
	return mt
}
