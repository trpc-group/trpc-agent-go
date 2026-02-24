// Package main provides an OpenClaw-like binary that wires:
// - HTTP gateway endpoints (webhook-friendly)
// - Telegram long-polling as a chat channel
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/gateway"
	"trpc.group/trpc-go/trpc-agent-go/skill"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/telegram"
)

const (
	appName = "openclaw"

	defaultHTTPAddr = ":8080"

	modeMock   = "mock"
	modeOpenAI = "openai"

	defaultOpenAIModel = "deepseek-chat"

	defaultSkillsDir = "skills"

	csvDelimiter = ","
)

const (
	channelTelegram = "telegram"

	telegramRequestIDPrefix = "telegram:"

	telegramMaxReplyRunes = 4000
)

func main() {
	httpAddr := flag.String(
		"http-addr",
		defaultHTTPAddr,
		"HTTP listen address for gateway endpoints",
	)
	modelMode := flag.String(
		"mode",
		modeMock,
		"Model mode: mock or openai",
	)
	openAIModel := flag.String(
		"model",
		defaultOpenAIModel,
		"OpenAI model name (mode=openai)",
	)
	telegramToken := flag.String(
		"telegram-token",
		"",
		"Telegram bot token; empty disables Telegram",
	)
	allowUsers := flag.String(
		"allow-users",
		"",
		"Comma-separated allowlist; empty allows all",
	)
	requireMention := flag.Bool(
		"require-mention",
		false,
		"Require mention in thread/group messages",
	)
	mention := flag.String(
		"mention",
		"",
		"Comma-separated mention patterns",
	)
	skillsRoot := flag.String(
		"skills-root",
		"",
		"Skills root directory (default: ./skills)",
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	telegramClient, botMention, err := setupTelegram(
		ctx,
		*telegramToken,
	)
	if err != nil {
		log.Fatalf("setup telegram failed: %v", err)
	}

	mentionPatterns := splitCSV(*mention)
	if *requireMention && len(mentionPatterns) == 0 && botMention != "" {
		mentionPatterns = []string{botMention}
	}

	mdl, err := newModel(*modelMode, *openAIModel)
	if err != nil {
		log.Fatalf("create model failed: %v", err)
	}

	llm, err := newAgent(mdl, *skillsRoot)
	if err != nil {
		log.Fatalf("create agent failed: %v", err)
	}

	r := runner.NewRunner(appName, llm)

	gwOpts := makeGatewayOptions(
		splitCSV(*allowUsers),
		*requireMention,
		mentionPatterns,
	)
	gwSrv, err := gateway.New(r, gwOpts...)
	if err != nil {
		log.Fatalf("create gateway failed: %v", err)
	}

	gw, err := gwclient.New(gwSrv.Handler(), gwSrv.MessagesPath())
	if err != nil {
		log.Fatalf("create gateway client failed: %v", err)
	}

	httpSrv := &http.Server{
		Addr:              *httpAddr,
		Handler:           gwSrv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	errCh := make(chan error, 2)
	go func() {
		log.Infof("Gateway listening on %s", httpSrv.Addr)
		log.Infof("Health:   GET  %s", gwSrv.HealthPath())
		log.Infof("Messages: POST %s", gwSrv.MessagesPath())
		log.Infof("Status:   GET  %s?request_id=...", gwSrv.StatusPath())
		log.Infof("Cancel:   POST %s", gwSrv.CancelPath())
		//nolint:gosec
		errCh <- httpSrv.ListenAndServe()
	}()

	if telegramClient != nil {
		go func() {
			errCh <- runTelegram(ctx, telegramClient, gw)
		}()
	}

	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Errorf("server error: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	_ = httpSrv.Shutdown(shutdownCtx)
	_ = r.Close()
}

func setupTelegram(
	ctx context.Context,
	token string,
) (*telegram.Client, string, error) {
	if strings.TrimSpace(token) == "" {
		return nil, "", nil
	}

	c, err := telegram.New(token)
	if err != nil {
		return nil, "", err
	}

	me, err := c.GetMe(ctx)
	if err != nil {
		return nil, "", err
	}

	mention := ""
	if strings.TrimSpace(me.Username) != "" {
		mention = "@" + strings.TrimSpace(me.Username)
	}

	log.Infof("Telegram enabled as @%s", me.Username)
	return c, mention, nil
}

func makeGatewayOptions(
	users []string,
	requireMention bool,
	mentionPatterns []string,
) []gateway.Option {
	opts := make([]gateway.Option, 0, 4)
	if len(users) > 0 {
		opts = append(opts, gateway.WithAllowUsers(users...))
	}
	if requireMention {
		opts = append(opts, gateway.WithRequireMentionInThreads(true))
	}
	if len(mentionPatterns) > 0 {
		opts = append(opts, gateway.WithMentionPatterns(mentionPatterns...))
	}
	return opts
}

func newAgent(mdl model.Model, skillsRoot string) (agent.Agent, error) {
	opts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithInstruction(
			"You are a helpful assistant. Keep replies concise.",
		),
	}

	root := strings.TrimSpace(skillsRoot)
	if root == "" {
		cwd, _ := os.Getwd()
		root = filepath.Join(cwd, defaultSkillsDir)
	}
	repo, err := skill.NewFSRepository(root)
	if err != nil {
		return nil, err
	}

	exec := localexec.New()

	opts = append(
		opts,
		llmagent.WithSkills(repo),
		llmagent.WithCodeExecutor(exec),
	)

	return llmagent.New("assistant", opts...), nil
}

func newModel(mode string, openAIModel string) (model.Model, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case modeMock:
		return &echoModel{name: "mock-echo"}, nil
	case modeOpenAI:
		return openai.New(openAIModel), nil
	default:
		return nil, fmt.Errorf("unsupported mode: %s", mode)
	}
}

func runTelegram(
	ctx context.Context,
	client *telegram.Client,
	gw *gwclient.Client,
) error {
	poller, err := telegram.NewPoller(
		client,
		telegram.WithMessageHandler(func(
			ctx context.Context,
			msg telegram.Message,
		) error {
			return handleTelegramMessage(ctx, client, gw, msg)
		}),
	)
	if err != nil {
		return err
	}
	return poller.Run(ctx)
}

func handleTelegramMessage(
	ctx context.Context,
	client *telegram.Client,
	gw *gwclient.Client,
	msg telegram.Message,
) error {
	if msg.Chat == nil || msg.From == nil {
		return nil
	}

	chatID := msg.Chat.ID
	fromID := strconv.FormatInt(msg.From.ID, 10)
	thread := ""
	if telegram.IsGroupChat(strings.TrimSpace(msg.Chat.Type)) {
		thread = strconv.FormatInt(chatID, 10)
	}

	requestID := fmt.Sprintf(
		"%s%d:%d",
		telegramRequestIDPrefix,
		chatID,
		msg.MessageID,
	)

	rsp, err := gw.SendMessage(ctx, gwclient.MessageRequest{
		Channel:   channelTelegram,
		From:      fromID,
		Thread:    thread,
		MessageID: strconv.Itoa(msg.MessageID),
		Text:      msg.Text,
		UserID:    fromID,
		RequestID: requestID,
	})
	if err != nil {
		log.WarnfContext(ctx, "telegram: gateway error: %v", err)
		return nil
	}
	if rsp.Ignored || strings.TrimSpace(rsp.Reply) == "" {
		return nil
	}

	parts := splitRunes(rsp.Reply, telegramMaxReplyRunes)
	for i, part := range parts {
		replyTo := 0
		if i == 0 {
			replyTo = msg.MessageID
		}
		_, err := client.SendMessage(ctx, chatID, replyTo, part)
		if err != nil {
			log.WarnfContext(ctx, "telegram: send message: %v", err)
			return nil
		}
	}
	return nil
}

func splitCSV(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	parts := strings.Split(input, csvDelimiter)
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return out
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

type echoModel struct {
	name string
}

func (m *echoModel) Info() model.Info {
	return model.Info{Name: m.name}
}

func (m *echoModel) GenerateContent(
	ctx context.Context,
	req *model.Request,
) (<-chan *model.Response, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nil context")
	}

	ch := make(chan *model.Response, 1)
	text := lastUserText(req)
	reply := fmt.Sprintf("Echo: %s", text)
	ch <- &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Model:  m.name,
		Choices: []model.Choice{
			{Message: model.NewAssistantMessage(reply)},
		},
		Done: true,
	}
	close(ch)
	return ch, nil
}

func lastUserText(req *model.Request) string {
	if req == nil {
		return ""
	}
	for i := len(req.Messages) - 1; i >= 0; i-- {
		msg := req.Messages[i]
		if msg.Role != model.RoleUser {
			continue
		}
		return msg.Content
	}
	return ""
}
