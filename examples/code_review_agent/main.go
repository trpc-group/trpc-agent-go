//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main provides the CLI entry point for the code review agent.
package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/diff"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/finding"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/report"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/runner"
	"trpc.group/trpc-go/trpc-agent-go/examples/code_review_agent/internal/runner/rules"
)

func main() {
	diffFile := flag.String("diff-file", "", "path to a unified diff file")
	repoPath := flag.String("repo-path", "", "path to a git repository")
	dryRun := flag.Bool("dry-run", false, "dry-run mode (deterministic rules only, no sandbox)")
	outputDir := flag.String("output", "./review_output", "output directory for reports")
	verbose := flag.Bool("verbose", false, "enable verbose logging")
	flag.Parse()

	if *diffFile == "" && *repoPath == "" {
		fmt.Fprintln(os.Stderr, "Error: either --diff-file or --repo-path is required")
		fmt.Fprintln(os.Stderr, "Usage: code-review-agent --diff-file <path> [--dry-run] [--output <dir>]")
		flag.PrintDefaults()
		os.Exit(1)
	}

	if *verbose {
		fmt.Printf("Code Review Agent\n  diff-file: %s\n  repo-path: %s\n  dry-run: %v\n  output: %s\n",
			*diffFile, *repoPath, *dryRun, *outputDir)
	}

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	if *diffFile != "" {
		if err := runWithDiffFile(*diffFile, *outputDir, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
	if *repoPath != "" {
		if err := runWithRepoPath(*repoPath, *outputDir, *dryRun); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
	}
}

func runWithDiffFile(path, outputDir string, dryRun bool) error {
	content, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read diff file: %w", err)
	}
	fmt.Printf("Loaded diff: %s (%d bytes)\n", path, len(content))
	return runReview(string(content), "diff_file:"+path, outputDir, dryRun)
}

func runWithRepoPath(repoPath, outputDir string, dryRun bool) error {
	info, err := os.Stat(repoPath)
	if err != nil {
		return fmt.Errorf("access repo path: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("repo path must be a directory: %s", repoPath)
	}

	cmd := exec.Command("git", "diff", "HEAD")
	cmd.Dir = repoPath
	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("git diff failed (is this a git repo?): %w", err)
	}

	diffContent := string(output)
	if diffContent == "" {
		// No staged changes, try working-tree diff against HEAD.
		cmd2 := exec.Command("git", "diff", "HEAD")
		cmd2.Dir = repoPath
		output2, err2 := cmd2.Output()
		if err2 != nil {
			return fmt.Errorf("git diff HEAD failed: %w", err2)
		}
		diffContent = string(output2)
	}

	if diffContent == "" {
		return fmt.Errorf("no changes found in repository: %s", repoPath)
	}

	fmt.Printf("Loaded git repo: %s (%d bytes diff)\n", repoPath, len(diffContent))
	return runReview(diffContent, "repo:"+repoPath, outputDir, dryRun)
}

// runReview is the shared review pipeline used by both --diff-file and --repo-path.
func runReview(diffContent, taskID, outputDir string, dryRun bool) error {
	_ = dryRun

	// Parse diff.
	changedFiles, err := diff.ParseUnifiedDiff(diffContent)
	if err != nil {
		return fmt.Errorf("parse diff: %w", err)
	}
	fileInfos := diff.ExtractFileInfo(changedFiles)
	summary := diff.DiffSummary(changedFiles)
	fmt.Printf("Parsed: %s\n", summary)

	// Run rules directly.
	var allFindings []finding.Finding
	for _, rule := range allRules() {
		for _, fi := range diff.NonTestFiles(diff.GoFileFilter(fileInfos)) {
			findings, checkErr := rule.Check(nil, fi, "")
			if checkErr != nil {
				continue
			}
			allFindings = append(allFindings, findings...)
		}
	}

	// Sanitize, dedup, sort.
	sanitizer := finding.NewSanitizer()
	for i := range allFindings {
		allFindings[i] = sanitizer.SanitizeFinding(allFindings[i])
	}
	dedup := finding.NewDedupEngine()
	result := dedup.Dedup(allFindings)
	report.SortFindings(result.Findings)

	// Build and save report.
	riskSummary := report.BuildRiskSummary(result.Findings, result.Warnings)
	monitoring := report.BuildMonitoringSummary(0, 0, result.Findings, result.Warnings, 0, 0, 0)
	reviewReport := &report.ReviewReport{
		TaskID:      taskID,
		DiffSummary: summary,
		Findings:    result.Findings,
		Warnings:    result.Warnings,
		RiskSummary: riskSummary,
		Monitoring:  monitoring,
		GeneratedAt: time.Now(),
	}

	if err := saveReports(reviewReport, outputDir); err != nil {
		return err
	}

	fmt.Printf("Review complete: %d findings, %d warnings\n", len(result.Findings), len(result.Warnings))
	return nil
}

func saveReports(r *report.ReviewReport, dir string) error {
	jsonData, err := report.ToJSON(*r)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "review_report.json"), []byte(jsonData), 0600); err != nil {
		return err
	}

	mdData := report.ToMarkdown(*r)
	if err := os.WriteFile(filepath.Join(dir, "review_report.md"), []byte(mdData), 0600); err != nil {
		return err
	}

	fmt.Printf("Reports saved to %s/\n", dir)
	return nil
}

func allRules() []runner.CRRule {
	return []runner.CRRule{
		rules.NewSecurityRule(),
		rules.NewHardcodedKeyRule(),
		rules.NewGoroutineLeakRule(),
		rules.NewResourceLeakRule(),
		rules.NewErrorHandlingRule(),
		rules.NewErrorNoReturnRule(),
		rules.NewTestMissingRule(),
		rules.NewTestFileMissingRule(),
		rules.NewDBLifecycleRule(),
		rules.NewDBRowsErrCheckRule(),
	}
}
