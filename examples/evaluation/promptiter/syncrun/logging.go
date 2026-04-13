//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync/atomic"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

const maxLoggedPayload = 6000

type loggingRunner struct {
	name    string
	inner   runner.Runner
	logger  *log.Logger
	enabled bool
	nextID  atomic.Uint64
}

type loggedRunnerOutput struct {
	eventCount        int
	finalContent      string
	structuredPayload any
	err               error
}

func newLoggingRunner(
	name string,
	inner runner.Runner,
	logger *log.Logger,
	enabled bool,
) runner.Runner {
	return &loggingRunner{
		name:    name,
		inner:   inner,
		logger:  logger,
		enabled: enabled,
	}
}

func (r *loggingRunner) Run(
	ctx context.Context,
	userID string,
	sessionID string,
	message model.Message,
	runOpts ...agent.RunOption,
) (<-chan *event.Event, error) {
	events, err := r.inner.Run(ctx, userID, sessionID, message, runOpts...)
	if err != nil {
		if r.enabled && r.logger != nil {
			r.logger.Printf("[%s] runner start failed: %v", r.name, err)
		}
		return nil, err
	}
	if !r.enabled || r.logger == nil {
		return events, nil
	}
	runID := r.nextID.Add(1)
	r.logger.Printf(
		"[%s #%d] input message: %s",
		r.name,
		runID,
		truncateForLog(marshalLogValue(message)),
	)
	forwarded := make(chan *event.Event)
	go func() {
		defer close(forwarded)
		output := loggedRunnerOutput{}
		for evt := range events {
			if evt != nil {
				output.observe(evt)
			}
			forwarded <- evt
		}
		r.logger.Printf("[%s #%d] events observed: %d", r.name, runID, output.eventCount)
		if output.finalContent != "" {
			r.logger.Printf(
				"[%s #%d] output text: %s",
				r.name,
				runID,
				truncateForLog(output.finalContent),
			)
		}
		if output.structuredPayload != nil {
			r.logger.Printf(
				"[%s #%d] output structured: %s",
				r.name,
				runID,
				truncateForLog(marshalLogValue(output.structuredPayload)),
			)
		}
		if output.err != nil {
			r.logger.Printf("[%s #%d] output error: %v", r.name, runID, output.err)
		}
	}()
	return forwarded, nil
}

func (r *loggingRunner) Close() error {
	return r.inner.Close()
}

func (o *loggedRunnerOutput) observe(evt *event.Event) {
	o.eventCount++
	if evt.StructuredOutput != nil {
		o.structuredPayload = evt.StructuredOutput
	}
	if evt.IsError() {
		if evt.Error != nil {
			o.err = evt.Error
		} else {
			o.err = fmt.Errorf("event object %q reported error", evt.Object)
		}
	}
	if len(evt.Choices) > 0 && evt.Choices[0].Message.Content != "" {
		o.finalContent = evt.Choices[0].Message.Content
	}
}

func marshalLogValue(value any) string {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(payload)
}

func truncateForLog(value string) string {
	if len(value) <= maxLoggedPayload {
		return value
	}
	return strings.TrimSpace(value[:maxLoggedPayload]) + "...(truncated)"
}
