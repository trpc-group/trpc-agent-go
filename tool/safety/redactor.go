//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"fmt"
	"regexp"
)

// Redactor masks sensitive information in strings before they are
// written to audit logs or span attributes.
type Redactor struct {
	patterns []*regexp.Regexp
}

// NewRedactor compiles the given regex patterns and returns a
// Redactor that replaces matches with "[REDACTED]".  If any pattern
// fails to compile, NewRedactor returns a non-nil error and a nil
// Redactor: a broken redaction pattern must not silently degrade
// into a no-op that would persist secrets in clear text.
func NewRedactor(patterns []string) (*Redactor, error) {
	compiled, err := compilePatterns(patterns)
	if err != nil {
		return nil, fmt.Errorf("safety: redactor: %w", err)
	}
	return &Redactor{patterns: compiled}, nil
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
