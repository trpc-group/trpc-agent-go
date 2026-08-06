// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// Package report 提供审查报告的生成功能。
// 支持 JSON 和 Markdown 两种输出格式。
package report

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
)

// ReviewReport 表示一次完整的代码审查报告。
type ReviewReport struct {
	// 基本信息
	TaskID    string `json:"task_id"`    // 审查任务 ID
	StartTime string `json:"start_time"` // 审查开始时间
	EndTime   string `json:"end_time"`   // 审查结束时间
	Duration  string `json:"duration"`   // 耗时

	// 输入信息
	InputType    string `json:"input_type"`     // 输入类型：diff_file / repo_path / fixture
	InputPath    string `json:"input_path"`     // 输入路径
	FilesCount   int    `json:"files_count"`    // 变更文件数
	GoFilesCount int    `json:"go_files_count"` // Go 文件数

	// 审查结果
	Summary  Summary            `json:"summary"`  // 摘要统计
	Findings []findings.Finding `json:"findings"` // 高置信度发现
	Warnings []findings.Finding `json:"warnings"` // 低置信度警告（需人工复核）

	// 治理拦截摘要
	Governance GovernanceSummary `json:"governance"`

	// 沙箱执行
	SandboxRuns    []SandboxRun   `json:"sandbox_runs,omitempty"`
	SandboxSummary SandboxSummary `json:"sandbox_summary"`

	// 监控字段
	Monitor MonitorInfo `json:"monitor"`
}

// GovernanceSummary 记录治理拦截摘要。
type GovernanceSummary struct {
	TotalChecks    int      `json:"total_checks"`              // 总检查次数
	Allowed        int      `json:"allowed"`                   // 允许执行
	Denied         int      `json:"denied"`                    // 拒绝执行
	AskHuman       int      `json:"ask_human"`                 // 需要人工确认
	DeniedCommands []string `json:"denied_commands,omitempty"` // 被拦截的命令列表
}

// SandboxSummary 记录沙箱执行摘要。
type SandboxSummary struct {
	TotalRuns     int    `json:"total_runs"`     // 总执行次数
	Successful    int    `json:"successful"`     // 成功次数
	Failed        int    `json:"failed"`         // 失败次数
	TimedOut      int    `json:"timed_out"`      // 超时次数
	TotalDuration string `json:"total_duration"` // 总耗时
}

// Summary 是审查结果的摘要统计。
type Summary struct {
	TotalFindings int            `json:"total_findings"` // 高置信度发现数
	TotalWarnings int            `json:"total_warnings"` // 低置信度警告数
	DedupRemoved  int            `json:"dedup_removed"`  // 去重移除数
	BySeverity    map[string]int `json:"by_severity"`    // 按严重级别统计
	ByCategory    map[string]int `json:"by_category"`    // 按分类统计
}

// SandboxRun 记录一次沙箱执行（Week 3 扩展）。
type SandboxRun struct {
	Command   string `json:"command"`
	Backend   string `json:"backend"`
	ExitCode  int    `json:"exit_code"`
	Duration  string `json:"duration"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

// MonitorInfo 记录监控审计信息。
type MonitorInfo struct {
	TotalDuration    string  `json:"total_duration"`    // 总耗时
	RuleDuration     string  `json:"rule_duration"`     // 规则执行耗时
	SandboxDuration  string  `json:"sandbox_duration"`  // 沙箱执行耗时
	ToolCallCount    int     `json:"tool_call_count"`   // 工具调用次数
	RuleCount        int     `json:"rule_count"`        // 规则数量
	FilesScanned     int     `json:"files_scanned"`     // 扫描文件数
	PermissionDenied int     `json:"permission_denied"` // 权限拦截次数
	ExceptionCount   int     `json:"exception_count"`   // 异常次数
	RiskScore        float64 `json:"risk_score"`        // 风险评分
	RiskGrade        string  `json:"risk_grade"`        // 风险等级
}

// NewReport 创建一个新的审查报告。
func NewReport(taskID, inputType, inputPath string) *ReviewReport {
	return &ReviewReport{
		TaskID:    taskID,
		StartTime: time.Now().Format(time.RFC3339),
		InputType: inputType,
		InputPath: inputPath,
		Summary: Summary{
			BySeverity: make(map[string]int),
			ByCategory: make(map[string]int),
		},
		Monitor: MonitorInfo{},
	}
}

// SetResult 设置审查结果（findings 去重后的结果）。
func (r *ReviewReport) SetResult(result findings.DedupResult, filesCount, goFilesCount int) {
	r.Findings = result.Findings
	r.Warnings = result.Warnings
	r.FilesCount = filesCount
	r.GoFilesCount = goFilesCount

	// 统计摘要
	r.Summary.TotalFindings = len(result.Findings)
	r.Summary.TotalWarnings = len(result.Warnings)
	r.Summary.DedupRemoved = result.Removed

	for _, f := range result.Findings {
		r.Summary.BySeverity[string(f.Severity)]++
		r.Summary.ByCategory[string(f.Category)]++
	}
}

// Finalize 设置结束时间和耗时。在审查完成后调用。
func (r *ReviewReport) Finalize(start time.Time) {
	r.EndTime = time.Now().Format(time.RFC3339)
	r.Duration = time.Since(start).Round(time.Millisecond).String()
}

// SetGovernance 设置治理拦截摘要。
func (r *ReviewReport) SetGovernance(totalChecks, allowed, denied, askHuman int, deniedCommands []string) {
	r.Governance = GovernanceSummary{
		TotalChecks:    totalChecks,
		Allowed:        allowed,
		Denied:         denied,
		AskHuman:       askHuman,
		DeniedCommands: deniedCommands,
	}
}

// SetSandboxSummary 设置沙箱执行摘要。
func (r *ReviewReport) SetSandboxSummary(totalRuns, successful, failed, timedOut int, totalDuration string) {
	r.SandboxSummary = SandboxSummary{
		TotalRuns:     totalRuns,
		Successful:    successful,
		Failed:        failed,
		TimedOut:      timedOut,
		TotalDuration: totalDuration,
	}
}

// WriteJSON 将报告写入 JSON 文件。
func (r *ReviewReport) WriteJSON(path string) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化 JSON 失败: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

// WriteMarkdown 将报告写入 Markdown 文件。
func (r *ReviewReport) WriteMarkdown(path string) error {
	md := r.ToMarkdown()
	return os.WriteFile(path, []byte(md), 0644)
}

// ToMarkdown 生成 Markdown 格式的报告内容。
func (r *ReviewReport) ToMarkdown() string {
	return generateMarkdown(r)
}
