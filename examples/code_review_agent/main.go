//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/output"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/parser"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/policy"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/redaction"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/storage"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/telemetry"
	"trpc.group/trpc-go/trpc-agent-go/skill"
)

var (
	diffFile     = flag.String("diff-file", "", "Path to diff file")
	repoPath     = flag.String("repo-path", "", "Path to repository")
	outputDir    = flag.String("output-dir", "output", "Output directory")
	dbPath       = flag.String("db-path", "code_review.db", "SQLite database path")
	dryRun       = flag.Bool("dry-run", false, "Run without LLM, but still run static analysis and sandbox checks")
	runFixture   = flag.String("fixture", "", "Run specific fixture")
	listFixtures = flag.Bool("list-fixtures", false, "List available fixtures")
	unsafeLocal  = flag.Bool("unsafe-local", false, "Enable unsafe local sandbox mode for testing (runs untrusted code on host)")
)

func main() {
	flag.Parse()

	if *listFixtures {
		listAvailableFixtures()
		return
	}

	if *runFixture != "" {
		err := runFixtureReview(*runFixture)
		if err != nil {
			log.Fatalf("Failed to run fixture: %v", err)
		}
		return
	}

	if *diffFile == "" && *repoPath == "" {
		log.Fatal("Please provide a diff file with --diff-file or a repository path with --repo-path")
	}

	if *repoPath == "" {
		*repoPath = "."
	}

	if *diffFile == "" {
		log.Printf("Generating diff from repository: %s", *repoPath)
		diffData, err := generateDiffFromRepo(*repoPath)
		if err != nil {
			log.Fatalf("Failed to generate diff from repository: %v", err)
		}
		tempFile, err := os.CreateTemp("", "review_diff_*.diff")
		if err != nil {
			log.Fatalf("Failed to create temp file: %v", err)
		}
		defer os.Remove(tempFile.Name())
		tempFile.WriteString(diffData)
		tempFile.Close()
		*diffFile = tempFile.Name()
	}

	err := runCodeReview(*diffFile, *repoPath, *outputDir, *dbPath, *dryRun)
	if err != nil {
		log.Fatalf("Code review failed: %v", err)
	}
}

func generateDiffFromRepo(repoPath string) (string, error) {
	baseRef := os.Getenv("BASE_REF")
	if baseRef == "" {
		baseRef = "origin/main"
	}

	var output []byte
	var err error

	cmd := exec.Command("git", "-C", repoPath, "merge-base", baseRef, "HEAD")
	mergeBase, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("Warning: Failed to find merge base with %s, falling back to HEAD~1", baseRef)
		cmd := exec.Command("git", "-C", repoPath, "diff", "HEAD~1", "HEAD")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git diff failed: %w", err)
		}
	} else {
		mergeBaseHash := strings.TrimSpace(string(mergeBase))
		cmd = exec.Command("git", "-C", repoPath, "diff", mergeBaseHash, "HEAD")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git diff failed: %w", err)
		}
	}

	stagedOutput, err := exec.Command("git", "-C", repoPath, "diff", "--cached").CombinedOutput()
	if err != nil {
		log.Printf("Warning: Failed to get staged diff: %v", err)
	} else if len(stagedOutput) > 0 {
		output = append(output, stagedOutput...)
	}

	unstagedOutput, err := exec.Command("git", "-C", repoPath, "diff").CombinedOutput()
	if err != nil {
		log.Printf("Warning: Failed to get unstaged diff: %v", err)
	} else if len(unstagedOutput) > 0 {
		output = append(output, unstagedOutput...)
	}

	return string(output), nil
}

func listAvailableFixtures() {
	fixturesDir := filepath.Join(".", "fixtures")
	files, err := os.ReadDir(fixturesDir)
	if err != nil {
		log.Printf("No fixtures found: %v", err)
		return
	}

	fmt.Println("Available fixtures:")
	for _, file := range files {
		if !file.IsDir() && filepath.Ext(file.Name()) == ".diff" {
			name := file.Name()[:len(file.Name())-5]
			fmt.Printf("  - %s\n", name)
		}
	}
}

func runFixtureReview(fixtureName string) error {
	var diffPath string
	if filepath.Dir(fixtureName) == "." {
		diffPath = filepath.Join(".", "fixtures", fixtureName+".diff")
	} else {
		diffPath = fixtureName
		if !strings.HasSuffix(diffPath, ".diff") {
			diffPath += ".diff"
		}
	}
	if _, err := os.Stat(diffPath); os.IsNotExist(err) {
		return fmt.Errorf("fixture not found: %s", diffPath)
	}

	outputDir := filepath.Join(".", "output", fixtureName)
	dbPath := filepath.Join(".", "output", fixtureName+".db")

	log.Printf("Running fixture: %s", fixtureName)
	log.Printf("Diff file: %s", diffPath)
	log.Printf("Output dir: %s", outputDir)

	return runCodeReview(diffPath, ".", outputDir, dbPath, true)
}

func runCodeReview(diffPath, repoPath, outputDir, dbPath string, dryRun bool) error {
	start := time.Now()
	log.Printf("Starting code review...")

	ctx := context.Background()

	taskID := uuid.New().String()
	log.Printf("Task ID: %s", taskID)

	if err := validatePath(diffPath); err != nil {
		return fmt.Errorf("invalid diff path: %w", err)
	}
	if err := validatePath(repoPath); err != nil {
		return fmt.Errorf("invalid repo path: %w", err)
	}
	if err := validatePath(outputDir); err != nil {
		return fmt.Errorf("invalid output dir: %w", err)
	}
	if err := validatePath(dbPath); err != nil {
		return fmt.Errorf("invalid db path: %w", err)
	}

	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	db, err := storage.NewSQLiteStorage(dbPath)
	if err != nil {
		return fmt.Errorf("create storage: %w", err)
	}
	defer db.Close()

	if err := db.Init(ctx); err != nil {
		return fmt.Errorf("init storage: %w", err)
	}

	task := storage.ReviewTask{
		ID:        taskID,
		DiffPath:  diffPath,
		RepoPath:  repoPath,
		Status:    "running",
		StartedAt: time.Now(),
	}
	if err := db.CreateReviewTask(ctx, task); err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	defer func() {
		if task.Status == "running" {
			task.Status = "failed"
			now := time.Now()
			task.CompletedAt = &now
			_ = db.UpdateReviewTask(ctx, task)
		}
	}()

	metrics := telemetry.NewMetrics()
	permissionPolicy := policy.NewPermissionPolicy()
	ruleDetector := policy.NewRuleDetector()

	log.Printf("Loading skills...")
	skillRepo, err := skill.NewFSRepository("./skills")
	if err != nil {
		log.Printf("Warning: Failed to load skills: %v", err)
	} else {
		codeReviewSkill, err := skillRepo.Get("code-review")
		if err != nil {
			log.Printf("Warning: Failed to load code-review skill: %v", err)
		} else {
			log.Printf("Loaded skill: %s (description: %s)", codeReviewSkill.Summary.Name, codeReviewSkill.Summary.Description)
		}
	}

	log.Printf("Parsing diff file: %s", diffPath)
	diff, err := parser.ParseDiffFile(diffPath)
	if err != nil {
		return fmt.Errorf("parse diff: %w", err)
	}

	log.Printf("Found %d changed files", len(diff.Files))
	log.Printf("Total changes: %d lines added, %d lines removed", diff.TotalAdded, diff.TotalRemoved)

	var findings []storage.Finding

	log.Printf("Running static analysis...")
	sbx, err := sandbox.NewSandboxWithConfig(repoPath, sandbox.SandboxConfig{
		UnsafeLocal: *unsafeLocal,
	})
	if err != nil {
		log.Printf("Error: Failed to create sandbox: %v", err)
		log.Printf("Hint: Use --unsafe-local flag to enable local sandbox for testing")
		return fmt.Errorf("failed to create sandbox: %w", err)
	}
	log.Printf("Using sandbox type: %s", sbx.GetType())
	defer sbx.Close()

	commands := []string{
		"go vet ./...",
		"go test ./... -short",
	}

	for _, cmd := range commands {
		result := permissionPolicy.CheckCommand(cmd)
		record := storage.PermissionRecord{
			ID:        uuid.New().String(),
			TaskID:    taskID,
			Command:   cmd,
			Action:    string(result.Action),
			Reason:    result.Reason,
			CreatedAt: time.Now(),
		}
		if err := db.CreatePermissionRecord(ctx, record); err != nil {
			return fmt.Errorf("create permission record: %w", err)
		}

		if result.Action == policy.ActionDeny {
			log.Printf("Command denied: %s", cmd)
			metrics.RecordPermissionBlock()
			continue
		}

		if result.Action == policy.ActionReview {
			log.Printf("Command needs review: %s", cmd)
			continue
		}

		log.Printf("Executing: %s", cmd)
		sandboxResult, err := sbx.RunCommand(ctx, cmd, sandbox.DefaultConfig)
		if err != nil {
			log.Printf("Sandbox error: %v", err)
			metrics.RecordError()
			continue
		}

		redactedOutput := redaction.RedactSecrets(sandboxResult.Output)
		redactedError := redaction.RedactSecrets(sandboxResult.Error)

		runRecord := storage.SandboxRun{
			ID:         uuid.New().String(),
			TaskID:     taskID,
			Command:    cmd,
			Output:     redactedOutput,
			Error:      redactedError,
			ExitCode:   sandboxResult.ExitCode,
			TimedOut:   sandboxResult.TimedOut,
			DurationMs: int64(sandboxResult.Duration / time.Millisecond),
			CreatedAt:  time.Now(),
		}
		if err := db.CreateSandboxRun(ctx, runRecord); err != nil {
			return fmt.Errorf("create sandbox run: %w", err)
		}

		metrics.RecordSandboxExecution(sandboxResult.Duration)
		metrics.RecordToolCall()

		if sandboxResult.ExitCode != 0 {
			log.Printf("Command failed with exit code %d", sandboxResult.ExitCode)
			log.Printf("Output: %s", redactedOutput)
			log.Printf("Error: %s", redactedError)
		}
	}

	log.Printf("Running rule detection...")
	detected := ruleDetector.Detect(diff)
	findings = append(findings, detected...)

	findings = policy.RemoveDuplicates(findings)

	for i := range findings {
		findings[i].ID = uuid.New().String()
		findings[i].TaskID = taskID
		findings[i].CreatedAt = time.Now()
		findings[i].Message = redaction.RedactFindingContent(findings[i].Message)
		findings[i].Evidence = redaction.RedactFindingContent(findings[i].Evidence)
		findings[i].Suggestion = redaction.RedactFindingContent(findings[i].Suggestion)

		if err := db.CreateFinding(ctx, findings[i]); err != nil {
			return fmt.Errorf("create finding: %w", err)
		}

		metrics.RecordFinding(findings[i].Severity)
	}

	for _, f := range findings {
		log.Printf("[%s] %s: %s", f.Severity, f.RuleID, f.Message)
	}

	completedAt := time.Now()
	totalTime := completedAt.Sub(start)
	task.CompletedAt = &completedAt
	task.Status = "completed"
	task.TotalTimeMs = int64(totalTime / time.Millisecond)

	if err := db.UpdateReviewTask(ctx, task); err != nil {
		return fmt.Errorf("update task: %w", err)
	}

	metrics.RecordReviewTime(totalTime)
	metrics.RecordTaskCompleted()

	log.Printf("Saving telemetry metrics...")
	metricsSummary := metrics.GetSummary()
	findingsBySeverityJSON, _ := json.Marshal(metricsSummary.FindingsBySeverity)
	if err := db.CreateTelemetryMetrics(ctx, storage.TelemetryMetrics{
		ID:                     uuid.New().String(),
		TaskID:                 taskID,
		TotalReviewTimeMs:      metricsSummary.TotalReviewTime.Milliseconds(),
		SandboxExecutionTimeMs: metricsSummary.SandboxExecutionTime.Milliseconds(),
		SandboxExecutions:      metricsSummary.SandboxExecutions,
		ToolCalls:              metricsSummary.ToolCalls,
		PermissionBlocks:       metricsSummary.PermissionBlocks,
		TotalFindings:          metricsSummary.TotalFindings,
		Errors:                 metricsSummary.Errors,
		TasksCompleted:         metricsSummary.TasksCompleted,
		TasksFailed:            metricsSummary.TasksFailed,
		FindingsBySeverityJSON: string(findingsBySeverityJSON),
		CreatedAt:              time.Now(),
	}); err != nil {
		log.Printf("Warning: Failed to save telemetry metrics: %v", err)
	}

	log.Printf("Generating report...")
	sandboxRuns, _ := db.GetSandboxRunsByTask(ctx, taskID)
	permissionRecords, _ := db.GetPermissionRecords(ctx, taskID)

	if err := output.GenerateReport(taskID, diff, findings, metricsSummary,
		sandboxRuns, permissionRecords, outputDir); err != nil {
		return fmt.Errorf("generate report: %w", err)
	}

	reportContent := output.GenerateReportContent(taskID, diff, findings, metricsSummary, sandboxRuns, permissionRecords)
	reportID := uuid.New().String()
	if err := db.CreateReport(ctx, storage.Report{
		ID:        reportID,
		TaskID:    taskID,
		Content:   reportContent,
		Format:    "markdown",
		CreatedAt: time.Now(),
	}); err != nil {
		log.Printf("Warning: Failed to persist report: %v", err)
	}

	log.Printf("Code review completed in %s", totalTime)
	log.Printf("Total findings: %d", len(findings))
	log.Printf("Report saved to: %s", outputDir)

	return nil
}

func validatePath(path string) error {
	cleanPath := filepath.Clean(path)
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("path traversal detected: %s", path)
	}
	return nil
}
