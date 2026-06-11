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
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/codeexecutor/sandbox"
)

const (
	shellEnvDefaultAllMarker  = "SHELL_ENVIRONMENT_POLICY_DEFAULT_ALL_OK"
	shellEnvCoreMarker        = "SHELL_ENVIRONMENT_POLICY_CORE_OK"
	shellEnvNoneSetMarker     = "SHELL_ENVIRONMENT_POLICY_NONE_SET_OK"
	shellEnvIncludeOnlyMarker = "SHELL_ENVIRONMENT_POLICY_INCLUDE_ONLY_OK"
	shellEnvExcludeSetMarker  = "SHELL_ENVIRONMENT_POLICY_EXCLUDE_SET_OK"
	shellEnvAgentMarker       = "SHELL_ENVIRONMENT_POLICY_AGENT_OK"
)

func runShellEnvironmentPolicyDefaultAll(ctx context.Context, cfg config) error {
	restore, err := setScenarioEnv("TRPC_SANDBOX_SHELL_ENV_DEFAULT_ALL", "visible")
	if err != nil {
		return err
	}
	defer restore()
	return runShellEnvironmentPolicyCommand(ctx, cfg, "shell-environment-policy-default-all", `
if [ "$TRPC_SANDBOX_SHELL_ENV_DEFAULT_ALL" = "visible" ]; then
  echo SHELL_ENVIRONMENT_POLICY_DEFAULT_ALL_OK
else
  echo SHELL_ENVIRONMENT_POLICY_DEFAULT_ALL_FAIL
  exit 1
fi
`, shellEnvDefaultAllMarker, nil)
}

func runShellEnvironmentPolicyCore(ctx context.Context, cfg config) error {
	restore, err := setScenarioEnv("TRPC_SANDBOX_SHELL_ENV_CORE_HIDDEN", "hidden")
	if err != nil {
		return err
	}
	defer restore()
	return runShellEnvironmentPolicyCommand(ctx, cfg, "shell-environment-policy-core", `
if [ -n "$PATH" ] && [ -z "${TRPC_SANDBOX_SHELL_ENV_CORE_HIDDEN:-}" ]; then
  echo SHELL_ENVIRONMENT_POLICY_CORE_OK
else
  echo SHELL_ENVIRONMENT_POLICY_CORE_FAIL
  exit 1
fi
`, shellEnvCoreMarker, nil, sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
		Inherit: sandbox.ShellEnvironmentPolicyInheritCore,
	}))
}

func runShellEnvironmentPolicyNoneSet(ctx context.Context, cfg config) error {
	restore, err := setScenarioEnv("TRPC_SANDBOX_SHELL_ENV_NONE_HIDDEN", "hidden")
	if err != nil {
		return err
	}
	defer restore()
	return runShellEnvironmentPolicyCommand(ctx, cfg, "shell-environment-policy-none-set", `
if [ "$TRPC_SANDBOX_SHELL_ENV_NONE_SET" = "set" ] &&
   [ -z "${TRPC_SANDBOX_SHELL_ENV_NONE_HIDDEN:-}" ] &&
   [ -n "$HOME" ] &&
   [ -n "$WORK_DIR" ] &&
   [ -n "$PATH" ]; then
  echo SHELL_ENVIRONMENT_POLICY_NONE_SET_OK
else
  echo SHELL_ENVIRONMENT_POLICY_NONE_SET_FAIL
  exit 1
fi
`, shellEnvNoneSetMarker, nil, sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
		Inherit: sandbox.ShellEnvironmentPolicyInheritNone,
		Set:     map[string]string{"TRPC_SANDBOX_SHELL_ENV_NONE_SET": "set"},
	}))
}

func runShellEnvironmentPolicyIncludeOnly(ctx context.Context, cfg config) error {
	restoreAllowed, err := setScenarioEnv("TRPC_SANDBOX_SHELL_ENV_INCLUDE_ALLOWED", "host")
	if err != nil {
		return err
	}
	defer restoreAllowed()
	restoreBlocked, err := setScenarioEnv("TRPC_SANDBOX_SHELL_ENV_INCLUDE_BLOCKED", "host")
	if err != nil {
		return err
	}
	defer restoreBlocked()
	return runShellEnvironmentPolicyCommand(ctx, cfg, "shell-environment-policy-include-only", `
if [ "$TRPC_SANDBOX_SHELL_ENV_INCLUDE_ALLOWED" = "host" ] &&
   [ "$TRPC_SANDBOX_SHELL_ENV_INCLUDE_SET" = "set" ] &&
   [ "$TRPC_SANDBOX_SHELL_ENV_INCLUDE_RUN" = "run" ] &&
   [ -z "${TRPC_SANDBOX_SHELL_ENV_INCLUDE_BLOCKED:-}" ] &&
   [ -z "${TRPC_SANDBOX_SHELL_ENV_INCLUDE_SET_FILTERED:-}" ] &&
   [ -z "${TRPC_SANDBOX_SHELL_ENV_INCLUDE_RUN_FILTERED:-}" ] &&
   [ -n "$HOME" ] &&
   [ -n "$WORK_DIR" ]; then
  echo SHELL_ENVIRONMENT_POLICY_INCLUDE_ONLY_OK
else
  echo SHELL_ENVIRONMENT_POLICY_INCLUDE_ONLY_FAIL
  exit 1
fi
`, shellEnvIncludeOnlyMarker, map[string]string{
		"TRPC_SANDBOX_SHELL_ENV_INCLUDE_RUN":          "run",
		"TRPC_SANDBOX_SHELL_ENV_INCLUDE_RUN_FILTERED": "filtered",
	}, sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
		Inherit:     sandbox.ShellEnvironmentPolicyInheritAll,
		IncludeOnly: []string{"TRPC_SANDBOX_SHELL_ENV_INCLUDE_ALLOWED", "TRPC_SANDBOX_SHELL_ENV_INCLUDE_SET", "TRPC_SANDBOX_SHELL_ENV_INCLUDE_RUN"},
		Set: map[string]string{
			"TRPC_SANDBOX_SHELL_ENV_INCLUDE_SET":          "set",
			"TRPC_SANDBOX_SHELL_ENV_INCLUDE_SET_FILTERED": "filtered",
		},
	}))
}

func runShellEnvironmentPolicyExcludeSet(ctx context.Context, cfg config) error {
	restore, err := setScenarioEnv("TRPC_SANDBOX_SHELL_ENV_EXCLUDE_SET", "host")
	if err != nil {
		return err
	}
	defer restore()
	return runShellEnvironmentPolicyCommand(ctx, cfg, "shell-environment-policy-exclude-set", `
if [ "$TRPC_SANDBOX_SHELL_ENV_EXCLUDE_SET" = "set" ]; then
  echo SHELL_ENVIRONMENT_POLICY_EXCLUDE_SET_OK
else
  echo SHELL_ENVIRONMENT_POLICY_EXCLUDE_SET_FAIL
  exit 1
fi
`, shellEnvExcludeSetMarker, nil, sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
		Inherit: sandbox.ShellEnvironmentPolicyInheritAll,
		Exclude: []string{"TRPC_SANDBOX_SHELL_ENV_EXCLUDE_SET"},
		Set:     map[string]string{"TRPC_SANDBOX_SHELL_ENV_EXCLUDE_SET": "set"},
	}))
}

func runShellEnvironmentPolicyAgent(ctx context.Context, cfg config) error {
	restoreAllowed, err := setScenarioEnv("TRPC_SANDBOX_SHELL_ENV_AGENT_ALLOWED", "host")
	if err != nil {
		return err
	}
	defer restoreAllowed()
	restoreBlocked, err := setScenarioEnv("TRPC_SANDBOX_SHELL_ENV_AGENT_BLOCKED", "host")
	if err != nil {
		return err
	}
	defer restoreBlocked()
	h, err := newAgentToolHarness(
		ctx,
		cfg,
		sandbox.WorkspaceWriteProfile(),
		nil,
		withAgentToolSandboxOptions(sandbox.WithShellEnvironmentPolicy(sandbox.ShellEnvironmentPolicy{
			Inherit:     sandbox.ShellEnvironmentPolicyInheritAll,
			IncludeOnly: []string{"TRPC_SANDBOX_SHELL_ENV_AGENT_ALLOWED", "TRPC_SANDBOX_SHELL_ENV_AGENT_SET"},
			Set:         map[string]string{"TRPC_SANDBOX_SHELL_ENV_AGENT_SET": "set"},
		})),
	)
	if err != nil {
		return err
	}
	defer h.runner.Close()
	defer h.printToolTrace()
	final, err := h.runTurn(ctx, "shell-environment-policy-agent", `Use workspace_exec to verify shell environment policy behavior.

Run a shell command equivalent to:
if [ "$TRPC_SANDBOX_SHELL_ENV_AGENT_ALLOWED" = "host" ] &&
   [ "$TRPC_SANDBOX_SHELL_ENV_AGENT_SET" = "set" ] &&
   [ -z "${TRPC_SANDBOX_SHELL_ENV_AGENT_BLOCKED:-}" ] &&
   [ -z "${OPENAI_API_KEY:-}" ] &&
   [ -n "$HOME" ] &&
   [ -n "$WORK_DIR" ]; then
  echo SHELL_ENVIRONMENT_POLICY_AGENT_OK
else
  echo SHELL_ENVIRONMENT_POLICY_AGENT_FAIL
  exit 1
fi

After the tool result, answer concisely and include SHELL_ENVIRONMENT_POLICY_AGENT_OK. Do not print environment variable values.`)
	if err != nil {
		return err
	}
	if err := h.requireWorkspaceExecCalls(1); err != nil {
		return err
	}
	if err := expectContains(final, shellEnvAgentMarker); err != nil {
		return err
	}
	fmt.Println(redact(final))
	return nil
}

func runShellEnvironmentPolicyCommand(
	ctx context.Context,
	cfg config,
	sessionID string,
	script string,
	marker string,
	env map[string]string,
	opts ...sandbox.Option,
) error {
	rt := newRuntime(cfg, sandbox.WorkspaceWriteProfile(), 1<<20, 3*time.Second, opts...)
	if err := requireManagedSandbox(ctx, rt, cfg); err != nil {
		return err
	}
	ws, err := rt.CreateWorkspace(ctx, sessionID, codeexecutor.WorkspacePolicy{})
	if err != nil {
		return err
	}
	res, err := rt.RunProgram(ctx, ws, codeexecutor.RunProgramSpec{
		Cmd:  "bash",
		Args: []string{"-c", script},
		Env:  env,
		Cwd:  codeexecutor.DirWork,
	})
	if err != nil {
		return err
	}
	if res.ExitCode != 0 {
		return fmt.Errorf(
			"shell environment policy check failed: stdout=%q stderr=%q",
			redact(res.Stdout),
			redact(res.Stderr),
		)
	}
	return expectContains(res.Stdout, marker)
}

func setScenarioEnv(key, value string) (func(), error) {
	oldValue, hadValue := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		return nil, err
	}
	return func() {
		var err error
		if hadValue {
			err = os.Setenv(key, oldValue)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil && !errors.Is(err, os.ErrInvalid) {
			fmt.Fprintf(os.Stderr, "restore %s: %v\n", key, err)
		}
	}, nil
}
