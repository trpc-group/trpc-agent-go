//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(password|passwd|token|api[_-]?key|client[_-]?secret|private[_-]?key|secret)(\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`),
	regexp.MustCompile(`(?i)\b(gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}|sk-[A-Za-z0-9_-]{16,}|xox[baprs]-[A-Za-z0-9-]{16,})\b`),
	regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`),
	regexp.MustCompile(`(?i)\bBearer\s+[A-Za-z0-9._~+/=-]{16,}`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\b`),
	regexp.MustCompile(`(?i)(postgres(?:ql)?|mysql)://[^\s"']+`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
}

var secretLiteralAssignment = regexp.MustCompile(
	`(?i)\b(?:password|passwd|token|api[_-]?key|apikey|client[_-]?secret|secret|private[_-]?key)\b\s*(?::=|=|:)\s*("[^"]+"|'[^']+'|` + "`[^`]+`" + `)`,
)

var secretAssignmentAnchor = regexp.MustCompile(
	`(?i)\b(?:password|passwd|token|api[_-]?key|apikey|client[_-]?secret|secret|private[_-]?key)\b\s*(?::=|=|:)\s*(?:` + "`" + `)?`,
)

var pemPrivateKeyBegin = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)

func redact(value string) string {
	for index, pattern := range redactionPatterns {
		if index == 0 {
			value = pattern.ReplaceAllString(value, `$1$2"[REDACTED]"`)
		} else {
			value = pattern.ReplaceAllString(value, `[REDACTED]`)
		}
	}
	return value
}

// redactReport returns a copy with every persisted string field redacted.
// Keeping this boundary centralized prevents new report fields from bypassing
// redaction when reports are written to disk or a store.
func redactReport(report Report) Report {
	report.SandboxRuns = append([]SandboxRun(nil), report.SandboxRuns...)
	report.PermissionDecisions = append([]PermissionDecision(nil), report.PermissionDecisions...)
	report.FilterDecisions = append([]FilterDecision(nil), report.FilterDecisions...)
	report.Artifacts = append([]Artifact(nil), report.Artifacts...)
	// Task IDs are validated at the API boundary and are persistence keys, so
	// redacting them would break report, artifact, and database correlation.
	report.Task.Status = TaskStatus(redact(string(report.Task.Status)))
	report.Task.InputMode = redact(report.Task.InputMode)
	report.Input.Digest = redact(report.Input.Digest)
	report.Findings = redactFindings(report.Findings)
	report.Warnings = redactFindings(report.Warnings)
	report.NeedsHumanReview = redactFindings(report.NeedsHumanReview)
	for index := range report.SandboxRuns {
		run := &report.SandboxRuns[index]
		run.Command = redact(run.Command)
		run.Args = redactStrings(run.Args)
		run.Executor = Executor(redact(string(run.Executor)))
		run.Status = RunStatus(redact(string(run.Status)))
		run.Stdout = redact(run.Stdout)
		run.Stderr = redact(run.Stderr)
		run.ErrorType = ErrorType(redact(string(run.ErrorType)))
	}
	for index := range report.PermissionDecisions {
		decision := &report.PermissionDecisions[index]
		decision.Command = redact(decision.Command)
		decision.Action = PermissionAction(redact(string(decision.Action)))
		decision.Reason = redact(decision.Reason)
	}
	for index := range report.FilterDecisions {
		decision := &report.FilterDecisions[index]
		decision.Fingerprint = redact(decision.Fingerprint)
		decision.Action = FilterAction(redact(string(decision.Action)))
		decision.Reason = redact(decision.Reason)
		decision.TargetBucket = redact(decision.TargetBucket)
	}
	for index := range report.Artifacts {
		artifact := &report.Artifacts[index]
		artifact.Name = redact(artifact.Name)
		artifact.Path = redact(artifact.Path)
		artifact.MIMEType = redact(artifact.MIMEType)
		artifact.Provenance = redact(artifact.Provenance)
		artifact.content = redact(artifact.content)
	}
	report.Metrics.SeverityDistribution = redactMetricKeys(report.Metrics.SeverityDistribution)
	report.Metrics.ErrorDistribution = redactMetricKeys(report.Metrics.ErrorDistribution)
	report.Conclusion = redact(report.Conclusion)
	report.Mode = ExecutionMode(redact(string(report.Mode)))
	return report
}

func redactFindings(values []Finding) []Finding {
	copyValues := append([]Finding(nil), values...)
	for index := range copyValues {
		finding := &copyValues[index]
		finding.Severity = Severity(redact(string(finding.Severity)))
		finding.Category = redact(finding.Category)
		finding.File = redact(finding.File)
		finding.Title = redact(finding.Title)
		finding.Evidence = redact(finding.Evidence)
		finding.Recommendation = redact(finding.Recommendation)
		finding.Source = redact(finding.Source)
		finding.RuleID = redact(finding.RuleID)
		finding.Fingerprint = redact(finding.Fingerprint)
	}
	return copyValues
}

func redactStrings(values []string) []string {
	copyValues := append([]string(nil), values...)
	for index := range copyValues {
		copyValues[index] = redact(copyValues[index])
	}
	return copyValues
}

func redactMetricKeys(values map[string]int) map[string]int {
	if values == nil {
		return nil
	}
	copyValues := make(map[string]int, len(values))
	for key, value := range values {
		copyValues[redact(key)] += value
	}
	return copyValues
}

func truncate(value string, limit int) (string, bool) {
	value = redact(value)
	if limit <= 0 || len(value) <= limit {
		return value, false
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut], true
}

func looksSecret(value string) bool {
	if match := secretLiteralAssignment.FindStringSubmatch(value); len(match) == 2 {
		literal := strings.Trim(match[1], "\"'`")
		if plausibleSecretLiteral(literal) {
			return true
		}
	}
	for _, pattern := range redactionPatterns[1:] {
		if pattern.MatchString(value) {
			return true
		}
	}
	return false
}

func plausibleSecretLiteral(value string) bool {
	if len(value) < 8 || regexp.MustCompile(`^[A-Z][A-Z0-9_]*$`).MatchString(value) {
		return false
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"x-token", "authorization", "content-type", "api-key", "bearer"} {
		if lower == marker {
			return false
		}
	}
	return true
}
