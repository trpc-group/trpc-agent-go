//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package llmagent_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	openaiopt "github.com/openai/openai-go/option"

	"trpc.group/trpc-go/trpc-agent-go/agent"
	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/model/openai"
	"trpc.group/trpc-go/trpc-agent-go/runner"
	"trpc.group/trpc-go/trpc-agent-go/session"
	"trpc.group/trpc-go/trpc-agent-go/session/inmemory"
	"trpc.group/trpc-go/trpc-agent-go/tool"
	"trpc.group/trpc-go/trpc-agent-go/tool/function"
)

const (
	truncatedToolCallMessage = "truncated by the length limit"
	repairableArguments      = `{"content":"truncated`
	maxLLMExceededMessage    = "max LLM calls (3) exceeded"
)

type truncatedWriteArgs struct {
	FilePath string `json:"file_path" jsonschema:"required,description=Absolute path."`
	Content  string `json:"content" jsonschema:"required,description=File content."`
}

type truncatedWriteResult struct {
	Status string `json:"status"`
}

type truncatedWireMessage struct {
	Role      string          `json:"role"`
	Content   string          `json:"content"`
	ToolCalls json.RawMessage `json:"tool_calls"`
}

type truncatedWireRequest struct {
	Stream   bool                   `json:"stream"`
	Messages []truncatedWireMessage `json:"messages"`
}

// truncatedToolCallServer serves a provider that always finishes with
// finish_reason=length while emitting a tool call whose arguments are cut off.
type truncatedToolCallServer struct {
	mu                   sync.Mutex
	requests             []truncatedWireRequest
	resuming             atomic.Bool
	textToolCalls        atomic.Bool
	laterTruncatedChoice bool
}

func (s *truncatedToolCallServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

func (s *truncatedToolCallServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var request truncatedWireRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, request)
	n := len(s.requests)
	s.mu.Unlock()
	if n > 12 {
		http.Error(w, "test safety cap", http.StatusBadRequest)
		return
	}

	call := map[string]any{
		"id":   fmt.Sprintf("call_%d", n),
		"type": "function",
		"function": map[string]any{
			"name":      "file_write",
			"arguments": repairableArguments,
		},
	}
	finishReason := model.FinishReasonLength
	if s.laterTruncatedChoice {
		call["function"].(map[string]any)["arguments"] = `{"file_path":"/mock/example.txt","content":"complete"}`
		finishReason = "tool_calls"
	}
	if request.Stream {
		call["index"] = 0
	}
	response := func(object string, choice map[string]any) map[string]any {
		choices := []any{choice}
		if s.laterTruncatedChoice && n == 1 {
			choices = append(choices, map[string]any{
				"index": 1,
				"message": map[string]any{
					"role": "assistant",
					"tool_calls": []any{map[string]any{
						"id": "call-unselected", "type": "function",
						"function": map[string]any{"name": "file_write", "arguments": `{"content":"unselected-probe`},
					}},
				},
				"finish_reason": model.FinishReasonLength,
			})
		}
		return map[string]any{
			"id":      fmt.Sprintf("response_%d", n),
			"object":  object,
			"created": 1,
			"model":   "mock-length",
			"choices": choices,
		}
	}
	// After the rejected truncated call, the provider answers the resumed run
	// with a plain stop response.
	if s.resuming.Load() || (s.laterTruncatedChoice && n > 1) {
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, choice := range []map[string]any{
				{
					"index":         0,
					"delta":         map[string]any{"role": "assistant", "content": "Finished."},
					"finish_reason": nil,
				},
				{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"},
			} {
				data, _ := json.Marshal(response("chat.completion.chunk", choice))
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response("chat.completion", map[string]any{
			"index": 0,
			"message": map[string]any{
				"role":    "assistant",
				"content": "Finished.",
			},
			"finish_reason": "stop",
		}))
		return
	}
	content := "Writing the file."
	var toolCalls []any
	if s.textToolCalls.Load() {
		// Emit the tool call as visible text so only text repair turns it into a
		// structured call. Text repair rewrites the finish reason afterwards.
		content = "<tool_call>file_write" +
			"<arg_key>file_path</arg_key><arg_value>/mock/example.txt</arg_value>" +
			"<arg_key>content</arg_key><arg_value>complete</arg_value></tool_call>"
	} else {
		toolCalls = []any{call}
	}
	if request.Stream {
		w.Header().Set("Content-Type", "text/event-stream")
		delta := map[string]any{"role": "assistant", "content": content}
		if toolCalls != nil {
			delta["tool_calls"] = toolCalls
		}
		for _, choice := range []map[string]any{
			{"index": 0, "delta": delta, "finish_reason": nil},
			{"index": 0, "delta": map[string]any{}, "finish_reason": finishReason},
		} {
			data, _ := json.Marshal(response("chat.completion.chunk", choice))
			fmt.Fprintf(w, "data: %s\n\n", data)
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	message := map[string]any{"role": "assistant", "content": content}
	if toolCalls != nil {
		message["tool_calls"] = toolCalls
	}
	_ = json.NewEncoder(w).Encode(response("chat.completion", map[string]any{
		"index":         0,
		"message":       message,
		"finish_reason": finishReason,
	}))
}

type truncatedRun struct {
	requests           int
	toolCalls          int32
	errors             []string
	errorTypes         []string
	resumeRequests     int
	resumeToolCalls    int32
	resumeErrors       []string
	historyToolCallIDs []string
}

// runTruncatedScenario runs one agent against the truncated tool-call provider.
func runTruncatedScenario(
	t *testing.T,
	stream bool,
	policy agent.TruncatedToolCallPolicy,
	textToolCalls bool,
	extraOptions ...llmagent.Option,
) truncatedRun {
	t.Helper()
	stub := &truncatedToolCallServer{}
	stub.textToolCalls.Store(textToolCalls)
	return runTruncatedServer(t, stream, policy, stub, policy != agent.TruncatedToolCallPolicyIgnore, extraOptions...)
}

func runTruncatedServer(
	t *testing.T,
	stream bool,
	policy agent.TruncatedToolCallPolicy,
	stub *truncatedToolCallServer,
	checkRejected bool,
	extraOptions ...llmagent.Option,
) truncatedRun {
	t.Helper()
	server := httptest.NewServer(stub)
	defer server.Close()

	var toolCalls atomic.Int32
	fileWrite := function.NewFunctionTool(
		func(_ context.Context, args truncatedWriteArgs) (truncatedWriteResult, error) {
			toolCalls.Add(1)
			if args.FilePath == "" {
				return truncatedWriteResult{Status: "write file failed: path is empty"}, nil
			}
			return truncatedWriteResult{Status: "success"}, nil
		},
		function.WithName("file_write"),
		function.WithDescription("Write a mock file."),
	)
	llm := openai.New(
		"mock-length",
		openai.WithBaseURL(server.URL+"/v1"),
		openai.WithAPIKey("local-test"),
		openai.WithOpenAIOptions(openaiopt.WithMaxRetries(0)),
	)
	opts := []llmagent.Option{
		llmagent.WithModel(llm),
		llmagent.WithTools([]tool.Tool{fileWrite}),
		llmagent.WithGenerationConfig(model.GenerationConfig{Stream: stream}),
		llmagent.WithEnablePostToolPrompt(false),
		llmagent.WithMaxLLMCalls(3),
	}
	if policy != "" {
		opts = append(opts, llmagent.WithTruncatedToolCallPolicy(policy))
	}
	opts = append(opts, extraOptions...)
	a := llmagent.New("main", opts...)

	svc := inmemory.NewSessionService()
	defer svc.Close()
	r := runner.NewRunner("truncated-policy", a, runner.WithSessionService(svc))
	defer r.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runOpts := []agent.RunOption{
		agent.WithToolCallArgumentsJSONRepairEnabled(true),
		agent.WithDisableTracing(true),
	}
	if stub.textToolCalls.Load() {
		runOpts = append(runOpts, agent.WithToolCallTextRepairEnabled(true))
	}
	events, err := r.Run(ctx, "user", "session", model.NewUserMessage("Write a file."), runOpts...)
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	result := truncatedRun{}
	for e := range events {
		if e.Response == nil {
			continue
		}
		if e.Error != nil {
			result.errors = append(result.errors, e.Error.Message)
			result.errorTypes = append(result.errorTypes, e.Error.Type)
		}
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("run did not finish within safety timeout: %v", err)
	}
	result.requests = stub.count()
	result.toolCalls = toolCalls.Load()

	sess, err := svc.GetSession(ctx, session.Key{
		AppName:   "truncated-policy",
		UserID:    "user",
		SessionID: "session",
	})
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	for _, e := range sess.Events {
		if e.Response != nil && !e.IsPartial {
			result.historyToolCallIDs = append(result.historyToolCallIDs, e.Response.GetToolCallIDs()...)
			if checkRejected && e.IsToolCallResponse() {
				t.Fatalf("rejected tool call was persisted as completed history (tool executions: %d)", result.toolCalls)
			}
		}
	}

	if checkRejected {
		// A resumed run must start a fresh model call instead of replaying the
		// rejected truncated call.
		stub.resuming.Store(true)
		resumed, err := r.Run(ctx, "user", "session", model.NewUserMessage("Continue."),
			agent.WithResume(true),
			agent.WithToolCallArgumentsJSONRepairEnabled(true),
			agent.WithDisableTracing(true),
		)
		if err != nil {
			t.Fatalf("resume failed: %v", err)
		}
		for e := range resumed {
			if e.Response != nil && e.Error != nil {
				result.resumeErrors = append(result.resumeErrors, e.Error.Message)
			}
		}
		result.resumeRequests = stub.count() - result.requests
		result.resumeToolCalls = toolCalls.Load()
	}
	return result
}

func TestTruncatedToolCallRejectIsDefault(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%v", stream), func(t *testing.T) {
			// No policy option: rejection is the default behavior.
			result := runTruncatedScenario(t, stream, "", false)
			if result.requests != 1 {
				t.Errorf("model calls = %d; want 1", result.requests)
			}
			if result.toolCalls != 0 {
				t.Errorf("tool executions = %d; want 0", result.toolCalls)
			}
			if !strings.Contains(strings.Join(result.errors, " | "), truncatedToolCallMessage) {
				t.Errorf("terminal errors = %q; want truncation error", result.errors)
			}
			if !strings.Contains(strings.Join(result.errorTypes, ","), model.ErrorTypeFlowError) {
				t.Errorf("terminal error types = %q; want %q", result.errorTypes, model.ErrorTypeFlowError)
			}
			if result.resumeRequests != 1 {
				t.Errorf("resume model calls = %d; want 1", result.resumeRequests)
			}
			if result.resumeToolCalls != 0 {
				t.Errorf("tool executions after resume = %d; want 0", result.resumeToolCalls)
			}
			if len(result.resumeErrors) != 0 {
				t.Errorf("resume errors = %q; want none", result.resumeErrors)
			}
		})
	}
}

// TestTruncatedToolCallTextRepairStillRejected covers tool calls that only
// exist as visible text: text repair turns them into a structured call and
// rewrites the finish reason, but the original output-limit signal still
// rejects them.
func TestTruncatedToolCallTextRepairStillRejected(t *testing.T) {
	result := runTruncatedScenario(t, false, "", true)
	if result.requests != 1 {
		t.Errorf("model calls = %d; want 1", result.requests)
	}
	if result.toolCalls != 0 {
		t.Errorf("tool executions = %d; want 0", result.toolCalls)
	}
	if !strings.Contains(strings.Join(result.errors, " | "), truncatedToolCallMessage) {
		t.Errorf("terminal errors = %q; want truncation error", result.errors)
	}
}

// TestTruncatedToolCallIgnorePolicy covers the explicit opt-out: with
// TruncatedToolCallPolicyIgnore, truncated tool calls are still repaired and
// executed exactly as before.
func TestTruncatedToolCallIgnorePolicy(t *testing.T) {
	result := runTruncatedScenario(t, false, agent.TruncatedToolCallPolicyIgnore, false)
	if result.requests != 3 {
		t.Errorf("model calls = %d; want 3", result.requests)
	}
	if result.toolCalls != 3 {
		t.Errorf("tool executions = %d; want 3", result.toolCalls)
	}
	if !strings.Contains(strings.Join(result.errors, " | "), maxLLMExceededMessage) {
		t.Errorf("terminal errors = %q; want %q", result.errors, maxLLMExceededMessage)
	}
}

func TestTruncatedToolCallStillRejectedAfterCallback(t *testing.T) {
	for _, tc := range []struct {
		name                string
		stream, clone, text bool
	}{
		{name: "in-place", stream: true},
		{name: "copied response", clone: true},
		{name: "retained text", clone: true, text: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			callbacks := model.NewCallbacks().RegisterAfterModel(
				func(_ context.Context, _ *model.Request, rsp *model.Response, _ error) (*model.Response, error) {
					if rsp == nil || rsp.IsPartial || len(rsp.Choices) == 0 || !rsp.Choices[0].HasOutputLimitFinishReason() {
						return nil, nil
					}
					if tc.clone {
						rsp = rsp.Clone()
					}
					rsp.Choices[0].FinishReason = nil
					if tc.clone {
						return rsp, nil
					}
					return nil, nil
				},
			)
			result := runTruncatedScenario(t, tc.stream, "", tc.text, llmagent.WithModelCallbacks(callbacks))
			if result.requests != 1 || result.toolCalls != 0 || !strings.Contains(strings.Join(result.errors, " | "), truncatedToolCallMessage) {
				t.Fatalf("requests=%d tool calls=%d errors=%v; want one request and a truncation error", result.requests, result.toolCalls, result.errors)
			}
			if result.resumeRequests != 1 || result.resumeToolCalls != 0 || len(result.resumeErrors) != 0 {
				t.Fatalf("unexpected resume after rejected callback response: %+v", result)
			}
		})
	}
}

func TestTruncatedToolCallCallbackCanReplaceWithNewCall(t *testing.T) {
	callbacks := model.NewCallbacks().RegisterAfterModel(
		func(_ context.Context, _ *model.Request, rsp *model.Response, _ error) (*model.Response, error) {
			if rsp == nil || !rsp.IsTruncatedToolCallResponse() {
				return nil, nil
			}
			replacement := rsp.Clone()
			reason := "tool_calls"
			replacement.Choices[0].FinishReason = &reason
			call := &replacement.Choices[0].Message.ToolCalls[0]
			call.ID = "replacement-" + call.ID
			call.Function.Arguments = []byte(`{"file_path":"/mock/replacement.txt","content":"complete"}`)
			return replacement, nil
		},
	)
	result := runTruncatedServer(t, false, "", &truncatedToolCallServer{}, false, llmagent.WithModelCallbacks(callbacks))
	if result.requests != 3 || result.toolCalls != 3 || !strings.Contains(strings.Join(result.errors, " | "), maxLLMExceededMessage) {
		t.Fatalf("replacement calls did not execute: %+v", result)
	}
}

func TestUnselectedTruncatedToolCallStaysOutOfHistory(t *testing.T) {
	stub := &truncatedToolCallServer{laterTruncatedChoice: true}
	result := runTruncatedServer(t, false, "", stub, false)
	if result.requests != 2 || result.toolCalls != 1 || len(result.errors) != 0 {
		t.Fatalf("first choice did not execute normally: %+v", result)
	}
	for _, id := range result.historyToolCallIDs {
		if id == "call-unselected" {
			t.Errorf("unselected truncated call persisted: %v", result.historyToolCallIDs)
		}
	}
	for _, message := range stub.requests[1].Messages {
		if strings.Contains(string(message.ToolCalls), "call-unselected") {
			t.Error("next model request contained the unselected truncated call")
		}
		if strings.Contains(message.Content, "unselected-probe") {
			t.Error("next model request contained the unselected truncated arguments as text")
		}
	}
}
