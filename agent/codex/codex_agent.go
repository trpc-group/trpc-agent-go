//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codex

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// codexAgent invokes a locally installed Codex CLI and maps its JSONL events into trpc-agent-go events.
type codexAgent struct {
	name          string
	description   string
	bin           string
	globalArgs    []string
	args          []string
	env           []string
	workDir       string
	commandRunner commandRunner
	rawOutputHook RawOutputHook
}

// New creates a Codex CLI agent with the provided options.
func New(opt ...Option) (agent.Agent, error) {
	opts, err := newOptions(opt...)
	if err != nil {
		return nil, err
	}
	return &codexAgent{
		name:          opts.name,
		description:   opts.description,
		bin:           opts.bin,
		globalArgs:    opts.globalArgs,
		args:          opts.args,
		env:           opts.env,
		workDir:       opts.workDir,
		commandRunner: opts.commandRunner,
		rawOutputHook: opts.rawOutputHook,
	}, nil
}

// Info implements agent.Agent.
func (a *codexAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: a.description,
	}
}

// Tools implements agent.Agent.
func (a *codexAgent) Tools() []tool.Tool { return nil }

// SubAgents implements agent.Agent.
func (a *codexAgent) SubAgents() []agent.Agent { return nil }

// FindSubAgent implements agent.Agent.
func (a *codexAgent) FindSubAgent(string) agent.Agent { return nil }

// Run implements agent.Agent.
func (a *codexAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
	if invocation == nil {
		return nil, errors.New("invocation is nil")
	}
	if invocation.Session == nil {
		return nil, errors.New("invocation session is nil")
	}
	if invocation.Session.ID == "" {
		return nil, errors.New("invocation session id is empty")
	}
	if invocation.Message.Content == "" {
		return nil, errors.New("invocation prompt is empty")
	}
	out := make(chan *event.Event)
	runCtx := agent.CloneContext(ctx)
	go a.runInvocation(runCtx, invocation, out)
	return out, nil
}

// runInvocation executes the CLI invocation and emits tool events and the final response event.
func (a *codexAgent) runInvocation(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event) {
	defer close(out)
	resumeThreadID := sessionThreadID(invocation.Session)
	streamer := newCodexStreamEmitter(ctx, a, invocation, out)
	cmdResult := a.runWithSession(ctx, resumeThreadID, invocation.Message.Content, streamer)
	combined := bytes.TrimSpace(combineOutput(cmdResult.stdout, cmdResult.stderr))
	result := streamer.Result()
	observedThreadID := result.ThreadID
	if observedThreadID == "" {
		observedThreadID = extractThreadID(cmdResult.stdout)
	}
	if hookErr := a.handleRawOutputHook(ctx, invocation, resumeThreadID, observedThreadID, cmdResult.stdout, cmdResult.stderr, cmdResult.err()); hookErr != nil {
		err := fmt.Errorf("raw output hook: %w", hookErr)
		if cmdErr := cmdResult.err(); cmdErr != nil {
			err = errors.Join(err, fmt.Errorf("command error: %w", cmdErr))
		}
		a.emitFlowError(ctx, invocation, out, combined, err)
		return
	}
	if cmdResult.outputErr != nil {
		a.emitFlowError(ctx, invocation, out, combined, fmt.Errorf("stream codex transcript: %w", cmdResult.outputErr))
		return
	}
	if cmdResult.runErr != nil {
		if result.Error == nil {
			a.emitRunError(ctx, invocation, out, combined, cmdResult.runErr)
		}
		return
	}
	if result.Error != nil {
		return
	}
	a.emitFinalResponse(ctx, invocation, out, combined, result, observedThreadID, resumeThreadID)
}

// combineOutput joins stdout and stderr with the same display rules as the previous buffered path.
func combineOutput(stdout []byte, stderr []byte) []byte {
	combined := append([]byte(nil), stdout...)
	if len(stdout) > 0 && len(stderr) > 0 {
		combined = append(combined, '\n')
	}
	combined = append(combined, stderr...)
	return combined
}

// emitFinalResponse emits the final assistant response after the Codex turn completes.
func (a *codexAgent) emitFinalResponse(
	ctx context.Context,
	invocation *agent.Invocation,
	out chan<- *event.Event,
	combined []byte,
	result *transcriptResult,
	threadID string,
	resumeThreadID string,
) {
	finalContent := string(combined)
	if strings.TrimSpace(result.FinalMessage) != "" {
		finalContent = strings.TrimSpace(result.FinalMessage)
	}
	rsp := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
		Usage:  result.Usage,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: finalContent,
				},
			},
		},
	}
	evt := event.NewResponseEvent(invocation.InvocationID, a.name, rsp)
	if threadID != "" && threadID != resumeThreadID {
		evt.StateDelta = map[string][]byte{StateKeyThreadID: []byte(threadID)}
	}
	a.emitEvent(ctx, invocation, out, evt)
}

// codexStreamEmitter owns one incremental transcript parser for a CLI attempt.
type codexStreamEmitter struct {
	ctx     context.Context
	agent   *codexAgent
	inv     *agent.Invocation
	out     chan<- *event.Event
	stream  *transcriptStream
	emitted bool
}

// newCodexStreamEmitter creates an emitter for a Codex invocation.
func newCodexStreamEmitter(ctx context.Context, a *codexAgent, inv *agent.Invocation, out chan<- *event.Event) *codexStreamEmitter {
	e := &codexStreamEmitter{
		ctx:   ctx,
		agent: a,
		inv:   inv,
		out:   out,
	}
	e.Reset()
	return e
}

// Reset discards buffered transcript state for a fresh CLI attempt.
func (e *codexStreamEmitter) Reset() {
	e.stream = newTranscriptStream(e.inv.InvocationID, e.agent.name)
	e.emitted = false
}

// HandleLine parses and emits events from one Codex JSONL stdout line.
func (e *codexStreamEmitter) HandleLine(line []byte) error {
	events, err := e.stream.HandleLine(line)
	if err != nil {
		return err
	}
	for _, evt := range events {
		if evt == nil {
			continue
		}
		e.emitted = true
		if err := agent.EmitEvent(e.ctx, e.inv, e.out, evt); err != nil {
			return err
		}
	}
	return nil
}

// Result returns the accumulated transcript state.
func (e *codexStreamEmitter) Result() *transcriptResult {
	if e == nil || e.stream == nil {
		return &transcriptResult{}
	}
	return e.stream.Result()
}

// handleRawOutputHook invokes the configured raw output hook.
func (a *codexAgent) handleRawOutputHook(
	ctx context.Context,
	invocation *agent.Invocation,
	resumeThreadID string,
	threadID string,
	stdout []byte,
	stderr []byte,
	runErr error,
) error {
	if a == nil || a.rawOutputHook == nil || invocation == nil || invocation.Session == nil {
		return nil
	}
	return a.rawOutputHook(ctx, &RawOutputHookArgs{
		InvocationID:   invocation.InvocationID,
		SessionID:      invocation.Session.ID,
		ResumeThreadID: resumeThreadID,
		ThreadID:       threadID,
		Prompt:         invocation.Message.Content,
		Stdout:         stdout,
		Stderr:         stderr,
		Error:          runErr,
	})
}

// emitRunError emits a command execution error response.
func (a *codexAgent) emitRunError(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event, combined []byte, runErr error) {
	msg := string(combined)
	if len(combined) == 0 {
		msg = runErr.Error()
	}
	rsp := &model.Response{
		Object: model.ObjectTypeError,
		Done:   true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: msg,
				},
			},
		},
		Error: &model.ResponseError{
			Type:    model.ErrorTypeRunError,
			Message: msg,
		},
	}
	a.emitEvent(ctx, invocation, out, event.NewResponseEvent(invocation.InvocationID, a.name, rsp))
}

// emitFlowError emits an error response event and stops further invocation processing.
func (a *codexAgent) emitFlowError(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event, combined []byte, flowErr error) {
	rsp := &model.Response{
		Object: model.ObjectTypeError,
		Done:   true,
		Choices: []model.Choice{
			{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: string(combined),
				},
			},
		},
		Error: &model.ResponseError{
			Type:    model.ErrorTypeFlowError,
			Message: flowErr.Error(),
		},
	}
	a.emitEvent(ctx, invocation, out, event.NewResponseEvent(invocation.InvocationID, a.name, rsp))
}

// runWithSession executes the CLI with resume-first semantics and streams stdout events.
func (a *codexAgent) runWithSession(ctx context.Context, threadID, prompt string, streamer *codexStreamEmitter) commandResult {
	if strings.TrimSpace(threadID) != "" {
		streamer.Reset()
		resumeResult := a.runResume(ctx, threadID, prompt, streamer.HandleLine)
		if resumeResult.err() == nil {
			return resumeResult
		}
		if resumeResult.outputErr != nil || streamer.emitted {
			return resumeResult
		}
		streamer.Reset()
		createResult := a.runCreate(ctx, prompt, streamer.HandleLine)
		if createResult.err() == nil {
			return createResult
		}
		if createResult.runErr != nil {
			overallErr := fmt.Errorf("run resume %v: %w", threadID, resumeResult.runErr)
			createResult.runErr = errors.Join(overallErr, fmt.Errorf("run exec: %w", createResult.runErr))
		}
		return createResult
	}
	streamer.Reset()
	return a.runCreate(ctx, prompt, streamer.HandleLine)
}

// runCreate starts a new Codex exec session.
func (a *codexAgent) runCreate(ctx context.Context, prompt string, onStdoutLine commandOutputHandler) commandResult {
	cmdArgs := make([]string, 0, len(a.globalArgs)+len(a.args)+1)
	cmdArgs = append(cmdArgs, a.globalArgs...)
	cmdArgs = append(cmdArgs, "exec")
	cmdArgs = append(cmdArgs, a.args...)
	return a.commandRunner.Run(ctx, command{
		bin:   a.bin,
		args:  cmdArgs,
		stdin: []byte(prompt),
		env:   a.env,
		dir:   a.workDir,
	}, onStdoutLine)
}

// runResume resumes an existing Codex thread.
func (a *codexAgent) runResume(ctx context.Context, threadID, prompt string, onStdoutLine commandOutputHandler) commandResult {
	cmdArgs := make([]string, 0, len(a.globalArgs)+len(a.args)+3)
	cmdArgs = append(cmdArgs, a.globalArgs...)
	cmdArgs = append(cmdArgs, "exec", "resume")
	cmdArgs = append(cmdArgs, a.args...)
	cmdArgs = append(cmdArgs, threadID)
	return a.commandRunner.Run(ctx, command{
		bin:   a.bin,
		args:  cmdArgs,
		stdin: []byte(prompt),
		env:   a.env,
		dir:   a.workDir,
	}, onStdoutLine)
}

// emitEvent forwards an event to the caller and logs emission failures.
func (a *codexAgent) emitEvent(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event, evt *event.Event) {
	if err := agent.EmitEvent(ctx, invocation, out, evt); err != nil {
		log.ErrorfContext(ctx, "codex agent failed to emit event: %v", err)
	}
}

// sessionThreadID returns the persisted Codex thread id from session state.
func sessionThreadID(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	raw, ok := sess.GetState(StateKeyThreadID)
	if !ok {
		return ""
	}
	return strings.TrimSpace(string(raw))
}
