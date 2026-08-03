//go:build linux

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
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox/controlledegress"
)

func buildEgressRelay(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "egress-relay")
	modRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(
		"go",
		"build",
		"-o",
		out,
		"./codeexecutor/sandbox/cmd/egress-relay",
	)
	cmd.Dir = modRoot
	outb, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build egress-relay: %v\n%s", err, outb)
	}
	return out
}

func TestLinuxControlledEgressE2E(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not available")
	}
	relayBin := buildEgressRelay(t)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "UPSTREAM_OK")
	}))
	t.Cleanup(upstream.Close)

	sock := filepath.Join(t.TempDir(), "proxy.sock")
	policy := controlledegress.StaticAllowlist("example.com")
	policy.Resolver = stubIPResolver{ip: net.ParseIP("1.1.1.1")}
	proxy, err := controlledegress.StartTestProxy(sock, policy, func(ctx context.Context, network, address string) (net.Conn, error) {
		return net.Dial("tcp", upstream.Listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	profile := WorkspaceWriteProfile().WithControlledEgressProxy(controlledegressProxy(sock))
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
		WithControlledEgressRelayPath(relayBin),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "controlled-e2e", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}

	// Probe via python urllib with HTTP_PROXY (injected by sandbox).
	py := `import os, urllib.request
print("PROXY", os.environ.get("HTTP_PROXY"))
print(urllib.request.urlopen("http://example.com/hello", timeout=5).read().decode())
`
	result, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "/usr/bin/python3",
		Args: []string{"-c", py},
	})
	if err != nil {
		t.Fatalf("run: %v stderr=%s", err, result.Stderr)
	}
	if result.ExitCode != 0 {
		t.Fatalf("exit=%d stdout=%q stderr=%q", result.ExitCode, result.Stdout, result.Stderr)
	}
	if !strings.Contains(result.Stdout, "UPSTREAM_OK") {
		t.Fatalf("stdout=%q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "http://127.0.0.1:") {
		t.Fatalf("expected HTTP_PROXY injection, stdout=%q", result.Stdout)
	}

	// Direct IP must fail under unshare-net.
	direct := `import socket
s=socket.socket(); s.settimeout(2)
try:
  s.connect(("1.1.1.1", 80)); print("UNEXPECTED_OK")
except Exception as e:
  print("DIRECT_DENIED", type(e).__name__)
`
	result, err = rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "/usr/bin/python3",
		Args: []string{"-c", direct},
	})
	if err != nil {
		t.Fatalf("direct run: %v", err)
	}
	if strings.Contains(result.Stdout, "UNEXPECTED_OK") {
		t.Fatalf("direct connect succeeded: %q", result.Stdout)
	}
	if !strings.Contains(result.Stdout, "DIRECT_DENIED") {
		t.Fatalf("stdout=%q stderr=%q", result.Stdout, result.Stderr)
	}
}

func TestLinuxControlledRejectsProxySocketInWritableWorkspace(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(
		context.Background(),
		"controlled-writable-socket",
		codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(ws.Path, "proxy.sock")
	profile := WorkspaceWriteProfile().
		WithControlledEgressProxy(controlledegressProxy(sock))
	_, err = rt.linuxSandboxSetup(
		profile,
		ws,
		filepath.Join(ws.Path, codeexecutor.DirWork),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if !isKind(err, ErrPolicyViolation) ||
		!strings.Contains(err.Error(), "outside guest-writable mounts") {
		t.Fatalf("error = %v, want writable UnixPath policy violation", err)
	}
}

func TestLinuxControlledEgressDeny(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not available")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	relayBin := buildEgressRelay(t)
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	policy := controlledegress.StaticAllowlist("only-good.example")
	policy.Resolver = stubIPResolver{ip: net.ParseIP("1.1.1.1")}
	proxy, err := controlledegress.StartTestProxy(sock, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithControlledEgressProxy(controlledegressProxy(sock))),
		WithControlledEgressRelayPath(relayBin),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "controlled-deny", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	py := `import urllib.request
try:
  urllib.request.urlopen("http://evil.example/", timeout=5)
  print("UNEXPECTED_OK")
except Exception as e:
  print("DENIED", type(e).__name__)
`
	result, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd:  "/usr/bin/python3",
		Args: []string{"-c", py},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(result.Stdout, "UNEXPECTED_OK") {
		t.Fatalf("deny failed: %q", result.Stdout)
	}
}

func TestLinuxControlledArgsKeepUnshareNetAndWrapRelay(t *testing.T) {
	relayBin := buildEgressRelay(t)
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	proxy, err := controlledegress.StartTestProxy(sock, controlledegress.StaticAllowlist("example.com"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithControlledEgressRelayPath(relayBin),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "controlled-args", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithControlledEgressProxy(controlledegressProxy(sock))
	args, err := rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		[]string{"HTTP_PROXY=http://1.1.1.1:80", "FOO=bar"},
		codeexecutor.RunProgramSpec{Cmd: "/bin/echo", Args: []string{"hi"}},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArg(args, "--unshare-net") {
		t.Fatalf("missing --unshare-net: %#v", args)
	}
	if !hasArg(args, relayBin) {
		t.Fatalf("missing relay binary in args: %#v", args)
	}
	if !hasArgPair(args, "--setenv", "HTTP_PROXY") || !hasArg(args, "http://127.0.0.1:17923") {
		t.Fatalf("missing injected HTTP_PROXY: %#v", args)
	}
	if hasArg(args, "http://1.1.1.1:80") {
		t.Fatalf("guest HTTP_PROXY not overwritten: %#v", args)
	}
}

func TestControlledProxyProbeFailsClosed(t *testing.T) {
	relayBin := buildEgressRelay(t)
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithControlledEgressRelayPath(relayBin),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "probe-fail", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(t.TempDir(), "missing.sock")
	profile := WorkspaceWriteProfile().WithControlledEgressProxy(controlledegressProxy(missing))
	_, err = rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if !isKind(err, ErrSetupFailed) {
		t.Fatalf("error = %v, want ErrSetupFailed", err)
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Fatalf("error = %v, want unreachable probe message", err)
	}
}

func TestControlledMissingRelayFailsClosed(t *testing.T) {
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	proxy, err := controlledegress.StartTestProxy(sock, controlledegress.StaticAllowlist("example.com"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "no-relay", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithControlledEgressProxy(controlledegressProxy(sock))
	_, err = rt.linuxSandboxArgs(
		profile,
		ws,
		filepath.Join(ws.Path, "work"),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if !isKind(err, ErrSetupFailed) {
		t.Fatalf("error = %v, want ErrSetupFailed", err)
	}
	if !strings.Contains(err.Error(), "egress-relay") {
		t.Fatalf("error = %v, want missing relay message", err)
	}
}

func TestLinuxControlledDenyIsNotSetupFailed(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not available")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	relayBin := buildEgressRelay(t)
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	policy := controlledegress.StaticAllowlist("only-good.example")
	policy.Resolver = stubIPResolver{ip: net.ParseIP("1.1.1.1")}
	proxy, err := controlledegress.StartTestProxy(sock, policy)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })

	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithControlledEgressProxy(controlledegressProxy(sock))),
		WithControlledEgressRelayPath(relayBin),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "deny-not-setup", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
		Cmd: "/usr/bin/python3",
		Args: []string{"-c", `import urllib.request
try:
  urllib.request.urlopen("http://evil.example/", timeout=5)
except Exception:
  pass
`},
	})
	if isKind(err, ErrSetupFailed) {
		t.Fatalf("policy deny must not be ErrSetupFailed: %v", err)
	}
	_ = result
}

func TestLinuxControlledUserExitCodesArePreserved(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not available")
	}
	relayBin := buildEgressRelay(t)
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	proxy, err := controlledegress.StartTestProxy(
		sock,
		controlledegress.StaticAllowlist("example.com"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithControlledEgressProxy(controlledegressProxy(sock))),
		WithControlledEgressRelayPath(relayBin),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "user-exit-codes", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	for _, exitCode := range []int{
		controlledegress.ExitUsageError,
		controlledegress.ExitSetupFailed,
	} {
		result, runErr := rt.RunProgram(context.Background(), ws, codeexecutor.RunProgramSpec{
			Cmd: "/bin/sh",
			Args: []string{
				"-c",
				"printf '%s guest spoof\\n' '" + controlledegress.SetupErrorPrefix +
					"' >&2; exit " + fmt.Sprint(exitCode),
			},
		})
		if runErr != nil {
			t.Fatalf("user exit %d returned typed error: %v", exitCode, runErr)
		}
		if result.ExitCode != exitCode {
			t.Fatalf("user exit = %d, want %d", result.ExitCode, exitCode)
		}
	}
}

func TestLinuxControlledStartProcessMapsRelaySetupFailure(t *testing.T) {
	if _, err := exec.LookPath("bwrap"); err != nil {
		t.Skip("bwrap not available")
	}
	fakeRelay := filepath.Join(t.TempDir(), "egress-relay")
	script := "#!/bin/sh\nprintf '%s forced failure\\n' '" +
		controlledegress.SetupErrorPrefix + "' >&2\nexit 75\n"
	if err := os.WriteFile(fakeRelay, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	sock := filepath.Join(t.TempDir(), "proxy.sock")
	proxy, err := controlledegress.StartTestProxy(
		sock,
		controlledegress.StaticAllowlist("example.com"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = proxy.Close() })
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(WorkspaceWriteProfile().WithControlledEgressProxy(controlledegressProxy(sock))),
		WithControlledEgressRelayPath(fakeRelay),
	)
	ws, err := rt.CreateWorkspace(context.Background(), "process-setup-failure", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	process, err := rt.StartProcess(context.Background(), ws, ProcessSpec{Cmd: "/bin/true"})
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Wait(); !isKind(err, ErrSetupFailed) {
		t.Fatalf("Wait error = %v, want ErrSetupFailed", err)
	}
}

func controlledegressProxy(sock string) ControlledEgressProxy {
	return ControlledEgressProxy{UnixPath: sock}
}

type stubIPResolver struct{ ip net.IP }

func (s stubIPResolver) LookupIPAddr(ctx context.Context, host string) ([]net.IPAddr, error) {
	return []net.IPAddr{{IP: s.ip}}, nil
}
