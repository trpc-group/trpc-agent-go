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
	dryRun       = flag.Bool("dry-run", false, "Run without LLM")
	runFixture   = flag.String("fixture", "", "Run specific fixture")
	listFixtures = flag.Bool("list-fixtures", false, "List available fixtures")
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
	cmd := exec.Command("git", "-C", repoPath, "diff", "HEAD~1", "HEAD")
	output, err := cmd.CombinedOutput()
	if err != nil {
		cmd := exec.Command("git", "-C", repoPath, "diff")
		output, err = cmd.CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("git diff failed: %w", err)
		}
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
	diffPath := filepath.Join(".", "fixtures", fixtureName+".diff")
	if _, err := os.Stat(diffPath); os.IsNotExist(err) {
		return fmt.Errorf("fixture not found: %s", fixtureName)
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

	if !dryRun {
		log.Printf("Running static analysis...")
		sbx, err := sandbox.NewSandbox(repoPath)
		if err != nil {
			log.Printf("Warning: Failed to create sandbox: %v", err)
		} else {
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
				db.CreatePermissionRecord(ctx, record)

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
				}

				runRecord := storage.SandboxRun{
					ID:         uuid.New().String(),
					TaskID:     taskID,
					Command:    cmd,
					Output:     sandboxResult.Output,
					Error:      sandboxResult.Error,
					ExitCode:   sandboxResult.ExitCode,
					TimedOut:   sandboxResult.TimedOut,
					DurationMs: int64(sandboxResult.Duration / time.Millisecond),
					CreatedAt:  time.Now(),
				}
				db.CreateSandboxRun(ctx, runRecord)

				metrics.RecordSandboxExecution(sandboxResult.Duration)
				metrics.RecordToolCall()

				if sandboxResult.ExitCode != 0 {
					log.Printf("Command failed with exit code %d", sandboxResult.ExitCode)
					log.Printf("Output: %s", sandboxResult.Output)
					log.Printf("Error: %s", sandboxResult.Error)
				}
			}
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
			log.Printf("Warning: Failed to save finding: %v", err)
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

	log.Printf("Code review completed in %s", totalTime)
	log.Printf("Total findings: %d", len(findings))
	log.Printf("Report saved to: %s", outputDir)

	return nil
}
