//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package codeact

import (
	"bufio"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

//go:embed guest.py
var guestPython string

var completedGuestWaitTimeout = 2 * time.Second

// stdioRunner starts a guest process that speaks the local stdio protocol.
// It is an implementation detail of LocalRunner; non-stdio backends implement
// Runtime directly.
type stdioRunner interface {
	start(context.Context, string) (stdioProcess, error)
}

type stdioProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Wait() error
	Kill() error
}

// LocalRunner runs the guest with a caller-supplied Python executable. Use it
// only in an already isolated environment or for development/tests.
type LocalRunner struct{ Python string }

func (r LocalRunner) start(ctx context.Context, script string) (stdioProcess, error) {
	python := r.Python
	if python == "" {
		python = "python3"
	}
	dir, err := os.MkdirTemp("", "trpc-codeact-")
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "guest.py")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	cmd := exec.CommandContext(ctx, python, "-u", path)
	in, err := cmd.StdinPipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	return &localProcess{
		cmd:     cmd,
		in:      in,
		out:     out,
		cleanup: func() { _ = os.RemoveAll(dir) },
	}, nil
}

// ExecuteCodeAct implements Runtime using a fresh local Python stdio guest.
func (r LocalRunner) ExecuteCodeAct(ctx context.Context, req Request, handler ToolCallHandler) (Result, error) {
	return executeStdio(ctx, r, req, handler)
}

type localProcess struct {
	cmd     *exec.Cmd
	in      io.WriteCloser
	out     io.ReadCloser
	cleanup func()
}

func (p *localProcess) Stdin() io.WriteCloser { return p.in }
func (p *localProcess) Stdout() io.ReadCloser { return p.out }
func (p *localProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
func (p *localProcess) Wait() error {
	err := p.cmd.Wait()
	p.cleanup()
	return err
}

type protocolMessage struct {
	Type string          `json:"type"`
	ID   string          `json:"id,omitempty"`
	Name string          `json:"name,omitempty"`
	Args json.RawMessage `json:"args,omitempty"`
	Code string          `json:"code,omitempty"`
}

type protocolResponse struct {
	Type   string          `json:"type"`
	ID     string          `json:"id,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
}

func executeStdio(ctx context.Context, runner stdioRunner, req Request, handler ToolCallHandler) (Result, error) {
	if runner == nil {
		return Result{}, errRequired("runner")
	}
	if handler == nil {
		return Result{}, errRequired("tool call handler")
	}
	if req.Code == "" {
		return Result{}, errRequired("code")
	}
	if err := validateLocalLanguage(req.Language); err != nil {
		return Result{}, err
	}
	p, err := runner.start(ctx, guestPython)
	if err != nil {
		return Result{}, fmt.Errorf("codeact: start guest: %w", err)
	}
	waited := false
	stdinClosed := false
	closeStdin := func() {
		if !stdinClosed {
			_ = p.Stdin().Close()
			stdinClosed = true
		}
	}
	defer func() {
		closeStdin()
		if !waited {
			_ = p.Kill()
			_ = p.Wait()
		}
	}()

	enc := json.NewEncoder(p.Stdin())
	dec := bufio.NewScanner(p.Stdout())
	dec.Buffer(make([]byte, 1024), 4<<20)
	if err := enc.Encode(protocolMessage{Type: "run", Code: req.Code}); err != nil {
		return Result{}, err
	}
	for dec.Scan() {
		var msg protocolMessage
		if err := json.Unmarshal(dec.Bytes(), &msg); err != nil {
			return Result{}, fmt.Errorf("codeact: malformed guest message: %w", err)
		}
		switch msg.Type {
		case "tool_call":
			value, callErr := handler.HandleToolCall(ctx, ToolCall{
				ID:   msg.ID,
				Name: msg.Name,
				Args: msg.Args,
			})
			out := protocolResponse{Type: "tool_result", ID: msg.ID, Result: value}
			if callErr != nil {
				out.Error = callErr.Error()
			}
			if err := enc.Encode(out); err != nil {
				return Result{}, err
			}
		case "complete":
			closeStdin()
			waited = true
			waitErr := waitForCompletedGuest(ctx, p, completedGuestWaitTimeout)
			if waitErr != nil {
				return Result{}, fmt.Errorf("codeact: wait for guest: %w", waitErr)
			}
			if msg.Name != "" {
				return Result{}, errors.New(msg.Name)
			}
			return Result{Value: msg.Args, Stdout: msg.Code}, nil
		default:
			return Result{}, fmt.Errorf("codeact: unknown guest message %q", msg.Type)
		}
	}
	if err := dec.Err(); err != nil {
		return Result{}, err
	}
	select {
	case <-ctx.Done():
		return Result{}, ctx.Err()
	case <-time.After(10 * time.Millisecond):
	}
	return Result{}, errors.New("codeact: guest exited without a completion message")
}

func validateLocalLanguage(language string) error {
	normalized := strings.TrimSpace(language)
	if normalized == "" || strings.EqualFold(normalized, "python") {
		return nil
	}
	return fmt.Errorf("codeact: unsupported language %q", language)
}

func waitForCompletedGuest(ctx context.Context, p stdioProcess, timeout time.Duration) error {
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- p.Wait()
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case err := <-waitCh:
		return err
	case <-ctx.Done():
		_ = p.Kill()
		select {
		case <-waitCh:
		case <-time.After(100 * time.Millisecond):
		}
		return ctx.Err()
	case <-timer.C:
		_ = p.Kill()
		select {
		case <-waitCh:
		case <-time.After(100 * time.Millisecond):
		}
		return nil
	}
}

var _ Runtime = LocalRunner{}
