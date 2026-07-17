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
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	atrace "trpc.group/trpc-go/trpc-agent-go/telemetry/trace"
)

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
	var cleanupSmokeRepo func() error
	if cfg.ContainerSmoke {
		repoPath, cleanup, err := prepareContainerSmokeRepo(ctx)
		if err != nil {
			return ReviewReport{}, "", "", err
		}
		cfg.RepoPath = repoPath
		cfg.Executor = "container"
		cfg.InstallStaticcheck = true
		cleanupSmokeRepo = cleanup
		defer cleanupSmokeRepo()
	}
	if err := loadCodeReviewSkill(); err != nil {
		return ReviewReport{}, "", "", err
	}
	diff, inputMode, err := loadInputDiff(ctx, cfg)
	if err != nil {
		return ReviewReport{}, "", "", err
	}
	pd, err := ParseUnifiedDiff(diff)
	if err != nil {
		return ReviewReport{}, "", "", err
	}
	if cfg.RepoPath != "" {
		enrichPackageInfoFromRepo(&pd, cfg.RepoPath)
	}
	task := ReviewTask{
		ID:        newID("review"),
		Status:    StatusCompleted,
		StartedAt: start,
		InputMode: inputMode,
	}
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
	needsHuman = append(needsHuman, sandboxReviewItems(sandboxResult.Runs, sandboxResult.Findings)...)
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

	if err := os.MkdirAll(cfg.OutputDir, 0o755); err != nil {
		return ReviewReport{}, "", "", err
	}
	jsonPath, mdPath, err := WriteReports(cfg.OutputDir, report)
	if err != nil {
		return ReviewReport{}, "", "", err
	}
	report.Artifacts, report.ArtifactPolicy = reportArtifacts(task.ID, append(sandboxResult.Artifacts, reportFileArtifacts(task.ID, jsonPath, mdPath)...))
	jsonPath, mdPath, err = WriteReports(cfg.OutputDir, report)
	if err != nil {
		return ReviewReport{}, "", "", err
	}
	store, err := OpenStore(ctx, cfg.DBPath)
	if err != nil {
		return ReviewReport{}, "", "", err
	}
	defer store.Close()
	if err := wrapStoreErr("save review", store.SaveReport(ctx, report, pd, jsonPath, mdPath)); err != nil {
		return ReviewReport{}, "", "", err
	}
	return report, jsonPath, mdPath, nil
}

func enrichPackageInfoFromRepo(pd *ParsedDiff, repoPath string) {
	for i := range pd.Files {
		file := &pd.Files[i]
		if !file.IsGo || file.PackageName != "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(repoPath, filepath.FromSlash(file.NewPath)))
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(data), "\n") {
			if m := packageDeclRE.FindStringSubmatch(line); m != nil {
				file.PackageName = m[1]
				break
			}
		}
	}
	attachPackageInfoFromFiles(pd)
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
		data, err := os.ReadFile(cfg.DiffFile)
		if err != nil {
			return "", "", err
		}
		return string(data), "diff-file", nil
	case cfg.FileList != "":
		diff, err := fileListSyntheticDiff(cfg.FileList)
		if err != nil {
			return "", "", err
		}
		return diff, "file-list", nil
	case cfg.Fixture != "":
		path := filepath.Join(exampleDir(), "fixtures", cfg.Fixture+".diff")
		data, err := os.ReadFile(path)
		if err != nil {
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
	var chunks []string
	out, err := gitOutput(ctx, repoPath, "diff", "--no-ext-diff", "--no-color", "--unified=3")
	if err == nil && len(out) > 0 {
		chunks = append(chunks, string(out))
	}
	cached, cachedErr := gitOutput(ctx, repoPath, "diff", "--cached", "--no-ext-diff", "--no-color", "--unified=3")
	if cachedErr == nil && len(cached) > 0 {
		chunks = append(chunks, string(cached))
	}
	untracked, untrackedErr := gitOutput(ctx, repoPath, "ls-files", "--others", "--exclude-standard", "-z")
	if untrackedErr == nil && len(untracked) > 0 {
		untrackedDiff, err := untrackedFileDiffs(repoPath, untracked)
		if err != nil {
			return "", err
		}
		if untrackedDiff != "" {
			chunks = append(chunks, untrackedDiff)
		}
	}
	if len(chunks) > 0 {
		return strings.Join(chunks, "\n"), nil
	}
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	if cachedErr != nil {
		return "", fmt.Errorf("git diff --cached: %w", cachedErr)
	}
	if untrackedErr != nil {
		return "", fmt.Errorf("git ls-files: %w", untrackedErr)
	}
	return "", nil
}

func gitOutput(ctx context.Context, repoPath string, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-C", repoPath}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil && strings.TrimSpace(stderr.String()) != "" {
		return nil, fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return out, err
}

func fileListSyntheticDiff(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file list: %w", err)
	}
	var files []string
	for _, line := range strings.Split(string(data), "\n") {
		file := filepath.ToSlash(strings.TrimSpace(line))
		if file == "" || strings.HasPrefix(file, "#") {
			continue
		}
		files = append(files, file)
	}
	sort.Strings(files)
	var b strings.Builder
	for _, file := range files {
		writeSyntheticFileDiff(&b, file)
	}
	return b.String(), nil
}

func untrackedFileDiffs(repoPath string, raw []byte) (string, error) {
	parts := bytes.Split(raw, []byte{0})
	var files []string
	for _, part := range parts {
		file := filepath.ToSlash(strings.TrimSpace(string(part)))
		if file != "" {
			files = append(files, file)
		}
	}
	sort.Strings(files)
	var b strings.Builder
	for _, file := range files {
		abs := filepath.Join(repoPath, filepath.FromSlash(file))
		info, err := os.Lstat(abs)
		if err != nil {
			return "", fmt.Errorf("stat untracked file %s: %w", file, err)
		}
		if info.IsDir() {
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(abs)
			if err != nil {
				return "", fmt.Errorf("read untracked symlink %s: %w", file, err)
			}
			writeNewFileDiff(&b, file, []string{filepath.ToSlash(target)}, false)
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			return "", fmt.Errorf("read untracked file %s: %w", file, err)
		}
		if bytes.Contains(data, []byte{0}) {
			fmt.Fprintf(&b, "diff --git a/%s b/%s\nnew file mode 100644\nBinary files /dev/null and b/%s differ\n", file, file, file)
			continue
		}
		text := string(data)
		noNewline := text != "" && !strings.HasSuffix(text, "\n")
		text = strings.TrimSuffix(text, "\n")
		lines := []string{}
		if text != "" {
			lines = strings.Split(text, "\n")
		}
		writeNewFileDiff(&b, file, lines, noNewline)
	}
	return b.String(), nil
}

func writeSyntheticFileDiff(b *strings.Builder, file string) {
	fmt.Fprintf(b, "diff --git a/%s b/%s\n", file, file)
	fmt.Fprintf(b, "--- a/%s\n", file)
	fmt.Fprintf(b, "+++ b/%s\n", file)
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
	fmt.Fprintf(b, "diff --git a/%s b/%s\n", file, file)
	fmt.Fprintf(b, "new file mode 100644\n")
	fmt.Fprintf(b, "--- /dev/null\n")
	fmt.Fprintf(b, "+++ b/%s\n", file)
	fmt.Fprintf(b, "@@ -0,0 +1,%d @@\n", len(lines))
	for _, line := range lines {
		fmt.Fprintf(b, "+%s\n", line)
	}
	if noNewline {
		b.WriteString("\\ No newline at end of file\n")
	}
}

func sandboxReviewItems(runs []SandboxRun, parsed []Finding) []Finding {
	var out []Finding
	parsedByCommand := map[string]bool{}
	for _, f := range parsed {
		parsedByCommand[strings.TrimPrefix(f.Source, "sandbox:")] = true
	}
	for _, run := range runs {
		if run.Status == "success" || run.Status == "skipped" {
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
	return out
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
			CreatedAt: time.Now(),
		})
	}
	return out
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
