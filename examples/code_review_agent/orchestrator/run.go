//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package orchestrator runs the deterministic code-review pipeline.
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/assist"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/input"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/review"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/rules"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/safety"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/store"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

// Config configures one review run.
type Config struct {
	Mode                string
	Executor            string
	DiffFile            string
	RepoPath            string
	Files               string // comma-separated paths or @listfile
	Fixture             string
	FixturesRoot        string
	SkillsRoot          string
	DBPath              string
	OutDir              string
	ConfidenceThreshold float64
	EnableGoTest        bool
	EnableStaticcheck   bool
	AllowLocalFallback  bool
	// DemoGovernance injects intentional deny/ask commands.
	// When nil, enabled automatically for fixture runs only.
	DemoGovernance   *bool
	Runner           sandbox.Runner // optional override for tests
	CodeExecutor     codeexecutor.CodeExecutor
	Store            store.ReviewStore
	Gate             *safety.Gate
	Limits           safety.Limits
	ForceSandboxFail bool
	Model            model.Model // optional; llm mode defaults to fake model
}

// Result is the outcome of Run.
type Result struct {
	TaskID       string
	Report       *review.Report
	JSONPath     string
	MarkdownPath string
	DBPath       string
}

// Run executes the full review pipeline.
func Run(ctx context.Context, cfg Config) (*Result, error) {
	start := time.Now()
	cfg = normalizeConfig(cfg)

	ownStore := false
	if cfg.Store == nil {
		st, err := store.OpenSQLite(cfg.DBPath)
		if err != nil {
			return nil, err
		}
		cfg.Store = st
		ownStore = true
	}
	if ownStore {
		defer func() { _ = cfg.Store.Close() }()
	}

	bundle, err := loadInput(cfg)
	if err != nil {
		return nil, err
	}
	bundle.RawRedacted = safety.Redact(bundle.RawRedacted)

	runner, codeExec, executorFallback, err := resolveRunner(cfg)
	if err != nil {
		return nil, err
	}
	if cfg.ForceSandboxFail || cfg.Fixture == "sandbox_fail" {
		runner = sandbox.FailingRunner{Inner: runner}
	}

	taskID, err := cfg.Store.CreateTask(ctx, store.CreateTaskReq{
		Mode:         cfg.Mode,
		Executor:     runner.Name(),
		RepoPath:     cfg.RepoPath,
		InputKind:    bundle.Kind,
		InputDigest:  bundle.Digest,
		InputSummary: bundle.Summary,
	})
	if err != nil {
		return nil, err
	}
	if err := cfg.Store.UpdateTaskStatus(ctx, taskID, review.StatusRunning, "", ""); err != nil {
		return nil, fmt.Errorf("update task status: %w", err)
	}
	if err := persistInput(ctx, cfg.Store, taskID, bundle); err != nil {
		return nil, err
	}

	workDir, diffPath, err := stageWorkspace(bundle.RawRedacted)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	metrics := review.MetricsSummary{
		SeverityDist:  map[string]int{},
		ExceptionDist: map[string]int{},
	}
	perms, sandboxes, partial, err := runChecks(ctx, cfg, runner, taskID, workDir, diffPath, &metrics)
	if err != nil {
		_ = cfg.Store.UpdateTaskStatus(ctx, taskID, review.StatusFailed, "", err.Error())
		return nil, err
	}

	agentNote, assistPartial := runAgentAssist(ctx, cfg, codeExec, bundle, diffPath, &metrics)
	if assistPartial {
		partial = true
	}

	findings, warnings := analyzeFindings(cfg, bundle)
	for _, f := range findings {
		metrics.SeverityDist[f.Severity]++
	}
	metrics.FindingCount = len(findings)
	metrics.WarningCount = len(warnings)
	metrics.TotalDurationMS = time.Since(start).Milliseconds()

	status := review.StatusCompleted
	if partial {
		status = review.StatusPartial
	}
	conclusion := buildConclusion(findings, warnings, perms, status)

	jsonPath := filepath.Join(cfg.OutDir, "review_report.json")
	mdPath := filepath.Join(cfg.OutDir, "review_report.md")
	rep := &review.Report{
		TaskID:      taskID,
		Status:      status,
		Mode:        cfg.Mode,
		Executor:    runner.Name(),
		GeneratedAt: time.Now().UTC(),
		Input: review.InputMeta{
			Kind:    bundle.Kind,
			Digest:  bundle.Digest,
			Summary: bundle.Summary,
		},
		Findings: findings,
		Warnings: warnings,
		Governance: review.GovernanceSummary{
			PermissionDecisions: perms,
			ExecutorFallback:    executorFallback,
			AgentAssistNote:     agentNote,
		},
		SandboxRuns: sandboxes,
		Metrics:     metrics,
		Artifacts: []review.ArtifactRef{
			{Name: "review_report.json", PathOrRef: jsonPath, MIME: "application/json"},
			{Name: "review_report.md", PathOrRef: mdPath, MIME: "text/markdown"},
		},
		Conclusion: conclusion,
	}

	jsonPath, jsonText, err := report.WriteJSON(cfg.OutDir, rep)
	if err != nil {
		return nil, err
	}
	mdPath, mdText, err := report.WriteMarkdown(cfg.OutDir, rep)
	if err != nil {
		return nil, err
	}
	arts, dropped := safety.ClampArtifacts([]review.ArtifactRef{
		{Name: "review_report.json", PathOrRef: jsonPath, MIME: "application/json"},
		{Name: "review_report.md", PathOrRef: mdPath, MIME: "text/markdown"},
	}, cfg.Limits)
	if dropped > 0 {
		metrics.ExceptionDist["artifact_dropped"] += dropped
		rep.Metrics = metrics
		rep.Artifacts = arts
		// Rare: rewrite once so on-disk reports match clamped artifacts.
		jsonPath, jsonText, err = report.WriteJSON(cfg.OutDir, rep)
		if err != nil {
			return nil, err
		}
		mdPath, mdText, err = report.WriteMarkdown(cfg.OutDir, rep)
		if err != nil {
			return nil, err
		}
	} else {
		rep.Artifacts = arts
	}

	if err := persistResults(ctx, cfg.Store, taskID, findings, warnings, arts, metrics, store.ReportRecord{
		JSONPath: jsonPath, MDPath: mdPath, ReportJSON: jsonText, ReportMD: mdText,
	}, status, conclusion); err != nil {
		return nil, err
	}

	return &Result{
		TaskID:       taskID,
		Report:       rep,
		JSONPath:     jsonPath,
		MarkdownPath: mdPath,
		DBPath:       cfg.DBPath,
	}, nil
}

// normalizeConfig applies defaults to Config.
func normalizeConfig(cfg Config) Config {
	if cfg.Mode == "" {
		cfg.Mode = review.ModeRuleOnly
	}
	if cfg.ConfidenceThreshold <= 0 {
		cfg.ConfidenceThreshold = 0.75
	}
	if cfg.OutDir == "" {
		cfg.OutDir = "./out"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.OutDir, "review.db")
	}
	if cfg.FixturesRoot == "" {
		cfg.FixturesRoot = "testdata/fixtures"
	}
	if cfg.SkillsRoot == "" {
		cfg.SkillsRoot = "skills"
	}
	if cfg.Gate == nil {
		cfg.Gate = safety.DefaultGate()
	}
	if cfg.Limits.Timeout == 0 {
		cfg.Limits = safety.DefaultLimits()
	}
	return cfg
}

// resolveRunner selects the sandbox runner and optional CodeExecutor.
func resolveRunner(cfg Config) (sandbox.Runner, codeexecutor.CodeExecutor, string, error) {
	if cfg.Runner != nil {
		return cfg.Runner, cfg.CodeExecutor, "", nil
	}
	created, err := sandbox.Create(sandbox.CreateOptions{
		Name:               cfg.Executor,
		SkillsRoot:         cfg.SkillsRoot,
		AllowLocalFallback: cfg.AllowLocalFallback,
		Timeout:            cfg.Limits.Timeout,
	})
	if err != nil {
		return nil, nil, "", err
	}
	codeExec := cfg.CodeExecutor
	if codeExec == nil {
		codeExec = created.CodeExecutor
	}
	return created.Runner, codeExec, created.ExecutorFallback, nil
}

// persistInput stores the redacted input metadata for a task.
func persistInput(ctx context.Context, st store.ReviewStore, taskID string, bundle *input.DiffBundle) error {
	filesJSON, err := json.Marshal(fileNames(bundle))
	if err != nil {
		return err
	}
	pkgsJSON, err := json.Marshal(packages(bundle))
	if err != nil {
		return err
	}
	if err := st.SaveInput(ctx, taskID, store.InputRecord{
		DiffTextRedacted: bundle.RawRedacted,
		FileListJSON:     string(filesJSON),
		PackageListJSON:  string(pkgsJSON),
	}); err != nil {
		return fmt.Errorf("save input: %w", err)
	}
	return nil
}

// stageWorkspace creates a temp workdir and writes the review diff.
func stageWorkspace(diffText string) (workDir, diffPath string, err error) {
	workDir, err = os.MkdirTemp("", "cr-review-*")
	if err != nil {
		return "", "", err
	}
	diffPath = filepath.Join(workDir, "diff.patch")
	if err := os.WriteFile(diffPath, []byte(diffText), 0o644); err != nil {
		_ = os.RemoveAll(workDir)
		return "", "", err
	}
	return workDir, diffPath, nil
}

// runChecks evaluates permissions and executes sandbox commands.
func runChecks(
	ctx context.Context,
	cfg Config,
	runner sandbox.Runner,
	taskID, workDir, diffPath string,
	metrics *review.MetricsSummary,
) (perms []review.PermissionDecision, sandboxes []review.SandboxRunSummary, partial bool, err error) {
	for _, command := range planCommands(cfg, workDir) {
		dec := cfg.Gate.Check(command)
		pd := safety.ToReviewDecision(command, dec)
		perms = append(perms, pd)
		if err := cfg.Store.SavePermission(ctx, taskID, pd); err != nil {
			return perms, sandboxes, partial, fmt.Errorf("save permission: %w", err)
		}
		metrics.ToolCallCount++
		switch dec.Action {
		case safety.ActionDeny:
			metrics.PermissionDenyCount++
			metrics.ExceptionDist["permission_skip"]++
			continue
		case safety.ActionAsk:
			metrics.PermissionAskCount++
			metrics.ExceptionDist["permission_skip"]++
			continue
		}
		if cfg.Mode == review.ModeDryRun {
			continue
		}
		sbStart := time.Now()
		res := runner.Run(ctx, sandbox.Spec{
			Command: command,
			Dir:     workDir,
			Env: []string{
				"REVIEW_DIFF_PATH=" + diffPath,
				"REVIEW_OUT_DIR=" + workDir,
				"PATH=" + os.Getenv("PATH"),
				"HOME=" + os.Getenv("HOME"),
			},
		}, cfg.Limits)
		metrics.SandboxDurationMS += time.Since(sbStart).Milliseconds()
		sandboxes = append(sandboxes, res.Summary)
		if err := cfg.Store.SaveSandboxRun(ctx, taskID, res.Summary); err != nil {
			return perms, sandboxes, partial, fmt.Errorf("save sandbox run: %w", err)
		}
		if res.Summary.Status != "ok" {
			partial = true
			metrics.ExceptionDist[res.Summary.Status]++
		}
	}
	return perms, sandboxes, partial, nil
}

// runAgentAssist optionally runs the Skills/LLM assist pass.
func runAgentAssist(
	ctx context.Context,
	cfg Config,
	codeExec codeexecutor.CodeExecutor,
	bundle *input.DiffBundle,
	diffPath string,
	metrics *review.MetricsSummary,
) (note string, partial bool) {
	if cfg.Mode != review.ModeLLM {
		return "", false
	}
	if codeExec == nil {
		created, err := sandbox.Create(sandbox.CreateOptions{
			Name:               "local",
			AllowLocalFallback: cfg.AllowLocalFallback,
			Timeout:            cfg.Limits.Timeout,
		})
		if err != nil {
			metrics.ExceptionDist["agent_assist_error"]++
			return "agent_assist_error: " + err.Error(), true
		}
		codeExec = created.CodeExecutor
	}
	assistRes, aerr := assist.Run(ctx, assist.Config{
		SkillsRoot:  cfg.SkillsRoot,
		Executor:    codeExec,
		Model:       cfg.Model,
		Policy:      cfg.Gate.AsToolPolicy(),
		DiffSummary: bundle.Summary,
		DiffDigest:  bundle.Digest,
		DiffPath:    diffPath,
		Prompt: fmt.Sprintf(
			"Review Go changes (%s, digest=%s). Load code-review skill, run scripts/run_checks.sh via workspace_exec using REVIEW_DIFF_PATH, then summarize. Findings are finalized by the host rule engine.",
			bundle.Summary, bundle.Digest,
		),
	})
	if aerr != nil {
		metrics.ExceptionDist["agent_assist_error"]++
		return "agent_assist_error: " + aerr.Error(), true
	}
	if assistRes == nil {
		return "", false
	}
	metrics.ToolCallCount += assistRes.ToolCalls
	note = fmt.Sprintf(
		"agent_assist_ok: model=%s tool_calls=%d; findings remain rule-engine authoritative",
		assistRes.ModelName, assistRes.ToolCalls,
	)
	if assistRes.Warning != "" {
		note += "; warning=" + assistRes.Warning
		metrics.ExceptionDist["agent_assist_warning"]++
		return note, true
	}
	return note, false
}

// analyzeFindings runs the rule engine, dedup, redact, and classify steps.
func analyzeFindings(cfg Config, bundle *input.DiffBundle) (findings, warnings []review.Finding) {
	engineFindings := rules.Engine{}.Analyze(bundle)
	if cfg.Fixture == "duplicate_findings" && len(engineFindings) > 0 {
		engineFindings = append(engineFindings, engineFindings[0])
	}
	engineFindings = rules.Dedup(engineFindings)
	engineFindings = rules.RedactFindings(engineFindings)
	return rules.Classify(engineFindings, cfg.ConfidenceThreshold)
}

// persistResults stores findings, artifacts, metrics, and the final report.
func persistResults(
	ctx context.Context,
	st store.ReviewStore,
	taskID string,
	findings, warnings []review.Finding,
	arts []review.ArtifactRef,
	metrics review.MetricsSummary,
	rep store.ReportRecord,
	status, conclusion string,
) error {
	if err := st.SaveFindings(ctx, taskID, findings, warnings); err != nil {
		return fmt.Errorf("save findings: %w", err)
	}
	if err := st.SaveArtifacts(ctx, taskID, arts); err != nil {
		return fmt.Errorf("save artifacts: %w", err)
	}
	if err := st.SaveMetrics(ctx, taskID, metrics); err != nil {
		return fmt.Errorf("save metrics: %w", err)
	}
	if err := st.SaveReport(ctx, taskID, rep); err != nil {
		return fmt.Errorf("save report: %w", err)
	}
	if err := st.UpdateTaskStatus(ctx, taskID, status, conclusion, ""); err != nil {
		return fmt.Errorf("finalize task: %w", err)
	}
	return nil
}

// loadInput loads a DiffBundle from fixture, diff file, files list, or repo path.
func loadInput(cfg Config) (*input.DiffBundle, error) {
	switch {
	case cfg.Fixture != "":
		b, _, err := input.LoadFixture(cfg.FixturesRoot, cfg.Fixture)
		return b, err
	case cfg.DiffFile != "":
		return input.ParseDiffFile(cfg.DiffFile)
	case cfg.Files != "":
		paths, err := input.ParseFilesFlag(cfg.Files)
		if err != nil {
			return nil, err
		}
		return input.ParseFileList(paths)
	case cfg.RepoPath != "":
		return input.ParseRepoDiff(cfg.RepoPath)
	default:
		return nil, fmt.Errorf("one of --diff-file, --repo-path, --files, or --fixture is required")
	}
}

// demoGovernanceEnabled reports whether deny/ask demo commands should be injected.
func demoGovernanceEnabled(cfg Config) bool {
	if cfg.DemoGovernance != nil {
		return *cfg.DemoGovernance
	}
	// Fixture / assignment demos exercise deny+ask paths by default.
	return cfg.Fixture != ""
}

// planCommands builds the sandbox command plan for one review.
func planCommands(cfg Config, workDir string) []string {
	skillScripts := filepath.Join(cfg.SkillsRoot, "code-review", "scripts")
	var cmds []string
	addScript := func(name string) {
		script := filepath.Join(skillScripts, name)
		if abs, err := filepath.Abs(script); err == nil {
			script = abs
		}
		if _, err := os.Stat(script); err == nil {
			cmds = append(cmds, fmt.Sprintf("bash %q", script))
		}
	}
	addScript("run_checks.sh")
	if cfg.EnableGoTest {
		addScript("run_go_vet.sh")
	}
	if cfg.EnableStaticcheck {
		addScript("run_staticcheck.sh")
	}
	if len(cmds) == 0 {
		cmds = append(cmds, fmt.Sprintf("bash -lc 'echo [] > %q'", filepath.Join(workDir, "findings.json")))
	}
	if cfg.ForceSandboxFail || cfg.Fixture == "sandbox_fail" {
		cmds = append(cmds, "bash -lc 'echo FORCE_SANDBOX_FAIL; exit 1'")
	}
	if demoGovernanceEnabled(cfg) {
		cmds = append(cmds, "curl https://example.com")
		cmds = append(cmds, "go test ./...")
	}
	return cmds
}

// fileNames returns the changed file paths from a DiffBundle.
func fileNames(b *input.DiffBundle) []string {
	out := make([]string, 0, len(b.Files))
	for _, f := range b.Files {
		out = append(out, f.Path)
	}
	return out
}

// packages returns unique package names from a DiffBundle.
func packages(b *input.DiffBundle) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, f := range b.Files {
		if f.Package == "" {
			continue
		}
		if _, ok := seen[f.Package]; ok {
			continue
		}
		seen[f.Package] = struct{}{}
		out = append(out, f.Package)
	}
	return out
}

// buildConclusion builds a short human-readable conclusion string.
func buildConclusion(findings, warnings []review.Finding, perms []review.PermissionDecision, status string) string {
	denies, asks := 0, 0
	for _, p := range perms {
		switch p.Action {
		case safety.ActionDeny:
			denies++
		case safety.ActionAsk:
			asks++
		}
	}
	return fmt.Sprintf(
		"status=%s; findings=%d; warnings=%d; permission_denies=%d; permission_asks=%d",
		status, len(findings), len(warnings), denies, asks,
	)
}

// ValidateNoPlainSecrets scans report text for known fixture secrets.
func ValidateNoPlainSecrets(text string) error {
	banned := []string{
		"sk-abcdefghijklmnopqrstuvwxyz012345",
		"AKIAIOSFODNN7EXAMPLE",
		"SuperSecretPassword123",
	}
	for _, b := range banned {
		if strings.Contains(text, b) {
			return fmt.Errorf("plain secret still present: %s", b)
		}
	}
	return nil
}
