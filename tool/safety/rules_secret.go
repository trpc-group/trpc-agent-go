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
	"sort"
	"strings"
)

// secretPattern describes one secret-detection pattern. The Pattern is a
// regex; the ID is a stable identifier used in evidence. Patterns must
// not capture the secret value into evidence; the redactor replaces the
// matched substring with [REDACTED:ID:length].
type secretPattern struct {
	id      string
	pattern *regexp.Regexp
}

// secretPatterns is the canonical list. Order matters: longer/more
// specific patterns are checked first so a JWT claim is not mistaken for
// a generic API key.
var secretPatterns = []secretPattern{
	{
		id:      "private_key_block",
		pattern: regexp.MustCompile(`(?s)-----BEGIN (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----.*?-----END (?:RSA |EC |DSA |OPENSSH |PGP )?PRIVATE KEY-----`),
	},
	{
		id:      "jwt",
		pattern: regexp.MustCompile(`(?s)eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`),
	},
	{
		id:      "aws_access_key_id",
		pattern: regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	},
	{
		id:      "aws_secret_access_key",
		pattern: regexp.MustCompile(`aws_secret_access_key\s*=\s*['"][A-Za-z0-9/+=]{40}['"]`),
	},
	{
		id:      "github_pat",
		pattern: regexp.MustCompile(`(?:gh[pousr]_[A-Za-z0-9]{36}|github_pat_[A-Za-z0-9_]{22,})`),
	},
	{
		id:      "google_api_key",
		pattern: regexp.MustCompile(`AIza[0-9A-Za-z_-]{35}`),
	},
	{
		id:      "slack_token",
		pattern: regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),
	},
	{
		id:      "stripe_key",
		pattern: regexp.MustCompile(`(?:sk|pk|rk)_(?:live|test)_[0-9A-Za-z]{16,}`),
	},
	{
		id:      "bearer_token",
		pattern: regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._\-+/=]{16,}`),
	},
	{
		id:      "url_credentials",
		pattern: regexp.MustCompile(`[a-z][a-z0-9+\-.]*://[^/\s:@]{1,64}:[^/\s:@]{1,64}@[^\s/]+`),
	},
	{
		id:      "password_assignment",
		pattern: regexp.MustCompile(`(?i)(?:password|passwd|pwd)\s*[:=]\s*['"][^'"\s]{6,}['"]`),
	},
	{
		id:      "api_key_assignment",
		pattern: regexp.MustCompile(`(?i)(?:api[_-]?key|apikey)\s*[:=]\s*['"][A-Za-z0-9_\-]{16,}['"]`),
	},
	{
		id:      "token_assignment",
		pattern: regexp.MustCompile(`(?i)(?:secret|token|access[_-]?token)\s*[:=]\s*['"][A-Za-z0-9_\-]{16,}['"]`),
	},
	{
		id:      "env_secret_assignment",
		pattern: regexp.MustCompile(`(?i)(?:API_KEY|SECRET|TOKEN|PASSWORD|PRIVATE_KEY)=[A-Za-z0-9_\-/+=]{8,}`),
	},
}

// secretMatch is one detected secret occurrence. Value is the matched
// text; callers must never put Value into evidence or audit events.
// Start and End are byte offsets into the source text.
type secretMatch struct {
	id    string
	value string
	start int
	end   int
}

// findSecrets scans text and returns the matches sorted by byte offset.
// Patterns are checked in priority order; once a span is consumed by a
// higher-priority match, lower-priority patterns are not applied to it.
// The returned slice is sorted ascending by start offset so the redactor
// can walk it left-to-right without re-locating values via strings.Index
// (which was unsafe when an earlier low-priority match's value reoccurs
// inside a later high-priority match's span).
func findSecrets(text string) []secretMatch {
	if text == "" {
		return nil
	}
	var matches []secretMatch
	consumed := make([]bool, len(text))
	for _, p := range secretPatterns {
		idxs := p.pattern.FindAllStringIndex(text, -1)
		for _, idx := range idxs {
			start, end := idx[0], idx[1]
			overlaps := false
			for i := start; i < end && i < len(consumed); i++ {
				if consumed[i] {
					overlaps = true
					break
				}
			}
			if overlaps {
				continue
			}
			for i := start; i < end && i < len(consumed); i++ {
				consumed[i] = true
			}
			matches = append(matches, secretMatch{
				id:    p.id,
				value: text[start:end],
				start: start,
				end:   end,
			})
		}
	}
	// Sort by start offset so redactString can walk the matches in
	// source order without re-locating values.
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].start < matches[j].start
	})
	return matches
}

// redactString replaces every secret match in text with a redaction
// marker and returns the redacted text plus whether any change was made.
// The marker contains only the pattern id and the matched length so it
// can be safely persisted.
//
// The matches are walked in source order using the recorded byte offsets;
// the value field is never re-located with strings.Index, so an earlier
// low-priority match whose value happens to appear inside a later
// high-priority match's span cannot cause the redactor to drop it or
// replace the wrong bytes.
func redactString(text string) (string, bool) {
	matches := findSecrets(text)
	if len(matches) == 0 {
		return text, false
	}
	var sb strings.Builder
	sb.Grow(len(text))
	cursor := 0
	for _, m := range matches {
		if m.start < cursor {
			// Defensive: overlaps should already be filtered out by
			// findSecrets. Skip if a previous (longer) match already
			// consumed this span.
			continue
		}
		sb.WriteString(text[cursor:m.start])
		sb.WriteString("[REDACTED:")
		sb.WriteString(m.id)
		sb.WriteString(":len=")
		sb.WriteString(itoa(len(m.value)))
		sb.WriteString("]")
		cursor = m.end
	}
	sb.WriteString(text[cursor:])
	return sb.String(), true
}

// itoa formats n as a decimal string without importing strconv (keeps
// the secret package free of incidental dependencies).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	negative := false
	if n < 0 {
		negative = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if negative {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// hasSecret returns true when text contains at least one secret pattern.
func hasSecret(text string) bool {
	for _, p := range secretPatterns {
		if p.pattern.MatchString(text) {
			return true
		}
	}
	return false
}

// ruleSecret evaluates secret-leak rules against the input command, code
// blocks, and environment values. Evidence records only the pattern id
// and the matched length; never the secret value.
//
// Rule ids:
//
//   - secret.input_or_code    secret detected in command or code block.
//   - secret.env_value        secret detected in an environment value.
//   - secret.env_name         environment variable name not in the
//     policy whitelist (also surfaced by
//     ruleEnvName).
func ruleSecret(in ScanInput, p Policy) []Finding {
	if !p.Rules.SecretLeak.Enabled {
		return nil
	}
	var out []Finding
	seen := map[string]bool{}
	add := func(ruleID, evidence string, risk RiskLevel) {
		if seen[ruleID+evidence] {
			return
		}
		seen[ruleID+evidence] = true
		out = append(out, Finding{
			RuleID:         ruleID,
			RiskLevel:      risk,
			Decision:       ruleDecision(p.Rules.SecretLeak.Action, risk, p),
			Evidence:       evidence,
			Recommendation: "Refuse the request; require the caller to inject secrets through a secret manager, not the tool payload",
		})
	}

	// Command and code blocks. Scan the FULL value first; redaction
	// must happen before any truncation so a token spanning the
	// truncation boundary is still matched and replaced.
	if matches := findSecrets(in.Command); len(matches) > 0 {
		add("secret.input_or_code", summarizeMatches(matches), RiskCritical)
	}
	for _, b := range in.CodeBlocks {
		if matches := findSecrets(b.Code); len(matches) > 0 {
			add("secret.input_or_code", summarizeMatches(matches), RiskCritical)
		}
	}

	// Environment values.
	for _, v := range in.Env {
		if matches := findSecrets(v); len(matches) > 0 {
			add("secret.env_value", summarizeMatches(matches), RiskCritical)
		}
	}

	return out
}

// summarizeMatches produces a redacted evidence string for a set of
// matches. It lists up to three distinct pattern ids with their lengths.
// It never includes the matched value.
func summarizeMatches(matches []secretMatch) string {
	byID := map[string]int{}
	var order []string
	for _, m := range matches {
		if _, ok := byID[m.id]; !ok {
			order = append(order, m.id)
		}
		byID[m.id] += len(m.value)
	}
	var parts []string
	for i, id := range order {
		if i >= 3 {
			parts = append(parts, "...")
			break
		}
		parts = append(parts, id+":len="+itoa(byID[id]))
	}
	return "patterns=" + strings.Join(parts, ",")
}
