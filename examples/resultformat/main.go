//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package main demonstrates per-tool result formatting for Function Tools.
package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
	"trpc.group/trpc-go/trpc-agent-go/tool/resultformat"
)

const (
	formattedToolName = "run_formatted"
	defaultToolName   = "run_default"
)

type commandArgs struct {
	Command string `json:"command"`
}

type commandResult struct {
	ExitCode int    `json:"exit_code"`
	Output   string `json:"output"`
}

type observation struct {
	XMLName  xml.Name `xml:"observation"`
	ExitCode int      `xml:"exit_code"`
	Output   string   `xml:"output"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "result formatting example failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	formattedTool := function.NewFunctionTool(
		runCommand,
		function.WithName(formattedToolName),
		function.WithDescription("Run a command and return an XML observation."),
		function.WithResultFormatter(
			resultformat.FormatterFunc[commandResult](formatObservation),
		),
	)
	defaultTool := function.NewFunctionTool(
		runCommand,
		function.WithName(defaultToolName),
		function.WithDescription("Run a command and use the default JSON result."),
	)

	ag := llmagent.New(
		"result-formatting-example",
		llmagent.WithModel(&scriptedModel{}),
		llmagent.WithTools([]tool.Tool{formattedTool, defaultTool}),
	)
	r := runner.NewRunner(
		"result-formatting-example",
		ag,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	events, err := r.Run(
		context.Background(),
		"example-user",
		"result-formatting-session",
		model.NewUserMessage("Run both command tools."),
	)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}

	results, err := collectToolResults(events)
	if err != nil {
		return err
	}
	for _, name := range []string{formattedToolName, defaultToolName} {
		content, ok := results[name]
		if !ok {
			return fmt.Errorf("missing result for tool %q", name)
		}
		fmt.Printf("%s:\n%s\n\n", name, content)
	}
	return nil
}

func runCommand(_ context.Context, args commandArgs) (commandResult, error) {
	return commandResult{
		ExitCode: 0,
		Output:   "ran " + args.Command + ": <ok> & \"done\"",
	}, nil
}

func formatObservation(
	_ context.Context,
	result commandResult,
) (string, error) {
	content, err := xml.Marshal(observation{
		ExitCode: result.ExitCode,
		Output:   result.Output,
	})
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func collectToolResults(
	events <-chan *event.Event,
) (map[string]string, error) {
	results := make(map[string]string)
	for evt := range events {
		if evt == nil {
			continue
		}
		if evt.Error != nil {
			return nil, errors.New(evt.Error.Message)
		}
		if evt.Response == nil || !evt.Response.IsToolResultResponse() {
			continue
		}
		for _, choice := range evt.Response.Choices {
			msg := choice.Message
			if msg.Role == model.RoleTool && msg.ToolName != "" {
				results[msg.ToolName] = msg.Content
			}
		}
	}
	return results, nil
}

type scriptedModel struct {
	step int
}

func (m *scriptedModel) Info() model.Info {
	return model.Info{Name: "result-formatting-scripted-model"}
}

func (m *scriptedModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.step++
	var response *model.Response
	switch m.step {
	case 1:
		response = toolCallResponse("call-formatted", formattedToolName)
	case 2:
		response = toolCallResponse("call-default", defaultToolName)
	default:
		response = assistantResponse("Both tools completed.")
	}

	responses := make(chan *model.Response, 1)
	responses <- response
	close(responses)
	return responses, nil
}

func toolCallResponse(id string, toolName string) *model.Response {
	return &model.Response{
		ID:      id,
		Object:  model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Done:    true,
		Choices: []model.Choice{{
			Message: model.Message{
				Role: model.RoleAssistant,
				ToolCalls: []model.ToolCall{{
					Type: "function",
					ID:   id,
					Function: model.FunctionDefinitionParam{
						Name:      toolName,
						Arguments: []byte(`{"command":"status"}`),
					},
				}},
			},
		}},
	}
}

func assistantResponse(content string) *model.Response {
	return &model.Response{
		ID:      "final-response",
		Object:  model.ObjectTypeChatCompletion,
		Created: time.Now().Unix(),
		Done:    true,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(content),
		}},
	}
}
