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

var secretPatterns = []*regexp.Regexp{
	// API keys, tokens, passwords, secrets with value 8+ chars.
	regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret)(\s*[:=]\s*["']?)[A-Za-z0-9_./+=-]{8,}`),
	// Bearer tokens with value 12+ chars.
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9_./+=-]{12,}`),
	// AWS-style access key IDs.
	regexp.MustCompile(`AKID[A-Za-z0-9]{12,}`),
	// Private key headers.
	regexp.MustCompile(`-----BEGIN (?:RSA |EC )?PRIVATE KEY-----`),
	// Connection strings (MySQL, PostgreSQL, MongoDB, Redis).
	regexp.MustCompile(`(?i)(mysql|postgres(?:ql)?|mongodb|redis)://[^\s'"<>]+`),
	// JWT tokens (three base64url segments separated by dots).
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]+\.eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+`),
	// Generic "xxx=" patterns that may contain embedded secrets.
	regexp.MustCompile(`(?i)(xxx|yyy|zzz)\s*[:=]\s*[A-Za-z0-9_./+=-]{4,}`),
}

// RedactSecrets removes credential-like values from text before logging,
// reporting, or persistence.
func RedactSecrets(s string) string {
	out := s
	for _, re := range secretPatterns {
		out = re.ReplaceAllString(out, `${1}${2}[REDACTED]`)
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
