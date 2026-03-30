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

	"trpc.group/trpc-go/trpc-agent-go/openclaw/croncmd"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
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
	_, err := c.sendTextMessage(ctx, tgapi.SendMessageParams{
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
	jobsUsageMessage       = "Usage:\n" +
		"/cron list\n" +
		"/cron status <index|id>\n" +
		"/cron stop <index|id>\n" +
		"/cron resume <index|id>\n" +
		"/cron remove <index|id>\n" +
		"/cron clear"
	jobsListFailedMessage   = "Failed to list scheduled jobs."
	jobsClearFailedMessage  = "Failed to clear scheduled jobs."
	jobsUpdateFailedMessage = "Failed to update the scheduled job."
	jobsRemoveFailedMessage = "Failed to remove the scheduled job."
	jobsEmptyMessage        = "No scheduled jobs for this chat."
	jobsClearNoopMessage    = "No scheduled jobs to clear for this chat."
	jobsMessageHeader       = "Scheduled jobs for this chat:"
	jobsClearOKFmt          = "Cleared %d scheduled job(s) for this chat."
	jobsStopOKFmt           = "Stopped scheduled job %s."
	jobsResumeOKFmt         = "Resumed scheduled job %s."
	jobsRemoveOKFmt         = "Removed scheduled job %s."
	jobsStatusHeader        = "Scheduled job details:"
	jobsSelectorHint        = "Use the list index or a unique job id prefix."
	jobTimeLayout           = "2006-01-02 15:04:05 MST"

	personaUnsupportedMessage = "Preset personas are not supported."
	personaListFailedMessage  = "Failed to load persona presets."
	personaSetFailedMessage   = "Failed to update the persona preset."
	personaUnknownMessage     = "Unknown persona preset. " +
		"Use the personas list command."
	personaResetOKMessage = "Persona reset to default."
	personaSetOKFmt       = "Persona set to %s."
	personaMessageHeader  = "Persona presets for this chat:"
	personaCurrentPrefix  = "Current: "
	personaUsageMessage   = "Tap a button below to switch instantly. " +
		"Use /persona <id> if you prefer typing. Use default " +
		"to reset."

	personaCallbackPrefix      = "persona:set:"
	personaButtonActivePrefix  = "> "
	personaKeyboardColumns     = 2
	personaSelectionFailedHint = "Could not update the preset."
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
	SetScheduledJobEnabled(
		ctx context.Context,
		channel string,
		userID string,
		target string,
		jobID string,
		enabled bool,
	) (gwclient.ScheduledJobSummary, error)
	RemoveScheduledJob(
		ctx context.Context,
		channel string,
		userID string,
		target string,
		jobID string,
	) (bool, error)
}

type personaManager interface {
	ListPresetPersonas() []persona.Preset
	GetPresetPersona(
		ctx context.Context,
		scopeKey string,
	) (persona.Preset, error)
	SetPresetPersona(
		ctx context.Context,
		scopeKey string,
		presetID string,
	) (persona.Preset, error)
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

func (c *Channel) handleCronCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	userID string,
	args string,
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

	cmd, err := croncmd.Parse(args)
	if err != nil {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsUsageMessage,
		)
		return nil
	}

	target := currentChatTarget(chatID, messageThreadID)
	switch cmd.Action {
	case croncmd.ActionHelp:
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsUsageMessage,
		)
		return nil
	case croncmd.ActionClear:
		removed, err := manager.ClearScheduledJobs(
			ctx,
			channelID,
			userID,
			target,
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

	jobs, err := manager.ListScheduledJobs(
		ctx,
		channelID,
		userID,
		target,
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

	switch cmd.Action {
	case croncmd.ActionList:
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			formatScheduledJobsMessage(jobs),
		)
		return nil
	case croncmd.ActionStatus:
		return c.replySelectedJob(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobs,
			cmd.Selector,
		)
	case croncmd.ActionStop, croncmd.ActionResume:
		enabled := cmd.Action == croncmd.ActionResume
		return c.setSelectedJobEnabled(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			manager,
			userID,
			target,
			jobs,
			cmd.Action,
			cmd.Selector,
			enabled,
		)
	case croncmd.ActionRemove:
		return c.removeSelectedJob(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			manager,
			userID,
			target,
			jobs,
			cmd.Selector,
		)
	default:
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsUsageMessage,
		)
		return nil
	}
}

func (c *Channel) handleJobsCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	userID string,
) error {
	return c.handleCronCommand(
		ctx,
		chatID,
		messageThreadID,
		replyTo,
		userID,
		croncmd.ActionList,
	)
}

func (c *Channel) handleJobsClearCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	userID string,
) error {
	return c.handleCronCommand(
		ctx,
		chatID,
		messageThreadID,
		replyTo,
		userID,
		croncmd.ActionClear,
	)
}

func (c *Channel) replySelectedJob(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	jobs []gwclient.ScheduledJobSummary,
	selector string,
) error {
	job, err := croncmd.ResolveSelector(jobs, selector)
	if err != nil {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsUsageMessage,
		)
		return nil
	}
	c.reply(
		ctx,
		chatID,
		messageThreadID,
		replyTo,
		formatScheduledJobDetails(job),
	)
	return nil
}

func (c *Channel) setSelectedJobEnabled(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	manager scheduledJobManager,
	userID string,
	target string,
	jobs []gwclient.ScheduledJobSummary,
	action string,
	selector string,
	enabled bool,
) error {
	job, err := croncmd.ResolveSelector(jobs, selector)
	if err != nil {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsUsageMessage,
		)
		return nil
	}

	updated, err := manager.SetScheduledJobEnabled(
		ctx,
		channelID,
		userID,
		target,
		job.ID,
		enabled,
	)
	if err != nil {
		log.WarnfContext(
			ctx,
			"telegram: update job %s: %v",
			job.ID,
			err,
		)
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsUpdateFailedMessage,
		)
		return nil
	}

	text := fmt.Sprintf(
		jobsStopOKFmt,
		scheduledJobDisplayName(updated),
	)
	if action == croncmd.ActionResume {
		text = fmt.Sprintf(
			jobsResumeOKFmt,
			scheduledJobDisplayName(updated),
		)
	}
	c.reply(ctx, chatID, messageThreadID, replyTo, text)
	return nil
}

func (c *Channel) removeSelectedJob(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	manager scheduledJobManager,
	userID string,
	target string,
	jobs []gwclient.ScheduledJobSummary,
	selector string,
) error {
	job, err := croncmd.ResolveSelector(jobs, selector)
	if err != nil {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsUsageMessage,
		)
		return nil
	}

	removed, err := manager.RemoveScheduledJob(
		ctx,
		channelID,
		userID,
		target,
		job.ID,
	)
	if err != nil {
		log.WarnfContext(
			ctx,
			"telegram: remove job %s: %v",
			job.ID,
			err,
		)
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsRemoveFailedMessage,
		)
		return nil
	}
	if !removed {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			jobsUsageMessage,
		)
		return nil
	}

	c.reply(
		ctx,
		chatID,
		messageThreadID,
		replyTo,
		fmt.Sprintf(
			jobsRemoveOKFmt,
			scheduledJobDisplayName(job),
		),
	)
	return nil
}

func (c *Channel) handlePersonaCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	scopeKey string,
	args string,
) error {
	manager, ok := c.gw.(personaManager)
	if !ok {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			personaUnsupportedMessage,
		)
		return nil
	}

	presetID := firstCommandArg(args)
	if presetID == "" {
		return c.replyPersonaSummary(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			scopeKey,
			manager,
		)
	}

	preset, err := manager.SetPresetPersona(ctx, scopeKey, presetID)
	if err != nil {
		if errors.Is(err, persona.ErrUnknownPreset) {
			c.reply(
				ctx,
				chatID,
				messageThreadID,
				replyTo,
				personaUnknownMessage,
			)
			return nil
		}
		log.WarnfContext(ctx, "telegram: set persona: %v", err)
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			personaSetFailedMessage,
		)
		return nil
	}

	c.reply(
		ctx,
		chatID,
		messageThreadID,
		replyTo,
		personaSelectionText(preset),
	)
	return nil
}

func (c *Channel) handlePersonasCommand(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	scopeKey string,
) error {
	manager, ok := c.gw.(personaManager)
	if !ok {
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			personaUnsupportedMessage,
		)
		return nil
	}
	return c.replyPersonaSummary(
		ctx,
		chatID,
		messageThreadID,
		replyTo,
		scopeKey,
		manager,
	)
}

func (c *Channel) replyPersonaSummary(
	ctx context.Context,
	chatID int64,
	messageThreadID int,
	replyTo int,
	scopeKey string,
	manager personaManager,
) error {
	current, presets, err := personaSummaryData(
		ctx,
		scopeKey,
		manager,
	)
	if err != nil {
		log.WarnfContext(ctx, "telegram: get persona: %v", err)
		c.reply(
			ctx,
			chatID,
			messageThreadID,
			replyTo,
			personaListFailedMessage,
		)
		return nil
	}

	_, err = c.sendTextMessage(
		ctx,
		tgapi.SendMessageParams{
			ChatID:           chatID,
			MessageThreadID:  messageThreadID,
			ReplyToMessageID: replyTo,
			Text: formatPersonaMessage(
				current,
				presets,
			),
			ReplyMarkup: personaReplyMarkup(
				current,
				presets,
			),
		},
	)
	if err != nil {
		log.WarnfContext(ctx, "telegram: send persona summary: %v", err)
	}
	return nil
}

func (c *Channel) handlePersonaCallbackQuery(
	ctx context.Context,
	q tgapi.CallbackQuery,
	scopeKey string,
	messageThreadID int,
) error {
	manager, ok := c.gw.(personaManager)
	if !ok {
		return c.answerCallbackQuery(
			ctx,
			q.ID,
			personaUnsupportedMessage,
			true,
		)
	}

	presetID := personaPresetIDFromCallback(q.Data)
	if presetID == "" {
		return c.answerCallbackQuery(ctx, q.ID, "", false)
	}

	preset, err := manager.SetPresetPersona(ctx, scopeKey, presetID)
	if err != nil {
		if errors.Is(err, persona.ErrUnknownPreset) {
			return c.answerCallbackQuery(
				ctx,
				q.ID,
				personaUnknownMessage,
				true,
			)
		}
		log.WarnfContext(ctx, "telegram: set persona via callback: %v", err)
		return c.answerCallbackQuery(
			ctx,
			q.ID,
			personaSelectionFailedHint,
			true,
		)
	}

	current, presets, err := personaSummaryData(
		ctx,
		scopeKey,
		manager,
	)
	if err != nil {
		log.WarnfContext(
			ctx,
			"telegram: refresh persona summary: %v",
			err,
		)
		return c.answerCallbackQuery(
			ctx,
			q.ID,
			personaSelectionText(preset),
			false,
		)
	}

	if _, err := c.editTextMessage(
		ctx,
		tgapi.EditMessageTextParams{
			ChatID:    q.Message.Chat.ID,
			MessageID: q.Message.MessageID,
			Text: formatPersonaMessage(
				current,
				presets,
			),
			ReplyMarkup: personaReplyMarkup(
				current,
				presets,
			),
		},
	); err != nil {
		if !tgapi.IsMessageNotModifiedError(err) {
			log.WarnfContext(
				ctx,
				"telegram: edit persona summary: %v",
				err,
			)
			c.reply(
				ctx,
				q.Message.Chat.ID,
				messageThreadID,
				q.Message.MessageID,
				personaSelectionText(preset),
			)
		}
	}

	return c.answerCallbackQuery(
		ctx,
		q.ID,
		personaSelectionText(preset),
		false,
	)
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

func formatScheduledJobDetails(
	job gwclient.ScheduledJobSummary,
) string {
	lines := []string{
		jobsStatusHeader,
		"Name: " + scheduledJobDisplayName(job),
		"ID: " + strings.TrimSpace(job.ID),
		"Schedule: " + strings.TrimSpace(job.Schedule),
		"Enabled: " + formatJobEnabled(job.Enabled),
		"Last status: " + valueOrDash(job.LastStatus),
		"Runs: " + valueOrDash(formatScheduledJobRunCount(job)),
		"Overlap: " + valueOrDash(
			formatScheduledJobOverlap(job.OverlapPolicy),
		),
	}
	if job.EndsAt != nil && !job.EndsAt.IsZero() {
		lines = append(
			lines,
			"Ends at: "+
				job.EndsAt.Local().Format(jobTimeLayout),
		)
	}
	if job.NextRunAt != nil && !job.NextRunAt.IsZero() {
		lines = append(
			lines,
			"Next run: "+
				job.NextRunAt.Local().Format(jobTimeLayout),
		)
	}
	if text := strings.TrimSpace(job.LastError); text != "" {
		lines = append(lines, "Last error: "+text)
	}
	if text := strings.TrimSpace(job.LastOutput); text != "" {
		lines = append(
			lines,
			"Last output: "+trimJobText(text),
		)
	}
	lines = append(lines, jobsSelectorHint)
	return strings.Join(lines, "\n")
}

func formatScheduledJobLine(job gwclient.ScheduledJobSummary) string {
	jobID := strings.TrimSpace(job.ID)
	if jobID == "" {
		return ""
	}

	parts := []string{scheduledJobDisplayName(job)}
	if schedule := strings.TrimSpace(job.Schedule); schedule != "" {
		parts = append(parts, schedule)
	}
	if runCount := formatScheduledJobRunCount(job); runCount != "" {
		parts = append(parts, runCount)
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
	parts = append(parts, "id "+croncmd.ShortID(jobID))
	return "- " + strings.Join(parts, " | ")
}

func scheduledJobDisplayName(job gwclient.ScheduledJobSummary) string {
	name := strings.TrimSpace(job.Name)
	if name != "" {
		return name
	}
	return strings.TrimSpace(job.ID)
}

func formatJobEnabled(enabled bool) string {
	if enabled {
		return "yes"
	}
	return "no"
}

func valueOrDash(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "-"
	}
	return trimmed
}

func formatScheduledJobRunCount(job gwclient.ScheduledJobSummary) string {
	switch {
	case job.MaxRuns > 0:
		return fmt.Sprintf(
			"runs %d/%d",
			job.RunCount,
			job.MaxRuns,
		)
	case job.RunCount > 0:
		return fmt.Sprintf("runs %d", job.RunCount)
	default:
		return ""
	}
}

func formatScheduledJobOverlap(policy string) string {
	trimmed := strings.TrimSpace(policy)
	if trimmed == "" {
		return ""
	}
	return trimmed
}

func trimJobText(text string) string {
	const maxRunes = 120

	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + "..."
}

func firstCommandArg(args string) string {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func formatPersonaMessage(
	current persona.Preset,
	presets []persona.Preset,
) string {
	var b strings.Builder
	b.WriteString(personaMessageHeader)
	b.WriteByte('\n')
	b.WriteString(personaCurrentPrefix)
	b.WriteString(current.ID)
	for _, preset := range presets {
		line := formatPersonaLine(preset, current.ID)
		if line == "" {
			continue
		}
		b.WriteByte('\n')
		b.WriteString(line)
	}
	b.WriteByte('\n')
	b.WriteString(personaUsageMessage)
	return b.String()
}

func formatPersonaLine(
	preset persona.Preset,
	currentID string,
) string {
	id := strings.TrimSpace(preset.ID)
	if id == "" {
		return ""
	}

	line := "- " + id
	desc := strings.TrimSpace(preset.Description)
	if desc != "" {
		line += ": " + desc
	}
	if id == strings.TrimSpace(currentID) {
		line += " (active)"
	}
	return line
}

func personaSummaryData(
	ctx context.Context,
	scopeKey string,
	manager personaManager,
) (persona.Preset, []persona.Preset, error) {
	current, err := manager.GetPresetPersona(ctx, scopeKey)
	if err != nil {
		return persona.Preset{}, nil, err
	}
	return current, manager.ListPresetPersonas(), nil
}

func personaReplyMarkup(
	current persona.Preset,
	presets []persona.Preset,
) *tgapi.InlineKeyboardMarkup {
	rows := make([][]tgapi.InlineKeyboardButton, 0)
	row := make([]tgapi.InlineKeyboardButton, 0, personaKeyboardColumns)
	for _, preset := range presets {
		id := strings.TrimSpace(preset.ID)
		if id == "" {
			continue
		}
		row = append(row, tgapi.InlineKeyboardButton{
			Text:         personaButtonText(preset, current.ID),
			CallbackData: personaCallbackPrefix + id,
		})
		if len(row) == personaKeyboardColumns {
			rows = append(rows, row)
			row = make([]tgapi.InlineKeyboardButton, 0,
				personaKeyboardColumns)
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		return nil
	}
	return &tgapi.InlineKeyboardMarkup{
		InlineKeyboard: rows,
	}
}

func personaButtonText(
	preset persona.Preset,
	currentID string,
) string {
	label := strings.TrimSpace(preset.Name)
	if label == "" {
		label = strings.TrimSpace(preset.ID)
	}
	if strings.TrimSpace(preset.ID) == strings.TrimSpace(currentID) {
		return personaButtonActivePrefix + label
	}
	return label
}

func isPersonaCallbackData(data string) bool {
	return personaPresetIDFromCallback(data) != ""
}

func personaPresetIDFromCallback(data string) string {
	trimmed := strings.TrimSpace(data)
	if !strings.HasPrefix(trimmed, personaCallbackPrefix) {
		return ""
	}
	return strings.TrimSpace(
		strings.TrimPrefix(trimmed, personaCallbackPrefix),
	)
}

func personaSelectionText(preset persona.Preset) string {
	text := fmt.Sprintf(personaSetOKFmt, preset.ID)
	if preset.ID == persona.PresetDefault {
		return personaResetOKMessage
	}
	return text
}
