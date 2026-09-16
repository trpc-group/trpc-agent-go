//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package sandbox

import (
	"context"
	"errors"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestWithDiagnosticsReturnsBufferedChannel(t *testing.T) {
	ctx, ch := WithDiagnostics(context.Background())
	if ctx == nil || ch == nil {
		t.Fatalf("WithDiagnostics returned nil context or channel")
	}
	if diagnosticsChanFromContext(ctx) == nil {
		t.Fatalf("diagnostics channel was not stored in context")
	}
	if diagnosticsChanFromContext(context.Background()) != nil {
		t.Fatalf("background context unexpectedly had diagnostics channel")
	}

	ctx, ch = WithDiagnostics(ctx)
	if diagnosticsChanFromContext(ctx) == nil {
		t.Fatalf("nested WithDiagnostics lost diagnostics channel")
	}
	select {
	case <-ch:
		t.Fatalf("fresh diagnostics channel should be empty")
	default:
	}
}

func TestRuntimeAndExecutorCloseAreNilSafeAndIdempotent(t *testing.T) {
	var nilRuntime *Runtime
	if err := nilRuntime.Close(); err != nil {
		t.Fatalf("nil Runtime.Close: %v", err)
	}
	var nilExecutor *CodeExecutor
	if err := nilExecutor.Close(); err != nil {
		t.Fatalf("nil CodeExecutor.Close: %v", err)
	}
	if err := (&CodeExecutor{}).Close(); err != nil {
		t.Fatalf("CodeExecutor without runtime Close: %v", err)
	}

	executor := New()
	if err := executor.Close(); err != nil {
		t.Fatalf("CodeExecutor.Close: %v", err)
	}
	if err := executor.Close(); err != nil {
		t.Fatalf("second CodeExecutor.Close: %v", err)
	}
}

func TestRunProgramWithDiagnosticsDisabledProfile(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "run/diagnostics-disabled", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	ctx, diagnosticsCh := WithDiagnostics(context.Background())
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "echo ok"},
	})
	diagnostics := readDiagnostics(t, diagnosticsCh)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if res.ExitCode != 0 || diagnostics.Denials != nil {
		t.Fatalf("result=%#v diagnostics=%#v, want success with nil denials", res, diagnostics)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatalf("sandboxDenialCollectingReady = true after disabled profile run, want false")
	}
}

func TestRunProgramWithDiagnosticsContextReuseDoesNotBlock(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "run/diagnostics-reused-context", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	ctx, diagnosticsCh := WithDiagnostics(context.Background())
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "echo first"},
	})
	if err != nil {
		t.Fatalf("first run error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("first run result = %#v, want success", res)
	}

	done := make(chan error, 1)
	go func() {
		res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
			Cmd:  "bash",
			Args: []string{"-c", "echo second"},
		})
		if err != nil {
			done <- err
			return
		}
		if res.ExitCode != 0 {
			done <- errors.New("second run returned non-zero exit code")
			return
		}
		done <- nil
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("second run error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("second run blocked while diagnostics channel was full")
	}

	_ = readDiagnostics(t, diagnosticsCh)
	select {
	case diagnostics := <-diagnosticsCh:
		t.Fatalf("reused diagnostics channel received extra value: %#v", diagnostics)
	default:
	}
}

func TestRunProgramWithDiagnosticsEmptyCommandStillDeliversResult(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "run/diagnostics-empty-cmd", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	ctx, diagnosticsCh := WithDiagnostics(context.Background())
	_, err = rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{})
	diagnostics := readDiagnostics(t, diagnosticsCh)
	if !isKind(err, ErrPolicyViolation) {
		t.Fatalf("empty command error = %v, want ErrPolicyViolation", err)
	}
	if diagnostics.Denials != nil {
		t.Fatalf("diagnostics = %#v, want nil denials on prepare failure", diagnostics)
	}
}

func TestDiagnosticsCapabilityReturnsZeroValueOnNonManagedProfile(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
	)
	caps := rt.DiagnosticsCapability()
	if caps.Supported || caps.EventStreamAvailable || caps.StrongCorrelation ||
		caps.ProbeCompleted || caps.ExplicitDenialsCollectible ||
		caps.DefaultDenialsCollectible {
		t.Fatalf("disabled profile caps = %#v, want zero value", caps)
	}
}

func TestWithDenialFilterOption(t *testing.T) {
	filter := DenialFilter{
		DisableAutomatic: true,
		Ignore: []DenialIgnoreRule{{
			Operations: []string{"file-read-data"},
		}},
	}
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithDenialFilter(filter),
	)
	if denials, _ := rt.collectSandboxDenials(
		context.Background(), sandboxDenialRun{}, "/bin/cat", time.Millisecond,
	); denials != nil {
		t.Fatalf("collectSandboxDenials with empty tag = %#v, want nil", denials)
	}
	run := rt.sandboxDenialRunForCollecting(DangerFullAccessProfile())
	if run.enabled {
		t.Fatalf("sandboxDenialRunForCollecting on disabled profile = %#v, want disabled", run)
	}
	if rt.sandboxDenialCollectingReady() {
		t.Fatalf("sandboxDenialCollectingReady = true before monitor init, want false")
	}
	run = rt.sandboxDenialRunForCollecting(WorkspaceWriteProfile())
	if run.enabled {
		t.Fatalf("sandboxDenialRunForCollecting before monitor init = %#v, want disabled", run)
	}
}

func TestRunProgramWithDiagnosticsTimeoutStillDeliversResult(t *testing.T) {
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(DangerFullAccessProfile()),
		WithDefaultTimeout(50*time.Millisecond),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "run/diagnostics-timeout", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	ctx, diagnosticsCh := WithDiagnostics(context.Background())
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", "sleep 1"},
	})
	diagnostics := readDiagnostics(t, diagnosticsCh)
	if !isKind(err, ErrTimeout) {
		t.Fatalf("timeout error = %v, want ErrTimeout", err)
	}
	if !res.TimedOut || res.ExitCode != -1 {
		t.Fatalf("timeout result = %#v, want timed out exit -1", res)
	}
	if diagnostics.Denials != nil {
		t.Fatalf("diagnostics = %#v, want nil denials on disabled profile timeout", diagnostics)
	}
}

func TestCollectRunDiagnosticsDisabledReturnsZeroValue(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	got := rt.collectRunDiagnostics(
		context.Background(),
		sandboxDenialRun{},
		"/bin/cat",
		true,
	)
	if len(got.Denials) != 0 || got.Truncated {
		t.Fatalf("disabled collect = %#v, want zero value", got)
	}
}

func TestCollectRunDiagnosticsEnabledPaths(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	run := sandboxDenialRun{enabled: true, runTag: "coverage-tag"}

	got := rt.collectRunDiagnostics(
		context.Background(),
		run,
		"/bin/cat",
		false,
	)
	if len(got.Denials) != 0 || got.Truncated {
		t.Fatalf("non-timeout collect = %#v, want zero value", got)
	}

	// A canceled runCtx must not block timeout collection: that path installs
	// its own bounded Background context before calling collectSandboxDenials.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	got = rt.collectRunDiagnostics(canceled, run, "/bin/cat", true)
	if len(got.Denials) != 0 || got.Truncated {
		t.Fatalf("timeout collect = %#v, want zero value", got)
	}
}

func readDiagnostics(t *testing.T, ch <-chan Diagnostics) Diagnostics {
	t.Helper()
	select {
	case diagnostics := <-ch:
		return diagnostics
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for diagnostics")
		return Diagnostics{}
	}
}
