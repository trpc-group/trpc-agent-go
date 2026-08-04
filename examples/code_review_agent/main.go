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
//	go run . --diff-file=changes.patch --dry-run --fake-model
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

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
	fakeModel := flag.Bool("fake-model", false, "用确定性 fake model 做离线语义审查（无网络，耗时 < 2min）")
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
	ctx := context.Background()
	task, dedupCount, err := internal.RunReview(ctx, internal.RunReviewInput{
		InputType:    inputType,
		InputContent: inputContent,
		RepoPath:     *repoPath,
		DBPath:       *dbPath,
		OutputDir:    *outputDir,
		DryRun:       *dryRun,
		FakeModel:    *fakeModel,
		PRTitle:      *prTitle,
		Author:       *author,
		Branch:       *branch,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "审查管线失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("审查完成: task_id=%s, status=%s, findings=%d (去重移除 %d)\n",
		task.ID, task.Status, task.Summary.Total-task.Summary.Duplicates, dedupCount)
}
