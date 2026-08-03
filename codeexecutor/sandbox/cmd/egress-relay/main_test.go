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
	"io"
	"net"
	"strconv"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox/controlledegress"
)

func TestRunRejectsUsageAndSetupFailures(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{
			name: "invalid flag",
			args: []string{"-unknown"},
			want: controlledegress.ExitUsageError,
		},
		{
			name: "missing command",
			args: []string{"-unix", "/tmp/proxy.sock"},
			want: controlledegress.ExitUsageError,
		},
		{
			name: "listen failure",
			args: []string{"-unix", "/tmp/proxy.sock", "-port", "70000", "--", "/bin/true"},
			want: controlledegress.ExitSetupFailed,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr bytes.Buffer
			got := run(tt.args, strings.NewReader(""), io.Discard, &stderr, runCommand)
			if got != tt.want {
				t.Fatalf("run exit = %d, want %d", got, tt.want)
			}
			if !strings.Contains(stderr.String(), controlledegress.SetupErrorPrefix) {
				t.Fatalf("stderr = %q, want setup marker", stderr.String())
			}
		})
	}
}

func TestRunExecutesCommandAndClosesRelay(t *testing.T) {
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := probe.Addr().(*net.TCPAddr).Port
	_ = probe.Close()

	called := false
	execute := func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		called = true
		if strings.Join(args, " ") != "/bin/echo ok" {
			t.Fatalf("command args = %#v", args)
		}
		return 0
	}
	got := run(
		[]string{"-unix", "/tmp/proxy.sock", "-port", strconv.Itoa(port), "--", "/bin/echo", "ok"},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
		execute,
	)
	if got != 0 || !called {
		t.Fatalf("run exit = %d called=%v", got, called)
	}
	rebound, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		t.Fatalf("relay listener was not closed: %v", err)
	}
	_ = rebound.Close()
}

func TestRunCommandExitAndStartFailure(t *testing.T) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if got := runCommand(
		[]string{"/bin/sh", "-c", "printf ok; exit 75"},
		strings.NewReader(""),
		&stdout,
		&stderr,
	); got != controlledegress.ExitSetupFailed {
		t.Fatalf("user exit = %d, want %d", got, controlledegress.ExitSetupFailed)
	}
	if stdout.String() != "ok" ||
		!strings.Contains(stderr.String(), controlledegress.UserExitPrefix+" 75") {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}

	stderr.Reset()
	if got := runCommand(
		[]string{"/definitely/missing-command"},
		strings.NewReader(""),
		io.Discard,
		&stderr,
	); got != 1 {
		t.Fatalf("missing command exit = %d, want 1", got)
	}
	if !strings.Contains(stderr.String(), controlledegress.RunErrorPrefix) {
		t.Fatalf("stderr = %q, want run error marker", stderr.String())
	}

	if got := runCommand(
		[]string{"/bin/true"},
		strings.NewReader(""),
		io.Discard,
		io.Discard,
	); got != 0 {
		t.Fatalf("successful command exit = %d, want 0", got)
	}
}
