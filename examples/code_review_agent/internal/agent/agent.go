//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package agent 编排基于 trpc-agent-go 的代码评审链路。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/approval"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/execution"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/llm"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/storage/sqlite"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"
	"trpc.group/trpc-go/trpc-agent-go/artifact"
	artifactinmemory "trpc.group/trpc-go/trpc-agent-go/artifact/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	agentevent "trpc.group/trpc-go/trpc-agent-go/event"
	skillrepo "trpc.group/trpc-go/trpc-agent-go/skill"
	telemetrytrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	toolcodeexec "trpc.group/trpc-go/trpc-agent-go/tool/codeexec"
	toolskill "trpc.group/trpc-go/trpc-agent-go/tool/skill"
)

const (
	// RuntimeContainer 是默认沙箱运行时。
	RuntimeContainer = execution.RuntimeContainer
	// RuntimeLocalFallback 仅用于本地开发和测试。
	RuntimeLocalFallback = execution.RuntimeLocalFallback
	// RuntimeE2B 是预留的 E2B 沙箱入口；当前显式返回 unsupported。
	RuntimeE2B = execution.RuntimeE2B

	// ModeReview 执行确定性规则，并按独立能力开关追加沙箱或模型检查。
	ModeReview = "review"
	// ModeRuleOnly 只执行确定性规则。
	// Deprecated: use ModeReview with both capability switches disabled.
	ModeRuleOnly = "rule-only"
	// ModeDryRun 只演练治理和落库。
	ModeDryRun = "dry-run"
	// ModeSandbox 执行规则和 Go 检查。
	// Deprecated: use ModeReview with SandboxEnabled set to true.
	ModeSandbox = "sandbox"
	// ModeFakeModel 运行规则链路和 deterministic fake model provider。
	// Deprecated: use ModeReview with ModelEnabled set to true.
	ModeFakeModel = "fake-model"
)

const (
	defaultSkillName             = "code-review"
	defaultSkillCommand          = "scripts/check.sh"
	defaultOutputLimitBytes      = 64 * 1024
	defaultMaxArtifactBytes      = 1024 * 1024
	defaultMaxArtifactTotalBytes = 4 * 1024 * 1024
	defaultMaxArtifactCount      = 4
	defaultTimeout               = 30 * time.Second
	taskCleanupTimeout           = 5 * time.Second
	containerRepoMountPath       = execution.ContainerRepoMountPath
	defaultContainerImage        = execution.DefaultContainerImage
	goSandboxCacheDir            = execution.GoSandboxCacheDir
	goSandboxBinary              = execution.GoSandboxBinary
	goSandboxPath                = execution.GoSandboxPath
	sandboxEnvWhitelist          = execution.SandboxEnvWhitelist
)

// Config 保存一次审查的依赖和边界。
type Config struct {
	// SkillsRoot 是 Skill 根目录。
	SkillsRoot string
	// Runtime 是执行器类型。
	Runtime string
	// SQLitePath 非空时启用落库。
	SQLitePath string
	// OutputDir 是报告目录。
	OutputDir string
	// FixturesRoot 是样本 diff 根目录。
	FixturesRoot string
	// MaxInputBytes limits loaded or generated diff input.
	MaxInputBytes int64
	// ContainerRepoHostPath 是容器只读挂载源。
	ContainerRepoHostPath string
	// Timeout 是执行超时。
	Timeout time.Duration
	// OutputLimitBytes 是输出上限。
	OutputLimitBytes int
	// MaxArtifactBytes 是单个产物大小上限。
	MaxArtifactBytes int64
	// MaxArtifactTotalBytes limits all report artifacts for one review.
	MaxArtifactTotalBytes int64
	// MaxArtifactCount limits the number of report artifacts.
	MaxArtifactCount int
	// EnableStaticcheck 控制可选 staticcheck。
	EnableStaticcheck bool
	// ArtifactService 接入官方 artifact service。
	ArtifactService artifact.Service
	// ModelProvider 是可选的模型审查边界；fake-model 默认使用 deterministic provider。
	ModelProvider llm.Provider
	// ModelHTTP 是显式开启的 HTTP 模型 provider 配置。
	ModelHTTP llm.HTTPConfig
	// ModelOpenAI 是显式开启的官方 OpenAI-compatible 模型 provider 配置。
	ModelOpenAI llm.OpenAIConfig
	// EventSink 接收本项目通过官方 event.Event 暴露的阶段事件。
	EventSink func(context.Context, *agentevent.Event)
	// ExecutorFactory overrides runtime executor construction for tests.
	ExecutorFactory execution.ExecutorFactory
}

// Request 描述一次审查输入。
type Request struct {
	// DiffFile 是外部 diff 文件。
	DiffFile string
	// FileList 是待审文件路径列表。
	FileList string
	// RepoPath 是本地 Git 工作区。
	RepoPath string
	// Fixture 是内置样本名。
	Fixture string
	// BaseRef 是审查上下文中的基础引用。
	BaseRef string
	// HeadRef 是审查上下文中的目标引用。
	HeadRef string
	// Mode 是执行模式。
	Mode string
	// SandboxEnabled 显式控制可选 Go 沙箱检查；nil 时允许旧模式推导默认值。
	SandboxEnabled *bool
	// ModelEnabled 显式控制模型评审；nil 时允许旧模式推导默认值。
	ModelEnabled *bool
}

// defaultPermissionPolicy 返回代码审查命令的固定 allowlist。
func defaultPermissionPolicy(enableStaticcheck bool, outputLimit int) tool.PermissionPolicy {
	commands := approval.AllowedReviewCommands(enableStaticcheck)
	for _, command := range approval.AllowedReviewCommands(enableStaticcheck) {
		containerCommand := execution.SandboxExecCommand(RuntimeContainer, command)
		commands = append(commands,
			containerCommand,
			execution.BoundedSandboxCommand(command, outputLimit),
			execution.BoundedSandboxCommand(containerCommand, outputLimit),
		)
	}
	return approval.NewPermissionPolicy(defaultSkillCommand, commands)
}

// Agent 持有工具、策略和存储。
type Agent struct {
	// cfg 是运行配置。
	cfg Config
	// loadTool 加载 Skill。
	loadTool tool.CallableTool
	// runTool 执行 Skill 脚本。
	runTool tool.CallableTool
	// checkTool 执行 Go 检查。
	checkTool tool.CallableTool
	// exec 是底层执行器，供 workspaceexec 使用。
	exec codeexecutor.CodeExecutor
	// policy 审批工具调用。
	policy tool.PermissionPolicy
	// store 持久化审计数据。
	store storage.Store
	// artifactService 保存官方产物。
	artifactService artifact.Service
	// modelProvider 提供语义审查增量。
	modelProvider llm.Provider
	closeOnce     sync.Once
	closeErr      error
}

// New 创建基于 trpc-agent-go 的 CR Agent。
func New(cfg Config) (*Agent, error) {
	cfg = normalizeConfig(cfg)
	providerCount := 0
	if cfg.ModelProvider != nil {
		providerCount++
	}
	if cfg.ModelHTTP.Enabled {
		providerCount++
	}
	if cfg.ModelOpenAI.Enabled {
		providerCount++
	}
	if providerCount > 1 {
		return nil, errors.New("multiple model providers are configured")
	}
	if cfg.SkillsRoot == "" {
		return nil, errors.New("skills root is required")
	}
	if cfg.Runtime == RuntimeContainer && cfg.EnableStaticcheck {
		return nil, errors.New("staticcheck is unavailable in container runtime")
	}

	// 建立 Skill 仓库，供 skill_load 和 skill_run 共用。
	repo, err := skillrepo.NewFSRepository(cfg.SkillsRoot)
	if err != nil {
		return nil, fmt.Errorf("load skill repository: %w", err)
	}
	execCfg := execution.Config{
		Runtime:               cfg.Runtime,
		Timeout:               cfg.Timeout,
		ContainerRepoHostPath: cfg.ContainerRepoHostPath,
	}
	execFactory := cfg.ExecutorFactory
	if execFactory == nil {
		execFactory = execution.NewExecutor
	}
	// Container runtime setup can be expensive and dry-run never executes code,
	// so defer construction until a real execution tool call occurs.
	var exec codeexecutor.CodeExecutor
	if cfg.Runtime == RuntimeContainer {
		exec = execution.NewLazyExecutor(execCfg, execFactory)
	} else {
		exec, err = execFactory(execCfg)
		if err != nil {
			return nil, err
		}
	}

	var store storage.Store
	cleanupOnError := true
	defer func() {
		if !cleanupOnError {
			return
		}
		if store != nil {
			_ = store.Close()
		}
		_ = execution.CleanupExecutor(exec)
	}()
	if cfg.SQLitePath != "" {
		if err := ensureSQLiteParentDir(cfg.SQLitePath); err != nil {
			return nil, err
		}
		// Agent 只依赖 storage.Store 接口。
		store, err = sqlite.Open(cfg.SQLitePath)
		if err != nil {
			return nil, fmt.Errorf("open sqlite store: %w", err)
		}
	}

	// allowlist 只放行 Skill 内固定脚本。
	runTool := toolskill.NewRunTool(
		repo,
		exec,
		toolskill.WithAllowedCommands(defaultSkillCommand),
		toolskill.WithRunOutputLimits(toolskill.RunOutputLimits{
			StdoutStderrBytes:  cfg.OutputLimitBytes,
			PrimaryOutputBytes: cfg.OutputLimitBytes,
		}),
	)

	agent := &Agent{
		cfg:             cfg,
		loadTool:        toolskill.NewLoadTool(repo),
		runTool:         runTool,
		checkTool:       toolcodeexec.NewTool(exec, toolcodeexec.WithName("execute_code"), toolcodeexec.WithLanguages("bash")),
		exec:            exec,
		policy:          defaultPermissionPolicy(cfg.EnableStaticcheck, cfg.OutputLimitBytes),
		store:           store,
		artifactService: cfg.ArtifactService,
		modelProvider:   cfg.ModelProvider,
	}
	cleanupOnError = false
	return agent, nil
}

// Run 通过官方 Runner/Event 路线执行一次审查，并返回最终结果。
func (a *Agent) Run(ctx context.Context, req Request) (review.Result, error) {
	events, err := a.RunWithEvents(ctx, req)
	if err != nil {
		return review.Result{}, err
	}
	var result review.Result
	for ev := range events {
		if ev == nil {
			continue
		}
		if ev.Response != nil && ev.Response.Error != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return review.Result{}, ctxErr
			}
			return review.Result{}, errors.New(ev.Response.Error.Message)
		}
		if ev.Object == reviewEventTaskFinished {
			if structured, ok := ev.StructuredOutput.(review.Result); ok {
				result = structured
			}
		}
	}
	if result.TaskID == "" {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return review.Result{}, ctxErr
		}
		return review.Result{}, errors.New("review runner did not produce a result")
	}
	return result, nil
}

// runDirect 执行一次完整审查。官方 Runner adapter 调用该兼容执行体。
func (a *Agent) runDirect(ctx context.Context, req Request) (result review.Result, err error) {
	plan, err := normalizeExecutionPlan(req)
	if err != nil {
		return review.Result{}, err
	}
	ctx, span := telemetrytrace.Tracer.Start(ctx, "cr-agent.review")
	taskID := ""
	defer func() {
		if err != nil {
			recordReviewErrorTelemetry(span, err)
			if taskID != "" {
				a.emitReviewEvent(ctx, taskID, reviewEventTaskFailed, "review failed")
			}
		}
		span.End()
	}()

	start := time.Now()
	mode := plan.Mode
	recordReviewStartTelemetry(span, a.cfg, req, plan)

	// 统一把输入收敛成 diff。
	diff, inputRef, err := readInput(a.cfg, req)
	if err != nil {
		return review.Result{}, err
	}
	inputMeta := inputMetadataForRequest(diff, req)
	// taskID 便于报告和数据库关联。
	taskID = newTaskID(diff)
	span.SetAttributes(attribute.String("cr_agent.task_id", taskID))
	a.emitReviewEvent(ctx, taskID, reviewEventInputLoaded, requestInputKind(req))
	taskStarted := false
	defer func() {
		if err != nil && taskStarted && a.store != nil {
			cleanupCtx, cancel := a.taskCleanupContext(ctx)
			defer cancel()
			if saveErr := a.saveTaskStatus(cleanupCtx, taskID, inputRef, digestBytes(diff), req.RepoPath, mode, terminalTaskStatus(ctx, err), start, time.Now()); saveErr != nil {
				err = errors.Join(err, fmt.Errorf("save terminal task status: %w", saveErr))
			}
		}
	}()

	if a.store != nil {
		// 先记录 running，失败也可回放。
		if err := a.saveTaskStatus(ctx, taskID, inputRef, digestBytes(diff), req.RepoPath, mode, "running", start, time.Time{}); err != nil {
			return review.Result{}, err
		}
		taskStarted = true
	}

	result, decisions, runs, toolCallCount, runErr := a.runReviewChecks(ctx, taskID, plan, req.RepoPath, diff)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return review.Result{}, ctxErr
	}
	for _, run := range runs {
		a.emitReviewEvent(ctx, taskID, reviewEventSandboxRun, run.Command)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return review.Result{}, ctxErr
	}
	if runErr != nil {
		// 执行失败降级为人工复核项。
		result = resultWithRunError(result, runErr)
	}
	result = finalizeReviewResult(result, reviewResultContext{
		TaskID:        taskID,
		InputMetadata: inputMeta,
		StartedAt:     start,
		ToolCallCount: toolCallCount,
		Decisions:     decisions,
		Runs:          runs,
		Plan:          plan,
	})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return review.Result{}, ctxErr
	}
	if provider, audit := a.configuredModelProvider(plan.ModelRequested); provider != nil {
		var modelSummary llm.RunSummary
		result, modelSummary = a.runModelReview(ctx, taskID, provider, audit, result, diff, inputMeta)
		if ctxErr := ctx.Err(); ctxErr != nil {
			return review.Result{}, ctxErr
		}
		a.emitReviewEvent(ctx, taskID, reviewEventModelReview, fmt.Sprintf("calls=%d findings=%d exceptions=%d", modelSummary.CallCount, modelSummary.FindingCount, modelSummary.ExceptionCount))
		result = finalizeReviewResult(result, reviewResultContext{
			TaskID:        taskID,
			InputMetadata: inputMeta,
			StartedAt:     start,
			ToolCallCount: toolCallCount,
			Decisions:     decisions,
			Runs:          runs,
			Model:         modelSummary,
			Plan:          plan,
		})
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return review.Result{}, ctxErr
	}

	// 报告文件和 SQLite 使用同一份内容。
	reports, err := buildReportBundle(result)
	if err != nil {
		return review.Result{}, err
	}
	if err := a.writeReviewArtifacts(ctx, taskID, result, reports); err != nil {
		return review.Result{}, err
	}
	a.emitReviewEvent(ctx, taskID, reviewEventReportWritten, "review_report.json")
	if a.store != nil {
		// 完整审计数据和最终任务状态在同一事务提交。
		if err := a.persist(ctx, storage.Task{
			ID: taskID, InputType: "diff", InputRef: inputRef, InputDigest: digestBytes(diff),
			RepoPath: req.RepoPath, Status: "done", Mode: mode, CreatedAt: start,
			StartedAt: start, FinishedAt: time.Now(),
		}, result, decisions, runs, reports.JSON, reports.Markdown, reports.MarkdownZH, reports.Diagnostics); err != nil {
			return review.Result{}, err
		}
	}
	recordReviewResultTelemetry(span, result)
	a.emitReviewResultEvent(ctx, result)
	return result, nil
}

// runReviewChecks 执行规则链路并收集治理/沙箱记录。
func (a *Agent) runReviewChecks(ctx context.Context, taskID string, plan executionPlan, repoPath string, diff []byte) (review.Result, []storage.DecisionRecord, []storage.SandboxRunRecord, int, error) {
	if plan.Mode == ModeDryRun {
		// dry-run validates Skill loading but never enters a code executor or model provider.
		result, run, decision, err := a.runDryRun(ctx, taskID)
		return result, []storage.DecisionRecord{decision}, []storage.SandboxRunRecord{run}, 1, err
	}
	if a.cfg.Runtime == RuntimeE2B {
		result, run, decision := a.runUnsupportedRuntime(taskID, RuntimeE2B)
		return result, []storage.DecisionRecord{decision}, []storage.SandboxRunRecord{run}, 0, nil
	}

	toolCallCount := 2
	var result review.Result
	var runRecord storage.SandboxRunRecord
	var decision storage.DecisionRecord
	var err error
	// review mode always executes the code-review Skill first.
	result, runRecord, decision, err = a.runSkillChecks(ctx, taskID, diff)
	decisions := []storage.DecisionRecord{decision}
	runs := []storage.SandboxRunRecord{runRecord}
	if plan.SandboxRequested {
		if strings.TrimSpace(repoPath) == "" {
			skipResult, skipDecision, skipRun := sandboxUnavailableAudit(taskID)
			result.Warnings = append(result.Warnings, skipResult.Warnings...)
			decisions = append(decisions, skipDecision)
			runs = append(runs, skipRun)
		} else {
			// sandbox capability appends Go checks after the common Skill stage.
			checkDecisions, checkRuns := a.runGoSandboxChecks(ctx, taskID, repoPath)
			decisions = append(decisions, checkDecisions...)
			runs = append(runs, checkRuns...)
			toolCallCount += len(checkRuns)
		}
	}
	return result, decisions, runs, toolCallCount, err
}

func (a *Agent) runUnsupportedRuntime(taskID string, runtime string) (review.Result, storage.SandboxRunRecord, storage.DecisionRecord) {
	now := time.Now()
	reason := fmt.Sprintf("runtime %s is configured but no adapter is available yet", runtime)
	decision := storage.DecisionRecord{
		TaskID:  taskID,
		Command: defaultSkillCommand,
		Action:  "unsupported",
		Reason:  reason,
		At:      now,
	}
	run := storage.SandboxRunRecord{
		TaskID:           taskID,
		Command:          defaultSkillCommand,
		Runtime:          runtime,
		Status:           "unsupported",
		TimeoutMS:        a.cfg.Timeout.Milliseconds(),
		OutputLimitBytes: a.cfg.OutputLimitBytes,
		EnvWhitelist:     sandboxEnvWhitelist,
		Output:           reason,
		At:               now,
		FinishedAt:       now,
	}
	return review.Result{
		Warnings: []review.Finding{{
			Severity:       "low",
			Category:       "sandbox",
			Title:          "E2B runtime adapter is unsupported",
			Evidence:       reason,
			Recommendation: "Run again with container or local-fallback until the E2B adapter is implemented.",
			Confidence:     "high",
			Source:         "runtime",
			RuleID:         "e2b-runtime-unsupported",
			Status:         "needs_human_review",
		}},
	}, run, decision
}

// Close 释放 Agent 持有的存储连接。
func (a *Agent) Close() error {
	if a == nil {
		return nil
	}
	a.closeOnce.Do(func() {
		if a.store != nil {
			a.closeErr = errors.Join(a.closeErr, a.store.Close())
		}
		a.closeErr = errors.Join(a.closeErr, execution.CleanupExecutor(a.exec))
	})
	return a.closeErr
}

// saveTaskStatus 保存任务状态。
func (a *Agent) saveTaskStatus(ctx context.Context, taskID, inputRef, inputDigest, repoPath, mode, status string, startedAt, finishedAt time.Time) error {
	return a.store.SaveTask(ctx, storage.Task{
		ID:          taskID,
		InputType:   "diff",
		InputRef:    inputRef,
		InputDigest: inputDigest,
		RepoPath:    repoPath,
		Status:      status,
		Mode:        mode,
		CreatedAt:   startedAt,
		StartedAt:   startedAt,
		FinishedAt:  finishedAt,
	})
}

// saveArtifacts 使用官方 artifact service 持久化报告和诊断产物。
func (a *Agent) saveArtifacts(ctx context.Context, taskID string, result review.Result, payloads []artifactPayload) error {
	sessionInfo := artifactSessionInfo(taskID)
	payloadByName := make(map[string][]byte, len(payloads))
	for _, payload := range payloads {
		payloadByName[payload.Name] = payload.Data
	}
	for _, art := range result.Artifacts {
		mime := artifactMIMEType(art.Name)
		if mime == "" {
			continue
		}
		payload := payloadByName[art.Name]
		if _, err := a.artifactService.SaveArtifact(ctx, sessionInfo, art.Path, &artifact.Artifact{
			Data:     payload,
			MimeType: mime,
			Name:     art.Name,
		}); err != nil {
			return err
		}
	}
	return nil
}

// artifactSessionInfo 让报告产物按 task 维度归档。
func artifactSessionInfo(taskID string) artifact.SessionInfo {
	return artifact.SessionInfo{
		AppName:   "cr-agent",
		UserID:    "local",
		SessionID: taskID,
	}
}

func ensureSQLiteParentDir(path string) error {
	dir := filepath.Dir(path)
	if dir == "." || dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create sqlite directory %q: %w", dir, err)
	}
	return nil
}

func artifactMIMEType(name string) string {
	switch name {
	case "review_report.json", "review_diagnostics.json":
		return "application/json"
	case "review_report.md", "review_report.zh.md":
		return "text/markdown"
	default:
		return ""
	}
}

// normalizeConfig 填充默认配置。
func normalizeConfig(cfg Config) Config {
	if cfg.Runtime == "" {
		cfg.Runtime = RuntimeContainer
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultTimeout
	}
	if cfg.OutputLimitBytes <= 0 {
		cfg.OutputLimitBytes = defaultOutputLimitBytes
	}
	if cfg.MaxArtifactBytes <= 0 {
		cfg.MaxArtifactBytes = defaultMaxArtifactBytes
	}
	if cfg.MaxArtifactTotalBytes <= 0 {
		cfg.MaxArtifactTotalBytes = defaultMaxArtifactTotalBytes
	}
	if cfg.MaxArtifactCount <= 0 {
		cfg.MaxArtifactCount = defaultMaxArtifactCount
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "."
	}
	if cfg.ArtifactService == nil {
		cfg.ArtifactService = artifactinmemory.NewService()
	}
	return cfg
}

func (a *Agent) taskCleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := taskCleanupTimeout
	if a != nil && a.cfg.Timeout > 0 && a.cfg.Timeout < timeout {
		timeout = a.cfg.Timeout
	}
	return context.WithTimeout(context.WithoutCancel(ctx), timeout)
}

func terminalTaskStatus(ctx context.Context, err error) string {
	switch {
	case ctx != nil && errors.Is(ctx.Err(), context.Canceled):
		return "canceled"
	case ctx != nil && errors.Is(ctx.Err(), context.DeadlineExceeded):
		return "timed_out"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "timed_out"
	default:
		return "failed"
	}
}

// recordReviewStartTelemetry 记录审查入口边界。
func recordReviewStartTelemetry(span oteltrace.Span, cfg Config, req Request, plan executionPlan) {
	span.SetAttributes(
		attribute.String("cr_agent.runtime", cfg.Runtime),
		attribute.String("cr_agent.mode", plan.Mode),
		attribute.Bool("cr_agent.sandbox_requested", plan.SandboxRequested),
		attribute.Bool("cr_agent.model_requested", plan.ModelRequested),
		attribute.String("cr_agent.input_type", requestInputKind(req)),
		attribute.String("cr_agent.base_ref", req.BaseRef),
		attribute.String("cr_agent.head_ref", req.HeadRef),
		attribute.Bool("cr_agent.staticcheck_enabled", cfg.EnableStaticcheck),
	)
}

// recordReviewResultTelemetry 记录审查结果摘要。
func recordReviewResultTelemetry(span oteltrace.Span, result review.Result) {
	span.SetAttributes(
		attribute.Int("cr_agent.finding_count", len(result.Findings)),
		attribute.Int("cr_agent.warning_count", len(result.Warnings)),
		attribute.Int("cr_agent.human_review_count", len(result.HumanReviewItems)),
		attribute.Int("cr_agent.artifact_count", len(result.Artifacts)),
		attribute.Int("cr_agent.permission_block_count", result.Metrics.PermissionBlocks),
		attribute.Int("cr_agent.tool_call_count", result.Metrics.ToolCallCount),
		attribute.Int("cr_agent.model_call_count", result.Metrics.ModelCallCount),
		attribute.Int("cr_agent.model_finding_count", result.Metrics.ModelFindingCount),
		attribute.Int("cr_agent.model_exception_count", result.Metrics.ModelExceptionCount),
		attribute.Bool("cr_agent.sandbox_executed", result.Metrics.SandboxExecuted),
		attribute.Bool("cr_agent.model_executed", result.Metrics.ModelExecuted),
		attribute.String("cr_agent.model_provider", result.Metrics.ModelProvider),
		attribute.String("cr_agent.model_name", result.Metrics.ModelName),
		attribute.String("cr_agent.model_backend", result.Metrics.ModelBackend),
		attribute.Int("cr_agent.sandbox_run_count", len(result.SandboxSummary.Runs)),
		attribute.Int("cr_agent.redaction_count", result.Metrics.RedactionCount),
		attribute.Int("cr_agent.exception_count", exceptionCount(result.Metrics.ExceptionCounts)),
		attribute.Int64("cr_agent.total_duration_ms", result.Metrics.TotalDurationMS),
		attribute.Int64("cr_agent.sandbox_duration_ms", result.Metrics.SandboxDurationMS),
		attribute.Int64("cr_agent.model_duration_ms", result.Metrics.ModelDurationMS),
		attribute.String("cr_agent.severity_counts", metricDistribution(result.Metrics.SeverityCounts)),
		attribute.String("cr_agent.exception_counts", metricDistribution(result.Metrics.ExceptionCounts)),
		attribute.String("cr_agent.conclusion_status", result.Conclusion.Status),
		attribute.String("cr_agent.conclusion_reason", result.Conclusion.Reason),
	)
}

// recordReviewErrorTelemetry 记录失败状态但不写入敏感错误正文。
func recordReviewErrorTelemetry(span oteltrace.Span, err error) {
	span.SetStatus(codes.Error, "review failed")
	span.SetAttributes(attribute.String("cr_agent.error_type", fmt.Sprintf("%T", err)))
}

// requestInputKind 返回审查输入类型。
func requestInputKind(req Request) string {
	switch {
	case strings.TrimSpace(req.DiffFile) != "":
		return "diff_file"
	case strings.TrimSpace(req.FileList) != "":
		return "file_list"
	case strings.TrimSpace(req.RepoPath) != "":
		return "repo_path"
	case strings.TrimSpace(req.Fixture) != "":
		return "fixture"
	default:
		return "unknown"
	}
}

func exceptionCount(counts map[string]int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

func metricDistribution(counts map[string]int) string {
	if counts == nil {
		return "{}"
	}
	data, err := json.Marshal(counts)
	if err != nil {
		return "{}"
	}
	return string(data)
}
