// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"regexp"

	internalredact "trpc.group/trpc-go/trpc-agent-go/internal/redact"
)

var (
	privateKeyPattern = regexp.MustCompile(
		`(?s)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----.*?(?:-----END [A-Z0-9 ]*PRIVATE KEY-----|\z)`,
	)
	credentialPathPattern = regexp.MustCompile(
		`(?i)(?:~|/|\.{1,2}/)?[A-Za-z0-9_./~-]*(?:\.ssh/[A-Za-z0-9_.-]+|\.aws/credentials|/credentials|/id_rsa|/id_ed25519|\.env|\.npmrc|\.pypirc)\b`,
	)
	basicAuthorizationPattern = regexp.MustCompile(
		`(?i)(authorization\s*:\s*basic\s+)([A-Za-z0-9+/=]+)`,
	)
	userPasswordFlagPattern = regexp.MustCompile(
		`(?i)((?:^|\s)(?:-u|--user)(?:=|\s+)[^:\s]+:)([^\s"']+)`,
	)
	urlUserInfoPattern = regexp.MustCompile(
		`(?i)([a-z][a-z0-9+.-]*://[^/@\s:]+:)([^/@\s]+)(@)`,
	)
	proxyAuthShortFlagPattern = regexp.MustCompile(
		`((?:^|\s)(?i:(?:[^\s]*/)?(?:nc|netcat))\s+(?:[^\s]+\s+)*-[A-Za-z0-9]*?P(?:=|\s+))("[^"]*"|'[^']*'|[^\s]+)`,
	)
	proxyAuthAttachedShortPattern = regexp.MustCompile(
		`((?:^|\s)(?i:(?:[^\s]*/)?(?:nc|netcat))\s+(?:[^\s]+\s+)*-[A-Za-z0-9]*?P)([^=\s][^\s]*)`,
	)
	proxyAuthLongFlagPattern = regexp.MustCompile(
		`(?i)((?:^|\s)(?:--proxy-user|--proxy-username)(?:=|\s+))("[^"]*"|'[^']*'|[^\s]+)`,
	)
	githubTokenPattern  = regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}\b`)
	awsAccessKeyPattern = regexp.MustCompile(`\b(?:AKIA|ASIA)[A-Z0-9]{16}\b`)
)

func scanSensitiveContent(text string) []Finding {
	if privateKeyPattern.MatchString(text) {
		return []Finding{newFinding(
			DecisionDeny, RiskCritical, "sensitive.private_key",
			"request contains private key material",
			"remove private key material and use a secret reference",
		)}
	}
	if redactSecretText(text) != text {
		return []Finding{newFinding(
			DecisionNeedsHumanReview, RiskHigh, "sensitive.secret",
			"request contains a credential-shaped value",
			"remove inline credentials and use an approved secret provider",
		)}
	}
	return nil
}

func redactReport(report *Report) {
	if report == nil {
		return
	}
	report.Command = redactReportString(report.Command, &report.Redacted)
	report.Recommendation = redactReportString(report.Recommendation, &report.Redacted)
	report.SafeSummary = redactReportString(report.SafeSummary, &report.Redacted)
	redactStrings(report.Evidence, &report.Redacted)
	for i := range report.Findings {
		redactStrings(report.Findings[i].Evidence, &report.Redacted)
		report.Findings[i].Recommendation = redactReportString(
			report.Findings[i].Recommendation, &report.Redacted,
		)
	}
}

func redactStrings(values []string, changed *bool) {
	for i := range values {
		values[i] = redactReportString(values[i], changed)
	}
}

func redactReportString(value string, changed *bool) string {
	redacted := redactSecretText(value)
	redacted = credentialPathPattern.ReplaceAllString(redacted, internalredact.Value)
	if redacted != value {
		*changed = true
	}
	return redacted
}

func redactSecretText(value string) string {
	redacted := basicAuthorizationPattern.ReplaceAllString(
		value, `${1}`+internalredact.Value,
	)
	redacted = userPasswordFlagPattern.ReplaceAllString(
		redacted, `${1}`+internalredact.Value,
	)
	redacted = urlUserInfoPattern.ReplaceAllString(
		redacted, `${1}`+internalredact.Value+`${3}`,
	)
	redacted = proxyAuthShortFlagPattern.ReplaceAllString(
		redacted, `${1}`+internalredact.Value,
	)
	redacted = proxyAuthAttachedShortPattern.ReplaceAllString(
		redacted, `${1}`+internalredact.Value,
	)
	redacted = proxyAuthLongFlagPattern.ReplaceAllString(
		redacted, `${1}`+internalredact.Value,
	)
	redacted = githubTokenPattern.ReplaceAllString(redacted, internalredact.Value)
	redacted = awsAccessKeyPattern.ReplaceAllString(redacted, internalredact.Value)
	redacted = internalredact.SensitiveText(redacted)
	return privateKeyPattern.ReplaceAllString(redacted, internalredact.Value)
}
