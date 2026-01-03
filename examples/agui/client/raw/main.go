//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/client/sse"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/types"
	"github.com/sirupsen/logrus"
)

const (
	defaultEndpoint    = "http://127.0.0.1:8080/agui"
	requestTimeout     = 2 * time.Minute
	connectTimeout     = 30 * time.Second
	readTimeout        = 5 * time.Minute
	streamBufferSize   = 100
	stdinBufferInitial = 64 * 1024
	stdinBufferMax     = 1 << 20
)

func main() {
	endpoint := flag.String("endpoint", defaultEndpoint, "AG-UI SSE endpoint")
	flag.Parse()

	if err := runInteractive(*endpoint); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func runInteractive(endpoint string) error {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, stdinBufferInitial), stdinBufferMax)

	fmt.Printf("Simple AG-UI client. Endpoint: %s\n", endpoint)
	fmt.Println("Type your prompt and press Enter (Ctrl+D to exit).")

	for {
		fmt.Print("You> ")
		if !scanner.Scan() {
			if err := scanner.Err(); err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("read input: %w", err)
			}
			fmt.Println()
			return nil
		}
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		if strings.EqualFold(prompt, "quit") || strings.EqualFold(prompt, "exit") {
			return nil
		}
		if err := streamConversation(endpoint, prompt); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
	}
}

func streamConversation(endpoint, prompt string) error {
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	client := newSSEClient(endpoint)
	defer client.Close()

	payload := types.RunAgentInput{
		ThreadID: "demo-thread",
		RunID:    fmt.Sprintf("run-%d", time.Now().UnixNano()),
		Messages: []types.Message{{Role: types.RoleUser, Content: prompt}},
	}

	frames, errCh, err := client.Stream(sse.StreamOptions{Context: ctx, Payload: payload})
	if err != nil {
		return fmt.Errorf("start SSE stream: %w", err)
	}

	printed := false

	for frames != nil || errCh != nil {
		select {
		case frame, ok := <-frames:
			if !ok {
				frames = nil
				continue
			}
			evt, err := events.EventFromJSON(frame.Data)
			if err != nil {
				return fmt.Errorf("parse event: %w", err)
			}
			lines := formatEvent(evt)
			if len(lines) == 0 {
				continue
			}
			printed = true
			for _, line := range lines {
				fmt.Println(line)
			}
		case err, ok := <-errCh:
			if !ok {
				errCh = nil
				continue
			}
			if err != nil {
				return fmt.Errorf("stream error: %w", err)
			}
		case <-ctx.Done():
			return fmt.Errorf("stream timeout: %w", ctx.Err())
		}
	}

	if !printed {
		fmt.Println("Agent> (no response)")
	}
	fmt.Println()
	return nil
}

func newSSEClient(endpoint string) *sse.Client {
	logger := logrus.New()
	logger.SetLevel(logrus.WarnLevel)

	return sse.NewClient(sse.Config{
		Endpoint:       endpoint,
		ConnectTimeout: connectTimeout,
		ReadTimeout:    readTimeout,
		BufferSize:     streamBufferSize,
		Logger:         logger,
	})
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
	case *events.CustomEvent:
		data, err := json.Marshal(e.Value)
		if err != nil {
			return nil
		}
		return []string{fmt.Sprintf("Agent> %s '%s': %s", label, e.Name, string(data))}
	default:
		return []string{fmt.Sprintf("Agent> %s", label)}
	}
}
