//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// code_review_agent 是一个自动化的 Go 代码评审工具。
//
// 输入 git diff 或 PR patch，通过静态规则扫描、沙箱执行和敏感信息
// 检测，生成结构化的审查报告（JSON + Markdown），并将结果持久化到
// SQLite 数据库。
//
// 用法:
//
//	go run . --diff-file=changes.patch
//	go run . --diff-file=changes.patch --dry-run --db-path=/tmp/cr.db
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal"
)

func main() {
	// CLI 参数
	diffFile := flag.String("diff-file", "", "diff 文件路径")
	diffText := flag.String("diff", "", "diff 文本内容")
	repoPath := flag.String("repo-path", "", "仓库路径（用于 go vet 和文件检查）")
	dbPath := flag.String("db-path", "review.db", "SQLite 数据库路径")
	outputDir := flag.String("output-dir", ".", "报告输出目录")
	dryRun := flag.Bool("dry-run", false, "dry-run 模式（无 API 调用、无沙箱真执行）")
	prTitle := flag.String("pr-title", "", "PR 标题")
	author := flag.String("author", "", "作者")
	branch := flag.String("branch", "", "分支名")
	flag.Parse()

	if *diffFile == "" && *diffText == "" {
		fmt.Fprintln(os.Stderr, "错误: 必须指定 --diff-file 或 --diff 参数")
		flag.Usage()
		os.Exit(1)
	}

	// 读取 diff 输入
	var inputType, inputContent string
	if *diffFile != "" {
		data, err := os.ReadFile(*diffFile)
		if err != nil {
			fmt.Fprintf(os.Stderr, "读取 diff 文件失败: %v\n", err)
			os.Exit(1)
		}
		inputContent = string(data)
		inputType = "diff_file"
	} else {
		inputContent = *diffText
		inputType = "diff_text"
	}

	// 执行审查管线
	task, dedupCount, err := runPipeline(
		inputType, inputContent,
		*repoPath, *dbPath, *outputDir,
		*dryRun, *prTitle, *author, *branch,
	)
	if err != nil {
		fmt.Fprintf(os.Stderr, "审查管线失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("审查完成: task_id=%s, status=%s, findings=%d (去重移除 %d)\n",
		task.ID, task.Status, task.Summary.Total-task.Summary.Duplicates, dedupCount)
}

// runPipeline 执行完整的代码审查管线。
func runPipeline(
	inputType, inputContent string,
	repoPath, dbPath, outputDir string,
	dryRun bool,
	prTitle, author, branch string,
) (*internal.ReviewTask, int, error) {

	startTime := time.Now()
	ctx := context.Background()

	// 生成 task ID
	inputHash := sha256Hash(inputContent)
	taskID := "cr-" + timestampPrefix() + "-" + inputHash[:8]
	task := internal.NewReviewTask(taskID, inputType, inputHash)

	// --- Stage 1: Diff 解析 ---
	diffFiles, err := internal.ParseDiff(inputContent)
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
	scanner := internal.NewRuleScanner()
	var allFindings []internal.Finding

	for _, df := range diffFiles {
		findings := scanner.ScanFile(df)
		allFindings = append(allFindings, findings...)

		sensFindings := internal.DetectSensitiveInfo(df)
		allFindings = append(allFindings, sensFindings...)
	}

	// 测试缺失检测：新 Go 文件必须有对应 _test.go（repo-path 提供完整
	// 文件列表，否则以 diff 内文件推断）。
	allGoFiles := collectGoFiles(repoPath, diffFiles)
	allFindings = append(allFindings, scanner.CheckMissingTests(allGoFiles, diffFiles)...)

	// --- Stage 3: 安全门禁 + 沙箱执行 ---
	sandboxCfg := internal.DefaultSandboxConfig()
	executor := internal.NewSandboxExecutor(sandboxCfg, dryRun)

	var sandboxDurationMs int64
	var toolCalls, intercepts int
	var sandboxRuns []internal.SandboxRun
	var permissionDecisions []internal.PermissionDecision

	recordPermission := func(result internal.SandboxResult) {
		command, _ := internal.MaskSensitive(result.Command)
		reason, _ := internal.MaskSensitive(result.Recommendation)
		permissionDecisions = append(permissionDecisions, internal.PermissionDecision{
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

	if repoPath != "" {
		result, err := executor.RunGoVet(ctx, repoPath)
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
			command, _ := internal.MaskSensitive(result.Command)
			stdout, _ := internal.MaskSensitive(result.Stdout)
			stderr, _ := internal.MaskSensitive(result.Stderr)
			sandboxRuns = append(sandboxRuns, internal.SandboxRun{
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
				f := internal.NewFinding(
					internal.SeverityLow,
					internal.CategoryErrorHandling,
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

	// --- Stage 4: 去重 + 低置信度降级 ---
	beforeDedup := len(allFindings)
	allFindings = internal.DeduplicateFindings(allFindings)
	dedupCount := beforeDedup - internal.CountNonDuplicate(allFindings)
	// 低置信度问题降级为 warning / needs_human_review，不混入高置信 findings。
	internal.ApplyConfidencePolicy(allFindings)

	// --- Stage 5: 排序 ---
	internal.SortFindings(allFindings)

	// --- Stage 6: 脱敏 ---
	allFindings, maskedCount := internal.MaskSensitiveInFindings(allFindings)
	if maskedCount > 0 {
		fmt.Printf("已脱敏 %d 处敏感信息\n", maskedCount)
	}

	// --- Stage 7: 数据库存储 ---
	var store *internal.Store
	if dbPath != "" {
		store, err = internal.NewStore(dbPath)
		if err != nil {
			task.Status = "failed"
			task.ErrorMessage = fmt.Sprintf("数据库初始化失败: %v", err)
			return task, dedupCount, err
		}
		defer store.Close()

		var uniqueFindings []internal.Finding
		for _, f := range allFindings {
			if !f.IsDuplicate {
				uniqueFindings = append(uniqueFindings, f)
			}
		}

		task.Findings = uniqueFindings
		task.TotalFindings = len(uniqueFindings)
		task.Summary = internal.ComputeSummary(allFindings)

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
	} else {
		task.Findings = allFindings
		task.TotalFindings = len(allFindings)
		task.Summary = internal.ComputeSummary(allFindings)
	}

	// --- Stage 8: 报告生成 ---
	task.Status = "completed"
	task.CompletedAt = time.Now().Unix()
	task.DurationMs = time.Since(startTime).Milliseconds()

	monitoring := internal.MonitoringSummary{
		TaskID:               taskID,
		TotalDurationMs:      task.DurationMs,
		SandboxDurationMs:    sandboxDurationMs,
		ToolCallsCount:       toolCalls,
		PermissionIntercepts: intercepts,
		FindingCount:         task.TotalFindings,
	}
	meta := internal.ReportMeta{
		Monitoring:          monitoring,
		PermissionDecisions: permissionDecisions,
		SandboxRuns:         sandboxRuns,
	}

	cfg := internal.ReportConfig{
		TaskTitle: prTitle,
		Author:    author,
		Branch:    branch,
		Meta:      meta,
	}
	if err := internal.GenerateJSONReport(
		outputDir+"/review_report.json", task, dedupCount, cfg,
	); err != nil {
		fmt.Fprintf(os.Stderr, "生成 JSON 报告失败: %v\n", err)
	}

	if err := internal.GenerateMarkdownReport(
		outputDir+"/review_report.md", task, cfg,
	); err != nil {
		fmt.Fprintf(os.Stderr, "生成 Markdown 报告失败: %v\n", err)
	}

	// 保存 task 到数据库
	if store != nil {
		store.SaveTask(ctx, task)
		store.SaveMonitoringSummary(ctx, monitoring)
	}

	return task, dedupCount, nil
}

// collectGoFiles 收集用于测试缺失检测的 Go 文件列表。
// repoPath 非空时遍历仓库（相对路径与 diff 路径一致）；否则仅用 diff 内
// 出现的文件推断（新增 .go 文件若无对应 _test.go 会被检出）。
func collectGoFiles(repoPath string, diffFiles []internal.DiffFile) []string {
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
