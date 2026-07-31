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
	for i := 1; i < len(argv); i++ {
		if argv[i] == "--" {
			break
		}
		option := parseNetworkOption(cmd, argv, i)
		if curlDestinationOverrideOption(option.name) &&
			strings.TrimSpace(option.value) != "" {
			return true
		}
		if option.consumesNext {
			i++
		}
	}
	return false
}

func curlDestinationOverrideOption(option string) bool {
	switch option {
	case "-x", "--resolve", "--connect-to", "--unix-socket",
		"--abstract-unix-socket", "--proxy", "--proxy1.0", "--preproxy",
		"--socks4", "--socks4a", "--socks5", "--socks5-hostname":
		return true
	default:
		return false
	}
}

type networkOption struct {
	name         string
	value        string
	consumesNext bool
}

func parseNetworkOption(cmd string, argv []string, index int) networkOption {
	arg := strings.TrimSpace(argv[index])
	if arg == "" || arg == "-" || !strings.HasPrefix(arg, "-") {
		return networkOption{}
	}
	if arg == "--" {
		return networkOption{name: arg}
	}
	if strings.HasPrefix(arg, "--") {
		name, value, hasValue := strings.Cut(arg, "=")
		name = strings.ToLower(name)
		if hasValue {
			return networkOption{name: name, value: value}
		}
		option := networkOption{name: name}
		if networkLongOptionTakesValue(cmd, name) && index+1 < len(argv) {
			option.value = argv[index+1]
			option.consumesNext = true
		}
		return option
	}

	cluster := strings.TrimPrefix(arg, "-")
	for i := 0; i < len(cluster); i++ {
		if !networkShortOptionTakesValue(cmd, cluster[i]) {
			continue
		}
		option := networkOption{name: "-" + string(cluster[i])}
		if i+1 < len(cluster) {
			option.value = cluster[i+1:]
		} else if index+1 < len(argv) {
			option.value = argv[index+1]
			option.consumesNext = true
		}
		return option
	}
	return networkOption{name: arg}
}

func networkShortOptionTakesValue(cmd string, option byte) bool {
	switch cmd {
	case "curl":
		return strings.ContainsRune("AbcCdDeEFHKmoPQrTtUuwxXyYz", rune(option))
	case "wget":
		return strings.ContainsRune("eoaiBtOTwQPURADlIX", rune(option))
	default:
		return false
	}
}

func networkLongOptionTakesValue(cmd, option string) bool {
	switch cmd {
	case "curl":
		return curlLongOptionTakesValue(option)
	case "wget":
		return wgetLongOptionTakesValue(option)
	default:
		return false
	}
}

func curlLongOptionTakesValue(option string) bool {
	switch option {
	case "--abstract-unix-socket", "--alt-svc", "--aws-sigv4", "--cacert",
		"--capath", "--cert", "--cert-type", "--ciphers", "--config",
		"--connect-timeout", "--connect-to", "--cookie", "--cookie-jar",
		"--create-file-mode", "--data", "--data-ascii", "--data-binary",
		"--data-raw", "--data-urlencode", "--delegation", "--dns-interface",
		"--dns-ipv4-addr", "--dns-ipv6-addr", "--doh-url", "--dump-header",
		"--egd-file", "--engine", "--etag-compare", "--etag-save",
		"--expect100-timeout", "--expand-data", "--expand-data-binary",
		"--expand-form", "--expand-form-string", "--expand-header",
		"--expand-url", "--expand-variable", "--form", "--form-string",
		"--ftp-account", "--ftp-alternative-to-user", "--ftp-method",
		"--ftp-port", "--happy-eyeballs-timeout-ms", "--haproxy-clientip",
		"--header", "--hostpubmd5", "--hsts", "--interface", "--json",
		"--key", "--key-type", "--krb", "--libcurl", "--limit-rate",
		"--local-port", "--login-options", "--mail-auth", "--mail-from",
		"--mail-rcpt", "--max-filesize", "--max-redirs", "--max-time",
		"--netrc-file", "--noproxy", "--oauth2-bearer", "--output",
		"--output-dir", "--parallel-max", "--pass", "--pinnedpubkey",
		"--preproxy", "--proto", "--proto-default", "--proto-redir",
		"--proxy", "--proxy-cacert", "--proxy-capath", "--proxy-cert",
		"--proxy-cert-type", "--proxy-ciphers", "--proxy-crlfile",
		"--proxy-header", "--proxy-key", "--proxy-key-type", "--proxy-pass",
		"--proxy-service-name", "--proxy-tls13-ciphers", "--proxy-user",
		"--proxy1.0", "--pubkey", "--quote", "--random-file", "--range",
		"--rate", "--referer", "--request", "--request-target", "--resolve",
		"--retry", "--retry-delay", "--retry-max-time", "--service-name",
		"--speed-limit", "--speed-time", "--socks4", "--socks4a",
		"--socks5", "--socks5-gssapi-service", "--socks5-hostname",
		"--stderr", "--telnet-option", "--tftp-blksize", "--time-cond",
		"--tls-max", "--tls13-ciphers", "--unix-socket", "--upload-file",
		"--url", "--url-query", "--user", "--user-agent", "--variable",
		"--write-out":
		return true
	default:
		return false
	}
}

func wgetLongOptionTakesValue(option string) bool {
	switch option {
	case "--accept", "--append-output", "--backup-converted", "--base",
		"--bind-address", "--ca-certificate", "--ca-directory", "--certificate",
		"--certificate-type", "--config", "--connect-timeout", "--cut-dirs",
		"--directory-prefix", "--domains", "--exclude-directories",
		"--exclude-domains", "--execute", "--follow-tags", "--ftp-password",
		"--ftp-user", "--header", "--ignore-tags", "--include-directories",
		"--input-file", "--level", "--limit-rate", "--load-cookies",
		"--local-encoding", "--output-document", "--output-file", "--password",
		"--post-data", "--post-file", "--private-key", "--private-key-type",
		"--progress", "--protocol-directories", "--proxy-password", "--proxy-user",
		"--quota", "--read-timeout", "--referer", "--reject", "--remote-encoding",
		"--restrict-file-names", "--retry-on-http-error", "--save-cookies",
		"--secure-protocol", "--timeout", "--tries", "--user", "--user-agent",
		"--wait", "--waitretry":
		return true
	default:
		return false
	}
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
	optionsEnded := false
	for i := 1; i < len(argv); i++ {
		arg := argv[i]
		if (cmd == "curl" || cmd == "wget") && !optionsEnded {
			if arg == "--" {
				optionsEnded = true
				continue
			}
			if option := parseNetworkOption(cmd, argv, i); option.name != "" {
				if networkOptionIsDestination(cmd, option.name) {
					hosts = appendNetworkArgumentHosts(hosts, seen, cmd, option.value)
				}
				if option.consumesNext {
					i++
				}
				continue
			}
		}
		hosts = appendNetworkArgumentHosts(hosts, seen, cmd, arg)
	}
	return hosts
}

func networkOptionIsDestination(cmd, option string) bool {
	switch cmd {
	case "curl":
		return option == "--url" || option == "--expand-url" || option == "--doh-url"
	case "wget":
		return option == "-B" || option == "--base"
	default:
		return false
	}
}

func appendNetworkArgumentHosts(
	hosts []string,
	seen map[string]struct{},
	cmd string,
	arg string,
) []string {
	hosts = appendHosts(hosts, seen, extractHosts(arg)...)
	if host, ok := networkArgHost(cmd, arg); ok {
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
