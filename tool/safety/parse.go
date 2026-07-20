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
	if !strings.Contains(s, "://") {
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
	case "scp", "sftp", "rsync":
		return dedupHosts(scpHosts(argv))
	case "curl", "wget":
		return dedupHosts(curlHosts(argv))
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

// sshHosts handles ssh: after skipping option values, the first operand is the
// target host (scheme-less and single-label forms accepted).
func sshHosts(argv []string) []string {
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
			if _, ok := sshValueFlags[a]; ok {
				skipNext = true
			}
			continue
		}
		if h := bareHost(a); h != "" {
			return []string{h}
		}
		return []string{strings.ToLower(strings.Trim(a, `"'`))}
	}
	return nil
}

// scpHosts handles scp/sftp/rsync: a remote operand carries a colon
// ([user@]host:path); local files have none, so they are not read as hosts.
func scpHosts(argv []string) []string {
	var hosts []string
	for _, a := range argv[1:] {
		if a == "" || isFlag(a) {
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

// curlFileFlags are curl/wget flags whose value is a local file (an output or
// config path), not a URL, so the value must not be read as a host.
var curlFileFlags = map[string]struct{}{
	"-o": {}, "--output": {}, "--output-dir": {}, "-T": {}, "--upload-file": {},
	"-D": {}, "--dump-header": {}, "-K": {}, "--config": {}, "-c": {}, "--cookie-jar": {},
}

// curlHosts handles curl/wget: positional operands are URLs (scheme-less
// accepted); @file uploads and the values of file-valued flags are excluded.
func curlHosts(argv []string) []string {
	var hosts []string
	skipNext := false
	for _, a := range argv[1:] {
		if skipNext {
			skipNext = false
			continue
		}
		if a == "" || strings.HasPrefix(a, "@") {
			continue
		}
		if isFlag(a) {
			// A file-valued flag in separate form (-o FILE) consumes the next
			// token; the attached form (--output=FILE) carries its own value.
			if !strings.Contains(a, "=") {
				if _, ok := curlFileFlags[a]; ok {
					skipNext = true
				}
			}
			continue
		}
		if h := bareHost(a); h != "" {
			hosts = append(hosts, h)
		}
	}
	return hosts
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
