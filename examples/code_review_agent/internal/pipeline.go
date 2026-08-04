//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package internal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RunReviewInput 是 RunReview 的输入参数。
// InputType 为 "diff_file" 或 "diff_text"。
type RunReviewInput struct {
	InputType    string
	InputContent string
	RepoPath     string
	DBPath       string
	OutputDir    string
	DryRun       bool
	// FakeModel 表示是否以确定性的 fake model 做一次离线语义审查
	// （无网络调用，满足验收标准 6 的 dry-run / fake-model 模式）。
	FakeModel bool
	PRTitle   string
	Author    string
	Branch    string
}

// RunReview 执行完整的代码审查管线。
// 返回审查任务、去重移除数、错误。管线被下沉到 internal 包，
// 使 CLI、测试与 benchmark 评测复用同一条真实路径
// （ParseDiff -> Scan -> Sandbox/Gate -> Dedup -> Mask -> DB -> Report）。
func RunReview(ctx context.Context, in RunReviewInput) (*ReviewTask, int, error) {
	startTime := time.Now()
	inputType, inputContent := in.InputType, in.InputContent

	// 生成 task ID
	inputHash := sha256Hash(inputContent)
	taskID := "cr-" + timestampPrefix() + "-" + inputHash[:8]
	task := NewReviewTask(taskID, inputType, inputHash)

	// --- Stage 1: Diff 解析 ---
	diffFiles, err := ParseDiff(inputContent)
	if err != nil {
		task.Status = "failed"
		task.ErrorMessage = fmt.Sprintf("diff 解析失败: %v", err)
		return task, 0, err
	}
	task.TotalFiles = len(diffFiles)

	if len(diffFiles) == 0 {
		task.Status = "completed"
		task.CompletedAt = time.Now().Unix()
		task.DurationMs = time.Since(startTime).Milliseconds()
		return task, 0, nil
	}

	// --- Stage 2: 规则扫描 ---
	scanner := NewRuleScanner()
	var allFindings []Finding

	for _, df := range diffFiles {
		findings := scanner.ScanFile(df)
		allFindings = append(allFindings, findings...)

		sensFindings := DetectSensitiveInfo(df)
		allFindings = append(allFindings, sensFindings...)
	}

	// 测试缺失检测：新 Go 文件必须有对应 _test.go（repo-path 提供完整
	// 文件列表，否则以 diff 内文件推断）。
	allGoFiles := collectGoFiles(in.RepoPath, diffFiles)
	allFindings = append(allFindings, scanner.CheckMissingTests(allGoFiles, diffFiles)...)

	// --- Stage 3: 安全门禁 + 沙箱执行 ---
	sandboxCfg := DefaultSandboxConfig()
	executor := NewSandboxExecutor(sandboxCfg, in.DryRun)

	var sandboxDurationMs int64
	var toolCalls, intercepts int
	var sandboxRuns []SandboxRun
	var permissionDecisions []PermissionDecision

	recordPermission := func(result SandboxResult) {
		command, _ := MaskSensitive(result.Command)
		reason, _ := MaskSensitive(result.Recommendation)
		permissionDecisions = append(permissionDecisions, PermissionDecision{
			TaskID:      taskID,
			Command:     command,
			Decision:    result.Decision,
			RuleID:      result.RuleID,
			RiskLevel:   result.RiskLevel,
			Reason:      reason,
			Intercepted: result.Intercepted,
			CreatedAt:   time.Now().Unix(),
		})
	}

	if in.RepoPath != "" {
		result, err := executor.RunGoVet(ctx, in.RepoPath)
		recordPermission(result)
		if result.Intercepted {
			intercepts++
			fmt.Fprintf(os.Stderr, "沙箱命令被安全策略拦截 (decision=%s, rule=%s)\n",
				result.Decision, result.RuleID)
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "沙箱执行警告: %v (exit=%d)\n", err, result.ExitCode)
		} else {
			toolCalls++
			sandboxDurationMs += result.DurationMs
			// 沙箱输出在落库/报告前脱敏，防止命令输出中的仓库数据或密钥泄漏。
			command, _ := MaskSensitive(result.Command)
			stdout, _ := MaskSensitive(result.Stdout)
			stderr, _ := MaskSensitive(result.Stderr)
			sandboxRuns = append(sandboxRuns, SandboxRun{
				TaskID:     taskID,
				Command:    command,
				ExitCode:   result.ExitCode,
				Stdout:     stdout,
				Stderr:     stderr,
				DurationMs: result.DurationMs,
				TimedOut:   result.TimedOut,
				CreatedAt:  time.Now().Unix(),
			})
		}
		if result.Stderr != "" || result.ExitCode != 0 {
			vetLines := parseVetOutput(result.Stderr + result.Stdout)
			for _, vl := range vetLines {
				f := NewFinding(
					SeverityLow,
					CategoryErrorHandling,
					vl.file, vl.line,
					"go vet 发现问题",
					vl.message,
					"请根据 go vet 建议修复代码",
					"go_vet",
					"govet_001",
				)
				f.Confidence = 0.9
				allFindings = append(allFindings, f)
			}
		}
	}

	// --- Stage 3.5: fake model 语义摘要（验收标准 6） ---
	// 确定性、无网络，记录一次 tool call，保证 dry-run / fake-model
	// 模式完整流程耗时远小于 2 分钟。
	var modelSummary string
	var modelCalls []ModelCall
	if in.FakeModel {
		// 注意：用 = 而非 := 给已声明的 modelSummary 赋值，避免 if 块内 shadow。
		var mc ModelCall
		var err error
		modelSummary, mc, err = RunFakeModel(taskID, allFindings, ComputeSummary(allFindings))
		if err != nil {
			fmt.Fprintf(os.Stderr, "fake model 调用失败: %v\n", err)
		} else {
			modelCalls = append(modelCalls, mc)
			toolCalls++
			fmt.Printf("%s\n", modelSummary)
		}
	}

	// --- Stage 4: 去重 + 低置信度降级 ---
	beforeDedup := len(allFindings)
	allFindings = DeduplicateFindings(allFindings)
	dedupCount := beforeDedup - CountNonDuplicate(allFindings)
	// 低置信度问题降级为 warning / needs_human_review，不混入高置信 findings。
	ApplyConfidencePolicy(allFindings)

	// --- Stage 5: 排序 ---
	SortFindings(allFindings)

	// --- Stage 6: 脱敏 ---
	allFindings, maskedCount := MaskSensitiveInFindings(allFindings)
	if maskedCount > 0 {
		fmt.Printf("已脱敏 %d 处敏感信息\n", maskedCount)
	}

	// --- Stage 7: 数据库存储 ---
	var store *Store
	if in.DBPath != "" {
		store, err = NewStore(in.DBPath)
		if err != nil {
			task.Status = "failed"
			task.ErrorMessage = fmt.Sprintf("数据库初始化失败: %v", err)
			return task, dedupCount, err
		}
		defer store.Close()

		var uniqueFindings []Finding
		for _, f := range allFindings {
			if !f.IsDuplicate {
				uniqueFindings = append(uniqueFindings, f)
			}
		}

		task.Findings = uniqueFindings
		task.TotalFindings = len(uniqueFindings)
		task.Summary = ComputeSummary(allFindings)

		if err := store.InsertFindingsBatch(ctx, taskID, uniqueFindings); err != nil {
			task.Status = "failed"
			task.ErrorMessage = fmt.Sprintf("存储 findings 失败: %v", err)
			return task, dedupCount, err
		}

		// 落库沙箱执行与权限决策记录（审计链路）。
		for _, run := range sandboxRuns {
			if err := store.InsertSandboxRun(ctx, run); err != nil {
				fmt.Fprintf(os.Stderr, "存储 sandbox run 失败: %v\n", err)
			}
		}
		for _, d := range permissionDecisions {
			if err := store.InsertPermissionDecision(ctx, d); err != nil {
				fmt.Fprintf(os.Stderr, "存储 permission decision 失败: %v\n", err)
			}
		}
		for _, mc := range modelCalls {
			if err := store.InsertModelCall(ctx, mc); err != nil {
				fmt.Fprintf(os.Stderr, "存储 model call 失败: %v\n", err)
			}
		}
	} else {
		task.Findings = allFindings
		task.TotalFindings = len(allFindings)
		task.Summary = ComputeSummary(allFindings)
	}

	// --- Stage 8: 报告生成 ---
	task.Status = "completed"
	task.CompletedAt = time.Now().Unix()
	task.DurationMs = time.Since(startTime).Milliseconds()

	// 监控异常分布（验收标准 8）：从沙箱运行记录统计超时 / 失败计数。
	monitoring := MonitoringSummary{
		TaskID:               taskID,
		TotalDurationMs:      task.DurationMs,
		SandboxDurationMs:    sandboxDurationMs,
		ToolCallsCount:       toolCalls,
		PermissionIntercepts: intercepts,
		FindingCount:         task.TotalFindings,
	}
	for _, r := range sandboxRuns {
		if r.TimedOut {
			monitoring.TimeoutCount++
		} else if r.ExitCode != 0 {
			monitoring.SandboxFailureCount++
		}
	}
	meta := ReportMeta{
		Monitoring:          monitoring,
		PermissionDecisions: permissionDecisions,
		SandboxRuns:         sandboxRuns,
		ModelCalls:          modelCalls,
		ModelSummary:        modelSummary,
	}

	cfg := ReportConfig{
		TaskTitle: in.PRTitle,
		Author:    in.Author,
		Branch:    in.Branch,
		Meta:      meta,
	}
	if err := GenerateJSONReport(
		in.OutputDir+"/review_report.json", task, dedupCount, cfg,
	); err != nil {
		fmt.Fprintf(os.Stderr, "生成 JSON 报告失败: %v\n", err)
	}

	if err := GenerateMarkdownReport(
		in.OutputDir+"/review_report.md", task, cfg,
	); err != nil {
		fmt.Fprintf(os.Stderr, "生成 Markdown 报告失败: %v\n", err)
	}

	// 报告路径落库（验收标准 3）：报告可经 DB 按 task 定位到磁盘产物。
	task.ReportJSONPath = in.OutputDir + "/review_report.json"
	task.ReportMDPath = in.OutputDir + "/review_report.md"
	task.ReportGeneratedAt = time.Now().Unix()

	// 保存 task 到数据库
	if store != nil {
		store.SaveTask(ctx, task)
		store.SaveMonitoringSummary(ctx, monitoring)
		store.SaveReportMeta(ctx, taskID, task.ReportJSONPath, task.ReportMDPath, task.ReportGeneratedAt)
	}

	return task, dedupCount, nil
}

// collectGoFiles 收集用于测试缺失检测的 Go 文件列表。
// repoPath 非空时遍历仓库（相对路径与 diff 路径一致）；否则仅用 diff 内
// 出现的文件推断（新增 .go 文件若无对应 _test.go 会被检出）。
func collectGoFiles(repoPath string, diffFiles []DiffFile) []string {
	var files []string
	if repoPath != "" {
		_ = filepath.WalkDir(repoPath, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".go") {
				if rel, relErr := filepath.Rel(repoPath, path); relErr == nil {
					files = append(files, rel)
				} else {
					files = append(files, path)
				}
			}
			return nil
		})
		return files
	}
	for _, df := range diffFiles {
		files = append(files, df.NewPath)
	}
	return files
}

// vetLine 表示 go vet 输出的一行。
type vetLine struct {
	file    string
	line    int
	message string
}

// parseVetOutput 简单解析 go vet 输出（格式：file:line:col: message）。
func parseVetOutput(output string) []vetLine {
	var lines []vetLine
	for _, l := range strings.Split(output, "\n") {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		parts := strings.SplitN(l, ":", 4)
		if len(parts) >= 3 {
			file := parts[0]
			line := 0
			fmt.Sscanf(parts[1], "%d", &line)
			msg := ""
			if len(parts) >= 4 {
				msg = strings.TrimSpace(parts[3])
			} else {
				msg = strings.TrimSpace(parts[2])
			}
			if file != "" {
				lines = append(lines, vetLine{file: file, line: line, message: msg})
			}
		}
	}
	return lines
}

func sha256Hash(s string) string {
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:])
}

func timestampPrefix() string {
	return time.Now().Format("20060102150405")
}
