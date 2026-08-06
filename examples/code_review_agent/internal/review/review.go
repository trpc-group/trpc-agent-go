//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/skill"
)

// Run executes one governed review pipeline and persists its report.
func Run(ctx context.Context, cfg Config) (Report, ReportPaths, error) {
	started := time.Now()
	if err := normalizeConfig(&cfg); err != nil {
		return Report{}, ReportPaths{}, err
	}
	baseDir, err := exampleDir()
	if err != nil {
		return Report{}, ReportPaths{}, err
	}
	reviewSkill, err := loadReviewSkill(baseDir)
	if err != nil {
		return Report{}, ReportPaths{}, err
	}
	taskID := cfg.TaskID
	if taskID == "" {
		taskID = newTaskID()
	}
	store, err := cfg.StoreFactory(ctx, cfg)
	if err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("open review store: %w", err)
	}
	defer store.Close()
	if recovered, paths, ok, err := recoverPublishedReview(ctx, store, cfg, taskID); err != nil {
		return Report{}, ReportPaths{}, err
	} else if ok {
		return recovered, paths, nil
	}
	input, mode, inputDecisions, err := loadInput(ctx, cfg, baseDir)
	if err != nil {
		return persistFailure(ctx, store, cfg, Task{ID: taskID, Status: TaskRunning, InputMode: firstNonEmpty(mode, "input_error"), StartedAt: started}, DiffSummary{}, inputDecisions, "load_input", err)
	}
	task := Task{ID: taskID, Status: TaskRunning, InputMode: mode, StartedAt: started}
	findings, warnings, needsHuman, filterAudit := analyzeWithRuleIDs(input, reviewSkill.RuleIDs)
	sandboxRunner, err := newSandbox(ctx, cfg, baseDir)
	if err != nil {
		return persistFailure(ctx, store, cfg, task, input.Summary, inputDecisions, "initialize_sandbox", err)
	}
	defer sandboxRunner.Close()
	runs, sandboxDecisions, sandboxArtifacts, err := sandboxRunner.run(ctx, task.ID, cfg.RepoPath, input)
	if err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("run sandbox checks: %w", err)
	}
	sandboxItems := sandboxReviewItems(runs)
	needsHuman = append(needsHuman, sandboxItems...)
	retainedAudit := filterAudit[:0]
	for _, decision := range filterAudit {
		if decision.TargetBucket != "needs_human_review" {
			retainedAudit = append(retainedAudit, decision)
		}
	}
	filterAudit = append(retainedAudit, filterDecisions(needsHuman, "needs_human_review", FilterRouteHuman)...)
	needsHuman = dedupe(needsHuman)
	report := Report{
		Task: task, Input: input.Summary, Findings: findings, Warnings: warnings,
		NeedsHumanReview: needsHuman, SandboxRuns: runs, PermissionDecisions: append(inputDecisions, sandboxDecisions...),
		Artifacts: sandboxArtifacts, Mode: executionMode(cfg), FilterDecisions: filterAudit,
	}
	report.Conclusion = conclusion(report)
	report = redactReport(report)
	if err := store.Save(ctx, report); err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("store review: %w", err)
	}
	failStoredReview := func(operation string, cause error) (Report, ReportPaths, error) {
		if failureErr := markStoredFailure(ctx, store, cfg, report, operation, cause); failureErr != nil {
			return Report{}, ReportPaths{}, fmt.Errorf("%s: %w; persist failure record: %v", operation, cause, failureErr)
		}
		return Report{}, ReportPaths{}, fmt.Errorf("%s for task %s: %w", operation, report.Task.ID, cause)
	}
	stored, err := store.Load(ctx, report.Task.ID)
	if err != nil {
		return failStoredReview("verify_initial_store", err)
	}
	if stored.Task.ID != report.Task.ID {
		return failStoredReview("verify_initial_store", errors.New("stored review task ID mismatch"))
	}
	// The immutable report files contain a completed pre-publication snapshot so
	// they never present a running task after a successful directory rename. The
	// canonical task timestamp is refreshed at the terminal Store finalization
	// boundary below.
	report.Task.Status, report.Task.EndedAt = TaskCompleted, time.Now()
	report.Metrics = collectMetrics(started, report)
	// Report files are immutable inputs to the atomic directory rename. Record a
	// useful pre-publication snapshot in those files; the canonical final value
	// is measured immediately after the rename and persisted below.
	report.Metrics.TotalDurationMS = time.Since(started).Milliseconds()
	report.Metrics.TotalDurationScope = "pre_publication_snapshot"
	stagedReport, paths, staged, err := stageReport(report, cfg.OutputDir)
	if err != nil {
		return failStoredReview("stage_report", err)
	}
	report = stagedReport
	defer staged.cleanup()
	if err := staged.commit(); err != nil {
		return failStoredReview("publish_report", err)
	}
	failPublishedReview := func(operation string, cause error) (Report, ReportPaths, error) {
		if rollbackErr := staged.rollback(); rollbackErr != nil {
			cause = fmt.Errorf("%w; roll back published report: %v", cause, rollbackErr)
		}
		return failStoredReview(operation, cause)
	}
	// Keep the stored task running until the report directory is durable. The
	// single terminal transaction then records a mutually consistent completed
	// task timestamp and duration, so a process interruption cannot leave a
	// partially finalized completed record behind.
	report.Task.EndedAt = time.Now()
	finalizationStarted := time.Now()
	if err := store.Finalize(ctx, report); err != nil {
		return failPublishedReview("finalize_report", err)
	}
	report.Metrics.FinalizationDurationMS = time.Since(finalizationStarted).Milliseconds()
	verificationStarted := time.Now()
	stored, err = store.Load(ctx, report.Task.ID)
	if err != nil {
		return failPublishedReview("verify_final_store", err)
	}
	if stored.Task.Status != TaskCompleted || stored.Metrics.PreparationDurationMS != report.Metrics.PreparationDurationMS || stored.Metrics.TotalDurationMS != report.Metrics.TotalDurationMS {
		return failPublishedReview("verify_final_store", errors.New("finalized review verification mismatch"))
	}
	// The first terminal transaction above is independently verified before we
	// persist the composite end-to-end timing fields. This keeps the canonical
	// metric inclusive of report publication, terminal durability, and the
	// read-back consistency check rather than labelling a pre-verification
	// timestamp as the total review duration. Persisting the derived metric is
	// necessarily a subsequent operation, so the scope names that boundary
	// instead of implying self-inclusive timing.
	report.Metrics.VerificationDurationMS = time.Since(verificationStarted).Milliseconds()
	report.Task.EndedAt = time.Now()
	report.Metrics.TotalDurationMS = time.Since(started).Milliseconds()
	report.Metrics.TotalDurationScope = "verified_before_metric_persistence"
	if err := store.Finalize(ctx, report); err != nil {
		return failPublishedReview("persist_end_to_end_metrics", err)
	}
	return report, paths, nil
}

// recoverPublishedReview closes the interruption window between publishing the
// immutable report directory and finalizing its Store record. A matching
// running record is finalized from the already-published JSON. Incomplete or
// malformed publication is rolled back with its running record so the task ID
// can be retried without overwriting a completed audit.
func recoverPublishedReview(ctx context.Context, store Store, cfg Config, taskID string) (Report, ReportPaths, bool, error) {
	stored, err := store.Load(ctx, taskID)
	if errors.Is(err, sql.ErrNoRows) {
		return Report{}, ReportPaths{}, false, nil
	}
	if err != nil {
		return Report{}, ReportPaths{}, true, fmt.Errorf("load existing task %s: %w", taskID, err)
	}
	if stored.Task.Status != TaskRunning {
		return Report{}, ReportPaths{}, false, nil
	}
	paths := reportPaths(cfg.OutputDir, taskID)
	data, readErr := os.ReadFile(paths.JSON)
	if readErr == nil {
		if _, err := os.Stat(paths.Markdown); err != nil {
			readErr = err
		}
	}
	if readErr != nil {
		if !os.IsNotExist(readErr) {
			return Report{}, ReportPaths{}, true, fmt.Errorf("read interrupted report %s: %w", taskID, readErr)
		}
		return rollbackInterruptedReview(ctx, store, cfg.OutputDir, taskID)
	}
	var published Report
	if err := json.Unmarshal(data, &published); err != nil || !matchesRunningReport(stored, published) {
		return rollbackInterruptedReview(ctx, store, cfg.OutputDir, taskID)
	}
	published = redactReport(published)
	published.Task.Status = TaskCompleted
	published.Task.EndedAt = time.Now()
	// The durable report was already published before interruption. Its
	// pre-publication duration is the only trustworthy elapsed-time value, so
	// preserve it and mark the Store record as a recovered terminal audit.
	published.Metrics.TotalDurationScope = "recovered_publication"
	if err := store.Finalize(ctx, published); err != nil {
		return Report{}, ReportPaths{}, true, fmt.Errorf("recover published task %s: %w", taskID, err)
	}
	return published, paths, true, nil
}

func rollbackInterruptedReview(ctx context.Context, store Store, outputDir, taskID string) (Report, ReportPaths, bool, error) {
	if err := os.RemoveAll(filepath.Join(outputDir, taskID)); err != nil {
		return Report{}, ReportPaths{}, true, fmt.Errorf("roll back interrupted report %s: %w", taskID, err)
	}
	if err := store.Delete(ctx, taskID); err != nil {
		return Report{}, ReportPaths{}, true, fmt.Errorf("remove interrupted task %s: %w", taskID, err)
	}
	return Report{}, ReportPaths{}, false, nil
}

func matchesRunningReport(stored, published Report) bool {
	return published.Task.ID == stored.Task.ID &&
		published.Task.Status == TaskCompleted &&
		published.Input.Digest != "" &&
		published.Input.Digest == stored.Input.Digest &&
		published.Task.StartedAt.Equal(stored.Task.StartedAt)
}

func reportPaths(outputDir, taskID string) ReportPaths {
	taskDir := filepath.Join(outputDir, taskID, "report")
	return ReportPaths{JSON: filepath.Join(taskDir, "review_report.json"), Markdown: filepath.Join(taskDir, "review_report.md")}
}

func markStoredFailure(ctx context.Context, store Store, cfg Config, report Report, operation string, cause error) error {
	report.Task.Status, report.Task.EndedAt = TaskFailed, time.Now()
	report.Artifacts = nil
	report.SandboxRuns = append(report.SandboxRuns, setupFailure(cfg.Executor, operation, cause, cfg.OutputLimit))
	report.Conclusion = "Review failed after persistence; inspect the audit record before retrying."
	report.Metrics = collectMetrics(report.Task.StartedAt, report)
	report.Metrics.TotalDurationScope = "failure_audit"
	return store.Finalize(ctx, redactReport(report))
}

func persistFailure(ctx context.Context, store Store, cfg Config, task Task, input DiffSummary, decisions []PermissionDecision, operation string, cause error) (Report, ReportPaths, error) {
	task.Status, task.EndedAt = TaskFailed, time.Now()
	report := Report{
		Task: task, Input: input, PermissionDecisions: decisions, Mode: executionMode(cfg),
		SandboxRuns: []SandboxRun{setupFailure(cfg.Executor, operation, cause, cfg.OutputLimit)},
		Conclusion:  "Review setup failed; inspect the persisted failure record before retrying.",
	}
	report.Metrics = collectMetrics(task.StartedAt, report)
	report.Metrics.TotalDurationScope = "failure_audit"
	report = redactReport(report)
	if err := store.Save(ctx, report); err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("persist %s failure: %w", operation, err)
	}
	if err := store.Finalize(ctx, report); err != nil {
		return Report{}, ReportPaths{}, fmt.Errorf("finalize %s failure: %w", operation, err)
	}
	return Report{}, ReportPaths{}, fmt.Errorf("%s for task %s: %w", operation, task.ID, cause)
}

func normalizeConfig(cfg *Config) error {
	if cfg.TaskID != "" {
		for _, r := range cfg.TaskID {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
				return errors.New("task id may contain only letters, digits, hyphen, and underscore")
			}
		}
		if len(cfg.TaskID) > 80 {
			return errors.New("task id must not exceed 80 characters")
		}
		if looksSecret(cfg.TaskID) {
			return errors.New("task id must not contain credential-like material")
		}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 45 * time.Second
	}
	if cfg.OutputLimit <= 0 {
		cfg.OutputLimit = 64 << 10
	}
	if cfg.OutputLimit > 1<<20 {
		return errors.New("output limit must not exceed 1 MiB")
	}
	if cfg.OutputDir == "" {
		cfg.OutputDir = "output"
	}
	if cfg.DatabasePath == "" {
		cfg.DatabasePath = filepath.Join(cfg.OutputDir, "reviews.sqlite")
	}
	if cfg.Executor == "" {
		cfg.Executor = ExecutorContainer
	}
	if cfg.DryRun {
		cfg.Executor = ExecutorFake
	}
	if cfg.StoreFactory == nil {
		cfg.StoreFactory = defaultStoreFactory
	}
	return nil
}

func exampleDir() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("cannot locate example directory")
	}
	dir := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	if _, err := os.Stat(filepath.Join(dir, "skills", "code-review", "SKILL.md")); err != nil {
		return "", err
	}
	return dir, nil
}

type loadedReviewSkill struct {
	RuleIDs map[string]bool
}

func loadReviewSkill(baseDir string) (loadedReviewSkill, error) {
	repository, err := skill.NewFSRepository(filepath.Join(baseDir, "skills"))
	if err != nil {
		return loadedReviewSkill{}, err
	}
	loaded, err := repository.Get("code-review")
	if err != nil {
		return loadedReviewSkill{}, err
	}
	if strings.TrimSpace(loaded.Body) == "" || len(loaded.Docs) == 0 {
		return loadedReviewSkill{}, errors.New("code-review skill must include a body and rule documentation")
	}
	path, err := repository.Path("code-review")
	if err != nil {
		return loadedReviewSkill{}, err
	}
	if _, err := os.Stat(filepath.Join(path, "scripts", "diff_stats.sh")); err != nil {
		return loadedReviewSkill{}, err
	}
	doc, err := os.ReadFile(filepath.Join(path, "docs", "go-review-rules.md"))
	if err != nil {
		return loadedReviewSkill{}, err
	}
	ruleIDs := map[string]bool{}
	for _, match := range regexpRuleID.FindAllString(string(doc), -1) {
		ruleIDs[strings.Trim(match, "`")] = true
	}
	if len(ruleIDs) == 0 {
		return loadedReviewSkill{}, errors.New("code-review skill declares no rule IDs")
	}
	return loadedReviewSkill{RuleIDs: ruleIDs}, nil
}

var regexpRuleID = regexp.MustCompile("`go/[a-z0-9_/-]+`")

func sandboxReviewItems(runs []SandboxRun) []Finding {
	var result []Finding
	for _, run := range runs {
		if run.Status == RunSuccess || run.Status == RunSkipped && run.ErrorType == ErrorToolUnavailable || run.ErrorType == ErrorDryRun {
			continue
		}
		result = append(result, fingerprint(Finding{
			Severity: SeverityMedium, Category: "sandbox", Title: "Sandbox check requires human review",
			Evidence:       redact(strings.TrimSpace(run.Command + " " + stringsJoin(run.Args) + ": " + string(run.ErrorType) + " " + run.Stderr)),
			Recommendation: "Inspect the recorded failure or governance decision and rerun the check in a healthy sandbox.",
			Confidence:     .99, Source: "sandbox", RuleID: "sandbox/" + firstNonEmpty(string(run.ErrorType), string(run.Status)),
		}))
	}
	return result
}

func collectMetrics(started time.Time, report Report) Metrics {
	durationMS := time.Since(started).Milliseconds()
	metrics := Metrics{PreparationDurationMS: durationMS, TotalDurationMS: durationMS, TotalDurationScope: "preparation", ToolCallCount: len(report.SandboxRuns), FindingCount: len(report.Findings), WarningCount: len(report.Warnings), NeedsHumanCount: len(report.NeedsHumanReview), SeverityDistribution: map[string]int{}, ErrorDistribution: map[string]int{}}
	for _, run := range report.SandboxRuns {
		metrics.SandboxDurationMS += run.DurationMS
		if run.ErrorType != "" && run.ErrorType != ErrorDryRun {
			metrics.ErrorDistribution[string(run.ErrorType)]++
		}
	}
	for _, decision := range report.PermissionDecisions {
		if decision.Action == PermissionDeny {
			metrics.PermissionDenyCount++
		}
		if decision.Action == PermissionAsk {
			metrics.PermissionAskCount++
		}
	}
	for _, bucket := range [][]Finding{report.Findings, report.Warnings, report.NeedsHumanReview} {
		for _, finding := range bucket {
			metrics.SeverityDistribution[string(finding.Severity)]++
		}
	}
	return metrics
}

func conclusion(report Report) string {
	for _, finding := range report.Findings {
		if finding.Severity == SeverityCritical && finding.RuleID == "go/security/hardcoded-secret" {
			return "Critical findings block merge until remediation and credential rotation are complete."
		}
	}
	for _, finding := range report.Findings {
		if finding.Severity == SeverityCritical {
			return "Critical findings block merge until the reported issues are remediated."
		}
	}
	if len(report.Findings) > 0 {
		return "Review found actionable issues that should be fixed before merge."
	}
	if len(report.NeedsHumanReview) > 0 {
		return "No high-confidence issue was confirmed, but listed items require human review."
	}
	return "No actionable issue was detected by the configured deterministic checks."
}

func executionMode(cfg Config) ExecutionMode {
	if cfg.FakeModel {
		return "deterministic-rule-only+fake-model"
	}
	return "deterministic-rule-only"
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "unknown"
}
func newTaskID() string {
	raw := make([]byte, 12)
	if _, err := rand.Read(raw); err != nil {
		return fmt.Sprintf("review-%d", time.Now().UnixNano())
	}
	return "review-" + hex.EncodeToString(raw)
}
