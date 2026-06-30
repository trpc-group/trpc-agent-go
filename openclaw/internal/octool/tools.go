//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package octool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspacesession"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/identity"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/conversationscope"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/memoryfile"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
	"trpc.group/trpc-go/trpc-agent-go/openclaw/runtimeprofile"
)

const (
	toolExecCommand = "exec_command"
	toolWriteStdin  = "write_stdin"
	toolKillSession = "kill_session"

	errExecToolNotConfigured  = "exec tool is not configured"
	errCommandRequired        = "command is required"
	errSandboxExecUnsupported = "sandbox exec_command only supports foreground non-interactive commands"
	errWriteToolNotConfigured = "write_stdin tool is not configured"
	errKillToolNotConfigured  = "kill_session tool is not configured"
	errSessionIDRequired      = "session id is required"

	envSessionUploadsDir = "OPENCLAW_SESSION_UPLOADS_DIR"
	envLastUploadPath    = "OPENCLAW_LAST_UPLOAD_PATH"
	envLastUploadHostRef = "OPENCLAW_LAST_UPLOAD_HOST_REF"
	envLastUploadName    = "OPENCLAW_LAST_UPLOAD_NAME"
	envLastUploadMIME    = "OPENCLAW_LAST_UPLOAD_MIME"
	envRecentUploadsJSON = "OPENCLAW_RECENT_UPLOADS_JSON"

	envLastImagePath    = "OPENCLAW_LAST_IMAGE_PATH"
	envLastImageHostRef = "OPENCLAW_LAST_IMAGE_HOST_REF"
	envLastImageName    = "OPENCLAW_LAST_IMAGE_NAME"
	envLastImageMIME    = "OPENCLAW_LAST_IMAGE_MIME"

	envLastAudioPath    = "OPENCLAW_LAST_AUDIO_PATH"
	envLastAudioHostRef = "OPENCLAW_LAST_AUDIO_HOST_REF"
	envLastAudioName    = "OPENCLAW_LAST_AUDIO_NAME"
	envLastAudioMIME    = "OPENCLAW_LAST_AUDIO_MIME"

	envLastVideoPath    = "OPENCLAW_LAST_VIDEO_PATH"
	envLastVideoHostRef = "OPENCLAW_LAST_VIDEO_HOST_REF"
	envLastVideoName    = "OPENCLAW_LAST_VIDEO_NAME"
	envLastVideoMIME    = "OPENCLAW_LAST_VIDEO_MIME"

	envLastPDFPath    = "OPENCLAW_LAST_PDF_PATH"
	envLastPDFHostRef = "OPENCLAW_LAST_PDF_HOST_REF"
	envLastPDFName    = "OPENCLAW_LAST_PDF_NAME"
	envLastPDFMIME    = "OPENCLAW_LAST_PDF_MIME"

	envMemoryFile     = "OPENCLAW_MEMORY_FILE"
	envUserMemoryFile = "OPENCLAW_USER_MEMORY_FILE"
	envChatMemoryFile = "OPENCLAW_CHAT_MEMORY_FILE"

	recentUploadsLimit = 6

	execOutputMediaMarker    = "MEDIA:"
	execOutputMediaDirMarker = "MEDIA_DIR:"
	maxExecOutputMarkers     = 16
)

const (
	uploadKindImage = "image"
	uploadKindAudio = "audio"
	uploadKindVideo = "video"
	uploadKindPDF   = "pdf"
	uploadKindFile  = "file"
)

type execUploadMeta struct {
	Name     string `json:"name,omitempty"`
	Path     string `json:"path,omitempty"`
	HostRef  string `json:"host_ref,omitempty"`
	MimeType string `json:"mime_type,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Source   string `json:"source,omitempty"`
}

type execTool struct {
	mgr         *Manager
	uploads     *uploads.Store
	memoryStore *memoryfile.Store
}

type sandboxExecTool struct {
	engine      codeexecutor.Engine
	uploads     *uploads.Store
	memoryStore *memoryfile.Store
	registry    *codeexecutor.WorkspaceRegistry
	policy      CommandPolicy
	redactor    OutputRedactor
}

// NewExecCommandTool creates the canonical host command tool.
func NewExecCommandTool(
	mgr *Manager,
	stores ...*uploads.Store,
) tool.Tool {
	var store *uploads.Store
	if len(stores) > 0 {
		store = stores[0]
	}
	return &execTool{
		mgr:     mgr,
		uploads: store,
	}
}

// NewSandboxExecCommandTool creates an exec_command tool backed by a sandbox
// code executor. The first version supports foreground non-interactive commands
// only; background, TTY, and session-continuation workflows are rejected in
// sandbox mode and do not fall back to host execution.
func NewSandboxExecCommandTool(
	engine codeexecutor.Engine,
	stores ...*uploads.Store,
) tool.Tool {
	var store *uploads.Store
	if len(stores) > 0 {
		store = stores[0]
	}
	return &sandboxExecTool{
		engine:   engine,
		uploads:  store,
		registry: codeexecutor.NewWorkspaceRegistry(),
	}
}

// NewSandboxExecCommandToolWithMemoryFileStore creates a sandbox-backed
// exec_command tool with memory-file environment metadata.
func NewSandboxExecCommandToolWithMemoryFileStore(
	engine codeexecutor.Engine,
	uploadStore *uploads.Store,
	memoryStore *memoryfile.Store,
) tool.Tool {
	return NewSandboxExecCommandToolWithPolicy(
		engine,
		uploadStore,
		memoryStore,
		nil,
		nil,
	)
}

// NewSandboxExecCommandToolWithPolicy creates a sandbox-backed exec_command
// tool with the same command policy and output redaction hooks as the host
// manager path.
func NewSandboxExecCommandToolWithPolicy(
	engine codeexecutor.Engine,
	uploadStore *uploads.Store,
	memoryStore *memoryfile.Store,
	policy CommandPolicy,
	redactor OutputRedactor,
) tool.Tool {
	return &sandboxExecTool{
		engine:      engine,
		uploads:     uploadStore,
		memoryStore: memoryStore,
		registry:    codeexecutor.NewWorkspaceRegistry(),
		policy:      policy,
		redactor:    redactor,
	}
}

// NewExecCommandToolWithMemoryFileStore creates the canonical host command
// tool with file-based memory environment injection.
func NewExecCommandToolWithMemoryFileStore(
	mgr *Manager,
	uploadStore *uploads.Store,
	memoryStore *memoryfile.Store,
) tool.Tool {
	return &execTool{
		mgr:         mgr,
		uploads:     uploadStore,
		memoryStore: memoryStore,
	}
}

func (t *execTool) Declaration() *tool.Declaration {
	return execCommandDeclaration(
		execToolDescription(t != nil && t.memoryStore != nil),
	)
}

func (t *sandboxExecTool) Declaration() *tool.Declaration {
	return execCommandDeclaration(
		sandboxExecToolDescription(t != nil && t.memoryStore != nil),
	)
}

func execCommandDeclaration(description string) *tool.Declaration {
	return &tool.Declaration{
		Name:        toolExecCommand,
		Description: description,
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"command"},
			Properties: map[string]*tool.Schema{
				"command": {
					Type: "string",
					Description: "Shell command to execute on " +
						"the current machine.",
				},
				"workdir": {
					Type:        "string",
					Description: "Optional working directory.",
				},
				"env": {
					Type: "object",
					Description: "Optional environment variable " +
						"overrides.",
				},
				"yield_time_ms": {
					Type: "number",
					Description: "How long to wait before " +
						"returning. 0 waits for exit when " +
						"possible.",
				},
				"background": {
					Type: "boolean",
					Description: "Run in the background " +
						"immediately.",
				},
				"timeout_sec": {
					Type: "number",
					Description: "Maximum command runtime in " +
						"seconds.",
				},
				"tty": {
					Type: "boolean",
					Description: "Allocate a TTY for interactive " +
						"commands.",
				},
				"yieldMs": {
					Type:        "number",
					Description: "Alias for yield_time_ms.",
				},
				"timeoutSec": {
					Type:        "number",
					Description: "Alias for timeout_sec.",
				},
				"pty": {
					Type:        "boolean",
					Description: "Alias for tty.",
				},
			},
		},
	}
}

func execToolDescription(hasMemoryFile bool) string {
	parts := []string{
		"Execute a host shell command. Use this for general local shell work.",
		"Interactive commands can continue with write_stdin.",
		"If you say you will run, inspect, create, write, or verify " +
			"something with host shell work, the same assistant " +
			"message must call exec_command or the required tool.",
		"Protected shell and credential paths may be blocked by policy.",
		"Sensitive env values may be redacted from returned output.",
		"Do not use this just to inspect a PDF or spreadsheet already " +
			"in chat; prefer read_document or read_spreadsheet for that.",
	}
	uploadText := "When a chat upload is available, " +
		"OPENCLAW_LAST_UPLOAD_PATH, OPENCLAW_LAST_UPLOAD_HOST_REF, " +
		"OPENCLAW_LAST_UPLOAD_NAME, OPENCLAW_LAST_UPLOAD_MIME, " +
		"kind-specific OPENCLAW_LAST_*_PATH vars, " +
		"OPENCLAW_SESSION_UPLOADS_DIR, and " +
		"OPENCLAW_RECENT_UPLOADS_JSON point to stable " +
		"attachment metadata, host refs, and host paths."
	if hasMemoryFile {
		uploadText = "When a chat upload is available, " +
			"OPENCLAW_LAST_UPLOAD_PATH, OPENCLAW_LAST_UPLOAD_HOST_REF, " +
			"OPENCLAW_LAST_UPLOAD_NAME, OPENCLAW_LAST_UPLOAD_MIME, " +
			"kind-specific OPENCLAW_LAST_*_PATH vars, " +
			"OPENCLAW_MEMORY_FILE, OPENCLAW_USER_MEMORY_FILE, " +
			"OPENCLAW_CHAT_MEMORY_FILE, OPENCLAW_SESSION_UPLOADS_DIR, " +
			"and OPENCLAW_RECENT_UPLOADS_JSON point to stable " +
			"attachment metadata, memory-file paths, host refs, " +
			"and host paths."
	}
	parts = append(
		parts,
		uploadText,
	)
	if hasMemoryFile {
		parts = append(
			parts,
			"OPENCLAW_MEMORY_FILE is a visible MEMORY.md file for the "+
				"current scope, and remains a compatibility alias. "+
				"OPENCLAW_USER_MEMORY_FILE is this user's personal "+
				"memory file. OPENCLAW_CHAT_MEMORY_FILE is the "+
				"current chat's shared memory file. These are visible "+
				"memory files, not hidden internal state. If the user "+
				"asks what you remember or asks to inspect memory, read "+
				"the relevant file and quote or summarize the relevant "+
				"lines.",
			"If the user explicitly says 'remember this' or asks you to "+
				"remember a durable fact, preference, task list, "+
				"checklist, or reminder list, "+
				"update the narrowest relevant memory file with a short "+
				"bullet.",
			"Use memory files only for stable, cross-session facts, "+
				"preferences, durable user tasks, checklists, and "+
				"reminder lists. Do not update memory files for "+
				"reusable task workflows, output formats, tool "+
				"procedures, or post-task feedback unless the user "+
				"explicitly asks to save that content as memory.",
		)
	}
	parts = append(
		parts,
		"Write derived outputs under OPENCLAW_SESSION_UPLOADS_DIR when "+
			"you plan to send them back to the user.",
		"If the command prints lines like `MEDIA: /path/to/file` or "+
			"`MEDIA_DIR: /path/to/dir`, those paths are returned in "+
			"structured media_files/media_dirs fields.",
	)
	return strings.Join(parts, " ")
}

func sandboxExecToolDescription(hasMemoryFile bool) string {
	parts := []string{
		"Execute a shell command inside the configured sandbox.",
		"Use this for general local shell work when the runtime is " +
			"configured with Code Executor sandbox.",
		"Only foreground non-interactive commands are supported in " +
			"sandbox mode; background, TTY, write_stdin, and " +
			"kill_session are not available.",
		"Sandbox filesystem, network, timeout, and environment behavior " +
			"follow the Code Executor sandbox configuration.",
	}
	if hasMemoryFile {
		parts = append(
			parts,
			"Memory-file environment metadata may be present, but host "+
				"paths are not automatically mounted into the sandbox.",
		)
	}
	return strings.Join(parts, " ")
}

type execInput struct {
	Command       string            `json:"command"`
	Workdir       string            `json:"workdir,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	YieldTimeMS   *int              `json:"yield_time_ms,omitempty"`
	YieldMs       *int              `json:"yieldMs,omitempty"`
	Background    bool              `json:"background,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
}

func (t *execTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.mgr == nil {
		return nil, errors.New(errExecToolNotConfigured)
	}

	var in execInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return nil, errors.New(errCommandRequired)
	}

	workdir, err := resolveWorkdir(in.Workdir)
	if err != nil {
		return nil, err
	}
	workdir, err = runtimeprofile.ResolveWorkdir(ctx, workdir)
	if err != nil {
		return nil, err
	}

	yield := firstInt(in.YieldTimeMS, in.YieldMs)
	timeout := firstInt(in.TimeoutSec, in.TimeoutSecOld)
	tty := firstBool(in.TTY, in.PTY)
	env := mergeExecEnv(
		in.Env,
		mergeExecEnv(
			identity.EnvVarsFromContext(ctx),
			mergeExecEnv(
				uploadEnvFromContext(ctx, t.uploads),
				memoryFileEnvFromContext(ctx, t.memoryStore),
			),
		),
	)

	res, err := t.mgr.Exec(ctx, execParams{
		Command:    in.Command,
		Workdir:    workdir,
		Env:        env,
		Pty:        tty,
		Background: in.Background,
		YieldMs:    yield,
		TimeoutS:   timeout,
	})
	if err != nil {
		return nil, err
	}
	annotateExecResult(&res)
	return res, nil
}

func (t *sandboxExecTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.engine == nil {
		return nil, errors.New(errExecToolNotConfigured)
	}

	var in execInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return nil, errors.New(errCommandRequired)
	}

	yield := firstInt(in.YieldTimeMS, in.YieldMs)
	timeout := firstInt(in.TimeoutSec, in.TimeoutSecOld)
	tty := firstBool(in.TTY, in.PTY)
	if in.Background || tty || (yield != nil && *yield > 0) {
		return nil, errors.New(errSandboxExecUnsupported)
	}

	cwd, err := sandboxExecCwd(in.Workdir)
	if err != nil {
		return nil, err
	}
	env := mergeExecEnv(
		in.Env,
		mergeExecEnv(
			identity.EnvVarsFromContext(ctx),
			mergeExecEnv(
				uploadEnvFromContext(ctx, t.uploads),
				memoryFileEnvFromContext(ctx, t.memoryStore),
			),
		),
	)
	req := newCommandRequest(execParams{
		Command:  in.Command,
		Workdir:  cwd,
		Env:      env,
		YieldMs:  yield,
		TimeoutS: timeout,
	})
	if t.policy != nil {
		if err := t.policy(ctx, req); err != nil {
			return nil, err
		}
	}
	redact := t.outputRedactor(req)
	ws, err := t.workspace(ctx)
	if err != nil {
		return nil, err
	}
	spec := codeexecutor.RunProgramSpec{
		Cmd:  shellProgram,
		Args: []string{shellLoginFlag, in.Command},
		Env:  env,
		Cwd:  cwd,
	}
	if timeout != nil && *timeout > 0 {
		spec.Timeout = time.Duration(*timeout) * time.Second
	}
	run, err := t.engine.Runner().RunProgram(ctx, ws, spec)
	output := applyOutputRedactor(redact, run.Stdout+run.Stderr)
	res := execResult{
		Status:   "exited",
		Output:   output,
		ExitCode: run.ExitCode,
	}
	if run.TimedOut {
		res.Status = "timeout"
	}
	annotateExecResult(&res)
	if err != nil {
		return res, err
	}
	return res, nil
}

func (t *sandboxExecTool) outputRedactor(
	req CommandRequest,
) func(string) string {
	if t == nil || t.redactor == nil {
		return nil
	}
	copied := copyCommandRequest(req)
	return func(output string) string {
		return t.redactor(copied, output)
	}
}

func (t *sandboxExecTool) workspace(
	ctx context.Context,
) (codeexecutor.Workspace, error) {
	workspaceID := "openclaw-exec-command"
	if inv, ok := agent.InvocationFromContext(ctx); ok && inv != nil {
		if key := workspacesession.KeyFromInvocation(inv); key != "" {
			workspaceID = key
		}
	}
	if t.registry == nil {
		return codeexecutor.NewWorkspaceRegistry().Acquire(
			ctx,
			t.engine.Manager(),
			workspaceID,
		)
	}
	return t.registry.Acquire(ctx, t.engine.Manager(), workspaceID)
}

func sandboxExecCwd(workdir string) (string, error) {
	workdir = strings.TrimSpace(workdir)
	if workdir == "" {
		return "", nil
	}
	if filepath.IsAbs(workdir) {
		return "", fmt.Errorf(
			"%w: absolute workdir is not supported in sandbox exec_command",
			errors.New(errSandboxExecUnsupported),
		)
	}
	clean := filepath.Clean(workdir)
	if clean == "." {
		return "", nil
	}
	return filepath.ToSlash(clean), nil
}

type writeTool struct {
	mgr *Manager
}

// NewWriteStdinTool creates the stdin continuation tool.
func NewWriteStdinTool(mgr *Manager) tool.Tool {
	return &writeTool{mgr: mgr}
}

func (t *writeTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolWriteStdin,
		Description: "Write to an existing exec_command session. " +
			"When chars is empty, this acts like a poll.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"session_id"},
			Properties: map[string]*tool.Schema{
				"session_id": {
					Type: "string",
					Description: "Session id returned by " +
						"exec_command.",
				},
				"chars": {
					Type: "string",
					Description: "Characters to write. Include " +
						"\\n when the program expects Enter.",
				},
				"yield_time_ms": {
					Type: "number",
					Description: "Optional wait before polling " +
						"recent output.",
				},
				"append_newline": {
					Type:        "boolean",
					Description: "Append a newline after chars.",
				},
				"sessionId": {
					Type:        "string",
					Description: "Alias for session_id.",
				},
				"yieldMs": {
					Type:        "number",
					Description: "Alias for yield_time_ms.",
				},
				"submit": {
					Type:        "boolean",
					Description: "Alias for append_newline.",
				},
			},
		},
	}
}

type writeInput struct {
	SessionID     string `json:"session_id,omitempty"`
	SessionIDOld  string `json:"sessionId,omitempty"`
	Chars         string `json:"chars,omitempty"`
	YieldTimeMS   *int   `json:"yield_time_ms,omitempty"`
	YieldMs       *int   `json:"yieldMs,omitempty"`
	AppendNewline *bool  `json:"append_newline,omitempty"`
	Submit        *bool  `json:"submit,omitempty"`
}

func (t *writeTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.mgr == nil {
		return nil, errors.New(errWriteToolNotConfigured)
	}

	var in writeInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(in.SessionIDOld)
	}
	if sessionID == "" {
		return nil, errors.New(errSessionIDRequired)
	}

	appendNewline := firstBool(in.AppendNewline, in.Submit)
	if _, err := t.mgr.write(
		sessionID,
		in.Chars,
		appendNewline,
	); err != nil {
		return nil, err
	}

	yield := defaultWriteYield
	if v := firstInt(in.YieldTimeMS, in.YieldMs); v != nil &&
		*v >= 0 {
		yield = *v
	}
	if yield > 0 {
		timer := time.NewTimer(time.Duration(yield) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
		}
	}

	poll, err := t.mgr.poll(sessionID, nil)
	if err != nil {
		return nil, err
	}
	return mapPollResult(sessionID, poll), nil
}

type killTool struct {
	mgr *Manager
}

// NewKillSessionTool creates the session termination tool.
func NewKillSessionTool(mgr *Manager) tool.Tool {
	return &killTool{mgr: mgr}
}

func (t *killTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        toolKillSession,
		Description: "Terminate a running exec_command session.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"session_id"},
			Properties: map[string]*tool.Schema{
				"session_id": {
					Type: "string",
					Description: "Session id returned by " +
						"exec_command.",
				},
				"sessionId": {
					Type:        "string",
					Description: "Alias for session_id.",
				},
			},
		},
	}
}

type killInput struct {
	SessionID    string `json:"session_id,omitempty"`
	SessionIDOld string `json:"sessionId,omitempty"`
}

func (t *killTool) Call(ctx context.Context, args []byte) (any, error) {
	_ = ctx
	if t == nil || t.mgr == nil {
		return nil, errors.New(errKillToolNotConfigured)
	}

	var in killInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}

	sessionID := strings.TrimSpace(in.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(in.SessionIDOld)
	}
	if sessionID == "" {
		return nil, errors.New(errSessionIDRequired)
	}

	err := t.mgr.kill(sessionID)
	return map[string]any{
		"ok":         err == nil,
		"session_id": sessionID,
	}, err
}

const (
	defaultWriteYield = 200
)

func mapPollResult(
	sessionID string,
	poll processPoll,
) map[string]any {
	out := map[string]any{
		"session_id":  sessionID,
		"status":      poll.Status,
		"output":      poll.Output,
		"offset":      poll.Offset,
		"next_offset": poll.NextOffset,
	}
	if poll.ExitCode != nil {
		out["exit_code"] = *poll.ExitCode
	}
	addExecOutputMarkers(out, poll.Output)
	return out
}

func annotateExecResult(out *execResult) {
	if out == nil {
		return
	}
	mediaFiles, mediaDirs := parseExecOutputMarkers(out.Output)
	out.MediaFiles = mediaFiles
	out.MediaDirs = mediaDirs
}

func addExecOutputMarkers(
	out map[string]any,
	output string,
) {
	mediaFiles, mediaDirs := parseExecOutputMarkers(output)
	if len(mediaFiles) > 0 {
		out["media_files"] = mediaFiles
	}
	if len(mediaDirs) > 0 {
		out["media_dirs"] = mediaDirs
	}
}

func parseExecOutputMarkers(output string) ([]string, []string) {
	if strings.TrimSpace(output) == "" {
		return nil, nil
	}
	lines := strings.Split(output, "\n")
	mediaFiles := make([]string, 0, 2)
	mediaDirs := make([]string, 0, 1)
	seenFiles := make(map[string]struct{})
	seenDirs := make(map[string]struct{})
	for _, line := range lines {
		prefix, path, ok := splitExecOutputMarker(line)
		if !ok {
			continue
		}
		switch prefix {
		case execOutputMediaMarker:
			mediaFiles = appendExecOutputMarker(
				mediaFiles,
				seenFiles,
				path,
			)
		case execOutputMediaDirMarker:
			mediaDirs = appendExecOutputMarker(
				mediaDirs,
				seenDirs,
				path,
			)
		}
		if len(mediaFiles)+len(mediaDirs) >= maxExecOutputMarkers {
			break
		}
	}
	return mediaFiles, mediaDirs
}

func splitExecOutputMarker(line string) (string, string, bool) {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, execOutputMediaDirMarker):
		path := strings.TrimSpace(
			strings.TrimPrefix(trimmed, execOutputMediaDirMarker),
		)
		return execOutputMediaDirMarker, path, path != ""
	case strings.HasPrefix(trimmed, execOutputMediaMarker):
		path := strings.TrimSpace(
			strings.TrimPrefix(trimmed, execOutputMediaMarker),
		)
		return execOutputMediaMarker, path, path != ""
	default:
		return "", "", false
	}
}

func appendExecOutputMarker(
	out []string,
	seen map[string]struct{},
	path string,
) []string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return out
	}
	if _, ok := seen[trimmed]; ok {
		return out
	}
	seen[trimmed] = struct{}{}
	return append(out, trimmed)
}

func firstInt(values ...*int) *int {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func firstBool(values ...*bool) bool {
	for _, value := range values {
		if value != nil {
			return *value
		}
	}
	return false
}

func resolveWorkdir(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", nil
	}
	if s == "~" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return home, nil
	}
	if strings.HasPrefix(s, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		s = filepath.Join(home, strings.TrimPrefix(s, "~/"))
	}
	return s, nil
}

func mergeExecEnv(
	base map[string]string,
	extra map[string]string,
) map[string]string {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := make(map[string]string, len(base)+len(extra))
	for key, value := range extra {
		out[key] = value
	}
	for key, value := range base {
		out[key] = value
	}
	return out
}

func uploadEnvFromContext(
	ctx context.Context,
	store *uploads.Store,
) map[string]string {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil
	}

	env := make(map[string]string)
	if scope, ok := uploadScopeFromInvocation(inv); ok &&
		store != nil {
		dir := strings.TrimSpace(store.ScopeDir(scope))
		if dir != "" {
			env[envSessionUploadsDir] = dir
		}
	}

	recent := recentUploadsFromInvocation(
		inv,
		store,
		recentUploadsLimit,
	)
	if len(recent) == 0 {
		if len(env) == 0 {
			return nil
		}
		return env
	}
	latest := recent[0]

	env[envLastUploadPath] = latest.Path
	env[envLastUploadHostRef] = latest.HostRef
	if _, ok := env[envSessionUploadsDir]; !ok {
		env[envSessionUploadsDir] = filepath.Dir(latest.Path)
	}
	if latest.Name != "" {
		env[envLastUploadName] = latest.Name
	}
	if latest.MimeType != "" {
		env[envLastUploadMIME] = latest.MimeType
	}
	if raw, err := json.Marshal(recent); err == nil {
		env[envRecentUploadsJSON] = string(raw)
	}
	addLatestKindUploadEnv(
		env,
		recent,
		uploadKindImage,
		envLastImagePath,
		envLastImageHostRef,
		envLastImageName,
		envLastImageMIME,
	)
	addLatestKindUploadEnv(
		env,
		recent,
		uploadKindAudio,
		envLastAudioPath,
		envLastAudioHostRef,
		envLastAudioName,
		envLastAudioMIME,
	)
	addLatestKindUploadEnv(
		env,
		recent,
		uploadKindVideo,
		envLastVideoPath,
		envLastVideoHostRef,
		envLastVideoName,
		envLastVideoMIME,
	)
	addLatestKindUploadEnv(
		env,
		recent,
		uploadKindPDF,
		envLastPDFPath,
		envLastPDFHostRef,
		envLastPDFName,
		envLastPDFMIME,
	)
	return env
}

func memoryFileEnvFromContext(
	ctx context.Context,
	store *memoryfile.Store,
) map[string]string {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil || store == nil {
		return nil
	}

	appName := strings.TrimSpace(inv.Session.AppName)
	userID := strings.TrimSpace(inv.Session.UserID)
	if appName == "" || userID == "" {
		return nil
	}

	personalUserID := conversationscope.UserStorageIDFromContext(
		ctx,
		userID,
	)
	storageUserID := conversationscope.StorageUserIDFromContext(
		ctx,
		personalUserID,
	)
	currentPath := ensureMemoryEnvPath(ctx, store, appName, storageUserID)
	personalPath := ensureMemoryEnvPath(ctx, store, appName, personalUserID)
	if currentPath == "" {
		return nil
	}
	env := map[string]string{
		envMemoryFile:     currentPath,
		envChatMemoryFile: currentPath,
	}
	if personalPath != "" {
		env[envUserMemoryFile] = personalPath
	}
	return env
}

func ensureMemoryEnvPath(
	ctx context.Context,
	store *memoryfile.Store,
	appName string,
	userID string,
) string {
	userID = strings.TrimSpace(userID)
	if userID == "" {
		return ""
	}
	if ctx == nil {
		ctx = context.Background()
	}
	path, err := store.EnsureMemory(
		ctx,
		appName,
		userID,
	)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(path)
}

func addLatestKindUploadEnv(
	env map[string]string,
	recent []execUploadMeta,
	kind string,
	pathKey string,
	hostRefKey string,
	nameKey string,
	mimeKey string,
) {
	latest, ok := latestUploadOfKind(recent, kind)
	if !ok {
		return
	}
	env[pathKey] = latest.Path
	if latest.HostRef != "" {
		env[hostRefKey] = latest.HostRef
	}
	if latest.Name != "" {
		env[nameKey] = latest.Name
	}
	if latest.MimeType != "" {
		env[mimeKey] = latest.MimeType
	}
}

func latestUploadOfKind(
	recent []execUploadMeta,
	kind string,
) (execUploadMeta, bool) {
	for _, item := range recent {
		if item.Kind == kind {
			return item, true
		}
	}
	return execUploadMeta{}, false
}

func recentUploadsFromInvocation(
	inv *agent.Invocation,
	store *uploads.Store,
	limit int,
) []execUploadMeta {
	if inv == nil {
		return nil
	}

	out := make([]execUploadMeta, 0, limit)
	seen := make(map[string]struct{})
	out = appendRecentUploadsFromMessage(
		out,
		seen,
		inv.Message,
		limit,
	)
	if inv.Session == nil {
		return out
	}

	inv.Session.EventMu.RLock()
	defer inv.Session.EventMu.RUnlock()

	for i := len(inv.Session.Events) - 1; i >= 0; i-- {
		if limit > 0 && len(out) >= limit {
			break
		}
		evt := inv.Session.Events[i]
		if evt.Response == nil {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if msg.Role != model.RoleUser && msg.Role != "" {
				continue
			}
			out = appendRecentUploadsFromMessage(
				out,
				seen,
				msg,
				limit,
			)
		}
	}
	out = appendRecentUploadsFromStore(
		out,
		seen,
		store,
		inv,
		limit,
	)
	return out
}

func appendRecentUploadsFromMessage(
	out []execUploadMeta,
	seen map[string]struct{},
	msg model.Message,
	limit int,
) []execUploadMeta {
	for i := len(msg.ContentParts) - 1; i >= 0; i-- {
		if limit > 0 && len(out) >= limit {
			break
		}
		part := msg.ContentParts[i]
		if part.Type != model.ContentTypeFile || part.File == nil {
			continue
		}
		path, ok := uploads.PathFromHostRef(part.File.FileID)
		if !ok {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		name := strings.TrimSpace(part.File.Name)
		if name == "" {
			name = filepath.Base(path)
		}
		name = uploads.PreferredName(
			name,
			strings.TrimSpace(part.File.MimeType),
		)
		seen[path] = struct{}{}
		out = append(out, execUploadMeta{
			Name:     name,
			Path:     path,
			HostRef:  uploads.HostRef(path),
			MimeType: strings.TrimSpace(part.File.MimeType),
			Kind:     uploadKindFromMeta(name, part.File.MimeType),
			Source:   uploads.SourceInbound,
		})
	}
	return out
}

func appendRecentUploadsFromStore(
	out []execUploadMeta,
	seen map[string]struct{},
	store *uploads.Store,
	inv *agent.Invocation,
	limit int,
) []execUploadMeta {
	if store == nil || inv == nil || inv.Session == nil {
		return out
	}
	if limit > 0 && len(out) >= limit {
		return out
	}

	scope, ok := uploadScopeFromInvocation(inv)
	if !ok {
		return out
	}
	files, err := store.ListScope(scope, limit)
	if err != nil {
		return out
	}
	for _, file := range files {
		if limit > 0 && len(out) >= limit {
			break
		}
		path := strings.TrimSpace(file.Path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		if _, err := os.Stat(path); err != nil {
			continue
		}
		name := strings.TrimSpace(file.Name)
		if name == "" {
			name = filepath.Base(path)
		}
		name = uploads.PreferredName(
			name,
			strings.TrimSpace(file.MimeType),
		)
		seen[path] = struct{}{}
		out = append(out, execUploadMeta{
			Name:     name,
			Path:     path,
			HostRef:  uploads.HostRef(path),
			MimeType: strings.TrimSpace(file.MimeType),
			Kind:     uploadKindFromMeta(name, file.MimeType),
			Source:   strings.TrimSpace(file.Source),
		})
	}
	return out
}

func uploadScopeFromInvocation(
	inv *agent.Invocation,
) (uploads.Scope, bool) {
	if inv == nil || inv.Session == nil {
		return uploads.Scope{}, false
	}
	sessionID := strings.TrimSpace(inv.Session.ID)
	userID := strings.TrimSpace(inv.Session.UserID)
	if sessionID == "" || userID == "" {
		return uploads.Scope{}, false
	}
	return uploads.Scope{
		Channel:   uploadChannelFromSessionID(sessionID),
		UserID:    userID,
		SessionID: sessionID,
	}, true
}

func uploadChannelFromSessionID(sessionID string) string {
	trimmed := strings.TrimSpace(sessionID)
	if trimmed == "" {
		return ""
	}
	idx := strings.Index(trimmed, ":")
	if idx <= 0 {
		return ""
	}
	return strings.TrimSpace(trimmed[:idx])
}

func uploadKindFromMeta(name string, mimeType string) string {
	return uploads.KindFromMeta(name, mimeType)
}

var _ tool.CallableTool = (*execTool)(nil)
var _ tool.CallableTool = (*writeTool)(nil)
var _ tool.CallableTool = (*killTool)(nil)
