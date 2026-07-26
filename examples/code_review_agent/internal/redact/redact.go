//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package redact detects credential-shaped values and replaces their plaintext
// before review data crosses into model messages or durable storage.
package redact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/session"
)

const maskedValue = "[REDACTED]"

// Signal preserves the review value of a detected credential without keeping
// its plaintext. Line is one-based within the scanned input.
type Signal struct {
	Kind        string  `json:"kind"`
	RuleID      string  `json:"rule_id"`
	Line        int     `json:"line"`
	Evidence    string  `json:"evidence"`
	Confidence  float64 `json:"confidence"`
	Fingerprint string  `json:"fingerprint"`
}

// Result is the masked content together with structured detection signals.
type Result struct {
	Masked  []byte
	Signals []Signal
}

// Sanitizer is immutable after construction and safe for concurrent use.
type Sanitizer struct {
	rules []rule
}

type rule struct {
	id         string
	kind       string
	pattern    *regexp.Regexp
	secretPart int
	confidence float64
}

type match struct {
	start     int
	end       int
	rule      rule
	plaintext []byte
	inputLine int
	ruleOrder int
}

// New returns the shared sanitizer used by every persistence path in the
// example. Rules favor credential-shaped values over broad entropy guesses so
// ordinary source code is not aggressively destroyed.
func New() *Sanitizer {
	return &Sanitizer{rules: []rule{
		{
			// Matches a complete PEM block delimited by "BEGIN ... PRIVATE KEY"
			// and the corresponding END marker. The optional diff prefix before
			// the END marker recognizes both plain text and unified-diff hunks.
			// The match starts after the BEGIN line's prefix, so the first hunk
			// marker remains available as navigation context.
			id:         "SECRET-PRIVATE-KEY",
			kind:       "private_key",
			pattern:    regexp.MustCompile(`(?ms)-----BEGIN (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----.*?^[+ -]?-----END (?:RSA |EC |OPENSSH |DSA )?PRIVATE KEY-----`),
			secretPart: 0,
			confidence: 0.99,
		},
		{
			// Matches authorization values such as "Bearer ey..." and masks
			// only the credential after the scheme. Short labels like
			// "Bearer user" are deliberately ignored.
			id:         "SECRET-BEARER",
			kind:       "bearer_token",
			pattern:    regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9._~+/=-]{8,})`),
			secretPart: 1,
			confidence: 0.98,
		},
		{
			// Matches GitHub token families using ghp_, gho_, ghu_, ghs_, or
			// ghr_, followed by at least 20 ASCII letters or digits.
			id:         "SECRET-GITHUB",
			kind:       "github_token",
			pattern:    regexp.MustCompile(`\b(gh[pousr]_[A-Za-z0-9]{20,})\b`),
			secretPart: 1,
			confidence: 0.99,
		},
		{
			// Matches OpenAI-shaped keys beginning with "sk-" and at least 16
			// URL-safe token characters. The prefix makes this narrower than a
			// generic high-entropy-string detector.
			id:         "SECRET-OPENAI",
			kind:       "api_key",
			pattern:    regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{16,})\b`),
			secretPart: 1,
			confidence: 0.98,
		},
		{
			id:         "SECRET-GITLAB",
			kind:       "gitlab_token",
			pattern:    regexp.MustCompile(`\b(glpat-[A-Za-z0-9_-]{20,})\b`),
			secretPart: 1,
			confidence: 0.99,
		},
		{
			id:         "SECRET-SLACK",
			kind:       "slack_token",
			pattern:    regexp.MustCompile(`\b(xox[baprs]-[A-Za-z0-9-]{10,})\b`),
			secretPart: 1,
			confidence: 0.99,
		},
		{
			id:         "SECRET-GOOGLE-API-KEY",
			kind:       "api_key",
			pattern:    regexp.MustCompile(`\b(AIza[0-9A-Za-z_-]{20,})\b`),
			secretPart: 1,
			confidence: 0.99,
		},
		{
			id:         "SECRET-STRIPE",
			kind:       "api_key",
			pattern:    regexp.MustCompile(`\b((?:sk|rk)_(?:live|test)_[A-Za-z0-9]{16,})\b`),
			secretPart: 1,
			confidence: 0.99,
		},
		{
			id:         "SECRET-SENDGRID",
			kind:       "api_key",
			pattern:    regexp.MustCompile(`\b(SG\.[A-Za-z0-9_-]{16,}\.[A-Za-z0-9_-]{16,})\b`),
			secretPart: 1,
			confidence: 0.99,
		},
		{
			id:         "SECRET-NPM",
			kind:       "access_token",
			pattern:    regexp.MustCompile(`\b(npm_[A-Za-z0-9]{20,})\b`),
			secretPart: 1,
			confidence: 0.99,
		},
		{
			id:         "SECRET-TWILIO",
			kind:       "api_key",
			pattern:    regexp.MustCompile(`\b(SK[0-9a-fA-F]{32})\b`),
			secretPart: 1,
			confidence: 0.99,
		},
		{
			// Matches the fixed AWS access-key form: AKIA or ASIA followed by
			// exactly 16 uppercase letters or digits.
			id:         "SECRET-AWS-ACCESS-KEY",
			kind:       "access_key",
			pattern:    regexp.MustCompile(`\b((?:AKIA|ASIA)[A-Z0-9]{16})\b`),
			secretPart: 1,
			confidence: 0.99,
		},
		{
			// Matches a compact JWT-shaped value with three base64url-like
			// segments separated by dots and an encoded JSON header prefix.
			id:         "SECRET-JWT",
			kind:       "token",
			pattern:    regexp.MustCompile(`\b(eyJ[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,}\.[A-Za-z0-9_-]{5,})\b`),
			secretPart: 1,
			confidence: 0.96,
		},
		{
			// Matches the password portion of URL userinfo, for example
			// "https://user:password@host", while retaining the URL and user.
			id:         "SECRET-URL-USERINFO",
			kind:       "password",
			pattern:    regexp.MustCompile(`(?i)(?:https?|postgres(?:ql)?|mysql|mongodb(?:\+srv)?|redis|amqp)://[^:/\s]+:([^@/\s]{4,})@`),
			secretPart: 1,
			confidence: 0.96,
		},
		{
			// Matches common credential field assignments such as
			// `api_key="value"`, `password: value`, or `client-secret=value`.
			// Values shorter than six characters and quoted prose containing
			// whitespace are ignored to limit obvious false positives.
			id:         "SECRET-ASSIGNMENT",
			kind:       "credential",
			pattern:    regexp.MustCompile(`(?i)\b(?:api[_-]?key|access[_-]?token|auth[_-]?token|refresh[_-]?token|token|password|passwd|secret|client[_-]?secret|aws[_-]?secret[_-]?access[_-]?key|private[_-]?key|signing[_-]?key|webhook[_-]?secret)\b\s*[:=]\s*["']?([^\s"',;}{]{6,})`),
			secretPart: 1,
			confidence: 0.92,
		},
	}}
}

// AppendEventHook returns the framework-native Session persistence hook. It is
// intentionally a final safety net; input and tool paths still redact before
// they hand content to Session Service.
func AppendEventHook(s *Sanitizer) session.AppendEventHook {
	return func(ctx *session.AppendEventContext, next func() error) error {
		if s == nil {
			return errors.New("session redaction hook requires a sanitizer")
		}
		if ctx == nil {
			return errors.New("session redaction hook received nil context")
		}
		if _, err := s.MaskEvent(ctx.Event); err != nil {
			return err
		}
		return next()
	}
}

// DetectAndMask replaces every detected credential and returns review-safe
// signals. Multiline replacements preserve newline counts so diff navigation
// and candidate-line mappings remain valid.
func (s *Sanitizer) DetectAndMask(input []byte) Result {
	if len(input) == 0 {
		return Result{Masked: append([]byte(nil), input...)}
	}
	matches := s.findMatches(input)
	if len(matches) == 0 {
		return Result{Masked: append([]byte(nil), input...)}
	}

	masked := replaceMatches(input, matches)
	signals := make([]Signal, 0, len(matches))
	for _, m := range matches {
		line := lineAt(input, m.start)
		evidence := string(replaceMatches(line, s.findMatches(line)))
		signals = append(signals, Signal{
			Kind:        m.rule.kind,
			RuleID:      m.rule.id,
			Line:        m.inputLine,
			Evidence:    strings.TrimSpace(evidence),
			Confidence:  m.rule.confidence,
			Fingerprint: fingerprint(m.plaintext),
		})
	}
	return Result{Masked: masked, Signals: signals}
}

// MaskString is a convenience for fields written to Review Store records.
func (s *Sanitizer) MaskString(input string) (masked string, count int) {
	result := s.DetectAndMask([]byte(input))
	return string(result.Masked), len(result.Signals)
}

// MaskValue masks a JSON-compatible tool result while preserving its JSON
// shape. It is used only when a detection occurred; callers can keep the
// original concrete value when count is zero.
func (s *Sanitizer) MaskValue(value any) (masked any, count int, err error) {
	if value == nil {
		return nil, 0, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, 0, fmt.Errorf("marshal value for redaction: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return nil, 0, fmt.Errorf("decode value for redaction: %w", err)
	}
	masked, count = s.maskJSONValue(decoded)
	if count == 0 {
		return value, 0, nil
	}
	return masked, count, nil
}

// MaskEvent applies the sanitizer to the exact JSON surface persisted by the
// framework Session Service. The event is replaced in place so the in-memory
// Session and the durable row observe the same content.
func (s *Sanitizer) MaskEvent(evt *event.Event) (count int, err error) {
	if evt == nil {
		return 0, nil
	}
	count = 0
	// StateDelta values are []byte and therefore become base64 strings during
	// JSON marshaling. Scan their real bytes first; otherwise a credential in
	// session state would evade the generic JSON-string traversal below.
	for key, value := range evt.StateDelta {
		result := s.DetectAndMask(value)
		if len(result.Signals) == 0 {
			continue
		}
		evt.StateDelta[key] = result.Masked
		count += len(result.Signals)
	}
	data, err := json.Marshal(evt)
	if err != nil {
		return 0, fmt.Errorf("marshal session event for redaction: %w", err)
	}
	var decoded any
	if err := json.Unmarshal(data, &decoded); err != nil {
		return 0, fmt.Errorf("decode session event for redaction: %w", err)
	}
	maskedValue, jsonCount := s.maskJSONValue(decoded)
	count += jsonCount
	if jsonCount == 0 {
		return count, nil
	}
	maskedData, err := json.Marshal(maskedValue)
	if err != nil {
		return 0, fmt.Errorf("marshal redacted session event: %w", err)
	}
	var masked event.Event
	if err := json.Unmarshal(maskedData, &masked); err != nil {
		return 0, fmt.Errorf("unmarshal redacted session event: %w", err)
	}
	// These fields are intentionally excluded from Event JSON because they are
	// runtime-only payloads. The Session safety hook must not erase them while
	// rebuilding the serializable portion of the event.
	masked.StructuredOutput = evt.StructuredOutput
	masked.ExecutionTrace = evt.ExecutionTrace
	*evt = masked
	return count, nil
}

// findMatches orders candidates by position, then prefers the longest and
// earlier-declared rule at the same position. That gives specific token rules
// precedence over the generic assignment rule and keeps replacement offsets
// non-overlapping.
func (s *Sanitizer) findMatches(input []byte) []match {
	if s == nil {
		return nil
	}
	var matches []match
	for ruleIndex, r := range s.rules {
		for _, submatchOffsets := range r.pattern.FindAllSubmatchIndex(input, -1) {
			// regexp returns a flat list of byte-offset pairs:
			// [wholeStart, wholeEnd, group1Start, group1End, ...].
			// secretPart is the capture-group number whose text is sensitive,
			// so multiplying it by two selects that group's start/end pair.
			secretGroupOffset := r.secretPart * 2
			if secretGroupOffset+1 >= len(submatchOffsets) ||
				submatchOffsets[secretGroupOffset] < 0 ||
				submatchOffsets[secretGroupOffset+1] <= submatchOffsets[secretGroupOffset] {
				// An unmatched optional capture is represented by -1, -1. Treat
				// malformed or empty capture ranges as no secret rather than
				// risking an out-of-bounds slice.
				continue
			}
			start := submatchOffsets[secretGroupOffset]
			end := submatchOffsets[secretGroupOffset+1]
			matches = append(matches, match{
				start:     start,
				end:       end,
				rule:      r,
				plaintext: bytes.Clone(input[start:end]),
				// Counting preceding newlines converts the byte offset to the
				// one-based line number exposed in SecretSignal.
				inputLine: 1 + bytes.Count(input[:start], []byte{'\n'}),
				// Declaration order is the final tie-breaker when two rules find
				// the same span; specific rules are declared before generic ones.
				ruleOrder: ruleIndex,
			})
		}
	}
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].start != matches[j].start {
			return matches[i].start < matches[j].start
		}
		if matches[i].end != matches[j].end {
			return matches[i].end > matches[j].end
		}
		return matches[i].ruleOrder < matches[j].ruleOrder
	})

	// When a specific token rule and the generic assignment rule overlap, keep
	// the first (more specific) span. Overlapping replacements would otherwise
	// corrupt offsets and duplicate secret signals.
	filtered := matches[:0]
	lastEnd := -1
	for _, m := range matches {
		if m.start < lastEnd {
			continue
		}
		filtered = append(filtered, m)
		lastEnd = m.end
	}
	return filtered
}

// maskJSONValue recursively visits JSON string values while preserving arrays,
// objects, numbers, and booleans. Tool results and Event JSON therefore keep a
// model-compatible shape after masking.
func (s *Sanitizer) maskJSONValue(value any) (maskedValue any, redactionCount int) {
	switch typed := value.(type) {
	case string:
		result := s.DetectAndMask([]byte(typed))
		return string(result.Masked), len(result.Signals)
	case []any:
		out := make([]any, len(typed))
		count := 0
		for i, item := range typed {
			maskedItem, itemCount := s.maskJSONValue(item)
			out[i] = maskedItem
			count += itemCount
		}
		return out, count
	case map[string]any:
		out := make(map[string]any, len(typed))
		count := 0
		for key, item := range typed {
			maskedItem, itemCount := s.maskJSONValue(item)
			out[key] = maskedItem
			count += itemCount
		}
		return out, count
	default:
		return value, 0
	}
}

// replaceMatches consumes the non-overlapping offsets produced by findMatches.
// PEM replacements retain their original newline count so diff navigation
// stays stable even though the complete credential block is removed.
func replaceMatches(input []byte, matches []match) []byte {
	if len(matches) == 0 {
		return append([]byte(nil), input...)
	}
	var out strings.Builder
	last := 0
	for _, m := range matches {
		out.Write(input[last:m.start])
		out.WriteString(maskedValue)
		if m.rule.kind == "private_key" {
			// A PEM value commonly spans multiple source or diff lines. Keep its
			// newline count stable so hunk line navigation remains meaningful,
			// while removing every byte of the credential itself.
			out.WriteString(strings.Repeat("\n", bytes.Count(m.plaintext, []byte{'\n'})))
		}
		last = m.end
	}
	out.Write(input[last:])
	return []byte(out.String())
}

func lineAt(input []byte, offset int) []byte {
	start := offset
	for start > 0 && input[start-1] != '\n' {
		start--
	}
	end := offset
	for end < len(input) && input[end] != '\n' {
		end++
	}
	return input[start:end]
}

func fingerprint(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:8])
}
