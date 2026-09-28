//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox/controlledegress"
)

const testSetupToken = "0123456789abcdef0123456789abcdef"

func TestRunRejectsUsageAndSetupFailures(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{
			name: "invalid flag",
			args: []string{"-setup-token", testSetupToken, "-unknown"},
			want: controlledegress.ExitUsageError,
		},
		{
			name: "missing command",
			args: []string{
				"-unix", "/tmp/proxy.sock",
				"-bwrap", "/usr/bin/bwrap",
				"-seccomp-fd", "3",
				"-setup-token", testSetupToken,
			},
			want: controlledegress.ExitUsageError,
		},
		{
			name: "listen failure",
			args: []string{
				"-unix", "/tmp/proxy.sock",
				"-bwrap", "/usr/bin/bwrap",
				"-seccomp-fd", "3",
				"-setup-token", testSetupToken,
				"-port", "70000",
				"--", "/bin/true",
			},
			want: controlledegress.ExitSetupFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got := run(tt.args, strings.NewReader(""), io.Discard, &stderr, runCommand)
			if got != tt.want {
				t.Fatalf("run exit = %d, want %d stderr=%q", got, tt.want, stderr.String())
			}
			wantMarker := controlledegress.SetupErrorPrefix + testSetupToken + ":"
			if !strings.Contains(stderr.String(), wantMarker) {
				t.Fatalf("stderr = %q, want authenticated setup marker %q", stderr.String(), wantMarker)
			}
		})
	}
}

func TestRunExecutesCommandAndClosesRelay(t *testing.T) {
	unixPath := filepath.Join(t.TempDir(), "proxy.sock")
	unixListener, err := net.Listen("unix", unixPath)
	if err != nil {
		t.Fatal(err)
	}
	defer unixListener.Close()
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	called := false
	execute := func(
		args []string,
		bwrapPath string,
		seccompFD int,
		mountProc bool,
		stdin io.Reader,
		stdout, stderr io.Writer,
	) int {
		called = true
		if strings.Join(args, " ") != "/bin/echo ok" {
			t.Fatalf("command args = %#v", args)
		}
		if bwrapPath != "/trusted/bwrap" || seccompFD != 3 || !mountProc {
			t.Fatalf(
				"bwrap=%q seccompFD=%d mountProc=%v",
				bwrapPath,
				seccompFD,
				mountProc,
			)
		}
		return 0
	}
	got := run(
		[]string{
			"-unix", unixPath,
			"-bwrap", "/trusted/bwrap",
			"-seccomp-fd", "3",
			"-setup-token", testSetupToken,
			"-mount-proc=true",
			"-port", fmt.Sprint(port),
			"--", "/bin/echo", "ok",
		},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
		execute,
	)
	if got != 0 || !called {
		t.Fatalf("run exit = %d called=%v", got, called)
	}
	rebound, err := net.Listen("tcp", "127.0.0.1:"+fmt.Sprint(port))
	if err != nil {
		t.Fatalf("relay listener was not closed: %v", err)
	}
	_ = rebound.Close()
}

func TestRunCommandExitAndStartFailure(t *testing.T) {
	bwrap := writeFakeBwrap(t)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	seccomp := openTestSeccompFD(t)
	if got := runCommand(
		[]string{"/bin/sh", "-c", "printf ok; exit 75"},
		bwrap,
		int(seccomp.Fd()),
		false,
		strings.NewReader(""),
		&stdout,
		&stderr,
	); got != controlledegress.ExitSetupFailed {
		t.Fatalf("user exit = %d, want %d", got, controlledegress.ExitSetupFailed)
	}
	if stdout.String() != "ok" || stderr.String() != "" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	stderr.Reset()
	seccomp = openTestSeccompFD(t)
	if got := runCommand(
		[]string{"/definitely/missing-command"},
		bwrap,
		int(seccomp.Fd()),
		false,
		strings.NewReader(""),
		io.Discard,
		&stderr,
	); got != 1 {
		t.Fatalf("missing command exit = %d, want 1", got)
	}
	if !strings.Contains(stderr.String(), controlledegress.RunErrorPrefix) {
		t.Fatalf("stderr = %q, want run error marker", stderr.String())
	}

	seccomp = openTestSeccompFD(t)
	if got := runCommand(
		[]string{"/bin/true"},
		bwrap,
		int(seccomp.Fd()),
		false,
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	); got != 0 {
		t.Fatalf("successful command exit = %d, want 0", got)
	}
}

func writeFakeBwrap(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bwrap")
	script := `#!/bin/sh
while [ "$#" -gt 0 ] && [ "$1" != "--" ]; do shift; done
[ "$#" -gt 0 ] && shift
exec "$@"
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func openTestSeccompFD(t *testing.T) *os.File {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "seccomp")
	if err != nil {
		t.Fatal(err)
	}
	return file
}
