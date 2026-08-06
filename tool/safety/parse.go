//
// Tencent is pleased to support the open source community by making trpc-agent-go available.
//
// Copyright (C) 2025 Tencent.  All rights reserved.
//
// trpc-agent-go is licensed under the Apache License Version 2.0.
//

package safety

import (
	"net/url"
	"path"
	"strconv"
	"strings"

	"trpc.group/trpc-go/trpc-agent-go/internal/shellsafe"
)

// shellsafeWrapperPolicy is a deny-only policy whose single sentinel entry never
// matches a real command; it exists only to activate internal/shellsafe's
// implicit-deny set (shell wrappers plus re-executing and stateful builtins:
// sh/eval/command/env/xargs/trap/alias/export/cd/printf/...). On an
// already-parseable line, a non-nil CheckCommand result therefore means such a
// wrapper/builtin is present. Delegating to shellsafe keeps this in lockstep
// with the framework's own deny set instead of a hand-maintained copy.
var shellsafeWrapperPolicy = shellsafe.PolicyFromLists(nil, []string{"\x00safety-wrapper-sentinel"})

// lineHasShellWrapper reports whether a parseable command line contains a shell
// wrapper or re-executing/stateful builtin from shellsafe's implicit-deny set.
// Only call it on lines that already parsed (parsePipeline succeeded).
func lineHasShellWrapper(line string) bool {
	return shellsafe.CheckCommand(line, shellsafeWrapperPolicy) != nil
}

// windowsExecExts are stripped from command names so "curl.exe" matches "curl".
var windowsExecExts = []string{".exe", ".cmd", ".bat", ".com", ".ps1"}

// ncAddrFlags are nc/ncat/telnet short flags whose next argv token is an
// address (source/proxy/bind), which must not be mistaken for the target host.
var ncAddrFlags = map[string]struct{}{"-s": {}, "-x": {}, "-X": {}, "-b": {}}

// commandBase returns the lower-cased basename of an executable reference with
// any Windows executable suffix removed, e.g. "/usr/bin/Curl" -> "curl",
// "cmd.exe" -> "cmd". It mirrors the normalisation internal/shellsafe applies
// so the guard and the underlying policy agree on command identity.
func commandBase(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"'`)
	s = strings.ReplaceAll(s, "\\", "/")
	if idx := strings.LastIndex(s, "/"); idx >= 0 {
		s = s[idx+1:]
	}
	s = strings.ToLower(s)
	for _, ext := range windowsExecExts {
		if strings.HasSuffix(s, ext) {
			return s[:len(s)-len(ext)]
		}
	}
	return s
}

// normalizePathArg canonicalises a path argument for denied-path matching:
// forward slashes, unquoted, $HOME / ${HOME} folded to "~", and "." / ".."
// segments collapsed so /tmp/../etc/shadow resolves like /etc/shadow.
func normalizePathArg(s string) string {
	s = strings.Trim(strings.TrimSpace(s), `"'`)
	if s == "" {
		return ""
	}
	s = strings.ReplaceAll(s, "\\", "/")
	s = strings.ReplaceAll(s, "${HOME}", "~")
	s = strings.ReplaceAll(s, "$HOME", "~")
	// Collapse dot segments. URLs are left alone: path.Clean would corrupt the
	// scheme's "//" and a URL never matches a filesystem denied path anyway.
	switch {
	case strings.Contains(s, "://"):
	case strings.HasPrefix(s, "~"):
		// A ~ or ~user prefix is a directory the shell expands BEFORE resolving
		// "..", so it must clean like one path element, not a relative segment
		// (plain Clean turns ~root/../etc/shadow into etc/shadow and loses the
		// real target /etc/shadow). Anchoring at "/" folds the home away when
		// ".." climbs out of it, leaving the absolute remainder to match.
		if c := path.Clean("/" + s); strings.HasPrefix(c, "/~") {
			s = c[1:]
		} else {
			s = c
		}
	default:
		s = path.Clean(s)
	}
	return s
}

// parsePipeline runs the conservative shellsafe parser and returns the pipeline
// segments (each is an argv slice). A non-nil error means the command used a
// construct shellsafe rejects (command substitution, redirection, subshell,
// leading assignment, ...) and must not be treated as safe.
func parsePipeline(command string) ([][]string, error) {
	pipe, err := shellsafe.Parse(command)
	if err != nil {
		return nil, err
	}
	return pipe.Commands, nil
}

// splitScriptLines splits a possibly multi-line script into logical command
// lines, joining backslash continuations and dropping blank / comment lines.
func splitScriptLines(script string) []string {
	raw := strings.Split(script, "\n")
	var lines []string
	var pending strings.Builder
	for _, ln := range raw {
		ln = strings.TrimRight(ln, "\r")
		trimmed := strings.TrimSpace(ln)
		if pending.Len() == 0 && (trimmed == "" || strings.HasPrefix(trimmed, "#")) {
			continue
		}
		// A trailing backslash escapes the newline (line continuation) only when
		// the run of trailing backslashes is odd; an even run is literal
		// backslashes and the shell runs the next line as a separate command.
		if trailingBackslashes(ln)%2 == 1 {
			pending.WriteString(ln[:len(ln)-1])
			pending.WriteString(" ")
			continue
		}
		pending.WriteString(ln)
		line := strings.TrimSpace(pending.String())
		pending.Reset()
		if line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	if rest := strings.TrimSpace(pending.String()); rest != "" && !strings.HasPrefix(rest, "#") {
		lines = append(lines, rest)
	}
	return lines
}

// trailingBackslashes counts the run of "\" characters at the end of s.
func trailingBackslashes(s string) int {
	n := 0
	for i := len(s) - 1; i >= 0 && s[i] == '\\'; i-- {
		n++
	}
	return n
}

// isFlag reports whether an argv token is an option flag.
func isFlag(s string) bool {
	return strings.HasPrefix(s, "-") && s != "-"
}

// splitFlagValue splits the attached option form "--flag=value" into its parts.
// ok is false for a token that carries no attached value, in which case flag is
// the token itself.
func splitFlagValue(a string) (flag, value string, ok bool) {
	if i := strings.IndexByte(a, '='); i > 0 {
		return a[:i], a[i+1:], true
	}
	return a, "", false
}

// operandCandidates returns the path/host candidate strings in a command's
// arguments. Beyond the raw tokens it expands operands embedded in options so
// they are not missed by denied-path or host matching: option values
// (--output=/etc/shadow, --url=https://x), curl-style file uploads
// (@/etc/shadow, name=@/etc/shadow) and short flags with an attached path
// (-o/etc/shadow).
func operandCandidates(argv []string) []string {
	out := make([]string, 0, len(argv))
	for _, a := range argv[1:] {
		if a == "" {
			continue
		}
		out = append(out, a)
		if i := strings.IndexByte(a, '='); i >= 0 && i+1 < len(a) {
			out = append(out, a[i+1:])
		}
		if i := strings.LastIndexByte(a, '@'); i >= 0 && i+1 < len(a) {
			out = append(out, a[i+1:])
		}
		// A short flag with an attached path (curl -o/etc/shadow) hides the
		// path behind the flag letters; surface it from the first '/'.
		if isFlag(a) {
			if i := strings.IndexByte(a, '/'); i > 0 {
				out = append(out, a[i:])
			}
		}
	}
	return out
}

// extractHosts returns the target hosts referenced by a network command. For
// multi-target tools (curl/wget/ssh/scp/...) it returns every referenced host
// (positional operands and option values), de-duplicated in first-seen order,
// so a benign host cannot mask a second non-allowlisted target. For single-host
// tools (nc/ncat/telnet) it returns only the first operand, since trailing
// operands are ports or data rather than additional hosts.
func extractHosts(argv []string) []string {
	switch commandBase(argv[0]) {
	case "nc", "ncat", "telnet":
		return ncHosts(argv)
	case "ssh":
		return dedupHosts(sshHosts(argv))
	case "sftp":
		return dedupHosts(sftpHosts(argv))
	case "scp", "rsync":
		return dedupHosts(scpHosts(argv))
	case "curl":
		return dedupHosts(curlHosts(argv))
	case "wget":
		return dedupHosts(wgetHosts(argv))
	case "git":
		return dedupHosts(gitHosts(argv))
	default:
		// Unknown network command: accept only an unambiguous host — a scheme
		// URL or user@host form — since we do not know its operand grammar.
		return dedupHosts(explicitHosts(argv))
	}
}

func dedupHosts(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, h := range in {
		if h == "" {
			continue
		}
		if _, ok := seen[h]; ok {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out
}

// bareHost returns hostFromToken's host regardless of whether the token used an
// explicit scheme/user@host form — for command positions that are known to be
// hosts (ssh/scp/curl operands), a scheme-less dotted host is still a host.
func bareHost(token string) string {
	h, _ := hostFromToken(token)
	return h
}

// ncHosts handles nc/ncat/telnet: only the first operand is the target; a bare
// port and address-carrying flag values (-s/-x/-X/-b) are skipped.
func ncHosts(argv []string) []string {
	skipNext := false
	for _, a := range argv[1:] {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "" || a == "-" {
			continue
		}
		if isFlag(a) {
			if _, ok := ncAddrFlags[a]; ok {
				skipNext = true
			}
			continue
		}
		if _, err := strconv.Atoi(a); err == nil {
			continue // bare port
		}
		if h := bareHost(a); h != "" {
			return []string{h}
		}
		return []string{strings.ToLower(strings.Trim(a, `"'`))}
	}
	return nil
}

// sshValueFlags are ssh short flags that consume the next argv token.
var sshValueFlags = map[string]struct{}{
	"-b": {}, "-c": {}, "-D": {}, "-E": {}, "-e": {}, "-F": {}, "-I": {},
	"-i": {}, "-J": {}, "-L": {}, "-l": {}, "-m": {}, "-O": {}, "-o": {},
	"-p": {}, "-Q": {}, "-R": {}, "-S": {}, "-W": {}, "-w": {},
}

// sftpValueFlags are sftp short flags that consume the next argv token, so an
// option value is not mistaken for the host operand.
var sftpValueFlags = map[string]struct{}{
	"-B": {}, "-b": {}, "-c": {}, "-D": {}, "-F": {}, "-i": {}, "-l": {},
	"-o": {}, "-P": {}, "-R": {}, "-S": {}, "-s": {}, "-X": {},
}

// scpValueFlags are scp/rsync short flags that consume the next argv token.
var scpValueFlags = map[string]struct{}{
	"-c": {}, "-D": {}, "-F": {}, "-i": {}, "-l": {}, "-o": {}, "-P": {},
	"-S": {}, "-X": {},
}

// sshHosts handles ssh: after skipping option values, the first operand is the
// target host (scheme-less and single-label forms accepted). -J jump hosts are
// egress peers too and are collected alongside the destination.
func sshHosts(argv []string) []string {
	return firstOperandHost(argv, sshValueFlags)
}

// sftpHosts handles sftp, whose grammar is ssh-like: the first operand is the
// target ([user@]host, host:path, or an sftp:// URL), NOT scp-like — a plain
// `sftp evil.example.com` carries no colon and must still be a host.
func sftpHosts(argv []string) []string {
	return firstOperandHost(argv, sftpValueFlags)
}

// firstOperandHost walks an ssh-style argv: option values are skipped, -J jump
// hosts are collected as egress peers, and the first operand is the target
// host. Single-label operands are accepted (conservative: an unknown name must
// fail the allowlist, not vanish).
func firstOperandHost(argv []string, valueFlags map[string]struct{}) []string {
	var hosts []string
	skipNext, isJump := false, false
	for _, a := range argv[1:] {
		if skipNext {
			if isJump {
				hosts = append(hosts, jumpHosts(a)...)
			}
			skipNext, isJump = false, false
			continue
		}
		if a == "" {
			continue
		}
		if isFlag(a) {
			if a == "-J" {
				skipNext, isJump = true, true
				continue
			}
			if strings.HasPrefix(a, "-J") && len(a) > 2 {
				hosts = append(hosts, jumpHosts(a[2:])...)
				continue
			}
			if _, ok := valueFlags[a]; ok {
				skipNext = true
			}
			continue
		}
		if h := hostOperand(a); h != "" {
			return append(hosts, h)
		}
		return hosts
	}
	return hosts
}

// hostOperand reads a known host-position operand: URL / user@host / dotted
// name via hostFromToken, with a raw lower-cased fallback for single-label
// hosts, and the :path/:port suffix stripped (sftp host:path).
func hostOperand(a string) string {
	if h := bareHost(a); h != "" {
		return h
	}
	a = strings.Trim(a, `"'`)
	if i := strings.IndexByte(a, ':'); i >= 0 {
		a = a[:i]
	}
	return strings.ToLower(strings.TrimSpace(a))
}

// jumpHosts splits a -J value (host[,host...], each [user@]host[:port]) into
// its egress peers.
func jumpHosts(v string) []string {
	var out []string
	for _, part := range strings.Split(v, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if h := hostOperand(part); h != "" {
			out = append(out, h)
		}
	}
	return out
}

// scpHosts handles scp/rsync: a remote operand carries a colon
// ([user@]host:path) or a scheme URL (rsync://h/m); local files have neither,
// so they are not read as hosts. Option values are skipped and -J jump hosts
// are collected as egress peers.
func scpHosts(argv []string) []string {
	var hosts []string
	skipNext, isJump := false, false
	for _, a := range argv[1:] {
		if skipNext {
			if isJump {
				hosts = append(hosts, jumpHosts(a)...)
			}
			skipNext, isJump = false, false
			continue
		}
		if a == "" {
			continue
		}
		if isFlag(a) {
			if a == "-J" {
				skipNext, isJump = true, true
				continue
			}
			if strings.HasPrefix(a, "-J") && len(a) > 2 {
				hosts = append(hosts, jumpHosts(a[2:])...)
				continue
			}
			if _, ok := scpValueFlags[a]; ok {
				skipNext = true
			}
			continue
		}
		if strings.Contains(a, "://") {
			if h, explicit := hostFromToken(a); explicit && h != "" {
				hosts = append(hosts, h)
			}
			continue
		}
		i := strings.IndexByte(a, ':')
		if i <= 0 {
			continue // local file operand
		}
		hostPart := a[:i]
		if at := strings.LastIndex(hostPart, "@"); at >= 0 {
			hostPart = hostPart[at+1:]
		}
		if hostPart = strings.ToLower(strings.Trim(hostPart, `"'`)); hostPart != "" {
			hosts = append(hosts, hostPart)
		}
	}
	return hosts
}

// curlFileFlags are curl flags whose value is a local file (an output or
// config path), not a URL, so the value must not be read as a host.
var curlFileFlags = map[string]struct{}{
	"-o": {}, "--output": {}, "--output-dir": {}, "-T": {}, "--upload-file": {},
	"-D": {}, "--dump-header": {}, "-K": {}, "--config": {}, "-c": {}, "--cookie-jar": {},
}

// curlHostFlags are curl/wget flags whose VALUE names a host the request
// actually reaches: the request URL itself (--url) and the proxies every
// connection is routed through (-x/--proxy/--socks5/...). A proxy is the real
// egress peer, so it must clear the allowlist as well — otherwise
// `curl --proxy=http://evil.example.com https://github.com` would pass on the
// allowlisted URL alone while the connection goes to the proxy.
var curlHostFlags = map[string]struct{}{
	"--url": {}, "-x": {}, "--proxy": {}, "--preproxy": {}, "--proxy1.0": {},
	"--socks4": {}, "--socks4a": {}, "--socks5": {}, "--socks5-hostname": {},
}

// curlRoutingFlags rewrite where a request connects independently of its URL:
// --connect-to HOST1:PORT1:HOST2:PORT2 redirects the connection to a different
// peer and --resolve HOST:PORT:ADDR pins a name to an arbitrary address. The
// allowlisted URL is then no longer the real destination, and the mapping
// cannot be modelled by a static host check, so their presence is denied
// outright rather than silently trusted.
var curlRoutingFlags = map[string]struct{}{
	"--connect-to": {}, "--resolve": {},
}

// pendingValue records what the token following a flag means when that flag
// took its value in separate form.
type pendingValue int

const (
	// pendingNone means the next token is not a flag value.
	pendingNone pendingValue = iota
	// pendingHost means the next token names a host (a URL or a proxy).
	pendingHost
	// pendingSkip means the next token is a local file, never a host.
	pendingSkip
)

// curlHosts handles curl. Positional operands are URLs (scheme-less accepted)
// and so are the values of host-bearing options, in both the separate
// (--proxy URL) and attached (--proxy=URL, -xURL) forms. @file uploads and the
// values of file-valued flags are excluded.
func curlHosts(argv []string) []string {
	var hosts []string
	pending := pendingNone
	for _, a := range argv[1:] {
		if pending != pendingNone {
			if pending == pendingHost {
				hosts = appendHost(hosts, a)
			}
			pending = pendingNone
			continue
		}
		if a == "" || strings.HasPrefix(a, "@") {
			continue
		}
		if !isFlag(a) {
			hosts = appendHost(hosts, a)
			continue
		}
		kind, val, attached := curlFlagOperand(a)
		switch {
		case !attached:
			// A separate-form flag leaves its value to the next token. An
			// unrecognised flag yields pendingNone, so its value is still examined
			// as a positional operand rather than being trusted blindly.
			pending = kind
		case kind == pendingHost:
			hosts = appendHost(hosts, val)
		}
	}
	return hosts
}

// curlFlagOperand classifies one curl option token: what its value denotes, the
// value itself when the token carries it attached (--proxy=URL, -xURL), and
// whether it was attached.
func curlFlagOperand(a string) (kind pendingValue, value string, attached bool) {
	flag, val, attached := splitFlagValue(a)
	// curl also accepts a short flag with its value attached (-xHOST, -o/tmp/f),
	// which hides the value behind the flag letter.
	if !attached && len(flag) > 2 && !strings.HasPrefix(flag, "--") {
		if short := flag[:2]; isCurlHostFlag(short) || isCurlFileFlag(short) {
			flag, val, attached = short, flag[2:], true
		}
	}
	switch {
	case isCurlHostFlag(flag):
		return pendingHost, val, attached
	case isCurlFileFlag(flag):
		return pendingSkip, val, attached
	default:
		return pendingNone, "", attached
	}
}

// appendHost appends the host token names, if it names one.
func appendHost(hosts []string, token string) []string {
	if h := bareHost(token); h != "" {
		return append(hosts, h)
	}
	return hosts
}

func isCurlHostFlag(flag string) bool {
	_, ok := curlHostFlags[flag]
	return ok
}

func isCurlFileFlag(flag string) bool {
	_, ok := curlFileFlags[flag]
	return ok
}

// gitGlobalValueFlags are git's pre-subcommand options that consume the next
// token, so the subcommand is not mistaken for one of their values
// (git -C /repo clone ...).
var gitGlobalValueFlags = map[string]struct{}{
	"-C": {}, "-c": {}, "--git-dir": {}, "--work-tree": {},
	"--namespace": {}, "--exec-path": {}, "--config-env": {},
}

// gitNetworkSubcommands are the git subcommands that contact a remote. git is a
// network command only for these; purely local subcommands (status, log,
// commit, diff, ...) perform no egress and stay unflagged.
var gitNetworkSubcommands = map[string]struct{}{
	"clone": {}, "fetch": {}, "pull": {}, "push": {}, "ls-remote": {},
}

// gitSubcommand returns git's subcommand, skipping global options and the
// values they consume. It is empty when argv carries no subcommand.
func gitSubcommand(argv []string) string {
	skipNext := false
	for _, a := range argv[1:] {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "" {
			continue
		}
		if isFlag(a) {
			if _, ok := gitGlobalValueFlags[a]; ok {
				skipNext = true
			}
			continue
		}
		return strings.ToLower(strings.Trim(a, `"'`))
	}
	return ""
}

// gitIsNetworkOp reports whether a git invocation contacts a remote.
func gitIsNetworkOp(argv []string) bool {
	_, ok := gitNetworkSubcommands[gitSubcommand(argv)]
	return ok
}

// gitHosts handles git's remote operand, which may be a URL
// (https://h/r, ssh://h/r, git://h/r), an scp-style remote (user@h:path or
// h:path), or absent entirely because the remote is named in the repository
// configuration (git push origin main). The last form yields no host, which the
// network rule reports as an undetermined target rather than an allowed call.
func gitHosts(argv []string) []string {
	var hosts []string
	for _, a := range argv[1:] {
		if a == "" || isFlag(a) {
			continue
		}
		if h, explicit := hostFromToken(a); explicit && h != "" {
			hosts = append(hosts, h)
			continue
		}
		// scp-style "host:path" remote, which carries no scheme and no user@.
		if i := strings.IndexByte(a, ':'); i > 0 && !strings.Contains(a, "://") {
			if h := strings.ToLower(strings.Trim(a[:i], `"'`)); strings.Contains(h, ".") {
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}

// wgetFileFlags are wget flags whose value is a local file or directory, never
// a host. (-i/--input-file and --config are also unscannable-input flags — see
// netConfigFile — but must still not be read as hosts.)
var wgetFileFlags = map[string]struct{}{
	"-O": {}, "--output-document": {}, "-o": {}, "--output-file": {},
	"-a": {}, "--append-output": {}, "-P": {}, "--directory-prefix": {},
	"-i": {}, "--input-file": {}, "--config": {},
	"--load-cookies": {}, "--save-cookies": {},
}

// wgetHosts handles wget, which differs from curl: there are no proxy flags
// (proxies arrive via -e key=value wgetrc commands), and its file-valued flags
// are its own set. Positional operands are URLs (scheme-less accepted); -e
// proxy assignments contribute the proxy as an egress host.
func wgetHosts(argv []string) []string {
	var hosts []string
	pending := pendingNone
	pendingExec := false
	for _, a := range argv[1:] {
		if pending != pendingNone || pendingExec {
			if pendingExec {
				if _, v, ok := proxyAssignment(a); ok {
					hosts = appendHost(hosts, v)
				}
			}
			pending, pendingExec = pendingNone, false
			continue
		}
		if a == "" {
			continue
		}
		if !isFlag(a) {
			hosts = appendHost(hosts, a)
			continue
		}
		flag, val, attached := wgetFlag(a)
		switch {
		case flag == "-e" || flag == "--execute":
			if !attached {
				pendingExec = true
				continue
			}
			if _, v, ok := proxyAssignment(val); ok {
				hosts = appendHost(hosts, v)
			}
		case isWgetFileFlag(flag) && !attached:
			pending = pendingSkip
		}
	}
	return hosts
}

func isWgetFileFlag(flag string) bool {
	_, ok := wgetFileFlags[flag]
	return ok
}

// wgetFlag splits one wget option token into flag/value/attached. A short flag
// with an attached value is recognised BEFORE the '=' split, because -e values
// are themselves key=value wgetrc commands: -ehttps_proxy=URL means -e with
// value "https_proxy=URL", which a plain '='-split would misread.
func wgetFlag(a string) (flag, value string, attached bool) {
	if len(a) > 2 && !strings.HasPrefix(a, "--") {
		if short := a[:2]; short == "-e" || isWgetFileFlag(short) {
			return short, a[2:], true
		}
	}
	return splitFlagValue(a)
}

// proxyAssignment reports whether a wget -e value is a proxy assignment
// (http_proxy=URL / https_proxy=URL / ftp_proxy=URL), returning its key and
// URL. Any other -e value rewrites arbitrary wgetrc configuration and is
// handled as an unmodellable routing override instead (see netRoutingOverride).
func proxyAssignment(v string) (key, value string, ok bool) {
	k, val, found := strings.Cut(v, "=")
	if !found || val == "" {
		return "", "", false
	}
	if !strings.HasSuffix(strings.ToLower(strings.TrimSpace(k)), "_proxy") {
		return "", "", false
	}
	return k, val, true
}

// netRoutingOverride reports a per-command option that reroutes where the
// connection actually goes in a way a static host check cannot model: curl's
// --connect-to/--resolve, and wget's -e/--execute with a non-proxy wgetrc
// command (a proxy assignment is modelled by wgetHosts instead). The offending
// flag is returned as evidence.
func netRoutingOverride(base string, argv []string) (string, bool) {
	switch base {
	case "curl":
		for _, a := range argv[1:] {
			flag, _, _ := splitFlagValue(a)
			if _, ok := curlRoutingFlags[flag]; ok {
				return flag, true
			}
		}
	case "wget":
		return wgetRoutingOverride(argv)
	}
	return "", false
}

// wgetRoutingOverride flags -e/--execute values that are not plain proxy
// assignments: they inject arbitrary wgetrc commands (use_proxy, no_proxy,
// http_proxy indirection, output redirection, ...) that the scanner cannot
// model.
func wgetRoutingOverride(argv []string) (string, bool) {
	pendingExec := false
	for _, a := range argv[1:] {
		if pendingExec {
			pendingExec = false
			if _, _, ok := proxyAssignment(a); !ok {
				return "-e " + a, true
			}
			continue
		}
		flag, val, attached := wgetFlag(a)
		if flag != "-e" && flag != "--execute" {
			continue
		}
		if !attached {
			pendingExec = true
			continue
		}
		if _, _, ok := proxyAssignment(val); !ok {
			return a, true
		}
	}
	return "", false
}

// netConfigFileFlags name per-command options that read additional request
// configuration from a local file the scanner cannot see: the file can add
// URLs, proxies or routing overrides to arbitrary peers, so its presence
// defeats static host analysis.
var netConfigFileFlags = map[string]map[string]struct{}{
	"curl": {"-K": {}, "--config": {}},
	"wget": {"-i": {}, "--input-file": {}, "--config": {}},
}

// netConfigFile reports whether argv supplies such a config/input file,
// returning the offending flag as evidence. Both the separate and attached
// (--config=FILE, -Kfile) forms are detected.
func netConfigFile(base string, argv []string) (string, bool) {
	set, ok := netConfigFileFlags[base]
	if !ok {
		return "", false
	}
	for _, a := range argv[1:] {
		flag, _, _ := splitFlagValue(a)
		if _, hit := set[flag]; hit {
			return flag, true
		}
		if len(flag) > 2 && !strings.HasPrefix(flag, "--") {
			if _, hit := set[flag[:2]]; hit {
				return flag[:2], true
			}
		}
	}
	return "", false
}

// sshCommandOptionKeys are ssh_config options whose VALUE is a command the
// client executes (ProxyCommand and LocalCommand run locally, RemoteCommand on
// the remote, KnownHostsCommand locally). An allowlisted destination says
// nothing about that hidden command, so their presence is denied.
var sshCommandOptionKeys = map[string]struct{}{
	"proxycommand": {}, "localcommand": {}, "permitlocalcommand": {},
	"remotecommand": {}, "knownhostscommand": {},
}

// sshCommandOption reports an option that makes ssh/scp/sftp/rsync execute a
// hidden command: -o ProxyCommand=... (separate or attached), scp/sftp's
// -S program, and rsync's -e/--rsh remote-shell command. The offending token is
// returned as evidence.
func sshCommandOption(base string, argv []string) (string, bool) {
	switch base {
	case "ssh", "scp", "sftp", "rsync":
	default:
		return "", false
	}
	expectOValue := false
	for _, a := range argv[1:] {
		if expectOValue {
			expectOValue = false
			if sshOptionExecutes(a) {
				return "-o " + a, true
			}
			continue
		}
		switch {
		case a == "-o":
			expectOValue = true
		case strings.HasPrefix(a, "-o") && len(a) > 2 && !strings.HasPrefix(a, "--"):
			if sshOptionExecutes(a[2:]) {
				return a, true
			}
		case (base == "scp" || base == "sftp") && (a == "-S" || (strings.HasPrefix(a, "-S") && len(a) > 2)):
			return a, true
		case base == "rsync" && (a == "-e" || a == "--rsh" || strings.HasPrefix(a, "--rsh=")):
			return a, true
		}
	}
	return "", false
}

// sshOptionExecutes reports whether an -o value names a command-executing
// ssh_config option ("ProxyCommand=..." or "ProxyCommand ...").
func sshOptionExecutes(v string) bool {
	v = strings.TrimSpace(strings.Trim(v, `"'`))
	if i := strings.IndexAny(v, "= \t"); i >= 0 {
		v = v[:i]
	}
	_, ok := sshCommandOptionKeys[strings.ToLower(v)]
	return ok
}

// explicitHosts accepts only operands that explicitly mark a host position (a
// scheme URL or user@host form), plus their option values.
func explicitHosts(argv []string) []string {
	var hosts []string
	for _, a := range argv[1:] {
		if a == "" || strings.HasPrefix(a, "@") {
			continue
		}
		if h, explicit := hostFromToken(a); explicit && h != "" {
			hosts = append(hosts, h)
		}
		if i := strings.IndexByte(a, '='); i >= 0 && i+1 < len(a) {
			if v := a[i+1:]; !strings.HasPrefix(v, "@") {
				if h, explicit := hostFromToken(v); explicit && h != "" {
					hosts = append(hosts, h)
				}
			}
		}
	}
	return hosts
}

// hostFromToken extracts a hostname from a single token and reports whether the
// token EXPLICITLY marked a host position — a scheme URL (curl http://h) or a
// user@host form (scp f user@h:/p). A bare dotted token (example.com,
// release.tar.gz) is returned with explicit=false: callers that must not
// confuse a local filename with a host (multi-target host extraction) require
// explicit=true, while the single-host nc/telnet path accepts the operand
// regardless.
func hostFromToken(a string) (host string, explicit bool) {
	a = strings.Trim(a, `"'`)
	if i := strings.Index(a, "://"); i >= 0 {
		if u, err := url.Parse(a); err == nil && u.Hostname() != "" {
			return strings.ToLower(u.Hostname()), true
		}
		a = a[i+3:]
		explicit = true
	}
	if i := strings.LastIndex(a, "@"); i >= 0 {
		a = a[i+1:]
		explicit = true
	}
	if i := strings.IndexByte(a, '/'); i >= 0 {
		a = a[:i]
	}
	if i := strings.LastIndex(a, ":"); i >= 0 {
		a = a[:i]
	}
	a = strings.ToLower(strings.TrimSpace(a))
	if a == "" {
		return "", false
	}
	if explicit || a == "localhost" || strings.Contains(a, ".") {
		return a, explicit
	}
	return "", false
}
