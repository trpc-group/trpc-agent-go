//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package util

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEventHasReplayDataPreservesLegacyDecodePolicy(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
		want bool
	}{
		{name: "native message", data: `{"choices":[{"message":{"replay":{"version":99}}}]}`, want: true},
		{name: "native delta", data: `{"choices":[{"delta":{"replay":{"version":99}}}]}`, want: true},
		{name: "empty native object", data: `{"choices":[{"message":{"replay":{}}}]}`, want: true},
		{name: "invalid native type", data: `{"choices":[{"message":{"replay":"invalid"}}]}`, want: true},
		{name: "native after malformed choice", data: `{"choices":[1,{"message":{"replay":{}}}]}`, want: true},
		{name: "native delta after malformed message", data: `{"choices":[{"message":1,"delta":{"replay":{}}}]}`, want: true},
		{name: "case insensitive fields", data: `{"Choices":[{"Message":{"Replay":{"version":99}}}]}`, want: true},
		{name: "null native record", data: `{"choices":[{"message":{"replay": null}}]}`},
		{name: "legacy message", data: `{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`},
		{name: "replay word in text", data: `{"choices":[{"message":{"content":"replay"}}]}`},
		{name: "unrelated replay extension", data: `{"extensions":{"replay":{"version":99}},"choices":[]}`},
		{name: "malformed legacy field", data: `{"choices":[{"message":1}]}`},
		{name: "malformed legacy choice", data: `{"choices":[1]}`},
		{name: "malformed legacy choices", data: `{"choices":1}`},
		{name: "malformed legacy JSON", data: `{`},
		{name: "empty choices", data: `{"choices":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, EventHasReplayData([]byte(tc.data)))
		})
	}
}
