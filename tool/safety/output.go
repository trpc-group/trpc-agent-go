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
	"sync"
	"unicode/utf8"
)

const outputTruncatedMarker = "\n[truncated]"

const maxPendingPEMMarkerBytes = 256

var (
	pemBeginMarkerRE = regexp.MustCompile(`(?i)-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	pemEndMarkerRE   = regexp.MustCompile(`(?i)-----END [A-Z ]*PRIVATE KEY-----`)
)

// SanitizedOutput describes output transformations applied by the scanner.
type SanitizedOutput struct {
	Value     string
	Redacted  bool
	Truncated bool
}

// OutputSanitizer redacts a sequence of output chunks while preserving
// sensitive-value state across calls. It is intended for one execution
// session and is safe for concurrent use.
type OutputSanitizer struct {
	mu           sync.Mutex
	scanner      *Scanner
	pending      string
	inPrivateKey bool
}

// NewOutputSanitizer creates an output sanitizer for one streaming session.
func (s *Scanner) NewOutputSanitizer() *OutputSanitizer {
	return &OutputSanitizer{scanner: s}
}

// Sanitize redacts one incremental output chunk.
func (s *OutputSanitizer) Sanitize(output string) string {
	if s == nil || s.scanner == nil {
		return output
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scanner.redactor == nil || !s.scanner.redactor.enabled {
		return s.scanner.SanitizeOutput(output)
	}
	visible := s.redactPrivateKeyChunks(s.pending + output)
	return s.scanner.SanitizeOutput(visible)
}

func (s *OutputSanitizer) redactPrivateKeyChunks(input string) string {
	s.pending = ""
	var out strings.Builder
	for input != "" {
		if s.inPrivateKey {
			loc := pemEndMarkerRE.FindStringIndex(input)
			if loc == nil {
				s.pending = privateKeyMarkerTail(input, "-----END ")
				return out.String()
			}
			s.inPrivateKey = false
			input = input[loc[1]:]
			continue
		}
		loc := pemBeginMarkerRE.FindStringIndex(input)
		if loc != nil {
			out.WriteString(input[:loc[0]])
			out.WriteString(s.scanner.redactor.replacement)
			s.inPrivateKey = true
			input = input[loc[1]:]
			continue
		}
		start := possiblePEMBeginStart(input)
		if start >= 0 {
			out.WriteString(input[:start])
			s.pending = input[start:]
			return out.String()
		}
		out.WriteString(input)
		return out.String()
	}
	return out.String()
}

func possiblePEMBeginStart(input string) int {
	upper := strings.ToUpper(input)
	if idx := strings.LastIndex(upper, "-----BEGIN "); idx >= 0 {
		candidate := upper[idx+len("-----BEGIN "):]
		if len(candidate) <= maxPendingPEMMarkerBytes &&
			(candidate == "" || strings.IndexFunc(candidate, func(r rune) bool {
				return r != ' ' && (r < 'A' || r > 'Z') && r != '-'
			}) == -1) {
			return idx
		}
	}
	prefix := "-----BEGIN "
	for n := min(len(input), len(prefix)-1); n > 0; n-- {
		if strings.EqualFold(input[len(input)-n:], prefix[:n]) {
			return len(input) - n
		}
	}
	return -1
}

func privateKeyMarkerTail(input, marker string) string {
	upper := strings.ToUpper(input)
	if idx := strings.LastIndex(upper, marker); idx >= 0 {
		candidate := input[idx:]
		if len(candidate) <= maxPendingPEMMarkerBytes {
			return candidate
		}
	}
	for n := min(len(input), len(marker)-1); n > 0; n-- {
		if strings.EqualFold(input[len(input)-n:], marker[:n]) {
			return input[len(input)-n:]
		}
	}
	return ""
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
