//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-openapi/testify/v2/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/plugin/guardrail/approval/review"
)

// scriptedModel replaces only inference; the application still uses the real
// LLMAgent, runner, session service, approval plugin, reviewer, and host tools.
type scriptedModel struct {
	mu       sync.Mutex
	commands []string
	workdir  string
	requests [][]model.Message
}

func (m *scriptedModel) Info() model.Info { return model.Info{Name: "scripted-main-model"} }

func (m *scriptedModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	call := len(m.requests)
	m.requests = append(m.requests, append([]model.Message(nil), req.Messages...))
	if call/2 >= len(m.commands) {
		return nil, fmt.Errorf("unexpected model call %d", call)
	}
	msg := model.Message{Role: model.RoleAssistant}
	if call%2 == 0 {
		if _, ok := req.Tools["hostexec_exec_command"]; !ok {
			return nil, fmt.Errorf("hostexec tool is not registered")
		}
		args, err := json.Marshal(map[string]any{
			"command": m.commands[call/2], "workdir": m.workdir, "yield_time_ms": 0, "timeout_sec": 10,
		})
		if err != nil {
			return nil, err
		}
		msg.ToolCalls = []model.ToolCall{{ID: fmt.Sprintf("call-%d", call), Type: "function",
			Function: model.FunctionDefinitionParam{Name: "hostexec_exec_command", Arguments: args}}}
	} else {
		for i := len(req.Messages) - 1; i >= 0; i-- {
			if req.Messages[i].Role == model.RoleTool {
				msg.Content = "Observed tool result: " + req.Messages[i].Content
				break
			}
		}
		if msg.Content == "" {
			return nil, fmt.Errorf("tool result did not reach the main model")
		}
	}
	responses := make(chan *model.Response, 1)
	responses <- &model.Response{Object: model.ObjectTypeChatCompletion,
		Choices: []model.Choice{{Message: msg}}, Done: true}
	close(responses)
	return responses, nil
}

type recordedReview struct {
	Model string `json:"model"`
	State struct {
		Context review.Context `json:"context"`
		Action  struct {
			ToolName  string          `json:"tool_name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"action"`
		Transcript []struct {
			Role    model.Role `json:"role"`
			Content string     `json:"content"`
		} `json:"transcript"`
	} `json:"state"`
}

type reviewServer struct {
	mu         sync.Mutex
	requests   []recordedReview
	status     int
	authorized bool
}

func (s *reviewServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.URL.Path != "/v1/systemone" {
		http.Error(w, "unexpected route", http.StatusNotFound)
		return
	}
	var req recordedReview
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
	if s.status != http.StatusOK {
		http.Error(w, "review service unavailable", s.status)
		return
	}
	choice, supported, insufficient := "supported", 1, 0
	if !s.authorized {
		choice, supported, insufficient = "insufficient", 0, 1
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"model":"test-review-model","answers":{
"authorization":{"type":"choice","choice":%q,"confidence":1,
"probabilities":{"supported":%d,"contradicted":0,"insufficient":%d}},
"risk":{"type":"score","score":0,"confidence":1,
"probabilities":{"0":1,"1":0,"2":0,"3":0,"4":0},
"legend":{"0":"negligible","1":"low","2":"moderate","3":"high","4":"critical"}}}}`,
		choice, supported, insufficient)
}

func (s *reviewServer) snapshot() []recordedReview {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedReview(nil), s.requests...)
}

func testApp(t *testing.T, srv *reviewServer, commands ...string) (*application, *scriptedModel) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("test commands require a POSIX shell")
	}
	server := httptest.NewServer(srv)
	t.Cleanup(server.Close)
	cfg := defaultConfig()
	cfg.baseDir = t.TempDir()
	cfg.reviewBaseURL = server.URL
	cfg.reviewModel = "test-review-model"
	m := &scriptedModel{commands: commands} // Callback must resolve the omitted workdir.
	a, err := newApp(cfg, m)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, a.close()) })
	return a, m
}

func TestToolExecutionRequiresApproval(t *testing.T) {
	for _, tt := range []struct {
		name       string
		status     int
		authorized bool
		wantResult string
	}{
		{"approved", http.StatusOK, true, `"exit_code":0`},
		{"denied", http.StatusOK, false, "Automatic approval review denied"},
		{"review service error", http.StatusServiceUnavailable, false, "approval review failed"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := &reviewServer{status: tt.status, authorized: tt.authorized}
			a, m := testApp(t, srv, "printf reviewed > review-marker.txt")
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var output bytes.Buffer
			prompt := "Write reviewed to review-marker.txt in the current directory."
			require.NoError(t, a.runTurn(ctx, prompt, &output))
			require.Contains(t, output.String(), "Tool call: hostexec_exec_command")
			require.Contains(t, output.String(), "Tool result (hostexec_exec_command)")
			require.Contains(t, output.String(), tt.wantResult)
			require.Contains(t, output.String(), "Assistant: Observed tool result:")
			data, err := os.ReadFile(filepath.Join(a.config.baseDir, "review-marker.txt"))
			if tt.authorized {
				require.NoError(t, err)
				require.Equal(t, "reviewed", string(data))
			} else {
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			requests := srv.snapshot()
			require.Len(t, requests, 1)
			require.Equal(t, "test-review-model", requests[0].Model)
			require.Equal(t, "hostexec_exec_command", requests[0].State.Action.ToolName)
			var arguments map[string]any
			require.NoError(t, json.Unmarshal(requests[0].State.Action.Arguments, &arguments))
			require.Equal(t, "printf reviewed > review-marker.txt", arguments["command"])
			require.Equal(t, a.config.baseDir, arguments["workdir"])
			require.NotEmpty(t, requests[0].State.Context.ToolCallID)
			require.NotNil(t, requests[0].State.Context.InputSchema)
			require.NotEmpty(t, requests[0].State.Context.Supplement.Facts)
			require.Equal(t, len(requests[0].State.Transcript), len(requests[0].State.Context.TranscriptInfo))
			require.Equal(t, review.OriginUserInput, requests[0].State.Context.TranscriptInfo[0].Origin)
			require.Contains(t, transcriptText(requests[0], model.RoleUser), prompt)
			m.mu.Lock()
			defer m.mu.Unlock()
			require.Len(t, m.requests, 2)
		})
	}
}

func TestChatPreservesReviewHistory(t *testing.T) {
	srv := &reviewServer{status: http.StatusOK, authorized: true}
	a, _ := testApp(t, srv, "printf reviewed > review-marker.txt", "cat review-marker.txt")
	first := "Write reviewed to review-marker.txt."
	second := "Now read the file from the previous turn."
	input := strings.NewReader("/help\n\n" + first + "\n" + second + "\n/exit\n")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var output bytes.Buffer
	require.NoError(t, a.chat(ctx, input, &output))
	require.Contains(t, output.String(), "All hostexec tools require review")
	requests := srv.snapshot()
	require.Len(t, requests, 2)
	require.Contains(t, transcriptText(requests[1], model.RoleUser), first)
	require.Contains(t, transcriptText(requests[1], model.RoleUser), second)
	require.Contains(t, transcriptText(requests[1], model.RoleTool), "exit_code")
	require.Contains(t, transcriptText(requests[1], model.RoleAssistant), "Observed tool result")
}

func TestRunTurnPropagatesModelError(t *testing.T) {
	srv := &reviewServer{status: http.StatusOK, authorized: true}
	// An empty script rejects the first model call, exercising stream-level
	// failure propagation rather than only setup errors from Runner.Run.
	a, _ := testApp(t, srv)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var output bytes.Buffer
	err := a.runTurn(ctx, "Run pwd.", &output)
	require.ErrorContains(t, err, "unexpected model call 0")
	require.Empty(t, srv.snapshot())
}

func transcriptText(req recordedReview, role model.Role) string {
	var text strings.Builder
	for _, entry := range req.State.Transcript {
		if entry.Role == role {
			text.WriteString(entry.Content)
			text.WriteByte('\n')
		}
	}
	return text.String()
}
