//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package claudecode

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// claudeCodeAgent invokes a locally installed Claude Code CLI and maps its transcript into trpc-agent-go events.
type claudeCodeAgent struct {
	name          string
	description   string
	bin           string
	args          []string
	env           []string
	workDir       string
	commandRunner commandRunner
	rawOutputHook RawOutputHook
}

// New creates a Claude Code CLI agent with the provided options.
func New(opt ...Option) (agent.Agent, error) {
	opts, err := newOptions(opt...)
	if err != nil {
		return nil, err
	}
	return &claudeCodeAgent{
		name:          opts.name,
		description:   opts.description,
		bin:           opts.bin,
		args:          opts.args,
		env:           opts.env,
		workDir:       opts.workDir,
		commandRunner: opts.commandRunner,
		rawOutputHook: opts.rawOutputHook,
	}, nil
}

// Info implements agent.Agent.
func (a *claudeCodeAgent) Info() agent.Info {
	return agent.Info{
		Name:        a.name,
		Description: a.description,
	}
}

// Tools implements agent.Agent.
func (a *claudeCodeAgent) Tools() []tool.Tool { return nil }

// SubAgents implements agent.Agent.
func (a *claudeCodeAgent) SubAgents() []agent.Agent { return nil }

// FindSubAgent implements agent.Agent.
func (a *claudeCodeAgent) FindSubAgent(string) agent.Agent { return nil }

// Run implements agent.Agent.
func (a *claudeCodeAgent) Run(ctx context.Context, invocation *agent.Invocation) (<-chan *event.Event, error) {
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
func (a *claudeCodeAgent) runInvocation(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event) {
	defer close(out)
	cliSessID := cliSessionID(invocation.Session)
	if cliSessID == "" {
		a.emitFlowError(ctx, invocation, out, nil, errors.New("claude cli session id is empty"))
		return
	}
	stdout, stderr, runErr := a.runWithSession(ctx, cliSessID, invocation.Message.Content)
	combined := bytes.TrimSpace(append(append([]byte(nil), stdout...), stderr...))
	if hookErr := a.handleRawOutputHook(ctx, invocation, cliSessID, stdout, stderr, runErr); hookErr != nil {
		err := fmt.Errorf("raw output hook: %w", hookErr)
		if runErr != nil {
			err = errors.Join(err, fmt.Errorf("command error: %w", runErr))
		}
		a.emitFlowError(ctx, invocation, out, combined, err)
		return
	}
	if runErr != nil {
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
		return
	}
	finalResult := ""
	if len(stdout) > 0 {
		events, result, err := parseTranscriptToolEvents(stdout, invocation.InvocationID, a.name)
		if err != nil {
			decodeErr := fmt.Errorf("parse claude transcript: %w", err)
			a.emitFlowError(ctx, invocation, out, combined, decodeErr)
			return
		}
		finalResult = strings.TrimSpace(result)
		for _, e := range events {
			a.emitEvent(ctx, invocation, out, e)
		}
	}
	finalContent := string(combined)
	if finalResult != "" {
		finalContent = finalResult
	}
	rsp := &model.Response{
		Object: model.ObjectTypeChatCompletion,
		Done:   true,
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
	a.emitEvent(ctx, invocation, out, evt)
}

// handleRawOutputHook invokes the configured raw output hook.
func (a *claudeCodeAgent) handleRawOutputHook(
	ctx context.Context,
	invocation *agent.Invocation,
	cliSessionID string,
	stdout []byte,
	stderr []byte,
	runErr error,
) error {
	if a == nil || a.rawOutputHook == nil || invocation == nil || invocation.Session == nil {
		return nil
	}
	return a.rawOutputHook(ctx, &RawOutputHookArgs{
		InvocationID: invocation.InvocationID,
		SessionID:    invocation.Session.ID,
		CLISessionID: cliSessionID,
		Prompt:       invocation.Message.Content,
		Stdout:       stdout,
		Stderr:       stderr,
		Error:        runErr,
	})
}

// emitFlowError emits an error response event and stops further invocation processing.
func (a *claudeCodeAgent) emitFlowError(
	ctx context.Context,
	invocation *agent.Invocation,
	out chan<- *event.Event,
	combined []byte,
	flowErr error,
) {
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

// runWithSession executes the CLI with resume-first semantics and returns stdout/stderr.
func (a *claudeCodeAgent) runWithSession(ctx context.Context, sessionID, prompt string) ([]byte, []byte, error) {
	// Copy base args to avoid mutating shared backing arrays across concurrent invocations.
	resumeArgs := make([]string, 0, len(a.args)+3)
	resumeArgs = append(resumeArgs, a.args...)
	resumeArgs = append(resumeArgs, "--resume", sessionID, prompt)
	stdout, stderr, runErr := a.commandRunner.Run(ctx, command{
		bin:  a.bin,
		args: resumeArgs,
		env:  a.env,
		dir:  a.workDir,
	})
	if runErr == nil {
		return stdout, stderr, nil
	}
	createArgs := make([]string, 0, len(a.args)+3)
	createArgs = append(createArgs, a.args...)
	createArgs = append(createArgs, "--session-id", sessionID, prompt)
	createStdout, createStderr, createErr := a.commandRunner.Run(ctx, command{
		bin:  a.bin,
		args: createArgs,
		env:  a.env,
		dir:  a.workDir,
	})
	if createErr == nil {
		return createStdout, createStderr, nil
	}
	overallErr := fmt.Errorf("run --resume %v: %w", sessionID, runErr)
	overallErr = errors.Join(overallErr, fmt.Errorf("run --session-id %v: %w", sessionID, createErr))
	return stdout, stderr, overallErr
}

// emitEvent forwards an event to the caller and logs emission failures.
func (a *claudeCodeAgent) emitEvent(ctx context.Context, invocation *agent.Invocation, out chan<- *event.Event, evt *event.Event) {
	if err := agent.EmitEvent(ctx, invocation, out, evt); err != nil {
		log.ErrorfContext(ctx, "claude agent failed to emit event: %v", err)
	}
}

// cliSessionID returns a deterministic UUID session id suitable for Claude Code CLI flags.
func cliSessionID(sess *session.Session) string {
	if sess == nil {
		return ""
	}
	id := strings.TrimSpace(sess.ID)
	if id == "" {
		return ""
	}
	name := sess.AppName + ":" + sess.UserID + ":" + id
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte(name)).String()
}
