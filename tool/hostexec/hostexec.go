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

	"trpc.group/trpc-go/trpc-agent-go/internal/envscrub"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
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
	safety   *safety.Guard
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

// WithSafetyGuard enables pre-execution scanning and output redaction.
// Without this option hostexec preserves its historical behavior.
func WithSafetyGuard(guard *safety.Guard) Option {
	return func(c *config) {
		c.safety = guard
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
		&execCommandTool{mgr: mgr, baseDir: baseDir, safety: cfg.safety},
		&writeStdinTool{mgr: mgr, safety: cfg.safety},
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
	safety  *safety.Guard
}

func (t *execCommandTool) ToolPermissionPolicy() tool.PermissionPolicy {
	if t == nil {
		return nil
	}
	return t.safety
}

func (t *execCommandTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: toolExecCommand,
		Description: "Execute a host shell command. Use this for " +
			"general local shell work in the configured base " +
			"directory. Long-running or interactive commands can " +
			"continue with write_stdin. Relative workdir values " +
			"resolve from the tool set base directory. If a file " +
			"tool must read command output later, write that output " +
			"to a relative path under the workdir/base directory " +
			"instead of an absolute temp path.",
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
	guard := effectiveHostSafetyGuard(ctx, t.safety)
	profile := effectiveHostRuntimeSafetyProfile(ctx, t.safety, toolExecCommand)
	execEnv := in.Env
	cleanEnv := false
	if guard != nil {
		execEnv = safetyExecutionEnv(t.mgr.baseEnv, in.Env)
		cleanEnv = true
	}
	params := execParams{
		Command:        in.Command,
		Workdir:        workdir,
		Env:            execEnv,
		SafetyEnv:      safetyScanEnv(t.mgr.baseEnv, in.Env),
		CleanEnv:       cleanEnv,
		Pty:            firstBool(in.TTY, in.PTY),
		Background:     in.Background,
		YieldMs:        yield,
		TimeoutS:       timeout,
		MaxOutputBytes: profile.MaxOutputBytes,
	}
	scanParams := params
	scanParams.TimeoutS = safetyTimeoutForProfile(profile, params.TimeoutS)
	if t.safety != nil {
		if result, blocked, err := checkSafetyGuard(
			ctx,
			t.safety,
			t,
			t.Declaration(),
			execSafetyArguments(scanParams, t.mgr.baseEnv),
		); blocked {
			return result, err
		}
	}
	profile = effectiveHostRuntimeSafetyProfile(ctx, t.safety, toolExecCommand)
	params.TimeoutS = cappedSafetyTimeoutForProfile(profile, params.TimeoutS)
	params.MaxTimeout = time.Duration(profile.MaxTimeout)
	// Re-read the limit after permission evaluation so an atomic policy
	// reload that completed during the scan cannot leave execution using
	// the older output budget.
	params.MaxOutputBytes = profile.MaxOutputBytes

	res, err := t.mgr.exec(ctx, params)
	if err != nil {
		return sanitizeSafetyResult(
			ctx, t.safety, t.Declaration(), args, nil, err,
		)
	}
	return sanitizeSafetyResult(
		ctx, t.safety, t.Declaration(), args, mapExecResult(res), nil,
	)
}

type writeStdinTool struct {
	mgr    *manager
	safety *safety.Guard
}

func (t *writeStdinTool) ToolPermissionPolicy() tool.PermissionPolicy {
	if t == nil {
		return nil
	}
	return t.safety
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
	appendNewline := firstBool(in.AppendNewline, in.Submit)
	if t.safety != nil {
		if result, blocked, err := checkSafetyGuard(
			ctx,
			t.safety,
			t,
			t.Declaration(),
			writeSafetyArguments(sessionID, in.Chars, appendNewline),
		); blocked {
			return result, err
		}
	}

	if err := t.mgr.write(
		sessionID,
		in.Chars,
		appendNewline,
	); err != nil {
		return sanitizeSafetyResult(
			ctx, t.safety, t.Declaration(), args, nil, err,
		)
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
			return sanitizeSafetyResult(
				ctx, t.safety, t.Declaration(), args, nil, ctx.Err(),
			)
		case <-timer.C:
		}
	}

	poll, err := t.mgr.poll(sessionID, nil)
	if err != nil {
		return sanitizeSafetyResult(
			ctx, t.safety, t.Declaration(), args, nil, err,
		)
	}
	return sanitizeSafetyResult(
		ctx,
		t.safety,
		t.Declaration(),
		args,
		mapPollResult(sessionID, poll),
		nil,
	)
}

func execSafetyArguments(params execParams, baseEnv map[string]string) []byte {
	timeoutS := defaultTimeoutS
	if params.TimeoutS != nil && *params.TimeoutS > 0 {
		timeoutS = *params.TimeoutS
	}
	env := mergeInheritedEnv(baseEnv, params.Env)
	if params.SafetyEnv != nil {
		env = params.SafetyEnv
	}
	if params.CleanEnv {
		if params.SafetyEnv == nil {
			env = params.Env
		}
	}
	data, _ := json.Marshal(map[string]any{
		"backend":          "hostexec",
		"command":          params.Command,
		"workdir":          params.Workdir,
		"env":              env,
		"timeout_sec":      timeoutS,
		"pty":              params.Pty,
		"background":       params.Background,
		"max_output_bytes": params.MaxOutputBytes,
	})
	return data
}

func writeSafetyArguments(
	sessionID string,
	chars string,
	appendNewline bool,
) []byte {
	if appendNewline {
		chars += "\n"
	}
	data, _ := json.Marshal(map[string]any{
		"backend":    "hostexec",
		"session_id": sessionID,
		"chars":      chars,
	})
	return data
}

func checkSafetyGuard(
	ctx context.Context,
	guard *safety.Guard,
	t tool.Tool,
	declaration *tool.Declaration,
	args []byte,
) (any, bool, error) {
	if guard == nil {
		return nil, false, nil
	}
	decision, err := guard.CheckToolPermission(ctx, &tool.PermissionRequest{
		Tool: t, ToolName: declaration.Name, Declaration: declaration,
		Arguments: args, Metadata: tool.MetadataOf(t),
	})
	if err != nil {
		return nil, true, err
	}
	decision, err = tool.NormalizePermissionDecision(decision)
	if err != nil {
		return nil, true, err
	}
	if decision.Action == tool.PermissionActionAllow {
		return nil, false, nil
	}
	return tool.PermissionResultFor(declaration.Name, decision), true, nil
}

func sanitizeSafetyResult(
	ctx context.Context,
	guard *safety.Guard,
	declaration *tool.Declaration,
	args []byte,
	result any,
	runErr error,
) (any, error) {
	if guard == nil {
		return result, runErr
	}
	sanitized, err := guard.SanitizeToolResult(ctx, &tool.AfterToolArgs{
		ToolName: declaration.Name, Declaration: declaration,
		Arguments: args, Result: result, Error: runErr,
	})
	if err != nil {
		return nil, err
	}
	sanitizedErr, err := guard.SanitizeToolError(ctx, &tool.AfterToolArgs{
		ToolName: declaration.Name, Declaration: declaration,
		Arguments: args, Result: sanitized, Error: runErr,
	})
	if err != nil {
		return nil, err
	}
	return sanitized, sanitizedErr
}

func safetyMaxOutputBytes(guard *safety.Guard, toolName string) int64 {
	return safetyToolProfile(guard, toolName).MaxOutputBytes
}

func safetyToolProfile(guard *safety.Guard, toolName string) safety.ToolProfile {
	if guard == nil {
		return safety.ToolProfile{}
	}
	return guard.ToolProfile(toolName)
}

func effectiveHostSafetyGuard(ctx context.Context, direct *safety.Guard) *safety.Guard {
	if direct != nil {
		return direct
	}
	guard, _ := tool.PermissionPolicyFromContext(ctx).(*safety.Guard)
	return guard
}

func effectiveHostRuntimeSafetyProfile(
	ctx context.Context,
	direct *safety.Guard,
	toolName string,
) safety.ToolProfile {
	var profile safety.ToolProfile
	if direct != nil {
		profile = direct.ToolProfile(toolName)
	}
	contextGuard, _ := tool.PermissionPolicyFromContext(ctx).(*safety.Guard)
	if contextGuard != nil && contextGuard != direct {
		contextProfile := contextGuard.ToolProfile(toolName)
		profile.MaxTimeout = minPositiveSafetyDuration(
			profile.MaxTimeout, contextProfile.MaxTimeout,
		)
		profile.MaxOutputBytes = minPositiveSafetyInt64(
			profile.MaxOutputBytes, contextProfile.MaxOutputBytes,
		)
	}
	return profile
}

func minPositiveSafetyDuration(a, b safety.Duration) safety.Duration {
	if a <= 0 || b > 0 && b < a {
		return b
	}
	return a
}

func minPositiveSafetyInt64(a, b int64) int64 {
	if a <= 0 || b > 0 && b < a {
		return b
	}
	return a
}

func cappedSafetyTimeout(
	guard *safety.Guard,
	toolName string,
	requested *int,
) *int {
	profile := safetyToolProfile(guard, toolName)
	return cappedSafetyTimeoutForProfile(profile, requested)
}

func cappedSafetyTimeoutForProfile(
	profile safety.ToolProfile,
	requested *int,
) *int {
	if profile.MaxTimeout <= 0 {
		return requested
	}
	maxSeconds := int(time.Duration(profile.MaxTimeout) / time.Second)
	if maxSeconds <= 0 {
		return requested
	}
	if requested == nil || *requested <= 0 || *requested > maxSeconds {
		return &maxSeconds
	}
	return requested
}

func safetyTimeoutForScan(
	guard *safety.Guard,
	toolName string,
	requested *int,
) *int {
	if requested != nil && *requested > 0 {
		return requested
	}
	profile := safetyToolProfile(guard, toolName)
	return safetyTimeoutForProfile(profile, requested)
}

func safetyTimeoutForProfile(
	profile safety.ToolProfile,
	requested *int,
) *int {
	if requested != nil && *requested > 0 {
		return requested
	}
	maxSeconds := int(time.Duration(profile.MaxTimeout) / time.Second)
	if maxSeconds <= 0 {
		return requested
	}
	return &maxSeconds
}

func mergeInheritedEnv(baseEnv, extra map[string]string) map[string]string {
	out := make(map[string]string, len(os.Environ())+len(baseEnv)+len(extra))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			out[key] = value
		}
	}
	for key, value := range envscrub.Scrub(
		mergeExplicitEnv(baseEnv, extra), true,
	) {
		out[key] = value
	}
	return out
}

func safetyExecutionEnv(baseEnv, extra map[string]string) map[string]string {
	out := make(map[string]string, len(os.Environ())+len(baseEnv)+len(extra))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || !safeInheritedHostEnvKey(key) {
			continue
		}
		if _, changed := safety.RedactValue(map[string]string{key: value}); changed {
			continue
		}
		out[key] = value
	}
	for key, value := range envscrub.Scrub(baseEnv, true) {
		out[key] = value
	}
	for key, value := range envscrub.Scrub(extra, true) {
		if protectedHostRuntimeEnvKey(key) {
			continue
		}
		out[key] = value
	}
	return out
}

func protectedHostRuntimeEnvKey(key string) bool {
	switch strings.ToUpper(key) {
	case "PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT":
		return true
	default:
		return false
	}
}

func safetyScanEnv(baseEnv, extra map[string]string) map[string]string {
	out := make(map[string]string, len(os.Environ())+len(baseEnv)+len(extra))
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" || !safeInheritedHostEnvKey(key) || envscrub.IsBlocked(key, true) {
			continue
		}
		if _, changed := safety.RedactValue(map[string]string{key: value}); changed {
			continue
		}
		out[key] = value
	}
	for key, value := range mergeExplicitEnv(baseEnv, extra) {
		out[key] = value
	}
	return out
}

func safeInheritedHostEnvKey(key string) bool {
	upper := strings.ToUpper(key)
	switch upper {
	case "PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT",
		"TEMP", "TMP", "TMPDIR", "LANG", "LANGUAGE", "TZ", "TERM",
		"USER", "USERNAME":
		return true
	default:
		return strings.HasPrefix(upper, "LC_")
	}
}

func mergeExplicitEnv(baseEnv, extra map[string]string) map[string]string {
	if len(baseEnv) == 0 && len(extra) == 0 {
		return nil
	}
	out := cloneEnvMap(baseEnv)
	if out == nil {
		out = make(map[string]string, len(extra))
	}
	for key, value := range extra {
		if strings.TrimSpace(key) != "" {
			out[key] = value
		}
	}
	return out
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
