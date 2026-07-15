//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package report

import (
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
)

func TestJSONAndMarkdownReportsIncludeFindings(t *testing.T) {
	rep := review.Result{
		TaskID: "task-1",
		Findings: []review.Finding{{
			Severity: "high",
			Category: "security",
			File:     "main.go",
			Line:     10,
			Title:    "Hardcoded secret",
			Source:   "rule",
			RuleID:   "secret-leak",
		}},
	}

	j, err := BuildJSON(rep)
	if err != nil {
		t.Fatalf("BuildJSON returned error: %v", err)
	}
	if !strings.Contains(string(j), "\"high\"") {
		t.Fatalf("expected JSON report to include finding severity, got %s", string(j))
	}

	md := BuildMarkdown(rep)
	if !strings.Contains(md, "Hardcoded secret") {
		t.Fatalf("expected Markdown report to include finding title, got %s", md)
	}
}

func TestReportsAlwaysExposeCanonicalCapabilityAudit(t *testing.T) {
	rep := review.Result{Metrics: review.Metrics{
		Mode:             "review",
		SandboxRequested: false,
		SandboxExecuted:  false,
		ModelRequested:   true,
		ModelExecuted:    true,
	}}
	j, err := BuildJSON(rep)
	if err != nil {
		t.Fatalf("BuildJSON: %v", err)
	}
	for _, want := range []string{
		`"mode": "review"`,
		`"sandbox_requested": false`,
		`"sandbox_executed": false`,
		`"model_requested": true`,
		`"model_executed": true`,
	} {
		if !strings.Contains(string(j), want) {
			t.Fatalf("JSON missing %q: %s", want, j)
		}
	}
	md := BuildMarkdown(rep)
	if !strings.Contains(md, "Capabilities: mode=review sandbox=requested:false/executed:false model=requested:true/executed:true") {
		t.Fatalf("Markdown missing capability audit: %s", md)
	}
}

func TestChineseMarkdownReportIncludesLocalizedReviewFields(t *testing.T) {
	rep := review.Result{
		Summary: "1 findings, 0 warnings",
		Findings: []review.Finding{{
			Severity:       "high",
			Category:       "security",
			File:           "main.go",
			Line:           10,
			Title:          "Hardcoded secret",
			Evidence:       "Line 10: password = [REDACTED]",
			Recommendation: "Move the value to a secret manager.",
			Confidence:     "high",
			Source:         "skill_run",
			RuleID:         "secret-leak",
			Status:         "finding",
		}},
		Conclusion: review.Conclusion{
			Status:  "fail",
			Reason:  "blocking_findings",
			Summary: "Critical or high severity findings require changes before merge.",
		},
	}

	md := BuildMarkdownChinese(rep)
	for _, want := range []string{
		"# 代码审查报告",
		"## 最终结论",
		"状态: fail",
		"审查发现: 1",
		"[HIGH] main.go:10 Hardcoded secret",
		"证据: Line 10: password = [REDACTED]",
		"修复建议: Move the value to a secret manager.",
		"来源: skill_run",
		"规则: secret-leak",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected Chinese Markdown report to include %q, got %s", want, md)
		}
	}
}

func TestChineseMarkdownReportAddsRuleLocalizedTextWithoutDroppingAuditFields(t *testing.T) {
	rep := review.Result{
		Findings: []review.Finding{{
			Severity:       "critical",
			Category:       "security",
			File:           "config.go",
			Line:           3,
			Title:          "Potential secret appears in added code",
			Evidence:       "const apiKey=[REDACTED]",
			Recommendation: "Replace the literal with a secret manager or environment lookup.",
			Confidence:     "high",
			Source:         "skill_run",
			RuleID:         "secret-leak",
			Status:         "finding",
		}},
	}

	md := BuildMarkdownChinese(rep)
	for _, want := range []string{
		"中文标题: 新增代码疑似包含敏感信息",
		"中文建议: 不要把 API key、token 或 password 写入代码；改用环境变量、密钥管理服务或安全配置注入。",
		"原始标题: Potential secret appears in added code",
		"原始建议: Replace the literal with a secret manager or environment lookup.",
		"来源: skill_run",
		"规则: secret-leak",
		"类别: security",
		"置信度: high",
		"状态: finding",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected localized Chinese report to include %q, got %s", want, md)
		}
	}
}

func TestReportsIncludeGovernanceSandboxArtifactsAndHumanReviewContract(t *testing.T) {
	rep := review.Result{
		TaskID: "task-contract",
		Findings: []review.Finding{{
			Severity:       "critical",
			Category:       "security",
			File:           "config.go",
			Line:           3,
			Title:          "Potential secret appears in added code",
			Recommendation: "Move the value to a secret manager.",
			Source:         "skill_run",
			RuleID:         "secret-leak",
			Status:         "finding",
		}},
		Warnings: []review.Finding{{
			Severity: "low",
			Category: "governance",
			Title:    "Command requires human review",
			Source:   "permission",
			RuleID:   "permission-ask",
			Status:   "needs_human_review",
		}},
		Metrics: review.Metrics{
			FindingCount:      1,
			SeverityCounts:    map[string]int{"critical": 1, "low": 1},
			ExceptionCounts:   map[string]int{"skill_run": 1},
			PermissionBlocks:  1,
			ToolCallCount:     2,
			ModelCallCount:    1,
			ModelFindingCount: 1,
			ModelDurationMS:   3,
			SandboxDurationMS: 12,
			TotalDurationMS:   20,
		},
		GovernanceSummary: review.GovernanceSummary{
			PermissionDecisions: []review.PermissionDecisionSummary{{
				Command: "scripts/check.sh",
				Action:  "allow",
				Reason:  "policy allow",
			}},
		},
		SandboxSummary: review.SandboxSummary{
			Runs: []review.SandboxRunSummary{{
				Command:          "scripts/check.sh",
				Runtime:          "local-fallback",
				Status:           "ok",
				TimeoutMS:        5000,
				OutputLimitBytes: 65536,
				DurationMS:       12,
			}},
		},
		Artifacts: []review.ArtifactSummary{{
			Name: "review_report.json",
			Kind: "report",
			Path: "review_report.json",
		}},
		Conclusion: review.Conclusion{
			Status:  "fail",
			Reason:  "blocking_findings",
			Summary: "Critical or high severity findings require changes before merge.",
		},
	}

	j, err := BuildJSON(rep)
	if err != nil {
		t.Fatalf("BuildJSON returned error: %v", err)
	}
	jsonText := string(j)
	for _, want := range []string{
		"\"human_review_items\"",
		"\"governance_summary\"",
		"\"sandbox_summary\"",
		"\"artifacts\"",
		"\"severity_counts\"",
		"\"recommendation\"",
		"\"conclusion\"",
		"\"blocking_findings\"",
	} {
		if !strings.Contains(jsonText, want) {
			t.Fatalf("expected JSON report to include %s, got %s", want, jsonText)
		}
	}

	md := BuildMarkdown(rep)
	for _, want := range []string{
		"Human Review",
		"Governance",
		"Sandbox",
		"Artifacts",
		"Conclusion",
		"model_calls=1",
		"model_findings=1",
		"blocking_findings",
		"Move the value to a secret manager.",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected Markdown report to include %q, got %s", want, md)
		}
	}
}
