// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.

package safety

import (
	"net"
	"net/url"
	"regexp"
	"strings"
)

var explicitURLPattern = regexp.MustCompile(
	`(?i)\b[a-z][a-z0-9+.-]*://[^\s"'<>]+`,
)

var networkCommands = map[string]struct{}{
	"curl": {}, "wget": {}, "nc": {}, "netcat": {}, "ssh": {},
	"scp": {}, "sftp": {}, "ftp": {}, "git": {},
}

var nonNetworkCommands = map[string]struct{}{
	"cat": {}, "echo": {}, "false": {}, "grep": {}, "head": {},
	"ls": {}, "pwd": {}, "tail": {}, "test": {}, "true": {}, "wc": {},
}

func scanNetwork(policy Policy, segments [][]string) []Finding {
	var findings []Finding
	for _, argv := range segments {
		if len(argv) == 0 {
			continue
		}
		classification := classifyNetworkCommand(argv[0])
		if classification == networkCommandNone {
			continue
		}
		if classification == networkCommandUnknown {
			if unknownNetworkSignal(argv) {
				findings = append(findings, newFinding(
					DecisionNeedsHumanReview, RiskMedium, "network.unknown_client",
					"unknown executable receives network-shaped arguments",
					"review the executable semantics or use a recognized network tool",
				))
			}
			continue
		}
		if finding, ok := destinationOverrideFinding(argv); ok {
			findings = append(findings, finding)
		}
		if finding, ok := networkConfigFinding(argv); ok {
			findings = append(findings, finding)
		}

		for i, arg := range argv[1:] {
			host, ok := explicitHost(arg)
			if !ok && !networkOptionValue(argv, i+1) {
				host, ok = schemelessHost(arg)
			}
			if finding, denied := networkDestinationFinding(policy, host); ok && denied {
				findings = append(findings, finding)
			}
		}
	}
	return findings
}

type networkCommandClassification int

const (
	networkCommandUnknown networkCommandClassification = iota
	networkCommandNone
	networkCommandKnown
)

func classifyNetworkCommand(command string) networkCommandClassification {
	base := commandBase(command)
	if _, ok := networkCommands[base]; ok || strings.Contains(base, "fetch") ||
		strings.Contains(base, "download") {
		return networkCommandKnown
	}
	if _, ok := nonNetworkCommands[base]; ok {
		return networkCommandNone
	}
	return networkCommandUnknown
}

func unknownNetworkSignal(argv []string) bool {
	for _, arg := range argv[1:] {
		if _, ok := explicitHost(arg); ok {
			return true
		}
		name := strings.ToLower(strings.SplitN(arg, "=", 2)[0])
		switch name {
		case "--resolve", "--connect-to", "--proxy", "--preproxy",
			"--proxy-command", "--proxycommand":
			return true
		}
	}
	return false
}

func scanNetworkText(policy Policy, text string) []Finding {
	var findings []Finding
	for _, candidate := range explicitURLPattern.FindAllString(text, -1) {
		candidate = strings.TrimRight(candidate, ".,;:)]}")
		if host, ok := explicitHost(candidate); ok {
			if finding, denied := networkDestinationFinding(policy, host); denied {
				findings = append(findings, finding)
			}
		}
	}
	return findings
}

func destinationOverrideFinding(argv []string) (Finding, bool) {
	base := commandBase(argv[0])
	for i := 1; i < len(argv); i++ {
		arg := strings.ToLower(argv[i])
		if base == "ssh" && (arg == "-j" || strings.HasPrefix(arg, "-j") ||
			arg == "-oproxycommand" || strings.HasPrefix(arg, "-oproxycommand=") ||
			arg == "-oproxyjump" || strings.HasPrefix(arg, "-oproxyjump=") ||
			arg == "-ohostname" || strings.HasPrefix(arg, "-ohostname=") ||
			(arg == "-o" && i+1 < len(argv) && sshDestinationOverrideOption(argv[i+1]))) {
			return newFinding(
				DecisionDeny, RiskHigh, "network.destination_override",
				"SSH option can replace or relay the network destination",
				"remove ProxyCommand or ProxyJump and connect directly to an allowlisted host",
			), true
		}
		name := strings.SplitN(arg, "=", 2)[0]
		switch name {
		case "--resolve", "--connect-to", "--proxy", "--preproxy",
			"--proxy-command", "--proxycommand":
			return newFinding(
				DecisionDeny, RiskHigh, "network.destination_override",
				"network option can replace the effective destination",
				"remove destination-changing options and use an allowlisted URL directly",
			), true
		}
		if (base == "curl" || base == "wget") && name == "-x" {
			return newFinding(
				DecisionDeny, RiskHigh, "network.destination_override",
				"network option can replace the effective destination",
				"remove destination-changing options and use an allowlisted URL directly",
			), true
		}
	}
	return Finding{}, false
}

func sshDestinationOverrideOption(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "proxycommand=") ||
		strings.HasPrefix(value, "proxyjump=") ||
		strings.HasPrefix(value, "hostname=")
}

func networkConfigFinding(argv []string) (Finding, bool) {
	base := commandBase(argv[0])
	if base == "ssh" {
		for _, arg := range argv[1:] {
			if arg == "-F" || strings.HasPrefix(arg, "-F") {
				return newFinding(
					DecisionNeedsHumanReview, RiskMedium, "network.config",
					"SSH loads options from a configuration file",
					"review the SSH configuration and connect directly to an allowlisted host",
				), true
			}
		}
		return Finding{}, false
	}
	if base != "curl" && base != "wget" {
		return Finding{}, false
	}
	for _, arg := range argv[1:] {
		longName := strings.ToLower(strings.SplitN(arg, "=", 2)[0])
		shortConfig := base == "curl" && (arg == "-K" || strings.HasPrefix(arg, "-K"))
		if longName == "--config" || shortConfig {
			return newFinding(
				DecisionNeedsHumanReview, RiskMedium, "network.config",
				"network client loads options from a configuration file",
				"review the configuration file or specify the allowlisted destination directly",
			), true
		}
	}
	return Finding{}, false
}

func explicitHost(candidate string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(candidate))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return "", false
	}
	return normalizeHost(parsed.Hostname()), true
}

func schemelessHost(candidate string) (string, bool) {
	candidate = strings.TrimSpace(strings.Trim(candidate, `"'`))
	if candidate == "" || strings.HasPrefix(candidate, "-") || strings.Contains(candidate, "=") {
		return "", false
	}
	parsed, err := url.Parse("//" + candidate)
	if err == nil && probableHost(parsed.Hostname()) {
		return normalizeHost(parsed.Hostname()), true
	}

	hostPort := strings.SplitN(candidate, "/", 2)[0]
	if host, _, err := net.SplitHostPort(hostPort); err == nil && probableHost(host) {
		return normalizeHost(host), true
	}
	if index := strings.LastIndex(hostPort, ":"); index > 0 && probableHost(hostPort[:index]) {
		return normalizeHost(hostPort[:index]), true
	}
	return "", false
}

func probableHost(host string) bool {
	host = normalizeHost(host)
	if host == "" {
		return false
	}
	if net.ParseIP(strings.Trim(host, "[]")) != nil || host == "localhost" {
		return true
	}
	return strings.Contains(host, ".") && !strings.ContainsAny(host, " \\/")
}

func normalizeHost(host string) string {
	return strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
}

func networkDestinationFinding(policy Policy, host string) (Finding, bool) {
	if networkHostAllowed(host, policy.NetworkAllowlist) {
		return Finding{}, false
	}
	return newFinding(
		DecisionDeny, RiskHigh, "network.destination",
		"network destination is not allowlisted: "+host,
		"add the trusted host to network_allowlist or remove the network request",
	), true
}

func networkHostAllowed(host string, allowlist []string) bool {
	host = normalizeHost(host)
	for _, allowed := range allowlist {
		allowed = normalizeHost(allowed)
		if allowed != "" && (host == allowed || strings.HasSuffix(host, "."+allowed)) {
			return true
		}
	}
	return false
}

func networkOptionValue(argv []string, index int) bool {
	if index <= 1 {
		return false
	}
	rawPrevious := argv[index-1]
	if rawPrevious == "-F" || rawPrevious == "-K" || rawPrevious == "-J" {
		return true
	}
	previous := strings.ToLower(strings.SplitN(rawPrevious, "=", 2)[0])
	switch previous {
	case "-h", "--header", "-a", "--user-agent", "-d", "--data",
		"--data-raw", "--data-binary", "-o", "--output", "-u", "--user",
		"--resolve", "--connect-to", "--proxy", "--preproxy", "-x",
		"--config", "-j":
		return true
	}
	return false
}
