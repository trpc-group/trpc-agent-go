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
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/uploads"
)

const (
	toolExecCommand = "exec_command"
	toolWriteStdin  = "write_stdin"
	toolKillSession = "kill_session"

	errExecToolNotConfigured  = "exec tool is not configured"
	errCommandRequired        = "command is required"
	errWriteToolNotConfigured = "write_stdin tool is not configured"
	errKillToolNotConfigured  = "kill_session tool is not configured"
	errSessionIDRequired      = "session id is required"

	envSessionUploadsDir = "OPENCLAW_SESSION_UPLOADS_DIR"
	envLastUploadPath    = "OPENCLAW_LAST_UPLOAD_PATH"
	envLastUploadName    = "OPENCLAW_LAST_UPLOAD_NAME"
	envLastUploadMIME    = "OPENCLAW_LAST_UPLOAD_MIME"
	envRecentUploadsJSON = "OPENCLAW_RECENT_UPLOADS_JSON"

	envLastImagePath = "OPENCLAW_LAST_IMAGE_PATH"
	envLastImageName = "OPENCLAW_LAST_IMAGE_NAME"
	envLastImageMIME = "OPENCLAW_LAST_IMAGE_MIME"

	envLastAudioPath = "OPENCLAW_LAST_AUDIO_PATH"
	envLastAudioName = "OPENCLAW_LAST_AUDIO_NAME"
	envLastAudioMIME = "OPENCLAW_LAST_AUDIO_MIME"

	envLastVideoPath = "OPENCLAW_LAST_VIDEO_PATH"
	envLastVideoName = "OPENCLAW_LAST_VIDEO_NAME"
	envLastVideoMIME = "OPENCLAW_LAST_VIDEO_MIME"

	envLastPDFPath = "OPENCLAW_LAST_PDF_PATH"
	envLastPDFName = "OPENCLAW_LAST_PDF_NAME"
	envLastPDFMIME = "OPENCLAW_LAST_PDF_MIME"

	recentUploadsLimit = 6
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
}

type execTool struct {
	mgr *Manager
}

// NewExecCommandTool creates the canonical host command tool.
func NewExecCommandTool(mgr *Manager) tool.Tool {
	return &execTool{mgr: mgr}
}

func (t *execTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolExecCommand,
		Description: "Execute a host shell command. Use this for " +
			"general local shell work. Interactive commands can " +
			"continue with write_stdin. When a chat upload is " +
			"available, OPENCLAW_LAST_UPLOAD_PATH, " +
			"OPENCLAW_LAST_UPLOAD_NAME, OPENCLAW_LAST_UPLOAD_MIME, " +
			"kind-specific OPENCLAW_LAST_*_PATH vars, " +
			"OPENCLAW_SESSION_UPLOADS_DIR, and " +
			"OPENCLAW_RECENT_UPLOADS_JSON point to stable " +
			"attachment metadata and host paths. Write derived " +
			"outputs under OPENCLAW_SESSION_UPLOADS_DIR when " +
			"you plan to send them back to the user.",
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

	yield := firstInt(in.YieldTimeMS, in.YieldMs)
	timeout := firstInt(in.TimeoutSec, in.TimeoutSecOld)
	tty := firstBool(in.TTY, in.PTY)
	env := mergeExecEnv(in.Env, uploadEnvFromContext(ctx))

	return t.mgr.Exec(ctx, execParams{
		Command:    in.Command,
		Workdir:    workdir,
		Env:        env,
		Pty:        tty,
		Background: in.Background,
		YieldMs:    yield,
		TimeoutS:   timeout,
	})
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
	return out
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

func uploadEnvFromContext(ctx context.Context) map[string]string {
	inv, ok := agent.InvocationFromContext(ctx)
	if !ok || inv == nil || inv.Session == nil {
		return nil
	}

	recent := recentUploadsFromInvocation(inv, recentUploadsLimit)
	if len(recent) == 0 {
		return nil
	}
	latest := recent[0]

	env := map[string]string{
		envLastUploadPath:    latest.Path,
		envSessionUploadsDir: filepath.Dir(latest.Path),
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
		envLastImageName,
		envLastImageMIME,
	)
	addLatestKindUploadEnv(
		env,
		recent,
		uploadKindAudio,
		envLastAudioPath,
		envLastAudioName,
		envLastAudioMIME,
	)
	addLatestKindUploadEnv(
		env,
		recent,
		uploadKindVideo,
		envLastVideoPath,
		envLastVideoName,
		envLastVideoMIME,
	)
	addLatestKindUploadEnv(
		env,
		recent,
		uploadKindPDF,
		envLastPDFPath,
		envLastPDFName,
		envLastPDFMIME,
	)
	return env
}

func addLatestKindUploadEnv(
	env map[string]string,
	recent []execUploadMeta,
	kind string,
	pathKey string,
	nameKey string,
	mimeKey string,
) {
	latest, ok := latestUploadOfKind(recent, kind)
	if !ok {
		return
	}
	env[pathKey] = latest.Path
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
		seen[path] = struct{}{}
		out = append(out, execUploadMeta{
			Name:     name,
			Path:     path,
			HostRef:  uploads.HostRef(path),
			MimeType: strings.TrimSpace(part.File.MimeType),
			Kind:     uploadKindFromMeta(name, part.File.MimeType),
		})
	}
	return out
}

func uploadKindFromMeta(name string, mimeType string) string {
	return uploads.KindFromMeta(name, mimeType)
}

var _ tool.CallableTool = (*execTool)(nil)
var _ tool.CallableTool = (*writeTool)(nil)
var _ tool.CallableTool = (*killTool)(nil)
