//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"strings"
	"testing"
)

// TestRunFakeModel_Deterministic 验证 fake model 输出确定性一致、无网络、
// 记录合理的调用元数据（验收标准 6）。
func TestRunFakeModel_Deterministic(t *testing.T) {
	summary := ReviewSummary{Total: 5, Critical: 1, High: 2, Medium: 0, Low: 1, Warning: 1}

	s1, mc1, err := RunFakeModel("cr-fake-test", nil, summary)
	if err != nil {
		t.Fatalf("RunFakeModel 失败: %v", err)
	}
	s2, mc2, err := RunFakeModel("cr-fake-test", nil, summary)
	if err != nil {
		t.Fatalf("RunFakeModel 二次调用失败: %v", err)
	}

	if s1 != s2 {
		t.Errorf("fake model 输出应确定性一致:\n%q\n%q", s1, s2)
	}
	if mc1.Model != "fake" {
		t.Errorf("model 名应为 fake, 实际 %q", mc1.Model)
	}
	if mc1.LatencyMs < 0 {
		t.Errorf("latency 不应为负: %d", mc1.LatencyMs)
	}
	if mc1.ResponseLen != len(s1) {
		t.Errorf("response_len 不匹配: %d != %d", mc1.ResponseLen, len(s1))
	}
	if mc1.TaskID != "cr-fake-test" {
		t.Errorf("task id 不匹配: %q", mc1.TaskID)
	}
	_ = mc2
}

// TestRunFakeModel_SummaryContent 验证摘要包含各严重级别计数。
func TestRunFakeModel_SummaryContent(t *testing.T) {
	summary := ReviewSummary{Total: 3, Critical: 1, High: 1, Medium: 1}
	s, _, err := RunFakeModel("cr-fake-test", nil, summary)
	if err != nil {
		t.Fatalf("RunFakeModel 失败: %v", err)
	}
	if !strings.Contains(s, "critical=1") || !strings.Contains(s, "high=1") || !strings.Contains(s, "medium=1") {
		t.Errorf("摘要应包含严重级别计数: %q", s)
	}
}
