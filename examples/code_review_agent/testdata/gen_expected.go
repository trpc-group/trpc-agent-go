//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command gen_expected regenerates the curated expected outputs from an
// actual --fixture all run.
//
// Usage: go run gen_expected.go <run_dir> <expected_dir>
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// finding mirrors the review.Finding JSON shape.
type finding struct {
	Severity       string  `json:"severity"`
	Category       string  `json:"category"`
	File           string  `json:"file"`
	Line           int     `json:"line"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	Recommendation string  `json:"recommendation"`
	Confidence     float64 `json:"confidence"`
	Source         string  `json:"source"`
	RuleID         string  `json:"rule_id"`
}

// report mirrors the subset of review.ReviewReport used here.
type report struct {
	Summary          string    `json:"summary"`
	Findings         []finding `json:"findings"`
	NeedsHumanReview []finding `json:"needs_human_review"`
}

// curatedHuman is the reduced shape stored for human-review items.
type curatedHuman struct {
	Severity string `json:"severity"`
	Category string `json:"category"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Title    string `json:"title"`
	RuleID   string `json:"rule_id"`
}

// curated is the expected-output JSON document.
type curated struct {
	Summary          string         `json:"summary"`
	Findings         []finding      `json:"findings,omitempty"`
	NeedsHumanReview []curatedHuman `json:"needs_human_review,omitempty"`
}

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: go run gen_expected.go <run_dir> <expected_dir>")
		os.Exit(1)
	}
	if err := run(os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(runDir, expectedDir string) error {
	if err := os.MkdirAll(expectedDir, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(runDir)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		path := filepath.Join(runDir, name, "review_report.json")
		data, err := os.ReadFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return err
		}
		var r report
		if err := json.Unmarshal(data, &r); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		jsonPath := filepath.Join(expectedDir, name+"_review_report.json")
		if err := writeJSON(jsonPath, curate(r)); err != nil {
			return err
		}
		mdPath := filepath.Join(expectedDir, name+"_review_report.md")
		if err := os.WriteFile(mdPath, []byte(markdown(r)), 0o644); err != nil {
			return err
		}
		fmt.Println("wrote", jsonPath, "and", mdPath)
	}
	return nil
}

func curate(r report) curated {
	out := curated{Summary: r.Summary, Findings: r.Findings}
	for _, f := range r.NeedsHumanReview {
		out.NeedsHumanReview = append(out.NeedsHumanReview, curatedHuman{
			Severity: f.Severity,
			Category: f.Category,
			File:     f.File,
			Line:     f.Line,
			Title:    f.Title,
			RuleID:   f.RuleID,
		})
	}
	return out
}

func writeJSON(path string, doc curated) error {
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func markdown(r report) string {
	var b strings.Builder
	b.WriteString("# Code Review Report\n\n")
	fmt.Fprintf(&b, "- Summary: %s\n", r.Summary)
	fmt.Fprintf(&b, "- Findings: %d\n", len(r.Findings))
	fmt.Fprintf(&b, "- Needs human review: %d\n\n", len(r.NeedsHumanReview))
	b.WriteString("## Findings\n\n")
	if len(r.Findings) == 0 {
		b.WriteString("None.\n\n")
	}
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "### [%s] %s\n\n", f.Severity, f.Title)
		fmt.Fprintf(&b, "- File: `%s:%d`\n", f.File, f.Line)
		fmt.Fprintf(&b, "- Rule: `%s`\n", f.RuleID)
		fmt.Fprintf(&b, "- Evidence: `%s`\n", f.Evidence)
		fmt.Fprintf(&b, "- Recommendation: %s\n\n", f.Recommendation)
	}
	b.WriteString("## Needs Human Review\n\n")
	if len(r.NeedsHumanReview) == 0 {
		b.WriteString("None.\n\n")
	}
	for _, f := range r.NeedsHumanReview {
		fmt.Fprintf(&b, "### [%s] %s\n\n", f.Severity, f.Title)
		fmt.Fprintf(&b, "- File: `%s:%d`\n", f.File, f.Line)
		fmt.Fprintf(&b, "- Rule: `%s`\n\n", f.RuleID)
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}
