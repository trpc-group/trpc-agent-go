package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type Reviewer struct {
	cfg      Config
	store    Store
	redactor Redactor
}

func NewReviewer(cfg Config) *Reviewer {
	redactor := NewRedactor()
	if cfg.Runtime == "" {
		cfg.Runtime = "fake"
	}
	if cfg.OutDir == "" {
		cfg.OutDir = "out"
	}
	if cfg.StorePath == "" {
		cfg.StorePath = filepath.Join(cfg.OutDir, "review_store.json")
	}
	if cfg.SkillPath == "" {
		cfg.SkillPath = filepath.Join("skills", "code-review")
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.MaxOutputBytes <= 0 {
		cfg.MaxOutputBytes = 64 * 1024
	}
	return &Reviewer{cfg: cfg, store: &JSONStore{Path: cfg.StorePath}, redactor: redactor}
}

func NewReviewerWithStore(cfg Config, store Store) *Reviewer {
	r := NewReviewer(cfg)
	r.store = store
	return r
}

func (r *Reviewer) Run(ctx context.Context) (ReviewReport, error) {
	started := time.Now().UTC()
	if err := r.store.Init(); err != nil {
		return ReviewReport{}, err
	}
	if _, err := LoadSkill(r.cfg.SkillPath); err != nil {
		return ReviewReport{}, err
	}

	input, inputKind, err := r.loadInput(ctx)
	if err != nil {
		return ReviewReport{}, err
	}
	input.RawDiff = r.redactor.Redact(input.RawDiff)
	task := ReviewTask{
		ID:           NewID("task"),
		Status:       TaskStatusRunning,
		InputKind:    inputKind,
		InputSummary: fmt.Sprintf("files=%d go_files=%d added=%d deleted=%d sha256=%s", input.Summary.FileCount, input.Summary.GoFileCount, input.Summary.AddedLineCount, input.Summary.DeletedLineCount, input.Summary.DiffSHA256),
		RepoPath:     r.cfg.RepoPath,
		SkillPath:    r.cfg.SkillPath,
		Runtime:      r.cfg.Runtime,
		DryRun:       r.cfg.DryRun,
		RuleOnly:     r.cfg.RuleOnly,
		StartedAt:    started,
	}
	if err := r.store.SaveTask(task); err != nil {
		return ReviewReport{}, err
	}

	policy := PermissionPolicy{AllowStaticcheck: r.cfg.EnableStaticcheck}
	runner := SandboxRunner{Runtime: r.cfg.Runtime, RepoPath: r.cfg.RepoPath, Timeout: r.cfg.Timeout, MaxOutputBytes: r.cfg.MaxOutputBytes, ForceSandboxFailure: r.cfg.ForceSandboxFailure, redactor: r.redactor}
	var decisions []PermissionDecision
	var runs []SandboxRun
	var ruleFindings []Finding

	if r.cfg.RepoPath != "" || r.cfg.Runtime == "fake" || r.cfg.ForceSandboxFailure {
		for _, spec := range SandboxCommands(r.cfg) {
			decision := policy.Decide(task.ID, spec.Tool, spec.Command)
			decisions = append(decisions, decision)
			_ = r.store.SavePermissionDecision(decision)
			if decision.Decision != DecisionAllow {
				continue
			}
			run := runner.Run(ctx, task.ID, spec)
			runs = append(runs, run)
			_ = r.store.SaveSandboxRun(run)
			if warn := SandboxWarningFromRun(run); warn != nil {
				ruleFindings = append(ruleFindings, *warn)
			}
		}
	}

	ruleFindings = append(ruleFindings, NewRuleEngine(r.redactor).Analyze(input)...)
	for i := range ruleFindings {
		ruleFindings[i].Evidence = r.redactor.Redact(ruleFindings[i].Evidence)
		ruleFindings[i].Recommendation = r.redactor.Redact(ruleFindings[i].Recommendation)
	}
	findings, warnings, needsHuman := DeduplicateAndTriage(ruleFindings)
	for _, f := range findings {
		_ = r.store.SaveFinding(task.ID, f, "finding")
	}
	for _, f := range warnings {
		_ = r.store.SaveFinding(task.ID, f, "warning")
	}
	for _, f := range needsHuman {
		_ = r.store.SaveFinding(task.ID, f, "needs_human_review")
	}

	monitoring := BuildMonitoring(task.ID, started, findings, warnings, needsHuman, decisions, runs)
	task = finalTask(task, TaskStatusComplete, "")
	report := ReviewReport{
		Task:                task,
		Input:               input.Summary,
		Findings:            findings,
		Warnings:            warnings,
		NeedsHumanReview:    needsHuman,
		SandboxRuns:         runs,
		PermissionDecisions: decisions,
		Monitoring:          monitoring,
		FinalConclusion:     FinalConclusion(findings, warnings, needsHuman, runs, decisions),
	}

	report, artifacts, err := WriteReports(r.cfg.OutDir, report, r.redactor)
	if err != nil {
		task = finalTask(task, TaskStatusFailed, err.Error())
		_ = r.store.UpdateTask(task)
		return report, err
	}
	report.Artifacts = artifacts
	for _, artifact := range artifacts {
		_ = r.store.SaveArtifact(artifact)
	}
	_ = r.store.SaveMonitoringSummary(monitoring)
	_ = r.store.UpdateTask(task)
	_ = r.store.SaveReport(task.ID, report)
	return report, nil
}

func (r *Reviewer) loadInput(ctx context.Context) (ReviewInput, string, error) {
	if r.cfg.DiffFile != "" {
		body, err := os.ReadFile(r.cfg.DiffFile)
		if err != nil {
			return ReviewInput{}, "diff_file", err
		}
		input, err := ParseUnifiedDiff(string(body))
		return input, "diff_file", err
	}
	if r.cfg.RepoPath != "" {
		raw, err := gitDiff(ctx, r.cfg.RepoPath)
		if err != nil {
			return ReviewInput{}, "repo_path", err
		}
		input, err := ParseUnifiedDiff(raw)
		return input, "repo_path", err
	}
	return ReviewInputFromFiles(r.cfg.Files), "files", nil
}

func gitDiff(ctx context.Context, repoPath string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cmdCtx, "git", "diff", "--no-ext-diff", "--", ".")
	cmd.Dir = repoPath
	out, err := cmd.CombinedOutput()
	if cmdCtx.Err() != nil {
		return "", cmdCtx.Err()
	}
	if err != nil {
		return "", fmt.Errorf("git diff: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}
