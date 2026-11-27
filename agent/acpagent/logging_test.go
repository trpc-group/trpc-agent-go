//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormatLogMessage(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		message string
		fields  []any
		want    string
	}{
		{
			name:    "no fields",
			message: "acpagent: ready",
			want:    "acpagent: ready",
		},
		{
			name:    "structured fields",
			message: "acpagent: closed ACP session",
			fields: []any{
				"app", "opencode-acp-example",
				"user_id", "demo-user",
				"session_id", "session-1",
			},
			want: "acpagent: closed ACP session app=opencode-acp-example user_id=demo-user session_id=session-1",
		},
		{
			name:    "quoted values",
			message: "acpagent: failed",
			fields:  []any{"error", errors.New("bad value")},
			want:    `acpagent: failed error="bad value"`,
		},
		{
			name:    "odd field count",
			message: "acpagent: invalid",
			fields:  []any{"agent"},
			want:    "acpagent: invalid agent",
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, formatLogMessage(test.message, test.fields...))
		})
	}
}
