//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

// redactValue walks value and replaces secret substrings in any string it
// contains. It preserves common JSON-compatible types (string, []any,
// map[string]any, []byte) and returns a safe replacement for unknown
// types that contain a secret. The boolean reports whether any change
// was made.
//
// Serializable unknown types that contain no secret are returned
// unchanged so callers do not lose type fidelity. Unserializable values
// are replaced because their contents cannot be verified safely.
func redactValue(value any) (any, bool, error) {
	return redactValueDepth(value, 0)
}

const maxRedactionDepth = 64

func redactValueDepth(value any, depth int) (any, bool, error) {
	if depth > maxRedactionDepth {
		return nil, false, fmt.Errorf(
			"tool result nesting exceeds %d levels",
			maxRedactionDepth,
		)
	}
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
		float32, float64, json.Number:
		return v, false, nil
	case []any:
		return redactSliceDepth(v, depth+1)
	case map[string]any:
		return redactMapDepth(v, depth+1)
	}
	return redactUnknownTypeDepth(value, depth+1)
}

// redactBytes redacts a []byte.
func redactBytes(v []byte) (any, bool, error) {
	if json.Valid(v) {
		safe, changed, err := redactRawMessage(json.RawMessage(v))
		if err != nil || !changed {
			return v, changed, err
		}
		raw, ok := safe.(json.RawMessage)
		if !ok {
			encoded, err := json.Marshal(safe)
			if err != nil {
				return nil, false, err
			}
			return encoded, true, nil
		}
		return []byte(raw), true, nil
	}
	s := string(v)
	out, changed := redactString(s)
	if !changed {
		return v, false, nil
	}
	return []byte(out), true, nil
}

// redactRawMessage redacts a json.RawMessage.
func redactRawMessage(v json.RawMessage) (any, bool, error) {
	raw := v
	redacted, rawChanged := redactString(string(v))
	if rawChanged {
		if !json.Valid([]byte(redacted)) {
			return nil, false, errors.New(
				"redacted raw JSON result is invalid",
			)
		}
		raw = json.RawMessage(redacted)
	}
	decoded, err := decodeJSONValue(raw)
	if err != nil {
		return nil, false, fmt.Errorf("decode raw JSON result: %w", err)
	}
	safe, changed, err := redactValue(decoded)
	if err != nil {
		return nil, false, err
	}
	if !changed && rawChanged {
		return raw, true, nil
	}
	if !changed && rawJSONHasSecretField(raw) {
		return json.RawMessage(`"[REDACTED]"`), true, nil
	}
	if !changed {
		return v, false, nil
	}
	encoded, err := json.Marshal(safe)
	if err != nil {
		return nil, false, fmt.Errorf(
			"encode redacted raw JSON result: %w", err,
		)
	}
	return json.RawMessage(encoded), true, nil
}

var rawJSONKeyRegex = regexp.MustCompile(
	`"((?:\\.|[^"\\])*)"\s*:`,
)

func rawJSONHasSecretField(raw json.RawMessage) bool {
	for _, match := range rawJSONKeyRegex.FindAllSubmatch(raw, -1) {
		if len(match) < 2 {
			continue
		}
		key, err := strconv.Unquote(`"` + string(match[1]) + `"`)
		if err == nil && isSecretFieldName(key) {
			return true
		}
	}
	return false
}

// redactSlice redacts a []any recursively.
func redactSlice(v []any) (any, bool, error) {
	return redactSliceDepth(v, 0)
}

func redactSliceDepth(v []any, depth int) (any, bool, error) {
	changed := false
	out := make([]any, len(v))
	for i, item := range v {
		safe, c, err := redactValueDepth(item, depth)
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
// api_key, etc.) and the value is non-empty, the value is replaced with
// a redaction marker regardless of its concrete type or whether it
// matches a secret regex.
func redactMap(v map[string]any) (any, bool, error) {
	return redactMapDepth(v, 0)
}

func redactMapDepth(v map[string]any, depth int) (any, bool, error) {
	changed := false
	out := make(map[string]any, len(v))
	keys := make([]string, 0, len(v))
	for k := range v {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		item := v[k]
		safeKey, keyChanged := redactString(k)
		if keyChanged {
			changed = true
		}
		baseKey := safeKey
		for suffix := 1; ; suffix++ {
			if _, exists := out[safeKey]; !exists {
				break
			}
			safeKey = baseKey + "#" + itoa(suffix)
			changed = true
		}
		if isSecretFieldName(k) && !isEmptySecretValue(item) {
			out[safeKey] = secretFieldMarker(safeKey, item)
			changed = true
			continue
		}
		safe, c, err := redactValueDepth(item, depth)
		if err != nil {
			return nil, false, err
		}
		if c {
			changed = true
		}
		out[safeKey] = safe
	}
	return out, changed, nil
}

func isEmptySecretValue(value any) bool {
	switch v := value.(type) {
	case nil:
		return true
	case string:
		return v == ""
	case []byte:
		return len(v) == 0
	case json.RawMessage:
		return len(v) == 0 || string(v) == "null"
	case []any:
		return len(v) == 0
	case map[string]any:
		return len(v) == 0
	}
	return false
}

func secretFieldMarker(name string, value any) string {
	if s, ok := value.(string); ok {
		return "[REDACTED:field:" + name + ":len=" + itoa(len(s)) + "]"
	}
	return "[REDACTED:field:" + name + "]"
}

// redactUnknownType handles types that are not JSON-compatible by
// marshaling to JSON, decoding the marshaled form into a generic JSON
// tree, and running the recursive field-aware redactor over that tree.
// Decoding first ensures secret-named fields and secret-bearing keys on
// concrete structs and typed maps are redacted exactly like
// map[string]any values.
func redactUnknownType(value any) (any, bool, error) {
	return redactUnknownTypeDepth(value, 0)
}

func redactUnknownTypeDepth(
	value any,
	depth int,
) (any, bool, error) {
	if containsSecretByteSlice(reflect.ValueOf(value), depth) {
		return map[string]any{
			"status": "redacted",
			"reason": "tool result contained secret bytes",
		}, true, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return map[string]any{
			"status": "redacted",
			"reason": "tool result type could not be serialized safely",
		}, true, nil
	}

	decoded, err := decodeJSONValue(raw)
	if err != nil {
		return nil, false, fmt.Errorf("decode serialized tool result: %w", err)
	}
	tree, treeChanged, err := redactValueDepth(decoded, depth)
	if err != nil {
		return nil, false, err
	}
	if !treeChanged {
		// No secret anywhere: return the original value unchanged so
		// callers do not lose type fidelity.
		return value, false, nil
	}
	return tree, true, nil
}

func containsSecretByteSlice(
	value reflect.Value,
	depth int,
) bool {
	if !value.IsValid() || depth > maxRedactionDepth {
		return false
	}
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if value.IsNil() {
			return false
		}
		return containsSecretByteSlice(value.Elem(), depth+1)
	case reflect.Slice, reflect.Array:
		if value.Type().Elem().Kind() == reflect.Uint8 {
			data := make([]byte, value.Len())
			for i := range data {
				data[i] = byte(value.Index(i).Uint())
			}
			return hasSecret(string(data))
		}
		for i := 0; i < value.Len(); i++ {
			if containsSecretByteSlice(value.Index(i), depth+1) {
				return true
			}
		}
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if containsSecretByteSlice(iter.Key(), depth+1) ||
				containsSecretByteSlice(iter.Value(), depth+1) {
				return true
			}
		}
	case reflect.Struct:
		for i := 0; i < value.NumField(); i++ {
			if containsSecretByteSlice(value.Field(i), depth+1) {
				return true
			}
		}
	}
	return false
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
// The per-leaf budget counts string values only; map keys, scalars, and
// container syntax are not charged. A final marshal-and-verify pass
// therefore checks the complete encoded form against maxBytes and
// truncates the marshaled representation when it still exceeds the
// limit, so the returned value always serializes to at most maxBytes.
//
// It returns the truncated value, whether any truncation happened, and
// the serialized byte size of the (redacted, truncated) result.
func limitResultBytes(value any, maxBytes int64) (any, bool, int64) {
	rawValue, marshalErr := json.Marshal(value)
	if maxBytes <= 0 {
		if marshalErr != nil {
			fallback, size := serializedFallback(1 << 20)
			return fallback, true, size
		}
		return value, false, int64(len(rawValue))
	}
	if marshalErr != nil {
		fallback, size := serializedFallback(maxBytes)
		return fallback, true, size
	}
	if marshalErr == nil && int64(len(rawValue)) <= maxBytes {
		return value, false, int64(len(rawValue))
	}
	budget := &byteBudget{remaining: maxBytes}
	out, truncated := limitWithBudget(value, budget)
	raw, err := json.Marshal(out)
	if err != nil {
		fallback, size := serializedFallback(maxBytes)
		return fallback, true, size
	}
	if int64(len(raw)) <= maxBytes {
		return out, truncated, int64(len(raw))
	}
	// The encoded form exceeds the budget even though every string
	// leaf fit (oversized map keys, many scalars, or container
	// syntax). Truncate the marshaled representation itself. The
	// result is a plain string, so its own JSON encoding adds quotes
	// and escapes; shrink until the re-encoded form fits the budget.
	strBudget := maxBytes
	for strBudget > 0 {
		s, _ := limitString(string(raw), strBudget)
		enc, _ := json.Marshal(s)
		if int64(len(enc)) <= maxBytes {
			return s, true, int64(len(enc))
		}
		strBudget -= int64(len(enc)) - maxBytes
	}
	fallback, size := serializedFallback(maxBytes)
	return fallback, true, size
}

// serializedFallback returns a non-nil JSON value whose encoded size is
// at most maxBytes.
func serializedFallback(maxBytes int64) (any, int64) {
	marker := "[truncated:tool_safety]"
	if raw, err := json.Marshal(marker); err == nil &&
		int64(len(raw)) <= maxBytes {
		return marker, int64(len(raw))
	}
	if maxBytes >= 2 {
		return map[string]any{}, 2
	}
	return 0, 1
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
		decoded, err := decodeJSONValue(v)
		if err != nil {
			fallback, size := serializedFallback(maxInt64(1, b.remaining))
			b.used += size
			b.remaining = maxInt64(0, b.remaining-size)
			return fallback, true
		}
		return limitWithBudget(decoded, b)
	case bool, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64, json.Number:
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
		// Iterate keys in sorted order so budget truncation is
		// deterministic: identical inputs always truncate the same
		// fields regardless of Go's randomized map iteration order.
		keys := make([]string, 0, len(v))
		for k := range v {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			item := v[k]
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
		fallback, size := serializedFallback(maxInt64(1, b.remaining))
		b.used += size
		b.remaining = maxInt64(0, b.remaining-size)
		return fallback, true
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
	raw, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return int64(len(raw))
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}

func decodeJSONValue(data []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return value, nil
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
		strings.Contains(low, "accesstoken") || strings.Contains(low, "accesskey") ||
		strings.Contains(low, "access_key") || strings.Contains(low, "token") ||
		strings.Contains(low, "private_key") ||
		strings.Contains(low, "privatekey") || strings.Contains(low, "client_secret") ||
		strings.Contains(low, "credential") ||
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
		// Truncate on a rune boundary so a multi-byte UTF-8 rune is
		// never split, mirroring limitString.
		cut := max
		for cut > 0 && !utf8.RuneStart(redacted[cut]) {
			cut--
		}
		redacted = redacted[:cut]
	}
	return strings.TrimSpace(redacted)
}
