//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

func (s *DefaultScanner) scanProxyEnv(key, value string) []Finding {
	if !isProxyEnv(key) || strings.TrimSpace(value) == "" {
		return nil
	}
	host, ok := proxyHost(value)
	if !ok {
		decision := DecisionAsk
		if len(s.policy.NetworkAllowlist) > 0 {
			decision = DecisionDeny
		}
		return []Finding{{
			RuleID:         "network.proxy_invalid",
			RiskLevel:      RiskHigh,
			Decision:       decision,
			Evidence:       key,
			Recommendation: "use a proxy value with an explicit resolvable host",
		}}
	}
	if s.hostAllowed(host) {
		return nil
	}
	decision := DecisionAsk
	rule := "network.proxy_external_domain"
	if len(s.policy.NetworkAllowlist) > 0 {
		decision = DecisionDeny
		rule = "network.proxy_non_allowlisted_domain"
	}
	return []Finding{{
		RuleID:         rule,
		RiskLevel:      RiskHigh,
		Decision:       decision,
		Evidence:       host,
		Recommendation: "add the proxy host to network_allowlist or remove the proxy override",
	}}
}

func isProxyEnv(key string) bool {
	switch strings.ToUpper(strings.TrimSpace(key)) {
	case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY":
		return true
	default:
		return false
	}
}

func proxyHost(value string) (string, bool) {
	raw := strings.Trim(strings.TrimSpace(value), `"'`)
	if raw == "" {
		return "", false
	}
	if !strings.Contains(raw, "://") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" {
		return "", false
	}
	return strings.ToLower(strings.TrimSuffix(u.Hostname(), ".")), true
}

func (s *DefaultScanner) scanNetwork(cmd string, argv []string) []Finding {
	text := strings.Join(argv, " ")
	hosts := extractNetworkHosts(cmd, argv)
	if !isNetworkCommand(cmd) && len(hosts) == 0 {
		return nil
	}
	if hasNetworkDestinationOverride(cmd, argv) {
		decision := DecisionAsk
		if len(s.policy.NetworkAllowlist) > 0 {
			decision = DecisionDeny
		}
		return []Finding{{
			RuleID:         "network.destination_override",
			RiskLevel:      RiskHigh,
			Decision:       decision,
			Evidence:       "curl destination override requires an explicit network review",
			Recommendation: "remove curl destination-routing options or review the effective destination",
		}}
	}
	if len(hosts) == 0 {
		decision := DecisionAsk
		if len(s.policy.NetworkAllowlist) > 0 {
			decision = DecisionDeny
		}
		if cmd == "nc" || cmd == "netcat" || cmd == "ssh" || cmd == "scp" {
			decision = DecisionDeny
		}
		return []Finding{{
			RuleID:         "network.external_tool",
			RiskLevel:      RiskHigh,
			Decision:       decision,
			Evidence:       text,
			Recommendation: "review network-capable commands and prefer allowlisted hosts",
		}}
	}
	var findings []Finding
	for _, host := range hosts {
		if s.hostAllowed(host) {
			continue
		}
		decision := DecisionAsk
		rule := "network.external_domain"
		if isHighRiskNetworkCommand(cmd) {
			decision = DecisionDeny
			rule = "network.external_tool"
		}
		if len(s.policy.NetworkAllowlist) > 0 {
			decision = DecisionDeny
			rule = "network.non_allowlisted_domain"
		}
		if isPrivateHost(host) {
			rule = "network.private_address"
			if decision != DecisionDeny {
				decision = DecisionAsk
			}
		}
		findings = append(findings, Finding{
			RuleID:         rule,
			RiskLevel:      RiskHigh,
			Decision:       decision,
			Evidence:       host,
			Recommendation: "add the host to network_allowlist or require human review",
		})
	}
	return findings
}

func (s *DefaultScanner) scanTextNetwork(text string) []Finding {
	hosts := extractHosts(text)
	if len(hosts) == 0 {
		return []Finding{{
			RuleID:         "network.external_tool",
			RiskLevel:      RiskHigh,
			Decision:       DecisionAsk,
			Evidence:       "text contains network-capable command",
			Recommendation: "review generated network access before execution",
		}}
	}
	var findings []Finding
	for _, host := range hosts {
		if s.hostAllowed(host) {
			continue
		}
		decision := DecisionAsk
		rule := "network.external_domain"
		if len(s.policy.NetworkAllowlist) > 0 {
			decision = DecisionDeny
			rule = "network.non_allowlisted_domain"
		}
		if isPrivateHost(host) {
			rule = "network.private_address"
		}
		findings = append(findings, Finding{
			RuleID:         rule,
			RiskLevel:      RiskHigh,
			Decision:       decision,
			Evidence:       host,
			Recommendation: "add the host to network_allowlist or require human review",
		})
	}
	return findings
}

func isNetworkCommand(cmd string) bool {
	switch cmd {
	case "curl", "wget", "nc", "netcat", "ssh", "scp":
		return true
	default:
		return false
	}
}

func hasNetworkDestinationOverride(cmd string, argv []string) bool {
	if cmd != "curl" {
		return false
	}
	options := []string{
		"--resolve", "--connect-to", "--unix-socket", "--abstract-unix-socket",
		"-x", "--proxy", "--proxy1.0", "--preproxy", "--socks4", "--socks4a",
		"--socks5", "--socks5-hostname",
	}
	for i := 1; i < len(argv); i++ {
		arg := strings.ToLower(strings.TrimSpace(argv[i]))
		for _, option := range options {
			if arg == option {
				return i+1 < len(argv) && strings.TrimSpace(argv[i+1]) != ""
			}
			if strings.HasPrefix(arg, option+"=") {
				return strings.TrimSpace(strings.TrimPrefix(arg, option+"=")) != ""
			}
		}
	}
	return false
}

func isHighRiskNetworkCommand(cmd string) bool {
	switch cmd {
	case "nc", "netcat", "ssh", "scp":
		return true
	default:
		return false
	}
}

var urlLikePattern = regexp.MustCompile(`(?i)\b(?:https?|ftp)://[^\s"'<>]+`)

func extractHosts(text string) []string {
	seen := make(map[string]struct{})
	var hosts []string
	for _, raw := range urlLikePattern.FindAllString(text, -1) {
		u, err := url.Parse(raw)
		if err != nil || u.Hostname() == "" {
			continue
		}
		hosts = appendHost(hosts, seen, u.Hostname())
	}
	return hosts
}

func extractNetworkHosts(cmd string, argv []string) []string {
	seen := make(map[string]struct{})
	var hosts []string
	hosts = appendHosts(hosts, seen, extractHosts(strings.Join(argv, " "))...)
	for _, arg := range argv[1:] {
		host, ok := networkArgHost(cmd, arg)
		if !ok {
			continue
		}
		hosts = appendHost(hosts, seen, host)
	}
	return hosts
}

func networkArgHost(cmd, arg string) (string, bool) {
	arg = strings.Trim(strings.TrimSpace(arg), `"'`)
	if arg == "" || strings.HasPrefix(arg, "-") {
		return "", false
	}
	if strings.Contains(arg, "://") {
		u, err := url.Parse(arg)
		if err != nil || u.Hostname() == "" {
			return "", false
		}
		return u.Hostname(), true
	}
	if !isNetworkCommand(cmd) && cmd != "git" {
		return "", false
	}
	switch cmd {
	case "curl", "wget":
		if host, _, ok := strings.Cut(arg, "/"); ok {
			arg = host
		}
		if host, _, ok := strings.Cut(arg, ":"); ok {
			arg = host
		}
	case "ssh", "scp":
		if userHost, _, ok := strings.Cut(arg, ":"); ok {
			arg = userHost
		}
		if _, host, ok := strings.Cut(arg, "@"); ok {
			arg = host
		}
	case "nc", "netcat":
	}
	arg = strings.TrimSuffix(arg, ".")
	if !looksLikeHost(arg) {
		return "", false
	}
	return arg, true
}

func appendHosts(hosts []string, seen map[string]struct{}, values ...string) []string {
	for _, host := range values {
		hosts = appendHost(hosts, seen, host)
	}
	return hosts
}

func appendHost(hosts []string, seen map[string]struct{}, host string) []string {
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
	if host == "" {
		return hosts
	}
	if _, ok := seen[host]; ok {
		return hosts
	}
	seen[host] = struct{}{}
	return append(hosts, host)
}

func looksLikeHost(s string) bool {
	if s == "localhost" || net.ParseIP(s) != nil {
		return true
	}
	if strings.ContainsAny(s, ":/\\") {
		return false
	}
	parts := strings.Split(s, ".")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		if part == "" {
			return false
		}
		for _, r := range part {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' {
				return false
			}
		}
	}
	return true
}

func (s *DefaultScanner) hostAllowed(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, pattern := range s.policy.NetworkAllowlist {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		pattern = strings.TrimSuffix(pattern, ".")
		if pattern == "" {
			continue
		}
		if strings.HasPrefix(pattern, ".") {
			if strings.HasSuffix(host, pattern) ||
				host == strings.TrimPrefix(pattern, ".") {
				return true
			}
			continue
		}
		if host == pattern {
			return true
		}
	}
	return false
}

func isPrivateHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func containsDownloaderOrURL(lower string) bool {
	return strings.Contains(lower, "download") ||
		strings.Contains(lower, "curl ") ||
		strings.Contains(lower, "wget ") ||
		strings.Contains(lower, "http://") ||
		strings.Contains(lower, "https://")
}
