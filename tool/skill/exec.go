//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package skill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/internal/programsession"
	"trpc.group/trpc-go/trpc-agent-go/internal/toolcache"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	defaultExecYieldMS = programsession.DefaultExecYieldMS
	defaultIOYieldMS   = programsession.DefaultIOYieldMS
	defaultPollLines   = programsession.DefaultPollLines
	defaultSessionTTL  = programsession.DefaultSessionTTL
	defaultSessionKill = programsession.DefaultSessionKill
)

const (
	interactionKindPrompt    = "prompt"
	interactionKindSelection = "selection"
)

type execInput struct {
	runInput
	TTY       bool `json:"tty,omitempty"`
	YieldMS   int  `json:"yield_ms,omitempty"`
	PollLines int  `json:"poll_lines,omitempty"`
}

type sessionWriteInput struct {
	SessionID string `json:"session_id"`
	Chars     string `json:"chars,omitempty"`
	Submit    bool   `json:"submit,omitempty"`
	YieldMS   int    `json:"yield_ms,omitempty"`
	PollLines int    `json:"poll_lines,omitempty"`
}

type sessionPollInput struct {
	SessionID string `json:"session_id"`
	YieldMS   int    `json:"yield_ms,omitempty"`
	PollLines int    `json:"poll_lines,omitempty"`
}

type sessionKillInput struct {
	SessionID string `json:"session_id"`
}

type sessionInteraction struct {
	NeedsInput bool   `json:"needs_input"`
	Kind       string `json:"kind,omitempty"`
	Hint       string `json:"hint,omitempty"`
}

type execOutput struct {
	Status      string              `json:"status"`
	SessionID   string              `json:"session_id"`
	Output      string              `json:"output,omitempty"`
	Offset      int                 `json:"offset"`
	NextOffset  int                 `json:"next_offset"`
	ExitCode    *int                `json:"exit_code,omitempty"`
	Interaction *sessionInteraction `json:"interaction,omitempty"`
	Result      *runOutput          `json:"result,omitempty"`
}

type sessionKillOutput struct {
	OK        bool   `json:"ok"`
	SessionID string `json:"session_id"`
	Status    string `json:"status"`
}

type execSession struct {
	mu sync.Mutex

	proc codeexecutor.ProgramSession
	eng  codeexecutor.Engine
	ws   codeexecutor.Workspace

	in                    runInput
	staged                []stagedInput
	stageWarnings         []string
	saveRequested         bool
	outputsSaveSkipReason string
	final                 *runOutput
	exitedAt              time.Time
	finalized             bool
	finalizedAt           time.Time
}

// ExecTool starts interactive commands inside skill workspaces.
type ExecTool struct {
	run *RunTool

	mu       sync.Mutex
	sessions map[string]*execSession
	ttl      time.Duration
	clock    func() time.Time
}

// WriteStdinTool writes stdin to running skill sessions.
type WriteStdinTool struct {
	exec *ExecTool
}

// PollSessionTool polls running skill sessions for new output.
type PollSessionTool struct {
	exec *ExecTool
}

// KillSessionTool terminates running skill sessions.
type KillSessionTool struct {
	exec *ExecTool
}

// NewExecTool creates the interactive skill execution tool.
func NewExecTool(run *RunTool) *ExecTool {
	return &ExecTool{
		run:      run,
		sessions: map[string]*execSession{},
		ttl:      defaultSessionTTL,
		clock:    time.Now,
	}
}

// NewWriteStdinTool creates the stdin write tool for skill_exec sessions.
func NewWriteStdinTool(exec *ExecTool) *WriteStdinTool {
	return &WriteStdinTool{exec: exec}
}

// NewPollSessionTool creates the poll tool for skill_exec sessions.
func NewPollSessionTool(exec *ExecTool) *PollSessionTool {
	return &PollSessionTool{exec: exec}
}

// NewKillSessionTool creates the kill tool for skill_exec sessions.
func NewKillSessionTool(exec *ExecTool) *KillSessionTool {
	return &KillSessionTool{exec: exec}
}

// Declaration returns the tool schema for skill_exec.
func (t *ExecTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "skill_exec",
		Description: "Start an interactive command inside a skill " +
			"workspace. Use it when a skill command may prompt for " +
			"stdin, selection, or TTY interaction. It shares the " +
			"same workspace, inputs, outputs, artifact, stdin, and " +
			"editor_text semantics as skill_run.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Start an interactive skill session",
			Required:    []string{"skill", "command"},
			Properties: map[string]*tool.Schema{
				"skill":   skillNameSchema(t.run.repo, "Skill name"),
				"command": {Type: "string", Description: "Command"},
				"cwd":     {Type: "string", Description: "Working dir"},
				"env": {
					Type:        "object",
					Description: "Env vars",
					AdditionalProperties: &tool.Schema{
						Type: "string",
					},
				},
				"stdin": {
					Type:        "string",
					Description: "Optional initial stdin",
				},
				"editor_text": {
					Type: "string",
					Description: "Optional text for $EDITOR-driven " +
						"CLIs",
				},
				"tty": {
					Type:        "boolean",
					Description: "Allocate a TTY when needed",
				},
				"yield_ms": {
					Type: "integer",
					Description: "How long to wait for initial " +
						"output before returning",
				},
				"poll_lines": {
					Type: "integer",
					Description: "Maximum number of new lines to " +
						"return per call",
				},
				"output_files": {Type: "array",
					Items: &tool.Schema{Type: "string"},
					Description: "Workspace-relative paths/globs to " +
						"collect after exit"},
				"timeout": {Type: "integer", Description: "Seconds"},
				"save_as_artifacts": {Type: "boolean", Description: "" +
					"Persist collected files via Artifact service"},
				"omit_inline_content": {Type: "boolean", Description: "" +
					"Omit inline output file content"},
				"artifact_prefix": {Type: "string", Description: "" +
					"Artifact name prefix"},
				"inputs":  inputSpecsSchema(),
				"outputs": outputSpecSchema(),
			},
		},
		OutputSchema: execOutputSchema(
			"Structured result of skill_exec and related session " +
				"tools. When status is running, inspect output and " +
				"interaction. When status is exited, result carries " +
				"the final run output.",
		),
	}
}

// Declaration returns the tool schema for skill_write_stdin.
func (t *WriteStdinTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "skill_write_stdin",
		Description: "Write to a running skill_exec session. Set " +
			"submit=true to append a newline. When chars is empty " +
			"and submit is false, it acts like a lightweight poll.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Write stdin to a skill session",
			Required:    []string{"session_id"},
			Properties: map[string]*tool.Schema{
				"session_id": {
					Type:        "string",
					Description: "Session id from skill_exec",
				},
				"chars": {
					Type:        "string",
					Description: "Stdin text to write",
				},
				"submit": {
					Type:        "boolean",
					Description: "Append a newline after chars",
				},
				"yield_ms": {
					Type:        "integer",
					Description: "How long to wait for new output",
				},
				"poll_lines": {
					Type: "integer",
					Description: "Maximum number of new lines to " +
						"return",
				},
			},
		},
		OutputSchema: execOutputSchema(
			"Structured result of a stdin write or follow-up poll.",
		),
	}
}

// Declaration returns the tool schema for skill_poll_session.
func (t *PollSessionTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name: "skill_poll_session",
		Description: "Poll a running or recently exited skill_exec " +
			"session for additional output or final results.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Poll a skill session",
			Required:    []string{"session_id"},
			Properties: map[string]*tool.Schema{
				"session_id": {
					Type:        "string",
					Description: "Session id from skill_exec",
				},
				"yield_ms": {
					Type:        "integer",
					Description: "How long to wait for new output",
				},
				"poll_lines": {
					Type: "integer",
					Description: "Maximum number of new lines to " +
						"return",
				},
			},
		},
		OutputSchema: execOutputSchema(
			"Structured result of a skill session poll.",
		),
	}
}

// Declaration returns the tool schema for skill_kill_session.
func (t *KillSessionTool) Declaration() *tool.Declaration {
	return &tool.Declaration{
		Name:        "skill_kill_session",
		Description: "Terminate and remove a skill_exec session.",
		InputSchema: &tool.Schema{
			Type:        "object",
			Description: "Kill a skill session",
			Required:    []string{"session_id"},
			Properties: map[string]*tool.Schema{
				"session_id": {
					Type:        "string",
					Description: "Session id from skill_exec",
				},
			},
		},
		OutputSchema: &tool.Schema{
			Type:        "object",
			Description: "Result of killing a skill session.",
			Required:    []string{"ok", "session_id", "status"},
			Properties: map[string]*tool.Schema{
				"ok": {
					Type:        "boolean",
					Description: "True when the session was removed",
				},
				"session_id": {
					Type:        "string",
					Description: "Session id",
				},
				"status": {
					Type:        "string",
					Description: "Final status",
				},
			},
		},
	}
}

// StreamableCall starts an interactive skill session and streams output.
func (t *ExecTool) StreamableCall(
	ctx context.Context,
	args []byte,
) (*tool.StreamReader, error) {
	in, err := parseExecArgs(args)
	if err != nil {
		return nil, err
	}
	if t.run.requireSkillLoaded &&
		!isSkillLoadedInContext(ctx, in.Skill) {
		return nil, fmt.Errorf(
			"skill_exec requires skill_load first for %q",
			in.Skill,
		)
	}

	runIn, saveRequested, outputsSaveSkipReason := t.run.
		applyArtifactSaveOverrides(ctx, in.runInput)
	in.runInput = runIn
	eng, ws, skillRoot, ctxIO, staged, stageWarn, err := t.run.
		prepareWorkspaceForRun(ctx, in.runInput)
	if err != nil {
		return nil, err
	}
	runner, ok := eng.Runner().(codeexecutor.InteractiveProgramRunner)
	if !ok {
		return nil, fmt.Errorf(
			"skill_exec is not supported by the current executor",
		)
	}
	cwd := resolveCWD(in.Cwd, skillRoot)
	spec, err := t.run.buildRunProgramSpec(
		ctxIO,
		eng,
		ws,
		skillRoot,
		cwd,
		in.runInput,
	)
	if err != nil {
		return nil, err
	}
	proc, err := runner.StartProgram(
		ctxIO,
		ws,
		codeexecutor.InteractiveProgramSpec{
			RunProgramSpec: spec,
			TTY:            in.TTY,
		},
	)
	if err != nil {
		return nil, err
	}

	sess := &execSession{
		proc:                  proc,
		eng:                   eng,
		ws:                    ws,
		in:                    in.runInput,
		staged:                staged,
		stageWarnings:         stageWarn,
		saveRequested:         saveRequested,
		outputsSaveSkipReason: outputsSaveSkipReason,
	}
	t.putSession(proc.ID(), sess)

	poll := waitForProgramOutput(
		proc,
		programsession.YieldDuration(in.YieldMS, defaultExecYieldMS),
		programsession.PollLineLimit(in.PollLines),
	)
	result, err := t.buildExecOutput(ctx, proc.ID(), sess, poll)
	if err != nil {
		return nil, err
	}
	return buildExecStream(poll.Output, result), nil
}

// StreamableCall writes stdin to a running skill session.
func (t *WriteStdinTool) StreamableCall(
	ctx context.Context,
	args []byte,
) (*tool.StreamReader, error) {
	if t.exec == nil {
		return nil, fmt.Errorf("skill_write_stdin is not configured")
	}
	in, err := parseSessionWriteArgs(args)
	if err != nil {
		return nil, err
	}
	sess, err := t.exec.getSession(in.SessionID)
	if err != nil {
		return nil, err
	}
	if in.Chars != "" || in.Submit {
		if err := sess.proc.Write(in.Chars, in.Submit); err != nil {
			return nil, err
		}
	}
	poll := waitForProgramOutput(
		sess.proc,
		programsession.YieldDuration(in.YieldMS, defaultIOYieldMS),
		programsession.PollLineLimit(in.PollLines),
	)
	result, err := t.exec.buildExecOutput(ctx, in.SessionID, sess, poll)
	if err != nil {
		return nil, err
	}
	return buildExecStream(poll.Output, result), nil
}

// StreamableCall polls a running skill session for new output.
func (t *PollSessionTool) StreamableCall(
	ctx context.Context,
	args []byte,
) (*tool.StreamReader, error) {
	if t.exec == nil {
		return nil, fmt.Errorf("skill_poll_session is not configured")
	}
	in, err := parseSessionPollArgs(args)
	if err != nil {
		return nil, err
	}
	sess, err := t.exec.getSession(in.SessionID)
	if err != nil {
		return nil, err
	}
	poll := waitForProgramOutput(
		sess.proc,
		programsession.YieldDuration(in.YieldMS, defaultIOYieldMS),
		programsession.PollLineLimit(in.PollLines),
	)
	result, err := t.exec.buildExecOutput(ctx, in.SessionID, sess, poll)
	if err != nil {
		return nil, err
	}
	return buildExecStream(poll.Output, result), nil
}

// Call terminates a running skill session and removes it.
func (t *KillSessionTool) Call(
	_ context.Context,
	args []byte,
) (any, error) {
	if t.exec == nil {
		return nil, fmt.Errorf("skill_kill_session is not configured")
	}
	var in sessionKillInput
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	sess, err := t.exec.getSession(in.SessionID)
	if err != nil {
		return nil, err
	}

	status := codeexecutor.ProgramStatusExited
	poll := sess.proc.Poll(nil)
	if poll.Status == codeexecutor.ProgramStatusRunning {
		if err := sess.proc.Kill(defaultSessionKill); err != nil {
			return nil, err
		}
		status = "killed"
	}
	_ = sess.proc.Close()
	if _, err := t.exec.removeSession(in.SessionID); err != nil {
		return nil, err
	}
	return sessionKillOutput{
		OK:        true,
		SessionID: in.SessionID,
		Status:    status,
	}, nil
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
			_ = sess.proc.Close()
			delete(t.sessions, id)
		}
	}
}

func (t *ExecTool) buildExecOutput(
	ctx context.Context,
	sessionID string,
	sess *execSession,
	poll codeexecutor.ProgramPoll,
) (execOutput, error) {
	result, err := t.captureFinalResult(ctx, sess, poll)
	if err != nil {
		return execOutput{}, err
	}
	out := execOutput{
		Status:     poll.Status,
		SessionID:  sessionID,
		Output:     poll.Output,
		Offset:     poll.Offset,
		NextOffset: poll.NextOffset,
		ExitCode:   poll.ExitCode,
		Result:     result,
	}
	out.Interaction = detectInteraction(poll)
	return out, nil
}

func (t *ExecTool) captureFinalResult(
	ctx context.Context,
	sess *execSession,
	poll codeexecutor.ProgramPoll,
) (*runOutput, error) {
	if poll.Status != codeexecutor.ProgramStatusExited {
		return nil, nil
	}

	sess.mu.Lock()
	defer sess.mu.Unlock()
	if sess.finalized {
		return sess.final, nil
	}

	ctxIO := withArtifactContext(ctx)
	autoFiles := t.run.autoExportWorkspaceOut(
		ctxIO,
		sess.eng,
		sess.ws,
		sess.in,
	)
	files, manifest, err := t.run.prepareOutputs(
		ctxIO,
		sess.eng,
		sess.ws,
		sess.in,
	)
	if err != nil {
		return nil, err
	}

	rr := sessionRunResult(sess.proc, poll)
	filteredOutputs := filterFailedEmptyOutputs(rr, files, manifest)
	files = filteredOutputs.files
	manifest = filteredOutputs.manifest
	out, err := t.run.buildRunOutput(
		ctx,
		rr,
		autoFiles,
		files,
		manifest,
		sess.in,
		sess.saveRequested,
		sess.outputsSaveSkipReason,
	)
	if err != nil {
		return nil, err
	}
	if len(filteredOutputs.warnings) > 0 {
		out.Warnings = append(out.Warnings, filteredOutputs.warnings...)
	}
	out.StagedInputs = sess.staged
	if len(sess.stageWarnings) > 0 {
		out.Warnings = append(out.Warnings, sess.stageWarnings...)
	}
	if len(filteredOutputs.omittedNames) > 0 {
		toolcache.DeleteSkillRunOutputFilesFromContext(
			ctx,
			filteredOutputs.omittedNames,
		)
	}
	toolcache.StoreSkillRunOutputFilesFromContext(ctx, files)

	sess.final = &out
	sess.finalized = true
	sess.finalizedAt = t.clock()
	sess.exitedAt = sess.finalizedAt
	_ = sess.proc.Close()
	return sess.final, nil
}

func sessionRunResult(
	proc codeexecutor.ProgramSession,
	poll codeexecutor.ProgramPoll,
) codeexecutor.RunResult {
	if provider, ok := proc.(codeexecutor.ProgramResultProvider); ok {
		return provider.RunResult()
	}
	log := proc.Log(nil, nil)
	res := codeexecutor.RunResult{
		Stdout: log.Output,
	}
	if poll.ExitCode != nil {
		res.ExitCode = *poll.ExitCode
	}
	return res
}

func buildExecStream(
	output string,
	result execOutput,
) *tool.StreamReader {
	stream := tool.NewStream(4)
	go func() {
		defer stream.Writer.Close()
		if output != "" {
			stream.Writer.Send(
				tool.StreamChunk{Content: output},
				nil,
			)
		}
		stream.Writer.Send(
			tool.StreamChunk{
				Content: tool.FinalResultChunk{Result: result},
			},
			nil,
		)
	}()
	return stream.Reader
}

func waitForProgramOutput(
	proc codeexecutor.ProgramSession,
	yield time.Duration,
	limit *int,
) codeexecutor.ProgramPoll {
	return programsession.WaitForProgramOutput(proc, yield, limit)
}

func yieldDuration(ms int, fallback int) time.Duration {
	return programsession.YieldDuration(ms, fallback)
}

func pollLineLimit(lines int) *int {
	return programsession.PollLineLimit(lines)
}

func detectInteraction(
	poll codeexecutor.ProgramPoll,
) *sessionInteraction {
	if poll.Status != codeexecutor.ProgramStatusRunning {
		return nil
	}
	hint := lastNonEmptyLine(poll.Output)
	if hint == "" {
		return nil
	}
	lower := strings.ToLower(hint)
	switch {
	case strings.Contains(lower, "enter the number"),
		strings.Contains(lower, "choose a number"),
		strings.Contains(lower, "select a number"),
		hasSelectionItems(poll.Output):
		return &sessionInteraction{
			NeedsInput: true,
			Kind:       interactionKindSelection,
			Hint:       hint,
		}
	case strings.HasSuffix(hint, ":"),
		strings.HasSuffix(hint, "?"),
		strings.Contains(lower, "press enter"),
		strings.Contains(lower, "type your"):
		return &sessionInteraction{
			NeedsInput: true,
			Kind:       interactionKindPrompt,
			Hint:       hint,
		}
	default:
		return nil
	}
}

func lastNonEmptyLine(text string) string {
	lines := strings.Split(text, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line != "" {
			return line
		}
	}
	return ""
}

func hasSelectionItems(text string) bool {
	lines := strings.Split(text, "\n")
	count := 0
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if len(line) >= 2 && line[0] >= '0' && line[0] <= '9' &&
			(line[1] == '.' || line[1] == ')') {
			count++
		}
		if count >= 2 {
			return true
		}
	}
	return false
}

func parseExecArgs(args []byte) (execInput, error) {
	var in execInput
	if err := json.Unmarshal(args, &in); err != nil {
		return execInput{}, fmt.Errorf("invalid args: %w", err)
	}
	if strings.TrimSpace(in.Skill) == "" ||
		strings.TrimSpace(in.Command) == "" {
		return execInput{}, fmt.Errorf(
			"skill and command are required",
		)
	}
	normalizeRunInput(&in.runInput)
	return in, nil
}

func parseSessionWriteArgs(
	args []byte,
) (sessionWriteInput, error) {
	var in sessionWriteInput
	if err := json.Unmarshal(args, &in); err != nil {
		return sessionWriteInput{}, fmt.Errorf(
			"invalid args: %w",
			err,
		)
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return sessionWriteInput{}, fmt.Errorf(
			"session_id is required",
		)
	}
	return in, nil
}

func parseSessionPollArgs(
	args []byte,
) (sessionPollInput, error) {
	var in sessionPollInput
	if err := json.Unmarshal(args, &in); err != nil {
		return sessionPollInput{}, fmt.Errorf(
			"invalid args: %w",
			err,
		)
	}
	if strings.TrimSpace(in.SessionID) == "" {
		return sessionPollInput{}, fmt.Errorf(
			"session_id is required",
		)
	}
	return in, nil
}

func execOutputSchema(desc string) *tool.Schema {
	return &tool.Schema{
		Type:        "object",
		Description: desc,
		Required: []string{
			"status",
			"session_id",
			"offset",
			"next_offset",
		},
		Properties: map[string]*tool.Schema{
			"status": {
				Type:        "string",
				Description: "running or exited",
			},
			"session_id": {
				Type:        "string",
				Description: "Interactive session id",
			},
			"output": {
				Type: "string",
				Description: "Most recent terminal output chunk " +
					"observed for this call",
			},
			"offset": {
				Type:        "integer",
				Description: "Start offset of returned output",
			},
			"next_offset": {
				Type:        "integer",
				Description: "Next output offset",
			},
			"exit_code": {
				Type: "integer",
				Description: "Exit code when the session has " +
					"exited",
			},
			"interaction": {
				Type: "object",
				Description: "Best-effort hint for whether the " +
					"program appears to be waiting for input",
				Properties: map[string]*tool.Schema{
					"needs_input": {
						Type:        "boolean",
						Description: "Whether input is expected",
					},
					"kind": {
						Type: "string",
						Description: "prompt or selection " +
							"(best effort)",
					},
					"hint": {
						Type: "string",
						Description: "The most relevant prompt " +
							"line",
					},
				},
			},
			"result": skillRunOutputSchema(),
		},
	}
}

func execArtifactsStateDelta(
	toolCallID string,
	resultJSON []byte,
) map[string][]byte {
	toolCallID = strings.TrimSpace(toolCallID)
	if toolCallID == "" || len(resultJSON) == 0 {
		return nil
	}
	var out execOutput
	if err := json.Unmarshal(resultJSON, &out); err != nil {
		return nil
	}
	if out.Result == nil {
		return nil
	}
	b, err := json.Marshal(out.Result)
	if err != nil {
		return nil
	}
	tmp := &RunTool{}
	return tmp.StateDelta(toolCallID, nil, b)
}

// StateDelta stores artifact references from a completed session result.
func (t *ExecTool) StateDelta(
	toolCallID string,
	_ []byte,
	resultJSON []byte,
) map[string][]byte {
	return execArtifactsStateDelta(toolCallID, resultJSON)
}

// StateDelta stores artifact references from a completed session result.
func (t *WriteStdinTool) StateDelta(
	toolCallID string,
	_ []byte,
	resultJSON []byte,
) map[string][]byte {
	return execArtifactsStateDelta(toolCallID, resultJSON)
}

// StateDelta stores artifact references from a completed session result.
func (t *PollSessionTool) StateDelta(
	toolCallID string,
	_ []byte,
	resultJSON []byte,
) map[string][]byte {
	return execArtifactsStateDelta(toolCallID, resultJSON)
}

var _ tool.Tool = (*ExecTool)(nil)
var _ tool.StreamableTool = (*ExecTool)(nil)
var _ stateDeltaProvider = (*ExecTool)(nil)
var _ tool.Tool = (*WriteStdinTool)(nil)
var _ tool.StreamableTool = (*WriteStdinTool)(nil)
var _ stateDeltaProvider = (*WriteStdinTool)(nil)
var _ tool.Tool = (*PollSessionTool)(nil)
var _ tool.StreamableTool = (*PollSessionTool)(nil)
var _ stateDeltaProvider = (*PollSessionTool)(nil)
var _ tool.Tool = (*KillSessionTool)(nil)
var _ tool.CallableTool = (*KillSessionTool)(nil)
