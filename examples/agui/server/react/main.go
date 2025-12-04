//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package main is the main package for the AG-UI server.
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"strings"

	aguievents "github.com/ag-ui-protocol/ag-ui/sdks/community/go/pkg/core/events"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/log"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/planner/react"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/adapter"
	aguirunner "trpc.group/trpc-go/trpc-agent-go/server/agui/runner"
	"trpc.group/trpc-go/trpc-agent-go/server/agui/translator"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

var (
	modelName = flag.String("model", "deepseek-chat", "Model to use")
	isStream  = flag.Bool("stream", true, "Whether to stream the response")
	address   = flag.String("address", "127.0.0.1:8080", "Listen address")
	path      = flag.String("path", "/agui", "HTTP path")
)

func main() {
	flag.Parse()
	modelInstance := openai.New(*modelName)
	generationConfig := model.GenerationConfig{
		MaxTokens:   intPtr(512),
		Temperature: floatPtr(0.7),
		Stream:      *isStream,
	}
	calculatorTool := function.NewFunctionTool(
		calculator,
		function.WithName("calculator"),
		function.WithDescription("A calculator tool, you can use it to calculate the result of the operation. "+
			"a is the first number, b is the second number, "+
			"the operation can be add, subtract, multiply, divide, power."),
	)
	agent := llmagent.New(
		"agui-agent",
		llmagent.WithTools([]tool.Tool{calculatorTool}),
		llmagent.WithModel(modelInstance),
		llmagent.WithGenerationConfig(generationConfig),
		llmagent.WithInstruction("You are a helpful assistant."),
		llmagent.WithPlanner(react.New()),
	)
	runner := runner.NewRunner(agent.Info().Name, agent)
	// Ensure runner resources are cleaned up (trpc-agent-go >= v0.5.0)
	defer runner.Close()
	server, err := agui.New(
		runner,
		agui.WithPath(*path),
		agui.WithAGUIRunnerOptions(aguirunner.WithTranslatorFactory(newReactTranslator)),
	)
	if err != nil {
		log.Fatalf("failed to create AG-UI server: %v", err)
	}
	log.Infof("AG-UI: serving agent %q on http://%s%s", agent.Info().Name, *address, *path)
	if err := http.ListenAndServe(*address, server.Handler()); err != nil {
		log.Fatalf("server stopped with error: %v", err)
	}
}

type reactTranslator struct {
	inner            translator.Translator
	message          string
	receivingMessage bool
}

func newReactTranslator(ctx context.Context, input *adapter.RunAgentInput) translator.Translator {
	return &reactTranslator{
		inner: translator.New(ctx, input.ThreadID, input.RunID),
	}
}

// Translate routes AG-UI events through a small state machine to rebuild final answers and custom sections for React UI consumption.
func (t *reactTranslator) Translate(ctx context.Context, event *event.Event) ([]aguievents.Event, error) {
	events, err := t.inner.Translate(ctx, event)
	if err != nil {
		return nil, err
	}
	var reactEvents []aguievents.Event
	for _, e := range events {
		processed, err := t.handleAGUIEvent(e)
		if err != nil {
			return nil, err
		}
		if len(processed) > 0 {
			reactEvents = append(reactEvents, processed...)
		}
	}
	return reactEvents, nil
}

// handleAGUIEvent keeps track of streaming state and dispatches events to the appropriate handlers.
func (t *reactTranslator) handleAGUIEvent(e aguievents.Event) ([]aguievents.Event, error) {
	if t.receivingMessage {
		return t.handleReceivingEvent(e)
	}
	if e.Type() == aguievents.EventTypeTextMessageStart {
		t.startReceiving()
		return nil, nil
	}
	return []aguievents.Event{e}, nil
}

// handleReceivingEvent consumes text message chunks until the message is done.
func (t *reactTranslator) handleReceivingEvent(e aguievents.Event) ([]aguievents.Event, error) {
	switch e.Type() {
	case aguievents.EventTypeTextMessageContent:
		return t.collectContent(e)
	case aguievents.EventTypeTextMessageEnd:
		return t.finishMessage(e)
	default:
		return nil, fmt.Errorf("unexpected event type %s while receiving message", e.Type())
	}
}

// collectContent appends streamed delta text into the buffered message.
func (t *reactTranslator) collectContent(e aguievents.Event) ([]aguievents.Event, error) {
	contentEvent, ok := e.(*aguievents.TextMessageContentEvent)
	if !ok {
		return nil, fmt.Errorf("invalid text message content event %T", e)
	}
	t.message += contentEvent.Delta
	return nil, nil
}

// finishMessage finalizes the aggregated text and converts planner sections to React events.
func (t *reactTranslator) finishMessage(e aguievents.Event) ([]aguievents.Event, error) {
	endEvent, ok := e.(*aguievents.TextMessageEndEvent)
	if !ok {
		return nil, fmt.Errorf("invalid text message end event %T", e)
	}
	sections := splitTaggedSections(t.message)
	t.resetReceiving()
	return t.buildSectionEvents(endEvent.MessageID, sections), nil
}

// startReceiving initializes state for a new streamed message.
func (t *reactTranslator) startReceiving() {
	t.receivingMessage = true
	t.message = ""
}

// resetReceiving clears state after a message has been fully processed.
func (t *reactTranslator) resetReceiving() {
	t.receivingMessage = false
	t.message = ""
}

// buildSectionEvents converts planner tagged sections into AG-UI events for the React frontend.
func (t *reactTranslator) buildSectionEvents(messageID string, sections []taggedSection) []aguievents.Event {
	var reactEvents []aguievents.Event
	for _, section := range sections {
		if section.tag == react.FinalAnswerTag {
			reactEvents = append(reactEvents,
				aguievents.NewTextMessageStartEvent(messageID),
				aguievents.NewTextMessageContentEvent(messageID, section.content),
				aguievents.NewTextMessageEndEvent(messageID))
			continue
		}
		customEvent := aguievents.NewCustomEvent(
			fmt.Sprintf("react.%s", strings.ToLower(section.name)),
			aguievents.WithValue(map[string]any{
				"tag":       section.tag,
				"content":   section.content,
				"messageId": messageID,
			}),
		)
		reactEvents = append(reactEvents, customEvent)
	}
	return reactEvents
}

var tagPattern = regexp.MustCompile(`/\*([A-Z_]+)\*/`)

type taggedSection struct {
	tag     string
	name    string
	content string
}

// splitTaggedSections splits content by React planner tags.
func splitTaggedSections(message string) []taggedSection {
	matches := tagPattern.FindAllStringSubmatchIndex(message, -1)
	sections := make([]taggedSection, 0, len(matches))
	for i, match := range matches {
		tag := message[match[0]:match[1]]
		name := message[match[2]:match[3]]
		contentStart := match[1]
		contentEnd := len(message)
		if i+1 < len(matches) {
			contentEnd = matches[i+1][0]
		}
		content := message[contentStart:contentEnd]
		sections = append(sections, taggedSection{tag: tag, name: name, content: content})
	}
	return sections
}

func calculator(ctx context.Context, args calculatorArgs) (calculatorResult, error) {
	var result float64
	switch args.Operation {
	case "add", "+":
		result = args.A + args.B
	case "subtract", "-":
		result = args.A - args.B
	case "multiply", "*":
		result = args.A * args.B
	case "divide", "/":
		result = args.A / args.B
	case "power", "^":
		result = math.Pow(args.A, args.B)
	default:
		return calculatorResult{Result: 0}, fmt.Errorf("invalid operation: %s", args.Operation)
	}
	return calculatorResult{Result: result}, nil
}

type calculatorArgs struct {
	Operation string  `json:"operation" description:"add, subtract, multiply, divide, power"`
	A         float64 `json:"a" description:"First number"`
	B         float64 `json:"b" description:"Second number"`
}

type calculatorResult struct {
	Result float64 `json:"result"`
}

func intPtr(i int) *int {
	return &i
}

func floatPtr(f float64) *float64 {
	return &f
}
