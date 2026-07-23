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
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"trpc.group/trpc-go/trpc-agent-go/session/replaytest/backends"
)

func TestRunAllEmptyBackendsReturnsError(t *testing.T) {
	_, err := RunAll(context.Background(), "testdata", "light", []*backends.Backend{})
	require.Error(t, err)
}
