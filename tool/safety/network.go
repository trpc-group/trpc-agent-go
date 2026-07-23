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
	"strconv"
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
		destinations, unresolved := networkDestinations(argv)
		var rewritePrefixes []string
		if commandBase(argv[0]) == "git" {
			var rewriteFindings []Finding
			rewriteFindings, rewritePrefixes = scanGitURLRewrites(
				policy, argv, destinations,
			)
			findings = append(findings, rewriteFindings...)
		}
		for _, destination := range destinations {
			if destinationUsesRewrite(destination, rewritePrefixes) {
				continue
			}
			if isFileURL(destination) {
				continue
			}
			host, ok := knownDestinationHost(destination)
			if !ok {
				unresolved = true
				continue
			}
			if finding, denied := networkDestinationFinding(policy, host); denied {
				findings = append(findings, finding)
			}
		}
		if unresolved {
			findings = append(findings, newFinding(
				DecisionNeedsHumanReview, RiskMedium, "network.destination_unparsed",
				"network destination could not be parsed conservatively",
				"use an explicit allowlisted hostname or URL",
			))
		}
	}
	return findings
}

// networkDestinations isolates only the operands that each supported client
// interprets as remote destinations. Local output and checkout operands must
// not be fed to hostname classification.
func networkDestinations(argv []string) ([]string, bool) {
	if len(argv) == 0 {
		return nil, false
	}
	switch commandBase(argv[0]) {
	case "ssh":
		return firstPositional(argv[1:], sshValueOptions), false
	case "scp":
		return scpRemoteDestinations(argv[1:], scpValueOptions), false
	case "sftp":
		return firstPositional(argv[1:], sftpValueOptions), false
	case "git":
		return gitNetworkDestinations(argv[1:])
	case "nc", "netcat":
		return netcatDestination(argv[1:]), false
	case "ftp":
		return firstPositional(argv[1:], ftpValueOptions), false
	default:
		return webClientDestinations(commandBase(argv[0]), argv[1:])
	}
}

var sshValueOptions = map[string]struct{}{
	"-B": {}, "-b": {}, "-c": {}, "-D": {}, "-E": {}, "-e": {}, "-F": {},
	"-I": {}, "-i": {}, "-J": {}, "-L": {}, "-l": {}, "-m": {},
	"-O": {}, "-o": {}, "-p": {}, "-Q": {}, "-R": {}, "-S": {},
	"-W": {}, "-w": {},
}

var scpValueOptions = map[string]struct{}{
	"-c": {}, "-D": {}, "-F": {}, "-i": {}, "-J": {}, "-l": {},
	"-o": {}, "-P": {}, "-S": {}, "-X": {},
}

var sftpValueOptions = map[string]struct{}{
	"-B": {}, "-b": {}, "-c": {}, "-D": {}, "-F": {}, "-i": {},
	"-J": {}, "-l": {}, "-o": {}, "-P": {}, "-R": {}, "-S": {}, "-X": {},
}

var ftpValueOptions = map[string]struct{}{
	"-P": {}, "-p": {},
}

func firstPositional(args []string, valueOptions map[string]struct{}) []string {
	positionals := positionalTokens(args, valueOptions)
	if len(positionals) == 0 {
		return nil
	}
	return positionals[:1]
}

func positionalTokens(args []string, valueOptions map[string]struct{}) []string {
	var positionals []string
	options := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		if options && strings.HasPrefix(arg, "-") && arg != "-" {
			if _, consumes := valueOptions[arg]; consumes && i+1 < len(args) {
				i++
			}
			continue
		}
		positionals = append(positionals, arg)
	}
	return positionals
}

func webClientDestinations(base string, args []string) ([]string, bool) {
	valueOptions := map[string]struct{}{
		"--cacert": {}, "--capath": {}, "--cert": {}, "--config": {},
		"--connect-timeout": {}, "--connect-to": {}, "--cookie": {}, "--cookie-jar": {},
		"--data": {}, "--data-binary": {}, "--data-raw": {}, "--directory-prefix": {},
		"--execute": {}, "--form": {}, "--header": {}, "--interface": {}, "--json": {}, "--key": {},
		"--limit-rate": {}, "--max-filesize": {}, "--max-redirs": {}, "--max-time": {},
		"--method": {}, "--output": {}, "--post-data": {}, "--post-file": {},
		"--output-document": {}, "--preproxy": {}, "--proxy": {},
		"--read-timeout": {}, "--referer": {}, "--request": {}, "--resolve": {},
		"--retry": {}, "--retry-delay": {}, "--speed-limit": {}, "--speed-time": {},
		"--timeout": {}, "--tries": {}, "--upload-file": {}, "--user": {},
		"--user-agent": {}, "--wait": {}, "--waitretry": {},
	}
	curlValues := []string{
		"-A", "-b", "-c", "-C", "-d", "-D", "-e", "-E", "-F", "-H", "-K",
		"-m", "-o", "-P", "-Q", "-r", "-T", "-u", "-w", "-x", "-X", "-Y",
	}
	wgetValues := []string{
		"-a", "-A", "-B", "-D", "-e", "-i", "-l", "-O", "-o", "-P",
		"-Q", "-R", "-t", "-T", "-U", "-w",
	}
	values := curlValues
	if base == "wget" {
		values = wgetValues
	} else if base != "curl" {
		values = append(append([]string{}, curlValues...), wgetValues...)
	}
	for _, option := range values {
		valueOptions[option] = struct{}{}
	}
	flagOptions := map[string]struct{}{
		"--compressed": {}, "--content-disposition": {}, "--continue": {},
		"--fail": {}, "--fail-with-body": {}, "--globoff": {}, "--head": {},
		"--help": {}, "--include": {}, "--insecure": {}, "--inet4-only": {},
		"--inet6-only": {}, "--ipv4": {}, "--ipv6": {}, "--location": {},
		"--no-buffer": {}, "--no-check-certificate": {}, "--no-clobber": {},
		"--no-verbose": {}, "--parallel": {}, "--quiet": {}, "--recursive": {},
		"--remote-header-name": {}, "--remote-name": {}, "--show-error": {},
		"--silent": {}, "--spider": {}, "--trust-server-names": {},
		"--verbose": {}, "--version": {},
	}
	curlShortFlags := "012346VZfghiIkLNOnpqRrsSv"
	wgetShortFlags := "cdHhNpqrSV"
	shortFlags := curlShortFlags
	if base == "wget" {
		shortFlags = wgetShortFlags
	} else if base != "curl" {
		shortFlags += wgetShortFlags
	}
	var destinations []string
	unresolved := false
	options := true
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if options && arg == "--" {
			options = false
			continue
		}
		if options && (arg == "--url" || arg == "-url") && i+1 < len(args) {
			destinations = append(destinations, args[i+1])
			i++
			continue
		}
		if options && strings.HasPrefix(arg, "--url=") {
			destinations = append(destinations, strings.TrimPrefix(arg, "--url="))
			continue
		}
		if options && strings.HasPrefix(arg, "-") && arg != "-" {
			name := strings.SplitN(arg, "=", 2)[0]
			if strings.HasPrefix(arg, "--") {
				if _, consumes := valueOptions[name]; consumes {
					if !strings.Contains(arg, "=") && i+1 < len(args) {
						i++
					}
					continue
				}
				if _, flag := flagOptions[arg]; flag {
					continue
				}
				unresolved = true
				if !strings.Contains(arg, "=") && i+1 < len(args) &&
					!strings.HasPrefix(args[i+1], "-") {
					i++
				}
				continue
			}
			consumesNext, recognized := parseShortOptions(arg, valueOptions, shortFlags)
			if recognized {
				if consumesNext && i+1 < len(args) {
					i++
				}
				continue
			}
			unresolved = true
			if !strings.Contains(arg, "=") && i+1 < len(args) &&
				!strings.HasPrefix(args[i+1], "-") {
				i++
			}
			continue
		}
		destinations = append(destinations, arg)
	}
	return destinations, unresolved
}

func parseShortOptions(
	arg string,
	valueOptions map[string]struct{},
	allowedFlags string,
) (bool, bool) {
	if len(arg) < 2 || !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false, false
	}
	options := arg[1:]
	for index, flag := range options {
		if _, consumes := valueOptions["-"+string(flag)]; consumes {
			return index == len(options)-1, true
		}
		if !strings.ContainsRune(allowedFlags, flag) {
			return false, false
		}
	}
	return false, true
}

func scpRemoteDestinations(args []string, valueOptions map[string]struct{}) []string {
	positionals := positionalTokens(args, valueOptions)
	var destinations []string
	for _, operand := range positionals {
		if _, ok := scpRemoteHost(operand); ok {
			destinations = append(destinations, operand)
		}
	}
	return destinations
}

func gitNetworkDestinations(args []string) ([]string, bool) {
	globalValues := map[string]struct{}{"-C": {}, "-c": {}, "--git-dir": {}, "--work-tree": {}}
	subcommand, rest, ok := commandAndRest(args, globalValues)
	if !ok {
		return nil, false
	}
	valueOptions := map[string]struct{}{
		"-b": {}, "-c": {}, "-j": {}, "-o": {}, "-u": {}, "--branch": {}, "--config": {},
		"--deepen": {}, "--depth": {}, "--filter": {}, "--jobs": {}, "--origin": {},
		"--reference": {}, "--reference-if-able": {}, "--separate-git-dir": {},
		"--server-option": {}, "--shallow-exclude": {}, "--shallow-since": {},
		"--template": {}, "--upload-pack": {},
	}
	positionals := positionalTokens(rest, valueOptions)
	switch subcommand {
	case "clone":
		if len(positionals) == 0 {
			return nil, false
		}
		remote := positionals[0]
		if isExplicitNetworkURL(remote) {
			return []string{remote}, false
		}
		if _, ok := scpRemoteHost(remote); ok {
			return []string{remote}, false
		}
		return nil, false
	case "fetch", "pull", "push", "ls-remote":
		if len(positionals) == 0 {
			return nil, true
		}
		remote := positionals[0]
		if isExplicitNetworkURL(remote) {
			return []string{remote}, false
		}
		if _, ok := scpRemoteHost(remote); ok {
			return []string{remote}, false
		}
		return nil, true
	default:
		return nil, false
	}
}

func scanGitURLRewrites(
	policy Policy,
	argv []string,
	destinations []string,
) ([]Finding, []string) {
	var findings []Finding
	var prefixes []string
	for _, config := range gitConfigValues(argv[1:]) {
		key, prefix, _ := strings.Cut(config, "=")
		lowerKey := strings.ToLower(key)
		if !strings.HasPrefix(lowerKey, "url.") || !strings.HasSuffix(lowerKey, ".insteadof") {
			continue
		}
		base := key[len("url.") : len(key)-len(".insteadOf")]
		if prefix == "" || !destinationUsesRewriteList(destinations, prefix) {
			continue
		}
		prefixes = append(prefixes, prefix)
		if base == "" || isFileURL(base) {
			findings = append(findings, gitRewriteUnparsedFinding())
			continue
		}
		host, parsed := knownDestinationHost(base)
		if !parsed {
			findings = append(findings, gitRewriteUnparsedFinding())
			continue
		}
		if !networkHostAllowed(host, policy.NetworkAllowlist) {
			findings = append(findings, newFinding(
				DecisionDeny, RiskHigh, "network.destination_override",
				"Git URL rewrite changes the effective destination to a non-allowlisted host",
				"remove the rewrite or use an allowlisted remote directly",
			))
			continue
		}
		findings = append(findings, newFinding(
			DecisionNeedsHumanReview, RiskHigh, "network.destination_override",
			"Git URL rewrite changes the effective destination",
			"review the rewrite and use the allowlisted remote directly",
		))
	}
	return findings, prefixes
}

func destinationUsesRewriteList(destinations []string, prefix string) bool {
	for _, destination := range destinations {
		if strings.HasPrefix(destination, prefix) {
			return true
		}
	}
	return false
}

func gitConfigValues(args []string) []string {
	var configs []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "-c" {
			if i+1 < len(args) {
				configs = append(configs, args[i+1])
				i++
			}
			continue
		}
		if strings.HasPrefix(arg, "-c") && len(arg) > 2 {
			configs = append(configs, strings.TrimPrefix(arg, "-c"))
		}
	}
	return configs
}

func destinationUsesRewrite(destination string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if prefix != "" && strings.HasPrefix(destination, prefix) {
			return true
		}
	}
	return false
}

func gitRewriteUnparsedFinding() Finding {
	return newFinding(
		DecisionNeedsHumanReview, RiskHigh, "network.destination_unparsed",
		"Git URL rewrite destination could not be parsed conservatively",
		"remove the rewrite or use an explicit allowlisted remote",
	)
}

func commandAndRest(args []string, valueOptions map[string]struct{}) (string, []string, bool) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") && arg != "-" {
			if _, consumes := valueOptions[arg]; consumes && i+1 < len(args) {
				i++
			}
			continue
		}
		return strings.ToLower(arg), args[i+1:], true
	}
	return "", nil, false
}

func netcatDestination(args []string) []string {
	for _, arg := range args {
		if arg == "-l" || arg == "--listen" {
			return nil
		}
	}
	values := map[string]struct{}{
		"-i": {}, "-P": {}, "-p": {}, "-q": {}, "-s": {}, "-w": {}, "-x": {}, "-X": {},
	}
	return firstPositional(args, values)
}

func knownDestinationHost(candidate string) (string, bool) {
	if host, ok := explicitHost(candidate); ok {
		return host, true
	}
	if host, ok := scpRemoteHost(candidate); ok {
		return normalizeHost(host), true
	}
	candidate = strings.TrimSpace(strings.Trim(candidate, `"'`))
	if candidate == "" || strings.HasPrefix(candidate, "-") || strings.Contains(candidate, "=") {
		return "", false
	}
	parsed, err := url.Parse("//" + candidate)
	if err == nil && validKnownHost(parsed.Hostname()) {
		return normalizeHost(parsed.Hostname()), true
	}
	hostPort := strings.SplitN(candidate, "/", 2)[0]
	if validNumericHost(hostPort) || validHostname(hostPort) {
		return normalizeHost(hostPort), true
	}
	return "", false
}

func scpRemoteHost(candidate string) (string, bool) {
	candidate = strings.TrimSpace(candidate)
	if strings.HasPrefix(candidate, "[") {
		if end := strings.Index(candidate, "]:"); end > 1 {
			return candidate[1:end], true
		}
	}
	colon := strings.Index(candidate, ":")
	if colon <= 0 || strings.Contains(candidate[:colon], "/") {
		return "", false
	}
	host := candidate[:colon]
	if at := strings.LastIndex(host, "@"); at >= 0 {
		host = host[at+1:]
	}
	return host, validKnownHost(host)
}

func validKnownHost(host string) bool {
	return probableHost(host) || validHostname(host) || validNumericHost(host)
}

func validHostname(host string) bool {
	host = strings.TrimSuffix(host, ".")
	if host == "" || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") ||
			strings.HasSuffix(label, "-") {
			return false
		}
		for _, char := range label {
			if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
				(char < '0' || char > '9') && char != '-' {
				return false
			}
		}
	}
	return true
}

func validNumericHost(host string) bool {
	if strings.HasPrefix(strings.ToLower(host), "0x") {
		_, err := strconv.ParseUint(host[2:], 16, 32)
		return err == nil
	}
	if host == "" {
		return false
	}
	for _, char := range host {
		if char < '0' || char > '9' {
			return false
		}
	}
	_, err := strconv.ParseUint(host, 10, 32)
	return err == nil
}

func isFileURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && strings.EqualFold(parsed.Scheme, "file")
}

func isExplicitNetworkURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme != "" && parsed.Hostname() != "" &&
		!strings.EqualFold(parsed.Scheme, "file")
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
	sshClient := base == "ssh" || base == "scp" || base == "sftp"
	for i := 1; i < len(argv); i++ {
		rawArg := argv[i]
		arg := strings.ToLower(rawArg)
		if sshClient && (arg == "-j" || strings.HasPrefix(arg, "-j") ||
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
		if base == "curl" && curlProxyOption(rawArg) {
			return newFinding(
				DecisionDeny, RiskHigh, "network.destination_override",
				"network option can replace the effective destination",
				"remove destination-changing options and use an allowlisted URL directly",
			), true
		}
		rawName := strings.SplitN(rawArg, "=", 2)[0]
		if (base == "nc" || base == "netcat") &&
			(name == "-x" || strings.HasPrefix(arg, "-x") || rawName == "-X") {
			return newFinding(
				DecisionDeny, RiskHigh, "network.destination_override",
				"netcat proxy options replace the effective destination",
				"remove proxy options and connect directly to an allowlisted host",
			), true
		}
	}
	return Finding{}, false
}

func curlProxyOption(arg string) bool {
	if len(arg) < 2 || !strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "--") {
		return false
	}
	for _, option := range arg[1:] {
		if option == 'x' {
			return true
		}
		if strings.ContainsRune("AbcCdDeEFHKmoPQrTuwXY", option) {
			return false
		}
		if !strings.ContainsRune("012346VZfghiIkLNOnpqRrsSv", option) {
			return false
		}
	}
	return false
}

func sshDestinationOverrideOption(value string) bool {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(value, "proxycommand=") ||
		strings.HasPrefix(value, "proxyjump=") ||
		strings.HasPrefix(value, "hostname=")
}

func networkConfigFinding(argv []string) (Finding, bool) {
	base := commandBase(argv[0])
	if base == "ssh" || base == "scp" || base == "sftp" {
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
