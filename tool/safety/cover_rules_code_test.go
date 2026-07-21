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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// --- rules_code.go ---

func TestCoverrules_ScanCodeBlock_Empty(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{Language: "python", Code: "   "})
	require.Empty(t, a.Executables)
	require.Empty(t, a.codeMatches)
}

func TestCoverrules_ScanCodeBlock_ShellLanguage(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{Language: "bash", Code: "ls /tmp"})
	require.Contains(t, a.Executables, "ls")
	require.Contains(t, a.PathOps, pathOp{Token: "/tmp", Op: "read", Executable: "ls"})
	// Shell blocks are parsed wholesale; no code-pattern record is kept.
	require.Empty(t, a.codeMatches)
}

func TestCoverrules_ScanCodeBlock_EmbeddedShellParsed(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{
		Language: "python",
		Code:     `os.system("cat /etc/hostname")`,
	})
	require.Contains(t, a.Executables, "cat")
	require.Contains(t, a.WrapperNames, "code.shell_exec")
}

func TestCoverrules_ScanCodeBlock_ShellWrapperPattern(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{
		Language: "javascript",
		Code:     `const args = ['sh' -c, 'ls']`,
	})
	require.Contains(t, a.WrapperNames, "code.shell_wrapper")
}

func TestCoverrules_ScanCodeBlock_NetworkCallWithURL(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{
		Language: "python",
		Code:     `requests.get("https://github.com/org/repo")`,
	})
	require.NotEmpty(t, a.NetworkTargets)
	require.Equal(t, "github.com", a.NetworkTargets[0].Host)
	require.NotEmpty(t, a.codeMatches)
	require.Equal(t, []string{"https://github.com/org/repo"}, a.codeMatches[0].networkURLs)
}

func TestCoverrules_ScanCodeBlock_NetworkCallWithoutURL(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{
		Language: "python",
		Code:     `requests.get(target_url)`,
	})
	require.NotEmpty(t, a.NetworkTargets)
	require.True(t, a.NetworkTargets[0].Malformed)
	require.Equal(t, "code:python", a.NetworkTargets[0].Raw)
}

func TestCoverrules_ScanCodeBlock_PackageInstall(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{Language: "python", Code: `pip install requests`})
	require.True(t, a.InstallPackages)
}

func TestCoverrules_ScanCodeBlock_CredentialPath(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{Language: "python", Code: `open("~/.ssh/id_rsa")`})
	require.NotEmpty(t, a.PathOps)
	require.Equal(t, "read", a.PathOps[0].Op)
	require.Equal(t, "code:python", a.PathOps[0].Executable)
	require.Contains(t, a.PathOps[0].Token, ".ssh")
}

func TestCoverrules_ScanCodeBlock_DangerousDelete(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{Language: "python", Code: `shutil.rmtree("/")`})
	require.NotEmpty(t, a.PathOps)
	require.Equal(t, "delete", a.PathOps[0].Op)
	require.Equal(t, "/", a.PathOps[0].Token)
}

func TestCoverrules_ScanCodeBlock_OutputBombAndLoop(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{
		Language: "python",
		Code:     "while True:\n    print('x')",
	})
	require.True(t, a.HasOutputBomb)
	require.True(t, a.HasUnboundedLoop)
}

func TestCoverrules_ScanCodeBlock_BoundedLoopNotFlagged(t *testing.T) {
	a := &analysis{SleepSeconds: -1}
	scanCodeBlock(a, CodeBlock{
		Language: "python",
		Code:     "while True:\n    if done:\n        break",
	})
	require.False(t, a.HasOutputBomb)
	require.False(t, a.HasUnboundedLoop)
}

func TestCoverrules_ExtractCredentialPathFromCode(t *testing.T) {
	require.Equal(t, "~/.ssh/id_rsa", extractCredentialPathFromCode(`open("~/.ssh/id_rsa")`))
	require.Equal(t, "/.aws/credentials", extractCredentialPathFromCode(`open("/.aws/credentials")`))
	require.Equal(t, "/proc/self/environ", extractCredentialPathFromCode(`read("/proc/self/environ")`))
	// No credential path: stable fallback token.
	require.Equal(t, "code:credential_path", extractCredentialPathFromCode(`print("hi")`))
}

func TestCoverrules_ExtractDeleteTargetFromCode(t *testing.T) {
	require.Equal(t, "/data", extractDeleteTargetFromCode(`shutil.rmtree("/data")`))
	require.Equal(t, "/tmp/f", extractDeleteTargetFromCode(`os.remove("/tmp/f")`))
	require.Equal(t, "/var/log", extractDeleteTargetFromCode(`os.unlink("/var/log")`))
	require.Equal(t, "/x", extractDeleteTargetFromCode(`rm -rf /x`))
	// No recognizable target: root is the conservative fallback.
	require.Equal(t, "/", extractDeleteTargetFromCode(`print("hi")`))
}

func TestCoverrules_AllURLsAllowlisted(t *testing.T) {
	allow := []string{"github.com"}
	require.False(t, allURLsAllowlisted(nil, allow))
	require.True(t, allURLsAllowlisted([]string{"https://github.com/x"}, allow))
	require.False(t, allURLsAllowlisted([]string{"https://evil.example/x"}, allow))
	// A malformed (ambiguous) URL is never allowlisted.
	require.False(t, allURLsAllowlisted([]string{"https://127.0.0.1/x"}, allow))
	// One bad URL in the list fails the whole set.
	require.False(t, allURLsAllowlisted(
		[]string{"https://github.com/x", "https://evil.example/y"}, allow))
}

func TestCoverrules_CodeRuleFindings_AllPatterns(t *testing.T) {
	p := DefaultPolicy()
	a := &analysis{
		codeMatches: []*codeMatchRecord{{
			language:        "python",
			shellExec:       true,
			shellWrapper:    true,
			networkCall:     true,
			networkURLs:     []string{"https://evil.example/x"},
			packageInstall:  true,
			credentialPath:  true,
			dangerousDelete: true,
			outputBomb:      true,
		}},
	}
	ids := ruleIDSet(codeRuleFindings(a, p))
	for _, want := range []string{
		"code.shell_exec", "code.shell_wrapper", "code.network_call",
		"code.package_install", "code.credential_path",
		"code.dangerous_delete", "code.output_bomb",
	} {
		require.Contains(t, ids, want)
	}
}

func TestCoverrules_CodeRuleFindings_AllowlistedNetworkSkipped(t *testing.T) {
	p := DefaultPolicy()
	a := &analysis{
		codeMatches: []*codeMatchRecord{{
			language:    "python",
			networkCall: true,
			networkURLs: []string{"https://github.com/x"},
		}},
	}
	ids := ruleIDSet(codeRuleFindings(a, p))
	require.NotContains(t, ids, "code.network_call")
}

func TestCoverrules_CodeRuleFindings_NoMatches(t *testing.T) {
	require.Empty(t, codeRuleFindings(&analysis{}, DefaultPolicy()))
}

// --- rules_metadata.go ---

func TestCoverrules_RuleMetadata_NoFlags(t *testing.T) {
	require.Empty(t, ruleMetadata(ScanInput{}, DefaultPolicy()))
}

func TestCoverrules_RuleMetadata_Destructive(t *testing.T) {
	in := ScanInput{Metadata: ToolMetadata{Destructive: true}}

	// Default policy: dangerous_commands action is deny.
	p := DefaultPolicy()
	findings := ruleMetadata(in, p)
	require.Len(t, findings, 1)
	require.Equal(t, "metadata.destructive", findings[0].RuleID)
	require.Equal(t, DecisionDeny, findings[0].Decision)

	// An allow action is never silently honored for destructive tools.
	p.Rules.DangerousCommands.Action = DecisionAllow
	findings = ruleMetadata(in, p)
	require.Equal(t, DecisionAsk, findings[0].Decision)

	// Empty action falls back to the medium threshold (ask).
	p.Rules.DangerousCommands.Action = ""
	findings = ruleMetadata(in, p)
	require.Equal(t, DecisionAsk, findings[0].Decision)
}

func TestCoverrules_RuleMetadata_OpenWorld(t *testing.T) {
	in := ScanInput{Metadata: ToolMetadata{OpenWorld: true}}

	p := DefaultPolicy()
	findings := ruleMetadata(in, p)
	require.Len(t, findings, 1)
	require.Equal(t, "metadata.open_world", findings[0].RuleID)
	require.Equal(t, DecisionDeny, findings[0].Decision)

	// Read-only/search tools are exempt.
	readIn := ScanInput{Metadata: ToolMetadata{OpenWorld: true, SearchOrRead: true}}
	require.Empty(t, ruleMetadata(readIn, p))

	// An allow action is upgraded to ask for non-read-only tools.
	p.Rules.Network.Action = DecisionAllow
	findings = ruleMetadata(in, p)
	require.Equal(t, DecisionAsk, findings[0].Decision)
}

// --- rules_resource.go ---

func TestCoverrules_RuleResource_Disabled(t *testing.T) {
	p := DefaultPolicy()
	p.Rules.ResourceAbuse.Enabled = false
	in := ScanInput{Timeout: time.Hour}
	require.Nil(t, ruleResource(in, &analysis{}, p))
}

func TestCoverrules_RuleResource_TimeoutExceeded(t *testing.T) {
	p := DefaultPolicy()
	in := ScanInput{Timeout: p.MaxTimeout + time.Second}
	ids := ruleIDSet(ruleResource(in, &analysis{}, p))
	require.Contains(t, ids, "resource.timeout_exceeded")

	in.Timeout = p.MaxTimeout
	require.Empty(t, ruleResource(in, &analysis{}, p))
}

func TestCoverrules_RuleResource_LongSleep(t *testing.T) {
	p := DefaultPolicy()
	a := analyzeShell("sleep 400")
	ids := ruleIDSet(ruleResource(ScanInput{}, &a, p))
	require.Contains(t, ids, "resource.long_sleep")

	short := analyzeShell("sleep 1")
	require.Empty(t, ruleResource(ScanInput{}, &short, p))
}

func TestCoverrules_RuleResource_OutputBomb(t *testing.T) {
	p := DefaultPolicy()
	a := analyzeShell("yes")
	ids := ruleIDSet(ruleResource(ScanInput{}, &a, p))
	require.Contains(t, ids, "resource.output_bomb")
}

func TestCoverrules_RuleResource_OutputSizeHint(t *testing.T) {
	p := DefaultPolicy()
	in := ScanInput{OutputSizeHint: p.MaxOutputSize + 1}
	ids := ruleIDSet(ruleResource(in, &analysis{}, p))
	require.Contains(t, ids, "resource.output_size")
}

func TestCoverrules_RuleResource_UnboundedLoop(t *testing.T) {
	p := DefaultPolicy()
	in := ScanInput{CodeBlocks: []CodeBlock{
		{Language: "python", Code: "while True:\n    pass"},
	}}
	ids := ruleIDSet(ruleResource(in, &analysis{}, p))
	require.Contains(t, ids, "resource.unbounded_loop")

	bounded := ScanInput{CodeBlocks: []CodeBlock{
		{Language: "go", Code: "for {\n if done { break }\n}"},
	}}
	require.Empty(t, ruleResource(bounded, &analysis{}, p))
}

func TestCoverrules_LoopHasExit(t *testing.T) {
	require.True(t, loopHasExit("while true { break }"))
	require.True(t, loopHasExit("while True: return"))
	require.True(t, loopHasExit("exit(1)"))
	require.True(t, loopHasExit("sys.exit(0)"))
	require.True(t, loopHasExit("os.Exit(1)"))
	// "os.exit" without parentheses is still an exit hint; this form is
	// not shadowed by the earlier "exit(" substring check.
	require.True(t, loopHasExit("defer os.Exit"))
	require.False(t, loopHasExit("while True:\n    pass"))
}

func TestCoverrules_EffectiveTimeout(t *testing.T) {
	require.Equal(t, 5*time.Second, effectiveTimeout(ScanInput{Timeout: 5 * time.Second}, 10*time.Second))
	require.Equal(t, 10*time.Second, effectiveTimeout(ScanInput{}, 10*time.Second))
	require.Equal(t, time.Duration(0), effectiveTimeout(ScanInput{}, 0))
}

// --- rules_secret.go ---

func TestCoverrules_Itoa(t *testing.T) {
	require.Equal(t, "0", itoa(0))
	require.Equal(t, "123", itoa(123))
	require.Equal(t, "-42", itoa(-42))
}

func TestCoverrules_HasSecret(t *testing.T) {
	require.True(t, hasSecret("key is sk_live_1234567890abcdef"))
	require.False(t, hasSecret("nothing to see here"))
	require.False(t, hasSecret(""))
}

func TestCoverrules_RuleSecret_Disabled(t *testing.T) {
	p := DefaultPolicy()
	p.Rules.SecretLeak.Enabled = false
	in := ScanInput{Command: "sk_live_1234567890abcdef"}
	require.Nil(t, ruleSecret(in, p))
}

func TestCoverrules_RuleSecret_EnvValue(t *testing.T) {
	p := DefaultPolicy()
	in := ScanInput{Env: map[string]string{"KEY": "sk_live_1234567890abcdef"}}
	findings := ruleSecret(in, p)
	require.Len(t, findings, 1)
	require.Equal(t, "secret.env_value", findings[0].RuleID)
	require.Equal(t, RiskCritical, findings[0].RiskLevel)
	// Evidence must never carry the secret value.
	require.NotContains(t, findings[0].Evidence, "sk_live_1234567890abcdef")
}

func TestCoverrules_SummarizeMatches_ManyPatterns(t *testing.T) {
	matches := []secretMatch{
		{id: "a", value: "1"},
		{id: "b", value: "22"},
		{id: "c", value: "333"},
		{id: "d", value: "4444"},
	}
	s := summarizeMatches(matches)
	require.True(t, strings.HasPrefix(s, "patterns="))
	require.Contains(t, s, "a:len=1")
	require.Contains(t, s, "b:len=2")
	require.Contains(t, s, "c:len=3")
	require.Contains(t, s, "...")
	require.NotContains(t, s, "d:len=4")

	// Lengths for repeated ids accumulate under one entry.
	dup := summarizeMatches([]secretMatch{{id: "a", value: "1"}, {id: "a", value: "22"}})
	require.Equal(t, "patterns=a:len=3", dup)
}

// --- rules_shell.go ---

func TestCoverrules_RuleShell_Disabled(t *testing.T) {
	p := DefaultPolicy()
	p.Rules.ShellBypass.Enabled = false
	a := analyzeShell("sh -c ls")
	require.Nil(t, ruleShell(&a, p))
}

func TestCoverrules_RuleShell_ParseFailureShapes(t *testing.T) {
	p := DefaultPolicy()

	sub := analyzeShell("echo $(cat /etc/hostname)")
	findings := ruleShell(&sub, p)
	require.NotEmpty(t, findings)
	require.Equal(t, "shell.substitution", findings[0].RuleID)

	redir := analyzeShell("echo hi > out.txt")
	findings = ruleShell(&redir, p)
	require.Equal(t, "shell.redirection_or_background", findings[0].RuleID)
	require.Contains(t, findings[0].Evidence, "redirection")

	bg := analyzeShell("sleep 1 &")
	findings = ruleShell(&bg, p)
	require.Equal(t, "shell.redirection_or_background", findings[0].RuleID)
	require.Contains(t, findings[0].Evidence, "background")

	// An unclassified parse failure keeps the generic rule id and a
	// redacted evidence snippet.
	generic := &analysis{ParseError: errors.New("unbalanced quote near sk_live_1234567890abcdef")}
	findings = ruleShell(generic, p)
	require.Equal(t, "shell.parse_failure", findings[0].RuleID)
	require.NotContains(t, findings[0].Evidence, "sk_live_1234567890abcdef")
}

func TestCoverrules_RuleShell_WrapperOnly(t *testing.T) {
	p := DefaultPolicy()
	// Wrapper detected via parsed pipeline without a parse failure.
	a := analyzeShell("xargs ls")
	require.NoError(t, a.ParseError)
	findings := ruleShell(&a, p)
	require.Len(t, findings, 1)
	require.Equal(t, "shell.wrapper", findings[0].RuleID)

	// No parse error and no wrappers: no findings.
	clean := analyzeShell("ls /tmp")
	require.Empty(t, ruleShell(&clean, p))
}
