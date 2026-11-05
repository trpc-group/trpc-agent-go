//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent. All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package demo

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/agent/llmagent"
	"trpc.group/trpc-go/trpc-agent-go/model"
	"trpc.group/trpc-go/trpc-agent-go/runner"
)

func TestModelSelectionAndValidation(t *testing.T) {
	t.Setenv("OPENAI_MODEL", "")
	t.Setenv("MODEL_NAME", "glm-test")
	c, err := parse(nil)
	if err != nil || c.model != "glm-test" {
		t.Fatalf("config: %+v %v", c, err)
	}
	t.Setenv("OPENAI_MODEL", "existing")
	c, err = parse(nil)
	if err != nil || c.model != "existing" {
		t.Fatalf("config: %+v %v", c, err)
	}
	c, err = parse([]string{"-model", "explicit"})
	if err != nil || c.model != "explicit" {
		t.Fatalf("config: %+v %v", c, err)
	}
	for _, args := range [][]string{{"-calls", "2"}, {"-timeout", "0s"}} {
		if _, err := parse(args); err == nil {
			t.Fatal("expected invalid configuration", args)
		}
	}
}

type chatModel struct {
	calls    atomic.Int32
	started  chan struct{}
	requests chan []model.Message
}

func (m *chatModel) Info() model.Info { return model.Info{Name: "chat-test"} }
func (m *chatModel) GenerateContent(ctx context.Context, req *model.Request) (<-chan *model.Response, error) {
	m.requests <- append([]model.Message(nil), req.Messages...)
	n := m.calls.Add(1)
	out := make(chan *model.Response, 1)
	go func() {
		defer close(out)
		if n == 1 {
			close(m.started)
			<-ctx.Done()
			return
		}
		out <- &model.Response{Done: true, Choices: []model.Choice{{Message: model.NewAssistantMessage("done")}}}
	}()
	return out, nil
}
func TestChatInputDuringGeneration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := &chatModel{started: make(chan struct{}), requests: make(chan []model.Message, 3)}
	r := runner.NewRunner("chat-test", llmagent.New("agent", llmagent.WithModel(m), llmagent.WithInterruptOnUserMessage(true)))
	defer r.Close()
	reader, writer := io.Pipe()
	written := make(chan error, 1)
	go func() {
		if _, err := io.WriteString(writer, "Q1\n"); err != nil {
			written <- err
			return
		}
		select {
		case <-m.started:
		case <-ctx.Done():
			written <- ctx.Err()
			return
		}
		_, err := io.WriteString(writer, "Q2\n")
		writer.Close()
		written <- err
	}()
	var output bytes.Buffer
	if err := chatLoop(ctx, r, reader, &output); err != nil {
		t.Fatal(err)
	}
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if m.calls.Load() != 2 {
		t.Fatalf("calls=%d", m.calls.Load())
	}
	<-m.requests
	second := <-m.requests
	found := false
	for _, msg := range second {
		if msg.Role == model.RoleUser && msg.Content == "Q2" {
			found = true
		}
	}
	if !found {
		t.Fatal("Q2 absent from replacement request")
	}
	if strings.Count(output.String(), "[run] started") != 1 || strings.Count(output.String(), "[run] completion") != 1 {
		t.Fatal(output.String())
	}
}
