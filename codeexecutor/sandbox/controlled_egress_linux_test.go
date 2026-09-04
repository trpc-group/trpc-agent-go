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
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox/controlledegress"
)

func TestLinuxControlledDefersSeccompAndWrapsRelay(t *testing.T) {
	sockDir := t.TempDir()
	sock := filepath.Join(sockDir, "proxy.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	relayDir := t.TempDir()
	relayPath := filepath.Join(relayDir, "egress-relay")
	if err := os.WriteFile(relayPath, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	profile := WorkspaceWriteProfile().WithControlledEgressProxy(ControlledEgressProxy{
		UnixPath:  sock,
		RelayPort: 17923,
	})
	rt := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
		WithControlledEgressRelayPath(relayPath),
	)
	rt.bwrapPath = "/trusted/bwrap"
	ws, err := rt.CreateWorkspace(context.Background(), "controlled", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	setup, err := rt.linuxSandboxSetup(
		profile,
		ws,
		filepath.Join(ws.Path, codeexecutor.DirWork),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArg(setup.args, "--unshare-net") {
		t.Fatalf("controlled args missing network namespace isolation: %#v", setup.args)
	}
	if hasArgPair(setup.args, "--seccomp", "3") {
		t.Fatalf("controlled outer bwrap applied workload seccomp too early: %#v", setup.args)
	}
	if !setup.needsSeccompFD {
		t.Fatal("controlled setup did not inherit workload seccomp filter")
	}
	if !hasArgPair(setup.args, "-unix", sock) ||
		!hasArgPair(setup.args, "-bwrap", "/trusted/bwrap") ||
		!hasArgPair(setup.args, "-seccomp-fd", "3") ||
		!hasArg(setup.args, "-mount-proc=true") {
		t.Fatalf("controlled args missing direct relay/workload isolation: %#v", setup.args)
	}
	if token := controlledEgressSetupToken(setup.args); len(token) != 32 {
		t.Fatalf("controlled args missing authenticated setup token: %#v", setup.args)
	}
	if indexOf(setup.args, relayPath) < 0 || indexOf(setup.args, "/bin/true") < 0 {
		t.Fatalf("controlled args missing relay wrap: %#v", setup.args)
	}
	withoutProc, err := rt.linuxSandboxSetup(
		profile,
		ws,
		filepath.Join(ws.Path, codeexecutor.DirWork),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !hasArg(withoutProc.args, "-mount-proc=false") {
		t.Fatalf("no-proc controlled args missing fallback: %#v", withoutProc.args)
	}
}

func TestLinuxControlledRejectsWritableUnixPath(t *testing.T) {
	rt := NewRuntime(WithWorkspaceRoot(t.TempDir()))
	ws, err := rt.CreateWorkspace(context.Background(), "controlled-deny", codeexecutor.WorkspacePolicy{})
	if err != nil {
		t.Fatal(err)
	}
	profile := WorkspaceWriteProfile().WithControlledEgressProxy(ControlledEgressProxy{
		UnixPath: filepath.Join(ws.Path, "proxy.sock"),
	})
	_, err = rt.linuxSandboxSetup(
		profile,
		ws,
		filepath.Join(ws.Path, codeexecutor.DirWork),
		nil,
		codeexecutor.RunProgramSpec{Cmd: "/bin/true"},
		true,
	)
	if err == nil || !strings.Contains(err.Error(), "guest-writable") {
		t.Fatalf("err = %v, want writable mount rejection", err)
	}
}

func TestLinuxControlledDirectRelayEndToEnd(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping controlled-egress end-to-end test in short mode")
	}
	goPath, err := exec.LookPath("go")
	if err != nil {
		t.Skipf("go tool unavailable: %v", err)
	}
	relayPath := filepath.Join(t.TempDir(), "egress-relay")
	build := exec.Command(goPath, "build", "-o", relayPath, "./cmd/egress-relay")
	build.Dir = "."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build egress-relay: %v\n%s", err, output)
	}

	socketPath := filepath.Join(t.TempDir(), "proxy.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	echoDone := make(chan error, 1)
	go func() {
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				echoDone <- acceptErr
				return
			}
			var request [4]byte
			_, readErr := io.ReadFull(conn, request[:])
			if readErr == io.EOF {
				_ = conn.Close()
				continue
			}
			if readErr != nil {
				_ = conn.Close()
				echoDone <- readErr
				return
			}
			if string(request[:]) != "ping" {
				_ = conn.Close()
				echoDone <- fmt.Errorf("request = %q, want ping", request[:])
				return
			}
			_, writeErr := conn.Write([]byte("pong"))
			_ = conn.Close()
			echoDone <- writeErr
			return
		}
	}()

	profile := WorkspaceWriteProfile().WithControlledEgressProxy(
		ControlledEgressProxy{UnixPath: socketPath},
	)
	readOnlyPath := filepath.Join(t.TempDir(), "read-only-probe")
	if err := os.WriteFile(readOnlyPath, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(
		WithWorkspaceRoot(t.TempDir()),
		WithPermissionProfile(profile),
		WithControlledEgressRelayPath(relayPath),
	)
	bwrap, mountProc, err := runtime.linuxPreflight(context.Background())
	if err != nil {
		t.Skipf("bubblewrap unavailable: %v", err)
	}
	if err := runtime.linuxRestrictedPreflight(
		context.Background(),
		bwrap,
		mountProc,
	); err != nil {
		t.Skipf("restricted seccomp unavailable: %v", err)
	}
	workspace, err := runtime.CreateWorkspace(
		context.Background(),
		"controlled-direct-relay",
		codeexecutor.WorkspacePolicy{},
	)
	if err != nil {
		t.Fatal(err)
	}
	testBinary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	result, err := runtime.RunProgram(
		context.Background(),
		workspace,
		codeexecutor.RunProgramSpec{
			Cmd:  testBinary,
			Args: []string{"-test.run=TestControlledEgressWorkloadHelperProcess"},
			Env: map[string]string{
				"TRPC_CONTROLLED_EGRESS_HELPER":      "1",
				"TRPC_CONTROLLED_EGRESS_SOCKET_PATH": socketPath,
				"TRPC_CONTROLLED_EGRESS_READONLY":    readOnlyPath,
				"TRPC_CONTROLLED_EGRESS_RELAY_PORT": strconv.Itoa(
					controlledegress.DefaultRelayPort,
				),
			},
			Timeout: 15 * time.Second,
		},
	)
	if err != nil {
		t.Fatalf("controlled run: %v; result=%+v", err, result)
	}
	if result.ExitCode != 0 || !strings.Contains(result.Stdout, "relay-ok") {
		t.Fatalf("controlled result=%+v", result)
	}
	if content, err := os.ReadFile(readOnlyPath); err != nil ||
		string(content) != "original" {
		t.Fatalf("outer read-only file content=%q err=%v", content, err)
	}
	select {
	case err := <-echoDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("proxy echo server did not finish")
	}
}

func TestControlledEgressWorkloadHelperProcess(t *testing.T) {
	if os.Getenv("TRPC_CONTROLLED_EGRESS_HELPER") != "1" {
		return
	}
	port := os.Getenv("TRPC_CONTROLLED_EGRESS_RELAY_PORT")
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 2*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial relay: %v\n", err)
		os.Exit(10)
	}
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		fmt.Fprintf(os.Stderr, "set relay deadline: %v\n", err)
		os.Exit(11)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		fmt.Fprintf(os.Stderr, "write relay: %v\n", err)
		os.Exit(12)
	}
	var response [4]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil {
		fmt.Fprintf(os.Stderr, "read relay: %v\n", err)
		os.Exit(13)
	}
	_ = conn.Close()
	if string(response[:]) != "pong" {
		fmt.Fprintf(os.Stderr, "relay response = %q\n", response[:])
		os.Exit(14)
	}
	unixConn, unixErr := net.DialTimeout(
		"unix",
		os.Getenv("TRPC_CONTROLLED_EGRESS_SOCKET_PATH"),
		time.Second,
	)
	if unixErr == nil {
		_ = unixConn.Close()
		fmt.Fprintln(os.Stderr, "workload unexpectedly opened AF_UNIX socket")
		os.Exit(15)
	}
	if !errors.Is(unixErr, syscall.EPERM) {
		fmt.Fprintf(os.Stderr, "AF_UNIX error = %v, want EPERM\n", unixErr)
		os.Exit(16)
	}
	procEntries, err := os.ReadDir("/proc")
	if err != nil {
		fmt.Fprintf(os.Stderr, "read /proc: %v\n", err)
		os.Exit(17)
	}
	for _, entry := range procEntries {
		if !entry.IsDir() {
			continue
		}
		if _, err := strconv.Atoi(entry.Name()); err != nil {
			continue
		}
		cmdline, _ := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if strings.Contains(string(cmdline), "egress-relay") {
			fmt.Fprintln(os.Stderr, "workload can see trusted relay process")
			os.Exit(18)
		}
	}
	if err := os.WriteFile(
		os.Getenv("TRPC_CONTROLLED_EGRESS_READONLY"),
		[]byte("changed"),
		0o600,
	); err == nil {
		fmt.Fprintln(os.Stderr, "nested sandbox reopened outer read-only mount")
		os.Exit(19)
	}
	fmt.Fprintln(os.Stdout, "relay-ok")
	os.Exit(0)
}

func indexOf(args []string, want string) int {
	for i, arg := range args {
		if arg == want {
			return i
		}
	}
	return -1
}
