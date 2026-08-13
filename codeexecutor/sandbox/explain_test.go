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
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
)

func TestExplainProfilesAndNetworkModes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		profile    PermissionProfile
		wantFS     FileSystemSandboxType
		wantNet    NetworkMode
		wantStatus PreflightStatus
	}{
		{
			name:       "workspace-write restricted",
			profile:    WorkspaceWriteProfile(),
			wantFS:     FileSystemSandboxWorkspaceWrite,
			wantNet:    NetworkRestricted,
			wantStatus: "", // platform-dependent managed status
		},
		{
			name:       "read-only restricted",
			profile:    ReadOnlyProfile(),
			wantFS:     FileSystemSandboxReadOnly,
			wantNet:    NetworkRestricted,
			wantStatus: "",
		},
		{
			name:       "read-only with workspace-relative write path",
			profile:    ReadOnlyProfile().WithWritePaths("work"),
			wantFS:     FileSystemSandboxWorkspaceWrite,
			wantNet:    NetworkRestricted,
			wantStatus: "",
		},
		{
			name:       "read-only with host-absolute write path",
			profile:    ReadOnlyProfile().WithWritePaths("/tmp/host-write"),
			wantFS:     FileSystemSandboxReadOnly,
			wantNet:    NetworkRestricted,
			wantStatus: "",
		},
		{
			name:       "disabled",
			profile:    DangerFullAccessProfile(),
			wantFS:     FileSystemSandboxDisabled,
			wantNet:    NetworkEnabled,
			wantStatus: PreflightNotRequired,
		},
		{
			name: "external",
			profile: ExternalSandboxProfile(NetworkPolicy{
				Mode: NetworkEnabled,
			}),
			wantFS:     FileSystemSandboxExternal,
			wantNet:    NetworkEnabled,
			wantStatus: PreflightUnsupported,
		},
		{
			name: "workspace-write enabled network",
			profile: WorkspaceWriteProfile().WithNetworkPolicy(NetworkPolicy{
				Mode: NetworkEnabled,
			}),
			wantFS:     FileSystemSandboxWorkspaceWrite,
			wantNet:    NetworkEnabled,
			wantStatus: "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rt := NewRuntime(WithPermissionProfile(tc.profile))
			report, err := rt.Explain(context.Background())
			if report.FileSystemSandbox != tc.wantFS {
				t.Fatalf("filesystem = %q, want %q", report.FileSystemSandbox, tc.wantFS)
			}
			if report.NetworkMode != tc.wantNet {
				t.Fatalf("network = %q, want %q", report.NetworkMode, tc.wantNet)
			}
			if tc.wantStatus != "" {
				if report.PreflightStatus != tc.wantStatus {
					t.Fatalf("preflight = %q, want %q", report.PreflightStatus, tc.wantStatus)
				}
				if err != nil {
					t.Fatalf("Explain() unexpected error: %v", err)
				}
				return
			}
			assertManagedExplainStatus(t, report, err)
		})
	}
}

func TestExplainBackendAutoAndExplicit(t *testing.T) {
	t.Parallel()

	auto := NewRuntime()
	report, err := auto.Explain(context.Background())
	if report.RequestedBackend != BackendAuto {
		t.Fatalf("requested = %q, want %q", report.RequestedBackend, BackendAuto)
	}
	wantResolved := resolveExplainBackend(BackendAuto)
	if report.ResolvedBackend != wantResolved {
		t.Fatalf("resolved = %q, want %q", report.ResolvedBackend, wantResolved)
	}
	assertManagedExplainStatus(t, report, err)

	explicit := BackendLinuxBubblewrap
	if runtime.GOOS == "darwin" {
		explicit = BackendMacOSSandboxExec
	}
	rt := NewRuntime(WithBackend(explicit))
	report, err = rt.Explain(context.Background())
	if report.RequestedBackend != explicit || report.ResolvedBackend != explicit {
		t.Fatalf("backend = %q -> %q, want %q -> %q",
			report.RequestedBackend, report.ResolvedBackend, explicit, explicit)
	}
	assertManagedExplainStatus(t, report, err)
}

func TestExplainUnsupportedBackend(t *testing.T) {
	t.Parallel()

	var wrong BackendType
	switch runtime.GOOS {
	case "linux":
		wrong = BackendMacOSSandboxExec
	case "darwin":
		wrong = BackendLinuxBubblewrap
	default:
		wrong = BackendLinuxBubblewrap
	}
	rt := NewRuntime(
		WithBackend(wrong),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	report, err := rt.Explain(context.Background())
	if report.RequestedBackend != wrong || report.ResolvedBackend != wrong {
		t.Fatalf("backend = %q -> %q, want %q",
			report.RequestedBackend, report.ResolvedBackend, wrong)
	}
	if report.PreflightStatus != PreflightUnsupported {
		t.Fatalf("preflight = %q, want %q", report.PreflightStatus, PreflightUnsupported)
	}
	if err == nil {
		t.Fatal("Explain() error = nil, want unsupported backend error")
	}
	if report.PreflightError == "" {
		t.Fatal("PreflightError is empty")
	}
	if !isKind(err, ErrUnsupportedBackend) {
		t.Fatalf("error kind = %v, want %s", err, ErrUnsupportedBackend)
	}
}

func TestExplainContextCanceled(t *testing.T) {
	t.Parallel()

	rt := NewRuntime(WithPermissionProfile(WorkspaceWriteProfile()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := rt.Explain(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if report.FileSystemSandbox != "" || report.PreflightStatus != "" {
		// Before preflight starts, canceled context returns immediately.
		t.Fatalf("unexpected partial report on canceled context: %+v", report)
	}
}

func TestExplainDoesNotCreateWorkspace(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	rt := NewRuntime(
		WithWorkspaceRoot(root),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	if _, err := rt.Explain(context.Background()); err != nil &&
		!isKind(err, ErrUnsupportedBackend) && !isKind(err, ErrSetupFailed) {
		t.Fatalf("Explain() unexpected error: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		names := make([]string, 0, len(entries))
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		t.Fatalf("Explain created files under workspace root: %v", names)
	}
}

func TestExplainDoesNotAcquireRunLock(t *testing.T) {
	t.Parallel()

	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile()),
	)
	ws := codeexecutor.Workspace{ID: "explain-lock", Path: t.TempDir()}
	unlock, err := rt.lockWorkspaceRunContext(context.Background(), ws)
	if err != nil {
		t.Fatal(err)
	}
	defer unlock()

	done := make(chan error, 1)
	go func() {
		_, explainErr := rt.Explain(context.Background())
		done <- explainErr
	}()
	select {
	case err := <-done:
		if err != nil && !isKind(err, ErrUnsupportedBackend) && !isKind(err, ErrSetupFailed) {
			t.Fatalf("Explain() unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Explain blocked on workspace run lock")
	}
}

func TestExplainReportString(t *testing.T) {
	t.Parallel()

	report := ExplainReport{
		RequestedBackend:  BackendAuto,
		ResolvedBackend:   BackendLinuxBubblewrap,
		FileSystemSandbox: FileSystemSandboxWorkspaceWrite,
		NetworkMode:       NetworkRestricted,
		PreflightStatus:   PreflightReady,
	}
	got := report.String()
	for _, want := range []string{
		"Sandbox",
		"backend:    auto -> linux-bubblewrap",
		"filesystem: workspace-write",
		"network:    restricted",
		"preflight:  ready",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("String() missing %q\n%s", want, got)
		}
	}

	failed := ExplainReport{
		RequestedBackend:  BackendLinuxBubblewrap,
		ResolvedBackend:   BackendLinuxBubblewrap,
		FileSystemSandbox: FileSystemSandboxReadOnly,
		NetworkMode:       NetworkEnabled,
		PreflightStatus:   PreflightFailed,
		PreflightError:    "SetupFailed backend=linux-bubblewrap bubblewrap executable not found in PATH",
	}
	text := failed.String()
	if !strings.Contains(text, "preflight:  failed:") {
		t.Fatalf("failed String() = %q", text)
	}
	for _, secret := range []string{
		"PASSWORD=",
		"TOKEN=",
		"API_KEY=",
		"--setenv",
		"HOME=/",
	} {
		if strings.Contains(text, secret) {
			t.Fatalf("String() leaked %q\n%s", secret, text)
		}
	}

	unsupported := ExplainReport{
		RequestedBackend:  BackendLinuxBubblewrap,
		ResolvedBackend:   BackendLinuxBubblewrap,
		FileSystemSandbox: FileSystemSandboxExternal,
		NetworkMode:       NetworkEnabled,
		PreflightStatus:   PreflightUnsupported,
		PreflightError:    "UnsupportedBackend backend=linux-bubblewrap",
	}
	if !strings.Contains(unsupported.String(), "preflight:  unsupported:") {
		t.Fatalf("unsupported String() = %q", unsupported.String())
	}
}

func TestExplainNilRuntime(t *testing.T) {
	t.Parallel()

	var rt *Runtime
	report, err := rt.Explain(context.Background())
	if err == nil || err.Error() != "nil sandbox runtime" {
		t.Fatalf("error = %v, want nil sandbox runtime", err)
	}
	if report != (ExplainReport{}) {
		t.Fatalf("report = %+v, want zero value", report)
	}
}

func TestExplainSetupFailed(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("linux bubblewrap PATH probe only")
	}
	t.Setenv("PATH", "")
	rt := NewRuntime(WithPermissionProfile(WorkspaceWriteProfile()))
	report, err := rt.Explain(context.Background())
	if report.PreflightStatus != PreflightFailed {
		t.Fatalf("preflight = %q, want %q", report.PreflightStatus, PreflightFailed)
	}
	if err == nil || !isKind(err, ErrSetupFailed) {
		t.Fatalf("error = %v, want ErrSetupFailed", err)
	}
	if report.PreflightError == "" {
		t.Fatal("PreflightError is empty")
	}
	if !strings.Contains(report.PreflightError, string(ErrSetupFailed)) {
		t.Fatalf("PreflightError missing kind: %q", report.PreflightError)
	}
	if strings.Contains(report.PreflightError, "\n") {
		t.Fatalf("PreflightError contains newline: %q", report.PreflightError)
	}
}

func TestExplainManagedPreflightHonorsCanceledContext(t *testing.T) {
	t.Parallel()

	rt := NewRuntime(WithPermissionProfile(WorkspaceWriteProfile()))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := rt.explainManagedPreflight(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestExplainReportStringZeroValues(t *testing.T) {
	t.Parallel()

	got := ExplainReport{}.String()
	for _, want := range []string{
		"backend:    auto",
		"filesystem: disabled",
		"network:    restricted",
		"preflight:  not-required",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("zero-value String() missing %q\n%s", want, got)
		}
	}
}

func TestPreflightStatusFromError(t *testing.T) {
	t.Parallel()

	if got := preflightStatusFromError(nil); got != PreflightReady {
		t.Fatalf("nil = %q, want %q", got, PreflightReady)
	}
	if got := preflightStatusFromError(backendError(ErrUnsupportedBackend, "linux-bubblewrap", errors.New("no"))); got != PreflightUnsupported {
		t.Fatalf("unsupported = %q", got)
	}
	if got := preflightStatusFromError(backendError(ErrSetupFailed, "linux-bubblewrap", errors.New("missing"))); got != PreflightFailed {
		t.Fatalf("setup failed = %q", got)
	}
}

func TestSummarizePreflightErrorTruncates(t *testing.T) {
	t.Parallel()

	err := backendError(
		ErrSetupFailed,
		string(BackendLinuxBubblewrap),
		errors.New(strings.Repeat("x", 300)+"\nsecond-line"),
	)
	got := summarizePreflightError(err)
	if strings.Contains(got, "\n") {
		t.Fatalf("summary contains newline: %q", got)
	}
	if len(got) > 210 {
		t.Fatalf("summary too long: %d", len(got))
	}
	if !strings.Contains(got, string(ErrSetupFailed)) {
		t.Fatalf("summary missing kind: %q", got)
	}
}

type probeCause struct {
	err    error
	stderr string
}

func (e probeCause) Error() string {
	return e.err.Error() + ": " + e.stderr
}

func (e probeCause) Unwrap() error {
	return e.err
}

func TestSummarizePreflightErrorOmitsProbeStderr(t *testing.T) {
	t.Parallel()

	if got := summarizePreflightError(nil); got != "" {
		t.Fatalf("nil summary = %q", got)
	}
	if got := summarizePreflightError(errors.New("plain cause")); got != "plain cause" {
		t.Fatalf("plain summary = %q", got)
	}

	got := summarizePreflightError(backendError(
		ErrSetupFailed,
		string(BackendLinuxBubblewrap),
		probeCause{
			err:    errors.New("exit status 1"),
			stderr: "bwrap: Can't mount proc on /host/secret/path: Permission denied",
		},
	))
	if strings.Contains(got, "/host/secret/path") || strings.Contains(got, "Can't mount proc") {
		t.Fatalf("summary leaked probe stderr: %q", got)
	}
	if !strings.Contains(got, string(ErrSetupFailed)) || !strings.Contains(got, "exit status 1") {
		t.Fatalf("summary = %q, want kind and unwrapped cause", got)
	}

	emptyCause := summarizePreflightError(backendError(ErrSetupFailed, string(BackendLinuxBubblewrap), nil))
	if emptyCause != string(ErrSetupFailed)+" backend="+string(BackendLinuxBubblewrap) {
		t.Fatalf("empty cause summary = %q", emptyCause)
	}
}

func assertManagedExplainStatus(t *testing.T, report ExplainReport, err error) {
	t.Helper()
	switch runtime.GOOS {
	case "linux", "darwin":
		switch report.PreflightStatus {
		case PreflightReady:
			if err != nil {
				t.Fatalf("ready status with error: %v", err)
			}
			if report.PreflightError != "" {
				t.Fatalf("ready status with PreflightError %q", report.PreflightError)
			}
		case PreflightFailed, PreflightUnsupported:
			if err == nil {
				t.Fatal("non-ready managed status requires an error")
			}
			if report.PreflightError == "" {
				t.Fatal("non-ready managed status requires PreflightError")
			}
		default:
			t.Fatalf("unexpected managed preflight status %q", report.PreflightStatus)
		}
	default:
		if report.PreflightStatus != PreflightUnsupported {
			t.Fatalf("preflight = %q, want %q", report.PreflightStatus, PreflightUnsupported)
		}
		if err == nil {
			t.Fatal("unsupported platform requires an error")
		}
	}
}
