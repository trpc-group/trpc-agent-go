//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package fakemodel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestNewForFixtureRejectsUnknownScenario(t *testing.T) {
	_, err := NewForFixture("not-registered")
	if err == nil || !strings.Contains(err.Error(), "not-registered") {
		t.Fatalf("NewForFixture error = %v, want unknown fixture error", err)
	}
}

func TestGenerateContentRunsDeterministicToolCallingFlow(t *testing.T) {
	fake, err := NewForFixture("acceptance-clean")
	if err != nil {
		t.Fatal(err)
	}
	messages := []model.Message{{
		Role:    model.RoleUser,
		Content: "Review this code change.",
	}}
	availableTools := map[string]tool.Tool{
		"skill_load":            nil,
		"workspace_exec":        nil,
		"submit_review_results": nil,
	}

	load := generateOne(t, fake, &model.Request{Messages: messages, Tools: availableTools})
	assertToolCall(t, load, "skill_load", map[string]any{
		"skill": "code-review",
		"docs":  []any{"references/rules.md"},
	})
	messages = append(messages, load.Choices[0].Message, model.Message{
		Role: model.RoleTool, ToolID: load.Choices[0].Message.ToolCalls[0].ID,
		ToolName: "skill_load", Content: `"loaded: code-review"`,
	})

	runChecks := generateOne(t, fake, &model.Request{Messages: messages, Tools: availableTools})
	assertToolCall(t, runChecks, "workspace_exec", map[string]any{
		"command":     "sh skills/code-review/scripts/run-go-checks.sh work/inputs/repo",
		"timeout_sec": float64(120),
	})
	if explanation := runChecks.Choices[0].Message.Content; explanation == "" ||
		!strings.Contains(explanation, "go test") || !strings.Contains(explanation, "go vet") {
		t.Fatalf("workspace_exec permission explanation = %q", explanation)
	}
	messages = append(messages, runChecks.Choices[0].Message, model.Message{
		Role: model.RoleTool, ToolID: runChecks.Choices[0].Message.ToolCalls[0].ID,
		ToolName: "workspace_exec", Content: `{"status":"completed","output":"go test: ok","exit_code":0}`,
	})

	submit := generateOne(t, fake, &model.Request{Messages: messages, Tools: availableTools})
	assertToolCall(t, submit, "submit_review_results", map[string]any{
		"conclusion": "No actionable issues found. Go checks completed successfully.",
	})
	messages = append(messages, submit.Choices[0].Message, model.Message{
		Role: model.RoleTool, ToolID: submit.Choices[0].Message.ToolCalls[0].ID,
		ToolName: "submit_review_results", Content: `{"status":"accepted"}`,
	})

	completion := generateOne(t, fake, &model.Request{Messages: messages, Tools: availableTools})
	if completion.IsToolCallResponse() {
		t.Fatalf("completion = %#v, want final response", completion)
	}
	if got, want := completion.Choices[0].Message.Content, "Review results submitted for acceptance-clean."; got != want {
		t.Fatalf("response content = %q, want %q", got, want)
	}
}

func TestGenerateContentCorrectsRejectedResultRouting(t *testing.T) {
	fake, err := NewForFixture("acceptance-duplicate-finding")
	if err != nil {
		t.Fatal(err)
	}
	tools := map[string]tool.Tool{"submit_review_results": nil}
	messages := []model.Message{
		{Role: model.RoleUser, Content: "Review this code change."},
		{
			Role: model.RoleTool, ToolName: "workspace_exec",
			Content: `{"status":"completed","exit_code":0}`,
		},
	}
	first := generateOne(t, fake, &model.Request{
		Messages: messages,
		Tools:    tools,
	})
	firstCall := first.Choices[0].Message.ToolCalls[0]
	var conflicting submission
	if err := json.Unmarshal(firstCall.Function.Arguments, &conflicting); err != nil {
		t.Fatal(err)
	}
	if len(conflicting.Findings) != 2 || len(conflicting.Warnings) != 1 {
		t.Fatalf("initial submission = %#v, want duplicate findings plus one routing conflict", conflicting)
	}

	messages = append(messages, first.Choices[0].Message, model.Message{
		Role: model.RoleTool, ToolID: firstCall.ID, ToolName: submitResultsTool,
		Content: `review result kind conflict: finding[0] conflicts with warning[0]`,
	})
	retry := generateOne(t, fake, &model.Request{
		Messages: messages,
		Tools:    tools,
	})
	retryCall := retry.Choices[0].Message.ToolCalls[0]
	var corrected submission
	if err := json.Unmarshal(retryCall.Function.Arguments, &corrected); err != nil {
		t.Fatal(err)
	}
	if len(corrected.Findings) != 2 || len(corrected.Warnings) != 0 {
		t.Fatalf("corrected submission = %#v, want only the mergeable findings", corrected)
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

func generateOne(t *testing.T, fake *FakeModel, request *model.Request) *model.Response {
	t.Helper()
	responses, err := fake.GenerateContent(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	response, ok := <-responses
	if !ok || response == nil {
		t.Fatal("fake model returned no response")
	}
	if _, ok := <-responses; ok {
		t.Fatal("fake model returned more than one response")
	}
	if !response.Done || response.IsPartial || response.Model != modelName || len(response.Choices) != 1 {
		t.Fatalf("invalid fake response: %#v", response)
	}
	return response
}

func assertToolCall(t *testing.T, response *model.Response, name string, wantArgs map[string]any) {
	t.Helper()
	if !response.IsToolCallResponse() {
		t.Fatalf("response = %#v, want tool call", response)
	}
	toolCalls := response.Choices[0].Message.ToolCalls
	if len(toolCalls) != 1 || toolCalls[0].Function.Name != name {
		t.Fatalf("tool calls = %#v, want one %s call", toolCalls, name)
	}
	var got map[string]any
	if err := json.Unmarshal(toolCalls[0].Function.Arguments, &got); err != nil {
		t.Fatal(err)
	}
	for key, want := range wantArgs {
		gotJSON, _ := json.Marshal(got[key])
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Fatalf("argument %s = %s, want %s", key, gotJSON, wantJSON)
		}
	}
}
