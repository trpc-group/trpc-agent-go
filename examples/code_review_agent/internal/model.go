//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"fmt"
	"time"
)

// ModelCall 记录一次模型调用（审计链路，落库 model_calls 表）。
type ModelCall struct {
	ID          int64  `json:"id"`
	TaskID      string `json:"task_id"`
	Model       string `json:"model"`
	LatencyMs   int64  `json:"latency_ms"`
	ResponseLen int    `json:"response_len"`
	CreatedAt   int64  `json:"created_at"`
}

// RunFakeModel 以确定性 fake model 生成一份语义审查摘要（验收标准 6）。
// 不调用任何网络 API，仅根据已扫描的 findings 汇总出模板化结论，
// 保证 dry-run / fake-model 模式下完整流程耗时远小于 2 分钟，且输出可复现。
func RunFakeModel(taskID string, findings []Finding, summary ReviewSummary) (string, ModelCall, error) {
	start := time.Now()

	resp := fmt.Sprintf(
		"FakeModel 摘要: 共分析 %d 处变更，发现 %d 个问题（critical=%d, high=%d, medium=%d, low=%d, warning=%d）。建议优先处理 critical 与 high 级别问题，其余进入人工复核。",
		summary.Total,
		summary.Critical+summary.High+summary.Medium+summary.Low+summary.Warning,
		summary.Critical, summary.High, summary.Medium, summary.Low, summary.Warning,
	)

	_ = findings // fake model 不消费 findings，仅用于可扩展签名。

	return resp, ModelCall{
		TaskID:      taskID,
		Model:       "fake",
		LatencyMs:   time.Since(start).Milliseconds(),
		ResponseLen: len(resp),
		CreatedAt:   time.Now().Unix(),
	}, nil
}
