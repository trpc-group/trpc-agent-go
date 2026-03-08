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
	"strconv"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
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

type telegramMessageSummary struct {
	ChatID          int64  `json:"chat_id"`
	MessageThreadID int    `json:"message_thread_id,omitempty"`
	ReplyTo         int    `json:"reply_to,omitempty"`
	IncomingReplyTo int    `json:"incoming_reply_to,omitempty"`
	FromID          string `json:"from_id,omitempty"`
	Thread          string `json:"thread,omitempty"`
	RequestID       string `json:"request_id,omitempty"`
	SessionID       string `json:"session_id,omitempty"`
	Text            string `json:"text,omitempty"`
	Caption         string `json:"caption,omitempty"`

	HasPhoto     bool `json:"has_photo,omitempty"`
	HasDocument  bool `json:"has_document,omitempty"`
	HasAudio     bool `json:"has_audio,omitempty"`
	HasVoice     bool `json:"has_voice,omitempty"`
	HasVideo     bool `json:"has_video,omitempty"`
	HasAnimation bool `json:"has_animation,omitempty"`
	HasVideoNote bool `json:"has_video_note,omitempty"`

	ReplyHasPhoto     bool `json:"reply_has_photo,omitempty"`
	ReplyHasDocument  bool `json:"reply_has_document,omitempty"`
	ReplyHasAudio     bool `json:"reply_has_audio,omitempty"`
	ReplyHasVoice     bool `json:"reply_has_voice,omitempty"`
	ReplyHasVideo     bool `json:"reply_has_video,omitempty"`
	ReplyHasAnimation bool `json:"reply_has_animation,omitempty"`
	ReplyHasVideoNote bool `json:"reply_has_video_note,omitempty"`
}

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
	sessionID string,
	requestID string,
	msg tgapi.Message,
) (err error) {
	traceStartedAt := time.Time{}
	traceStatus := ""
	traceErr := ""

	trace := debugrecorder.TraceFromContext(ctx)
	if trace == nil {
		rec := debugrecorder.RecorderFromContext(ctx)
		if rec != nil {
			traceStartedAt = time.Now()
			t, recErr := rec.Start(debugrecorder.TraceStart{
				Channel:   channelID,
				UserID:    fromID,
				SessionID: sessionID,
				Thread:    thread,
				MessageID: strconv.Itoa(msg.MessageID),
				RequestID: requestID,
				Source:    channelID,
			})
			if recErr == nil && t != nil {
				trace = t
				ctx = debugrecorder.WithTrace(ctx, trace)
				traceStatus = "ok"
				_ = trace.Record(
					debugrecorder.KindTelegramMessage,
					telegramMessageSummary{
						ChatID:          chatID,
						MessageThreadID: messageThreadID,
						ReplyTo:         replyTo,
						IncomingReplyTo: incomingReplyTo(msg),
						FromID:          fromID,
						Thread:          thread,
						RequestID:       requestID,
						SessionID:       sessionID,
						Text:            msg.Text,
						Caption:         msg.Caption,
						HasPhoto:        len(msg.Photo) > 0,
						HasDocument:     msg.Document != nil,
						HasAudio:        msg.Audio != nil,
						HasVoice:        msg.Voice != nil,
						HasVideo:        msg.Video != nil,
						HasAnimation:    msg.Animation != nil,
						HasVideoNote:    msg.VideoNote != nil,
						ReplyHasPhoto: replyHasPhoto(
							msg.ReplyToMessage,
						),
						ReplyHasDocument: replyHasDocument(
							msg.ReplyToMessage,
						),
						ReplyHasAudio: replyHasAudio(
							msg.ReplyToMessage,
						),
						ReplyHasVoice: replyHasVoice(
							msg.ReplyToMessage,
						),
						ReplyHasVideo: replyHasVideo(
							msg.ReplyToMessage,
						),
						ReplyHasAnimation: replyHasAnimation(
							msg.ReplyToMessage,
						),
						ReplyHasVideoNote: replyHasVideoNote(
							msg.ReplyToMessage,
						),
					},
				)
				defer func() {
					end := debugrecorder.TraceEnd{
						Duration: time.Since(traceStartedAt),
						Status:   traceStatus,
						Error:    traceErr,
					}
					if err != nil && end.Error == "" {
						end.Status = "error"
						end.Error = err.Error()
					}
					_ = trace.Close(end)
				}()
			}
		}
	}

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

	req, err := c.buildGatewayRequest(
		ctx,
		fromID,
		thread,
		sessionID,
		requestID,
		msg,
	)
	if err != nil {
		if trace != nil {
			traceStatus = "error"
			traceErr = err.Error()
			_ = trace.RecordError(err)
		}
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
		if trace != nil {
			traceStatus = "error"
			traceErr = err.Error()
			_ = trace.RecordError(err)
		}
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
		if trace != nil {
			traceStatus = "error"
			traceErr = "empty reply"
		}
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

	replyFiles := c.collectReplyFiles(rsp.Reply, fromID, sessionID)
	if hasAudioAsVoiceTag(rsp.Reply) {
		replyFiles = markReplyFilesAsVoice(replyFiles)
	}
	replyFiles = c.filterAlreadySentReplyFiles(
		requestID,
		chatID,
		messageThreadID,
		replyFiles,
	)
	parts := splitRunes(rsp.Reply, maxReplyRunes)
	if !hasPreview || mode == streamingOff {
		c.sendReplyParts(ctx, chatID, messageThreadID, replyTo, parts)
		c.sendReplyFiles(
			ctx,
			chatID,
			messageThreadID,
			fromID,
			sessionID,
			replyFiles,
		)
		return nil
	}

	if !c.editPreview(ctx, chatID, preview.MessageID, parts[0]) {
		c.sendReplyParts(ctx, chatID, messageThreadID, replyTo, parts)
		c.sendReplyFiles(
			ctx,
			chatID,
			messageThreadID,
			fromID,
			sessionID,
			replyFiles,
		)
		return nil
	}

	for _, part := range parts[1:] {
		_, err := c.sendTextMessage(ctx, tgapi.SendMessageParams{
			ChatID:          chatID,
			MessageThreadID: messageThreadID,
			Text:            part,
		})
		if err != nil {
			log.WarnfContext(ctx, "telegram: send message: %v", err)
			return nil
		}
	}
	c.sendReplyFiles(
		ctx,
		chatID,
		messageThreadID,
		fromID,
		sessionID,
		replyFiles,
	)
	return nil
}

func markReplyFilesAsVoice(
	files []channel.OutboundFile,
) []channel.OutboundFile {
	if len(files) == 0 {
		return files
	}
	out := make([]channel.OutboundFile, 0, len(files))
	for _, file := range files {
		file.AsVoice = true
		out = append(out, file)
	}
	return out
}

func (c *Channel) filterAlreadySentReplyFiles(
	requestID string,
	chatID int64,
	threadID int,
	files []channel.OutboundFile,
) []channel.OutboundFile {
	if len(files) == 0 || c == nil || c.sentFiles == nil {
		return files
	}
	seen := c.sentFiles.Consume(requestID, chatID, threadID)
	if len(seen) == 0 {
		return files
	}
	out := make([]channel.OutboundFile, 0, len(files))
	for _, file := range files {
		clean := cleanReplyFilePath(file.Path)
		if _, ok := seen[clean]; ok {
			continue
		}
		out = append(out, file)
	}
	return out
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

	msg, err := c.sendTextMessage(ctx, tgapi.SendMessageParams{
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
	_, err := c.editTextMessage(ctx, tgapi.EditMessageTextParams{
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
		_, err := c.sendTextMessage(ctx, tgapi.SendMessageParams{
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
