//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//
//

package workspaceexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	localexec "trpc.group/trpc-go/trpc-agent-go/codeexecutor/local"
)

const policyTestTimeoutSec = 5

func TestExecTool_AllowedCommands_AllowsListedCommand(t *testing.T) {
	tl := NewExecTool(localexec.New(), WithAllowedCommands("echo"))

	args := execInput{
		Command: "echo hello",
		Timeout: policyTestTimeoutSec,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.Contains(t, out.Output, "hello")
}

func TestExecTool_AllowedCommands_RejectsUnlistedCommand(t *testing.T) {
	tl := NewExecTool(localexec.New(), WithAllowedCommands("echo"))

	args := execInput{
		Command: "ls -la",
		Timeout: policyTestTimeoutSec,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), enc)
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "ls"),
		"expected error to mention ls, got: %v", err,
	)
}

func TestExecTool_AllowedCommands_AllowsPipelineWhenEverySegmentListed(t *testing.T) {
	tl := NewExecTool(
		localexec.New(),
		WithAllowedCommands("echo", "wc"),
	)

	args := execInput{
		Command: "echo hello world | wc -w",
		Timeout: policyTestTimeoutSec,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.Contains(t, strings.TrimSpace(out.Output), "2")
}

func TestExecTool_AllowedCommands_RejectsPipelineWithUnlistedSegment(t *testing.T) {
	tl := NewExecTool(
		localexec.New(),
		WithAllowedCommands("echo", "wc"),
	)

	args := execInput{
		Command: "echo hello | curl http://example.com",
		Timeout: policyTestTimeoutSec,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), enc)
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "curl"),
		"expected curl rejection, got %v", err,
	)
}

func TestExecTool_DeniedCommands_RejectsCurl(t *testing.T) {
	tl := NewExecTool(localexec.New(), WithDeniedCommands("curl", "wget"))

	args := execInput{
		Command: "curl http://internal.example.com",
		Timeout: policyTestTimeoutSec,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	_, err = tl.Call(context.Background(), enc)
	require.Error(t, err)
	require.True(t,
		strings.Contains(err.Error(), "curl"),
		"expected curl rejection, got %v", err,
	)
}

func TestExecTool_DeniedCommands_AllowsOtherCommands(t *testing.T) {
	tl := NewExecTool(localexec.New(), WithDeniedCommands("curl"))

	args := execInput{
		Command: "echo hello",
		Timeout: policyTestTimeoutSec,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.Contains(t, out.Output, "hello")
}

func TestExecTool_PolicyRejectsBypassAttempts(t *testing.T) {
	tl := NewExecTool(localexec.New(), WithDeniedCommands("curl"))

	bypassAttempts := []string{
		`echo $(curl http://x)`,
		"echo `curl http://x`",
		"sh -c 'curl http://x'",
		"echo > /tmp/foo",
		"(curl http://x)",
		"FOO=curl $FOO http://x",
	}
	for _, cmd := range bypassAttempts {
		t.Run(cmd, func(t *testing.T) {
			args := execInput{
				Command: cmd,
				Timeout: policyTestTimeoutSec,
			}
			enc, err := json.Marshal(args)
			require.NoError(t, err)
			_, err = tl.Call(context.Background(), enc)
			require.Error(t, err,
				"command %q should be rejected by shellsafe", cmd,
			)
		})
	}
}

func TestExecTool_PolicyDescriptionMentionsRestrictions(t *testing.T) {
	tl := NewExecTool(
		localexec.New(),
		WithAllowedCommands("echo", "ls"),
	)
	decl := tl.Declaration()
	require.NotNil(t, decl)
	require.Contains(t, decl.Description, "Restricted")
	require.Contains(t, decl.Description, "Allowed commands")
	require.Contains(t, decl.Description, "blocked unconditionally")
	require.NotContains(t, decl.Description, "unless explicitly allow-listed")
	require.Contains(t, decl.Description, "echo")
}

func TestExecTool_NoPolicy_DescriptionUnchanged(t *testing.T) {
	tl := NewExecTool(localexec.New())
	decl := tl.Declaration()
	require.NotNil(t, decl)
	require.NotContains(t, decl.Description, "Restricted")
	require.NotContains(t, decl.Description, "Allowed commands")
}

// TestShellArgsForPolicy locks down the rule that the historical
// "-lc" (login shell, sources /etc/profile + $HOME/.profile) is
// only used when no policy is active. With a policy a planted
// .profile (e.g. via attacker-controlled HOME) is a passive
// bypass of any allow/deny list, so "-c" is used instead.
func TestShellArgsForPolicy(t *testing.T) {
	const cmd = "echo hi"
	require.Equal(
		t,
		[]string{"-c", cmd},
		shellArgsForPolicy(true, cmd),
		"policy active should drop -l",
	)
	require.Equal(
		t,
		[]string{"-lc", cmd},
		shellArgsForPolicy(false, cmd),
		"no policy should preserve historical -lc",
	)
}

// TestEnvForPolicy_NoPolicyPassesThrough makes sure the env scrub
// is a no-op when no command policy is configured, so callers that
// rely on HOME / LD_LIBRARY_PATH / etc. see no behaviour change.
func TestEnvForPolicy_NoPolicyPassesThrough(t *testing.T) {
	in := map[string]string{
		"HOME":       "/tmp/x",
		"LD_PRELOAD": "/tmp/evil.so",
		"PATH":       "/usr/bin",
	}
	got := envForPolicy(false, in)
	require.Equal(t, in, got)
}

// TestEnvForPolicy_StripsShellStartupVectors enumerates every
// blocklist entry and asserts it is dropped when a policy is
// active. PATH is included in the hostile set because a caller can
// repoint it at a workspace-controlled directory and ship a
// malicious "echo" / "python" / "git" that passes the policy.
func TestEnvForPolicy_StripsShellStartupVectors(t *testing.T) {
	hostile := []string{
		"HOME", "ENV", "BASH_ENV", "PROMPT_COMMAND", "PS4",
		"SHELL", "SHELLOPTS", "BASHOPTS",
		"PATH",
		"IFS", "CDPATH", "GLOBIGNORE",
		"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT",
		"DYLD_INSERT_LIBRARIES", "DYLD_LIBRARY_PATH",
		"DYLD_FORCE_FLAT_NAMESPACE",
	}
	in := map[string]string{}
	for _, k := range hostile {
		in[k] = "hostile"
	}
	in["BASH_FUNC_ls%%"] = "() { echo pwned; }" // Shellshock vector
	in["LANG"] = "en_US.UTF-8"

	got := envForPolicy(true, in)
	for _, k := range hostile {
		_, present := got[k]
		require.Falsef(t, present,
			"policy-active env should not contain %q", k)
	}
	_, bashFunc := got["BASH_FUNC_ls%%"]
	require.False(t, bashFunc,
		"BASH_FUNC_ entries should be stripped (Shellshock)")
	require.Equal(t, "en_US.UTF-8", got["LANG"],
		"benign LANG must survive the scrub")
}

// TestEnvForPolicy_RejectsMalformedKeys guards the bypass where a
// caller supplies an env key that already embeds "=" (or other
// invalid bytes). The local runtime serialises entries as
// "key=value", so a key like "PATH=." produces "PATH=.=<value>",
// and libc parses that as PATH set to ".=<value>" — putting the
// attacker back in control of the search path even though the
// scrub never sees a plain "PATH". Reject the malformed key
// outright so the policy mode contract holds regardless of how
// the runtime serialises the map.
func TestEnvForPolicy_RejectsMalformedKeys(t *testing.T) {
	in := map[string]string{
		"PATH=.":           ":/attacker/bin",
		"":                 "anything",
		"NEW\nLINE":        "x",
		"NULL\x00":         "x",
		"CARRIAGE\rRETURN": "x",
		"GOOD":             "kept",
	}
	got := envForPolicy(true, in)
	for _, k := range []string{
		"PATH=.", "", "NEW\nLINE", "NULL\x00", "CARRIAGE\rRETURN",
	} {
		if _, present := got[k]; present {
			t.Fatalf(
				"malformed env key %q must be dropped, got value %q",
				k, got[k],
			)
		}
	}
	require.Equal(t, "kept", got["GOOD"],
		"benign key must survive the scrub")
}

// TestEnvForPolicyOnGOOS_WindowsCaseInsensitive guards the Windows
// bypass where the caller passes "Path=./bin" or "Home=." in mixed
// case. Windows treats env names case-insensitively at runtime, so
// a case-sensitive scrub leaves the hostile entry in place and the
// runtime then picks it up as PATH / HOME. The scrub must fold
// case on Windows; on Linux the scrub stays exact, so a deliberate
// caller-supplied "Path" (a literal lowercase variable) survives.
func TestEnvForPolicyOnGOOS_WindowsCaseInsensitive(t *testing.T) {
	in := map[string]string{
		"Path":             "./bin",
		"Home":             "/tmp/attacker",
		"Ld_Preload":       "/tmp/x.so",
		"BASH_FUNC_ls%%":   "() { echo a; }",
		"bash_func_grep%%": "() { echo a; }",
		"LANG":             "en_US.UTF-8",
	}

	t.Run("windows folds case", func(t *testing.T) {
		got := envForPolicyOnGOOS(true, in, "windows")
		for _, k := range []string{
			"Path", "Home", "Ld_Preload",
			"BASH_FUNC_ls%%", "bash_func_grep%%",
		} {
			if _, present := got[k]; present {
				t.Fatalf(
					"policy-active windows env should not contain %q",
					k,
				)
			}
		}
		require.Equal(t, "en_US.UTF-8", got["LANG"])
	})

	t.Run("linux stays exact", func(t *testing.T) {
		got := envForPolicyOnGOOS(true, in, "linux")
		require.Equal(t, "./bin", got["Path"],
			"linux should treat lowercase Path as a distinct key")
		require.Equal(t, "/tmp/attacker", got["Home"])
		require.Equal(t, "/tmp/x.so", got["Ld_Preload"])
		// Both BASH_FUNC_* shapes contain "%%", which is not a
		// valid POSIX env name character, so the POSIX-only key
		// grammar enforced by envscrub.IsMalformedKey drops both
		// regardless of OS. This is stricter than the previous
		// "rely on the case-sensitive BASH_FUNC_ blocklist" path
		// and catches the lowercase-prefix variant on Linux too.
		_, bashFuncLower := got["bash_func_grep%%"]
		require.False(t, bashFuncLower,
			"non-POSIX env names must be dropped on every OS")
		_, bashFuncUpper := got["BASH_FUNC_ls%%"]
		require.False(t, bashFuncUpper,
			"BASH_FUNC_ prefix entries should always be stripped")
	})
}

// TestCheckRunnerSupportsPolicy guards the policy-mode contract
// that env isolation (CleanEnv) is not optional: if the underlying
// runtime does not advertise SupportsCleanEnv, refuse policy mode
// up front instead of silently degrading to "command name check
// only, host env inherited".
//
// A zero-capabilities engine here represents the shape returned by
// codeexecutor.NewEngine (today: container, e2b). Local opts in
// via NewEngineWithCapabilities(SupportsCleanEnv: true).
func TestCheckRunnerSupportsPolicy(t *testing.T) {
	zero := codeexecutor.NewEngine(nil, nil, nil)
	clean := codeexecutor.NewEngineWithCapabilities(
		nil, nil, nil,
		codeexecutor.Capabilities{SupportsCleanEnv: true},
	)

	t.Run("no policy is a no-op on any runner", func(t *testing.T) {
		require.NoError(t, checkRunnerSupportsPolicy(zero, false))
		require.NoError(t, checkRunnerSupportsPolicy(clean, false))
		require.NoError(t, checkRunnerSupportsPolicy(nil, false))
	})

	t.Run("policy requires SupportsCleanEnv", func(t *testing.T) {
		err := checkRunnerSupportsPolicy(zero, true)
		require.Error(t, err,
			"policy mode must fail closed on a runner without SupportsCleanEnv")
		require.True(t,
			strings.Contains(err.Error(), "CleanEnv"),
			"error should mention the missing CleanEnv capability, got: %v", err,
		)
	})

	t.Run("policy passes when runner supports CleanEnv", func(t *testing.T) {
		require.NoError(t, checkRunnerSupportsPolicy(clean, true))
	})
}

// TestExecTool_PolicyActive_HardensSpawn drives the full prepareExec
// path through Call's input shape and asserts that the spec the
// executor sees has both hardenings applied: the "-c" shell flag
// and a scrubbed env. This catches a future refactor that bypasses
// shellArgsForPolicy / envForPolicy from prepareExec.
func TestExecTool_PolicyActive_HardensSpawn(t *testing.T) {
	tl := NewExecTool(localexec.New(), WithAllowedCommands("echo"))

	in := execInput{
		Command: "echo hi",
		Timeout: policyTestTimeoutSec,
		Env: map[string]string{
			"HOME":       "/tmp/attacker",
			"BASH_ENV":   "/tmp/attacker/.bashenv",
			"LD_PRELOAD": "/tmp/attacker.so",
			"PATH":       "/usr/bin",
		},
	}
	req, err := tl.prepareExec(context.Background(), in)
	require.NoError(t, err)

	require.Equal(t,
		[]string{"-c", "echo hi"}, req.spec.Args,
		"policy active should spawn with -c, not -lc")
	require.True(t, req.spec.CleanEnv,
		"policy active should not inherit host environment variables")
	_, hasHome := req.spec.Env["HOME"]
	require.False(t, hasHome,
		"HOME must be stripped to defeat .profile injection")
	_, hasBashEnv := req.spec.Env["BASH_ENV"]
	require.False(t, hasBashEnv,
		"BASH_ENV must be stripped to defeat bash start-up injection")
	_, hasPreload := req.spec.Env["LD_PRELOAD"]
	require.False(t, hasPreload,
		"LD_PRELOAD must be stripped to defeat linker injection")
	_, hasPath := req.spec.Env["PATH"]
	require.False(t, hasPath,
		"caller-supplied PATH must be stripped so an allowed basename "+
			"cannot be redirected at a workspace-controlled binary")
}

// TestExecTool_NoPolicy_PreservesHistoricalSpawn confirms that a
// caller that does NOT configure a policy keeps the pre-existing
// "-lc" + raw-env behaviour, so this PR is a pure additive harden
// rather than a silent semantic change.
func TestExecTool_NoPolicy_PreservesHistoricalSpawn(t *testing.T) {
	tl := NewExecTool(localexec.New())

	in := execInput{
		Command: "echo hi",
		Timeout: policyTestTimeoutSec,
		Env: map[string]string{
			"HOME": "/home/user",
			"PATH": "/usr/bin",
		},
	}
	req, err := tl.prepareExec(context.Background(), in)
	require.NoError(t, err)

	require.Equal(t,
		[]string{"-lc", "echo hi"}, req.spec.Args,
		"no policy must keep historical -lc")
	require.False(t, req.spec.CleanEnv,
		"no policy must preserve historical host environment inheritance")
	require.Equal(t, in.Env, req.spec.Env,
		"no policy must keep env untouched")
}

func TestExecTool_NoPolicy_AllowsArbitraryShell(t *testing.T) {
	tl := NewExecTool(localexec.New())

	args := execInput{
		Command: "echo $(printf hello) > /tmp/__wsexec_test_$$ ; cat /tmp/__wsexec_test_$$ ; rm /tmp/__wsexec_test_$$",
		Timeout: policyTestTimeoutSec,
	}
	enc, err := json.Marshal(args)
	require.NoError(t, err)

	res, err := tl.Call(context.Background(), enc)
	require.NoError(t, err)
	out := res.(execOutput)
	require.Equal(t, codeexecutor.ProgramStatusExited, out.Status)
	require.Contains(t, out.Output, "hello")
}

func TestExecTool_EnvVar_LoadsAllowedCommands(t *testing.T) {
	t.Setenv(envWorkspaceExecAllowedCommands, "echo, ls")
	tl := NewExecTool(localexec.New())
	require.NotNil(t, tl.allowedCmds)
	require.Contains(t, tl.allowedCmds, "echo")
	require.Contains(t, tl.allowedCmds, "ls")
}

func TestExecTool_EnvVar_LoadsDeniedCommands(t *testing.T) {
	t.Setenv(envWorkspaceExecDeniedCommands, "curl wget")
	tl := NewExecTool(localexec.New())
	require.NotNil(t, tl.deniedCmds)
	require.Contains(t, tl.deniedCmds, "curl")
	require.Contains(t, tl.deniedCmds, "wget")
}

func TestExecTool_OptionPrecedenceOverEnv(t *testing.T) {
	t.Setenv(envWorkspaceExecAllowedCommands, "ignored")
	tl := NewExecTool(localexec.New(), WithAllowedCommands("echo"))
	require.Contains(t, tl.allowedCmds, "echo")
	require.NotContains(t, tl.allowedCmds, "ignored")
}

func TestExecTool_setAllowedCommands_TrimsAndSkipsEmpty(t *testing.T) {
	tl := &ExecTool{}
	tl.setAllowedCommands(nil)
	require.Nil(t, tl.allowedCmds)

	tl.setAllowedCommands([]string{"", "  ", "echo"})
	require.Contains(t, tl.allowedCmds, "echo")
	require.NotContains(t, tl.allowedCmds, "")
	require.NotContains(t, tl.allowedCmds, "  ")
}

func TestExecTool_setDeniedCommands_TrimsAndSkipsEmpty(t *testing.T) {
	tl := &ExecTool{}
	tl.setDeniedCommands(nil)
	require.Nil(t, tl.deniedCmds)

	tl.setDeniedCommands([]string{"", "  ", "curl"})
	require.Contains(t, tl.deniedCmds, "curl")
}
