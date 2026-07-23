// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"regexp"
	"strings"
)

// Redactor redacts sensitive values before report or audit output.
type Redactor struct {
	replacement string
	patterns    []*regexp.Regexp
	enabled     bool
}

var credentialPatternSources = []string{
	`(?i)(api[_-]?key|token|password|passwd|secret|credential)\s*[:=]\s*['"]?[^'"\s]+`,
	`(?i)(authorization:\s*bearer\s+)[A-Za-z0-9._~+/\-=]+`,
	`(?i)(x-api-key:\s*)[A-Za-z0-9._~+/\-=]+`,
	`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`,
	`(?i)(sk-[A-Za-z0-9]{12,})`,
	`(?i)(ghp_[A-Za-z0-9_]{20,})`,
	`(?i)(mysql|postgres|postgresql|mongodb)://[^@\s]+@`,
}

// NewRedactor builds a redactor from config.
func NewRedactor(cfg RedactionConfig) (*Redactor, error) {
	replacement := cfg.Replacement
	if replacement == "" {
		replacement = "[REDACTED]"
	}
	patterns := append([]string(nil), credentialPatternSources...)
	patterns = append(patterns, cfg.ExtraPatterns...)
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, re)
	}
	enabled := cfg.Enabled == nil || *cfg.Enabled
	return &Redactor{replacement: replacement, patterns: compiled, enabled: enabled}, nil
}

// Redact replaces sensitive substrings and reports whether anything changed.
func (r *Redactor) Redact(s string) (string, bool) {
	if r == nil || !r.enabled || len(r.patterns) == 0 || s == "" {
		return s, false
	}
	out := s
	for _, re := range r.patterns {
		out = re.ReplaceAllStringFunc(out, func(match string) string {
			if strings.Contains(match, ":") {
				if idx := strings.Index(match, ":"); idx >= 0 && idx < len(match)-1 {
					return match[:idx+1] + " " + r.replacement
				}
			}
			if strings.Contains(match, "=") {
				if idx := strings.Index(match, "="); idx >= 0 && idx < len(match)-1 {
					return match[:idx+1] + r.replacement
				}
			}
			return r.replacement
		})
	}
	return out, out != s
}

func (r *Redactor) contains(s string) bool {
	if r == nil || s == "" {
		return false
	}
	if pemBeginMarkerRE.MatchString(s) {
		return true
	}
	for _, re := range r.patterns {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}
