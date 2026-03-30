//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package gwproto

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUsageJSONIncludesZeroCounters(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(struct {
		Usage *Usage `json:"usage,omitempty"`
	}{
		Usage: &Usage{},
	})
	require.NoError(t, err)
	require.JSONEq(
		t,
		`{"usage":{"prompt_tokens":0,"completion_tokens":0,"total_tokens":0}}`,
		string(payload),
	)
}
