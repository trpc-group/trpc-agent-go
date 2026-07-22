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
)

// Redactor masks sensitive information in strings before they are
// written to audit logs or span attributes.
type Redactor struct {
	patterns []*regexp.Regexp
}

// NewRedactor compiles the given regex patterns and returns a
// Redactor that replaces matches with "[REDACTED]".
func NewRedactor(patterns []string) *Redactor {
	compiled, err := compilePatterns(patterns)
	if err != nil {
		// Fall back to a no-op redactor if patterns are invalid.
		return &Redactor{}
	}
	return &Redactor{patterns: compiled}
}

// Redact returns a copy of s with all sensitive matches replaced by
// "[REDACTED]".  If no patterns are configured the input is returned
// unchanged.
func (r *Redactor) Redact(s string) string {
	result := s
	for _, re := range r.patterns {
		result = re.ReplaceAllString(result, "[REDACTED]")
	}
	return result
}
