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
	"strings"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/internal/coderuntime/localpython"
)

//go:embed guest.py
var guestPython string

var completedGuestWaitTimeout = 2 * time.Second
var completedGuestKillWaitTimeout = 100 * time.Millisecond

// stdioRunner starts a guest process that speaks the local stdio protocol.
// It is an implementation detail of LocalRunner; non-stdio backends implement
// Runtime directly.
type stdioRunner interface {
	start(context.Context, Request, string) (stdioProcess, error)
}

type stdioProcess interface {
	Stdin() io.WriteCloser
	Stdout() io.ReadCloser
	Wait() error
	Kill() error
}

// LocalRunner runs the guest with a caller-supplied Python executable. Use it
// only in an already isolated environment or for development/tests.
type LocalRunner struct {
	// Python selects the Python interpreter. The default is python3.
	Python string
	// Timeout optionally bounds the local guest process lifetime. The zero
	// value preserves existing behavior and relies on the caller's context.
	Timeout time.Duration
	// MaxCodeBytes bounds the generated code size before launching Python.
	// The default is 64 KiB. Use a negative value to disable this limit.
	MaxCodeBytes int
	// WorkDir sets the guest process working directory. When empty, LocalRunner
	// creates an empty temporary directory and removes it after the guest exits.
	// WorkDir is not automatically added to Python's module search path.
	WorkDir string
}

func (r LocalRunner) start(ctx context.Context, req Request, script string) (stdioProcess, error) {
	return localpython.StartScript(
		ctx,
		localpython.Config{
			Python:       r.Python,
			Timeout:      r.Timeout,
			MaxCodeBytes: r.MaxCodeBytes,
			WorkDir:      r.WorkDir,
		},
		req.Code,
		"guest.py",
		[]byte(script),
		[]string{"-u"},
		nil,
		nil,
	)
}

// ExecuteCodeAct implements Runtime using a fresh local Python stdio guest.
func (r LocalRunner) ExecuteCodeAct(ctx context.Context, req Request, handler ToolCallHandler) (Result, error) {
	return executeStdio(ctx, r, req, handler)
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
	p, err := runner.start(ctx, req, guestPython)
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
		killErr := p.Kill()
		select {
		case err := <-waitCh:
			return err
		case <-time.After(completedGuestKillWaitTimeout):
			if killErr != nil {
				return fmt.Errorf("kill timed-out guest: %w", killErr)
			}
			return errors.New("timed-out guest did not exit after kill")
		}
	}
}

var _ Runtime = LocalRunner{}
