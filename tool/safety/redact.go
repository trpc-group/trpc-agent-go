//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"regexp"
	"strings"
)

const redaction = "[REDACTED]"
const sensitivePathRedaction = "[SENSITIVE_PATH]"

var (
	sshPathTokenRe = regexp.MustCompile(`(?i)(~|/home/[^/\s'"|;&<>]+|/root)/\.ssh(?:/[^\s'"|;&<>]*)?`)
	awsPathTokenRe = regexp.MustCompile(`(?i)(~|/home/[^/\s'"|;&<>]+|/root)/\.aws(?:/[^\s'"|;&<>]*)?`)
	envFileTokenRe = regexp.MustCompile(`(?i)(^|[\s'"|;&<>])([^\s'"|;&<>]*/)?\.env(?:[.\w-]*)?`)
)

type redactedText struct {
	text    string
	changed bool
}

func redactIfNeeded(s string, p Policy) redactedText {
	if !p.RedactSensitiveEvidence {
		return redactedText{text: s}
	}
	out := s
	changed := false
	for _, re := range secretRes {
		next := re.ReplaceAllString(out, redaction)
		if next != out {
			changed = true
			out = next
		}
	}
	return redactedText{text: out, changed: changed}
}

// RedactText removes secrets and, when enabled by policy, sensitive paths from
// text before it is returned to a model, written to logs, or exported.
func RedactText(s string, p Policy) (string, bool) {
	p = p.Normalize()
	secret := redactIfNeeded(s, p)
	path := redactSensitivePaths(secret.text, p)
	return path.text, secret.changed || path.changed
}

func redactReport(r Report) Report {
	command := redactIfNeeded(r.Command, Policy{RedactSensitiveEvidence: true})
	r.Command = command.text
	r.Redacted = r.Redacted || command.changed
	for i := range r.Findings {
		ev := redactIfNeeded(r.Findings[i].Evidence, Policy{RedactSensitiveEvidence: true})
		r.Findings[i].Evidence = ev.text
		r.Redacted = r.Redacted || ev.changed
	}
	return r
}

func redactSensitivePathsReport(r Report, p Policy) Report {
	if !p.RedactSensitivePaths {
		return r
	}
	command := redactSensitivePaths(r.Command, p)
	r.Command = command.text
	r.Redacted = r.Redacted || command.changed
	for i := range r.Findings {
		ev := redactSensitivePaths(r.Findings[i].Evidence, p)
		r.Findings[i].Evidence = ev.text
		r.Redacted = r.Redacted || ev.changed
	}
	return r
}

func redactSensitivePaths(s string, p Policy) redactedText {
	out := s
	changed := false
	for _, path := range p.DeniedPaths {
		path = strings.TrimSpace(path)
		if path == "" || path == "/" {
			continue
		}
		next := redactSensitivePath(out, path)
		if next != out {
			changed = true
			out = next
		}
	}
	return redactedText{text: out, changed: changed}
}

func redactSensitivePath(text, denied string) string {
	switch strings.ToLower(denied) {
	case "~/.ssh":
		return sshPathTokenRe.ReplaceAllString(text, sensitivePathRedaction)
	case "~/.aws":
		return awsPathTokenRe.ReplaceAllString(text, sensitivePathRedaction)
	case ".env":
		return envFileTokenRe.ReplaceAllString(text, "${1}"+sensitivePathRedaction)
	default:
		return strings.ReplaceAll(text, denied, sensitivePathRedaction)
	}
}
