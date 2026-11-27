//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package acpagent

import (
	"context"
	"errors"
	"fmt"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

func promptContentFromMessage(message model.Message) ([]acpsdk.ContentBlock, error) {
	blocks := make([]acpsdk.ContentBlock, 0, 1+len(message.ContentParts))
	if message.Content != "" {
		blocks = append(blocks, acpsdk.TextBlock(message.Content))
	}
	for index, part := range message.ContentParts {
		switch part.Type {
		case model.ContentTypeText:
			if part.Text != nil && *part.Text != "" {
				blocks = append(blocks, acpsdk.TextBlock(*part.Text))
			}
		case model.ContentTypeFile:
			if part.File == nil {
				return nil, fmt.Errorf("acpagent: content part %d has no file", index)
			}
			if part.File.URL == "" {
				return nil, fmt.Errorf(
					"acpagent: content part %d file has no resource link URL",
					index,
				)
			}
			name := part.File.Name
			if name == "" {
				name = part.File.URL
			}
			block := acpsdk.ResourceLinkBlock(name, part.File.URL)
			if part.File.MimeType != "" {
				mimeType := part.File.MimeType
				block.ResourceLink.MimeType = &mimeType
			}
			blocks = append(blocks, block)
		default:
			return nil, fmt.Errorf(
				"acpagent: content part %d type %q is not supported",
				index,
				part.Type,
			)
		}
	}
	if len(blocks) == 0 {
		return nil, errors.New("acpagent: invocation prompt is empty")
	}
	return blocks, nil
}

func (a *ACPAgent) runPrompt(
	ctx context.Context,
	invocation *agent.Invocation,
	session *acpSession,
	content []acpsdk.ContentBlock,
	out chan<- *event.Event,
) {
	defer close(out)
	started := time.Now()
	fields := acpSessionLogFields(session)
	if invocation != nil {
		fields = append(fields, "invocation_id", invocation.InvocationID)
	}
	debugContext(
		ctx,
		"acpagent: prompt started",
		append(fields, "content_blocks", len(content))...,
	)
	eventCtx := context.WithoutCancel(ctx)
	if err := session.acquirePrompt(ctx); err != nil {
		a.emitTerminalError(eventCtx, invocation, session, out, err)
		return
	}
	defer session.releasePrompt()

	turnCtx, cancel := context.WithCancel(ctx)
	turn := newActiveTurn(turnCtx, invocation, cancel)
	a.activeTurns.set(session.id, turn)
	defer func() {
		a.activeTurns.clear(session.id, turn)
		cancel()
	}()

	promptResult := make(chan struct {
		response acpsdk.PromptResponse
		err      error
	}, 1)
	go func() {
		response, err := session.process.connection.Prompt(turnCtx, acpsdk.PromptRequest{
			SessionId: session.id,
			Prompt:    content,
		})
		promptResult <- struct {
			response acpsdk.PromptResponse
			err      error
		}{response: response, err: err}
	}()

	translator := newSessionUpdateTranslator(
		invocation.InvocationID,
		a.config.Name,
	)
	for {
		select {
		case <-turn.overflowed():
			a.emitTerminalError(eventCtx, invocation, session, out, errSessionUpdateOverflow)
			return
		case notification := <-turn.updates:
			updates, err := translator.translate(notification)
			if err != nil {
				a.emitTerminalError(eventCtx, invocation, session, out, err)
				return
			}
			for _, update := range updates {
				if err := a.emitEvent(eventCtx, invocation, session, out, update); err != nil {
					return
				}
			}
		case result := <-promptResult:
			for {
				select {
				case notification := <-turn.updates:
					updates, err := translator.translate(notification)
					if err != nil {
						a.emitTerminalError(eventCtx, invocation, session, out, err)
						return
					}
					for _, update := range updates {
						if err := a.emitEvent(eventCtx, invocation, session, out, update); err != nil {
							return
						}
					}
				default:
					if turn.hasOverflowed() {
						a.emitTerminalError(
							eventCtx,
							invocation,
							session,
							out,
							errSessionUpdateOverflow,
						)
						return
					}
					if result.err != nil {
						terminalErr := result.err
						if ctx.Err() == nil {
							if processErr := session.process.failureWithin(
								200 * time.Millisecond,
							); processErr != nil {
								terminalErr = processErr
							}
						}
						if turnErr := turn.failureError(); turnErr != nil {
							terminalErr = turnErr
						}
						a.emitTerminalError(eventCtx, invocation, session, out, terminalErr)
						return
					}
					final := translator.finalEvent(result.response)
					if err := a.emitEvent(eventCtx, invocation, session, out, final); err != nil {
						return
					}
					completionFields := append(
						fields,
						"duration", time.Since(started),
						"stop_reason", string(result.response.StopReason),
						"text_bytes", len(translator.text.String()),
						"reasoning_bytes", len(translator.reasoning.String()),
						"tool_count", len(translator.tools),
					)
					if result.response.Usage != nil {
						completionFields = append(
							completionFields,
							"input_tokens", result.response.Usage.InputTokens,
							"output_tokens", result.response.Usage.OutputTokens,
							"total_tokens", result.response.Usage.TotalTokens,
						)
					}
					debugContext(eventCtx, "acpagent: prompt completed", completionFields...)
					return
				}
			}
		}
	}
}

func (a *ACPAgent) emitEvent(
	ctx context.Context,
	invocation *agent.Invocation,
	session *acpSession,
	out chan<- *event.Event,
	evt *event.Event,
) error {
	if evt == nil {
		return nil
	}
	session.applyStateDelta(evt)
	err := agent.EmitEvent(ctx, invocation, out, evt)
	if err != nil {
		fields := acpSessionLogFields(session)
		if invocation != nil {
			fields = append(fields, "invocation_id", invocation.InvocationID)
		}
		warnContext(ctx, "acpagent: emit event failed", append(fields, "error", err)...)
	}
	return err
}

func (a *ACPAgent) emitTerminalError(
	ctx context.Context,
	invocation *agent.Invocation,
	session *acpSession,
	out chan<- *event.Event,
	err error,
) {
	if err == nil {
		err = errors.New("acpagent: prompt failed")
	}
	fields := acpSessionLogFields(session)
	if invocation != nil {
		fields = append(fields, "invocation_id", invocation.InvocationID)
	}
	fields = append(fields, "error", err)
	switch {
	case errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, errSessionUpdateOverflow):
		warnContext(ctx, "acpagent: prompt stopped", fields...)
	default:
		errorContext(ctx, "acpagent: prompt failed", fields...)
	}
	evt := event.NewErrorEvent(
		invocation.InvocationID,
		a.config.Name,
		model.ErrorTypeRunError,
		err.Error(),
	)
	_ = a.emitEvent(ctx, invocation, session, out, evt)
}
