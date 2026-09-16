//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/skill"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

const (
	runWorkflowToolName     = "run_workflow"
	omittedWorkflowCodeJSON = `"[omitted]"`
	omittedWorkflowArgs     = "[workflow code omitted]"
	toolResultMaxRunes      = 500
)

func (c *dynamicWorkflowChat) printStartup() {
	fmt.Println("Dynamic Workflow with Skills")
	fmt.Printf("Model: %s\n", c.modelName)
	fmt.Printf("Workflow recipes: %s\n", strings.Join(c.workflowSkills, ", "))
	fmt.Printf("Session: %s\n", c.sessionID)
}

func (c *dynamicWorkflowChat) printBanner() {
	c.printStartup()
	fmt.Println("Type '/new' to start a new session or '/exit' to quit.")
	fmt.Println("Sample prompts:")
	fmt.Println("  Explain the difference between a Go interface and a struct in one sentence.")
	fmt.Println("  Draft a concise API deprecation notice for replacing v1 with v2. Have an independent reviewer check migration clarity, ownership, timeline, and rollback safety, and revise it until approved.")
	fmt.Println("  Compare two approaches to introducing a new service boundary. Analyze feasibility, operational impact, migration risk, and open questions from independent perspectives, then synthesize a recommendation.")
	fmt.Println(strings.Repeat("=", 72))
}

func skillSummaryNames(summaries []skill.Summary) []string {
	names := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		if name := strings.TrimSpace(summary.Name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func traceToolCallbacks(enabled bool) *model.Callbacks {
	if !enabled {
		return nil
	}
	var call int
	return model.NewCallbacks().RegisterBeforeModel(func(
		_ context.Context,
		args *model.BeforeModelArgs,
	) (*model.BeforeModelResult, error) {
		call++
		fmt.Printf("[before root model #%d] visible tools:\n", call)
		for _, name := range requestToolNames(args.Request) {
			fmt.Printf("  - %s\n", name)
		}
		fmt.Println()
		return nil, nil
	})
}

func requestToolNames(req *model.Request) []string {
	if req == nil {
		return nil
	}
	names := make([]string, 0, len(req.Tools))
	for name := range req.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type workflowCodePrintingTool struct {
	inner tool.CallableTool
}

func (t workflowCodePrintingTool) Declaration() *tool.Declaration {
	return t.inner.Declaration()
}

func (t workflowCodePrintingTool) Call(
	ctx context.Context,
	raw []byte,
) (any, error) {
	t.printWorkflowCode(raw)
	return t.inner.Call(ctx, raw)
}

func (t workflowCodePrintingTool) StreamableCall(
	ctx context.Context,
	raw []byte,
) (*tool.StreamReader, error) {
	t.printWorkflowCode(raw)
	streamable, ok := t.inner.(tool.StreamableTool)
	if !ok {
		return nil, fmt.Errorf(
			"workflow code printer: inner tool is not streamable",
		)
	}
	return streamable.StreamableCall(ctx, raw)
}

func (t workflowCodePrintingTool) TRPCAgentGoStructuredStreamErrorsOptIn() bool {
	structured, ok := t.inner.(interface {
		TRPCAgentGoStructuredStreamErrorsOptIn() bool
	})
	return ok && structured.TRPCAgentGoStructuredStreamErrorsOptIn()
}

func (t workflowCodePrintingTool) printWorkflowCode(raw []byte) {
	var input struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &input); err != nil ||
		strings.TrimSpace(input.Code) == "" {
		return
	}
	fmt.Fprintln(os.Stderr, "\n===== generated dynamic workflow code =====")
	fmt.Fprintln(os.Stderr, input.Code)
	fmt.Fprintln(os.Stderr, "===== end generated dynamic workflow code =====")
	fmt.Fprintln(os.Stderr)
}

func printEvents(events <-chan *event.Event) error {
	var runErr error
	for evt := range events {
		runErr = updateRunOutcome(runErr, evt)
		if evt == nil {
			continue
		}
		if evt.Response != nil && evt.Response.Error != nil {
			fmt.Fprintf(
				os.Stderr,
				"[%s] error: %s\n",
				eventLabel(evt),
				evt.Response.Error.Message,
			)
			continue
		}
		if printToolEvent(evt) {
			continue
		}
		if evt.StructuredOutput != nil {
			raw, err := json.Marshal(evt.StructuredOutput)
			if err == nil {
				fmt.Printf("[%s] structured: %s\n", eventLabel(evt), raw)
			}
		}
		if evt.Response == nil || len(evt.Response.Choices) == 0 {
			continue
		}
		choice := evt.Response.Choices[0]
		content := choice.Delta.Content
		if content == "" {
			content = choice.Message.Content
		}
		if strings.TrimSpace(content) != "" {
			fmt.Printf("[%s] %s\n", eventLabel(evt), content)
		}
	}
	return runErr
}

// updateRunOutcome mirrors Runner stream semantics for this example: the last
// terminal error is retained, a later non-partial successful non-runner
// response may recover, and a clean runner.completion must not erase an
// already-streamed terminal error.
func updateRunOutcome(runErr error, evt *event.Event) error {
	if evt == nil || evt.Response == nil || evt.IsPartial {
		return runErr
	}
	if evt.IsRunnerCompletion() {
		if evt.IsTerminalError() {
			return errors.New(evt.Response.Error.Message)
		}
		return runErr
	}
	if evt.IsTerminalError() {
		return errors.New(evt.Response.Error.Message)
	}
	if evt.Response.Error == nil {
		return nil
	}
	return runErr
}

func printToolEvent(evt *event.Event) bool {
	if evt == nil || evt.Response == nil {
		return false
	}
	if evt.Response.IsToolCallResponse() {
		for _, choice := range evt.Response.Choices {
			calls := append(choice.Message.ToolCalls, choice.Delta.ToolCalls...)
			for _, call := range calls {
				fmt.Printf(
					"[%s] tool call: %s args: %s\n",
					eventLabel(evt),
					call.Function.Name,
					formatToolCallArgs(call.Function.Name, call.Function.Arguments),
				)
			}
		}
		return true
	}
	if !evt.Response.IsToolResultResponse() {
		return false
	}
	for _, choice := range evt.Response.Choices {
		msg := choice.Message
		if msg.ToolID == "" {
			msg = choice.Delta
		}
		if msg.ToolID == "" {
			continue
		}
		content := truncateRunes(strings.TrimSpace(msg.Content), toolResultMaxRunes)
		fmt.Printf(
			"[%s] tool result: %s: %s\n",
			eventLabel(evt),
			msg.ToolName,
			content,
		)
	}
	return true
}

func formatToolCallArgs(name string, args []byte) string {
	if name != runWorkflowToolName || *showWorkflowCode {
		return string(args)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(args, &obj); err != nil {
		return omittedWorkflowArgs
	}
	if _, ok := obj["code"]; ok {
		obj["code"] = json.RawMessage(omittedWorkflowCodeJSON)
	}
	redacted, err := json.Marshal(obj)
	if err != nil {
		return omittedWorkflowArgs
	}
	return string(redacted)
}

func truncateRunes(s string, max int) string {
	runes := []rune(s)
	if max < 0 || len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "..."
}

func eventLabel(evt *event.Event) string {
	if evt == nil {
		return "event"
	}
	if strings.TrimSpace(evt.Author) != "" {
		return evt.Author
	}
	return "event"
}
