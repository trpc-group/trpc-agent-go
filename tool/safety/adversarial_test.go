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
	"fmt"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

func TestAdversarialCommandMatrix(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		command string
		want    tool.PermissionAction
		rule    string
	}{
		{"safe go test", "go test ./...", tool.PermissionActionAllow, ""},
		{"recursive force split flags", "rm -r -f /", tool.PermissionActionDeny, "dangerous_command"},
		{"recursive force long flags", "rm --recursive --force /", tool.PermissionActionDeny, "dangerous_command"},
		{"wrapped delete", "env rm -rf /", tool.PermissionActionDeny, "dangerous_command"},
		{"command substitution", "echo $(date)", tool.PermissionActionAsk, "shell_bypass"},
		{"backtick secret read", "echo `cat .env`", tool.PermissionActionDeny, "sensitive_path"},
		{"redirection is review not secret", "echo hi > out.txt", tool.PermissionActionAsk, "shell_bypass"},
		{"dependency change", "go install example.com/tool@latest", tool.PermissionActionAsk, "dependency_change"},
		{"minute sleep", "sleep 2m", tool.PermissionActionDeny, "resource_abuse"},
		{"hour sleep", "sleep 1h", tool.PermissionActionDeny, "resource_abuse"},
		{"short sleep", "sleep 30s", tool.PermissionActionAllow, ""},
		{"infinite loop", "while true; do :; done", tool.PermissionActionDeny, "resource_abuse"},
		{"xargs excessive concurrency", "printf x | xargs -P 100 echo", tool.PermissionActionDeny, "resource_abuse"},
		{"make excessive concurrency", "make -j128", tool.PermissionActionDeny, "resource_abuse"},
		{"go test excessive concurrency", "go test -p 128 ./...", tool.PermissionActionDeny, "resource_abuse"},
		{"quoted delete documentation", "echo 'rm -rf / is dangerous'", tool.PermissionActionAllow, ""},
		{"quoted URL documentation", "echo https://evil.example", tool.PermissionActionAllow, ""},
		{"quoted loop documentation", "echo 'while true'", tool.PermissionActionAllow, ""},
		{"recursive local cleanup", "rm -rf ./build", tool.PermissionActionAllow, ""},
		{"python inline delete", `python -c "import os; os.remove('/tmp/x')"`, tool.PermissionActionDeny, "dangerous_command"},
		{"versioned python inline delete", `python3.12 -c "import os; os.remove('/tmp/x')"`, tool.PermissionActionDeny, "dangerous_command"},
		{"compact python inline delete", `python3.12 -cimport\ os\;os.remove\('/tmp/x'\)`, tool.PermissionActionDeny, "dangerous_command"},
		{"node long eval delete", `node --eval "require('fs').rmSync('/tmp/x')"`, tool.PermissionActionDeny, "dangerous_command"},
		{"node long print environment", `node --print "process.env"`, tool.PermissionActionDeny, "sensitive_path"},
		{"shell wrapped versioned python delete", `sh -c "python3.12 -c 'import os; os.remove(\"/tmp/x\")'"`, tool.PermissionActionDeny, "dangerous_command"},
		{"login shell wrapped root delete", `bash -lc "rm -rf /"`, tool.PermissionActionDeny, "dangerous_command"},
		{"login shell wrapped versioned python delete", `bash -lc "python3.12 -c 'import os; os.remove(\"/tmp/x\")'"`, tool.PermissionActionDeny, "dangerous_command"},
		{"encoded PowerShell", `powershell -EncodedCommand ZQBjAGgAbwAgAGgAaQA=`, tool.PermissionActionDeny, "shell_bypass"},
		{"node inline environment read", `node -e "console.log(process.env)"`, tool.PermissionActionDeny, "sensitive_path"},
		{"python printed delete text", `python -c "print(\"os.remove('x')\")"`, tool.PermissionActionAllow, ""},
		{"versioned python printed delete text", `python3.12 -c "print(\"os.remove('x')\")"`, tool.PermissionActionAllow, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), ScanRequest{Command: tc.command})
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tc.want {
				t.Fatalf("decision = %q, want %q: %+v", report.Decision, tc.want, report)
			}
			if tc.rule != "" && !hasRule(report, tc.rule) {
				t.Fatalf("missing rule %q: %+v", tc.rule, report.Findings)
			}
			if tc.command != "" && report.RequestID == "" {
				t.Fatal("missing stable request digest")
			}
		})
	}
}
func TestAdversarialNetworkMatrix(t *testing.T) {
	policy := DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"net": {
		AllowedDomains: []string{"api.github.com", "*.corp.example"},
	}}
	guard, err := NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		command string
		want    tool.PermissionAction
	}{
		{"exact domain", "curl https://api.github.com/repos", tool.PermissionActionAllow},
		{"wildcard subdomain", "curl https://build.corp.example", tool.PermissionActionAllow},
		{"wildcard excludes apex", "curl https://corp.example", tool.PermissionActionDeny},
		{"prefix bait", "curl https://evilapi.github.com", tool.PermissionActionDeny},
		{"suffix bait", "curl https://api.github.com.evil.example", tool.PermissionActionDeny},
		{"query bait", "curl 'https://evil.example/?next=api.github.com'", tool.PermissionActionDeny},
		{"userinfo bait", "curl https://api.github.com@evil.example", tool.PermissionActionDeny},
		{"all destinations", "curl https://api.github.com https://evil.example", tool.PermissionActionDeny},
		{"schemeless", "curl evil.example:443/upload", tool.PermissionActionDeny},
		{"proxy remap", "curl https://api.github.com --proxy=http://evil.example", tool.PermissionActionDeny},
		{"connect remap", "curl --connect-to api.github.com:443:evil.example:443 https://api.github.com", tool.PermissionActionDeny},
		{"external curl config", "curl -K request.conf https://api.github.com", tool.PermissionActionAsk},
		{"ssh host override", "ssh -o HostName=evil.example api.github.com", tool.PermissionActionDeny},
		{"ssh external config", "ssh -F ssh.conf api.github.com", tool.PermissionActionAsk},
		{"git SCP remote", "git clone git@evil.example:org/repo.git", tool.PermissionActionDeny},
		{"path qualified curl", "/usr/bin/curl evil.example", tool.PermissionActionDeny},
		{"path qualified ssh", "/usr/bin/ssh evil.example", tool.PermissionActionDeny},
		{"Windows path curl", `C:\Windows\System32\curl.exe evil.example`, tool.PermissionActionDeny},
		{"compact proxy", "curl -xevil.example:8080 https://api.github.com", tool.PermissionActionDeny},
		{"compact curl config", "curl -Kconfig https://api.github.com", tool.PermissionActionAsk},
		{"compact SSH hostname", "ssh -oHostName=evil.example api.github.com", tool.PermissionActionDeny},
		{"compact SSH proxy command", "ssh -oProxyCommand='id' api.github.com", tool.PermissionActionAsk},
		{"compact SSH config", "ssh -Fconfig api.github.com", tool.PermissionActionAsk},
		{"compact SSH stdio forwarding", "ssh -Wevil.example:443 api.github.com", tool.PermissionActionDeny},
		{"compact SSH local forwarding", "ssh -L8080:evil.example:443 api.github.com", tool.PermissionActionDeny},
		{"path qualified Git", "/usr/bin/git clone evil.example:repo", tool.PermissionActionDeny},
		{"Windows path Git", `C:\Git\bin\git.exe clone evil.example:repo`, tool.PermissionActionDeny},
		{"quoted SSH hostname", "ssh -o 'HostName=evil.example' api.github.com", tool.PermissionActionDeny},
		{"quoted SSH proxy command", "ssh -o 'ProxyCommand=id' api.github.com", tool.PermissionActionAsk},
		{"ssh proxy command", "ssh -o ProxyCommand='nc %h %p' api.github.com", tool.PermissionActionAsk},
		{"ssh stdio forwarding", "ssh -W api.github.com:443 api.github.com", tool.PermissionActionAsk},
		{"ssh local forwarding allowed destination", "ssh -L 8080:api.github.com:443 api.github.com", tool.PermissionActionAsk},
		{"ssh local forwarding forbidden destination", "ssh -L 8080:evil.example:443 api.github.com", tool.PermissionActionDeny},
		{"ssh remote forwarding", "ssh -R 8080:api.github.com:443 api.github.com", tool.PermissionActionAsk},
		{"ssh dynamic forwarding", "ssh -D 1080 api.github.com", tool.PermissionActionAsk},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			report, err := guard.Scan(context.Background(), ScanRequest{ToolName: "net", Command: tc.command})
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tc.want {
				t.Fatalf("decision = %q, want %q: %+v", report.Decision, tc.want, report)
			}
		})
	}
}

func TestAdversarialIPv6Allowlist(t *testing.T) {
	policy := DefaultPolicy()
	policy.Profiles = map[string]ToolProfile{"net": {
		AllowedDomains: []string{"2001:db8::1"},
	}}
	guard, err := NewGuard(policy)
	if err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{
		"http://[2001:db8::1]/health",
		"http://[2001:0db8::1]/health",
	} {
		report, err := guard.Scan(context.Background(), ScanRequest{
			ToolName: "net", RawFields: map[string]any{"url": destination},
		})
		if err != nil {
			t.Fatal(err)
		}
		if report.Decision != tool.PermissionActionAllow {
			t.Fatalf("IPv6 destination %q decision = %q: %+v", destination, report.Decision, report)
		}
	}
}

func TestAdversarialSensitivePathNormalization(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{
		"cat /etc/./passwd",
		"cat /etc//shadow",
		"cat /etc/../etc/shadow",
		`type C:\\Windows\\System32\\config\\SAM`,
		`type C:\\Windows\\System32\\..\\System32\\config\\SAM`,
	} {
		report, err := guard.Scan(context.Background(), ScanRequest{Command: command})
		if err != nil {
			t.Fatal(err)
		}
		if report.Decision != tool.PermissionActionDeny || !hasRule(report, "sensitive_path") {
			t.Fatalf("command %q escaped sensitive-path detection: %+v", command, report)
		}
	}
}

func TestPermissionNormalizerAdversarialShapes(t *testing.T) {
	guard, err := NewDefaultGuard()
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		args string
		want tool.PermissionAction
	}{
		{"argv", `{"argv":["rm","-rf","/"]}`, tool.PermissionActionDeny},
		{"MCP nested args", `{"payload":{"args":["curl","evil.example"]}}`, tool.PermissionActionAsk},
		{"unicode escaped command", `{"command":"rm\u0020-rf\u0020/"}`, tool.PermissionActionDeny},
		{"multiple code blocks", `{"code_blocks":[{"language":"python","code":"print('ok')"},{"language":"bash","code":"rm -rf /"}]}`, tool.PermissionActionDeny},
		{"double encoded code blocks", `{"code_blocks":"[{\"language\":\"bash\",\"code\":\"rm -rf /\"}]"}`, tool.PermissionActionDeny},
		{"stdin secret path", `{"stdin":"cat .env\\n"}`, tool.PermissionActionDeny},
		{"empty write poll", `{"chars":""}`, tool.PermissionActionAllow},
		{"nonempty write", `{"chars":"y"}`, tool.PermissionActionAsk},
		{"newline submit", `{"chars":"","submit":true}`, tool.PermissionActionAsk},
		{"append newline", `{"chars":"","append_newline":true}`, tool.PermissionActionAsk},
		{"trailing garbage", `{"command":"echo ok"} garbage`, tool.PermissionActionAsk},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := guard.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName: "write_stdin", Arguments: []byte(tc.args),
			})
			if err != nil {
				t.Fatal(err)
			}
			if decision.Action != tc.want {
				t.Fatalf("decision = %q, want %q (%s)", decision.Action, tc.want, decision.Reason)
			}
		})
	}
}

func FuzzGuardScanNeverPanics(f *testing.F) {
	for _, seed := range []string{
		"go test ./...", "rm -rf /", "echo $(date)", "curl https://evil.example",
		"\x00", "'unterminated", "${PATH}", "`cat .env`",
	} {
		f.Add(seed)
	}
	guard, err := NewDefaultGuard()
	if err != nil {
		f.Fatal(err)
	}
	f.Fuzz(func(t *testing.T, command string) {
		report, err := guard.Scan(context.Background(), ScanRequest{Command: command})
		if err != nil {
			t.Fatal(err)
		}
		switch report.Decision {
		case tool.PermissionActionAllow, tool.PermissionActionAsk, tool.PermissionActionDeny:
		default:
			t.Fatalf("invalid decision %q", report.Decision)
		}
	})
}

func BenchmarkGuardScan500(b *testing.B) {
	guard, err := NewDefaultGuard()
	if err != nil {
		b.Fatal(err)
	}
	requests := make([]ScanRequest, 500)
	for i := range requests {
		requests[i] = ScanRequest{Command: fmt.Sprintf("go test ./pkg/%d", i)}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, request := range requests {
			if _, err := guard.Scan(context.Background(), request); err != nil {
				b.Fatal(err)
			}
		}
	}
}
