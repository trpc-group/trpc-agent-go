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
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func TestGAIAFinalAnswerVerifier(t *testing.T) {
	t.Parallel()

	v := gaiaFinalAnswerVerifier{}

	res, err := v.Verify(
		context.Background(),
		&agent.Invocation{},
		nil,
	)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Passed {
		t.Fatalf("Passed = true, want false")
	}

	okEvt := &event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "FINAL ANSWER: 42",
				},
			}},
		},
	}
	res, err = v.Verify(context.Background(), &agent.Invocation{}, okEvt)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.Passed {
		t.Fatalf("Passed = false, want true")
	}

	partText := "FINAL ANSWER: 42"
	okEvtParts := &event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role: model.RoleAssistant,
					ContentParts: []model.ContentPart{{
						Type: model.ContentTypeText,
						Text: &partText,
					}},
				},
			}},
		},
	}
	res, err = v.Verify(context.Background(), &agent.Invocation{}, okEvtParts)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !res.Passed {
		t.Fatalf("Passed = false, want true (ContentParts)")
	}

	badEvt := &event.Event{
		Response: &model.Response{
			Done: true,
			Choices: []model.Choice{{
				Index: 0,
				Message: model.Message{
					Role:    model.RoleAssistant,
					Content: "Answer: 42",
				},
			}},
		},
	}
	res, err = v.Verify(context.Background(), &agent.Invocation{}, badEvt)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if res.Passed {
		t.Fatalf("Passed = true, want false")
	}
	if !strings.Contains(res.Feedback, gaiaFinalAnswerPrefix) {
		t.Fatalf(
			"feedback missing %q: %q",
			gaiaFinalAnswerPrefix,
			res.Feedback,
		)
	}
}

func TestGAIAFinalAnswerVerifier_ReturnTypes(t *testing.T) {
	t.Parallel()

	var v gaiaFinalAnswerVerifier
	var _ runner.Verifier = v
}
