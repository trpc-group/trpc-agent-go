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
	"bytes"
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"unicode/utf8"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/event"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type stubRunner struct {
	events []*event.Event
	err    error
}

func (s stubRunner) Run(
	_ context.Context,
	_ string,
	_ string,
	_ model.Message,
	_ ...agent.RunOption,
) (<-chan *event.Event, error) {
	if s.err != nil {
		return nil, s.err
	}
	ch := make(chan *event.Event, len(s.events))
	for _, evt := range s.events {
		ch <- evt
	}
	close(ch)
	return ch, nil
}

func (stubRunner) Close() error { return nil }

func TestProcessMessage_RunOutcome(t *testing.T) {
	tests := []struct {
		name       string
		events     []*event.Event
		wantErr    string
		wantOutput string
		notErr     string
	}{
		{
			name: "terminal error then clean completion",
			events: []*event.Event{
				newTerminalError("first failed"),
				newPartialContent("DRAIN_MARKER"),
				newRunnerCompletion(nil),
			},
			wantErr:    "first failed",
			wantOutput: "DRAIN_MARKER",
		},
		{
			name: "terminal error then complete success recovers",
			events: []*event.Event{
				newTerminalError("recovered failure"),
				newCompleteSuccess("all good"),
				newRunnerCompletion(nil),
			},
		},
		{
			name: "terminal error then non-final success recovers",
			events: []*event.Event{
				newTerminalError("recovered failure"),
				newToolCall("continue_work", []byte(`{"step":2}`)),
				newRunnerCompletion(nil),
			},
		},
		{
			name: "latest terminal error wins",
			events: []*event.Event{
				newTerminalError("first failed"),
				newTerminalError("second failed"),
				newRunnerCompletion(nil),
			},
			wantErr: "second failed",
			notErr:  "first failed",
		},
		{
			name: "non-terminal error promoted on completion",
			events: []*event.Event{
				newNonTerminalError("soft failed"),
				newRunnerCompletion(&model.ResponseError{Message: "soft failed"}),
			},
			wantErr: "soft failed",
		},
		{
			name: "non-terminal error then success recovers",
			events: []*event.Event{
				newNonTerminalError("soft failed"),
				newCompleteSuccess("recovered"),
				newRunnerCompletion(nil),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := processEvents(t, tt.events...)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("processMessage() error = %v, want nil", err)
				}
			} else {
				if err == nil {
					t.Fatal("processMessage() error = nil, want error")
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("processMessage() error = %q, want to contain %q", err, tt.wantErr)
				}
				if tt.notErr != "" && strings.Contains(err.Error(), tt.notErr) {
					t.Fatalf("processMessage() error = %q, should not contain %q", err, tt.notErr)
				}
			}
			if tt.wantOutput != "" && !strings.Contains(out, tt.wantOutput) {
				t.Fatalf("processMessage() output %q, want to contain %q", out, tt.wantOutput)
			}
		})
	}
}

func TestTruncateRunes_UTF8Boundary(t *testing.T) {
	exact := strings.Repeat("a", toolResultMaxRunes-1) + "世"
	if got := truncateRunes(exact, toolResultMaxRunes); got != exact {
		t.Fatalf("truncateRunes() = %q, want unchanged 500-rune input", got)
	}
	if !utf8.ValidString(exact) {
		t.Fatal("fixture is not valid UTF-8")
	}

	over := strings.Repeat("a", toolResultMaxRunes-1) + "世界"
	got := truncateRunes(over, toolResultMaxRunes)
	want := strings.Repeat("a", toolResultMaxRunes-1) + "世" + "..."
	if got != want {
		t.Fatalf("truncateRunes() = %q, want %q", got, want)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("truncateRunes() produced invalid UTF-8: %q", got)
	}
	if strings.Contains(got, "界") {
		t.Fatalf("truncateRunes() kept the overflow rune: %q", got)
	}

	out, err := processEvents(t,
		newToolResult("skill_load", exact),
		newToolResult("skill_load", over),
		newRunnerCompletion(nil),
	)
	if err != nil {
		t.Fatalf("processMessage() error = %v", err)
	}
	if !utf8.ValidString(out) {
		t.Fatalf("tool result output is not valid UTF-8: %q", out)
	}
	if !strings.Contains(out, "世") {
		t.Fatalf("tool result lost the UTF-8 boundary rune: %q", out)
	}
	if strings.Count(out, "界") != 0 {
		t.Fatalf("tool result kept the overflow rune: %q", out)
	}
}

func TestShowWorkflowCodeFlag(t *testing.T) {
	const secret = "SECRET_WORKFLOW"
	args := []byte(`{"code":"return '` + secret + `'","foo":1}`)

	t.Run("disabled omits code and keeps other args", func(t *testing.T) {
		withShowWorkflowCode(t, false)
		out, err := processEvents(t,
			newToolCall(runWorkflowToolName, args),
			newRunnerCompletion(nil),
		)
		if err != nil {
			t.Fatalf("processMessage() error = %v", err)
		}
		if strings.Contains(out, secret) {
			t.Fatalf("disabled flag leaked workflow code: %s", out)
		}
		if !strings.Contains(out, "[omitted]") {
			t.Fatalf("disabled flag missing omit marker: %s", out)
		}
		if !strings.Contains(out, `"foo":1`) {
			t.Fatalf("disabled flag dropped non-code args: %s", out)
		}
	})

	t.Run("disabled fail closed on invalid json", func(t *testing.T) {
		withShowWorkflowCode(t, false)
		out, err := processEvents(t,
			newToolCall(runWorkflowToolName, []byte("return '"+secret+"'")),
			newRunnerCompletion(nil),
		)
		if err != nil {
			t.Fatalf("processMessage() error = %v", err)
		}
		if strings.Contains(out, secret) {
			t.Fatalf("invalid JSON leaked workflow code: %s", out)
		}
		if !strings.Contains(out, omittedWorkflowArgs) {
			t.Fatalf("invalid JSON missing fail-closed marker: %s", out)
		}
	})

	t.Run("other tools stay visible when disabled", func(t *testing.T) {
		withShowWorkflowCode(t, false)
		out, err := processEvents(t,
			newToolCall("skill_load", []byte(`{"name":"quality-loop"}`)),
			newRunnerCompletion(nil),
		)
		if err != nil {
			t.Fatalf("processMessage() error = %v", err)
		}
		if !strings.Contains(out, "quality-loop") {
			t.Fatalf("non-workflow tool args were hidden: %s", out)
		}
	})

	t.Run("enabled prints full source through wrapper", func(t *testing.T) {
		withShowWorkflowCode(t, true)
		out, err := captureOutput(t, func() error {
			workflowCodePrintingTool{}.printWorkflowCode(args)
			return nil
		})
		if err != nil {
			t.Fatalf("printWorkflowCode() error = %v", err)
		}
		if !strings.Contains(out, secret) {
			t.Fatalf("enabled wrapper omitted workflow source: %s", out)
		}
	})
}

func processEvents(t *testing.T, events ...*event.Event) (string, error) {
	t.Helper()
	chat := &dynamicWorkflowChat{
		runner:    stubRunner{events: events},
		userID:    userID,
		sessionID: "test-session",
	}
	return captureOutput(t, func() error {
		return chat.processMessage(context.Background(), "hello")
	})
}

func withShowWorkflowCode(t *testing.T, enabled bool) {
	t.Helper()
	original := *showWorkflowCode
	*showWorkflowCode = enabled
	t.Cleanup(func() { *showWorkflowCode = original })
}

func newTerminalError(message string) *event.Event {
	return event.NewErrorEvent("inv", "agent", "test_error", message)
}

func newNonTerminalError(message string) *event.Event {
	return event.NewResponseEvent("inv", "agent", &model.Response{
		Object:    model.ObjectTypeChatCompletion,
		Done:      false,
		IsPartial: false,
		Error:     &model.ResponseError{Message: message},
	})
}

func newCompleteSuccess(content string) *event.Event {
	return event.NewResponseEvent("inv", "agent", &model.Response{
		Object:    model.ObjectTypeChatCompletion,
		Done:      true,
		IsPartial: false,
		Choices: []model.Choice{{
			Message: model.NewAssistantMessage(content),
		}},
	})
}

func newPartialContent(content string) *event.Event {
	return event.NewResponseEvent("inv", "agent", &model.Response{
		Object:    model.ObjectTypeChatCompletionChunk,
		Done:      false,
		IsPartial: true,
		Choices: []model.Choice{{
			Delta: model.Message{Content: content},
		}},
	})
}

func newRunnerCompletion(err *model.ResponseError) *event.Event {
	return event.NewResponseEvent("inv", appName, &model.Response{
		Object:    model.ObjectTypeRunnerCompletion,
		Done:      true,
		IsPartial: false,
		Error:     err,
	})
}

func newToolCall(name string, args []byte) *event.Event {
	return event.NewResponseEvent("inv", "agent", &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				ToolCalls: []model.ToolCall{{
					Function: model.FunctionDefinitionParam{
						Name:      name,
						Arguments: args,
					},
				}},
			},
		}},
	})
}

func newToolResult(name, content string) *event.Event {
	return event.NewResponseEvent("inv", "agent", &model.Response{
		Choices: []model.Choice{{
			Message: model.Message{
				Role:     model.RoleTool,
				ToolID:   "call-1",
				ToolName: name,
				Content:  content,
			},
		}},
	})
}

func captureOutput(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	stdoutR, stdoutW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stdout pipe: %v", err)
	}
	stderrR, stderrW, err := os.Pipe()
	if err != nil {
		t.Fatalf("stderr pipe: %v", err)
	}
	origOut, origErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = stdoutW, stderrW
	defer func() {
		os.Stdout = origOut
		os.Stderr = origErr
	}()

	var stdoutBuf, stderrBuf bytes.Buffer
	stdoutDone := make(chan error, 1)
	stderrDone := make(chan error, 1)
	go func() {
		_, copyErr := io.Copy(&stdoutBuf, stdoutR)
		stdoutDone <- copyErr
	}()
	go func() {
		_, copyErr := io.Copy(&stderrBuf, stderrR)
		stderrDone <- copyErr
	}()

	runErr := fn()
	_ = stdoutW.Close()
	_ = stderrW.Close()
	if copyErr := <-stdoutDone; copyErr != nil {
		t.Fatalf("read stdout: %v", copyErr)
	}
	if copyErr := <-stderrDone; copyErr != nil {
		t.Fatalf("read stderr: %v", copyErr)
	}
	_ = stdoutR.Close()
	_ = stderrR.Close()
	return stdoutBuf.String() + stderrBuf.String(), runErr
}
