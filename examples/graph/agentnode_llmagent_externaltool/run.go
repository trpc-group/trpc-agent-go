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
	"fmt"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/graph"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

type interruptInfo struct {
	LineageID    string      `json:"lineageId"`
	CheckpointID string      `json:"checkpointId"`
	Request      toolRequest `json:"interruptValue"`
}

func runUntilInterrupt(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	lineageID string,
	input string,
) (interruptInfo, error) {
	events, err := runGraph(ctx, r, sessionID, model.NewUserMessage(input), map[string]any{
		graph.CfgKeyLineageID: lineageID,
	})
	if err != nil {
		return interruptInfo{}, err
	}
	for ev := range events {
		if err := eventError(ev); err != nil {
			return interruptInfo{}, err
		}
		info, ok, err := interruptFromEvent(ev)
		if err != nil || ok {
			return info, err
		}
	}
	return interruptInfo{}, errors.New("graph completed without external tool interrupt")
}

func resumeAndPrint(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	interrupt interruptInfo,
	result string,
) error {
	events, err := runGraph(ctx, r, sessionID, model.Message{}, map[string]any{
		graph.CfgKeyLineageID:    interrupt.LineageID,
		graph.CfgKeyCheckpointID: interrupt.CheckpointID,
		graph.StateKeyCommand: graph.NewResumeCommand().WithResumeMap(map[string]any{
			interrupt.Request.ToolCallID: result,
		}),
	})
	if err != nil {
		return err
	}
	var final string
	for ev := range events {
		if err := eventError(ev); err != nil {
			return err
		}
		if ev == nil || len(ev.Choices) == 0 {
			continue
		}
		if content := strings.TrimSpace(ev.Choices[0].Message.Content); content != "" {
			final = content
		}
	}
	if final == "" {
		return errors.New("resumed graph completed without final answer")
	}
	fmt.Printf("\nFinal answer:\n%s\n", final)
	return nil
}

func runGraph(
	ctx context.Context,
	r runner.Runner,
	sessionID string,
	message model.Message,
	state map[string]any,
) (<-chan *event.Event, error) {
	return r.Run(
		ctx,
		userID,
		sessionID,
		message,
		agent.WithRuntimeState(state),
		agent.WithGraphEmitFinalModelResponses(true),
	)
}

func interruptFromEvent(ev *event.Event) (interruptInfo, bool, error) {
	if ev == nil || ev.Object != graph.ObjectTypeGraphPregelStep || ev.StateDelta == nil {
		return interruptInfo{}, false, nil
	}
	raw := ev.StateDelta[graph.MetadataKeyPregel]
	if len(raw) == 0 {
		return interruptInfo{}, false, nil
	}
	var info interruptInfo
	if err := json.Unmarshal(raw, &info); err != nil {
		return interruptInfo{}, false, err
	}
	if info.Request.ToolCallID == "" {
		return interruptInfo{}, false, nil
	}
	if info.Request.Name != externalToolName {
		return interruptInfo{}, false, fmt.Errorf("unsupported external tool %q", info.Request.Name)
	}
	return info, true, nil
}

func eventError(ev *event.Event) error {
	if ev == nil {
		return nil
	}
	if ev.Error != nil {
		return fmt.Errorf("event error: %s: %s", ev.Error.Type, ev.Error.Message)
	}
	if ev.Response != nil && ev.Response.Error != nil {
		return fmt.Errorf("response error: %s: %s", ev.Response.Error.Type, ev.Response.Error.Message)
	}
	return nil
}

func readToolResult(scanner *bufio.Scanner) (string, error) {
	if strings.TrimSpace(*toolResult) != "" {
		return *toolResult, nil
	}
	fmt.Print("external_search result> ")
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", err
		}
		return "", errors.New("external search result is empty")
	}
	result := strings.TrimSpace(scanner.Text())
	if result == "" {
		return "", errors.New("external search result is empty")
	}
	return result, nil
}
