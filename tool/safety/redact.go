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
	return out, redacted
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
		if err != nil || u.User == nil {
			continue
		}
		password, hasPassword := u.User.Password()
		if !hasPassword || password == "" {
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
