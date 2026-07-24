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
	"os"
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

	// --- Stage 3: 安全门禁 + 沙箱执行 ---
	sandboxCfg := internal.DefaultSandboxConfig()
	executor := internal.NewSandboxExecutor(sandboxCfg, dryRun)

	var sandboxDurationMs int64
	if repoPath != "" {
		result, err := executor.RunGoVet(ctx, repoPath)
		sandboxDurationMs = result.DurationMs
		if err != nil {
			fmt.Fprintf(os.Stderr, "沙箱执行警告: %v (exit=%d)\n", err, result.ExitCode)
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

	// --- Stage 4: 去重 ---
	beforeDedup := len(allFindings)
	allFindings = internal.DeduplicateFindings(allFindings)
	dedupCount := beforeDedup - internal.CountNonDuplicate(allFindings)

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
	} else {
		task.Findings = allFindings
		task.TotalFindings = len(allFindings)
		task.Summary = internal.ComputeSummary(allFindings)
	}

	// --- Stage 8: 报告生成 ---
	task.Status = "completed"
	task.CompletedAt = time.Now().Unix()
	task.DurationMs = time.Since(startTime).Milliseconds()

	if err := internal.GenerateJSONReport(
		outputDir+"/review_report.json", task, dedupCount,
	); err != nil {
		fmt.Fprintf(os.Stderr, "生成 JSON 报告失败: %v\n", err)
	}

	cfg := internal.ReportConfig{
		TaskTitle: prTitle,
		Author:    author,
		Branch:    branch,
	}
	if err := internal.GenerateMarkdownReport(
		outputDir+"/review_report.md", task, cfg,
	); err != nil {
		fmt.Fprintf(os.Stderr, "生成 Markdown 报告失败: %v\n", err)
	}

	// 保存 task 到数据库
	if store != nil {
		store.SaveTask(ctx, task)
		store.SaveMonitoringSummary(ctx, internal.MonitoringSummary{
			TaskID:            taskID,
			TotalDurationMs:   task.DurationMs,
			SandboxDurationMs: sandboxDurationMs,
			ToolCallsCount:    1,
			FindingCount:      task.TotalFindings,
		})
	}

	return task, dedupCount, nil
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
