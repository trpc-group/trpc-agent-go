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
	"flag"
	"fmt"
	"hash/crc32"
	"net"
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
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/admin"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
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

	defaultDebugRecorderDir = "debug"

	defaultAgentName        = "assistant"
	defaultAgentInstruction = "You are a helpful assistant. " +
		"Keep replies concise."

	openClawToolingGuidance = "For general local shell work, use " +
		"exec_command. For interactive follow-up input, use " +
		"write_stdin and kill_session when needed. Use message " +
		"to send to the current chat or an explicit target. " +
		"Chat uploads are saved to stable host paths. For host " +
		"commands, prefer OPENCLAW_LAST_UPLOAD_PATH or " +
		"OPENCLAW_SESSION_UPLOADS_DIR, OPENCLAW_LAST_UPLOAD_HOST_REF, " +
		"OPENCLAW_LAST_UPLOAD_NAME, " +
		"OPENCLAW_LAST_UPLOAD_MIME, and " +
		"OPENCLAW_RECENT_UPLOADS_JSON instead of guessing " +
		"attachment paths. When a user follows " +
		"up about 'the PDF/audio/video I just sent', assume they " +
		"mean the recent upload already present in this chat unless " +
		"the reference is genuinely ambiguous. Match by media kind " +
		"first: prefer OPENCLAW_LAST_PDF_PATH, " +
		"OPENCLAW_LAST_AUDIO_PATH, OPENCLAW_LAST_VIDEO_PATH, or " +
		"OPENCLAW_LAST_IMAGE_PATH when the request clearly targets " +
		"one of those kinds. Telegram voice notes count as audio, " +
		"video notes count as video, and documents with image/audio/" +
		"video MIME types still count as that media kind. If the " +
		"user replies " +
		"to an earlier media message, treat that replied media as " +
		"the default target unless they clearly ask for something " +
		"else. Do not ask the user to re-upload a file or provide " +
		"a local path when the recent upload context already lists " +
		"a matching upload for this chat. If the user asks you to " +
		"'send it back', '发给我', " +
		"'回传', or similar, send the derived files directly in " +
		"the current chat with message instead of asking which " +
		"channel or delivery method to use. For exec_command, do " +
		"not assume skill workspace paths like work/inputs. Do not " +
		"expose local host paths to the user; when acknowledging a " +
		"new upload, refer to it only by filename and media kind, " +
		"not by OPENCLAW_* vars or a machine path. If Telegram gives " +
		"you an opaque placeholder filename like file_11.oga, avoid " +
		"surfacing that raw placeholder to the user unless they " +
		"explicitly ask for the exact filename. Refer to uploads " +
		"and generated files by user-facing filenames, and use " +
		"OPENCLAW_LAST_*_NAME instead of basename(" +
		"OPENCLAW_LAST_*_PATH) when deriving output filenames, " +
		"because stored host paths may include internal dedupe " +
		"prefixes. Use message " +
		"with host refs when possible, or with local file " +
		"paths/artifact refs when needed, to send " +
		"PDFs, images, audio, or video back to the current chat " +
		"when needed instead of asking for chat_id or another " +
		"upload. Merely mentioning a filename in text does not " +
		"send it; call message with files when the user should " +
		"actually receive media or documents. If a command " +
		"returns media_files or media_dirs, call message with " +
		"those paths unless your final reply already includes " +
		"`MEDIA:` or `MEDIA_DIR:` lines for OpenClaw to " +
		"auto-attach and hide from the user. When exec_command " +
		"or write_stdin generates images that you need to inspect, " +
		"prefer printing `MEDIA:` / `MEDIA_DIR:` lines or the " +
		"absolute image paths on their own lines. OpenClaw can " +
		"reattach those generated images to the model for direct " +
		"visual inspection, so inspect the image before assuming " +
		"OCR failed. If you intentionally " +
		"use that directive path, keep the visible prose separate " +
		"from the `MEDIA:` lines. If a compatible audio reply " +
		"should arrive as a Telegram voice bubble instead of a " +
		"generic audio file, call message with as_voice=true or " +
		"include `[[audio_as_voice]]` in the final reply along " +
		"with the `MEDIA:` lines. If a command " +
		"produces multiple files in one " +
		"directory, send that directory or the matching files " +
		"directly with message instead of only describing their " +
		"paths. When you mention generated files in the final " +
		"reply, use concise filenames rather than local machine " +
		"paths, and ensure those filenames actually exist under " +
		"the current working directory or " +
		"OPENCLAW_SESSION_UPLOADS_DIR. Prefer writing derived " +
		"files under " +
		"OPENCLAW_SESSION_UPLOADS_DIR when you will send them " +
		"back to the user. Prefer already installed local tools " +
		"for OCR, PDF, audio, image, and video work before " +
		"trying package installs or long downloads. " +
		"When creating a cron job from chat, omit channel and " +
		"target to send results back to the current chat by " +
		"default. When adding cron jobs, write the stored task " +
		"as a one-time execution instruction, not as another " +
		"scheduling request. Prefer concise, outcome-oriented " +
		"tasks over brittle shell transcripts unless exact " +
		"commands are truly required. Use cron for future or " +
		"recurring work. " +
		"Use skill_run only for skill workspace workflows."

	agentTypeLLM        = "llm"
	agentTypeClaudeCode = "claude-code"

	openAIVariantAuto = "auto"

	defaultOpenAIVariant = openAIVariantAuto

	deepSeekModelHint = "deepseek"
	qwenModelHint     = "qwen"
	hunyuanModelHint  = "hunyuan"

	openAIBaseURLEnvName = "OPENAI_BASE_URL"
	openAIModelEnvName   = "OPENAI_MODEL"

	errClaudeCodeAgentNoPrompts = "claude-code agent does not support " +
		"agent prompts"
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
			if errors.Is(exitErr.Err, flag.ErrHelp) {
				return 0
			}
			if shouldLogExitError(exitErr.Err) {
				log.Errorf("%v", exitErr.Err)
			}
			return exitErr.ExitCode()
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

type startupLogLine struct {
	warn bool
	text string
}

func applyOpenClawToolDefaults(
	agentType string,
	opts *runOptions,
) {
	if opts == nil || opts.enableOpenClawToolsExplicit {
		return
	}
	if agentType == agentTypeLLM {
		opts.EnableOpenClawTools = true
	}
}

func (e *exitError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

// ExitCode returns the suggested process exit code for this error.
func (e *exitError) ExitCode() int {
	if e == nil {
		return 1
	}
	if e.Code == 0 {
		return 1
	}
	return e.Code
}

func shouldLogExitError(err error) bool {
	return err != nil && !errors.Is(err, flag.ErrHelp)
}

func logStartupLines(lines []startupLogLine) {
	for _, line := range lines {
		if line.warn {
			log.Warn(line.text)
			continue
		}
		log.Info(line.text)
	}
}

func gatewayStartupLines(
	httpAddr string,
	gwSrv *gateway.Server,
) []startupLogLine {
	return []startupLogLine{
		{text: fmt.Sprintf("Gateway listening on %s", httpAddr)},
		{text: fmt.Sprintf("Health:   GET  %s", gwSrv.HealthPath())},
		{text: fmt.Sprintf("Messages: POST %s", gwSrv.MessagesPath())},
		{text: fmt.Sprintf(
			"Status:   GET  %s?request_id=...",
			gwSrv.StatusPath(),
		)},
		{text: fmt.Sprintf("Cancel:   POST %s", gwSrv.CancelPath())},
	}
}

func adminStartupLines(
	preferredAddr string,
	binding *adminBinding,
) []startupLogLine {
	if binding == nil {
		return nil
	}

	lines := make([]startupLogLine, 0, 3)
	if binding.relocated {
		lines = append(lines, startupLogLine{
			warn: true,
			text: fmt.Sprintf(
				"Admin UI preferred address %s was busy; using %s "+
					"instead",
				preferredAddr,
				binding.addr,
			),
		})
	}

	lines = append(lines,
		startupLogLine{
			text: fmt.Sprintf(
				"Admin UI listening on %s",
				binding.addr,
			),
		},
		startupLogLine{
			text: fmt.Sprintf("Admin UI: %s", binding.url),
		},
	)
	return lines
}

// Runtime wires OpenClaw components without owning the HTTP listener.
//
// Downstream distributions can mount Gateway.Handler into any HTTP server
// implementation (including framework-managed servers) while reusing the
// default OpenClaw runner + channel wiring.
type Runtime struct {
	Gateway  Gateway
	Admin    AdminSurface
	Channels []channel.Channel

	runner     runner.Runner
	cronRunner closeFunc
	sessionSvc closeFunc
	memorySvc  closeFunc
	cronSvc    closeFunc
	toolSets   []tool.ToolSet
}

// Gateway provides the HTTP handler and routes served by OpenClaw.
type Gateway struct {
	Handler      http.Handler
	HealthPath   string
	MessagesPath string
	StatusPath   string
	CancelPath   string
}

type AdminSurface struct {
	Handler http.Handler
	Addr    string
	URL     string
}

// NewRuntime constructs an OpenClaw runtime based on CLI args / config file,
// but does not start an HTTP server.
func NewRuntime(
	ctx context.Context,
	args []string,
) (rt *Runtime, err error) {
	rt = &Runtime{}
	startedAt := time.Now()
	cleanup := rt
	defer func() {
		if err != nil {
			_ = cleanup.Close()
		}
	}()

	opts, err := parseRunOptions(args)
	if err != nil {
		return nil, err
	}

	agentType, err := normalizeAgentType(opts.AgentType)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent config failed: %w", err),
		}
	}
	applyOpenClawToolDefaults(agentType, &opts)
	if err := validateAgentRunOptions(agentType, opts); err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent config failed: %w", err),
		}
	}

	mentionPatterns := splitCSV(opts.Mention)

	resolvedStateDir, err := resolveStateDir(opts.StateDir)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("resolve state dir failed: %w", err),
		}
	}
	opts.StateDir = resolvedStateDir

	ctx, debugRec, err := maybeEnableDebugRecorder(ctx, opts)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("debug recorder config failed: %w", err),
		}
	}

	needsModel := agentType == agentTypeLLM ||
		opts.SessionSummaryEnabled ||
		opts.MemoryAutoEnabled

	var mdl model.Model
	if needsModel {
		mdl, err = modelFromOptions(opts)
		if err != nil {
			return nil, &exitError{
				Code: 1,
				Err:  fmt.Errorf("create model failed: %w", err),
			}
		}
	}

	instanceID := runtimeInstanceID(
		agentType,
		opts,
		needsModel,
		resolvedStateDir,
	)
	log.Infof("Instance: %s", instanceID)

	sessionSvc, err := newSessionService(mdl, opts)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create session service failed: %w", err),
		}
	}
	rt.sessionSvc = sessionSvc

	memSvc, err := newMemoryService(mdl, opts)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create memory service failed: %w", err),
		}
	}
	rt.memorySvc = memSvc

	prompts, err := resolveAgentPrompts(opts)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent prompt config failed: %w", err),
		}
	}

	openClawTools := buildOpenClawTools(
		opts.EnableOpenClawTools,
		resolvedStateDir,
	)
	extraTools := append([]tool.Tool(nil), memSvc.Tools()...)
	extraTools = append(extraTools, openClawTools.tools...)

	var (
		toolSets []tool.ToolSet
		ag       agent.Agent
	)
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
			return nil, &exitError{
				Code: 1,
				Err:  fmt.Errorf("create toolsets failed: %w", err),
			}
		}
		ag, err = newAgent(mdl, agentConfig{
			AppName:           opts.AppName,
			AddSessionSummary: opts.AddSessionSummary,
			MaxHistoryRuns:    opts.MaxHistoryRuns,
			PreloadMemory:     opts.PreloadMemory,
			Instruction:       prompts.Instruction,
			SystemPrompt:      prompts.SystemPrompt,

			SkillsRoot:      opts.SkillsRoot,
			SkillsExtraDirs: splitCSV(opts.SkillsExtraDir),
			SkillsDebug:     opts.SkillsDebug,
			SkillsAllowBundled: splitCSV(
				opts.SkillsAllowBundled,
			),
			SkillConfigs:       opts.SkillConfigs,
			SkillConfigKeys:    resolveSkillConfigKeys(opts),
			SkillsLoadMode:     opts.SkillsLoadMode,
			SkillsMaxLoaded:    opts.SkillsMaxLoaded,
			SkillsToolResults:  opts.SkillsToolResults,
			SkillsSkipFallback: opts.SkillsSkipFallback,
			SkillsToolingGuide: opts.SkillsToolingGuide,
			StateDir:           resolvedStateDir,

			EnableLocalExec:     opts.EnableLocalExec,
			EnableOpenClawTools: opts.EnableOpenClawTools,
			EnableParallelTools: opts.EnableParallelTools,

			ToolProviders: opts.ToolProviders,
			ToolSets:      opts.ToolSets,

			RefreshToolSetsOnRun: opts.RefreshToolSetsOnRun,
		}, extraTools, toolSets)
	}
	if err != nil {
		closeToolSets(toolSets)
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create agent failed: %w", err),
		}
	}
	rt.toolSets = toolSets

	runnerOpts := []runner.Option{
		runner.WithSessionService(sessionSvc),
		runner.WithMemoryService(memSvc),
	}
	rlCfg, err := ralphLoopConfigFromRunOptions(opts)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent ralph loop config failed: %w", err),
		}
	}
	if rlCfg != nil {
		runnerOpts = append(
			runnerOpts,
			runner.WithRalphLoop(*rlCfg),
		)
	}

	r := runner.NewRunner(opts.AppName, ag, runnerOpts...)
	rt.runner = r

	uploadStore, err := uploads.NewStore(resolvedStateDir)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create upload store failed: %w", err),
		}
	}
	personaPath, err := persona.DefaultStorePath(resolvedStateDir)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create persona store path failed: %w", err),
		}
	}
	personaStore, err := persona.NewStore(personaPath)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create persona store failed: %w", err),
		}
	}

	gwOpts := makeGatewayOptions(
		splitCSV(opts.AllowUsers),
		opts.RequireMention,
		mentionPatterns,
	)
	gwOpts = append(gwOpts, gateway.WithUploadStore(uploadStore))
	gwOpts = append(gwOpts, gateway.WithPersonaStore(personaStore))
	if debugRec != nil {
		gwOpts = append(gwOpts, gateway.WithDebugRecorder(debugRec))
	}
	gwSrv, err := gateway.New(r, gwOpts...)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create gateway failed: %w", err),
		}
	}
	rt.Gateway = Gateway{
		Handler:      gwSrv.Handler(),
		HealthPath:   gwSrv.HealthPath(),
		MessagesPath: gwSrv.MessagesPath(),
		StatusPath:   gwSrv.StatusPath(),
		CancelPath:   gwSrv.CancelPath(),
	}

	debugDir := filepath.Join(resolvedStateDir, defaultDebugRecorderDir)
	if debugRec != nil {
		debugDir = debugRec.Dir()
	}
	gw := newInProcGatewayClient(
		gwSrv,
		opts.AppName,
		sessionSvc,
		memSvc,
		debugDir,
		uploadStore,
	)
	gw.SetPersonaStore(personaStore)

	if len(opts.Channels) > 0 {
		extra, err := channelsFromRegistry(
			ctx,
			gw,
			opts.AppName,
			resolvedStateDir,
			splitCSV(opts.AllowUsers),
			opts.Channels,
		)
		if err != nil {
			return nil, &exitError{
				Code: 1,
				Err:  fmt.Errorf("create channels failed: %w", err),
			}
		}
		rt.Channels = append(rt.Channels, extra...)
	}

	var (
		cronSvc    *cron.Service
		cronRunner runner.Runner
	)
	if openClawTools.router != nil {
		for _, ch := range rt.Channels {
			openClawTools.router.Register(ch)
		}
		cronRunner = newCronRunner(
			opts.AppName,
			ag,
			memSvc,
			rlCfg,
		)
		cronSvc, err = cron.NewService(
			resolvedStateDir,
			cronRunner,
			openClawTools.router,
		)
		if err != nil {
			if cronRunner != nil {
				_ = cronRunner.Close()
			}
			return nil, &exitError{
				Code: 1,
				Err:  fmt.Errorf("create cron service failed: %w", err),
			}
		}
		openClawTools.cronTool.SetService(cronSvc)
		gw.SetCronService(cronSvc)
		cronSvc.Start(ctx)
		rt.cronSvc = cronSvc
		rt.cronRunner = cronRunner
	}

	if opts.AdminEnabled {
		adminURL := listenURL(opts.AdminAddr)
		adminSvc := admin.New(buildAdminConfig(
			opts,
			agentType,
			instanceID,
			resolvedStateDir,
			debugDir,
			startedAt,
			rt.Channels,
			admin.Routes{
				HealthPath:   gwSrv.HealthPath(),
				MessagesPath: gwSrv.MessagesPath(),
				StatusPath:   gwSrv.StatusPath(),
				CancelPath:   gwSrv.CancelPath(),
			},
			cronSvc,
			openClawTools.execMgr,
			opts.AdminAddr,
			adminURL,
		))
		rt.Admin = AdminSurface{
			Handler: adminSvc.Handler(),
			Addr:    opts.AdminAddr,
			URL:     adminURL,
		}
	}

	return rt, nil
}

// Close releases owned resources (session/memory services, toolsets, runner).
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}

	if r.cronSvc != nil {
		_ = r.cronSvc.Close()
	}
	if r.cronRunner != nil {
		_ = r.cronRunner.Close()
	}
	closeToolSets(r.toolSets)
	closeMemoryService(r.memorySvc)
	closeSessionService(r.sessionSvc)

	if r.runner == nil {
		return nil
	}
	return r.runner.Close()
}

func run(ctx context.Context, args []string) error {
	startedAt := time.Now()
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
	applyOpenClawToolDefaults(agentType, &opts)
	if err := validateAgentRunOptions(agentType, opts); err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent config failed: %w", err),
		}
	}

	mentionPatterns := splitCSV(opts.Mention)

	resolvedStateDir, err := resolveStateDir(opts.StateDir)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("resolve state dir failed: %w", err),
		}
	}
	opts.StateDir = resolvedStateDir

	ctx, debugRec, err := maybeEnableDebugRecorder(ctx, opts)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("debug recorder config failed: %w", err),
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

	instanceID := runtimeInstanceID(
		agentType,
		opts,
		needsModel,
		resolvedStateDir,
	)
	log.Infof("Instance: %s", instanceID)

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

	prompts, err := resolveAgentPrompts(opts)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent prompt config failed: %w", err),
		}
	}

	openClawTools := buildOpenClawTools(
		opts.EnableOpenClawTools,
		resolvedStateDir,
	)
	extraTools := append([]tool.Tool(nil), memSvc.Tools()...)
	extraTools = append(extraTools, openClawTools.tools...)

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
			Instruction:       prompts.Instruction,
			SystemPrompt:      prompts.SystemPrompt,

			SkillsRoot:      opts.SkillsRoot,
			SkillsExtraDirs: splitCSV(opts.SkillsExtraDir),
			SkillsDebug:     opts.SkillsDebug,
			SkillsAllowBundled: splitCSV(
				opts.SkillsAllowBundled,
			),
			SkillConfigs:       opts.SkillConfigs,
			SkillConfigKeys:    resolveSkillConfigKeys(opts),
			SkillsLoadMode:     opts.SkillsLoadMode,
			SkillsMaxLoaded:    opts.SkillsMaxLoaded,
			SkillsToolResults:  opts.SkillsToolResults,
			SkillsSkipFallback: opts.SkillsSkipFallback,
			SkillsToolingGuide: opts.SkillsToolingGuide,
			StateDir:           resolvedStateDir,

			EnableLocalExec:     opts.EnableLocalExec,
			EnableOpenClawTools: opts.EnableOpenClawTools,
			EnableParallelTools: opts.EnableParallelTools,

			ToolProviders: opts.ToolProviders,
			ToolSets:      opts.ToolSets,

			RefreshToolSetsOnRun: opts.RefreshToolSetsOnRun,
		}, extraTools, toolSets)
	}
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create agent failed: %w", err),
		}
	}

	runnerOpts := []runner.Option{
		runner.WithSessionService(sessionSvc),
		runner.WithMemoryService(memSvc),
	}
	rlCfg, err := ralphLoopConfigFromRunOptions(opts)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent ralph loop config failed: %w", err),
		}
	}
	if rlCfg != nil {
		runnerOpts = append(
			runnerOpts,
			runner.WithRalphLoop(*rlCfg),
		)
	}
	r := runner.NewRunner(opts.AppName, ag, runnerOpts...)

	uploadStore, err := uploads.NewStore(resolvedStateDir)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create upload store failed: %w", err),
		}
	}
	personaPath, err := persona.DefaultStorePath(resolvedStateDir)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create persona store path failed: %w", err),
		}
	}
	personaStore, err := persona.NewStore(personaPath)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create persona store failed: %w", err),
		}
	}

	gwOpts := makeGatewayOptions(
		splitCSV(opts.AllowUsers),
		opts.RequireMention,
		mentionPatterns,
	)
	gwOpts = append(gwOpts, gateway.WithUploadStore(uploadStore))
	gwOpts = append(gwOpts, gateway.WithPersonaStore(personaStore))
	if debugRec != nil {
		gwOpts = append(gwOpts, gateway.WithDebugRecorder(debugRec))
	}
	gwSrv, err := gateway.New(r, gwOpts...)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create gateway failed: %w", err),
		}
	}

	debugDir := filepath.Join(resolvedStateDir, defaultDebugRecorderDir)
	if debugRec != nil {
		debugDir = debugRec.Dir()
	}
	gw := newInProcGatewayClient(
		gwSrv,
		opts.AppName,
		sessionSvc,
		memSvc,
		debugDir,
		uploadStore,
	)
	gw.SetPersonaStore(personaStore)

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	httpSrv := &http.Server{
		Addr:              opts.HTTPAddr,
		Handler:           gwSrv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	var (
		adminSrv     *http.Server
		adminBinding *adminBinding
	)

	var channels []channel.Channel
	if len(opts.Channels) > 0 {
		extra, err := channelsFromRegistry(
			ctx,
			gw,
			opts.AppName,
			resolvedStateDir,
			splitCSV(opts.AllowUsers),
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

	var (
		cronSvc    *cron.Service
		cronRunner runner.Runner
	)
	if openClawTools.router != nil {
		for _, ch := range channels {
			openClawTools.router.Register(ch)
		}
		cronRunner = newCronRunner(
			opts.AppName,
			ag,
			memSvc,
			rlCfg,
		)
		cronSvc, err = cron.NewService(
			resolvedStateDir,
			cronRunner,
			openClawTools.router,
		)
		if err != nil {
			if cronRunner != nil {
				_ = cronRunner.Close()
			}
			return &exitError{
				Code: 1,
				Err:  fmt.Errorf("create cron service failed: %w", err),
			}
		}
		openClawTools.cronTool.SetService(cronSvc)
		gw.SetCronService(cronSvc)
		cronSvc.Start(runCtx)
	}

	if opts.AdminEnabled {
		adminBinding, err = openAdminBinding(
			opts.AdminAddr,
			opts.AdminAutoPort,
		)
		if err != nil {
			return &exitError{
				Code: 1,
				Err:  err,
			}
		}
		adminSvc := admin.New(buildAdminConfig(
			opts,
			agentType,
			instanceID,
			resolvedStateDir,
			debugDir,
			startedAt,
			channels,
			admin.Routes{
				HealthPath:   gwSrv.HealthPath(),
				MessagesPath: gwSrv.MessagesPath(),
				StatusPath:   gwSrv.StatusPath(),
				CancelPath:   gwSrv.CancelPath(),
			},
			cronSvc,
			openClawTools.execMgr,
			adminBinding.addr,
			adminBinding.url,
		))
		adminSrv = &http.Server{
			Handler:           adminSvc.Handler(),
			ReadHeaderTimeout: 5 * time.Second,
		}
	}

	workerCount := 1 + len(channels)
	if adminSrv != nil {
		workerCount++
	}
	errCh := make(chan error, workerCount)

	logStartupLines(gatewayStartupLines(httpSrv.Addr, gwSrv))
	go func() {
		//nolint:gosec
		errCh <- httpSrv.ListenAndServe()
	}()

	if adminSrv != nil {
		logStartupLines(adminStartupLines(opts.AdminAddr, adminBinding))
		go func() {
			//nolint:gosec
			errCh <- adminSrv.Serve(adminBinding.listener)
		}()
	}

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
	if adminSrv != nil {
		_ = adminSrv.Shutdown(shutdownCtx)
	}
	if cronSvc != nil {
		_ = cronSvc.Close()
	}
	if cronRunner != nil {
		_ = cronRunner.Close()
	}
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

func newCronRunner(
	appName string,
	ag agent.Agent,
	memSvc memory.Service,
	rlCfg *runner.RalphLoopConfig,
) runner.Runner {
	opts := []runner.Option{
		runner.WithSessionService(
			sessioninmemory.NewSessionService(),
		),
		runner.WithMemoryService(memSvc),
	}
	if rlCfg != nil {
		opts = append(opts, runner.WithRalphLoop(*rlCfg))
	}
	return runner.NewRunner(appName, ag, opts...)
}

func runtimeInstanceID(
	agentType string,
	opts runOptions,
	needsModel bool,
	stateDir string,
) string {
	if agentType == agentTypeLLM {
		return configFingerprint(
			opts.ModelMode,
			opts.OpenAIModel,
			stateDir,
		)
	}

	parts := []string{
		agentType,
		strings.TrimSpace(opts.ClaudeBin),
		strings.TrimSpace(opts.ClaudeOutputFormat),
		stateDir,
	}
	if needsModel {
		parts = append(parts, opts.ModelMode, opts.OpenAIModel)
	}
	return configFingerprint(parts...)
}

func channelIDs(channels []channel.Channel) []string {
	if len(channels) == 0 {
		return nil
	}
	out := make([]string, 0, len(channels))
	for _, ch := range channels {
		if ch == nil {
			continue
		}
		if id := strings.TrimSpace(ch.ID()); id != "" {
			out = append(out, id)
		}
	}
	return out
}

func listenURL(addr string) string {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		trimmed := strings.TrimSpace(addr)
		if trimmed == "" {
			return ""
		}
		return "http://" + trimmed
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return "http://" + net.JoinHostPort(host, port)
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
	if opts.RalphLoopEnabled && agentType != agentTypeLLM {
		return errors.New(
			"claude-code agent does not support ralph loop",
		)
	}
	if opts.RalphLoopEnabled {
		if _, err := ralphLoopConfigFromRunOptions(opts); err != nil {
			return err
		}
	}

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
	if strings.TrimSpace(opts.AgentInstruction) != "" ||
		strings.TrimSpace(opts.AgentInstructionFiles) != "" ||
		strings.TrimSpace(opts.AgentInstructionDir) != "" {
		return errors.New(errClaudeCodeAgentNoPrompts)
	}
	if strings.TrimSpace(opts.AgentSystemPrompt) != "" ||
		strings.TrimSpace(opts.AgentSystemPromptFiles) != "" ||
		strings.TrimSpace(opts.AgentSystemPromptDir) != "" {
		return errors.New(errClaudeCodeAgentNoPrompts)
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
	if opts.EnableParallelTools {
		return errors.New(
			"claude-code agent does not support enable-parallel-tools",
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

func ralphLoopConfigFromRunOptions(
	opts runOptions,
) (*runner.RalphLoopConfig, error) {
	if !opts.RalphLoopEnabled {
		return nil, nil
	}

	promise := strings.TrimSpace(opts.RalphLoopCompletionPromise)
	verifyCmd := strings.TrimSpace(opts.RalphLoopVerifyCommand)
	if promise == "" && verifyCmd == "" {
		return nil, errors.New(
			"agent.ralph_loop requires completion_promise or verify.command",
		)
	}

	if opts.RalphLoopMaxIterations < 0 {
		return nil, errors.New(
			"agent.ralph_loop.max_iterations must be >= 0",
		)
	}
	if opts.RalphLoopVerifyTimeout < 0 {
		return nil, errors.New(
			"agent.ralph_loop.verify.timeout must be >= 0",
		)
	}

	env, err := parseKVOverrides(splitCSV(opts.RalphLoopVerifyEnv))
	if err != nil {
		return nil, fmt.Errorf(
			"agent.ralph_loop.verify.env: %w",
			err,
		)
	}

	cfg := &runner.RalphLoopConfig{
		MaxIterations:     opts.RalphLoopMaxIterations,
		CompletionPromise: promise,
		PromiseTagOpen: strings.TrimSpace(
			opts.RalphLoopPromiseTagOpen,
		),
		PromiseTagClose: strings.TrimSpace(
			opts.RalphLoopPromiseTagClose,
		),
		VerifyCommand: verifyCmd,
		VerifyWorkDir: strings.TrimSpace(opts.RalphLoopVerifyWorkDir),
		VerifyTimeout: opts.RalphLoopVerifyTimeout,
		VerifyEnv:     env,
	}
	return cfg, nil
}

func parseKVOverrides(items []string) (map[string]string, error) {
	if len(items) == 0 {
		return nil, nil
	}

	out := make(map[string]string, len(items))
	for _, item := range items {
		key, val, ok := strings.Cut(item, "=")
		if !ok {
			return nil, fmt.Errorf("invalid override: %q", item)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("empty key in override: %q", item)
		}
		out[key] = val
	}

	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
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
	instruction := strings.TrimSpace(cfg.Instruction)
	if instruction == "" {
		instruction = defaultAgentInstruction
	}
	if cfg.EnableOpenClawTools {
		instruction = strings.TrimSpace(
			instruction + "\n\n" + openClawToolingGuidance,
		)
	}

	opts := []llmagent.Option{
		llmagent.WithModel(mdl),
		llmagent.WithInstruction(instruction),
		llmagent.WithGlobalInstruction(strings.TrimSpace(cfg.SystemPrompt)),
		llmagent.WithAddSessionSummary(cfg.AddSessionSummary),
		llmagent.WithMaxHistoryRuns(cfg.MaxHistoryRuns),
		llmagent.WithPreloadMemory(cfg.PreloadMemory),
		llmagent.WithEnableParallelTools(cfg.EnableParallelTools),
	}

	cwd, _ := os.Getwd()
	roots := resolveSkillRoots(cwd, cfg)
	bundledRoot := filepath.Join(cwd, appName, defaultSkillsDir)
	repo, err := ocskills.NewRepository(
		roots,
		ocskills.WithDebug(cfg.SkillsDebug),
		ocskills.WithConfigKeys(cfg.SkillConfigKeys),
		ocskills.WithBundledSkillsRoot(bundledRoot),
		ocskills.WithAllowBundled(cfg.SkillsAllowBundled),
		ocskills.WithSkillConfigs(cfg.SkillConfigs),
	)
	if err != nil {
		return nil, err
	}

	opts = append(opts, llmagent.WithSkills(repo))
	opts = append(
		opts,
		llmagent.WithSkillLoadMode(cfg.SkillsLoadMode),
		llmagent.WithSkillsLoadedContentInToolResults(
			cfg.SkillsToolResults,
		),
		llmagent.WithSkipSkillsFallbackOnSessionSummary(
			cfg.SkillsSkipFallback,
		),
	)
	if cfg.SkillsMaxLoaded > 0 {
		opts = append(
			opts,
			llmagent.WithMaxLoadedSkills(cfg.SkillsMaxLoaded),
		)
	}
	if cfg.SkillsToolingGuide != nil {
		opts = append(
			opts,
			llmagent.WithSkillsToolingGuidance(
				*cfg.SkillsToolingGuide,
			),
		)
	}

	tools := append([]tool.Tool(nil), extraTools...)
	tools = append(tools, ocskills.NewListTool(repo))
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

	callbacks := tool.NewCallbacks()
	callbacks.RegisterToolResultMessages(openClawToolResultMessages)
	opts = append(opts, llmagent.WithToolCallbacks(callbacks))

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
	ctx context.Context,
	gw registry.GatewayClient,
	appName string,
	stateDir string,
	allowUsers []string,
	specs []pluginSpec,
) ([]channel.Channel, error) {
	deps := registry.ChannelDeps{
		Ctx:        ctx,
		Gateway:    gw,
		StateDir:   stateDir,
		AppName:    appName,
		AllowUsers: allowUsers,
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
	Instruction       string
	SystemPrompt      string

	SkillsRoot         string
	SkillsExtraDirs    []string
	SkillsDebug        bool
	SkillsAllowBundled []string
	SkillConfigs       map[string]ocskills.SkillConfig
	SkillConfigKeys    []string
	SkillsLoadMode     string
	SkillsMaxLoaded    int
	SkillsToolResults  bool
	SkillsSkipFallback bool
	SkillsToolingGuide *string

	StateDir string

	EnableLocalExec bool

	EnableOpenClawTools bool
	EnableParallelTools bool

	ToolProviders []pluginSpec

	ToolSets []pluginSpec

	RefreshToolSetsOnRun bool
}

type openClawToolsBundle struct {
	tools    []tool.Tool
	execMgr  *octool.Manager
	router   *outbound.Router
	cronTool *cron.Tool
}

func buildOpenClawTools(
	enabled bool,
	stateDir string,
) openClawToolsBundle {
	if !enabled {
		return openClawToolsBundle{}
	}

	mgr := octool.NewManager()
	router := outbound.NewRouter()
	cronTool := cron.NewTool(nil)
	var uploadStore *uploads.Store
	if store, err := uploads.NewStore(stateDir); err == nil {
		uploadStore = store
	}

	tools := []tool.Tool{
		octool.NewExecCommandTool(mgr, uploadStore),
		octool.NewWriteStdinTool(mgr),
		octool.NewKillSessionTool(mgr),
		outbound.NewTool(router),
		cronTool,
	}
	return openClawToolsBundle{
		tools:    tools,
		execMgr:  mgr,
		router:   router,
		cronTool: cronTool,
	}
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

func maybeEnableDebugRecorder(
	ctx context.Context,
	opts runOptions,
) (context.Context, *debugrecorder.Recorder, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if !opts.DebugRecorderEnabled {
		return ctx, nil, nil
	}

	dir := strings.TrimSpace(opts.DebugRecorderDir)
	if dir == "" {
		dir = filepath.Join(opts.StateDir, defaultDebugRecorderDir)
	}
	mode, err := debugrecorder.ParseMode(opts.DebugRecorderMode)
	if err != nil {
		return ctx, nil, err
	}
	rec, err := debugrecorder.New(dir, mode)
	if err != nil {
		return ctx, nil, err
	}

	log.Infof(
		"Debug recorder enabled: dir = %s mode = %s",
		rec.Dir(),
		rec.Mode(),
	)

	return debugrecorder.WithRecorder(ctx, rec), rec, nil
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

	opts := []openai.Option{
		openai.WithVariant(variant),
		openai.WithOmitFileContentParts(true),
	}
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
