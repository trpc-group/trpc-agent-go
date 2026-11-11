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
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-openapi/testify/v2/require"
	"trpc.group/trpc-go/trpc-agent-go/model"
)

type scriptedChat struct {
	err      error
	mu       sync.Mutex
	reply    string
	requests [][]model.Message
}

func (m *scriptedChat) Info() model.Info { return model.Info{Name: "scripted-chat"} }
func (m *scriptedChat) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	if m.err != nil {
		return nil, m.err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.requests = append(m.requests, append([]model.Message(nil), req.Messages...))
	msg := model.NewAssistantMessage(m.reply)
	if m.reply == "" {
		if len(req.Messages) > 0 && req.Messages[len(req.Messages)-1].Role == model.RoleTool {
			msg = model.NewAssistantMessage("Observed: " + req.Messages[len(req.Messages)-1].Content)
		} else {
			msg = model.Message{Role: model.RoleAssistant, ToolCalls: []model.ToolCall{{ID: fmt.Sprintf("exec-%d", len(m.requests)), Type: "function", Function: model.FunctionDefinitionParam{Name: toolExecCommand, Arguments: []byte(`{"command":"printf reviewed > marker.txt","yield_time_ms":0}`)}}}}
		}
	}
	ch := make(chan *model.Response, 1)
	ch <- &model.Response{Object: model.ObjectTypeChatCompletion, Done: true, Choices: []model.Choice{{Message: msg}}}
	close(ch)
	return ch, nil
}

func TestLLMApprovalApplication(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("commands require a POSIX shell")
	}
	for _, tc := range []struct {
		name, reply string
		approved    bool
	}{
		{"approve", `{"risk_score":10,"risk_level":"low","reason":"scoped"}`, true},
		{"deny", `{"risk_score":95,"risk_level":"high","reason":"out of scope"}`, false},
		{"missing score", `{"reason":"incomplete"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mainModel := &scriptedChat{}
			reviewer := &scriptedChat{reply: tc.reply}
			app := &demoApp{baseDir: t.TempDir()}
			require.NoError(t, app.setupModels(mainModel, reviewer))
			defer app.close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			for i := 0; i < 2; i++ {
				require.NoError(t, app.runTurn(ctx, "Write reviewed to marker.txt in this directory."))
			}
			data, err := os.ReadFile(filepath.Join(app.baseDir, "marker.txt"))
			if tc.approved {
				require.NoError(t, err)
				require.Equal(t, "reviewed", string(data))
			} else {
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			reviewer.mu.Lock()
			defer reviewer.mu.Unlock()
			require.Len(t, reviewer.requests, 2)
			for _, messages := range reviewer.requests {
				users := 0
				for _, msg := range messages {
					if msg.Role == model.RoleUser {
						users++
						require.Contains(t, msg.Content, `"workdir": "`+app.baseDir+`"`)
						require.Contains(t, msg.Content, `"origin": "user_input"`)
						require.Contains(t, msg.Content, "hostexec configuration")
						require.Contains(t, msg.Content, ">>> REVIEW CONTEXT START")
					}
				}
				require.Equal(t, 1, users, "each review uses a fresh session with one self-contained evidence request")
			}
			mainModel.mu.Lock()
			defer mainModel.mu.Unlock()
			last := mainModel.requests[len(mainModel.requests)-1]
			found := false
			for _, msg := range last {
				if msg.Role == model.RoleTool {
					found = true
					if tc.name == "missing score" {
						require.True(t, strings.Contains(msg.Content, "missing risk score"))
					}
				}
			}
			require.True(t, found, "the main model must observe execution or failure")
		})
	}
}

func TestApplicationPropagatesModelFailure(t *testing.T) {
	app := &demoApp{baseDir: t.TempDir()}
	require.NoError(t, app.setupModels(&scriptedChat{err: errors.New("model unavailable")}, &scriptedChat{reply: `{"risk_score":10}`}))
	defer app.close()
	require.ErrorContains(t, app.runTurn(context.Background(), "Inspect the directory."), "model unavailable")
}
