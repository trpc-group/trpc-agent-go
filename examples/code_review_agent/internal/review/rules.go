//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type rule struct {
	id             string
	category       string
	severity       Severity
	title          string
	recommendation string
	confidence     float64
	match          func(ChangedLine, map[string]string) bool
}

var (
	sqlConcat    = regexp.MustCompile(`(?i)(query|exec|where|select|insert|update|delete)[^\n]*(?:\+|fmt\.Sprintf)`)
	ignoredError = regexp.MustCompile(`(?:^|\s)_\s*:?=\s*[A-Za-z_][A-Za-z0-9_.]*\s*\(`)
	nilOnError   = regexp.MustCompile(`if\s+err\s*!=\s*nil[^\n]*\{?\s*return\s+(?:nil|0|"")\s*(?:,\s*nil)?`)
)

func analyze(input ParsedInput) (findings, warnings, needsHuman []Finding) {
	findings, warnings, needsHuman, _ = analyzeWithDecisions(input)
	return findings, warnings, needsHuman
}

func analyzeWithDecisions(input ParsedInput) (findings, warnings, needsHuman []Finding, decisions []FilterDecision) {
	joined := map[string]string{}
	for file, context := range input.Context {
		joined[file] = context
	}
	for _, line := range input.Lines {
		if input.Context == nil {
			joined[line.File] += line.Text + "\n"
		}
	}
	rules := []rule{
		{
			id: "go/security/hardcoded-secret", category: "security", severity: SeverityCritical,
			title: "Hard-coded credential-like value", confidence: .98,
			recommendation: "Move the credential to a secret manager or an injected environment variable, then rotate the exposed value.",
			match:          func(line ChangedLine, _ map[string]string) bool { return looksSecret(line.Text) },
		},
		{
			id: "go/security/dynamic-shell", category: "security", severity: SeverityHigh,
			title: "Dynamic shell command may permit command injection", confidence: .92,
			recommendation: "Avoid shell interpretation; invoke a fixed executable with validated arguments.",
			match: func(line ChangedLine, all map[string]string) bool {
				return isDynamicShellCommand(line.Text) || strings.Contains(line.Text, "exec.Command") && isDynamicShellCommand(strings.ReplaceAll(all[line.File], "\n", " "))
			},
		},
		{
			id: "go/database/sql-concatenation", category: "database", severity: SeverityHigh,
			title: "SQL appears to be assembled dynamically", confidence: .88,
			recommendation: "Use placeholders and parameter binding instead of concatenating SQL values.",
			match:          func(line ChangedLine, _ map[string]string) bool { return sqlConcat.MatchString(line.Text) },
		},
		{
			id: "go/context/cancel-leak", category: "context", severity: SeverityHigh,
			title: "Context cancel function may not be called", confidence: .86,
			recommendation: "Call defer cancel() immediately after checking context creation errors.",
			match: func(line ChangedLine, all map[string]string) bool {
				creates := strings.Contains(line.Text, "context.WithCancel(") || strings.Contains(line.Text, "context.WithTimeout(")
				return creates && !strings.Contains(all[line.File], "cancel()")
			},
		},
		{
			id: "go/concurrency/unbounded-goroutine", category: "concurrency", severity: SeverityMedium,
			title: "Goroutine has no visible cancellation or completion path", confidence: .76,
			recommendation: "Tie the goroutine to context cancellation and a wait/error group owned by the caller.",
			match: func(line ChangedLine, all map[string]string) bool {
				return strings.Contains(line.Text, "go func(") && !strings.Contains(all[line.File], "ctx.Done()") && !strings.Contains(all[line.File], "errgroup") && !strings.Contains(all[line.File], "WaitGroup")
			},
		},
		{
			id: "go/resource/close", category: "resource", severity: SeverityHigh,
			title: "Opened resource has no visible Close", confidence: .84,
			recommendation: "Check the open/query error and defer Close as soon as ownership is acquired.",
			match: func(line ChangedLine, all map[string]string) bool {
				opens := strings.Contains(line.Text, "os.Open(") || strings.Contains(line.Text, "http.Get(") || strings.Contains(line.Text, ".Query(") || strings.Contains(line.Text, ".QueryContext(")
				return opens && !strings.Contains(all[line.File], ".Close()")
			},
		},
		{
			id: "go/database/transaction-rollback", category: "database", severity: SeverityHigh,
			title: "Transaction has no rollback guard", confidence: .90,
			recommendation: "Defer tx.Rollback after Begin succeeds; commit only after all operations succeed.",
			match: func(line ChangedLine, all map[string]string) bool {
				return (strings.Contains(line.Text, ".Begin(") || strings.Contains(line.Text, ".BeginTx(")) && !strings.Contains(all[line.File], ".Rollback()")
			},
		},
		{
			id: "go/error/ignored", category: "error_handling", severity: SeverityMedium,
			title: "Returned error appears to be ignored", confidence: .80,
			recommendation: "Handle or explicitly propagate the error with operation context.",
			match:          func(line ChangedLine, _ map[string]string) bool { return ignoredError.MatchString(line.Text) },
		},
		{
			id: "go/error/swallowed", category: "error_handling", severity: SeverityHigh,
			title: "Error path appears to return success", confidence: .83,
			recommendation: "Return or wrap the original error instead of returning a nil error.",
			match:          func(line ChangedLine, _ map[string]string) bool { return nilOnError.MatchString(line.Text) },
		},
	}
	for _, line := range input.Lines {
		if !strings.HasSuffix(line.File, ".go") {
			continue
		}
		for _, candidate := range rules {
			if !candidate.match(line, joined) {
				continue
			}
			finding := newFinding(candidate, line)
			if finding.Confidence >= .70 {
				findings = append(findings, finding)
			} else {
				warnings = append(warnings, finding)
			}
		}
	}
	if file := missingTestFile(input); file != "" {
		needsHuman = append(needsHuman, fingerprint(Finding{
			Severity: SeverityLow, Category: "test_coverage", File: file, Line: 1,
			Title:          "Production Go changes have no accompanying test change",
			Evidence:       "No changed file ends with _test.go.",
			Recommendation: "Add focused tests for changed behavior, error paths, and lifecycle cleanup.",
			Confidence:     .64, Source: "rule", RuleID: "go/test/missing-change",
		}))
	}
	decisions = append(decisions, filterDecisions(findings, "finding", FilterKeep)...)
	decisions = append(decisions, filterDecisions(warnings, "warning", FilterKeep)...)
	decisions = append(decisions, filterDecisions(needsHuman, "needs_human_review", FilterRouteHuman)...)
	findings = dedupe(findings)
	warnings = dedupe(warnings)
	needsHuman = dedupe(needsHuman)
	return findings, warnings, needsHuman, decisions
}

func isDynamicShellCommand(line string) bool {
	if !strings.Contains(line, "exec.Command") {
		return false
	}
	shell := strings.Index(line, `"sh"`)
	if bash := strings.Index(line, `"bash"`); shell < 0 || bash >= 0 && bash < shell {
		shell = bash
	}
	if shell < 0 {
		return false
	}
	cFlag := strings.Index(line[shell:], `"-c"`)
	if cFlag < 0 {
		return false
	}
	argument := strings.TrimSpace(line[shell+cFlag+len(`"-c"`):])
	if !strings.HasPrefix(argument, ",") {
		return false
	}
	argument = strings.TrimSpace(strings.TrimPrefix(argument, ","))
	if argument == "" {
		return false
	}
	if argument[0] != '"' && argument[0] != '`' {
		return true
	}
	quote := argument[0]
	for index := 1; index < len(argument); index++ {
		if argument[index] == quote && (quote == '`' || argument[index-1] != '\\') {
			remainder := strings.TrimSpace(argument[index+1:])
			return strings.HasPrefix(remainder, "+") || strings.HasPrefix(remainder, ",") && strings.Contains(remainder, "+")
		}
	}
	return true
}

func filterDecisions(values []Finding, bucket string, retainedAction FilterAction) []FilterDecision {
	best := selectBestFindings(values)
	kept := map[string]bool{}
	decisions := make([]FilterDecision, 0, len(values))
	for _, value := range values {
		key := findingLocationKey(value)
		if selected := best[key]; !kept[key] && selected.Fingerprint == value.Fingerprint {
			kept[key] = true
			reason := "candidate retained after confidence and duplicate filtering"
			if retainedAction == FilterRouteHuman {
				reason = "candidate routed to human review because automated confirmation is insufficient"
			}
			decisions = append(decisions, FilterDecision{Fingerprint: value.Fingerprint, Action: retainedAction, Reason: reason, TargetBucket: bucket})
			continue
		}
		decisions = append(decisions, FilterDecision{Fingerprint: value.Fingerprint, Action: FilterDropDuplicate, Reason: "higher-confidence or earlier equivalent candidate retained", TargetBucket: bucket})
	}
	return decisions
}

func newFinding(candidate rule, line ChangedLine) Finding {
	return fingerprint(Finding{
		Severity: candidate.severity, Category: candidate.category, File: line.File, Line: line.Line,
		Title: candidate.title, Evidence: redact(strings.TrimSpace(line.Text)),
		Recommendation: candidate.recommendation, Confidence: candidate.confidence,
		Source: "rule", RuleID: candidate.id,
	})
}

func fingerprint(finding Finding) Finding {
	value := strings.ToLower(finding.File) + "|" + strconv.Itoa(finding.Line) + "|" + finding.Category + "|" + finding.RuleID
	sum := sha256.Sum256([]byte(value))
	finding.Fingerprint = hex.EncodeToString(sum[:])
	finding.Evidence = redact(finding.Evidence)
	return finding
}

func dedupe(values []Finding) []Finding {
	best := selectBestFindings(values)
	out := make([]Finding, 0, len(best))
	for _, value := range best {
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].File != out[j].File {
			return out[i].File < out[j].File
		}
		if out[i].Line != out[j].Line {
			return out[i].Line < out[j].Line
		}
		return out[i].Category < out[j].Category
	})
	return out
}

func selectBestFindings(values []Finding) map[string]Finding {
	best := make(map[string]Finding, len(values))
	for _, value := range values {
		key := findingLocationKey(value)
		if previous, ok := best[key]; !ok || value.Confidence > previous.Confidence {
			best[key] = value
		}
	}
	return best
}

func findingLocationKey(value Finding) string {
	return strings.ToLower(value.File) + "|" + strconv.Itoa(value.Line) + "|" + value.Category
}

func missingTests(files []string) bool {
	return missingTestFile(ParsedInput{Files: files}) != ""
}

func firstProductionGo(files []string) string {
	for _, file := range files {
		if strings.HasSuffix(file, ".go") && !strings.HasSuffix(file, "_test.go") {
			return file
		}
	}
	return ""
}

func missingTestFile(input ParsedInput) string {
	testDirs := map[string]bool{}
	for _, file := range input.Files {
		if input.Statuses[file] == fileDeleted {
			continue
		}
		if strings.HasSuffix(file, "_test.go") {
			testDirs[packageFor(file)] = true
		}
	}
	for _, file := range input.Files {
		if input.Statuses[file] == fileDeleted || !strings.HasSuffix(file, ".go") || strings.HasSuffix(file, "_test.go") {
			continue
		}
		if !testDirs[packageFor(file)] {
			return file
		}
	}
	return ""
}
