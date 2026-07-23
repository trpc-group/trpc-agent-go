// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"trpc.group/trpc-go/trpc-agent-go/codeexecutor"
	"trpc.group/trpc-go/trpc-agent-go/tool/safety"
)

func TestGuardAppliesNetworkAllowlist(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	guard := mustGuard(t, policy)
	tests := []struct {
		name     string
		command  string
		want     safety.Decision
		wantRule string
	}{
		{"explicit allowlisted URL", "curl https://api.github.com/repos/x/y", safety.DecisionAllow, "safety.no_findings"},
		{"schemeless denied host", "curl evil.example", safety.DecisionDeny, "network.destination"},
		{"host port denied", "curl evil.example:443/path", safety.DecisionDeny, "network.destination"},
		{"explicit denied URL", "wget https://evil.example/file", safety.DecisionDeny, "network.destination"},
		{"resolve changes destination", "curl --resolve api.github.com:443:203.0.113.1 https://api.github.com", safety.DecisionDeny, "network.destination_override"},
		{"connect to changes destination", "curl --connect-to api.github.com:443:evil.example:443 https://api.github.com", safety.DecisionDeny, "network.destination_override"},
		{"proxy changes destination", "curl --proxy https://evil.example https://api.github.com", safety.DecisionDeny, "network.destination_override"},
		{"config may change destination", "curl --config request.conf", safety.DecisionNeedsHumanReview, "network.config"},
		{"ssh proxy command", "ssh -o ProxyCommand=relay api.github.com", safety.DecisionDeny, "network.destination_override"},
		{"ssh hostname override", "ssh -o HostName=evil.example api.github.com", safety.DecisionDeny, "network.destination_override"},
		{"ssh config file", "ssh -F ssh.conf api.github.com", safety.DecisionNeedsHumanReview, "network.config"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := guard.Scan(safety.Request{Command: tc.command})
			require.Equal(t, tc.want, report.Decision)
			require.Equal(t, tc.wantRule, report.RuleID)
		})
	}
}

func TestGuardTreatsEmptyNetworkAllowlistAsDenyAll(t *testing.T) {
	report := mustGuard(t, safety.DefaultPolicy()).Scan(
		safety.Request{Command: "curl https://example.com"},
	)
	require.Equal(t, safety.DecisionDeny, report.Decision)
	require.Equal(t, "network.destination", report.RuleID)
}

func TestGuardScansCustomNetworkDestinations(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	guard := mustGuard(t, policy)
	tests := []struct {
		name    string
		request safety.Request
		rule    string
	}{
		{"generic URL scheme", safety.Request{Command: "custom-fetch git://evil.example/repo"}, "network.destination"},
		{"custom proxy override", safety.Request{Command: "custom-fetch --proxy evil.example https://api.github.com/file"}, "network.destination_override"},
		{"unknown endpoint", safety.Request{Backend: safety.BackendUnknown, RawArguments: json.RawMessage(`{"endpoint":"evil.example:443"}`)}, "network.destination"},
		{"top-level encoded command", safety.Request{Backend: safety.BackendUnknown, RawArguments: json.RawMessage(`"rm -rf /"`)}, "dangerous.rm_rf"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := guard.Scan(tc.request)
			require.Equal(t, safety.DecisionDeny, report.Decision)
			require.Equal(t, tc.rule, report.RuleID)
		})
	}
}

func TestGuardClassifiesNetworkClientsWithoutURLFalsePositives(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	guard := mustGuard(t, policy)
	tests := []struct {
		name     string
		command  string
		decision safety.Decision
		rule     string
	}{
		{"known non-network URL argument", "echo https://evil.example", safety.DecisionAllow, "safety.no_findings"},
		{"known non-network proxy flag", "echo --proxy evil.example", safety.DecisionAllow, "safety.no_findings"},
		{"unknown executable URL", "mystery-tool https://evil.example", safety.DecisionNeedsHumanReview, "network.unknown_client"},
		{"known client URL", "curl https://evil.example", safety.DecisionDeny, "network.destination"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := guard.Scan(safety.Request{Command: tc.command})
			require.Equal(t, tc.decision, report.Decision)
			require.Equal(t, tc.rule, report.RuleID)
		})
	}
}

func TestGuardHandlesCaseSensitiveAttachedConfigOptions(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	guard := mustGuard(t, policy)
	for _, tc := range []struct {
		command  string
		decision safety.Decision
		rule     string
	}{
		{"ssh -Fssh.conf api.github.com", safety.DecisionNeedsHumanReview, "network.config"},
		{"curl -Krequest.conf", safety.DecisionNeedsHumanReview, "network.config"},
		{"curl -k https://api.github.com", safety.DecisionAllow, "safety.no_findings"},
	} {
		report := guard.Scan(safety.Request{Command: tc.command})
		require.Equal(t, tc.decision, report.Decision, tc.command)
		require.Equal(t, tc.rule, report.RuleID, tc.command)
	}
}

func TestGuardScansInlineInterpreterPayloads(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	guard := mustGuard(t, policy)
	for _, tc := range []struct {
		name    string
		command string
		rule    string
	}{
		{"python credential read", `python -c 'open("~/.ssh/id_rsa").read()'`, "sensitive.path"},
		{"python network call", `python -c 'urllib.request.urlopen("https://evil.example")'`, "network.destination"},
		{"node dangerous process", `node -e 'require("child_process").exec("rm -rf /")'`, "code.process_bridge"},
		{"node attached eval", `node --eval='require("child_process").exec("rm -rf /")'`, "code.process_bridge"},
		{"ruby credential read", `ruby -e 'File.read("~/.ssh/id_rsa")'`, "sensitive.path"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := guard.Scan(safety.Request{Command: tc.command})
			require.Equal(t, safety.DecisionDeny, report.Decision)
			require.Equal(t, tc.rule, report.RuleID)
		})
	}
}

func TestGuardScansSchemelessCodeNetworkLiterals(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	guard := mustGuard(t, policy)
	for _, block := range []codeexecutor.CodeBlock{
		{Language: "python", Code: `socket.create_connection(("evil.example", 443))`},
		{Language: "go", Code: `net.Dial("tcp", "evil.example:443")`},
		{Language: "javascript", Code: `net.connect(443, "evil.example")`},
	} {
		report := guard.Scan(safety.Request{CodeBlocks: []codeexecutor.CodeBlock{block}})
		require.Equal(t, safety.DecisionDeny, report.Decision, block.Language)
		require.Equal(t, "network.destination", report.RuleID, block.Language)
	}
}

func TestGuardEnforcesResourcePolicy(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	tests := []struct {
		name     string
		req      safety.Request
		want     safety.Decision
		wantRule string
	}{
		{"environment variable", safety.Request{Command: "go test ./...", Env: map[string]string{"PATH": "/usr/bin", "API_TOKEN": "secret-value"}}, safety.DecisionDeny, "environment.variable"},
		{"timeout", safety.Request{Command: "go test ./...", TimeoutSeconds: 301}, safety.DecisionDeny, "resource.timeout"},
		{"output budget", safety.Request{Command: "go test ./...", MaxOutputBytes: 4*1024*1024 + 1}, safety.DecisionDeny, "resource.output_limit"},
		{"host background", safety.Request{Backend: safety.BackendHostExec, Command: "go test ./...", TimeoutSeconds: 300, Background: true}, safety.DecisionDeny, "host.background"},
		{"host tty", safety.Request{Backend: safety.BackendHostExec, Command: "go test ./...", TimeoutSeconds: 300, TTY: true}, safety.DecisionNeedsHumanReview, "host.tty"},
		{"host default timeout", safety.Request{Backend: safety.BackendHostExec, Command: "go test ./..."}, safety.DecisionDeny, "resource.timeout"},
		{"long sleep", safety.Request{Command: "sleep 600"}, safety.DecisionNeedsHumanReview, "resource.long_running"},
		{"infinite sleep", safety.Request{Command: "sleep infinity"}, safety.DecisionNeedsHumanReview, "resource.long_running"},
		{"day sleep", safety.Request{Command: "sleep 1d"}, safety.DecisionNeedsHumanReview, "resource.long_running"},
		{"output generator", safety.Request{Command: "yes data"}, safety.DecisionNeedsHumanReview, "resource.large_output"},
		{"high parallelism", safety.Request{Command: "make -j100"}, safety.DecisionNeedsHumanReview, "resource.concurrency"},
		{"host effective timeout", safety.Request{Backend: safety.BackendHostExec, Command: "go test ./...", TimeoutSeconds: 1800}, safety.DecisionDeny, "resource.timeout"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := guard.Scan(tc.req)
			require.Equal(t, tc.want, report.Decision)
			require.Equal(t, tc.wantRule, report.RuleID)
		})
	}
}

func TestGuardDoesNotApplyHostExecDefaultWithoutExecution(t *testing.T) {
	report := mustGuard(t, safety.DefaultPolicy()).Scan(safety.Request{
		Backend: safety.BackendHostExec,
	})
	require.Equal(t, safety.DecisionAllow, report.Decision)
}

func TestGuardReviewsDependencyMutationWithGlobalOptions(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	for _, command := range []string{
		"go install example.com/tool@latest",
		"npm --global install example-package",
		"npm --prefix packages install example-package",
		"pip --disable-pip-version-check install example-package",
		"apt-get -y install example-package",
		"python -m pip install example-package",
		"python3 -m pip install example-package",
		"yarn add example-package",
		"pnpm add example-package",
		"gem install example-package",
	} {
		report := guard.Scan(safety.Request{Command: command})
		require.Equal(t, safety.DecisionNeedsHumanReview, report.Decision, command)
		require.Equal(t, "dependency.install", report.RuleID, command)
	}
}

func TestGuardScansEveryCodeBlock(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	report := guard.Scan(safety.Request{
		Backend: safety.BackendCodeExec,
		CodeBlocks: []codeexecutor.CodeBlock{
			{Language: "python", Code: "print(1)"},
			{Language: "bash", Code: "rm -rf /"},
		},
	})
	require.Equal(t, safety.DecisionDeny, report.Decision)
	require.Equal(t, "dangerous.rm_rf", report.RuleID)
}

func TestGuardScansLanguageProcessBridges(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	tests := []codeexecutor.CodeBlock{
		{Language: "python", Code: `subprocess.run(["rm", "-rf", "/"])`},
		{Language: "go", Code: `exec.Command("rm", "-rf", "/").Run()`},
		{Language: "javascript", Code: `child_process.exec("rm -rf /")`},
	}
	for _, block := range tests {
		report := guard.Scan(safety.Request{Backend: safety.BackendCodeExec, CodeBlocks: []codeexecutor.CodeBlock{block}})
		require.Equal(t, safety.DecisionDeny, report.Decision, block.Language)
		require.Equal(t, "code.process_bridge", report.RuleID, block.Language)
	}
}

func TestGuardScansAliasedLanguageProcessBridges(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	for _, block := range []codeexecutor.CodeBlock{
		{Language: "python", Code: "from subprocess import run\nrun(['rm', '-rf', '/'])"},
		{Language: "go", Code: "import runner \"os/exec\"\nrunner.Command(\"rm\", \"-rf\", \"/\")"},
		{Language: "javascript", Code: `const {exec} = require("child_process"); exec("rm -rf /")`},
	} {
		report := guard.Scan(safety.Request{CodeBlocks: []codeexecutor.CodeBlock{block}})
		require.Equal(t, safety.DecisionDeny, report.Decision, block.Language)
		require.Equal(t, "code.process_bridge", report.RuleID, block.Language)
	}
}

func TestGuardAllowsImportWithoutProcessInvocation(t *testing.T) {
	report := mustGuard(t, safety.DefaultPolicy()).Scan(safety.Request{
		CodeBlocks: []codeexecutor.CodeBlock{{
			Language: "javascript",
			Code:     `const childProcess = require("child_process"); console.log("loaded")`,
		}},
	})
	require.Equal(t, safety.DecisionAllow, report.Decision)
}

func TestGuardScansCodeLiteralsWithoutShellParsingLanguageSyntax(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	guard := mustGuard(t, policy)

	safe := guard.Scan(safety.Request{CodeBlocks: []codeexecutor.CodeBlock{
		{Language: "go", Code: `fmt.Println("safe | text")`},
	}})
	require.Equal(t, safety.DecisionAllow, safe.Decision)

	deniedNetwork := guard.Scan(safety.Request{CodeBlocks: []codeexecutor.CodeBlock{
		{Language: "python", Code: `requests.get("https://evil.example/data")`},
	}})
	require.Equal(t, safety.DecisionDeny, deniedNetwork.Decision)
	require.Equal(t, "network.destination", deniedNetwork.RuleID)

	deniedPath := guard.Scan(safety.Request{CodeBlocks: []codeexecutor.CodeBlock{
		{Language: "python", Code: `open("~/.ssh/id_rsa").read()`},
	}})
	require.Equal(t, safety.DecisionDeny, deniedPath.Decision)
	require.Equal(t, "sensitive.path", deniedPath.RuleID)
}

func TestGuardDetectsCodeResourceAbuse(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	for _, block := range []codeexecutor.CodeBlock{
		{Language: "python", Code: "while True:\n    pass"},
		{Language: "go", Code: "for {}"},
		{Language: "javascript", Code: "while (true) {}"},
	} {
		report := guard.Scan(safety.Request{CodeBlocks: []codeexecutor.CodeBlock{block}})
		require.Equal(t, safety.DecisionDeny, report.Decision, block.Language)
		require.Equal(t, "resource.infinite_loop", report.RuleID, block.Language)
	}
}

func TestGuardReviewsCodeOutputAndConcurrencyAbuse(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	for _, block := range []codeexecutor.CodeBlock{
		{Language: "python", Code: `print("x" * 10000000)`},
		{Language: "go", Code: `fmt.Print(strings.Repeat("x", 10000000))`},
		{Language: "javascript", Code: `console.log("x".repeat(10000000))`},
		{Language: "python", Code: `concurrent.futures.ThreadPoolExecutor(max_workers=100)`},
	} {
		report := guard.Scan(safety.Request{CodeBlocks: []codeexecutor.CodeBlock{block}})
		require.Equal(t, safety.DecisionNeedsHumanReview, report.Decision, block.Language)
		require.Contains(t, []string{"resource.large_output", "resource.concurrency"}, report.RuleID)
	}
}

func TestGuardDecodesUnknownJSONBeforeScanning(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	tests := []struct {
		name string
		raw  json.RawMessage
		rule string
	}{
		{"escaped command", json.RawMessage(`{"command":"rm\u0020-rf\u0020/"}`), "dangerous.rm_rf"},
		{"nested command", json.RawMessage(`{"input":{"commands":["go test ./...","cat ~/.ssh/id_rsa"]}}`), "sensitive.path"},
		{"encoded JSON string", json.RawMessage(`{"payload":"{\"command\":\"curl https://evil.example\"}"}`), "network.destination"},
		{"malformed JSON", json.RawMessage(`{"command":`), "arguments.parse_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := guard.Scan(safety.Request{Backend: safety.BackendUnknown, RawArguments: tc.raw})
			require.Equal(t, safety.DecisionDeny, report.Decision)
			require.Equal(t, tc.rule, report.RuleID)
		})
	}
}

func TestReportRedactsSecretsAndCredentialPaths(t *testing.T) {
	policy := safety.DefaultPolicy()
	policy.NetworkAllowlist = []string{"github.com"}
	guard := mustGuard(t, policy)
	tests := []struct {
		name    string
		command string
		secret  string
	}{
		{"assignment", `echo API_KEY=super-secret-value`, "super-secret-value"},
		{"bearer", `curl https://api.github.com -H 'Authorization: Bearer abcdefghijklmnop'`, "abcdefghijklmnop"},
		{"basic auth", `curl https://api.github.com -H 'Authorization: Basic dXNlcjpzZWNyZXQ='`, "dXNlcjpzZWNyZXQ="},
		{"user flag", `curl https://api.github.com -u user:flag-password`, "flag-password"},
		{"URL user info", `curl https://user:url-password@api.github.com`, "url-password"},
		{"GitHub token", `echo ghp_abcdefghijklmnopqrstuvwxyz1234567890`, "ghp_abcdefghijklmnopqrstuvwxyz1234567890"},
		{"AWS access key", `echo AKIAIOSFODNN7EXAMPLE`, "AKIAIOSFODNN7EXAMPLE"},
		{"private key", "echo '-----BEGIN PRIVATE KEY-----\nprivate-material\n-----END PRIVATE KEY-----'", "private-material"},
		{"incomplete private key", "echo '-----BEGIN PRIVATE KEY-----\npartial-private-material'", "partial-private-material"},
		{"credential path", "cat /home/user/.aws/credentials", "/home/user/.aws/credentials"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report := guard.Scan(safety.Request{Command: tc.command})
			encoded, err := json.Marshal(report)
			require.NoError(t, err)
			require.True(t, report.Redacted)
			require.Contains(t, string(encoded), "[REDACTED]")
			require.NotContains(t, string(encoded), tc.secret)
		})
	}
}

func TestGuardHandlesDeepArgumentsAndDeterministicFindings(t *testing.T) {
	guard := mustGuard(t, safety.DefaultPolicy())
	value := any("go test ./...")
	for i := 0; i < maxTestArgumentDepth; i++ {
		value = map[string]any{"nested": value}
	}
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	report := guard.Scan(safety.Request{RawArguments: raw})
	require.Equal(t, safety.DecisionDeny, report.Decision)
	require.Equal(t, "arguments.max_depth", report.RuleID)

	const mixed = `{"commands":["sleep 1d","go install example.com/tool@latest"],"z":{"command":"cat ~/.ssh/id_rsa"}}`
	first := guard.Scan(safety.Request{RawArguments: json.RawMessage(mixed)})
	for i := 0; i < 20; i++ {
		next := guard.Scan(safety.Request{RawArguments: json.RawMessage(mixed)})
		require.Equal(t, first.Decision, next.Decision)
		require.Equal(t, first.RuleID, next.RuleID)
		require.Equal(t, first.Findings, next.Findings)
	}
}

const maxTestArgumentDepth = 40

func TestReportDoesNotRedactOrdinaryPathPrefixes(t *testing.T) {
	report := mustGuard(t, safety.DefaultPolicy()).Scan(
		safety.Request{Command: "echo .environment"},
	)
	require.Equal(t, safety.DecisionAllow, report.Decision)
	require.False(t, report.Redacted)
	require.Equal(t, "echo .environment", report.Command)
}
