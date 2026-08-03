//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"net/url"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

const secretKeyPattern = `api[_-]?key|access[_-]?token|refresh[_-]?token|id[_-]?token|oauth[_-]?token|session[_-]?token|csrf[_-]?token|xsrf[_-]?token|jwt[_-]?token|client[_-]?secret|db[_-]?(password|passwd|secret)|private[_-]?key|aws[_-]?(access[_-]?key|secret)|authorization(_(header|token|value|key))?|bearer(_(token|value))?|password|passwd|secret|token` // #nosec G101 -- credential-name matching pattern, not a credential

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)"(` + secretKeyPattern + `)"\s*:\s*"[^"\\]+(\\.[^"\\]*)*"`),
	regexp.MustCompile(`(?i)(` + secretKeyPattern + `)\s*[:=]\s*(?:"[^"]+"|'[^']+'|[^\s]+)`),
	regexp.MustCompile(`(?i)(authorization\s*:\s*bearer)\s+[A-Za-z0-9._~+/-]+=*`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{16,})`),
	regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`),
}

var credentialFlagPattern = regexp.MustCompile(`(?i)(--(?:api[-_]?key|access[-_]?token|refresh[-_]?token|id[-_]?token|oauth[-_]?token|session[-_]?token|csrf[-_]?token|xsrf[-_]?token|jwt[-_]?token|client[-_]?secret|password|passwd|secret|token)\b(?:\s+|=))(?:"[^"]+"|'[^']+'|[^\s]+)`)

var networkCredentialFlagPattern = regexp.MustCompile(`(?i)(--(?:user|proxy-user|oauth2-bearer|pass|proxy-pass|ftp-user|ftp-password|password|proxy-password)\b(?:\s+|=))(?:"[^"]+"|'[^']+'|[^\s]+)`)

var networkShortCredentialFlagPattern = regexp.MustCompile(`(?i)(^|[\s])(-[uU])(?:(\s+)(?:"[^"]+"|'[^']+'|[^\s]+)|(?:"[^"]+"|'[^']+'|[^\s]+))`)

func redactString(s string) (string, bool) {
	redacted := false
	out := s
	for _, re := range secretPatterns {
		next := re.ReplaceAllString(out, "<redacted>")
		if next != out {
			redacted = true
			out = next
		}
	}
	if next, changed := redactURLCredentials(out); changed {
		redacted = true
		out = next
	}
	if next, changed := redactCredentialFlags(out); changed {
		redacted = true
		out = next
	}
	if next, changed := redactNetworkCredentialFlags(out); changed {
		redacted = true
		out = next
	}
	return out, redacted
}

func redactCredentialFlags(s string) (string, bool) {
	out := credentialFlagPattern.ReplaceAllString(s, `${1}<redacted>`)
	return out, out != s
}

func redactNetworkCredentialFlags(s string) (string, bool) {
	pipe, err := shellsafe.Parse(s)
	if err != nil {
		return redactNetworkCredentialFlagsFallback(s)
	}
	spans := shellCommandSpans(s)
	if len(spans) != len(pipe.Commands) {
		return redactNetworkCredentialFlagsFallback(s)
	}
	commands := make([]string, len(pipe.Commands))
	for i, argv := range pipe.Commands {
		if len(argv) > 0 {
			commands[i] = normalizeCommand(argv[0])
		}
	}
	return redactNetworkCredentialCommandSpans(s, spans, commands)
}

func redactNetworkCredentialFlagsFallback(s string) (string, bool) {
	spans := shellCommandSpans(s)
	commands := make([]string, len(spans))
	for i, span := range spans {
		commands[i] = rawNetworkCommandName(s[span.start:span.end])
	}
	return redactNetworkCredentialCommandSpans(s, spans, commands)
}

func redactNetworkCredentialCommandSpans(
	s string,
	spans []shellCommandSpan,
	commands []string,
) (string, bool) {
	var out strings.Builder
	last := 0
	redacted := false
	for i, span := range spans {
		if i >= len(commands) {
			break
		}
		segment := s[span.start:span.end]
		next, changed := redactNetworkCredentialSegment(segment, commands[i])
		if !changed {
			continue
		}
		if !redacted {
			out.Grow(len(s))
		}
		out.WriteString(s[last:span.start])
		out.WriteString(next)
		last = span.end
		redacted = true
	}
	if !redacted {
		return s, false
	}
	out.WriteString(s[last:])
	return out.String(), true
}

func redactNetworkCredentialSegment(segment, command string) (string, bool) {
	if command != "curl" && command != "wget" {
		return segment, false
	}
	out := networkCredentialFlagPattern.ReplaceAllString(
		segment, `${1}<redacted>`)
	out = networkShortCredentialFlagPattern.ReplaceAllString(
		out, `${1}${2}${3}<redacted>`)
	return out, out != segment
}

func rawNetworkCommandName(segment string) string {
	fields := strings.Fields(segment)
	if len(fields) == 0 {
		return ""
	}
	return normalizeCommand(strings.Trim(fields[0], `"'`))
}

type shellCommandSpan struct {
	start int
	end   int
}

func shellCommandSpans(s string) []shellCommandSpan {
	var spans []shellCommandSpan
	segmentStart := 0
	var quote byte
	escaped := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if escaped {
			escaped = false
			continue
		}
		if quote != 0 {
			if c == '\\' && quote == '"' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		switch c {
		case '\'', '"':
			quote = c
		case '\\':
			escaped = true
		case '|', '&', ';':
			start, end := trimShellCommandSpan(s, segmentStart, i)
			if start < end {
				spans = append(spans, shellCommandSpan{start: start, end: end})
			}
			if i+1 < len(s) && s[i+1] == c && (c == '|' || c == '&') {
				i++
			}
			segmentStart = i + 1
		}
	}
	start, end := trimShellCommandSpan(s, segmentStart, len(s))
	if start < end {
		spans = append(spans, shellCommandSpan{start: start, end: end})
	}
	return spans
}

func trimShellCommandSpan(s string, start, end int) (int, int) {
	segment := strings.TrimSpace(s[start:end])
	if segment == "" {
		return end, end
	}
	offset := strings.Index(s[start:end], segment)
	return start + offset, start + offset + len(segment)
}

var credentialURLPattern = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`)

func redactURLCredentials(s string) (string, bool) {
	matches := credentialURLPattern.FindAllStringIndex(s, -1)
	if len(matches) == 0 {
		return s, false
	}
	var out strings.Builder
	last := 0
	redacted := false
	for _, match := range matches {
		raw := s[match[0]:match[1]]
		u, err := url.Parse(raw)
		if err != nil || u.User == nil || u.User.String() == "" {
			continue
		}
		password, hasPassword := u.User.Password()
		if !hasPassword && u.User.Username() == "" {
			continue
		}
		if hasPassword && password == "" && u.User.Username() == "" {
			continue
		}
		if !redacted {
			out.Grow(len(s))
		}
		out.WriteString(s[last:match[0]])
		u.User = nil
		out.WriteString(u.String())
		last = match[1]
		redacted = true
	}
	if !redacted {
		return s, false
	}
	out.WriteString(s[last:])
	return out.String(), true
}

func containsSecret(s string) bool {
	_, ok := redactString(s)
	return ok || containsJSONSecret([]byte(s))
}

func redactEnv(env map[string]string) (map[string]string, bool) {
	if len(env) == 0 {
		return nil, false
	}
	out := make(map[string]string, len(env))
	redacted := false
	for k, v := range env {
		if looksSecretName(k) || containsSecret(v) {
			out[k] = "<redacted>"
			redacted = true
			continue
		}
		out[k] = v
	}
	return out, redacted
}

func looksSecretName(s string) bool {
	name := strings.ToLower(strings.TrimSpace(s))
	name = strings.ReplaceAll(name, "-", "_")
	switch name {
	case "token", "password", "passwd", "secret", "api_key", "apikey",
		"access_token", "refresh_token", "id_token", "oauth_token",
		"session_token", "csrf_token", "xsrf_token", "jwt_token",
		"client_secret", "private_key", "authorization", "bearer",
		"aws_access_key", "aws_secret_access_key":
		return true
	}
	if strings.HasSuffix(name, "_token") ||
		strings.HasSuffix(name, "_password") ||
		strings.HasSuffix(name, "_passwd") ||
		strings.HasSuffix(name, "_secret") ||
		strings.HasSuffix(name, "_api_key") {
		return true
	}
	if strings.HasPrefix(name, "authorization_") {
		switch strings.TrimPrefix(name, "authorization_") {
		case "header", "token", "value", "key":
			return true
		}
	}
	if strings.HasPrefix(name, "bearer_") {
		switch strings.TrimPrefix(name, "bearer_") {
		case "token", "value":
			return true
		}
	}
	return strings.HasPrefix(name, "aws_access_key_") ||
		strings.HasPrefix(name, "private_key_") ||
		strings.HasPrefix(name, "db_password_")
}

func containsJSONSecret(raw []byte) bool {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return false
	}
	return valueContainsJSONSecret(v)
}

func valueContainsJSONSecret(v any) bool {
	switch x := v.(type) {
	case map[string]any:
		for key, value := range x {
			if looksSecretName(key) && jsonValueLooksSecret(value) {
				return true
			}
			if valueContainsJSONSecret(value) {
				return true
			}
		}
	case []any:
		for _, value := range x {
			if valueContainsJSONSecret(value) {
				return true
			}
		}
	case string:
		_, redacted := redactString(x)
		return redacted
	}
	return false
}

func jsonValueLooksSecret(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x) != ""
	case []any:
		for _, item := range x {
			if jsonValueLooksSecret(item) {
				return true
			}
		}
	}
	return false
}
