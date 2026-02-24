//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package telegram

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestIsGroupChat(t *testing.T) {
	t.Parallel()

	require.False(t, IsGroupChat(chatTypePrivate))
	require.True(t, IsGroupChat(chatTypeGroup))
	require.True(t, IsGroupChat(chatTypeSuperGroup))
}
