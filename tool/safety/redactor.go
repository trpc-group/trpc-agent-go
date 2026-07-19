//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import "regexp"

const redactedValue = "[REDACTED]"

var redactionPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|auth[_-]?token|password|passwd|secret)["']?(\s*[:=]\s*)(?:"(?:(?s:\\(?:.|$))|[^"\\])*(?:"|$)|'(?:(?s:\\(?:.|$))|[^'\\])*(?:'|$)|[^\s"',;&}]+)`),
	regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)([?&](?:api[_-]?key|access[_-]?token|token|password|secret)=)[^&#\s]+`),
	regexp.MustCompile(`(?i)(?:[A-Z]:\\Users\\[^\\\s]+|/(?:home|Users)/[^/\s]+)`),
}

func redactText(value string) (string, bool) {
	redacted := false
	for index, pattern := range redactionPatterns {
		var replacement string
		switch index {
		case 0:
			replacement = `${1}${2}` + redactedValue
		case 1, 5:
			replacement = `${1}` + redactedValue
		default:
			replacement = redactedValue
		}
		updated := pattern.ReplaceAllString(value, replacement)
		if updated != value {
			redacted = true
			value = updated
		}
	}
	return value, redacted
}

func redactReport(report Report) Report {
	redacted := report.Redacted
	redact := func(value *string) {
		updated, changed := redactText(*value)
		*value = updated
		redacted = redacted || changed
	}
	redact(&report.RuleID)
	redact(&report.Evidence)
	redact(&report.Recommendation)
	redact(&report.ToolName)
	redact(&report.Command)
	redact(&report.PolicyVersion)
	for index := range report.Findings {
		redact(&report.Findings[index].RuleID)
		redact(&report.Findings[index].Evidence)
		redact(&report.Findings[index].Recommendation)
	}
	report.Redacted = redacted
	return report
}

func hasSensitiveText(value string) bool {
	_, redacted := redactText(value)
	return redacted
}
