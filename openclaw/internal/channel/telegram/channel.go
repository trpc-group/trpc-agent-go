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
func ProbeBotInfo(ctx context.Context, token string) (BotInfo, error) {
	if strings.TrimSpace(token) == "" {
		return BotInfo{}, nil
	}
	c, err := tgapi.New(token)
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

// Channel implements a Telegram long-polling chat surface.
type Channel struct {
	bot   botAPI
	gw    gatewayClient
	store tgapi.OffsetStore

	startFromLatest bool
	pollTimeout     time.Duration
	errorBackoff    time.Duration
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
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	stateDir, err := resolveStateDir(cfg.stateDir)
	if err != nil {
		return nil, err
	}

	api, err := tgapi.New(token)
	if err != nil {
		return nil, err
	}

	store, err := newOffsetStore(stateDir, bot)
	if err != nil {
		return nil, err
	}

	return &Channel{
		bot:             api,
		gw:              gw,
		store:           store,
		startFromLatest: cfg.startFromLatest,
		pollTimeout:     cfg.pollTimeout,
		errorBackoff:    cfg.errorBackoff,
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

	thread := ""
	messageThreadID := 0
	if tgapi.IsGroupChat(strings.TrimSpace(msg.Chat.Type)) {
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
