// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import "unicode/utf8"

const outputTruncatedMarker = "\n[truncated]"

// SanitizedOutput describes output transformations applied by the scanner.
type SanitizedOutput struct {
	Value     string
	Redacted  bool
	Truncated bool
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
