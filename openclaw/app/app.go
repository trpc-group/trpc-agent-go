//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package app provides an OpenClaw-like runnable wiring for
// `trpc-agent-go`.
//
// It is designed to be imported by downstream distributions that want to
// inject internal-only plugins (channels, backends, tools) via anonymous
// imports.
package app

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/claudecode"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/gwclient"
	tgch "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/channel/telegram"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
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

	defaultAgentName = "assistant"

	agentTypeLLM        = "llm"
	agentTypeClaudeCode = "claude-code"

	defaultTelegramMaxRetries = 3
	defaultTelegramStreaming  = "progress"

	openAIVariantAuto = "auto"

	defaultOpenAIVariant = openAIVariantAuto

	deepSeekModelHint = "deepseek"
	qwenModelHint     = "qwen"
	hunyuanModelHint  = "hunyuan"

	openAIBaseURLEnvName = "OPENAI_BASE_URL"
	openAIModelEnvName   = "OPENAI_MODEL"
)

// Main runs the OpenClaw-like CLI and returns an exit code.
//
// args should not include the program name.
func Main(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case subcmdPairing:
			return runPairing(args[1:])
		case subcmdDoctor:
			return runDoctor(args[1:])
		case subcmdInspect:
			return runInspect(args[1:])
		}
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := run(ctx, args); err != nil {
		var exitErr *exitError
		if errors.As(err, &exitErr) {
			log.Errorf("%v", exitErr.Err)
			return exitErr.Code
		}
		log.Errorf("%v", err)
		return 1
	}
	return 0
}

type exitError struct {
	Code int
	Err  error
}

func (e *exitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func run(ctx context.Context, args []string) error {
	opts, err := parseRunOptions(args)
	if err != nil {
		return err
	}

	agentType, err := normalizeAgentType(opts.AgentType)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent config failed: %w", err),
		}
	}
	if err := validateAgentRunOptions(agentType, opts); err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent config failed: %w", err),
		}
	}

	var (
		telegramBot tgch.BotInfo
		tgapiOpts   []telegramAPIOption
	)
	if strings.TrimSpace(opts.TelegramToken) != "" {
		tgapiOpts, err = makeTelegramAPIOptions(
			opts.TelegramProxy,
			opts.TelegramHTTPTimeout,
			opts.TelegramMaxRetries,
		)
		if err != nil {
			return &exitError{
				Code: 1,
				Err:  fmt.Errorf("telegram config failed: %w", err),
			}
		}

		telegramBot, err = tgch.ProbeBotInfo(
			ctx,
			opts.TelegramToken,
			tgapiOpts...,
		)
		if err != nil {
			return &exitError{
				Code: 1,
				Err:  fmt.Errorf("probe telegram bot failed: %w", err),
			}
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

	mentionPatterns := splitCSV(opts.Mention)
	if opts.RequireMention &&
		len(mentionPatterns) == 0 &&
		telegramBot.Mention != "" {
		mentionPatterns = []string{telegramBot.Mention}
	}

	resolvedStateDir, err := resolveStateDir(opts.StateDir)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("resolve state dir failed: %w", err),
		}
	}

	needsModel := agentType == agentTypeLLM ||
		opts.SessionSummaryEnabled ||
		opts.MemoryAutoEnabled

	var mdl model.Model
	if needsModel {
		mdl, err = modelFromOptions(opts)
		if err != nil {
			return &exitError{
				Code: 1,
				Err:  fmt.Errorf("create model failed: %w", err),
			}
		}
	}

	if agentType == agentTypeLLM {
		log.Infof(
			"Instance: %s",
			configFingerprint(
				opts.ModelMode,
				opts.OpenAIModel,
				resolvedStateDir,
			),
		)
	} else {
		parts := []string{
			agentType,
			strings.TrimSpace(opts.ClaudeBin),
			strings.TrimSpace(opts.ClaudeOutputFormat),
			resolvedStateDir,
		}
		if needsModel {
			parts = append(parts, opts.ModelMode, opts.OpenAIModel)
		}
		log.Infof("Instance: %s", configFingerprint(parts...))
	}

	sessionSvc, err := newSessionService(mdl, opts)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create session service failed: %w", err),
		}
	}
	defer closeSessionService(sessionSvc)

	memSvc, err := newMemoryService(mdl, opts)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create memory service failed: %w", err),
		}
	}
	defer closeMemoryService(memSvc)

	var (
		toolSets []tool.ToolSet
		ag       agent.Agent
	)
	defer func() {
		closeToolSets(toolSets)
	}()
	if agentType == agentTypeClaudeCode {
		ag, err = newClaudeCodeAgent(opts)
	} else {
		toolSets, err = toolSetsFromProviders(
			mdl,
			opts.AppName,
			resolvedStateDir,
			opts.ToolSets,
		)
		if err != nil {
			return &exitError{
				Code: 1,
				Err:  fmt.Errorf("create toolsets failed: %w", err),
			}
		}
		ag, err = newAgent(mdl, agentConfig{
			AppName:           opts.AppName,
			AddSessionSummary: opts.AddSessionSummary,
			MaxHistoryRuns:    opts.MaxHistoryRuns,
			PreloadMemory:     opts.PreloadMemory,

			SkillsRoot:      opts.SkillsRoot,
			SkillsExtraDirs: splitCSV(opts.SkillsExtraDir),
			SkillsDebug:     opts.SkillsDebug,
			SkillConfigKeys: resolveSkillConfigKeys(opts),
			StateDir:        resolvedStateDir,

			EnableLocalExec:     opts.EnableLocalExec,
			EnableOpenClawTools: opts.EnableOpenClawTools,

			ToolProviders: opts.ToolProviders,
			ToolSets:      opts.ToolSets,

			RefreshToolSetsOnRun: opts.RefreshToolSetsOnRun,
		}, memSvc.Tools(), toolSets)
	}
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create agent failed: %w", err),
		}
	}

	r := runner.NewRunner(
		opts.AppName,
		ag,
		runner.WithSessionService(sessionSvc),
		runner.WithMemoryService(memSvc),
	)

	gwOpts := makeGatewayOptions(
		splitCSV(opts.AllowUsers),
		opts.RequireMention,
		mentionPatterns,
	)
	gwSrv, err := gateway.New(r, gwOpts...)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create gateway failed: %w", err),
		}
	}

	gw, err := gwclient.New(
		gwSrv.Handler(),
		gwSrv.MessagesPath(),
		gwSrv.CancelPath(),
	)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create gateway client failed: %w", err),
		}
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	httpSrv := &http.Server{
		Addr:              opts.HTTPAddr,
		Handler:           gwSrv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	var channels []channel.Channel
	if strings.TrimSpace(opts.TelegramToken) != "" {
		users := splitCSV(opts.AllowUsers)
		threads := splitCSV(opts.TelegramAllowThreads)
		ch, err := tgch.New(
			opts.TelegramToken,
			telegramBot,
			gw,
			tgch.WithAPIOptions(tgapiOpts...),
			tgch.WithStateDir(resolvedStateDir),
			tgch.WithStartFromLatest(opts.TelegramStartFromLatest),
			tgch.WithStreamingMode(opts.TelegramStreaming),
			tgch.WithDMPolicy(opts.TelegramDMPolicy),
			tgch.WithGroupPolicy(opts.TelegramGroupPolicy),
			tgch.WithAllowUsers(users...),
			tgch.WithAllowThreads(threads...),
			tgch.WithPairingTTL(opts.TelegramPairingTTL),
		)
		if err != nil {
			return &exitError{
				Code: 1,
				Err:  fmt.Errorf("create telegram channel failed: %w", err),
			}
		}
		channels = append(channels, ch)
	}

	if len(opts.Channels) > 0 {
		extra, err := channelsFromRegistry(
			gw,
			opts.AppName,
			resolvedStateDir,
			opts.Channels,
		)
		if err != nil {
			return &exitError{
				Code: 1,
				Err:  fmt.Errorf("create channels failed: %w", err),
			}
		}
		channels = append(channels, extra...)
	}

	workerCount := 1 + len(channels)
	errCh := make(chan error, workerCount)

	go func() {
		log.Infof("Gateway listening on %s", httpSrv.Addr)
		log.Infof("Health:   GET  %s", gwSrv.HealthPath())
		log.Infof("Messages: POST %s", gwSrv.MessagesPath())
		log.Infof("Status:   GET  %s?request_id=...", gwSrv.StatusPath())
		log.Infof("Cancel:   POST %s", gwSrv.CancelPath())
		//nolint:gosec
		errCh <- httpSrv.ListenAndServe()
	}()

	for _, ch := range channels {
		ch := ch
		go func() {
			errCh <- ch.Run(runCtx)
		}()
	}

	received := 0
	select {
	case <-ctx.Done():
	case err := <-errCh:
		received++
		if err != nil && err != http.ErrServerClosed {
			log.Errorf("server error: %v", err)
		}
	}

	cancelRun()

	shutdownCtx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()

	_ = httpSrv.Shutdown(shutdownCtx)
	_ = r.Close()

	for received < workerCount {
		select {
		case err := <-errCh:
			received++
			if err != nil && err != http.ErrServerClosed {
				log.Errorf("server error: %v", err)
			}
		case <-shutdownCtx.Done():
			return nil
		}
	}

	return nil
}

type closeFunc interface {
	Close() error
}

func closeSessionService(svc closeFunc) {
	if svc == nil {
		return
	}
	if err := svc.Close(); err != nil {
		log.Warnf("close session service failed: %v", err)
	}
}

func closeMemoryService(svc closeFunc) {
	if svc == nil {
		return
	}
	if err := svc.Close(); err != nil {
		log.Warnf("close memory service failed: %v", err)
	}
}

func closeToolSets(sets []tool.ToolSet) {
	for _, ts := range sets {
		if ts == nil {
			continue
		}
		if err := ts.Close(); err != nil {
			log.Warnf("close toolset %q failed: %v", ts.Name(), err)
		}
	}
}

func defaultOpenAIModelName() string {
	modelName := strings.TrimSpace(os.Getenv(openAIModelEnvName))
	if modelName != "" {
		return modelName
	}
	return defaultOpenAIModel
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

func normalizeAgentType(raw string) (string, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return agentTypeLLM, nil
	}
	switch v {
	case agentTypeLLM:
		return agentTypeLLM, nil
	case agentTypeClaudeCode, "claudecode":
		return agentTypeClaudeCode, nil
	default:
		return "", fmt.Errorf("unsupported agent type: %s", raw)
	}
}

func validateAgentRunOptions(agentType string, opts runOptions) error {
	if agentType == agentTypeLLM {
		return nil
	}
	if agentType != agentTypeClaudeCode {
		return fmt.Errorf("unsupported agent type: %s", agentType)
	}

	if opts.AddSessionSummary {
		return errors.New(
			"claude-code agent does not support add-session-summary",
		)
	}
	if opts.MaxHistoryRuns != 0 {
		return errors.New(
			"claude-code agent does not support max-history-runs",
		)
	}
	if opts.PreloadMemory != 0 {
		return errors.New(
			"claude-code agent does not support preload-memory",
		)
	}
	if opts.EnableLocalExec {
		return errors.New(
			"claude-code agent does not support enable-local-exec",
		)
	}
	if opts.EnableOpenClawTools {
		return errors.New(
			"claude-code agent does not support enable-openclaw-tools",
		)
	}
	if len(opts.ToolProviders) > 0 {
		return errors.New(
			"claude-code agent does not support tools.providers",
		)
	}
	if len(opts.ToolSets) > 0 {
		return errors.New(
			"claude-code agent does not support tools.toolsets",
		)
	}
	if opts.RefreshToolSetsOnRun {
		return errors.New(
			"claude-code agent does not support refresh-toolsets-on-run",
		)
	}
	return nil
}

func parseClaudeOutputFormat(
	raw string,
) (claudecode.OutputFormat, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" {
		return "", errors.New("claude output format is empty")
	}
	format := claudecode.OutputFormat(v)
	switch format {
	case claudecode.OutputFormatJSON,
		claudecode.OutputFormatStreamJSON:
		return format, nil
	default:
		return "", fmt.Errorf(
			"unsupported claude output format: %s",
			raw,
		)
	}
}

func newClaudeCodeAgent(opts runOptions) (agent.Agent, error) {
	claudeOpts := make([]claudecode.Option, 0, 6)
	claudeOpts = append(claudeOpts, claudecode.WithName(defaultAgentName))

	if v := strings.TrimSpace(opts.ClaudeBin); v != "" {
		claudeOpts = append(claudeOpts, claudecode.WithBin(v))
	}

	if v := strings.TrimSpace(opts.ClaudeOutputFormat); v != "" {
		format, err := parseClaudeOutputFormat(v)
		if err != nil {
			return nil, err
		}
		claudeOpts = append(
			claudeOpts,
			claudecode.WithOutputFormat(format),
		)
	}

	if args := splitCSV(opts.ClaudeExtraArgs); len(args) > 0 {
		claudeOpts = append(claudeOpts, claudecode.WithExtraArgs(args...))
	}
	if env := splitCSV(opts.ClaudeEnv); len(env) > 0 {
		claudeOpts = append(claudeOpts, claudecode.WithEnv(env...))
	}
	if v := strings.TrimSpace(opts.ClaudeWorkDir); v != "" {
		claudeOpts = append(claudeOpts, claudecode.WithWorkDir(v))
	}
	return claudecode.New(claudeOpts...)
}

func newAgent(
	mdl model.Model,
	cfg agentConfig,
	extraTools []tool.Tool,
	toolSets []tool.ToolSet,
) (agent.Agent, error) {
	opts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithInstruction(
			"You are a helpful assistant. Keep replies concise.",
		),
		llmagent.WithAddSessionSummary(cfg.AddSessionSummary),
		llmagent.WithMaxHistoryRuns(cfg.MaxHistoryRuns),
		llmagent.WithPreloadMemory(cfg.PreloadMemory),
	}

	cwd, _ := os.Getwd()
	roots := resolveSkillRoots(cwd, cfg)
	repo, err := ocskills.NewRepository(
		roots,
		ocskills.WithDebug(cfg.SkillsDebug),
		ocskills.WithConfigKeys(cfg.SkillConfigKeys),
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
	if len(cfg.ToolProviders) > 0 {
		extra, err := toolsFromProviders(
			mdl,
			cfg.AppName,
			cfg.StateDir,
			cfg.ToolProviders,
		)
		if err != nil {
			return nil, err
		}
		tools = append(tools, extra...)
	}
	if len(tools) > 0 {
		opts = append(opts, llmagent.WithTools(tools))
	}
	if len(toolSets) > 0 {
		opts = append(opts, llmagent.WithToolSets(toolSets))
	}
	if cfg.RefreshToolSetsOnRun {
		opts = append(opts, llmagent.WithRefreshToolSetsOnRun(true))
	}
	if cfg.EnableLocalExec {
		exec := localexec.New()
		opts = append(opts, llmagent.WithCodeExecutor(exec))
	}

	return llmagent.New(defaultAgentName, opts...), nil
}

func toolsFromProviders(
	mdl model.Model,
	appName string,
	stateDir string,
	specs []pluginSpec,
) ([]tool.Tool, error) {
	deps := registry.ToolProviderDeps{
		Model:    mdl,
		StateDir: stateDir,
		AppName:  appName,
	}

	out := make([]tool.Tool, 0, len(specs))
	for i := range specs {
		spec := specs[i]
		typeName := strings.ToLower(strings.TrimSpace(spec.Type))
		if typeName == "" {
			return nil, fmt.Errorf(
				"tools.providers[%d].type is empty",
				i,
			)
		}

		f, ok := registry.LookupToolProvider(typeName)
		if !ok {
			return nil, fmt.Errorf(
				"unsupported tool provider: %s",
				typeName,
			)
		}

		tools, err := f(deps, registry.PluginSpec{
			Type:   typeName,
			Name:   strings.TrimSpace(spec.Name),
			Config: spec.Config,
		})
		if err != nil {
			return nil, fmt.Errorf(
				"tool provider %s failed: %w",
				typeName,
				err,
			)
		}
		out = append(out, tools...)
	}
	return out, nil
}

func toolSetsFromProviders(
	mdl model.Model,
	appName string,
	stateDir string,
	specs []pluginSpec,
) ([]tool.ToolSet, error) {
	if len(specs) == 0 {
		return nil, nil
	}

	deps := registry.ToolSetProviderDeps{
		Model:    mdl,
		StateDir: stateDir,
		AppName:  appName,
	}

	out := make([]tool.ToolSet, 0, len(specs))
	for i := range specs {
		spec := specs[i]
		typeName := strings.ToLower(strings.TrimSpace(spec.Type))
		if typeName == "" {
			return nil, fmt.Errorf(
				"tools.toolsets[%d].type is empty",
				i,
			)
		}

		f, ok := registry.LookupToolSetProvider(typeName)
		if !ok {
			return nil, fmt.Errorf(
				"unsupported toolset provider: %s",
				typeName,
			)
		}

		ts, err := f(deps, registry.PluginSpec{
			Type:   typeName,
			Name:   strings.TrimSpace(spec.Name),
			Config: spec.Config,
		})
		if err != nil {
			return nil, fmt.Errorf(
				"toolset provider %s failed: %w",
				typeName,
				err,
			)
		}
		out = append(out, ts)
	}
	return out, nil
}

func channelsFromRegistry(
	gw registry.GatewayClient,
	appName string,
	stateDir string,
	specs []pluginSpec,
) ([]channel.Channel, error) {
	deps := registry.ChannelDeps{
		Gateway:  gw,
		StateDir: stateDir,
		AppName:  appName,
	}

	out := make([]channel.Channel, 0, len(specs))
	for i := range specs {
		spec := specs[i]
		typeName := strings.ToLower(strings.TrimSpace(spec.Type))
		if typeName == "" {
			return nil, fmt.Errorf(
				"channels[%d].type is empty",
				i,
			)
		}

		f, ok := registry.LookupChannel(typeName)
		if !ok {
			return nil, fmt.Errorf(
				"unsupported channel type: %s",
				typeName,
			)
		}

		ch, err := f(deps, registry.PluginSpec{
			Type:   typeName,
			Name:   strings.TrimSpace(spec.Name),
			Config: spec.Config,
		})
		if err != nil {
			return nil, fmt.Errorf(
				"channel %s failed: %w",
				typeName,
				err,
			)
		}
		out = append(out, ch)
	}
	return out, nil
}

type agentConfig struct {
	AppName string

	AddSessionSummary bool
	MaxHistoryRuns    int
	PreloadMemory     int

	SkillsRoot      string
	SkillsExtraDirs []string
	SkillsDebug     bool
	SkillConfigKeys []string

	StateDir string

	EnableLocalExec bool

	EnableOpenClawTools bool

	ToolProviders []pluginSpec

	ToolSets []pluginSpec

	RefreshToolSetsOnRun bool
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
	sum := crc32.ChecksumIEEE([]byte(joined))
	return fmt.Sprintf("%08x", sum)
}

func newMockModel(_ registry.ModelSpec) (model.Model, error) {
	return &echoModel{name: "mock-echo"}, nil
}

func newOpenAIModel(spec registry.ModelSpec) (model.Model, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, errors.New("openai model name is empty")
	}

	variant, err := parseOpenAIVariant(spec.OpenAIVariant, name)
	if err != nil {
		return nil, err
	}

	opts := []openai.Option{openai.WithVariant(variant)}
	baseURL := strings.TrimSpace(spec.BaseURL)
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	return openai.New(name, opts...), nil
}

func modelFromOptions(opts runOptions) (model.Model, error) {
	mode := strings.ToLower(strings.TrimSpace(opts.ModelMode))
	if mode == "" {
		mode = modeOpenAI
	}

	f, ok := registry.LookupModel(mode)
	if !ok {
		return nil, fmt.Errorf("unsupported mode: %s", mode)
	}

	baseURL := strings.TrimSpace(opts.OpenAIBaseURL)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv(openAIBaseURLEnvName))
	}

	spec := registry.ModelSpec{
		Type:          mode,
		Name:          opts.OpenAIModel,
		BaseURL:       baseURL,
		OpenAIVariant: opts.OpenAIVariant,
		Config:        opts.ModelConfig,
	}
	return f(spec)
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
