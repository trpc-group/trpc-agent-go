//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package harness

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReplayCaseJSONRoundTrip(t *testing.T) {
	raw := []byte(`{
		"name":"demo",
		"key":{"appName":"a","userID":"u","sessionID":"s"},
		"operations":[
			{"type":"append_event","event":{"author":"user","role":"user","content":"hi"}},
			{"type":"set_state","key":"lang","value":"en"}
		]
	}`)
	var c ReplayCase
	require.NoError(t, json.Unmarshal(raw, &c))
	require.Equal(t, "demo", c.Name)
	require.Equal(t, "s", c.Key.SessionID)
	require.Len(t, c.Operations, 2)
	require.Equal(t, "append_event", c.Operations[0].Type)
	require.Equal(t, "user", c.Operations[0].Event.Author)
	require.Equal(t, "lang", c.Operations[1].Key)
}
