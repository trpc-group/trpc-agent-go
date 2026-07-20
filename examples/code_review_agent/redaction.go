//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import "regexp"

type secretRedactor struct {
	re      *regexp.Regexp
	replace func(string) string
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(api[_-]?key|token|password|secret)\b(\s*:?\=\s*["']?|\s*[:=]\s*["']?)([A-Za-z0-9_./+=-]{8,})`),
	regexp.MustCompile(`(?i)(bearer\s+)([A-Za-z0-9_./+=-]{12,})`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`(?s)-----BEGIN (?:[A-Z0-9 ]+ )?PRIVATE KEY-----.*?-----END (?:[A-Z0-9 ]+ )?PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)(mysql|postgres(?:ql)?|mongodb|redis)://[^\s'"<>]+`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
	regexp.MustCompile(`(?i)(xxx|yyy|zzz)\s*:?\=\s*[A-Za-z0-9_./+=-]{4,}`),
}

var secretRedactors = []secretRedactor{
	{
		re: secretPatterns[0],
		replace: func(s string) string {
			m := secretPatterns[0].FindStringSubmatch(s)
			if len(m) != 4 {
				return "[REDACTED]"
			}
			return m[1] + m[2] + "[REDACTED]"
		},
	},
	{
		re: secretPatterns[1],
		replace: func(s string) string {
			m := secretPatterns[1].FindStringSubmatch(s)
			if len(m) != 3 {
				return "[REDACTED]"
			}
			return m[1] + "[REDACTED]"
		},
	},
	{
		re:      secretPatterns[2],
		replace: func(string) string { return "[REDACTED]" },
	},
	{
		re:      secretPatterns[3],
		replace: func(string) string { return "[REDACTED PRIVATE KEY]" },
	},
	{
		re:      secretPatterns[4],
		replace: func(string) string { return "[REDACTED]" },
	},
	{
		re:      secretPatterns[5],
		replace: func(string) string { return "[REDACTED]" },
	},
	{
		re:      secretPatterns[6],
		replace: func(string) string { return "[REDACTED]" },
	},
}

// RedactSecrets removes credential-like values from text before logging,
// reporting, or persistence.
func RedactSecrets(s string) string {
	out := s
	for _, redactor := range secretRedactors {
		out = redactor.re.ReplaceAllStringFunc(out, redactor.replace)
	}
	return out
}

func redactFinding(f Finding) Finding {
	f.Evidence = RedactSecrets(f.Evidence)
	f.Recommendation = RedactSecrets(f.Recommendation)
	f.Title = RedactSecrets(f.Title)
	return f
}

func redactRun(r SandboxRun) SandboxRun {
	r.Output = RedactSecrets(r.Output)
	return r
}

func redactReviewReport(report ReviewReport) ReviewReport {
	report.Input = redactDiffSummary(report.Input)
	for i := range report.Findings {
		report.Findings[i] = redactFinding(report.Findings[i])
	}
	for i := range report.Warnings {
		report.Warnings[i] = redactFinding(report.Warnings[i])
	}
	for i := range report.NeedsHumanReview {
		report.NeedsHumanReview[i] = redactFinding(report.NeedsHumanReview[i])
	}
	for i := range report.SandboxRuns {
		report.SandboxRuns[i] = redactRun(report.SandboxRuns[i])
	}
	report.Conclusion = RedactSecrets(report.Conclusion)
	return report
}
