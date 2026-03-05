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
	"net/http"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"

	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const (
	streamingOff      = "off"
	streamingBlock    = "block"
	streamingProgress = "progress"

	defaultStreamingMode = streamingOff
)

const (
	chatActionTyping = "typing"

	processingMessage = "Processing..."

	progressInterval = 2 * time.Second

	progressEditIntervalFast     = progressInterval
	progressEditIntervalMedium   = 10 * time.Second
	progressEditIntervalSlow     = 30 * time.Second
	progressEditIntervalVerySlow = time.Minute

	progressEditAfterMedium   = time.Minute
	progressEditAfterSlow     = 10 * time.Minute
	progressEditAfterVerySlow = 30 * time.Minute
)

func parseStreamingMode(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return defaultStreamingMode, nil
	}
	switch v {
	case streamingOff, streamingBlock, streamingProgress:
		return v, nil
	default:
		return "", fmt.Errorf(
			"telegram: unsupported streaming mode: %s",
			raw,
		)
	}
}

func (c *Channel) callGatewayAndReply(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	fromID string,
	thread string,
	requestID string,
	msg tgapi.Message,
) error {
	mode := strings.TrimSpace(c.streamingMode)
	if mode == "" {
		mode = defaultStreamingMode
	}
	preview, hasPreview := c.sendPreviewMessage(
		ctx,
		chatID,
		messageThreadID,
		replyTo,
		mode,
	)

	progressCancel, progressWG := c.startProgressLoop(
		ctx,
		chatID,
		messageThreadID,
		preview,
		hasPreview,
		mode,
	)

	req, err := c.buildGatewayRequest(ctx, fromID, thread, requestID, msg)
	if err != nil {
		if progressCancel != nil {
			progressCancel()
		}
		if progressWG != nil {
			progressWG.Wait()
		}

		userMsg := "Failed to process message."
		var uerr *userError
		if errors.As(err, &uerr) &&
			strings.TrimSpace(uerr.userMessage) != "" {
			userMsg = uerr.userMessage
		}

		if hasPreview {
			_ = c.editPreview(ctx, chatID, preview.MessageID, userMsg)
		} else {
			c.reply(ctx, chatID, messageThreadID, replyTo, userMsg)
		}

		if errors.As(err, &uerr) {
			return nil
		}
		return err
	}

	rsp, err := c.gw.SendMessage(ctx, req)

	if progressCancel != nil {
		progressCancel()
	}
	if progressWG != nil {
		progressWG.Wait()
	}

	if err != nil {
		if hasPreview {
			msg := "Failed to process message."
			if rsp.Error != nil &&
				strings.TrimSpace(rsp.Error.Message) != "" {
				msg = rsp.Error.Message
			}
			_ = c.editPreview(ctx, chatID, preview.MessageID, msg)
		}
		if rsp.StatusCode >= http.StatusBadRequest &&
			rsp.StatusCode < http.StatusInternalServerError {
			log.WarnfContext(
				ctx,
				"telegram: gateway rejected: %v",
				err,
			)
			return nil
		}
		return err
	}
	if rsp.Ignored {
		if hasPreview {
			_ = c.editPreview(ctx, chatID, preview.MessageID, "Ignored.")
		}
		return nil
	}
	if strings.TrimSpace(rsp.Reply) == "" {
		if hasPreview {
			_ = c.editPreview(
				ctx,
				chatID,
				preview.MessageID,
				"No reply.",
			)
		}
		return nil
	}

	parts := splitRunes(rsp.Reply, maxReplyRunes)
	if !hasPreview || mode == streamingOff {
		c.sendReplyParts(ctx, chatID, messageThreadID, replyTo, parts)
		return nil
	}

	if !c.editPreview(ctx, chatID, preview.MessageID, parts[0]) {
		c.sendReplyParts(ctx, chatID, messageThreadID, replyTo, parts)
		return nil
	}

	for _, part := range parts[1:] {
		_, err := c.bot.SendMessage(ctx, tgapi.SendMessageParams{
			ChatID:          chatID,
			MessageThreadID: messageThreadID,
			Text:            part,
		})
		if err != nil {
			log.WarnfContext(ctx, "telegram: send message: %v", err)
			return nil
		}
	}
	return nil
}

func (c *Channel) sendPreviewMessage(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	mode string,
) (tgapi.Message, bool) {
	if mode == streamingOff || c.bot == nil {
		return tgapi.Message{}, false
	}

	_ = c.bot.SendChatAction(ctx, tgapi.SendChatActionParams{
		ChatID:          chatID,
		MessageThreadID: messageThreadID,
		Action:          chatActionTyping,
	})

	msg, err := c.bot.SendMessage(ctx, tgapi.SendMessageParams{
		ChatID:           chatID,
		MessageThreadID:  messageThreadID,
		ReplyToMessageID: replyTo,
		Text:             processingMessage,
	})
	if err != nil {
		log.WarnfContext(ctx, "telegram: send message: %v", err)
		return tgapi.Message{}, false
	}
	return msg, true
}

func (c *Channel) startProgressLoop(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	preview tgapi.Message,
	hasPreview bool,
	mode string,
) (context.CancelFunc, *sync.WaitGroup) {
	if mode != streamingProgress || !hasPreview {
		return nil, nil
	}

	progressCtx, cancel := context.WithCancel(ctx)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.progressLoop(
			progressCtx,
			chatID,
			messageThreadID,
			preview.MessageID,
		)
	}()
	return cancel, wg
}

func (c *Channel) progressLoop(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	messageID int,
) {
	start := time.Now()
	lastEditAt := start.Add(-progressEditIntervalFast)
	ticker := time.NewTicker(progressInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		_ = c.bot.SendChatAction(ctx, tgapi.SendChatActionParams{
			ChatID:          chatID,
			MessageThreadID: messageThreadID,
			Action:          chatActionTyping,
		})

		now := time.Now()
		elapsed := now.Sub(start).Round(time.Second)
		if now.Sub(lastEditAt) < progressEditInterval(elapsed) {
			continue
		}
		lastEditAt = now

		_ = c.editPreview(
			ctx,
			chatID,
			messageID,
			fmt.Sprintf("Processing... (%s)", elapsed),
		)
	}
}

func progressEditInterval(elapsed time.Duration) time.Duration {
	if elapsed >= progressEditAfterVerySlow {
		return progressEditIntervalVerySlow
	}
	if elapsed >= progressEditAfterSlow {
		return progressEditIntervalSlow
	}
	if elapsed >= progressEditAfterMedium {
		return progressEditIntervalMedium
	}
	return progressEditIntervalFast
}

func (c *Channel) editPreview(
	ctx context.Context,
	chatID int64,
	messageID int,
	text string,
) bool {
	_, err := c.bot.EditMessageText(ctx, tgapi.EditMessageTextParams{
		ChatID:    chatID,
		MessageID: messageID,
		Text:      text,
	})
	if err != nil {
		log.WarnfContext(ctx, "telegram: edit message: %v", err)
		return false
	}
	return true
}

func (c *Channel) sendReplyParts(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	parts []string,
) {
	for i, part := range parts {
		replyID := 0
		if i == 0 {
			replyID = replyTo
		}
		_, err := c.bot.SendMessage(ctx, tgapi.SendMessageParams{
			ChatID:           chatID,
			MessageThreadID:  messageThreadID,
			ReplyToMessageID: replyID,
			Text:             part,
		})
		if err != nil {
			log.WarnfContext(ctx, "telegram: send message: %v", err)
			return
		}
	}
}
