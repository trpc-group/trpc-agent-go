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
	"errors"
	"fmt"
	"os"
	"strings"
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
	xmlLikeToolName = "bash_xml_like"
	defaultToolName = "bash_default_json"

	sampleCommand = `rg --json 'ResultFormatter' tool internal`
	sampleArgs    = `{"command":"rg --json 'ResultFormatter' tool internal"}`
	sampleOutput  = `{"type":"match","data":{"path":{"text":"tool/resultformat/formatter.go"},"lines":{"text":"type Formatter interface {\n"},"line_number":20}}
{"type":"match","data":{"path":{"text":"tool/function/function_tool.go"},"lines":{"text":"func WithResultFormatter(formatter resultformat.Formatter) Option {\n"},"line_number":122}}
{"type":"match","data":{"path":{"text":"internal/flow/processor/functioncall.go"},"lines":{"text":"content, err := formatter.Format(ctx, result)\n"},"line_number":3661}}
{"type":"summary","data":{"elapsed_total":{"human":"0.004s"},"stats":{"searches":1,"searches_with_match":1,"bytes_searched":48291,"bytes_printed":615,"matched_lines":3,"matches":3}}}`
)

type commandArgs struct {
	Command string `json:"command"`
}

type commandResult struct {
	Exception  string `json:"exception,omitempty"`
	ReturnCode int    `json:"returncode"`
	Output     string `json:"output"`
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "result formatting example failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx := context.Background()
	xmlLikeTool := function.NewFunctionTool(
		runBash,
		function.WithName(xmlLikeToolName),
		function.WithDescription("Run a command and return an XML-like observation."),
		function.WithResultFormatter(
			resultformat.FormatterFunc[commandResult](formatObservation),
		),
	)
	defaultTool := function.NewFunctionTool(
		runBash,
		function.WithName(defaultToolName),
		function.WithDescription("Run a command and use the default JSON result."),
	)

	ag := llmagent.New(
		"tool-result-formatting-example",
		llmagent.WithModel(&scriptedModel{}),
		llmagent.WithTools([]tool.Tool{xmlLikeTool, defaultTool}),
	)
	r := runner.NewRunner(
		"tool-result-formatting-example",
		ag,
		runner.WithSessionService(inmemory.NewSessionService()),
	)
	defer r.Close()

	events, err := r.Run(
		ctx,
		"example-user",
		"tool-result-formatting-session",
		model.NewUserMessage("Run both command tools."),
	)
	if err != nil {
		return fmt.Errorf("run agent: %w", err)
	}

	results, err := collectToolResults(events)
	if err != nil {
		return err
	}
	counter := model.NewSimpleTokenCounter()
	for _, name := range []string{xmlLikeToolName, defaultToolName} {
		content, ok := results[name]
		if !ok {
			return fmt.Errorf("missing result for tool %q", name)
		}
		tokens, err := counter.CountTokens(
			ctx,
			model.Message{Content: content},
		)
		if err != nil {
			return fmt.Errorf("count tokens for tool %q: %w", name, err)
		}
		fmt.Printf("%s (estimated content tokens: %d):\n%s\n\n", name, tokens, content)
	}
	return nil
}

func runBash(_ context.Context, args commandArgs) (commandResult, error) {
	return commandResult{
		ReturnCode: 0,
		Output:     "$ " + args.Command + "\n" + sampleOutput,
	}, nil
}

func formatObservation(
	_ context.Context,
	result commandResult,
) (string, error) {
	var b strings.Builder
	if result.Exception != "" {
		fmt.Fprintf(&b, "<exception>%s</exception>\n", result.Exception)
	}
	fmt.Fprintf(&b, "<returncode>%d</returncode>\n", result.ReturnCode)
	fmt.Fprintf(&b, "<output>\n%s</output>", result.Output)
	return b.String(), nil
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
	return model.Info{Name: "tool-result-formatting-scripted-model"}
}

func (m *scriptedModel) GenerateContent(
	_ context.Context,
	_ *model.Request,
) (<-chan *model.Response, error) {
	m.step++
	var response *model.Response
	switch m.step {
	case 1:
		response = toolCallResponse("call-xml-like", xmlLikeToolName)
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
						Arguments: []byte(sampleArgs),
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
