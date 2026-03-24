//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreserveGraphCompletionCapture(t *testing.T) {
	base := WithGraphCompletionCapture(context.Background())
	next := context.WithValue(context.Background(), "key", "value")
	preserved := PreserveGraphCompletionCapture(base, next)
	require.True(t, ShouldCaptureGraphCompletion(preserved))
	require.Equal(t, "value", preserved.Value("key"))
	explicit := WithoutGraphCompletionCapture(context.Background())
	require.False(t, ShouldCaptureGraphCompletion(
		PreserveGraphCompletionCapture(base, explicit),
	))
}
