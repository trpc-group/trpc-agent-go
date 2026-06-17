//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package mongodb

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEncodeDecode_RoundTrip(t *testing.T) {
	cases := []string{
		"",
		"plain",
		"with.dot",
		"with$dollar",
		"with\x00nul",
		"with\\backslash",
		"all.together$\\\x00",
		"app:user_id", // colons are allowed by BSON; pass through unchanged
		"temp:abc",    // ditto
		"中文键名",        // multi-byte unicode pass through
		`a\\b`,        // already-escaped sequence: encoded form should round-trip
		`a\d`,         // sentinel literal in the input
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			enc := encodeKey(in)
			// Encoded form must not contain BSON-illegal bytes.
			assert.NotContains(t, enc, ".")
			assert.NotContains(t, enc, "$")
			assert.NotContains(t, enc, "\x00")
			// Round-trip preserves the original.
			require.Equal(t, in, decodeKey(enc))
		})
	}
}

func TestEncodeKey_NoOpForCleanInput(t *testing.T) {
	for _, s := range []string{"", "plain", "snake_case", "PascalCase", "with-dash"} {
		assert.Equal(t, s, encodeKey(s))
		// Determinism: encoding twice yields the same result.
		assert.Equal(t, encodeKey(s), encodeKey(s))
	}
}

func TestEncodeKey_KnownSequences(t *testing.T) {
	assert.Equal(t, `a\db`, encodeKey("a.b"))
	assert.Equal(t, `a\sb`, encodeKey("a$b"))
	assert.Equal(t, `a\0b`, encodeKey("a\x00b"))
	assert.Equal(t, `a\\b`, encodeKey(`a\b`))
}

func TestDecodeKey_LonelyBackslashPreserved(t *testing.T) {
	// A trailing lone '\' has no escape pair to consume; preserve verbatim.
	assert.Equal(t, "abc\\", decodeKey("abc\\"))
}

func TestDecodeKey_UnknownEscapePreserved(t *testing.T) {
	// Unknown escape sequences are passed through (forward-compat).
	assert.Equal(t, `\x`, decodeKey(`\x`))
	assert.Equal(t, `a\xb`, decodeKey(`a\xb`))
}
