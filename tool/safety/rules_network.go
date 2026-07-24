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

// networkFlagFindings detects transport and redirect flags that bypass
// URL-level allowlists.
func networkFlagFindings(a *analysis, p Policy) []Finding {
	if a == nil || a.Pipeline == nil {
		return nil
	}
	var out []Finding
	for _, argv := range a.Pipeline.Commands {
		if len(argv) == 0 {
			continue
		}
		out = append(out, networkCommandFlagFindings(argv, p)...)
	}
	return out
}

func networkCommandFlagFindings(
	argv []string,
	p Policy,
) []Finding {
	base := basenameLower(argv[0])
	switch base {
	case "ssh", "scp", "sftp":
		return sshDangerousOptionFindings(base, argv, p)
	}
	if base != "curl" && base != "wget" &&
		base != "aria2" && base != "aria2c" {
		return nil
	}
	var out []Finding
	for _, flag := range argv[1:] {
		if isTransportOverrideFlag(base, flag) {
			out = append(out, Finding{
				RuleID:         "network.dangerous_flag",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.Network.Action, RiskHigh, p),
				Evidence:       "network transport override bypasses URL allowlist",
				Recommendation: "Refuse the request; require explicit URL targets on the allowlist",
			})
			continue
		}
		if isRedirectFlag(base, flag) {
			out = append(out, Finding{
				RuleID:         "network.dangerous_flag",
				RiskLevel:      RiskMedium,
				Decision:       ruleDecision(p.Rules.Network.Action, RiskMedium, p),
				Evidence:       "redirect-following flag may traverse non-allowlisted hosts",
				Recommendation: "Disable redirect-following or pin the final host in the allowlist",
			})
		}
	}
	return out
}

func isTransportOverrideFlag(base, flag string) bool {
	if isCommonTransportOverrideFlag(flag) {
		return true
	}
	switch base {
	case "curl":
		return isCurlTransportOverrideFlag(flag)
	case "wget":
		return isWgetTransportOverrideFlag(flag)
	case "aria2", "aria2c":
		return isAriaTransportOverrideFlag(flag)
	}
	return false
}

func isCommonTransportOverrideFlag(flag string) bool {
	return flag == "-K" || matchesAnyLongOption(
		flag, "--config", "--resolve", "--connect-to",
		"--unix-socket", "--abstract-unix-socket",
	)
}

func isCurlTransportOverrideFlag(flag string) bool {
	return isCurlProxyOverrideFlag(flag) ||
		isCurlResolverOverrideFlag(flag) ||
		isCurlStateOverrideFlag(flag)
}

func isCurlProxyOverrideFlag(flag string) bool {
	return flag == "-x" ||
		curlShortOptionPresent(flag, 'x') ||
		curlShortOptionPresent(flag, 'K') ||
		matchesAnyLongOption(
			flag, "--proxy", "--preproxy", "--socks4",
			"--socks4a", "--socks5", "--socks5-hostname",
		)
}

func isCurlResolverOverrideFlag(flag string) bool {
	return matchesAnyLongOption(
		flag, "--doh-url", "--dns-servers", "--dns-interface",
		"--dns-ipv4-addr", "--dns-ipv6-addr",
	)
}

func isCurlStateOverrideFlag(flag string) bool {
	return matchesAnyLongOption(flag, "--alt-svc", "--hsts")
}

func isWgetTransportOverrideFlag(flag string) bool {
	return flag == "-e" || flag == "-i" || flag == "-H" ||
		shortOptionPresent(flag, 'e', wgetShortOptionsWithValue) ||
		shortOptionPresent(flag, 'i', wgetShortOptionsWithValue) ||
		shortOptionPresent(flag, 'H', wgetShortOptionsWithValue) ||
		matchesAnyLongOption(
			flag, "--execute", "--input-file", "--config",
			"--span-hosts", "--use-askpass",
		)
}

func isAriaTransportOverrideFlag(flag string) bool {
	return flag == "-i" || strings.HasPrefix(flag, "-i") ||
		flag == "--input-file" ||
		strings.HasPrefix(flag, "--input-file=") ||
		flag == "--conf-path" ||
		strings.HasPrefix(flag, "--conf-path=") ||
		flag == "--all-proxy" ||
		strings.HasPrefix(flag, "--all-proxy=") ||
		flag == "--http-proxy" ||
		strings.HasPrefix(flag, "--http-proxy=") ||
		flag == "--https-proxy" ||
		strings.HasPrefix(flag, "--https-proxy=") ||
		flag == "--ftp-proxy" ||
		strings.HasPrefix(flag, "--ftp-proxy=") ||
		flag == "--host-resolve" ||
		strings.HasPrefix(flag, "--host-resolve=")
}

func isRedirectFlag(base, flag string) bool {
	return flag == "-L" || flag == "--location" ||
		flag == "--location-trusted" ||
		strings.HasPrefix(flag, "--max-redirs") ||
		base == "curl" && curlShortOptionPresent(flag, 'L')
}

func sshDangerousOptionFindings(
	base string,
	argv []string,
	p Policy,
) []Finding {
	var out []Finding
	if base == "ssh" {
		for _, option := range []byte{'W', 'L', 'R', 'D'} {
			for _, value := range sshOptionValues(argv, option) {
				if value == "" {
					continue
				}
				out = append(out, Finding{
					RuleID:         "network.dangerous_flag",
					RiskLevel:      RiskHigh,
					Decision:       ruleDecision(p.Rules.Network.Action, RiskHigh, p),
					Evidence:       "ssh forwarding can reach non-allowlisted destinations",
					Recommendation: "Refuse SSH forwarding options or validate every forwarded destination",
				})
			}
		}
	}
	if base == "scp" || base == "sftp" {
		for _, value := range sshOptionValues(argv, 'S') {
			if value == "" {
				continue
			}
			out = append(out, Finding{
				RuleID:         "network.dangerous_flag",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.Network.Action, RiskHigh, p),
				Evidence:       "SSH client program override executes a local command",
				Recommendation: "Refuse SSH program overrides; use the standard client",
			})
		}
	}
	if base == "sftp" {
		for _, value := range sshOptionValues(argv, 'D') {
			if value == "" {
				continue
			}
			out = append(out, Finding{
				RuleID:         "network.dangerous_flag",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.Network.Action, RiskHigh, p),
				Evidence:       "SFTP server command override executes a local command",
				Recommendation: "Refuse SFTP server command overrides",
			})
		}
	}
	for _, configPath := range sshOptionValues(argv, 'F') {
		if configPath == "" || strings.EqualFold(configPath, "none") {
			continue
		}
		out = append(out, Finding{
			RuleID:         "network.dangerous_flag",
			RiskLevel:      RiskHigh,
			Decision:       ruleDecision(p.Rules.Network.Action, RiskHigh, p),
			Evidence:       "ssh configuration file can override destination or execute commands",
			Recommendation: "Refuse external SSH config files; use explicit allowlisted options",
		})
	}
	for _, setting := range sshOptionValues(argv, 'o') {
		name, value, ok := parseSSHSetting(setting)
		if !ok || strings.EqualFold(strings.TrimSpace(value), "none") {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "proxycommand", "localcommand", "knownhostscommand",
			"include", "localforward", "remoteforward",
			"dynamicforward", "stdioforward":
			out = append(out, Finding{
				RuleID:         "network.dangerous_flag",
				RiskLevel:      RiskHigh,
				Decision:       ruleDecision(p.Rules.Network.Action, RiskHigh, p),
				Evidence:       "ssh option executes a local command",
				Recommendation: "Refuse SSH command hooks; use a direct allowlisted destination",
			})
		}
	}
	return out
}

// curlShortOptionPresent parses a curl short-option bundle until an
// option that consumes the remaining token. This avoids mistaking
// letters inside an attached value (for example -Axyz) for options.
func curlShortOptionPresent(tok string, target byte) bool {
	return shortOptionPresent(tok, target, curlShortOptionsWithValue)
}

func shortOptionPresent(
	tok string,
	target byte,
	optionsWithValue string,
) bool {
	if len(tok) <= 2 || tok[0] != '-' || tok[1] == '-' {
		return false
	}
	for i := 1; i < len(tok); i++ {
		option := tok[i]
		if option == target {
			return true
		}
		if strings.ContainsRune(
			optionsWithValue, rune(option),
		) {
			return false
		}
	}
	return false
}
