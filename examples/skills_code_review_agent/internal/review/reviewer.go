//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

var reviewAuditPersistenceTimeout = 5 * time.Second

const (
	reviewDiffMaxTotalBytes    = 16 << 20
	reviewDiffMaxFileBytes     = 4 << 20
	reviewDiffMaxFileListBytes = 1 << 20
	reviewDiffMaxFiles         = 4096
	reviewDiffReadChunkSize    = 32 << 10
)

type diffInputBudgetError struct {
	path   string
	reason string
	limit  int64
	got    int64
}

func (e *diffInputBudgetError) Error() string {
	return fmt.Sprintf("review diff input %s for %s: got %d, limit %d", e.reason, e.path, e.got, e.limit)
}

type diffInputBudget struct {
	maxTotal int64
	used     int64
}

func newDiffInputBudget(maxTotal int64) *diffInputBudget {
	return &diffInputBudget{maxTotal: maxTotal}
}

func (b *diffInputBudget) remaining() int64 {
	if b == nil || b.maxTotal <= 0 {
		return 0
	}
	return b.maxTotal - b.used
}

func (b *diffInputBudget) reserve(path string, n int64) error {
	if n <= 0 {
		return nil
	}
	if b == nil {
		return nil
	}
	if b.maxTotal <= 0 {
		return &diffInputBudgetError{
			path:   path,
			reason: "total-byte budget exceeded",
			limit:  b.maxTotal,
			got:    n,
		}
	}
	if b.used+n > b.maxTotal {
		return &diffInputBudgetError{
			path:   path,
			reason: "total-byte budget exceeded",
			limit:  b.maxTotal,
			got:    b.used + n,
		}
	}
	b.used += n
	return nil
}

func RunReview(ctx context.Context, cfg ReviewConfig) (ReviewReport, string, string, error) {
	ctx, span := atrace.Tracer.Start(ctx, "examples.code_review_agent.review")
	defer span.End()
	start := time.Now()
	if cfg.OutputDir == "" {
		cfg.OutputDir = "output"
	}
	if cfg.DBPath == "" {
		cfg.DBPath = filepath.Join(cfg.OutputDir, "reviews.sqlite")
	}
	if cfg.OutputLimitBytes <= 0 {
		cfg.OutputLimitBytes = 64 * 1024
	}
	taskID := newID("review")
	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return ReviewReport{}, "", "", err
	}
	var store ReviewStore
	err := withReviewAuditContext(ctx, func(auditCtx context.Context) error {
		var openErr error
		store, openErr = OpenStore(auditCtx, cfg.DBPath)
		return openErr
	})
	if err != nil {
		return ReviewReport{}, "", "", err
	}
	defer store.Close()
	task := ReviewTask{
		ID:        taskID,
		Status:    StatusPending,
		StartedAt: start,
		InputMode: configuredInputMode(cfg),
	}
	if err := withReviewAuditContext(ctx, func(auditCtx context.Context) error {
		return store.CreateTask(auditCtx, task)
	}); err != nil {
		return ReviewReport{}, "", "", wrapStoreErr("create review task", err)
	}
	persistFailure := func(errorClass string, origErr error) (ReviewReport, string, string, error) {
		task.Status = StatusFailed
		task.EndedAt = time.Now()
		if saveErr := wrapStoreErr(
			"mark failed task",
			withReviewAuditContext(ctx, func(auditCtx context.Context) error {
				return store.MarkTaskFailed(auditCtx, task, errorClass)
			}),
		); saveErr != nil {
			return ReviewReport{}, "", "", errors.Join(origErr, saveErr)
		}
		return ReviewReport{}, "", "", origErr
	}
	if err := ctx.Err(); err != nil {
		return persistFailure(contextErrorClass(err), err)
	}

	var cleanupSmokeRepo func() error
	if cfg.ContainerSmoke {
		repoPath, cleanup, err := prepareContainerSmokeRepo(ctx)
		if err != nil {
			return persistFailure("prepare_smoke_repo", err)
		}
		cfg.RepoPath = repoPath
		cfg.Executor = "container"
		cfg.InstallStaticcheck = true
		cleanupSmokeRepo = cleanup
		defer cleanupSmokeRepo()
	}

	if err := loadCodeReviewSkill(); err != nil {
		return persistFailure("load_skill", err)
	}
	diff, inputMode, err := loadInputDiff(ctx, cfg)
	if err != nil {
		return persistFailure("load_input", err)
	}
	task.InputMode = inputMode
	pd, err := ParseUnifiedDiff(diff)
	if err != nil {
		return persistFailure("parse_diff", err)
	}
	if cfg.RepoPath != "" {
		enrichPackageInfoFromRepoContext(ctx, &pd, cfg.RepoPath)
	}
	task.Status = StatusCompleted
	findings, warnings, needsHuman := AnalyzeDiff(pd)
	if cfg.FakeModel || cfg.LLMReview {
		llmFindings, err := RunLLMReview(ctx, LLMReviewConfig{
			TaskID:       task.ID,
			DiffRaw:      pd.Raw,
			ParsedDiff:   pd,
			InputSummary: pd.Summary,
			RuleFindings: append(append([]Finding{}, findings...), append(warnings, needsHuman...)...),
			FakeModel:    cfg.FakeModel,
			Provider:     cfg.ModelProvider,
			Model:        cfg.Model,
			BaseURL:      cfg.ModelBaseURL,
			Timeout:      cfg.Timeout,
		})
		if err != nil {
			needsHuman = append(needsHuman, Finding{
				Severity:       SeverityMedium,
				Category:       "llm_review",
				File:           "",
				Line:           0,
				Title:          "LLM review path did not complete",
				Evidence:       redactSecrets(err.Error()),
				Recommendation: "Use --fake-model for deterministic local coverage or configure OPENAI_API_KEY before enabling --rule-only=false.",
				Confidence:     0.66,
				Source:         "llm",
				RuleID:         "llm/review-failed",
			})
		} else {
			llmConfirmed, llmWarnings, llmNeedsHuman := bucketSupplementalFindings(llmFindings)
			findings = append(findings, llmConfirmed...)
			warnings = append(warnings, llmWarnings...)
			needsHuman = append(needsHuman, llmNeedsHuman...)
		}
	}
	runnerCfg := cfg
	if cfg.DryRun {
		runnerCfg.Executor = "fake"
		if cfg.Fixture == "sandbox_failure" {
			runnerCfg.Executor = "fake-fail"
		}
	}
	runner, err := NewSandboxRunnerWithContext(ctx, runnerCfg)
	var sandboxResult SandboxResult
	if err != nil {
		sandboxResult = SandboxResult{Runs: []SandboxRun{
			failedSetupRun(task.ID, executorLabel(runnerCfg.Executor), "init_executor", err),
		}}
	} else {
		defer runner.Close()
		sandboxResult = runner.RunChecks(ctx, task.ID, cfg.RepoPath, pd)
	}
	findings = append(findings, sandboxResult.Findings...)
	needsHuman = append(needsHuman, sandboxReviewItems(sandboxResult.Runs, pd, sandboxResult.Findings)...)
	if inputMode == "file-list" {
		needsHuman = append(needsHuman, fileListIncompleteFinding())
	}
	task.EndedAt = time.Now()
	span.SetAttributes(
		attribute.String("review.task_id", task.ID),
		attribute.Int("review.files_changed", pd.Summary.FilesChanged),
		attribute.Int("review.go_files", pd.Summary.GoFiles),
		attribute.Bool("review.skill_loaded", sandboxResult.SkillLoaded),
	)

	report := ReviewReport{
		Task:              task,
		Input:             pd.Summary,
		Packages:          pd.Packages,
		Findings:          redactFindingSlice(DedupeFindings(findings)),
		Warnings:          redactFindingSlice(DedupeFindings(warnings)),
		NeedsHumanReview:  redactFindingSlice(DedupeFindings(needsHuman)),
		SandboxRuns:       sandboxResult.Runs,
		Permissions:       sandboxResult.Decisions,
		PermissionSummary: buildPermissionSummary(sandboxResult.Decisions),
		Conclusion:        buildConclusion(findings, needsHuman),
	}
	report.Metrics = buildMetrics(report, time.Since(start))

	if err := ctx.Err(); err != nil {
		return persistFailure(contextErrorClass(err), err)
	}
	jsonPath, mdPath, err := finalizeReportArtifacts(cfg.OutputDir, &report, sandboxResult.Artifacts)
	if err != nil {
		return persistFailure("write_report", err)
	}
	if err := withReviewAuditContext(ctx, func(auditCtx context.Context) error {
		return store.SaveReport(auditCtx, report, pd, jsonPath, mdPath)
	}); err != nil {
		return persistFailure("save_report", wrapStoreErr("save review", err))
	}
	return report, jsonPath, mdPath, nil
}

func withReviewAuditContext(ctx context.Context, persist func(context.Context) error) error {
	auditCtx, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		reviewAuditPersistenceTimeout,
	)
	defer cancel()
	return persist(auditCtx)
}

func contextErrorClass(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "context_deadline"
	}
	return "context_canceled"
}

func enrichPackageInfoFromRepo(pd *ParsedDiff, repoPath string) {
	enrichPackageInfoFromRepoContext(context.Background(), pd, repoPath)
}

func enrichPackageInfoFromRepoContext(ctx context.Context, pd *ParsedDiff, repoPath string) {
	for i := range pd.Files {
		file := &pd.Files[i]
		if !file.IsGo || file.PackageName != "" {
			continue
		}
		data, err := readReviewInputFile(
			ctx,
			filepath.Join(repoPath, filepath.FromSlash(file.NewPath)),
			file.NewPath,
			reviewDiffMaxFileBytes,
		)
		if err != nil {
			continue
		}
		_ = scanDiffLines(string(data), reviewDiffMaxFileBytes, func(line string) error {
			if m := packageDeclRE.FindStringSubmatch(line); m != nil {
				file.PackageName = m[1]
			}
			return nil
		})
	}
	attachPackageInfoFromFiles(pd)
}

func configuredInputMode(cfg ReviewConfig) string {
	switch {
	case cfg.ContainerSmoke:
		return "container-smoke"
	case cfg.DiffFile != "":
		return "diff-file"
	case cfg.FileList != "":
		return "file-list"
	case cfg.Fixture != "":
		return "fixture:" + cfg.Fixture
	case cfg.RepoPath != "":
		return "repo-path"
	default:
		return ""
	}
}

func loadCodeReviewSkill() error {
	repo, err := skill.NewFSRepository(filepath.Join(exampleDir(), "skills"))
	if err != nil {
		return fmt.Errorf("load skills repo: %w", err)
	}
	sk, err := repo.Get("code-review")
	if err != nil {
		return fmt.Errorf("load code-review skill: %w", err)
	}
	if strings.TrimSpace(sk.Body) == "" {
		return errors.New("code-review skill has empty SKILL.md body")
	}
	if _, err := repo.Path("code-review"); err != nil {
		return fmt.Errorf("resolve code-review skill path: %w", err)
	}
	return nil
}

func loadInputDiff(ctx context.Context, cfg ReviewConfig) (string, string, error) {
	if selected := selectedInputs(cfg); len(selected) > 1 {
		return "", "", fmt.Errorf("choose only one input source: %s", strings.Join(selected, ", "))
	}
	switch {
	case cfg.DiffFile != "":
		data, err := readReviewInputFile(ctx, cfg.DiffFile, cfg.DiffFile, reviewDiffMaxTotalBytes)
		if err != nil {
			return "", "", err
		}
		if err := validateDiffChunkPerFile(string(data)); err != nil {
			return "", "", err
		}
		return string(data), "diff-file", nil
	case cfg.FileList != "":
		diff, err := fileListSyntheticDiffContext(ctx, cfg.FileList)
		if err != nil {
			return "", "", err
		}
		return diff, "file-list", nil
	case cfg.Fixture != "":
		path := filepath.Join(exampleDir(), "fixtures", cfg.Fixture+".diff")
		data, err := readReviewInputFile(ctx, path, path, reviewDiffMaxTotalBytes)
		if err != nil {
			return "", "", err
		}
		if err := validateDiffChunkPerFile(string(data)); err != nil {
			return "", "", err
		}
		return string(data), "fixture:" + cfg.Fixture, nil
	case cfg.ContainerSmoke:
		diff, err := gitDiff(ctx, cfg.RepoPath)
		if err != nil {
			return "", "", err
		}
		return diff, "container-smoke", nil
	case cfg.RepoPath != "":
		diff, err := gitDiff(ctx, cfg.RepoPath)
		if err != nil {
			return "", "", err
		}
		return diff, "repo-path", nil
	default:
		return "", "", errors.New("one of --diff-file, --repo-path, --file-list or --fixture is required")
	}
}

func selectedInputs(cfg ReviewConfig) []string {
	var selected []string
	if strings.TrimSpace(cfg.DiffFile) != "" {
		selected = append(selected, "--diff-file")
	}
	if strings.TrimSpace(cfg.RepoPath) != "" && !cfg.ContainerSmoke {
		selected = append(selected, "--repo-path")
	}
	if strings.TrimSpace(cfg.FileList) != "" {
		selected = append(selected, "--file-list")
	}
	if strings.TrimSpace(cfg.Fixture) != "" {
		selected = append(selected, "--fixture")
	}
	if cfg.ContainerSmoke {
		selected = append(selected, "--container-smoke")
	}
	return selected
}

func gitDiff(ctx context.Context, repoPath string) (string, error) {
	budget := newDiffInputBudget(reviewDiffMaxTotalBytes)
	var chunks []string
	var out []byte
	var err error
	if gitHasHEAD(ctx, repoPath) {
		out, err = gitOutputLimited(ctx, repoPath, budget.remaining(), "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--unified=3", "HEAD")
		if err == nil && len(out) > 0 {
			if err := validateDiffChunkPerFile(string(out)); err != nil {
				return "", err
			}
			if err := appendDiffChunk(&chunks, string(out), budget, "git diff"); err != nil {
				return "", err
			}
		}
		if err != nil {
			return "", fmt.Errorf("git diff: %w", err)
		}
	} else {
		out, err = gitOutputLimited(ctx, repoPath, reviewDiffMaxFileListBytes, "ls-files", "--cached", "-z")
		if err == nil && len(out) > 0 {
			diff, diffErr := worktreeFileDiffsWithLimit(ctx, repoPath, out, true, budget.remaining())
			if diffErr != nil {
				return "", diffErr
			}
			out = []byte(diff)
			if err := appendDiffChunk(&chunks, string(out), budget, "cached worktree files"); err != nil {
				return "", err
			}
		}
		if err != nil {
			return "", fmt.Errorf("git ls-files: %w", err)
		}
	}
	untracked, untrackedErr := gitOutputLimited(ctx, repoPath, reviewDiffMaxFileListBytes, "ls-files", "--others", "--exclude-standard", "-z")
	if untrackedErr == nil && len(untracked) > 0 {
		untrackedDiff, err := worktreeFileDiffsWithLimit(ctx, repoPath, untracked, false, budget.remaining())
		if err != nil {
			return "", err
		}
		if untrackedDiff != "" {
			if err := appendDiffChunk(&chunks, untrackedDiff, budget, "untracked worktree files"); err != nil {
				return "", err
			}
		}
	}
	if untrackedErr != nil {
		return "", fmt.Errorf("git ls-files: %w", untrackedErr)
	}
	if len(chunks) > 0 {
		return strings.Join(chunks, "\n"), nil
	}
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	if untrackedErr != nil {
		return "", fmt.Errorf("git ls-files: %w", untrackedErr)
	}
	return "", nil
}

func gitHasHEAD(ctx context.Context, repoPath string) bool {
	if _, err := gitOutput(ctx, repoPath, "rev-parse", "--verify", "HEAD"); err != nil {
		return false
	}
	return true
}

func gitOutput(ctx context.Context, repoPath string, args ...string) ([]byte, error) {
	cmd := gitCommand(ctx, repoPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && strings.TrimSpace(stderr.String()) != "" {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, err
}

func fileListSyntheticDiff(path string) (string, error) {
	return fileListSyntheticDiffContext(context.Background(), path)
}

func fileListSyntheticDiffContext(ctx context.Context, path string) (string, error) {
	data, err := readReviewInputFile(ctx, path, path, reviewDiffMaxFileListBytes)
	if err != nil {
		return "", fmt.Errorf("read file list: %w", err)
	}
	var files []string
	if err := scanDiffLines(string(data), reviewDiffMaxFileListBytes, func(line string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		file := filepath.ToSlash(strings.TrimSpace(line))
		if file != "" && !strings.HasPrefix(file, "#") {
			files = append(files, file)
		}
		return nil
	}); err != nil {
		return "", err
	}
	sort.Strings(files)
	if len(files) > reviewDiffMaxFiles {
		return "", &diffInputBudgetError{
			path:   path,
			reason: "file-count budget exceeded",
			limit:  reviewDiffMaxFiles,
			got:    int64(len(files)),
		}
	}
	budget := newDiffInputBudget(reviewDiffMaxTotalBytes)
	var chunks []string
	for _, file := range files {
		var b strings.Builder
		writeSyntheticFileDiff(&b, file)
		if err := appendDiffChunk(&chunks, b.String(), budget, file); err != nil {
			return "", err
		}
	}
	return strings.Join(chunks, "\n"), nil
}

func untrackedFileDiffs(repoPath string, raw []byte) (string, error) {
	return worktreeFileDiffsWithLimit(context.Background(), repoPath, raw, false, reviewDiffMaxTotalBytes)
}

func worktreeFileDiffs(repoPath string, raw []byte, allowMissing bool) (string, error) {
	return worktreeFileDiffsWithLimit(context.Background(), repoPath, raw, allowMissing, reviewDiffMaxTotalBytes)
}

func worktreeFileDiffsWithLimit(
	ctx context.Context,
	repoPath string,
	raw []byte,
	allowMissing bool,
	maxTotalBytes int64,
) (string, error) {
	if len(raw) > reviewDiffMaxFileListBytes {
		return "", &diffInputBudgetError{
			path:   "worktree file list",
			reason: "file-list byte budget exceeded",
			limit:  reviewDiffMaxFileListBytes,
			got:    int64(len(raw)),
		}
	}
	parts := bytes.Split(raw, []byte{0})
	var files []string
	for _, part := range parts {
		file := filepath.ToSlash(string(part))
		if file != "" {
			if len(files)+1 > reviewDiffMaxFiles {
				return "", &diffInputBudgetError{
					path:   file,
					reason: "file-count budget exceeded",
					limit:  reviewDiffMaxFiles,
					got:    int64(len(files) + 1),
				}
			}
			files = append(files, file)
		}
	}
	sort.Strings(files)
	budget := newDiffInputBudget(maxTotalBytes)
	var chunks []string
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		abs := filepath.Join(repoPath, filepath.FromSlash(file))
		info, err := os.Lstat(abs)
		if err != nil {
			if allowMissing && errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", fmt.Errorf("stat worktree file %s: %w", file, err)
		}
		if info.IsDir() {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(abs)
			if err != nil {
				return "", fmt.Errorf("read worktree symlink %s: %w", file, err)
			}
			var b strings.Builder
			writeNewFileDiff(&b, file, []string{filepath.ToSlash(target)}, false)
			if err := validateDiffSectionSize(b.String(), file); err != nil {
				return "", err
			}
			if err := appendDiffChunk(&chunks, b.String(), budget, file); err != nil {
				return "", err
			}
			continue
		}
		data, err := readReviewInputFile(ctx, abs, file, reviewDiffMaxFileBytes)
		if err != nil {
			return "", fmt.Errorf("read worktree file %s: %w", file, err)
		}
		if bytes.Contains(data, []byte{0}) {
			var b strings.Builder
			newPath := gitDiffPath("b/", file)
			fmt.Fprintf(&b, "diff --git %s %s\nnew file mode 100644\nBinary files /dev/null and %s differ\n", gitDiffPath("a/", file), newPath, newPath)
			if err := validateDiffSectionSize(b.String(), file); err != nil {
				return "", err
			}
			if err := appendDiffChunk(&chunks, b.String(), budget, file); err != nil {
				return "", err
			}
			continue
		}
		text := string(data)
		noNewline := text != "" && !strings.HasSuffix(text, "\n")
		text = strings.TrimSuffix(text, "\n")
		var b strings.Builder
		writeNewFileDiffText(&b, file, text, noNewline)
		if err := validateDiffSectionSize(b.String(), file); err != nil {
			return "", err
		}
		if err := appendDiffChunk(&chunks, b.String(), budget, file); err != nil {
			return "", err
		}
	}
	return strings.Join(chunks, "\n"), nil
}

func appendDiffChunk(chunks *[]string, chunk string, budget *diffInputBudget, path string) error {
	if chunk == "" {
		return nil
	}
	extra := int64(len(chunk))
	if len(*chunks) > 0 {
		extra++
	}
	if err := budget.reserve(path, extra); err != nil {
		return err
	}
	*chunks = append(*chunks, chunk)
	return nil
}

func validateDiffSectionSize(section, path string) error {
	if int64(len(section)) > reviewDiffMaxFileBytes {
		return &diffInputBudgetError{
			path:   path,
			reason: "per-file diff byte budget exceeded",
			limit:  reviewDiffMaxFileBytes,
			got:    int64(len(section)),
		}
	}
	return nil
}

func readReviewInputFile(ctx context.Context, path, label string, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var out bytes.Buffer
	buf := make([]byte, reviewDiffReadChunkSize)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := file.Read(buf)
		if n > 0 {
			if maxBytes > 0 && int64(out.Len()+n) > maxBytes {
				return nil, &diffInputBudgetError{
					path:   label,
					reason: "per-input byte budget exceeded",
					limit:  maxBytes,
					got:    int64(out.Len() + n),
				}
			}
			_, _ = out.Write(buf[:n])
		}
		if errors.Is(readErr, io.EOF) {
			return out.Bytes(), nil
		}
		if readErr != nil {
			return nil, readErr
		}
	}
}

func gitOutputLimited(ctx context.Context, repoPath string, maxBytes int64, args ...string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	cmd := gitCommand(ctx, repoPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	out, readErr := io.ReadAll(io.LimitReader(stdout, maxBytes+1))
	if readErr != nil {
		_ = cmd.Wait()
		return nil, readErr
	}
	if err := ctx.Err(); err != nil {
		_ = cmd.Wait()
		return nil, err
	}
	if maxBytes >= 0 && int64(len(out)) > maxBytes {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = cmd.Wait()
		return nil, &diffInputBudgetError{
			path:   strings.Join(args, " "),
			reason: "git stdout byte budget exceeded",
			limit:  maxBytes,
			got:    int64(len(out)),
		}
	}
	waitErr := cmd.Wait()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if waitErr != nil && strings.TrimSpace(stderr.String()) != "" {
		return nil, fmt.Errorf("%w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}
	return out, waitErr
}

func gitCommand(ctx context.Context, repoPath string, args ...string) *exec.Cmd {
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	cmd.Env = sanitizedGitEnv()
	return cmd
}

func sanitizedGitEnv() []string {
	keys := []string{
		"PATH",
		"SystemRoot",
		"WINDIR",
		"COMSPEC",
		"PATHEXT",
		"TMPDIR",
		"TMP",
		"TEMP",
	}
	env := make([]string, 0, len(keys)+5)
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok {
			env = append(env, key+"="+value)
		}
	}
	env = append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL="+os.DevNull,
		"GIT_CONFIG_COUNT=0",
		"GIT_TERMINAL_PROMPT=0",
		"GIT_OPTIONAL_LOCKS=0",
	)
	return env
}

func validateDiffChunkPerFile(raw string) error {
	if raw == "" {
		return nil
	}
	sections := splitDiffSections(raw)
	if len(sections) == 0 {
		sections = []string{raw}
	}
	for _, section := range sections {
		if int64(len(section)) > reviewDiffMaxFileBytes {
			return &diffInputBudgetError{
				path:   diffSectionLabel(section),
				reason: "per-file diff byte budget exceeded",
				limit:  reviewDiffMaxFileBytes,
				got:    int64(len(section)),
			}
		}
	}
	return nil
}

func diffSectionLabel(section string) string {
	line, _, _ := strings.Cut(section, "\n")
	return strings.TrimSpace(line)
}

func writeNewFileDiffText(b *strings.Builder, file, text string, noNewline bool) {
	lineCount := 0
	if text != "" {
		lineCount = strings.Count(text, "\n") + 1
	}
	fmt.Fprintf(b, "diff --git %s %s\n", gitDiffPath("a/", file), gitDiffPath("b/", file))
	fmt.Fprintf(b, "new file mode 100644\n")
	fmt.Fprintf(b, "--- /dev/null\n")
	fmt.Fprintf(b, "+++ %s\n", gitDiffPath("b/", file))
	fmt.Fprintf(b, "@@ -0,0 +1,%d @@\n", lineCount)
	for start := 0; start < len(text); {
		end := strings.IndexByte(text[start:], '\n')
		if end < 0 {
			fmt.Fprintf(b, "+%s\n", text[start:])
			break
		}
		fmt.Fprintf(b, "+%s\n", text[start:start+end])
		start += end + 1
	}
	if noNewline {
		b.WriteString("\\ No newline at end of file\n")
	}
}

func writeSyntheticFileDiff(b *strings.Builder, file string) {
	fmt.Fprintf(b, "diff --git %s %s\n", gitDiffPath("a/", file), gitDiffPath("b/", file))
	fmt.Fprintf(b, "--- %s\n", gitDiffPath("a/", file))
	fmt.Fprintf(b, "+++ %s\n", gitDiffPath("b/", file))
}

func fileListIncompleteFinding() Finding {
	return Finding{
		Severity:       SeverityMedium,
		Category:       "input_coverage",
		File:           "",
		Line:           0,
		Title:          "File-list review is metadata-only",
		Evidence:       "The file-list input contains file names but no diff hunks or source content, so code rules, LLM review, and repository checks cannot inspect changed code.",
		Recommendation: "Provide --diff-file or --repo-path when code-level review is required, or treat this result as incomplete metadata coverage.",
		Confidence:     0.90,
		Source:         "input",
		RuleID:         "input/file-list-incomplete",
	}
}

func writeNewFileDiff(b *strings.Builder, file string, lines []string, noNewline bool) {
	fmt.Fprintf(b, "diff --git %s %s\n", gitDiffPath("a/", file), gitDiffPath("b/", file))
	fmt.Fprintf(b, "new file mode 100644\n")
	fmt.Fprintf(b, "--- /dev/null\n")
	fmt.Fprintf(b, "+++ %s\n", gitDiffPath("b/", file))
	fmt.Fprintf(b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		fmt.Fprintf(b, "+%s\n", line)
	}
	if noNewline {
		b.WriteString("\\ No newline at end of file\n")
	}
}

func gitDiffPath(prefix, file string) string {
	path := prefix + filepathSlash(file)
	if !needsGitPathQuoting(path) {
		return path
	}
	return strconv.Quote(path)
}

func needsGitPathQuoting(path string) bool {
	for _, r := range path {
		if r <= ' ' || r == '"' || r == '\\' || r == 0x7f {
			return true
		}
	}
	return false
}

// incompleteAnalysisReasons are sandbox skip reasons that indicate core
// checks could not run, making the overall analysis incomplete.
var incompleteAnalysisReasons = map[string]bool{
	"dependency_unavailable":   true,
	"snapshot_budget_exceeded": true,
	"e2b_egress_not_enforced":  true,
}

// coreCheckCommands are the sandbox commands whose absence represents
// incomplete code-quality analysis (as opposed to optional tools like
// staticcheck).
var coreCheckCommands = map[string]bool{
	"go": true,
}

func sandboxReviewItems(runs []SandboxRun, pd ParsedDiff, parsed []Finding) []Finding {
	var out []Finding
	parsedByCommand := map[string]bool{}
	for _, f := range parsed {
		parsedByCommand[strings.TrimPrefix(f.Source, "sandbox:")] = true
	}
	_, unanchored := splitSandboxDiagnostics(runs, pd)
	for _, f := range unanchored {
		parsedByCommand[strings.TrimPrefix(f.Source, "sandbox:")] = true
		out = append(out, f)
	}
	for _, run := range runs {
		if run.Status == "success" {
			continue
		}
		if run.Status == "skipped" {
			// Distinguish unavailable core checks from optional tool skips.
			// When go test or go vet cannot run due to a dependency or
			// infrastructure constraint, the review is structurally incomplete
			// and callers must be told.
			if coreCheckCommands[run.Command] && incompleteAnalysisReasons[run.ErrorType] {
				out = append(out, Finding{
					Severity:       SeverityMedium,
					Category:       "incomplete_analysis",
					File:           "",
					Line:           0,
					Title:          "Core sandbox check unavailable",
					Evidence:       strings.TrimSpace(run.Command + " " + strings.Join(run.Args, " ") + ": " + run.ErrorType + " " + run.Stderr),
					Recommendation: "Address the dependency or infrastructure constraint so repository checks can run, or perform a manual review of the untested changes.",
					Confidence:     0.9,
					Source:         "sandbox",
					RuleID:         "sandbox/core-check-unavailable",
				})
			}
			continue
		}
		if parsedByCommand[sandboxRunKey(run)] {
			continue
		}
		out = append(out, Finding{
			Severity:       SeverityMedium,
			Category:       "sandbox",
			File:           "",
			Line:           0,
			Title:          "Sandbox check did not complete cleanly",
			Evidence:       strings.TrimSpace(run.Command + " " + strings.Join(run.Args, " ") + ": " + run.ErrorType + " " + run.Stderr),
			Recommendation: "Inspect the sandbox run output and rerun the check after fixing environment or test failures.",
			Confidence:     0.66,
			Source:         "sandbox",
			RuleID:         "sandbox/check-failed",
		})
	}
	return DedupeFindings(out)
}

func buildMetrics(report ReviewReport, total time.Duration) AuditMetrics {
	m := AuditMetrics{
		TotalDurationMS: total.Milliseconds(),
		SeverityCounts:  map[string]int{},
		ErrorTypeCounts: map[string]int{},
	}
	all := append([]Finding{}, report.Findings...)
	all = append(all, report.Warnings...)
	all = append(all, report.NeedsHumanReview...)
	m.FindingCount = len(report.Findings)
	m.WarningCount = len(report.Warnings)
	m.NeedsHumanReviewCount = len(report.NeedsHumanReview)
	for _, f := range all {
		m.SeverityCounts[string(f.Severity)]++
	}
	for _, run := range report.SandboxRuns {
		if run.Status != "skipped" && run.ErrorType != "permission_decision" {
			m.ToolCallCount++
		}
		m.SandboxDurationMS += run.DurationMS
		if run.ErrorType != "" {
			m.ErrorTypeCounts[run.ErrorType]++
		}
	}
	for _, d := range report.Permissions {
		switch d.Action {
		case "deny":
			m.PermissionDenyCount++
		case "ask":
			m.PermissionAskCount++
		}
	}
	return m
}

func buildPermissionSummary(decisions []PermissionDecisionRecord) PermissionSummary {
	var summary PermissionSummary
	for _, d := range decisions {
		disposition := firstNonEmpty(d.Disposition, permissionDisposition(d.Action))
		switch disposition {
		case "allow":
			summary.AllowCount++
		case "deny":
			summary.DenyCount++
		case "needs_human_review":
			summary.NeedsHumanReviewCount++
		}
		if d.Action == "ask" {
			summary.AskCount++
		}
	}
	return summary
}

func buildConclusion(findings, needsHuman []Finding) string {
	if len(findings) == 0 && len(needsHuman) == 0 {
		return "No high-confidence code review issues were detected. Review sandbox warnings before merging if any checks were skipped or unavailable."
	}
	if hasCritical(findings) {
		return "Critical security findings were detected. Do not merge until the listed secret or credential issues are remediated and rotated."
	}
	if len(findings) > 0 {
		return "Actionable findings were detected. Address high and medium severity items before merge, then rerun the review."
	}
	return "Only human-review or warning items were detected. A maintainer should confirm the risk before merge."
}

func hasCritical(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityCritical {
			return true
		}
	}
	return false
}

func reportFileArtifacts(taskID, jsonPath, mdPath string) []ArtifactRecord {
	return reportFileArtifactsAt(taskID, jsonPath, mdPath, time.Now())
}

func reportFileArtifactsAt(taskID, jsonPath, mdPath string, createdAt time.Time) []ArtifactRecord {
	outputDir := filepath.Dir(jsonPath)
	paths := []struct{ name, path, mime string }{
		{"review_report.json", jsonPath, "application/json"},
		{"review_report.md", mdPath, "text/markdown"},
		{"review_diagnostics.json", filepath.Join(outputDir, "review_diagnostics.json"), "application/json"},
		{"review_report.zh.md", filepath.Join(outputDir, "review_report.zh.md"), "text/markdown"},
	}
	var out []ArtifactRecord
	for _, p := range paths {
		st, err := os.Stat(p.path)
		if err != nil {
			continue
		}
		out = append(out, ArtifactRecord{
			ID:        newID("artifact"),
			TaskID:    taskID,
			Name:      p.name,
			Path:      p.path,
			MimeType:  p.mime,
			SizeBytes: st.Size(),
			CreatedAt: createdAt,
		})
	}
	return out
}

func finalizeReportArtifacts(outputDir string, report *ReviewReport, sandboxArtifacts []ArtifactRecord) (string, string, error) {
	createdAt := report.Task.EndedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	var jsonPath string
	var mdPath string
	for i := 0; i < 8; i++ {
		nextJSONPath, nextMDPath, err := WriteReports(outputDir, *report)
		if err != nil {
			return "", "", err
		}
		jsonPath, mdPath = nextJSONPath, nextMDPath
		nextArtifacts, nextPolicy := reportArtifacts(
			report.Task.ID,
			append(append([]ArtifactRecord{}, sandboxArtifacts...), reportFileArtifactsAt(report.Task.ID, jsonPath, mdPath, createdAt)...),
		)
		if reportArtifactsEqual(report.Artifacts, nextArtifacts) && artifactPoliciesEqual(report.ArtifactPolicy, nextPolicy) {
			return jsonPath, mdPath, nil
		}
		report.Artifacts = nextArtifacts
		report.ArtifactPolicy = nextPolicy
	}
	jsonPath, mdPath, err := WriteReports(outputDir, *report)
	if err != nil {
		return "", "", err
	}
	finalArtifacts, finalPolicy := reportArtifacts(
		report.Task.ID,
		append(append([]ArtifactRecord{}, sandboxArtifacts...), reportFileArtifactsAt(report.Task.ID, jsonPath, mdPath, createdAt)...),
	)
	report.Artifacts = finalArtifacts
	report.ArtifactPolicy = finalPolicy
	return WriteReports(outputDir, *report)
}

func reportArtifactsEqual(a, b []ArtifactRecord) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].TaskID != b[i].TaskID ||
			a[i].Name != b[i].Name ||
			a[i].Path != b[i].Path ||
			a[i].MimeType != b[i].MimeType ||
			a[i].SizeBytes != b[i].SizeBytes {
			return false
		}
	}
	return true
}

func artifactPoliciesEqual(a, b ArtifactPolicy) bool {
	return a.MaxArtifacts == b.MaxArtifacts &&
		a.MaxBytesPerFile == b.MaxBytesPerFile &&
		a.RetainedCount == b.RetainedCount &&
		a.RejectedCount == b.RejectedCount &&
		strings.Join(a.AllowedFileNames, "\x00") == strings.Join(b.AllowedFileNames, "\x00")
}

func reportArtifacts(taskID string, candidates []ArtifactRecord) ([]ArtifactRecord, ArtifactPolicy) {
	policy := defaultArtifactPolicy()
	allowed := map[string]bool{}
	for _, name := range policy.AllowedFileNames {
		allowed[name] = true
	}
	var kept []ArtifactRecord
	for _, artifact := range candidates {
		if len(kept) >= policy.MaxArtifacts || !allowed[artifact.Name] || artifact.SizeBytes > policy.MaxBytesPerFile {
			policy.RejectedCount++
			continue
		}
		artifact.TaskID = taskID
		kept = append(kept, artifact)
	}
	policy.RetainedCount = len(kept)
	return kept, policy
}

func defaultArtifactPolicy() ArtifactPolicy {
	return ArtifactPolicy{
		MaxArtifacts:     5,
		MaxBytesPerFile:  1 << 20,
		AllowedFileNames: []string{"review_report.json", "review_report.md", "review_diagnostics.json", "review_report.zh.md", "diff_summary.json"},
	}
}

func redactFindingSlice(in []Finding) []Finding {
	out := append([]Finding(nil), in...)
	for i := range out {
		out[i].Evidence = redactSecrets(out[i].Evidence)
		out[i].Recommendation = redactSecrets(out[i].Recommendation)
	}
	return out
}
