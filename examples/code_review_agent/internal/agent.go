//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package internal

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ReviewInput holds the input for a code review.
type ReviewInput struct {
	DiffFile  string   // path to the diff file
	RepoPath  string   // optional: git repo path for sandbox execution
	DryRun    bool     // if true, skip sandbox execution
	TaskID    string   // optional: pre-set task ID
	FilePaths []string // optional: repository-relative path filter
}

// ReviewResult holds the result of a code review.
type ReviewResult struct {
	TaskID            string
	Task              *ReviewTask
	Findings          []Finding
	Warnings          []Warning
	SandboxRuns       []SandboxRun
	PermissionRecords []PermissionRecord
	Monitoring        *MonitoringSummary
	ReportJSON        string
	ReportMD          string
}

// ReviewAgent orchestrates the full code review pipeline.
type ReviewAgent struct {
	storage Storage
	rules   *RuleEngine
	policy  *PermissionPolicy
	sandbox SandboxExecutor
}

// NewReviewAgentWithSandbox creates an agent with an explicitly selected
// execution backend. Production callers should pass a container-backed
// executor; NewReviewAgentWithConfig is the local development fallback.
func NewReviewAgentWithSandbox(storage Storage, sandbox SandboxExecutor) *ReviewAgent {
	return &ReviewAgent{
		storage: storage,
		rules:   DefaultRuleEngine(),
		policy:  NewDefaultPermissionPolicy(),
		sandbox: sandbox,
	}
}

// NewReviewAgent creates a ReviewAgent with the given storage and
// default rules, policy, and sandbox.
func NewReviewAgent(storage Storage) *ReviewAgent {
	return &ReviewAgent{
		storage: storage,
		rules:   DefaultRuleEngine(),
		policy:  NewDefaultPermissionPolicy(),
		sandbox: NewDefaultSandbox(),
	}
}

// NewReviewAgentWithConfig creates a ReviewAgent with custom sandbox
// config.
func NewReviewAgentWithConfig(storage Storage, sandboxCfg SandboxConfig) *ReviewAgent {
	return &ReviewAgent{
		storage: storage,
		rules:   DefaultRuleEngine(),
		policy:  NewDefaultPermissionPolicy(),
		sandbox: NewSandbox(sandboxCfg),
	}
}

// Review runs the full code review pipeline on the given input.
func (a *ReviewAgent) Review(ctx context.Context, input ReviewInput) (result *ReviewResult, err error) {
	taskID := input.TaskID
	if taskID == "" {
		taskID = "review-" + uuid.NewString()
	}

	monitor := NewMonitor(taskID)
	taskPersisted := false
	defer func() {
		if err != nil && taskPersisted {
			updateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			defer cancel()
			if updateErr := a.storage.UpdateTaskStatus(
				updateCtx, taskID, "failed", time.Now(), monitor.Finalize().TotalDurationMs,
			); updateErr != nil {
				err = errors.Join(err, fmt.Errorf("persist failed task status: %w", updateErr))
			}
		}
	}()

	// 1. Create review task.
	task := &ReviewTask{
		ID:        taskID,
		InputType: "diff",
		InputPath: input.DiffFile,
		Status:    "running",
		CreatedAt: time.Now(),
	}
	if input.DryRun {
		task.InputType = "diff-dry-run"
	}
	if err := a.storage.SaveTask(ctx, task); err != nil {
		return nil, fmt.Errorf("save task: %w", err)
	}
	taskPersisted = true

	// 2. Parse diff.
	var reviewCommit string
	if input.RepoPath != "" && (input.DiffFile == "" || !input.DryRun) {
		root, resolveErr := filepath.Abs(input.RepoPath)
		if resolveErr != nil {
			return nil, fmt.Errorf("resolve repository path: %w", resolveErr)
		}
		reviewCommit, resolveErr = resolveGitCommit(ctx, root, "HEAD")
		if resolveErr != nil {
			return nil, resolveErr
		}
	}
	diffContent, inputType, err := loadReviewInputAt(ctx, input, reviewCommit)
	if err != nil {
		return nil, err
	}
	task.InputType = inputType
	if input.DryRun {
		task.InputType += "-dry-run"
	}
	if input.DiffFile == "" {
		task.InputPath = input.RepoPath
	}

	files, err := ParseDiff(strings.NewReader(string(diffContent)))
	if err != nil {
		return nil, fmt.Errorf("parse diff: %w", err)
	}
	diffSummary := DiffSummary(files)
	task.DiffSummary = diffSummary
	if err := a.storage.UpdateTaskDiff(ctx, task.ID, task.InputType, task.InputPath, task.DiffSummary); err != nil {
		return nil, fmt.Errorf("save diff summary: %w", err)
	}

	// 3. Run rules.
	monitor.RecordToolCall()
	rawFindings := a.rules.Run(files)

	// 4. Redact sensitive info (already done in RuleEngine.Run, but
	// double-check here for safety).
	for i := range rawFindings {
		rawFindings[i].Evidence = RedactSensitiveInfo(rawFindings[i].Evidence)
		if rawFindings[i].ID == "" {
			rawFindings[i].ID = uuid.NewString()
		}
	}

	// 5. Dedup.
	deduped := DedupFindings(rawFindings)

	// 6. Split into findings + warnings.
	findings, warnings := SplitFindings(deduped)

	// Record findings in monitor.
	for _, f := range findings {
		monitor.RecordFinding(f)
	}
	for range warnings {
		monitor.RecordWarning()
	}

	// 7. Permission decisions + sandbox execution.
	var permissionRecords []PermissionRecord
	var sandboxRuns []SandboxRun

	if !input.DryRun && input.RepoPath != "" {
		sandboxCommands, err := a.determineSandboxCommandsAt(
			ctx,
			files,
			input.RepoPath,
			reviewCommit,
		)
		if err != nil {
			return nil, fmt.Errorf("determine sandbox commands: %w", err)
		}
		diffHash := sha256.Sum256(diffContent)
		reviewScope := ReviewScope{
			FilePaths:  append([]string(nil), input.FilePaths...),
			HeadCommit: reviewCommit,
			DiffSHA256: hex.EncodeToString(diffHash[:]),
		}

		for _, cmd := range sandboxCommands {
			decision, reason := a.policy.Decide(cmd)
			rec := &PermissionRecord{
				ID:        uuid.NewString(),
				TaskID:    taskID,
				Command:   cmd,
				Decision:  decision,
				Reason:    reason,
				Timestamp: time.Now().Format(time.RFC3339Nano),
			}
			permissionRecords = append(permissionRecords, *rec)
			if err := a.storage.SavePermissionDecision(ctx, rec); err != nil {
				return nil, fmt.Errorf("save permission decision: %w", err)
			}

			monitor.RecordToolCall()
			if IsBlocked(decision) {
				monitor.RecordPermissionBlock()
				continue
			}

			// Execute in sandbox.
			monitor.StartSandbox()
			run := a.sandbox.ExecuteReview(
				ctx,
				taskID,
				cmd,
				decision,
				reason,
				ReviewScope{
					FilePaths:  append([]string(nil), reviewScope.FilePaths...),
					HeadCommit: reviewScope.HeadCommit,
					DiffSHA256: reviewScope.DiffSHA256,
				},
			)
			sandboxRuns = append(sandboxRuns, *run)
			monitor.EndSandbox()

			if err := a.storage.SaveSandboxRun(ctx, run); err != nil {
				return nil, fmt.Errorf("save sandbox run: %w", err)
			}

			if run.Status == SandboxStatusTimeout {
				monitor.RecordError("sandbox_timeout")
			} else if run.Status == SandboxStatusFailed {
				monitor.RecordError("sandbox_failed")
			} else if run.Status == SandboxStatusError {
				monitor.RecordError("sandbox_error")
			}
		}
	}

	// 8. Save findings to storage.
	for i := range findings {
		if findings[i].ID == "" {
			findings[i].ID = uuid.NewString()
		}
		if err := a.storage.SaveFinding(ctx, taskID, &findings[i]); err != nil {
			return nil, fmt.Errorf("save finding: %w", err)
		}
	}

	// 9. Generate reports.
	monitoring := monitor.Finalize()
	if err := a.storage.SaveMonitoring(ctx, monitoring); err != nil {
		return nil, fmt.Errorf("save monitoring summary: %w", err)
	}

	reportData := NewReportData(
		taskID, diffSummary, findings, warnings,
		permissionRecords, sandboxRuns, monitoring,
	)
	jsonReport, err := GenerateJSONReport(reportData)
	if err != nil {
		return nil, fmt.Errorf("generate json report: %w", err)
	}
	mdReport := GenerateMarkdownReport(reportData)

	report := &ReviewReport{
		ID:         uuid.NewString(),
		TaskID:     taskID,
		ReportJSON: jsonReport,
		ReportMD:   mdReport,
		CreatedAt:  time.Now(),
	}
	if err := a.storage.SaveReport(ctx, report); err != nil {
		return nil, fmt.Errorf("save report: %w", err)
	}
	for _, artifact := range []*Artifact{
		{ID: uuid.NewString(), TaskID: taskID, Name: "review_report.json", MIMEType: "application/json", Size: int64(len(jsonReport)), CreatedAt: time.Now()},
		{ID: uuid.NewString(), TaskID: taskID, Name: "review_report.md", MIMEType: "text/markdown", Size: int64(len(mdReport)), CreatedAt: time.Now()},
	} {
		if err := a.storage.SaveArtifact(ctx, artifact); err != nil {
			return nil, fmt.Errorf("save artifact metadata: %w", err)
		}
	}

	// 10. Update task status.
	completedAt := time.Now()
	if err := a.storage.UpdateTaskStatus(ctx, taskID, "completed", completedAt, monitoring.TotalDurationMs); err != nil {
		return nil, fmt.Errorf("complete review task: %w", err)
	}

	task.Status = "completed"
	task.CompletedAt = &completedAt
	task.TotalDurationMs = monitoring.TotalDurationMs

	return &ReviewResult{
		TaskID:            taskID,
		Task:              task,
		Findings:          findings,
		Warnings:          warnings,
		SandboxRuns:       sandboxRuns,
		PermissionRecords: permissionRecords,
		Monitoring:        monitoring,
		ReportJSON:        jsonReport,
		ReportMD:          mdReport,
	}, nil
}

// determineSandboxCommands figures out what commands to run in the
// sandbox based on the changed files.
func (a *ReviewAgent) determineSandboxCommands(
	ctx context.Context,
	files []DiffFile,
	repoPath string,
) ([]string, string, error) {
	root, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, "", fmt.Errorf("resolve repository path: %w", err)
	}
	headCommit, err := resolveGitCommit(ctx, root, "HEAD")
	if err != nil {
		return nil, "", err
	}
	commands, err := a.determineSandboxCommandsAt(
		ctx,
		files,
		root,
		headCommit,
	)
	if err != nil {
		return nil, "", err
	}
	return commands, headCommit, nil
}

func (a *ReviewAgent) determineSandboxCommandsAt(
	ctx context.Context,
	files []DiffFile,
	repoPath string,
	headCommit string,
) ([]string, error) {
	root, err := filepath.Abs(repoPath)
	if err != nil {
		return nil, fmt.Errorf("resolve repository path: %w", err)
	}
	selectedModuleStates := make(map[string]bool)
	for _, file := range files {
		if file.IsRename && file.OldPath != "" &&
			filepath.Base(filepath.FromSlash(file.OldPath)) == "go.mod" {
			clean, err := cleanChangedPath(file.OldPath)
			if err != nil {
				return nil, err
			}
			selectedModuleStates[filepath.ToSlash(clean)] = false
		}
		if filepath.Base(filepath.FromSlash(file.Path)) == "go.mod" {
			clean, err := cleanChangedPath(file.Path)
			if err != nil {
				return nil, err
			}
			selectedModuleStates[filepath.ToSlash(clean)] = !file.IsDeleted
		}
	}
	var ownerPaths []string
	for _, f := range files {
		paths := []string{f.Path}
		if f.IsRename && f.OldPath != "" && f.OldPath != f.Path {
			paths = append(paths, f.OldPath)
		}
		for _, path := range paths {
			base := filepath.Base(filepath.FromSlash(path))
			if !strings.HasSuffix(path, ".go") && base != "go.mod" && base != "go.sum" {
				continue
			}
			ownerPaths = append(ownerPaths, path)
		}
	}
	if len(ownerPaths) == 0 {
		return nil, nil
	}
	headModuleStates, err := loadModuleStatesAtCommit(
		ctx,
		root,
		headCommit,
	)
	if err != nil {
		return nil, err
	}
	modules := map[string]bool{}
	for _, path := range ownerPaths {
		module, err := owningGoModule(
			path,
			selectedModuleStates,
			headModuleStates,
		)
		if err != nil {
			return nil, err
		}
		modules[module] = true
	}
	ordered := make([]string, 0, len(modules))
	for module := range modules {
		ordered = append(ordered, module)
	}
	sort.Strings(ordered)
	cmds := make([]string, 0, len(ordered)*2)
	for _, module := range ordered {
		prefix := "go "
		if module != "." {
			if strings.ContainsAny(module, " \t\r\n") {
				return nil, fmt.Errorf("Go module path %q contains unsupported whitespace", module)
			}
			prefix += "-C ./" + filepath.ToSlash(module) + " "
		}
		cmds = append(cmds, prefix+"vet ./...")
		cmds = append(cmds, prefix+"test ./... -count=1 -timeout=30s")
	}
	return cmds, nil
}

func cleanChangedPath(changedPath string) (string, error) {
	clean := filepath.Clean(filepath.FromSlash(changedPath))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("changed Go file escapes repository: %q", changedPath)
	}
	return clean, nil
}

func owningGoModule(
	changedPath string,
	selectedModuleStates map[string]bool,
	headModuleStates map[string]bool,
) (string, error) {
	clean, err := cleanChangedPath(changedPath)
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(clean)
	for {
		modulePath := filepath.ToSlash(filepath.Join(dir, "go.mod"))
		exists, selected := selectedModuleStates[modulePath]
		if selected && exists {
			return dir, nil
		}
		if !selected && headModuleStates[modulePath] {
			return dir, nil
		}
		if dir == "." {
			break
		}
		dir = filepath.Dir(dir)
	}
	return "", fmt.Errorf("no go.mod owns changed Go file %q", changedPath)
}

func loadModuleStatesAtCommit(
	ctx context.Context,
	root string,
	headCommit string,
) (map[string]bool, error) {
	output, err := runSnapshotGitList(
		ctx,
		root,
		[]string{"ls-tree", "-r", "-z", "--full-tree", headCommit},
		"list HEAD files for module discovery",
	)
	if err != nil {
		return nil, err
	}
	modules := make(map[string]bool)
	for _, record := range strings.Split(string(output), "\x00") {
		if record == "" {
			continue
		}
		metadata, rawPath, ok := strings.Cut(record, "\t")
		if !ok {
			return nil, fmt.Errorf("malformed HEAD tree entry during module discovery")
		}
		if filepath.Base(filepath.FromSlash(rawPath)) != "go.mod" {
			continue
		}
		fields := strings.Fields(metadata)
		if len(fields) != 3 {
			return nil, fmt.Errorf("malformed HEAD tree metadata for %q", rawPath)
		}
		if fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") {
			return nil, fmt.Errorf("HEAD path %q is not a regular file", rawPath)
		}
		clean, err := cleanChangedPath(rawPath)
		if err != nil {
			return nil, err
		}
		modules[filepath.ToSlash(clean)] = true
	}
	return modules, nil
}
