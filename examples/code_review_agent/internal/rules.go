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
	"strings"
)

// Rule defines a code review rule that inspects diff lines.
type Rule interface {
	ID() string
	Category() string
	Description() string
	// Check inspects a single added line within a file's hunk context.
	// It may return zero or more findings.
	Check(file DiffFile, hunk DiffHunk, line DiffLine) []Finding
}

// RuleEngine holds a collection of rules and runs them against parsed
// diffs.
type RuleEngine struct {
	rules []Rule
}

// NewRuleEngine creates a RuleEngine with the given rules.
func NewRuleEngine(rules ...Rule) *RuleEngine {
	return &RuleEngine{rules: rules}
}

// DefaultRuleEngine creates a RuleEngine with all built-in rules.
func DefaultRuleEngine() *RuleEngine {
	return NewRuleEngine(
		// Security
		&SQLInjectionRule{},
		&CommandInjectionRule{},
		&HardcodedSecretRule{},
		// Goroutine / context
		&GoroutineLeakRule{},
		&ContextNotPassedRule{},
		// Resource leak
		&UnclosedResourceRule{},
		&HTTPBodyNotClosedRule{},
		// Error handling
		&IgnoredErrorRule{},
		&PanicInGoroutineRule{},
		// DB lifecycle
		&DBConnectionLeakRule{},
		&MissingTransactionRollbackRule{},
		// Sensitive info
		&SensitiveInfoInLogRule{},
		&TestMissingRule{},
	)
}

// Run executes all rules against the parsed diff files and returns the
// collected findings (before dedup).
func (e *RuleEngine) Run(files []DiffFile) []Finding {
	var findings []Finding
	for _, file := range files {
		if file.IsDeleted {
			continue
		}
		// Only check .go files for Go-specific rules.
		isGo := strings.HasSuffix(file.Path, ".go")
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Type != LineAdded {
					continue
				}
				for _, rule := range e.rules {
					// Skip test-missing rule for non-go files.
					if !isGo && rule.ID() != "TEST_MISSING" {
						continue
					}
					if !isGo {
						continue
					}
					finds := rule.Check(file, hunk, line)
					for i := range finds {
						if finds[i].File == "" {
							finds[i].File = file.Path
						}
						if finds[i].Line == 0 {
							finds[i].Line = line.Number
						}
						if finds[i].Source == "" {
							finds[i].Source = SourceRule
						}
						if finds[i].RuleID == "" {
							finds[i].RuleID = rule.ID()
						}
						if finds[i].Category == "" {
							finds[i].Category = rule.Category()
						}
						// Redact sensitive info in evidence.
						finds[i].Evidence = RedactSensitiveInfo(finds[i].Evidence)
						findings = append(findings, finds[i])
					}
				}
			}
		}
		// Run test-missing check at file level.
		if isGo {
			if tr, ok := (findTestMissingRule(e.rules)); ok {
				finds := tr.CheckFile(file)
				for i := range finds {
					if finds[i].File == "" {
						finds[i].File = file.Path
					}
					if finds[i].Source == "" {
						finds[i].Source = SourceRule
					}
					if finds[i].RuleID == "" {
						finds[i].RuleID = tr.ID()
					}
					if finds[i].Category == "" {
						finds[i].Category = tr.Category()
					}
					findings = append(findings, finds[i])
				}
			}
		}
	}
	return findings
}

// FileLevelRule extends Rule with a whole-file check.
type FileLevelRule interface {
	Rule
	CheckFile(file DiffFile) []Finding
}

func findTestMissingRule(rules []Rule) (FileLevelRule, bool) {
	for _, r := range rules {
		if fl, ok := r.(FileLevelRule); ok && r.ID() == "TEST_MISSING" {
			return fl, true
		}
	}
	return nil, false
}
