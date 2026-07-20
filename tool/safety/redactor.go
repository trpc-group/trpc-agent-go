//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// redactValue walks value and replaces secret substrings in any string it
// contains. It preserves common JSON-compatible types (string, []any,
// map[string]any, []byte) and returns a safe replacement for unknown
// types that contain a secret. The boolean reports whether any change
// was made.
//
// For an unknown type that has no secret in its string representation,
// the original value is returned unchanged so callers do not lose type
// fidelity.
func redactValue(value any) (any, bool, error) {
	switch v := value.(type) {
	case nil:
		return nil, false, nil
	case string:
		out, changed := redactString(v)
		return out, changed, nil
	case []byte:
		return redactBytes(v)
	case json.RawMessage:
		return redactRawMessage(v)
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return v, false, nil
	case []any:
		return redactSlice(v)
	case map[string]any:
		return redactMap(v)
	}
	return redactUnknownType(value)
}

// redactBytes redacts a []byte.
func redactBytes(v []byte) (any, bool, error) {
	s := string(v)
	out, changed := redactString(s)
	if !changed {
		return v, false, nil
	}
	return []byte(out), true, nil
}

// redactRawMessage redacts a json.RawMessage.
func redactRawMessage(v json.RawMessage) (any, bool, error) {
	out, changed := redactString(string(v))
	if !changed {
		return v, false, nil
	}
	return json.RawMessage(out), true, nil
}

// redactSlice redacts a []any recursively.
func redactSlice(v []any) (any, bool, error) {
	changed := false
	out := make([]any, len(v))
	for i, item := range v {
		safe, c, err := redactValue(item)
		if err != nil {
			return nil, false, err
		}
		if c {
			changed = true
		}
		out[i] = safe
	}
	return out, changed, nil
}

// redactMap redacts a map[string]any with field-aware key checking.
// When a key name indicates a secret-bearing field (password, token,
// api_key, etc.) and the value is a non-empty string, the value is
// replaced with a redaction marker regardless of whether it matches a
// secret regex. This catches values like "correct-horse-battery-staple"
// that are clearly secrets by context but do not match any pattern.
func redactMap(v map[string]any) (any, bool, error) {
	changed := false
	out := make(map[string]any, len(v))
	for k, item := range v {
		if isSecretFieldName(k) {
			if s, ok := item.(string); ok && s != "" {
				out[k] = "[REDACTED:field:" + k + ":len=" + itoa(len(s)) + "]"
				changed = true
				continue
			}
		}
		safe, c, err := redactValue(item)
		if err != nil {
			return nil, false, err
		}
		if c {
			changed = true
		}
		out[k] = safe
	}
	return out, changed, nil
}

// redactUnknownType handles types that are not JSON-compatible by
// marshaling to JSON, redacting, and attempting to unmarshal back.
func redactUnknownType(value any) (any, bool, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		s := fmt.Sprintf("%v", value)
		if hasSecret(s) {
			return map[string]any{
				"status":  "redacted",
				"reason":  "tool result type could not be serialized after secret detection",
				"pattern": "unknown",
			}, true, nil
		}
		return value, false, nil
	}
	redacted, changed := redactString(string(raw))
	if !changed {
		return value, false, nil
	}
	var decoded any
	if err := json.Unmarshal([]byte(redacted), &decoded); err == nil {
		return decoded, true, nil
	}
	return map[string]any{
		"status":  "redacted",
		"reason":  "tool result contained a secret and could not be re-decoded",
		"pattern": "unknown",
	}, true, nil
}

// limitString truncates s to at most maxBytes after redaction, appending a
// machine-readable truncation marker when it cuts. It never splits a
// multi-byte UTF-8 rune. The marker bytes are counted against the budget
// so the returned string is always <= maxBytes.
func limitString(s string, maxBytes int64) (string, bool) {
	if maxBytes <= 0 || int64(len(s)) <= maxBytes {
		return s, false
	}
	marker := "\n[truncated:tool_safety]"
	markerLen := int64(len(marker))
	// Reserve room for the marker; if the budget is too small to hold
	// both a non-empty prefix and the marker, return just the marker
	// (or an empty string for pathological budgets).
	budget := maxBytes - markerLen
	if budget <= 0 {
		if maxBytes <= markerLen {
			// Pathological budget: return what we can of the marker.
			if maxBytes <= 0 {
				return "", true
			}
			return marker[:maxBytes], true
		}
		return marker, true
	}
	cut := budget
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + marker, true
}

// limitResultBytes walks value and truncates string leaves so the total
// serialized byte size of the result is at most maxBytes. The budget is
// GLOBAL across all leaves: a 1 MiB policy with two 700 KiB fields must
// truncate at least one field, not allow both through as the previous
// per-leaf implementation did. The truncation marker is counted against
// the budget.
//
// It returns the truncated value, whether any truncation happened, and
// the total byte size of the (redacted, truncated) result.
func limitResultBytes(value any, maxBytes int64) (any, bool, int64) {
	if maxBytes <= 0 {
		// Unlimited: return unchanged.
		return value, false, measureBytes(value)
	}
	budget := &byteBudget{remaining: maxBytes}
	out, truncated := limitWithBudget(value, budget)
	return out, truncated, budget.used
}

// byteBudget tracks the remaining byte budget across all leaves of a
// result tree. The used field records how many bytes have been
// committed.
type byteBudget struct {
	remaining int64
	used      int64
}

// limitWithBudget walks value and truncates string leaves against the
// shared budget. It mirrors redactValue's type switch so JSON-compatible
// types preserve their shape while unknown types are JSON-marshaled.
func limitWithBudget(value any, b *byteBudget) (any, bool) {
	switch v := value.(type) {
	case nil:
		return nil, false
	case string:
		return limitStringWithBudget(v, b)
	case []byte:
		out, truncated := limitStringWithBudget(string(v), b)
		return []byte(out), truncated
	case json.RawMessage:
		out, truncated := limitStringWithBudget(string(v), b)
		return json.RawMessage(out), truncated
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		// Scalars are tiny; count them as 0 against the budget to
		// avoid penalizing numeric metadata. The serialized size of
		// a number is accounted for when the parent object is
		// marshaled by the framework.
		return v, false
	case []any:
		truncated := false
		out := make([]any, 0, len(v))
		for _, item := range v {
			if b.remaining <= 0 {
				truncated = true
				// Drop remaining items.
				continue
			}
			safe, t := limitWithBudget(item, b)
			if t {
				truncated = true
			}
			out = append(out, safe)
		}
		return out, truncated
	case map[string]any:
		truncated := false
		out := make(map[string]any, len(v))
		for k, item := range v {
			if b.remaining <= 0 {
				truncated = true
				continue
			}
			safe, t := limitWithBudget(item, b)
			if t {
				truncated = true
			}
			out[k] = safe
		}
		return out, truncated
	}
	// Unknown types: marshal to JSON, truncate the JSON string.
	raw, err := json.Marshal(value)
	if err != nil {
		return value, false
	}
	if int64(len(raw)) <= b.remaining {
		b.used += int64(len(raw))
		b.remaining -= int64(len(raw))
		return value, false
	}
	s, truncated := limitStringWithBudget(string(raw), b)
	return s, truncated
}

// limitStringWithBudget truncates s against the shared budget. The
// truncation marker is counted against the budget.
func limitStringWithBudget(s string, b *byteBudget) (string, bool) {
	if int64(len(s)) <= b.remaining {
		b.used += int64(len(s))
		b.remaining -= int64(len(s))
		return s, false
	}
	out, _ := limitString(s, b.remaining)
	b.used += int64(len(out))
	b.remaining = 0
	return out, true
}

// measureBytes returns the serialized byte size of value without
// truncating. Used when maxBytes is 0 (unlimited).
func measureBytes(value any) int64 {
	switch v := value.(type) {
	case string:
		return int64(len(v))
	case []byte:
		return int64(len(v))
	case json.RawMessage:
		return int64(len(v))
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return int64(len(raw))
}

// isSecretFieldName returns true when the field name (case-insensitive)
// indicates a secret-bearing field. When the value of such a field is a
// non-empty string, it is replaced with a redaction marker regardless of
// whether it matches a secret regex. This catches values like
// "correct-horse-battery-staple" that are clearly secrets by context
// but do not match any pattern.
func isSecretFieldName(name string) bool {
	low := strings.ToLower(name)
	// Exact matches.
	switch low {
	case "password", "passwd", "pwd", "secret", "token", "apikey",
		"api_key", "access_token", "accesskey", "access_key",
		"refresh_token", "private_key", "privatekey",
		"client_secret", "clientsecret",
		"bearer_token", "bearer", "authorization",
		"credentials", "credential":
		return true
	}
	// Substring matches.
	if strings.Contains(low, "password") || strings.Contains(low, "passwd") ||
		strings.Contains(low, "secret") || strings.Contains(low, "api_key") ||
		strings.Contains(low, "apikey") || strings.Contains(low, "access_token") ||
		strings.Contains(low, "accesstoken") || strings.Contains(low, "private_key") ||
		strings.Contains(low, "privatekey") || strings.Contains(low, "client_secret") ||
		strings.Contains(low, "bearer") || strings.Contains(low, "authorization") {
		return true
	}
	return false
}

// redactedSnippet redacts the FULL string first, then truncates the
// redacted result. The previous order (truncate-then-redact) could leave
// the original prefix of a token spanning the truncation boundary in the
// evidence, because the truncated prefix no longer matched the full
// secret regex. Scan-then-truncate guarantees no raw secret value
// reaches evidence, even when the token crosses the boundary.
func redactedSnippet(s string, max int) string {
	redacted, _ := redactString(s)
	if max > 0 && len(redacted) > max {
		redacted = redacted[:max]
	}
	return strings.TrimSpace(redacted)
}
