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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactString_JWT(t *testing.T) {
	in := "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	out, changed := redactString(in)
	require.True(t, changed)
	require.NotContains(t, out, "eyJhbGciOiJIUzI1NiJ9")
	require.Contains(t, out, "[REDACTED:jwt:")
}

func TestRedactString_AWSAccessKey(t *testing.T) {
	in := "AKIAIOSFODNN7EXAMPLE"
	out, changed := redactString(in)
	require.True(t, changed)
	require.NotContains(t, out, "AKIAIOSFODNN7EXAMPLE")
	require.Contains(t, out, "[REDACTED:aws_access_key_id:")
}

func TestRedactString_PrivateKeyBlock(t *testing.T) {
	in := "-----BEGIN RSA PRIVATE KEY-----\nMIIEpAI...\n-----END RSA PRIVATE KEY-----"
	out, changed := redactString(in)
	require.True(t, changed)
	require.NotContains(t, out, "MIIEpAI")
	require.Contains(t, out, "[REDACTED:private_key_block:")
}

func TestRedactString_NoSecret(t *testing.T) {
	in := "go test ./..."
	out, changed := redactString(in)
	require.False(t, changed)
	require.Equal(t, in, out)
}

func TestRedactValue_NestedMapSliceByteSlice(t *testing.T) {
	value := map[string]any{
		"text":   "Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c",
		"list":   []any{"safe", "xoxb-1234567890-abcdef"},
		"bytes":  []byte("API_KEY=sk_live_1234567890abcdef1234"),
		"number": 42,
		"bool":   true,
		"nested": map[string]any{
			"deep": "AKIAIOSFODNN7EXAMPLE",
		},
	}
	out, changed, err := redactValue(value)
	require.NoError(t, err)
	require.True(t, changed)
	raw, _ := json.Marshal(out)
	require.False(t, strings.Contains(string(raw), "eyJhbGciOiJIUzI1NiJ9"))
	require.False(t, strings.Contains(string(raw), "xoxb-1234567890-abcdef"))
	require.False(t, strings.Contains(string(raw), "sk_live_1234567890abcdef1234"))
	require.False(t, strings.Contains(string(raw), "AKIAIOSFODNN7EXAMPLE"))
	require.True(t, strings.Contains(string(raw), "[REDACTED:"))
	// Non-secret scalars are preserved.
	require.Equal(t, 42, out.(map[string]any)["number"])
	require.Equal(t, true, out.(map[string]any)["bool"])
}

func TestRedactValue_UnknownTypeWithSecret(t *testing.T) {
	type weird struct {
		Inner string
	}
	w := &weird{Inner: "API_KEY=sk_live_1234567890abcdef1234"}
	out, changed, err := redactValue(w)
	require.NoError(t, err)
	require.True(t, changed)
	raw, _ := json.Marshal(out)
	require.False(t, strings.Contains(string(raw), "sk_live_1234567890abcdef1234"))
}

func TestRedactValue_UnknownTypeNoSecret(t *testing.T) {
	type weird struct {
		Inner string
	}
	w := &weird{Inner: "hello"}
	out, changed, err := redactValue(w)
	require.NoError(t, err)
	require.False(t, changed)
	// Original value returned unchanged.
	require.Equal(t, w, out)
}

// TestRedactValue_StructSecretFieldRedacted verifies the P1 regression:
// a secret-named field on a concrete result struct must be redacted even
// when the value matches no secret regex. The marshaled form is decoded
// into a generic JSON tree so the field-aware redactor applies.
func TestRedactValue_StructSecretFieldRedacted(t *testing.T) {
	type result struct {
		Password string `json:"password"`
		Note     string `json:"note"`
	}
	out, changed, err := redactValue(result{Password: "hunter2", Note: "ok"})
	require.NoError(t, err)
	require.True(t, changed)
	m, ok := out.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "[REDACTED:field:password:len=7]", m["password"])
	require.Equal(t, "ok", m["note"])
}

// TestRedactValue_TypedMapSecretFieldRedacted verifies the P1 regression:
// a secret-named key on a typed map (map[string]string rather than
// map[string]any) must be redacted even when the value matches no
// secret regex.
func TestRedactValue_TypedMapSecretFieldRedacted(t *testing.T) {
	in := map[string]string{"api_key": "short-secret", "note": "ok"}
	out, changed, err := redactValue(in)
	require.NoError(t, err)
	require.True(t, changed)
	m, ok := out.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "[REDACTED:field:api_key:len=12]", m["api_key"])
	require.Equal(t, "ok", m["note"])
}

// TestRedactValue_TypedResultNoSecretUnchanged verifies that a concrete
// result struct without any secret is still returned unchanged, so
// callers do not lose type fidelity.
func TestRedactValue_TypedResultNoSecretUnchanged(t *testing.T) {
	type result struct {
		Password string `json:"password"`
		Note     string `json:"note"`
	}
	in := result{Password: "", Note: "plain"}
	out, changed, err := redactValue(in)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, in, out)
}

func TestLimitString_TruncatesAndPreservesUTF8(t *testing.T) {
	in := "héllo " + strings.Repeat("x", 100)
	// Use a maxBytes large enough for the truncation marker (25 bytes)
	// but small enough to trigger truncation of the 107-byte input.
	out, truncated := limitString(in, 50)
	require.True(t, truncated)
	require.True(t, strings.HasSuffix(out, "[truncated:tool_safety]"))
	// Must not split a multi-byte rune.
	require.True(t, isValidUTF8(out))
}

func TestLimitString_NoTruncationWhenUnderLimit(t *testing.T) {
	in := "hello"
	out, truncated := limitString(in, 100)
	require.False(t, truncated)
	require.Equal(t, in, out)
}

func TestLimitResultBytes_MapAndSlice(t *testing.T) {
	value := map[string]any{
		"output": strings.Repeat("x", 100),
		"items":  []any{strings.Repeat("y", 100), strings.Repeat("z", 100)},
	}
	// Use a maxBytes large enough for the truncation marker (25 bytes)
	// but small enough to trigger truncation of the 100-byte strings.
	out, truncated, size := limitResultBytes(value, 40)
	require.True(t, truncated)
	require.Less(t, size, int64(300))
	raw, _ := json.Marshal(out)
	require.True(t, strings.Contains(string(raw), "[truncated:tool_safety]"))
}

// TestLimitResultBytes_MarshaledFormWithinBudget verifies the P1
// regression: the budget must account for the complete encoded
// representation, not only string leaves. A map with a huge key and an
// integer value, or a large numeric slice, serializes far beyond
// maxBytes even though no string leaf is oversized, and must be
// truncated so len(json.Marshal(out)) <= max.
func TestLimitResultBytes_MarshaledFormWithinBudget(t *testing.T) {
	const max = int64(4096)
	t.Run("oversized map key with scalar value", func(t *testing.T) {
		v := map[string]any{strings.Repeat("k", 1<<20): 1}
		out, truncated, size := limitResultBytes(v, max)
		require.True(t, truncated)
		raw, err := json.Marshal(out)
		require.NoError(t, err)
		require.LessOrEqual(t, int64(len(raw)), max)
		require.LessOrEqual(t, size, max)
	})
	t.Run("large numeric slice", func(t *testing.T) {
		v := make([]any, 0, 100000)
		for i := 0; i < 100000; i++ {
			v = append(v, i)
		}
		out, truncated, size := limitResultBytes(v, max)
		require.True(t, truncated)
		raw, err := json.Marshal(out)
		require.NoError(t, err)
		require.LessOrEqual(t, int64(len(raw)), max)
		require.LessOrEqual(t, size, max)
	})
	t.Run("scalar-only result still within budget", func(t *testing.T) {
		v := map[string]any{"count": 42, "ok": true}
		out, truncated, _ := limitResultBytes(v, max)
		require.False(t, truncated)
		raw, err := json.Marshal(out)
		require.NoError(t, err)
		require.LessOrEqual(t, int64(len(raw)), max)
	})
}

func TestRedactString_URLCredentials(t *testing.T) {
	in := "https://user:supersecretpass123@host.example/path"
	out, changed := redactString(in)
	require.True(t, changed)
	require.NotContains(t, out, "supersecretpass123")
}

// TestRedactString_MixedPrioritySecretsBothReplaced verifies the P0
// regression: an earlier low-priority assignment secret followed by a
// later high-priority secret must both be redacted. The previous
// implementation re-located matches with strings.Index and could drop
// the earlier match when its value reoccurred inside the later match's
// span.
func TestRedactString_MixedPrioritySecretsBothReplaced(t *testing.T) {
	// Use a TOKEN= value (low priority) and an AWS access key (high
	// priority). Both must be redacted regardless of priority order.
	in := "TOKEN=abcdefgh1234567890 then key=AKIAIOSFODNN7EXAMPLE"
	out, changed := redactString(in)
	require.True(t, changed)
	require.NotContains(t, out, "abcdefgh1234567890")
	require.NotContains(t, out, "AKIAIOSFODNN7EXAMPLE")
	require.Contains(t, out, "[REDACTED:env_secret_assignment:")
	require.Contains(t, out, "[REDACTED:aws_access_key_id:")
}

// TestRedactString_TokenSpanningTruncationBoundary verifies the P0
// regression: summarizeCommand and redactedSnippet must scan the full
// value before truncating so a token that crosses the truncation
// boundary is still matched and replaced.
func TestRedactString_TokenSpanningTruncationBoundary(t *testing.T) {
	// Build a string where the JWT starts before the 40-byte boundary
	// but extends past it.
	prefix := strings.Repeat("a", 30) + " "
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c"
	in := prefix + jwt
	snippet := redactedSnippet(in, 40)
	require.NotContains(t, snippet, "eyJhbGciOiJIUzI1NiJ9")
	require.NotContains(t, snippet, "SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c")
}

// isValidUTF8 returns true when s is valid UTF-8. Used to verify the
// truncation never splits a multi-byte rune.
func isValidUTF8(s string) bool {
	for _, r := range s {
		if r == 0xFFFD {
			return false
		}
	}
	return true
}

// TestLimitWithBudget_DeterministicKeyOrder verifies that global-budget
// truncation of a map is deterministic: identical inputs must truncate
// the same fields regardless of Go's randomized map iteration order.
func TestLimitWithBudget_DeterministicKeyOrder(t *testing.T) {
	mk := func() map[string]any {
		return map[string]any{
			"aaa": strings.Repeat("x", 100),
			"bbb": strings.Repeat("y", 100),
		}
	}
	// The budget fits the first field in full but not the second; the
	// lexicographically first key must always be the one preserved.
	first, truncated := limitWithBudget(mk(), &byteBudget{remaining: 120})
	require.True(t, truncated)
	firstMap, ok := first.(map[string]any)
	require.True(t, ok)
	require.Equal(t, strings.Repeat("x", 100), firstMap["aaa"])
	for i := 0; i < 50; i++ {
		out, truncated := limitWithBudget(mk(), &byteBudget{remaining: 120})
		require.True(t, truncated)
		require.Equal(t, first, out)
	}
}

// TestIsSecretFieldName_TokenSubstrings verifies that embedded
// token/access-key field names force redaction, matching the exact-match
// list.
func TestIsSecretFieldName_TokenSubstrings(t *testing.T) {
	for _, name := range []string{
		"id_token", "session_token", "csrf_token", "my_access_key",
		"myaccesskey",
	} {
		require.True(t, isSecretFieldName(name), "name %q", name)
	}
	require.False(t, isSecretFieldName("username"))
}
