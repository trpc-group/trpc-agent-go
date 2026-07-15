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
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/approval"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/execution"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func sandboxUnavailableAudit(taskID string) (review.Result, storage.DecisionRecord, storage.SandboxRunRecord) {
	now := time.Now()
	reason := "sandbox Go checks require --repo-path"
	decision := storage.DecisionRecord{TaskID: taskID, Command: "go checks", Action: "skipped", Reason: reason, At: now}
	run := storage.SandboxRunRecord{
		TaskID: taskID, Command: "go checks", Status: "skipped", Runtime: "",
		EnvWhitelist: sandboxEnvWhitelist, Output: reason, At: now, FinishedAt: now,
	}
	result := review.Result{Warnings: []review.Finding{{
		Severity: "low", Category: "sandbox", Title: "Sandbox checks require a repository",
		Evidence: reason, Recommendation: "Provide --repo-path to run go test and go vet.",
		Confidence: "high", Source: "sandbox", RuleID: "sandbox-repo-required", Status: "needs_human_review",
	}}}
	return result, decision, run
}

// runGoSandboxChecks 在配置的 runtime 中执行 Go 工程检查。
func (a *Agent) runGoSandboxChecks(ctx context.Context, taskID string, repoPath string) ([]storage.DecisionRecord, []storage.SandboxRunRecord) {
	commands := approval.AllowedReviewCommands(a.cfg.EnableStaticcheck)
	decisions := make([]storage.DecisionRecord, 0, len(commands))
	runs := make([]storage.SandboxRunRecord, 0, len(commands))
	for _, command := range commands {
		commandDecisions, run := a.runGoSandboxCommand(ctx, taskID, repoPath, command)
		decisions = append(decisions, commandDecisions...)
		runs = append(runs, run)
	}
	return decisions, runs
}

// runGoSandboxCommand 执行一条已审批的 Go 检查命令。
func (a *Agent) runGoSandboxCommand(ctx context.Context, taskID string, repoPath string, command string) ([]storage.DecisionRecord, storage.SandboxRunRecord) {
	execCommand := execution.BoundedSandboxCommand(
		execution.SandboxExecCommand(a.cfg.Runtime, command),
		a.cfg.OutputLimitBytes,
	)
	workspaceArgs, _ := execution.WorkspaceArgs(execCommand, a.cfg.Timeout, execution.SandboxEnv(a.cfg.Runtime))
	legacyArgs, _ := json.Marshal(map[string]any{
		"code_blocks": []map[string]string{{
			"language": "bash",
			"code":     execution.SandboxCode(a.cfg.Runtime, repoPath, execCommand),
		}},
		"execution_id": taskID + "-" + strings.ReplaceAll(command, " ", "-"),
	})
	permReq := &tool.PermissionRequest{
		Tool:        a.checkTool,
		ToolName:    "workspace_exec",
		Declaration: a.checkTool.Declaration(),
		Arguments:   workspaceArgs,
	}
	perm, err := a.policy.CheckToolPermission(ctx, permReq)
	if err != nil {
		perm = tool.DenyPermission(err.Error())
	}
	perm, err = tool.NormalizePermissionDecision(perm)
	if err != nil {
		perm = tool.DenyPermission(err.Error())
	}
	decision := storage.DecisionRecord{
		TaskID: taskID, Command: command,
		Action: string(perm.Action), Reason: perm.Reason, At: time.Now(),
	}
	decisions := []storage.DecisionRecord{decision}
	run := storage.SandboxRunRecord{
		TaskID: taskID, Command: command, Runtime: a.cfg.Runtime,
		Status: "skipped", TimeoutMS: a.cfg.Timeout.Milliseconds(),
		OutputLimitBytes: a.cfg.OutputLimitBytes,
		EnvWhitelist:     sandboxEnvWhitelist,
		At:               time.Now(),
	}
	if perm.Action != tool.PermissionActionAllow {
		run.Status = string(perm.Action)
		return decisions, run
	}

	start := time.Now()
	run.ExecutionStarted = true
	execCtx, cancel := context.WithTimeout(ctx, a.cfg.Timeout)
	defer cancel()
	raw, err := execution.RunWorkspaceCommand(execCtx, a.exec, repoPath, execCommand, a.cfg.Timeout, execution.SandboxEnv(a.cfg.Runtime))
	if err != nil {
		fallbackReq := &tool.PermissionRequest{
			Tool:        a.checkTool,
			ToolName:    "execute_code",
			Declaration: a.checkTool.Declaration(),
			Arguments:   legacyArgs,
		}
		fallbackPerm, permissionErr := a.policy.CheckToolPermission(execCtx, fallbackReq)
		if permissionErr != nil {
			fallbackPerm = tool.DenyPermission(permissionErr.Error())
		}
		fallbackPerm, permissionErr = tool.NormalizePermissionDecision(fallbackPerm)
		if permissionErr != nil {
			fallbackPerm = tool.DenyPermission(permissionErr.Error())
		}
		decisions = append(decisions, storage.DecisionRecord{
			TaskID: taskID, Command: command,
			Action: string(fallbackPerm.Action), Reason: fallbackPerm.Reason, At: time.Now(),
		})
		if fallbackPerm.Action != tool.PermissionActionAllow {
			run.Status = string(fallbackPerm.Action)
			run.DurationMS = time.Since(start).Milliseconds()
			return decisions, run
		}
		raw, err = a.checkTool.Call(execCtx, legacyArgs)
	}
	run.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		if execCtx.Err() != nil {
			run.Status = "timed_out"
		} else {
			run.Status = "error"
		}
		run.StderrDigest = digestString(err.Error())
		return decisions, run
	}
	output := sandboxCommandOutput(raw)
	run.StdoutDigest = digestString(output.Text)
	run.Output = sandboxRunOutput(output.Text, a.cfg.OutputLimitBytes)
	if output.ExitCode != nil {
		run.ExitCode = *output.ExitCode
	}
	if output.Status != "" && output.Status != "exited" && output.ExitCode == nil {
		run.Status = output.Status
		return decisions, run
	}
	if run.ExitCode != 0 || strings.Contains(output.Text, "Error executing code block") {
		run.Status = "failed"
		if run.ExitCode == 0 {
			run.ExitCode = 1
		}
		return decisions, run
	}
	run.Status = "ok"
	return decisions, run
}
