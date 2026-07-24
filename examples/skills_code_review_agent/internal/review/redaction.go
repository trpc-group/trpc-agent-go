//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package review

import "regexp"

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?key(?:[_-]?id)?|secret[_-]?access[_-]?key|client[_-]?secret|refresh[_-]?token|session[_-]?token|id[_-]?token|auth[_-]?token|token|secret|credential|password|passwd|private[_-]?key)\s*:?=\s*["'][^"']+["']`),
	regexp.MustCompile("(?i)(api[_-]?key|access[_-]?key(?:[_-]?id)?|secret[_-]?access[_-]?key|client[_-]?secret|refresh[_-]?token|session[_-]?token|id[_-]?token|auth[_-]?token|token|secret|credential|password|passwd|private[_-]?key)\\s*:?=\\s*[^\"'`\\s,;]+"),
	regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?key(?:[_-]?id)?|secret[_-]?access[_-]?key|client[_-]?secret|refresh[_-]?token|session[_-]?token|id[_-]?token|auth[_-]?token|token|secret|credential|password|passwd|private[_-]?key)\s*:\s*["'][^"']+["']`),
	regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?key(?:[_-]?id)?|secret[_-]?access[_-]?key|client[_-]?secret|refresh[_-]?token|session[_-]?token|id[_-]?token|auth[_-]?token|token|secret|credential|password|passwd|private[_-]?key)\s*:\s*[A-Za-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)(authorization:\s*bearer\s+)[a-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)(authorization:\s*basic\s+)[a-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)(x-api-key:\s*)[a-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)(bearer\s+)[a-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)(basic\s+)[a-z0-9._~+/=-]{12,}`),
	regexp.MustCompile(`(?i)(://[^:/\s]+:)[^@\s/]+(@)`),
	regexp.MustCompile(`(?i)SetBasicAuth\(\s*"[^"]+"\s*,\s*"[^"]+"\s*\)`),
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`),
	regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`),
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{20,}`),
	regexp.MustCompile(`(?i)(sk|rk)_(live|test)_[a-z0-9]{16,}`),
	regexp.MustCompile(`(?i)sk-[a-z0-9]{20,}`),
	regexp.MustCompile(`SG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`(?i)npm_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`(?i)key-[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`),
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----[\s\S]*?-----END [A-Z ]*PRIVATE KEY-----`),
}

func redactSecrets(s string) string {
	out := s
	for _, re := range secretPatterns {
		out = re.ReplaceAllStringFunc(out, redactMatch)
	}
	return out
}

func redactMatch(s string) string {
	switch {
	case regexp.MustCompile(`(?i)^authorization:\s*bearer`).MatchString(s):
		return "Authorization: Bearer [REDACTED]"
	case regexp.MustCompile(`(?i)^authorization:\s*basic`).MatchString(s):
		return "Authorization: Basic [REDACTED]"
	case regexp.MustCompile(`(?i)^x-api-key:`).MatchString(s):
		return "X-API-Key: [REDACTED]"
	case regexp.MustCompile(`(?i)^-----BEGIN`).MatchString(s):
		return "[REDACTED_PRIVATE_KEY]"
	case regexp.MustCompile(`(?i)^(AKIA|AIza|gh[pousr]_|github_pat_|glpat-|xox[baprs]-|sk-|sk_|rk_|SG\.|npm_|key-|eyJ)`).MatchString(s):
		return "[REDACTED_SECRET]"
	case regexp.MustCompile(`(?i)^bearer\s+`).MatchString(s):
		return "Bearer [REDACTED]"
	case regexp.MustCompile(`(?i)^basic\s+`).MatchString(s):
		return "Basic [REDACTED]"
	case regexp.MustCompile(`(?i)^SetBasicAuth\(`).MatchString(s):
		return `SetBasicAuth("[REDACTED]", "[REDACTED]")`
	case regexp.MustCompile(`(?i)^://`).FindStringIndex(s) != nil:
		return regexp.MustCompile(`(?i)(://[^:/\s]+:)[^@\s/]+(@)`).ReplaceAllString(s, `${1}[REDACTED]${2}`)
	default:
		idx := regexp.MustCompile(`:?=`).FindStringIndex(s)
		if idx == nil {
			idx = regexp.MustCompile(`:`).FindStringIndex(s)
		}
		if idx == nil {
			return "[REDACTED_SECRET]"
		}
		return s[:idx[1]] + " [REDACTED]"
	}
}
