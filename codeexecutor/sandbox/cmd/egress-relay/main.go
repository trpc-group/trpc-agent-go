//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

// Command egress-relay runs an in-sandbox loopback→UDS bridge then runs a
// user command. It is the Claude Code socat equivalent for controlled egress.
// The relay must stay alive for the duration of the command, so this helper
// does not exec-overwrite itself.
//
// Exit codes:
//   - 2  usage error
//   - 75 relay setup failure (listen/start)
//   - other: user command exit code
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox/controlledegress"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, runCommand))
}

type commandRunner func(args []string, stdin io.Reader, stdout, stderr io.Writer) int

func run(
	args []string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
	execute commandRunner,
) int {
	flags := flag.NewFlagSet("egress-relay", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	unixPath := flags.String(
		"unix",
		"",
		"host controlled egress proxy unix socket",
	)
	port := flags.Int(
		"port",
		controlledegress.DefaultRelayPort,
		"loopback listen port",
	)
	if err := flags.Parse(args); err != nil {
		fmt.Fprintf(
			stderr,
			"%s %v\n",
			controlledegress.SetupErrorPrefix,
			err,
		)
		return controlledegress.ExitUsageError
	}
	command := flags.Args()
	if *unixPath == "" || len(command) == 0 {
		fmt.Fprintf(
			stderr,
			"%s usage: egress-relay -unix SOCK [-port PORT] -- COMMAND [ARGS...]\n",
			controlledegress.SetupErrorPrefix,
		)
		return controlledegress.ExitUsageError
	}
	relay, err := controlledegress.StartRelay(*port, *unixPath)
	if err != nil {
		fmt.Fprintf(
			stderr,
			"%s %v\n",
			controlledegress.SetupErrorPrefix,
			err,
		)
		return controlledegress.ExitSetupFailed
	}
	defer func() { _ = relay.Close() }()
	return execute(command, stdin, stdout, stderr)
}

func runCommand(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = stdin
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	cmd.Env = os.Environ()
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			fmt.Fprintf(
				stderr,
				"%s %d\n",
				controlledegress.UserExitPrefix,
				exitErr.ExitCode(),
			)
			return exitErr.ExitCode()
		}
		fmt.Fprintf(
			stderr,
			"%s %v\n",
			controlledegress.RunErrorPrefix,
			err,
		)
		return 1
	}
	return 0
}
