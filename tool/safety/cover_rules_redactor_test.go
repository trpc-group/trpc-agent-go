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
	"unicode/utf8"

	"github.com/stretchr/testify/require"
)

// coverrulesSecret is a value matching the stripe_key secret pattern.
const coverrulesSecret = "sk_live_1234567890abcdef"

func TestCoverrules_RedactValue_ScalarTypes(t *testing.T) {
	for _, v := range []any{nil, true, 1, int8(2), int16(3), int32(4), int64(5),
		uint(6), uint8(7), uint16(8), uint32(9), uint64(10), 1.5, float32(2.5)} {
		out, changed, err := redactValue(v)
		require.NoError(t, err)
		require.False(t, changed)
		require.Equal(t, v, out)
	}
}

func TestCoverrules_RedactValue_String(t *testing.T) {
	out, changed, err := redactValue("key=" + coverrulesSecret)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, out, coverrulesSecret)

	out, changed, err = redactValue("nothing secret here")
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, "nothing secret here", out)
}

func TestCoverrules_RedactValue_Bytes(t *testing.T) {
	out, changed, err := redactValue([]byte("token " + coverrulesSecret))
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, string(out.([]byte)), coverrulesSecret)

	orig := []byte("plain bytes")
	out, changed, err = redactValue(orig)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, orig, out)
}

func TestCoverrules_RedactValue_RawMessage(t *testing.T) {
	raw := json.RawMessage(`{"key":"` + coverrulesSecret + `"}`)
	out, changed, err := redactValue(raw)
	require.NoError(t, err)
	require.True(t, changed)
	require.NotContains(t, string(out.(json.RawMessage)), coverrulesSecret)

	plain := json.RawMessage(`{"key":"value"}`)
	out, changed, err = redactValue(plain)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, plain, out)
}

func TestCoverrules_RedactValue_Slice(t *testing.T) {
	in := []any{"ok", "secret " + coverrulesSecret, 42}
	out, changed, err := redactValue(in)
	require.NoError(t, err)
	require.True(t, changed)
	s := out.([]any)
	require.Equal(t, "ok", s[0])
	require.NotContains(t, s[1], coverrulesSecret)
	require.Equal(t, 42, s[2])
}

func TestCoverrules_RedactMap_SecretFieldName(t *testing.T) {
	in := map[string]any{
		"password": "correct-horse-battery-staple",
		"note":     "no secret",
	}
	out, changed, err := redactMap(in)
	require.NoError(t, err)
	require.True(t, changed)
	m := out.(map[string]any)
	marker, ok := m["password"].(string)
	require.True(t, ok)
	require.Contains(t, marker, "[REDACTED:field:password:len=28]")
	require.Equal(t, "no secret", m["note"])
}

func TestCoverrules_RedactMap_SecretFieldNonString(t *testing.T) {
	// A secret-named field whose value is not a string falls through to
	// the generic redaction path.
	in := map[string]any{"password": 12345}
	out, changed, err := redactMap(in)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 12345, out.(map[string]any)["password"])
}

func TestCoverrules_RedactMap_EmptySecretFieldString(t *testing.T) {
	in := map[string]any{"token": ""}
	out, changed, err := redactMap(in)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, "", out.(map[string]any)["token"])
}

func TestCoverrules_RedactUnknownType_UnmarshalableNoSecret(t *testing.T) {
	v := func() {}
	out, changed, err := redactUnknownType(v)
	require.NoError(t, err)
	require.False(t, changed)
	require.NotNil(t, out)
}

func TestCoverrules_RedactUnknownType_MarshalableStruct(t *testing.T) {
	type cfg struct {
		Key string `json:"key"`
	}
	out, changed, err := redactUnknownType(cfg{Key: coverrulesSecret})
	require.NoError(t, err)
	require.True(t, changed)
	m, ok := out.(map[string]any)
	require.True(t, ok)
	require.NotContains(t, m["key"], coverrulesSecret)
}

func TestCoverrules_RedactUnknownType_MarshalableNoSecret(t *testing.T) {
	v := struct {
		Name string `json:"name"`
	}{Name: "plain"}
	out, changed, err := redactUnknownType(v)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, v, out)
}

func TestCoverrules_RedactUnknownType_UnmarshalableContainingSecret(t *testing.T) {
	// A type that fails json.Marshal and whose %v form contains a secret
	// must be replaced by a redaction placeholder map.
	// Wrap a channel in a struct together with a secret string field.
	type mixed struct {
		Ch     chan int
		Secret string
	}
	m := mixed{Ch: make(chan int), Secret: coverrulesSecret}
	out, changed, err := redactUnknownType(m)
	require.NoError(t, err)
	// json.Marshal fails on the channel; fmt %v includes the secret.
	require.True(t, changed)
	placeholder, ok := out.(map[string]any)
	require.True(t, ok)
	require.Equal(t, "redacted", placeholder["status"])
}

func TestCoverrules_LimitString(t *testing.T) {
	t.Run("no truncation needed", func(t *testing.T) {
		s, cut := limitString("short", 100)
		require.False(t, cut)
		require.Equal(t, "short", s)
	})
	t.Run("non-positive budget returns unchanged", func(t *testing.T) {
		s, cut := limitString("anything", 0)
		require.False(t, cut)
		require.Equal(t, "anything", s)
	})
	t.Run("truncation appends marker within budget", func(t *testing.T) {
		s, cut := limitString(strings.Repeat("a", 100), 50)
		require.True(t, cut)
		require.LessOrEqual(t, int64(len(s)), int64(50))
		require.Contains(t, s, "[truncated:tool_safety]")
	})
	t.Run("budget smaller than marker", func(t *testing.T) {
		s, cut := limitString(strings.Repeat("a", 100), 10)
		require.True(t, cut)
		require.LessOrEqual(t, int64(len(s)), int64(10))
	})
	t.Run("budget just over marker length", func(t *testing.T) {
		marker := "\n[truncated:tool_safety]"
		s, cut := limitString(strings.Repeat("a", 100), int64(len(marker)+5))
		require.True(t, cut)
		require.True(t, strings.HasSuffix(s, marker))
	})
	t.Run("never splits a multibyte rune", func(t *testing.T) {
		src := strings.Repeat("a", 20) + strings.Repeat("世", 20)
		s, cut := limitString(src, 45)
		require.True(t, cut)
		require.True(t, utf8.ValidString(s), "output must be valid UTF-8")
		for _, r := range s {
			require.NotEqual(t, '\uFFFD', r)
		}
	})
}

func TestCoverrules_LimitResultBytes_Unlimited(t *testing.T) {
	v := map[string]any{"k": "value"}
	out, truncated, size := limitResultBytes(v, 0)
	require.False(t, truncated)
	require.Equal(t, v, out)
	require.Equal(t, measureBytes(v), size)
}

func TestCoverrules_LimitWithBudget_Types(t *testing.T) {
	t.Run("bytes within budget", func(t *testing.T) {
		b := &byteBudget{remaining: 100}
		out, truncated := limitWithBudget([]byte("hello"), b)
		require.False(t, truncated)
		require.Equal(t, []byte("hello"), out)
		require.Equal(t, int64(5), b.used)
	})
	t.Run("raw message over budget", func(t *testing.T) {
		b := &byteBudget{remaining: 40}
		out, truncated := limitWithBudget(json.RawMessage(strings.Repeat("a", 100)), b)
		require.True(t, truncated)
		require.LessOrEqual(t, int64(len(out.(json.RawMessage))), int64(40))
	})
	t.Run("scalars are free", func(t *testing.T) {
		b := &byteBudget{remaining: 1}
		out, truncated := limitWithBudget(int64(12345), b)
		require.False(t, truncated)
		require.Equal(t, int64(12345), out)
		require.Equal(t, int64(0), b.used)
	})
	t.Run("nil value", func(t *testing.T) {
		b := &byteBudget{remaining: 10}
		out, truncated := limitWithBudget(nil, b)
		require.False(t, truncated)
		require.Nil(t, out)
	})
	t.Run("slice drops items when budget exhausted", func(t *testing.T) {
		b := &byteBudget{remaining: 5}
		out, truncated := limitWithBudget([]any{"abcde", "fghij"}, b)
		require.True(t, truncated)
		require.Len(t, out.([]any), 1)
	})
	t.Run("map drops keys when budget exhausted", func(t *testing.T) {
		b := &byteBudget{remaining: 5}
		out, truncated := limitWithBudget(map[string]any{"a": "12345", "b": "67890"}, b)
		require.True(t, truncated)
		m := out.(map[string]any)
		require.Contains(t, m, "a")
		require.NotContains(t, m, "b")
	})
	t.Run("unknown type within budget", func(t *testing.T) {
		b := &byteBudget{remaining: 100}
		v := struct {
			A string `json:"a"`
		}{A: "x"}
		out, truncated := limitWithBudget(v, b)
		require.False(t, truncated)
		require.Equal(t, v, out)
		require.Positive(t, b.used)
	})
	t.Run("unknown type over budget truncates JSON", func(t *testing.T) {
		b := &byteBudget{remaining: 30}
		v := struct {
			A string `json:"a"`
		}{A: strings.Repeat("x", 100)}
		out, truncated := limitWithBudget(v, b)
		require.True(t, truncated)
		require.LessOrEqual(t, int64(len(out.(string))), int64(30))
	})
	t.Run("unknown unmarshalable type returned as-is", func(t *testing.T) {
		b := &byteBudget{remaining: 1}
		v := func() {}
		out, truncated := limitWithBudget(v, b)
		require.False(t, truncated)
		require.NotNil(t, out)
	})
}

func TestCoverrules_LimitResultBytes_GlobalBudget(t *testing.T) {
	// Two fields that individually fit the budget must not both pass
	// when their combined size exceeds it.
	v := map[string]any{
		"a": strings.Repeat("x", 700),
		"b": strings.Repeat("y", 700),
	}
	out, truncated, size := limitResultBytes(v, 1000)
	require.True(t, truncated)
	require.LessOrEqual(t, size, int64(1000))
	// The complete serialized form, not only the string leaves, must
	// fit the budget.
	raw, err := json.Marshal(out)
	require.NoError(t, err)
	require.LessOrEqual(t, int64(len(raw)), int64(1000))
}

func TestCoverrules_MeasureBytes(t *testing.T) {
	require.Equal(t, int64(5), measureBytes("hello"))
	require.Equal(t, int64(3), measureBytes([]byte("abc")))
	require.Equal(t, int64(4), measureBytes(json.RawMessage(`"ab"`)))
	require.Equal(t, int64(len(`{"k":"v"}`)), measureBytes(map[string]string{"k": "v"}))
	require.Equal(t, int64(0), measureBytes(func() {}))
}

func TestCoverrules_IsSecretFieldName(t *testing.T) {
	for _, name := range []string{
		"password", "Passwd", "PWD", "secret", "Token", "api_key",
		"apiKey", "access_token", "refresh_token", "private_key",
		"client_secret", "bearer", "Authorization", "credentials",
		"db_password", "mysecretvalue", "oauth_access_token",
	} {
		require.True(t, isSecretFieldName(name), name)
	}
	for _, name := range []string{"username", "note", "path", ""} {
		require.False(t, isSecretFieldName(name), name)
	}
}

func TestCoverrules_RedactedSnippet(t *testing.T) {
	s := redactedSnippet("key="+coverrulesSecret, 0)
	require.NotContains(t, s, coverrulesSecret)

	long := strings.Repeat("a", 100)
	require.Len(t, redactedSnippet(long, 10), 10)
	require.Equal(t, long, redactedSnippet(long, 0))
}
