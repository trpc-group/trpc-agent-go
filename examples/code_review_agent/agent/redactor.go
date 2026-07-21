package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
)

type Redactor struct {
	patterns []redactionPattern
}

type redactionPattern struct {
	re       *regexp.Regexp
	replacer func([]string) string
}

func NewRedactor() Redactor {
	return Redactor{patterns: []redactionPattern{
		{
			re: regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|auth[_-]?token|token|secret|password|passwd|pwd)(\s*(?::=|=|:)\s*["']?)([A-Za-z0-9_./+=\-]{8,})`),
			replacer: func(m []string) string {
				return m[1] + m[2] + redactionToken(m[1], m[3])
			},
		},
		{
			re: regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
			replacer: func(m []string) string {
				return redactionToken("aws_access_key", m[0])
			},
		},
		{
			re: regexp.MustCompile(`gh[pousr]_[A-Za-z0-9_]{20,}`),
			replacer: func(m []string) string {
				return redactionToken("github_token", m[0])
			},
		},
		{
			re: regexp.MustCompile(`(?i)Bearer\s+[A-Za-z0-9_./+=\-]{12,}`),
			replacer: func(m []string) string {
				return "Bearer " + redactionToken("bearer_token", m[0])
			},
		},
	}}
}

func (r Redactor) Redact(s string) string {
	out := s
	for _, p := range r.patterns {
		out = p.re.ReplaceAllStringFunc(out, func(match string) string {
			parts := p.re.FindStringSubmatch(match)
			if len(parts) == 0 {
				return match
			}
			return p.replacer(parts)
		})
	}
	return out
}

func redactionToken(kind, secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return "<redacted:" + kind + ":" + hex.EncodeToString(sum[:])[:12] + ">"
}
