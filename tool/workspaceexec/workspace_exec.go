//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package workspaceexec exposes shared executor-workspace tools such as
// workspace_exec and workspace_save_artifact.
package workspaceexec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/programsession"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspaceinput"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspaceprep"
	"trpc.group/trpc-go/trpc-agent-go/internal/workspacesession"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultWorkspaceExecTimeout = 5 * time.Minute
	defaultWorkspaceWriteYield  = 200
)

// ExecTool executes shell commands in the shared executor workspace.
type ExecTool struct {
	exec      codeexecutor.CodeExecutor
	reg       *codeexecutor.WorkspaceRegistry
	resolver  *workspacesession.Resolver
	sessional bool

	// providers contribute workspace requirements (loaded skills,
	// bootstrap files, conversation files, ...). When non-empty,
	// reconciler is always non-nil. These fields are populated only
	// through the WithWorkspaceBootstrap / WithLoadedSkills options
	// so internal workspaceprep types never leak into this tool's
	// public surface.
	providers              []workspaceprep.Provider
	reconciler             workspaceprep.Reconciler
	conversationFilesWired bool

	mu       sync.Mutex
	sessions map[string]*execSession
	ttl      time.Duration
	clock    func() time.Time
}

// WriteStdinTool sends additional stdin to a running workspace_exec session.
type WriteStdinTool struct {
	exec *ExecTool
}

// KillSessionTool terminates a running workspace_exec session.
type KillSessionTool struct {
	exec *ExecTool
}

type execSession struct {
	mu sync.Mutex

	proc        codeexecutor.ProgramSession
	exitedAt    time.Time
	finalized   bool
	finalizedAt time.Time
}

type execInput struct {
	Command       string            `json:"command"`
	Cwd           string            `json:"cwd,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	Stdin         string            `json:"stdin,omitempty"`
	YieldTimeMS   *int              `json:"yield_time_ms,omitempty"`
	YieldMs       *int              `json:"yieldMs,omitempty"`
	Background    bool              `json:"background,omitempty"`
	Timeout       int               `json:"timeout,omitempty"`
	TimeoutSec    *int              `json:"timeout_sec,omitempty"`
	TimeoutSecOld *int              `json:"timeoutSec,omitempty"`
	TTY           *bool             `json:"tty,omitempty"`
	PTY           *bool             `json:"pty,omitempty"`
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

type killInput struct {
	SessionID    string `json:"session_id,omitempty"`
	SessionIDOld string `json:"sessionId,omitempty"`
}

type execOutput struct {
	Status     string `json:"status"`
	Output     string `json:"output,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	SessionID  string `json:"session_id,omitempty"`
	Offset     int    `json:"offset"`
	NextOffset int    `json:"next_offset"`
}

type execRequest struct {
	background bool
	tty        bool
	yield      *int
	eng        codeexecutor.Engine
	ws         codeexecutor.Workspace
	spec       codeexecutor.RunProgramSpec
}

type killOutput struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

// NewExecTool creates a workspace_exec tool for the provided executor.
func NewExecTool(
	exec codeexecutor.CodeExecutor,
	opts ...func(*ExecTool),
) *ExecTool {
	tl := &ExecTool{
		exec:      exec,
		sessional: supportsInteractiveSessions(exec),
		sessions:  map[string]*execSession{},
		ttl:       programsession.DefaultSessionTTL,
		clock:     time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(tl)
		}
	}
	tl.resolver = workspacesession.NewResolver(exec, tl.reg)
	return tl
}

// NewWriteStdinTool creates a tool for continuing or polling a running session.
func NewWriteStdinTool(exec *ExecTool) *WriteStdinTool {
	return &WriteStdinTool{exec: exec}
}

// NewKillSessionTool creates a tool for terminating a running session.
func NewKillSessionTool(exec *ExecTool) *KillSessionTool {
	return &KillSessionTool{exec: exec}
}

// WithWorkspaceRegistry reuses a caller-provided workspace registry so
// workspace_exec can share the same invocation workspace with other tools.
func WithWorkspaceRegistry(
	reg *codeexecutor.WorkspaceRegistry,
) func(*ExecTool) {
	return func(t *ExecTool) {
		t.reg = reg
	}
}

// WithWorkspaceBootstrap declares static files and one-shot commands
// that must exist/run in the workspace before workspace_exec runs
// user commands. The spec is converted into reconciler Requirements
// internally; idempotency and skip-on-fingerprint-match are handled
// by the framework. Files are staged first, then commands run, both
// in declaration order.
//
// A malformed spec panics during option application: silently
// dropping a partially-configured bootstrap would leave the agent
// running in a state the caller did not ask for.
//
// Passing an empty spec (no files, no commands) is a no-op.
func WithWorkspaceBootstrap(
	spec codeexecutor.WorkspaceBootstrapSpec,
) func(*ExecTool) {
	if len(spec.Files) == 0 && len(spec.Commands) == 0 {
		return func(*ExecTool) {}
	}
	provider, err := workspaceprep.NewBootstrapProvider(
		toInternalBootstrapSpec(spec),
	)
	if err != nil {
		panic(fmt.Sprintf(
			"workspaceexec: invalid WorkspaceBootstrapSpec: %v", err,
		))
	}
	return func(t *ExecTool) {
		t.addPreparer(provider)
	}
}

// WithLoadedSkills wires the reconciler to materialize skills that
// have been recorded in session state via skill_load, using the
// supplied repository to resolve skill sources. Skills are staged
// into skills/<name> as writable working copies.
//
// Passing a nil repository is a no-op.
func WithLoadedSkills(repo skill.Repository) func(*ExecTool) {
	if repo == nil {
		return func(*ExecTool) {}
	}
	provider, err := workspaceprep.NewLoadedSkillsProvider(repo)
	if err != nil {
		panic(fmt.Sprintf(
			"workspaceexec: cannot build loaded-skills provider: %v",
			err,
		))
	}
	return func(t *ExecTool) {
		t.addPreparer(provider)
	}
}

// addPreparer records a provider and ensures the companion pieces
// (default reconciler, conversation-files provider) are installed.
// It is only used by the options above so that callers never see
// an internal workspaceprep type.
func (t *ExecTool) addPreparer(p workspaceprep.Provider) {
	if t.reconciler == nil {
		t.reconciler = workspaceprep.NewReconciler()
	}
	t.providers = append(t.providers, p)
	if !t.conversationFilesWired {
		t.providers = append(
			t.providers, workspaceprep.NewConversationFilesProvider(),
		)
		t.conversationFilesWired = true
	}
}

// toInternalBootstrapSpec bridges codeexecutor.WorkspaceBootstrapSpec
// (the stable public type) to workspaceprep.BootstrapSpec (the
// internal representation). Keeping the two struct families
// nominally distinct lets the public surface evolve independently of
// the reconciler internals.
func toInternalBootstrapSpec(
	in codeexecutor.WorkspaceBootstrapSpec,
) workspaceprep.BootstrapSpec {
	out := workspaceprep.BootstrapSpec{
		Files:    make([]workspaceprep.FileSpec, 0, len(in.Files)),
		Commands: make([]workspaceprep.CommandSpec, 0, len(in.Commands)),
	}
	for _, f := range in.Files {
		out.Files = append(out.Files, workspaceprep.FileSpec{
			Key:      f.Key,
			Target:   f.Target,
			Content:  f.Content,
			Mode:     f.Mode,
			Input:    f.Input,
			Optional: f.Optional,
		})
	}
	for _, c := range in.Commands {
		out.Commands = append(out.Commands, workspaceprep.CommandSpec{
			Key:               c.Key,
			Cmd:               c.Cmd,
			Args:              c.Args,
			Env:               c.Env,
			Cwd:               c.Cwd,
			Timeout:           c.Timeout,
			MarkerPath:        c.MarkerPath,
			ObservedPaths:     c.ObservedPaths,
			FingerprintInputs: c.FingerprintInputs,
			FingerprintSalt:   c.FingerprintSalt,
			Optional:          c.Optional,
		})
	}
	return out
}

// Declaration returns the schema for workspace_exec.
func (t *ExecTool) Declaration() *tool.Declaration {
	desc := "Execute a shell command inside the current " +
		"executor workspace. This is the general shell runner " +
		"for shared executor-side work that does not depend on " +
		"a specific skill, including file operations, " +
		"repo inspection, validation commands, and " +
		"environment-dependent network commands such as curl or " +
		"git. It does not require a skill name and runs in the " +
		"shared executor workspace, not on the agent host. " +
		"Conversation file inputs are staged automatically under " +
		"work/inputs when available."
	outputDesc := "Result of workspace_exec. The output field is aggregated terminal text and does not guarantee preservation of the original stdout/stderr interleaving."
	props := map[string]*tool.Schema{
		"command": {
			Type:        "string",
			Description: "Shell command to execute.",
		},
		"cwd": {
			Type: "string",
			Description: "Optional workspace-relative cwd. " +
				"Command paths are resolved relative to cwd. " +
				"Set cwd to a target directory and use paths relative to it, or leave cwd at the workspace root and use workspace-root paths.",
		},
		"env": {
			Type: "object",
			Description: "Optional environment overrides " +
				"for this command.",
			AdditionalProperties: &tool.Schema{Type: "string"},
		},
		"stdin": {
			Type:        "string",
			Description: "Optional initial stdin text.",
		},
		"timeout": {
			Type:        "integer",
			Description: "Alias for timeout_sec.",
		},
		"timeout_sec": {
			Type:        "integer",
			Description: "Maximum command runtime in seconds.",
		},
		"timeoutSec": {
			Type:        "integer",
			Description: "Alias for timeout_sec.",
		},
	}
	if t.sessional {
		props["yield_time_ms"] = &tool.Schema{
			Type: "integer",
			Description: "How long to wait before " +
				"returning. Use 0 to return as soon as possible.",
		}
		props["yieldMs"] = &tool.Schema{
			Type:        "integer",
			Description: "Alias for yield_time_ms.",
		}
		desc += " Set background=true or tty=true when the " +
			"command may need follow-up stdin."
		outputDesc += " When status is running, use " +
			"workspace_write_stdin to continue or poll."
		props["background"] = &tool.Schema{
			Type:        "boolean",
			Description: "Start the command and return a session when it keeps running.",
		}
		props["tty"] = &tool.Schema{
			Type:        "boolean",
			Description: "Allocate a TTY for interactive commands.",
		}
		props["pty"] = &tool.Schema{
			Type:        "boolean",
			Description: "Alias for tty.",
		}
	}
	return &tool.Declaration{
		Name:        "workspace_exec",
		Description: desc,
		InputSchema: &tool.Schema{
			Type:       "object",
			Required:   []string{"command"},
			Properties: props,
		},
		OutputSchema: execOutputSchema(outputDesc),
	}
}

// Declaration returns the schema for workspace_write_stdin.
func (t *WriteStdinTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "workspace_write_stdin",
		Description: "Write to a running workspace_exec session. " +
			"When chars is empty, this acts like a poll.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"session_id"},
			Properties: map[string]*tool.Schema{
				"session_id": {Type: "string", Description: "Session id returned by workspace_exec."},
				"sessionId":  {Type: "string", Description: "Alias for session_id."},
				"chars": {
					Type:        "string",
					Description: "Characters to write. Include \\n when the program expects Enter.",
				},
				"yield_time_ms": {Type: "integer", Description: "Optional wait before polling recent output."},
				"yieldMs":       {Type: "integer", Description: "Alias for yield_time_ms."},
				"append_newline": {
					Type:        "boolean",
					Description: "Append a newline after chars.",
				},
				"submit": {Type: "boolean", Description: "Alias for append_newline."},
			},
		},
		OutputSchema: execOutputSchema(
			"Result of a workspace_exec stdin write or follow-up poll. The output field is aggregated terminal text and does not guarantee preservation of the original stdout/stderr interleaving.",
		),
	}
}

// Declaration returns the schema for workspace_kill_session.
func (t *KillSessionTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "workspace_kill_session",
		Description: "Terminate a running workspace_exec session.",
		InputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"session_id"},
			Properties: map[string]*tool.Schema{
				"session_id": {Type: "string", Description: "Session id returned by workspace_exec."},
				"sessionId":  {Type: "string", Description: "Alias for session_id."},
			},
		},
		OutputSchema: &tool.Schema{
			Type:     "object",
			Required: []string{"ok", "session_id", "status"},
			Properties: map[string]*tool.Schema{
				"ok":         {Type: "boolean", Description: "True when the session was removed."},
				"session_id": {Type: "string", Description: "Session id."},
				"status":     {Type: "string", Description: "Final status."},
			},
		},
	}
}

// Call executes workspace_exec once or starts a resumable session.
func (t *ExecTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.resolver == nil {
		return nil, errors.New("workspace_exec is not configured")
	}
	in, err := parseExecInput(args)
	if err != nil {
		return nil, err
	}
	req, err := t.prepareExec(ctx, in)
	if err != nil {
		return nil, err
	}
	if t.sessional {
		return t.callSessional(ctx, req)
	}
	return t.callNonSessional(ctx, req)
}

func parseExecInput(args []byte) (execInput, error) {
	var in execInput
	if err := json.Unmarshal(args, &in); err != nil {
		return execInput{}, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(in.Command) == "" {
		return execInput{}, errors.New("command is required")
	}
	return in, nil
}

func (t *ExecTool) prepareExec(
	ctx context.Context,
	in execInput,
) (execRequest, error) {
	cwd, err := normalizeCWD(in.Cwd)
	if err != nil {
		return execRequest{}, err
	}
	eng, err := t.liveEngine()
	if err != nil {
		return execRequest{}, err
	}
	ws, err := t.resolver.CreateWorkspace(ctx, eng, "workspace")
	if err != nil {
		return execRequest{}, err
	}
	if err := t.reconcileWorkspace(ctx, eng, ws); err != nil {
		return execRequest{}, err
	}
	timeout := firstIntValue(in.TimeoutSec, in.TimeoutSecOld)
	if timeout <= 0 {
		timeout = in.Timeout
	}
	return execRequest{
		background: in.Background,
		tty:        firstBoolValue(in.TTY, in.PTY),
		yield:      firstIntPtr(in.YieldTimeMS, in.YieldMs),
		eng:        eng,
		ws:         ws,
		spec: codeexecutor.RunProgramSpec{
			Cmd:     "sh",
			Args:    []string{"-lc", in.Command},
			Env:     in.Env,
			Cwd:     cwd,
			Stdin:   in.Stdin,
			Timeout: execTimeout(timeout),
		},
	}, nil
}

func (t *ExecTool) callNonSessional(
	ctx context.Context,
	req execRequest,
) (execOutput, error) {
	if req.background || req.tty {
		return execOutput{}, errors.New(
			"workspace_exec interactive sessions are not supported by the current executor",
		)
	}
	out, err := runOneShot(ctx, req.eng, req.ws, req.spec)
	if err != nil {
		return execOutput{}, err
	}
	return out, nil
}

func (t *ExecTool) callSessional(
	ctx context.Context,
	req execRequest,
) (execOutput, error) {
	if !req.background && !req.tty && (req.yield == nil || *req.yield == 0) {
		out, err := runOneShot(ctx, req.eng, req.ws, req.spec)
		if err != nil {
			return execOutput{}, err
		}
		return out, nil
	}
	return t.startInteractive(ctx, req)
}

func runOneShot(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
	spec codeexecutor.RunProgramSpec,
) (execOutput, error) {
	rr, err := eng.Runner().RunProgram(ctx, ws, spec)
	if err != nil {
		return execOutput{}, err
	}
	return execOutput{
		Status:     codeexecutor.ProgramStatusExited,
		Output:     combineOutput(rr.Stdout, rr.Stderr),
		ExitCode:   intPtrValue(rr.ExitCode),
		Offset:     0,
		NextOffset: 0,
	}, nil
}

func (t *ExecTool) startInteractive(
	ctx context.Context,
	req execRequest,
) (execOutput, error) {
	runner, ok := req.eng.Runner().(codeexecutor.InteractiveProgramRunner)
	if !ok {
		return execOutput{}, errors.New(
			"workspace_exec interactive sessions are not supported by the current executor",
		)
	}
	proc, err := runner.StartProgram(
		ctx,
		req.ws,
		codeexecutor.InteractiveProgramSpec{
			RunProgramSpec: req.spec,
			TTY:            req.tty,
		},
	)
	if err != nil {
		return execOutput{}, err
	}
	t.putSession(proc.ID(), &execSession{proc: proc})
	poll := initialPoll(proc, req.background, req.yield)
	out := pollOutput(proc.ID(), poll)
	if poll.Status == codeexecutor.ProgramStatusExited {
		if err := t.finalizeAndRemoveSession(proc.ID()); err != nil {
			out.SessionID = proc.ID()
		}
	}
	return out, nil
}

func initialPoll(
	proc codeexecutor.ProgramSession,
	background bool,
	yield *int,
) codeexecutor.ProgramPoll {
	if background && (yield == nil || *yield == 0) {
		return proc.Poll(programsession.PollLineLimit(0))
	}
	return programsession.WaitForProgramOutput(
		proc,
		execYield(background, yield),
		programsession.PollLineLimit(0),
	)
}

// Call writes stdin to an interactive workspace_exec session or polls it.
func (t *WriteStdinTool) Call(ctx context.Context, args []byte) (any, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_write_stdin is not configured")
	}
	var in writeInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	sessionID := firstNonEmpty(in.SessionID, in.SessionIDOld)
	if sessionID == "" {
		return nil, errors.New("session_id is required")
	}

	sess, err := t.exec.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	appendNewline := firstBoolValue(in.AppendNewline, in.Submit)
	if in.Chars != "" || appendNewline {
		if err := sess.proc.Write(in.Chars, appendNewline); err != nil {
			return nil, err
		}
	}

	yield := firstIntPtr(in.YieldTimeMS, in.YieldMs)
	poll := programsession.WaitForProgramOutput(
		sess.proc,
		writeYield(yield),
		programsession.PollLineLimit(0),
	)
	out := pollOutput(sessionID, poll)
	if poll.Status == codeexecutor.ProgramStatusExited {
		if err := t.exec.finalizeAndRemoveSession(sessionID); err != nil {
			out.SessionID = sessionID
		}
	}
	return out, nil
}

// Call terminates a running workspace_exec session.
func (t *KillSessionTool) Call(_ context.Context, args []byte) (any, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_kill_session is not configured")
	}
	var in killInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	sessionID := firstNonEmpty(in.SessionID, in.SessionIDOld)
	if sessionID == "" {
		return nil, errors.New("session_id is required")
	}
	sess, err := t.exec.getSession(sessionID)
	if err != nil {
		return nil, err
	}
	status := codeexecutor.ProgramStatusExited
	poll := sess.proc.Poll(nil)
	if poll.Status == codeexecutor.ProgramStatusRunning {
		if err := sess.proc.Kill(programsession.DefaultSessionKill); err != nil {
			return nil, err
		}
		status = "killed"
	}
	if err := t.exec.finalizeAndRemoveSession(sessionID); err != nil {
		return nil, err
	}
	return killOutput{
		OK:        true,
		SessionID: sessionID,
		Status:    status,
	}, nil
}

// reconcileWorkspace converges the workspace to the desired state
// before executing a user command. When no provider is configured
// the function preserves the legacy behavior of staging conversation
// files inline; otherwise it delegates to the reconciler which
// collects Requirements from every provider and applies them in
// phase order (file -> skill -> command).
func (t *ExecTool) reconcileWorkspace(
	ctx context.Context,
	eng codeexecutor.Engine,
	ws codeexecutor.Workspace,
) error {
	if t == nil || len(t.providers) == 0 {
		_, warnings := workspaceinput.StageConversationFiles(ctx, eng, ws)
		for _, warning := range warnings {
			log.WarnfContext(
				ctx,
				"workspace_exec input staging warning: %s",
				warning,
			)
		}
		return nil
	}
	inv, _ := agent.InvocationFromContext(ctx)
	var all []workspaceprep.Requirement
	for _, p := range t.providers {
		reqs, err := p.Requirements(ctx, inv)
		if err != nil {
			return fmt.Errorf(
				"workspace_exec provider %s: %w", p.Name(), err,
			)
		}
		all = append(all, reqs...)
	}
	warnings, err := t.reconciler.Reconcile(ctx, eng, ws, all)
	for _, warning := range warnings {
		log.WarnfContext(
			ctx,
			"workspace_exec reconcile warning: %s",
			warning,
		)
	}
	if err != nil {
		return fmt.Errorf("workspace_exec reconcile: %w", err)
	}
	return nil
}

func (t *ExecTool) liveEngine() (codeexecutor.Engine, error) {
	if t == nil || t.exec == nil {
		return nil, errors.New("workspace_exec requires an executor")
	}
	ep, ok := t.exec.(codeexecutor.EngineProvider)
	if !ok || ep == nil {
		return nil, errors.New(
			"workspace_exec requires an executor that exposes EngineProvider",
		)
	}
	eng := ep.Engine()
	if eng == nil || eng.Manager() == nil || eng.Runner() == nil {
		return nil, errors.New(
			"workspace_exec requires an executor with live workspace support",
		)
	}
	return eng, nil
}

func (t *ExecTool) putSession(id string, sess *execSession) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanupExpiredLocked()
	t.sessions[id] = sess
}

func (t *ExecTool) getSession(id string) (*execSession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanupExpiredLocked()
	sess, ok := t.sessions[id]
	if !ok {
		return nil, fmt.Errorf("unknown session_id: %s", id)
	}
	return sess, nil
}

func (t *ExecTool) removeSession(id string) (*execSession, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cleanupExpiredLocked()
	sess, ok := t.sessions[id]
	if !ok {
		return nil, fmt.Errorf("unknown session_id: %s", id)
	}
	delete(t.sessions, id)
	return sess, nil
}

func (t *ExecTool) finalizeAndRemoveSession(id string) error {
	sess, err := t.getSession(id)
	if err != nil {
		return err
	}
	t.markSessionFinalized(sess)
	if err := sess.proc.Close(); err != nil {
		return err
	}
	_, err = t.removeSession(id)
	return err
}

func (t *ExecTool) cleanupExpiredLocked() {
	if t.ttl <= 0 {
		return
	}
	now := t.clock()
	for id, sess := range t.sessions {
		sess.mu.Lock()
		if sess.exitedAt.IsZero() {
			if state, ok := programsession.State(sess.proc); ok &&
				state.Status == codeexecutor.ProgramStatusExited {
				sess.exitedAt = now
			}
		}
		expired := !sess.exitedAt.IsZero() &&
			now.Sub(sess.exitedAt) >= t.ttl
		sess.mu.Unlock()
		if expired {
			if err := sess.proc.Close(); err == nil {
				delete(t.sessions, id)
			}
		}
	}
}

func (t *ExecTool) markSessionFinalized(sess *execSession) {
	sess.mu.Lock()
	defer sess.mu.Unlock()
	now := t.clock()
	sess.finalized = true
	sess.finalizedAt = now
	sess.exitedAt = now
}

func normalizeCWD(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "\\", "/")
	if s == "" {
		return ".", nil
	}
	if hasGlobMeta(s) {
		return "", errors.New("cwd must not contain glob patterns")
	}
	if isWorkspaceEnvPath(s) {
		out := codeexecutor.NormalizeGlobs([]string{s})
		if len(out) == 0 {
			return "", errors.New("invalid cwd")
		}
		s = out[0]
	}
	if strings.HasPrefix(s, "/") {
		rel := strings.TrimPrefix(path.Clean(s), "/")
		if rel == "" || rel == "." {
			return ".", nil
		}
		if !isAllowedWorkspacePath(rel) {
			return "", fmt.Errorf("cwd must stay under workspace roots: %q", raw)
		}
		return rel, nil
	}
	rel := path.Clean(s)
	if rel == "." {
		return ".", nil
	}
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", errors.New("cwd must stay within the workspace")
	}
	if !isAllowedWorkspacePath(rel) {
		return "", fmt.Errorf(
			"cwd must stay under supported workspace roots such as work/, out/, or runs/: %q",
			raw,
		)
	}
	return rel, nil
}

func supportsInteractiveSessions(exec codeexecutor.CodeExecutor) bool {
	if exec == nil {
		return false
	}
	provider, ok := exec.(codeexecutor.EngineProvider)
	if !ok {
		return false
	}
	eng := provider.Engine()
	if eng == nil {
		return false
	}
	_, ok = eng.Runner().(codeexecutor.InteractiveProgramRunner)
	return ok
}

func execOutputSchema(desc string) *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: desc,
		Required:    []string{"status", "offset", "next_offset"},
		Properties: map[string]*tool.Schema{
			"status":      {Type: "string", Description: "running or exited"},
			"output":      {Type: "string", Description: "Aggregated terminal text observed for this call. It may combine stdout and stderr and does not guarantee preservation of their original interleaving."},
			"exit_code":   {Type: "integer", Description: "Exit code when the session has exited."},
			"session_id":  {Type: "string", Description: "Interactive session id when still running."},
			"offset":      {Type: "integer", Description: "Start offset of returned output."},
			"next_offset": {Type: "integer", Description: "Next output offset."},
		},
	}
}

func pollOutput(sessionID string, poll codeexecutor.ProgramPoll) execOutput {
	out := execOutput{
		Status:     poll.Status,
		Output:     poll.Output,
		ExitCode:   poll.ExitCode,
		Offset:     poll.Offset,
		NextOffset: poll.NextOffset,
	}
	if poll.Status == codeexecutor.ProgramStatusRunning {
		out.SessionID = sessionID
	}
	return out
}

func hasGlobMeta(s string) bool {
	return strings.ContainsAny(s, "*?[")
}

func execTimeout(raw int) time.Duration {
	if raw <= 0 {
		return defaultWorkspaceExecTimeout
	}
	return time.Duration(raw) * time.Second
}

func execYield(background bool, raw *int) time.Duration {
	if background {
		if raw != nil && *raw > 0 {
			return time.Duration(*raw) * time.Millisecond
		}
		return 0
	}
	val := 0
	if raw != nil {
		val = *raw
	}
	return programsession.YieldDuration(val, programsession.DefaultExecYieldMS)
}

func writeYield(raw *int) time.Duration {
	val := defaultWorkspaceWriteYield
	if raw != nil && *raw >= 0 {
		val = *raw
	}
	return time.Duration(val) * time.Millisecond
}

func combineOutput(stdout, stderr string) string {
	switch {
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + stderr
	}
}

func firstIntPtr(vs ...*int) *int {
	for _, v := range vs {
		if v != nil {
			return v
		}
	}
	return nil
}

func firstIntValue(vs ...*int) int {
	for _, v := range vs {
		if v != nil {
			return *v
		}
	}
	return 0
}

func firstBoolValue(vs ...*bool) bool {
	for _, v := range vs {
		if v != nil {
			return *v
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if s := strings.TrimSpace(value); s != "" {
			return s
		}
	}
	return ""
}

func intPtrValue(v int) *int {
	return &v
}

const (
	envVarPrefix = "$"
	envVarLBrace = "${"
	envVarRBrace = "}"
)

func hasEnvPrefix(s string, name string) bool {
	if strings.HasPrefix(s, envVarPrefix+name) {
		tail := s[len(envVarPrefix+name):]
		return tail == "" || strings.HasPrefix(tail, "/") || strings.HasPrefix(tail, "\\")
	}
	prefix := envVarLBrace + name + envVarRBrace
	if strings.HasPrefix(s, prefix) {
		tail := s[len(prefix):]
		return tail == "" || strings.HasPrefix(tail, "/") || strings.HasPrefix(tail, "\\")
	}
	return false
}

func isWorkspaceEnvPath(s string) bool {
	return hasEnvPrefix(s, codeexecutor.WorkspaceEnvDirKey) ||
		hasEnvPrefix(s, codeexecutor.EnvSkillsDir) ||
		hasEnvPrefix(s, codeexecutor.EnvWorkDir) ||
		hasEnvPrefix(s, codeexecutor.EnvOutputDir) ||
		hasEnvPrefix(s, codeexecutor.EnvRunDir)
}

func isAllowedWorkspacePath(rel string) bool {
	switch {
	case rel == codeexecutor.DirSkills || strings.HasPrefix(rel, codeexecutor.DirSkills+"/"):
		return true
	case rel == codeexecutor.DirWork || strings.HasPrefix(rel, codeexecutor.DirWork+"/"):
		return true
	case rel == codeexecutor.DirOut || strings.HasPrefix(rel, codeexecutor.DirOut+"/"):
		return true
	case rel == codeexecutor.DirRuns || strings.HasPrefix(rel, codeexecutor.DirRuns+"/"):
		return true
	default:
		return false
	}
}

var _ tool.Tool = (*ExecTool)(nil)
var _ tool.CallableTool = (*ExecTool)(nil)
var _ tool.Tool = (*WriteStdinTool)(nil)
var _ tool.CallableTool = (*WriteStdinTool)(nil)
var _ tool.Tool = (*KillSessionTool)(nil)
var _ tool.CallableTool = (*KillSessionTool)(nil)
