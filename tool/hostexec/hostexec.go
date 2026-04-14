//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package hostexec provides direct host command execution tools for
// personal-agent style workflows.
package hostexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultBaseDir      = "."
	defaultToolSetName  = "hostexec"
	defaultWriteYieldMS = 200

	toolExecCommand = "exec_command"
	toolWriteStdin  = "write_stdin"
	toolKillSession = "kill_session"
)

const (
	errExecToolNotConfigured  = "exec tool is not configured"
	errWriteToolNotConfigured = "write_stdin tool is not configured"
	errKillToolNotConfigured  = "kill_session tool is not configured"
	errCommandRequired        = "command is required"
	errSessionIDRequired      = "session id is required"
)

type config struct {
	baseDir  string
	name     string
	maxLines int
	jobTTL   time.Duration
	baseEnv  map[string]string
}

// Option configures the hostexec tool set.
type Option func(*config)

// WithBaseDir sets the default directory used by exec_command.
func WithBaseDir(baseDir string) Option {
	return func(c *config) {
		c.baseDir = baseDir
	}
}

// WithName sets the tool set name.
func WithName(name string) Option {
	return func(c *config) {
		c.name = name
	}
}

// WithMaxLines sets the maximum retained output lines per session.
func WithMaxLines(lines int) Option {
	return func(c *config) {
		if lines > 0 {
			c.maxLines = lines
		}
	}
}

// WithJobTTL sets how long finished sessions are retained.
func WithJobTTL(ttl time.Duration) Option {
	return func(c *config) {
		if ttl > 0 {
			c.jobTTL = ttl
		}
	}
}

// WithBaseEnv sets environment variables applied to all commands.
func WithBaseEnv(env map[string]string) Option {
	return func(c *config) {
		if len(env) == 0 {
			return
		}
		c.baseEnv = cloneEnvMap(env)
	}
}

func defaultConfig() config {
	return config{
		baseDir: defaultBaseDir,
		name:    defaultToolSetName,
	}
}

// NewToolSet creates a host command execution tool set.
func NewToolSet(opts ...Option) (tool.ToolSet, error) {
	cfg := defaultConfig()
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	baseDir, err := resolveBaseDir(cfg.baseDir)
	if err != nil {
		return nil, err
	}

	mgr := newManager()
	if cfg.maxLines > 0 {
		mgr.maxLines = cfg.maxLines
	}
	if cfg.jobTTL > 0 {
		mgr.jobTTL = cfg.jobTTL
	}
	if len(cfg.baseEnv) > 0 {
		mgr.baseEnv = cloneEnvMap(cfg.baseEnv)
	}

	set := &toolSet{
		name:    strings.TrimSpace(cfg.name),
		baseDir: baseDir,
		mgr:     mgr,
	}
	if set.name == "" {
		set.name = defaultToolSetName
	}
	set.tools = []tool.Tool{
		&execCommandTool{mgr: mgr, baseDir: baseDir},
		&writeStdinTool{mgr: mgr},
		&killSessionTool{mgr: mgr},
	}
	return set, nil
}

type toolSet struct {
	name    string
	baseDir string
	mgr     *manager
	tools   []tool.Tool
}

func (s *toolSet) Tools(context.Context) []tool.Tool {
	return s.tools
}

func (s *toolSet) Close() error {
	if s == nil || s.mgr == nil {
		return nil
	}
	return s.mgr.close()
}

func (s *toolSet) Name() string {
	return s.name
}

type execCommandTool struct {
	mgr     *manager
	baseDir string
}

func (t *execCommandTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolExecCommand,
		Description: "Execute a host shell command. Use this for " +
			"general local shell work in the configured base " +
			"directory. Long-running or interactive commands can " +
			"continue with write_stdin. Relative workdir values " +
			"resolve from the tool set base directory.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"command"},
			Properties: map[string]*tool.Schema{
				"command": {
					Type:        "string",
					Description: "Shell command to execute.",
				},
				"workdir": {
					Type: "string",
					Description: "Optional working directory. " +
						"Relative paths resolve from the base " +
						"directory.",
				},
				"env": {
					Type:        "object",
					Description: "Optional environment overrides.",
				},
				"yield_time_ms": {
					Type: "integer",
					Description: "Wait this long before " +
						"returning. Use 0 to wait for exit " +
						"when possible.",
				},
				"background": {
					Type:        "boolean",
					Description: "Start the command and return.",
				},
				"timeout_sec": {
					Type:        "integer",
					Description: "Maximum command runtime.",
				},
				"tty": {
					Type: "boolean",
					Description: "Allocate a TTY for " +
						"interactive commands.",
				},
				"yieldMs": {
					Type:        "integer",
					Description: "Alias for yield_time_ms.",
				},
				"timeoutSec": {
					Type:        "integer",
					Description: "Alias for timeout_sec.",
				},
				"pty": {
					Type:        "boolean",
					Description: "Alias for tty.",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"status"},
			Properties: map[string]*tool.Schema{
				"status": {
					Type:        "string",
					Description: "running or exited",
				},
				"output": {
					Type:        "string",
					Description: "Combined command output.",
				},
				"exit_code": {
					Type:        "integer",
					Description: "Process exit code when exited.",
				},
				"session_id": {
					Type: "string",
					Description: "Session id for running " +
						"commands.",
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

func (t *execCommandTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
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

	workdir, err := resolveWorkdir(in.Workdir, t.baseDir)
	if err != nil {
		return nil, err
	}
	yield := firstInt(in.YieldTimeMS, in.YieldMs)
	timeout := firstInt(in.TimeoutSec, in.TimeoutSecOld)

	res, err := t.mgr.exec(ctx, execParams{
		Command:    in.Command,
		Workdir:    workdir,
		Env:        in.Env,
		Pty:        firstBool(in.TTY, in.PTY),
		Background: in.Background,
		YieldMs:    yield,
		TimeoutS:   timeout,
	})
	if err != nil {
		return nil, err
	}
	return mapExecResult(res), nil
}

type writeStdinTool struct {
	mgr *manager
}

func (t *writeStdinTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolWriteStdin,
		Description: "Write to a running exec_command session. " +
			"When chars is empty and append_newline is false, " +
			"this acts like a lightweight poll.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"session_id"},
			Properties: map[string]*tool.Schema{
				"session_id": {
					Type:        "string",
					Description: "Session id from exec_command.",
				},
				"chars": {
					Type: "string",
					Description: "Characters to write. Use " +
						"append_newline when Enter is needed.",
				},
				"append_newline": {
					Type:        "boolean",
					Description: "Append a newline after chars.",
				},
				"yield_time_ms": {
					Type:        "integer",
					Description: "Optional wait before polling.",
				},
				"sessionId": {
					Type:        "string",
					Description: "Alias for session_id.",
				},
				"yieldMs": {
					Type:        "integer",
					Description: "Alias for yield_time_ms.",
				},
				"submit": {
					Type: "boolean",
					Description: "Alias for " +
						"append_newline.",
				},
			},
		},
		OutputSchema: pollOutputSchema(
			"Structured session output after writing or polling.",
		),
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

func (t *writeStdinTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.mgr == nil {
		return nil, errors.New(errWriteToolNotConfigured)
	}

	var in writeInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	sessionID := firstNonEmpty(in.SessionID, in.SessionIDOld)
	if sessionID == "" {
		return nil, errors.New(errSessionIDRequired)
	}

	if err := t.mgr.write(
		sessionID,
		in.Chars,
		firstBool(in.AppendNewline, in.Submit),
	); err != nil {
		return nil, err
	}

	yield := defaultWriteYieldMS
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

type killSessionTool struct {
	mgr *manager
}

func (t *killSessionTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        toolKillSession,
		Description: "Terminate a running exec_command session.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"session_id"},
			Properties: map[string]*tool.Schema{
				"session_id": {
					Type:        "string",
					Description: "Session id from exec_command.",
				},
				"sessionId": {
					Type:        "string",
					Description: "Alias for session_id.",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"ok", "session_id"},
			Properties: map[string]*tool.Schema{
				"ok": {
					Type:        "boolean",
					Description: "Whether termination succeeded.",
				},
				"session_id": {
					Type:        "string",
					Description: "The terminated session id.",
				},
			},
		},
	}
}

type killInput struct {
	SessionID    string `json:"session_id,omitempty"`
	SessionIDOld string `json:"sessionId,omitempty"`
}

func (t *killSessionTool) Call(
	ctx context.Context,
	args []byte,
) (any, error) {
	if t == nil || t.mgr == nil {
		return nil, errors.New(errKillToolNotConfigured)
	}

	var in killInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	sessionID := firstNonEmpty(in.SessionID, in.SessionIDOld)
	if sessionID == "" {
		return nil, errors.New(errSessionIDRequired)
	}

	err := t.mgr.killContext(ctx, sessionID)
	return map[string]any{
		"ok":         err == nil,
		"session_id": sessionID,
	}, err
}

func pollOutputSchema(desc string) *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: desc,
		Required:    []string{"session_id", "status"},
		Properties: map[string]*tool.Schema{
			"session_id": {
				Type:        "string",
				Description: "Session id from exec_command.",
			},
			"status": {
				Type:        "string",
				Description: "running or exited",
			},
			"output": {
				Type:        "string",
				Description: "New output since the last poll.",
			},
			"offset": {
				Type:        "integer",
				Description: "Log start offset.",
			},
			"next_offset": {
				Type:        "integer",
				Description: "Next cursor offset.",
			},
			"exit_code": {
				Type:        "integer",
				Description: "Process exit code when exited.",
			},
		},
	}
}

func mapExecResult(res execResult) map[string]any {
	out := map[string]any{
		"status": res.Status,
		"output": res.Output,
	}
	if res.ExitCode != nil {
		out["exit_code"] = *res.ExitCode
	}
	if res.SessionID != "" {
		out["session_id"] = res.SessionID
	}
	return out
}

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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func resolveBaseDir(raw string) (string, error) {
	baseDir, err := resolveWorkdir(raw, "")
	if err != nil {
		return "", err
	}
	if baseDir == "" {
		return "", nil
	}
	return filepath.Abs(baseDir)
}

func resolveWorkdir(
	raw string,
	baseDir string,
) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return baseDir, nil
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
	if baseDir != "" && !filepath.IsAbs(s) {
		return filepath.Join(baseDir, s), nil
	}
	if filepath.IsAbs(s) {
		return s, nil
	}
	return filepath.Abs(s)
}

func cloneEnvMap(env map[string]string) map[string]string {
	if len(env) == 0 {
		return nil
	}
	out := make(map[string]string, len(env))
	for key, value := range env {
		if strings.TrimSpace(key) == "" {
			continue
		}
		out[key] = value
	}
	return out
}

var _ tool.ToolSet = (*toolSet)(nil)
var _ tool.CallableTool = (*execCommandTool)(nil)
var _ tool.CallableTool = (*writeStdinTool)(nil)
var _ tool.CallableTool = (*killSessionTool)(nil)
