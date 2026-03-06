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

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
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

func buildLaneKey(fromID string, thread string) string {
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

func parseDMBlockCleanup(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return defaultDMBlockCleanup, nil
	}
	switch v {
	case dmBlockCleanupNone,
		dmBlockCleanupReset,
		dmBlockCleanupForget:
		return v, nil
	default:
		return "", fmt.Errorf(
			"telegram: unsupported dm block cleanup: %s",
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

	resetOKMessage     = "Started a new session."
	resetFailedMessage = "Failed to start a new session."

	forgetOKMessage          = "Forgot your data."
	forgetFailedMessage      = "Failed to forget your data."
	forgetUnsupportedMessage = "Forget is not supported."

	jobsUnsupportedMessage = "Scheduled job management is not supported."
	jobsListFailedMessage  = "Failed to list scheduled jobs."
	jobsClearFailedMessage = "Failed to clear scheduled jobs."
	jobsEmptyMessage       = "No scheduled jobs for this chat."
	jobsClearNoopMessage   = "No scheduled jobs to clear for this chat."
	jobsMessageHeader      = "Scheduled jobs for this chat:"
	jobsClearOKFmt         = "Cleared %d scheduled job(s) for this chat."
	jobTimeLayout          = "2006-01-02 15:04:05 MST"
)

func (c *Channel) handleCancelCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	laneKey string,
) error {
	requestID := c.inflight.Get(laneKey)
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

type userForgetter interface {
	ForgetUser(ctx context.Context, channel, userID string) error
}

type scheduledJobManager interface {
	ListScheduledJobs(
		ctx context.Context,
		channel string,
		userID string,
		target string,
	) ([]gwclient.ScheduledJobSummary, error)
	ClearScheduledJobs(
		ctx context.Context,
		channel string,
		userID string,
		target string,
	) (int, error)
}

func (c *Channel) cancelInflight(
	ctx context.Context,
	laneKey string,
) bool {
	requestID := strings.TrimSpace(c.inflight.Get(laneKey))
	if requestID == "" {
		return false
	}

	canceled, err := c.gw.Cancel(ctx, requestID)
	if err != nil {
		log.WarnfContext(ctx, "telegram: cancel: %v", err)
		return false
	}
	c.inflight.Clear(laneKey, requestID)
	return canceled
}

func (c *Channel) handleResetCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	laneKey string,
	userID string,
) error {
	c.cancelInflight(ctx, laneKey)

	if c.dmSessions == nil {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			resetFailedMessage,
		)
		return nil
	}

	if _, err := c.dmSessions.Rotate(ctx, userID, laneKey); err != nil {
		log.WarnfContext(ctx, "telegram: reset: %v", err)
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			resetFailedMessage,
		)
		return nil
	}

	c.reply(ctx, chatID, messageThreadID, replyTo, resetOKMessage)
	return nil
}

func (c *Channel) handleForgetCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	laneKey string,
	userID string,
) error {
	c.cancelInflight(ctx, laneKey)

	f, ok := c.gw.(userForgetter)
	if !ok {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			forgetUnsupportedMessage,
		)
		return nil
	}

	if err := f.ForgetUser(ctx, channelID, userID); err != nil {
		log.WarnfContext(ctx, "telegram: forget: %v", err)
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			forgetFailedMessage,
		)
		return nil
	}

	if c.dmSessions != nil {
		if _, err := c.dmSessions.ForgetUser(ctx, userID); err != nil {
			log.WarnfContext(
				ctx,
				"telegram: forget dm session: %v",
				err,
			)
		}
	}

	c.reply(ctx, chatID, messageThreadID, replyTo, forgetOKMessage)
	return nil
}

func (c *Channel) handleJobsCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	userID string,
) error {
	manager, ok := c.gw.(scheduledJobManager)
	if !ok {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsUnsupportedMessage,
		)
		return nil
	}

	jobs, err := manager.ListScheduledJobs(
		ctx,
		channelID,
		userID,
		currentChatTarget(chatID, messageThreadID),
	)
	if err != nil {
		log.WarnfContext(ctx, "telegram: list jobs: %v", err)
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsListFailedMessage,
		)
		return nil
	}

	c.reply(
		ctx,
		chatID,
		messageThreadID,
		replyTo,
		formatScheduledJobsMessage(jobs),
	)
	return nil
}

func (c *Channel) handleJobsClearCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	userID string,
) error {
	manager, ok := c.gw.(scheduledJobManager)
	if !ok {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsUnsupportedMessage,
		)
		return nil
	}

	removed, err := manager.ClearScheduledJobs(
		ctx,
		channelID,
		userID,
		currentChatTarget(chatID, messageThreadID),
	)
	if err != nil {
		log.WarnfContext(ctx, "telegram: clear jobs: %v", err)
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsClearFailedMessage,
		)
		return nil
	}

	text := jobsClearNoopMessage
	if removed > 0 {
		text = fmt.Sprintf(jobsClearOKFmt, removed)
	}
	c.reply(ctx, chatID, messageThreadID, replyTo, text)
	return nil
}

func currentChatTarget(chatID int64, messageThreadID int) string {
	chat := strconv.FormatInt(chatID, 10)
	if messageThreadID == 0 {
		return chat
	}
	return fmt.Sprintf(
		"%s%s%d",
		chat,
		threadTopicSep,
		messageThreadID,
	)
}

func formatScheduledJobsMessage(
	jobs []gwclient.ScheduledJobSummary,
) string {
	if len(jobs) == 0 {
		return jobsEmptyMessage
	}

	var b strings.Builder
	b.WriteString(jobsMessageHeader)
	for _, job := range jobs {
		line := formatScheduledJobLine(job)
		if line == "" {
			continue
		}
		b.WriteByte('\n')
		b.WriteString(line)
	}
	return b.String()
}

func formatScheduledJobLine(job gwclient.ScheduledJobSummary) string {
	id := strings.TrimSpace(job.ID)
	if id == "" {
		return ""
	}

	name := strings.TrimSpace(job.Name)
	if name == "" {
		name = id
	}

	parts := []string{name}
	if schedule := strings.TrimSpace(job.Schedule); schedule != "" {
		parts = append(parts, schedule)
	}
	if job.NextRunAt != nil && !job.NextRunAt.IsZero() {
		parts = append(
			parts,
			"next "+job.NextRunAt.Local().Format(jobTimeLayout),
		)
	}
	if status := strings.TrimSpace(job.LastStatus); status != "" {
		parts = append(parts, status)
	}
	parts = append(parts, "id "+id)
	return "- " + strings.Join(parts, " | ")
}
