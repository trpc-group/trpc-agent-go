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
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"hash/crc32"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/claudecode"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
	sandboxexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/evolution"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/memory"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/conversation"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	sessioninmemory "trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/admin"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/channel"
	ocbrowser "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/browser"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationtool"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/cron"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/debugrecorder"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/deps"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/gateway"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/octool"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/outbound"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/persona"
	ocskills "trpc.group/trpc-go/trpc-agent-go/openclaw/internal/skills"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/subagentrun"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/registry"
	openclawsubagent "trpc.group/trpc-go/trpc-agent-go/openclaw/subagent"
)

const (
	appName = "openclaw"

	defaultHTTPAddr = ":8080"

	modeMock   = "mock"
	modeOpenAI = "openai"

	defaultOpenAIModel = "gpt-5"

	defaultSkillsDir        = "skills"
	defaultAgentsDir        = ".agents"
	defaultBundledSkillsDir = "bundled-skills"

	csvDelimiter = ","

	defaultDebugRecorderDir = "debug"

	defaultAgentName = "assistant"

	preambleOnlyFinalAnswerRule = "A preamble-only response such as " +
		"`I will ...`, `I'll ...`, `我先...`, or " +
		"`接下来...` is not a valid final answer. If " +
		"tool work is needed, the same assistant message " +
		"must include the tool call; if no tool is needed, " +
		"return the requested content or completed result " +
		"instead of an announcement. "
	substantiveSameTurnRule = "For writing, summarization, " +
		"recommendation, explanation, or analysis tasks, " +
		"produce the requested content in the same turn. "
	artifactCompletionRule = "For requests to create, write, send, " +
		"publish, upload, schedule, or update an artifact " +
		"or external resource, the turn is not complete " +
		"until you have performed the action and returned " +
		"the resulting link, id, file marker, or exact " +
		"blocker after recovery."
	openClawPostToolPrompt = "[OpenClaw Tool Result Prompt] " +
		"Treat tool results as mid-task state, not as " +
		"permission to stop. Compare the latest tool result " +
		"with the user's original/current request and keep " +
		"working autonomously until the requested content, " +
		"artifact, or external action is complete or blocked. " +
		"If the request is complete, return the concrete " +
		"user-facing result: link, id, file marker, sent " +
		"status, created document title, scheduled job id, " +
		"or the exact blocker. If work is still needed and " +
		"an available tool can advance, verify, or recover " +
		"the task, call that tool in the same assistant turn. " +
		"Do not answer only with what you will do next. Do " +
		"not stop at a plan, progress note, or tool-result " +
		"summary when a next tool call is available. Do not " +
		"ask for confirmation for an in-scope next step unless " +
		"it is destructive, expensive, or genuinely ambiguous. " +
		"Do not claim that a document, message, upload, " +
		"schedule, file, wiki page, or external resource was " +
		"created, sent, written, published, or updated unless " +
		"the latest tool result proves it. If a tool failed " +
		"or returned a partial result, recover with available " +
		"tools when there is a clear path: corrected " +
		"parameters, canonical ids, alternate lookup, retry, " +
		"or verification. Only return a blocker when no safe " +
		"next step remains. For files and media, return " +
		"`MEDIA:` or `MEDIA_DIR:` lines only after the " +
		"file or directory exists and is intended to be " +
		"sent. For docs and iWiki, return the link, id, " +
		"or title when available. " +
		"Keep final answers concise and user-facing; avoid " +
		"exposing tool/source/process details unless they are " +
		"the exact result or blocker."
	skillPreambleOnlyRule = "A preamble-only skill response is " +
		"invalid. "
	skillSameTurnToolRule = "If you say you will read, load, " +
		"use, write, create, send, or publish through a " +
		"skill, the same assistant message must include " +
		"the required tool call. Do not stop after " +
		"announcing the skill-backed next step. "

	defaultAgentInstruction = "You are a helpful assistant. " +
		"Keep replies concise, but make each reply " +
		"substantive enough to complete the user's request. " +
		"Act without asking for confirmation when the next " +
		"step is clearly in scope, cheap, and reversible. " +
		preambleOnlyFinalAnswerRule +
		substantiveSameTurnRule +
		artifactCompletionRule
	openClawSkillsGuidance = "Treat the skill overview below as the " +
		"skills available in this session. Each entry " +
		"includes a path to that skill's SKILL.md on disk. " +
		"This is a blocking requirement for matching " +
		"skills. " +
		"If the user names a skill, names a slash command, " +
		"or the task clearly matches a skill description, " +
		"you must use that skill in the same turn. Start " +
		"with one brief user-visible preamble about the " +
		"immediate next step, then call `skill_load` for " +
		"that skill right away in the same turn. That " +
		"brief preamble is part of acting immediately, " +
		"not a pause to ask what to do next. That preamble may " +
		"not be the whole reply. " +
		skillPreambleOnlyRule +
		skillSameTurnToolRule +
		"That preamble may " +
		"announce the immediate task, but do not use it " +
		"for substantive guidance, capability " +
		"disclaimers, or explanations about which " +
		"subsystem loads versus runs the skill. Never mention " +
		"reading, loading, or using a matching skill " +
		"unless you already called `skill_load` for it in " +
		"this turn. Never say that you could read or load " +
		"a matching skill later without actually doing it " +
		"first. Do not answer a matching skill task from " +
		"the short summary, prior knowledge, or partial " +
		"memory. Even if you think you already know the " +
		"answer, load `SKILL.md` first. Load `SKILL.md` " +
		"before giving substantive guidance or acting on " +
		"the workflow. When " +
		"`SKILL.md` references " +
		"relative paths, resolve them from the skill " +
		"directory first. Read only the supporting docs, " +
		"scripts, assets, examples, or templates you still " +
		"need. Do not respond with capability disclaimers " +
		"such as `I can read the skill` when you can load " +
		"it now. Announce the next step briefly and do it. " +
		"When the user asks you to add, teach, configure, " +
		"preserve, or reuse a durable capability, workflow, " +
		"integration, domain rule, team process, API, CLI, " +
		"MCP endpoint, document convention, or tool usage " +
		"pattern, or to remember an executable workflow or " +
		"integration, prefer creating or updating a local " +
		"skill over treating it as a one-off answer. For " +
		"lightweight facts, preferences, or simple standing " +
		"rules, use memory instead. " +
		"Use platform code and tools for stable safety " +
		"boundaries, secrets, permissions, file paths, " +
		"validation, and execution guarantees; use skill " +
		"context for evolving behavior, triggers, " +
		"constraints, examples, recovery paths, and domain " +
		"knowledge. If you create or update a skill, do not " +
		"stop after describing the idea: choose a writable " +
		"user-managed skill root, not bundled skills unless " +
		"explicitly asked to edit them, write the skill files, " +
		"avoid storing raw secrets, validate or inspect the skill, " +
		"refresh or reload skills when the runtime provides " +
		"that path, and then use the skill to complete the " +
		"current task. " +
		"Reuse bundled scripts, templates, and assets " +
		"when they already fit. If multiple skills match, " +
		"use the smallest set that covers the task. Keep " +
		"context small and avoid bulk-loading docs. Do not " +
		"invent commands, flags, auth steps, file layouts, " +
		"or workflows from a short summary or partial " +
		"memory. Keep exploring nearby runtime facts, " +
		"retries, and recovery paths yourself before asking " +
		"for more input. If a matching skill is missing, " +
		"unreadable, or still lacks a required external " +
		"input after reasonable local exploration, state " +
		"the issue briefly and continue with the best " +
		"fallback."
	openClawSkillsPathGuidance = "Each entry includes a path to that " +
		"skill's SKILL.md on disk."
	openClawCompactSkillsGuidance = "The overview may show a compact " +
		"subset. When no shown skill clearly matches, call " +
		"`skill_list` to inspect the full catalog before " +
		"choosing a skill."
	openClawSkillLoadToolDescription = "Load a skill body and optional " +
		"docs. This is a blocking requirement when the user " +
		"names a listed skill, names a slash command, or the " +
		"task clearly matches a listed skill description. " +
		"Before the first matching load, start with one " +
		"brief user-visible preamble that says which skill " +
		"or skill docs you are reading next, then call this " +
		"tool right away in the same turn. Do not pause " +
		"after that preamble to ask what to do next, and do " +
		"not send the preamble as the whole reply. " +
		skillPreambleOnlyRule +
		skillSameTurnToolRule +
		"Do not " +
		"use that preamble for " +
		"capability disclaimers, implementation-split " +
		"explanations, or substantive guidance about the " +
		"task. Do not answer from a short skill summary, " +
		"prior knowledge, or partial memory when a matching " +
		"skill exists. Load `SKILL.md` first, then load " +
		"only the extra docs you still need."
	openClawToolingGuidance = "For common PDF, DOCX, text, CSV, " +
		"and spreadsheet uploads already in the chat, prefer " +
		"read_document or read_spreadsheet before falling back " +
		"to exec_command. " +
		"For questions about the active chat history, recent " +
		"turns, or who said something in the current session, use " +
		"conversation_history before searching long-term memory. " +
		"Only use long-term memory tools for facts that are not " +
		"available in the current session. " +
		"Do not call exec_command just to print OPENCLAW_* upload " +
		"vars or inspect recent upload metadata when a matching " +
		"chat file is already available. For other general local " +
		"shell work, use exec_command. For interactive follow-up " +
		"input, use " +
		"write_stdin and kill_session when needed. Use message " +
		"to send to the current chat or an explicit target. " +
		artifactCompletionRule + " " +
		"Use the available tool path to complete the request " +
		"in this turn. " +
		"Chat uploads are saved to stable host paths. For host " +
		"commands, prefer OPENCLAW_LAST_UPLOAD_PATH or " +
		"OPENCLAW_SESSION_UPLOADS_DIR, OPENCLAW_LAST_UPLOAD_HOST_REF, " +
		"OPENCLAW_LAST_UPLOAD_NAME, " +
		"OPENCLAW_LAST_UPLOAD_MIME, OPENCLAW_MEMORY_FILE, " +
		"OPENCLAW_USER_MEMORY_FILE, OPENCLAW_CHAT_MEMORY_FILE, and " +
		"OPENCLAW_RECENT_UPLOADS_JSON instead of guessing " +
		"attachment paths. For long-running work, independent " +
		"verification, or background work that can continue after " +
		"this turn, use subagents_spawn with mode=async. When a " +
		"subagent result is required before continuing, use " +
		"mode=sync. When the user must review the subagent result " +
		"before you continue, use mode=review, show the result, " +
		"and wait for the next user reply. Do not use subagents " +
		"for small, tightly-coupled steps, and do not spawn " +
		"nested subagents. " +
		"When a user follows up about a " +
		"recent upload in the current chat, assume they mean " +
		"that existing upload unless the reference is " +
		"genuinely ambiguous. Match by media kind first: " +
		"prefer OPENCLAW_LAST_PDF_PATH, " +
		"OPENCLAW_LAST_AUDIO_PATH, OPENCLAW_LAST_VIDEO_PATH, or " +
		"OPENCLAW_LAST_IMAGE_PATH when the request clearly targets " +
		"one of those kinds. Telegram voice notes count as audio, " +
		"video notes count as video, and documents with image/audio/" +
		"video MIME types still count as that media kind. If the " +
		"user replies to an earlier media message, treat that " +
		"replied media as " +
		"the default target unless they clearly ask for something " +
		"else. Do not ask the user to re-upload a file or provide " +
		"a local path when the recent upload context already lists " +
		"a matching upload for this chat. If the user wants a " +
		"derived file sent back in the current chat, send it with " +
		"message instead of asking which channel or delivery " +
		"method to use. For exec_command, do " +
		"not assume skill workspace paths like work/inputs. Do not " +
		"expose local host paths to the user; when acknowledging a " +
		"new upload, refer to it only by filename and media kind, " +
		"not by OPENCLAW_* vars or a machine path. If the channel " +
		"gives you an opaque placeholder filename, avoid surfacing " +
		"that raw placeholder to the user unless they explicitly " +
		"ask for the exact filename. Refer to uploads " +
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
		"back to the user. OPENCLAW_MEMORY_FILE is a visible " +
		"MEMORY.md file for the current scope, and remains a " +
		"compatibility alias. OPENCLAW_USER_MEMORY_FILE is this " +
		"user's personal memory file. OPENCLAW_CHAT_MEMORY_FILE " +
		"is the current chat's shared memory file. These files are " +
		"not hidden internal state. If the user asks what you " +
		"remember or asks to inspect memory, read the relevant " +
		"file and quote or summarize the relevant lines. If the " +
		"user explicitly says 'remember this' or asks you to " +
		"remember a durable fact, preference, task list, " +
		"checklist, or reminder list, update the narrowest relevant " +
		"memory file with a short bullet in the same turn. Do not " +
		"store secrets or large transcripts in memory files. Do not " +
		"store reusable task workflows, output formats, tool " +
		"procedures, or post-task feedback in memory files unless " +
		"the user explicitly " +
		"asks to save that content as memory. " +
		"If a memory file does not exist yet, you may create it " +
		"at that exact path. Prefer already installed local tools " +
		"for OCR, PDF, audio, image, and video work before " +
		"trying package installs or long downloads. " +
		"When creating a cron job from chat, omit channel and " +
		"target to send results back to the current chat by " +
		"default. When adding cron jobs, write the stored task " +
		"as a one-time execution instruction, not as another " +
		"scheduling request. Prefer concise, outcome-oriented " +
		"tasks over brittle shell transcripts unless exact " +
		"commands are truly required. Use cron for future or " +
		"recurring work."

	openClawDeferredToolingGuidance = "Tool-backed work is available " +
		"through `tool_search` and `dynamic_agent`, with some " +
		"latency-sensitive tools kept directly available when " +
		"configured. Use direct tools for simple local actions when " +
		"they are present. Use `tool_search` when you need exact " +
		"tool or skill names, then call `dynamic_agent`; pass exact " +
		"tool names such as web_fetch or browser in its `tools` " +
		"field, and pass only real skill names in its `skills` " +
		"field. Use `dynamic_agent` for broader " +
		"files, uploads, browser automation, shell work, messaging, " +
		"cron, memory, skills, knowledge, external tools, or " +
		"verification. Give the sub-agent a self-contained request " +
		"and ask it to complete the concrete action or return the " +
		"exact blocker. Answer directly only when no tool work is " +
		"needed."

	openClawDeferredToolDescription = "Run a focused OpenClaw tool " +
		"worker with access to configured local tools, toolsets, " +
		"skills, memory, knowledge, and messaging capabilities. Use " +
		"this for any task that needs tool work; include all relevant " +
		"user context in the request and ask for the completed result " +
		"or exact blocker."

	browserToolingGuidance = "For real browser automation, use " +
		"browser. Prefer browser snapshot plus act for page " +
		"interaction, use browser screenshot when visual " +
		"verification matters, and keep using the same targetId " +
		"after tabs or snapshot calls. When the user mentions " +
		"their current browser tab, relay, or extension attach " +
		"flow, use profile=\"chrome\" when that profile exists."

	agentTypeLLM        = "llm"
	agentTypeClaudeCode = "claude-code"

	openAIVariantAuto = "auto"

	defaultOpenAIVariant = openAIVariantAuto

	deepSeekAPIHost = "api.deepseek.com"
	qwenAPIHost     = "dashscope.aliyuncs.com"
	hunyuanAPIHost  = "api.hunyuan.cloud.tencent.com"

	openAIAPIKeyEnvName  = "OPENAI_API_KEY"
	openAIBaseURLEnvName = "OPENAI_BASE_URL"
	openAIHeadersEnvName = "OPENAI_HEADERS"
	openAIModelEnvName   = "OPENAI_MODEL"

	errClaudeCodeAgentNoPrompts = "claude-code agent does not support " +
		"agent prompts"
)

// Main runs the OpenClaw-like CLI and returns an exit code.
//
// args should not include the program name.
func Main(args []string) int {
	return MainWithOptions(args)
}

// MainWithOptions runs the OpenClaw-like CLI with runtime options and returns
// an exit code.
//
// args should not include the program name.
func MainWithOptions(args []string, options ...RuntimeOption) int {
	if len(args) > 0 {
		switch args[0] {
		case subcmdPairing:
			return runPairing(args[1:])
		case subcmdDoctor:
			return runDoctor(args[1:])
		case subcmdInspect:
			return runInspect(args[1:])
		case subcmdBootstrap:
			return runBootstrap(args[1:])
		case subcmdEvolution:
			return runEvolution(args[1:])
		}
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		os.Interrupt,
		syscall.SIGTERM,
	)
	defer stop()

	if err := RunWithOptions(ctx, args, options...); err != nil {
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

// RunWithOptions runs OpenClaw until ctx is canceled or the runtime exits.
func RunWithOptions(
	ctx context.Context,
	args []string,
	options ...RuntimeOption,
) error {
	return run(ctx, args, options...)
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

func runtimeStartupLines(
	opts runOptions,
	stateDir string,
	channels []channel.Channel,
	needsModel bool,
) []startupLogLine {
	return []startupLogLine{
		{text: fmt.Sprintf("App name: %s", strings.TrimSpace(opts.AppName))},
		{text: configStartupSummary(opts.ConfigPath)},
		{text: fmt.Sprintf(
			"State dir: %s",
			startupPathSummary(stateDir),
		)},
		{text: fmt.Sprintf(
			"Channels: %s",
			channelStartupSummary(channels),
		)},
		{text: fmt.Sprintf(
			"Model: %s",
			modelStartupSummary(opts, needsModel),
		)},
		{text: fmt.Sprintf(
			"Storage: session=%s memory=%s",
			strings.TrimSpace(opts.SessionBackend),
			resolveMemoryBackendType(opts.MemoryBackend),
		)},
	}
}

func configStartupSummary(configPath string) string {
	path := strings.TrimSpace(configPath)
	if path == "" {
		return "Config: built-in defaults and CLI flags"
	}
	return fmt.Sprintf("Config: %s", startupPathSummary(path))
}

func startupPathSummary(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}
	absPath, err := filepath.Abs(trimmed)
	if err != nil {
		return trimmed
	}
	return absPath
}

func channelStartupSummary(channels []channel.Channel) string {
	ids := channelIDs(channels)
	if len(ids) == 0 {
		return "none"
	}
	return strings.Join(ids, ", ")
}

func modelStartupSummary(
	opts runOptions,
	needsModel bool,
) string {
	if !needsModel {
		return "disabled"
	}
	mode := strings.ToLower(strings.TrimSpace(opts.ModelMode))
	if mode == "" {
		mode = modeOpenAI
	}
	if mode != modeOpenAI {
		return mode
	}
	modelName := strings.TrimSpace(opts.OpenAIModel)
	if modelName == "" {
		return mode
	}
	return fmt.Sprintf("%s/%s", mode, modelName)
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
			"Stream:   POST %s",
			gwSrv.MessagesStreamPath(),
		)},
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
	A2A      A2ASurface
	Admin    AdminSurface
	Channels []channel.Channel
	prompts  *RuntimePromptController
	adminCfg *admin.Config
	appName  string
	session  session.Service
	subagent SubagentService

	runner            runner.Runner
	cronRunner        closeFunc
	sessionSvc        closeFunc
	memorySvc         closeFunc
	cronSvc           closeFunc
	subagentSvc       closeFunc
	skillsWatch       closeFunc
	evolutionService  evolution.Service
	toolSets          []tool.ToolSet
	telemetryShutdown func(context.Context) error
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

type PromptSnapshot struct {
	Instruction  string
	SystemPrompt string
}

type RuntimePromptController struct {
	agent agent.Agent

	mu       sync.RWMutex
	snapshot PromptSnapshot
}

func newRuntimePromptController(
	agt agent.Agent,
	instruction string,
	systemPrompt string,
) *RuntimePromptController {
	if agt == nil {
		return nil
	}
	if _, ok := agt.(*llmagent.LLMAgent); !ok {
		return nil
	}
	return &RuntimePromptController{
		agent: agt,
		snapshot: PromptSnapshot{
			Instruction:  instruction,
			SystemPrompt: systemPrompt,
		},
	}
}

// PromptController exposes runtime prompt updates without changing
// Runtime's exported struct layout.
func (r *Runtime) PromptController() *RuntimePromptController {
	if r == nil {
		return nil
	}
	return r.prompts
}

func (r *Runtime) AppName() string {
	if r == nil {
		return ""
	}
	return strings.TrimSpace(r.appName)
}

func (r *Runtime) SessionService() session.Service {
	if r == nil {
		return nil
	}
	return r.session
}

// SubagentService is the OpenClaw subagent control-plane service exposed by
// Runtime.
type SubagentService interface {
	ListForUser(
		userID string,
		filter openclawsubagent.ListFilter,
	) []openclawsubagent.Run
	GetForUser(userID string, runID string) (*openclawsubagent.Run, error)
	CancelForUser(
		userID string,
		runID string,
	) (*openclawsubagent.Run, bool, error)
}

func (r *Runtime) SubagentService() SubagentService {
	if r == nil {
		return nil
	}
	return r.subagent
}

func (r *Runtime) ConfigureAdmin(
	configure func(*admin.Config),
) {
	if r == nil || r.adminCfg == nil || configure == nil {
		return
	}
	cfg := *r.adminCfg
	configure(&cfg)
	r.applyAdminConfig(cfg)
}

// AddAdminOptions appends runtime-scoped admin options without changing
// Runtime's exported struct layout.
func (r *Runtime) AddAdminOptions(opts ...admin.Option) {
	if r == nil || len(opts) == 0 {
		return
	}

	filtered := make([]admin.Option, 0, len(opts))
	filtered = append(filtered, runtimeAdminOptions(r)...)
	for _, opt := range opts {
		if opt != nil {
			filtered = append(filtered, opt)
		}
	}
	if len(filtered) == 0 {
		return
	}

	setRuntimeAdminOptions(r, filtered)
	if r.adminCfg == nil {
		return
	}
	r.applyAdminConfig(*r.adminCfg)
}

func (r *Runtime) applyAdminConfig(cfg admin.Config) {
	if r == nil {
		return
	}
	r.adminCfg = &cfg
	r.Admin.Handler = admin.New(cfg, runtimeAdminOptions(r)...).Handler()
	r.Admin.Addr = strings.TrimSpace(cfg.AdminAddr)
	r.Admin.URL = strings.TrimSpace(cfg.AdminURL)
}

func (c *RuntimePromptController) Snapshot() PromptSnapshot {
	if c == nil {
		return PromptSnapshot{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.snapshot
}

func (c *RuntimePromptController) SetInstruction(
	instruction string,
) {
	if c == nil {
		return
	}
	llm, ok := c.agent.(*llmagent.LLMAgent)
	if !ok {
		return
	}
	llm.SetInstruction(instruction)
	c.mu.Lock()
	c.snapshot.Instruction = instruction
	c.mu.Unlock()
}

func (c *RuntimePromptController) SetSystemPrompt(
	systemPrompt string,
) {
	if c == nil {
		return
	}
	llm, ok := c.agent.(*llmagent.LLMAgent)
	if !ok {
		return
	}
	llm.SetGlobalInstruction(systemPrompt)
	c.mu.Lock()
	c.snapshot.SystemPrompt = systemPrompt
	c.mu.Unlock()
}

func (c *RuntimePromptController) SetPrompts(
	instruction string,
	systemPrompt string,
) {
	if c == nil {
		return
	}
	llm, ok := c.agent.(*llmagent.LLMAgent)
	if !ok {
		return
	}
	llm.SetPrompts(instruction, systemPrompt)
	c.mu.Lock()
	c.snapshot = PromptSnapshot{
		Instruction:  instruction,
		SystemPrompt: systemPrompt,
	}
	c.mu.Unlock()
}

// NewRuntime constructs an OpenClaw runtime based on CLI args / config file,
// but does not start an HTTP server.
func NewRuntime(
	ctx context.Context,
	args []string,
) (*Runtime, error) {
	return NewRuntimeWithOptions(ctx, args)
}

// NewRuntimeWithOptions constructs an embedded OpenClaw runtime with options.
func NewRuntimeWithOptions(
	ctx context.Context,
	args []string,
	options ...RuntimeOption,
) (rt *Runtime, err error) {
	rt = &Runtime{}
	startedAt := time.Now()
	runtimeOpts := buildRuntimeOptions(options)
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
	langfuseRT, err := maybeEnableLangfuse(ctx, opts)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("langfuse config failed: %w", err),
		}
	}
	langfuseStatus := admin.LangfuseStatus{}
	if langfuseRT != nil {
		langfuseStatus = langfuseRT.adminStatus
		rt.telemetryShutdown = langfuseRT.shutdown
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
	rt.appName = opts.AppName

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

	stores, err := newRuntimeStores(resolvedStateDir)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create runtime stores failed: %w", err),
		}
	}

	prompts, err := resolveAgentPrompts(opts)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent prompt config failed: %w", err),
		}
	}

	fileMemoryStore := fileMemoryStoreForBackend(
		opts.MemoryBackend,
		stores.memoryFiles,
	)
	codeExec, err := codeExecutorLoader(runtimeOpts)(
		resolvedStateDir,
		opts.EnableLocalExec,
		opts.CodeExecutor,
	)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create code executor failed: %w", err),
		}
	}
	var sandboxExecEngine codeexecutor.Engine
	if isSandboxCodeExecutor(opts.CodeExecutor) {
		sandboxExecEngine = codeExecutorEngine(codeExec)
		if sandboxExecEngine == nil {
			return nil, &exitError{
				Code: 1,
				Err: errors.New(
					"sandbox code executor does not expose a program runner",
				),
			}
		}
	}
	openClawTools := buildOpenClawTools(
		opts.EnableOpenClawTools,
		resolvedStateDir,
		stores.uploads,
		fileMemoryStore,
		sandboxExecEngine,
		opts.HostExecDefaultTimeout,
	)
	extraTools := memoryServiceTools(memSvc)
	extraTools = append(extraTools, openClawTools.tools...)

	var (
		toolSets    []tool.ToolSet
		ag          agent.Agent
		skillsRepo  *ocskills.Repository
		skillsProv  skill.RepositoryProvider
		skillsWatch *ocskills.WatchService
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
		postToolPromptEnabled := resolvePostToolPromptEnabled(
			opts,
			runtimeOpts,
		)
		agentCfg := agentConfig{
			AppName:                 opts.AppName,
			AddSessionSummary:       opts.AddSessionSummary,
			EnableContextCompaction: opts.EnableContextCompaction,
			ContextCompactionOversizedToolResultMaxTokens: opts.
				ContextCompactionOversizedToolResultMaxTokens,
			MaxHistoryRuns:        opts.MaxHistoryRuns,
			MaxLLMCalls:           opts.MaxLLMCalls,
			MaxToolIterations:     opts.MaxToolIterations,
			PreloadMemory:         opts.PreloadMemory,
			GenerationConfig:      opts.GenerationConfig,
			PostToolPromptEnabled: postToolPromptEnabled,
			Instruction:           prompts.Instruction,
			SystemPrompt:          prompts.SystemPrompt,

			SkillsRoot:      opts.SkillsRoot,
			SkillsExtraDirs: splitCSV(opts.SkillsExtraDir),
			SkillsDebug:     opts.SkillsDebug,
			SkillsAllowBundled: splitCSV(
				opts.SkillsAllowBundled,
			),
			SkillConfigs:            opts.SkillConfigs,
			SkillConfigKeys:         resolveSkillConfigKeys(opts),
			SkillsWatch:             opts.SkillsWatch,
			SkillsWatchBundled:      opts.SkillsWatchBundled,
			SkillsWatchDebounce:     opts.SkillsWatchDebounce,
			SkillsSummaryCacheTTL:   opts.SkillsSummaryCacheTTL,
			SkillsOverviewLimit:     opts.SkillsOverviewLimit,
			SkillsOverviewPinned:    splitCSV(opts.SkillsOverviewPinned),
			SkillsToolProfile:       opts.SkillsToolProfile,
			SkillsLoadMode:          opts.SkillsLoadMode,
			SkillsMaxLoaded:         opts.SkillsMaxLoaded,
			SkillsToolResults:       opts.SkillsToolResults,
			SkillsSkipFallback:      opts.SkillsSkipFallback,
			SkillsToolingGuide:      opts.SkillsToolingGuide,
			KnowledgesConfig:        opts.KnowledgesConfig,
			EvolutionSkillScopeMode: opts.EvolutionSkillScopeMode,
			StateDir:                resolvedStateDir,
			MemoryFileStore:         fileMemoryStore,

			EnableLocalExec:      opts.EnableLocalExec,
			CodeExecutor:         opts.CodeExecutor,
			EnableOpenClawTools:  opts.EnableOpenClawTools,
			OpenClawToolingGuide: opts.OpenClawToolingGuide,
			EnableParallelTools:  opts.EnableParallelTools,
			codeExecutor:         codeExec,

			ToolProviders: opts.ToolProviders,
			ToolSets:      opts.ToolSets,

			RefreshToolSetsOnRun: opts.RefreshToolSetsOnRun,
			DeferToolSurface:     opts.DeferToolSurface,
			DeferToolSurfaceMode: opts.DeferToolSurfaceMode,
			DeferToolSurfaceThresholdChars: opts.
				DeferToolSurfaceChars,
			DeferToolSurfaceDefaultDirectTools: boolPtr(
				opts.DeferToolSurfaceDefaultDirectTools,
			),
			DeferToolSurfaceDirectTools: splitCSV(
				opts.DeferToolSurfaceDirect,
			),
			DynamicAgentTimeout: opts.DynamicAgentTimeout,
		}
		cwd, _ := os.Getwd()
		skillsProv = newScopedSkillRepositoryProvider(cwd, agentCfg)
		agentCfg.SkillRepositoryProvider = skillsProv
		ag, skillsRepo, err = newAgent(
			mdl,
			agentCfg,
			extraTools,
			toolSets,
		)
		if err == nil {
			skillsWatch = newSkillsWatchService(
				cwd,
				agentCfg,
				skillsRepo,
			)
		}
	}
	if err != nil {
		closeToolSets(toolSets)
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create agent failed: %w", err),
		}
	}
	rt.prompts = newRuntimePromptController(
		ag,
		prompts.Instruction,
		prompts.SystemPrompt,
	)
	rt.toolSets = toolSets
	rt.skillsWatch = skillsWatch

	bridgedSessionSvc := conversationscope.WrapSessionService(sessionSvc)
	rt.session = bridgedSessionSvc
	runnerOpts := []runner.Option{
		runner.WithSessionService(bridgedSessionSvc),
		runner.WithPlugins(conversation.Plugin{}),
		runner.WithAwaitUserReplyRouting(true),
	}
	runnerOpts = appendMemoryServiceRunnerOption(runnerOpts, memSvc)
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

	// Evolution: if skills repo exists, wire up the async learning loop.
	evoSvc := maybeCreateEvolutionService(
		opts,
		skillsRepo,
		skillsProv,
	)
	if evoSvc != nil {
		runnerOpts = append(runnerOpts, runner.WithEvolutionService(evoSvc))
		rt.evolutionService = evoSvc
	}

	r := runner.NewRunner(opts.AppName, ag, runnerOpts...)
	rt.runner = r

	runtimeProfileResolver, runtimeProfileCatalog, runtimeProfileRequired :=
		runtimeProfileResolverFromOptions(
			opts.RuntimeProfiles,
			runtimeOpts,
		)

	gwOpts := makeGatewayOptions(
		splitCSV(opts.AllowUsers),
		opts.RequireMention,
		mentionPatterns,
	)
	gwOpts = append(gwOpts, gateway.WithAppName(opts.AppName))
	gwOpts = append(gwOpts, gateway.WithUploadStore(stores.uploads))
	gwOpts = append(gwOpts, gateway.WithPersonaStore(stores.personas))
	if fileMemoryStore != nil {
		gwOpts = append(
			gwOpts,
			gateway.WithMemoryFileStore(fileMemoryStore),
		)
	}
	if debugRec != nil {
		gwOpts = append(gwOpts, gateway.WithDebugRecorder(debugRec))
	}
	gwOpts = appendRuntimeProfileGatewayOption(
		gwOpts,
		runtimeProfileResolver,
		runtimeProfileRequired,
	)
	if langfuseRT != nil && langfuseRT.runOptionResolver != nil {
		gwOpts = append(
			gwOpts,
			gateway.WithRunOptionResolver(
				langfuseRT.runOptionResolver,
			),
		)
	}
	gwOpts = appendLatencyDiagnosticsGatewayOption(
		gwOpts,
		opts.StateDir,
		opts.LatencyDiagnosticsEnabled,
		opts.LatencyDiagnosticsEvents,
	)
	gwOpts = appendSkillsOverviewGatewayOption(
		gwOpts,
		opts.SkillsOverviewLimit,
		splitCSV(opts.SkillsOverviewPinned),
	)
	gwOpts = append(
		gwOpts,
		gateway.WithRunOptionResolver(
			buildDeliveryRunOptionResolver(),
		),
		gateway.WithRunOptionResolver(
			buildConversationRunOptionResolver(
				opts.AppName,
				bridgedSessionSvc,
				conversation.HistoryOptions{
					AddSessionSummary: opts.AddSessionSummary,
					MaxHistoryRuns:    opts.MaxHistoryRuns,
				},
			),
		),
	)
	gwOpts = appendRuntimeGatewayRunOptions(gwOpts, runtimeOpts)
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
	rt.A2A, err = newA2ASurface(ag, r, opts)
	if err != nil {
		return nil, &exitError{
			Code: 1,
			Err:  fmt.Errorf("create a2a failed: %w", err),
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
		stores.uploads,
	)
	gw.SetPersonaStore(stores.personas)
	gw.SetMemoryFileStore(fileMemoryStore)
	gw.SetRuntimeProfileAppNames(runtimeProfileAppNames(opts.RuntimeProfiles))
	gw.SetRuntimeProfileCatalog(runtimeProfileCatalog)

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
		cronSvc     *cron.Service
		cronRunner  runner.Runner
		subagentSvc *subagentrun.Service
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
		cronOpts := runtimeProfileCronOptions(runtimeProfileResolver)
		if debugRec != nil {
			cronOpts = append(
				cronOpts,
				cron.WithDebugRecorder(debugRec),
			)
		}
		cronSvc, err = cron.NewService(
			resolvedStateDir,
			cronRunner,
			openClawTools.router,
			cronOpts...,
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

		subagentSvc, err = subagentrun.NewService(
			resolvedStateDir,
			r,
			openClawTools.router,
		)
		if err != nil {
			return nil, &exitError{
				Code: 1,
				Err:  fmt.Errorf("create subagent service failed: %w", err),
			}
		}
		openClawTools.subagentTools.SetService(subagentSvc)
		subagentSvc.Start(ctx)
		rt.subagent = subagentSvc
		rt.subagentSvc = subagentSvc
	}

	if opts.AdminEnabled {
		adminURL := listenURL(opts.AdminAddr)
		adminCfg := buildAdminConfig(
			opts,
			agentType,
			instanceID,
			langfuseStatus,
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
			rt.prompts,
			nil,
			opts.AdminAddr,
			adminURL,
			skillsRepo,
			skillsWatch,
			fileMemoryStore,
			rt.SessionService(),
		)
		setRuntimeAdminOptions(rt, buildAdminOptions(opts))
		rt.applyAdminConfig(adminCfg)
	}

	return rt, nil
}

// Run sends one message through the OpenClaw runtime runner.
func (r *Runtime) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	if r == nil || r.runner == nil {
		return nil, errors.New("openclaw runtime runner is not configured")
	}
	return r.runner.Run(ctx, userID, sessionID, message, runOpts...)
}

// Close releases owned resources (session/memory services, toolsets, runner).
func (r *Runtime) Close() error {
	if r == nil {
		return nil
	}

	var errs []error
	if r.cronSvc != nil {
		_ = r.cronSvc.Close()
	}
	if r.cronRunner != nil {
		_ = r.cronRunner.Close()
	}
	if r.subagentSvc != nil {
		_ = r.subagentSvc.Close()
	}
	if r.skillsWatch != nil {
		if err := r.skillsWatch.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	closeToolSets(r.toolSets)
	closeMemoryService(r.memorySvc)
	closeSessionService(r.sessionSvc)

	if r.runner != nil {
		if err := r.runner.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if r.evolutionService != nil {
		if err := r.evolutionService.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := shutdownTelemetry(r.telemetryShutdown); err != nil {
		errs = append(errs, err)
	}
	clearRuntimeAdminOptions(r)
	return errors.Join(errs...)
}

func run(
	ctx context.Context,
	args []string,
	options ...RuntimeOption,
) error {
	startedAt := time.Now()
	runtimeOpts := buildRuntimeOptions(options)
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
	debugDir := filepath.Join(resolvedStateDir, defaultDebugRecorderDir)
	if debugRec != nil {
		debugDir = debugRec.Dir()
	}

	browserServerSup, err := maybeStartBrowserServerSupervisor(
		ctx,
		opts.ToolProviders,
		debugDir,
	)
	if err != nil {
		log.Warnf("browser server auto-start failed: %v", err)
	}
	langfuseRT, err := maybeEnableLangfuse(ctx, opts)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("langfuse config failed: %w", err),
		}
	}
	langfuseStatus := admin.LangfuseStatus{}
	var langfuseShutdown func(context.Context) error
	if langfuseRT != nil {
		langfuseStatus = langfuseRT.adminStatus
		langfuseShutdown = langfuseRT.shutdown
	}
	defer func() {
		if langfuseShutdown == nil {
			return
		}
		if err := shutdownTelemetry(langfuseShutdown); err != nil {
			log.Warnf("shutdown langfuse failed: %v", err)
		}
	}()

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

	stores, err := newRuntimeStores(resolvedStateDir)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create runtime stores failed: %w", err),
		}
	}

	prompts, err := resolveAgentPrompts(opts)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("agent prompt config failed: %w", err),
		}
	}

	fileMemoryStore := fileMemoryStoreForBackend(
		opts.MemoryBackend,
		stores.memoryFiles,
	)
	codeExec, err := codeExecutorLoader(runtimeOpts)(
		resolvedStateDir,
		opts.EnableLocalExec,
		opts.CodeExecutor,
	)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create code executor failed: %w", err),
		}
	}
	var sandboxExecEngine codeexecutor.Engine
	if isSandboxCodeExecutor(opts.CodeExecutor) {
		sandboxExecEngine = codeExecutorEngine(codeExec)
		if sandboxExecEngine == nil {
			return &exitError{
				Code: 1,
				Err: errors.New(
					"sandbox code executor does not expose a program runner",
				),
			}
		}
	}
	openClawTools := buildOpenClawTools(
		opts.EnableOpenClawTools,
		resolvedStateDir,
		stores.uploads,
		fileMemoryStore,
		sandboxExecEngine,
		opts.HostExecDefaultTimeout,
	)
	extraTools := memoryServiceTools(memSvc)
	extraTools = append(extraTools, openClawTools.tools...)

	var (
		toolSets    []tool.ToolSet
		ag          agent.Agent
		skillsRepo  *ocskills.Repository
		skillsProv  skill.RepositoryProvider
		skillsWatch *ocskills.WatchService
	)
	defer func() {
		closeToolSets(toolSets)
	}()
	defer func() {
		if skillsWatch == nil {
			return
		}
		if err := skillsWatch.Close(); err != nil {
			log.Warnf("close skills watch failed: %v", err)
		}
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
		postToolPromptEnabled := resolvePostToolPromptEnabled(
			opts,
			runtimeOpts,
		)
		agentCfg := agentConfig{
			AppName:                 opts.AppName,
			AddSessionSummary:       opts.AddSessionSummary,
			EnableContextCompaction: opts.EnableContextCompaction,
			ContextCompactionOversizedToolResultMaxTokens: opts.
				ContextCompactionOversizedToolResultMaxTokens,
			MaxHistoryRuns:        opts.MaxHistoryRuns,
			MaxLLMCalls:           opts.MaxLLMCalls,
			MaxToolIterations:     opts.MaxToolIterations,
			PreloadMemory:         opts.PreloadMemory,
			GenerationConfig:      opts.GenerationConfig,
			PostToolPromptEnabled: postToolPromptEnabled,
			Instruction:           prompts.Instruction,
			SystemPrompt:          prompts.SystemPrompt,

			SkillsRoot:      opts.SkillsRoot,
			SkillsExtraDirs: splitCSV(opts.SkillsExtraDir),
			SkillsDebug:     opts.SkillsDebug,
			SkillsAllowBundled: splitCSV(
				opts.SkillsAllowBundled,
			),
			SkillConfigs:            opts.SkillConfigs,
			SkillConfigKeys:         resolveSkillConfigKeys(opts),
			SkillsWatch:             opts.SkillsWatch,
			SkillsWatchBundled:      opts.SkillsWatchBundled,
			SkillsWatchDebounce:     opts.SkillsWatchDebounce,
			SkillsSummaryCacheTTL:   opts.SkillsSummaryCacheTTL,
			SkillsOverviewLimit:     opts.SkillsOverviewLimit,
			SkillsOverviewPinned:    splitCSV(opts.SkillsOverviewPinned),
			SkillsToolProfile:       opts.SkillsToolProfile,
			SkillsLoadMode:          opts.SkillsLoadMode,
			SkillsMaxLoaded:         opts.SkillsMaxLoaded,
			SkillsToolResults:       opts.SkillsToolResults,
			SkillsSkipFallback:      opts.SkillsSkipFallback,
			SkillsToolingGuide:      opts.SkillsToolingGuide,
			KnowledgesConfig:        opts.KnowledgesConfig,
			EvolutionSkillScopeMode: opts.EvolutionSkillScopeMode,
			StateDir:                resolvedStateDir,
			MemoryFileStore:         fileMemoryStore,

			EnableLocalExec:     opts.EnableLocalExec,
			CodeExecutor:        opts.CodeExecutor,
			EnableOpenClawTools: opts.EnableOpenClawTools,
			EnableParallelTools: opts.EnableParallelTools,
			codeExecutor:        codeExec,

			ToolProviders: opts.ToolProviders,
			ToolSets:      opts.ToolSets,

			RefreshToolSetsOnRun: opts.RefreshToolSetsOnRun,
			DeferToolSurface:     opts.DeferToolSurface,
			DeferToolSurfaceMode: opts.DeferToolSurfaceMode,
			DeferToolSurfaceThresholdChars: opts.
				DeferToolSurfaceChars,
			DeferToolSurfaceDefaultDirectTools: boolPtr(
				opts.DeferToolSurfaceDefaultDirectTools,
			),
			DeferToolSurfaceDirectTools: splitCSV(
				opts.DeferToolSurfaceDirect,
			),
			DynamicAgentTimeout: opts.DynamicAgentTimeout,
		}
		cwd, _ := os.Getwd()
		skillsProv = newScopedSkillRepositoryProvider(cwd, agentCfg)
		agentCfg.SkillRepositoryProvider = skillsProv
		ag, skillsRepo, err = newAgent(
			mdl,
			agentCfg,
			extraTools,
			toolSets,
		)
		if err == nil {
			skillsWatch = newSkillsWatchService(
				cwd,
				agentCfg,
				skillsRepo,
			)
		}
	}
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create agent failed: %w", err),
		}
	}
	promptController := newRuntimePromptController(
		ag,
		prompts.Instruction,
		prompts.SystemPrompt,
	)

	bridgedSessionSvc := conversationscope.WrapSessionService(sessionSvc)
	runnerOpts := []runner.Option{
		runner.WithSessionService(bridgedSessionSvc),
		runner.WithPlugins(conversation.Plugin{}),
		runner.WithAwaitUserReplyRouting(true),
	}
	runnerOpts = appendMemoryServiceRunnerOption(runnerOpts, memSvc)
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
	var evoSvc evolution.Service
	defer func() {
		if evoSvc != nil {
			_ = evoSvc.Close()
		}
	}()
	evoSvc = maybeCreateEvolutionService(
		opts,
		skillsRepo,
		skillsProv,
	)
	if evoSvc != nil {
		runnerOpts = append(runnerOpts, runner.WithEvolutionService(evoSvc))
	}
	r := runner.NewRunner(opts.AppName, ag, runnerOpts...)

	runtimeProfileResolver, runtimeProfileCatalog, runtimeProfileRequired :=
		runtimeProfileResolverFromOptions(
			opts.RuntimeProfiles,
			runtimeOpts,
		)

	gwOpts := makeGatewayOptions(
		splitCSV(opts.AllowUsers),
		opts.RequireMention,
		mentionPatterns,
	)
	gwOpts = append(gwOpts, gateway.WithAppName(opts.AppName))
	gwOpts = append(gwOpts, gateway.WithUploadStore(stores.uploads))
	gwOpts = append(gwOpts, gateway.WithPersonaStore(stores.personas))
	if fileMemoryStore != nil {
		gwOpts = append(
			gwOpts,
			gateway.WithMemoryFileStore(fileMemoryStore),
		)
	}
	if debugRec != nil {
		gwOpts = append(gwOpts, gateway.WithDebugRecorder(debugRec))
	}
	gwOpts = appendRuntimeProfileGatewayOption(
		gwOpts,
		runtimeProfileResolver,
		runtimeProfileRequired,
	)
	if langfuseRT != nil && langfuseRT.runOptionResolver != nil {
		gwOpts = append(
			gwOpts,
			gateway.WithRunOptionResolver(
				langfuseRT.runOptionResolver,
			),
		)
	}
	gwOpts = appendLatencyDiagnosticsGatewayOption(
		gwOpts,
		opts.StateDir,
		opts.LatencyDiagnosticsEnabled,
		opts.LatencyDiagnosticsEvents,
	)
	gwOpts = appendSkillsOverviewGatewayOption(
		gwOpts,
		opts.SkillsOverviewLimit,
		splitCSV(opts.SkillsOverviewPinned),
	)
	gwOpts = append(
		gwOpts,
		gateway.WithRunOptionResolver(
			buildDeliveryRunOptionResolver(),
		),
		gateway.WithRunOptionResolver(
			buildConversationRunOptionResolver(
				opts.AppName,
				bridgedSessionSvc,
				conversation.HistoryOptions{
					AddSessionSummary: opts.AddSessionSummary,
					MaxHistoryRuns:    opts.MaxHistoryRuns,
				},
			),
		),
	)
	gwOpts = appendRuntimeGatewayRunOptions(gwOpts, runtimeOpts)
	gwSrv, err := gateway.New(r, gwOpts...)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create gateway failed: %w", err),
		}
	}

	gw := newInProcGatewayClient(
		gwSrv,
		opts.AppName,
		sessionSvc,
		memSvc,
		debugDir,
		stores.uploads,
	)
	gw.SetPersonaStore(stores.personas)
	gw.SetMemoryFileStore(fileMemoryStore)
	gw.SetRuntimeProfileAppNames(runtimeProfileAppNames(opts.RuntimeProfiles))
	gw.SetRuntimeProfileCatalog(runtimeProfileCatalog)

	a2aSurface, err := newA2ASurface(ag, r, opts)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("create a2a failed: %w", err),
		}
	}

	httpHandler, err := buildRuntimeHTTPHandler(
		gwSrv.Handler(),
		a2aSurface,
	)
	if err != nil {
		return &exitError{
			Code: 1,
			Err:  fmt.Errorf("build runtime handler failed: %w", err),
		}
	}

	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	httpSrv := &http.Server{
		Addr:              opts.HTTPAddr,
		Handler:           httpHandler,
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
		cronSvc     *cron.Service
		cronRunner  runner.Runner
		subagentSvc *subagentrun.Service
	)
	var cleanupSubagent func()
	defer func() {
		if cleanupSubagent != nil {
			cleanupSubagent()
		}
	}()
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
		cronOpts := runtimeProfileCronOptions(runtimeProfileResolver)
		if debugRec != nil {
			cronOpts = append(
				cronOpts,
				cron.WithDebugRecorder(debugRec),
			)
		}
		cronSvc, err = cron.NewService(
			resolvedStateDir,
			cronRunner,
			openClawTools.router,
			cronOpts...,
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

		subagentSvc, err = subagentrun.NewService(
			resolvedStateDir,
			r,
			openClawTools.router,
		)
		if err != nil {
			if cronSvc != nil {
				_ = cronSvc.Close()
			}
			if cronRunner != nil {
				_ = cronRunner.Close()
			}
			return &exitError{
				Code: 1,
				Err:  fmt.Errorf("create subagent service failed: %w", err),
			}
		}
		openClawTools.subagentTools.SetService(subagentSvc)
		subagentSvc.Start(runCtx)
		cleanupSubagent = func() {
			openClawTools.subagentTools.SetService(nil)
			if subagentSvc != nil {
				_ = subagentSvc.Close()
				subagentSvc = nil
			}
		}
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
		adminSvc := admin.New(
			buildAdminConfig(
				opts,
				agentType,
				instanceID,
				langfuseStatus,
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
				promptController,
				browserServerSup,
				adminBinding.addr,
				adminBinding.url,
				skillsRepo,
				skillsWatch,
				fileMemoryStore,
				bridgedSessionSvc,
			),
			buildAdminOptions(opts)...,
		)
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

	logStartupLines(runtimeStartupLines(
		opts,
		resolvedStateDir,
		channels,
		needsModel,
	))
	logStartupLines(browserServerSup.startupLines())
	logStartupLines(gatewayStartupLines(httpSrv.Addr, gwSrv))
	logStartupLines(a2aStartupLines(a2aSurface))
	logStartupLines(toolDepsStartupLines(openClawTools.deps))
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
	if subagentSvc != nil {
		openClawTools.subagentTools.SetService(nil)
		cleanupSubagent = nil
		_ = subagentSvc.Close()
		subagentSvc = nil
	}
	if cronRunner != nil {
		_ = cronRunner.Close()
	}
	if browserServerSup != nil {
		if err := browserServerSup.Close(); err != nil {
			log.Warnf("close browser server failed: %v", err)
		}
	}
	_ = r.Close()
	if err := shutdownTelemetryWithContext(
		shutdownCtx,
		langfuseShutdown,
	); err != nil {
		log.Warnf("shutdown langfuse failed: %v", err)
	}
	langfuseShutdown = nil

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

func memoryServiceTools(svc memory.Service) []tool.Tool {
	if svc == nil {
		return nil
	}
	return append([]tool.Tool(nil), svc.Tools()...)
}

func fileMemoryStoreForBackend(
	backend string,
	store *memoryfile.Store,
) *memoryfile.Store {
	if resolveMemoryBackendType(backend) != memoryBackendFile {
		return nil
	}
	return store
}

func appendMemoryServiceRunnerOption(
	opts []runner.Option,
	svc memory.Service,
) []runner.Option {
	if svc == nil {
		return opts
	}
	return append(opts, runner.WithMemoryService(svc))
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

func shutdownTelemetry(
	shutdown func(context.Context) error,
) error {
	if shutdown == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()
	return shutdownTelemetryWithContext(ctx, shutdown)
}

func shutdownTelemetryWithContext(
	ctx context.Context,
	shutdown func(context.Context) error,
) error {
	if shutdown == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return shutdown(ctx)
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
	}
	opts = appendMemoryServiceRunnerOption(opts, memSvc)
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
	if opts.MaxToolIterations != 0 {
		return errors.New(
			"claude-code agent does not support max-tool-iterations",
		)
	}
	if opts.MaxLLMCalls != 0 {
		return errors.New(
			"claude-code agent does not support max-llm-calls",
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
	if opts.CodeExecutor.Type != "" {
		return errors.New(
			"claude-code agent does not support tools.code_executor",
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
	if len(opts.KnowledgesConfig) > 0 {
		return errors.New(
			"claude-code agent does not support knowledges",
		)
	}
	if opts.RefreshToolSetsOnRun {
		return errors.New(
			"claude-code agent does not support refresh-toolsets-on-run",
		)
	}
	deferMode, _ := normalizeDeferToolSurfaceMode(opts.DeferToolSurfaceMode)
	if opts.DeferToolSurface {
		deferMode = deferToolSurfaceModeOn
	}
	if deferMode == deferToolSurfaceModeAuto &&
		!opts.deferToolSurfaceModeExplicit &&
		!opts.DeferToolSurface {
		deferMode = deferToolSurfaceModeOff
	}
	if deferMode != deferToolSurfaceModeOff {
		return errors.New(
			"claude-code agent does not support defer-tools-to-dynamic-agent",
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
) (agent.Agent, *ocskills.Repository, error) {
	baseInstruction := strings.TrimSpace(cfg.Instruction)
	if baseInstruction == "" {
		baseInstruction = defaultAgentInstruction
	}
	knowledgeTools, err := buildKnowledgeTools(
		cfg.KnowledgesConfig,
	)
	if err != nil {
		return nil, nil, err
	}
	cwd, _ := os.Getwd()
	roots := resolveSkillRoots(cwd, cfg)
	bundledRoot := resolveBundledSkillsRoot(cwd, cfg.StateDir)
	repoOptions := []ocskills.Option{
		ocskills.WithDebug(cfg.SkillsDebug),
		ocskills.WithConfigKeys(cfg.SkillConfigKeys),
		ocskills.WithBundledSkillsRoot(bundledRoot),
		ocskills.WithAllowBundled(cfg.SkillsAllowBundled),
		ocskills.WithSkillConfigs(cfg.SkillConfigs),
	}
	if cfg.SkillsSummaryCacheTTL > 0 {
		repoOptions = append(
			repoOptions,
			ocskills.WithSummaryCacheDirtyCheckTTL(
				cfg.SkillsSummaryCacheTTL,
			),
		)
	}
	repo, err := ocskills.NewRepository(roots, repoOptions...)
	if err != nil {
		return nil, nil, err
	}
	repoProvider := cfg.SkillRepositoryProvider
	if repoProvider == nil {
		repoProvider = newScopedSkillRepositoryProvider(cwd, cfg)
	}

	tools := append([]tool.Tool(nil), extraTools...)
	if knowledgeTools != nil && len(knowledgeTools.tools) > 0 {
		tools = append(tools, knowledgeTools.tools...)
	}
	tools = append(
		tools,
		newScopedSkillListTool(
			repo,
			repoProvider,
			cfg.EvolutionSkillScopeMode,
		),
	)
	if len(cfg.ToolProviders) > 0 {
		extra, err := toolsFromProviders(
			mdl,
			cfg.AppName,
			cfg.StateDir,
			cfg.ToolProviders,
		)
		if err != nil {
			return nil, nil, err
		}
		tools = append(tools, extra...)
	}

	deferToolSurface, directTools, err := resolveDeferredToolSurface(
		cfg,
		tools,
		toolSets,
	)
	if err != nil {
		return nil, nil, err
	}
	instruction := baseInstruction
	childInstruction := baseInstruction
	if deferToolSurface {
		instruction = joinPromptParts(
			instruction,
			openClawDeferredToolingGuidance,
		)
	} else if cfg.EnableOpenClawTools {
		instruction = joinPromptParts(
			instruction,
			buildOpenClawToolingGuidance(cfg),
		)
	}
	if cfg.EnableOpenClawTools {
		childInstruction = joinPromptParts(
			childInstruction,
			buildOpenClawToolingGuidance(cfg),
		)
	}
	if hasToolNamed(tools, ocbrowser.ToolName) {
		if deferToolSurface {
			childInstruction = joinPromptParts(
				childInstruction,
				browserToolingGuidance,
			)
		} else {
			instruction = joinPromptParts(
				instruction,
				browserToolingGuidance,
			)
		}
	}
	genConfig := model.GenerationConfig{Stream: true}
	if cfg.GenerationConfig != nil {
		genConfig = *cfg.GenerationConfig
	}

	callbacks := tool.NewCallbacks()
	registerMemoryFileToolCallback(
		callbacks,
		cfg.MemoryFileStore,
		cfg.StateDir,
	)
	callbacks.RegisterToolResultMessages(openClawToolResultMessages)

	exec := cfg.codeExecutor
	if exec == nil {
		var err error
		exec, err = codeExecutorFromConfig(
			cfg.StateDir,
			cfg.EnableLocalExec,
			cfg.CodeExecutor,
		)
		if err != nil {
			return nil, nil, err
		}
	}

	opts := baseLLMAgentOptions(
		mdl,
		cfg,
		instruction,
		strings.TrimSpace(cfg.SystemPrompt),
		genConfig,
		repo,
	)
	if cfg.PostToolPromptEnabled != nil {
		opts = append(
			opts,
			llmagent.WithEnablePostToolPrompt(
				*cfg.PostToolPromptEnabled,
			),
		)
	}
	if deferToolSurface {
		searchTool := newDeferredCapabilitySearchTool(
			deferredToolSurfaceConfig{
				Model:         mdl,
				Config:        cfg,
				Instruction:   childInstruction,
				SystemPrompt:  strings.TrimSpace(cfg.SystemPrompt),
				Generation:    genConfig,
				Repository:    repo,
				RepoProvider:  repoProvider,
				Tools:         tools,
				ToolSets:      toolSets,
				CodeExecutor:  exec,
				ToolCallbacks: callbacks,
			})
		dynamicTool := newDeferredToolSurfaceTool(deferredToolSurfaceConfig{
			Model:         mdl,
			Config:        cfg,
			Instruction:   childInstruction,
			SystemPrompt:  strings.TrimSpace(cfg.SystemPrompt),
			Generation:    genConfig,
			Repository:    repo,
			RepoProvider:  repoProvider,
			Tools:         tools,
			ToolSets:      toolSets,
			CodeExecutor:  exec,
			ToolCallbacks: callbacks,
		})
		parentTools := []tool.Tool{searchTool, dynamicTool}
		parentTools = append(parentTools, directTools...)
		opts = append(opts, llmagent.WithTools(parentTools))
	} else {
		opts = appendOpenClawSkillOptions(opts, cfg, repo, repoProvider)
		if len(tools) > 0 {
			opts = append(opts, llmagent.WithTools(tools))
		}
		if len(toolSets) > 0 {
			opts = append(opts, llmagent.WithToolSets(toolSets))
		}
		if cfg.RefreshToolSetsOnRun {
			opts = append(opts, llmagent.WithRefreshToolSetsOnRun(true))
		}
		opts = appendCodeExecutionOptions(opts, exec, cfg.CodeExecutor)
	}
	opts = append(opts, llmagent.WithToolCallbacks(callbacks))

	return llmagent.New(defaultAgentName, opts...), repo, nil
}

func codeExecutorFromConfig(
	stateDir string,
	enableLocal bool,
	cfg codeExecutorOptions,
) (codeexecutor.CodeExecutor, error) {
	typeName := strings.ToLower(strings.TrimSpace(cfg.Type))
	if typeName == "" {
		if enableLocal {
			return localexec.New(), nil
		}
		return nil, nil
	}
	switch typeName {
	case codeExecutorTypeSandbox:
		return sandboxCodeExecutorFromConfig(stateDir, cfg.Sandbox), nil
	default:
		return nil, fmt.Errorf("unsupported code executor type: %s", typeName)
	}
}

func isSandboxCodeExecutor(cfg codeExecutorOptions) bool {
	return strings.ToLower(strings.TrimSpace(cfg.Type)) == codeExecutorTypeSandbox
}

func codeExecutorEngine(exec codeexecutor.CodeExecutor) codeexecutor.Engine {
	provider, ok := exec.(codeexecutor.EngineProvider)
	if !ok || provider == nil {
		return nil
	}
	return provider.Engine()
}

func codeExecutorLoader(
	runtimeOpts runtimeOptions,
) codeExecutorConfigLoader {
	if runtimeOpts.codeExecutorLoader != nil {
		return runtimeOpts.codeExecutorLoader
	}
	return codeExecutorFromConfig
}

func sandboxCodeExecutorFromConfig(
	stateDir string,
	cfg sandboxCodeExecutorOptions,
) codeexecutor.CodeExecutor {
	root := strings.TrimSpace(cfg.WorkspaceRoot)
	if root == "" {
		root = filepath.Join(stateDir, "sandbox")
	}
	profile := sandboxPermissionProfileFromConfig(cfg)
	return sandboxexec.New(
		sandboxexec.WithWorkspaceRoot(root),
		sandboxexec.WithBackend(sandboxBackendFromConfig(cfg.Backend)),
		sandboxexec.WithPermissionProfile(profile),
		sandboxexec.WithShellEnvironmentPolicy(
			sandboxShellEnvPolicyFromConfig(cfg.ShellEnv),
		),
		sandboxexec.WithDefaultTimeout(cfg.DefaultTimeout),
		sandboxexec.WithOutputMaxBytes(cfg.OutputMaxBytes),
	)
}

func sandboxBackendFromConfig(raw string) sandboxexec.BackendType {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case sandboxBackendLinuxBubblewrap:
		return sandboxexec.BackendLinuxBubblewrap
	default:
		return sandboxexec.BackendAuto
	}
}

func sandboxPermissionProfileFromConfig(
	cfg sandboxCodeExecutorOptions,
) sandboxexec.PermissionProfile {
	var profile sandboxexec.PermissionProfile
	switch strings.ToLower(strings.TrimSpace(cfg.Profile)) {
	case sandboxProfileReadOnly:
		profile = sandboxexec.ReadOnlyProfile()
	case sandboxProfileDisabled:
		return sandboxexec.DangerFullAccessProfile()
	default:
		profile = sandboxexec.WorkspaceWriteProfile()
	}
	switch strings.ToLower(strings.TrimSpace(cfg.Network)) {
	case sandboxNetworkEnabled:
		profile = profile.WithNetworkPolicy(sandboxexec.NetworkPolicy{
			Mode: sandboxexec.NetworkEnabled,
		})
	default:
		profile = profile.WithNetworkPolicy(sandboxexec.NetworkPolicy{
			Mode: sandboxexec.NetworkRestricted,
		})
	}
	return profile
}

func sandboxShellEnvPolicyFromConfig(
	cfg sandboxShellEnvOptions,
) sandboxexec.ShellEnvironmentPolicy {
	return sandboxexec.ShellEnvironmentPolicy{
		Inherit:              sandboxShellEnvInheritFromConfig(cfg.Inherit),
		ApplyDefaultExcludes: cfg.ApplyDefaultExcludes,
		Exclude:              append([]string(nil), cfg.Exclude...),
		Set:                  copyStringMap(cfg.Set),
		IncludeOnly:          append([]string(nil), cfg.IncludeOnly...),
	}
}

func sandboxShellEnvInheritFromConfig(
	raw string,
) sandboxexec.ShellEnvironmentPolicyInherit {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case sandboxShellEnvInheritAll:
		return sandboxexec.ShellEnvironmentPolicyInheritAll
	case sandboxShellEnvInheritNone:
		return sandboxexec.ShellEnvironmentPolicyInheritNone
	default:
		return sandboxexec.ShellEnvironmentPolicyInheritCore
	}
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func buildOpenClawToolingGuidance(cfg agentConfig) string {
	if cfg.OpenClawToolingGuide != nil {
		return strings.TrimSpace(*cfg.OpenClawToolingGuide)
	}
	guidance := strings.TrimSpace(openClawToolingGuidance)
	if !isSandboxCodeExecutor(cfg.CodeExecutor) {
		return guidance
	}
	guidance = strings.Replace(
		guidance,
		"For other general local shell work, use exec_command. For interactive follow-up "+
			"input, use write_stdin and kill_session when needed. Use message "+
			"to send to the current chat or an explicit target. ",
		"For other general local shell work, use exec_command. In sandbox mode, "+
			"exec_command only supports foreground non-interactive commands; "+
			"write_stdin, kill_session, background execution, TTY allocation, "+
			"and session continuation are unavailable. Use message to send to "+
			"the current chat or an explicit target. ",
		1,
	)
	guidance = strings.Replace(
		guidance,
		"Chat uploads are saved to stable host paths. For host "+
			"commands, prefer OPENCLAW_LAST_UPLOAD_PATH or "+
			"OPENCLAW_SESSION_UPLOADS_DIR, OPENCLAW_LAST_UPLOAD_HOST_REF, "+
			"OPENCLAW_LAST_UPLOAD_NAME, "+
			"OPENCLAW_LAST_UPLOAD_MIME, OPENCLAW_MEMORY_FILE, "+
			"OPENCLAW_USER_MEMORY_FILE, OPENCLAW_CHAT_MEMORY_FILE, and "+
			"OPENCLAW_RECENT_UPLOADS_JSON instead of guessing "+
			"attachment paths. ",
		"Chat uploads still provide stable OPENCLAW_* metadata, but sandbox "+
			"exec_command does not automatically mount host paths such as "+
			"OPENCLAW_LAST_UPLOAD_PATH, OPENCLAW_SESSION_UPLOADS_DIR, "+
			"OPENCLAW_MEMORY_FILE, OPENCLAW_USER_MEMORY_FILE, or "+
			"OPENCLAW_CHAT_MEMORY_FILE. Use that metadata for filenames and "+
			"host refs, and avoid assuming those host paths are directly "+
			"readable inside the sandbox. ",
		1,
	)
	guidance = strings.Replace(
		guidance,
		"When exec_command or write_stdin generates images",
		"When exec_command generates images",
		1,
	)
	return guidance
}

func buildOpenClawSkillsGuidance(cfg agentConfig) string {
	if cfg.SkillsToolingGuide != nil {
		return strings.TrimSpace(*cfg.SkillsToolingGuide)
	}
	guidance := strings.TrimSpace(openClawSkillsGuidance)
	if cfg.SkillsOverviewLimit > 0 {
		guidance = strings.Replace(
			guidance,
			openClawSkillsPathGuidance,
			openClawCompactSkillsGuidance,
			1,
		)
	}
	return guidance
}

func hasToolNamed(tools []tool.Tool, name string) bool {
	for i := range tools {
		decl := tools[i].Declaration()
		if decl == nil {
			continue
		}
		if decl.Name == name {
			return true
		}
	}
	return false
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

	AddSessionSummary                             bool
	EnableContextCompaction                       bool
	ContextCompactionOversizedToolResultMaxTokens int
	MaxHistoryRuns                                int
	MaxLLMCalls                                   int
	MaxToolIterations                             int
	PreloadMemory                                 int
	GenerationConfig                              *model.GenerationConfig
	PostToolPromptEnabled                         *bool
	Instruction                                   string
	SystemPrompt                                  string

	SkillsRoot              string
	SkillsExtraDirs         []string
	SkillsDebug             bool
	SkillsAllowBundled      []string
	SkillConfigs            map[string]ocskills.SkillConfig
	SkillConfigKeys         []string
	SkillsWatch             bool
	SkillsWatchBundled      bool
	SkillsWatchDebounce     time.Duration
	SkillsSummaryCacheTTL   time.Duration
	SkillsOverviewLimit     int
	SkillsOverviewPinned    []string
	SkillsToolProfile       string
	SkillsLoadMode          string
	SkillsMaxLoaded         int
	SkillsToolResults       bool
	SkillsSkipFallback      bool
	SkillsToolingGuide      *string
	KnowledgesConfig        []knowledgeEntry
	EvolutionSkillScopeMode skill.SkillScopeMode
	SkillRepositoryProvider skill.RepositoryProvider

	StateDir string

	MemoryFileStore *memoryfile.Store

	EnableLocalExec bool
	CodeExecutor    codeExecutorOptions
	codeExecutor    codeexecutor.CodeExecutor

	EnableOpenClawTools  bool
	OpenClawToolingGuide *string
	EnableParallelTools  bool

	ToolProviders []pluginSpec

	ToolSets []pluginSpec

	RefreshToolSetsOnRun               bool
	DeferToolSurface                   bool
	DeferToolSurfaceMode               string
	DeferToolSurfaceThresholdChars     int
	DeferToolSurfaceDefaultDirectTools *bool
	DeferToolSurfaceDirectTools        []string
	DynamicAgentTimeout                time.Duration
}

func resolvePostToolPromptEnabled(
	opts runOptions,
	runtimeOpts runtimeOptions,
) *bool {
	if runtimeOpts.postToolPromptEnabled != nil {
		return runtimeOpts.postToolPromptEnabled
	}
	return opts.PostToolPromptEnabled
}

type openClawToolsBundle struct {
	tools         []tool.Tool
	execMgr       *octool.Manager
	router        *outbound.Router
	cronTool      *cron.Tool
	subagentTools subagentrun.Tools
	deps          *deps.Report
}

type runtimeStores struct {
	uploads     *uploads.Store
	personas    *persona.Store
	memoryFiles *memoryfile.Store
}

func newRuntimeStores(stateDir string) (runtimeStores, error) {
	uploadStore, err := uploads.NewStore(stateDir)
	if err != nil {
		return runtimeStores{}, fmt.Errorf("create upload store: %w", err)
	}

	personaPath, err := persona.DefaultStorePath(stateDir)
	if err != nil {
		return runtimeStores{}, fmt.Errorf(
			"create persona store path: %w",
			err,
		)
	}
	personaStore, err := persona.NewStore(personaPath)
	if err != nil {
		return runtimeStores{}, fmt.Errorf("create persona store: %w", err)
	}

	memoryRoot, err := memoryfile.DefaultRoot(stateDir)
	if err != nil {
		return runtimeStores{}, fmt.Errorf(
			"create memory root: %w",
			err,
		)
	}
	memoryStore, err := memoryfile.NewStore(memoryRoot)
	if err != nil {
		return runtimeStores{}, fmt.Errorf(
			"create memory store: %w",
			err,
		)
	}

	return runtimeStores{
		uploads:     uploadStore,
		personas:    personaStore,
		memoryFiles: memoryStore,
	}, nil
}

func buildOpenClawTools(
	enabled bool,
	stateDir string,
	uploadStore *uploads.Store,
	memoryFileStore *memoryfile.Store,
	sandboxExecEngine codeexecutor.Engine,
	hostExecDefaultTimeout time.Duration,
) openClawToolsBundle {
	if !enabled {
		return openClawToolsBundle{}
	}

	router := outbound.NewRouter()
	cronTool := cron.NewTool(nil)
	subagentTools := subagentrun.NewTools(nil)
	var depsReport *deps.Report
	if sources, err := deps.SourcesForProfiles(deps.DefaultProfiles()); err ==
		nil {
		report, err := deps.InspectStartup(stateDir, sources)
		if err == nil {
			depsReport = &report
		}
	}

	var mgr *octool.Manager
	var execTool tool.Tool
	commandPolicy := octool.NewChatCommandSafetyPolicy()
	outputRedactor := octool.NewChatCommandOutputRedactor()
	if sandboxExecEngine != nil {
		execTool = octool.NewSandboxExecCommandToolWithPolicy(
			sandboxExecEngine,
			uploadStore,
			memoryFileStore,
			commandPolicy,
			outputRedactor,
		)
	} else {
		mgrOpts := []octool.Option{
			octool.WithBaseEnv(deps.ToolEnv(stateDir)),
			octool.WithCommandPolicy(commandPolicy),
			octool.WithOutputRedactor(outputRedactor),
		}
		if hostExecDefaultTimeout > 0 {
			mgrOpts = append(
				mgrOpts,
				octool.WithDefaultTimeout(hostExecDefaultTimeout),
			)
		}
		mgr = octool.NewManager(mgrOpts...)
		execTool = octool.NewExecCommandTool(mgr, uploadStore)
		if memoryFileStore != nil {
			execTool = octool.NewExecCommandToolWithMemoryFileStore(
				mgr,
				uploadStore,
				memoryFileStore,
			)
		}
	}
	tools := []tool.Tool{
		conversationtool.NewTool(),
		octool.NewReadDocumentTool(uploadStore),
		octool.NewReadSpreadsheetTool(uploadStore),
		execTool,
		outbound.NewTool(router),
		cronTool,
	}
	if mgr != nil {
		tools = append(
			tools,
			octool.NewWriteStdinTool(mgr),
			octool.NewKillSessionTool(mgr),
		)
	}
	tools = append(tools, subagentTools.All()...)
	return openClawToolsBundle{
		tools:         tools,
		execMgr:       mgr,
		router:        router,
		cronTool:      cronTool,
		subagentTools: subagentTools,
		deps:          depsReport,
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
	bundledSkills := resolveBundledSkillsRoot(cwd, cfg.StateDir)

	roots := make([]string, 0, 6+len(cfg.SkillsExtraDirs))
	roots = append(roots, workspaceSkills)
	roots = append(roots, projectAgentsSkills)
	roots = append(roots, personalAgentsSkills)
	roots = append(roots, managedSkills)
	if bundledSkills != workspaceSkills &&
		bundledSkills != managedSkills {
		roots = append(roots, bundledSkills)
	}
	roots = append(roots, cfg.SkillsExtraDirs...)
	return roots
}

func newSkillsWatchService(
	cwd string,
	cfg agentConfig,
	repo *ocskills.Repository,
) *ocskills.WatchService {
	if repo == nil {
		return nil
	}

	return ocskills.NewWatchService(
		repo,
		resolveSkillRoots(cwd, cfg),
		ocskills.WatchConfig{
			Enabled:      cfg.SkillsWatch,
			Debounce:     cfg.SkillsWatchDebounce,
			WatchBundled: cfg.SkillsWatchBundled,
			BundledRoot: resolveBundledSkillsRoot(
				cwd,
				cfg.StateDir,
			),
		},
	)
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
	return cwdSkills
}

func dirExists(path string) bool {
	st, err := os.Stat(path)
	if err != nil {
		return false
	}
	return st.IsDir()
}

func resolveBundledSkillsRoot(cwd, stateDir string) string {
	installedBundled := filepath.Join(
		stateDir,
		defaultBundledSkillsDir,
	)
	if dirExists(installedBundled) {
		return installedBundled
	}

	repoBundled := filepath.Join(cwd, appName, defaultSkillsDir)
	if dirExists(repoBundled) {
		return repoBundled
	}
	if strings.TrimSpace(stateDir) != "" {
		return installedBundled
	}
	return repoBundled
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
	return filepath.Join(home, ".trpc-agent-go-github", appName), nil
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

func recordDebugOpenAIChatRequestJSON(
	ctx context.Context,
	raw []byte,
	marshalErr error,
) {
	if debugrecorder.TraceFromContext(ctx) == nil {
		return
	}

	err := marshalErr
	if err == nil && len(raw) > 0 {
		var payload any
		err = json.Unmarshal(raw, &payload)
		if err == nil {
			err = debugrecorder.RecordModelRequest(
				ctx,
				debugrecorder.ProviderOpenAIChatCompletions,
				payload,
			)
		}
	}
	if err != nil {
		log.Warnf(
			"debug recorder failed to capture chat request: %v",
			err,
		)
	}
}

func newOpenAIModel(spec registry.ModelSpec) (model.Model, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return nil, errors.New("openai model name is empty")
	}

	baseURL := strings.TrimSpace(spec.BaseURL)
	variant, err := parseOpenAIVariant(spec.OpenAIVariant, baseURL)
	if err != nil {
		return nil, err
	}

	opts := []openai.Option{
		openai.WithVariant(variant),
		openai.WithOmitFileContentParts(true),
	}
	if spec.DebugRecorderEnabled {
		opts = append(
			opts,
			openai.WithChatRequestJSONCallback(
				recordDebugOpenAIChatRequestJSON,
			),
		)
	}
	if baseURL != "" {
		opts = append(opts, openai.WithBaseURL(baseURL))
	}
	if apiKey := strings.TrimSpace(spec.APIKey); apiKey != "" {
		opts = append(opts, openai.WithAPIKey(apiKey))
	}
	if len(spec.Headers) > 0 {
		opts = append(opts, openai.WithHeaders(spec.Headers))
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
	var headers map[string]string
	if mode == modeOpenAI {
		resolved, err := resolveOpenAIHeaders(opts.OpenAIHeaders)
		if err != nil {
			return nil, err
		}
		headers = resolved
	}

	spec := registry.ModelSpec{
		Type:                 mode,
		Name:                 opts.OpenAIModel,
		BaseURL:              baseURL,
		APIKey:               strings.TrimSpace(os.Getenv(openAIAPIKeyEnvName)),
		OpenAIVariant:        opts.OpenAIVariant,
		Headers:              headers,
		DebugRecorderEnabled: opts.DebugRecorderEnabled,
		Config:               opts.ModelConfig,
	}
	return f(spec)
}

func resolveOpenAIHeaders(
	config map[string]string,
) (map[string]string, error) {
	headers := cleanHeaderMap(config)
	envHeaders, err := parseHeaderPairs(os.Getenv(openAIHeadersEnvName))
	if err != nil {
		return nil, err
	}
	if len(envHeaders) == 0 {
		return headers, nil
	}
	if headers == nil {
		headers = map[string]string{}
	}
	for key, value := range envHeaders {
		headers[key] = value
	}
	return headers, nil
}

func cleanHeaderMap(headers map[string]string) map[string]string {
	if len(headers) == 0 {
		return nil
	}
	cleaned := make(map[string]string, len(headers))
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		cleaned[key] = value
	}
	if len(cleaned) == 0 {
		return nil
	}
	return cleaned
}

func parseHeaderPairs(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	fields, err := splitHeaderFields(raw)
	if err != nil {
		return nil, fmt.Errorf("invalid %s: %w", openAIHeadersEnvName, err)
	}
	headers := make(map[string]string, len(fields))
	for _, field := range fields {
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			key, value, ok = strings.Cut(field, ":")
		}
		if !ok {
			return nil, fmt.Errorf(
				"invalid %s entry %q: want KEY=VALUE",
				openAIHeadersEnvName,
				field,
			)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			return nil, fmt.Errorf(
				"invalid %s entry %q: empty key or value",
				openAIHeadersEnvName,
				field,
			)
		}
		headers[key] = value
	}
	return headers, nil
}

func splitHeaderFields(raw string) ([]string, error) {
	var fields []string
	var field strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if s := strings.TrimSpace(field.String()); s != "" {
			fields = append(fields, s)
		}
		field.Reset()
	}

	for _, r := range raw {
		if escaped {
			field.WriteRune(r)
			escaped = false
			continue
		}
		if quote != 0 {
			if quote == '"' && r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
				continue
			}
			field.WriteRune(r)
			continue
		}

		switch {
		case r == '"' || r == '\'':
			quote = r
		case r == ',' || unicode.IsSpace(r):
			flush()
		default:
			field.WriteRune(r)
		}
	}
	if escaped {
		return nil, errors.New("unterminated escape sequence")
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated %q quote", string(quote))
	}
	flush()
	return fields, nil
}

func parseOpenAIVariant(
	raw string,
	baseURL string,
) (openai.Variant, error) {
	v := strings.ToLower(strings.TrimSpace(raw))
	if v == "" || v == openAIVariantAuto {
		return inferOpenAIVariant(baseURL), nil
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

func inferOpenAIVariant(baseURL string) openai.Variant {
	host, ok := openAIBaseURLHost(baseURL)
	if !ok {
		return openai.VariantOpenAI
	}
	switch {
	case strings.EqualFold(host, deepSeekAPIHost):
		return openai.VariantDeepSeek
	case strings.EqualFold(host, qwenAPIHost):
		return openai.VariantQwen
	case strings.EqualFold(host, hunyuanAPIHost):
		return openai.VariantHunyuan
	default:
		return openai.VariantOpenAI
	}
}

func openAIBaseURLHost(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", false
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return "", false
	}
	return host, true
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
