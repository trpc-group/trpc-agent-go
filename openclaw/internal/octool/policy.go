//
// Tencent is pleased to support the open source community by making
// trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package octool

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/openclaw/internal/searchresult"
)

const (
	errCommandPolicyRejected = "exec_command blocked by safety " +
		"policy: %s"

	reasonSensitivePath = "reading or modifying shell or " +
		"credential files is not allowed in chat; do not retry " +
		"protected shell or credential paths, and use non-sensitive " +
		"working files or public sources instead"
	reasonSystemPackageInstall = "installing system packages with " +
		"host package managers is not allowed in chat; do not retry " +
		"host package manager commands, and use existing tools or " +
		"user-space dependencies instead"
	reasonSearchResultHTTP = "scraping %s result pages with " +
		"command-line HTTP clients is not allowed in chat; use " +
		"dedicated search tools, direct source URLs, archives, or " +
		"existing evidence instead"
	reasonNetworkProxy = "routing command traffic through ad hoc " +
		"proxies or tunnels is not allowed in chat; use the " +
		"configured network path or built-in web tools instead"

	sensitivePathBoundaryChars = " \t\r\n\"'`=:/\\|&;()[]{}<>"

	envTRPCClawEnvFile  = "TRPC_CLAW_ENV_FILE"
	envTRPCClawStateDir = "TRPC_CLAW_STATE_DIR"

	protectedRuntimeEnvRelPath = "runtime/env.sh"
	protectedGitCredentialFile = "git-credentials"

	shellQuoteChars = `"'`
)

var httpURLPattern = regexp.MustCompile("https?://[^\\s\"'`<>\\\\]+")

var protectedPathFragments = []string{
	".aws/credentials",
	".bash_profile",
	".bashrc",
	".config/gcloud",
	".docker/config.json",
	".git-credentials",
	".kube/config",
	".netrc",
	".npmrc",
	".profile",
	".pypirc",
	".ssh/",
	".zprofile",
	".zshenv",
	".zshrc",
}

// CommandRequest is the normalized command metadata checked by policies.
type CommandRequest struct {
	Command    string
	Workdir    string
	Env        map[string]string
	Pty        bool
	Background bool
	YieldMs    *int
	TimeoutS   *int
}

// CommandPolicy decides whether one exec_command call is allowed.
type CommandPolicy func(context.Context, CommandRequest) error

// NewChatCommandSafetyPolicy blocks direct access to protected shell and
// credential paths in chat contexts.
func NewChatCommandSafetyPolicy() CommandPolicy {
	return func(
		_ context.Context,
		req CommandRequest,
	) error {
		if blocksSensitivePathRequest(req) {
			return fmt.Errorf(
				errCommandPolicyRejected,
				reasonSensitivePath,
			)
		}
		if blocksSystemPackageInstall(req.Command) {
			return fmt.Errorf(
				errCommandPolicyRejected,
				reasonSystemPackageInstall,
			)
		}
		if source, ok := blocksSearchResultHTTP(req.Command); ok {
			return fmt.Errorf(
				errCommandPolicyRejected,
				fmt.Sprintf(reasonSearchResultHTTP, source),
			)
		}
		if blocksNetworkProxy(req.Command) {
			return fmt.Errorf(
				errCommandPolicyRejected,
				reasonNetworkProxy,
			)
		}
		return nil
	}
}

func newCommandRequest(params execParams) CommandRequest {
	return CommandRequest{
		Command:    strings.TrimSpace(params.Command),
		Workdir:    strings.TrimSpace(params.Workdir),
		Env:        params.Env,
		Pty:        params.Pty,
		Background: params.Background,
		YieldMs:    params.YieldMs,
		TimeoutS:   params.TimeoutS,
	}
}

func blocksSensitivePath(command string) bool {
	return matchesProtectedPathFragments(
		normalizePolicyCommand(command),
		protectedPathFragments,
	)
}

func blocksSystemPackageInstall(command string) bool {
	return blocksSystemPackageInstallDepth(normalizePolicyCommand(command), 0)
}

func blocksSearchResultHTTP(command string) (string, bool) {
	command = normalizePolicyCommand(command)
	if command == "" || !usesCommandLineHTTPClient(command, 0) {
		return "", false
	}
	for _, raw := range httpURLPattern.FindAllString(command, -1) {
		source, ok := searchresult.Match(trimShellURL(raw))
		if ok {
			return source, true
		}
	}
	return "", false
}

func blocksNetworkProxy(command string) bool {
	return blocksNetworkProxyDepth(normalizeProxyPolicyCommand(command), 0)
}

func blocksNetworkProxyDepth(command string, depth int) bool {
	if command == "" || depth > 2 {
		return false
	}
	for _, segment := range shellPolicySegments(command) {
		words := shellPolicyWords(segment)
		if blocksNetworkProxyWords(words) {
			return true
		}
		for i := 0; i < len(words); i++ {
			if !isShellExecutable(policyCommandName(words[i])) {
				continue
			}
			cmdArg, ok := shellCommandStringArg(words[i+1:])
			if ok && blocksNetworkProxyDepth(cmdArg, depth+1) {
				return true
			}
		}
	}
	return false
}

func blocksNetworkProxyWords(words []string) bool {
	for i, word := range words {
		if isProxyEnvAssignmentInCommand(words, i) {
			return true
		}
		cmd := strings.ToLower(policyCommandName(word))
		switch cmd {
		case "curl":
			if curlArgsUseProxy(words[i+1:]) {
				return true
			}
		case "wget":
			if wgetArgsUseProxy(words[i+1:]) {
				return true
			}
		case "proxychains", "proxychains4", "torsocks", "tsocks":
			return true
		case "ssh":
			if sshArgsOpenTunnel(words[i+1:]) {
				return true
			}
		}
	}
	return false
}

func normalizeProxyPolicyCommand(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return ""
	}
	return filepath.ToSlash(trimmed)
}

func isProxyEnvAssignmentInCommand(words []string, index int) bool {
	if index < 0 || index >= len(words) ||
		!isProxyEnvAssignment(words[index]) {
		return false
	}
	if index == 0 {
		return true
	}
	prev := policyCommandName(words[index-1])
	if prev == "env" || prev == "export" {
		return true
	}
	for i := 0; i < index; i++ {
		if policyCommandName(words[i]) != "" {
			return false
		}
	}
	return true
}

func isProxyEnvAssignment(word string) bool {
	name, value, ok := strings.Cut(strings.Trim(word, shellQuoteChars), "=")
	if !ok || strings.TrimSpace(value) == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "http_proxy", "https_proxy", "all_proxy":
		return true
	default:
		return false
	}
}

func curlArgsUseProxy(words []string) bool {
	for _, word := range words {
		flag := strings.Trim(word, shellQuoteChars)
		if flag == "-x" ||
			strings.HasPrefix(flag, "-xhttp://") ||
			strings.HasPrefix(flag, "-xhttps://") ||
			strings.HasPrefix(flag, "-xsocks") ||
			flag == "--proxy" ||
			strings.HasPrefix(flag, "--proxy=") ||
			flag == "--preproxy" ||
			strings.HasPrefix(flag, "--preproxy=") ||
			strings.HasPrefix(flag, "--socks4") ||
			strings.HasPrefix(flag, "--socks5") {
			return true
		}
	}
	return false
}

func wgetArgsUseProxy(words []string) bool {
	for i, word := range words {
		flag := strings.Trim(word, shellQuoteChars)
		if strings.HasPrefix(flag, "--proxy") ||
			strings.Contains(strings.ToLower(flag), "_proxy=") {
			return true
		}
		if flag == "-e" && i+1 < len(words) {
			next := strings.ToLower(strings.Trim(words[i+1], shellQuoteChars))
			if strings.Contains(next, "proxy") {
				return true
			}
		}
	}
	return false
}

func sshArgsOpenTunnel(words []string) bool {
	for _, word := range words {
		flag := strings.Trim(word, shellQuoteChars)
		if flag == "-D" || flag == "-L" || flag == "-R" ||
			flag == "-W" ||
			strings.HasPrefix(flag, "-D") ||
			strings.HasPrefix(flag, "-L") ||
			strings.HasPrefix(flag, "-R") ||
			strings.HasPrefix(flag, "-W") {
			return true
		}
	}
	return false
}

func usesCommandLineHTTPClient(command string, depth int) bool {
	if command == "" || depth > 2 {
		return false
	}
	words := shellPolicyWords(command)
	for i, word := range words {
		cmd := policyCommandName(word)
		switch cmd {
		case "curl", "wget", "http", "https", "aria2c", "lynx", "w3m":
			return true
		case "python", "python3", "python2", "pypy", "pypy3":
			if containsPythonHTTPClient(command) {
				return true
			}
		case "node", "nodejs", "bun", "deno":
			if containsJSHTTPClient(command) {
				return true
			}
		}
		if !isShellExecutable(cmd) {
			continue
		}
		cmdArg, ok := shellCommandStringArg(words[i+1:])
		if ok && usesCommandLineHTTPClient(cmdArg, depth+1) {
			return true
		}
	}
	return false
}

func trimShellURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), ".,;:)]}")
}

func containsPythonHTTPClient(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "requests") ||
		strings.Contains(lower, "urllib") ||
		strings.Contains(lower, "http.client") ||
		strings.Contains(lower, "urlopen")
}

func containsJSHTTPClient(command string) bool {
	lower := strings.ToLower(command)
	return strings.Contains(lower, "fetch(") ||
		strings.Contains(lower, "axios") ||
		strings.Contains(lower, "http.get") ||
		strings.Contains(lower, "https.get")
}

func blocksSystemPackageInstallDepth(command string, depth int) bool {
	if command == "" || depth > 2 {
		return false
	}
	words := shellPolicyWords(command)
	if blocksSystemPackageInstallWords(words) {
		return true
	}
	for i := 0; i < len(words); i++ {
		if !isShellExecutable(policyCommandName(words[i])) {
			continue
		}
		cmdArg, ok := shellCommandStringArg(words[i+1:])
		if ok && blocksSystemPackageInstallDepth(cmdArg, depth+1) {
			return true
		}
	}
	return false
}

func blocksSystemPackageInstallWords(words []string) bool {
	for i, word := range words {
		cmd := policyCommandName(word)
		switch cmd {
		case "apt", "apt-get", "aptitude":
			if nextPolicyWord(words, i+1) == "install" {
				return true
			}
		case "yum", "dnf", "microdnf", "zypper", "brew":
			if nextPolicyWord(words, i+1) == "install" {
				return true
			}
		case "apk":
			if nextPolicyWord(words, i+1) == "add" {
				return true
			}
		case "pacman":
			next := nextPolicyWord(words, i+1)
			if next == "--sync" || strings.HasPrefix(next, "-s") {
				return true
			}
		}
	}
	return false
}

func shellPolicySegments(command string) []string {
	segments := make([]string, 0, 2)
	var b strings.Builder
	var quote rune
	flush := func() {
		segment := strings.TrimSpace(b.String())
		if segment != "" {
			segments = append(segments, segment)
		}
		b.Reset()
	}
	for _, r := range command {
		if quote != 0 {
			if r == quote {
				quote = 0
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
			b.WriteRune(r)
		case ';', '|', '&', '\n':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return segments
}

func shellPolicyWords(command string) []string {
	words := make([]string, 0, 8)
	var b strings.Builder
	var quote rune
	flush := func() {
		word := strings.TrimSpace(b.String())
		if word != "" {
			words = append(words, word)
		}
		b.Reset()
	}
	for _, r := range command {
		if quote != 0 {
			if r == quote {
				quote = 0
				continue
			}
			b.WriteRune(r)
			continue
		}
		switch r {
		case '\'', '"', '`':
			quote = r
		case ' ', '\t', '\r', '\n', ';', '|', '&', '(', ')', '<', '>':
			flush()
		default:
			b.WriteRune(r)
		}
	}
	flush()
	return words
}

func nextPolicyWord(words []string, start int) string {
	for i := start; i < len(words); i++ {
		word := strings.TrimSpace(words[i])
		if word == "" {
			continue
		}
		if strings.Contains(word, "=") && !strings.HasPrefix(word, "-") {
			before, _, _ := strings.Cut(word, "=")
			if before != "" && !strings.ContainsAny(before, "/.") {
				continue
			}
		}
		if strings.HasPrefix(word, "-") &&
			word != "--sync" &&
			!strings.HasPrefix(word, "-s") {
			continue
		}
		return word
	}
	return ""
}

func policyCommandName(word string) string {
	word = strings.TrimSpace(word)
	if word == "" {
		return ""
	}
	word = strings.Trim(word, shellQuoteChars)
	word = strings.TrimPrefix(word, "command:")
	word = strings.TrimPrefix(word, "exec:")
	if strings.Contains(word, "=") && !strings.HasPrefix(word, "-") {
		before, after, _ := strings.Cut(word, "=")
		if before != "" && after != "" && !strings.ContainsAny(before, "/.") {
			return ""
		}
	}
	word = strings.TrimRight(word, ",")
	return filepath.Base(word)
}

func isShellExecutable(cmd string) bool {
	switch cmd {
	case "sh", "bash", "dash", "ksh", "zsh":
		return true
	default:
		return false
	}
}

func shellCommandStringArg(words []string) (string, bool) {
	for i := 0; i < len(words); i++ {
		word := strings.TrimSpace(words[i])
		if word == "" {
			continue
		}
		if word == "-c" || strings.HasSuffix(word, "c") &&
			strings.HasPrefix(word, "-") &&
			!strings.HasPrefix(word, "--") {
			if i+1 < len(words) {
				return words[i+1], true
			}
			return "", false
		}
		if !strings.HasPrefix(word, "-") {
			return "", false
		}
	}
	return "", false
}

func blocksSensitivePathRequest(req CommandRequest) bool {
	workdirFragments := dynamicProtectedWorkdirFragments(req.Env)
	if blocksSensitivePathValue(
		req.Workdir,
		protectedWorkdirFragments(),
		workdirFragments,
		req.Env,
	) {
		return true
	}

	return blocksSensitivePathValue(
		req.Command,
		protectedPathFragments,
		dynamicProtectedPathFragments(req.Env),
		req.Env,
	)
}

func blocksSensitivePathValue(
	raw string,
	protectedFragments []string,
	dynamicFragments []string,
	env map[string]string,
) bool {
	value := normalizePolicyCommand(raw)
	if value == "" {
		return false
	}
	if matchesProtectedPathFragments(value, protectedFragments) {
		return true
	}
	if matchesProtectedPathFragments(value, dynamicFragments) {
		return true
	}
	return matchesProtectedPathFragments(
		expandProtectedEnvReferences(value, env),
		dynamicFragments,
	)
}

func normalizePolicyCommand(command string) string {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(filepath.ToSlash(trimmed))
}

func blocksSensitivePathWithFragments(
	command string,
	fragments []string,
) bool {
	if command == "" {
		return false
	}
	for _, fragment := range fragments {
		if containsSensitivePathFragment(command, fragment) {
			return true
		}
	}
	return false
}

func matchesProtectedPathFragments(
	command string,
	fragments []string,
) bool {
	if blocksSensitivePathWithFragments(command, fragments) {
		return true
	}
	unquoted := stripShellQuotes(command)
	if unquoted == command {
		return false
	}
	return blocksSensitivePathWithFragments(unquoted, fragments)
}

func dynamicProtectedPathFragments(env map[string]string) []string {
	out := make([]string, 0, 3)
	if len(env) == 0 {
		return out
	}
	out = appendProtectedPathFragment(
		out,
		env[envTRPCClawEnvFile],
	)

	stateDir := strings.TrimSpace(env[envTRPCClawStateDir])
	if stateDir == "" {
		return out
	}
	out = appendProtectedPathFragment(
		out,
		filepath.Join(stateDir, protectedRuntimeEnvRelPath),
	)
	out = appendProtectedPathFragment(
		out,
		filepath.Join(stateDir, protectedGitCredentialFile),
	)
	return out
}

func protectedWorkdirFragments() []string {
	out := make([]string, 0, len(protectedPathFragments))
	for _, fragment := range protectedPathFragments {
		out = append(
			out,
			strings.TrimSuffix(fragment, "/"),
		)
	}
	return out
}

func dynamicProtectedWorkdirFragments(env map[string]string) []string {
	out := make([]string, 0, 3)
	if len(env) == 0 {
		return out
	}
	out = appendProtectedPathDir(
		out,
		env[envTRPCClawEnvFile],
	)

	stateDir := strings.TrimSpace(env[envTRPCClawStateDir])
	if stateDir == "" {
		return out
	}
	out = appendProtectedPathFragment(
		out,
		filepath.Join(
			stateDir,
			filepath.Dir(protectedRuntimeEnvRelPath),
		),
	)
	return out
}

func expandProtectedEnvReferences(
	command string,
	env map[string]string,
) string {
	if command == "" || len(env) == 0 {
		return command
	}
	expanded := expandProtectedEnvReference(
		command,
		envTRPCClawEnvFile,
		env[envTRPCClawEnvFile],
	)
	return expandProtectedEnvReference(
		expanded,
		envTRPCClawStateDir,
		env[envTRPCClawStateDir],
	)
}

func expandProtectedEnvReference(
	command string,
	envName string,
	value string,
) string {
	fragment := normalizePathFragment(value)
	if fragment == "" {
		return command
	}
	lowerName := strings.ToLower(envName)
	replacer := strings.NewReplacer(
		"$"+lowerName,
		fragment,
		"${"+lowerName+"}",
		fragment,
	)
	return replacer.Replace(command)
}

func appendProtectedPathFragment(out []string, raw string) []string {
	fragment := normalizePathFragment(raw)
	if fragment == "" {
		return out
	}
	return append(out, fragment)
}

func appendProtectedPathDir(out []string, raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return out
	}
	dir := filepath.Dir(trimmed)
	if dir == "." {
		return out
	}
	return appendProtectedPathFragment(out, dir)
}

func normalizePathFragment(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	return strings.ToLower(filepath.ToSlash(trimmed))
}

func stripShellQuotes(command string) string {
	if command == "" {
		return ""
	}
	return strings.Map(func(r rune) rune {
		if strings.ContainsRune(shellQuoteChars, r) {
			return -1
		}
		return r
	}, command)
}

func containsSensitivePathFragment(command, fragment string) bool {
	if fragment == "" {
		return false
	}
	offset := 0
	for offset < len(command) {
		idx := strings.Index(command[offset:], fragment)
		if idx < 0 {
			return false
		}
		idx += offset
		if hasSensitivePathBoundaryBefore(command, idx) &&
			hasSensitivePathBoundaryAfter(command, idx+len(fragment), fragment) {
			return true
		}
		offset = idx + 1
	}
	return false
}

func hasSensitivePathBoundaryBefore(command string, idx int) bool {
	if idx <= 0 {
		return true
	}
	return isSensitivePathBoundary(command[idx-1])
}

func hasSensitivePathBoundaryAfter(
	command string,
	idx int,
	fragment string,
) bool {
	if strings.HasSuffix(fragment, "/") {
		return true
	}
	if idx >= len(command) {
		return true
	}
	if fragment == ".env" && command[idx] == '.' {
		return true
	}
	return isSensitivePathBoundary(command[idx])
}

func isSensitivePathBoundary(ch byte) bool {
	return strings.ContainsRune(sensitivePathBoundaryChars, rune(ch))
}
