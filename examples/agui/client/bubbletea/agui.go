// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/sirupsen/logrus"
)

type errMsg struct {
	error
}

type chatStreamReadyMsg struct {
	stream *chatStream
}

type chatStreamEventMsg struct {
	stream *chatStream
	lines  []string
}

type chatStreamFinishedMsg struct {
	stream *chatStream
}

func startChatCmd(prompt, endpoint string) tea.Cmd {
	return func() tea.Msg {
		stream, err := openChatStream(prompt, endpoint)
		if err != nil {
			return errMsg{err}
		}
		return chatStreamReadyMsg{stream: stream}
	}
}

type chatStream struct {
	ctx    context.Context
	cancel context.CancelFunc
	client *sse.Client
	frames <-chan sse.Frame
	errCh  <-chan error
	once   sync.Once
}

func (s *chatStream) Close() {
	if s == nil {
		return
	}
	s.once.Do(func() {
		s.cancel()
		s.client.Close()
	})
}

func openChatStream(prompt, endpoint string) (*chatStream, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)
	client := sse.NewClient(sse.Config{
		Endpoint:       endpoint,
		ConnectTimeout: 30 * time.Second,
		ReadTimeout:    5 * time.Minute,
		BufferSize:     100,
		Logger:         logger,
	})
	payload := map[string]any{
		"threadId": "demo-thread",
		"runId":    fmt.Sprintf("run-%d", time.Now().UnixNano()),
		"messages": []map[string]any{{"role": "user", "content": prompt}},
	}
	frames, errCh, err := client.Stream(sse.StreamOptions{Context: ctx, Payload: payload})
	if err != nil {
		cancel()
		client.Close()
		return nil, fmt.Errorf("failed to start SSE stream: %w", err)
	}
	return &chatStream{
		ctx:    ctx,
		cancel: cancel,
		client: client,
		frames: frames,
		errCh:  errCh,
	}, nil
}

func readNextEventCmd(stream *chatStream) tea.Cmd {
	if stream == nil {
		return nil
	}
	return func() tea.Msg {
		for {
			select {
			case frame, ok := <-stream.frames:
				if !ok {
					stream.Close()
					return chatStreamFinishedMsg{stream: stream}
				}
				evt, err := events.EventFromJSON(frame.Data)
				if err != nil {
					stream.Close()
					return errMsg{fmt.Errorf("parse event: %w", err)}
				}
				lines := formatEvent(evt)
				if len(lines) == 0 {
					continue
				}
				return chatStreamEventMsg{stream: stream, lines: lines}
			case err, ok := <-stream.errCh:
				if !ok || err == nil {
					continue
				}
				stream.Close()
				return errMsg{err}
			case <-stream.ctx.Done():
				stream.Close()
				return errMsg{stream.ctx.Err()}
			}
		}
	}
}

func formatEvent(evt events.Event) []string {
	label := fmt.Sprintf("[%s]", evt.Type())
	switch e := evt.(type) {
	case *events.RunStartedEvent:
		return []string{fmt.Sprintf("Agent> %s", label)}
	case *events.RunFinishedEvent:
		return []string{fmt.Sprintf("Agent> %s", label)}
	case *events.RunErrorEvent:
		return []string{fmt.Sprintf("Agent> %s: %s", label, e.Message)}
	case *events.TextMessageStartEvent:
		return []string{fmt.Sprintf("Agent> %s", label)}
	case *events.TextMessageContentEvent:
		if strings.TrimSpace(e.Delta) == "" {
			return nil
		}
		return []string{fmt.Sprintf("Agent> %s %s", label, e.Delta)}
	case *events.TextMessageEndEvent:
		return []string{fmt.Sprintf("Agent> %s", label)}
	case *events.ToolCallStartEvent:
		return []string{fmt.Sprintf("Agent> %s tool call '%s' started, id: %s", label, e.ToolCallName, e.ToolCallID)}
	case *events.ToolCallArgsEvent:
		return []string{fmt.Sprintf("Agent> %s tool args: %s", label, e.Delta)}
	case *events.ToolCallEndEvent:
		return []string{fmt.Sprintf("Agent> %s tool call completed, id: %s", label, e.ToolCallID)}
	case *events.ToolCallResultEvent:
		return []string{fmt.Sprintf("Agent> %s tool result: %s", label, e.Content)}
	default:
		return nil
	}
}
