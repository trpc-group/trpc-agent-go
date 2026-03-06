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
	"strconv"
	"strings"

	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const (
	sessionDMPrefix     = channelID + ":dm:"
	sessionThreadPrefix = channelID + ":thread:"
)

// ResolveTextTargetFromSessionID converts a Telegram session id into the
// channel-specific outbound target used by SendText.
func ResolveTextTargetFromSessionID(sessionID string) (string, bool) {
	switch {
	case strings.HasPrefix(sessionID, sessionDMPrefix):
		target := strings.TrimSpace(
			strings.TrimPrefix(sessionID, sessionDMPrefix),
		)
		return target, target != ""
	case strings.HasPrefix(sessionID, sessionThreadPrefix):
		target := strings.TrimSpace(
			strings.TrimPrefix(sessionID, sessionThreadPrefix),
		)
		return target, target != ""
	default:
		return "", false
	}
}

// SendText implements channel.TextSender for Telegram.
func (c *Channel) SendText(
	ctx context.Context,
	target string,
	text string,
) error {
	if c == nil || c.bot == nil {
		return fmt.Errorf("telegram: sender unavailable")
	}

	chatID, threadID, err := parseTextTarget(target)
	if err != nil {
		return err
	}

	for _, part := range splitRunes(text, maxReplyRunes) {
		if strings.TrimSpace(part) == "" {
			continue
		}
		_, err := c.bot.SendMessage(ctx, tgapi.SendMessageParams{
			ChatID:          chatID,
			MessageThreadID: threadID,
			Text:            part,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func parseTextTarget(target string) (int64, int, error) {
	raw := strings.TrimSpace(target)
	if raw == "" {
		return 0, 0, fmt.Errorf("telegram: empty target")
	}

	if resolved, ok := ResolveTextTargetFromSessionID(raw); ok {
		raw = resolved
	}

	chatPart := raw
	threadID := 0

	if idx := strings.Index(raw, threadTopicSep); idx >= 0 {
		chatPart = strings.TrimSpace(raw[:idx])
		topicPart := strings.TrimSpace(
			raw[idx+len(threadTopicSep):],
		)
		if topicPart == "" {
			return 0, 0, fmt.Errorf("telegram: empty topic target")
		}
		topicID, err := strconv.Atoi(topicPart)
		if err != nil {
			return 0, 0, fmt.Errorf(
				"telegram: invalid topic target: %w",
				err,
			)
		}
		threadID = topicID
	}

	chatID, err := strconv.ParseInt(chatPart, 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf(
			"telegram: invalid chat target: %w",
			err,
		)
	}
	return chatID, threadID, nil
}
