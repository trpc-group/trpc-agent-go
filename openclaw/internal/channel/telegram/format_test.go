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
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

func TestRenderTelegramHTMLText(t *testing.T) {
	t.Parallel()

	rendered, ok := renderTelegramHTMLText(
		"# Title\n\n**bold** `code` [link](https://example.com)\n\n" +
			"> quote\n\n- item",
	)
	require.True(t, ok)
	require.Contains(t, rendered, "<b>Title</b>")
	require.Contains(t, rendered, "<b>bold</b>")
	require.Contains(t, rendered, "<code>code</code>")
	require.Contains(
		t,
		rendered,
		"<a href=\"https://example.com\">link</a>",
	)
	require.Contains(t, rendered, "<blockquote>quote</blockquote>")
	require.Contains(t, rendered, "- item")
}

func TestRenderTelegramHTMLText_EscapesRawHTML(t *testing.T) {
	t.Parallel()

	rendered, ok := renderTelegramHTMLText("<b>unsafe</b>")
	require.True(t, ok)
	require.Equal(t, "&lt;b&gt;unsafe&lt;/b&gt;", rendered)
}

func TestChannel_SendTextMessageFallsBackToPlainText(t *testing.T) {
	t.Parallel()

	bot := &stubBot{
		sendHook: func(params tgapi.SendMessageParams) error {
			if params.ParseMode == tgapi.ParseModeHTML {
				return errors.New(
					"telegram: api error 400: can't parse entities",
				)
			}
			return nil
		},
	}
	ch := &Channel{bot: bot}

	_, err := ch.sendTextMessage(
		context.Background(),
		tgapi.SendMessageParams{
			ChatID: 1,
			Text:   "**bold**",
		},
	)
	require.NoError(t, err)
	require.Len(t, bot.sent, 2)
	require.Equal(t, tgapi.ParseModeHTML, bot.sent[0].ParseMode)
	require.Equal(t, "<b>bold</b>", bot.sent[0].Text)
	require.Empty(t, bot.sent[1].ParseMode)
	require.Equal(t, "**bold**", bot.sent[1].Text)
}

func TestChannel_EditTextMessageFallsBackToPlainText(t *testing.T) {
	t.Parallel()

	bot := &stubBot{
		editHook: func(params tgapi.EditMessageTextParams) error {
			if params.ParseMode == tgapi.ParseModeHTML {
				return errors.New(
					"telegram: api error 400: find end of the entity",
				)
			}
			return nil
		},
	}
	ch := &Channel{bot: bot}

	_, err := ch.editTextMessage(
		context.Background(),
		tgapi.EditMessageTextParams{
			ChatID:    1,
			MessageID: 2,
			Text:      "`code`",
		},
	)
	require.NoError(t, err)
	require.Len(t, bot.edits, 2)
	require.Equal(t, tgapi.ParseModeHTML, bot.edits[0].ParseMode)
	require.Equal(t, "<code>code</code>", bot.edits[0].Text)
	require.Empty(t, bot.edits[1].ParseMode)
	require.Equal(t, "`code`", bot.edits[1].Text)
}
