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

// ruleNetwork evaluates network egress rules. It recognizes curl, wget,
// nc/netcat, ssh, scp, sftp, git clone/fetch/push, and any configured
// network_commands. URL hosts are parsed with the url helper; malformed,
// non-ASCII, IP-literal, loopback, link-local, metadata, and ambiguous
// targets are denied by default. Allowlist entries match the exact host
// or an explicit *.example.com subdomain rule.
//
// Rule ids:
//
//   - network.non_whitelisted_domain   target host is not in the allowlist.
//   - network.malformed_target         URL/host could not be safely parsed.
//   - network.deny_all                 policy denies all egress.
//   - network.dangerous_flag           curl -K/--config/--resolve/-L with
//     redirect-following or config file.
func ruleNetwork(a *analysis, p Policy) []Finding {
	if !p.Rules.Network.Enabled {
		return nil
	}
	if p.Network.DenyAll && len(a.NetworkTargets) > 0 {
		return []Finding{{
			RuleID:         "network.deny_all",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.Network.Action, RiskHigh, p),
			Evidence:       "network egress attempted under deny_all policy",
			Recommendation: "Disable deny_all or remove the network command from the request",
		}}
	}
	if len(a.NetworkTargets) == 0 {
		// Still inspect argv for dangerous curl/wget flags even when no
		// explicit URL target was extracted.
		return networkFlagFindings(a, p)
	}

	var out []Finding
	seen := map[string]bool{}
	add := func(f Finding) {
		key := f.RuleID + "|" + f.Evidence
		if seen[key] {
			return
		}
		seen[key] = true
		out = append(out, f)
	}

	for _, t := range a.NetworkTargets {
		if t.Malformed || t.Host == "" {
			add(Finding{
				RuleID:         "network.malformed_target",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.Network.Action, RiskHigh, p),
				Evidence:       "malformed or ambiguous network target",
				Recommendation: "Refuse the request; require a fully-qualified, allowlisted domain",
			})
			continue
		}
		if !hostAllowedByList(t.Host, p.Network.AllowedDomains) {
			add(Finding{
				RuleID:         "network.non_whitelisted_domain",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.Network.Action, RiskHigh, p),
				Evidence:       "host not in allowlist: " + t.Host,
				Recommendation: "Add the host to network.allowed_domains or remove the network command",
			})
		}
	}

	if extra := networkFlagFindings(a, p); len(extra) > 0 {
		out = append(out, extra...)
	}
	return out
}

// networkFlagFindings detects curl -K/--config/--resolve and redirect-
// following flags that bypass URL-level allowlists.
func networkFlagFindings(a *analysis, p Policy) []Finding {
	if a == nil || a.Pipeline == nil {
		return nil
	}
	var out []Finding
	for _, argv := range a.Pipeline.Commands {
		if len(argv) == 0 {
			continue
		}
		base := basenameLower(argv[0])
		if base != "curl" && base != "wget" && base != "aria2c" {
			continue
		}
		for _, flag := range argv[1:] {
			switch {
			case flag == "-K", flag == "--config",
				strings.HasPrefix(flag, "--config="),
				flag == "--resolve",
				strings.HasPrefix(flag, "--resolve="):
				out = append(out, Finding{
					RuleID:         "network.dangerous_flag",
					RiskLevel:      RiskHigh,
					Decision:       ruleDecision(p.Rules.Network.Action, RiskHigh, p),
					Evidence:       "curl/wget config or resolve flag bypasses URL allowlist",
					Recommendation: "Refuse the request; require explicit URL targets on the allowlist",
				})
			case flag == "-L", flag == "--location",
				flag == "--location-trusted",
				strings.HasPrefix(flag, "--max-redirs"):
				out = append(out, Finding{
					RuleID:         "network.dangerous_flag",
					RiskLevel:      RiskMedium,
					Decision:       ruleDecision(p.Rules.Network.Action, RiskMedium, p),
					Evidence:       "redirect-following flag may traverse non-allowlisted hosts",
					Recommendation: "Disable redirect-following or pin the final host in the allowlist",
				})
			}
		}
	}
	return out
}
