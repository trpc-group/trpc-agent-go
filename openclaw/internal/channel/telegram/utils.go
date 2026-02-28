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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/log"

	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

func buildRequestID(
	chatID int64,
	messageThreadID int,
	messageID int,
) string {
	if messageThreadID == 0 {
		return fmt.Sprintf(
			"%s%d:%d",
			requestIDPrefix,
			chatID,
			messageID,
		)
	}
	return fmt.Sprintf(
		"%s%d:%d:%d",
		requestIDPrefix,
		chatID,
		messageThreadID,
		messageID,
	)
}

func buildSessionID(fromID string, thread string) string {
	if strings.TrimSpace(thread) != "" {
		return fmt.Sprintf("%s:thread:%s", channelID, thread)
	}
	return fmt.Sprintf("%s:dm:%s", channelID, fromID)
}

func parseDMPolicy(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return defaultDMPolicy, nil
	}
	switch v {
	case dmPolicyDisabled,
		dmPolicyOpen,
		dmPolicyAllowlist,
		dmPolicyPairing:
		return v, nil
	default:
		return "", fmt.Errorf("telegram: unsupported dm policy: %s", raw)
	}
}

func parseGroupPolicy(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return defaultGroupPolicy, nil
	}
	switch v {
	case groupPolicyDisabled,
		groupPolicyOpen,
		groupPolicyAllowlist:
		return v, nil
	default:
		return "", fmt.Errorf(
			"telegram: unsupported group policy: %s",
			raw,
		)
	}
}

func splitRunes(text string, maxRunes int) []string {
	if maxRunes <= 0 {
		return []string{text}
	}
	runes := []rune(text)
	if len(runes) <= maxRunes {
		return []string{text}
	}

	out := make([]string, 0, (len(runes)/maxRunes)+1)
	for len(runes) > 0 {
		if len(runes) <= maxRunes {
			out = append(out, string(runes))
			break
		}

		cut := splitIndex(runes[:maxRunes], maxRunes)
		out = append(out, string(runes[:cut]))
		runes = runes[cut:]
	}
	return out
}

func splitIndex(segment []rune, maxRunes int) int {
	if len(segment) <= 1 {
		return len(segment)
	}

	min := maxRunes / 2
	if min < 1 {
		min = 1
	}

	for i := len(segment) - 1; i > 0; i-- {
		if segment[i] == '\n' && segment[i-1] == '\n' {
			if i+1 >= min {
				return i + 1
			}
		}
	}
	for i := len(segment) - 1; i >= 0; i-- {
		if segment[i] == '\n' {
			if i+1 >= min {
				return i + 1
			}
		}
	}
	for i := len(segment) - 1; i >= 0; i-- {
		if segment[i] == ' ' || segment[i] == '\t' {
			if i+1 >= min {
				return i + 1
			}
		}
	}
	return len(segment)
}

func resolveStateDir(stateDir string) (string, error) {
	trimmed := strings.TrimSpace(stateDir)
	if trimmed != "" {
		return trimmed, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(
		home,
		defaultStateRootDir,
		defaultStateAppName,
	), nil
}

func newOffsetStore(
	stateDir string,
	bot BotInfo,
) (*tgapi.FileOffsetStore, error) {
	if strings.TrimSpace(stateDir) == "" {
		return nil, errors.New("telegram: empty state dir")
	}
	filename := fmt.Sprintf(
		"%s%s%s",
		offsetStoreFilePrefix,
		offsetKey(bot),
		offsetStoreFileSuffix,
	)
	path := filepath.Join(stateDir, offsetStoreDir, filename)
	return tgapi.NewFileOffsetStore(path)
}

func offsetKey(bot BotInfo) string {
	if strings.TrimSpace(bot.Username) != "" {
		return sanitizeFileToken(bot.Username)
	}
	if bot.ID != 0 {
		return strconv.FormatInt(bot.ID, 10)
	}
	return defaultOffsetKey
}

func sanitizeFileToken(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultOffsetKey
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		if r >= 'a' && r <= 'z' ||
			r >= 'A' && r <= 'Z' ||
			r >= '0' && r <= '9' ||
			r == '.' || r == '_' || r == '-' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('_')
	}
	return b.String()
}

// PairingStorePath returns the path used for storing DM pairing state.
func PairingStorePath(stateDir string, bot BotInfo) (string, error) {
	if strings.TrimSpace(stateDir) == "" {
		return "", errors.New("telegram: empty state dir")
	}
	filename := pairingStoreFilePrefix +
		offsetKey(bot) +
		pairingStoreFileSuffix
	return filepath.Join(stateDir, offsetStoreDir, filename), nil
}

func (l *laneLocker) withLockErr(key string, fn func() error) error {
	if fn == nil {
		return nil
	}
	var err error
	l.withLock(key, func() {
		err = fn()
	})
	return err
}

func (c *Channel) reply(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	text string,
) {
	_, err := c.bot.SendMessage(ctx, tgapi.SendMessageParams{
		ChatID:           chatID,
		MessageThreadID:  messageThreadID,
		ReplyToMessageID: replyTo,
		Text:             text,
	})
	if err != nil {
		log.WarnfContext(ctx, "telegram: send message: %v", err)
	}
}

const (
	cancelNoopMessage   = "No running request to cancel."
	cancelFailedMessage = "Cancel failed."
	cancelOKMessage     = "Canceled."
)

func (c *Channel) handleCancelCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	sessionID string,
) error {
	requestID := c.inflight.Get(sessionID)
	if strings.TrimSpace(requestID) == "" {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			cancelNoopMessage,
		)
		return nil
	}

	canceled, err := c.gw.Cancel(ctx, requestID)
	if err != nil {
		log.WarnfContext(ctx, "telegram: cancel: %v", err)
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			cancelFailedMessage,
		)
		return nil
	}
	if !canceled {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			cancelNoopMessage,
		)
		return nil
	}

	c.reply(
		ctx,
		chatID,
		messageThreadID,
		replyTo,
		cancelOKMessage,
	)
	return nil
}
