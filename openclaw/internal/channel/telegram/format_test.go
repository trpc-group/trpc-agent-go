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
	"os"
	"path/filepath"
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

func TestSanitizeTelegramText_HidesInternalRefs(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join("/tmp", "openclaw")
	text := "See `host:///tmp/openclaw/uploads/a.pdf` and " +
		"`artifact://out/report.pdf@1` and " +
		"`workspace://frames/f1.png`."

	got := sanitizeTelegramText(text, stateDir)
	require.NotContains(t, got, "host://")
	require.NotContains(t, got, "artifact://")
	require.NotContains(t, got, "workspace://")
	require.Contains(t, got, "`a.pdf`")
	require.Contains(t, got, "`report.pdf`")
	require.Contains(t, got, "`f1.png`")
}

func TestSanitizeTelegramText_HidesStateDirAbsolutePaths(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join("/tmp", "openclaw")
	text := "Saved to `/tmp/openclaw/uploads/chat/frame.png`."

	got := sanitizeTelegramText(text, stateDir)
	require.Equal(t, "Saved to `frame.png`.", got)
}

func TestSanitizeTelegramText_HidesPathsWithRelativeStateDir(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	cwd, err := os.Getwd()
	require.NoError(t, err)
	relRoot, err := filepath.Rel(cwd, root)
	require.NoError(t, err)

	text := "Saved to `" + filepath.Join(root, "uploads", "frame.png") + "`."
	got := sanitizeTelegramText(text, relRoot)
	require.Equal(t, "Saved to `frame.png`.", got)
}

func TestSanitizeTelegramText_HidesAbsolutePaths(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join("/tmp", "openclaw")
	text := "User path: `/tmp/other/report.pdf`."

	got := sanitizeTelegramText(text, stateDir)
	require.Equal(t, "User path: `report.pdf`.", got)
}

func TestSanitizeTelegramText_HidesRelativePathsInCodeSpans(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join("/tmp", "openclaw")
	text := "Generated: `out_pdf_split/report_page_3.pdf`."

	got := sanitizeTelegramText(text, stateDir)
	require.Equal(t, "Generated: `report_page_3.pdf`.", got)
}

func TestSanitizeTelegramText_StripsReplyDirectives(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join("/tmp", "openclaw")
	text := "已生成结果。\n" +
		"MEDIA: /tmp/openclaw/uploads/chat/frame 1.png\n" +
		"MEDIA_DIR: /tmp/openclaw/uploads/chat/out pdf\n" +
		"请查收。"

	got := sanitizeTelegramText(text, stateDir)
	require.Equal(t, "已生成结果。\n请查收。", got)
}

func TestSanitizeTelegramText_StripsAudioAsVoiceTag(t *testing.T) {
	t.Parallel()

	got := sanitizeTelegramText(
		"准备好了。\n[[audio_as_voice]]\n请查收。",
		"",
	)
	require.Equal(t, "准备好了。\n\n请查收。", got)
}

func TestSanitizeTelegramText_RewritesPlaceholderFileNames(t *testing.T) {
	t.Parallel()

	got := sanitizeTelegramText(
		"收到 `file_11.oga`，导出的图片是 `file_10.png`。",
		"",
	)
	require.Equal(
		t,
		"收到 `audio.oga`，导出的图片是 `photo.png`。",
		got,
	)
}

func TestChannel_SendTextMessage_SanitizesPaths(t *testing.T) {
	t.Parallel()

	bot := &stubBot{}
	ch := &Channel{
		bot:   bot,
		state: filepath.Join("/tmp", "openclaw"),
	}

	_, err := ch.sendTextMessage(
		context.Background(),
		tgapi.SendMessageParams{
			ChatID: 1,
			Text:   "ready: `/tmp/openclaw/uploads/chat/frame.png`",
		},
	)
	require.NoError(t, err)
	require.Len(t, bot.sent, 1)
	require.Contains(t, bot.sent[0].Text, "<code>frame.png</code>")
	require.NotContains(t, bot.sent[0].Text, "/tmp/openclaw/")
}

func TestSanitizeInternalRefToken(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"report.pdf",
		sanitizeInternalRefToken("artifact://out/report.pdf@1"),
	)
	require.Equal(
		t,
		"frame.png",
		sanitizeInternalRefToken("workspace://frames/frame.png"),
	)
	require.Equal(
		t,
		"clip.mp4",
		sanitizeInternalRefToken("host:///tmp/openclaw/clip.mp4"),
	)
	require.Equal(
		t,
		"clip.mp4",
		sanitizeInternalRefToken("file:///tmp/openclaw/clip.mp4"),
	)
	require.Empty(t, sanitizeInternalRefToken("https://example.com/x"))
}

func TestSanitizeTelegramPathToken_PreservesTrailingPunct(t *testing.T) {
	t.Parallel()

	stateDir := filepath.Join("/tmp", "openclaw")
	token := filepath.Join(
		stateDir,
		"uploads",
		"chat",
		"frame.png",
	) + ")."

	require.Equal(
		t,
		"frame.png).",
		sanitizeTelegramPathToken(token, stateDir),
	)
}

func TestSanitizeGenericPathToken(t *testing.T) {
	t.Parallel()

	require.Equal(
		t,
		"clip.mp4",
		sanitizeGenericPathToken("~/media/clip.mp4"),
	)
	require.Equal(
		t,
		"report.pdf",
		sanitizeGenericPathToken("out_pdf_split/report.pdf"),
	)
	require.Equal(
		t,
		"tmp",
		sanitizeGenericPathToken("/var/tmp/"),
	)
	require.Empty(t, sanitizeGenericPathToken("frame.png"))
	require.Empty(t, sanitizeGenericPathToken("https://example.com/x"))
	require.Empty(t, sanitizeGenericPathToken("~"))
}

func TestSanitizeStatePathTokenAndPathUnderRoot(t *testing.T) {
	t.Parallel()

	root := filepath.Join("/tmp", "openclaw")
	inRoot := filepath.Join(root, "uploads", "frame.png")
	outRoot := filepath.Join("/tmp", "elsewhere", "frame.png")

	require.Equal(t, "frame.png", sanitizeStatePathToken(inRoot, root))
	require.Empty(t, sanitizeStatePathToken(outRoot, root))
	require.True(t, pathUnderRoot(inRoot, root))
	require.False(t, pathUnderRoot(outRoot, root))
}

func TestRenderHelpers(t *testing.T) {
	t.Parallel()

	require.Equal(t, "", wrapHTMLTag(htmlTagBold, ""))
	require.Equal(t, "label", renderLink("label", ""))
	require.Equal(
		t,
		"<pre><code class=\"language-go\">x</code></pre>",
		renderCodeBlock([]byte("x\n"), "go"),
	)
	require.Equal(
		t,
		"1. first\n  second",
		prefixLines("first\nsecond", "1. "),
	)
}

func TestRenderTelegramHTMLText_CodeBlocksAndOrderedLists(t *testing.T) {
	t.Parallel()

	rendered, ok := renderTelegramHTMLText(
		"1. first\n2. second\n\n```python\nprint('hi')\n```",
	)
	require.True(t, ok)
	require.Contains(t, rendered, "1. first")
	require.Contains(t, rendered, "2. second")
	require.Contains(t, rendered, "<pre><code class=\"language-python\">")
	require.Contains(t, rendered, "print(&#39;hi&#39;)")
}

func TestRenderTelegramHTMLText_CoversMoreNodeTypes(t *testing.T) {
	t.Parallel()

	rendered, ok := renderTelegramHTMLText(
		"~~gone~~ <https://example.com>\n\n---\n\n<div>x</div>",
	)
	require.True(t, ok)
	require.Contains(t, rendered, "<s>gone</s>")
	require.Contains(
		t,
		rendered,
		"<a href=\"https://example.com\">https://example.com</a>",
	)
	require.Contains(t, rendered, "---")
	require.Contains(t, rendered, "&lt;div&gt;x&lt;/div&gt;")
}

func TestRenderTelegramHTMLText_CoversInlineHTMLAndLineBreaks(t *testing.T) {
	t.Parallel()

	rendered, ok := renderTelegramHTMLText(
		"alpha  \nbeta\n\n_setext_\n\nbefore <span>x</span> after",
	)
	require.True(t, ok)
	require.Contains(t, rendered, "alpha\nbeta")
	require.Contains(t, rendered, "<i>setext</i>")
	require.Contains(
		t,
		rendered,
		"before &lt;span&gt;x&lt;/span&gt; after",
	)
}
