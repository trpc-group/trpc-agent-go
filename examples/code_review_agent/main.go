// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
// code-review-agent 是一个自动代码审查系统。
//
// 输入 git diff、PR patch 或本地变更目录，通过规则引擎检测代码问题，
// 输出结构化审查报告（JSON + Markdown），并持久化到 SQLite 数据库。
//
// 用法：
//
//	code-review-agent --diff-file changes.diff
//	code-review-agent --repo-path /path/to/repo
//	code-review-agent --diff-file changes.diff --dry-run
//	code-review-agent --diff-file changes.diff --rules-dir ./rules/custom
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/findings"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/scoring"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/storage"
)

func main() {
	// ========== 命令行参数 ==========
	diffFile := flag.String("diff-file", "", "diff 文件路径")
	repoPath := flag.String("repo-path", "", "git 仓库路径")
	rulesDir := flag.String("rules-dir", "", "自定义 YAML 规则目录")
	dbPath := flag.String("db", "review.db", "SQLite 数据库路径")
	outputDir := flag.String("output", ".", "报告输出目录")
	sandboxMode := flag.String("sandbox", "container", "沙箱模式：container / local（local 仅作开发 fallback）")
	dryRun := flag.Bool("dry-run", false, "dry-run 模式（不写数据库、不执行沙箱）")
	verbose := flag.Bool("verbose", false, "详细输出")
	flag.Parse()

	// ========== 参数验证 ==========
	if *diffFile == "" && *repoPath == "" {
		fmt.Fprintln(os.Stderr, "错误：必须指定 --diff-file 或 --repo-path")
		flag.Usage()
		os.Exit(1)
	}

	start := time.Now()
	taskID := fmt.Sprintf("task-%s", start.Format("20060102-150405"))
	ctx := context.Background()

	// 监控计数器
	var sandboxDuration time.Duration
	var permissionDeniedCount int
	var exceptionCount int
	var sandboxRuns []storage.SandboxRun
	var permissionDecisions []storage.PermissionDecision

	if *verbose {
		fmt.Printf("🔍 开始审查任务: %s\n", taskID)
	}

	// ========== Step 1: 读取 diff ==========
	var files []diff.FileDiff
	var inputType, inputPath string

	if *diffFile != "" {
		inputType = "diff_file"
		inputPath = *diffFile
		var err error
		files, err = diff.ReadFromFile(*diffFile)
		if err != nil {
			log.Fatalf("读取 diff 文件失败: %v", err)
		}
	} else {
		inputType = "repo_path"
		inputPath = *repoPath
		var err error
		files, err = diff.ReadFromGitDiff(*repoPath)
		if err != nil {
			log.Fatalf("读取 git diff 失败: %v", err)
		}
	}

	if len(files) == 0 {
		fmt.Println("没有变更文件，退出。")
		os.Exit(0)
	}

	goFiles := diff.ChangedGoFiles(files)
	if *verbose {
		fmt.Printf("📄 变更文件: %d 个（其中 Go 文件 %d 个）\n", len(files), len(goFiles))
	}

	// ========== Step 2: 初始化规则引擎 ==========
	engine := rules.NewEngine()

	// 注册 Token 感知规则
	engine.Register(rules.NewTokenSecretRule())
	engine.Register(rules.NewTokenLeakRule())
	engine.Register(rules.NewTokenGoroutineRule())
	engine.Register(rules.NewTokenResourceRule())
	engine.Register(rules.NewTokenErrorRule())
	engine.Register(rules.NewTokenMissingTestRule())

	// 加载 YAML 自定义规则
	if *rulesDir != "" {
		dslRules, err := rules.LoadDSLRules(*rulesDir)
		if err != nil {
			log.Fatalf("加载 YAML 规则失败: %v", err)
		}
		engine.RegisterAll(dslRules...)
		if *verbose {
			fmt.Printf("📋 加载了 %d 条 YAML 自定义规则\n", len(dslRules))
		}
	}

	if *verbose {
		fmt.Printf("⚙️  规则引擎: 已注册 %d 条规则\n", len(engine.Rules()))
	}

	// ========== Step 3: 执行审查 ==========
	ruleStart := time.Now()
	allFindings, err := engine.Run(files)
	ruleDuration := time.Since(ruleStart)
	if err != nil {
		exceptionCount++
		log.Printf("⚠️ 规则执行出错: %v", err)
	}

	if *verbose {
		fmt.Printf("🔎 发现 %d 个问题（去重前）\n", len(allFindings))
	}

	// ========== Step 3.5: 沙箱执行（对 Go 文件）==========
	if !*dryRun && len(goFiles) > 0 && *repoPath != "" {
		if *verbose {
			fmt.Printf("🧪 开始沙箱执行 (%s 模式)...\n", *sandboxMode)
		}

		filter := safety.NewSafetyFilter(nil)
		var sb sandbox.Sandbox

		switch *sandboxMode {
		case "container":
			var err error
			sb, err = sandbox.NewContainerSandbox("")
			if err != nil {
				log.Printf("⚠️ 创建容器沙箱失败，回退到本地: %v", err)
				exceptionCount++
				sb, err = sandbox.NewLocalSandbox("")
				if err != nil {
					log.Printf("⚠️ 创建本地沙箱也失败: %v", err)
					exceptionCount++
				}
			}
		default: // "local"
			var err error
			sb, err = sandbox.NewLocalSandbox("")
			if err != nil {
				log.Printf("⚠️ 创建本地沙箱失败: %v", err)
				exceptionCount++
			}
		}

		if sb != nil {
			defer sb.Close()

			// 对 Go 项目执行 go vet 和 go test
			sandboxCmds := []string{"go vet ./...", "go test -count=1 -timeout=30s ./..."}

			for _, cmd := range sandboxCmds {
				// Permission 检查
				decision := filter.Check(cmd)

				// 记录权限决策
				permDecision := storage.PermissionDecision{
					TaskID:    taskID,
					ToolName:  "sandbox",
					Command:   cmd,
					Action:    string(decision.Decision),
					Reason:    decision.Reason,
					DecidedAt: time.Now(),
				}
				permissionDecisions = append(permissionDecisions, permDecision)

				if decision.Decision == safety.DecisionDeny {
					permissionDeniedCount++
					if *verbose {
						fmt.Printf("  🚫 命令被拒绝: %s (%s)\n", cmd, decision.Reason)
					}
					continue
				}
				if decision.Decision == safety.DecisionAsk {
					if *verbose {
						fmt.Printf("  ❓ 命令需要人工确认: %s (%s)\n", cmd, decision.Reason)
					}
					continue
				}

				// 执行沙箱命令
				sandboxStart := time.Now()
				result, err := sb.Execute(ctx, sandbox.ExecuteOptions{
					Command:   cmd,
					WorkDir:   *repoPath,
					Timeout:   30 * time.Second,
					MaxOutput: 1024 * 1024,
				})
				sandboxDuration += time.Since(sandboxStart)

				// 记录沙箱执行
				run := storage.SandboxRun{
					TaskID:    taskID,
					Command:   cmd,
					Backend:   sb.Name(),
					StartedAt: sandboxStart,
				}

				if err != nil {
					exceptionCount++
					run.ExitCode = -1
					run.Output = fmt.Sprintf("执行错误: %v", err)
					run.Duration = time.Since(sandboxStart).Round(time.Millisecond).String()
					log.Printf("⚠️ 沙箱执行失败 (%s): %v", cmd, err)
				} else {
					run.ExitCode = result.ExitCode
					run.Output = result.Output
					run.Truncated = result.Truncated
					run.Duration = result.Duration

					if result.TimedOut {
						exceptionCount++
						if *verbose {
							fmt.Printf("  ⏰ 命令超时: %s\n", cmd)
						}
					}
				}

				sandboxRuns = append(sandboxRuns, run)

				if *verbose {
					status := "✅"
					if run.ExitCode != 0 {
						status = "❌"
					}
					fmt.Printf("  %s %s (退出码: %d, 耗时: %s)\n", status, cmd, run.ExitCode, run.Duration)
				}
			}
		}
	} else if *dryRun && *verbose {
		fmt.Println("⏭️  dry-run 模式，跳过沙箱执行")
	}

	// ========== Step 4: 去重和分组 ==========
	dedupResult := findings.Deduplicate(allFindings)

	if *verbose {
		fmt.Printf("📊 去重后: %d 个高置信度发现, %d 个低置信度警告, %d 个被移除\n",
			len(dedupResult.Findings), len(dedupResult.Warnings), dedupResult.Removed)
	}

	// ========== Step 5: 风险评分 ==========
	riskScore := scoring.Calculate(dedupResult.Findings, dedupResult.Warnings)

	if *verbose {
		fmt.Printf("\n%s\n", riskScore.ToReport())
	}

	// ========== Step 6: 生成报告 ==========
	reviewReport := report.NewReport(taskID, inputType, inputPath)
	reviewReport.SetResult(dedupResult, len(files), len(goFiles))

	// 填充监控信息
	reviewReport.Monitor.TotalDuration = time.Since(start).Round(time.Millisecond).String()
	reviewReport.Monitor.RuleDuration = ruleDuration.Round(time.Millisecond).String()
	reviewReport.Monitor.SandboxDuration = sandboxDuration.Round(time.Millisecond).String()
	reviewReport.Monitor.ToolCallCount = len(allFindings)
	reviewReport.Monitor.RuleCount = len(engine.Rules())
	reviewReport.Monitor.FilesScanned = len(files)
	reviewReport.Monitor.PermissionDenied = permissionDeniedCount
	reviewReport.Monitor.ExceptionCount = exceptionCount
	reviewReport.Monitor.RiskScore = riskScore.Score
	reviewReport.Monitor.RiskGrade = riskScore.Grade

	// 填充沙箱执行记录
	reviewReport.SandboxRuns = make([]report.SandboxRun, len(sandboxRuns))
	var sandboxSuccess, sandboxFailed, sandboxTimedOut int
	for i, run := range sandboxRuns {
		reviewReport.SandboxRuns[i] = report.SandboxRun{
			Command:   run.Command,
			Backend:   run.Backend,
			ExitCode:  run.ExitCode,
			Output:    run.Output,
			Truncated: run.Truncated,
			Duration:  run.Duration,
		}
		if run.ExitCode == 0 {
			sandboxSuccess++
		} else {
			sandboxFailed++
		}
	}

	// 填充治理拦截摘要
	var deniedCommands []string
	totalPermChecks := len(permissionDecisions)
	permAllowed := 0
	permDenied := 0
	permAsk := 0
	for _, dec := range permissionDecisions {
		switch safety.Decision(dec.Action) {
		case safety.DecisionAllow:
			permAllowed++
		case safety.DecisionDeny:
			permDenied++
			deniedCommands = append(deniedCommands, dec.Command)
		case safety.DecisionAsk:
			permAsk++
		}
	}
	reviewReport.SetGovernance(totalPermChecks, permAllowed, permDenied, permAsk, deniedCommands)

	// 填充沙箱执行摘要
	reviewReport.SetSandboxSummary(
		len(sandboxRuns),
		sandboxSuccess,
		sandboxFailed,
		sandboxTimedOut,
		sandboxDuration.Round(time.Millisecond).String(),
	)

	reviewReport.Finalize(start)

	// 写入 JSON 报告
	jsonPath := *outputDir + "/review_report.json"
	if err := reviewReport.WriteJSON(jsonPath); err != nil {
		log.Fatalf("写入 JSON 报告失败: %v", err)
	}

	// 写入 Markdown 报告
	mdPath := *outputDir + "/review_report.md"
	if err := reviewReport.WriteMarkdown(mdPath); err != nil {
		log.Fatalf("写入 Markdown 报告失败: %v", err)
	}

	if *verbose {
		fmt.Printf("📝 报告已生成: %s, %s\n", jsonPath, mdPath)
	}

	// ========== Step 7: 存储到数据库 ==========
	if !*dryRun {
		store, err := storage.NewSQLiteStore(*dbPath)
		if err != nil {
			log.Fatalf("打开数据库失败: %v", err)
		}
		defer store.Close()

		// 保存任务
		task := &storage.ReviewTask{
			TaskID:       taskID,
			Status:       storage.TaskStatusCompleted,
			InputType:    inputType,
			InputPath:    inputPath,
			FilesCount:   len(files),
			GoFilesCount: len(goFiles),
			StartedAt:    start,
		}
		if err := store.CreateTask(task); err != nil {
			log.Fatalf("保存任务失败: %v", err)
		}

		// 保存 findings
		if err := store.SaveFindings(taskID, dedupResult.Findings); err != nil {
			log.Fatalf("保存 findings 失败: %v", err)
		}

		// 保存沙箱执行记录
		for _, run := range sandboxRuns {
			if err := store.SaveSandboxRun(&run); err != nil {
				log.Printf("⚠️ 保存沙箱执行记录失败: %v", err)
			}
		}

		// 保存权限决策记录
		for _, dec := range permissionDecisions {
			if err := store.SavePermissionDecision(&dec); err != nil {
				log.Printf("⚠️ 保存权限决策记录失败: %v", err)
			}
		}

		// 保存报告
		jsonData, _ := os.ReadFile(jsonPath)
		mdData, _ := os.ReadFile(mdPath)
		if err := store.SaveReport(taskID, string(jsonData), string(mdData)); err != nil {
			log.Fatalf("保存报告失败: %v", err)
		}

		if *verbose {
			fmt.Printf("💾 已保存到数据库: %s (task: %s)\n", *dbPath, taskID)
		}
	} else {
		if *verbose {
			fmt.Println("⏭️  dry-run 模式，跳过数据库写入")
		}
	}

	// ========== Step 8: 完成 ==========
	duration := time.Since(start)
	fmt.Printf("\n✅ 审查完成！耗时: %s\n", duration.Round(time.Millisecond))
	fmt.Printf("   高危: %d  中危: %d  低危: %d  信息: %d\n",
		riskScore.Breakdown["安全问题"].Count+riskScore.Breakdown["敏感信息"].Count,
		riskScore.Breakdown["资源泄漏"].Count+riskScore.Breakdown["错误处理"].Count,
		riskScore.Breakdown["测试覆盖"].Count,
		0)
	fmt.Printf("   风险评分: %.0f/100 (%s)\n", riskScore.Score, riskScore.Grade)
	fmt.Printf("   报告: %s\n", jsonPath)

	if len(sandboxRuns) > 0 {
		fmt.Printf("   沙箱执行: %d 次, 耗时 %s\n", len(sandboxRuns), sandboxDuration.Round(time.Millisecond))
	}
	if permissionDeniedCount > 0 {
		fmt.Printf("   权限拦截: %d 次\n", permissionDeniedCount)
	}
}
