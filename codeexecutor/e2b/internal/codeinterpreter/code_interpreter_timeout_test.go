//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2026 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package codeinterpreter

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// The old RunCode deadline was execution timeout plus the full request
// timeout. These bounds sit between one phase and that sum, so a combined
// budget fails the upper bound without depending on a few milliseconds.
const (
	phaseBudget  = 400 * time.Millisecond
	timeoutOther = 2 * time.Second
	timeoutFloor = 250 * time.Millisecond
	timeoutCeil  = 1500 * time.Millisecond
)

func TestRunCodeTimeout_ErrorBodyUsesExecutionDeadlineWhenHeaderDisabled(t *testing.T) {
	h := newExecuteHandler(t, executeErrorThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        phaseBudget,
		RequestTimeout: -time.Second,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertExecutionTimeout(t, err)
}

func TestRunCodeTimeout_ErrorBodyDropsRemainingHeaderDeadline(t *testing.T) {
	h := newExecuteHandler(t, executeErrorThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        phaseBudget,
		RequestTimeout: timeoutOther,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertExecutionTimeout(t, err)
}

func TestRunCodeTimeout_ShortHeaderTimeoutDoesNotCutErrorBody(t *testing.T) {
	const (
		requestTimeout = 300 * time.Millisecond
		bodyDelay      = 700 * time.Millisecond
	)
	h := newExecuteHandler(t, executeErrorDelayThenFinish)
	h.delay = bodyDelay
	sbx := newTimeoutSandbox(t, h, requestTimeout)
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: requestTimeout,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	if elapsed < bodyDelay-100*time.Millisecond || elapsed >= timeoutOther {
		t.Fatalf("elapsed %s, want the error body to finish inside the execution deadline", elapsed)
	}
	assertMappedBadRequest(t, err, "slow error")
}

func TestRunCodeTimeout_DisabledExecutionDoesNotKeepHeaderDeadlineOnErrorBody(t *testing.T) {
	const callerDeadline = 900 * time.Millisecond
	h := newExecuteHandler(t, executeErrorThenBlock)
	sbx := newTimeoutSandbox(t, h, phaseBudget)
	ctx, cancel := context.WithTimeout(context.Background(), callerDeadline)
	defer cancel()
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        -time.Second,
		RequestTimeout: phaseBudget,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	if elapsed < 600*time.Millisecond || elapsed > 1400*time.Millisecond {
		t.Fatalf("elapsed %s, want the caller deadline after headers", elapsed)
	}
	assertCallerDeadline(t, err)
}

func TestRunCodeTimeout_CompletedErrorResponseKeepsHTTPMapping(t *testing.T) {
	h := newExecuteHandler(t, executeErrorImmediate)
	sbx := newTimeoutSandbox(t, h, phaseBudget)
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        time.Second,
		RequestTimeout: phaseBudget,
	})
	h.waitExit(t)
	assertMappedBadRequest(t, err, "bad request")
}

func TestRunCodeTimeout_CallerDeadlineDuringErrorBody(t *testing.T) {
	h := newExecuteHandler(t, executeErrorThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	ctx, cancel := context.WithTimeout(context.Background(), phaseBudget)
	defer cancel()
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: timeoutOther,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertCallerDeadline(t, err)
}

func TestRunCodeTimeout_CallerCancelDuringErrorBody(t *testing.T) {
	h := newExecuteHandler(t, executeErrorThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		select {
		case <-h.headersSent:
			time.Sleep(50 * time.Millisecond)
			cancel()
		case <-time.After(2 * time.Second):
			cancel()
		}
	}()
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: -time.Second,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	if elapsed > timeoutCeil {
		t.Fatalf("elapsed %s, want caller cancel before the body deadline", elapsed)
	}
	assertCallerCancel(t, err)
}

func TestRunCodeTimeout_HeaderWaitDoesNotAddExecutionBudget(t *testing.T) {
	h := newExecuteHandler(t, executeBlockHeaders)
	sbx := newTimeoutSandbox(t, h, phaseBudget)
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout: timeoutOther,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertRequestTimeout(t, err)
}

func TestRunCodeTimeout_RequestTimeoutOverride(t *testing.T) {
	h := newExecuteHandler(t, executeBlockHeaders)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: phaseBudget,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertRequestTimeout(t, err)
	if h.payloadHasTimeoutKey() {
		t.Fatal("execute request body contains a timeout field")
	}
}

func TestRunCodeTimeout_ZeroRequestTimeoutUsesConnection(t *testing.T) {
	h := newExecuteHandler(t, executeBlockHeaders)
	sbx := newTimeoutSandbox(t, h, phaseBudget)
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: 0,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertRequestTimeout(t, err)
}

func TestRunCodeTimeout_DisabledExecutionKeepsRequestTimeout(t *testing.T) {
	h := newExecuteHandler(t, executeBlockHeaders)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	// Safety net only. The request deadline must win before this parent deadline,
	// and the result must not be relabeled as the caller's deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        -time.Second,
		RequestTimeout: phaseBudget,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertRequestTimeout(t, err)
}

func TestRunCodeTimeout_DisabledRequestTimeoutStillBoundsExecution(t *testing.T) {
	h := newExecuteHandler(t, executeStreamThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	var stdout int
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        phaseBudget,
		RequestTimeout: -time.Second,
		OnStdout:       func(OutputMessage) { stdout++ },
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertExecutionTimeout(t, err)
	if stdout != 1 {
		t.Fatalf("stdout callbacks = %d, want 1", stdout)
	}
}

func TestRunCodeTimeout_StreamReadDoesNotAddRequestBudget(t *testing.T) {
	h := newExecuteHandler(t, executeStreamThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	var lines []string
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout: phaseBudget,
		OnStdout: func(m OutputMessage) {
			lines = append(lines, m.Line)
		},
		OnStderr: func(OutputMessage) {},
		OnResult: func(*Result) {},
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertExecutionTimeout(t, err)
	if len(lines) != 1 || lines[0] == "" {
		t.Fatalf("stdout before timeout = %#v", lines)
	}
}

func TestRunCodeTimeout_DisabledExecutionAllowsLongStream(t *testing.T) {
	const (
		requestTimeout = 300 * time.Millisecond
		bodyDelay      = 700 * time.Millisecond
	)
	h := newExecuteHandler(t, executeDelayThenFinish)
	h.delay = bodyDelay
	sbx := newTimeoutSandbox(t, h, requestTimeout)
	start := time.Now()
	exec, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        -time.Second,
		RequestTimeout: requestTimeout,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	if err != nil {
		t.Fatalf("disabled execution timeout returned %v after %s", err, elapsed)
	}
	if elapsed < bodyDelay-100*time.Millisecond {
		t.Fatalf("elapsed %s, want the stream to outlive the request timeout", elapsed)
	}
	if exec == nil || exec.Text() != "done" {
		t.Fatalf("execution = %#v", exec)
	}
}

func TestRunCodeTimeout_ClientTimeoutDuringStreamIsTransportTimeout(t *testing.T) {
	h := newExecuteHandler(t, executeStreamThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	sbx.connection.HTTPClient.Timeout = phaseBudget
	var stdout int
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: timeoutOther,
		OnStdout:       func(OutputMessage) { stdout++ },
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertClientTransportTimeout(t, err, clientTimeoutBodyText, clientTimeoutHeaderText)
	if stdout != 1 {
		t.Fatalf("stdout callbacks = %d, want the body read to start", stdout)
	}
	if sbx.connection.HTTPClient.Timeout != phaseBudget {
		t.Fatalf("client timeout = %s, want it unchanged", sbx.connection.HTTPClient.Timeout)
	}
}

func TestRunCodeTimeout_ClientTimeoutDuringErrorBodyIsTransportTimeout(t *testing.T) {
	h := newExecuteHandler(t, executeErrorThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	sbx.connection.HTTPClient.Timeout = phaseBudget
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        -time.Second,
		RequestTimeout: timeoutOther,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertClientTransportTimeout(t, err, clientTimeoutBodyText, clientTimeoutHeaderText)
	var sandboxErr *SandboxError
	if errors.As(err, &sandboxErr) {
		t.Fatalf("incomplete error body returned HTTP mapping: %v", err)
	}
}

func TestRunCodeTimeout_ClientTimeoutDuringHeaderWaitIsTransportTimeout(t *testing.T) {
	h := newExecuteHandler(t, executeBlockHeaders)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	sbx.connection.HTTPClient.Timeout = phaseBudget
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: -time.Second,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertClientTransportTimeout(t, err, clientTimeoutHeaderText, clientTimeoutBodyText)
}

func TestRunCodeTimeout_HeaderDeadlineWinsOverLongerClientTimeout(t *testing.T) {
	h := newExecuteHandler(t, executeBlockHeaders)
	sbx := newTimeoutSandbox(t, h, phaseBudget)
	sbx.connection.HTTPClient.Timeout = timeoutOther
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: phaseBudget,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertRequestTimeout(t, err)
}

func TestRunCodeTimeout_ExecutionDeadlineWinsOverLongerClientTimeout(t *testing.T) {
	h := newExecuteHandler(t, executeStreamThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	sbx.connection.HTTPClient.Timeout = timeoutOther
	start := time.Now()
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        phaseBudget,
		RequestTimeout: timeoutOther,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertExecutionTimeout(t, err)
}

func TestRunCodeTimeout_LongStreamIgnoresRequestTimeout(t *testing.T) {
	const (
		requestTimeout = 300 * time.Millisecond
		bodyDelay      = 700 * time.Millisecond
		execTimeout    = 3 * time.Second
	)
	h := newExecuteHandler(t, executeDelayThenFinish)
	h.delay = bodyDelay
	sbx := newTimeoutSandbox(t, h, requestTimeout)
	var got []string
	start := time.Now()
	exec, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        execTimeout,
		RequestTimeout: requestTimeout,
		OnStdout: func(m OutputMessage) {
			got = append(got, m.Line)
		},
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	if err != nil {
		t.Fatalf("long stream returned %v after %s", err, elapsed)
	}
	if elapsed < bodyDelay-100*time.Millisecond {
		t.Fatalf("elapsed %s, want at least the body delay", elapsed)
	}
	if elapsed > execTimeout {
		t.Fatalf("elapsed %s exceeded execution timeout", elapsed)
	}
	if exec == nil || exec.Text() != "done" {
		t.Fatalf("execution = %#v", exec)
	}
	if len(got) != 1 || got[0] != "partial\n" {
		t.Fatalf("stdout = %#v", got)
	}
}

func TestRunCodeTimeout_CallerCancelBeforeHeaders(t *testing.T) {
	h := newExecuteHandler(t, executeBlockHeaders)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(phaseBudget)
		cancel()
	}()
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: timeoutOther,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertCallerCancel(t, err)
}

func TestRunCodeTimeout_CallerCancelDuringStream(t *testing.T) {
	h := newExecuteHandler(t, executeStreamThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	ctx, cancel := context.WithCancel(context.Background())
	stdoutSeen := make(chan struct{})
	cancelDone := make(chan struct{})
	go func() {
		defer close(cancelDone)
		select {
		case <-stdoutSeen:
		case <-time.After(2 * time.Second):
		}
		cancel()
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-cancelDone:
		case <-time.After(2 * time.Second):
			t.Errorf("cancel goroutine did not return")
		}
	})
	var stdout int
	var once sync.Once
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: timeoutOther,
		OnStdout: func(OutputMessage) {
			stdout++
			once.Do(func() { close(stdoutSeen) })
		},
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	if elapsed > timeoutCeil {
		t.Fatalf("elapsed %s, want caller cancel before the execution budget", elapsed)
	}
	assertCallerCancel(t, err)
	if stdout != 1 {
		t.Fatalf("stdout callbacks = %d, want 1", stdout)
	}
}

func TestRunCodeTimeout_CallerDeadlineBeforeHeaders(t *testing.T) {
	h := newExecuteHandler(t, executeBlockHeaders)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	ctx, cancel := context.WithTimeout(context.Background(), phaseBudget)
	defer cancel()
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: timeoutOther,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertCallerDeadline(t, err)
}

func TestRunCodeTimeout_CallerDeadlineDuringStream(t *testing.T) {
	h := newExecuteHandler(t, executeStreamThenBlock)
	sbx := newTimeoutSandbox(t, h, timeoutOther)
	ctx, cancel := context.WithTimeout(context.Background(), phaseBudget)
	defer cancel()
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: timeoutOther,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertCallerDeadline(t, err)
}

func TestRunCodeTimeout_DisabledDeadlinesKeepCallerDeadline(t *testing.T) {
	h := newExecuteHandler(t, executeBlockHeaders)
	sbx := newTimeoutSandbox(t, h, 50*time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), phaseBudget)
	defer cancel()
	start := time.Now()
	_, err := sbx.RunCode(ctx, "print(1)", &RunCodeOpts{
		Timeout:        -time.Second,
		RequestTimeout: -time.Second,
	})
	elapsed := time.Since(start)
	h.waitExit(t)
	assertPhaseElapsed(t, elapsed)
	assertCallerDeadline(t, err)
}

func TestRunCodeTimeout_SuccessPreservesCallbacksAndClient(t *testing.T) {
	h := newExecuteHandler(t, executeImmediate)
	sbx := newTimeoutSandbox(t, h, time.Second)
	clientTimeout := sbx.connection.HTTPClient.Timeout
	var stdout, stderr int
	var result *Result
	exec, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        time.Second,
		RequestTimeout: time.Second,
		OnStdout:       func(OutputMessage) { stdout++ },
		OnStderr:       func(OutputMessage) { stderr++ },
		OnResult:       func(r *Result) { result = r },
	})
	h.waitExit(t)
	if err != nil {
		t.Fatalf("run code: %v", err)
	}
	if sbx.connection.HTTPClient.Timeout != clientTimeout {
		t.Fatalf("client timeout changed from %s to %s", clientTimeout, sbx.connection.HTTPClient.Timeout)
	}
	if stdout != 1 || stderr != 1 || result == nil || !result.IsMainResult {
		t.Fatalf("callbacks stdout=%d stderr=%d result=%v", stdout, stderr, result)
	}
	if exec.Text() != "ok" || len(exec.Logs.Stdout) != 1 || len(exec.Logs.Stderr) != 1 {
		t.Fatalf("execution = %#v logs=%#v", exec, exec.Logs)
	}
	if h.payloadHasTimeoutKey() {
		t.Fatal("execute request body contains a timeout field")
	}
}

func TestResolveCodeTimeouts(t *testing.T) {
	got, ok := resolveRequestTimeout(0, 30*time.Second)
	if !ok || got != 30*time.Second {
		t.Fatalf("inherit request timeout = %s ok=%t", got, ok)
	}
	if _, ok := resolveRequestTimeout(-time.Second, 30*time.Second); ok {
		t.Fatal("negative request timeout was enabled")
	}
	if _, ok := resolveRequestTimeout(0, -time.Second); ok {
		t.Fatal("negative connection request timeout was enabled")
	}
	got, ok = resolveRequestTimeout(time.Second, 30*time.Second)
	if !ok || got != time.Second {
		t.Fatalf("request override = %s ok=%t", got, ok)
	}

	got, ok = resolveExecutionTimeout(0)
	if !ok || got != DefaultTimeout*time.Second {
		t.Fatalf("default execution timeout = %s ok=%t", got, ok)
	}
	if _, ok := resolveExecutionTimeout(-time.Second); ok {
		t.Fatal("negative execution timeout was enabled")
	}
	got, ok = resolveExecutionTimeout(2 * time.Second)
	if !ok || got != 2*time.Second {
		t.Fatalf("execution override = %s ok=%t", got, ok)
	}
}

func TestTimeoutErrorWithoutCauseUnwrapsNil(t *testing.T) {
	err := formatRequestTimeoutError()
	if errors.Unwrap(err) != nil {
		t.Fatalf("unwrap = %v, want nil", errors.Unwrap(err))
	}
	var nilTimeout *TimeoutError
	if nilTimeout.Unwrap() != nil {
		t.Fatal("nil TimeoutError unwrap")
	}
	if errors.Is(err, errRequestTimeout) {
		t.Fatal("status-style timeout error matched a RunCode request cause")
	}
	const (
		wantRequest = "timeout: Request timed out — the 'RequestTimeout' option can be used to increase this timeout"
		wantExecute = "timeout: Execution timed out — the 'Timeout' option can be used to increase this timeout"
	)
	if err.Error() != wantRequest {
		t.Fatalf("request timeout text = %q", err.Error())
	}
	if got := runCodeExecutionTimeout(nil).Error(); got != wantExecute {
		t.Fatalf("execution timeout text = %q", got)
	}
	triggered := runCodeRequestTimeout(context.DeadlineExceeded)
	if errors.Is(triggered, context.DeadlineExceeded) || !errors.Is(triggered, errRequestTimeout) {
		t.Fatalf("triggered timeout = %v", triggered)
	}
	if unwrapped := errors.Unwrap(triggered); unwrapped == nil || !strings.Contains(unwrapped.Error(), "deadline exceeded") {
		t.Fatalf("unwrap = %v", unwrapped)
	}
}

func TestCodeExecTimerSwitchKeepsLongRequestDeadline(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	timer := newCodeExecTimer(cancel)
	if !timer.arm(timeoutPhaseRequest, time.Hour, true, errRequestTimeout) {
		t.Fatal("arm request deadline")
	}
	if !timer.arm(timeoutPhaseExecution, time.Hour, true, errExecutionTimeout) {
		t.Fatal("request deadline won before the switch")
	}
	time.Sleep(20 * time.Millisecond)
	if timer.cause() != nil || ctx.Err() != nil {
		t.Fatalf("switched timer fired: cause %v ctx %v", timer.cause(), ctx.Err())
	}
	timer.stop()
}

func TestCodeExecTimerSwitchDropsRequestDeadline(t *testing.T) {
	for i := 0; i < 40; i++ {
		ctx, cancel := context.WithCancelCause(context.Background())
		timer := newCodeExecTimer(cancel)
		if !timer.arm(timeoutPhaseRequest, time.Nanosecond, true, errRequestTimeout) {
			t.Fatal("arm request deadline")
		}
		switched := timer.arm(timeoutPhaseExecution, time.Hour, true, errExecutionTimeout)
		if !switched {
			if !errors.Is(timer.cause(), errRequestTimeout) {
				t.Fatalf("switch lost without request cause: %v", timer.cause())
			}
			timer.stop()
			cancel(nil)
			continue
		}
		time.Sleep(15 * time.Millisecond)
		if errors.Is(timer.cause(), errRequestTimeout) || errors.Is(context.Cause(ctx), errRequestTimeout) {
			t.Fatal("request deadline canceled the execution phase")
		}
		timer.stop()
		if ctx.Err() != nil && !errors.Is(context.Cause(ctx), errExecutionTimeout) {
			t.Fatalf("context after stop = %v cause %v", ctx.Err(), context.Cause(ctx))
		}
		cancel(nil)
	}
}

func TestCodeExecTimerExecutionDeadlineFires(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	timer := newCodeExecTimer(cancel)
	if !timer.arm(timeoutPhaseRequest, time.Hour, true, errRequestTimeout) {
		t.Fatal("arm request deadline")
	}
	if !timer.arm(timeoutPhaseExecution, 30*time.Millisecond, true, errExecutionTimeout) {
		t.Fatal("arm execution deadline")
	}
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("execution deadline did not cancel the stream")
	}
	if !errors.Is(timer.cause(), errExecutionTimeout) || !errors.Is(context.Cause(ctx), errExecutionTimeout) {
		t.Fatalf("cause = %v context cause = %v", timer.cause(), context.Cause(ctx))
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("execution deadline was reported as context.DeadlineExceeded")
	}
}

func TestCodeExecTimerArmAfterStopReturnsFalse(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	timer := newCodeExecTimer(cancel)
	if !timer.arm(timeoutPhaseRequest, time.Hour, true, errRequestTimeout) {
		t.Fatal("arm request deadline")
	}
	timer.stop()
	if timer.arm(timeoutPhaseExecution, time.Hour, true, errExecutionTimeout) {
		t.Fatal("arm after stop succeeded")
	}
	if ctx.Err() != nil || timer.cause() != nil {
		t.Fatalf("stopped timer changed state: ctx %v cause %v", ctx.Err(), timer.cause())
	}
}

func TestCodeExecTimerStaleFireDoesNotCancel(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	timer := newCodeExecTimer(cancel)
	if !timer.arm(timeoutPhaseRequest, time.Hour, true, errRequestTimeout) {
		t.Fatal("arm request deadline")
	}
	if !timer.arm(timeoutPhaseExecution, time.Hour, true, errExecutionTimeout) {
		t.Fatal("arm execution deadline")
	}
	timer.fire(timeoutPhaseRequest, errRequestTimeout)
	if ctx.Err() != nil || timer.cause() != nil {
		t.Fatalf("stale header callback canceled execution: ctx %v cause %v", ctx.Err(), timer.cause())
	}
	timer.stop()
}

func TestClassifyRunCodeErrorTimerCauseBeforeCancel(t *testing.T) {
	stream, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	timer := newCodeExecTimer(nil)
	if !timer.arm(timeoutPhaseExecution, time.Hour, true, errExecutionTimeout) {
		t.Fatal("arm execution deadline")
	}
	timer.fire(timeoutPhaseExecution, errExecutionTimeout)
	err := classifyRunCodeError(context.Background(), stream, timer, nil)
	assertExecutionTimeout(t, err)
	if stream.Err() != nil {
		t.Fatalf("stream err = %v, want the deadline recorded before cancel", stream.Err())
	}
}

func TestClassifyRunCodeErrorUsesTimerCauseWhenStreamCauseDiffers(t *testing.T) {
	stream, cancel := context.WithCancelCause(context.Background())
	cancel(errors.New("transport closed"))
	timer := newCodeExecTimer(nil)
	if !timer.arm(timeoutPhaseExecution, time.Hour, true, errExecutionTimeout) {
		t.Fatal("arm execution deadline")
	}
	timer.fire(timeoutPhaseExecution, errExecutionTimeout)
	err := classifyRunCodeError(context.Background(), stream, timer, nil)
	assertExecutionTimeout(t, err)
}

func TestClassifyRunCodeErrorPreservesCallerWithoutTransportError(t *testing.T) {
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	stream, stop := context.WithCancel(context.Background())
	defer stop()
	err := classifyRunCodeError(caller, stream, newCodeExecTimer(nil), nil)
	assertCallerCancel(t, err)
}

func TestClassifyRunCodeErrorJoinsUnrelatedTransportError(t *testing.T) {
	caller, cancel := context.WithCancel(context.Background())
	cancel()
	stream, stop := context.WithCancel(context.Background())
	defer stop()
	err := classifyRunCodeError(caller, stream, newCodeExecTimer(nil), errors.New("reset by peer"))
	if !errors.Is(err, context.Canceled) || !strings.Contains(err.Error(), "reset by peer") {
		t.Fatalf("error = %v, want caller cancellation and the transport error", err)
	}
	var timeoutErr *TimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("joined caller cancellation reported as TimeoutError: %v", err)
	}
}

func TestClassifyRunCodeErrorKeepsNonTimeoutNetError(t *testing.T) {
	err := classifyRunCodeError(context.Background(), context.Background(), newCodeExecTimer(nil), temporaryNetError{})
	if err.Error() != "temporary outage" {
		t.Fatalf("error = %v", err)
	}
	var timeoutErr *TimeoutError
	if errors.As(err, &timeoutErr) || errors.Is(err, errRequestTimeout) || errors.Is(err, errExecutionTimeout) {
		t.Fatalf("non-timeout network error was relabeled: %v", err)
	}
}

func TestTransportTimeoutWithoutPhaseDoesNotClaimOne(t *testing.T) {
	err := newTransportTimeout(timeoutPhaseDone, nil)
	if err.Error() != clientTimeoutPlainText {
		t.Fatalf("error = %q", err.Error())
	}
	if errors.Unwrap(err) != nil {
		t.Fatal("unwrap of a timeout without a transport error")
	}
	wrapped := classifyRunCodeError(
		context.Background(), context.Background(), newCodeExecTimer(nil), timeoutNetError{},
	)
	if !strings.HasPrefix(wrapped.Error(), clientTimeoutPlainText) ||
		strings.Contains(wrapped.Error(), "while waiting") ||
		strings.Contains(wrapped.Error(), "while reading") {
		t.Fatalf("unspecified phase = %v", wrapped)
	}
	var netErr net.Error
	if !errors.As(wrapped, &netErr) || !netErr.Timeout() {
		t.Fatalf("error = %v, want the transport timeout", wrapped)
	}
}

func TestRunCodeTimeout_NonTimeoutTransportErrorIsUnchanged(t *testing.T) {
	h := newExecuteHandler(t, executeBlockHeaders)
	sbx := newTimeoutSandbox(t, h, time.Second)
	sbx.connection.HTTPClient.Transport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial failed")
	})
	_, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        time.Second,
		RequestTimeout: time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "dial failed") {
		t.Fatalf("error = %v", err)
	}
	var timeoutErr *TimeoutError
	if errors.As(err, &timeoutErr) || errors.Is(err, errRequestTimeout) || errors.Is(err, errExecutionTimeout) {
		t.Fatalf("transport error was relabeled: %v", err)
	}
}

func TestRunCodeTimeout_HeaderDeadlineBeforePhaseSwitchStaysRequestTimeout(t *testing.T) {
	h := newExecuteHandler(t, executeImmediate)
	sbx := newTimeoutSandbox(t, h, phaseBudget)
	sbx.connection.HTTPClient.Transport = respondAfterCancel{}
	start := time.Now()
	exec, err := sbx.RunCode(context.Background(), "print(1)", &RunCodeOpts{
		Timeout:        timeoutOther,
		RequestTimeout: phaseBudget,
	})
	elapsed := time.Since(start)
	assertPhaseElapsed(t, elapsed)
	assertRequestTimeout(t, err)
	if exec != nil {
		t.Fatalf("execution = %#v, want nil after the header deadline won", exec)
	}
}

func TestCodeExecTimerStopPreventsFire(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	timer := newCodeExecTimer(cancel)
	if !timer.arm(timeoutPhaseRequest, 20*time.Millisecond, true, errRequestTimeout) {
		t.Fatal("arm request deadline")
	}
	timer.stop()
	time.Sleep(50 * time.Millisecond)
	if timer.cause() != nil || ctx.Err() != nil {
		t.Fatalf("stopped timer fired: cause %v ctx %v", timer.cause(), ctx.Err())
	}
}

func assertPhaseElapsed(t *testing.T, elapsed time.Duration) {
	t.Helper()
	if elapsed < timeoutFloor || elapsed > timeoutCeil {
		t.Fatalf("elapsed %s, want %s..%s", elapsed, timeoutFloor, timeoutCeil)
	}
}

func assertRequestTimeout(t *testing.T, err error) {
	t.Helper()
	assertTimeoutKind(t, err, "Request timed out", "Execution timed out")
	if !errors.Is(err, errRequestTimeout) || errors.Is(err, errExecutionTimeout) {
		t.Fatalf("error = %v, want the request timeout cause only", err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request timeout matches a caller context error: %v", err)
	}
}

func assertExecutionTimeout(t *testing.T, err error) {
	t.Helper()
	assertTimeoutKind(t, err, "Execution timed out", "Request timed out")
	if !errors.Is(err, errExecutionTimeout) || errors.Is(err, errRequestTimeout) {
		t.Fatalf("error = %v, want the execution timeout cause only", err)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("execution timeout matches a caller context error: %v", err)
	}
}

func assertTimeoutKind(t *testing.T, err error, want, reject string) {
	t.Helper()
	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("error = %T (%v), want *TimeoutError containing %q", err, err, want)
	}
	if !strings.Contains(timeoutErr.Message, want) || strings.Contains(timeoutErr.Message, reject) {
		t.Fatalf("timeout message = %q, want %q", timeoutErr.Message, want)
	}
}

func assertCallerCancel(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want caller cancellation", err)
	}
	var timeoutErr *TimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("caller cancellation reported as TimeoutError: %v", err)
	}
}

func assertMappedBadRequest(t *testing.T, err error, message string) {
	t.Helper()
	var sandboxErr *SandboxError
	if !errors.As(err, &sandboxErr) {
		t.Fatalf("error = %T (%v), want *SandboxError", err, err)
	}
	if sandboxErr.StatusCode != http.StatusBadRequest || !strings.Contains(sandboxErr.Message, message) {
		t.Fatalf("sandbox error = %+v, want status 400 and %q", sandboxErr, message)
	}
	if errors.Is(err, errRequestTimeout) || errors.Is(err, errExecutionTimeout) {
		t.Fatalf("completed HTTP error matched a RunCode deadline: %v", err)
	}
}

func assertClientTransportTimeout(t *testing.T, err error, want, reject string) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want an HTTP client or transport timeout")
	}
	var timeoutErr *TimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("HTTP client timeout reported as TimeoutError: %v", err)
	}
	if errors.Is(err, errRequestTimeout) || errors.Is(err, errExecutionTimeout) {
		t.Fatalf("HTTP client timeout matched a local deadline: %v", err)
	}
	if strings.Contains(err.Error(), "RequestTimeout") || strings.Contains(err.Error(), "the 'Timeout' option") {
		t.Fatalf("error suggests a deadline option that does not apply: %v", err)
	}
	if !strings.Contains(err.Error(), want) || strings.Contains(err.Error(), reject) {
		t.Fatalf("error = %v, want %q without %q", err, want, reject)
	}
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("error = %v, want the transport timeout in the chain", err)
	}
	if !strings.Contains(err.Error(), "Client.Timeout") {
		t.Fatalf("error = %v, want the original client timeout text", err)
	}
}

func assertCallerDeadline(t *testing.T, err error) {
	t.Helper()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want caller deadline", err)
	}
	var timeoutErr *TimeoutError
	if errors.As(err, &timeoutErr) {
		t.Fatalf("caller deadline reported as TimeoutError: %v", err)
	}
}

const (
	executeBlockHeaders = iota
	executeStreamThenBlock
	executeDelayThenFinish
	executeImmediate
	executeErrorThenBlock
	executeErrorDelayThenFinish
	executeErrorImmediate
)

type executeHandler struct {
	mode        int
	delay       time.Duration
	headersSent chan struct{}
	exited      chan struct{}

	mu      sync.Mutex
	payload map[string]any
}

func newExecuteHandler(t *testing.T, mode int) *executeHandler {
	t.Helper()
	return &executeHandler{
		mode:        mode,
		headersSent: make(chan struct{}),
		exited:      make(chan struct{}),
	}
}

func (h *executeHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer close(h.exited)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return
	}
	h.mu.Lock()
	h.payload = payload
	h.mu.Unlock()

	switch h.mode {
	case executeBlockHeaders:
		<-r.Context().Done()
	case executeStreamThenBlock:
		if !h.writeStreamHeader(w) {
			return
		}
		if _, err := io.WriteString(w, "{\"type\":\"stdout\",\"text\":\"partial\\n\",\"timestamp\":1}\n"); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	case executeDelayThenFinish:
		if !h.writeStreamHeader(w) {
			return
		}
		timer := time.NewTimer(h.delay)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		_, _ = io.WriteString(w, "{\"type\":\"stdout\",\"text\":\"partial\\n\",\"timestamp\":1}\n")
		_, _ = io.WriteString(w, "{\"type\":\"result\",\"text\":\"done\",\"is_main_result\":true}\n")
		w.(http.Flusher).Flush()
	case executeImmediate:
		w.Header().Set("Content-Type", "application/x-ndjson")
		_, _ = io.WriteString(w, "{\"type\":\"stdout\",\"text\":\"out\\n\",\"timestamp\":1}\n")
		_, _ = io.WriteString(w, "{\"type\":\"stderr\",\"text\":\"err\\n\",\"timestamp\":2}\n")
		_, _ = io.WriteString(w, "{\"type\":\"result\",\"text\":\"ok\",\"is_main_result\":true}\n")
	case executeErrorThenBlock:
		if !h.writeErrorHeader(w) {
			return
		}
		<-r.Context().Done()
	case executeErrorDelayThenFinish:
		if !h.writeErrorHeader(w) {
			return
		}
		timer := time.NewTimer(h.delay)
		select {
		case <-r.Context().Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		_, _ = io.WriteString(w, "slow error")
	case executeErrorImmediate:
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, "bad request")
		h.signalHeaders()
	}
}

func (h *executeHandler) writeErrorHeader(w http.ResponseWriter) bool {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusBadRequest)
	w.(http.Flusher).Flush()
	h.signalHeaders()
	return true
}

func (h *executeHandler) signalHeaders() {
	select {
	case <-h.headersSent:
	default:
		close(h.headersSent)
	}
}

func (h *executeHandler) writeStreamHeader(w http.ResponseWriter) bool {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
	select {
	case <-h.headersSent:
	default:
		close(h.headersSent)
	}
	return true
}

func (h *executeHandler) waitExit(t *testing.T) {
	t.Helper()
	select {
	case <-h.exited:
	case <-time.After(2 * time.Second):
		t.Fatal("httptest handler did not return after the client stopped waiting")
	}
}

func (h *executeHandler) payloadHasTimeoutKey() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.payload == nil {
		return false
	}
	_, ok := h.payload["timeout"]
	return ok
}

type temporaryNetError struct{}

func (temporaryNetError) Error() string   { return "temporary outage" }
func (temporaryNetError) Timeout() bool   { return false }
func (temporaryNetError) Temporary() bool { return true }

type timeoutNetError struct{}

func (timeoutNetError) Error() string   { return "net deadline" }
func (timeoutNetError) Timeout() bool   { return true }
func (timeoutNetError) Temporary() bool { return true }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

// respondAfterCancel returns headers only after the caller context is
// canceled, so RunCode observes a header deadline that won before it can
// switch to the body phase.
type respondAfterCancel struct{}

func (respondAfterCancel) RoundTrip(req *http.Request) (*http.Response, error) {
	<-req.Context().Done()
	rec := httptest.NewRecorder()
	rec.Header().Set("Content-Type", "application/x-ndjson")
	rec.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(rec, "{\"type\":\"result\",\"text\":\"late\",\"is_main_result\":true}\n")
	return rec.Result(), nil
}

func newTimeoutSandbox(t *testing.T, h http.Handler, requestTimeout time.Duration) *Sandbox {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	sbx := newMockSandbox(t, srv)
	sbx.connection.RequestTimeout = requestTimeout
	if sbx.connection.HTTPClient.Timeout != 0 {
		t.Fatalf("test client timeout = %s, want 0 so the stream is not capped by http.Client", sbx.connection.HTTPClient.Timeout)
	}
	return sbx
}
