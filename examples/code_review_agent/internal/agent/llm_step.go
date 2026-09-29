//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package agent

import (
	"context"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/llm"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

// configuredModelProvider 为本次运行选择可选 LLM 审查边界。
func (a *Agent) configuredModelProvider(enabled bool) (llm.Provider, llm.Audit) {
	return llm.ConfiguredProvider(llm.ProviderSelectionConfig{
		Enabled: enabled,
		Custom:  a.modelProvider,
		HTTP:    a.cfg.ModelHTTP,
		OpenAI:  a.cfg.ModelOpenAI,
	})
}

// runModelReview 向配置的 Provider 请求增量语义 findings。
func (a *Agent) runModelReview(ctx context.Context, taskID string, provider llm.Provider, audit llm.Audit, result review.Result, diff []byte, inputMeta review.InputMetadata) (review.Result, llm.RunSummary) {
	return llm.RunReview(ctx, taskID, provider, audit, result, diff, inputMeta)
}
