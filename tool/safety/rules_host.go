//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"strings"
)

// ruleHost evaluates hostexec-specific rules. It uses the ScanInput
// metadata (Background, PTY, Timeout, SessionID, SessionInput) rather than
// the parsed shell pipeline, because the host boundary is enforced at the
// tool-call level.
//
// Rule ids:
//
//   - host.privilege           sudo/su/doas/runuser invocation.
//   - host.pty_long_session    PTY session without a bounded timeout.
//   - host.background_session  background host process without cleanup plan.
//   - host.unknown_session     write_stdin input for an unknown session.
//   - host.residual_session    session id reused after kill.
//   - capability.missing_isolation  profile does not declare required
//     isolation/environment/network boundary.
func ruleHost(in ScanInput, a *analysis, p Policy, sess *sessionTracker) []Finding {
	if !p.Rules.HostExec.Enabled {
		return nil
	}
	var out []Finding

	// host.privilege: inspect pipeline argv and the raw source.
	if hasPrivilegeCommand(a) {
		out = append(out, Finding{
			RuleID:         "host.privilege",
			RiskLevel:      RiskCritical,
			Decision:       ruleDecision(p.Rules.HostExec.Action, RiskCritical, p),
			Evidence:       "privilege escalation command (sudo/su/doas/runuser)",
			Recommendation: "Refuse privilege escalation; require the operator to approve an isolated workflow",
		})
	}

	// PTY long session: deny a PTY session without a bounded timeout.
	if in.PTY && in.Timeout <= 0 {
		out = append(out, Finding{
			RuleID:         "host.pty_long_session",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.HostExec.Action, RiskHigh, p),
			Evidence:       "PTY session requested without bounded timeout",
			Recommendation: "Require an explicit bounded timeout for PTY sessions",
		})
	}

	// Background host process without timeout.
	if in.Background && in.Timeout <= 0 {
		out = append(out, Finding{
			RuleID:         "host.background_session",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.HostExec.Action, RiskHigh, p),
			Evidence:       "background host session without bounded timeout",
			Recommendation: "Require an explicit bounded timeout and a kill_session plan for background work",
		})
	}

	// write_stdin to an unknown session with non-empty input.
	if sess != nil && in.SessionID != "" && in.SessionInput != "" {
		if !sess.isKnown(in.SessionID) {
			out = append(out, Finding{
				RuleID:         "host.unknown_session",
				RiskLevel:      RiskMedium,
				Decision:       ruleDecision(p.Rules.HostExec.Action, RiskMedium, p),
				Evidence:       "write_stdin to a session not created in this run",
				Recommendation: "Issue the session-creating exec_command first, or pre-register the session id with the guard",
			})
		}
	}

	// Residual session: kill_session on an already-killed id.
	if sess != nil && in.SessionID != "" {
		toolBase := in.ToolName
		if strings.HasSuffix(toolBase, "kill_session") || strings.HasSuffix(toolBase, "workspace_kill_session") {
			if sess.isKilled(in.SessionID) {
				out = append(out, Finding{
					RuleID:         "host.residual_session",
					RiskLevel:      RiskLow,
					Decision:       ruleDecision(p.Rules.HostExec.Action, RiskLow, p),
					Evidence:       "kill_session on an already-finalized session",
					Recommendation: "Skip the duplicate kill or refresh the session tracking state",
				})
			}
		}
	}

	return out
}

// ruleCapability evaluates RequireIsolation against the tool profile.
// When RequireIsolation is true, the profile must declare filesystem
// isolation, environment isolation, AND network restriction. The
// previous implementation only checked filesystem and environment,
// which could falsely approve a local code executor that has no network
// boundary. When the profile is unknown, the rule returns ask rather
// than deny so the operator can register a profile.
func ruleCapability(in ScanInput, p Policy, profiles profileRegistry) []Finding {
	if !p.RequireIsolation {
		return nil
	}
	profile, ok := profiles.lookup(in.ToolProfile)
	if !ok {
		profile, ok = profiles.lookup(in.ToolName)
	}
	if !ok {
		return []Finding{{
			RuleID:         "capability.missing_isolation",
			RiskLevel:      RiskMedium,
			Decision:       DecisionAsk,
			Evidence:       "tool profile not registered; isolation cannot be verified",
			Recommendation: "Register a ToolProfile for this tool or disable RequireIsolation",
		}}
	}
	var missing []string
	if !profile.Isolated {
		missing = append(missing, "filesystem")
	}
	if !profile.EnvironmentIsolated {
		missing = append(missing, "environment")
	}
	if !profile.NetworkRestricted {
		missing = append(missing, "network")
	}
	if len(missing) == 0 {
		return nil
	}
	return []Finding{{
		RuleID:         "capability.missing_isolation",
		RiskLevel:      RiskHigh,
		Decision:       DecisionDeny,
		Evidence:       "profile does not declare " + strings.Join(missing, ",") + " isolation",
		Recommendation: "Use a workspace_exec or execute_code backend that enforces filesystem, environment, and network isolation",
	}}
}

// hasPrivilegeCommand returns true when the parsed pipeline or the raw
// source mentions a privilege-escalation command.
func hasPrivilegeCommand(a *analysis) bool {
	if a == nil {
		return false
	}
	if a.Pipeline != nil {
		for _, argv := range a.Pipeline.Commands {
			if len(argv) == 0 {
				continue
			}
			switch basenameLower(argv[0]) {
			case "sudo", "su", "doas", "runuser", "pkexec", "gosu":
				return true
			}
		}
	}
	return rawSourceHasPrivilegeCommand(a.Source)
}

// rawSourceHasPrivilegeCommand does a best-effort scan of the raw source
// for a privilege-escalation command in command position: the first
// token of the source or the first token after a shell separator. A
// plain substring match would false-positive on quoted text such as
// `echo "please su to root"`.
func rawSourceHasPrivilegeCommand(src string) bool {
	segments := strings.FieldsFunc(strings.ToLower(src), func(r rune) bool {
		return r == '|' || r == ';' || r == '&' || r == '(' || r == ')'
	})
	for _, seg := range segments {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		first := strings.Trim(fields[0], `'"`)
		switch basenameLower(first) {
		case "sudo", "su", "doas", "runuser", "pkexec", "gosu":
			return true
		}
	}
	return false
}
