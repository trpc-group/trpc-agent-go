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
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"

	"trpc.group/trpc-go/trpc-agent-go/session"
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

func TestActiveFiltersUseDeletedAtNilPredicate(t *testing.T) {
	active := activeFilter(time.Now(), bson.M{"app_name": "app"})
	assert.Equal(t, nil, active["deleted_at"])
	assert.Equal(t, "app", active["app_name"])
	assert.Contains(t, active, "$or")

	noExpiry := activeFilterNoExpiry(bson.M{"app_name": "app"})
	assert.Equal(t, nil, noExpiry["deleted_at"])
	assert.Equal(t, "app", noExpiry["app_name"])
	assert.NotContains(t, noExpiry, "$or")
}

func TestStateMapToBSONHandlesNilAndCopiesBytes(t *testing.T) {
	src := []byte("value")
	got := stateMapToBSON(session.StateMap{
		"a.b": src,
		"nil": nil,
	})

	assert.Nil(t, got[encodeKey("nil")])
	copied := got[encodeKey("a.b")].([]byte)
	src[0] = 'X'
	assert.Equal(t, "value", string(copied))
}

func TestBSONToStateMapHandlesNilBinaryAndUnexpectedValues(t *testing.T) {
	raw := []byte("raw")
	bin := []byte("bin")
	got := bsonToStateMap(bson.M{
		encodeKey("nil"): nil,
		encodeKey("raw"): raw,
		encodeKey("bin"): primitive.Binary{Data: bin},
		encodeKey("bad"): "not-bytes",
	})

	assert.Contains(t, got, "nil")
	assert.Nil(t, got["nil"])
	assert.Equal(t, []byte("raw"), got["raw"])
	assert.Equal(t, []byte("bin"), got["bin"])
	assert.NotContains(t, got, "bad")

	raw[0] = 'X'
	bin[0] = 'Y'
	assert.Equal(t, []byte("raw"), got["raw"])
	assert.Equal(t, []byte("bin"), got["bin"])
}

func TestCopyStateBytesHandlesNilAndCopiesBytes(t *testing.T) {
	assert.Nil(t, copyStateBytes(nil))

	src := []byte("state")
	got := copyStateBytes(src)
	src[0] = 'X'
	assert.Equal(t, []byte("state"), got)
}
