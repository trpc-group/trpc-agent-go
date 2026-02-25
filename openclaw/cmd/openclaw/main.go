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
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spaolacci/murmur3"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/log"
	meminmemory "trpc.group/trpc-go/trpc-agent-go/memory/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel"
	tgch "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gwclient"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/pairing"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
)

const (
	appName = "openclaw"

	defaultHTTPAddr = ":8080"

	modeMock   = "mock"
	modeOpenAI = "openai"

	defaultOpenAIModel = "gpt-5"

	defaultSkillsDir = "skills"
	defaultAgentsDir = ".agents"

	csvDelimiter = ","

	subcmdPairing = "pairing"

	pairingCmdList    = "list"
	pairingCmdApprove = "approve"

	defaultTelegramMaxRetries = 3

	openAIVariantAuto = "auto"

	defaultOpenAIVariant = openAIVariantAuto

	deepSeekModelHint = "deepseek"
	qwenModelHint     = "qwen"
	hunyuanModelHint  = "hunyuan"

	openAIBaseURLEnvName = "OPENAI_BASE_URL"
	openAIModelEnvName   = "OPENAI_MODEL"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case subcmdPairing:
			os.Exit(runPairing(os.Args[2:]))
		case subcmdDoctor:
			os.Exit(runDoctor(os.Args[2:]))
		}
	}

	httpAddr := flag.String(
		"http-addr",
		defaultHTTPAddr,
		"HTTP listen address for gateway endpoints",
	)
	modelMode := flag.String(
		"mode",
		modeOpenAI,
		"Model mode: mock or openai",
	)
	defaultModel := strings.TrimSpace(os.Getenv(openAIModelEnvName))
	if defaultModel == "" {
		defaultModel = defaultOpenAIModel
	}
	openAIModel := flag.String(
		"model",
		defaultModel,
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
	telegramProxy := flag.String(
		"telegram-proxy",
		"",
		"HTTP proxy URL for Telegram API calls (optional)",
	)
	telegramHTTPTimeout := flag.Duration(
		"telegram-http-timeout",
		0,
		"HTTP client timeout for Telegram API calls (optional)",
	)
	telegramMaxRetries := flag.Int(
		"telegram-max-retries",
		defaultTelegramMaxRetries,
		"Max retries for Telegram API calls (429/5xx/transport errors)",
	)
	telegramDMPolicy := flag.String(
		"telegram-dm-policy",
		"",
		"Telegram DM policy: disabled|open|allowlist|pairing",
	)
	telegramGroupPolicy := flag.String(
		"telegram-group-policy",
		"",
		"Telegram group policy: disabled|open|allowlist",
	)
	telegramAllowThreads := flag.String(
		"telegram-allow-threads",
		"",
		"Comma-separated allowlist of chat/topic threads",
	)
	telegramPairingTTL := flag.Duration(
		"telegram-pairing-ttl",
		time.Hour,
		"How long pairing codes stay valid",
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
	skillsExtraDirs := flag.String(
		"skills-extra-dirs",
		"",
		"Extra skills roots (comma-separated, lowest precedence)",
	)
	skillsDebug := flag.Bool(
		"skills-debug",
		false,
		"Log skill gating decisions",
	)
	stateDir := flag.String(
		"state-dir",
		"",
		"State dir for offsets and managed skills",
	)
	enableLocalExec := flag.Bool(
		"enable-local-exec",
		false,
		"Enable local code execution tool (unsafe)",
	)
	enableOpenClawTools := flag.Bool(
		"enable-openclaw-tools",
		false,
		"Enable OpenClaw-compatible exec/process tools (unsafe)",
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
		tgapiOpts   []telegramAPIOption
		err         error
	)
	if strings.TrimSpace(*telegramToken) != "" {
		tgapiOpts, err = makeTelegramAPIOptions(
			*telegramProxy,
			*telegramHTTPTimeout,
			*telegramMaxRetries,
		)
		if err != nil {
			log.Fatalf("telegram config failed: %v", err)
		}

		telegramBot, err = tgch.ProbeBotInfo(
			ctx,
			*telegramToken,
			tgapiOpts...,
		)
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

	resolvedStateDir, err := resolveStateDir(*stateDir)
	if err != nil {
		log.Fatalf("resolve state dir failed: %v", err)
	}
	log.Infof(
		"Instance: %s",
		configFingerprint(*modelMode, *openAIModel, resolvedStateDir),
	)

	sessionSvc := sessioninmemory.NewSessionService()
	defer func() {
		if err := sessionSvc.Close(); err != nil {
			log.Warnf("close session service failed: %v", err)
		}
	}()

	memSvc := meminmemory.NewMemoryService()
	defer func() {
		if err := memSvc.Close(); err != nil {
			log.Warnf("close memory service failed: %v", err)
		}
	}()

	llm, err := newAgent(mdl, agentConfig{
		SkillsRoot:          *skillsRoot,
		SkillsExtraDirs:     splitCSV(*skillsExtraDirs),
		SkillsDebug:         *skillsDebug,
		StateDir:            resolvedStateDir,
		EnableLocalExec:     *enableLocalExec,
		EnableOpenClawTools: *enableOpenClawTools,
	}, memSvc.Tools())
	if err != nil {
		log.Fatalf("create agent failed: %v", err)
	}

	r := runner.NewRunner(
		appName,
		llm,
		runner.WithSessionService(sessionSvc),
		runner.WithMemoryService(memSvc),
	)

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
		users := splitCSV(*allowUsers)
		threads := splitCSV(*telegramAllowThreads)
		ch, err := tgch.New(
			*telegramToken,
			telegramBot,
			gw,
			tgch.WithAPIOptions(tgapiOpts...),
			tgch.WithStateDir(resolvedStateDir),
			tgch.WithStartFromLatest(*telegramStartFromLatest),
			tgch.WithDMPolicy(*telegramDMPolicy),
			tgch.WithGroupPolicy(*telegramGroupPolicy),
			tgch.WithAllowUsers(users...),
			tgch.WithAllowThreads(threads...),
			tgch.WithPairingTTL(*telegramPairingTTL),
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
	cfg agentConfig,
	extraTools []tool.Tool,
) (agent.Agent, error) {
	opts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithInstruction(
			"You are a helpful assistant. Keep replies concise.",
		),
	}

	cwd, _ := os.Getwd()
	roots := resolveSkillRoots(cwd, cfg)
	repo, err := ocskills.NewRepository(
		roots,
		ocskills.WithDebug(cfg.SkillsDebug),
	)
	if err != nil {
		return nil, err
	}

	opts = append(opts, llmagent.WithSkills(repo))

	tools := append([]tool.Tool(nil), extraTools...)
	if cfg.EnableOpenClawTools {
		mgr := octool.NewManager()
		tools = append(tools,
			octool.NewExecTool("exec", mgr),
			octool.NewExecTool("bash", mgr),
			octool.NewProcessTool(mgr),
		)
	}
	if len(tools) > 0 {
		opts = append(opts, llmagent.WithTools(tools))
	}
	if cfg.EnableLocalExec {
		exec := localexec.New()
		opts = append(opts, llmagent.WithCodeExecutor(exec))
	}

	return llmagent.New("assistant", opts...), nil
}

type agentConfig struct {
	SkillsRoot      string
	SkillsExtraDirs []string
	SkillsDebug     bool

	StateDir string

	EnableLocalExec bool

	EnableOpenClawTools bool
}

func resolveSkillRoots(cwd string, cfg agentConfig) []string {
	workspaceSkills := resolveWorkspaceSkillsRoot(cwd, cfg.SkillsRoot)
	projectAgentsSkills := filepath.Join(
		cwd,
		defaultAgentsDir,
		defaultSkillsDir,
	)
	home, _ := os.UserHomeDir()
	personalAgentsSkills := filepath.Join(
		home,
		defaultAgentsDir,
		defaultSkillsDir,
	)
	managedSkills := filepath.Join(cfg.StateDir, defaultSkillsDir)
	bundledSkills := filepath.Join(cwd, appName, defaultSkillsDir)

	roots := make([]string, 0, 6+len(cfg.SkillsExtraDirs))
	roots = append(roots, workspaceSkills)
	roots = append(roots, projectAgentsSkills)
	roots = append(roots, personalAgentsSkills)
	roots = append(roots, managedSkills)
	if bundledSkills != workspaceSkills {
		roots = append(roots, bundledSkills)
	}
	roots = append(roots, cfg.SkillsExtraDirs...)
	return roots
}

func resolveWorkspaceSkillsRoot(cwd, raw string) string {
	root := strings.TrimSpace(raw)
	if root != "" {
		return root
	}

	cwdSkills := filepath.Join(cwd, defaultSkillsDir)
	if dirExists(cwdSkills) {
		return cwdSkills
	}

	repoBundled := filepath.Join(cwd, appName, defaultSkillsDir)
	if dirExists(repoBundled) {
		return repoBundled
	}
	return cwdSkills
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return st.IsDir()
}

func resolveStateDir(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s != "" {
		return s, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".trpc-agent-go", appName), nil
}

func configFingerprint(parts ...string) string {
	joined := strings.Join(parts, "\n")
	sum := murmur3.Sum32([]byte(joined))
	return fmt.Sprintf("%08x", sum)
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

func runPairing(args []string) int {
	fs := flag.NewFlagSet(subcmdPairing, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	token := fs.String(
		"telegram-token",
		"",
		"Telegram bot token (required)",
	)
	stateDir := fs.String(
		"state-dir",
		"",
		"State dir (default: $HOME/.trpc-agent-go/openclaw)",
	)

	if err := fs.Parse(args); err != nil {
		return 2
	}

	rest := fs.Args()
	if len(rest) == 0 {
		printPairingUsage()
		return 2
	}
	action := rest[0]

	ctx := context.Background()
	switch action {
	case pairingCmdList:
		return runPairingList(ctx, *token, *stateDir)
	case pairingCmdApprove:
		if len(rest) < 2 {
			fmt.Fprintln(os.Stderr, "missing pairing code")
			printPairingUsage()
			return 2
		}
		return runPairingApprove(ctx, *token, *stateDir, rest[1])
	default:
		fmt.Fprintf(os.Stderr, "unknown pairing command: %s\n", action)
		printPairingUsage()
		return 2
	}
}

func printPairingUsage() {
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr,
		"  openclaw pairing list -telegram-token <TOKEN> [-state-dir <DIR>]",
	)
	fmt.Fprintln(os.Stderr,
		"  openclaw pairing approve <CODE> -telegram-token <TOKEN>"+
			" [-state-dir <DIR>]",
	)
}

func runPairingList(
	ctx context.Context,
	token string,
	rawStateDir string,
) int {
	store, err := openPairingStore(ctx, token, rawStateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	pending, err := store.ListPending(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	sort.Slice(pending, func(i, j int) bool {
		return pending[i].CreatedAt.Before(pending[j].CreatedAt)
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "CODE\tUSER_ID\tEXPIRES_AT")
	for _, req := range pending {
		fmt.Fprintf(
			w,
			"%s\t%s\t%s\n",
			req.Code,
			req.UserID,
			req.ExpiresAt.UTC().Format(time.RFC3339),
		)
	}
	_ = w.Flush()
	return 0
}

func runPairingApprove(
	ctx context.Context,
	token string,
	rawStateDir string,
	code string,
) int {
	store, err := openPairingStore(ctx, token, rawStateDir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	userID, ok, err := store.Approve(ctx, code)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if !ok {
		fmt.Fprintln(os.Stderr, "pairing code not found or expired")
		return 1
	}
	fmt.Printf("approved user: %s\n", userID)
	return 0
}

func openPairingStore(
	ctx context.Context,
	token string,
	rawStateDir string,
) (*pairing.FileStore, error) {
	if strings.TrimSpace(token) == "" {
		return nil, errors.New("pairing: missing -telegram-token")
	}

	resolvedStateDir, err := resolveStateDir(rawStateDir)
	if err != nil {
		return nil, err
	}

	bot, err := tgch.ProbeBotInfo(ctx, token)
	if err != nil {
		return nil, err
	}

	path, err := tgch.PairingStorePath(resolvedStateDir, bot)
	if err != nil {
		return nil, err
	}

	return pairing.NewFileStore(path)
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
