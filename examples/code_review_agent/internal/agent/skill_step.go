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
	"encoding/json"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// runDryRun 只加载 Skill 并记录跳过执行的治理/沙箱摘要。
func (a *Agent) runDryRun(ctx context.Context, taskID string) (review.Result, storage.SandboxRunRecord, storage.DecisionRecord, error) {
	// dry-run 仍验证 Skill 可加载。
	loadArgs := []byte(`{"skill":"code-review"}`)
	if _, err := a.loadTool.Call(ctx, loadArgs); err != nil {
		return review.Result{}, storage.SandboxRunRecord{}, storage.DecisionRecord{}, err
	}
	now := time.Now()
	// 记录跳过执行的审计摘要。
	decision := storage.DecisionRecord{
		TaskID:  taskID,
		Command: defaultSkillCommand,
		Action:  "dry_run",
		Reason:  "executor skipped by dry-run mode",
		At:      now,
	}
	runRecord := storage.SandboxRunRecord{
		TaskID:           taskID,
		Command:          defaultSkillCommand,
		Runtime:          a.cfg.Runtime,
		Status:           "skipped",
		TimeoutMS:        a.cfg.Timeout.Milliseconds(),
		OutputLimitBytes: a.cfg.OutputLimitBytes,
		EnvWhitelist:     sandboxEnvWhitelist,
		At:               now,
	}
	return review.Result{
		Warnings: []review.Finding{{
			Severity:       "low",
			Category:       "governance",
			Title:          "Sandbox execution skipped by dry-run mode",
			Evidence:       "dry-run",
			Recommendation: "Run again with rule-only or sandbox mode before merging.",
			Confidence:     "high",
			Source:         "mode",
			RuleID:         "dry-run-skipped-executor",
			Status:         "needs_human_review",
		}},
	}, runRecord, decision, nil
}

// runSkillChecks 执行 code-review Skill。
func (a *Agent) runSkillChecks(ctx context.Context, taskID string, diff []byte) (review.Result, storage.SandboxRunRecord, storage.DecisionRecord, error) {
	// 先加载受控 Skill。
	loadArgs := []byte(`{"skill":"code-review"}`)
	if _, err := a.loadTool.Call(ctx, loadArgs); err != nil {
		return review.Result{}, storage.SandboxRunRecord{}, storage.DecisionRecord{}, err
	}

	// diff 通过 stdin 传给脚本。
	runArgs, err := json.Marshal(map[string]any{
		"skill":   defaultSkillName,
		"command": defaultSkillCommand,
		"stdin":   string(diff),
		"timeout": int(a.cfg.Timeout.Seconds()),
	})
	if err != nil {
		return review.Result{}, storage.SandboxRunRecord{}, storage.DecisionRecord{}, err
	}

	// skill_run 也必须先审批。
	permReq := &tool.PermissionRequest{
		Tool:        a.runTool,
		ToolName:    a.runTool.Declaration().Name,
		Declaration: a.runTool.Declaration(),
		Arguments:   runArgs,
	}
	perm, err := a.policy.CheckToolPermission(ctx, permReq)
	if err != nil {
		return review.Result{}, storage.SandboxRunRecord{}, storage.DecisionRecord{}, err
	}
	perm, err = tool.NormalizePermissionDecision(perm)
	if err != nil {
		return review.Result{}, storage.SandboxRunRecord{}, storage.DecisionRecord{}, err
	}
	decision := storage.DecisionRecord{
		TaskID: taskID, Command: defaultSkillCommand,
		Action: string(perm.Action), Reason: perm.Reason, At: time.Now(),
	}
	runRecord := storage.SandboxRunRecord{
		TaskID: taskID, Command: defaultSkillCommand,
		Runtime: a.cfg.Runtime, TimeoutMS: a.cfg.Timeout.Milliseconds(),
		OutputLimitBytes: a.cfg.OutputLimitBytes,
		EnvWhitelist:     sandboxEnvWhitelist,
		At:               time.Now(),
	}
	if perm.Action != tool.PermissionActionAllow {
		// 非 allow 转为人工复核项。
		runRecord.Status = string(perm.Action)
		return review.Result{Warnings: []review.Finding{{
			Severity: "low", Category: "governance", Title: "Command requires human review",
			Evidence: perm.Reason, Confidence: "high", Source: "permission",
			RuleID: "permission-non-allow", Status: "needs_human_review",
		}}}, runRecord, decision, nil
	}

	start := time.Now()
	a.emitReviewEvent(ctx, taskID, reviewEventSkillRun, defaultSkillCommand)
	// 通过 skill_run 进入 runtime。
	raw, err := a.runTool.Call(ctx, runArgs)
	runRecord.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		runRecord.Status = "error"
		runRecord.StderrDigest = digestString(err.Error())
		return review.Result{}, runRecord, decision, err
	}
	out, err := decodeSkillRunOutput(raw)
	if err != nil {
		runRecord.Status = "error"
		runRecord.StderrDigest = digestString(err.Error())
		return review.Result{}, runRecord, decision, err
	}
	runRecord.Status = "ok"
	// 以 skill_run 返回值记录状态。
	if out.TimedOut {
		runRecord.Status = "timed_out"
	} else if out.ExitCode != 0 {
		runRecord.Status = "failed"
	}
	runRecord.ExitCode = out.ExitCode
	runRecord.StdoutDigest = digestString(out.Stdout)
	runRecord.StderrDigest = digestString(out.Stderr)
	if runRecord.DurationMS == 0 {
		runRecord.DurationMS = out.DurationMS
	}

	// stdout 承载结构化 findings。
	result, err := parseSkillFindings(out.Stdout)
	return result, runRecord, decision, err
}
