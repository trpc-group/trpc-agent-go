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
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/log"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/pairing"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gwclient"
	tgapi "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const (
	channelID = "telegram"

	requestIDPrefix = "telegram:"

	threadTopicSep = ":topic:"

	maxReplyRunes = 4000

	defaultStateRootDir = ".trpc-agent-go"
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

	defaultPairingTTL = time.Hour
)

const (
	notAllowedMessage = "You are not allowed to use this bot."

	pairingMessageTemplate = `Pairing required.

Code: %s

Ask the operator to approve:
openclaw pairing approve %s`
)

type gatewayClient interface {
	SendMessage(
		ctx context.Context,
		req gwclient.MessageRequest,
	) (gwclient.MessageResponse, error)
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
	gw    gatewayClient
	store tgapi.OffsetStore

	startFromLatest bool
	pollTimeout     time.Duration
	errorBackoff    time.Duration

	dmPolicy    string
	groupPolicy string

	allowUsers   map[string]struct{}
	allowThreads map[string]struct{}

	pairing pairingStore
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
		startFromLatest: true,
		pollTimeout:     25 * time.Second,
		errorBackoff:    1 * time.Second,
		dmPolicy:        defaultDMPolicy,
		groupPolicy:     defaultGroupPolicy,
		pairingTTL:      defaultPairingTTL,
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

	api, err := tgapi.New(token)
	if err != nil {
		return nil, err
	}

	store, err := newOffsetStore(stateDir, bot)
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
		bot:             api,
		gw:              gw,
		store:           store,
		startFromLatest: cfg.startFromLatest,
		pollTimeout:     cfg.pollTimeout,
		errorBackoff:    cfg.errorBackoff,
		dmPolicy:        dmPolicy,
		groupPolicy:     groupPolicy,
		allowUsers:      cfg.allowUsers,
		allowThreads:    cfg.allowThreads,
		pairing:         dmPairing,
	}, nil
}

// ID returns the channel identifier used by the gateway.
func (c *Channel) ID() string { return channelID }

// Run starts polling Telegram and blocks until ctx is done.
func (c *Channel) Run(ctx context.Context) error {
	if c == nil {
		return errors.New("telegram: nil channel")
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
			return c.handleMessage(ctx, msg)
		}),
	)
	if err != nil {
		return err
	}
	return poller.Run(ctx)
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

	rsp, err := c.gw.SendMessage(ctx, gwclient.MessageRequest{
		Channel:   channelID,
		From:      fromID,
		Thread:    thread,
		MessageID: strconv.Itoa(msg.MessageID),
		Text:      msg.Text,
		UserID:    fromID,
		RequestID: requestID,
	})
	if err != nil {
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
	if rsp.Ignored || strings.TrimSpace(rsp.Reply) == "" {
		return nil
	}

	parts := splitRunes(rsp.Reply, maxReplyRunes)
	for i, part := range parts {
		replyTo := 0
		if i == 0 {
			replyTo = msg.MessageID
		}
		_, err := c.bot.SendMessage(ctx, tgapi.SendMessageParams{
			ChatID:           chatID,
			MessageThreadID:  messageThreadID,
			ReplyToMessageID: replyTo,
			Text:             part,
		})
		if err != nil {
			log.WarnfContext(ctx, "telegram: send message: %v", err)
			return nil
		}
	}
	return nil
}

func (c *Channel) sendDM(
	ctx context.Context,
	chatID int64,
	text string,
) {
	_, err := c.bot.SendMessage(ctx, tgapi.SendMessageParams{
		ChatID: chatID,
		Text:   text,
	})
	if err != nil {
		log.WarnfContext(ctx, "telegram: send message: %v", err)
	}
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
		n := maxRunes
		if len(runes) < n {
			n = len(runes)
		}
		out = append(out, string(runes[:n]))
		runes = runes[n:]
	}
	return out
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
