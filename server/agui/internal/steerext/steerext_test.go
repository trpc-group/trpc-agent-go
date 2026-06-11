//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package steerext

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestQueuedUserMessageWireValues(t *testing.T) {
	require.Equal(
		t,
		"trpc_agent.steer.queued_user_message",
		QueuedUserMessageExtensionKey,
	)
	require.Equal(t, "consumed", QueuedUserMessageStatusConsumed)

	payload, err := json.Marshal(QueuedUserMessageMetadata{
		Status: QueuedUserMessageStatusConsumed,
	})
	require.NoError(t, err)
	require.JSONEq(t, `{"status":"consumed"}`, string(payload))
}
