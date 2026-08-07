//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finding

import "fmt"

// DedupKey identifies a unique finding for deduplication.
type DedupKey struct {
	File   string
	Line   int
	RuleID string
	Title  string
}

// DedupEngine removes duplicate findings based on configurable rules.
type DedupEngine struct {
	SameLineSameRule bool
	SameFileSameMsg  bool
	MaxWarnings      int
}

// NewDedupEngine creates a default dedup engine.
func NewDedupEngine() *DedupEngine {
	return &DedupEngine{
		SameLineSameRule: true,
		SameFileSameMsg:  true,
		MaxWarnings:      20,
	}
}

// DedupResult holds the deduplication result.
type DedupResult struct {
	Findings   []Finding
	Warnings   []Finding
	Suppressed int
}

// Dedup processes a list of findings, removing duplicates and separating warnings.
func (e *DedupEngine) Dedup(findings []Finding) DedupResult {
	seen := make(map[string]bool)
	var result Findings
	var warnings []Finding
	suppressed := 0

	for _, f := range findings {
		// Low confidence findings go to warnings.
		if f.Confidence == ConfidenceLow {
			warnings = append(warnings, f)
			continue
		}

		// Generate dedup key.
		key := e.dedupKey(f)

		if seen[key] {
			f.IsDuplicate = true
			suppressed++
			continue
		}
		seen[key] = true
		result = append(result, f)
	}

	// Limit warnings count.
	if len(warnings) > e.MaxWarnings {
		warnings = warnings[:e.MaxWarnings]
	}

	return DedupResult{
		Findings:   result,
		Warnings:   warnings,
		Suppressed: suppressed,
	}
}

func (e *DedupEngine) dedupKey(f Finding) string {
	file := f.File
	line := f.Line
	ruleID := f.RuleID

	if e.SameLineSameRule {
		// Primary: dedup by file, line, and rule.
		if e.SameFileSameMsg {
			return fmt.Sprintf("%s:%d:%s", file, line, ruleID)
		}
		return fmt.Sprintf("%s:%d:%s", file, line, ruleID)
	}
	if e.SameFileSameMsg {
		// Secondary: dedup by file and title (message).
		return fmt.Sprintf("%s:%s", file, f.Title)
	}
	// Fallback: exact match.
	return fmt.Sprintf("%s:%d:%s:%s", file, line, ruleID, f.Title)
}

// Findings is a sortable slice of Finding for consistent ordering.
type Findings []Finding

func (f Findings) Len() int           { return len(f) }
func (f Findings) Less(i, j int) bool { return f[i].Line < f[j].Line }
func (f Findings) Swap(i, j int)      { f[i], f[j] = f[j], f[i] }
