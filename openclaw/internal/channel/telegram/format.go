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
	"fmt"
	"html"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	gtext "github.com/yuin/goldmark/text"

	"trpc.group/trpc-go/trpc-agent-go/internal/fileref"
	"trpc.group/trpc-go/trpc-agent-go/log"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	htmlTagAnchor     = "a"
	htmlTagBlockquote = "blockquote"
	htmlTagBold       = "b"
	htmlTagCode       = "code"
	htmlTagItalic     = "i"
	htmlTagPre        = "pre"
	htmlTagStrike     = "s"

	listIndent  = "  "
	listMarker  = "- "
	doubleBreak = "\n\n"
	lineBreak   = "\n"

	pathTokenTrailingPunct = ".,:;!?)]}"

	inlineCodeDelimiter = "`"

	replyDirectiveMedia    = "MEDIA:"
	replyDirectiveMediaDir = "MEDIA_DIR:"

	audioAsVoiceTag = "[[audio_as_voice]]"
)

var telegramPathTokenRE = regexp.MustCompile(
	`(?:artifact|workspace|host|file)://[^\s<>()\[\]{}"'` +
		"`" + `]+|/[^\s<>()\[\]{}"'` + "`" + `]+`,
)

var telegramInlineCodeRE = regexp.MustCompile(
	"`([^`\n]+)`",
)

var telegramPlaceholderNameRE = regexp.MustCompile(
	`\bfile_\d+(?:\.[A-Za-z0-9]+)?\b`,
)

func (c *Channel) sendTextMessage(
	ctx context.Context,
	params tgapi.SendMessageParams,
) (tgapi.Message, error) {
	params.Text = sanitizeTelegramText(params.Text, c.state)
	formatted, ok := renderTelegramHTMLText(params.Text)
	if ok {
		richParams := params
		richParams.Text = formatted
		richParams.ParseMode = tgapi.ParseModeHTML

		msg, err := c.bot.SendMessage(ctx, richParams)
		if err == nil || !tgapi.IsEntityParseError(err) {
			return msg, err
		}
		log.WarnfContext(
			ctx,
			"telegram: html parse failed, retry plain text: %v",
			err,
		)
	}

	plainParams := params
	plainParams.ParseMode = ""
	return c.bot.SendMessage(ctx, plainParams)
}

func (c *Channel) editTextMessage(
	ctx context.Context,
	params tgapi.EditMessageTextParams,
) (tgapi.Message, error) {
	params.Text = sanitizeTelegramText(params.Text, c.state)
	formatted, ok := renderTelegramHTMLText(params.Text)
	if ok {
		richParams := params
		richParams.Text = formatted
		richParams.ParseMode = tgapi.ParseModeHTML

		msg, err := c.bot.EditMessageText(ctx, richParams)
		if err == nil || !tgapi.IsEntityParseError(err) {
			return msg, err
		}
		log.WarnfContext(
			ctx,
			"telegram: html parse failed on edit, retry plain text: %v",
			err,
		)
	}

	plainParams := params
	plainParams.ParseMode = ""
	return c.bot.EditMessageText(ctx, plainParams)
}

func renderTelegramHTMLText(markdown string) (string, bool) {
	trimmed := strings.TrimSpace(markdown)
	if trimmed == "" {
		return "", false
	}

	md := goldmark.New(
		goldmark.WithExtensions(
			extension.Linkify,
			extension.Strikethrough,
		),
		goldmark.WithParserOptions(
			parser.WithAutoHeadingID(),
		),
	)

	source := []byte(strings.ReplaceAll(trimmed, "\r\n", "\n"))
	root := md.Parser().Parse(gtext.NewReader(source))
	rendered := strings.TrimSpace(renderBlockChildren(root, source))
	return rendered, rendered != ""
}

func sanitizeTelegramText(text string, stateDir string) string {
	if strings.TrimSpace(text) == "" {
		return text
	}
	text = stripTelegramReplyDirectives(text)
	text = stripAudioAsVoiceTag(text)
	root := cleanStateRoot(stateDir)
	sanitized := sanitizeTelegramInlineCodePaths(text, root)
	sanitized = telegramPathTokenRE.ReplaceAllStringFunc(
		sanitized,
		func(token string) string {
			return sanitizeTelegramPathToken(token, root)
		},
	)
	return sanitizeTelegramPlaceholderNames(sanitized)
}

func stripTelegramReplyDirectives(text string) string {
	lines := strings.Split(text, lineBreak)
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if isTelegramReplyDirectiveLine(line) {
			continue
		}
		out = append(out, line)
	}
	return strings.TrimSpace(strings.Join(out, lineBreak))
}

func isTelegramReplyDirectiveLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	upper := strings.ToUpper(trimmed)
	return strings.HasPrefix(upper, replyDirectiveMedia) ||
		strings.HasPrefix(upper, replyDirectiveMediaDir)
}

func stripAudioAsVoiceTag(text string) string {
	return strings.ReplaceAll(text, audioAsVoiceTag, "")
}

func hasAudioAsVoiceTag(text string) bool {
	return strings.Contains(text, audioAsVoiceTag)
}

func sanitizeTelegramPlaceholderNames(text string) string {
	return telegramPlaceholderNameRE.ReplaceAllStringFunc(
		text,
		func(token string) string {
			name := uploads.PreferredName(token, "")
			if name == "" {
				return token
			}
			return name
		},
	)
}

func sanitizeTelegramPathToken(token string, stateRoot string) string {
	core, suffix := splitTrailingPathPunct(token)
	if core == "" {
		return token
	}
	if name := sanitizeInternalRefToken(core); name != "" {
		return name + suffix
	}
	if name := sanitizeStatePathToken(core, stateRoot); name != "" {
		return name + suffix
	}
	if name := sanitizeGenericPathToken(core); name != "" {
		return name + suffix
	}
	return token
}

func splitTrailingPathPunct(token string) (string, string) {
	end := len(token)
	for end > 0 {
		last := token[end-1]
		if !strings.ContainsRune(pathTokenTrailingPunct, rune(last)) {
			break
		}
		end--
	}
	return token[:end], token[end:]
}

func sanitizeInternalRefToken(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, fileref.ArtifactPrefix) ||
		strings.HasPrefix(trimmed, fileref.WorkspacePrefix) {
		return fileref.DisplayName(trimmed)
	}
	if path, ok := uploads.PathFromHostRef(trimmed); ok {
		return filepath.Base(path)
	}
	if strings.HasPrefix(trimmed, fileURLPrefix) {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return ""
		}
		if parsed.Path == "" {
			return ""
		}
		return filepath.Base(parsed.Path)
	}
	return ""
}

func sanitizeStatePathToken(token string, stateRoot string) string {
	if stateRoot == "" || !filepath.IsAbs(token) {
		return ""
	}
	clean := filepath.Clean(token)
	if !pathUnderRoot(clean, stateRoot) {
		return ""
	}
	base := filepath.Base(clean)
	if base == "." || base == string(filepath.Separator) || base == ".." {
		return ""
	}
	return base
}

func sanitizeTelegramInlineCodePaths(
	text string,
	stateRoot string,
) string {
	return telegramInlineCodeRE.ReplaceAllStringFunc(
		text,
		func(span string) string {
			if len(span) < 2 {
				return span
			}
			token := strings.TrimSuffix(
				strings.TrimPrefix(span, inlineCodeDelimiter),
				inlineCodeDelimiter,
			)
			sanitized := sanitizeTelegramPathToken(token, stateRoot)
			if sanitized == token {
				return span
			}
			return inlineCodeDelimiter + sanitized + inlineCodeDelimiter
		},
	)
}

func sanitizeGenericPathToken(token string) string {
	trimmed := strings.TrimSpace(token)
	if trimmed == "" || strings.Contains(trimmed, "://") {
		return ""
	}
	if trimmed == "~" {
		return ""
	}
	if !filepath.IsAbs(trimmed) &&
		!strings.HasPrefix(trimmed, "~/") &&
		!strings.Contains(trimmed, "/") {
		return ""
	}
	base := filepath.Base(strings.TrimRight(trimmed, "/"))
	if base == "." || base == string(filepath.Separator) || base == ".." {
		return ""
	}
	return base
}

func cleanStateRoot(stateDir string) string {
	trimmed := strings.TrimSpace(stateDir)
	if trimmed == "" {
		return ""
	}
	if filepath.IsAbs(trimmed) {
		return filepath.Clean(trimmed)
	}
	abs, err := filepath.Abs(trimmed)
	if err != nil {
		return filepath.Clean(trimmed)
	}
	return filepath.Clean(abs)
}

func pathUnderRoot(path string, root string) bool {
	if path == "" || root == "" {
		return false
	}
	if path == root {
		return true
	}
	prefix := root + string(filepath.Separator)
	return strings.HasPrefix(path, prefix)
}

func renderBlockChildren(node gast.Node, source []byte) string {
	var parts []string
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		part := strings.TrimSpace(renderBlock(child, source))
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, doubleBreak)
}

func renderBlock(node gast.Node, source []byte) string {
	switch n := node.(type) {
	case *gast.Paragraph:
		return renderInlineChildren(n, source)
	case *gast.TextBlock:
		return renderInlineChildren(n, source)
	case *gast.Heading:
		text := renderInlineChildren(n, source)
		if text == "" {
			return ""
		}
		return wrapHTMLTag(htmlTagBold, text)
	case *gast.Blockquote:
		text := renderBlockChildren(n, source)
		if text == "" {
			return ""
		}
		return wrapHTMLTag(htmlTagBlockquote, text)
	case *gast.List:
		return renderList(n, source)
	case *gast.CodeBlock:
		return renderCodeBlock(n.Lines().Value(source), "")
	case *gast.FencedCodeBlock:
		return renderCodeBlock(
			n.Lines().Value(source),
			string(n.Language(source)),
		)
	case *gast.HTMLBlock:
		return escapeHTML(string(n.Lines().Value(source)))
	case *gast.ThematicBreak:
		return escapeHTML("---")
	default:
		if node.HasChildren() {
			return renderBlockChildren(node, source)
		}
		return ""
	}
}

func renderList(list *gast.List, source []byte) string {
	var parts []string
	index := list.Start
	if index <= 0 {
		index = 1
	}

	for child := list.FirstChild(); child != nil; child = child.NextSibling() {
		item, ok := child.(*gast.ListItem)
		if !ok {
			continue
		}
		marker := listMarker
		if list.IsOrdered() {
			marker = fmt.Sprintf("%d. ", index)
			index++
		}
		text := renderListItem(item, source)
		if text == "" {
			continue
		}
		parts = append(parts, prefixLines(text, marker))
	}
	return strings.Join(parts, lineBreak)
}

func renderListItem(item *gast.ListItem, source []byte) string {
	var parts []string
	for child := item.FirstChild(); child != nil; child = child.NextSibling() {
		part := strings.TrimSpace(renderBlock(child, source))
		if part == "" {
			continue
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, lineBreak)
}

func prefixLines(text string, prefix string) string {
	lines := strings.Split(text, lineBreak)
	for i, line := range lines {
		if i == 0 {
			lines[i] = prefix + line
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		lines[i] = listIndent + line
	}
	return strings.Join(lines, lineBreak)
}

func renderInlineChildren(node gast.Node, source []byte) string {
	var builder strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		builder.WriteString(renderInline(child, source))
	}
	return builder.String()
}

func renderInline(node gast.Node, source []byte) string {
	switch n := node.(type) {
	case *gast.Text:
		return renderTextNode(n, source)
	case *gast.String:
		return escapeHTML(string(n.Value))
	case *gast.CodeSpan:
		return wrapHTMLTag(
			htmlTagCode,
			escapeHTML(renderCodeSpanText(n, source)),
		)
	case *gast.Emphasis:
		return renderEmphasis(n, source)
	case *extast.Strikethrough:
		return wrapHTMLTag(
			htmlTagStrike,
			renderInlineChildren(n, source),
		)
	case *gast.Link:
		return renderLink(
			renderInlineChildren(n, source),
			string(n.Destination),
		)
	case *gast.AutoLink:
		label := string(n.Label(source))
		return renderLink(escapeHTML(label), string(n.URL(source)))
	case *gast.RawHTML:
		return escapeHTML(string(n.Segments.Value(source)))
	default:
		if node.HasChildren() {
			return renderInlineChildren(node, source)
		}
		return ""
	}
}

func renderTextNode(node *gast.Text, source []byte) string {
	text := escapeHTML(string(node.Segment.Value(source)))
	switch {
	case node.HardLineBreak():
		return text + lineBreak
	case node.SoftLineBreak():
		return text + lineBreak
	default:
		return text
	}
}

func renderCodeSpanText(node *gast.CodeSpan, source []byte) string {
	var builder strings.Builder
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		switch n := child.(type) {
		case *gast.Text:
			builder.Write(n.Segment.Value(source))
		case *gast.String:
			builder.Write(n.Value)
		default:
			builder.Write(child.Text(source))
		}
	}
	return builder.String()
}

func renderEmphasis(node *gast.Emphasis, source []byte) string {
	tag := htmlTagItalic
	if node.Level >= 2 {
		tag = htmlTagBold
	}
	return wrapHTMLTag(tag, renderInlineChildren(node, source))
}

func renderLink(label string, destination string) string {
	href := strings.TrimSpace(destination)
	if href == "" {
		return label
	}
	return fmt.Sprintf(
		"<%s href=\"%s\">%s</%s>",
		htmlTagAnchor,
		escapeHTML(href),
		label,
		htmlTagAnchor,
	)
}

func renderCodeBlock(code []byte, language string) string {
	body := escapeHTML(strings.TrimRight(string(code), lineBreak))
	lang := strings.TrimSpace(language)
	if lang == "" {
		return fmt.Sprintf(
			"<%s><%s>%s</%s></%s>",
			htmlTagPre,
			htmlTagCode,
			body,
			htmlTagCode,
			htmlTagPre,
		)
	}
	return fmt.Sprintf(
		"<%s><%s class=\"language-%s\">%s</%s></%s>",
		htmlTagPre,
		htmlTagCode,
		escapeHTML(lang),
		body,
		htmlTagCode,
		htmlTagPre,
	)
}

func wrapHTMLTag(tag string, text string) string {
	if text == "" {
		return ""
	}
	return fmt.Sprintf("<%s>%s</%s>", tag, text, tag)
}

func escapeHTML(text string) string {
	return html.EscapeString(text)
}
