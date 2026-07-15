//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Package fakemodel provides deterministic model behavior for offline review
// runs.
package fakemodel

import (
	"context"
	"errors"
	"fmt"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

const modelName = "fake-model"

type scenario struct {
	fixture    string
	completion string
}

var scenarios = map[string]scenario{
	"acceptance-clean":              inputScenario("acceptance-clean"),
	"acceptance-context-leak":       inputScenario("acceptance-context-leak"),
	"acceptance-database-lifecycle": inputScenario("acceptance-database-lifecycle"),
	"acceptance-duplicate-finding":  inputScenario("acceptance-duplicate-finding"),
	"acceptance-missing-tests":      inputScenario("acceptance-missing-tests"),
	"acceptance-resource-leak":      inputScenario("acceptance-resource-leak"),
	"acceptance-sandbox-failure":    inputScenario("acceptance-sandbox-failure"),
	"acceptance-secret-redaction":   inputScenario("acceptance-secret-redaction"),
	"acceptance-security":           inputScenario("acceptance-security"),
}

// FakeModel is one task-scoped deterministic model for a registered fixture
// scenario. Each scenario implements the review flow supported by the current
// development stage.
type FakeModel struct {
	scenario scenario
}

// NewForFixture creates the deterministic model scenario registered for a
// review fixture.
func NewForFixture(fixture string) (fake *FakeModel, err error) {
	scenario, ok := scenarios[fixture]
	if !ok {
		return nil, fmt.Errorf("fake model fixture %q is not registered", fixture)
	}
	return &FakeModel{scenario: scenario}, nil
}

// GenerateContent validates the model-visible input and returns the
// deterministic completion for the selected fixture scenario.
func (f *FakeModel) GenerateContent(ctx context.Context, request *model.Request) (responses <-chan *model.Response, err error) {
	if f == nil {
		return nil, errors.New("fake model is nil")
	}
	if request == nil {
		return nil, errors.New("fake model request is nil")
	}
	if !hasUserMessage(request.Messages) {
		return nil, errors.New("fake model request requires user input")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	finishReason := "stop"
	response := &model.Response{
		ID:     "fake-model-" + f.scenario.fixture,
		Object: model.ObjectTypeChatCompletion,
		Model:  modelName,
		Done:   true,
		Choices: []model.Choice{{
			Index:        0,
			Message:      model.NewAssistantMessage(f.scenario.completion),
			FinishReason: &finishReason,
		}},
	}
	ch := make(chan *model.Response, 1)
	ch <- response
	close(ch)
	return ch, nil
}

// Info returns the stable provider-independent name used by fake-model runs.
func (f *FakeModel) Info() (info model.Info) {
	return model.Info{Name: modelName}
}

// Fixture identifies the fixture scenario bound to this model instance.
func (f *FakeModel) Fixture() (fixture string) {
	if f == nil {
		return ""
	}
	return f.scenario.fixture
}

func inputScenario(fixture string) (configured scenario) {
	return scenario{
		fixture:    fixture,
		completion: "Accepted prepared review input for " + fixture + ".",
	}
}

func hasUserMessage(messages []model.Message) bool {
	for _, message := range messages {
		if message.Role == model.RoleUser {
			return true
		}
	}
	return false
}
