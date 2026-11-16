//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

// Package codeinterpreter provides a CodeInterpreter implementation for E2B.
package codeinterpreter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// RunCodeOpts holds options for Sandbox.RunCode.
//
// Either Language or Context may be supplied — not both. When both are empty
// the default Python context is used.
type RunCodeOpts struct {
	// Language to use. Must not be combined with Context.
	Language RunCodeLanguage
	// Context (pre-created kernel) to run the code in.
	Context *Context
	// OnStdout is called for every stdout chunk.
	OnStdout OnStdoutFunc
	// OnStderr is called for every stderr chunk.
	OnStderr OnStderrFunc
	// OnResult is called for every Result (display call or final result).
	OnResult OnResultFunc
	// OnError is called when the kernel reports an error for this cell.
	OnError OnErrorFunc
	// Envs are extra environment variables exposed to the running code.
	Envs map[string]string
	// Timeout bounds how long the client reads the response body after headers
	// arrive. That covers the execution stream and an HTTP error body. Zero
	// uses DefaultTimeout seconds. A negative value disables this deadline
	// and does not disable RequestTimeout. Stopping the read does not kill
	// the remote code or the sandbox. The execute request body does not carry
	// this value.
	Timeout time.Duration
	// RequestTimeout bounds only the wait for HTTP response headers.
	// Zero inherits the sandbox connection's request timeout. A negative
	// value disables the header deadline for this call. Once headers arrive
	// the deadline is cleared, so it does not limit the execution stream or
	// an HTTP error body.
	RequestTimeout time.Duration
}

// CreateCodeContextOpts holds options for Sandbox.CreateCodeContext.
type CreateCodeContextOpts struct {
	// Cwd is the working directory for the context (default /home/user).
	Cwd string
	// Language of the new context (default python).
	Language RunCodeLanguage
	// RequestTimeout overrides the default HTTP request timeout.
	RequestTimeout time.Duration
}

// RunCode executes the supplied code in the sandbox and returns the full
// Execution result.
//
// Streaming output is forwarded to the callbacks on opts as each chunk arrives.
// Chunks received before a failure remain available through those callbacks.
// A timeout or cancellation returns a nil Execution.
//
// RequestTimeout and Timeout are separate client deadlines. RequestTimeout
// ends the wait for response headers and is cleared when they arrive. Timeout
// then bounds reading the body, including an HTTP error body. Disabling one
// deadline leaves the other in place. An earlier caller cancellation or caller
// deadline is returned as that context error and is not reported as TimeoutError.
// Ending the client wait does not kill the remote execution or the sandbox.
// An http.Client.Timeout configured on the sandbox client still covers the
// whole exchange, including the body, and should be left unset or longer than
// a stream that must stay open. When that deadline or another transport
// deadline fires, the error names the source and whether the client was
// waiting for headers or reading the body. It is not a TimeoutError, and
// increasing RequestTimeout or Timeout does not extend it. The transport
// error remains in the unwrap chain.
func (s *Sandbox) RunCode(ctx context.Context, code string, opts *RunCodeOpts) (*Execution, error) {
	if opts == nil {
		opts = &RunCodeOpts{}
	}

	if opts.Language != "" && opts.Context != nil {
		return nil, &InvalidArgumentError{
			Message: "You can provide context or language, but not both at the same time.",
		}
	}

	// Build request body
	payload := map[string]any{
		"code": code,
	}
	if opts.Context != nil {
		payload["context_id"] = opts.Context.ID
	}
	if opts.Language != "" {
		payload["language"] = string(opts.Language)
	}
	if len(opts.Envs) > 0 {
		payload["env_vars"] = opts.Envs
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal execute payload: %w", err)
	}

	requestTimeout, requestTimed := resolveRequestTimeout(opts.RequestTimeout, s.connection.RequestTimeout)
	executionTimeout, executionTimed := resolveExecutionTimeout(opts.Timeout)
	// streamCtx lives for both the header wait and the body read. Deadlines are
	// attached with codeExecTimer so the header budget is not added to the
	// execution budget and is not inherited by the stream.
	streamCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timers := newCodeExecTimer(cancel)
	defer timers.stop()
	// The timer starts idle, so this first arm cannot lose a race.
	timers.arm(timeoutPhaseRequest, requestTimeout, requestTimed, errRequestTimeout)

	u := s.jupyterURL() + "/execute"
	req, err := http.NewRequestWithContext(streamCtx, "POST", u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	s.addAuthHeaders(req.Header)

	resp, err := s.connection.HTTPClient.Do(req)
	if err != nil {
		return nil, classifyRunCodeError(ctx, streamCtx, timers, err)
	}
	defer resp.Body.Close()

	// Headers have arrived, so the header deadline must not keep running while
	// the body is read. An error body uses the same post-header budget as the
	// execution stream. A disabled execution deadline leaves this read to the
	// caller context.
	if !timers.arm(timeoutPhaseExecution, executionTimeout, executionTimed, errExecutionTimeout) {
		return nil, classifyRunCodeError(ctx, streamCtx, timers, context.Cause(streamCtx))
	}

	if resp.StatusCode >= 400 {
		b, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return nil, classifyRunCodeError(ctx, streamCtx, timers, readErr)
		}
		return nil, mapHTTPError(resp.StatusCode, string(b))
	}

	execution := NewExecution()

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		if err := parseOutputLine(execution, line, opts); err != nil {
			return nil, err
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, classifyRunCodeError(ctx, streamCtx, timers, err)
	}

	return execution, nil
}

// parseOutputLine decodes a single NDJSON line emitted by /execute and
// dispatches it to the correct handler.
func parseOutputLine(execution *Execution, line string, opts *RunCodeOpts) error {
	var msg map[string]any
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		// Ignore un-parseable lines — they shouldn't happen but we don't
		// want to blow up a long running execution over them.
		return nil
	}

	t, _ := msg["type"].(string)
	switch t {
	case "result":
		// The "type" / "is_main_result" fields are not relevant as keys on the
		// resulting Result object; newResultFromRaw handles the split.
		result := newResultFromRaw(msg)
		execution.Results = append(execution.Results, result)
		if opts.OnResult != nil {
			opts.OnResult(result)
		}
	case "stdout":
		text := getString(msg, "text")
		execution.Logs.Stdout = append(execution.Logs.Stdout, text)
		if opts.OnStdout != nil {
			opts.OnStdout(OutputMessage{
				Line:      text,
				Timestamp: getInt64(msg, "timestamp"),
				Error:     false,
			})
		}
	case "stderr":
		text := getString(msg, "text")
		execution.Logs.Stderr = append(execution.Logs.Stderr, text)
		if opts.OnStderr != nil {
			opts.OnStderr(OutputMessage{
				Line:      text,
				Timestamp: getInt64(msg, "timestamp"),
				Error:     true,
			})
		}
	case "error":
		execution.Error = &ExecutionError{
			Name:      getString(msg, "name"),
			Value:     getString(msg, "value"),
			Traceback: getString(msg, "traceback"),
		}
		if opts.OnError != nil {
			opts.OnError(execution.Error)
		}
	case "number_of_executions":
		if v, ok := msg["execution_count"]; ok {
			switch n := v.(type) {
			case float64:
				execution.ExecutionCount = int(n)
			case int:
				execution.ExecutionCount = n
			case json.Number:
				i, _ := n.Int64()
				execution.ExecutionCount = int(i)
			}
		}
	}
	return nil
}

func getInt64(m map[string]any, key string) int64 {
	if v, ok := m[key]; ok {
		switch n := v.(type) {
		case float64:
			return int64(n)
		case int:
			return int64(n)
		case int64:
			return n
		case json.Number:
			i, _ := n.Int64()
			return i
		}
	}
	return 0
}

// CreateCodeContext creates a fresh kernel in which subsequent code can be run.
func (s *Sandbox) CreateCodeContext(ctx context.Context, opts *CreateCodeContextOpts) (*Context, error) {
	if opts == nil {
		opts = &CreateCodeContextOpts{}
	}

	body := map[string]any{}
	if opts.Language != "" {
		body["language"] = string(opts.Language)
	}
	if opts.Cwd != "" {
		body["cwd"] = opts.Cwd
	}

	return s.doContextRequest(ctx, "POST", "/contexts", body, opts.RequestTimeout)
}

// ListCodeContexts lists the contexts currently available in the sandbox.
func (s *Sandbox) ListCodeContexts(ctx context.Context) ([]*Context, error) {
	b, status, err := s.jupyterRequest(ctx, "GET", "/contexts", nil, 0)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, mapHTTPError(status, string(b))
	}

	var raw []map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("decode contexts: %w", err)
	}

	contexts := make([]*Context, 0, len(raw))
	for _, data := range raw {
		contexts = append(contexts, contextFromJSON(data))
	}
	return contexts, nil
}

// RestartCodeContext restarts the given context. The parameter can either be a
// *Context or a context-id string.
func (s *Sandbox) RestartCodeContext(ctx context.Context, c any) error {
	id, err := contextID(c)
	if err != nil {
		return err
	}
	_, status, err := s.jupyterRequest(ctx, "POST", "/contexts/"+url.PathEscape(id)+"/restart", nil, 0)
	if err != nil {
		return err
	}
	if status >= 400 {
		return mapHTTPError(status, "")
	}
	return nil
}

// RemoveCodeContext removes the context. The parameter can either be a
// *Context or a context-id string.
func (s *Sandbox) RemoveCodeContext(ctx context.Context, c any) error {
	id, err := contextID(c)
	if err != nil {
		return err
	}
	_, status, err := s.jupyterRequest(ctx, "DELETE", "/contexts/"+url.PathEscape(id), nil, 0)
	if err != nil {
		return err
	}
	if status >= 400 {
		return mapHTTPError(status, "")
	}
	return nil
}

// doContextRequest is a helper for creating a Context via POST /contexts.
func (s *Sandbox) doContextRequest(ctx context.Context, method, path string, body map[string]any, reqTimeout time.Duration) (*Context, error) {
	b, status, err := s.jupyterRequest(ctx, method, path, body, reqTimeout)
	if err != nil {
		return nil, err
	}
	if status >= 400 {
		return nil, mapHTTPError(status, string(b))
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, fmt.Errorf("decode context response: %w", err)
	}
	return contextFromJSON(raw), nil
}

// jupyterRequest performs an HTTP request against the sandbox jupyter server
// and returns the raw body, status code and any transport-level error.
func (s *Sandbox) jupyterRequest(ctx context.Context, method, path string, body any, reqTimeout time.Duration) ([]byte, int, error) {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
		reader = bytes.NewReader(b)
	}

	if reqTimeout == 0 {
		reqTimeout = s.connection.RequestTimeout
	}

	reqCtx := ctx
	var cancel context.CancelFunc
	if reqTimeout > 0 {
		reqCtx, cancel = context.WithTimeout(ctx, reqTimeout)
		defer cancel()
	}

	u := s.jupyterURL() + path
	req, err := http.NewRequestWithContext(reqCtx, method, u, reader)
	if err != nil {
		return nil, 0, err
	}
	s.addAuthHeaders(req.Header)

	resp, err := s.connection.HTTPClient.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, 0, formatRequestTimeoutError()
		}
		return nil, 0, err
	}
	defer resp.Body.Close()

	out, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return out, resp.StatusCode, nil
}

// contextID returns the string ID from either a *Context or a plain string.
func contextID(c any) (string, error) {
	switch v := c.(type) {
	case string:
		return v, nil
	case *Context:
		if v == nil {
			return "", &InvalidArgumentError{Message: "context is nil"}
		}
		return v.ID, nil
	case Context:
		return v.ID, nil
	default:
		return "", &InvalidArgumentError{Message: fmt.Sprintf("unsupported context type %T", c)}
	}
}
