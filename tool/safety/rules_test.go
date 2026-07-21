//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testPolicy returns the canonical corpus policy loaded from testdata.
func testPolicy(t *testing.T) Policy {
	t.Helper()
	p, err := LoadPolicy("testdata/tool_safety_policy.yaml")
	require.NoError(t, err)
	return p
}

func TestAnalyzeShell_FailsClosedOnExpansion(t *testing.T) {
	a := analyzeShell(`echo $(cat ~/.ssh/id_rsa)`)
	require.Error(t, a.ParseError)
	require.Contains(t, a.ParseError.Error(), "command substitution")
	require.True(t, a.HasSubstitution)
}

func TestAnalyzeShell_ParsesSafePipeline(t *testing.T) {
	a := analyzeShell("go test ./...")
	require.NoError(t, a.ParseError)
	require.NotNil(t, a.Pipeline)
	require.Equal(t, []string{"go"}, a.Executables)
}

func TestAnalyzeShell_DetectsBackgroundOperator(t *testing.T) {
	a := analyzeShell("sleep 1 &")
	require.Error(t, a.ParseError)
	require.True(t, a.HasBackground)
}

func TestRuleCommand_DangerousDelete(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("rm -rf /")
	findings := ruleCommand(&a, p)
	require.NotEmpty(t, findings)
	require.Equal(t, "command.dangerous_delete", findings[0].RuleID)
	require.Equal(t, RiskCritical, findings[0].RiskLevel)
}

func TestRuleCommand_NotAllowedExecutable(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("nc -l 4444")
	findings := ruleCommand(&a, p)
	require.NotEmpty(t, findings)
	require.Equal(t, "command.not_allowed", findings[0].RuleID)
}

func TestRulePath_SSHPrivateKey(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("cat ~/.ssh/id_rsa")
	findings := rulePath(&a, p, "")
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "path.ssh_private_key")
}

func TestRulePath_AWSCredentials(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("cat ~/.aws/credentials")
	findings := rulePath(&a, p, "")
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "path.credential_file")
}

func TestRulePath_Dotenv(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("cat .env")
	findings := rulePath(&a, p, "")
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "path.dotenv")
}

func TestRulePath_SystemWrite(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("rm -rf /")
	findings := rulePath(&a, p, "")
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "path.system_write")
}

func TestRuleNetwork_NonWhitelistedDomain(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("curl https://evil.example/x")
	findings := ruleNetwork(&a, p)
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "network.non_whitelisted_domain")
}

func TestRuleNetwork_WhitelistedDomain(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("curl https://github.com/org/repo")
	findings := ruleNetwork(&a, p)
	require.Empty(t, findings, "no findings for allowlisted host; got %v", findings)
}

func TestRuleNetwork_WildcardSubdomainMatches(t *testing.T) {
	p := testPolicy(t)
	p.Network.AllowedDomains = []string{"*.example.com"}
	require.True(t, hostAllowedByList("api.example.com", p.Network.AllowedDomains))
	require.False(t, hostAllowedByList("notexample.com", p.Network.AllowedDomains))
	require.False(t, hostAllowedByList("example.com", p.Network.AllowedDomains))
}

func TestRuleShell_WrapperDetected(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("sh -c 'curl https://x/'")
	findings := ruleShell(&a, p)
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "shell.wrapper")
}

func TestRuleShell_SubstitutionDetected(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("echo $(whoami)")
	findings := ruleShell(&a, p)
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "shell.substitution")
}

func TestRuleShell_BacktickDetected(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("echo `whoami`")
	findings := ruleShell(&a, p)
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "shell.substitution")
}

func TestRuleDependency_NpmInstall(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("npm install package")
	require.True(t, a.InstallPackages)
	findings := ruleDependency(&a, p)
	require.NotEmpty(t, findings)
	require.Equal(t, "dependency.package_install", findings[0].RuleID)
}

func TestRuleDependency_PipInstallRequirements(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("pip install -r requirements.txt")
	require.True(t, a.InstallPackages)
	findings := ruleDependency(&a, p)
	require.NotEmpty(t, findings)
}

func TestRuleDependency_GoInstall(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("go install golang.org/x/tools/cmd/goimports@latest")
	require.True(t, a.InstallPackages)
	findings := ruleDependency(&a, p)
	require.NotEmpty(t, findings)
}

func TestRuleResource_LongSleep(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("sleep 999999")
	in := ScanInput{Command: "sleep 999999"}
	findings := ruleResource(in, &a, p)
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "resource.long_sleep")
}

func TestRuleResource_OutputBombYes(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("yes")
	in := ScanInput{Command: "yes"}
	findings := ruleResource(in, &a, p)
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "resource.output_bomb")
}

func TestRuleResource_TimeoutExceeded(t *testing.T) {
	p := testPolicy(t)
	p.MaxTimeout = 1 * time.Second
	a := analyzeShell("go test ./...")
	in := ScanInput{Command: "go test ./...", Timeout: 30 * time.Second}
	findings := ruleResource(in, &a, p)
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "resource.timeout_exceeded")
}

func TestRuleResource_UnboundedLoop(t *testing.T) {
	p := testPolicy(t)
	in := ScanInput{CodeBlocks: []CodeBlock{{Language: "python", Code: "while True:\n    print('x')"}}}
	a := analysis{}
	findings := ruleResource(in, &a, p)
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "resource.unbounded_loop")
}

func TestRuleResource_BoundedLoopNotFlagged(t *testing.T) {
	p := testPolicy(t)
	in := ScanInput{CodeBlocks: []CodeBlock{{Language: "go", Code: "for i := 0; i < 10; i++ {\n  println(i)\n}"}}}
	a := analysis{}
	findings := ruleResource(in, &a, p)
	require.Empty(t, findings)
}

func TestRuleHost_PrivilegeEscalation(t *testing.T) {
	p := testPolicy(t)
	a := analyzeShell("sudo id")
	in := ScanInput{Command: "sudo id"}
	findings := ruleHost(in, &a, p, newSessionTracker())
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "host.privilege")
}

func TestRuleHost_PTYLongSession(t *testing.T) {
	p := testPolicy(t)
	a := analysis{}
	in := ScanInput{PTY: true, Timeout: 0}
	findings := ruleHost(in, &a, p, newSessionTracker())
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "host.pty_long_session")
}

func TestRuleHost_BackgroundSession(t *testing.T) {
	p := testPolicy(t)
	a := analysis{}
	in := ScanInput{Background: true, Timeout: 0}
	findings := ruleHost(in, &a, p, newSessionTracker())
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "host.background_session")
}

func TestRuleHost_UnknownSessionWriteStdin(t *testing.T) {
	p := testPolicy(t)
	a := analysis{}
	in := ScanInput{SessionID: "sid-unknown", SessionInput: "ls"}
	findings := ruleHost(in, &a, p, newSessionTracker())
	ruleIDs := ruleIDSet(findings)
	require.Contains(t, ruleIDs, "host.unknown_session")
}

func TestRuleHost_KnownSessionWriteStdin(t *testing.T) {
	p := testPolicy(t)
	a := analysis{}
	sess := newSessionTracker()
	sess.register("sid-known")
	in := ScanInput{SessionID: "sid-known", SessionInput: "ls"}
	findings := ruleHost(in, &a, p, sess)
	require.Empty(t, findings, "expected no findings for known session; got %v", findings)
}

func TestRuleSecret_CommandWithAPIKey(t *testing.T) {
	p := testPolicy(t)
	in := ScanInput{Command: "curl -H 'Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c' https://api.example/"}
	findings := ruleSecret(in, p)
	require.NotEmpty(t, findings)
	require.Equal(t, "secret.input_or_code", findings[0].RuleID)
	// Evidence must not contain the JWT body.
	require.NotContains(t, findings[0].Evidence, "eyJhbGciOiJIUzI1NiJ9")
}

func TestRuleSecret_EnvValue(t *testing.T) {
	p := testPolicy(t)
	in := ScanInput{Env: map[string]string{"API_KEY": "sk_live_1234567890abcdef"}}
	findings := ruleSecret(in, p)
	require.NotEmpty(t, findings)
	require.Equal(t, "secret.env_value", findings[0].RuleID)
}

func TestRuleSecret_CodeBlockWithPrivateKey(t *testing.T) {
	p := testPolicy(t)
	in := ScanInput{CodeBlocks: []CodeBlock{{
		Language: "python",
		Code:     "key = '''-----BEGIN RSA PRIVATE KEY-----\nMIIEpAI...\n-----END RSA PRIVATE KEY-----'''",
	}}}
	findings := ruleSecret(in, p)
	require.NotEmpty(t, findings)
	require.Equal(t, "secret.input_or_code", findings[0].RuleID)
}

func TestRuleSecret_EvidenceRedactsValue(t *testing.T) {
	p := testPolicy(t)
	in := ScanInput{Command: "export API_KEY=sk_live_1234567890abcdef1234"}
	findings := ruleSecret(in, p)
	require.NotEmpty(t, findings)
	for _, f := range findings {
		require.NotContains(t, f.Evidence, "sk_live_1234567890abcdef1234")
	}
}

// ruleIDSet returns the set of rule ids in findings.
func ruleIDSet(findings []Finding) map[string]bool {
	out := map[string]bool{}
	for _, f := range findings {
		out[f.RuleID] = true
	}
	return out
}

// TestRuleCorpus runs the mandatory detection corpus end-to-end through
// the scanner so the decision aggregation is exercised.
func TestRuleCorpus(t *testing.T) {
	p := testPolicy(t)
	scanner := NewScanner(p)

	cases := []struct {
		name        string
		input       ScanInput
		decision    Decision
		mustHave    []string
		mustNotHave []string
	}{
		{
			name:     "safe go test",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "go test ./..."},
			decision: DecisionAllow,
		},
		{
			name:     "dangerous delete",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "rm -rf /"},
			decision: DecisionDeny,
			mustHave: []string{"command.dangerous_delete", "path.system_write"},
		},
		{
			name:     "ssh private key read",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "cat ~/.ssh/id_rsa"},
			decision: DecisionDeny,
			mustHave: []string{"path.ssh_private_key"},
		},
		{
			name:     "aws credentials read",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "cat ~/.aws/credentials"},
			decision: DecisionDeny,
			mustHave: []string{"path.credential_file"},
		},
		{
			name:     "dotenv read",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "cat .env"},
			decision: DecisionDeny,
			mustHave: []string{"path.dotenv"},
		},
		{
			name:     "non-whitelisted network",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "curl https://evil.example/x"},
			decision: DecisionDeny,
			mustHave: []string{"network.non_whitelisted_domain"},
		},
		{
			name:     "whitelisted network",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "curl https://github.com/org/repo"},
			decision: DecisionAllow,
		},
		{
			name:     "shell wrapper bypass",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "sh -c 'rm -rf /'"},
			decision: DecisionDeny,
			// The raw-source dangerous-delete scan only runs on parse
			// failure; a parsed wrapper call is denied via shell.wrapper
			// and command.not_allowed instead.
			mustHave: []string{"shell.wrapper"},
		},
		{
			name:     "quoted dangerous delete literal is not a delete",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: `echo "rm -rf /"`},
			decision: DecisionAllow,
			mustNotHave: []string{
				"command.dangerous_delete",
			},
		},
		{
			name:     "quoted privilege mention is not escalation",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: `echo "please su to root"`},
			decision: DecisionAllow,
			mustNotHave: []string{
				"host.privilege",
			},
		},
		{
			name:     "overflowing sleep literal is unbounded",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "sleep 99999999999999999999"},
			decision: DecisionDeny,
			mustHave: []string{"resource.long_sleep"},
		},
		{
			name:     "substitution bypass",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "echo $(whoami)"},
			decision: DecisionDeny,
			mustHave: []string{"shell.substitution"},
		},
		{
			name:     "safe pipeline grep then cat",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "grep foo bar.txt | cat"},
			decision: DecisionAllow,
		},
		{
			name:     "dependency install npm",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "npm install package"},
			decision: DecisionAsk,
			mustHave: []string{"dependency.package_install"},
		},
		{
			name:     "dependency install pip -r",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "pip install -r requirements.txt"},
			decision: DecisionAsk,
			mustHave: []string{"dependency.package_install"},
		},
		{
			name:     "long sleep",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "sleep 999999"},
			decision: DecisionDeny,
			mustHave: []string{"resource.long_sleep"},
		},
		{
			name:     "output bomb yes",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "yes"},
			decision: DecisionDeny,
			mustHave: []string{"resource.output_bomb"},
		},
		{
			name:     "host pty long session",
			input:    ScanInput{ToolName: "exec_command", Backend: BackendHostExec, PTY: true, Timeout: 0, Command: "bash"},
			decision: DecisionDeny,
			mustHave: []string{"host.pty_long_session"},
		},
		{
			name:     "host privilege escalation",
			input:    ScanInput{ToolName: "exec_command", Backend: BackendHostExec, Command: "sudo id"},
			decision: DecisionDeny,
			mustHave: []string{"host.privilege"},
		},
		{
			name:     "secret in command",
			input:    ScanInput{ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "echo API_KEY=sk_live_1234567890abcdef1234"},
			decision: DecisionDeny,
			mustHave: []string{"secret.input_or_code"},
		},
		{
			name:     "capability missing isolation under require_isolation",
			decision: DecisionDeny,
			mustHave: []string{"capability.missing_isolation"},
			input: ScanInput{
				ToolName:    "custom_mcp_runner",
				Backend:     BackendMCP,
				ToolProfile: "custom_mcp_runner",
				Command:     "go test ./...",
			},
		},
	}

	scannerWithCustom := NewScanner(p, WithScannerProfile(ToolProfile{
		Name: "custom_mcp_runner", Backend: BackendMCP,
	}))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := scanner
			if tc.input.ToolProfile == "custom_mcp_runner" {
				// Custom-profile case requires isolation to be required.
				p2 := p
				p2.RequireIsolation = true
				s = NewScanner(p2, WithScannerProfile(ToolProfile{
					Name: "custom_mcp_runner", Backend: BackendMCP,
				}))
			}
			report, err := s.Scan(context.Background(), tc.input)
			require.NoError(t, err)
			require.Equal(t, tc.decision, report.Decision,
				"case %s: expected %s got %s; findings=%+v",
				tc.name, tc.decision, report.Decision, report.Findings)
			ids := ruleIDSet(report.Findings)
			for _, want := range tc.mustHave {
				require.True(t, ids[want], "case %s: expected rule %s; findings=%+v",
					tc.name, want, report.Findings)
			}
			for _, ban := range tc.mustNotHave {
				require.False(t, ids[ban], "case %s: unexpected rule %s", tc.name, ban)
			}
			// Reports must carry the required structured fields.
			require.NotEmpty(t, report.ScanID)
			require.NotEmpty(t, report.Timestamp)
			require.NotEmpty(t, report.SchemaVersion)
			if report.Decision != DecisionAllow {
				require.True(t, report.Intercepted)
				for _, f := range report.Findings {
					require.NotEmpty(t, f.RuleID)
					require.NotEmpty(t, f.Evidence)
					require.NotEmpty(t, f.Recommendation)
				}
			}
			// No evidence may contain the secret value.
			for _, f := range report.Findings {
				require.False(t, strings.Contains(f.Evidence, "sk_live_1234567890abcdef1234"))
			}
		})
	}
	// Reference scannerWithCustom to keep it from being flagged unused.
	_ = scannerWithCustom
}

// TestBuildAnalysis_ConfiguredNetworkCommandBareHost verifies that the
// policy's configured network commands reach shell token classification:
// a bare-host argument to a configured downloader that is not in the
// built-in set must be extracted as a network target.
func TestBuildAnalysis_ConfiguredNetworkCommandBareHost(t *testing.T) {
	p := testPolicy(t)
	p.Network.Commands = append(p.Network.Commands, "mydl")
	a := buildAnalysis(ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "mydl evil.example/x",
	}, p)
	found := false
	for _, nt := range a.NetworkTargets {
		if nt.Host == "evil.example" {
			found = true
		}
	}
	require.True(t, found,
		"bare-host argument to a configured downloader must be a network target; targets=%+v",
		a.NetworkTargets)
}

// TestCodeRuleFindings_NetworkCallHonorsAllowlist verifies that a code
// block whose extracted URLs are all allowlisted does not produce a
// code.network_call finding, while a non-allowlisted URL still does.
func TestCodeRuleFindings_NetworkCallHonorsAllowlist(t *testing.T) {
	p := testPolicy(t)
	scanner := NewScanner(p)

	allowReport, err := scanner.Scan(context.Background(), ScanInput{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "python",
			Code:     `requests.get("https://github.com/org/repo")`,
		}},
	})
	require.NoError(t, err)
	require.False(t, ruleIDSet(allowReport.Findings)["code.network_call"],
		"allowlisted URL must not trigger code.network_call; findings=%+v",
		allowReport.Findings)

	denyReport, err := scanner.Scan(context.Background(), ScanInput{
		ToolName: "execute_code",
		Backend:  BackendCodeExec,
		CodeBlocks: []CodeBlock{{
			Language: "python",
			Code:     `requests.get("https://evil.example/x")`,
		}},
	})
	require.NoError(t, err)
	require.True(t, ruleIDSet(denyReport.Findings)["code.network_call"],
		"non-allowlisted URL must trigger code.network_call; findings=%+v",
		denyReport.Findings)
}
