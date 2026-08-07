//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package finding

import "regexp"

// Sensitive patterns recognized for redaction.
var (
	openAIKeyPattern   = regexp.MustCompile(`sk-[A-Za-z0-9]{20,}`)
	gitHubTokenPattern = regexp.MustCompile(`gh[ps]_[A-Za-z0-9]{36}`)
	genericKeyPattern  = regexp.MustCompile(`(?i)(api[_-]?key|apikey|secret|token|password|passwd)\s*[:=]\s*['"][^'"]{8,}`)
	privateKeyPattern  = regexp.MustCompile(`-----BEGIN\s+(RSA|EC|DSA|OPENSSH)\s+PRIVATE\s+KEY-----`)
	awsKeyPattern      = regexp.MustCompile(`(AKIA[0-9A-Z]{16})`)
)

// Sanitizer handles redaction of sensitive information.
type Sanitizer struct {
	patterns    []*regexp.Regexp
	replacement string
}

// NewSanitizer creates a sanitizer with default patterns.
func NewSanitizer() *Sanitizer {
	return &Sanitizer{
		patterns: []*regexp.Regexp{
			openAIKeyPattern,
			gitHubTokenPattern,
			genericKeyPattern,
			privateKeyPattern,
			awsKeyPattern,
		},
		replacement: "***REDACTED***",
	}
}

// Sanitize redacts all sensitive patterns in the input string.
func (s *Sanitizer) Sanitize(input string) string {
	if input == "" {
		return input
	}
	result := input
	for _, pat := range s.patterns {
		result = pat.ReplaceAllString(result, s.replacement)
	}
	return result
}

// SanitizeFinding redacts sensitive patterns from a Finding's evidence field.
// Returns a copy of the finding with sanitized evidence, leaving the original unchanged.
func (s *Sanitizer) SanitizeFinding(f Finding) Finding {
	original := f.Evidence
	sanitized := s.Sanitize(original)
	if sanitized != original {
		f.Evidence = sanitized
		f.Sanitized = true
	}
	return f
}

// HasSensitiveContent checks if the input contains any sensitive patterns.
func (s *Sanitizer) HasSensitiveContent(input string) bool {
	for _, pat := range s.patterns {
		if pat.MatchString(input) {
			return true
		}
	}
	return false
}

// AddPattern adds a custom sensitive pattern to the sanitizer.
func (s *Sanitizer) AddPattern(pattern *regexp.Regexp) {
	s.patterns = append(s.patterns, pattern)
}

// DefaultSanitizer is a package-level instance for quick use.
var DefaultSanitizer = NewSanitizer()

// Sanitize is a convenience function using the default sanitizer.
func Sanitize(input string) string {
	return DefaultSanitizer.Sanitize(input)
}

// HasSensitiveContent is a convenience function using the default sanitizer.
func HasSensitiveContent(input string) bool {
	return DefaultSanitizer.HasSensitiveContent(input)
}
