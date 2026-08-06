//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"
)

func main() {
	diffFile := flag.String("diff-file", "", "Path to the unified diff file to review")
	repoPath := flag.String("repo-path", ".", "Target repository path under review")
	skillsDir := flag.String("skills-dir", "./skills", "Directory containing agent skills")
	dbPath := flag.String("db-path", "review_agent.db", "SQLite database storage file path")
	outputJSON := flag.String("output-json", "review_report.json", "Output JSON report filepath")
	outputMD := flag.String("output-md", "review_report.md", "Output Markdown report filepath")
	useSandbox := flag.Bool("use-sandbox", true, "Enable workspace sandbox execution for vet/test checks")
	flag.Parse()

	var diffContent string
	var err error

	if *diffFile != "" {
		diffContent, err = GenerateDiffFromFixture(*diffFile)
		if err != nil {
			log.Fatalf("Failed to read diff file: %v", err)
		}
	} else {
		// Default sample diff for demonstration
		diffContent = `--- a/service.go
+++ b/service.go
@@ -1,5 +1,10 @@
 package service

+import "net/http"
+
+func ProcessData() {
+	apiKey := "sk-proj-1234567890secretkeyvalue"
+	resp, err := http.Get("http://example.com")
+	go func() {
+		_ = resp
+	}()
+}
`
	}

	reviewer, err := NewCodeReviewer(CodeReviewerOptions{
		SkillsDir:  *skillsDir,
		DBPath:     *dbPath,
		UseSandbox: *useSandbox,
	})
	if err != nil {
		log.Fatalf("Failed to initialize CodeReviewer: %v", err)
	}
	defer reviewer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	input := ReviewTaskInput{
		TaskID:   fmt.Sprintf("task-%d", time.Now().UnixNano()),
		RepoPath: *repoPath,
		DiffText: diffContent,
	}

	fmt.Printf("Starting Code Review Agent Task [%s]...\n", input.TaskID)
	result, err := reviewer.ExecuteReview(ctx, input)
	if err != nil {
		log.Fatalf("Code Review task failed: %v", err)
	}

	if err := SaveReportJSON(result, *outputJSON); err != nil {
		log.Printf("Warning: failed to save JSON report: %v", err)
	}
	if err := SaveReportMarkdown(result, *outputMD); err != nil {
		log.Printf("Warning: failed to save Markdown report: %v", err)
	}

	fmt.Println("--------------------------------------------------")
	fmt.Printf("Code Review Task Completed in %d ms\n", result.DurationMs)
	fmt.Printf("Status: %s | Total Findings: %d\n", result.Status, len(result.Findings))
	fmt.Printf("High: %d | Medium: %d | Low: %d | Warnings: %d\n",
		result.Metrics.SeverityCounts["high"],
		result.Metrics.SeverityCounts["medium"],
		result.Metrics.SeverityCounts["low"],
		result.Metrics.SeverityCounts["warning"],
	)
	fmt.Printf("JSON Report saved to: %s\n", *outputJSON)
	fmt.Printf("Markdown Report saved to: %s\n", *outputMD)
	fmt.Println("--------------------------------------------------")
}
