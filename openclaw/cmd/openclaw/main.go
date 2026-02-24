//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

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

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel"
	tgch "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gwclient"
)

const (
	appName = "openclaw"

	defaultHTTPAddr = ":8080"

	modeMock   = "mock"
	modeOpenAI = "openai"

	defaultOpenAIModel = "deepseek-chat"

	defaultSkillsDir = "skills"

	csvDelimiter = ","

	openAIVariantAuto = "auto"

	defaultOpenAIVariant = openAIVariantAuto

	deepSeekModelHint = "deepseek"
	qwenModelHint     = "qwen"
	hunyuanModelHint  = "hunyuan"

	openAIBaseURLEnvName = "OPENAI_BASE_URL"
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
	openAIVariant := flag.String(
		"openai-variant",
		defaultOpenAIVariant,
		"OpenAI variant: auto, openai, deepseek, qwen, hunyuan",
	)
	telegramToken := flag.String(
		"telegram-token",
		"",
		"Telegram bot token; empty disables Telegram",
	)
	telegramStartFromLatest := flag.Bool(
		"telegram-start-from-latest",
		true,
		"Drain pending updates on first start (no offset)",
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
	stateDir := flag.String(
		"state-dir",
		"",
		"State dir for offsets (default: $HOME/.trpc-agent-go/openclaw)",
	)
	enableLocalExec := flag.Bool(
		"enable-local-exec",
		false,
		"Enable local code execution tool (unsafe)",
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	var (
		telegramBot tgch.BotInfo
		err         error
	)
	if strings.TrimSpace(*telegramToken) != "" {
		telegramBot, err = tgch.ProbeBotInfo(ctx, *telegramToken)
		if err != nil {
			log.Fatalf("probe telegram bot failed: %v", err)
		}

		if strings.TrimSpace(telegramBot.Username) != "" {
			log.Infof(
				"Telegram enabled as @%s",
				telegramBot.Username,
			)
		} else if telegramBot.ID != 0 {
			log.Infof("Telegram enabled as id %d", telegramBot.ID)
		} else {
			log.Infof("Telegram enabled")
		}
	}

	mentionPatterns := splitCSV(*mention)
	if *requireMention &&
		len(mentionPatterns) == 0 &&
		telegramBot.Mention != "" {
		mentionPatterns = []string{telegramBot.Mention}
	}

	mdl, err := newModel(*modelMode, *openAIModel, *openAIVariant)
	if err != nil {
		log.Fatalf("create model failed: %v", err)
	}

	llm, err := newAgent(mdl, *skillsRoot, *enableLocalExec)
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

	var channels []channel.Channel
	if strings.TrimSpace(*telegramToken) != "" {
		ch, err := tgch.New(
			*telegramToken,
			telegramBot,
			gw,
			tgch.WithStateDir(*stateDir),
			tgch.WithStartFromLatest(*telegramStartFromLatest),
		)
		if err != nil {
			log.Fatalf("create telegram channel failed: %v", err)
		}
		channels = append(channels, ch)
	}

	for _, ch := range channels {
		ch := ch
		go func() {
			errCh <- ch.Run(ctx)
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

func newAgent(
	mdl model.Model,
	skillsRoot string,
	enableLocalExec bool,
) (agent.Agent, error) {
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

	opts = append(opts, llmagent.WithSkills(repo))
	if enableLocalExec {
		exec := localexec.New()
		opts = append(opts, llmagent.WithCodeExecutor(exec))
	}

	return llmagent.New("assistant", opts...), nil
}

func newModel(
	mode string,
	openAIModel string,
	openAIVariant string,
) (model.Model, error) {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case modeMock:
		return &echoModel{name: "mock-echo"}, nil
	case modeOpenAI:
		variant, err := parseOpenAIVariant(openAIVariant, openAIModel)
		if err != nil {
			return nil, err
		}
		opts := []openai.Option{openai.WithVariant(variant)}
		baseURL := strings.TrimSpace(os.Getenv(openAIBaseURLEnvName))
		if baseURL != "" {
			opts = append(opts, openai.WithBaseURL(baseURL))
		}
		return openai.New(openAIModel, opts...), nil
	default:
		return nil, fmt.Errorf("unsupported mode: %s", mode)
	}
}

func parseOpenAIVariant(
	raw string,
	modelName string,
) (openai.Variant, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || v == openAIVariantAuto {
		return inferOpenAIVariant(modelName), nil
	}

	variant := openai.Variant(v)
	switch variant {
	case openai.VariantOpenAI,
		openai.VariantDeepSeek,
		openai.VariantHunyuan,
		openai.VariantQwen:
		return variant, nil
	default:
		return "", fmt.Errorf("unsupported openai variant: %s", raw)
	}
}

func inferOpenAIVariant(modelName string) openai.Variant {
	name := strings.ToLower(strings.TrimSpace(modelName))
	switch {
	case strings.Contains(name, deepSeekModelHint):
		return openai.VariantDeepSeek
	case strings.Contains(name, qwenModelHint):
		return openai.VariantQwen
	case strings.Contains(name, hunyuanModelHint):
		return openai.VariantHunyuan
	default:
		return openai.VariantOpenAI
	}
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
