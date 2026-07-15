//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package fakemodel

import (
	"context"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
)

func TestNewForFixtureRegistersFixtureScenarios(t *testing.T) {
	fixtures := []string{
		"acceptance-clean",
		"acceptance-context-leak",
		"acceptance-database-lifecycle",
		"acceptance-duplicate-finding",
		"acceptance-missing-tests",
		"acceptance-resource-leak",
		"acceptance-sandbox-failure",
		"acceptance-secret-redaction",
		"acceptance-security",
	}

	for _, fixture := range fixtures {
		t.Run(fixture, func(t *testing.T) {
			fake, err := NewForFixture(fixture)
			if err != nil {
				t.Fatal(err)
			}
			if fake.Fixture() != fixture {
				t.Fatalf("fixture = %q, want %q", fake.Fixture(), fixture)
			}
		})
	}
}

func TestNewForFixtureRejectsUnknownScenario(t *testing.T) {
	_, err := NewForFixture("not-registered")
	if err == nil || !strings.Contains(err.Error(), "not-registered") {
		t.Fatalf("NewForFixture error = %v, want unknown fixture error", err)
	}
}

func TestGenerateContentCompletesDeterministically(t *testing.T) {
	fake, err := NewForFixture("acceptance-clean")
	if err != nil {
		t.Fatal(err)
	}
	request := &model.Request{Messages: []model.Message{{
		Role:    model.RoleUser,
		Content: "Review this code change.",
	}}}
	responses, err := fake.GenerateContent(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := <-responses
	if !ok || response == nil {
		t.Fatal("fake model returned no response")
	}
	if !response.Done || response.IsPartial || response.IsToolCallResponse() {
		t.Fatalf("response = %#v, want final non-tool completion", response)
	}
	if response.Model != modelName {
		t.Fatalf("response model = %q, want %q", response.Model, modelName)
	}
	if len(response.Choices) != 1 {
		t.Fatalf("response choices = %d, want 1", len(response.Choices))
	}
	if got, want := response.Choices[0].Message.Content, "Accepted prepared review input for acceptance-clean."; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}
	if _, ok := <-responses; ok {
		t.Fatal("fake model returned more than one response")
	}
}

func TestGenerateContentRejectsMissingUserInput(t *testing.T) {
	fake, err := NewForFixture("acceptance-clean")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fake.GenerateContent(context.Background(), &model.Request{})
	if err == nil {
		t.Fatal("GenerateContent accepted a request without user input")
	}
}
