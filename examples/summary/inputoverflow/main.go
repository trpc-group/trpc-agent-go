//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/summary"
)

func main() {
	mode := flag.String("mode", "original", "overflow decision: original, replace, reject, or default")
	flag.Parse()
	// Match the business's rune-based heuristic in this offline reproduction.
	summary.SetTokenCounter(model.NewSimpleTokenCounter(model.WithApproxRunesPerToken(1.6)))
	longSpaces := regexp.MustCompile(` {32,}`)
	summarizer := summary.NewSummarizer(localSummaryModel{},
		summary.WithRequestInputTokenBudget(168_000),
		summary.WithInputOverflowHook(func(_ context.Context, in summary.InputOverflow) (*summary.OverflowDecision, error) {
			fmt.Printf("Hook input characters: %d\n", utf8.RuneCountInString(in.Text))
			switch *mode {
			case "original":
				return &summary.OverflowDecision{Text: in.Text}, nil
			case "replace":
				// This example's business policy treats long space runs as noise.
				return &summary.OverflowDecision{Text: longSpaces.ReplaceAllString(in.Text, " ")}, nil
			case "reject":
				return nil, errors.New("business rejected this summary input")
			case "default":
				return nil, nil
			default:
				return nil, fmt.Errorf("unknown mode %q", *mode)
			}
		}),
	)
	sess := &session.Session{ID: "overflow-demo", Events: demoEvents()}
	text, err := summarizer.Summarize(context.Background(), sess)
	fmt.Printf("Summary: %q\nError: %v\n", text, err)
	fmt.Printf("Committed through event: %s\n", sess.State[session.SummaryLastIncludedEventIDStateKey])
	fmt.Printf("Original events retained: %d\n", len(sess.Events))
}

// localSummaryModel accepts the submitted prompt for an offline flow demo.
// It does not represent the tokenizer or behavior of a production provider.
type localSummaryModel struct{}

func (localSummaryModel) Info() model.Info {
	return model.Info{Name: "local-overflow-demo", ContextWindow: 240_000}
}

func (localSummaryModel) GenerateContent(_ context.Context, request *model.Request) (<-chan *model.Response, error) {
	fmt.Printf("Model received prompt characters: %d\n", utf8.RuneCountInString(request.Messages[0].Content))
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{Done: true, Choices: []model.Choice{{
		Message: model.NewAssistantMessage("The first user asked for a diagram and the assistant returned one."),
	}}}
	close(responses)
	return responses, nil
}

func demoEvents() []event.Event {
	contents := []string{
		"Explain the diagram.",
		"The diagram begins here.\n" + strings.Repeat(" ", 1_470_000) + "\nThe diagram ends here.",
		"Explain the next step.",
		"The next step is to verify the result.",
	}
	roles := []model.Role{model.RoleUser, model.RoleAssistant, model.RoleUser, model.RoleAssistant}
	events := make([]event.Event, len(contents))
	now := time.Now().UTC()
	for i, content := range contents {
		author := "agent"
		if roles[i] == model.RoleUser {
			author = "user"
		}
		events[i] = event.Event{
			ID: fmt.Sprintf("event-%d", i), Author: author,
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
			Response: &model.Response{ID: fmt.Sprintf("response-%d", i),
				Choices: []model.Choice{{Message: model.Message{Role: roles[i], Content: content}}},
			},
		}
	}
	return events
}
