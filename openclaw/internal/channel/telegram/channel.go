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
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/pairing"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const (
	channelID = "telegram"

	requestIDPrefix = "telegram:"

	threadTopicSep = ":topic:"

	maxReplyRunes = 4000

	tgChatTypePrivate = "private"

	tgChatMemberStatusKicked = "kicked"
	tgChatMemberStatusLeft   = "left"

	defaultStateRootDir = ".trpc-agent-go-github"
	defaultStateAppName = "openclaw"

	mentionPrefix = "@"

	offsetStoreDir = "telegram"

	offsetStoreFilePrefix = "update-offset-"
	offsetStoreFileSuffix = ".json"

	defaultOffsetKey = "default"

	pairingStoreFilePrefix = "pairing-"
	pairingStoreFileSuffix = ".json"

	dmPolicyDisabled  = "disabled"
	dmPolicyOpen      = "open"
	dmPolicyAllowlist = "allowlist"
	dmPolicyPairing   = "pairing"

	groupPolicyDisabled  = "disabled"
	groupPolicyOpen      = "open"
	groupPolicyAllowlist = "allowlist"

	defaultDMPolicy    = dmPolicyPairing
	defaultGroupPolicy = groupPolicyDisabled

	dmBlockCleanupNone   = "none"
	dmBlockCleanupReset  = "reset"
	dmBlockCleanupForget = "forget"

	defaultDMBlockCleanup = dmBlockCleanupReset

	defaultPairingTTL = time.Hour

	defaultRegisterCommands = true

	defaultMaxDownloadMiB         = 20
	defaultMaxDownloadBytes int64 = defaultMaxDownloadMiB << 20
)

// ChannelName is the stable channel identifier used across OpenClaw.
const ChannelName = channelID

const (
	notAllowedMessage = "You are not allowed to use this bot."

	pairingMessageTemplate = `Pairing required.

Code: %s

Ask the operator to approve:
openclaw pairing approve %s -config <CONFIG>`

	errNonPositiveMaxDownloadBytes = "telegram: non-positive max download bytes"
)

type gatewayClient interface {
	SendMessage(
		ctx context.Context,
		req gwclient.MessageRequest,
	) (gwclient.MessageResponse, error)

	Cancel(ctx context.Context, requestID string) (bool, error)
}

type botAPI interface {
	GetUpdates(
		ctx context.Context,
		offset int,
		timeout time.Duration,
	) ([]tgapi.Update, error)

	SendMessage(
		ctx context.Context,
		params tgapi.SendMessageParams,
	) (tgapi.Message, error)

	AnswerCallbackQuery(
		ctx context.Context,
		params tgapi.AnswerCallbackQueryParams,
	) error

	SendDocument(
		ctx context.Context,
		params tgapi.SendFileParams,
	) (tgapi.Message, error)

	SendPhoto(
		ctx context.Context,
		params tgapi.SendFileParams,
	) (tgapi.Message, error)

	SendAudio(
		ctx context.Context,
		params tgapi.SendFileParams,
	) (tgapi.Message, error)

	SendVoice(
		ctx context.Context,
		params tgapi.SendFileParams,
	) (tgapi.Message, error)

	SendVideo(
		ctx context.Context,
		params tgapi.SendFileParams,
	) (tgapi.Message, error)

	EditMessageText(
		ctx context.Context,
		params tgapi.EditMessageTextParams,
	) (tgapi.Message, error)

	SendChatAction(
		ctx context.Context,
		params tgapi.SendChatActionParams,
	) error

	SetMyCommands(
		ctx context.Context,
		params tgapi.SetMyCommandsParams,
	) error

	DownloadFileByID(
		ctx context.Context,
		fileID string,
		maxBytes int64,
	) (tgapi.File, []byte, error)
}

// BotInfo represents Telegram bot metadata used by the channel.
type BotInfo struct {
	ID       int64
	Username string
	Mention  string
}

// ProbeBotInfo fetches bot metadata via getMe.
func ProbeBotInfo(
	ctx context.Context,
	token string,
	opts ...tgapi.Option,
) (BotInfo, error) {
	if strings.TrimSpace(token) == "" {
		return BotInfo{}, nil
	}
	c, err := tgapi.New(token, opts...)
	if err != nil {
		return BotInfo{}, err
	}
	me, err := c.GetMe(ctx)
	if err != nil {
		return BotInfo{}, err
	}
	username := strings.TrimSpace(me.Username)
	return BotInfo{
		ID:       me.ID,
		Username: username,
		Mention:  mentionFromUsername(username),
	}, nil
}

func mentionFromUsername(username string) string {
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	return mentionPrefix + username
}

type config struct {
	stateDir        string
	startFromLatest bool
	pollTimeout     time.Duration
	errorBackoff    time.Duration

	dmPolicy    string
	groupPolicy string

	allowUsers   map[string]struct{}
	allowThreads map[string]struct{}

	pairingTTL time.Duration

	apiOptions []tgapi.Option

	maxDownloadBytes int64

	streamingMode string

	dmResetPolicy dmSessionResetPolicy

	dmBlockCleanup string

	registerCommands bool
}

// Option configures the Telegram channel.
type Option func(*config)

// WithStateDir sets the state directory for offsets.
func WithStateDir(dir string) Option {
	return func(c *config) { c.stateDir = dir }
}

// WithStartFromLatest controls whether the poller drains pending
// updates when no stored offset exists yet.
func WithStartFromLatest(enabled bool) Option {
	return func(c *config) { c.startFromLatest = enabled }
}

// WithPollTimeout sets the long-poll timeout.
func WithPollTimeout(timeout time.Duration) Option {
	return func(c *config) { c.pollTimeout = timeout }
}

// WithErrorBackoff sets the delay after polling/handler errors.
func WithErrorBackoff(backoff time.Duration) Option {
	return func(c *config) { c.errorBackoff = backoff }
}

// WithDMPolicy sets the policy for direct messages.
func WithDMPolicy(policy string) Option {
	return func(c *config) { c.dmPolicy = policy }
}

// WithGroupPolicy sets the policy for group and thread messages.
func WithGroupPolicy(policy string) Option {
	return func(c *config) { c.groupPolicy = policy }
}

// WithAllowUsers sets a per-channel allowlist.
func WithAllowUsers(users ...string) Option {
	return func(c *config) {
		if len(users) == 0 {
			c.allowUsers = nil
			return
		}

		if c.allowUsers == nil {
			c.allowUsers = make(map[string]struct{})
		}
		for _, user := range users {
			user = strings.TrimSpace(user)
			if user == "" {
				continue
			}
			c.allowUsers[user] = struct{}{}
		}
	}
}

// WithAllowThreads sets an allowlist for group chats and topics.
//
// Values should match the `thread` field derived by this channel:
//   - Group chat: "<chat_id>"
//   - Forum topic: "<chat_id>:topic:<message_thread_id>"
func WithAllowThreads(threads ...string) Option {
	return func(c *config) {
		if len(threads) == 0 {
			c.allowThreads = nil
			return
		}

		if c.allowThreads == nil {
			c.allowThreads = make(map[string]struct{})
		}
		for _, thread := range threads {
			thread = strings.TrimSpace(thread)
			if thread == "" {
				continue
			}
			c.allowThreads[thread] = struct{}{}
		}
	}
}

// WithPairingTTL sets how long pairing codes stay valid.
func WithPairingTTL(ttl time.Duration) Option {
	return func(c *config) { c.pairingTTL = ttl }
}

// WithAPIOptions passes options to the underlying Telegram API client.
func WithAPIOptions(opts ...tgapi.Option) Option {
	return func(c *config) { c.apiOptions = append(c.apiOptions, opts...) }
}

// WithMaxDownloadBytes sets the per-file download limit for Telegram
// attachments.
func WithMaxDownloadBytes(maxBytes int64) Option {
	return func(c *config) { c.maxDownloadBytes = maxBytes }
}

// WithStreamingMode controls how replies are delivered to Telegram.
func WithStreamingMode(mode string) Option {
	return func(c *config) { c.streamingMode = mode }
}

// WithDMSessionIdleReset configures an automatic reset when a DM
// stays idle longer than the given duration.
func WithDMSessionIdleReset(idle time.Duration) Option {
	return func(c *config) { c.dmResetPolicy.Idle = idle }
}

// WithDMSessionDailyReset configures an automatic reset when the date
// changes (local time).
func WithDMSessionDailyReset(enabled bool) Option {
	return func(c *config) { c.dmResetPolicy.Daily = enabled }
}

// WithDMBlockCleanup configures what happens when the bot is blocked.
//
// Supported values:
//   - "none":  keep server state intact
//   - "reset": rotate to a new active session
//   - "forget": delete sessions, memories, and debug traces
func WithDMBlockCleanup(action string) Option {
	return func(c *config) { c.dmBlockCleanup = action }
}

// WithRegisterCommands controls whether the bot registers slash commands
// with Telegram on startup.
func WithRegisterCommands(enabled bool) Option {
	return func(c *config) { c.registerCommands = enabled }
}

type pairingStore interface {
	IsApproved(ctx context.Context, userID string) (bool, error)
	Request(
		ctx context.Context,
		userID string,
	) (string, bool, error)
}

// Channel implements a Telegram long-polling chat surface.
type Channel struct {
	bot   botAPI
	info  BotInfo
	gw    gatewayClient
	store tgapi.OffsetStore
	state string

	sentFiles *sentFileTracker

	audioInputConverter audioInputConverter

	dmSessions     *dmSessionStore
	dmResetPolicy  dmSessionResetPolicy
	dmBlockCleanup string

	startFromLatest bool
	pollTimeout     time.Duration
	errorBackoff    time.Duration

	dmPolicy    string
	groupPolicy string

	allowUsers   map[string]struct{}
	allowThreads map[string]struct{}

	pairing pairingStore

	maxDownloadBytes int64

	streamingMode string

	registerCommands bool

	lanes    *laneLocker
	inflight *inflightRequests
}

// New creates a Telegram channel. It persists polling offsets under
// the configured state directory.
func New(
	token string,
	bot BotInfo,
	gw gatewayClient,
	opts ...Option,
) (*Channel, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("telegram: empty token")
	}
	if gw == nil {
		return nil, errors.New("telegram: nil gateway client")
	}

	cfg := config{
		startFromLatest:  true,
		pollTimeout:      25 * time.Second,
		errorBackoff:     1 * time.Second,
		dmPolicy:         defaultDMPolicy,
		groupPolicy:      defaultGroupPolicy,
		pairingTTL:       defaultPairingTTL,
		registerCommands: defaultRegisterCommands,
		maxDownloadBytes: defaultMaxDownloadBytes,
		streamingMode:    defaultStreamingMode,
		dmBlockCleanup:   defaultDMBlockCleanup,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	stateDir, err := resolveStateDir(cfg.stateDir)
	if err != nil {
		return nil, err
	}

	dmPolicy, err := parseDMPolicy(cfg.dmPolicy)
	if err != nil {
		return nil, err
	}
	groupPolicy, err := parseGroupPolicy(cfg.groupPolicy)
	if err != nil {
		return nil, err
	}
	if cfg.pairingTTL <= 0 {
		return nil, errors.New("telegram: non-positive pairing ttl")
	}
	if cfg.maxDownloadBytes <= 0 {
		return nil, errors.New(errNonPositiveMaxDownloadBytes)
	}

	if cfg.dmResetPolicy.Idle < 0 {
		return nil, errors.New("telegram: negative dm reset idle")
	}

	streamingMode, err := parseStreamingMode(cfg.streamingMode)
	if err != nil {
		return nil, err
	}

	dmBlockCleanup, err := parseDMBlockCleanup(cfg.dmBlockCleanup)
	if err != nil {
		return nil, err
	}

	api, err := tgapi.New(token, cfg.apiOptions...)
	if err != nil {
		return nil, err
	}

	store, err := newOffsetStore(stateDir, bot)
	if err != nil {
		return nil, err
	}

	sessionsPath, err := dmSessionStorePath(stateDir, bot)
	if err != nil {
		return nil, err
	}
	dmSessions, err := newDMSessionStore(sessionsPath)
	if err != nil {
		return nil, err
	}

	var dmPairing pairingStore
	if dmPolicy == dmPolicyPairing {
		path, err := PairingStorePath(stateDir, bot)
		if err != nil {
			return nil, err
		}
		dmPairing, err = pairing.NewFileStore(
			path,
			pairing.WithTTL(cfg.pairingTTL),
		)
		if err != nil {
			return nil, err
		}
	}

	return &Channel{
		bot:                 api,
		info:                bot,
		gw:                  gw,
		store:               store,
		state:               stateDir,
		sentFiles:           newSentFileTracker(),
		audioInputConverter: defaultAudioInputConverter,
		dmSessions:          dmSessions,
		dmResetPolicy:       cfg.dmResetPolicy,
		dmBlockCleanup:      dmBlockCleanup,
		startFromLatest:     cfg.startFromLatest,
		pollTimeout:         cfg.pollTimeout,
		errorBackoff:        cfg.errorBackoff,
		dmPolicy:            dmPolicy,
		groupPolicy:         groupPolicy,
		allowUsers:          cfg.allowUsers,
		allowThreads:        cfg.allowThreads,
		pairing:             dmPairing,
		maxDownloadBytes:    cfg.maxDownloadBytes,
		streamingMode:       streamingMode,
		registerCommands:    cfg.registerCommands,
		lanes:               newLaneLocker(),
		inflight:            newInflightRequests(),
	}, nil
}

// ID returns the channel identifier used by the gateway.
func (c *Channel) ID() string { return channelID }

// Run starts polling Telegram and blocks until ctx is done.
func (c *Channel) Run(ctx context.Context) error {
	if c == nil {
		return errors.New("telegram: nil channel")
	}

	if err := c.registerBotCommands(ctx); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		log.WarnfContext(
			ctx,
			"telegram: register commands: %v",
			err,
		)
	}

	poller, err := tgapi.NewPoller(
		c.bot,
		tgapi.WithOffsetStore(c.store),
		tgapi.WithStartFromLatest(c.startFromLatest),
		tgapi.WithPollTimeout(c.pollTimeout),
		tgapi.WithErrorBackoff(c.errorBackoff),
		tgapi.WithOnError(func(err error) {
			log.WarnfContext(ctx, "telegram: poller: %v", err)
		}),
		tgapi.WithMessageHandler(func(
			ctx context.Context,
			msg tgapi.Message,
		) error {
			go func() {
				if err := c.handleMessage(ctx, msg); err != nil {
					log.WarnfContext(
						ctx,
						"telegram: handle message: %v",
						err,
					)
				}
			}()
			return nil
		}),
		tgapi.WithCallbackQueryHandler(func(
			ctx context.Context,
			q tgapi.CallbackQuery,
		) error {
			go func() {
				if err := c.handleCallbackQuery(ctx, q); err != nil {
					log.WarnfContext(
						ctx,
						"telegram: handle callback query: %v",
						err,
					)
				}
			}()
			return nil
		}),
		tgapi.WithMyChatMemberHandler(func(
			ctx context.Context,
			ev tgapi.ChatMemberEvent,
		) error {
			go func() {
				if err := c.handleMyChatMember(ctx, ev); err != nil {
					log.WarnfContext(
						ctx,
						"telegram: handle my_chat_member: %v",
						err,
					)
				}
			}()
			return nil
		}),
	)
	if err != nil {
		return err
	}
	return poller.Run(ctx)
}

func (c *Channel) registerBotCommands(ctx context.Context) error {
	if c == nil || !c.registerCommands || c.bot == nil {
		return nil
	}
	return c.bot.SetMyCommands(
		ctx,
		tgapi.SetMyCommandsParams{
			Commands: defaultBotCommands(),
		},
	)
}

func (c *Channel) handleMessage(
	ctx context.Context,
	msg tgapi.Message,
) error {
	if msg.Chat == nil || msg.From == nil {
		return nil
	}

	chatID := msg.Chat.ID
	fromID := strconv.FormatInt(msg.From.ID, 10)

	isGroup := tgapi.IsGroupChat(strings.TrimSpace(msg.Chat.Type))
	if !c.isUserAllowed(fromID) {
		if !isGroup {
			c.sendDM(ctx, chatID, notAllowedMessage)
		}
		return nil
	}

	thread := ""
	messageThreadID := 0
	if isGroup {
		thread = strconv.FormatInt(chatID, 10)
		if msg.MessageThreadID != 0 {
			thread = fmt.Sprintf(
				"%s%s%d",
				thread,
				threadTopicSep,
				msg.MessageThreadID,
			)
			messageThreadID = msg.MessageThreadID
		}
	}

	if !c.isChatAllowed(isGroup, thread) {
		return nil
	}
	if !isGroup {
		ok, err := c.isDMAllowed(ctx, chatID, fromID)
		if err != nil || !ok {
			return err
		}
	}

	requestID := buildRequestID(chatID, messageThreadID, msg.MessageID)
	laneKey := buildLaneKey(fromID, thread)

	sessionID := laneKey
	if !isGroup && c.dmSessions != nil {
		resolved, _, err := c.dmSessions.EnsureActiveSession(
			ctx,
			fromID,
			laneKey,
			c.dmResetPolicy,
		)
		if err != nil {
			return err
		}
		sessionID = resolved
	}

	cmd := parseCommandCall(
		joinMessageText(msg.Text, msg.Caption),
		c.info,
	)
	if cmd.Name != "" {
		switch cmd.Name {
		case commandHelp:
			c.reply(ctx, chatID, messageThreadID, msg.MessageID, helpMessage)
			return nil
		case commandCancel:
			return c.handleCancelCommand(
				ctx,
				chatID,
				messageThreadID,
				msg.MessageID,
				laneKey,
			)
		case commandReset, commandNew:
			if isGroup {
				return nil
			}
			return c.handleResetCommand(
				ctx,
				chatID,
				messageThreadID,
				msg.MessageID,
				laneKey,
				fromID,
			)
		case commandForget:
			if isGroup {
				return nil
			}
			return c.handleForgetCommand(
				ctx,
				chatID,
				messageThreadID,
				msg.MessageID,
				laneKey,
				fromID,
			)
		case commandJobs:
			return c.handleJobsCommand(
				ctx,
				chatID,
				messageThreadID,
				msg.MessageID,
				fromID,
			)
		case commandJobsClear:
			return c.handleJobsClearCommand(
				ctx,
				chatID,
				messageThreadID,
				msg.MessageID,
				fromID,
			)
		case commandPersona:
			return c.handlePersonaCommand(
				ctx,
				chatID,
				messageThreadID,
				msg.MessageID,
				laneKey,
				cmd.Args,
			)
		case commandPersonas:
			return c.handlePersonasCommand(
				ctx,
				chatID,
				messageThreadID,
				msg.MessageID,
				laneKey,
			)
		default:
			if !isGroup {
				c.reply(
					ctx,
					chatID,
					messageThreadID,
					msg.MessageID,
					helpMessage,
				)
			}
			return nil
		}
	}

	return c.lanes.withLockErr(laneKey, func() error {
		c.inflight.Set(laneKey, requestID)
		defer c.inflight.Clear(laneKey, requestID)

		return c.callGatewayAndReply(
			ctx,
			chatID,
			messageThreadID,
			msg.MessageID,
			fromID,
			thread,
			sessionID,
			requestID,
			msg,
		)
	})
}

func (c *Channel) handleMyChatMember(
	ctx context.Context,
	ev tgapi.ChatMemberEvent,
) error {
	if ev.Chat == nil {
		return nil
	}
	if strings.TrimSpace(ev.Chat.Type) != tgChatTypePrivate {
		return nil
	}

	status := ""
	if ev.NewChatMember != nil {
		status = strings.ToLower(strings.TrimSpace(ev.NewChatMember.Status))
	}
	if status != tgChatMemberStatusKicked &&
		status != tgChatMemberStatusLeft {
		return nil
	}

	userID := strconv.FormatInt(ev.Chat.ID, 10)
	laneKey := buildLaneKey(userID, "")
	c.cancelInflight(ctx, laneKey)

	switch strings.ToLower(strings.TrimSpace(c.dmBlockCleanup)) {
	case dmBlockCleanupNone:
		return nil
	case dmBlockCleanupReset:
		if c.dmSessions == nil {
			return nil
		}
		_, err := c.dmSessions.Rotate(ctx, userID, laneKey)
		return err
	case dmBlockCleanupForget:
		if f, ok := c.gw.(userForgetter); ok {
			if err := f.ForgetUser(ctx, channelID, userID); err != nil {
				return err
			}
		}
		if c.dmSessions != nil {
			_, err := c.dmSessions.ForgetUser(ctx, userID)
			return err
		}
		return nil
	default:
		return nil
	}
}

func (c *Channel) handleCallbackQuery(
	ctx context.Context,
	q tgapi.CallbackQuery,
) error {
	if q.Message == nil || q.Message.Chat == nil || q.From == nil {
		return c.answerCallbackQuery(ctx, q.ID, "", false)
	}

	data := strings.TrimSpace(q.Data)
	if data == "" {
		return c.answerCallbackQuery(ctx, q.ID, "", false)
	}

	chatID := q.Message.Chat.ID
	fromID := strconv.FormatInt(q.From.ID, 10)
	isGroup := tgapi.IsGroupChat(strings.TrimSpace(q.Message.Chat.Type))

	if !c.isUserAllowed(fromID) {
		return c.answerCallbackQuery(
			ctx,
			q.ID,
			notAllowedMessage,
			true,
		)
	}

	thread := ""
	messageThreadID := 0
	if isGroup {
		thread = strconv.FormatInt(chatID, 10)
		if q.Message.MessageThreadID != 0 {
			thread = fmt.Sprintf(
				"%s%s%d",
				thread,
				threadTopicSep,
				q.Message.MessageThreadID,
			)
			messageThreadID = q.Message.MessageThreadID
		}
	}

	if !c.isChatAllowed(isGroup, thread) {
		return c.answerCallbackQuery(ctx, q.ID, "", false)
	}
	if !isGroup {
		ok, err := c.isDMAllowed(ctx, chatID, fromID)
		if err != nil {
			return err
		}
		if !ok {
			return c.answerCallbackQuery(ctx, q.ID, "", false)
		}
	}

	switch {
	case isPersonaCallbackData(data):
		return c.handlePersonaCallbackQuery(
			ctx,
			q,
			buildLaneKey(fromID, thread),
			messageThreadID,
		)
	default:
		return c.answerCallbackQuery(ctx, q.ID, "", false)
	}
}

func (c *Channel) sendDM(
	ctx context.Context,
	chatID int64,
	text string,
) {
	_, err := c.sendTextMessage(ctx, tgapi.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
	if err != nil {
		log.WarnfContext(ctx, "telegram: send message: %v", err)
	}
}

func (c *Channel) answerCallbackQuery(
	ctx context.Context,
	callbackID string,
	text string,
	showAlert bool,
) error {
	if c == nil || c.bot == nil {
		return nil
	}
	if strings.TrimSpace(callbackID) == "" {
		return nil
	}
	return c.bot.AnswerCallbackQuery(
		ctx,
		tgapi.AnswerCallbackQueryParams{
			CallbackQueryID: callbackID,
			Text:            text,
			ShowAlert:       showAlert,
		},
	)
}

func (c *Channel) isUserAllowed(userID string) bool {
	if c.allowUsers == nil {
		return true
	}
	_, ok := c.allowUsers[userID]
	return ok
}

func (c *Channel) isChatAllowed(isGroup bool, thread string) bool {
	if !isGroup {
		return true
	}
	switch c.groupPolicy {
	case groupPolicyOpen:
		return true
	case groupPolicyDisabled:
		return false
	case groupPolicyAllowlist:
		if len(c.allowThreads) == 0 {
			return false
		}
		if _, ok := c.allowThreads[thread]; ok {
			return true
		}
		if idx := strings.Index(thread, threadTopicSep); idx > 0 {
			if _, ok := c.allowThreads[thread[:idx]]; ok {
				return true
			}
		}
		return false
	default:
		return false
	}
}

func (c *Channel) isDMAllowed(
	ctx context.Context,
	chatID int64,
	fromID string,
) (bool, error) {
	switch c.dmPolicy {
	case dmPolicyDisabled:
		return false, nil
	case dmPolicyOpen:
		return true, nil
	case dmPolicyAllowlist:
		if c.allowUsers == nil {
			c.sendDM(ctx, chatID, notAllowedMessage)
			return false, nil
		}
		if !c.isUserAllowed(fromID) {
			c.sendDM(ctx, chatID, notAllowedMessage)
			return false, nil
		}
		return true, nil
	case dmPolicyPairing:
		if c.pairing == nil {
			return false, errors.New("telegram: pairing store unavailable")
		}
		ok, err := c.pairing.IsApproved(ctx, fromID)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
		code, _, err := c.pairing.Request(ctx, fromID)
		if err != nil {
			return false, err
		}
		c.sendDM(
			ctx,
			chatID,
			fmt.Sprintf(pairingMessageTemplate, code, code),
		)
		return false, nil
	default:
		return false, fmt.Errorf(
			"telegram: unsupported dm policy: %s",
			c.dmPolicy,
		)
	}
}
