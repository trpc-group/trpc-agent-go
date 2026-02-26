//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRun_UnknownFlagReturnsUsageCode(t *testing.T) {
	t.Parallel()

	require.Equal(t, 2, run([]string{"-unknown-flag"}))
}
