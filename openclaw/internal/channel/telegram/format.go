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
	"strings"

	"github.com/yuin/goldmark"
	gast "github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	extast "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	gtext "github.com/yuin/goldmark/text"

	"trpc.group/trpc-go/trpc-agent-go/log"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
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
)

func (c *Channel) sendTextMessage(
	ctx context.Context,
	params tgapi.SendMessageParams,
) (tgapi.Message, error) {
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
