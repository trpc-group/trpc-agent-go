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
	"errors"
	"strings"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
)

const (
	appName        = "runner-factory-demo"
	fileIDPrefix   = "file:"
	fileLinkPrefix = "https://files.example.local/"
)

func newAGUIServer(cfg serverConfig) (*agui.Server, string, func() error, error) {
	agent := newAgent(cfg.ModelName, cfg.Stream)
	sessionService := inmemory.NewSessionService()
	agentRunner := runner.NewRunner(appName, agent, runner.WithSessionService(sessionService))
	server, err := agui.New(
		agentRunner,
		agui.WithPath(cfg.Path),
		agui.WithCancelEnabled(true),
		agui.WithCancelPath(cfg.CancelPath),
		agui.WithMessagesSnapshotEnabled(true),
		agui.WithMessagesSnapshotPath(cfg.MessagesSnapshotPath),
		agui.WithAppName(appName),
		agui.WithSessionService(sessionService),
		agui.WithTimeout(cfg.Timeout),
		agui.WithRunnerFactory(newFileLinkRunner),
	)
	if err != nil {
		agentRunner.Close()
		return nil, "", nil, err
	}
	return server, agent.Info().Name, agentRunner.Close, nil
}

func newFileLinkRunner(base runner.Runner, opts ...aguirunner.Option) (aguirunner.Runner, error) {
	inner := aguirunner.New(base, opts...)
	return &fileLinkRunner{inner: inner}, nil
}

type fileLinkRunner struct {
	inner aguirunner.Runner
}

func (r *fileLinkRunner) Run(
	ctx context.Context,
	input *adapter.RunAgentInput,
) (<-chan aguievents.Event, error) {
	if input != nil {
		next := *input
		next.Messages = rewriteMessages(input.Messages, fileIDPrefix, fileLinkPrefix)
		input = &next
	}
	return r.inner.Run(ctx, input)
}

func (r *fileLinkRunner) MessagesSnapshot(
	ctx context.Context,
	input *adapter.RunAgentInput,
) (<-chan aguievents.Event, error) {
	snapshotter, ok := r.inner.(aguirunner.MessagesSnapshotter)
	if !ok {
		return nil, errors.New("inner runner does not support messages snapshot")
	}
	events, err := snapshotter.MessagesSnapshot(ctx, input)
	if err != nil {
		return nil, err
	}
	out := make(chan aguievents.Event)
	go func() {
		defer close(out)
		for event := range events {
			if snapshot, ok := event.(*aguievents.MessagesSnapshotEvent); ok {
				snapshot.Messages = rewriteMessages(snapshot.Messages, fileLinkPrefix, fileIDPrefix)
			}
			select {
			case out <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func (r *fileLinkRunner) Cancel(ctx context.Context, input *adapter.RunAgentInput) error {
	canceler, ok := r.inner.(aguirunner.Canceler)
	if !ok {
		return errors.New("inner runner does not support cancel")
	}
	return canceler.Cancel(ctx, input)
}

func rewriteMessages(messages []types.Message, old, new string) []types.Message {
	if len(messages) == 0 {
		return messages
	}
	next := make([]types.Message, len(messages))
	for i, message := range messages {
		next[i] = message
		if content, ok := message.ContentString(); ok {
			next[i].Content = strings.ReplaceAll(content, old, new)
		}
	}
	return next
}
