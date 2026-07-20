//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"regexp"
	"strings"
	"time"
)

// ruleResource evaluates resource-abuse rules.
//
// Rule ids:
//
//   - resource.timeout_exceeded   requested timeout exceeds max_timeout.
//   - resource.long_sleep         sleep exceeds max_sleep_seconds.
//   - resource.output_bomb        unbounded output generator (yes, dd, ...).
//   - resource.output_size        declared OutputSizeHint exceeds max_output_size.
//   - resource.unbounded_loop     code block contains while(true)/for(;;).
func ruleResource(in ScanInput, a *analysis, p Policy) []Finding {
	if !p.Rules.ResourceAbuse.Enabled {
		return nil
	}
	var out []Finding

	// Timeout.
	if p.MaxTimeout > 0 && in.Timeout > p.MaxTimeout {
		out = append(out, Finding{
			RuleID:         "resource.timeout_exceeded",
			RiskLevel:      RiskMedium,
			Decision:       ruleDecision(p.Rules.ResourceAbuse.Action, RiskMedium, p),
			Evidence:       "requested timeout exceeds max_timeout",
			Recommendation: "Reduce the requested timeout or raise max_timeout in the policy",
		})
	}

	// Long sleep.
	if a.SleepSeconds >= 0 && p.MaxSleepSeconds > 0 && a.SleepSeconds > p.MaxSleepSeconds {
		out = append(out, Finding{
			RuleID:         "resource.long_sleep",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.ResourceAbuse.Action, RiskHigh, p),
			Evidence:       "sleep duration exceeds max_sleep_seconds",
			Recommendation: "Reduce the sleep duration or raise max_sleep_seconds in the policy",
		})
	}

	// Output bomb.
	if a.HasOutputBomb {
		out = append(out, Finding{
			RuleID:         "resource.output_bomb",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.ResourceAbuse.Action, RiskHigh, p),
			Evidence:       "unbounded output generator (yes/dd/seq/follow)",
			Recommendation: "Refuse unbounded output; cap with head, count, or a timeout",
		})
	}

	// Declared output size hint.
	if in.OutputSizeHint > 0 && p.MaxOutputSize > 0 && in.OutputSizeHint > p.MaxOutputSize {
		out = append(out, Finding{
			RuleID:         "resource.output_size",
			RiskLevel:      RiskMedium,
			Decision:       ruleDecision(p.Rules.ResourceAbuse.Action, RiskMedium, p),
			Evidence:       "declared output size exceeds max_output_size",
			Recommendation: "Reduce the expected output size or raise max_output_size in the policy",
		})
	}

	// Unbounded loops in code blocks.
	if len(in.CodeBlocks) > 0 {
		if hasUnboundedLoop(in.CodeBlocks) {
			out = append(out, Finding{
				RuleID:         "resource.unbounded_loop",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.ResourceAbuse.Action, RiskHigh, p),
				Evidence:       "code block contains while(true)/for(;;) without break",
				Recommendation: "Bound the loop explicitly or refuse the code block",
			})
		}
	}

	return out
}

// loopRegex matches common unbounded loop shapes across python, bash,
// javascript, go, rust, and ruby. The patterns cover:
//   - Python: while True:, while 1:, while true:
//   - Go: for {}, for ; ;, for ;;
//   - JavaScript: while (true), while (1), for (;;)
//   - Rust: loop {}, while true {}
//   - Ruby: loop do, while true
//   - Bash: while true; do, while :; do
var loopRegex = regexp.MustCompile(`(?m)(?:while\s*\(\s*(?:true|1|True|TRUE)\s*\)|while\s+(?:true|True|TRUE|1)\s*:|while\s+true\b|while\s+\[\s*\]|for\s*\(\s*;\s*;\s*\)|for\s*\(;;\)|for\s+;\s*;|for\s*\{\s*\}|while\s+:\s*\n?\s*pass|loop\s*\{\s*\}|loop\s+do|while\s+true\s*do|while\s+:\s*do)`)

// hasUnboundedLoop returns true when any code block contains an unbounded
// loop shape without an obvious break/return exit.
func hasUnboundedLoop(blocks []CodeBlock) bool {
	for _, b := range blocks {
		code := b.Code
		if loopRegex.MatchString(code) {
			// Heuristic: if the loop body contains break/return/exit,
			// do not flag. This keeps "while true { if cond { break } }"
			// from being a false positive.
			if !loopHasExit(code) {
				return true
			}
		}
	}
	return false
}

// loopHasExit returns true when code contains an obvious exit statement.
func loopHasExit(code string) bool {
	low := strings.ToLower(code)
	if strings.Contains(low, "break") || strings.Contains(low, "return") {
		return true
	}
	if strings.Contains(low, "exit(") || strings.Contains(low, "sys.exit") {
		return true
	}
	if strings.Contains(low, "os.exit") {
		return true
	}
	return false
}

// effectiveTimeout returns the requested timeout, or the profile default
// when the request omits one. Used by the guard and tests.
func effectiveTimeout(in ScanInput, defaultTimeout time.Duration) time.Duration {
	if in.Timeout > 0 {
		return in.Timeout
	}
	if defaultTimeout > 0 {
		return defaultTimeout
	}
	return 0
}
