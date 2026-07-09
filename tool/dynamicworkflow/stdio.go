//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package dynamicworkflow

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

var errWorkflowGuestExitTimeout = errors.New("dynamicworkflow: guest did not exit after completion")

const (
	workflowGuestExitGrace           = 250 * time.Millisecond
	workflowGuestCallbackDrainGrace  = time.Second
	workflowGuestCallbackConcurrency = 32
	workflowGuestProtocolLineLimit   = 4 << 20
	workflowGuestCapturedOutputLimit = 1 << 20
)

// LocalRunner executes workflow Python on the local host through a stdio
// callback protocol. It is intended only for development or an environment
// that the application has already isolated; it is not a security sandbox.
//
// Set Python to select a specific interpreter. The default is python3.
type LocalRunner struct {
	Python string
}

type protocolMessage struct {
	Type   string          `json:"type"`
	ID     string          `json:"id,omitempty"`
	Kind   CallKind        `json:"kind,omitempty"`
	Name   string          `json:"name,omitempty"`
	Args   json.RawMessage `json:"args,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Stdout string          `json:"stdout,omitempty"`
	Error  string          `json:"error,omitempty"`
}

type workflowGuestProcess struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.Reader
	stderr *limitedBuffer
}

type workflowGuestState struct {
	completed *Result
	guestErr  error
}

// ExecuteWorkflow implements Runtime with a fresh local Python guest.
func (r LocalRunner) ExecuteWorkflow(
	ctx context.Context,
	req Request,
	handler CallHandler,
) (Result, error) {
	if handler == nil {
		return Result{}, required("call handler")
	}
	guest, err := r.startWorkflowGuest(ctx, req.Code)
	if err != nil {
		return Result{}, err
	}
	return runWorkflowGuest(ctx, guest, handler)
}

func (r LocalRunner) startWorkflowGuest(
	ctx context.Context,
	code string,
) (*workflowGuestProcess, error) {
	python := strings.TrimSpace(r.Python)
	if python == "" {
		python = "python3"
	}
	cmd := exec.CommandContext(
		ctx,
		python,
		"-c",
		pythonGuest,
		base64.StdEncoding.EncodeToString([]byte(code)),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("dynamicworkflow: create guest stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("dynamicworkflow: create guest stdout: %w", err)
	}
	stderr := newLimitedBuffer(workflowGuestCapturedOutputLimit)
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("dynamicworkflow: start Python guest: %w", err)
	}
	return &workflowGuestProcess{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
	}, nil
}

func runWorkflowGuest(
	ctx context.Context,
	guest *workflowGuestProcess,
	handler CallHandler,
) (Result, error) {
	encoder := json.NewEncoder(guest.stdin)
	scanner := bufio.NewScanner(guest.stdout)
	scanner.Buffer(make([]byte, 64*1024), workflowGuestProtocolLineLimit)
	callbackCtx, cancelCallbacks := context.WithCancel(ctx)
	defer cancelCallbacks()
	callbackSlots := make(chan struct{}, workflowGuestCallbackConcurrency)
	responseMu := &sync.Mutex{}
	calls := &sync.WaitGroup{}
	writeErr := &workflowWriteError{}
	state := &workflowGuestState{}
	for scanner.Scan() {
		if stop := processWorkflowGuestMessage(
			callbackCtx,
			scanner.Bytes(),
			handler,
			encoder,
			responseMu,
			calls,
			writeErr,
			state,
			callbackSlots,
		); stop {
			break
		}
	}
	return finishWorkflowGuest(ctx, cancelCallbacks, guest, scanner, calls, writeErr, state)
}

type workflowWriteError struct {
	sync.Mutex
	err error
}

func processWorkflowGuestMessage(
	ctx context.Context,
	raw []byte,
	handler CallHandler,
	encoder *json.Encoder,
	responseMu *sync.Mutex,
	calls *sync.WaitGroup,
	writeErr *workflowWriteError,
	state *workflowGuestState,
	callbackSlots chan struct{},
) bool {
	var msg protocolMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		state.guestErr = fmt.Errorf("dynamicworkflow: malformed guest message: %w", err)
		return true
	}
	return handleWorkflowGuestMessage(ctx, msg, handler, encoder, responseMu, calls, writeErr, state, callbackSlots)
}

func handleWorkflowGuestMessage(
	ctx context.Context,
	msg protocolMessage,
	handler CallHandler,
	encoder *json.Encoder,
	responseMu *sync.Mutex,
	calls *sync.WaitGroup,
	writeErr *workflowWriteError,
	state *workflowGuestState,
	callbackSlots chan struct{},
) bool {
	switch msg.Type {
	case "call":
		if err := acquireWorkflowGuestCallbackSlot(ctx, callbackSlots); err != nil {
			state.guestErr = fmt.Errorf("dynamicworkflow: acquire callback slot: %w", err)
			return true
		}
		calls.Add(1)
		go func() {
			defer releaseWorkflowGuestCallbackSlot(callbackSlots)
			handleWorkflowGuestCall(ctx, msg, handler, encoder, responseMu, calls, writeErr)
		}()
	case "done":
		if !json.Valid(msg.Result) {
			state.guestErr = errors.New("dynamicworkflow: guest returned non-JSON result")
			return true
		}
		state.completed = &Result{Value: msg.Result, Stdout: msg.Stdout}
	case "error":
		state.guestErr = errors.New(msg.Error)
	}
	return state.guestErr != nil || state.completed != nil
}

func acquireWorkflowGuestCallbackSlot(ctx context.Context, slots chan struct{}) error {
	if slots == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case slots <- struct{}{}:
		return nil
	}
}

func releaseWorkflowGuestCallbackSlot(slots chan struct{}) {
	if slots == nil {
		return
	}
	<-slots
}

func handleWorkflowGuestCall(
	ctx context.Context,
	msg protocolMessage,
	handler CallHandler,
	encoder *json.Encoder,
	responseMu *sync.Mutex,
	calls *sync.WaitGroup,
	writeErr *workflowWriteError,
) {
	defer calls.Done()
	value, err := handler.HandleWorkflowCall(ctx, Call{
		ID:   msg.ID,
		Kind: msg.Kind,
		Name: msg.Name,
		Args: msg.Args,
	})
	response := protocolMessage{Type: "result", ID: msg.ID}
	if err != nil {
		response.Error = err.Error()
	} else {
		response.Result = value
	}
	writeWorkflowGuestResponse(encoder, responseMu, writeErr, response)
}

func writeWorkflowGuestResponse(
	encoder *json.Encoder,
	responseMu *sync.Mutex,
	writeErr *workflowWriteError,
	response protocolMessage,
) {
	responseMu.Lock()
	defer responseMu.Unlock()
	if err := encoder.Encode(response); err != nil {
		writeErr.Lock()
		if writeErr.err == nil {
			writeErr.err = err
		}
		writeErr.Unlock()
	}
}

func finishWorkflowGuest(
	ctx context.Context,
	cancelCallbacks context.CancelFunc,
	guest *workflowGuestProcess,
	scanner *bufio.Scanner,
	calls *sync.WaitGroup,
	writeErr *workflowWriteError,
	state *workflowGuestState,
) (Result, error) {
	if cancelCallbacks != nil {
		cancelCallbacks()
	}
	if err := waitWorkflowGuestCallbacks(ctx, calls); err != nil && state.guestErr == nil {
		state.guestErr = err
	}
	writeErr.Lock()
	if writeErr.err != nil && state.guestErr == nil && state.completed == nil {
		state.guestErr = fmt.Errorf("dynamicworkflow: write guest response: %w", writeErr.err)
	}
	writeErr.Unlock()
	if scanErr := workflowGuestScannerError(scanner.Err()); scanErr != nil && state.guestErr == nil {
		state.guestErr = scanErr
	}
	_ = guest.stdin.Close()
	waitErr := waitWorkflowGuest(ctx, guest)
	if guest.stderr.Exceeded() && state.guestErr == nil {
		state.guestErr = fmt.Errorf(
			"dynamicworkflow: guest stderr exceeds %d bytes",
			workflowGuestCapturedOutputLimit,
		)
	}
	if state.guestErr != nil {
		return Result{}, guestErrorWithStderr(state.guestErr, guest.stderr.String())
	}
	if waitErr != nil {
		if errors.Is(waitErr, errWorkflowGuestExitTimeout) && state.completed != nil {
			return *state.completed, nil
		}
		return Result{}, guestErrorWithStderr(
			fmt.Errorf("dynamicworkflow: wait for guest: %w", waitErr),
			guest.stderr.String(),
		)
	}
	if state.completed == nil {
		return Result{}, guestErrorWithStderr(
			errors.New("dynamicworkflow: guest exited without a completion message"),
			guest.stderr.String(),
		)
	}
	return *state.completed, nil
}

func waitWorkflowGuestCallbacks(ctx context.Context, calls *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		calls.Wait()
		close(done)
	}()
	timer := time.NewTimer(workflowGuestCallbackDrainGrace)
	defer timer.Stop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return fmt.Errorf(
			"dynamicworkflow: workflow callbacks did not finish within %s after guest completion",
			workflowGuestCallbackDrainGrace,
		)
	}
}

func workflowGuestScannerError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "token too long") {
		return fmt.Errorf(
			"dynamicworkflow: guest protocol message exceeds %d bytes",
			workflowGuestProtocolLineLimit,
		)
	}
	return fmt.Errorf("dynamicworkflow: read guest output: %w", err)
}

func waitWorkflowGuest(ctx context.Context, guest *workflowGuestProcess) error {
	waitCh := make(chan error, 1)
	go func() {
		waitCh <- guest.cmd.Wait()
	}()
	timer := time.NewTimer(workflowGuestExitGrace)
	defer timer.Stop()
	select {
	case err := <-waitCh:
		return err
	case <-ctx.Done():
		killWorkflowGuest(guest)
		<-waitCh
		return ctx.Err()
	case <-timer.C:
		killWorkflowGuest(guest)
		<-waitCh
		return errWorkflowGuestExitTimeout
	}
}

func killWorkflowGuest(guest *workflowGuestProcess) {
	if guest == nil || guest.cmd == nil || guest.cmd.Process == nil {
		return
	}
	_ = guest.cmd.Process.Kill()
}

func guestErrorWithStderr(err error, stderr string) error {
	stderr = strings.TrimSpace(stderr)
	if stderr == "" {
		return err
	}
	return fmt.Errorf("%w: %s", err, stderr)
}

type limitedBuffer struct {
	mu       sync.Mutex
	limit    int
	buf      bytes.Buffer
	exceeded bool
}

func newLimitedBuffer(limit int) *limitedBuffer {
	return &limitedBuffer{limit: limit}
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b == nil {
		return len(p), nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := b.limit - b.buf.Len()
	if remaining > 0 {
		if remaining > len(p) {
			remaining = len(p)
		}
		_, _ = b.buf.Write(p[:remaining])
	}
	if remaining < len(p) {
		b.exceeded = true
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string {
	if b == nil {
		return ""
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.buf.String()
	if b.exceeded {
		out += fmt.Sprintf("\n... stderr truncated after %d bytes", b.limit)
	}
	return out
}

func (b *limitedBuffer) Exceeded() bool {
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.exceeded
}

var _ Runtime = LocalRunner{}

// pythonGuest is deliberately small: workflow source receives only the
// documented host callbacks. LocalRunner is not a sandbox; production runners
// must still apply process, filesystem, and network isolation themselves.
const pythonGuest = `
import asyncio
import ast
import base64
import inspect
import io
import json
import sys
import threading
import traceback

_CAPTURED_OUTPUT_LIMIT = 1048576
_PROTOCOL_LINE_LIMIT = 4194304
_protocol_stdout = sys.stdout

class _LimitedStdout(io.StringIO):
    def __init__(self, limit):
        super().__init__()
        self._limit = limit
        self._size = 0

    def write(self, value):
        data = str(value)
        encoded = data.encode("utf-8")
        if self._size + len(encoded) > self._limit:
            remaining = self._limit - self._size
            if remaining > 0:
                super().write(encoded[:remaining].decode("utf-8", errors="ignore"))
                self._size = self._limit
            raise RuntimeError(f"workflow stdout exceeds {self._limit} bytes")
        self._size += len(encoded)
        return super().write(data)

_captured_stdout = _LimitedStdout(_CAPTURED_OUTPUT_LIMIT)
sys.stdout = _captured_stdout
_next_call_id = 0
_bridge = None

def _send(message):
    payload = json.dumps(message, separators=(",", ":"))
    if len(payload.encode("utf-8")) > _PROTOCOL_LINE_LIMIT:
        payload = json.dumps({
            "type": "error",
            "error": f"workflow protocol message exceeds {_PROTOCOL_LINE_LIMIT} bytes",
            "stdout": _captured_stdout.getvalue(),
        }, separators=(",", ":"))
        if len(payload.encode("utf-8")) > _PROTOCOL_LINE_LIMIT:
            payload = json.dumps({
                "type": "error",
                "error": f"workflow protocol message exceeds {_PROTOCOL_LINE_LIMIT} bytes",
            }, separators=(",", ":"))
    _protocol_stdout.write(payload + "\n")
    _protocol_stdout.flush()

class _Bridge:
    def __init__(self, loop):
        self._loop = loop
        self._pending = {}
        self._closed = False

    def start(self):
        threading.Thread(target=self._read_results, daemon=True).start()

    def call(self, kind, name, args):
        global _next_call_id
        if self._closed:
            raise RuntimeError("workflow bridge is closed")
        _next_call_id += 1
        call_id = str(_next_call_id)
        future = self._loop.create_future()
        self._pending[call_id] = future
        _send({"type": "call", "id": call_id, "kind": kind, "name": name, "args": args})
        return future

    def close(self):
        self._closed = True
        self._fail_pending(RuntimeError("workflow bridge is closed"))

    def _read_results(self):
        try:
            for line in sys.stdin:
                if not line:
                    break
                reply = json.loads(line)
                self._loop.call_soon_threadsafe(self._deliver, reply)
        except Exception as exc:
            self._notify_failure(exc)
            return
        self._notify_failure(RuntimeError("host closed workflow bridge"))

    def _notify_failure(self, exc):
        try:
            self._loop.call_soon_threadsafe(self._fail_pending, exc)
        except RuntimeError:
            # The workflow has already completed and the event loop is closed.
            pass

    def _deliver(self, reply):
        if reply.get("type") != "result":
            self._fail_pending(RuntimeError("invalid workflow bridge response"))
            return
        future = self._pending.pop(str(reply.get("id", "")), None)
        if future is None or future.done():
            return
        if reply.get("error"):
            future.set_exception(RuntimeError(reply["error"]))
            return
        future.set_result(reply.get("result"))

    def _fail_pending(self, exc):
        for future in self._pending.values():
            if not future.done():
                future.set_exception(exc)
        self._pending.clear()

class _AgentResult(dict):
    # Keep agent's metadata envelope while making common workflow code
    # ergonomic: missing keys are read from a structured result when present.
    # Envelope keys always take precedence over projected structured keys.
    def _structured(self):
        value = dict.get(self, "structured")
        return value if isinstance(value, dict) else None

    def __getitem__(self, key):
        try:
            return dict.__getitem__(self, key)
        except KeyError:
            structured = self._structured()
            if structured is not None:
                return structured[key]
            raise

    def get(self, key, default=None):
        if key in self:
            return dict.get(self, key, default)
        structured = self._structured()
        if structured is not None:
            return structured.get(key, default)
        return default

async def _call(kind, name, args):
    if _bridge is None:
        raise RuntimeError("workflow bridge is not initialized")
    result = await _bridge.call(kind, name, args)
    if kind == "agent" and isinstance(result, dict):
        return _AgentResult(result)
    return result

async def call_tool(name, **kwargs):
    return await _call("tool", name, kwargs)

_AGENT_OPTION_NAMES = {
    "template", "instance_id", "instruction", "tools", "skills",
    "structured_output", "schema",
}

async def agent(input, options=None, **overrides):
    # Both forms are intentional. The mapping form keeps a complete AgentSpec
    # together, while keyword options make generated Python natural:
    # await agent(task, instruction="review", structured_output={...}).
    if options is None:
        resolved = {}
    elif isinstance(options, str):
        resolved = {"template": options}
    elif isinstance(options, dict):
        resolved = dict(options)
    else:
        raise TypeError("agent options must be a mapping, template name, or None")
    unknown = set(resolved).union(overrides).difference(_AGENT_OPTION_NAMES)
    if unknown:
        raise TypeError("unsupported agent option(s): " + ", ".join(sorted(unknown)))
    resolved.update(overrides)
    if not resolved:
        return await _call("agent", "", {"input": input})
    return await _call("agent", "", {"input": input, "options": resolved})

async def parallel(thunks):
    if not isinstance(thunks, (list, tuple)):
        raise TypeError("parallel expects a list or tuple of zero-argument functions")

    async def _run(thunk):
        try:
            if not callable(thunk):
                raise TypeError("parallel expects zero-argument functions")
            awaitable = thunk()
            if not inspect.isawaitable(awaitable):
                raise TypeError("parallel functions must return an awaitable")
            return await awaitable
        except Exception:
            # A failed independent branch should not discard the completed
            # branches. None is the documented failure sentinel.
            return None

    return await asyncio.gather(*[_run(thunk) for thunk in thunks])

async def pipeline(items, *stages):
    if not isinstance(items, (list, tuple)):
        raise TypeError("pipeline expects a list or tuple of items")
    if not stages:
        return list(items)
    if not all(callable(stage) for stage in stages):
        raise TypeError("pipeline stages must be functions")

    async def _run_item(item, index):
        previous = item
        for stage in stages:
            if previous is None:
                return None
            awaitable = stage(previous, item, index)
            if not inspect.isawaitable(awaitable):
                raise TypeError("pipeline stages must return an awaitable")
            previous = await awaitable
        return previous

    return await parallel([
        lambda item=item, index=index: _run_item(item, index)
        for index, item in enumerate(items)
    ])

def _contains_outer_return(node):
    if isinstance(node, ast.Return):
        return True
    # A return nested in a helper is not a return from __workflow__. In
    # particular, this rejects an uncalled async def run() wrapper,
    # which otherwise completes successfully with a misleading null result.
    if isinstance(node, (ast.FunctionDef, ast.AsyncFunctionDef, ast.Lambda, ast.ClassDef)):
        return False
    return any(_contains_outer_return(child) for child in ast.iter_child_nodes(node))

async def _main():
    global _bridge
    source = base64.b64decode(sys.argv[1]).decode("utf-8")
    wrapped = "async def __workflow__():\n" + "\n".join("    " + line for line in source.splitlines())

    parsed = ast.parse(wrapped, "<dynamic-workflow>", "exec")
    workflow = parsed.body[0]
    if not any(_contains_outer_return(statement) for statement in workflow.body):
        raise RuntimeError(
            "workflow code must contain a return statement outside nested functions or classes"
        )
    # JSON-style literals make generated AgentSpec dictionaries less brittle
    # when a model emits JSON inside otherwise valid Python source.
    scope = {
        "call_tool": call_tool,
        "agent": agent,
        "parallel": parallel,
        "pipeline": pipeline,
        "true": True,
        "false": False,
        "null": None,
    }
    exec(compile(wrapped, "<dynamic-workflow>", "exec"), scope)
    _bridge = _Bridge(asyncio.get_running_loop())
    _bridge.start()
    try:
        return await scope["__workflow__"]()
    finally:
        _bridge.close()

try:
    value = asyncio.run(_main())
    _send({"type": "done", "result": value, "stdout": _captured_stdout.getvalue()})
except Exception:
    _send({"type": "error", "error": traceback.format_exc(), "stdout": _captured_stdout.getvalue()})
`
