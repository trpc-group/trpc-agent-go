// Package ruleengine implements the RuleEngine GraphAgent node.
// Applies deterministic rules (TokenRule + ToolRule) to file changes and sandbox output.
//
// ## Limitations (Issue #2004 — CodeRabbit review)
//
//  1. TokenRule matching is scoped to added (+) lines only, except for
//     resource-leak categories (goroutine_leak, resource_leak, db_lifecycle,
//     error_handling) which also scan removed (-) lines to detect deleted
//     cleanup calls (defer Close, cancel, rows.Close, tx.Rollback, etc.).
//  2. Regex matching operates on single lines — multi-line patterns
//     (e.g., a function call spanning multiple lines) will be missed.
//  3. The isFalsePositiveLine pre-filter skips comments, package/import
//     declarations, and pure string literals, which may cause false negatives
//     in unusual code styles.
//  4. Rules are loaded from Markdown files with a fixed format (see
//     skills/code-review/rules/). Rule evolution requires format versioning.
package ruleengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/state"
	"github.com/trpc-group/trpc-agent-go/examples/code_review_agent/internal/types"
	"trpc.group/trpc-go/trpc-agent-go/graph"
)

// Run is the RuleEngine GraphAgent node.
// Reads file_changes, sandbox_results, and skill_rules from state,
// writes rule_findings.
func Run(ctx context.Context, gs graph.State) (any, error) {
	start := time.Now()
	defer func() { gs[state.StateKeyNodeRuleEngineMs] = time.Since(start).Milliseconds() }()

	rules, _ := gs[state.StateKeySkillRules].([]types.Rule)
	changes, _ := gs[state.StateKeyFileChanges].([]types.FileChange)
	results, _ := gs[state.StateKeySandboxResults].([]types.SandboxResult)
	taskID, _ := gs[state.StateKeyTaskID].(string)

	var findings []types.Finding

	// ── Phase 1: TokenRule — regex match against added lines only ──
	// Pre-compute per-file added line counts for heuristic rules
	fileAddedLineCount := make(map[string]int)
	for _, fc := range changes {
		count := 0
		for _, hunk := range fc.Hunks {
			for _, l := range hunk.Lines {
				if l.Type == "+" && !isFalsePositiveLine(l.Content) {
					count++
				}
			}
		}
		fileAddedLineCount[fc.FilePath] = count
	}

	for _, rule := range rules {
		if rule.RuleType != "token" {
			continue
		}
		re, err := regexp.Compile(rule.Pattern)
		if err != nil {
			continue // skip broken patterns
		}
		for _, fc := range changes {
			for _, hunk := range fc.Hunks {
				for _, l := range hunk.Lines {
					if l.Type != "+" && !(isLeakCategory(rule.Category) && l.Type == "-") {
						continue
					}
					// Pre-filter: skip comments and pure string literals to
					// avoid false positives from harmless text.
					if isFalsePositiveLine(l.Content) {
						continue
					}
					// TEST-001 heuristic: need ≥4 added code lines in the file
					// to avoid flagging trivial one-liner wrappers (signature
					// + body + closing brace = 3 lines minimum). 4+ means the
					// function body has real logic worth testing.
					if rule.ID == "TEST-001" && fileAddedLineCount[fc.FilePath] < 4 {
						continue
					}
					// Strip leading diff prefix so ^ anchors work naturally
					codeLine := stripDiffPrefix(l.Content)
					if re.MatchString(codeLine) {
						findings = append(findings, types.Finding{
							ID:             uuid.New().String(),
							TaskID:         taskID,
							Severity:       rule.Severity,
							Category:       rule.Category,
							File:           fc.FilePath,
							Line:           l.NewLine,
							Title:          rule.Message,
							Evidence:       l.Content,
							Recommendation: rule.Fix,
							Confidence:     1.0,
							Source:         "rule_engine",
							DecisionKind:   "deterministic",
							RuleID:         rule.ID,
						})
					}
				}
			}
		}
	}

	// ── Phase 2: ToolRule — parse go_vet / staticcheck / go_build output ──
	for _, result := range results {
		findings = append(findings, parseToolOutput(taskID, result)...)
	}

	gs[state.StateKeyRuleFindings] = findings
	return gs, nil
}

func parseToolOutput(taskID string, result types.SandboxResult) []types.Finding {
	var findings []types.Finding

	if result.ErrorType != "" && result.ErrorType != "build_error" {
		// Sandbox infrastructure failure — report as a finding for visibility
		findings = append(findings, types.Finding{
			ID:           uuid.New().String(),
			TaskID:       taskID,
			Severity:     "warning",
			Category:     "other",
			File:         "",
			Line:         0,
			Title:        fmt.Sprintf("Sandbox command %s failed: %s", result.Command, result.ErrorType),
			Evidence:     result.Stderr,
			Confidence:   1.0,
			Source:       "rule_engine",
			DecisionKind: "deterministic",
			RuleID:       "TOOL-ERR",
		})
		return findings
	}

	// Parse go vet output: "<file>:<line>:<col>: <message>"
	vetRe := regexp.MustCompile(`^(.+\.go):(\d+):\d*:?\s*(.+)$`)
	for _, line := range strings.Split(result.Stderr+"\n"+result.Stdout, "\n") {
		if m := vetRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			severity := "medium"
			if strings.Contains(strings.ToLower(m[3]), "error") {
				severity = "high"
			}
			findings = append(findings, types.Finding{
				ID:           uuid.New().String(),
				TaskID:       taskID,
				Severity:     severity,
				Category:     "other",
				File:         m[1],
				Line:         parseInt(m[2]),
				Title:        m[3],
				Evidence:     line,
				Confidence:   1.0,
				Source:       result.Command,
				DecisionKind: "deterministic",
				RuleID:       "TOOL-VET",
			})
		}
	}
	return findings
}

func parseInt(s string) int {
	var n int
	fmt.Sscanf(s, "%d", &n)
	return n
}

// stripDiffPrefix removes the leading +, -, or space from a diff line.
func stripDiffPrefix(line string) string {
	if len(line) > 0 && (line[0] == '+' || line[0] == '-' || line[0] == ' ') {
		return line[1:]
	}
	return line
}

// isFalsePositiveLine checks if a code line (diff prefix already stripped) is harmless.
func isFalsePositiveLine(line string) bool {
	code := stripDiffPrefix(line)
	trimmed := strings.TrimSpace(code)

	// Skip blank lines
	if trimmed == "" {
		return true
	}
	// Skip comment lines
	if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
		return true
	}
	// Skip package and import declarations
	if strings.HasPrefix(trimmed, "package ") || strings.HasPrefix(trimmed, "import ") || strings.HasPrefix(trimmed, "import\t") {
		return true
	}
	// Skip import path lines (e.g. "github.com/foo/bar")
	if strings.HasPrefix(trimmed, "\"") && strings.Contains(trimmed, "/") {
		return true
	}
	// Skip lines that are entirely a string literal (not assignment)
	if strings.HasPrefix(trimmed, "\"") || strings.HasPrefix(trimmed, "`") {
		return true
	}
	return false
}

// ── Skill rule loading ──

// LoadRules reads rule definitions from the skills/code-review/rules/ directory.
// Each .md file is parsed for structured rule blocks.
func LoadRules(skillDir, rulesGlob string) ([]types.Rule, error) {
	pattern := filepath.Join(skillDir, rulesGlob)
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil, fmt.Errorf("glob rules %s: %w", pattern, err)
	}

	var rules []types.Rule
	for _, path := range matches {
		data, err := os.ReadFile(path)
		if err != nil {
			continue // skip unreadable rule files
		}
		rules = append(rules, parseRuleMarkdown(string(data))...)
	}
	return rules, nil
}

// parseRuleMarkdown extracts Rule structs from markdown rule files.
// Rules are expected in the format:
//
//	## RULE-ID: Title
//	- **type**: token
//	- **severity**: critical
//	- **pattern**: `regex`
//	- **message**: "description"
//	- **fix**: "suggestion"
func parseRuleMarkdown(content string) []types.Rule {
	var rules []types.Rule
	ruleHeaderRe := regexp.MustCompile(`^##\s+([A-Z]+-\d+):\s*(.+)$`)
	fieldRe := regexp.MustCompile(`^-?\s*\*\*(\w+)\*\*:\s*(.+)$`)

	var current *types.Rule
	for _, line := range strings.Split(content, "\n") {
		if m := ruleHeaderRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			if current != nil {
				rules = append(rules, *current)
			}
			current = &types.Rule{
				ID:       m[1],
				RuleType: "token",
				Category: inferCategoryFromID(m[1]),
			}
			continue
		}
		if current == nil {
			continue
		}
		if m := fieldRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			key := strings.TrimSpace(m[1])
			val := strings.TrimSpace(m[2])
			switch key {
			case "type":
				current.RuleType = val
			case "severity":
				current.Severity = val
			case "pattern":
				current.Pattern = strings.Trim(val, "`")
			case "message":
				current.Message = strings.Trim(val, "\"")
			case "fix":
				current.Fix = strings.Trim(val, "\"")
			}
		}
	}
	if current != nil {
		rules = append(rules, *current)
	}
	return rules
}

func inferCategoryFromID(id string) string {
	switch {
	case strings.HasPrefix(id, "SEC-"):
		return "security"
	case strings.HasPrefix(id, "ERR-"):
		return "error_handling"
	case strings.HasPrefix(id, "SEN-"):
		return "sensitive_info"
	case strings.HasPrefix(id, "DB-"):
		return "db_lifecycle"
	case strings.HasPrefix(id, "TEST-"):
		return "missing_test"
	case strings.HasPrefix(id, "GOR-"):
		return "goroutine_leak"
	case strings.HasPrefix(id, "RES-"):
		return "resource_leak"
	default:
		return "other"
	}
}

// isLeakCategory returns true for rule categories that benefit from scanning
// both added (+) and removed (-) lines. Deleted defer/Close/cancel calls are
// as significant as added leaky patterns for these categories.
func isLeakCategory(category string) bool {
	switch category {
	case "goroutine_leak", "resource_leak", "db_lifecycle", "error_handling":
		return true
	default:
		return false
	}
}
