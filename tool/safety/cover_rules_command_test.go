//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// --- rules_command.go ---

func TestCoverrules_RuleCommand_Disabled(t *testing.T) {
	p := DefaultPolicy()
	p.Rules.DangerousCommands.Enabled = false
	a := analyzeShell("rm -rf /")
	require.Nil(t, ruleCommand(&a, p))
}

func TestCoverrules_RuleCommand_DependencyAskSuppressesNotAllowed(t *testing.T) {
	p := DefaultPolicy()
	// npm is not in allowed_commands; with the dependency rule on Ask
	// the not-allowed finding is suppressed in favor of the dependency
	// approval flow.
	a := analyzeShell("npm install left-pad")
	for _, f := range ruleCommand(&a, p) {
		require.NotEqual(t, "command.not_allowed", f.RuleID)
	}

	// With a non-ask dependency action the suppression does not apply.
	p.Rules.Dependencies.Action = DecisionDeny
	ids := ruleIDSet(ruleCommand(&a, p))
	require.Contains(t, ids, "command.not_allowed")
}

func TestCoverrules_DependencyRuleOverridesCommand(t *testing.T) {
	a := &analysis{InstallPackages: true}
	p := DefaultPolicy()

	p.Rules.Dependencies.Enabled = false
	require.False(t, dependencyRuleOverridesCommand(a, p))

	p.Rules.Dependencies.Enabled = true
	p.Rules.Dependencies.Action = DecisionDeny
	require.False(t, dependencyRuleOverridesCommand(a, p))

	p.Rules.Dependencies.Action = DecisionAsk
	require.True(t, dependencyRuleOverridesCommand(a, p))

	a.InstallPackages = false
	require.False(t, dependencyRuleOverridesCommand(a, p))
}

func TestCoverrules_HasDangerousDelete_NilAndFallback(t *testing.T) {
	require.False(t, hasDangerousDelete(nil))

	// Parse failure falls back to the raw source scan.
	a := analyzeShell("echo $(rm -rf /)")
	require.Error(t, a.ParseError)
	require.True(t, hasDangerousDelete(&a))

	// Quoted literals in a parsed pipeline are not flagged.
	b := analyzeShell(`echo "rm -rf /"`)
	require.NoError(t, b.ParseError)
	require.False(t, hasDangerousDelete(&b))
}

func TestCoverrules_PipelineSegmentIsDangerous(t *testing.T) {
	require.False(t, pipelineSegmentIsDangerous(nil))
	require.True(t, pipelineSegmentIsDangerous([]string{"mkfs.ext4", "/dev/sda1"}))
	require.False(t, pipelineSegmentIsDangerous([]string{"ls", "/tmp"}))
}

func TestCoverrules_IsDangerousBaseCommand(t *testing.T) {
	cases := []struct {
		argv []string
		want bool
	}{
		{[]string{"rm", "-rf", "/tmp/x"}, true},
		{[]string{"rm", "-r", "/etc"}, true},
		{[]string{"rm", "/"}, true},
		{[]string{"rm", "-v", "file.txt"}, false},
		{[]string{"dd", "if=/dev/zero", "of=/dev/sda"}, true},
		{[]string{"dd", "if=a", "of=b"}, false},
		{[]string{"mkfs", "/dev/sda"}, true},
		{[]string{"shred", "-r", "/tmp/x"}, true},
		{[]string{"shred", "file.txt"}, false},
		{[]string{"ls"}, false},
	}
	for _, tc := range cases {
		require.Equal(t, tc.want, isDangerousBaseCommand(basenameLower(tc.argv[0]), tc.argv), "%v", tc.argv)
	}
}

func TestCoverrules_IsDangerousFindCommand(t *testing.T) {
	require.False(t, isDangerousFindCommand([]string{"ls", "-delete"}))
	require.True(t, isDangerousFindCommand([]string{"find", "/tmp", "-delete"}))
	require.True(t, isDangerousFindCommand([]string{"find", "/tmp", "--delete"}))
	require.True(t, isDangerousFindCommand([]string{"find", "/tmp", "-exec", "rm", "{}", "+"}))
	require.False(t, isDangerousFindCommand([]string{"find", "/tmp", "-name", "x"}))
}

func TestCoverrules_FindHasDestructiveExec(t *testing.T) {
	require.True(t, findHasDestructiveExec([]string{"find", ".", "-exec", "rm", "{}", "+"}))
	require.True(t, findHasDestructiveExec([]string{"find", ".", "-execdir", "shred", "{}", "+"}))
	require.True(t, findHasDestructiveExec([]string{"find", ".", "-ok", "dd", "{}", "+"}))
	require.True(t, findHasDestructiveExec([]string{"find", ".", "-okdir", "/bin/rm", "{}", "+"}))
	require.False(t, findHasDestructiveExec([]string{"find", ".", "-exec", "ls", "{}", "+"}))
	// -exec as the last token has no command after it.
	require.False(t, findHasDestructiveExec([]string{"find", ".", "-exec"}))
	require.False(t, findHasDestructiveExec([]string{"find", ".", "-name", "rm"}))
}

func TestCoverrules_RawSourceHasDangerousDelete(t *testing.T) {
	require.False(t, rawSourceHasDangerousDelete(""))
	require.True(t, rawSourceHasDangerousDelete("rm -rf /tmp/x"))
	require.True(t, rawSourceHasDangerousDelete("RM -FR /tmp/x"))
	require.True(t, rawSourceHasDangerousDelete("rm --recursive --force /tmp/x"))
	require.True(t, rawSourceHasDangerousDelete("find / -delete"))
	require.True(t, rawSourceHasDangerousDelete("find . -delete"))
	require.True(t, rawSourceHasDangerousDelete("python -c shutil.rmtree"))
	require.True(t, rawSourceHasDangerousDelete("os.remove('/x')"))
	require.False(t, rawSourceHasDangerousDelete("ls -la"))
}

func TestCoverrules_HasRecursiveFlag(t *testing.T) {
	require.True(t, hasRecursiveFlag([]string{"rm", "-r"}))
	require.True(t, hasRecursiveFlag([]string{"rm", "-R"}))
	require.True(t, hasRecursiveFlag([]string{"rm", "--recursive"}))
	require.True(t, hasRecursiveFlag([]string{"rm", "--recursive=yes"}))
	require.True(t, hasRecursiveFlag([]string{"rm", "-fr"}))
	require.False(t, hasRecursiveFlag([]string{"rm", "-v"}))
	require.False(t, hasRecursiveFlag([]string{"rm", "--verbose"}))
	require.False(t, hasRecursiveFlag([]string{"rm", "file"}))
}

func TestCoverrules_HasForceOrRootTarget(t *testing.T) {
	require.True(t, hasForceOrRootTarget([]string{"rm", "-f", "file"}))
	require.True(t, hasForceOrRootTarget([]string{"rm", "--force", "file"}))
	require.True(t, hasForceOrRootTarget([]string{"rm", "-rf", "/tmp"}))
	require.True(t, hasForceOrRootTarget([]string{"rm", "-v", "/etc"}))
	require.False(t, hasForceOrRootTarget([]string{"rm", "-v", "file"}))
}

func TestCoverrules_TargetsRootPath(t *testing.T) {
	require.True(t, targetsRootPath([]string{"rm", "/"}))
	require.True(t, targetsRootPath([]string{"rm", "-v", "/etc"}))
	require.False(t, targetsRootPath([]string{"rm", "/tmp/file"}))
}

func TestCoverrules_IsRootOrSystemPath(t *testing.T) {
	for _, p := range []string{
		"/", "/etc", "/etc/", "/usr", "/bin", "/sbin", "/boot", "/proc",
		"/sys", "/root", "/lib", "/var", "/dev", "/run",
		"/proc/self/environ", "/proc/1234/environ",
		"/run/secrets/db", "/var/run/secrets/token",
	} {
		require.True(t, isRootOrSystemPath(p), p)
	}
	for _, p := range []string{"/tmp", "/etc/hosts", "/home/user", "relative/path"} {
		require.False(t, isRootOrSystemPath(p), p)
	}
	// The empty string normalizes the same way as "/" after TrimRight
	// and is therefore treated as the root path.
	require.True(t, isRootOrSystemPath(""))
}

func TestCoverrules_IsShellsafeImplicitDeny(t *testing.T) {
	require.False(t, isShellsafeImplicitDeny(nil))
	require.False(t, isShellsafeImplicitDeny(errors.New("executable not allowed")))
	require.True(t, isShellsafeImplicitDeny(errors.New("shell wrapper or re-executing builtin: sh")))
}

func TestCoverrules_RuleDecision(t *testing.T) {
	p := DefaultPolicy()
	// Critical always denies, even with an allow action.
	require.Equal(t, DecisionDeny, ruleDecision(DecisionAllow, RiskCritical, p))
	// Explicit actions pass through for non-critical risks.
	require.Equal(t, DecisionAllow, ruleDecision(DecisionAllow, RiskHigh, p))
	require.Equal(t, DecisionDeny, ruleDecision(DecisionDeny, RiskMedium, p))
	require.Equal(t, DecisionAsk, ruleDecision(DecisionAsk, RiskLow, p))
	// Empty action falls back to the risk threshold.
	require.Equal(t, DecisionAsk, ruleDecision("", RiskMedium, p))
	require.Equal(t, DecisionAllow, ruleDecision("", RiskLow, p))
	// Empty threshold defaults to deny.
	require.Equal(t, DecisionDeny, ruleDecision("", RiskMedium, Policy{}))
}

// --- rules_path.go ---

func TestCoverrules_RulePath_BothFamiliesDisabled(t *testing.T) {
	p := DefaultPolicy()
	p.Rules.DangerousCommands.Enabled = false
	p.Rules.SecretLeak.Enabled = false
	a := analyzeShell("cat ~/.ssh/id_rsa")
	require.Nil(t, rulePath(&a, p, ""))
}

func TestCoverrules_RulePath_RawSourceFallbackOnParseFailure(t *testing.T) {
	p := DefaultPolicy()
	a := analyzeShell("echo $(cat ~/.ssh/id_rsa)")
	require.Error(t, a.ParseError)
	require.Nil(t, a.Pipeline)
	ids := ruleIDSet(rulePath(&a, p, ""))
	require.Contains(t, ids, "path.ssh_private_key")
}

func TestCoverrules_RulePath_CwdJoinForRelativeDotenv(t *testing.T) {
	p := DefaultPolicy()
	a := analyzeShell("cat .env")
	ids := ruleIDSet(rulePath(&a, p, "/work/project"))
	require.Contains(t, ids, "path.dotenv")
}

func TestCoverrules_IsRelativePath(t *testing.T) {
	require.False(t, isRelativePath(""))
	require.False(t, isRelativePath("/abs"))
	require.False(t, isRelativePath("~/home"))
	require.False(t, isRelativePath("C:file"))
	require.True(t, isRelativePath("rel/path"))
	require.True(t, isRelativePath(".env"))
}

func TestCoverrules_IsSSHRelativePath(t *testing.T) {
	require.False(t, isSSHRelativePath(""))
	require.True(t, isSSHRelativePath(".ssh/id_rsa"))
	require.True(t, isSSHRelativePath(".SSH/config"))
	require.False(t, isSSHRelativePath("work/.ssh/id_rsa"))
}

func TestCoverrules_IsCredentialRelativePath(t *testing.T) {
	cases := map[string]bool{
		"":                      false,
		".aws/credentials":      true,
		".AWS/credentials":      true,
		".aws/config":           false,
		".kube/config":          true,
		".kube/other":           false,
		".netrc":                true,
		".git-credentials":      true,
		".npmrc":                true,
		".pypirc":               true,
		".bashrc":               false,
		"work/.aws/credentials": false,
	}
	for in, want := range cases {
		require.Equal(t, want, isCredentialRelativePath(in), in)
	}
}

func TestCoverrules_EvaluatePathOp_AllBranches(t *testing.T) {
	p := DefaultPolicy()
	p.DeniedPaths = append(p.DeniedPaths, "/opt/secret")

	collect := func(op pathOp) []Finding {
		var out []Finding
		evaluatePathOp(op, p, func(f Finding) { out = append(out, f) })
		return out
	}

	ids := ruleIDSet(collect(pathOp{Token: "/etc", Op: "delete", Executable: "rm"}))
	require.Contains(t, ids, "path.system_write")

	ids = ruleIDSet(collect(pathOp{Token: "~/.ssh/id_rsa", Op: "read", Executable: "cat"}))
	require.Contains(t, ids, "path.ssh_private_key")

	ids = ruleIDSet(collect(pathOp{Token: "~/.aws/credentials", Op: "read", Executable: "cat"}))
	require.Contains(t, ids, "path.credential_file")

	ids = ruleIDSet(collect(pathOp{Token: ".env", Op: "read", Executable: "cat"}))
	require.Contains(t, ids, "path.dotenv")

	ids = ruleIDSet(collect(pathOp{Token: "/opt/secret/key", Op: "read", Executable: "cat"}))
	require.Contains(t, ids, "path.denied")

	// A benign path produces no findings.
	require.Empty(t, collect(pathOp{Token: "/tmp/ok.txt", Op: "read", Executable: "cat"}))
}

func TestCoverrules_EvaluateRawSourcePaths(t *testing.T) {
	p := DefaultPolicy()
	collect := func(src string) []Finding {
		var out []Finding
		evaluateRawSourcePaths(src, p, func(f Finding) { out = append(out, f) })
		return out
	}

	ids := ruleIDSet(collect("cat ~/.ssh/id_rsa"))
	require.Contains(t, ids, "path.ssh_private_key")

	ids = ruleIDSet(collect("cat authorized_keys"))
	require.Contains(t, ids, "path.ssh_private_key")

	ids = ruleIDSet(collect("cat /home/u/.aws/credentials"))
	require.Contains(t, ids, "path.credential_file")

	ids = ruleIDSet(collect("cat /home/u/.kube/config"))
	require.Contains(t, ids, "path.credential_file")

	ids = ruleIDSet(collect("cat /app/.env"))
	require.Contains(t, ids, "path.dotenv")

	require.Empty(t, collect("ls /tmp"))
}

func TestCoverrules_NormalizePath(t *testing.T) {
	require.Equal(t, "", normalizePath(""))
	require.Equal(t, "~", normalizePath("~"))
	require.Equal(t, "~/x", normalizePath("~/x"))
	// Backslash conversion is delegated to filepath.ToSlash, which is
	// platform-dependent; assert the contract rather than a literal.
	require.Equal(t, filepath.ToSlash(`a\b`), normalizePath(`a\b`))
	require.Equal(t, "/etc", normalizePath("/etc"))
}

func TestCoverrules_IsSSHPath(t *testing.T) {
	require.False(t, isSSHPath(""))
	require.True(t, isSSHPath("~/.ssh"))
	require.True(t, isSSHPath("~/.ssh/id_rsa"))
	require.True(t, isSSHPath("/home/u/id_ed25519"))
	require.True(t, isSSHPath("/home/u/authorized_keys"))
	require.True(t, isSSHPath("/srv/cert.pem"))
	require.True(t, isSSHPath("/srv/server.key"))
	require.False(t, isSSHPath("/etc/hosts"))
}

func TestCoverrules_IsTildeCredentialPath(t *testing.T) {
	cases := map[string]bool{
		"~/.aws/credentials":    true,
		"~/.aws/config":         false,
		"~/.kube/config":        true,
		"~/.kube/other":         false,
		"~/.docker/config.json": true,
		"~/.netrc":              true,
		"~/.git-credentials":    true,
		"~/x/.git-credentials":  true,
		"~/.npmrc":              true,
		"~/.pypirc":             true,
		"~/.bashrc":             false,
	}
	for in, want := range cases {
		require.Equal(t, want, isTildeCredentialPath(in), in)
	}
}

func TestCoverrules_IsAbsoluteHomeCredentialPath(t *testing.T) {
	require.True(t, isAbsoluteHomeCredentialPath("/home/u/.aws/credentials"))
	require.True(t, isAbsoluteHomeCredentialPath("/home/u/.ssh/id_rsa"))
	require.True(t, isAbsoluteHomeCredentialPath("/home/u/.kube/config"))
	require.False(t, isAbsoluteHomeCredentialPath("/home/u/.bashrc"))
	require.True(t, isAbsoluteHomeCredentialPath("/users/u/.aws/credentials"))
	require.True(t, isAbsoluteHomeCredentialPath("/users/u/.ssh/id_rsa"))
	require.False(t, isAbsoluteHomeCredentialPath("/users/u/.kube/config"))
	require.False(t, isAbsoluteHomeCredentialPath("/opt/.aws/credentials"))
}

func TestCoverrules_IsRuntimeSecretPath(t *testing.T) {
	require.True(t, isRuntimeSecretPath("/run/secrets/db"))
	require.True(t, isRuntimeSecretPath("/var/run/secrets/token"))
	require.True(t, isRuntimeSecretPath("/proc/self/environ"))
	require.False(t, isRuntimeSecretPath("/run/other"))
	require.False(t, isRuntimeSecretPath("/proc/self/status"))
}

func TestCoverrules_IsDotenvPath(t *testing.T) {
	require.False(t, isDotenvPath(""))
	require.True(t, isDotenvPath(".env"))
	require.True(t, isDotenvPath(".env.local"))
	require.True(t, isDotenvPath("/app/.env"))
	require.False(t, isDotenvPath("/app/env"))
	require.False(t, isDotenvPath("/app/environment"))
}

func TestCoverrules_MatchesDeniedPath(t *testing.T) {
	p := DefaultPolicy()
	p.DeniedPaths = []string{"/opt/secret"}
	p.DeniedPathGlobs = []string{"**/*.pem", "~/.ssh/*"}

	require.True(t, matchesDeniedPath("/opt/secret", p))
	require.True(t, matchesDeniedPath("/opt/secret/nested/key", p))
	require.False(t, matchesDeniedPath("/opt/secretfoo", p))
	require.True(t, matchesDeniedPath("/srv/cert.pem", p))
	// The ~-rooted glob also matches via the **/ alternative form.
	require.True(t, matchesDeniedPath("~/.ssh/id_rsa", p))
	require.True(t, matchesDeniedPath("home/u/.ssh/id_rsa", p))
	require.False(t, matchesDeniedPath("/tmp/ok.txt", p))
}

func TestCoverrules_IsDescendant(t *testing.T) {
	require.False(t, isDescendant("/etc", ""))
	require.False(t, isDescendant("/etc", "."))
	require.True(t, isDescendant("/etc", "/"))
	require.False(t, isDescendant("/", "/"))
	require.True(t, isDescendant("/etc/passwd", "/etc"))
	require.True(t, isDescendant("/etc/passwd", "/etc/"))
	require.False(t, isDescendant("/etcfoo", "/etc"))
	require.False(t, isDescendant("/etc", "/etc/passwd"))
}

// --- rules_env_cwd.go ---

func TestCoverrules_RuleEnvName_EmptyEnv(t *testing.T) {
	require.Nil(t, ruleEnvName(ScanInput{}, DefaultPolicy()))
}

func TestCoverrules_RuleEnvName_DangerousOverrideHostExec(t *testing.T) {
	p := DefaultPolicy()
	in := ScanInput{
		Backend: BackendHostExec,
		Env:     map[string]string{"PATH": "/evil/bin"},
	}
	findings := ruleEnvName(in, p)
	require.Len(t, findings, 1)
	require.Equal(t, "env.dangerous_override", findings[0].RuleID)
	require.Equal(t, RiskHigh, findings[0].RiskLevel)
}

func TestCoverrules_RuleEnvName_DangerousOverrideCaseDedup(t *testing.T) {
	p := DefaultPolicy()
	in := ScanInput{
		Backend: BackendWorkspaceExec,
		Env:     map[string]string{"PATH": "/a", "Path": "/b"},
	}
	findings := ruleEnvName(in, p)
	require.Len(t, findings, 1, "case variants of the same name dedupe")
	require.Equal(t, "env.dangerous_override", findings[0].RuleID)
}

func TestCoverrules_RuleEnvName_DangerousOverrideNonHostBackend(t *testing.T) {
	p := DefaultPolicy()
	// On a non-hostexec backend PATH is whitelisted by the default
	// policy, so no finding fires.
	in := ScanInput{
		Backend: BackendCodeExec,
		Env:     map[string]string{"PATH": "/usr/bin"},
	}
	require.Empty(t, ruleEnvName(in, p))

	// LD_PRELOAD is not whitelisted; on a codeexec backend it surfaces
	// as a non-whitelisted name rather than a dangerous override.
	in.Env = map[string]string{"LD_PRELOAD": "/evil.so"}
	ids := ruleIDSet(ruleEnvName(in, p))
	require.Contains(t, ids, "env.non_whitelisted_name")
	require.NotContains(t, ids, "env.dangerous_override")
}

func TestCoverrules_RuleEnvName_NoWhitelistSkipsCheck(t *testing.T) {
	p := DefaultPolicy()
	p.EnvWhitelist = nil
	in := ScanInput{
		Backend: BackendCodeExec,
		Env:     map[string]string{"ANYTHING": "x"},
	}
	require.Empty(t, ruleEnvName(in, p))
}

func TestCoverrules_RuleEnvName_NonWhitelisted(t *testing.T) {
	p := DefaultPolicy()
	in := ScanInput{
		Backend: BackendCodeExec,
		Env:     map[string]string{"FOO": "x", "LANG": "en_US.UTF-8"},
	}
	findings := ruleEnvName(in, p)
	require.Len(t, findings, 1)
	require.Equal(t, "env.non_whitelisted_name", findings[0].RuleID)
	require.Contains(t, findings[0].Evidence, "FOO")
}

func TestCoverrules_IsDangerousEnvOverride(t *testing.T) {
	for _, name := range []string{
		"PATH", "path", "Ld_Preload", "PYTHONPATH", "NODE_OPTIONS",
		"IFS", "BASH_ENV", "ENV", "SHELLOPTS", "GLIBC_TUNABLES", "HISTFILE",
	} {
		require.True(t, isDangerousEnvOverride(name), name)
	}
	require.False(t, isDangerousEnvOverride("GOPATH"))
	require.False(t, isDangerousEnvOverride(""))
}

func TestCoverrules_RuleCwd(t *testing.T) {
	p := DefaultPolicy()

	require.Nil(t, ruleCwd(ScanInput{Cwd: "  "}, p))

	ids := ruleIDSet(ruleCwd(ScanInput{Cwd: "/etc"}, p))
	require.Contains(t, ids, "cwd.system_path")

	// Lexical tricks still resolve to the system path.
	ids = ruleIDSet(ruleCwd(ScanInput{Cwd: "/etc/../etc"}, p))
	require.Contains(t, ids, "cwd.system_path")

	ids = ruleIDSet(ruleCwd(ScanInput{Cwd: "~/.ssh"}, p))
	require.Contains(t, ids, "cwd.ssh_or_credential")

	p.DeniedPaths = append(p.DeniedPaths, "/opt/secret")
	ids = ruleIDSet(ruleCwd(ScanInput{Cwd: "/opt/secret/sub"}, p))
	require.Contains(t, ids, "cwd.denied")

	require.Empty(t, ruleCwd(ScanInput{Cwd: "/work/ok"}, p))
}

func TestCoverrules_RuleUnknownTool(t *testing.T) {
	profiles := newProfileRegistry()

	// Registered by tool name.
	in := ScanInput{ToolName: "exec_command", Command: "ls"}
	require.Nil(t, ruleUnknownTool(in, &analysis{}, DefaultPolicy(), profiles))

	// Registered by profile name.
	in = ScanInput{ToolName: "custom", ToolProfile: "workspace_exec", Command: "ls"}
	require.Nil(t, ruleUnknownTool(in, &analysis{}, DefaultPolicy(), profiles))

	// Unregistered tool without a command surface passes through.
	in = ScanInput{ToolName: "mcp_search"}
	require.Nil(t, ruleUnknownTool(in, &analysis{}, DefaultPolicy(), profiles))

	// Unregistered tool with a command shape asks.
	in = ScanInput{ToolName: "mcp_custom", Command: "ls"}
	findings := ruleUnknownTool(in, &analysis{}, DefaultPolicy(), profiles)
	require.Len(t, findings, 1)
	require.Equal(t, "unknown.command_shaped_tool", findings[0].RuleID)
	require.Equal(t, DecisionAsk, findings[0].Decision)

	// Unregistered tool with argv only also asks.
	in = ScanInput{ToolName: "mcp_custom", Args: []string{"ls"}}
	require.Len(t, ruleUnknownTool(in, &analysis{}, DefaultPolicy(), profiles), 1)
}

// --- rules_host.go ---

func TestCoverrules_RuleHost_Disabled(t *testing.T) {
	p := DefaultPolicy()
	p.Rules.HostExec.Enabled = false
	in := ScanInput{PTY: true}
	require.Nil(t, ruleHost(in, &analysis{}, p, nil))
}

func TestCoverrules_RuleHost_PrivilegeCommand(t *testing.T) {
	p := DefaultPolicy()
	a := analyzeShell("sudo ls")
	ids := ruleIDSet(ruleHost(ScanInput{}, &a, p, nil))
	require.Contains(t, ids, "host.privilege")
}

func TestCoverrules_RuleHost_PTYAndBackground(t *testing.T) {
	p := DefaultPolicy()
	a := &analysis{}

	ids := ruleIDSet(ruleHost(ScanInput{PTY: true}, a, p, nil))
	require.Contains(t, ids, "host.pty_long_session")

	ids = ruleIDSet(ruleHost(ScanInput{Background: true}, a, p, nil))
	require.Contains(t, ids, "host.background_session")

	// Bounded timeouts clear both findings.
	in := ScanInput{PTY: true, Background: true, Timeout: 1}
	require.Empty(t, ruleHost(in, a, p, nil))
}

func TestCoverrules_RuleHost_SessionTracking(t *testing.T) {
	p := DefaultPolicy()
	a := &analysis{}
	sess := newSessionTracker()

	// write_stdin to an unknown session.
	in := ScanInput{SessionID: "s1", SessionInput: "ls\n"}
	ids := ruleIDSet(ruleHost(in, a, p, sess))
	require.Contains(t, ids, "host.unknown_session")

	// After registration the session is known.
	sess.register("s1")
	require.Empty(t, ruleHost(in, a, p, sess))

	// kill_session on an already-killed session is residual.
	sess.kill("s1")
	kill := ScanInput{ToolName: "workspace_kill_session", SessionID: "s1"}
	ids = ruleIDSet(ruleHost(kill, a, p, sess))
	require.Contains(t, ids, "host.residual_session")

	// kill_session on a live session is fine.
	sess.register("s2")
	kill.SessionID = "s2"
	require.Empty(t, ruleHost(kill, a, p, sess))

	// A non-kill tool name never reports residual sessions.
	other := ScanInput{ToolName: "exec_command", SessionID: "s1"}
	require.Empty(t, ruleHost(other, a, p, sess))
}

func TestCoverrules_RuleCapability(t *testing.T) {
	profiles := newProfileRegistry()
	in := ScanInput{ToolName: "custom_tool"}

	// RequireIsolation off: no findings.
	p := DefaultPolicy()
	p.RequireIsolation = false
	require.Nil(t, ruleCapability(in, p, profiles))

	p.RequireIsolation = true

	// Unknown profile: ask so the operator can register one.
	findings := ruleCapability(in, p, profiles)
	require.Len(t, findings, 1)
	require.Equal(t, "capability.missing_isolation", findings[0].RuleID)
	require.Equal(t, DecisionAsk, findings[0].Decision)

	// Fully isolated profile: no findings.
	profiles.register(ToolProfile{
		Name: "custom_tool", Isolated: true,
		EnvironmentIsolated: true, NetworkRestricted: true,
	})
	require.Empty(t, ruleCapability(in, p, profiles))

	// Partial isolation: deny with the missing boundaries in evidence.
	profiles.register(ToolProfile{Name: "custom_tool", Isolated: true})
	findings = ruleCapability(in, p, profiles)
	require.Len(t, findings, 1)
	require.Equal(t, DecisionDeny, findings[0].Decision)
	require.Contains(t, findings[0].Evidence, "environment")
	require.Contains(t, findings[0].Evidence, "network")
	require.NotContains(t, findings[0].Evidence, "filesystem")
}

func TestCoverrules_HasPrivilegeCommand(t *testing.T) {
	require.False(t, hasPrivilegeCommand(nil))

	a := analyzeShell("sudo ls")
	require.True(t, hasPrivilegeCommand(&a))

	// Unparsable source falls back to the raw scan.
	b := &analysis{Source: "sudo ls"}
	require.True(t, hasPrivilegeCommand(b))

	// Quoted prose must not be treated as a privilege command.
	c := &analysis{Source: `echo "please su to root"`}
	require.False(t, hasPrivilegeCommand(c))
}

func TestCoverrules_RawSourceHasPrivilegeCommand(t *testing.T) {
	require.False(t, rawSourceHasPrivilegeCommand(""))
	require.True(t, rawSourceHasPrivilegeCommand("sudo ls"))
	require.True(t, rawSourceHasPrivilegeCommand("cat x | sudo tee /etc/hosts"))
	require.True(t, rawSourceHasPrivilegeCommand("ls; doas id"))
	require.True(t, rawSourceHasPrivilegeCommand(`"su" - root`))
	require.True(t, rawSourceHasPrivilegeCommand("pkexec ls"))
	require.False(t, rawSourceHasPrivilegeCommand("echo sudo is a tool"))
	require.False(t, rawSourceHasPrivilegeCommand("cat /etc/passwd"))
}

// --- rules_network.go ---

func TestCoverrules_RuleNetwork_Disabled(t *testing.T) {
	p := DefaultPolicy()
	p.Rules.Network.Enabled = false
	a := analyzeShell("curl https://evil.example/x")
	require.Nil(t, ruleNetwork(&a, p))
}

func TestCoverrules_RuleNetwork_DenyAll(t *testing.T) {
	p := DefaultPolicy()
	p.Network.DenyAll = true
	a := analyzeShell("curl https://github.com/x")
	findings := ruleNetwork(&a, p)
	require.Len(t, findings, 1)
	require.Equal(t, "network.deny_all", findings[0].RuleID)
}

func TestCoverrules_RuleNetwork_MalformedTarget(t *testing.T) {
	p := DefaultPolicy()
	a := analyzeShell("curl https://127.0.0.1/x")
	ids := ruleIDSet(ruleNetwork(&a, p))
	require.Contains(t, ids, "network.malformed_target")
}

func TestCoverrules_RuleNetwork_DangerousFlagWithoutTarget(t *testing.T) {
	p := DefaultPolicy()
	a := analyzeShell("curl -K /tmp/config")
	ids := ruleIDSet(ruleNetwork(&a, p))
	require.Contains(t, ids, "network.dangerous_flag")
}

func TestCoverrules_RuleNetwork_AllowlistedHostNoFindings(t *testing.T) {
	p := DefaultPolicy()
	a := analyzeShell("curl https://github.com/org/repo")
	require.Empty(t, ruleNetwork(&a, p))
}

func TestCoverrules_NetworkFlagFindings(t *testing.T) {
	p := DefaultPolicy()

	require.Nil(t, networkFlagFindings(nil, p))
	require.Nil(t, networkFlagFindings(&analysis{}, p))

	build := func(segments ...[]string) *analysis {
		return &analysis{Pipeline: &shellsafe.Pipeline{Commands: segments}}
	}

	// Config and resolve flags are high risk.
	for _, flag := range []string{"-K", "--config", "--config=/tmp/c", "--resolve", "--resolve=h:443:1.2.3.4"} {
		a := build([]string{"curl", flag, "https://github.com"})
		findings := networkFlagFindings(a, p)
		require.NotEmpty(t, findings, flag)
		require.Equal(t, "network.dangerous_flag", findings[0].RuleID)
		require.Equal(t, RiskHigh, findings[0].RiskLevel, flag)
	}

	// Redirect-following flags are medium risk.
	for _, flag := range []string{"-L", "--location", "--location-trusted", "--max-redirs=5"} {
		a := build([]string{"wget", flag, "https://github.com"})
		findings := networkFlagFindings(a, p)
		require.NotEmpty(t, findings, flag)
		require.Equal(t, RiskMedium, findings[0].RiskLevel, flag)
	}

	// Non-downloader executables and empty segments are skipped.
	a := build([]string{}, []string{"ls", "-K"})
	require.Empty(t, networkFlagFindings(a, p))

	// aria2c is also inspected.
	a = build([]string{"aria2c", "--config=/tmp/c"})
	require.NotEmpty(t, networkFlagFindings(a, p))
}

// --- rules_dependency.go ---

func TestCoverrules_RuleDependency(t *testing.T) {
	p := DefaultPolicy()

	p.Rules.Dependencies.Enabled = false
	a := analyzeShell("npm install left-pad")
	require.Nil(t, ruleDependency(&a, p))

	p.Rules.Dependencies.Enabled = true
	require.Nil(t, ruleDependency(&analysis{}, p))

	findings := ruleDependency(&a, p)
	require.Len(t, findings, 1)
	require.Equal(t, "dependency.package_install", findings[0].RuleID)
	require.Equal(t, DecisionAsk, findings[0].Decision)
}
