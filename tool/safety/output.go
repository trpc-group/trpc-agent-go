// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"sync"
	"unicode/utf8"
)

const outputTruncatedMarker = "\n[truncated]"

// SanitizedOutput describes output transformations applied by the scanner.
type SanitizedOutput struct {
	Value     string
	Redacted  bool
	Truncated bool
}

// OutputSanitizer redacts a sequence of output chunks from one execution
// session. When redaction is enabled, it withholds output until SanitizeFinal
// so arbitrary configured regular expressions cannot leak across chunk
// boundaries. It is safe for concurrent use.
type OutputSanitizer struct {
	mu       sync.Mutex
	scanner  *Scanner
	pending  string
	overflow bool
}

// NewOutputSanitizer creates an output sanitizer for one streaming session.
func (s *Scanner) NewOutputSanitizer() *OutputSanitizer {
	return &OutputSanitizer{scanner: s}
}

// Sanitize buffers one incremental output chunk. When redaction is enabled, it
// returns an empty string until SanitizeFinal is called.
func (s *OutputSanitizer) Sanitize(output string) string {
	return s.sanitize(output, false)
}

// SanitizeFinal redacts the last chunk and releases buffered state.
func (s *OutputSanitizer) SanitizeFinal(output string) string {
	return s.sanitize(output, true)
}

func (s *OutputSanitizer) sanitize(output string, final bool) string {
	if s == nil || s.scanner == nil {
		return output
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanner.redactor == nil || !s.scanner.redactor.enabled {
		return s.scanner.SanitizeOutput(output)
	}
	if !s.overflow {
		limit := s.scanner.policy.ResourceLimits.MaxOutputBytes
		if limit > 0 && int64(len(s.pending))+int64(len(output)) > limit {
			s.pending = ""
			s.overflow = true
		} else {
			s.pending += output
		}
	}
	if !final {
		return ""
	}
	if s.overflow {
		s.overflow = false
		return s.scanner.SanitizeOutput(s.scanner.redactor.replacement + outputTruncatedMarker)
	}
	visible := s.pending
	s.pending = ""
	return s.scanner.SanitizeOutput(visible)
}

// SanitizeOutput redacts and bounds one user-visible executor output.
func (s *Scanner) SanitizeOutput(output string) string {
	outputs := s.SanitizeOutputParts(output)
	if len(outputs) == 0 {
		return ""
	}
	return outputs[0].Value
}

// SanitizeOutputs redacts output parts and applies one shared byte budget.
// The returned strings preserve the input order and their aggregate size does
// not exceed resource_limits.max_output_bytes when that limit is positive.
func (s *Scanner) SanitizeOutputs(outputs ...string) []string {
	parts := s.SanitizeOutputParts(outputs...)
	result := make([]string, len(parts))
	for i := range parts {
		result[i] = parts[i].Value
	}
	return result
}

// SanitizeOutputParts is SanitizeOutputs with transformation metadata.
func (s *Scanner) SanitizeOutputParts(outputs ...string) []SanitizedOutput {
	result := make([]SanitizedOutput, len(outputs))
	if s == nil {
		for i := range outputs {
			result[i].Value = outputs[i]
		}
		return result
	}
	for i, output := range outputs {
		result[i].Value = output
		if s.redactor != nil {
			result[i].Value, result[i].Redacted = s.redactor.Redact(result[i].Value)
		}
	}
	limit := s.policy.ResourceLimits.MaxOutputBytes
	if limit <= 0 {
		return result
	}
	remaining := limit
	for i := range result {
		if remaining <= 0 {
			result[i].Truncated = result[i].Value != ""
			result[i].Value = ""
			continue
		}
		before := result[i].Value
		result[i].Value = truncateUTF8(before, remaining)
		result[i].Truncated = result[i].Value != before
		remaining -= int64(len(result[i].Value))
	}
	return result
}

func truncateUTF8(value string, limit int64) string {
	if limit <= 0 || int64(len(value)) <= limit {
		return value
	}
	marker := outputTruncatedMarker
	if int64(len(marker)) >= limit {
		return marker[:limit]
	}
	end := int(limit) - len(marker)
	for end > 0 && !utf8.ValidString(value[:end]) {
		end--
	}
	return value[:end] + marker
}
