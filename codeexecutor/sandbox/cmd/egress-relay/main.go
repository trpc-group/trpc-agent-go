//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command egress-relay starts a trusted in-sandbox loopback→Unix bridge, then
// runs the user command in a nested bubblewrap process with seccomp applied.
// The relay starts before seccomp and remains outside the workload PID
// namespace.
//
// Exit codes:
//   - 2  usage error
//   - 75 relay setup failure (listen/start)
//   - other: user command exit code
package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox/controlledegress"
)

const setupTokenBytes = 16

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, runCommand))
}

type workloadRunner func(
	args []string,
	bwrapPath string,
	seccompFD int,
	mountProc bool,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int

func run(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	execute workloadRunner,
) int {
	flags := flag.NewFlagSet("egress-relay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	unixPath := flags.String(
		"unix",
		"",
		"host controlled-egress proxy Unix socket",
	)
	bwrapPath := flags.String(
		"bwrap",
		"",
		"trusted absolute bubblewrap path",
	)
	seccompFD := flags.Int(
		"seccomp-fd",
		-1,
		"inherited workload seccomp descriptor",
	)
	setupToken := flags.String(
		"setup-token",
		"",
		"host-generated relay setup authentication token",
	)
	mountProc := flags.Bool(
		"mount-proc",
		false,
		"mount a fresh procfs in the workload PID namespace",
	)
	port := flags.Int(
		"port",
		controlledegress.DefaultRelayPort,
		"loopback listen port",
	)
	if err := flags.Parse(args); err != nil {
		writeSetupError(stderr, *setupToken, "%v", err)
		return controlledegress.ExitUsageError
	}
	command := flags.Args()
	if *unixPath == "" || *bwrapPath == "" || *seccompFD < 3 ||
		!validSetupToken(*setupToken) || len(command) == 0 {
		writeSetupError(
			stderr,
			*setupToken,
			"usage: egress-relay -unix PATH -bwrap PATH "+
				"-seccomp-fd FD -setup-token TOKEN "+
				"[-port PORT] -- COMMAND [ARGS...]",
		)
		return controlledegress.ExitUsageError
	}
	probe, err := net.DialTimeout("unix", *unixPath, time.Second)
	if err != nil {
		writeSetupError(
			stderr,
			*setupToken,
			"connect proxy Unix socket: %v",
			err,
		)
		return controlledegress.ExitSetupFailed
	}
	_ = probe.Close()
	relay, err := controlledegress.StartRelay(*port, *unixPath)
	if err != nil {
		writeSetupError(stderr, *setupToken, "%v", err)
		return controlledegress.ExitSetupFailed
	}
	defer func() { _ = relay.Close() }()
	return execute(
		command,
		*bwrapPath,
		*seccompFD,
		*mountProc,
		stdin,
		stdout,
		stderr,
	)
}

func validSetupToken(token string) bool {
	decoded, err := hex.DecodeString(token)
	return err == nil && len(decoded) == setupTokenBytes
}

func writeSetupError(
	stderr io.Writer,
	setupToken string,
	format string,
	args ...any,
) {
	prefix := controlledegress.SetupErrorPrefix
	if validSetupToken(setupToken) {
		prefix += setupToken + ":"
	}
	fmt.Fprintf(stderr, "%s %s\n", prefix, fmt.Sprintf(format, args...))
}

func runCommand(
	args []string,
	bwrapPath string,
	seccompFD int,
	mountProc bool,
	stdin io.Reader,
	stdout, stderr io.Writer,
) int {
	if len(args) == 0 || bwrapPath == "" || seccompFD < 3 {
		fmt.Fprintf(
			stderr,
			"%s invalid workload launcher arguments\n",
			controlledegress.RunErrorPrefix,
		)
		return 1
	}
	if _, err := exec.LookPath(args[0]); err != nil {
		fmt.Fprintf(stderr, "%s %v\n", controlledegress.RunErrorPrefix, err)
		return 1
	}
	seccompFile := os.NewFile(uintptr(seccompFD), "controlled-egress-seccomp")
	if seccompFile == nil {
		fmt.Fprintf(
			stderr,
			"%s invalid seccomp descriptor %d\n",
			controlledegress.RunErrorPrefix,
			seccompFD,
		)
		return 1
	}
	defer seccompFile.Close()
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(
			stderr,
			"%s get working directory: %v\n",
			controlledegress.RunErrorPrefix,
			err,
		)
		return 1
	}
	bwrapArgs := []string{
		"--die-with-parent",
		"--unshare-pid",
		"--new-session",
		"--cap-drop", "ALL",
		"--bind", "/", "/",
		"--seccomp", "3",
		"--chdir", cwd,
	}
	if mountProc {
		bwrapArgs = append(bwrapArgs, "--proc", "/proc")
	}
	bwrapArgs = append(bwrapArgs, "--")
	bwrapArgs = append(bwrapArgs, args...)
	cmd := exec.Command(bwrapPath, bwrapArgs...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	cmd.ExtraFiles = []*os.File{seccompFile}
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		fmt.Fprintf(stderr, "%s %v\n", controlledegress.RunErrorPrefix, err)
		return 1
	}
	return 0
}
