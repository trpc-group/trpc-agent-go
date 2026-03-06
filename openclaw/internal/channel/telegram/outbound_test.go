//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package telegram

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveTextTargetFromSessionID(t *testing.T) {
	target, ok := ResolveTextTargetFromSessionID("telegram:dm:123")
	require.True(t, ok)
	require.Equal(t, "123", target)

	target, ok = ResolveTextTargetFromSessionID(
		"telegram:thread:100:topic:7",
	)
	require.True(t, ok)
	require.Equal(t, "100:topic:7", target)
}

func TestParseTextTarget(t *testing.T) {
	chatID, threadID, err := parseTextTarget("telegram:thread:100:topic:7")
	require.NoError(t, err)
	require.EqualValues(t, 100, chatID)
	require.Equal(t, 7, threadID)
}

func TestChannel_SendText_SplitsLongMessages(t *testing.T) {
	bot := &stubBot{}
	ch := &Channel{bot: bot}

	text := strings.Repeat("a", maxReplyRunes+5)
	err := ch.SendText(context.Background(), "100", text)
	require.NoError(t, err)

	require.Len(t, bot.sent, 2)
	require.EqualValues(t, 100, bot.sent[0].ChatID)
	require.Len(t, bot.sent[0].Text, maxReplyRunes)
	require.Len(t, bot.sent[1].Text, 5)
}
